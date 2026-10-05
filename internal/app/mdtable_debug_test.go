package app

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDebugTableBlock(t *testing.T) {
	a := &app{theme: codexTheme(), pal: codexPalette()}
	tt := ui.NewTester(func(c *ui.Context) {
		a.tableBlock(c, []string{"| Field | Type |", "|---|---|", "| attempts | `x` |"})
	}, 600, 300)
	tt.Frame()
	for _, s := range tt.Texts() {
		fmt.Printf("TEXT: %q\n", s)
	}
}
