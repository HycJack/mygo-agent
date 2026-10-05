package app

import (
	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo/ui"
)

// Markdown rendering lives in internal/ui (spec/architecture.md); the
// app keeps the per-message cache and the short call the views use.

// markdown renders a reply by its id through the shared renderer.
func (a *app) markdown(c *ui.Context, id, src string, complete bool) {
	if a.mdCache == nil {
		a.mdCache = uipkg.NewMdCache()
	}
	uipkg.Markdown(c, a.mdCache, id, src, complete, a.pal)
}
