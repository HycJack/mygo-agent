# Dashboard — a MyGo UI example

A generic product dashboard ("Acme Analytics") that shows off the
components of [MyGo](https://github.com/egoist/mygo)'s native UI
toolkit — the same toolkit the agent app in this repository is built
with. Its shell, palette and cards are abstracted from the agent's own
UI (`internal/ui`): GPU-drawn, no WebView, no HTML, no JavaScript.

```sh
go run ./examples/dashboard
```

## What it shows

| Page | Widgets |
| --- | --- |
| **Login** | the app opens here: a gradient brand panel and a sign-in card with the two ways in — a password form (masked input, inline validation, Enter submits) that issues the session's **JWT**, and **WeChat / Google / GitHub OAuth** buttons with their brand marks. Both flows are mocked in-process (a beat of handshake, a demo identity); the footer menu copies the token or signs out |
| **Shell** | a collapsible sidebar on **both sides**: the left navigation folds to a 58 px icon rail (tooltips and dot badges, ⌘B), the right **Activity panel** — live requests, the recent-activity feed, team online — slides away entirely (⌘J); both widths animate through `ElementTransition{Size}`; ⌘1–5 switch pages |
| **Overview** | KPI cards with sparklines and delta pills, a Painter-drawn area chart with a gradient fill, a donut of stroked arcs, a bar chart, `Meter` rows and a file drop zone |
| **Data** | a sortable, multi-select `Table` with `EditableText` and keyboard support, a virtualized `List` of 10,000 searchable rows, a `Tree`, a drag-to-reorder `GridView` and an `OutlineTable` |
| **Controls** | `Form`/`Field`/`Fieldset` with validation, text inputs, `TextArea`, `Select`, `Combobox`, `Autocomplete`, `TokenField`, switches, radios, checkbox groups, `Slider`/`StepSlider`/`RangeSlider`, `NumberInput`/`Stepper`, `Progress`, `Calendar`/`DateInput`/`TimeInput`, `ColorWell`, `Rating`, segmented controls, `ToggleGroup` and `Tabs` |
| **Overlays** | buttons and `MenuButton`, a `Popover`, `AlertDialog` and `Modal`, `Toast`/`ToastAction`, tooltips, a drag & drop kanban board (`Drag`/`Drop`/`DragOver`) and color transitions |
| **Settings** | a two-pane `Split` shell, `Collapsible` and `Accordion`, plan-usage `Meter`s and the danger zone |
| **MujicaUI ×5** | the [MujicaUI](https://github.com/ZacharyZhang-NY/MujicaUI) library embedded whole, under its own court-gothic theme (`core.Use` re-skins the window for as long as one of these pages renders): **Forms** — all 47 `input` + 28 `datetime` components (masked/scrub/pin/mention inputs, cascader, transfer, knob, signature pad, QR, the calendar family, cron and recurrence editors); **Data** — all 42 `data`/`display`/`navigation`/`layout` components (cards, tables, trees, timelines, avatars, tabs, command palette, app shell); **Feedback** — all 30 `feedback` + 12 `overlay` components (alerts, toasts, loaders, motion, dialogs, drawers, popovers); **Charts** — the chart package's catalog plus flagships from the wider library (chat message, mail list + reader, code viewer); **Agent** — the `agent` and `chat` packages as a working-looking agent conversation (message list, thinking and tool-call blocks, composer, conversation list, permission prompt, diff review) |

Around them, the app shell itself: a `Router` driven by a `Sidebar` with
sections, badges and a user footer (or the icon rail it folds to), a
`Toolbar` with back/forward, `Breadcrumbs`, search and notifications.

## Icons

The UI icons are Lucide (`lucide.dev`, ISC), parsed in `icons.go` the
same way the agent app parses its own. The sourcing order the project
prefers: **lucide** first, **iconify** (`icon-sets.iconify.design`) as
the supplement when another style or metaphor fits — its sets paste
into the same wrapper — and **lobehub** (`lobehub.com/icons`) for brand
and LLM-vendor logos in model pickers; those are filled multi-color
logos and would carry their own `MustParseSVG` rather than
`currentColor`. Anything still missing gets a pass in Sketch.

## Notes

- **Light and dark themes.** The palettes live in `theme.go`, abstracted
  from the agent's `internal/ui/theme.go`; the toolbar's moon button
  swaps them at runtime through `Context.SetTheme`.
- **Charts are custom painting.** `components.go` draws them with
  `Element.Draw` and the `Painter`: gridlines, smooth `QuadTo` curves,
  gradient fills and stroked arcs.
- **The auth is a prop, not a service.** `pages_login.go` mints a real
  three-segment JWT (base64url header.payload, dummy signature) with
  `fakeJWT`, and the OAuth handshakes are a goroutine's beat of latency
  through `Window.Update` — swap them for a real auth server and the
  rest of the example does not care.
- **The data is mock and deterministic** (`data.go`), seeded so every
  launch looks the same; the pure helpers (filtering, sorting,
  formatting) unit-test without a window.
- **A layout trap worth knowing, twice over.** An element with
  `Grow(1)` directly inside a scroll's content collapses the whole
  layout, and so does a growing element inside a container whose own
  height wraps its content (the charts carry fixed heights for exactly
  this reason). Cards take their grow from a row with a definite
  measure, never from a scroll. The render tests catch both: with
  `MYGO_UI_SHOTS` set they write every page's PNG plus the shell's
  three fold states, the way the agent app screenshots its own
  surfaces.

```sh
MYGO_UI_SHOTS=/tmp/shots go test ./examples/dashboard/ -run TestRender
```
