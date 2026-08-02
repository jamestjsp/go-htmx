# Workbench ergonomics

How the Process Lab canvas behaves, and why. Read this before changing
`internal/web/static/js/` (the canvas modules), `internal/web/static/tabs.js`,
`internal/web/static/menu.js`, `internal/web/static/app.css`, or the sheet
constants in `internal/studio/model.go`.

The canvas modules divide by the state they own: `viewport.js` (pan, zoom,
fit), `selection.js` (the selection set and the marquee), `dragging.js` (the
block drag, its guides and the nudge), `wiring.js` (the armed port and the
draft edge), `shell.js` (rails and dock), `shortcuts.js` (the reference
sheet), `contextmenu.js` (the canvas menu region), `geometry.js` and `dom.js`
(shared, stateless), `input.js` (every canvas binding, kept together because
they share precedence rather than state), `reapply.js` (the one htmx re-apply
entry point) and `main.js` (which names the order the canvas is rebuilt in).

Before this work the sheet was a fixed 590px box on a scrolling page,
positions were clamped to 1040×500, and there was no pan, no zoom, no
snapping, no multi-selection, and no way to collapse the side rails.

## The constraint that shapes everything

HTMX swaps the entire `#workbench` element on nearly every action. Every
block element, the canvas, and the sheet layer are replaced. **Any state the
client holds must be re-applied afterwards**, or it silently disappears on
the next edit. This is the recurring failure mode in this file; three
separate defects during implementation traced back to it.

Re-application happens in one place, on **both** `htmx:afterSwap` and
`htmx:afterSettle`, and again on `htmx:historyRestore`:

```js
const restoreViewport = (settled) => {
  syncViewportToFlow(settled); applyViewport(); applySelection(); redrawEdges()
}
document.addEventListener('htmx:afterSwap', () => restoreViewport(false))
document.addEventListener('htmx:afterSettle', () => restoreViewport(true))
document.addEventListener('htmx:historyRestore', () => restoreViewport(true))
```

Both swap events are required. At `afterSwap`,
`document.querySelector('#flow-canvas')` still returns the node htmx is about
to discard, so styling it there writes to a doomed element and the view snaps
back to the origin. `afterSettle` is what actually sticks. The pair is kept
because `afterSwap` restores sooner and avoids a visible flash.

`historyRestore` is the third case and is easy to miss: Back and Forward
rebuild the page from htmx's history cache and fire **neither** swap event, so
without it the canvas keeps the transform of the sheet you came from and the
signal wires are never drawn. Tab switching made that reachable in one click.

## Sheet geometry

The domain owns the sheet. `internal/studio/model.go` exports `GridPitch`
(20), `BlockWidth` (172), `BlockHeight` (84), `SheetWidth` (6000) and
`SheetHeight` (4000). `internal/web/view.go` passes them to the template as
`sheetGeometry`, and the client reads them off `data-` attributes on
`#flow-canvas`. Nothing on the client hardcodes these numbers, so the grid,
the snap step, and the bounds cannot drift from the server.

**The grid is authoritative on the server.** `clampPosition` snaps every
stored position to the grid and keeps the whole block inside the sheet, so
a replayed or hand-edited request cannot produce an off-grid block.

## Viewport

`#flow-canvas` is a clipped, non-scrolling box. `#sheet` inside it carries
`transform: translate(var(--pan-x), var(--pan-y)) scale(var(--zoom))` with
`transform-origin: 0 0`. Blocks keep absolute sheet coordinates, so no
template coordinate maths changed.

The grid is painted on the **viewport**, not the sheet, using
`background-size: calc(pitch * zoom)` and a `background-position` that
tracks pan. That gives an infinite grid without a 6000×4000 tiled element.
`data-zoom-band="coarse"` drops the fine lattice below 60% zoom.

Every interaction converts pointer coordinates through one helper:

```js
function screenToSheet(clientX, clientY) {
  const bounds = canvas().getBoundingClientRect()
  return { x: (clientX - bounds.left - viewport.x) / viewport.zoom,
           y: (clientY - bounds.top  - viewport.y) / viewport.zoom }
}
```

Reading `offsetLeft`/`scrollLeft` directly, as this file used to, breaks
silently the moment zoom leaves 100%. **Coordinate bugs at non-100% zoom
are the likeliest defect class here** — route new interactions through
`screenToSheet` and verify at 25% and 400%.

Zoom range is 25%–400%. Zooming pins the sheet coordinate under the
pointer; the invariant is exact (measured drift 2.8e-14 sheet units).

### Bindings and why

| Gesture | Action | Rationale |
| --- | --- | --- |
| Wheel | Pan | Matches every modern canvas tool |
| Cmd/Ctrl + wheel, pinch | Zoom about pointer | Trackpad pinch arrives as ctrl+wheel |
| Space + drag, middle-drag | Pan | Leaves plain drag free for the marquee |
| Drag empty canvas | Marquee select | Simulink and Figma both do this |

## Snapping

Snapping happens in sheet space, never screen space, or the step would
change with zoom. The grid is the default resting place; an edge or centre
shared with a neighbour (within 5px) overrides it and draws a hairline
guide, counter-scaled by zoom.

Two constraints are easy to get wrong:

- **Alignment candidates must themselves be on-grid.** A block is 172×84
  while the grid is 20, so centre and far-edge alignments land between
  intersections. The server then re-snaps and the block jumps on the next
  reload. Candidates are filtered by an `onGrid` test.
- **Alt suspends alignment only, never the grid.** The original plan
  promised Alt for arbitrary off-grid placement, which contradicts the
  authoritative grid: the position would be silently rewritten on save.
  Alt now escapes only the neighbour magnetism, which is the case where
  users actually want out.

## Selection

Multi-selection is a client-side `Set` of block ids. The server keeps its
single `selected` query parameter for the inspector, so the HTMX contract
is unchanged and a marquee drag costs no round trips.

- One block selected → normal server round trip, full parameter inspector.
- Two or more → no round trip; a floating action bar over the canvas shows
  the count with Fit and Delete. It is contextual to the work rather than
  parked in the inspector rail.
- After a swap, ids that no longer exist are dropped. With nothing
  selected the client defers to the server-rendered selection, so a swap
  never drops the inspector's highlight.

Dragging any selected block moves the whole selection by one delta, so
relative spacing is preserved exactly.

## Wiring

Drag from an output port to an input port is the primary gesture. The
pointer is captured on the canvas so the draft edge keeps tracking after
leaving the port, and the target is found geometrically with
`elementFromPoint`. The draft snaps to the hovered terminal before release.

A press with **no travel** leaves the older click-then-click mode armed, so
both gestures coexist. Because ports are real buttons, Enter or Space on a
focused output arms it and the same key on a focused input completes it.
Escape cancels either. Only the primary pointer button wires; pointer
cancellation, an HTMX swap, and Back/Forward history restoration all discard
an armed source before it can refer to stale markup.

Every rendered terminal carries its block id and zero-based port index.
`POST /flows/{id}/connections` receives `source_id`, `source_port`,
`target_id`, and `target_port`; omitting a port remains the compatibility path
for an older client and means port 0. A Sum renders one gray input pip per sign
and prints the sign beside it. Its inspector rows put the destination input
and sign first, then the source output, so two wires from one source into
different Sum ports remain distinguishable.

### Automatic signal routing

`orthogonal-routing.js` is the one authority for signal geometry. The server
renders connection ids, endpoint block ids, port ids, and resolved vertical
port centres; it does not render a competing path. `geometry.js` turns the
current block rectangles into obstacles and asks the router for every path on
first load, after an HTMX swap or history restore, and while a block moves.
`wiring.js` uses the same operation for the draft edge.

Routes follow the Simulink reading convention:

- every segment is horizontal or vertical;
- a signal leaves an output to the right and enters an input from the left;
- block rectangles are expanded by a clearance margin before pathfinding;
- short routes and fewer bends win, while crossings and shared segments add
  cost; and
- right-to-left feedback uses a clear outer lane instead of looping a spline
  back through the model.

The router builds a rectilinear visibility graph from the endpoint and obstacle
coordinates, then finds the lowest-cost deterministic path. Earlier connections
become occupancy hints for later ones, which separates avoidable overlaps while
leaving the result stable across reloads. If crowded geometry has no graph path,
the fallback is still orthogonal.

Automatic block arrangement, editable manual waypoints, and shared branch
junctions are intentionally separate problems. Routing reacts to the positions
the user chose; it does not move blocks or change the stored connection model.

Endpoint offsets remain the declared port centre when it exists, or the block
midpoint for a preserved legacy wire beyond the declared sign list. Those
offsets are carried on the path markup, not recomputed from currently visible
port buttons.

Self-connections are refused on the client with a status message rather
than a server round trip and an error banner. The server remains the
authority on everything else; client feedback is an affordance, not
validation.

**Port occlusion.** `.block-card` is `position: absolute; z-index: 5`, so
each card is its own stacking context and a card later in DOM order paints
over an earlier card's ports, making them unclickable where blocks overlap.
Cards rise on `:hover`/`:focus-within`, and higher again while wiring.
Ports also carry an invisible `calc(22px / var(--zoom))` hit pad so the
target stays roughly constant on screen. On a multi-port block the vertical
pad is capped at the distance to its neighbour, and dense valid Sum ports
shrink with that distance, so even sixteen terminals remain distinct hit
targets instead of overlapping. Single-port blocks retain their original
14px pip and 34px top offset throughout the 25%–400% zoom range.

## Keyboard

Every binding is guarded by `typingInAField` **first**. Without it, typing
a block name deletes the selection on Backspace and duplicates it on "d" —
the most destructive thing this file could get wrong. The dock resizer also
owns the arrow keys while focused, so the guard checks for it too.

| Keys | Action |
| --- | --- |
| Delete / Backspace | Delete selection (confirms above one block) |
| Arrows / Shift+arrows | Nudge one / five grid steps |
| Cmd/Ctrl + A | Select all |
| Cmd/Ctrl + D | Duplicate; wires between blocks are **not** copied |
| Cmd/Ctrl + = / − / 0 | Zoom in / out / reset |
| Shift + 1 | Fit to contents |
| Esc | Cancel wiring, or clear selection |
| Cmd/Ctrl + Enter | Run the simulation |
| Cmd/Ctrl + Shift + ← / → | Move the open sheet along the tab strip |
| ? | Shortcut sheet |

The sheet chord is deliberately not a bare arrow, and the nudge answers a bare
arrow and `Shift` + arrow and nothing else. Both bindings sit on `document`,
so without that rule one keypress would move the selection *and* the strip —
a collision the field-and-menu guard cannot see, because neither party is a
text field or a menu. `tabs.js` applies its own guard for the same reason it
exists here: an open field or an open menu takes the key first. The chord does
nothing at either end of the strip rather than wrapping around.

Duplicate deliberately does not copy wiring between the originals: a
sub-diagram that silently rewired itself is harder to reason about than one
the user connects on purpose. The shortcut sheet says so.

## Context menus

The menu primitives live in `menu.js`, not in the canvas modules: the canvas
and the tab strip both open one, and a second implementation would have
drifted on edge flipping, arrow keys, and focus restore. `js/contextmenu.js`
and `tabs.js` each supply their own items and keep nothing else.

The native menu is suppressed **only** over the sheet and the tab strip, so
the browser's own menu still works over the rails and the dock. Right-clicking
outside the current selection re-targets it; inside it, the selection and its
plural labels are kept. The menu flips near a viewport edge and is arrow-key
navigable.

Two rules the shared version has to keep, both of which the tab strip broke
when it first arrived.

- **Focus returns to what the menu was raised over, and `tabindex` is stamped
  only where it is missing.** `#flow-canvas` is a `div` and needs
  `tabindex="-1"` to accept focus back; a flowsheet tab is an `<a href>` and
  already takes it, and stamping `-1` on that link would drop the tab out of
  the tab order permanently — the menu would make the thing it was raised over
  unreachable by keyboard.
- **An open menu owns the keyboard**, the way a focused text field does.
  `ProcessLab.menu.ownsKey(event)` is what stops `Ctrl+Shift+←` reordering
  tabs underneath it. It answers yes for the whole dispatch of the `Escape`
  that closed the menu as well, or every handler downstream sees a menu that
  is already gone and treats the dismissal as a second Escape, clearing the
  selection and cancelling a wire in progress.

The empty-canvas menu places a block exactly where you right-clicked. It
reads the catalogue off the palette rather than duplicating it, so a new
block kind on the server appears with no client change.

## Shell

On desktop, `.workbench` is a 100dvh grid and the page does not scroll. At
860px and below, the layout deliberately stacks and the page scrolls so all
controls remain reachable without horizontal overflow. The palette list,
inspector body, and dock body scroll internally on desktop. Collapsing a rail
leaves a 46px icon strip rather than hiding it, so the palette's glyph buttons
still add blocks.

The flowsheet tab strip is the shell's fourth grid row, spanning the full
width below the simulation dock and above the readout rail. It is outside both
rails and outside the dock on purpose: the strip names the other sheets, and a
strip that disappeared with a collapsed rail or a dragged-down dock would take
the only way out of the open sheet with it.

Visually it belongs to the machined housing, not to the sheet. The active tab
lights a teal lamp bar along its top edge rather than adopting the vellum of
the drawing, because a lit white tab under the sheet reads as a second sheet.
Ink brightness carries the same signal as the lamp, so the state survives
without colour.

## Flowsheet tabs

`tabs.js` owns the strip. The markup is server-rendered and comes back whole
with every swap, so the file holds only what the markup cannot state: how far
the track is scrolled, whether the `‹ ›` arrows have anywhere left to go,
which tab is being renamed, and where a dragged tab would land.

Each tab is a real `<a href="/projects/{p}/flows/{f}">` carrying `hx-get`,
`hx-target="#workbench"` and `hx-push-url`. Without JavaScript it navigates
normally; with htmx it swaps the workbench and pushes the canonical URL. The
fragment URL is never pushed — it renders a bare `<main>` with no stylesheet
if it is reloaded or shared.

**Nothing here holds a tab node across a request.** The flowsheet id is the
identity and the node is looked up again on the other side, because any swap
can replace the strip mid-gesture.

### Bindings

| Gesture | Action | Rationale |
| --- | --- | --- |
| Click | Open that sheet | Swaps `#workbench` only, so the sheet does not flash |
| Double-click | Rename in place | The workbook gesture; `Enter` commits, `Esc` reverts |
| Right-click | Rename, Duplicate, Delete | Delete is absent at one sheet, matching the domain's refusal |
| Drag | Move along the strip | An insertion marker shows the landing place |
| Ctrl/Cmd + Shift + ← / → | Move the open sheet one place | The keyboard path for the drag; plain arrows already nudge blocks |
| `+` | Add a sheet and open it in rename | A dialog for a name that is about to be typed anyway is a step too many |
| `‹ ›` | Scroll by one tab | A fraction of the track would step by half a name |

### Traps

- **The CSP has no `'unsafe-eval'`, so htmx's `hx-trigger` filter syntax is
  dead.** `keyup[key=='Enter']` is compiled with `new Function`; in the
  browser it is dropped with a console violation while every Go test still
  passes. Every key gesture here is plain JavaScript for that reason, and the
  register's rename commits through a plain custom event for the same one.
- **Committing a rename on blur has to ignore the blur a swap causes.** The
  first click of a double-click opens the sheet, so the strip is replaced
  about ten milliseconds after the field is created and Blink blurs the field
  on the way out while it is still connected. Treating that as "the user left
  the field" ends the rename the gesture has only just asked for. A `swapping`
  flag set on `htmx:beforeSwap` is what distinguishes them, and the pending
  rename is re-attached by flowsheet id on the other side.
- **Creating a sheet is a redirect, so "open it in rename" has to survive a
  navigation.** `POST /projects/{id}/flows` answers with a redirect onto the
  new sheet; the request is left in `sessionStorage` under
  `processlab:name-new-sheet` and read by the page that lands.
- **A drag must not also open the sheet it was dropped on.** The click that
  follows the release is suppressed for one turn of the event loop, and a
  pointer released outside the window never arrives as `pointerup` — `blur`
  and `pointercancel` end the drag too, or the marker stays up and steals the
  next click.
- **Reorder is the one mutation htmx does not carry.** `PATCH
  /projects/{id}/flows/order` sends the project's full ordered id list and
  answers 204, as the two canvas drag endpoints do: the client has already
  drawn the order, and the workspace the domain returns opens the project's
  first tab, so rendering it would answer a drag by moving the user to another
  sheet. A refusal re-renders the strip from the server, which remains the
  authority on order.

### Per-sheet viewport

Switching sheets is the one swap that changes which flowsheet `#workbench`
holds, and `processlab:viewport:<flowID>` is keyed per flow. Applying the
outgoing sheet's pan and zoom to the incoming one both misplaces it *and* —
because `applyViewport()` ends by calling `saveViewport()` — overwrites the
incoming sheet's stored view with the outgoing sheet's. Whichever of the two
swap events first sees a new `data-flow-id` therefore loads that sheet's own
stored viewport, and fits the sheet when it has never been opened.

Rail and dock state stays global. `SHELL_KEYS` are fixed strings, not
per-flow, and making the rails remember themselves per sheet is neither
required nor obviously desirable.

## Client-held state

All per-user view state, none of it in the flow record:

| Key | Store | Value |
| --- | --- | --- |
| `processlab:rail-left`, `processlab:rail-right` | local | `collapsed` / `expanded` |
| `processlab:dock-height` | local | integer px |
| `processlab:viewport:<flowID>` | local | `{x, y, zoom}` |
| `processlab:name-new-sheet` | session | timestamp of a pending new-sheet rename |

Selection is in-memory only and does not survive a reload, deliberately. The
new-sheet request is in `sessionStorage` and expires after 15 seconds: it
describes one navigation in one tab, and a stale one would open a rename on a
sheet the user had since walked away from.

Tab **order** is not here. It is `flows.position` on the server, because it is
a property of the project rather than of the browser looking at it, and two
windows on the same project must not disagree about it.

## Batch endpoints

Move, delete and duplicate each take repeated `id` values and run in one
transaction. Per-block requests would be slow and non-atomic, leaving a
half-moved or half-deleted arrangement visible if any step failed. Each
rejects ids outside the named flow without touching anything.

| Endpoint | Operation |
| --- | --- |
| `PATCH /flows/{id}/blocks/positions` | `MoveBlocks` |
| `DELETE /flows/{id}/blocks` | `DeleteBlocks` |
| `POST /flows/{id}/blocks/duplicate` | `DuplicateBlocks` |
| `DELETE /blocks/{id}/connections` | `DisconnectBlock` |

## Verifying a change

Go tests cover the domain and the handlers:

```bash
gofmt -l . && go vet ./... && go test ./...
```

Interaction behaviour cannot be covered that way. **Templates and static
assets are `go:embed`-ed into the binary, so editing anything under `js/`,
`tabs.js`, `menu.js`, or either stylesheet changes nothing the server serves
— rebuild before any browser check.** During this work six CDP suites drove real
gestures against a headless Chrome, 88 checks in total, covering: viewport
(18), snapping (13), selection (15), keyboard (16), context menus (15) and
wiring (11).

Five traps worth knowing if you write more of them:

- The SQLite file outlives a page reload. Clearing localStorage is not a
  reset; restart the server with a fresh database between independent
  sections, or earlier checks leave blocks where they moved them.
- Pick interaction targets with `elementFromPoint`, not by DOM order.
  Cards form their own stacking contexts, so a neighbour can silently
  steal a drag and the assertion measures nothing.
- Choose fixtures against the model, not just the layout. Every input in
  the seeded flowsheet is already wired, so a wiring check drawn from it
  measures nothing — the server rightly rejects every pair. Add a free
  block first.
- Answer `Page.javascriptDialogOpening`. Deleting a sheet or a project goes
  through `window.confirm`, which blocks the renderer until it is handled, so
  a suite that does not listen for the dialog hangs rather than failing.
- Assert the landing rule against the strip's *order*, not against an index
  you assumed. Deleting the first tab opens its right neighbour and deleting
  any other opens its left, so a check written for one of those cases silently
  measures nothing in the other.

### Verification record

Last full pass, at 1440×900 unless noted. Rows above the rule are the canvas
suites; below it is the navigation redesign, driven end to end in one browser
session against a real Chrome over CDP.

| Area | Result |
| --- | --- |
| `gofmt -l`, `go vet`, `go test -race ./...` | clean |
| Type floor (`grep` for `font-size` below 11px) | clean |
| Viewport: zoom anchor, pan, fit, persistence across reload and swap | 18/18 |
| Snapping: grid landing at 25% and 199%, guides, rendered = persisted | 13/13 |
| Selection: marquee, shift-extend, uniform group delta, batch delete | 15/15 |
| Keyboard: input-focus guard, nudge, select-all, duplicate, sheet | 16/16 |
| Context menus: edge flipping, keyboard nav, placement, disconnect | 15/15 |
| Wiring: drag at 100% and 27%, cancel, self-refusal, sticky mode | 11/11 |
| Port model: Sum labels, pointer rewire to ports 0/1, draft snap, persisted/reloaded geometry, focused-port keyboard path, cancel/right-click/history safety, dense targets, seeded simulation | passed |
| Port migration: every target ranked by connection order, source ports on 0, non-Sum targets remaining on 0, second open idempotent, sign growth allowed and wired-port shrink refused | passed |
| — | — |
| Full path on a fresh database: create a project from the register, `+` twice, rename, duplicate, drag reorder, keyboard reorder, delete, switch projects both ways, rename and delete a project, restart, order and names intact | 77/77 |
| The same path on a database written before `position`, `project_id` and `model_updated_at` existed: migrated on open, tabs in the old by-name order, legacy per-column block parameters intact, every operation working, and a second open leaving the hand-made order alone | 46/46 |
| No horizontal page overflow at 1440, 1280, 860, 620 on both the register and the workbench | 8/8 |
| No console errors and no CSP violations on either page | clean |

The pre-migration fixture is the schema as it stood before projects: four
flows inserted out of name order, one of them wired, with parameters still in
the `gain` and `time_constant` columns. It is the only check that speaks for a
user's existing file, and a fresh database cannot stand in for it — `CREATE
TABLE` gives a new file `position` outright, so the backfill never runs.

The connection-port fixture is separate and uses the old
`UNIQUE(flow_id, source_id, target_id)` table. Startup rebuilds that table,
assigns every source endpoint to port 0, and ranks each target's inbound wires
in connection-id order. Non-Sum blocks had at most one inbound wire and remain
on target port 0; Sum wires retain the old compiler's sign positions. A
broadcast sign is widened only when doing so is numerically identical. The
test closes and reopens the file to prove the assignment is not repeated.

Behaviours confirmed by hand in the same pass: collapsing both rails to
icon strips, dragging the dock between header-only and 70vh, and the
readout rail tracking the cursor in sheet coordinates.
