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
| **Overview** | KPI cards with sparklines and delta pills, a Painter-drawn area chart with a gradient fill, a donut of stroked arcs, a bar chart, `Meter` rows, a virtualized activity feed with `Avatar` + context menus, a live card fed by a goroutine through `Window.Update`, and a file drop zone |
| **Data** | a sortable, multi-select `Table` with `EditableText` and keyboard support, a virtualized `List` of 10,000 searchable rows, a `Tree`, a drag-to-reorder `GridView` and an `OutlineTable` |
| **Controls** | `Form`/`Field`/`Fieldset` with validation, text inputs, `TextArea`, `Select`, `Combobox`, `Autocomplete`, `TokenField`, switches, radios, checkbox groups, `Slider`/`StepSlider`/`RangeSlider`, `NumberInput`/`Stepper`, `Progress`, `Calendar`/`DateInput`/`TimeInput`, `ColorWell`, `Rating`, segmented controls, `ToggleGroup` and `Tabs` |
| **Overlays** | buttons and `MenuButton`, a `Popover`, `AlertDialog` and `Modal`, `Toast`/`ToastAction`, tooltips, a drag & drop kanban board (`Drag`/`Drop`/`DragOver`) and color transitions |
| **Settings** | a two-pane `Split` shell, `Collapsible` and `Accordion`, plan-usage `Meter`s and the danger zone |

Around them, the app shell: a `Router` driven by a `Sidebar` with
sections, badges and a user footer, a `Toolbar` with back/forward,
`Breadcrumbs`, search and notifications, and ⌘1…5 to switch pages.

## Notes

- **Light and dark themes.** The palettes live in `theme.go`, abstracted
  from the agent's `internal/ui/theme.go`; the toolbar's moon button
  swaps them at runtime through `Context.SetTheme`.
- **Charts are custom painting.** `components.go` draws them with
  `Element.Draw` and the `Painter`: gridlines, smooth `QuadTo` curves,
  gradient fills and stroked arcs.
- **The data is mock and deterministic** (`data.go`), seeded so every
  launch looks the same; the pure helpers (filtering, sorting,
  formatting) unit-test without a window.
- **A layout trap worth knowing.** An element with `Grow(1)` directly
  inside a scroll's content collapses the whole layout — cards take
  their grow from a row (definite measure), never from a scroll. The
  render test in `dashboard_test.go` catches it: with `MYGO_UI_SHOTS`
  set it writes every page's PNG, the way the agent app screenshots its
  own surfaces.

```sh
MYGO_UI_SHOTS=/tmp/shots go test ./examples/dashboard/ -run TestRender
```
