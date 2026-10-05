package app

import (
	uipkg "mygo-agent/internal/ui"

	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
)

const (
	maxTextBytes = 2 << 20 // read at most 2 MB of a text file
	maxTextLines = 20000   // render at most this many lines
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
var markdownExts = map[string]bool{".md": true, ".markdown": true}
var diffExts = map[string]bool{".diff": true, ".patch": true}

// The viewer renders in internal/ui over a ViewModel snapshot
// (spec/architecture.md); this file is the bridge plus the host-side
// file reading: images as bitmaps, Markdown rendered, patches as
// colored diffs, everything else as text with line numbers.

// viewerOpenNow reports whether the viewer covers the main area.
func (a *app) viewerIsOpen() bool { return a.viewerOpen && a.viewer.Path != "" }

// openFile reads a file from the workspace and shows it in the viewer.
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
		lines := harness.ParseUnifiedDiff(text)
		add, del := harness.DiffStats(lines)
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

// renderViewer assembles the snapshot and renders the viewer pane.
func (a *app) renderViewer(c *ui.Context) {
	vm := &uipkg.ViewerVM{
		Path: a.viewer.Path, Kind: a.viewer.Kind, Title: a.viewer.Title,
		Sub: a.viewer.Sub, Note: a.viewer.Note, Err: a.viewer.Err,
		Raw: a.viewer.Raw, Text: a.viewer.Text, Loading: a.viewer.Loading,
		Lines: a.viewer.Lines, Bitmap: a.viewer.Bitmap,
		Wrap: a.viewerWrap, Md: a.md(), Pal: a.pal,
	}
	uipkg.Viewer(c, vm, viewerActions{a: a})
	a.viewerWrap = vm.Wrap // the toggle's binding
}

// viewerActions adapts *app to ui.ViewerActions.
type viewerActions struct{ a *app }

func (h viewerActions) Close() { h.a.viewerOpen = false }
