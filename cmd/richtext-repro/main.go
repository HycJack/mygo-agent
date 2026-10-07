// Command richtext-repro reproduces a mygo selectable-text bug: a
// RichText with Selectable renders its spans and syncs the editor's
// source, but a click or drag on the text never reaches that editor —
// double-click + Cmd+C copies nothing. A plain Text with Selectable,
// rendered right beside it, copies fine.
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
			// B: a selectable RichText whose children are the spans —
			// the documented inline form ("Selectable before Children
			// selects in the whole text", inline.go).
			ui.RichText(c).Selectable().Children(func() {
				ui.Text(c, "RICH Order ")
				ui.Text(c, "12345").FontWeight(700)
				ui.Text(c, " shipped")
			})
		})
	}, 600, 200)

	probe := func(label, text string) {
		r, ok := tt.Find(text)
		if !ok {
			fmt.Printf("%-30s NOT RENDERED\n", label)
			return
		}
		// Double-click the first word, then copy: toolkit's own
		// selectable-text interaction (select_test.go).
		tt.ClickAt(r.X+8, r.Y+8)
		tt.ClickAt(r.X+8, r.Y+8)
		tt.Key(ui.Cmd, ui.KeyC)
		fmt.Printf("%-30s double-click + Cmd+C → %q\n", label, tt.Clipboard())
		// Select all and copy: does the editor see the keyboard at all?
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		fmt.Printf("%-30s Cmd+A + Cmd+C       → %q\n", label, tt.Clipboard())
	}

	probe("A. Text().Selectable()", plainText)
	probe("B. RichText().Selectable()", richText)
	fmt.Println("\nexpected: B behaves like A — a double click selects a word of B.")
	fmt.Println("actual:   B's click never reaches any selectable editor: the")
	fmt.Println("copy is still A's selection (the focus never moved). The spans")
	fmt.Println("swallow the press; the parent's selectable editor is unpressed.")
}
