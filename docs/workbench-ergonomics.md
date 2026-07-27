# Workbench ergonomics

How the Process Lab canvas behaves, and why. Read this before changing
`internal/web/static/app.js`, `internal/web/static/app.css`, or the sheet
constants in `internal/studio/model.go`.

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
`htmx:afterSettle`:

```js
const restoreViewport = () => { applyViewport(); applySelection(); redrawEdges() }
document.addEventListener('htmx:afterSwap', restoreViewport)
document.addEventListener('htmx:afterSettle', restoreViewport)
```

Both are required. At `afterSwap`, `document.querySelector('#flow-canvas')`
still returns the node htmx is about to discard, so styling it there writes
to a doomed element and the view snaps back to the origin. `afterSettle` is
what actually sticks. The pair is kept because `afterSwap` restores sooner
and avoids a visible flash.

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
`elementFromPoint`.

A press with **no travel** leaves the older click-then-click mode armed, so
both gestures coexist and the keyboard-accessible path survives. Escape
cancels either.

Self-connections are refused on the client with a status message rather
than a server round trip and an error banner. The server remains the
authority on everything else; client feedback is an affordance, not
validation.

**Port occlusion.** `.block-card` is `position: absolute; z-index: 5`, so
each card is its own stacking context and a card later in DOM order paints
over an earlier card's ports, making them unclickable where blocks overlap.
Cards rise on `:hover`/`:focus-within`, and higher again while wiring.
Ports also carry an invisible `calc(22px / var(--zoom))` hit pad so the
target stays roughly constant on screen and wiring works at 40%.

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
| ? | Shortcut sheet |

Duplicate deliberately does not copy wiring between the originals: a
sub-diagram that silently rewired itself is harder to reason about than one
the user connects on purpose. The shortcut sheet says so.

## Context menus

The native menu is suppressed **only** over the sheet, so the browser's own
menu still works over the rails and the dock. Right-clicking outside the
current selection re-targets it; inside it, the selection and its plural
labels are kept. The menu flips near a viewport edge and is arrow-key
navigable.

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

## Client-held state

All per-user view state, none of it in the flow record:

| Key | Value |
| --- | --- |
| `processlab:rail-left`, `processlab:rail-right` | `collapsed` / `expanded` |
| `processlab:dock-height` | integer px |
| `processlab:viewport:<flowID>` | `{x, y, zoom}` |

Selection is in-memory only and does not survive a reload, deliberately.

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
assets are `go:embed`-ed into the binary, so editing `app.js` alone changes
nothing the server serves — rebuild before any browser check.** During this
work six CDP suites drove real gestures against a headless Chrome, 88
checks in total, covering: viewport (18), snapping (13), selection (15),
keyboard (16), context menus (15) and wiring (11).

Three traps worth knowing if you write more of them:

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

### Verification record

Last full pass, at 1440×900 unless noted:

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
| No horizontal overflow at 1440×900, 1280×720, 1099×800, 860, 620 | 5/5 |

Behaviours confirmed by hand in the same pass: collapsing both rails to
icon strips, dragging the dock between header-only and 70vh, and the
readout rail tracking the cursor in sheet coordinates.
