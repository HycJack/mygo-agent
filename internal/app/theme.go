package app

import "github.com/egoist/mygo/ui"

// palette holds the Codex dark look; widgets read the same values through
// the ui.Theme, the app's own elements read the palette directly.
type palette struct {
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

func codexPalette() palette {
	return palette{
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

// codexTheme maps the palette onto the widget theme. Codex is dark; the
// app forces this theme instead of following the desktop's appearance.
func codexTheme() *ui.Theme {
	p := codexPalette()
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

// icon parses a 24×24 stroked icon; the shapes below are the Lucide
// icon set's own paths (ISC licensed), drawn in currentColor at stroke
// width 2 with round caps and joins, the way Lucide draws them.
func icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

var (
	icPlus       = icon(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	icArrowUp    = icon(`<path d="m5 12 7-7 7 7"/><path d="M12 19V5"/>`)
	icStop       = icon(`<rect x="7" y="7" width="10" height="10" rx="2" fill="currentColor" stroke="none"/>`)
	icTerminal   = icon(`<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>`)
	icChevDown   = icon(`<path d="m6 9 6 6 6-6"/>`)
	icCheck      = icon(`<path d="M20 6 9 17l-5-5"/>`)
	icX          = icon(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	icTrash      = icon(`<path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/><line x1="10" x2="10" y1="11" y2="17"/><line x1="14" x2="14" y1="11" y2="17"/>`)
	icMessage    = icon(`<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>`)
	icSparkles   = icon(`<path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/><path d="M20 3v4"/><path d="M22 5h-4"/><path d="M4 17v2"/><path d="M5 18H3"/>`)
	icMore       = icon(`<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>`)
	icPanel      = icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M3 15h18"/>`)
	icPanelLeft  = icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>`)
	icPanelRight = icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M15 3v18"/>`)
	icCopy       = icon(`<rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>`)
	icFileCode   = icon(`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"/><path d="M14 2v6h6"/><path d="m10 13-2 2 2 2"/><path d="m14 17 2-2-2-2"/>`)
	icFileText   = icon(`<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>`)
	icGitBranch  = icon(`<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>`)
	icAlert      = icon(`<circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/>`)
	icDot        = icon(`<circle cx="12" cy="12" r="5" fill="currentColor" stroke="none"/>`)
	icSliders    = icon(`<line x1="4" x2="4" y1="21" y2="14"/><line x1="4" x2="4" y1="10" y2="3"/><line x1="12" x2="12" y1="21" y2="12"/><line x1="12" x2="12" y1="8" y2="3"/><line x1="20" x2="20" y1="21" y2="16"/><line x1="20" x2="20" y1="12" y2="3"/><line x1="2" x2="6" y1="14" y2="14"/><line x1="10" x2="14" y1="8" y2="8"/><line x1="18" x2="22" y1="16" y2="16"/>`)
	icFolder     = icon(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`)
	icRefresh    = icon(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`)
	icBack       = icon(`<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>`)
	icImage      = icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/>`)
)
