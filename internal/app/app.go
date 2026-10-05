// Package app implements the MyGo Agent application: its state, the
// agent backends it runs and the views that render them. main.go only
// starts it.
package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-agent/internal/harness"
)

// DiffLine is the agent package's diff line; the app renders it with
// hunk headers and word-level marks.
type DiffLine = harness.DiffLine

// App is the whole application state; the view is a function of it.
type App = app

// New creates the application for the given build version and loads
// its persisted state.
func New(version string) *App {
	a := newApp()
	a.version = version
	a.importLegacyThreads() // the pre-directory threads.json, once
	a.loadThreads()
	return a
}

// Setup wires the app into the running MyGo instance: environment
// knobs used for demos and screenshots, the window with the app's
// view, and the initial git refresh.
func (a *app) Setup() {
	a.applyEnv()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:         "Codex",
			Width:         1240,
			Height:        800,
			MinWidth:      880,
			MinHeight:     560,
			TitleBarStyle: mygo.TitleBarHiddenInset,
			StateKey:      "codex-main",
			Content:       ui.View(a.view),
		})
		if shot := os.Getenv("CODEX_SHOT"); shot != "" {
			a.captureShot(shot)
		}
	})
	a.refreshGit()
}

// applyEnv honours the CODEX_* environment knobs.
func (a *app) applyEnv() {
	if os.Getenv("CODEX_SEED") == "1" {
		a.seed()
	}
	if os.Getenv("CODEX_NAV") == "0" {
		a.navOpen = false
	}
	if os.Getenv("CODEX_WS") == "0" {
		a.wsOpen = false
	}
	if os.Getenv("CODEX_POPOVER") == "model" {
		a.modelMenu = true
	}
	if os.Getenv("CODEX_TERM") == "1" {
		a.toggleTerminal(nil)
	}
	if p := os.Getenv("CODEX_VIEW"); p != "" {
		if path, ok := strings.CutPrefix(p, "git:"); ok {
			a.openGitDiff(gitChange{Code: "M", Path: path})
		} else {
			a.openFile(p)
		}
	}
}

// captureShot writes a PNG of the window, then exits the process.
func (a *app) captureShot(path string) {
	go func() {
		time.Sleep(2 * time.Second)
		png, err := a.win.CapturePage()
		if err != nil {
			fmt.Fprintln(os.Stderr, "capture:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, png, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write shot:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "captured", path)
		os.Exit(0)
	}()
}

// Run runs the MyGo event loop until the last window closes.
func (a *app) Run() error {
	return mygo.App.Run()
}
