# Project navigation redesign

Date: 2026-07-27
Status: approved for planning
Supersedes: the header popover navigation shipped on `codex/project-flowsheets`

## Problem

The first pass at projects put navigation in two `<details>` popovers in the
workbench header: one listing projects, one listing flowsheets, each with an
inline create form. Two things break.

Opening a previously created project is not discoverable. The control reads as
a label rather than a menu, and `/` redirects straight into a flowsheet, so
there is no moment where the application shows you what projects exist.

Flowsheets inside a project are not navigable. They sit behind a menu that
shows only a count, so switching sheets costs two clicks and gives no sense of
how many sheets a project holds or which one is open.

## Goals

- A projects home at `/` that shows every project and every flowsheet.
- Flowsheets inside a project navigable as a persistent tab strip, with the
  operations a spreadsheet user expects: rename, duplicate, delete, reorder.
- Project rename and delete, which the domain does not support today.
- No regression to the workbench canvas, which is not part of this work.

## Non-goals

- Duplicating a project.
- Hierarchical subflowsheets or subsystem blocks inside a flowsheet.
- Any change to block, connection, or simulation behaviour.

## Visual direction

Two shells that share the `:root` tokens and nothing else.

The **register** at `/` is a drawing register: a dense table on drafting
vellum under a thin machined-housing bar, styled after the drawing register on
an engineering title block. Columns are Project, Sheets, Edited. A row expands
to reveal that project's flowsheets, so the home screen can take you to a
specific sheet rather than only to a project. It scales past a dozen projects
where cards would not.

The **workbench** keeps its machined housing unchanged. Its new tab strip is
annunciator-styled: tabs sit inside the housing, and the active tab lights a
teal lamp bar along its top edge rather than adopting the vellum of the sheet.
A tab whose model changed since its last simulation carries an amber dot.

## Architecture

### Routes

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/` | Register page. Replaces the redirect into a flowsheet. |
| `GET` | `/projects/{id}` | Unchanged: redirects to the project's first flowsheet. |
| `GET` | `/projects/{id}/flows/{id}` | Unchanged: the workbench. |
| `PUT` | `/projects/{id}/name` | Rename a project. |
| `DELETE` | `/projects/{id}` | Delete a project and everything under it. |
| `POST` | `/flows/{id}/duplicate` | Duplicate a flowsheet. |
| `DELETE` | `/flows/{id}` | Delete a flowsheet. |
| `PATCH` | `/projects/{id}/flows/order` | Persist tab order. |

`POST /projects/{id}/flows` stays, but accepts an empty name and generates one.

### Navigation mechanics

Tabs and register links are real `<a href>` elements carrying `hx-get` and
`hx-push-url`. Without JavaScript they navigate normally. With HTMX they swap
`#workbench` only and push the canonical URL, so switching sheets has no page
flash. `GET /flows/{id}/workbench` already returns that fragment; it becomes
project-aware so the swapped markup includes the tab strip.

Per-sheet viewport is added work, not an inheritance. `viewportKey()` is keyed
per flow (`app.js:601`), but `loadViewport()` runs exactly once at page load
(`app.js:1296`), while `restoreViewport` fires `applyViewport()` on every swap
and `applyViewport()` ends by calling `saveViewport()` (`app.js:636`) against
whatever flow id is in the live DOM. Switching sheets would therefore stamp
sheet A's pan and zoom onto sheet B *and* overwrite B's stored viewport. The
tab strip must detect the flow-id change on swap and load that sheet's stored
viewport, falling back to `fitView()`, before applying it.

Rail and dock state stays global: `SHELL_KEYS` are fixed strings
(`app.js:1317`), not per-flow, and making them per-sheet is neither required
nor obviously desirable.

HTMX history needs explicit handling. `hx-push-url="true"` would push the
fragment URL `/flows/{id}/workbench`, which renders a bare `<main>` with no
stylesheet if reloaded or shared, so tabs must push the canonical
`/projects/{p}/flows/{f}` explicitly. HTMX history restore fires neither
`htmx:afterSwap` nor `htmx:afterSettle`, and no `htmx:historyRestore` listener
exists today, so Back after a tab switch would leave the canvas transform
un-stamped and edges undrawn. The tab strip adds that listener.

Rejected: keeping every sheet in the DOM and toggling visibility. It duplicates
server state on the client and needs cache invalidation on every mutation,
which fights the server-rendered fragment model the application is built on.

### File organisation

`internal/web/server.go` is 553 lines and would pass 750 with the new
handlers. Project and flowsheet lifecycle handlers move to
`internal/web/navigation.go`; `server.go` keeps routing, the shell renderers,
and the block, connection, and simulation handlers.

`internal/studio/workspace.go` (271 lines) currently mixes read models with
lifecycle operations. It splits into `workspace.go` (the `Workspace` and
`Register` read models) and `lifecycle.go` (create, rename, delete, duplicate,
and reorder for both projects and flowsheets).

`internal/web/static/app.js` is 1533 lines and tab behaviour adds roughly 200.
The context-menu primitives (`buildMenu`, `menuButton`, open and close, arrow
key navigation) move to `menu.js`, since the canvas and the tabs both need
them. Tab behaviour lives in `tabs.js`. The register page loads neither.

## Data

### Schema change

`flows.position INTEGER NOT NULL DEFAULT 0`, added by an `ensureFlowPositions`
migration in the same style as `ensureModelUpdatedAt`, `ensureParametersJSON`,
and `ensureProjects`. It backfills per project using today's ordering
(`name COLLATE NOCASE, id`), so an existing database opens with its tabs in the
order the old menu showed. Every flow query then orders by `position, id`. New
flowsheets append at `max(position) + 1` within their project.

### Cascade hardening

Deleting a project relies on `ON DELETE CASCADE` reaching flows, blocks,
connections, events, and simulation runs. The schema declares those foreign
keys and `PRAGMA foreign_keys = ON` currently holds because the pool is pinned
to `SetMaxOpenConns(1)`. That is an implicit dependency for an operation that
destroys rows across five tables, so the pragma also goes in the DSN
(`?_pragma=foreign_keys(1)`).

### Operations

Each is a single transaction returning the affected `Workspace`, matching the
existing `CreateFlow` and `RenameFlow` shape.

- **`RenameProject`** — mirrors `RenameFlow`, including the 80-character limit
  and the required-name rule from `workspaceName`.
- **`DeleteProject`** — refuses when it is the last remaining project.
- **`DeleteFlow`** — refuses when it is the last flowsheet in its project. A
  project therefore always holds at least one sheet, which is what guarantees
  the tab strip is never empty.
- **`DuplicateFlow`** — deep-copies blocks, including positions and JSON
  parameters, and connections with block ids remapped to the copies. Simulation
  runs and events are not copied; the copy records one event, `Duplicated from
  ‹name›`. Named `‹name› copy` and inserted at the source's `position + 1`, so
  it appears immediately right of the tab it came from.
- **`ReorderFlows`** — takes the full ordered id list for one project and
  rewrites positions in a transaction, rejecting ids belonging to another
  project using the same guard `MoveBlocks` already applies.
- **`CreateFlow`** — when the submitted name is empty, generates the next free
  `Flowsheet N` for that project rather than rejecting the request.

### Register read model

`Register` in `workspace.go`: every project with its flowsheet count and
last-edited timestamp, plus every project's flowsheets in one grouped query.
No N+1, and rows expand with no further request.

`NeedsRun` is not a register-only concern — the tab strip needs the same flag —
so it lives on `studio.Flow` and is populated by the flow queries themselves.
It is derived from

```sql
NOT EXISTS (
  SELECT 1 FROM simulation_runs r
  WHERE r.flow_id = f.id AND r.created_at >= f.model_updated_at
)
```

which is the same predicate `snapshot` already uses to decide whether a chart
is current, so the amber tab dot and the simulation dock cannot disagree. Note
that `snapshot` compares RFC3339Nano *text*, not parsed times; the register and
the tab strip must use the identical raw-text comparison, because "fixing" one
side to a datetime comparison is exactly how the two would drift apart.

### Landing after a destructive edit

Deleting the active flowsheet opens its left neighbour, or its right neighbour
when it was first. Deleting the project you are inside returns you to the
register at `/`.

## Components

### Register page

`templates/register.html`, `static/register.css`, and `static/register.js`,
sharing only the `:root` tokens with the workbench. The third file is not
optional: the CSP sets `script-src 'self'` with no `'unsafe-inline'`
(`server.go:542`), so an inline `<script>` or an `onclick=` attribute is
silently blocked in the browser while every Go test still passes. The page also
needs the htmx `<script>` tag, integrity hash included, copied from
`page.html`.

A topbar carries the brand and **+ New project**. The register table follows:
Project, Sheets, Edited. Rows expand with `<details>`/`<summary>` — the
flowsheets are already in the DOM, so expansion needs no request and no
JavaScript. A project name opens that project's first sheet; a flowsheet chip
opens that sheet directly. Double-clicking a project name renames it in place.
The row menu holds Rename and Delete, and Delete is absent when only one
project exists. The empty-register state is defensive markup only — `seed`
creates a project whenever no flows exist and `DeleteProject` refuses the last
one, so the state is unreachable through the public API and is covered at the
view-model level rather than through `Open`.

### Tab strip

`templates/tabs.html`, included by `workbench.html`, sitting full width below
the simulation dock and above the readout rail, so it is visible regardless of
rail or dock state.

Each tab is an `<a href="/projects/{p}/flows/{f}">` with `hx-get` and
`hx-push-url`; the active tab carries `aria-current="page"` and the teal lamp.

- Double-click swaps the label for an input. Enter commits through the existing
  `PUT /flows/{id}/name`; Escape reverts.
- Right-click opens Rename, Duplicate, Delete. Delete is absent at one sheet.
- Dragging a tab along the strip shows an insertion marker and commits the new
  order on drop. `Ctrl+Shift+←` and `Ctrl+Shift+→` move the active tab.
- Overflow scrolls horizontally. The `‹ ›` arrows scroll by one tab and disable
  at each end. The active tab is scrolled into view after every swap.
- The right end shows `N SHEETS ⌄`, a jump list of every sheet in the project.
- **+** creates a sheet immediately with a generated name and opens inline
  rename on the new tab, with no dialog.

### Topbar cleanup

Both `<details>` popovers and the flowsheet-name form come out, replaced by
`Projects` (a link home), a separator, and a `‹project› ▾` switcher that lists
projects and offers New project. Block and signal counts and the saved
indicator stay. The `?` shortcut sheet gains the tab bindings.

## Error handling

Every refusal is enforced in the domain and also hidden in the interface, so
the last-sheet and last-project rules cannot be reached by accident and cannot
be bypassed by a direct request either. Deleting a project asks for
confirmation naming the project and its sheet count.

A rejected rename reverts the tab to its previous name and raises the existing
error banner. A failed reorder re-renders the strip from the server, which
remains the source of truth for order. Duplicate and delete return the
workbench fragment, matching every other mutation.

## Testing

Go tests cover the delete refusals for the last project and the last flowsheet;
duplicate fidelity including block parameters, positions, remapped connection
ids, and the absence of copied runs; reorder rejecting ids from another
project; the position backfill migrating a database created before the column;
the register query's counts and `NeedsRun` flag; the six new routes; and the
workbench fragment rendering tabs in position order with `aria-current` on the
active tab.

Interaction behaviour is verified by driving pointer and key gestures against
headless Chrome over CDP, as the previous round was: drag reorder, inline
rename, context menus, overflow scrolling, the keyboard paths, and rendering at
1440, 1280, 860, and 620px.

## Consequences

`README.md` needs its projects-and-flowsheets section rewritten: the popover
walkthrough no longer describes the interface, and `/` no longer redirects.
`docs/workbench-ergonomics.md` gains the tab strip in its description of the
shell.
