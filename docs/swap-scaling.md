# Workbench swap scaling

What the full-`#workbench` swap actually costs as a flowsheet grows, measured
rather than guessed. This is a decision document: it sets a block-count budget
and it names the one piece of follow-up work the numbers justify. No
application code was changed to produce it.

Read this before proposing out-of-band swaps, per-card patching, canvas
virtualisation, or any other change whose motivation is "the full swap must be
too slow by now". It probably is not the thing that is slow.

The harness is `docs/swap-scaling-bench.mjs`. It builds its own binary, its own
scratch database and its own Chrome profile, and removes all three on exit:

```
node docs/swap-scaling-bench.mjs --sizes 50,150,400 --out results.json
```

## The short version

At 400 blocks and 400 signals, a parameter edit — the mutation that re-renders
and swaps the entire workbench — takes **198 ms end to end**. The server
spends 12 ms of that and the wire carries 428 KB; parsing and swapping the
fragment costs 23 ms; htmx waits a further 25 ms on a settle timer. The
remaining **137 ms is the client re-applying its own state**, and 110 ms of
that is one function: `redrawEdges` in
`internal/web/static/js/geometry.js`, run twice per swap, rewriting 800 SVG
path attributes that the server had already written correctly. Zero of the
800 change.

The full swap is not the problem. Neither is the fragment size, the linear
`blockByID` scan in `view.go`, nor the zoom level. Fix `redrawEdges` and the
architecture holds.

## What was measured, and how

Three sizes — 50, 150 and 400 blocks — at two zoom levels, against a real
server and a real headless Chrome.

**Server time.** Node's `fetch` on loopback with the body fully drained by
`arrayBuffer()`, timed with Node's `performance.now()`, 30 samples per
endpoint after 5 warm-ups on a reused keep-alive connection. The `floor`
column is the same client fetching a small static asset: it lands at 0.1 ms,
so loopback framing and this client's own overhead are about a tenth of a
millisecond and everything above the floor is server work.

Time to first byte was deliberately *not* used. Go flushes response headers on
the handler's first write, so TTFB would report how long it took to produce
the first few kilobytes of a 428 KB fragment, not how long the render took.

**Browser time.** Chrome 150 headless, driven over the DevTools Protocol from
the harness with node's global `WebSocket` and `fetch`. The swap is bracketed
by listeners added to `document` in both capture and bubble phase. Every
application listener was registered when its module first evaluated, so a
capture-phase listener added afterwards still runs *before* them and a
bubble-phase one still runs *after* — which is what puts a timestamp on each
side of the re-apply pass without editing a line of it.

**Attribution.** A CDP sampling profile at 100 µs, taken in a separate pass
after the timings so it cannot perturb them.

**`redrawEdges` on its own.** `input.js` binds `redrawEdges` to window resize,
so dispatching a synthetic `resize` runs the real function synchronously and
its cost is the gap around `dispatchEvent`. `shell.js` also listens to resize,
so `applyShellState` is inside that figure; it does not grow with block count,
and at 50 blocks the whole dispatch is 1.9 ms, which bounds it.

### Precision and confounds, stated plainly

- `performance.now()` in the page is coarsened to **100 µs**. That is why the
  50-block drag figures sit on 0.1 ms steps. Paint timing is coarser again —
  the FCP values land on 4 ms multiples — so treat FCP as indicative only.
- htmx is loaded from `cdn.jsdelivr.net`. It came from the browser cache on
  **all 42 measured page loads** (the harness reports the count), so the CDN is
  not inside the load figures. A genuinely cold first load would add a network
  fetch that has nothing to do with block count.
- Headless Chrome is not a user's Chrome: no GPU raster, different
  compositing. The zoom comparison is the measurement most exposed to this.
  What survives the caveat is the ranking, not the absolute paint cost.
- The segment columns are each the median of their own sample, so they do not
  sum exactly to the median total.
- One machine — Apple M1 Pro, 8 cores, macOS 26.5.2, Go 1.26.4, Node 24.10,
  1600×1000 window — otherwise idle. Read every figure as a lower bound for
  slower hardware.
- The drag is driven with real mouse events over CDP because `startDrag()`
  calls `setPointerCapture`, which refuses a pointer id the browser never
  issued. Chrome coalesces pointer moves to one per frame; here 40 dispatched
  moves produced 40 `pointermove` events every time, which the harness checks
  and reports, but that is not guaranteed on a loaded machine.
- **Not measured:** the split of server time between the SQLite read, the view
  build and the template execution. Separating those means instrumenting the
  handlers, which this spike was not allowed to do. The total is trustworthy;
  the breakdown is not available.

### The fixture

A ten-block train — sine, gain, lag, gain, sum, integrator, transfer, PID,
scope, spectrum — repeated to length, wired 0→1→2→3→4→5→6→7→8 with two
branches per train: a second tap into the variadic Sum, and a spur from the
integrator into the spectrum sink. That gives one signal per block, which is
roughly what a real sheet carries, rather than 400 isolated cards.

Blocks are grid-placed 20 to a row from (60, 80) on a 240×120 pitch, and every
one is created through the real HTTP API, so the domain's grid snap, arity and
acyclicity rules have all been enforced on the fixture the harness measures.

## The numbers

Milliseconds, median with the interquartile band in brackets. Server figures
are 30 samples; swaps, moves and redraws 15 per zoom; page loads 7 per zoom.
In the parameter-edit table the band is shown on the total only; the segment
columns are bare medians.

Produced by `node docs/swap-scaling-bench.mjs --sizes 50,150,400
--server-reps 30 --swap-reps 15 --load-reps 7`. Two earlier runs of the same
command agree with these to within a few percent.

### Server

| blocks | wires | fragment | gzipped | GET workbench | PUT block | PATCH position | floor |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 50 | 50 | 73.8 KB | 7.8 KB | 2.2 (2.1–2.4) | 2.6 (2.5–2.9) | 0.5 (0.4–0.5) | 0.1 (0.1–0.1) |
| 150 | 150 | 174.2 KB | 13.1 KB | 5.0 (4.9–5.4) | 5.7 (5.4–5.9) | 0.5 (0.5–0.5) | 0.1 (0.1–0.1) |
| 400 | 400 | 427.7 KB | 25.6 KB | 12.2 (11.9–12.4) | 12.3 (12.2–12.7) | 0.5 (0.4–0.5) | 0.1 (0.1–0.1) |

### Initial page load

| blocks | zoom | cards on screen | FCP | DOMContentLoaded | load |
| --- | --- | --- | --- | --- | --- |
| 50 | 100% | 15 | 56 (52–56) | 45.0 (44.8–47.5) | 47.6 (47.5–50.1) |
| 50 | 25% | 46 | 52 (52–56) | 44.8 (41.5–47.3) | 47.6 (44.5–50.1) |
| 150 | 100% | 20 | 60 (56–60) | 60.4 (59.9–63.8) | 64.9 (64.4–68.1) |
| 150 | 25% | 136 | 64 (60–64) | 64.3 (63.4–66.8) | 69.2 (68.5–71.4) |
| 400 | 100% | 20 | 76 (76–80) | 138.1 (136.9–139.7) | 146.7 (145.6–148.2) |
| 400 | 25% | 270 | 84 (84–92) | 148.8 (146.1–151.8) | 158.4 (155.6–162.1) |

### Parameter edit — `PUT /blocks/{id}`, swaps the whole fragment

| blocks | zoom | request | swap | afterSwap re-apply | htmx settle wait | settle re-apply | total | to first frame |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 50 | 100% | 4.6 | 5.6 | 6.0 | 22.8 | 4.7 | **43.4** (43.3–45.0) | 49.1 |
| 50 | 25% | 4.4 | 5.5 | 6.0 | 22.8 | 5.0 | **43.6** (43.3–45.0) | 49.2 |
| 150 | 100% | 7.5 | 10.1 | 16.2 | 23.3 | 15.3 | **73.8** (72.7–74.7) | 75.8 |
| 150 | 25% | 8.0 | 10.1 | 15.8 | 23.1 | 14.8 | **73.0** (71.6–75.2) | 74.8 |
| 400 | 100% | 14.6 | 23.4 | 69.2 | 24.8 | 67.9 | **198.2** (197.6–204.5) | 205.6 |
| 400 | 25% | 15.2 | 22.9 | 68.0 | 25.0 | 67.1 | **198.8** (195.8–201.9) | 203.6 |

Full-sample range for the total: 41.9–49.6 at 50, 70.6–79.8 at 150, 191.7–232.8
at 400. That is one 232 ms outlier across the 30 samples taken at 400 blocks
(15 per zoom); everything else is tight.

`htmx settle wait` is htmx's own settle delay, the gap between the swap
finishing and `htmx:afterSettle` firing. It is 22–25 ms at every size, it does
not grow, and it is a timer rather than work. Its purpose is to let CSS
transitions on `.htmx-added` and `.htmx-settling` run — and this application
defines no such rules, in any stylesheet. At 50 blocks it is more than half of
the whole 43 ms edit.

### Block move — `PATCH /blocks/{id}/position`, 204, no swap

| blocks | zoom | round trip | drag frame handler | frames |
| --- | --- | --- | --- | --- |
| 50 | 100% | 1.0 (1.0–1.2) | 1.9 (1.8–2.0) | 40 |
| 50 | 25% | 1.2 (1.1–1.3) | 1.9 (1.8–1.9) | 40 |
| 150 | 100% | 1.0 (0.9–1.1) | 10.0 (9.7–10.5) | 40 |
| 150 | 25% | 1.0 (0.9–1.5) | 9.9 (9.7–10.0) | 40 |
| 400 | 100% | 1.3 (1.3–2.0) | 54.7 (54.1–55.7) | 40 |
| 400 | 25% | 1.2 (1.2–1.7) | 54.3 (53.5–54.8) | 40 |

The response body is **zero bytes at every size**, and the round trip is flat
at ~1 ms. The move endpoint does not scale with the flowsheet at all. What
scales is the drag that precedes it: the per-`pointermove` handler cost, which
is a lower bound on the frame, because it stops when the listener returns and
does not include the style, layout and paint that follow in the same frame.
The `frames` column is a check, not a result: Chrome coalesces pointer moves,
and all 40 dispatched moves survived as separate events in every cell.

### `redrawEdges` alone

| blocks | edge paths | one redraw | paths whose `d` actually changed |
| --- | --- | --- | --- |
| 50 | 100 | 1.9 (1.9–2.0) | **0** |
| 150 | 300 | 9.9 (9.8–10.4) | **0** |
| 400 | 800 | 54.8 (54.2–55.2) | **0** |

### Where the time goes

Self time from the sampling profile, 100 µs, five parameter edits at 400
blocks:

```
read · geometry.js          407 ms
querySelector · native      263 ms
(program) · native          141 ms
getBoundingClientRect       45 ms
parseHTMLUnsafe             19 ms
```

and one 40-frame drag at 400 blocks:

```
querySelector · native     1042 ms
edgePath · geometry.js      888 ms
read · geometry.js          315 ms
setAttribute · native        52 ms
```

## What the numbers say

**The server is not the constraint.** 12.2 ms to produce a 428 KB fragment at
400 blocks, and the cost per block *falls* as the sheet grows (0.044, 0.033,
0.031 ms/block) — it is template execution and byte production, growing
linearly. The `blockByID` linear scan in `newWorkbenchView` is a genuine
O(blocks × connections): 160,000 comparisons at 400×400. It is invisible in
these numbers. Fix it if you like the code better that way; do not fix it for
speed, and do not cite it as a scaling limit.

**The fragment size is not the constraint either — on loopback.** Parsing and
swapping 428 KB costs 23 ms of the 198 ms total. But the fragment is served
**uncompressed**: 427.7 KB on the wire against 25.6 KB gzipped, a factor of
16.7. Over loopback that is free. Over any real network it is the whole
budget, and it is paid again on every keystroke-apply.

**Zoom does not matter.** At 400 blocks, 25% zoom puts 270 cards on screen
against 20 at 100%, and the parameter edit is 198.8 ms against 198.2 ms — the
same number. Page load shows the only real difference — about 10 ms at 400
blocks — which is the extra initial paint. The conclusion is blunt: this is not a rasterisation
problem. Culling or virtualising the canvas by viewport would not have helped
the swap, because the cost is JavaScript walking the DOM, and it walks the
whole DOM whether it is on screen or not.

**The re-apply pass is the constraint, and it is one function.** At 400 blocks
the two re-apply passes cost 69.2 + 67.9 = 137 ms of the 198 ms total. A
single `redrawEdges` costs 54.8 ms, and `main.js` registers it with
`onReapply`, so `reapply.js` runs it on both `htmx:afterSwap` and
`htmx:afterSettle` — 110 ms of the 137 ms. The remaining ~27 ms is
`applySelection` toggling a class on 400 cards plus the viewport and shell
steps.

`redrawEdges` is expensive for three compounding reasons, all inside 47 lines
of `geometry.js`:

1. It calls `geometry()` once **per edge**, and `geometry()` reads five
   `data-` attributes off the canvas and converts each with `Number()`. At 800
   path elements that is 4,000 `dataset` lookups per pass. This is the
   `read · geometry.js` line in the profile, and it is the single largest term
   in the swap.
2. It resolves each edge's endpoints with
   `root.querySelector('[data-block-id="…"]')`, twice per path element. That
   is 1,600 whole-canvas attribute-selector scans per pass, each O(blocks) —
   so the redraw is O(blocks × edges). The measured growth confirms it: 3× the
   blocks costs 5.2× the time, 2.7× more costs 5.5× more.
3. It writes a `d` attribute and then reads `offsetLeft`/`offsetTop` on the
   next iteration, forcing a layout flush per edge. That is what the profiler
   attributes to `edgePath`.

**And after a swap it does nothing at all.** The template already emits a `d`
for every connection, computed by `edgePath` in `view.go` from the same block
positions with the same bend rule — `geometry.js` says as much in its own
comment, that the two curves have to be the same curve. The harness checks it:
**0 of 800 paths change value** when the redraw runs after a swap. It is
110 ms of recomputing what the server just sent.

`redrawEdges` is load-bearing during a drag, where blocks move on the client
with no round trip. It is dead weight in the re-apply pass — and on initial
load too, where `main.js` calls it once against markup the server has just
rendered. That is consistent with what the load column does: DOMContentLoaded
grows 45 → 60 → 138 ms while one redraw grows 1.9 → 9.9 → 54.8 ms, so over half
the growth in page load is the same redundant redraw.

**The drag hits the wall before the swap does.** At 400 blocks a single
`pointermove` costs 54.7 ms of listener time before the browser has laid
anything out — about 18 frames per second, and that is the floor. At 150
blocks it is 10.0 ms, already the main tenant of a 16.7 ms frame. The move
*request* is 1 ms and 0 bytes at every size. The block move being a 204 with
no swap is exactly why it does not appear as a scaling problem: the endpoint
is fine, and the thing that degrades is `redrawEdges` again, called once per
frame from `moveDrag`.

## Recommendation

**The full `#workbench` swap holds to 150 blocks as shipped, and to 400 after
the bounded work in `internal/web/static/js/geometry.js` listed below. Do not
build finer-grained swaps.**

Do not plan to revisit at 1000 either, because a flowsheet cannot reach it.
`openPosition` in `studio.go` places new blocks on a 240×120 lattice inside a
6000×4000 sheet, which is 25 columns by 32 rows — **800 slots**, after which
`AddBlock` answers "the flowsheet is full; move a block to make room". 400 is
half of everything the Add button can produce, and 800 is the number to
re-measure at if the sheet is ever enlarged.

Removing the two redundant redraws takes the 400-block parameter edit from
198 ms to about 90 ms, and dropping htmx's settle delay takes it to about
65 ms — projections from the measured 54.8 ms redraw and 24.8 ms wait, not
measurements. The harness exists so the next task can confirm them.

Out-of-band swapping the inspector, the dock and the tab strip, or patching
block cards in place, would address the 23 ms of parse-and-swap and leave the
137 ms of re-apply exactly where it is. It is the more invasive change and the
smaller prize. That option was measured and rejected on the numbers, not
deferred out of caution.

### The follow-up work, in priority order

1. **Stop calling `redrawEdges` on the server-rendered paths.** Drop it from
   the `onReapply` list in `main.js`, and from the initial-load sequence
   below it; keep the `moveDrag`, `nudgeSelection` and window-resize callers,
   which are the ones that move blocks without a round trip. Before landing
   it, run the harness's `paths whose d actually changed` check on the other
   swap-producing paths — add block, delete, connect, disconnect, run
   simulation, tab switch — not just the parameter edit measured here. Worth
   ~110 ms per swap and ~55 ms of page load at 400 blocks. Largest win,
   smallest diff.
2. **Hoist `geometry()` out of `redrawEdges`'s loop, and index the block
   nodes.** Read the geometry once per call, and build one `Map` from
   `data-block-id` to node instead of two `querySelector` scans per path
   element. This makes the redraw linear in edges instead of O(blocks × edges).
   It is what makes dragging at 400 blocks viable, and unlike item 1 it helps
   the drag, which item 1 does not touch. The redraw is the bulk of the
   54.7 ms drag frame; how much of it survives is for the harness to say, not
   for this document to predict.
3. **Set htmx's settle delay to zero.** No stylesheet defines `.htmx-added`
   or `.htmx-settling` rules, so the 20 ms default buys this application
   nothing and is paid on every mutation. `htmx.config.settleDelay = 0`, or
   `settle:0ms` on the `hx-swap` attributes. Check first that nothing depends
   on the gap: `reapply.js` deliberately runs its steps on both `afterSwap`
   and `afterSettle`, and both still fire — they just stop being 23 ms apart.
4. **Compress the fragment.** 427.7 KB against 25.6 KB gzipped. Free on
   loopback, decisive over a network, and it is one middleware. Relevant now
   that `compose.yaml` deploys this.
5. **Only then** re-run `docs/swap-scaling-bench.mjs` at 400 and 640 blocks
   and decide whether anything finer-grained is warranted. The harness prints
   the same tables, so the comparison is direct. 640 is as far as its layout
   reaches, and the sheet's own 800-slot ceiling above is why there is little
   point going further.

## What dependent tasks must know

**Block-count budget.** Design for **400 blocks and 400 signals** on one
flowsheet. As the code stands today, editing is comfortable to 150 and merely
usable at 400 (198 ms per mutation); dragging is comfortable to 150 and poor at
400 (18 fps). Items 1 and 2 above are what buy the full 400. The sheet itself
stops at 800 auto-placed blocks, so 400 is half the reachable maximum, not an
arbitrary line. Nothing has been measured above 400 — if you need 800, measure
it rather than extrapolating, because both the redraw and the drag grow
faster than linearly.

**Swap strategy: keep the full `#workbench` swap.** It is not the cost. A
mutation ships 428 KB and 12 ms of server work at 400 blocks, and parses and
swaps in 23 ms. Splitting that into out-of-band regions buys back a fifth of
the total and costs the architecture its single simplest property.

**The rule that replaces it.** The constraint is not the swap, it is the
re-apply pass:

> Every step registered with `onReapply` runs **twice** on every mutation in
> the application. A step that queries the DOM once per block or once per edge
> is quadratic against the sheet, and it will dominate every edit the user
> makes. Re-apply steps must be O(1), or linear with the work hoisted out of
> the loop.

That is the sentence to carry into `docs/workbench-ergonomics.md` alongside
the existing re-apply contract, and it is the thing to check in review when a
new `onReapply` step is proposed.

**Do not derive the budget from what is on screen.** Zoom changes the visible
card count by a factor of thirteen and changes the swap cost by nothing. The
cost is proportional to the DOM, not to the viewport, so viewport culling is
not a lever here.
