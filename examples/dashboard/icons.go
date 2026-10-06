package main

import "github.com/egoist/mygo/ui"

// Icon parses a 24×24 stroked icon, abstracted from the agent's
// internal/ui/icons.go: the shapes are Lucide icon paths (ISC licensed),
// drawn in currentColor at stroke width 2 with round caps and joins.
//
// Icon sourcing, in the order the project prefers them:
//  1. lucide.dev — the default set; every icon below comes from there.
//  2. icon-sets.iconify.design — the supplement when Lucide lacks a
//     metaphor or another style fits the screen (its thousands of sets
//     paste straight into this same wrapper).
//  3. lobehub.com/icons — brand and LLM vendor logos (OpenAI, Claude,
//     Gemini…) for model pickers; filled multi-color logos, so they
//     would take their own MustParseSVG rather than currentColor.
//
// Anything still missing gets a pass in Sketch and lands here as paths.
func Icon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

// The icon set the dashboard uses.
var (
	IconDashboard = Icon(`<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`)
	IconDatabase  = Icon(`<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/><path d="M3 12a9 3 0 0 0 18 0"/>`)
	IconSliders   = Icon(`<line x1="4" x2="4" y1="21" y2="14"/><line x1="4" x2="4" y1="10" y2="3"/><line x1="12" x2="12" y1="21" y2="12"/><line x1="12" x2="12" y1="8" y2="3"/><line x1="20" x2="20" y1="21" y2="16"/><line x1="20" x2="20" y1="12" y2="3"/><line x1="2" x2="6" y1="14" y2="14"/><line x1="10" x2="14" y1="8" y2="8"/><line x1="18" x2="22" y1="16" y2="16"/>`)
	IconLayers    = Icon(`<path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/>`)
	IconGear      = Icon(`<path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/>`)

	IconSearch  = Icon(`<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`)
	IconBell    = Icon(`<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>`)
	IconSun     = Icon(`<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>`)
	IconMoon    = Icon(`<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>`)
	IconRefresh = Icon(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`)
	IconMore    = Icon(`<circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/><circle cx="5" cy="12" r="1"/>`)
	IconPlus    = Icon(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	IconCheck   = Icon(`<path d="M20 6 9 17l-5-5"/>`)
	IconX       = Icon(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	IconCopy    = Icon(`<rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>`)
	IconChevR   = Icon(`<path d="m9 18 6-6-6-6"/>`)
	IconStar    = Icon(`<path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1-5.4-2.9-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z"/>`)
	IconDot     = Icon(`<circle cx="12" cy="12" r="5" fill="currentColor" stroke="none"/>`)
	IconAlert   = Icon(`<circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/>`)

	IconTrendUp   = Icon(`<polyline points="22 7 13.5 15.5 8.5 10.5 2 17"/><polyline points="16 7 22 7 22 13"/>`)
	IconTrendDown = Icon(`<polyline points="22 17 13.5 8.5 8.5 13.5 2 7"/><polyline points="16 17 22 17 22 11"/>`)
	IconDollar    = Icon(`<line x1="12" x2="12" y1="2" y2="22"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>`)
	IconUsers     = Icon(`<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>`)
	IconCart      = Icon(`<circle cx="8" cy="21" r="1"/><circle cx="19" cy="21" r="1"/><path d="M2.05 2.05h2l2.66 12.42a2 2 0 0 0 2 1.58h9.78a2 2 0 0 0 1.95-1.57l1.65-7.43H5.12"/>`)
	IconPercent   = Icon(`<line x1="19" x2="5" y1="5" y2="19"/><circle cx="6.5" cy="6.5" r="2.5"/><circle cx="17.5" cy="17.5" r="2.5"/>`)
	IconActivity  = Icon(`<polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>`)
	IconGlobe     = Icon(`<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>`)
	IconZap       = Icon(`<path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/>`)
	IconServer    = Icon(`<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/>`)

	IconDownload = Icon(`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/>`)
	IconInbox    = Icon(`<polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`)
	IconClock    = Icon(`<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>`)
	IconUser     = Icon(`<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>`)
	IconLogout   = Icon(`<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" x2="9" y1="12" y2="12"/>`)
	IconExternal = Icon(`<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>`)
	IconPackage  = Icon(`<path d="M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z"/><path d="M12 22V12"/><path d="m3.3 7 8.7 5 8.7-5"/>`)

	// Panel toggles, lucide.dev: panel-left/-right plus their open and
	// close variants — the chevron points the way the edge travels.
	IconPanelLeft       = Icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>`)
	IconPanelLeftOpen   = Icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m14 9-3 3 3 3"/>`)
	IconPanelLeftClose  = Icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m10 9 3 3-3 3"/>`)
	IconPanelRight      = Icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M15 3v18"/>`)
	IconPanelRightOpen  = Icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m10 9-3 3 3 3"/>`)
	IconPanelRightClose = Icon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m14 9 3 3-3 3"/>`)

	IconChevronsLeft  = Icon(`<path d="m11 17-5-5 5-5"/><path d="m18 17-5-5 5-5"/>`)
	IconChevronsRight = Icon(`<path d="m6 17 5-5-5-5"/><path d="m13 17 5-5-5-5"/>`)
	IconChevronLeft   = Icon(`<path d="m15 18-6-6 6-6"/>`)
	IconChevronRight  = Icon(`<path d="m9 18 6-6-6-6"/>`)
)
