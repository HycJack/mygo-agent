package ui

import (
	"strconv"
	"strings"
	"time"

	"mygo-agent/internal/harness"

	"github.com/egoist/mygo/ui"
)

// The thread transcript renders here over ViewModel snapshots
// (spec/architecture.md): the host copies its messages into a
// TranscriptVM each frame and the view reports back through
// TranscriptActions. Nothing in this file touches host state.

// BlockVM is one card of an assistant message: a tool call, a file
// change, an error, a pending approval, or a reasoning note.
type BlockVM struct {
	Type, Text, File, Output string
	Add, Del                 int
	Lines                    []harness.DiffLine
	Exit                     int
	Ms                       int64
	Open, Edit, Running      bool
	ToolID, ApprovalID       string
}

// Item kinds: the two shapes an ordered piece of a turn can take.
const (
	// ItemText is the agent's prose at the point it was said.
	ItemText = "text"
	// ItemBlock is a single card, drawn as itself.
	ItemBlock = "block"
	// ItemGroup is a folded run of two or more alike cards, drawn as one
	// row that expands into them.
	ItemGroup = "group"
)

// ItemVM is one piece of an assistant turn, in arrival order. The Host
// builds the sequence: prose, a tool call, more prose, another tool call.
// Order is the whole point — a turn has to read the way it happened, not
// as all the cards and then all the prose.
type ItemVM struct {
	Kind   string // ItemText | ItemBlock | ItemGroup
	Type   string // ItemBlock/ItemGroup: the card kind
	Text   string // ItemText
	Blocks []BlockVM
	At     int  // index of the first block in the message, for ToggleBlock
	Open   bool // ItemGroup: expanded
	Ms     int64
	Failed int // ItemGroup: members that failed
}

// MessageVM is one turn of the conversation.
type MessageVM struct {
	ID, Role, Text string
	At             time.Time
	Running        bool
	// Items is the ordered sequence the transcript draws. Text stays on
	// the message too: it is the aggregate the copy button, the rail
	// preview and the built-in transcript seeding all read.
	Items []ItemVM
	// Blocks is Items' card members, flattened, for callers that index by
	// block position. Deprecated in favour of Items; kept because the
	// approval and fold actions address blocks by that index.
	Blocks []BlockVM
}

// TranscriptVM is the render input of the conversation view. List and
// Md alias host-owned render state (the list's place and the markdown
// cache): they are pointers the view reads and writes, not policy.
type TranscriptVM struct {
	Messages []MessageVM
	Running  bool // a turn is in flight anywhere
	List     *ui.ListState
	Md       *MdCache
	Pal      Palette
}

// TranscriptActions is what the transcript calls back for. Block indexes
// are positions in the MessageVM's Blocks, which the host built in its
// own order.
type TranscriptActions interface {
	// ToggleBlock folds or unfolds a card.
	ToggleBlock(msgID string, bi int)
	// Resend runs a user message's text as a fresh turn.
	Resend(msgID, text string)
	// Regenerate re-runs the turn that produced the last reply.
	Regenerate()
	// ResolveApproval answers a pending approval card.
	ResolveApproval(callID string, approved bool)
}

// Transcript renders the conversation: the anchor rail beside a
// virtualized list that follows its end as the agent replies.
func Transcript(c *ui.Context, vm *TranscriptVM, acts TranscriptActions) {
	st := vm.List
	st.Key = func(i int) any {
		// The list can ask for a row this frame's snapshot does not
		// have, when the messages changed under it; answer with the
		// index instead of panicking.
		if i < 0 || i >= len(vm.Messages) {
			return i
		}
		return vm.Messages[i].ID
	}
	st.FollowEnd = true
	// The anchor rail hugs the sidebar divider, vertically centered by
	// the row's stretch; the messages keep a centered reading measure.
	// A click glides to the target instead of teleporting, ZCode-style,
	// and the last messages settle at the list's end — so what a jump
	// puts at the top of the viewport is not always the message it
	// jumped to, which is why the rail tracks the reader's place rather
	// than the top row.
	root := ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	glide := ui.Local(root, "glide", func() railGlide { return railGlide{} })
	// A glide in progress advances a few rows per frame; the last two
	// messages settle End-aligned, everything else Start-aligned.
	if glide.active {
		dist := glide.target - glide.pos
		step := dist / 4
		if dist < 0 && step > -2 {
			step = dist
		}
		if dist > 0 && step < 2 {
			step = dist
		}
		if dist == 0 {
			glide.active = false
			align := ui.Start
			if glide.target >= len(vm.Messages)-2 {
				align = ui.End
			}
			st.ScrollTo(glide.target, align)
		} else {
			glide.pos += step
			st.ScrollTo(glide.pos, ui.Start)
			c.AnimationFrame()
		}
	}
	root.Children(func() {
		// The anchor rail: one dash per message, hover previews it,
		// click glides to it.
		items := make([]RailItem, len(vm.Messages))
		first, last := st.Visible()
		// Where the reader is. Mid-transcript that is the top of the
		// viewport, but at the end it is the last row: the list settles
		// its tail End-aligned, so a jump to the last message leaves the
		// rows above it at the top of the viewport — and lighting the
		// top row would leave the rail pointing at an earlier message
		// than the one just jumped to, which is the one message the
		// reader is sure they are on.
		at := first
		if st.AtEnd() && last >= first {
			at = last
		}
		for i := range vm.Messages {
			m := &vm.Messages[i]
			items[i] = RailItem{ID: m.ID, Preview: railPreview(m), Active: i == at}
		}
		AnchorRail(c, items, Colors{
			Active:    vm.Pal.Text,
			TextMuted: vm.Pal.TextMuted,
			Border:    vm.Pal.Border,
			Surface:   vm.Pal.Card,
			Text:      vm.Pal.Text,
		}, func(it RailItem, index int) {
			*glide = railGlide{target: index, pos: at, active: true}
			c.AnimationFrame()
		})
		// AlignItems(Stretch) is load-bearing, not decoration: a Row
		// centers its children across its main axis by default, and a
		// list that is not stretched keeps its own measured height —
		// here the whole window — instead of the column's. Its viewport
		// then holds the entire transcript, so every row reads as
		// visible, atEnd stays true whatever it is scrolled to, and a
		// rail jump has no state left to take effect on.
		col := ui.Row(c).FillWidth().MaxWidth(880).Margin(0, ui.Auto).AlignItems(ui.Stretch)
		col.Children(func() {
			ui.List(c, st, len(vm.Messages), func(i int) {
				col := ui.Column(c).FillWidth().MaxWidth(860).Margin(0, ui.Auto)
				col.Children(func() {
					messageRow(c, vm, acts, i)
				})
			}).Grow(1).MinHeight(0).Justify(ui.End).Gap(20).Padding(24, 18, 16)
		})
	})
}

// railGlide is one jump's animated scroll: the row steps toward the
// target a few rows per frame and settles on it.
type railGlide struct {
	target int
	pos    int
	active bool
}

// railPreview boils a message down to a short preview for the rail. It
// reads the first thing the turn actually said rather than the aggregate:
// a turn that talked, ran a tool, then talked again has a prose aggregate
// of both halves, and the rail should name the turn, not concatenate it.
func railPreview(m *MessageVM) string {
	for i := range m.Items {
		it := &m.Items[i]
		if it.Kind == ItemText {
			if s := strings.TrimSpace(it.Text); s != "" {
				return firstLine(s)
			}
			continue
		}
		for _, b := range it.Blocks {
			switch b.Type {
			case "command":
				return "$ " + b.Text
			case "diff":
				return "edited " + b.File
			case "error":
				return b.Text
			case "reasoning":
				return b.Text
			}
		}
	}
	if s := strings.TrimSpace(m.Text); s != "" {
		return firstLine(s)
	}
	return "message"
}

// firstLine is the preview's one line: markdown headings and list markers
// are noise in a 40-pixel gutter.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimLeft(s, "#>-* \t")
	return strings.TrimSpace(s)
}

// actionRowHeight is the reserved height of every message's action row:
// the row always exists so hover never shifts layout — a row that grows
// under the pointer would push the list around and the buttons would
// move out from under the click.
const actionRowHeight = 24

// messageRow draws one turn: the user's prompt in a bubble, or the
// assistant's cards and reply — each with its hover actions (copy,
// resend / regenerate) and time, the way ZCode does.
func messageRow(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, i int) {
	m := &vm.Messages[i]

	if m.Role == "user" {
		// Hover is read off the row itself: an empty detector element has
		// no height, so it can never sit under the pointer.
		col := ui.Column(c).FillWidth().Gap(2)
		showActions := col.Hovered()
		col.Children(func() {
			bubble := ui.Box(c).MaxWidthPercent(72).Padding(9, 14).Radius(14).
				Background(vm.Pal.UserBubble)
			bubble.Margin(0, 0, 0, ui.Auto)
			bubble.Children(func() {
				ui.Text(c, m.Text).Selectable()
			})
			bar := ui.Row(c).MinHeight(actionRowHeight).Justify(ui.End).AlignItems(ui.Center)
			bar.Children(func() {
				messageActions(c, vm, acts, m, false, showActions)
			})
		})
		return
	}
	t := c.Theme()
	isLast := !vm.Running && i == len(vm.Messages)-1
	root := ui.Row(c).Gap(10).AlignItems(ui.Start)
	showActions := root.Hovered()
	fadeIn(c, root, m)
	root.Children(func() {
		avatar := ui.Box(c).Size(26, 26).Radius(7).Background(vm.Pal.Card).Border(1, vm.Pal.Border).Center()
		avatar.Children(func() { ui.Icon(c, IconSparkles).FontSize(14).TextColor(t.Text) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			// The turn in arrival order. Prose renders at the point it was
			// said, so a tool call the agent ran mid-sentence stays between
			// the two halves of what it said.
			for ii := range m.Items {
				item(c, vm, acts, m, &m.Items[ii])
			}
			if m.Running && len(m.Items) == 0 {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, "Working…").FontSize(12).TextColor(t.TextMuted)
				})
			}
			bar := ui.Row(c).MinHeight(actionRowHeight).AlignItems(ui.Center)
			bar.Children(func() {
				messageActions(c, vm, acts, m, isLast, showActions)
			})
		})
	})
}

// item draws one ordered piece of a turn: the prose, a single card, or a
// folded run of alike cards.
func item(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, it *ItemVM) {
	switch it.Kind {
	case ItemText:
		Markdown(c, vm.Md, "msg:"+m.ID+":"+strconv.Itoa(it.At), it.Text, !m.Running, vm.Pal)
	case ItemBlock:
		block(c, vm, acts, m, it.Blocks[0], it.At)
	case ItemGroup:
		itemGroup(c, vm, acts, m, it)
	}
}

// groupHeader is the summary a folded run shows: what it did, how many,
// how long, and whether anything failed. The count and the duration are
// the reason a run is worth folding — twenty rows of `$ go test` is
// noise, "20 commands · 4.1s" is the fact.
func groupHeader(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, it *ItemVM) {
	t := c.Theme()
	head := ui.Row(c).MinHeight(22).Padding(0, 8).Gap(8).AlignItems(ui.Center).
		Radius(6).Cursor(ui.CursorPointer)
	if head.Hovered() || it.Open {
		head.Background(vm.Pal.Hover)
	}
	if head.Clicked() {
		acts.ToggleBlock(m.ID, it.At)
	}
	label := ""
	icon := IconChevDown
	switch it.Type {
	case "command":
		icon = IconTerminal
		label = plural(len(it.Blocks), "command", "commands")
		if it.Failed > 0 {
			// Spelled out rather than plural()'d: "1 failed" has to keep
			// its number, and plural(n, "failed", "failed") drops it.
			label += " · " + strconv.Itoa(it.Failed) + " failed"
		}
	case "diff":
		icon = IconFileCode
		var add, del int
		if len(it.Blocks) > 0 {
			label = it.Blocks[0].File
		}
		for _, b := range it.Blocks {
			add += b.Add
			del += b.Del
		}
		if add > 0 {
			label += "  +" + strconv.Itoa(add)
		}
		if del > 0 {
			label += "  −" + strconv.Itoa(del)
		}
	case "reasoning":
		icon = IconSparkles
		label = "Thinking"
	case "note":
		icon = IconDot
		label = plural(len(it.Blocks), "note", "notes")
	}
	head.Children(func() {
		chev := ui.Icon(c, icon).FontSize(12).TextColor(vm.Pal.TextMuted)
		if it.Type == "command" || it.Type == "diff" {
			// A run with a failure earns attention even folded.
			if it.Failed > 0 {
				chev.TextColor(vm.Pal.Danger)
			}
		}
		ui.Text(c, label).FontSize(12).TextColor(t.Text).Grow(1).MinWidth(0).SingleLine()
		if it.Ms > 0 {
			ui.Text(c, Duration(it.Ms)).FontSize(10.5).TextColor(vm.Pal.TextMuted)
		}
		mark := ui.Icon(c, IconChevDown).FontSize(12).TextColor(vm.Pal.TextMuted)
		if it.Open {
			mark.Rotate(180)
		}
	})
}

// itemGroup is a folded run: one summary row that expands into its cards.
func itemGroup(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, it *ItemVM) {
	groupHeader(c, vm, acts, m, it)
	if !it.Open {
		return
	}
	// The members keep their own indent so an expanded run reads as the
	// same cards it was folded from.
	ui.Column(c).Gap(2).Padding(0, 0, 4, 12).Children(func() {
		for bi := range it.Blocks {
			block(c, vm, acts, m, it.Blocks[bi], it.At+bi)
		}
	})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return strconv.Itoa(n) + " " + many
}

// fadeIn is the ZCode-style motion: a message that was just created
// fades in over 180ms; older messages (history, scrolling back) render
// at full opacity from the first frame. State rides the keyed row, so a
// streaming message animates once, not per frame.
func fadeIn(c *ui.Context, root *ui.Element, m *MessageVM) {
	const dur = 180 * time.Millisecond
	born := ui.Local(root, "born", func() time.Time { return time.Time{} })
	if born.IsZero() {
		*born = c.Now()
	}
	age := c.Now().Sub(*born)
	if age >= dur || c.Now().Sub(m.At) > 2*time.Second {
		return
	}
	root.Opacity(float32(age) / float32(dur))
	c.AnimationFrame() // keep ticking until the fade completes
}

// messageActions is the action row under a message: its time and its
// copy / resend / regenerate buttons. The row is always built — only its
// opacity follows the hover — so a press never unmounts the button under
// the pointer (mygo suppresses Hovered for every element but the pressed
// one, which would otherwise make the buttons vanish mid-click). A
// pressed button keeps itself visible through its own Hovered.
func messageActions(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, canRegenerate, visible bool) {
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		stamp := ui.Text(c, m.At.Format("15:04")).FontSize(10.5).TextColor(vm.Pal.TextMuted).
			Tooltip(m.At.Format("2006-01-02 15:04:05"))
		if !visible {
			stamp.Opacity(0)
		}
		ui.Box(c).Width(4)
		msgAction(c, vm.Pal, "Copy", IconCopy, visible, func() {
			c.WriteClipboard(m.Text)
			c.Toast("Copied")
		})
		if m.Role == "user" {
			msgAction(c, vm.Pal, "Resend", IconArrowUp, visible, func() {
				acts.Resend(m.ID, m.Text)
			})
		} else if canRegenerate {
			msgAction(c, vm.Pal, "Regenerate", IconRefresh, visible, func() {
				acts.Regenerate()
			})
		}
	})
}

// msgAction is one small ghost icon button of a message's action row.
// visible shows it; a hovered-or-pressed button stays visible even when
// the row's hover is suppressed by the press.
func msgAction(c *ui.Context, pal Palette, tip string, ic *ui.SVG, visible bool, fn func()) {
	b := ui.ButtonBase(c).Size(22, 22).Radius(6).Center()
	if on := visible || b.Hovered(); on {
		// The label rides visibility, so hidden buttons stay out of the
		// accessibility tree instead of reading as phantom controls.
		b.Label(tip).Tooltip(tip)
		if b.Hovered() {
			b.Background(pal.Hover)
		}
	} else {
		b.Opacity(0)
	}
	if b.Clicked() {
		fn()
	}
	b.Children(func() { ui.Icon(c, ic).FontSize(12).TextColor(pal.TextMuted) })
}

// block draws one card of an assistant message. bi is its index in the
// HOST's block sequence, which is what ToggleBlock addresses — a card
// inside a folded group is at its own position, not the group's.
func block(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, b BlockVM, bi int) {
	switch b.Type {
	case "command":
		blockCommand(c, vm, acts, m, b, bi)
	case "diff":
		blockDiff(c, vm, acts, m, b, bi)
	case "error":
		blockError(c, &b, vm.Pal)
	case "approval":
		blockApproval(c, vm, acts, &b)
	case "reasoning":
		blockReasoning(c, &b, vm.Pal)
	case "note":
		blockNote(c, &b, vm.Pal)
	}
}

// blockCommand is one tool call as a quiet row: status, the command,
// its duration, and its output folded away until clicked — many rows
// stack tightly, the way ZCode lays tool calls out.
func blockCommand(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, b BlockVM, bi int) {
	t := c.Theme()
	head := ui.Row(c).MinHeight(22).Padding(0, 8).Gap(8).AlignItems(ui.Center).Radius(6).Cursor(ui.CursorPointer)
	if head.Hovered() || b.Open {
		head.Background(vm.Pal.Hover)
	}
	if head.Clicked() {
		acts.ToggleBlock(m.ID, bi)
	}
	head.Children(func() {
		switch {
		case b.Running:
			ui.Spinner(c)
		case b.Exit == 0:
			ui.Icon(c, IconCheck).FontSize(12).TextColor(vm.Pal.Success)
		default:
			ui.Icon(c, IconX).FontSize(12).TextColor(vm.Pal.Danger)
		}
		ui.Text(c, "$ "+b.Text).Font("monospace").FontSize(12).Grow(1).MinWidth(0).SingleLine()
		if b.Ms > 0 {
			ui.Text(c, Duration(b.Ms)).FontSize(10.5).TextColor(vm.Pal.TextMuted)
		}
		chev := ui.Icon(c, IconChevDown).FontSize(12).TextColor(vm.Pal.TextMuted)
		if b.Open {
			chev.Rotate(180)
		}
	})
	if b.Open {
		out := b.Output
		if strings.TrimSpace(out) == "" {
			out = "(no output)"
		}
		ui.Column(c).Padding(0, 28, 6).Children(func() {
			ui.ScrollBoth(c).MaxHeight(260).Radius(6).Background(vm.Pal.CodeBG).Children(func() {
				ui.Text(c, strings.TrimRight(out, "\n")).Font("monospace").FontSize(11.5).
					Padding(8, 10).NoWrap().TextColor(t.Text)
			})
		})
	}
}

// blockDiff is a card for a file the agent changed: the path, the counts,
// and the unified diff colored by line.
func blockDiff(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, m *MessageVM, b BlockVM, bi int) {
	ui.Column(c).Radius(8).Background(vm.Pal.CodeBG).Border(1, vm.Pal.Border).Clip().Children(func() {
		head := ui.Row(c).Padding(7, 10).Gap(8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		if head.Clicked() {
			acts.ToggleBlock(m.ID, bi)
		}
		head.Children(func() {
			ui.Icon(c, IconFileCode).FontSize(13).TextColor(vm.Pal.TextMuted)
			ui.Text(c, b.File).Font("monospace").FontSize(12).Grow(1).MinWidth(0).SingleLine()
			if b.Add > 0 {
				ui.Textf(c, "+%d", b.Add).Font("monospace").FontSize(11).TextColor(vm.Pal.DiffAddText)
			}
			if b.Del > 0 {
				ui.Textf(c, "−%d", b.Del).Font("monospace").FontSize(11).TextColor(vm.Pal.DiffDelText)
			}
			chev := ui.Icon(c, IconChevDown).FontSize(13).TextColor(vm.Pal.TextMuted)
			if b.Open {
				chev.Rotate(180)
			}
		})
		if b.Open && len(b.Lines) > 0 {
			ui.Scroll(c).MaxHeight(320).Children(func() {
				ui.Column(c).FillWidth().PaddingY(4).Children(func() {
					for _, l := range b.Lines {
						DiffLineRow(c, l, false, false, vm.Pal)
					}
				})
			})
		}
	})
}

// blockError is a card for something that went wrong.
func blockError(c *ui.Context, b *BlockVM, pal Palette) {
	t := c.Theme()
	ui.Row(c).Padding(9, 12).Gap(8).Radius(8).AlignItems(ui.Start).
		Background(pal.Danger.Alpha(0.10)).Border(1, pal.Danger.Alpha(0.35)).Children(func() {
		ui.Icon(c, IconAlert).FontSize(14).TextColor(pal.Danger)
		ui.Text(c, b.Text).FontSize(12.5).TextColor(t.Text).Grow(1).MinWidth(0)
	})
}

// blockReasoning is a quiet note about how the agent thought.
func blockReasoning(c *ui.Context, b *BlockVM, pal Palette) {
	t := c.Theme()
	ui.Row(c).Padding(2, 0).Gap(8).Children(func() {
		ui.Box(c).Width(2).MinHeight(16).Radius(1).Background(pal.Border)
		ui.Text(c, b.Text).Italic().FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0)
	})
}

// blockNote is what the harness said about the turn itself: a turn-limit
// notice, a declined permission escalation. It is a card, not the agent's
// thinking, so it reads as a quiet system line rather than a thought.
func blockNote(c *ui.Context, b *BlockVM, pal Palette) {
	t := c.Theme()
	ui.Row(c).Padding(2, 0).Gap(8).Children(func() {
		ui.Box(c).Width(2).MinHeight(16).Radius(1).Background(pal.Warning.Alpha(0.6))
		ui.Text(c, b.Text).FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0)
	})
}

// blockApproval is the approval card: while pending, the redacted summary
// with Allow once / Deny buttons; after the decision, its outcome.
// Approvals bind one call (spec/approvals.md): there is deliberately no
// "always allow" here — durable authority is the permissions config.
func blockApproval(c *ui.Context, vm *TranscriptVM, acts TranscriptActions, b *BlockVM) {
	t := c.Theme()
	card := ui.Column(c).Padding(9, 12).Radius(8).Gap(6).AlignItems(ui.Start).
		Border(1, vm.Pal.Warning.Alpha(0.5)).Background(vm.Pal.Warning.Alpha(0.06))
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, IconAlert).FontSize(14).TextColor(vm.Pal.Warning)
			ui.Text(c, b.Text).Font("monospace").FontSize(12).Grow(1).MinWidth(0).TextColor(t.Text)
			if b.Running {
				ui.Spinner(c)
			}
		})
		if b.Running {
			ui.Row(c).Gap(8).Children(func() {
				allow := ui.ButtonBase(c).Label("Allow once").Tooltip("Allow this one call").
					Padding(5, 12).Radius(7).Border(1, vm.Pal.Success).Cursor(ui.CursorPointer)
				if allow.Hovered() {
					allow.Background(vm.Pal.Success.Alpha(0.12))
				}
				if allow.Clicked() {
					acts.ResolveApproval(b.ApprovalID, true)
				}
				allow.Children(func() {
					ui.Icon(c, IconCheck).FontSize(12).TextColor(vm.Pal.Success)
					ui.Text(c, "Allow once").FontSize(12).TextColor(vm.Pal.Success)
				})
				deny := ui.ButtonBase(c).Label("Deny").Tooltip("Deny this call").
					Padding(5, 12).Radius(7).Border(1, vm.Pal.Border).Cursor(ui.CursorPointer)
				if deny.Hovered() {
					deny.Background(vm.Pal.Hover)
				}
				if deny.Clicked() {
					acts.ResolveApproval(b.ApprovalID, false)
				}
				deny.Children(func() {
					ui.Icon(c, IconX).FontSize(12).TextColor(vm.Pal.TextMuted)
					ui.Text(c, "Deny").FontSize(12).TextColor(t.Text)
				})
			})
			return
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			switch b.Exit {
			case 0:
				ui.Icon(c, IconCheck).FontSize(12).TextColor(vm.Pal.Success)
				ui.Text(c, "Allowed").FontSize(11.5).TextColor(vm.Pal.TextMuted)
			default:
				ui.Icon(c, IconX).FontSize(12).TextColor(vm.Pal.Danger)
				ui.Text(c, "Denied").FontSize(11.5).TextColor(vm.Pal.TextMuted)
				if b.Output != "" {
					ui.Text(c, b.Output).FontSize(11.5).TextColor(vm.Pal.TextMuted).Grow(1).MinWidth(0).SingleLine()
				}
			}
		})
	})
}
