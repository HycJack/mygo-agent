// Package app implements the MyGo Agent application: its state, the
// four agent backends and every view. main.go only starts it.
package app

import "mygo-agent/internal/harness"

// DiffLine is the agent package's diff line; the app renders it with
// hunk headers and word-level marks.
type DiffLine = harness.DiffLine

// App is the whole application state; the view is a function of it.
type App = app
