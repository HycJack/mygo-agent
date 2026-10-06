package main

// The Controls page: every input widget MyGo ships, in the cards a
// dashboard's filters and forms would use.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (d *dashboard) controlsPage(c *ui.Context, pal Palette) {
	PageTitle(c, pal, "Controls", "Text, choices, ranges, dates and colors — the form vocabulary of the toolkit.")

	ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
		// The profile form: fields with labels, descriptions, validation
		// and a save that offers an undo.
		Card(c, pal, "Profile", "a Form with validation", func() {
			ui.Form(c, func() {
				ui.Field(c, "Name", func() {
					ui.TextInput(c, &d.name).Placeholder("Ada Lovelace")
				})
				invalid := ""
				if d.email != "" && !strings.Contains(d.email, "@") {
					invalid = "Enter an email address, such as ada@acme.dev."
				}
				ui.Field(c, "Email", func() {
					if ui.TextInput(c, &d.email).Placeholder("ada@acme.dev").Submitted() {
						c.Toast("Saved")
					}
				}).Description("Press Enter to save.").Error(invalid)
				ui.Field(c, "Bio", func() {
					ui.TextArea(c, &d.bio).Placeholder("A line or two about you.").Height(80)
				}).Description(fmt.Sprintf("%d characters", len([]rune(d.bio))))
				ui.Fieldset(c, "Preferences", func() {
					ui.Field(c, "Start date", func() { ui.DateInput(c, &d.meeting) })
					ui.Field(c, "Newsletter", func() { ui.Checkbox(c, &d.news, "Send me the monthly digest") })
				})
			})
			if ui.PrimaryButton(c, "Save profile").Clicked() {
				d.notes++
				c.ToastAction("Profile saved", "Undo", func() { d.notes--; c.Toast("Reverted") })
			}
		}).Width(400).Shrink(0)

		ui.Column(c).Grow(1).Gap(14).Children(func() {
			// Choices: switches, radios, checkboxes, and the pickers that
			// choose one of many.
			Card(c, pal, "Choices", "", func() {
				ui.Column(c).Gap(10).Children(func() {
					labeledSwitch(c, pal, &d.notifyMail, "Alerts by email")
					labeledSwitch(c, pal, &d.notifyDigest, "Weekly digest")
					labeledSwitch(c, pal, &d.notifyMentions, "Mentions only")
				})
				ui.RadioGroup(c, func() {
					for _, p := range []string{"Starter", "Pro", "Enterprise"} {
						ui.Radio(c, &d.plan, p, p)
					}
				}).Row().Gap(18).Label("Plan")
				ui.CheckboxGroup(c, "Regions", func() {
					ui.Row(c).Gap(14).Children(func() {
						ui.Checkbox(c, &d.regionNA, "NA")
						ui.Checkbox(c, &d.regionEU, "EU")
					})
				})
				ui.Row(c).Gap(10).Wrap().Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Role").FontSize(12).TextColor(pal.TextMuted)
						ui.Select(c, &d.role, []string{"Admin", "Editor", "Analyst", "Viewer"}).Width(130)
					})
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Region").FontSize(12).TextColor(pal.TextMuted)
						ui.Select(c, &d.region, Regions).Width(110)
					})
				})
			})

			// Search and choose: the combobox, the autocomplete and the
			// token field.
			Card(c, pal, "Search and choose", "", func() {
				ui.Row(c).Gap(10).Wrap().Children(func() {
					ui.Combobox(c, &d.font, []string{"Inter", "Avenir", "Courier", "Futura", "Georgia", "Helvetica", "Menlo", "Optima"}).Label("Font").Width(180)
					ui.Autocomplete(c, &d.city, []string{"Amsterdam", "Berlin", "Lisbon", "London", "Madrid", "Paris", "Prague", "Rome", "Vienna"}).Label("HQ city").Placeholder("City").Width(180)
				})
				ui.TokenField(c, &d.tags, []string{"design", "go", "native", "performance", "release", "growth"}).Label("Tags")
				ui.Text(c, "Type to filter; Up and Down move, Enter chooses. Enter or a comma adds a tag; Backspace removes the last.").FontSize(11.5).TextColor(pal.TextMuted)
			})
		})
	})

	// Ranges: sliders of every kind, a number input and a stepper.
	ui.Row(c).Gap(14).AlignItems(ui.Start).Children(func() {
		Card(c, pal, "Ranges", "budgets, quality, prices", func() {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "Budget").Width(64).FontSize(12).TextColor(pal.TextMuted)
				ui.Slider(c, &d.budget, 0, 10000).Label("Budget").Grow(1)
				ui.Text(c, formatMoney(d.budget)).Width(70).TextAlign(ui.End).FontSize(12)
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "Quality").Width(64).FontSize(12).TextColor(pal.TextMuted)
				ui.StepSlider(c, &d.quality, 0, 100, 25).Label("Quality").Grow(1)
				ui.Textf(c, "%.0f%%", d.quality).Width(70).TextAlign(ui.End).FontSize(12)
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "Price").Width(64).FontSize(12).TextColor(pal.TextMuted)
				ui.RangeSlider(c, &d.priceLow, &d.priceHigh, 0, 500, 10).Label("Price").Grow(1)
				ui.Textf(c, "$%.0f–%.0f", d.priceLow, d.priceHigh).Width(90).TextAlign(ui.End).FontSize(12)
			})
			ui.Progress(c, d.quality/100)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Copies").Width(64).FontSize(12).TextColor(pal.TextMuted)
				ui.NumberInput(c, &d.copies, 1, 99, 1).Label("Copies")
				ui.Text(c, "Retention").FontSize(12).TextColor(pal.TextMuted).Margin(0, 0, 0, 12)
				ui.Stepper(c, &d.retention, 7, 365, 1).Label("Retention").Grow(1)
				ui.Textf(c, "%.0f days", d.retention).Width(70).TextAlign(ui.End).FontSize(12)
			})
		}).Grow(1)

		// Schedule: the calendar, a time input and the color well.
		Card(c, pal, "Schedule", "a report call", func() {
			ui.Row(c).Gap(16).AlignItems(ui.Start).Children(func() {
				ui.Calendar(c, &d.meeting).Label("Date")
				ui.Column(c).Gap(12).Grow(1).Children(func() {
					ui.Form(c, func() {
						ui.Field(c, "Time", func() { ui.TimeInput(c, &d.meeting).Label("Report call") })
						ui.Field(c, "Accent", func() { ui.ColorWell(c, &d.tint) })
					})
					ui.Text(c, d.meeting.Format("Monday, January 2 at 15:04")).TextColor(d.tint).FontSize(13)
					ui.Rating(c, &d.starts, 5).Label("Priority")
				})
			})
		}).Width(400).Shrink(0)
	})

	// The view switchers: the agent's Segments beside the toolkit's
	// Segmented, ToggleGroup and Tabs.
	Card(c, pal, "View switchers", "", func() {
		ui.Row(c).Gap(16).Wrap().AlignItems(ui.Center).Children(func() {
			Segments(c, pal, d.theRange, rangeNames, func(i int) { d.theRange = i })
			ui.Segmented(c, &d.prefTab, "Table", "Cards", "Chart").Label("Layout")
			ui.ToggleGroup(c, func() {
				ui.Toggle(c, &d.notifyMail, "Legend")
				ui.Toggle(c, &d.notifyDigest, "Gridlines")
			}).Label("Overlay")
			ui.Tabs(c, &d.prefTab, "Day", "Week", "Month")
		})
	})
}
