// Command richtext-repro reproduces a mygo selectable-text bug: a
// RichText with Selectable and CHILD elements renders its spans and
// syncs the editor's source, but a click or drag on the text never
// reaches that editor. The same paragraph passed as constructor SPANS —
// no child elements — selects and copies fine.
//
// Run: go run ./cmd/richtext-repro
package main

import (
	"fmt"
	"runtime/debug"

	"github.com/egoist/mygo/ui"
)

const (
	plainText = "PLAIN Order 12345 shipped"
	richText  = "RICH Order 12345 shipped"
	spansText = "SPANS Order 12345 shipped"
)

func main() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/egoist/mygo" {
				fmt.Println("mygo", d.Version)
			}
		}
	}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Gap(10).Children(func() {
			// A: a plain selectable text — the working control.
			ui.Text(c, plainText).Selectable()
			// B: the documented inline form ("Selectable before
			// Children selects in the whole text", inline.go) — a
			// selectable RichText whose children are the spans.
			ui.RichText(c).Selectable().Children(func() {
				ui.Text(c, "RICH Order ")
				ui.Text(c, "12345").FontWeight(700)
				ui.Text(c, " shipped")
			})
			// C: the same paragraph as constructor spans — no child
			// elements to swallow the press.
			ui.RichText(c, []ui.Span{
				{Text: "SPANS Order "},
				{Text: "12345", Weight: 700},
				{Text: " shipped"},
			}...).Selectable()
		})
	}, 600, 300)

	probe := func(label, text string) {
		r, ok := tt.Find(text)
		if !ok {
			fmt.Printf("%-34s NOT RENDERED\n", label)
			return
		}
		// Double-click the first word, then copy: the toolkit's own
		// selectable-text interaction (select_test.go).
		tt.ClickAt(r.X+8, r.Y+8)
		tt.ClickAt(r.X+8, r.Y+8)
		tt.Key(ui.Cmd, ui.KeyC)
		fmt.Printf("%-34s double-click + Cmd+C → %q\n", label, tt.Clipboard())
		// Select all and copy: does the editor see the keyboard at all?
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		fmt.Printf("%-34s Cmd+A + Cmd+C       → %q\n", label, tt.Clipboard())
	}

	probe("A. Text().Selectable()", plainText)
	probe("B. RichText().Children", richText)
	probe("C. RichText(spans).Selectable()", spansText)
	fmt.Println("\nC is the workaround: constructor spans carry no child")
	fmt.Println("elements, so the press lands on the selectable element itself.")
}
