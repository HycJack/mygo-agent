package ui

import (
	"github.com/egoist/mygo/ui"
	"mygo-agent/internal/harness"
)

// The file viewer renders here over a ViewModel snapshot
// (spec/architecture.md): the host reads the file, picks the kind and
// fills the VM; the view draws it and reports back through ViewerActions.

// ViewerVM is the render input of the file viewer. Wrap is a binding
// the toggle writes and the host syncs back after the frame.
type ViewerVM struct {
	Path, Kind, Title, Sub, Note, Err, Raw, Text string
	Loading                                      bool
	Lines                                        []harness.DiffLine
	Bitmap                                       *ui.Bitmap
	Wrap                                         bool
	Md                                           *MdCache
	Pal                                          Palette
}

// ViewerActions is what the viewer calls back for.
type ViewerActions interface {
	// Close returns to the conversation.
	Close()
}

// Viewer is the file viewer: a toolbar with the file and a back button,
// then the content by kind.
func Viewer(c *ui.Context, vm *ViewerVM, acts ViewerActions) {
	t := c.Theme()
	ui.Column(c).Fill().Background(vm.Pal.Bg).Children(func() {
		// Toolbar.
		ui.Row(c).Height(40).PaddingX(12).Gap(8).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(vm.Pal.Border).Children(func() {
			back := ui.ButtonBase(c).Gap(6).Padding(4, 8).Radius(7).Cursor(ui.CursorPointer)
			if back.Hovered() {
				back.Background(vm.Pal.Hover)
			}
			if back.Clicked() {
				acts.Close()
			}
			back.Children(func() {
				ui.Icon(c, IconBack).FontSize(14).TextColor(t.TextMuted)
				ui.Text(c, "Chat").FontSize(12).TextColor(t.TextMuted)
			})
			ui.Icon(c, viewerIcon(vm.Kind)).FontSize(14).TextColor(vm.Pal.TextMuted)
			ui.Text(c, vm.Title).Font("monospace").FontSize(12).SingleLine()
			if vm.Sub != "" {
				ui.Text(c, vm.Sub).FontSize(11).Font("monospace").TextColor(vm.Pal.TextMuted)
			}
			ui.Spacer(c)
			if vm.Note != "" {
				ui.Text(c, vm.Note).FontSize(11).TextColor(vm.Pal.Warning).SingleLine()
			}
			if vm.Kind == "diff" || vm.Kind == "text" {
				ui.Toggle(c, &vm.Wrap, "Wrap")
			}
			cp := ui.ButtonBase(c).Label("Copy contents").Tooltip("Copy").Size(26, 26).Radius(6).Center()
			if cp.Hovered() {
				cp.Background(vm.Pal.Hover)
			}
			if cp.Clicked() {
				c.WriteClipboard(vm.Raw)
				c.Toast("Copied")
			}
			cp.Children(func() { ui.Icon(c, IconCopy).FontSize(13).TextColor(t.TextMuted) })
		})
		// Content.
		switch {
		case vm.Loading:
			ui.Column(c).Fill().Center().Gap(10).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "Running git diff…").FontSize(12).TextColor(t.TextMuted)
			})
		case vm.Err != "":
			ui.Column(c).Fill().AlignItems(ui.Center).Children(func() {
				ui.Column(c).MaxWidth(520).Margin(24, ui.Auto).Children(func() {
					blockError(c, &BlockVM{Type: "error", Text: vm.Err}, vm.Pal)
				})
			})
		case vm.Kind == "image":
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).Fill().Center().Padding(20).Children(func() {
					ui.Image(c, vm.Bitmap).Radius(8)
				})
			})
		case vm.Kind == "markdown":
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).FillWidth().Padding(24).Gap(6).MaxWidth(860).Margin(0, ui.Auto).Children(func() {
					Markdown(c, vm.Md, "viewer:"+vm.Path, vm.Text, true, vm.Pal)
				})
			})
		case vm.Kind == "diff" || vm.Kind == "text":
			nums := vm.Kind == "text"
			ui.Scroll(c).Grow(1).Children(func() {
				ui.Column(c).FillWidth().PaddingY(6).Children(func() {
					for _, l := range vm.Lines {
						DiffLineRow(c, l, nums, vm.Wrap, vm.Pal)
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
func viewerIcon(kind string) *ui.SVG {
	switch kind {
	case "image":
		return IconImage
	case "markdown":
		return IconFileText
	case "diff":
		return IconGitBranch
	default:
		return IconFileText
	}
}
