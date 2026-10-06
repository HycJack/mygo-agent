package main

import "github.com/egoist/mygo/ui"

// Palette abstracted from the agent's internal/ui/theme.go: the shared
// surface, text and signal colors the widgets read through the ui.Theme
// and the example's own elements read directly. Two palettes ship — a
// dark one (the agent's Codex look) and a light one — and the toolbar's
// toggle swaps them through Context.SetTheme.
type Palette struct {
	Dark bool

	Bg        ui.Color
	SidebarBG ui.Color
	Card      ui.Color
	CardHover ui.Color
	Sel       ui.Color
	Hover     ui.Color
	Border    ui.Color

	Text      ui.Color
	TextMuted ui.Color
	Accent    ui.Color

	Success ui.Color
	Danger  ui.Color
	Warning ui.Color
	Info    ui.Color

	// The four hues the charts rotate through.
	Series [4]ui.Color
}

// CodexPalette is the agent's dark look, verbatim.
func CodexPalette() Palette {
	return Palette{
		Dark:      true,
		Bg:        ui.Hex("#17171a"),
		SidebarBG: ui.Hex("#0f0f12"),
		Card:      ui.Hex("#1d1d22"),
		CardHover: ui.Hex("#24242b"),
		Sel:       ui.Hex("#222229"),
		Hover:     ui.Hex("#1b1b21"),
		Border:    ui.Hex("#2b2b33"),

		Text:      ui.Hex("#ececf0"),
		TextMuted: ui.Hex("#8f8f9b"),
		Accent:    ui.Hex("#f4f4f6"),

		Success: ui.Hex("#3fb950"),
		Danger:  ui.Hex("#f85149"),
		Warning: ui.Hex("#d29922"),
		Info:    ui.Hex("#58a6ff"),

		Series: [4]ui.Color{ui.Hex("#58a6ff"), ui.Hex("#3fb950"), ui.Hex("#d29922"), ui.Hex("#bc8cff")},
	}
}

// DaylightPalette is the light counterpart, same roles.
func DaylightPalette() Palette {
	return Palette{
		Bg:        ui.Hex("#f6f6f8"),
		SidebarBG: ui.Hex("#ececf1"),
		Card:      ui.Hex("#ffffff"),
		CardHover: ui.Hex("#f0f0f4"),
		Sel:       ui.Hex("#e8e8f0"),
		Hover:     ui.Hex("#ededf2"),
		Border:    ui.Hex("#d8d8e0"),

		Text:      ui.Hex("#1a1a22"),
		TextMuted: ui.Hex("#6b6b78"),
		Accent:    ui.Hex("#2563eb"),

		Success: ui.Hex("#1a7f37"),
		Danger:  ui.Hex("#cf222e"),
		Warning: ui.Hex("#9a6700"),
		Info:    ui.Hex("#0969da"),

		Series: [4]ui.Color{ui.Hex("#2563eb"), ui.Hex("#16a34a"), ui.Hex("#d97706"), ui.Hex("#7c3aed")},
	}
}

// Theme maps a palette onto the widget theme, the way the agent's
// CodexTheme does; Radius, Spacing and FontSize carry the app-wide look.
// The dark palette's accent is near-white, so the text on it goes dark;
// the light palette's accent is a saturated blue, so its text is white.
func (p Palette) Theme() *ui.Theme {
	accentText := ui.RGB(255, 255, 255)
	if p.Dark {
		accentText = ui.Hex("#101013")
	}
	return &ui.Theme{
		Dark:           p.Dark,
		Background:     p.Bg,
		Surface:        p.Card,
		SurfaceHover:   p.CardHover,
		SurfacePressed: p.Sel,
		Border:         p.Border,
		Text:           p.Text,
		TextMuted:      p.TextMuted,
		Accent:         p.Accent,
		AccentHover:    p.Accent.Mix(p.Text, 0.15),
		AccentPressed:  p.Accent.Mix(p.Text, 0.3),
		AccentText:     accentText,
		Danger:         p.Danger,
		Warning:        p.Warning,
		Success:        p.Success,
		Selection:      ui.RGBA(140, 160, 255, 0.30),
		Focus:          ui.RGBA(120, 160, 255, 0.55),
		Scrollbar:      ui.RGBA(128, 128, 128, 0.35),
		ScrollbarWidth: 8,
		Radius:         8,
		Spacing:        4,
		FontSize:       13,
	}
}
