package main

// The Mujica Agent page: MujicaUI's chat and agent packages assembled
// into an agent-product screen — a working thread (ThinkingBlock,
// StreamingText, ToolCallCard, FileChangeCard, PromptComposer whose
// Send appends to the local transcript) plus a card for every other
// operator surface. Everything styles through the library's tokens
// (core.Tokens); the app palette is ignored, and demo state lives in
// ui.Local under "magent-" keys.

import (
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/ZacharyZhang-NY/MujicaUI/agent"
	"github.com/ZacharyZhang-NY/MujicaUI/chat"
	"github.com/ZacharyZhang-NY/MujicaUI/core"
	"github.com/ZacharyZhang-NY/MujicaUI/icons"
	"github.com/ZacharyZhang-NY/MujicaUI/input"
)

// mujicaAgentPage tours the chat and agent packages as one product
// screen. pal exists for signature parity with the other pages.
func (d *dashboard) mujicaAgentPage(c *ui.Context, pal Palette) {
	d.mujicaPage(c, "Mujica Agent", "the chat and agent packages: a working thread plus every operator surface", func() {
		mujicaAgentThread(c)
		// Session sidebar next to the welcome screen a new chat shows.
		mujicaAgentPair(c, func() { mujicaAgentSessions(c) }, func() { mujicaAgentWelcome(c) })
		mujicaAgentSurfaces(c)
		mujicaAgentBlocks(c)
	})
}

// mujicaAgentPair sets two cards side by side, each half the page, so
// the fixed-size demos inside stay within their card.
func mujicaAgentPair(c *ui.Context, left, right func()) {
	ui.Grid(c).Columns(2).Gap(12).Children(func() {
		left()
		right()
	})
}

// magentKind picks how one transcript row renders.
type magentKind int

const (
	magentPlain    magentKind = iota
	magentReasoned            // ThinkingBlock + StreamingText
	magentTools               // ToolCallCard + FileChangeCard
	magentTyping              // TypingIndicator
)

// magentMsg is one row of the demo transcript.
type magentMsg struct {
	id   string
	role chat.MessageRole
	kind magentKind
	text string
	at   time.Time
}

// magentThread is the Conversation card's state; MujicaUI panics on
// invalid input, so every seed here already satisfies the contracts.
type magentThread struct {
	msgs   []magentMsg
	list   chat.MessageListState
	draft  string
	mode   chat.ChatMode
	model  string
	rating chat.MessageFeedback
	think  bool
	decide agent.FileDecision
	ctx    []chat.ContextItem
}

// mujicaAgentSeed builds the transcript; two days of history so the
// MessageList shows its date separators.
func mujicaAgentSeed() magentThread {
	now := time.Now()
	day := 24 * time.Hour
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	return magentThread{
		mode:   chat.ModeAgent,
		model:  "atlas",
		decide: agent.FilePending,
		ctx: []chat.ContextItem{
			{ID: "log", Label: "export-nightly.log", Kind: chat.ContextFile},
			{ID: "run", Label: "Run #4812", Detail: "CI", Kind: chat.ContextDoc},
		},
		msgs: []magentMsg{
			{id: "u1", role: chat.MessageUser, text: "The nightly export failed again. Find out why and fix it.", at: at(2 * day)},
			{id: "a1", role: chat.MessageAssistant, kind: magentReasoned, at: at(2*day - 40*time.Second),
				text: "The job died on a **quota error** at 02:14 — the archive bucket holds seven days of dumps. I raised the cap and re-ran it."},
			{id: "a2", role: chat.MessageAssistant, kind: magentTools, at: at(2*day - 90*time.Second)},
			{id: "a3", role: chat.MessageAssistant, at: at(2*day - 2*time.Minute),
				text: "Fixed and verified: the job now prunes yesterday's dump first, and last night's run finished **green** in 4m12s."},
			{id: "u2", role: chat.MessageUser, text: "Nice. Watch it tonight and page me if it slips.", at: at(26 * time.Hour)},
			{id: "a4", role: chat.MessageAssistant, text: "Watching; I'll post the moment tonight's run passes ten minutes.", at: at(25*time.Hour + 30*time.Second)},
			{id: "t1", role: chat.MessageAssistant, kind: magentTyping, at: at(time.Minute)},
		},
	}
}

// mujicaAgentThread is the full-width Conversation card: a
// ChatContainer thread plus a PromptComposer wired to local state.
func mujicaAgentThread(c *ui.Context) {
	s := ui.Local(c.Root(), "magent-thread", mujicaAgentSeed)
	send := func() {
		text := strings.TrimSpace(s.draft)
		if text == "" {
			return
		}
		now := c.Now()
		s.msgs = append(s.msgs, magentMsg{id: fmt.Sprintf("magent-u%d", len(s.msgs)), role: chat.MessageUser, text: text, at: now})
		s.draft = ""
		s.msgs = append(s.msgs, magentMsg{id: fmt.Sprintf("magent-t%d", len(s.msgs)), role: chat.MessageAssistant, kind: magentTyping, at: now})
		s.list.ScrollToEnd()
	}
	k := core.Tokens(c)
	mujicaCard(c, "Conversation", "A ChatContainer thread over a PromptComposer — thinking, streaming, tool and file-change cards in the flow; Send appends below.", func() {
		// ChatContainer grows inside, so it needs this bounded box.
		ui.Box(c).Height(470).Border(1, k.Border).Clip().Children(func() {
			chat.ChatContainer(c, &s.list, len(s.msgs), chat.ChatContainerOptions{
				List: chat.MessageListOptions{
					ID:    func(i int) string { return s.msgs[i].id },
					Date:  func(i int) time.Time { return s.msgs[i].at },
					Label: func(i int) string { return s.msgs[i].text },
				},
				Input: func() { mujicaAgentComposer(c, s, send) },
			}, func(i int) { mujicaAgentRow(c, s, s.msgs[i]) })
		})
	})
}

// mujicaAgentRow renders one transcript row by kind. Assistant rows
// carry MessageActions; the seeded rating cell is shared.
func mujicaAgentRow(c *ui.Context, s *magentThread, m magentMsg) {
	switch m.kind {
	case magentTyping:
		chat.TypingIndicator(c, "Atlas")
	case magentTools:
		chat.MessageBubble(c, m.role, chat.MessageBubbleOptions{}, func() {
			agent.ToolCallCard(c, agent.ToolCall{ID: "call-1", Name: "read_file",
				Args: `{"path":"var/log/export-nightly.log","tail":40}`, Result: `{"lines":40,"error":"quota exceeded"}`,
				State: agent.AgentDone, Duration: 420 * time.Millisecond}, agent.ToolCallCardOptions{})
			agent.FileChangeCard(c, &s.decide, agent.FileChange{
				Path: "jobs/export-nightly.sh",
				Old:  "build dump\nupload dump\n",
				New:  "build dump\nprune yesterday\nupload dump\n",
			}, agent.FileChangeCardOptions{Preview: 4})
		})
	default:
		chat.MessageBubble(c, m.role, chat.MessageBubbleOptions{Name: "Atlas"}, func() {
			if m.kind == magentReasoned {
				chat.ThinkingBlock(c, &s.think, chat.ThinkingBlockOptions{Elapsed: 6 * time.Second}, func() {
					ui.Text(c, "The log ends at 02:14 with a quota error; the bucket keeps seven days of dumps.")
				})
				chat.StreamingText(c, m.text, chat.StreamingTextOptions{Streaming: true})
			} else {
				chat.MarkdownView(c, m.text)
			}
			if m.role == chat.MessageAssistant {
				chat.MessageActions(c, m.role, chat.MessageActionsOptions{Feedback: &s.rating})
			}
		})
	}
}

// mujicaAgentComposer is the thread's input area: context chips, the
// composer with mode selector and send, then model and budget meters.
func mujicaAgentComposer(c *ui.Context, s *magentThread, send func()) {
	ui.Column(c).Gap(8).Children(func() {
		if id, ok := chat.ContextChips(c, s.ctx, chat.ContextChipsOptions{Max: 2}).Removed(); ok {
			s.ctx = slices.DeleteFunc(s.ctx, func(it chat.ContextItem) bool { return it.ID == id })
		}
		p := chat.PromptComposer(c, &s.draft, chat.PromptComposerOptions{
			Placeholder: "Direct the agent — it can read files, run commands and edit.",
			Tools:       func() { chat.ModeSelector(c, &s.mode, chat.ModeSelectorOptions{}) },
			Actions: func() {
				if chat.SendButton(c, chat.SendButtonOptions{Shortcut: "Enter", Disabled: strings.TrimSpace(s.draft) == ""}).Sent() {
					send()
				}
			},
		})
		if p.Submitted() {
			send()
		}
		ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
			chat.ModelSelector(c, &s.model, []chat.ModelOption{
				{ID: "atlas", Name: "Atlas 4", Capabilities: []string{"Tools", "Vision"}, ContextTokens: 200000},
				{ID: "swift", Name: "Swift Mini", Capabilities: []string{"Fast"}, ContextTokens: 64000},
			}, chat.ModelSelectorOptions{})
			chat.TokenCounter(c, 6400, 8000)
		})
	})
}

// mujicaAgentSessions covers ConversationSearch, ConversationList and
// ConversationItem: search filters, the list groups by recency, the
// item edits in place.
func mujicaAgentSessions(c *ui.Context) {
	type sessionState struct {
		list  chat.ConversationListState
		query string
		conv  chat.ChatConversation
	}
	s := ui.Local(c.Root(), "magent-sessions", func() sessionState {
		return sessionState{
			query: "exp",
			conv:  chat.ChatConversation{ID: "c9", Title: "Nightly export postmortem", Updated: time.Now().Add(-2 * time.Hour)},
		}
	})
	mujicaCard(c, "Conversations", "ConversationSearch filters, ConversationList groups by recency, ConversationItem renames, pins and deletes.", func() {
		hits := []chat.ConversationSearchHit{
			{ID: "c9", Title: "Nightly export postmortem", Snippet: "…quota exceeded at 02:14…"},
			{ID: "c4", Title: "Archive retention policy", Snippet: "…raise the bucket cap…"},
		}
		v := chat.ConversationSearch(c, &s.query, hits, chat.ConversationSearchOptions{Label: "Search conversations"})
		if v.Chosen != "" {
			c.Toast("open " + v.Chosen)
		}
		now := time.Now()
		lv := chat.ConversationList(c, &s.list, []chat.ChatConversation{
			{ID: "c9", Title: "Nightly export postmortem", Updated: now.Add(-2 * time.Hour)},
			{ID: "c4", Title: "Archive retention policy", Updated: now.Add(-26 * time.Hour)},
			{ID: "c1", Title: "Seating chart macro", Updated: now.Add(-40 * 24 * time.Hour), Pinned: true},
		}, chat.ConversationListOptions{Label: "Recent"}, nil)
		lv.Element.Height(150)
		if lv.Changed() {
			c.Toast("select " + s.list.Selected)
		}
		if lv.Submitted() {
			c.Toast("open " + s.list.Selected)
		}
		iv := chat.ConversationItem(c, &s.conv, chat.ConversationItemOptions{Selected: true})
		if iv.PinToggled {
			s.conv.Pinned = !s.conv.Pinned
		}
		if iv.Opened || iv.Renamed || iv.Deleted {
			c.Toast("item " + s.conv.Title)
		}
	})
}

// mujicaAgentWelcome covers WelcomeScreen, CapabilityCards and
// SuggestionChips: a picked example lands in the draft line.
func mujicaAgentWelcome(c *ui.Context) {
	s := ui.Local(c.Root(), "magent-welcome", func() string { return "" })
	mujicaCard(c, "Welcome screen", "WelcomeScreen with CapabilityCards and SuggestionChips feeding the composer draft.", func() {
		v := chat.WelcomeScreen(c, chat.WelcomeScreenOptions{
			Greeting: "Good evening",
			Subtitle: "The agent is caught up. What should it look at next?",
			Cards: []chat.CapabilityCard{
				{Title: "Investigate", Description: "Logs, traces and metrics.", Icon: icons.Must("search"),
					Examples: []string{"Why did checkout latency spike?", "Which job failed last?"}},
				{Title: "Change code", Description: "Patches with tests.", Icon: icons.Must("pencil"),
					Examples: []string{"Add retries to the exporter"}},
			},
			Prompts: func() {
				chat.SuggestionChips(c, s, []string{"Summarize last night's failures", "Draft the incident report"},
					chat.SuggestionChipsOptions{Label: "Starters"})
			},
		})
		if v.Example != "" {
			*s = v.Example
		}
		ui.Text(c, "Draft: "+*s).FontSize(12).TextColor(core.Tokens(c).TextMuted).SingleLine()
	})
}

// mujicaAgentSurfaces gives the remaining agent package pieces their
// own cards, two per row, in run order.
func mujicaAgentSurfaces(c *ui.Context) {
	// Run state + step timeline.
	mujicaAgentPair(c, func() {
		mujicaCard(c, "AgentStatus + AgentProgress", "Live run states and the progress bar naming the current action.", func() {
			ui.Row(c).Gap(10).Wrap().Children(func() {
				agent.AgentStatus(c, agent.AgentRunning, agent.AgentStatusOptions{})
				agent.AgentStatus(c, agent.AgentWaiting, agent.AgentStatusOptions{})
				agent.AgentStatus(c, agent.AgentDone, agent.AgentStatusOptions{})
				agent.AgentStatus(c, agent.AgentFailed, agent.AgentStatusOptions{})
			})
			agent.AgentProgress(c, 0.62, agent.AgentProgressOptions{Label: "Nightly export", Action: "Re-running the job with the raised cap"})
		})
	}, func() {
		mujicaCard(c, "AgentStepList", "Step timeline with foldable details and retry on failure.", func() {
			r := agent.AgentStepList(c, []agent.AgentStep{
				{ID: "read", Title: "Read the job log", State: agent.AgentDone, Duration: 800 * time.Millisecond, Detail: "Quota error at 02:14."},
				{ID: "cap", Title: "Raise the bucket cap", State: agent.AgentDone, Duration: 1200 * time.Millisecond},
				{ID: "rerun", Title: "Re-run the export", State: agent.AgentRunning, Detail: "4m12s elapsed."},
				{ID: "verify", Title: "Verify the archive", State: agent.AgentPending},
			}, agent.AgentStepListOptions{})
			if r.Retry != "" {
				c.Toast("retry " + r.Retry)
			}
		})
	})

	// Plan + permission.
	plan := ui.Local(c.Root(), "magent-plan", func() []agent.PlanItem {
		return []agent.PlanItem{
			{ID: "p1", Text: "Read the job log", State: agent.AgentDone},
			{ID: "p2", Text: "Raise the bucket cap", State: agent.AgentDone},
			{ID: "p3", Text: "Re-run the export", State: agent.AgentRunning},
			{ID: "p4", Text: "Page the on-call if it slips"},
		}
	})
	// The prompts are modal dialogs, so they start closed and a button
	// opens each; deny keeps the focus while one is up.
	perm := ui.Local(c.Root(), "magent-perm", func() bool { return false })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "AgentPlan", "A live checklist the agent keeps; the user can tick, skip and add.", func() {
			agent.AgentPlan(c, plan, agent.AgentPlanOptions{})
		})
	}, func() {
		mujicaCard(c, "PermissionPrompt", "Allow once, always, or deny; deny keeps the focus.", func() {
			if input.Button(c, "Ask to rotate the archive key", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
				*perm = true
			}
			r := agent.PermissionPrompt(c, perm, agent.PermissionPromptOptions{
				Title:       "Rotate the archive key?",
				Description: "The agent needs the storage key to raise the cap.",
				Scope:       "Write access to gs://nightly-archive",
			})
			if r.Choice != agent.PermissionNone {
				*perm = false
				c.Toast("permission answered")
			}
		})
	})

	// Grouped calls + approval dialog.
	grp := ui.Local(c.Root(), "magent-group", func() bool { return true })
	ask := ui.Local(c.Root(), "magent-approval", func() bool { return false })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "ToolCallGroup", "A foldable run of related tool calls, failures included.", func() {
			agent.ToolCallGroup(c, grp, []agent.ToolCall{
				{ID: "g1", Name: "list_dir", Args: `{"path":"var/log"}`, Result: `["export-nightly.log","rotate.log"]`, State: agent.AgentDone, Duration: 90 * time.Millisecond},
				{ID: "g2", Name: "grep", Args: `{"pattern":"quota"}`, Result: `{"matches":2}`, State: agent.AgentDone, Duration: 240 * time.Millisecond},
				{ID: "g3", Name: "read_file", Args: `{"path":"rotate.log"}`, Error: "permission denied", State: agent.AgentFailed, Duration: time.Second},
			}, agent.ToolCallGroupOptions{})
		})
	}, func() {
		mujicaCard(c, "ToolApprovalDialog", "The agent asks before a destructive tool runs.", func() {
			if input.Button(c, "Ask to run rotate_key", input.ButtonOptions{Variant: input.Secondary}).Clicked() {
				*ask = true
			}
			r := agent.ToolApprovalDialog(c, ask, agent.ToolApprovalOptions{
				Tool: "rotate_key", Args: `{"service":"archive","grace":"1h"}`,
				Reason: "Rotating the storage key raises the bucket cap for tonight's export.",
			})
			if r.Allowed || r.Denied {
				*ask = false
				c.Toast("approval answered")
			}
		})
	})

	// Command output + multi-file review.
	cmd := ui.Local(c.Root(), "magent-cmd", func() agent.CommandRun {
		return agent.CommandRun{Command: "make verify", Dir: "~/jobs/export-nightly", ExitCode: 0, Duration: 41 * time.Second,
			Output: "\x1b[32mok\x1b[0m  quota probe\n\x1b[32mok\x1b[0m  archive checksum\n\x1b[32mok\x1b[0m  128 files indexed\n"}
	})
	files := []agent.FileChange{
		{Path: "jobs/export-nightly.sh", Old: "build dump\nupload dump\n", New: "build dump\nprune yesterday\nupload dump\n"},
		{Path: "jobs/quota.go", Old: "cap = 7\n", New: "cap = 14\n"},
		{Path: "docs/runbook.md", Old: "The bucket keeps 7 days.\n"},
	}
	decisions := ui.Local(c.Root(), "magent-review", func() []agent.FileDecision {
		return make([]agent.FileDecision, len(files))
	})
	mujicaAgentPair(c, func() {
		mujicaCard(c, "CommandExecutionCard", "Terminal output with ANSI colors; aborting is one click while it runs.", func() {
			if agent.CommandExecutionCard(c, *cmd, agent.CommandExecutionCardOptions{Height: 110}).Changed() {
				cmd.Running, cmd.ExitCode = false, 130
				cmd.Output += "\x1b[31maborted by user\x1b[0m\n"
			}
		})
	}, func() {
		mujicaCard(c, "MultiFileDiffReview", "Review a whole change set, one diff at a time.", func() {
			agent.MultiFileDiffReview(c, decisions, files, agent.MultiFileDiffReviewOptions{Height: 150})
		})
	})

	// Delegation + history.
	mujicaAgentPair(c, func() {
		mujicaCard(c, "SubAgentTree", "Nested sub-agents and their states.", func() {
			agent.SubAgentTree(c, []agent.SubAgent{
				{ID: "lead", Name: "Lead", Task: "Fix the nightly export", State: agent.AgentRunning, Children: []agent.SubAgent{
					{ID: "logs", Name: "Log reader", Task: "Find the failure", State: agent.AgentDone},
					{ID: "patch", Name: "Patcher", Task: "Edit the job", State: agent.AgentWaiting, Children: []agent.SubAgent{
						{ID: "test", Name: "Tester", Task: "Re-run make verify", State: agent.AgentRunning},
					}},
				}},
			}, agent.SubAgentTreeOptions{})
		})
	}, func() {
		cur := ui.Local(c.Root(), "magent-checkpoint", func() string { return "c2" })
		mujicaCard(c, "CheckpointList", "Rewind the run to a saved point; rollbacks confirm first.", func() {
			if agent.CheckpointList(c, cur, []agent.Checkpoint{
				{ID: "c3", Label: "Archive verified", Note: "128 files"},
				{ID: "c2", Label: "Cap raised", Note: "2 files changed"},
				{ID: "c1", Label: "Job started"},
			}, agent.CheckpointListOptions{}).Changed() {
				c.Toast("rolled back to " + *cur)
			}
		})
	})

	// Tools + memory.
	servers := ui.Local(c.Root(), "magent-mcp", func() []agent.MCPServer {
		return []agent.MCPServer{
			{ID: "fs", Name: "filesystem", Transport: "stdio", State: agent.MCPConnected, Tools: 12},
			{ID: "gh", Name: "github", Transport: "http", State: agent.MCPError, Error: "401 Unauthorized", Tools: 3},
			{ID: "db", Name: "postgres", Transport: "stdio", State: agent.MCPStopped, Tools: 0},
		}
	})
	mem := ui.Local(c.Root(), "magent-memory", func() []agent.MemoryEntry {
		return []agent.MemoryEntry{
			{ID: "m1", Text: "The archive bucket holds seven days of dumps.", Source: "Chat, 3 Oct"},
			{ID: "m2", Text: "Page the on-call if the export passes ten minutes.", Source: "Chat, yesterday"},
			{ID: "m3", Text: "make verify takes about 40s.", Source: "Ledger"},
		}
	})
	mujicaAgentPair(c, func() {
		mujicaCard(c, "MCPServerList", "Connected tool servers with start, stop and reconnect.", func() {
			r := agent.MCPServerList(c, *servers, agent.MCPServerListOptions{})
			if r.Action != agent.MCPNone {
				for i := range *servers {
					if (*servers)[i].ID == r.Server {
						if r.Action == agent.MCPStop {
							(*servers)[i].State = agent.MCPStopped
						} else {
							(*servers)[i].State, (*servers)[i].Error = agent.MCPConnected, ""
						}
					}
				}
				c.Toast("server updated")
			}
		})
	}, func() {
		mujicaCard(c, "MemoryPanel", "What the agent remembers, searchable and editable.", func() {
			r := agent.MemoryPanel(c, mem, agent.MemoryPanelOptions{MaxHeight: 170})
			if r.Deleted != "" {
				for i, e := range *mem {
					if e.ID == r.Deleted {
						*mem = append((*mem)[:i], (*mem)[i+1:]...)
						break
					}
				}
			}
		})
	})

	// Sandbox + a question for the human.
	ans := ui.Local(c.Root(), "magent-human", func() string { return "" })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "SandboxStatus", "The agent's sandbox with resources and time left.", func() {
			agent.SandboxStatus(c, agent.SandboxInfo{
				Name: "agent-sandbox-7", State: agent.SandboxRunning, Expires: c.Now().Add(9 * time.Minute),
				Resources: []agent.SandboxResource{
					{Name: "CPU", Used: 1.2, Limit: 4, Unit: "cores"},
					{Name: "Memory", Used: 2.4, Limit: 8, Unit: "GB"},
				},
			}, agent.SandboxStatusOptions{})
		})
	}, func() {
		mujicaCard(c, "HumanInputRequest", "The agent pauses to ask; free text is allowed too.", func() {
			r := agent.HumanInputRequest(c, ans, agent.HumanInputRequestOptions{
				Question: "Should tonight's run also mirror to the cold archive?",
				Options:  []string{"Yes, mirror it", "No, local only"}, AllowFree: true,
			})
			if r.Submitted {
				c.Toast("answer sent: " + *ans)
				*ans = ""
			}
		})
	})

	// Artifacts + versions.
	art := agent.Artifact{Title: "quota.go", Kind: agent.ArtifactCode, Language: "go",
		Content: "package jobs\n\n// cap keeps a week of dumps per bucket.\nvar cap = 14\n"}
	ver := ui.Local(c.Root(), "magent-version", func() int { return 2 })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "ArtifactPanel", "Code the agent wrote, with source view, copy and export.", func() {
			r := agent.ArtifactPanel(c, art, agent.ArtifactPanelOptions{Height: 170})
			if r.Copied {
				mygoClipboard(art.Content)
			}
			if r.Exported {
				c.Toast("export requested")
			}
		})
	}, func() {
		mujicaCard(c, "ArtifactVersionSwitcher", "Step between revisions; compare diffs against the previous one.", func() {
			r := agent.ArtifactVersionSwitcher(c, ver, []agent.ArtifactVersion{
				{Label: "v1", Note: "cap = 7"}, {Label: "v2", Note: "cap = 14"}, {Label: "v3", Note: "cap from flags"},
			}, agent.ArtifactVersionSwitcherOptions{})
			if r.Changed {
				c.Toast("version " + []string{"v1", "v2", "v3"}[*ver])
			}
			if r.Compare {
				c.Toast("compare v" + fmt.Sprint(*ver) + " with v" + fmt.Sprint(*ver+1))
			}
		})
	})

	// Computer-use frames, full width.
	frames := ui.Local(c.Root(), "magent-shots", func() agent.ScreenshotStreamState {
		return agent.ScreenshotStreamState{Index: 1}
	})
	mujicaCard(c, "ScreenshotStream", "What a computer-use agent saw, stepping or following live.", func() {
		if agent.ScreenshotStream(c, frames, []agent.ScreenshotFrame{mujicaAgentFrame(0), mujicaAgentFrame(1)},
			agent.ScreenshotStreamOptions{Height: 170}).Changed() {
			c.Toast(fmt.Sprintf("frame %d", frames.Index+1))
		}
	})
}

// mujicaAgentFrame paints a flat screenshot with a moving block so the
// stream has real bitmaps to show.
func mujicaAgentFrame(i int) agent.ScreenshotFrame {
	img := image.NewRGBA(image.Rect(0, 0, 160, 90))
	for y := range 90 {
		for x := range 160 {
			o := img.PixOffset(x, y)
			v := uint8(232)
			if x > 30+i*40 && x < 70+i*40 && y > 30 && y < 60 {
				v = 120
			}
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = v, v-8, v-16, 255
		}
	}
	return agent.ScreenshotFrame{Image: ui.NewBitmap(img), Caption: fmt.Sprintf("Opened the archive console (step %d)", i+1),
		At: time.Duration(i) * 30 * time.Second}
}

// mujicaAgentMarkdown is the message-body sample for MarkdownView.
const mujicaAgentMarkdown = "## Fix verified\n\nThe bucket cap was the cause; see **the runbook** at [the wiki](https://example.com/runbook).\n\n" +
	"1. Raise the cap\n2. Prune yesterday\n   - keep six dumps\n\n> Tonight's run is watched.\n\n" +
	"```go\nfunc prune(all []string) []string {\n\treturn all[len(all)-6:]\n}\n```\n\nVive l'export."

// mujicaAgentBlocks covers the remaining chat package pieces, two per
// row: message bodies, citations, menus, edits, feedback and meters.
func mujicaAgentBlocks(c *ui.Context) {
	mujicaAgentPair(c, func() {
		mujicaCard(c, "MarkdownView", "Headings, lists, quotes, tables and links inside a message.", func() {
			if url, ok := chat.MarkdownView(c, mujicaAgentMarkdown).Link(); ok {
				c.Toast("open " + url)
			}
		})
	}, func() {
		mujicaCard(c, "CodeBlock", "Highlighted code with copy, wrap and collapse.", func() {
			if chat.CodeBlock(c, "func prune(all []string) []string {\n\treturn all[len(all)-6:]\n}\n",
				chat.CodeBlockOptions{Language: "go", LineNumbers: true}).Copied() {
				c.Toast("copied")
			}
		})
	})

	mujicaAgentPair(c, func() {
		mujicaCard(c, "TableBlock", "A small message table; copies as TSV.", func() {
			chat.TableBlock(c, []string{"Run", "Files", "Duration"}, [][]string{
				{"#4811", "126", "3m58s"}, {"#4812", "128", "4m12s"}, {"#4813", "128", "4m02s"},
			}, chat.TableBlockOptions{Align: []ui.Align{ui.Start, ui.End, ui.End}})
		})
	}, func() {
		mujicaCard(c, "ImageGrid", "Up to nine pictures; a click opens the viewer.", func() {
			imgs := make([]chat.GridImage, 3)
			for i, col := range []string{"#7D2034", "#355E80", "#326247"} {
				imgs[i] = chat.GridImage{Source: mujicaAgentPicture(col), Alt: fmt.Sprintf("Archive console %d", i+1)}
			}
			chat.ImageGrid(c, imgs)
		})
	})

	ed := ui.Local(c.Root(), "magent-editor", func() string { return "Seat the ambassador by the window." })
	br := ui.Local(c.Root(), "magent-branch", func() int { return 1 })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "FileMessage + QuoteReply", "A file attached to a reply, and a quote that jumps back.", func() {
			r := chat.FileMessage(c, chat.FileMessageOptions{Name: "export-nightly.log", Size: 18432})
			if r.Downloaded() || r.Opened() {
				c.Toast("log file action")
			}
			if chat.QuoteReply(c, "Atlas", "The job died on a quota error at 02:14; the bucket holds seven days of dumps.").Clicked() {
				c.Toast("jump to the quoted message")
			}
		})
	}, func() {
		mujicaCard(c, "MessageEditor + BranchNavigator", "Edit a sent message into a new branch, then step between branches.", func() {
			r := chat.MessageEditor(c, ed, chat.MessageEditorOptions{Original: "Seat the ambassador by the window."})
			if r.Saved() {
				c.Toast("saved as a new branch")
			}
			if r.Canceled() {
				*ed = "Seat the ambassador by the window."
			}
			if chat.BranchNavigator(c, br, 3).Changed() {
				c.Toast(fmt.Sprintf("branch %d of 3", *br+1))
			}
		})
	})

	cites := ui.Local(c.Root(), "magent-cites", func() chat.CitationState { return chat.CitationState{} })
	srcs := []chat.Source{
		{Title: "Export runbook", URL: "https://example.com/runbook", Site: "wiki.example.com", Snippet: "Prune before upload."},
		{Title: "Incident 4812", URL: "https://example.com/incident", Site: "status.example.com", Snippet: "Quota exceeded at 02:14."},
	}
	mujicaAgentPair(c, func() {
		mujicaCard(c, "CitationBadge + SourceCard", "Inline citations that preview on hover, and the source card itself.", func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "The cap was the cause")
				for i, src := range srcs {
					chat.CitationBadge(c, i+1, src, cites)
				}
			})
			chat.SourceCard(c, 1, srcs[0], cites)
		})
	}, func() {
		mujicaCard(c, "SourcesPanel", "Every source behind the reply, in a scrolled list.", func() {
			ui.Column(c).Height(160).Children(func() {
				if n, ok := chat.SourcesPanel(c, srcs, cites).Opened(); ok {
					c.Toast(fmt.Sprintf("open source %d", n))
				}
			})
		})
	})

	slash := ui.Local(c.Root(), "magent-slash", func() string { return "/" })
	mention := ui.Local(c.Root(), "magent-mention", func() string { return "@" })
	items := []chat.ContextItem{
		{ID: "log", Label: "export-nightly.log", Kind: chat.ContextFile},
		{ID: "run", Label: "Run #4812", Detail: "CI", Kind: chat.ContextDoc},
		{ID: "wiki", Label: "Export runbook", Detail: "wiki.example.com", Kind: chat.ContextWeb},
	}
	mujicaAgentPair(c, func() {
		mujicaCard(c, "SlashCommandMenu", "Type / to pick a command; the menu completes the word.", func() {
			p := chat.PromptComposer(c, slash, chat.PromptComposerOptions{Label: "Command", MaxHeight: 80})
			if cmd, ok := chat.SlashCommandMenu(c, p, slash, []chat.SlashCommand{
				{Name: "summarize", Description: "Summarize the thread"},
				{Name: "plan", Description: "Draft a plan first"},
				{Name: "review", Description: "Review the pending diff"},
			}).Chosen(); ok {
				c.Toast("command " + cmd.Name)
			}
		})
	}, func() {
		mujicaCard(c, "ContextMentionMenu", "Type @ to attach a file, document or webpage.", func() {
			p := chat.PromptComposer(c, mention, chat.PromptComposerOptions{Label: "Context", MaxHeight: 80})
			if it, ok := chat.ContextMentionMenu(c, p, mention, items, chat.ContextMentionOptions{}).Chosen(); ok {
				c.Toast("attach " + it.ID)
			}
		})
	})

	mujicaAgentPair(c, func() {
		mujicaCard(c, "RegenerateMenu", "Retry the last reply, as-is, with another model, or a variant.", func() {
			if v := chat.RegenerateMenu(c, chat.RegenerateMenuOptions{Models: []string{"Atlas 4", "Swift Mini"}, Current: "Atlas 4"}); v.Chosen {
				c.Toast("retry with " + v.Choice.Model)
			}
		})
	}, func() {
		mujicaCard(c, "Small signals", "Typing, thinking, a date separator and the stop button.", func() {
			chat.TypingIndicator(c, "Atlas")
			chat.ThinkingIndicator(c, chat.ThinkingIndicatorOptions{Phase: "Reading the job log"})
			chat.DateSeparator(c, core.DateOf(time.Now()), chat.DateSeparatorOptions{})
			if chat.StopGeneratingButton(c, chat.StopGeneratingButtonOptions{Generating: true}).Changed() {
				c.Toast("stopped")
			}
		})
	})

	fb := ui.Local(c.Root(), "magent-feedback", func() chat.FeedbackValue { return chat.FeedbackValue{} })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "ErrorMessage", "A failed reply with the raw error and a retry.", func() {
			if chat.ErrorMessage(c, `429 Too Many Requests: rate_limit_error`, chat.ErrorMessageOptions{Title: "The model rate-limited us"}).Retried() {
				c.Toast("retry")
			}
		})
	}, func() {
		mujicaCard(c, "FeedbackForm", "Thumbs with reasons; submit lands in the caller.", func() {
			if v := chat.FeedbackForm(c, fb, chat.FeedbackFormOptions{Rating: chat.FeedbackDown}); v.Submitted {
				c.Toast("feedback recorded")
			}
		})
	})

	att := ui.Local(c.Root(), "magent-attach", func() bool { return true })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "VoiceWaveform + AttachmentChip", "Mic levels while recording, and a removable attachment.", func() {
			levels := make([]float32, 32)
			for i := range levels {
				levels[i] = float32(i%7+1) / 8
			}
			chat.VoiceWaveform(c, levels, chat.VoiceWaveformOptions{})
			if *att {
				if chat.AttachmentChip(c, chat.Attachment{Name: "export-nightly.log", Size: 18432}).Removed() {
					*att = false
				}
			} else {
				ui.Text(c, "Attachment removed").TextColor(core.Tokens(c).TextMuted)
			}
		})
	}, func() {
		mujicaCard(c, "ContextWindowMeter + CostEstimator", "Segmented context usage and the price of the next reply.", func() {
			chat.ContextWindowMeter(c, []chat.ContextSegment{
				{Label: "System", Tokens: 1400}, {Label: "History", Tokens: 21000}, {Label: "Attached files", Tokens: 9600},
			}, 200000, chat.ContextWindowMeterOptions{})
			chat.CostEstimator(c, chat.CostEstimatorOptions{InputTokens: 32000, OutputTokens: 4100, InputPrice: 3, OutputPrice: 15})
		})
	})

	lib := ui.Local(c.Root(), "magent-lib", func() chat.PromptLibraryState { return chat.PromptLibraryState{} })
	sys := ui.Local(c.Root(), "magent-sysprompt", func() string { return "You are the on-call agent for the export fleet. Be terse." })
	mujicaAgentPair(c, func() {
		mujicaCard(c, "PromptLibrary", "Browse templates, fill their variables, insert the result.", func() {
			v := chat.PromptLibrary(c, lib, []chat.PromptTemplate{
				{ID: "t1", Category: "Incidents", Title: "Postmortem", Body: "Summarize {{incident}} for the weekly review."},
				{ID: "t2", Category: "Code", Title: "Review request", Body: "Review the pending diff in {{repo}}."},
			}, chat.PromptLibraryOptions{Label: "Prompt library"})
			if v.Inserted != "" {
				c.Toast("template inserted")
			}
		})
	}, func() {
		mujicaCard(c, "SystemPromptEditor", "The standing instruction; saving flags the change.", func() {
			if chat.SystemPromptEditor(c, sys, chat.SystemPromptEditorOptions{Default: "You are the on-call agent for the export fleet. Be terse.", Saved: *sys}).Save {
				c.Toast("system prompt saved")
			}
		})
	})
}

// mujicaAgentPicture is a flat colored picture for image demos.
func mujicaAgentPicture(color string) ui.ImageSource {
	return ui.MustParseSVG([]byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="240" height="180"><rect width="240" height="180" fill="%s"/><circle cx="120" cy="90" r="40" fill="#F2EBDD" opacity="0.5"/></svg>`, color)))
}
