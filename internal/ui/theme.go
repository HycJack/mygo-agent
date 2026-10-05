package ui

import "github.com/egoist/mygo/ui"

// Palette holds the Codex dark look; widgets read the same values through
// the ui.Theme, the app's own elements read the palette directly.
type Palette struct {
	Bg         ui.Color
	SidebarBG  ui.Color
	Card       ui.Color
	CardHover  ui.Color
	CodeBG     ui.Color
	UserBubble ui.Color
	Sel        ui.Color
	Hover      ui.Color
	Border     ui.Color

	Text      ui.Color
	TextMuted ui.Color
	Accent    ui.Color
	AccentSub ui.Color

	Success ui.Color
	Danger  ui.Color
	Warning ui.Color

	DiffAddBG   ui.Color
	DiffDelBG   ui.Color
	DiffAddText ui.Color
	DiffDelText ui.Color
	DiffAddMark ui.Color // the word-level highlight inside a changed line
	DiffDelMark ui.Color
}

func CodexPalette() Palette {
	return Palette{
		Bg:         ui.Hex("#17171a"),
		SidebarBG:  ui.Hex("#0f0f12"),
		Card:       ui.Hex("#1d1d22"),
		CardHover:  ui.Hex("#24242b"),
		CodeBG:     ui.Hex("#0c0c0f"),
		UserBubble: ui.Hex("#26262d"),
		Sel:        ui.Hex("#222229"),
		Hover:      ui.Hex("#1b1b21"),
		Border:     ui.Hex("#2b2b33"),

		Text:      ui.Hex("#ececf0"),
		TextMuted: ui.Hex("#8f8f9b"),
		Accent:    ui.Hex("#f4f4f6"),
		AccentSub: ui.Hex("#101013"),

		Success: ui.Hex("#3fb950"),
		Danger:  ui.Hex("#f85149"),
		Warning: ui.Hex("#d29922"),

		DiffAddBG:   ui.RGBA(63, 185, 80, 0.13),
		DiffDelBG:   ui.RGBA(248, 81, 73, 0.13),
		DiffAddText: ui.Hex("#56d364"),
		DiffDelText: ui.Hex("#ff7b72"),
		DiffAddMark: ui.RGBA(63, 185, 80, 0.38),
		DiffDelMark: ui.RGBA(248, 81, 73, 0.38),
	}
}

// CodexTheme maps the palette onto the widget theme. Codex is dark; the
// app forces this theme instead of following the desktop's appearance.
func CodexTheme() *ui.Theme {
	p := CodexPalette()
	return &ui.Theme{
		Dark:           true,
		Background:     p.Bg,
		Surface:        p.Card,
		SurfaceHover:   p.CardHover,
		SurfacePressed: ui.Hex("#2a2a32"),
		Border:         p.Border,
		Text:           p.Text,
		TextMuted:      p.TextMuted,
		Accent:         p.Accent,
		AccentHover:    ui.Hex("#ffffff"),
		AccentPressed:  ui.Hex("#d8d8de"),
		AccentText:     p.AccentSub,
		Danger:         p.Danger,
		Warning:        p.Warning,
		Success:        p.Success,
		Selection:      ui.RGBA(140, 160, 255, 0.30),
		Focus:          ui.RGBA(120, 160, 255, 0.55),
		Scrollbar:      ui.RGBA(255, 255, 255, 0.22),
		ScrollbarWidth: 8,
		Radius:         8,
		Spacing:        4,
		FontSize:       13,
	}
}
