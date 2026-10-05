package app

import (
	"strings"

	uipkg "mygo-agent/internal/ui"

	"github.com/egoist/mygo/ui"
)

var modeNames = []string{"Read Only", "Agent", "Full Access"}

// composer renders the input box through the shared view in
// internal/ui — the same composer the home screen uses, bound to the
// same persistent draft (spec/architecture.md).
func (a *app) composer(c *ui.Context) {
	uipkg.Composer(c, a.homeViewModel(), homeActions{a: a})
}

// iconButton is a square icon-only button: ButtonBase has no built-in
// padding, so the icon stays centered and its hit area covers the glyph
// (a sized-down ui.Button would push the icon out of the hit area).
func (a *app) iconButton(c *ui.Context, tip string, size float32, ic *ui.SVG, fn func()) {
	b := ui.ButtonBase(c).Label(tip).Tooltip(tip).Size(size, size).Radius(999).Center().Cursor(ui.CursorPointer)
	if b.Hovered() {
		b.Background(a.pal.CardHover)
	}
	if b.Clicked() {
		fn()
	}
	b.Children(func() { ui.Icon(c, ic).FontSize(size * 0.5) })
}

// composerRow is the bottom line of the composer: the approval mode, the
// model, and the send (or stop) button.
func (a *app) composerRow(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		a.modeSegments(c)
		ui.Spacer(c)
		a.modelButton(c)
		send := ui.ButtonBase(c).Label("Send").Tooltip("Send (Enter)").Size(30, 30).Radius(999).Center()
		if a.running {
			send.Label("Stop").Tooltip("Stop")
			send.Background(t.Text)
			if send.Clicked() {
				a.stop()
			}
			send.Children(func() { ui.Icon(c, icStop).FontSize(14).TextColor(a.pal.AccentSub) })
			return
		}
		empty := strings.TrimSpace(a.draft) == ""
		send.Background(t.Accent)
		if empty {
			send.Disabled(true).Opacity(0.4)
		} else if send.Hovered() {
			send.Background(t.AccentHover)
		}
		if send.Clicked() {
			a.send()
		}
		send.Children(func() { ui.Icon(c, icArrowUp).FontSize(17).TextColor(t.AccentText) })
	})
}

// modelButton opens the model popover, grouped by provider like ZCode's:
// each provider's models with a check on the chosen pair, the provider
// manager, and the reasoning effort.
func (a *app) modelButton(c *ui.Context) {
	t := c.Theme()
	mb := ui.ButtonBase(c).Label("Model").Tooltip("Model and effort").Gap(6).Padding(4, 8).Radius(8).Cursor(ui.CursorPointer)
	if mb.Hovered() || a.modelMenu {
		mb.Background(a.pal.CardHover)
	}
	if mb.Clicked() {
		a.modelMenu = !a.modelMenu
	}
	mb.Children(func() {
		ui.Icon(c, icSparkles).FontSize(12).TextColor(a.pal.TextMuted)
		ui.Text(c, a.model).Font("monospace").FontSize(11.5).TextColor(t.TextMuted).SingleLine()
		chev := ui.Icon(c, icChevDown).FontSize(12).TextColor(a.pal.TextMuted)
		if a.modelMenu {
			chev.Rotate(180)
		}
	})
	ui.Popover(c, mb, &a.modelMenu, func() {
		ui.Column(c).Width(320).Padding(4).Radius(12).Background(a.pal.Card).
			Border(1, a.pal.Border).Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.4)).Children(func() {
			ui.Row(c).Padding(6, 10, 2).Children(func() {
				ui.Text(c, "PROVIDER / MODEL").FontSize(10).FontWeight(600).TextColor(a.pal.TextMuted).LetterSpacing(0.6).Grow(1)
				ui.Text(c, "EFFORT").FontSize(10).FontWeight(600).TextColor(a.pal.TextMuted).LetterSpacing(0.6)
			})
			ui.Scroll(c).MaxHeight(320).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for pi := range a.providers {
						p := &a.providers[pi]
						ui.Textf(c, "%s", p.Name).FontSize(10).FontWeight(600).TextColor(a.pal.TextMuted).
							Padding(8, 10, 2).LetterSpacing(0.4)
						for _, m := range p.Models {
							active := a.providerID == p.ID && a.model == m
							row := ui.ButtonBase(c).Padding(6, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
							if row.Hovered() {
								row.Background(a.pal.CardHover)
							}
							if row.Clicked() {
								a.providerID, a.model = p.ID, m
								a.modelMenu = false
								a.saveConfig()
							}
							row.Children(func() {
								ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
									ui.Text(c, m).Font("monospace").FontSize(12).SingleLine()
									ui.Text(c, p.Name).FontSize(10).TextColor(a.pal.TextMuted).SingleLine()
								})
								if active {
									ui.Icon(c, icCheck).FontSize(13).TextColor(t.Text)
								}
							})
						}
					}
				})
			})
			ui.Box(c).Height(1).Margin(4, 6).Background(a.pal.Border)
			mng := ui.ButtonBase(c).Padding(7, 10).Radius(7).Gap(8).Cursor(ui.CursorPointer)
			if mng.Hovered() {
				mng.Background(a.pal.CardHover)
			}
			if mng.Clicked() {
				a.modelMenu = false
				if a.settingsSel == "" {
					a.settingsSel = a.providerID
				}
				a.settingsOpen = true
			}
			mng.Children(func() {
				ui.Icon(c, icSliders).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "Manage providers & models…").FontSize(12).Grow(1)
			})
			prev := a.effort
			effort := ui.SegmentedBase(c, &a.effort, 3)
			effort.Track.Margin(6).Padding(2).Radius(8).Background(a.pal.Bg).Border(1, a.pal.Border).Children(func() {
				for i, name := range []string{"Low", "Medium", "High"} {
					s := effort.Segment(i).Padding(3, 10).Radius(6)
					chosen := i == a.effort
					if chosen {
						s.Background(a.pal.CardHover)
					}
					s.Children(func() {
						col := t.TextMuted
						if chosen {
							col = t.Text
						}
						ui.Text(c, name).FontSize(11).TextColor(col)
					})
				}
			})
			if a.effort != prev {
				a.saveConfig()
			}
		})
	})
}

// modeSegments is the approval mode: read only, agent, or full access.
func (a *app) modeSegments(c *ui.Context) {
	t := c.Theme()
	seg := ui.SegmentedBase(c, &a.mode, len(modeNames))
	seg.Track.Padding(2).Radius(8).Background(a.pal.Bg).Border(1, a.pal.Border).Children(func() {
		for i, name := range modeNames {
			s := seg.Segment(i).Padding(3, 8).Radius(6)
			chosen := i == a.mode
			if chosen {
				s.Background(a.pal.CardHover)
			}
			s.Children(func() {
				col := t.TextMuted
				if chosen {
					col = t.Text
				}
				ui.Text(c, name).FontSize(11).TextColor(col)
			})
		}
	})
}
