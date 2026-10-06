package main

// The MujicaUI Forms page: the components of the library's input and
// datetime packages, one small demo per card. The dashboard palette is
// ignored here on purpose — mujicaPage already applied core.Use, so
// every color on this page comes from the library's own tokens. All
// demo values live in one ui.Local keyed "mforms" (its init runs once,
// so the sample events and the clock-derived defaults stay stable);
// say() echoes the latest event into the footer. The host scroll owns
// vertical growth, so nothing here sets Grow.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ZacharyZhang-NY/MujicaUI/core"
	"github.com/ZacharyZhang-NY/MujicaUI/datetime"
	"github.com/ZacharyZhang-NY/MujicaUI/icons"
	"github.com/ZacharyZhang-NY/MujicaUI/input"
)

// mformsState is every demo value on the page; the ui.Local pointer is
// stable across frames, so &s.field goes straight to the controls.
type mformsState struct {
	last string // the latest demo event, shown in the page footer

	// Text-shaped values and numbers.
	name, site, bio, pass, query, phone, pin, guest, mention, size, plan, font, zone, cron string
	mentions                                                                               []input.Mention
	cents                                                                                  int64
	qty, scrub, volume, low, high, gain, level, stars                                      float64

	// Boolean choices.
	digest, mailCh, calCh, wifi, bold bool

	// Colors, tags, chosen sets and the datetime values.
	tint, tag           ui.Color
	align, chips        []string
	fruits, city, roles []string
	tags, band, paths   []string
	drops               []input.DroppedFile
	sig                 [][]input.SignaturePoint
	bulk                int
	banner              bool
	fst                 input.FormState
	fName, fMail        string
	day, due, week      core.Date
	stay                datetime.DateRange
	alarm               datetime.TimeOfDay
	shift               datetime.TimeRange
	meeting             time.Time
	bill                datetime.YearMonth
	vintage             int
	length              time.Duration
	rule                datetime.Recurrence
	free                [][]bool
	avDays              []core.Date
	reminders           []time.Duration
	guests              []datetime.Attendee
	evs                 []datetime.CalendarEvent
	month               datetime.YearMonth
	weekDay, dayV       core.Date
	version             int // bumped when a view moves an event in place
	year                int
	chipOpen            bool
	edit                datetime.CalendarEvent
	deadline, posted    time.Time
	sw                  datetime.StopwatchState
}

// mformsCell is one demo card in a two-column row.
type mformsCell struct {
	title, caption string
	body           func()
}

// mujicaFormsPage showcases MujicaUI's input and datetime packages.
func (d *dashboard) mujicaFormsPage(c *ui.Context, pal Palette) {
	d.mujicaPage(c, "Mujica Forms", "Every input and datetime component of the library, styled only by its own tokens.", func() {
		k := core.Tokens(c)
		s := ui.Local(c.Root(), "mforms", func() mformsState {
			now := time.Now()
			today := core.DateOf(now)
			// WeekPicker's value must BE a week's first day (it panics
			// otherwise), so seed the Monday of the current week.
			monday := today.AddDays(-((int(today.Weekday()) - int(time.Monday) + 7) % 7))
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
			at := func(d, h, m, mins int) (time.Time, time.Time) {
				t := midnight.AddDate(0, 0, d).Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
				return t, t.Add(time.Duration(mins) * time.Minute)
			}
			mk := func(id, title string, col datetime.EventColor, d, h, m, mins int) datetime.CalendarEvent {
				st, en := at(d, h, m, mins)
				return datetime.CalendarEvent{ID: id, Title: title, Color: col, Start: st, End: en, Location: "Rehearsal hall"}
			}
			free := make([][]bool, 5) // five days of half-hour slots, half filled
			for i := range free {
				free[i] = make([]bool, 18)
				for j := range free[i] {
					free[i][j] = (i*7+j*3)%4 == 0
				}
			}
			days := make([]core.Date, 5)
			for i := range days {
				days[i] = today.AddDays(i)
			}
			return mformsState{
				name: "Sakiko", site: "mujica", cents: 123450, qty: 3, scrub: 0.6, size: "m", guest: "Mutsumi Wakaba",
				wifi: true, bold: true, align: []string{"left"}, volume: 40, low: 20, high: 80, plan: "pro",
				font: "Didot", gain: 35, level: 40, tint: ui.Hex("#7D2034"), stars: 3.5, tags: []string{"rose", "thorn"},
				band: []string{"sakiko"}, bulk: 2, banner: true,
				day: today, due: today, week: monday, alarm: datetime.TimeOfDay{Hour: 9, Minute: 30},
				shift:   datetime.TimeRange{Start: datetime.TimeOfDay{Hour: 9}, End: datetime.TimeOfDay{Hour: 17}},
				meeting: now, bill: datetime.YearMonth{Year: now.Year(), Month: now.Month()}, vintage: now.Year(),
				// The calendar views panic on a zero cursor: seed each.
				month:   datetime.YearMonth{Year: now.Year(), Month: now.Month()},
				weekDay: today, dayV: today, year: now.Year(),
				length: 90 * time.Minute, cron: "0 9 * * MON-FRI",
				rule: datetime.Recurrence{Unit: datetime.RepeatWeekly, Interval: 1, Weekdays: []time.Weekday{now.Weekday()}},
				free: free, avDays: days, zone: "Asia/Shanghai", reminders: []time.Duration{15 * time.Minute},
				guests: []datetime.Attendee{
					{Name: "Sakiko Togawa", Status: datetime.AttendeeAccepted, Organizer: true},
					{Name: "Mutsumi Wakaba", Status: datetime.AttendeeTentative},
					{Name: "Uika Misumi", Status: datetime.AttendeeDeclined},
				},
				evs: []datetime.CalendarEvent{
					mk("rehearsal", "Rehearsal", datetime.EventAccent, 0, 9, 0, 120),
					mk("fitting", "Costume fitting", datetime.EventInfo, 0, 11, 0, 60),
					mk("review", "Lyrics review", datetime.EventWarning, 1, 14, 0, 90),
					mk("press", "Press day", datetime.EventDanger, 2, 16, 0, 45),
				},
				edit:     mk("rehearsal", "Rehearsal", datetime.EventAccent, 0, 9, 0, 120),
				deadline: now.Add(time.Hour), posted: now.Add(-5 * time.Minute),
			}
		})
		say := func(msg string) { s.last = msg }
		// row packs demo cards two per row; big demos skip it and take
		// the full width.
		row := func(cells ...mformsCell) {
			ui.Grid(c).Columns(2).Gap(12).Children(func() {
				for _, cell := range cells {
					mujicaCard(c, cell.title, cell.caption, cell.body)
				}
			})
		}
		// apply reports what a calendar view did and moves the sample
		// events in place, bumping the version the views key on.
		apply := func(r datetime.CalendarViewResult) {
			if id, ok := r.Opened(); ok {
				say("opened " + id)
			}
			if day, ok := r.DayChosen(); ok {
				say("day " + day.String())
			}
			if ch, ok := r.Changed(); ok {
				for i := range s.evs {
					if s.evs[i].ID == ch.ID {
						s.evs[i].Start, s.evs[i].End = ch.Start, ch.End
					}
				}
				s.version++
				say("moved " + ch.ID)
			}
		}

		// A page-level notice: Banner closes itself through *open.
		mujicaCard(c, "Banner", "A page-wide notice with an optional action.", func() {
			r := input.Banner(c, &s.banner, "Scheduled maintenance tonight from 22:00 to 23:00.", input.BannerOptions{Severity: core.SeverityWarning, Action: "Details"})
			if r.Action {
				say("banner action pressed")
			}
			if r.Closed {
				say("banner dismissed")
			}
		})

		row(
			mformsCell{"Button", "Click, or press Enter or Space when focused.", func() {
				if input.Button(c, "Save changes", input.ButtonOptions{Icon: icons.Must("check")}).Clicked() {
					say("Button clicked")
				}
			}},
			mformsCell{"IconButton", "A square button for toolbar actions.", func() {
				ui.Row(c).Gap(8).Children(func() {
					if input.IconButton(c, icons.Must("settings"), "Settings", input.IconButtonOptions{}).Clicked() {
						say("IconButton settings clicked")
					}
					if input.IconButton(c, icons.Must("trash-2"), "Delete", input.IconButtonOptions{Variant: input.Danger}).Clicked() {
						say("IconButton delete clicked")
					}
				})
			}},
		)
		row(
			mformsCell{"ButtonGroup", "Segmented actions, one click each.", func() {
				items := []input.ButtonGroupItem{{Label: "Day"}, {Label: "Week"}, {Label: "Month"}}
				if i := input.ButtonGroup(c, items, input.ButtonGroupOptions{Label: "Range"}).ClickedItem(); i >= 0 {
					say("ButtonGroup chose " + items[i].Label)
				}
			}},
			mformsCell{"CopyButton", "Copies text, then shows Copied for a moment.", func() {
				if input.CopyButton(c, "https://example.com/invite/42", input.CopyButtonOptions{Label: "Copy link"}).Changed() {
					say("link copied")
				}
			}},
		)
		row(
			mformsCell{"TextInput", "Single-line text; Enter submits, Escape clears.", func() {
				r := input.TextInput(c, &s.name, input.TextInputOptions{Label: "Stage name", Placeholder: "Your name", Clearable: true})
				if r.Changed() || r.Submitted() {
					say("TextInput: " + s.name)
				}
			}},
			mformsCell{"PasswordInput", "Masked text with a reveal toggle; never logged.", func() {
				if input.PasswordInput(c, &s.pass, input.PasswordInputOptions{Label: "Password", Placeholder: "********"}).Changed() {
					say(fmt.Sprintf("PasswordInput now %d characters", len(s.pass)))
				}
			}},
		)
		row(
			mformsCell{"TextArea", "Multi-line text with a length counter.", func() {
				r := input.TextArea(c, &s.bio, input.TextAreaOptions{Label: "Notes", Placeholder: "Write a dedication…", MinHeight: 56, MaxHeight: 100, MaxLength: 200})
				if r.Changed() {
					say(fmt.Sprintf("TextArea: %d characters", len([]rune(s.bio))))
				}
			}},
			mformsCell{"NumberInput", "A number with bounds, steps and arrows.", func() {
				if input.NumberInput(c, &s.qty, input.NumberInputOptions{Min: 0, Max: 10, Step: 1, Label: "Quantity"}).Changed() {
					say(fmt.Sprintf("quantity %v", s.qty))
				}
			}},
		)
		row(
			mformsCell{"SearchInput", "A search field; Enter submits the query.", func() {
				if input.SearchInput(c, &s.query, input.SearchInputOptions{}).Submitted() {
					say("search " + s.query)
				}
			}},
			mformsCell{"MaskedInput", "A mask of 9 digits, a letters and * either.", func() {
				if input.MaskedInput(c, &s.phone, input.MaskedInputOptions{Mask: "(999) 999-9999", Label: "Phone"}).Changed() {
					say("phone " + input.MaskFormat("(999) 999-9999", s.phone))
				}
			}},
		)
		row(
			mformsCell{"CurrencyInput", "Minor units in, grouped amount out.", func() {
				if input.CurrencyInput(c, &s.cents, input.CurrencyInputOptions{Symbol: "$", Label: "Price"}).Changed() {
					say("price " + input.CurrencyFormat(s.cents, 2, true))
				}
			}},
			mformsCell{"ScrubInput", "Drag the label to scrub, or type a value.", func() {
				if input.ScrubInput(c, "Opacity", &s.scrub, input.ScrubInputOptions{Max: 1, Step: 0.05}).Changed() {
					say(fmt.Sprintf("opacity %.2f", s.scrub))
				}
			}},
		)
		row(
			mformsCell{"PinInput", "One digit per cell; paste fills them all.", func() {
				if input.PinInput(c, &s.pin, input.PinInputOptions{Length: 6, Label: "Access code"}).Completed() {
					say("code " + s.pin)
				}
			}},
			mformsCell{"MentionInput", "Type @ to pick a candidate; Backspace removes one whole.", func() {
				people := []input.MentionCandidate{{ID: "u1", Name: "Sakiko"}, {ID: "u2", Name: "Mutsumi"}, {ID: "u3", Name: "Uika"}}
				if input.MentionInput(c, &s.mention, &s.mentions, input.MentionInputOptions{Label: "Comment",
					Candidates: func(q string) []input.MentionCandidate {
						var out []input.MentionCandidate
						for _, p := range people {
							if strings.HasPrefix(strings.ToLower(p.Name), strings.ToLower(q)) {
								out = append(out, p)
							}
						}
						return out
					}}).Changed() {
					say(fmt.Sprintf("%d mentions", len(s.mentions)))
				}
			}},
		)
		row(
			mformsCell{"Checkbox", "Single boxes and a select-all group.", func() {
				if input.Checkbox(c, &s.digest, "Send the monthly digest", input.CheckboxOptions{}).Changed() {
					say(fmt.Sprintf("digest = %v", s.digest))
				}
				items := []input.CheckboxItem{{Label: "Mail", Value: &s.mailCh}, {Label: "Calendar", Value: &s.calCh}}
				if input.CheckboxGroup(c, "Notifications", items, input.CheckboxGroupOptions{}).Changed() {
					say(fmt.Sprintf("notifications %v %v", s.mailCh, s.calCh))
				}
			}},
			mformsCell{"RadioGroup", "One choice; arrows move between items.", func() {
				sizes := []input.RadioItem[string]{{Value: "s", Label: "Small"}, {Value: "m", Label: "Medium"}, {Value: "l", Label: "Large"}}
				if input.RadioGroup(c, &s.size, sizes, input.RadioGroupOptions{Horizontal: true, Label: "Size"}).Changed() {
					say("size " + s.size)
				}
			}},
		)
		row(
			mformsCell{"Switch", "An on/off setting.", func() {
				if input.Switch(c, &s.wifi, "Wi-Fi", input.SwitchOptions{}).Changed() {
					say(fmt.Sprintf("Wi-Fi = %v", s.wifi))
				}
			}},
			mformsCell{"Toggle", "A button that stays pressed.", func() {
				t := input.Toggle(c, &s.bold, "Bold text", input.ToggleOptions{Icon: icons.Must("bold")})
				if t.Clicked() {
					say(fmt.Sprintf("bold = %v", s.bold))
				}
			}},
		)
		row(
			mformsCell{"ToggleGroup", "One or many pressed buttons.", func() {
				items := []input.ToggleItem[string]{{Value: "left", Label: "Left"}, {Value: "center", Label: "Center"}, {Value: "right", Label: "Right"}}
				if input.ToggleGroup(c, &s.align, items, input.ToggleGroupOptions{Label: "Align"}).Changed() {
					say(fmt.Sprintf("align %v", s.align))
				}
			}},
			mformsCell{"ChoiceChips", "Wrapping filter chips, single or multiple.", func() {
				items := []input.ToggleItem[string]{{Value: "open", Label: "Open"}, {Value: "closed", Label: "Closed"}, {Value: "mine", Label: "Mine"}}
				if input.ChoiceChips(c, &s.chips, items, input.ChoiceChipsOptions{Multiple: true, Label: "Status"}).Changed() {
					say(fmt.Sprintf("chips %v", s.chips))
				}
			}},
		)
		row(
			mformsCell{"Select", "One option from a popup list.", func() {
				plans := []input.SelectOption[string]{{Value: "free", Label: "Free"}, {Value: "pro", Label: "Pro"}, {Value: "team", Label: "Team", Disabled: true}}
				if input.Select(c, &s.plan, plans, input.SelectOptions{Clearable: true, Label: "Plan"}).Changed() {
					say("plan " + s.plan)
				}
			}},
			mformsCell{"MultiSelect", "Several options as removable tags.", func() {
				fruits := []input.SelectOption[string]{{Value: "apple", Label: "Apple"}, {Value: "banana", Label: "Banana"}, {Value: "cherry", Label: "Cherry"}, {Value: "date", Label: "Date"}}
				r := input.MultiSelect(c, &s.fruits, fruits, input.MultiSelectOptions{MaxTags: 2, Label: "Fruits"})
				if r.Changed() {
					say(fmt.Sprintf("fruits %v", s.fruits))
				}
			}},
		)
		row(
			mformsCell{"Combobox", "Type to filter; AllowCustom keeps new text.", func() {
				fonts := []string{"Baskerville", "Bodoni", "Caslon", "Didot", "Garamond", "Georgia"}
				if input.Combobox(c, &s.font, fonts, input.ComboboxOptions{AllowCustom: true, Label: "Typeface"}).Changed() {
					say("typeface " + s.font)
				}
			}},
			mformsCell{"Cascader", "Columns of levels; the value is the path.", func() {
				level := func(p []string) input.CascaderLevel {
					if len(p) == 0 {
						return input.CascaderLevel{Nodes: []input.CascaderNode{{Value: "eu", Label: "Europe"}, {Value: "jp", Label: "Japan"}}}
					}
					return input.CascaderLevel{Nodes: []input.CascaderNode{{Value: "paris", Label: "Paris", Leaf: true}, {Value: "lyon", Label: "Lyon", Leaf: true}, {Value: "tokyo", Label: "Tokyo", Leaf: true}}}
				}
				if input.Cascader(c, &s.city, input.CascaderOptions{Level: level, Label: "City"}).Changed() {
					say("path " + strings.Join(s.city, " / "))
				}
			}},
		)
		row(
			mformsCell{"TreeSelect", "Pick nodes of a tree, leaves or branches.", func() {
				nodes := []input.TreeSelectNode{{ID: "court", Label: "Court", Children: []input.TreeSelectNode{
					{ID: "king", Label: "King"}, {ID: "queen", Label: "Queen"},
				}}, {ID: "church", Label: "Church", Children: []input.TreeSelectNode{{ID: "bishop", Label: "Bishop"}}}}
				if input.TreeSelect(c, &s.roles, nodes, input.TreeSelectOptions{Multiple: true, MaxTags: 2, Label: "Roles"}).Changed() {
					say(fmt.Sprintf("roles %v", s.roles))
				}
			}},
			mformsCell{"TagsInput", "Enter or comma adds a tag, Backspace removes.", func() {
				if input.TagsInput(c, &s.tags, input.TagsInputOptions{Placeholder: "Add a flower", Label: "Flowers", Max: 5}).Changed() {
					say(fmt.Sprintf("tags %v", s.tags))
				}
			}},
		)
		row(
			mformsCell{"Slider", "Drag, arrows, Home and End.", func() {
				ui.Box(c).FillWidth().Children(func() {
					if input.Slider(c, &s.volume, input.SliderOptions{Step: 5, ShowValue: true, Label: "Volume"}).Changed() {
						say(fmt.Sprintf("volume %v", s.volume))
					}
				})
			}},
			mformsCell{"RangeSlider", "Two thumbs for a low–high range.", func() {
				ui.Box(c).FillWidth().Children(func() {
					if input.RangeSlider(c, &s.low, &s.high, input.RangeSliderOptions{Max: 100, Step: 5, ShowValue: true, Label: "Price"}).Changed() {
						say(fmt.Sprintf("price %v–%v", s.low, s.high))
					}
				})
			}},
		)
		row(
			mformsCell{"Knob", "Drag up and down, or use the arrows.", func() {
				if input.Knob(c, &s.gain, input.KnobOptions{Label: "Gain", Ticks: 11}).Changed() {
					say(fmt.Sprintf("gain %v", s.gain))
				}
			}},
			mformsCell{"VerticalSlider", "A slider standing on its side.", func() {
				if input.VerticalSlider(c, &s.level, input.VerticalSliderOptions{Step: 10, ShowValue: true, Label: "Level"}).Changed() {
					say(fmt.Sprintf("level %v", s.level))
				}
			}},
		)
		row(
			mformsCell{"Rating", "Stars in half steps; Home clears, End fills.", func() {
				if input.Rating(c, &s.stars, input.RatingOptions{Step: 0.5, ShowValue: true}).Changed() {
					say(fmt.Sprintf("rating %v", s.stars))
				}
			}},
			mformsCell{"ColorPicker", "A popup square and sliders for any color.", func() {
				if input.ColorPicker(c, &s.tint, input.ColorPickerOptions{Label: "Banner color"}).Changed() {
					say("color picked")
				}
			}},
		)
		row(
			mformsCell{"ColorPalette", "Preset swatches in a grid.", func() {
				swatches := []input.ColorSwatch{
					{Color: ui.Hex("#7D2034"), Name: "Wine"}, {Color: ui.Hex("#AD9164"), Name: "Old gold"},
					{Color: ui.Hex("#355E80"), Name: "Slate blue"}, {Color: ui.Hex("#4A5D43"), Name: "Moss"},
				}
				if input.ColorPalette(c, &s.tag, swatches, input.ColorPaletteOptions{Columns: 4, Label: "Tag color"}).Changed() {
					say("tag color changed")
				}
			}},
			mformsCell{"SignaturePad", "Draw with the pointer; Backspace clears.", func() {
				if input.SignaturePad(c, &s.sig, input.SignaturePadOptions{Height: 100, Label: "Signature"}).Changed() {
					say(fmt.Sprintf("%d strokes, %d bytes of path", len(s.sig), len(input.SignaturePath(s.sig))))
				}
			}},
		)
		row(
			mformsCell{"InputGroup", "Prefix, suffix and buttons around a field.", func() {
				g := input.InputGroup(c, &s.site, input.InputGroupOptions{Prefix: "https://", Suffix: ".org", Label: "Site",
					Buttons: []input.InputGroupButton{{Label: "Clear", Icon: icons.Must("x"), IconOnly: true}}})
				if g.Clicked == 0 {
					s.site = ""
					say("site cleared")
				}
				if g.Changed() {
					say("site " + s.site)
				}
			}},
			mformsCell{"FormField", "Label, description and error around a control.", func() {
				input.FormField(c, "Guest name", input.FormFieldOptions{Required: true, Description: "As written on the invitation."}, func() *ui.Element {
					return input.TextInput(c, &s.guest, input.TextInputOptions{Placeholder: "Full name"}).Element
				})
			}},
		)
		row(
			mformsCell{"FilePicker", "The system open dialog, filtered or not.", func() {
				p := input.FilePicker(c, &s.paths, input.FilePickerOptions{Multiple: true, Title: "Choose scores"})
				if p.Changed() {
					say(fmt.Sprintf("picked %v", s.paths))
				}
				if p.Canceled() {
					say("picker canceled")
				}
			}},
			mformsCell{"FileDropZone", "Drop files, or browse; the list is capped.", func() {
				if input.FileDropZone(c, &s.drops, input.FileDropZoneOptions{MaxFiles: 3, MaxSize: 10 << 20}).Changed() {
					say(fmt.Sprintf("%d files dropped", len(s.drops)))
				}
			}},
		)

		// The Form demo is full width: two validated items plus the
		// built-in submit and reset buttons.
		mujicaCard(c, "Form", "Validation on leave, submit and reset.", func() {
			res := input.Form(c, &s.fst, input.FormOptions{}, func(f *input.FormScope) {
				input.FormItem(c, f, "name", &s.fName, input.FormItemOptions[string]{Label: "Stage name", Required: true, Initial: "Sakiko"}, func() *ui.Element {
					return input.TextInput(c, &s.fName, input.TextInputOptions{Placeholder: "Your name"}).Element
				})
				input.FormItem(c, f, "mail", &s.fMail, input.FormItemOptions[string]{Label: "Email", Required: true,
					Validate: func(v string) string {
						if !strings.Contains(v, "@") {
							return "Enter an email address."
						}
						return ""
					}}, func() *ui.Element {
					return input.TextInput(c, &s.fMail, input.TextInputOptions{Placeholder: "you@example.com"}).Element
				})
			})
			if res.Submitted() {
				s.fst.EndSubmit("")
				say("form submitted")
			}
			if res.Reset() {
				say("form reset to initials")
			}
		})

		row(
			mformsCell{"Transfer", "Move items between two searchable lists.", func() {
				items := []input.TransferItem{
					{ID: "sakiko", Label: "Sakiko Togawa"}, {ID: "uika", Label: "Uika Misumi"},
					{ID: "mutsumi", Label: "Mutsumi Wakaba"}, {ID: "nyamu", Label: "Nyamu Yuutenji", Disabled: true},
				}
				if input.Transfer(c, &s.band, items, input.TransferOptions{SourceTitle: "Auditions", TargetTitle: "Band", Height: 130}).Changed() {
					say(fmt.Sprintf("band %v", s.band))
				}
			}},
			mformsCell{"BulkActionBar", "Appears while a selection exists; Escape clears.", func() {
				r := input.BulkActionBar(c, s.bulk, input.BulkActionBarOptions{}, func() {
					if input.Button(c, "Archive", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
						say(fmt.Sprintf("archived %d rows", s.bulk))
					}
				})
				if r.Changed() {
					s.bulk = 0
					say("selection cleared")
				}
			}},
		)
		row(
			mformsCell{"HoldToConfirm", "Hold for the whole duration to confirm.", func() {
				if input.HoldToConfirm(c, "Delete project", input.HoldToConfirmOptions{}).Changed() {
					say("project deleted")
				}
			}},
			mformsCell{"QRCode", "Encoded offline, any error-correction level.", func() {
				input.QRCode(c, "https://example.com/mujica", input.QRCodeOptions{Level: input.QRQuartile, Size: 120})
			}},
		)

		row(
			mformsCell{"Calendar", "A month grid; arrows move, Enter chooses.", func() {
				if datetime.Calendar(c, &s.day, datetime.CalendarOptions{WeekStart: datetime.WeekStartMonday, Label: "Calendar"}).Changed() {
					say("Calendar chose " + s.day.String())
				}
			}},
			mformsCell{"YearView", "Twelve mini months; a click picks a day.", func() {
				if day, ok := datetime.YearView(c, &s.year, s.evs, datetime.YearViewOptions{Location: time.Local}).DayChosen(); ok {
					say("day " + day.String())
				}
			}},
		)
		row(
			mformsCell{"DatePicker", "Type a date or open the calendar.", func() {
				if datetime.DatePicker(c, &s.due, datetime.DatePickerOptions{Label: "Due date", Clearable: true}).Changed() {
					say("due " + s.due.String())
				}
			}},
			mformsCell{"WeekPicker", "Picks the week a day falls in.", func() {
				if datetime.WeekPicker(c, &s.week, datetime.WeekPickerOptions{WeekStart: datetime.WeekStartMonday, Clearable: true}).Changed() {
					say("week of " + s.week.String())
				}
			}},
		)
		row(
			mformsCell{"DateRangePicker", "Two ends from one calendar.", func() {
				r := datetime.DateRangePicker(c, &s.stay, datetime.DateRangePickerOptions{Label: "Stay"})
				if r.Changed() {
					say(fmt.Sprintf("stay %v – %v (%d days)", s.stay.Start, s.stay.End, s.stay.Days()))
				}
				if r.Canceled() {
					say("stay canceled")
				}
			}},
			mformsCell{"MonthPicker", "A year of months, then a decade grid.", func() {
				if datetime.MonthPicker(c, &s.bill, datetime.MonthPickerOptions{Label: "Billing month", Clearable: true}).Changed() {
					say("month " + s.bill.String())
				}
			}},
		)
		row(
			mformsCell{"TimePicker", "Hour and minute segments, or the clock list.", func() {
				if datetime.TimePicker(c, &s.alarm, datetime.TimePickerOptions{Label: "Alarm", MinuteStep: 5}).Changed() {
					say("alarm " + s.alarm.String())
				}
			}},
			mformsCell{"TimeRangePicker", "A start and an end; overnight allowed.", func() {
				if datetime.TimeRangePicker(c, &s.shift, datetime.TimeRangePickerOptions{Label: "Shift", MinuteStep: 15}).Changed() {
					say(fmt.Sprintf("shift %v – %v", s.shift.Start, s.shift.End))
				}
			}},
		)
		row(
			mformsCell{"DateTimePicker", "A calendar and a time in one popup.", func() {
				r := datetime.DateTimePicker(c, &s.meeting, datetime.DateTimePickerOptions{Label: "Meeting", Location: time.Local})
				if r.Changed() {
					say("meeting " + s.meeting.Format(time.RFC3339))
				}
				if r.Canceled() {
					say("meeting canceled")
				}
			}},
			mformsCell{"DurationPicker", "A length between bounds.", func() {
				if datetime.DurationPicker(c, &s.length, datetime.DurationPickerOptions{Min: 15 * time.Minute, Max: 8 * time.Hour}).Changed() {
					say("length " + s.length.String())
				}
			}},
		)
		row(
			mformsCell{"YearPicker", "A grid of years with bounds.", func() {
				if datetime.YearPicker(c, &s.vintage, datetime.YearPickerOptions{Label: "Vintage", Min: 1990, Max: time.Now().Year(), Clearable: true}).Changed() {
					say(fmt.Sprintf("vintage %d", s.vintage))
				}
			}},
			mformsCell{"TimezoneSelect", "Searchable IANA zones with local time.", func() {
				if datetime.TimezoneSelect(c, &s.zone, datetime.TimezoneSelectOptions{}).Changed() {
					say("zone " + s.zone)
				}
			}},
		)
		row(
			mformsCell{"CronEditor", "Builds a five-field cron and previews runs.", func() {
				if datetime.CronEditor(c, &s.cron, datetime.CronEditorOptions{Location: time.Local}).Changed() {
					if _, err := datetime.ParseCron(s.cron); err != nil {
						say("invalid cron " + s.cron)
					} else {
						say("schedule " + s.cron)
					}
				}
			}},
			mformsCell{"ReminderPicker", "Before-hand offsets, capped by Max.", func() {
				if datetime.ReminderPicker(c, &s.reminders, datetime.ReminderPickerOptions{Max: 3}).Changed() {
					say(fmt.Sprintf("reminders %v", s.reminders))
				}
			}},
		)
		row(
			mformsCell{"RecurrenceEditor", "Repeat rule with a plain-language summary.", func() {
				r := datetime.RecurrenceEditor(c, &s.rule, datetime.RecurrenceEditorOptions{Start: core.DateOf(time.Now())})
				if r.Changed() {
					msg := datetime.RecurrenceSummary(c, s.rule)
					if p := r.Problem(); p != "" {
						msg += " — " + p
					}
					say(msg)
				}
			}},
			mformsCell{"AttendeeList", "Invitees with status; Remove drops a row.", func() {
				if i, ok := datetime.AttendeeList(c, s.guests, datetime.AttendeeListOptions{Removable: true}).Removed(); ok {
					say("removed " + s.guests[i].Name)
					s.guests = append(s.guests[:i:i], s.guests[i+1:]...)
				}
			}},
		)
		row(
			mformsCell{"EventChip", "One event as a clickable block.", func() {
				ev := s.evs[0]
				if datetime.EventChip(c, ev, datetime.EventChipOptions{Location: time.Local, Inline: true}).Clicked() {
					say("clicked " + ev.Title)
				}
			}},
			mformsCell{"EventPopover", "Click the chip to open the details.", func() {
				ev := s.evs[0]
				chip := datetime.EventChip(c, ev, datetime.EventChipOptions{Location: time.Local, Inline: true})
				if chip.Clicked() {
					s.chipOpen = true
				}
				r := datetime.EventPopover(c, chip, &s.chipOpen, ev, datetime.EventPopoverOptions{Location: time.Local})
				if r.Edit() {
					say("edit " + ev.ID)
				}
				if r.Delete() {
					say("delete " + ev.ID)
				}
			}},
		)
		row(
			mformsCell{"RelativeTime", "Renders a past or future time in words.", func() {
				datetime.RelativeTime(c, s.posted, datetime.RelativeTimeOptions{Location: time.Local})
				if input.Button(c, "Now", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
					s.posted = time.Now()
					say("posted set to now")
				}
			}},
			mformsCell{"Countdown", "Ticks toward a deadline, then finishes.", func() {
				if datetime.Countdown(c, s.deadline, datetime.CountdownOptions{Format: datetime.CountdownWords}).Finished() {
					say("countdown finished")
				}
			}},
		)
		row(
			mformsCell{"Stopwatch", "Start, lap and reset; ticks while running.", func() {
				if datetime.Stopwatch(c, &s.sw, datetime.StopwatchOptions{}).Changed() {
					verb := "paused at"
					if s.sw.Running() {
						verb = "running, lap at"
					}
					say(verb + " " + datetime.StopwatchText(s.sw.Elapsed(time.Now())))
				}
			}},
			mformsCell{"CurrentTimeIndicator", "The now line, drawn against a grid.", func() {
				ui.Box(c).Size(240, 140).Border(1, k.Border).Children(func() {
					datetime.CurrentTimeIndicator(c, datetime.CurrentTimeIndicatorOptions{Location: time.Local})
				})
			}},
		)

		// AvailabilityPicker: full width, five days of half-hour slots.
		mujicaCard(c, "AvailabilityPicker", "Paint your free slots against others'.", func() {
			if datetime.AvailabilityPicker(c, &s.free, datetime.AvailabilityPickerOptions{Days: s.avDays}).Changed() {
				n := 0
				for _, d := range s.free {
					for _, v := range d {
						if v {
							n++
						}
					}
				}
				say(fmt.Sprintf("%d free slots", n))
			}
		})

		// EventEditor: full width, it is a small form of its own.
		mujicaCard(c, "EventEditor", "Title, time and guests; Save or Cancel.", func() {
			r := datetime.EventEditor(c, &s.edit, datetime.EventEditorOptions{Location: time.Local})
			if r.Saved() {
				say("saved " + s.edit.Title)
			}
			if r.Canceled() {
				say("editor canceled")
			}
		})

		// The calendar views: full width each; drag events to move or
		// resize them, click an empty slot to create.
		mujicaCard(c, "CalendarMonthView", "A month of events; drag to reschedule.", func() {
			apply(datetime.CalendarMonthView(c, &s.month, s.evs, datetime.CalendarMonthViewOptions{Location: time.Local, Version: s.version, MaxPerDay: 2}))
		})
		mujicaCard(c, "CalendarWeekView", "A week time grid with 30-minute slots.", func() {
			apply(datetime.CalendarWeekView(c, &s.weekDay, s.evs, datetime.CalendarWeekViewOptions{Location: time.Local, Version: s.version, SlotMinutes: 30}))
		})
		mujicaCard(c, "CalendarDayView", "One day, its columns splitting overlaps.", func() {
			apply(datetime.CalendarDayView(c, &s.dayV, s.evs, datetime.CalendarDayViewOptions{Location: time.Local, Version: s.version}))
		})
		mujicaCard(c, "AgendaView", "A flat, scrollable list of upcoming events.", func() {
			ui.Column(c).Height(260).Children(func() {
				if id, ok := datetime.AgendaView(c, s.evs, datetime.AgendaViewOptions{Location: time.Local, Days: 7}).Opened(); ok {
					say("opened " + id)
				}
			})
		})

		// The footer: the latest event any demo reported this frame.
		last := s.last
		if last == "" {
			last = "interact with a demo"
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Last event").FontSize(11.5).TextColor(k.TextMuted)
			ui.Text(c, last).FontSize(11.5)
		})
	})
}
