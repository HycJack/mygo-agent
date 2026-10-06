package main

// The Settings page: a two-pane settings shell (a Split with categories
// beside the forms), the collapsibles and accordions of the toolkit, and
// the meters of a plan page.

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// rgbOf prints a color the way a dashboard's CSS custom property would.
func rgbOf(c ui.Color) string {
	return fmt.Sprintf("rgb(%d, %d, %d)", c.R, c.G, c.B)
}

func (d *dashboard) settingsPage(c *ui.Context, pal Palette) {
	PageTitle(c, pal, "Settings", "Workspace preferences, plan usage and the danger zone.")

	ui.Split(c, &d.split, func() {
		// The categories: a quiet list, the chosen one highlighted.
		ui.Column(c).Fill().Padding(8).Gap(2).Background(pal.Card).Children(func() {
			for i, name := range []string{"General", "Notifications", "Usage", "Danger zone"} {
				row := ui.Row(c).Padding(7, 10).Radius(7).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if d.settingsTab == i {
					row.Background(pal.Sel)
				} else if row.Hovered() {
					row.Background(pal.Hover)
				}
				if row.Clicked() {
					d.settingsTab = i
				}
				row.Children(func() {
					ui.Text(c, name).FontSize(12.5).SingleLine()
				})
			}
		})
	}, func() {
		ui.Scroll(c).Fill().Children(func() {
			ui.Column(c).Fill().Padding(0, 0, 20, 16).Gap(14).Children(func() {
				switch d.settingsTab {
				case 1:
					d.settingsNotifications(c, pal)
				case 2:
					d.settingsUsage(c, pal)
				case 3:
					d.settingsDanger(c, pal)
				default:
					d.settingsGeneral(c, pal)
				}
			})
		})
	}).Height(520).Border(1, pal.Border).Radius(10).Clip()
}

// settingsGeneral is the workspace's general form: identity, URL, and
// the advanced options behind a collapsible.
func (d *dashboard) settingsGeneral(c *ui.Context, pal Palette) {
	Card(c, pal, "Workspace", "", func() {
		ui.Form(c, func() {
			ui.Field(c, "Name", func() { ui.TextInput(c, &d.wsName).Placeholder("Acme Inc") })
			ui.Field(c, "URL", func() { ui.TextInput(c, &d.wsURL).Placeholder("acme.analytics.app") }).Description("Lowercase letters, digits and dashes.")
			ui.Collapsible(c, "Advanced", &d.advanced, func() {
				labeledSwitch(c, pal, &d.notifyMentions, "Allow guest access")
				labeledSwitch(c, pal, &d.advanced2, "Enforce single sign-on")
			})
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "Reset")
			if ui.PrimaryButton(c, "Save changes").Clicked() {
				c.Toast("Workspace saved")
			}
		})
	})
	Card(c, pal, "Members", "the accordion opens one at a time", func() {
		ui.Accordion(c, func() {
			ui.AccordionItem(c, "Pending invitations", &d.sections[0], func() {
				ui.Text(c, "2 invites waiting: barbara@acme.dev, ken@acme.dev").FontSize(12.5).TextColor(pal.TextMuted)
			})
			ui.AccordionItem(c, "Roles", &d.sections[1], func() {
				ui.Text(c, "Admins manage billing; editors publish dashboards; analysts only view.").FontSize(12.5).TextColor(pal.TextMuted)
			})
			ui.AccordionItem(c, "Single sign-on", &d.sections[2], func() {
				ui.Text(c, "SAML metadata is set up; enforcement lives under Advanced.").FontSize(12.5).TextColor(pal.TextMuted)
			})
		})
	})
}

// settingsNotifications is the switches-and-groups pane.
func (d *dashboard) settingsNotifications(c *ui.Context, pal Palette) {
	Card(c, pal, "Notifications", "", func() {
		ui.CheckboxGroup(c, "Email", func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Checkbox(c, &d.notifyMail, "Product updates")
				ui.Checkbox(c, &d.notifyDigest, "Weekly usage digest")
				ui.Checkbox(c, &d.notifyMentions, "Mentions and comments")
			})
		})
		ui.Column(c).Gap(8).Children(func() {
			labeledSwitch(c, pal, &d.advanced2, "Slack webhook")
			labeledSwitch(c, pal, &d.advanced, "PagerDuty escalation")
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Quiet hours").FontSize(12).TextColor(pal.TextMuted)
			ui.RangeSlider(c, &d.priceLow, &d.priceHigh, 0, 24, 1).Label("Quiet hours").Grow(1)
			ui.Textf(c, "%02.0f:00–%02.0f:00", d.priceLow, d.priceHigh).FontSize(12).Width(96).TextAlign(ui.End)
		})
	})
}

// settingsUsage is the plan page: meters, a stepper and the color well
// for the workspace accent.
func (d *dashboard) settingsUsage(c *ui.Context, pal Palette) {
	Card(c, pal, "Plan usage", "billed monthly", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "%s plan", d.plan).FontSize(15).FontWeight(600)
			Pill(c, pal, "renews Nov 1", pal.Info)
			ui.Spacer(c)
			ui.Segmented(c, &d.prefTab, "Monthly", "Yearly").Label("Cycle")
		})
		ui.Column(c).Gap(10).Margin(8, 0, 0, 0).Children(func() {
			ui.Textf(c, "Events: 8.2M of 10M").FontSize(12).TextColor(pal.TextMuted)
			ui.Meter(c, 82, 0, 100, &ui.MeterLevels{Warning: 80, Critical: 95}).Height(8).Label("Events")
			ui.Textf(c, "Seats: %d of 25", 9).FontSize(12).TextColor(pal.TextMuted)
			ui.Meter(c, 9, 0, 25, &ui.MeterLevels{Warning: 20, Critical: 24}).Height(8).Label("Seats")
			ui.Text(c, "Storage: 4.1 of 5 GB").FontSize(12).TextColor(pal.TextMuted)
			ui.Meter(c, 4.1, 0, 5, &ui.MeterLevels{Warning: 4, Critical: 4.8}).Height(8).Label("Storage")
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(10, 0, 0, 0).Children(func() {
			ui.Text(c, "Log retention").FontSize(12).TextColor(pal.TextMuted)
			ui.Stepper(c, &d.retention, 7, 365, 30).Label("Retention").Grow(1)
			ui.Textf(c, "%.0f days", d.retention).FontSize(12)
			if ui.Button(c, "Buy more").Clicked() {
				c.Toast("Contact sales to raise the limits")
			}
		})
	})
	Card(c, pal, "Appearance", "", func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Workspace accent").FontSize(12.5).Grow(1)
			ui.ColorWell(c, &d.tint)
			ui.Box(c).Size(64, 28).Radius(8).Background(d.tint)
		})
		ui.Textf(c, "Charts pick the accent up the next frame; the tint reads %s.", rgbOf(d.tint)).FontSize(11.5).TextColor(pal.TextMuted)
	})
}

// settingsDanger is the pane with the irreversible button.
func (d *dashboard) settingsDanger(c *ui.Context, pal Palette) {
	Card(c, pal, "Danger zone", "", func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, "Export everything, then delete the workspace").FontSize(12.5)
				ui.Text(c, "The data goes on the 1st of next month; this cannot be undone.").FontSize(11.5).TextColor(pal.TextMuted)
			})
			if ui.Button(c, "Export data").Clicked() {
				c.Toast("Export queued")
			}
			if ui.Button(c, "Delete workspace").Clicked() {
				d.alert = true
			}
		})
	}).Border(1, pal.Danger.Alpha(0.5))
}
