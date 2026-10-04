package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"mygo-agent/internal/agent"

	"github.com/egoist/mygo/ui"
)

const (
	maxTextBytes = 2 << 20 // read at most 2 MB of a text file
	maxTextLines = 20000   // render at most this many lines
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
var markdownExts = map[string]bool{".md": true, ".markdown": true}
var diffExts = map[string]bool{".diff": true, ".patch": true}

// viewerOpenNow reports whether the viewer covers the main area.
func (a *app) viewerIsOpen() bool { return a.viewerOpen && a.viewer.Path != "" }

// openFile reads a file from the workspace and shows it in the viewer:
// images as bitmaps, Markdown rendered, patches as colored diffs,
// everything else as text with line numbers.
func (a *app) openFile(path string) {
	ext := strings.ToLower(filepath.Ext(path))
	base := filepath.Base(path)

	if imageExts[ext] {
		data, err := os.ReadFile(path)
		if err != nil {
			a.showViewerError(path, err.Error())
			return
		}
		bmp, err := ui.DecodeBitmap(data)
		if err != nil {
			a.showViewerError(path, "not a readable image: "+err.Error())
			return
		}
		a.viewer = viewerState{Path: path, Kind: "image", Title: base, Sub: humanBytes(int64(len(data))), Bitmap: bmp}
		a.viewerOpen = true
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		a.showViewerError(path, err.Error())
		return
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		a.showViewerError(path, fmt.Sprintf("Binary file, %s.", humanBytes(int64(len(data)))))
		return
	}
	if len(data) > maxTextBytes {
		data = data[:maxTextBytes]
	}
	text := string(data)
	note := ""
	if len(data) == maxTextBytes {
		note = "showing the first 2 MB"
	}

	switch {
	case markdownExts[ext]:
		a.viewer = viewerState{Path: path, Kind: "markdown", Title: base, Raw: text, Text: text, Sub: humanBytes(int64(len(data)))}
	case diffExts[ext]:
		lines := agent.ParseUnifiedDiff(text)
		add, del := agent.DiffStats(lines)
		a.viewer = viewerState{Path: path, Kind: "diff", Title: base, Lines: lines, Raw: text, Sub: sprintStats(add, del)}
	default:
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) > maxTextLines {
			lines = lines[:maxTextLines]
			if note == "" {
				note = fmt.Sprintf("showing the first %s lines", humanCount(maxTextLines))
			}
		}
		a.viewer = viewerState{
			Path: path, Kind: "text", Title: base, Text: text,
			Lines: makeLines(lines), Raw: text, Sub: humanBytes(int64(len(data))), Note: note,
		}
	}
	a.viewerOpen = true
}

// makeLines packs raw lines into diff rows of context kind, so the
// viewer renders text and diffs with one renderer.
func makeLines(lines []string) []DiffLine {
	out := make([]DiffLine, len(lines))
	for i, l := range lines {
		l = strings.ReplaceAll(l, "\t", "    ")
		out[i] = DiffLine{Kind: ' ', Text: l, Number: i + 1}
	}
	return out
}

func (a *app) showViewerError(path, msg string) {
	a.viewer = viewerState{Path: path, Kind: "text", Title: filepath.Base(path), Err: msg}
	a.viewerOpen = true
}

func sprintStats(add, del int) string {
	switch {
	case add > 0 && del > 0:
		return fmt.Sprintf("+%d −%d", add, del)
	case add > 0:
		return fmt.Sprintf("+%d", add)
	case del > 0:
		return fmt.Sprintf("−%d", del)
	default:
		return ""
	}
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func humanCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%d,%03d", n/1000, n%1000)
	}
	return fmt.Sprint(n)
}

// viewerPane is the file viewer: a toolbar with the file and a back
// button, then the content by kind.
func (a *app) viewerPane(c *ui.Context) {
	t := c.Theme()
	v := &a.viewer
	ui.Column(c).Fill().Background(a.pal.Bg).Children(func() {
		// Toolbar.
		ui.Row(c).Height(40).PaddingX(12).Gap(8).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(a.pal.Border).Children(func() {
			back := ui.ButtonBase(c).Gap(6).Padding(4, 8).Radius(7).Cursor(ui.CursorPointer)
			if back.Hovered() {
				back.Background(a.pal.Hover)
			}
			if back.Clicked() {
				a.viewerOpen = false
			}
			back.Children(func() {
				ui.Icon(c, icBack).FontSize(14).TextColor(t.TextMuted)
				ui.Text(c, "Chat").FontSize(12).TextColor(t.TextMuted)
			})
			ui.Icon(c, a.viewerIcon(v.Kind)).FontSize(14).TextColor(a.pal.TextMuted)
			ui.Text(c, v.Title).Font("monospace").FontSize(12).SingleLine()
			if v.Sub != "" {
				ui.Text(c, v.Sub).FontSize(11).Font("monospace").TextColor(a.pal.TextMuted)
			}
			ui.Spacer(c)
			if v.Note != "" {
				ui.Text(c, v.Note).FontSize(11).TextColor(a.pal.Warning).SingleLine()
			}
			if v.Kind == "diff" || v.Kind == "text" {
				ui.Toggle(c, &a.viewerWrap, "Wrap")
			}
			cp := ui.ButtonBase(c).Label("Copy contents").Tooltip("Copy").Size(26, 26).Radius(6).Center()
			if cp.Hovered() {
				cp.Background(a.pal.Hover)
			}
			if cp.Clicked() {
				mygo.Clipboard.WriteText(v.Raw)
				c.Toast("Copied")
			}
			cp.Children(func() { ui.Icon(c, icCopy).FontSize(13).TextColor(t.TextMuted) })
		})
		// Content.
		switch {
		case v.Loading:
			ui.Column(c).Fill().Center().Gap(10).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Running git diff…").FontSize(12).TextColor(t.TextMuted)
			})
		case v.Err != "":
			ui.Column(c).Fill().AlignItems(ui.Center).Children(func() {
				ui.Column(c).MaxWidth(520).Margin(24, ui.Auto).Children(func() {
					a.blockError(c, &Block{Type: "error", Text: v.Err})
				})
			})
		case v.Kind == "image":
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Fill().Center().Padding(20).Children(func() {
					ui.Image(c, v.Bitmap).Radius(8)
				})
			})
		case v.Kind == "markdown":
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).FillWidth().Padding(24).Gap(6).MaxWidth(860).Margin(0, ui.Auto).Children(func() {
					a.markdown(c, v.Text)
				})
			})
		case v.Kind == "diff" || v.Kind == "text":
			nums := v.Kind == "text"
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).FillWidth().PaddingY(6).Children(func() {
					for _, l := range v.Lines {
						a.diffLineRow(c, l, nums, a.viewerWrap)
					}
				})
			})
		default:
			ui.Column(c).Fill().Center().Children(func() {
				ui.Text(c, "Nothing to show.").TextColor(t.TextMuted)
			})
		}
	})
}

// viewerIcon picks the toolbar icon by kind.
func (a *app) viewerIcon(kind string) *ui.SVG {
	switch kind {
	case "image":
		return icImage
	case "markdown":
		return icFileText
	case "diff":
		return icGitBranch
	default:
		return icFileText
	}
}

// diffLineRow renders one line of a diff or of a viewed text file: the
// row tinted by its kind, the line number for plain text, and the
// word-level change highlighted, as godiff does.
func (a *app) diffLineRow(c *ui.Context, l DiffLine, nums, wrap bool) {
	t := c.Theme()
	row := ui.Row(c).MinHeight(19).Padding(0, 12).AlignItems(ui.Start)
	switch l.Kind {
	case '+':
		row.Background(a.pal.DiffAddBG)
	case '-':
		row.Background(a.pal.DiffDelBG)
	case '@':
		row.Background(a.pal.Card)
	}
	row.Children(func() {
		if nums {
			num := ""
			if l.Kind != '@' {
				num = fmt.Sprint(l.Number)
			}
			ui.Text(c, num).Font("monospace").FontSize(11).Width(46).
				TextColor(a.pal.TextMuted).TextAlign(ui.End).Margin(0, 10, 0, 0)
		}
		if l.Kind == '@' {
			ui.Text(c, l.Text).Font("monospace").FontSize(11.5).TextColor(t.TextMuted)
			return
		}
		content := ui.Row(c).Grow(1).MinWidth(0)
		if wrap {
			content.Wrap()
		}
		content.Children(func() {
			if l.MarkHi > l.MarkLo {
				r := []rune(l.Text)
				lo, hi := l.MarkLo, l.MarkHi
				if lo > len(r) {
					lo = len(r)
				}
				if hi > len(r) {
					hi = len(r)
				}
				strong := a.pal.DiffAddMark
				if l.Kind == '-' {
					strong = a.pal.DiffDelMark
				}
				ui.RichText(c,
					ui.Span{Text: string(r[:lo])},
					ui.Span{Text: string(r[lo:hi]), Background: strong},
					ui.Span{Text: string(r[hi:])},
				).Font("monospace").FontSize(12)
				return
			}
			txt := ui.Text(c, l.Text).Font("monospace").FontSize(12)
			if !wrap {
				txt.NoWrap().SingleLine()
			}
			switch l.Kind {
			case '+':
				txt.TextColor(a.pal.DiffAddText)
			case '-':
				txt.TextColor(a.pal.DiffDelText)
			}
		})
	})
}
