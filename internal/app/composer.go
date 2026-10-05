package app

import (
	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo/ui"
)

// The composer renders in internal/ui (spec/architecture.md) — the same
// shared Composer the home screen uses, bound to the same persistent
// draft. This file is only the bridge.

// composer renders the input box below the thread.
func (a *app) composer(c *ui.Context) {
	uipkg.Composer(c, a.homeViewModel(), homeActions{a: a})
	// The thread surface has no other sync: without this, the draft, the
	// model-menu toggle and the consumed focus flag stay view-side — the
	// menu flashes shut and send() reads a stale draft.
	a.syncVM()
}
