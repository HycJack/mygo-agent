package main

// Mujica Feedback: every component of MujicaUI's feedback and overlay
// packages on one scrolling page. The dashboard palette is ignored here —
// mujicaPage applies core.Use for this frame, and every demo styles itself
// through the library's own tokens (k := core.Tokens(c)).
//
// Demo state lives in ui.Local on the window root under "mstate-" keys.
// Element handles are rebuilt each frame and never cached. The shell's
// scroll owns page growth, so nothing on this page may Grow.

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ZacharyZhang-NY/MujicaUI/core"
	"github.com/ZacharyZhang-NY/MujicaUI/data"
	"github.com/ZacharyZhang-NY/MujicaUI/feedback"
	"github.com/ZacharyZhang-NY/MujicaUI/input"
	"github.com/ZacharyZhang-NY/MujicaUI/overlay"
)

// mujicaStatePage is the "Mujica Feedback" route: one card per component of
// the feedback and overlay packages (tight groups for the loader family and
// the shared PopupMenu), packed two per row.
func (d *dashboard) mujicaStatePage(c *ui.Context, pal Palette) {
	_ = pal // MujicaUI tokens, not the dashboard palette, style this page.
	d.mujicaPage(c, "Mujica Feedback", "Alerts, progress, motion, toasts and every overlay of MujicaUI.", func() {
		k := core.Tokens(c)

		// grid lays one row of demos out as two equal cards.
		grid := func(cells func()) {
			ui.Grid(c).FillWidth().Columns(2).Gap(12).Children(cells)
		}
		// label separates the two packages on the page.
		label := func(s string) {
			ui.Text(c, s).FontSize(12.5).TextColor(k.TextMuted)
		}

		label("feedback package — attention, progress, motion")

		grid(func() {
			mujicaCard(c, "Alert", "Severity banner with an action; closable, then re-openable.", func() {
				open := ui.Local(c.Root(), "mstate-alert-open", func() bool { return true })
				r := feedback.Alert(c, open, "The archive could not be read.", feedback.AlertOptions{
					Severity: core.SeverityError,
					Title:    "Import failed",
					Action:   "Retry",
				})
				if r.Action {
					// a real caller would retry the import here
				}
				if !*open && input.Button(c, "Show again", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					*open = true
				}
			})
			mujicaCard(c, "Toast", "ShowToast queues; the shell-level Toaster below draws the stack.", func() {
				n := ui.Local(c.Root(), "mstate-toast-n", func() int { return 0 })
				if input.Button(c, "Save playlist", input.ButtonOptions{}).Clicked() {
					*n++
					feedback.ShowToast(c, feedback.ToastMessage{
						ID: fmt.Sprint(*n), Message: fmt.Sprintf("Playlist saved (change %d)", *n), Action: "Undo",
					})
				}
			})
		})

		grid(func() {
			mujicaCard(c, "Progress", "A labeled bar, an indeterminate ring, and a Meter with thresholds.", func() {
				feedback.Progress(c, 0.62, feedback.ProgressOptions{Label: "Uploading", ShowValue: true})
				feedback.Progress(c, 0, feedback.ProgressOptions{Shape: feedback.ProgressRing, Indeterminate: true, Label: "Connecting"})
				feedback.Meter(c, 93, feedback.MeterOptions{
					Max: 100, Label: "Disk", ShowValue: true,
					Levels: &ui.MeterLevels{Warning: 70, Critical: 90},
				})
			})
			mujicaCard(c, "Loaders", "The core Spinner plus the five feedback loaders.", func() {
				ui.Row(c).Gap(14).Wrap().AlignItems(ui.Center).Children(func() {
					core.Spinner(c, core.SpinnerOptions{Size: 20, Label: "Syncing", ShowLabel: true})
					feedback.DotsLoader(c, feedback.LoaderOptions{Label: "Thinking", ShowLabel: true})
					feedback.BarsLoader(c, feedback.LoaderOptions{Size: 20, Label: "Indexing", ShowLabel: true})
					feedback.PulseLoader(c, feedback.LoaderOptions{Size: 20, Label: "Listening", ShowLabel: true})
					feedback.OrbitLoader(c, feedback.LoaderOptions{Size: 20, Label: "Connecting", ShowLabel: true})
					feedback.WaveLoader(c, feedback.LoaderOptions{Label: "Speaking", ShowLabel: true})
				})
			})
		})

		grid(func() {
			mujicaCard(c, "Skeleton", "Placeholder shapes sized like the content they stand in for.", func() {
				loading := ui.Local(c.Root(), "mstate-skeleton", func() bool { return true })
				ui.Checkbox(c, loading, "Loading")
				ui.Row(c).Gap(12).Children(func() {
					if *loading {
						feedback.Skeleton(c, feedback.SkeletonOptions{Shape: feedback.SkeletonCircle})
						ui.Column(c).Grow(1).Gap(6).Children(func() {
							feedback.Skeleton(c, feedback.SkeletonOptions{Lines: 2})
						})
						return
					}
					ui.Box(c).Size(40, 40).Radius(20).Background(k.Selection)
					ui.Column(c).Grow(1).Gap(6).Children(func() {
						ui.Text(c, "Aurora — live now")
						ui.Text(c, "Episode 12 · 24 min").TextColor(k.TextMuted)
					})
				})
			})
			mujicaCard(c, "Shimmer", "Sweeps a highlight over real content while it loads.", func() {
				loading := ui.Local(c.Root(), "mstate-shimmer", func() bool { return true })
				ui.Checkbox(c, loading, "Loading")
				feedback.Shimmer(c, *loading, feedback.ShimmerOptions{Height: 48, Label: "Loading report"}, func() {
					ui.Text(c, "Quarterly report: 12 chapters, 340 pages.")
				})
			})
		})

		grid(func() {
			mujicaCard(c, "ShimmerText", "A thinking label swept by light while it is active.", func() {
				thinking := ui.Local(c.Root(), "mstate-shimmer-text", func() bool { return true })
				ui.Checkbox(c, thinking, "Thinking")
				feedback.ShimmerText(c, "Summarizing the thread…", feedback.ShimmerTextOptions{Active: *thinking})
			})
			mujicaCard(c, "CountUp", "Animates a number up to its new value.", func() {
				value := ui.Local(c.Root(), "mstate-count-up", func() float64 { return 12840 })
				feedback.CountUp(c, *value, feedback.CountUpOptions{Duration: 800 * time.Millisecond}).FontSize(core.FontSize(c, 28))
				if input.Button(c, "Add 500 plays", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					*value += 500
				}
			})
		})

		grid(func() {
			mujicaCard(c, "Typewriter", "Types text character by character; click to skip.", func() {
				round := ui.Local(c.Root(), "mstate-typewriter", func() int { return 0 })
				// The Key restarts the animation instead of resuming old state.
				ui.Column(c).Key(*round).Children(func() {
					feedback.Typewriter(c, "The court assembles at dusk.", feedback.TypewriterOptions{Speed: 30, Skippable: true})
				})
				if input.Button(c, "Restart", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					*round++
				}
			})
			mujicaCard(c, "BlinkHighlight", "Tints a value once on change and marks its direction.", func() {
				price := ui.Local(c.Root(), "mstate-blink", func() float64 { return 101.5 })
				feedback.BlinkHighlight(c, *price, feedback.BlinkHighlightOptions{})
				if input.Button(c, "Tick", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					*price += float64(rand.Intn(200)-100) / 50
				}
			})
		})

		grid(func() {
			mujicaCard(c, "Stagger", "Children enter one after another; replay to watch again.", func() {
				round := ui.Local(c.Root(), "mstate-stagger", func() int { return 0 })
				movements := []string{"Overture", "Masquerade", "Interlude", "Finale"}
				ui.Column(c).Key(*round).Gap(6).Children(func() {
					feedback.Stagger(c, len(movements), feedback.StaggerOptions{}, func(i int) {
						ui.Text(c, movements[i]).Padding(4, 10).Background(k.Selection).Radius(4)
					})
				})
				if input.Button(c, "Replay", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					*round++
				}
			})
			mujicaCard(c, "Presence", "Fades and slides content in and out of the flow.", func() {
				show := ui.Local(c.Root(), "mstate-presence", func() bool { return true })
				ui.Checkbox(c, show, "Show")
				feedback.Presence(c, *show, feedback.PresenceOptions{Kind: feedback.PresenceSlide}, func() {
					ui.Text(c, "Your changes were saved.").Padding(8, 12).Background(k.Selection).Radius(4)
				})
				ui.Text(c, "Content below moves with it.").TextColor(k.TextMuted)
			})
		})

		grid(func() {
			mujicaCard(c, "ReorderTransition", "Items animate to their new place when the order changes.", func() {
				order := ui.Local(c.Root(), "mstate-reorder", func() []string {
					return []string{"Sakiko", "Uika", "Mutsumi", "Nyamu"}
				})
				ui.Column(c).Width(220).Children(func() {
					feedback.ReorderTransition(c, *order, feedback.ReorderTransitionOptions{Gap: 4}, func(name string) {
						ui.Text(c, name).Padding(6, 12).Background(k.Selection).Radius(4)
					})
				})
				if input.Button(c, "Shuffle", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					next := append([]string(nil), *order...)
					rand.Shuffle(len(next), func(i, j int) { next[i], next[j] = next[j], next[i] })
					*order = next
				}
			})
			mujicaCard(c, "LayoutTransition", "Animates a layout change (FLIP) instead of jumping.", func() {
				open := ui.Local(c.Root(), "mstate-layout", func() bool { return false })
				ui.Checkbox(c, open, "Show the setlist")
				feedback.LayoutTransition(c, feedback.LayoutTransitionOptions{Clip: true}, func() {
					ui.Text(c, "Ave Mujica — setlist").FontWeight(600)
					if *open {
						for _, p := range []string{"Overture", "Masquerade", "Interlude", "Finale"} {
							ui.Text(c, p)
						}
					}
				})
			})
		})

		grid(func() {
			mujicaCard(c, "EmptyState", "Why a list is empty, with the way out.", func() {
				query := ui.Local(c.Root(), "mstate-empty", func() string { return "quarterly" })
				ui.TextInput(c, query).Width(220)
				if feedback.EmptyState(c, feedback.EmptyStateOptions{
					Kind:   feedback.EmptyNoResults,
					Query:  *query,
					Action: "Clear search",
				}).Action {
					*query = ""
				}
			})
			mujicaCard(c, "Result", "A full outcome page; r.Action indexes into Actions.", func() {
				r := feedback.Result(c, feedback.ResultOptions{
					Kind:        feedback.ResultForbidden,
					Description: "You need the Editor role to change billing.",
					Actions:     []string{"Request access", "Go back"},
				})
				_ = r // a real caller switches on r.Action
			})
		})

		grid(func() {
			mujicaCard(c, "StatusIndicator", "Shape plus word, so status never relies on color alone.", func() {
				ui.Row(c).Gap(16).Wrap().Children(func() {
					feedback.StatusIndicator(c, feedback.StatusSuccess, feedback.StatusIndicatorOptions{Label: "API"})
					feedback.StatusIndicator(c, feedback.StatusWarning, feedback.StatusIndicatorOptions{Label: "Queue"})
					feedback.StatusIndicator(c, feedback.StatusOffline, feedback.StatusIndicatorOptions{Label: "Replica"})
					feedback.StatusIndicator(c, feedback.StatusBusy, feedback.StatusIndicatorOptions{Label: "Index"})
				})
			})
			mujicaCard(c, "ActivityFeed", "A caller-owned stream of who did what, when.", func() {
				events := ui.Local(c.Root(), "mstate-activity", func() []feedback.Activity {
					now := time.Now()
					return []feedback.Activity{
						{ID: "a", Actor: "mutsumi", Action: "deployed", Object: "api@v42", Time: now.Add(-12 * time.Minute)},
						{ID: "b", Actor: "uika", Action: "commented on", Object: "PR #218", Time: now.Add(-3 * time.Hour)},
						{ID: "c", Actor: "sakiko", Action: "closed", Object: "incident 91", Time: now.Add(-26 * time.Hour)},
					}
				})
				feedback.ActivityFeed(c, *events, feedback.ActivityFeedOptions{}).FillWidth().Height(190)
			})
		})

		grid(func() {
			mujicaCard(c, "NotificationCenter", "Reads, opens and removes a caller-owned list in place.", func() {
				items := ui.Local(c.Root(), "mstate-notifications", func() []feedback.Notification {
					return []feedback.Notification{
						{ID: "a", Title: "Build failed", Body: "main · 3 tests failing", Time: "2 min ago", Action: "View log"},
						{ID: "b", Title: "Invite accepted", Body: "Nyamu joined the workspace", Time: "1 h ago"},
						{ID: "c", Title: "Backup complete", Time: "Yesterday", Read: true},
					}
				})
				r := feedback.NotificationCenter(c, items, feedback.NotificationCenterOptions{})
				r.Element.FillWidth().Height(210)
			})
			mujicaCard(c, "LoadingOverlay", "Covers one region while it works; the rest stays usable.", func() {
				loading := ui.Local(c.Root(), "mstate-loading-overlay", func() bool { return false })
				if input.Button(c, "Generate report", input.ButtonOptions{}).Clicked() {
					*loading = true
				}
				r := feedback.LoadingOverlay(c, *loading, feedback.LoadingOverlayOptions{
					Label: "Generating report…", Cancel: "Cancel",
				}, func() {
					ui.Column(c).Padding(12).Gap(4).FillWidth().Background(k.Surface).Radius(8).Border(1, k.Border).Children(func() {
						ui.Text(c, "Quarterly report").FontWeight(600)
						ui.Text(c, "42 pages · last export 14:02.").TextColor(k.TextMuted)
					})
				})
				if r.Canceled {
					*loading = false
				}
			})
		})

		grid(func() {
			mujicaCard(c, "UndoToast", "A countdown toast whose Undo restores the deletion.", func() {
				type mstateUndoState struct {
					notes   []string
					removed string
					open    bool
				}
				st := ui.Local(c.Root(), "mstate-undo", func() mstateUndoState {
					return mstateUndoState{notes: []string{"Call the florist", "Book the hall", "Order candles"}}
				})
				notes := st.notes
				for i, n := range notes {
					ui.Row(c).Key(n).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, n).Grow(1)
						if input.Button(c, "Delete", input.ButtonOptions{Variant: input.Ghost}).Clicked() {
							st.removed, st.open = n, true
							st.notes = append(notes[:i:i], notes[i+1:]...)
						}
					})
				}
				if st.open {
					removed := st.removed
					feedback.UndoToast(c, &st.open, "Deleted “"+removed+"”", feedback.UndoToastOptions{
						Duration: 5 * time.Second,
						OnUndo:   func() { st.notes = append(st.notes, removed) },
					})
				}
			})
			mujicaCard(c, "InterruptButton", "Stops an agent run; with Confirm it first asks to keep going.", func() {
				running := ui.Local(c.Root(), "mstate-interrupt-run", func() bool { return true })
				stopping := ui.Local(c.Root(), "mstate-interrupt-stop", func() bool { return false })
				until := ui.Local(c.Root(), "mstate-interrupt-until", func() time.Time { return time.Time{} })
				if *stopping && c.Now().After(*until) {
					*running, *stopping = false, false
				}
				if *stopping {
					c.After((*until).Sub(c.Now()))
				}
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					if *running && !*stopping {
						feedback.DotsLoader(c, feedback.LoaderOptions{Label: "Agent running", ShowLabel: true})
					}
					if feedback.InterruptButton(c, *running, feedback.InterruptButtonOptions{Confirm: true, Interrupting: *stopping}).Changed() {
						*stopping = true
						*until = c.Now().Add(1500 * time.Millisecond)
					}
				})
				if !*running && input.Button(c, "Start run", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					*running = true
				}
			})
		})

		grid(func() {
			mujicaCard(c, "TokenUsageChart", "Input vs output tokens per step, stacked.", func() {
				usage := []feedback.TokenUsage{
					{Label: "Plan", Input: 1200, Output: 300},
					{Label: "Search", Input: 4800, Output: 650},
					{Label: "Read", Input: 6100, Output: 420},
					{Label: "Write", Input: 2100, Output: 2900},
				}
				feedback.TokenUsageChart(c, usage, feedback.TokenUsageChartOptions{})
			})
			mujicaCard(c, "CostBreakdown", "Sums spend per model, tool or step in a sortable table.", func() {
				state := ui.Local(c.Root(), "mstate-cost", func() feedback.CostBreakdownState {
					return feedback.CostBreakdownState{}
				})
				items := []feedback.CostItem{
					{Model: "opus", Tool: "search", InputTokens: 1000, OutputTokens: 200, Cost: 0.30},
					{Model: "haiku", Tool: "read", InputTokens: 4000, OutputTokens: 500, Cost: 0.05},
					{Model: "opus", Tool: "write", InputTokens: 2000, OutputTokens: 3000, Cost: 1.15},
				}
				feedback.CostBreakdown(c, state, items, feedback.CostBreakdownOptions{GroupBy: feedback.CostByModel}).Element.Height(190)
			})
		})

		grid(func() {
			mujicaCard(c, "TraceViewer", "A span tree on a timeline; select one for its attributes.", func() {
				state := ui.Local(c.Root(), "mstate-trace", func() feedback.TraceViewerState {
					return feedback.TraceViewerState{}
				})
				spans := []feedback.TraceSpan{
					{ID: "run", Name: "agent.run", End: 900 * time.Millisecond},
					{ID: "plan", Parent: "run", Name: "llm.plan", Start: 10 * time.Millisecond, End: 210 * time.Millisecond},
					{ID: "search", Parent: "run", Name: "tool.search", Start: 220 * time.Millisecond, End: 520 * time.Millisecond, Failed: true},
					{ID: "write", Parent: "run", Name: "llm.write", Start: 530 * time.Millisecond, End: 880 * time.Millisecond},
				}
				feedback.TraceViewer(c, state, spans, feedback.TraceViewerOptions{}).Element.Height(200)
			})
			mujicaCard(c, "EvalResultTable", "Scores and pass marks for an eval run, with diffs.", func() {
				state := ui.Local(c.Root(), "mstate-eval", func() data.DataTableState[string] {
					return data.DataTableState[string]{}
				})
				cases := []feedback.EvalCase{
					{ID: "c1", Name: "Greets by title", Score: 0.98, Passed: true},
					{ID: "c2", Name: "Refuses forgery", Score: 0.41},
					{ID: "c3", Name: "Summarizes letters", Score: 0.87, Passed: true},
				}
				r := feedback.EvalResultTable(c, state, cases, feedback.EvalResultTableOptions{})
				r.Element.Height(200)
				if id, ok := r.Diff(); ok {
					_ = id // a real caller opens expected vs actual side by side
				}
			})
		})

		// ToolRegistryPanel wants the width for its schema columns.
		mujicaCard(c, "ToolRegistryPanel", "The agent's tools with schemas and enable switches.", func() {
			list := ui.Local(c.Root(), "mstate-tools-list", func() data.ListState[string] {
				return data.ListState[string]{}
			})
			tools := ui.Local(c.Root(), "mstate-tools", func() []feedback.AgentTool {
				return []feedback.AgentTool{
					{Name: "web_search", Description: "Search the web", Enabled: true, Params: []feedback.ToolParam{
						{Name: "query", Type: "string", Required: true},
					}},
					{Name: "read_file", Description: "Read a local file", Params: []feedback.ToolParam{
						{Name: "path", Type: "string", Required: true},
					}},
					{Name: "run_shell", Description: "Run a shell command", Params: []feedback.ToolParam{
						{Name: "command", Type: "string", Required: true},
						{Name: "timeout", Type: "number"},
					}},
				}
			})
			r := feedback.ToolRegistryPanel(c, list, *tools, feedback.ToolRegistryPanelOptions{})
			r.Element.FillWidth().Height(210)
			if name, on, ok := r.Toggled(); ok {
				for i := range *tools {
					if (*tools)[i].Name == name {
						(*tools)[i].Enabled = on
					}
				}
			}
		})

		label("overlay package — dialogs, menus, anchored panels")

		grid(func() {
			mujicaCard(c, "Dialog", "A decorated modal with a form; Escape or the backdrop closes it.", func() {
				open := ui.Local(c.Root(), "mstate-dialog-open", func() bool { return false })
				name := ui.Local(c.Root(), "mstate-dialog-name", func() string { return "Ave Mujica" })
				if input.Button(c, "Rename band…", input.ButtonOptions{}).Clicked() {
					*open = true
				}
				overlay.Dialog(c, open, overlay.DialogOptions{
					Title:       "Rename the band",
					Description: "The new name shows on every ticket.",
					Decorated:   true,
					Actions: func() {
						if input.Button(c, "Cancel", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
							*open = false
						}
						if input.Button(c, "Rename", input.ButtonOptions{}).Clicked() {
							*open = false
						}
					},
				}, func() { ui.TextInput(c, name) })
			})
			mujicaCard(c, "AlertDialog", "A destructive confirm; the exact ConfirmText unlocks it.", func() {
				open := ui.Local(c.Root(), "mstate-alert-dialog", func() bool { return false })
				deleted := ui.Local(c.Root(), "mstate-alert-dialog-done", func() bool { return false })
				if input.Button(c, "Delete archive", input.ButtonOptions{Variant: input.Danger}).Clicked() {
					*open, *deleted = true, false
				}
				r := overlay.AlertDialog(c, open, overlay.AlertDialogOptions{
					Title:       "Delete the archive?",
					Description: "Twelve letters will be destroyed. This cannot be undone.",
					ConfirmText: "delete",
				})
				if r.Confirmed {
					*deleted = true
				}
				if *deleted {
					ui.Text(c, "The archive is gone.").TextColor(k.TextMuted)
				}
			})
		})

		grid(func() {
			mujicaCard(c, "Drawer", "A panel sliding in from one side; Apply closes it.", func() {
				open := ui.Local(c.Root(), "mstate-drawer", func() bool { return false })
				only := ui.Local(c.Root(), "mstate-drawer-only", func() bool { return false })
				if input.Button(c, "Filters", input.ButtonOptions{}).Clicked() {
					*open = true
				}
				overlay.Drawer(c, open, overlay.DrawerOptions{
					Title: "Filters", Side: overlay.DrawerRight, Size: 300,
					Actions: func() {
						if input.Button(c, "Apply", input.ButtonOptions{}).Clicked() {
							*open = false
						}
					},
				}, func() {
					ui.Checkbox(c, only, "Only failing checks")
				})
			})
			mujicaCard(c, "Popover", "An anchored panel toggled by its trigger button.", func() {
				open := ui.Local(c.Root(), "mstate-popover", func() bool { return false })
				b := input.Button(c, "Guests", input.ButtonOptions{Variant: input.Secondary})
				if b.Clicked() {
					*open = !*open
				}
				overlay.Popover(c, b, open, overlay.PopoverOptions{Title: "Guest list"}, func() {
					ui.Text(c, "Twelve guests confirmed; three await a reply.")
				})
			})
		})

		grid(func() {
			mujicaCard(c, "Tooltip", "Hover or keyboard focus shows a short hint.", func() {
				ui.Row(c).Gap(12).Children(func() {
					b := input.Button(c, "Seal", input.ButtonOptions{})
					overlay.Tooltip(c, b, "Seal the letter with wax", overlay.TooltipOptions{})
					d := input.Button(c, "Burn", input.ButtonOptions{Variant: input.Danger})
					overlay.Tooltip(c, d, "Burn every copy; nothing is kept", overlay.TooltipOptions{Delay: 200 * time.Millisecond})
				})
			})
			mujicaCard(c, "HoverCard", "A richer card after a short hover; Escape dismisses it.", func() {
				b := input.Button(c, "Lady Sakiko", input.ButtonOptions{Variant: input.Ghost})
				overlay.HoverCard(c, b, overlay.HoverCardOptions{Width: 260}, func() {
					ui.Text(c, "Lady Sakiko").FontWeight(600)
					ui.Text(c, "Keyboardist of the court; writes the masquerade's score.").TextColor(k.TextMuted)
				})
			})
		})

		grid(func() {
			mujicaCard(c, "DropdownMenu", "Button menu with a group, a check, a submenu and a separator.", func() {
				pinned := ui.Local(c.Root(), "mstate-dropdown-pinned", func() bool { return false })
				overlay.DropdownMenu(c, "Actions", overlay.DropdownMenuOptions{}, func(m *overlay.PopupMenu) {
					m.Group("Letter", func() {
						m.Item("Rename", overlay.PopupMenuItemOptions{})
						m.Check("Pinned", pinned, overlay.PopupMenuItemOptions{KeepOpen: true})
					})
					m.Submenu("Move to", overlay.PopupMenuItemOptions{}, func(m *overlay.PopupMenu) {
						m.Item("Archive", overlay.PopupMenuItemOptions{})
						m.Item("Vault", overlay.PopupMenuItemOptions{})
					})
					m.Separator()
					m.Item("Burn", overlay.PopupMenuItemOptions{Disabled: true})
				})
			})
			mujicaCard(c, "ContextMenu", "Right-click the region, or press Shift+F10 inside it.", func() {
				row := ui.Column(c).Height(120).FillWidth().Padding(12).Gap(6).
					Background(k.Surface).Border(1, k.Border).Radius(6)
				row.Children(func() {
					ui.Text(c, "Letter to the Countess").FontWeight(600)
					ui.Text(c, "Right-click anywhere in this region.").TextColor(k.TextMuted)
				})
				overlay.ContextMenu(c, row, overlay.ContextMenuOptions{}, func(m *overlay.PopupMenu) {
					m.Item("Reply", overlay.PopupMenuItemOptions{})
					m.Item("Forward", overlay.PopupMenuItemOptions{})
					m.Separator()
					m.Item("Delete", overlay.PopupMenuItemOptions{})
				})
			})
		})

		grid(func() {
			mujicaCard(c, "Popconfirm", "A small anchored confirm for a single action.", func() {
				open := ui.Local(c.Root(), "mstate-popconfirm", func() bool { return false })
				b := input.Button(c, "Archive letter", input.ButtonOptions{Variant: input.Secondary})
				if b.Clicked() {
					*open = true
				}
				r := overlay.Popconfirm(c, b, open, overlay.PopconfirmOptions{
					Title:       "Archive this letter?",
					Description: "It leaves the inbox.",
				})
				if r.Confirmed {
					// a real caller moves the letter to the archive
				}
			})
			mujicaCard(c, "Overlay", "Free content placed in the window; here a corner player.", func() {
				open := ui.Local(c.Root(), "mstate-overlay", func() bool { return true })
				overlay.Overlay(c, open, overlay.OverlayOptions{Place: overlay.OverlayBottomRight, Label: "Now playing"}, func() {
					ui.Text(c, "Now playing").FontWeight(600)
					ui.Text(c, "Symbol I: △ — Ave Mujica").TextColor(k.TextMuted)
					if input.Button(c, "Close", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
						*open = false
					}
				})
				if !*open && input.Button(c, "Show player", input.ButtonOptions{}).Clicked() {
					*open = true
				}
			})
		})

		grid(func() {
			mujicaCard(c, "SplitButton", "A primary action with a menu of alternatives.", func() {
				r := overlay.SplitButton(c, "Save", overlay.SplitButtonOptions{}, func(m *overlay.PopupMenu) {
					m.Item("Save as…", overlay.PopupMenuItemOptions{})
					m.Item("Save a copy", overlay.PopupMenuItemOptions{})
					m.Item("Save all", overlay.PopupMenuItemOptions{})
				})
				_ = r // r.Changed() reports the main action
			})
			mujicaCard(c, "PopupMenu", "The raw anchored menu behind the dropdown triggers.", func() {
				open := ui.Local(c.Root(), "mstate-popup-menu", func() bool { return false })
				b := input.Button(c, "Custom trigger", input.ButtonOptions{Variant: input.Secondary})
				if b.Clicked() {
					*open = !*open
				}
				overlay.PopupMenuOpen(c, b, open, func(m *overlay.PopupMenu) {
					m.Item("Detached item", overlay.PopupMenuItemOptions{})
					m.Separator()
					m.Item("Another", overlay.PopupMenuItemOptions{})
				})
			})
		})

		// Toaster draws the window's toast stack, exactly like the
		// gallery's shell calls it last in the view each frame.
		feedback.Toaster(c)
	})
}
