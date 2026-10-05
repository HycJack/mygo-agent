package app

import (
	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo/ui"
)

// The palette and theme live in internal/ui; the app keeps the names it
// has always used.
type palette = uipkg.Palette

func codexPalette() palette { return uipkg.CodexPalette() }

func codexTheme() *ui.Theme { return uipkg.CodexTheme() }
