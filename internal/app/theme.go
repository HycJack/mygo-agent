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

// Icons moved to internal/ui; keep the short names.
var (
	icPlus       = uipkg.IconPlus
	icArrowUp    = uipkg.IconArrowUp
	icStop       = uipkg.IconStop
	icTerminal   = uipkg.IconTerminal
	icChevDown   = uipkg.IconChevDown
	icCheck      = uipkg.IconCheck
	icX          = uipkg.IconX
	icTrash      = uipkg.IconTrash
	icMessage    = uipkg.IconMessage
	icSparkles   = uipkg.IconSparkles
	icMore       = uipkg.IconMore
	icPanel      = uipkg.IconPanel
	icPanelLeft  = uipkg.IconPanelLeft
	icPanelRight = uipkg.IconPanelRight
	icCopy       = uipkg.IconCopy
	icFileCode   = uipkg.IconFileCode
	icFileText   = uipkg.IconFileText
	icGitBranch  = uipkg.IconGitBranch
	icAlert      = uipkg.IconAlert
	icDot        = uipkg.IconDot
	icSliders    = uipkg.IconSliders
	icFolder     = uipkg.IconFolder
	icRefresh    = uipkg.IconRefresh
	icBack       = uipkg.IconBack
	icImage      = uipkg.IconImage
)
