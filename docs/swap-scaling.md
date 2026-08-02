# Workbench swap scaling

What the full-`#workbench` swap actually costs as a flowsheet grows, measured
rather than guessed. This began as the decision document that set the
block-count budget; it now records the baseline, each benchmark-driven
milestone, and the final release evidence.

## 2026-08-02 performance-refactor baseline

The orthogonal-routing work changed the architecture after the original study:
the server now emits empty SVG `d` attributes and
`internal/web/static/js/orthogonal-routing.js` is the route authority. The old
recommendation below to remove every post-swap redraw is therefore obsolete.
One client route pass is required after initial render, a full swap, and history
restore. The second pass was measured and is redundant.

The benchmark fixture had also drifted behind the port-aware Sum contract. It
now creates three Sum input ports, wires its two fixture signals to distinct
ports, and leaves the third for the connect/disconnect interaction gate.

Focused current-main runs used:

```text
node docs/swap-scaling-bench.mjs --sizes 50 --server-reps 5 --swap-reps 1 --load-reps 1
node docs/swap-scaling-bench.mjs --sizes 150 --server-reps 2 --swap-reps 1 --load-reps 1 --skip-profile --skip-redundancy
node docs/swap-scaling-bench.mjs --sizes 400 --server-reps 5 --swap-reps 1 --load-reps 1 --skip-profile --skip-redundancy
```

These are focused reproduction runs rather than final statistical samples, but
the failure is several orders of magnitude larger than their sampling noise:

| blocks | fragment | server PUT | page load | route pass | parameter edit | longest task |
| --- | --- | --- | --- | --- | --- | --- |
| 50 | 118.7 KB / 10.3 KB gzip | 5.0 ms | 111–131 ms | 30–37 ms | 112–114 ms | below 50 ms |
| 150 | 268.9 KB / 15.6 KB gzip | 10.8 ms | 571–600 ms | 229–289 ms | 576–587 ms | 298–307 ms |
| 400 | 645.8 KB / 28.6 KB gzip | 25.8 ms | 3.94–4.03 s | 3.35–3.47 s | 6.86–7.00 s | not separately sampled |

This reproduces the reported unresponsive page in normal, unthrottled headless
Chrome. At 400 blocks, one parameter edit occupies the browser for about seven
seconds even though the complete server request takes about 26 ms. At 150
blocks, both parameter edits and drags already generate roughly 200–300 ms long
tasks.

The profile identifies `buildVisibilityGraph` and `segmentCrossesRect` as the
dominant functions. The router builds a Cartesian product of every obstacle
edge coordinate separately for every signal. The full swap then invokes that
work after both swap lifecycle events.

### Accepted budgets for this refactor

On the same unthrottled host and benchmark fixture:

- 400-block parameter edit median to first frame: at most 150 ms, with a
  stretch target of 100 ms;
- 400-block initial load: at most 500 ms;
- 400-block authoritative full route pass: at most 150 ms;
- 400-block drag: no routing long task above 50 ms and no dropped live drag
  sequence in the harness;
- 400-block dynamic HTML transfer with compression negotiated: at most 35 KiB;
- every normal route-authority gate row stays unchanged and the negative
  control remains non-zero.

A 4× CPU-slowdown run is the constrained-device gate after the normal profile
meets these budgets. It must remain operable, with a parameter edit under
500 ms and no single routing task above 250 ms.

The refactor will first reduce the lifecycle to one required route application,
then replace the per-edge Cartesian visibility-graph hot path with a
deterministic fast path and bounded fallback, and add negotiated compression.
Partial workbench swaps and a JavaScript/TypeScript asset pipeline remain
unjustified unless the post-fix profile still misses these budgets.

### Milestone 1: one swap pass and reusable routes

The first refactor removes the discarded `htmx:afterSwap` re-apply, sets
HTMX's unused settle delay to zero, indexes block and edge DOM nodes once per
redraw, and carries computed routes across whole-workbench swaps. Reuse is
allowed only when signatures of the flow ID, sheet geometry, every block
rectangle, and every connection endpoint match and every live edge has a
cached route.

The route-authority gate now checks the resulting architecture directly:
every server path is empty after `afterSwap`, every path is populated after
the single `afterSettle` pass, and moving a connected source in the negative
control must invalidate and change its two SVG paths.

Focused validation used:

```text
node docs/swap-scaling-bench.mjs --sizes 50 --server-reps 5 --swap-reps 2 --load-reps 2 --skip-profile
node docs/swap-scaling-bench.mjs --sizes 400 --server-reps 3 --swap-reps 3 --load-reps 1 --skip-profile
```

At 50 blocks, parameter edits fell from 112–114 ms to 31–33 ms. At 400
blocks, the edit median fell from 6.86–7.00 seconds to 108–112 ms total and
119–125 ms to the next frame, meeting the 150 ms acceptance budget. The
unchanged-route redraw is now about 6 ms.

Initial 400-block load remains 3.71–3.82 seconds, and the full redraw forced
at drag release still produced 1.5–2.3 second long tasks. Both results isolate
the next milestone: replace the Cartesian visibility graph. They also show
why caching alone is not a complete responsiveness fix.

### Milestone 2: bounded Manhattan routing

The Cartesian visibility graph and A* search have been removed. The router now
tries a fixed set of direct Manhattan lanes, consults at most 12 ranked
obstacle-boundary lanes only when the direct set is blocked, and uses spatial
indexes for both block collisions and existing-route crossing/overlap costs.
The indexes are built once per redraw rather than once per edge.

Live drag still reroutes only connected or obstructed paths in one animation
frame. The final authoritative build is split into 8 ms animation-frame chunks,
is canceled if newer geometry arrives, and emits
`processlab:routesSettled` when complete. The benchmark waits for that event,
so its drag result includes all final routing work rather than stopping early.

The full validation run used:

```text
node docs/swap-scaling-bench.mjs --sizes 50,150,400 --server-reps 5 --swap-reps 3 --load-reps 2 --skip-redundancy
node docs/swap-scaling-bench.mjs --sizes 150 --server-reps 2 --swap-reps 2 --load-reps 1 --cpu-slowdown 4 --skip-profile --skip-redundancy
```

| blocks | load | parameter edit | to frame | forced route pass | drag handler | drag long task |
| --- | --- | --- | --- | --- | --- | --- |
| 50 | 85–102 ms | 31–34 ms | 34–38 ms | 1–6 ms | 0.3–0.5 ms | none |
| 150 | 110–113 ms | 55–56 ms | 62–64 ms | 3–14 ms | 0.5–0.7 ms | none |
| 400 | 187–193 ms | 111–126 ms | 124–150 ms | 46–52 ms | 1.0–1.2 ms | none |

At 4× CPU slowdown, the prescribed 150-block edit is 215–238 ms and its
drag completes with every one of 40 live moves and no long task. A stricter
single-sample 400-block run remained below the 500 ms edit budget at
428–496 ms; its route build was about 250 ms before the final 128 px index
tuning.

Persistent tests cover deterministic forward and feedback paths, obstacle
clearance, blocked port stubs, occupancy choice, bounds, multichannel target
centers, dense layouts, indexed-versus-exhaustive parity, cache invalidation,
and cancelable frame-bounded completion.

### Milestone 3: negotiated delivery

Large HTML, CSS, JavaScript, JSON, and SVG responses now negotiate gzip.
Responses below 1 KiB, HEAD and bodyless statuses, byte ranges, binary media,
and already encoded bodies bypass compression without changing their status,
length, or body semantics. Dynamic responses are `private, no-store` and vary
on both `HX-Request` and `Accept-Encoding`; mutable application assets
revalidate, while the versioned HTMX asset alone receives a one-year
`immutable` policy.

The 400-block acceptance run used:

```text
node docs/swap-scaling-bench.mjs --sizes 400 --server-reps 5 --swap-reps 1 --load-reps 1 --skip-profile --skip-redundancy
```

The actual negotiated workbench transfer is **28.7 KiB gzip**, below the
35 KiB budget and 22.5× smaller than the 645.8 KiB identity body. Compression
adds about 2–3 ms of loopback server work: GET is 26.9–27.5 ms and PUT is
28.2–28.6 ms. End-to-end edits remain 120–123 ms, load remains 195–203 ms,
and completed drag routing still produces no long task.

Protocol tests cover explicit and wildcard quality values, identity preference,
body decompression integrity, content length, HEAD, 204, 206, pre-encoded,
small and incompressible responses, `Vary` composition, HTMX representation
separation, and cache policy. The package also passes its race suite.

### Final scale and browser verification

The release matrix runs the same application at 50, 150, 400, and 640 blocks,
both supported zoom levels, a sampling profile, every swap-producing
interaction, and the cache-invalidation falsification row:

```text
node docs/swap-scaling-bench.mjs --sizes 50,150,400,640 --server-reps 5 --swap-reps 3 --load-reps 2
node docs/swap-scaling-bench.mjs --sizes 400 --server-reps 1 --swap-reps 2 --load-reps 1 --cpu-slowdown 4 --skip-profile --skip-redundancy
```

At the 400-block design target:

| measure | baseline | final | improvement |
| --- | --- | --- | --- |
| initial load | 3.94–4.03 s | 198–204 ms | about 20× |
| parameter edit | 6.86–7.00 s | 118–126 ms | about 55× |
| authoritative route pass | 3.35–3.47 s | 50–54 ms | about 65× |
| live drag handler | 200–300 ms tasks | 1.0–1.3 ms | frame-safe |
| workbench transfer | 645.8 KiB identity | 28.7 KiB gzip | 22.5× smaller |

Every route-authority row at every size has zero populated paths after the raw
swap, all paths populated after the client pass, and a non-zero negative
control. Reload restored all routes, HTMX tab navigation reached a sibling
flow, Back restored the original flow and its routes, the project register
rendered independently, and a real 390×844 Chrome viewport had zero document
overflow.

At 4× CPU slowdown, removing layout-forcing `offsetLeft`/`offsetTop` reads from
route reconstruction brought the 400-block edit back inside the constrained
budget: 431–461 ms total at both zooms. All 40 drag moves remain live; final
routing is frame-chunked. The next frame arrives at 509–561 ms because the
full 400-block DOM replacement still creates a 394–426 ms browser task on this
artificially constrained profile.

The measured stretch case is 640 blocks. Load remains 284–305 ms and the live
drag handler 1.5–1.8 ms with no drag-routing long task, but edits rise to
171–230 ms, their browser task to 124–136 ms, forced full routing to about
102 ms, and transfer to 40.2 KiB gzip. These exceed the 400-block budgets and
define the remaining limit.

## 2026-08-02 bounded-edit follow-up

The `fast-web` deferred-strategy benchmark at commit `fae7790` resolves the
McMaster-Carr-inspired delivery ideas with controlled Chrome evidence. It keeps
bounded intent prefetch for predictable public navigation, and rejects critical
CSS, a redundant LCP-image preload, and a service-worker performance cache. The
positive result transfers as a design principle rather than as prefetch code:
keep the stable HTMX shell and update only the response regions the interaction
can invalidate. Prefetch cannot safely predict an editor mutation, and the
three rejected delivery layers do not reduce DOM replacement work.

A fresh control run used:

```text
node docs/swap-scaling-bench.mjs --sizes 400,640 --server-reps 3 --swap-reps 3 --load-reps 1 --skip-profile --skip-redundancy --expected-edit-swap full
```

| blocks | zoom | request | swap | re-apply total | edit total | to frame | longest task |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 400 | 100% | 30.7 ms | 38.4 ms | 44.8 ms | 120.6 ms | 132.7 ms | 92 ms |
| 400 | 25% | 32.1 ms | 34.8 ms | 43.5 ms | 117.2 ms | 130.6 ms | 87 ms |
| 640 | 100% | 47.5 ms | 47.2 ms | 62.9 ms | 165.9 ms | 189.8 ms | 121 ms |
| 640 | 25% | 51.7 ms | 47.3 ms | 62.9 ms | 168.9 ms | 193.2 ms | 119 ms |

The candidate is a bounded successful parameter edit. It must keep
`#workbench` alive while replacing the authoritative selected card,
`#inspector-rail`, `#simulation-results`, `#flow-tabs`, and the saved project
facts. Those regions cover the block's name, summary and port schema; activity;
simulation and analysis freshness; the tab's needs-run state; and the saved
timestamp. Validation errors retain the existing full-workbench response so
the error banner is not lost.

The harness now fails unless `--expected-edit-swap full` observes a replaced
workbench or `--expected-edit-swap bounded` observes the inverse plus all five
required bounded replacements. The candidate is retained only if its median
improves by at least 10%, p95 does not materially regress, and normal 400/640
plus 400-block 4× CPU correctness gates pass.

The result also matches the fast-web research principles used for review:
useful server-rendered HTML, a stable HTMX shell, pinned local dependencies,
explicit page modules, representation-aware private caching, immutable
versioned assets, and cold/warm/constrained measurement. Prefetch, service
workers, critical CSS, a bundler, and a TypeScript migration remain deferred
because this profile provides no evidence that they would improve the current
limits.

Read this before proposing out-of-band swaps, per-card patching, canvas
virtualisation, or any other change whose motivation is "the full swap must be
too slow by now". It probably is not the thing that is slow.

The harness is `docs/swap-scaling-bench.mjs`. It builds its own binary, its own
scratch database and its own Chrome profile, and removes all three on exit:

```
node docs/swap-scaling-bench.mjs --sizes 50,150,400 --out results.json
```

## Original study: the short version

At 400 blocks and 400 signals, a parameter edit — the mutation that re-renders
and swaps the entire workbench — takes **205 ms end to end**. The server
spends 12 ms of that and the wire carries 428 KB; parsing and swapping the
fragment costs 24 ms; htmx waits a further 24 ms on a settle timer. The
remaining **141 ms is the client re-applying its own state**, and roughly
118 ms of that is one function: `redrawEdges` in
`internal/web/static/js/geometry.js`, run twice per swap, rewriting 800 SVG
path attributes to curves the server had already sent. Compared against the
response body itself, **none of the 800 curves differ** — on any of the nine
interactions in this application that swap the workbench.

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
and at 50 blocks the whole dispatch is 1.8 ms, which bounds it.

**The redundancy gate.** Whether the redraw *changes* anything cannot be asked
of the DOM, because by the time any probe can look, the redraw has already
been over it — a before-and-after around a pure function of an unchanged DOM
returns zero by construction, whatever the server sent. So the comparison is
against the server's own bytes: a capture-phase `htmx:beforeSwap` listener
reads `detail.serverResponse`, the untouched response text, parses out its `d`
attributes, and compares them with the settled DOM after the re-apply.

Two guards keep that honest. The **raw string** comparison is a control that
must come out 100% different, since the server prints `232.0` where the client
prints `232`; if it ever reads 0%, the probe is comparing the DOM with itself
again and every numeric zero beneath it is meaningless. And a **negative
control** displaces a card between swap and re-apply, so the server's curve
really is stale, and must report a non-zero count. Both are columns in the
table, not assertions in this document.

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
| 50 | 50 | 73.8 KB | 7.8 KB | 2.2 (2.1–2.6) | 2.9 (2.7–3.2) | 0.5 (0.4–0.5) | 0.2 (0.1–0.2) |
| 150 | 150 | 174.2 KB | 13.1 KB | 5.2 (4.9–5.4) | 5.7 (5.5–5.9) | 0.5 (0.5–0.5) | 0.1 (0.1–0.1) |
| 400 | 400 | 427.7 KB | 25.6 KB | 12.1 (11.7–12.6) | 13.3 (12.8–13.8) | 0.6 (0.5–0.7) | 0.2 (0.1–0.2) |

### Initial page load

| blocks | zoom | cards on screen | FCP | DOMContentLoaded | load |
| --- | --- | --- | --- | --- | --- |
| 50 | 100% | 15 | 56 (56–60) | 47.7 (47.7–48.9) | 50.4 (50.3–51.8) |
| 50 | 25% | 46 | 56 (52–60) | 44.6 (43.7–47.5) | 47.4 (46.7–50.3) |
| 150 | 100% | 20 | 60 (60–60) | 63.1 (62.0–63.9) | 67.3 (66.5–68.3) |
| 150 | 25% | 136 | 60 (56–64) | 62.9 (62.6–66.9) | 67.7 (67.2–71.8) |
| 400 | 100% | 20 | 80 (80–80) | 138.9 (137.0–139.8) | 148.0 (145.9–148.9) |
| 400 | 25% | 270 | 84 (84–88) | 148.9 (148.5–152.8) | 159.1 (158.5–163.2) |

### Parameter edit — `PUT /blocks/{id}`, swaps the whole fragment

| blocks | zoom | request | swap | afterSwap re-apply | htmx settle wait | settle re-apply | total | to first frame |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 50 | 100% | 4.4 | 5.5 | 6.1 | 22.9 | 4.9 | **43.8** (43.3–44.7) | 49.3 |
| 50 | 25% | 4.3 | 5.5 | 6.1 | 22.8 | 4.9 | **44.0** (43.4–44.6) | 49.1 |
| 150 | 100% | 8.1 | 10.7 | 17.0 | 23.5 | 15.6 | **75.1** (72.6–76.5) | 76.8 |
| 150 | 25% | 8.2 | 10.5 | 16.9 | 22.7 | 15.4 | **74.7** (73.1–76.9) | 76.5 |
| 400 | 100% | 16.0 | 24.0 | 71.7 | 24.3 | 69.7 | **205.4** (201.6–211.3) | 208.9 |
| 400 | 25% | 16.7 | 23.4 | 69.0 | 24.9 | 68.4 | **202.4** (201.4–205.5) | 207.5 |

Full-sample range of the total, 30 samples per size (15 per zoom): 42.2–48.0
at 50, 70.4–82.5 at 150, 192.1–232.7 at 400. The tail at each size is a single
outlier; the interquartile bands above are what the interaction feels like.

`htmx settle wait` is htmx's own settle delay, the gap between the swap
finishing and `htmx:afterSettle` firing. It is 22–25 ms at every size, it does
not grow, and it is a timer rather than work. Its purpose is to let CSS
transitions on `.htmx-added` and `.htmx-settling` run — and this application
defines no such rules, in any stylesheet. At 50 blocks it is more than half of
the whole 43 ms edit.

### Block move — `PATCH /blocks/{id}/position`, 204, no swap

| blocks | zoom | round trip | drag frame handler | frames |
| --- | --- | --- | --- | --- |
| 50 | 100% | 1.3 (1.3–1.5) | 1.9 (1.9–2.0) | 40 |
| 50 | 25% | 1.4 (1.3–1.7) | 2.1 (2.0–2.2) | 40 |
| 150 | 100% | 1.7 (1.6–2.1) | 9.8 (9.4–10.2) | 40 |
| 150 | 25% | 1.6 (1.4–1.9) | 10.1 (9.9–10.5) | 40 |
| 400 | 100% | 2.2 (2.0–3.5) | 58.3 (57.5–59.0) | 40 |
| 400 | 25% | 2.6 (2.1–3.4) | 56.0 (55.6–56.5) | 40 |

The response body is **zero bytes at every size**, and the server's share of
the round trip is the flat 0.5–0.6 ms in the server table. The column drifts
from 1.3 to 2.2 ms only because it reproduces what `savePositions` does —
write `style.left`, then read `offsetLeft` back — and that read forces a
layout over the whole sheet. The move endpoint does not scale at all. What
scales is the drag that precedes it: the per-`pointermove` handler cost, which
is a lower bound on the frame, because it stops when the listener returns and
does not include the style, layout and paint that follow in the same frame.
The `frames` column is a check, not a result: Chrome coalesces pointer moves,
and all 40 dispatched moves survived as separate events in every cell.

### `redrawEdges` alone

| blocks | edge paths | one redraw |
| --- | --- | --- |
| 50 | 100 | 1.8 (1.8–1.9) |
| 150 | 300 | 10.2 (10.1–10.4) |
| 400 | 800 | 58.8 (58.7–59.3) |

This is cost only. Whether the redraw *changes* anything cannot be asked here:
by the time this probe runs, `redrawEdges` has already been over this DOM, so
comparing before with after would compare client output with client output and
could only ever return zero. That question needs the server's own bytes.

### Redundancy gate — response body against the settled DOM

For every interaction that swaps `#workbench`, the `d` attributes are read out
of `htmx:beforeSwap`'s `detail.serverResponse` — the untouched response text,
before any client code has seen it — and compared with what is on the page
once the re-apply pass has finished.

`raw` is the control. The server formats coordinates with `%.1f` and the
client emits bare numbers, so a probe that genuinely read the server's markup
must report 100%; a probe comparing the DOM with itself reports 0%. `numeric`
is the result. `negativeControl` displaces a card between the swap and the
re-apply — precisely the case where the redraw *is* load-bearing — and must be
non-zero, or the rest of the table means nothing.

| interaction | paths (50 / 150 / 400) | raw differs | numeric differs | max delta |
| --- | --- | --- | --- | --- |
| parameterEdit | 100 / 300 / 800 | 100% | **0** | 0 |
| selectBlock | 100 / 300 / 800 | 100% | **0** | 0 |
| addBlock | 100 / 300 / 800 | 100% | **0** | 0 |
| connect | 102 / 302 / 802 | 100% | **0** | 0 |
| disconnect | 100 / 300 / 800 | 100% | **0** | 0 |
| disconnectBlock | 98 / 298 / 798 | 100% | **0** | 0 |
| deleteBlock | 98 / 298 / 798 | 100% | **0** | 0 |
| runSimulation | 98 / 298 / 798 | 100% | **0** | 0 |
| tabSwitch | 100 / 300 / 800 | 100% | **0** | 0 |
| negativeControl | 100 / 300 / 800 | 100% | **2** | 150 |

One path, as the server sent it and as the client left it:

```
server: M 232.0 122.0 C 291.4 122.0, 40.6 122.0, 100.0 122.0
client: M 232 122 C 291.4 122, 40.6 122, 100 122
```

### Where the time goes

Self time from the sampling profile, 100 µs, five parameter edits at 400
blocks:

```
read · geometry.js          386 ms
querySelector · native      262 ms
(program) · native          124 ms
getBoundingClientRect       46 ms
parseHTMLUnsafe             19 ms
```

and one 40-frame drag at 400 blocks:

```
querySelector · native     1039 ms
edgePath · geometry.js      842 ms
read · geometry.js          332 ms
setAttribute · native        40 ms
```

## What the numbers say

**The server is not the constraint.** 12.1 ms to produce a 428 KB fragment at
400 blocks — measured from a loopback client, not from inside Chrome, so it
sits beside the browser figures as a magnitude rather than a summand — and the
cost per block *falls* as the sheet grows (0.044, 0.035, 0.030 ms/block). It
is template execution and byte production, growing linearly. The `blockByID`
linear scan in `newWorkbenchView` is a genuine O(blocks × connections):
160,000 comparisons at 400×400. It is invisible in
these numbers. Fix it if you like the code better that way; do not fix it for
speed, and do not cite it as a scaling limit.

**The fragment size is not the constraint either — on loopback.** Parsing and
swapping 428 KB costs 24 ms of the 205 ms total. But the fragment is served
**uncompressed**: 427.7 KB on the wire against 25.6 KB gzipped, a factor of
16.7. Over loopback that is free. Over any real network it is the whole
budget, and it is paid again on every keystroke-apply.

**Zoom does not matter.** At 400 blocks, 25% zoom puts 270 cards on screen
against 20 at 100%, and the parameter edit is 202.4 ms against 205.4 ms — the
same number. Page load shows the only real difference — about 10 ms at 400
blocks — which is the extra initial paint. The conclusion is blunt: this is
not a rasterisation problem. Culling or virtualising the canvas by viewport
would not have helped the swap, because the cost is JavaScript walking the
DOM, and it walks the whole DOM whether it is on screen or not.

**The re-apply pass is the constraint, and it is one function.** At 400 blocks
the two re-apply passes cost 71.7 + 69.7 = 141 ms of the 205 ms total. A
single `redrawEdges` costs 58.8 ms, and `main.js` registers it with
`onReapply`, so `reapply.js` runs it on both `htmx:afterSwap` and
`htmx:afterSettle` — about 118 ms of the 141 ms, leaving ~23 ms for
`applySelection` toggling a class on 400 cards plus the viewport and shell
steps.

Read that last split as an estimate, not a measurement. The 58.8 ms was taken
on a settled, already-laid-out DOM through the resize binding; inside the
re-apply pass the same function runs against markup the swap has just parsed,
where its first `offsetLeft` read forces a layout the browser owed anyway. The
direction is not in doubt — the sampling profile independently puts ~134 ms
per edit inside `geometry.js` and the `querySelector` calls it makes, against
a measured 141 ms of re-apply — but the boundary between "redraw" and "layout
the swap already owed" is not resolved by these numbers.

`redrawEdges` is expensive for three compounding reasons, all inside 47 lines
of `geometry.js`:

1. It calls `geometry()` once **per edge**, and `geometry()` reads five
   `data-` attributes off the canvas and converts each with `Number()`. At 800
   path elements that is 4,000 `dataset` lookups per pass. This is the
   `read · geometry.js` line in the profile, and it is the single largest term
   in the swap.
2. It resolves each edge's endpoints with
   `root.querySelector('[data-block-id="…"]')`, twice per path element, and
   `geometry()` calls `canvas()` for a third. That is 2,400 selector queries
   per pass at 800 path elements, of which 1,600 are whole-canvas
   attribute-selector scans, each O(blocks) — so the redraw is
   O(blocks × edges). The measured growth confirms it: 3× the blocks costs
   5.7× the time, 2.7× more costs 5.8× more.
3. It writes a `d` attribute and then reads `offsetLeft`/`offsetTop` on the
   next iteration, forcing a layout flush per edge. That is what the profiler
   attributes to `edgePath`. None of those 800 writes is elided, either: the
   string the client computes never equals the one the server sent, because
   the server prints `232.0` where the client prints `232`. The curve is
   identical and the attribute value is not, so the browser does the full
   invalidation every time.

**And after a swap it does nothing at all.** The template already emits a `d`
for every connection, computed by `edgePath` in `view.go` from the same block
positions with the same bend rule — `geometry.js` says as much in its own
comment, that the two curves have to be the same curve. The gate tests that
against the server's own response body, across all nine swap-producing
interactions at all three sizes: **every path is rewritten — raw differs 100%,
because the server writes `232.0` where the client writes `232` — and not one
curve moves.**

It holds because `offsetLeft` and the stored `Position.X` are the same number.
`workbench.html` writes the position inline on an absolutely positioned card
inside a zero-offset `.sheet`, and every stored position is an integer snapped
to the 20 px grid, so there is no rounding for the redraw to correct. It is
~118 ms per edit spent reformatting what the server just sent.

`redrawEdges` is load-bearing during a drag, where blocks move on the client
with no round trip. It is dead weight in the re-apply pass — and on initial
load too, where `main.js` calls it once against markup the server has just
rendered. That is consistent with what the load column does: DOMContentLoaded
grows 48 → 63 → 139 ms while one redraw grows 1.8 → 10.2 → 58.8 ms, so over half
the growth in page load is the same redundant redraw.

**The drag hits the wall before the swap does.** At 400 blocks a single
`pointermove` costs 58.3 ms of listener time before the browser has laid
anything out — about 17 frames per second, and that is the floor. At 150
blocks it is 9.8 ms, already the main tenant of a 16.7 ms frame. The move
*request* is 0.5 ms of server time and 0 bytes at every size. The block move
being a 204 with no swap is exactly why it does not appear as a scaling
problem: the endpoint is fine, and the thing that degrades is `redrawEdges`
again, called once per frame from `moveDrag`.

## Recommendation

**The full `#workbench` swap holds to 150 blocks as shipped, and to 400 after
the bounded work in `internal/web/static/js/geometry.js` listed below. Do not
build finer-grained swaps.**

Two ceilings bound how far this has to go, and they are different numbers.
`openPosition` in `studio.go` walks a 240×120 lattice inside the 6000×4000
sheet — 25 columns by 32 rows, **800 slots** — and once they are taken
`AddBlock` answers "the flowsheet is full; move a block to make room". That is
the ceiling on *auto-placement*, which is what the Add button and the palette
use, and 400 is half of it.

It is not a ceiling on the sheet. `openPosition` returns a caller's `desired`
position untouched when it is free, and `addBlock` passes posted coordinates
straight through, so explicit placement is bounded only by the 172×84 overlap
test and the 20 px grid: about 33 × 40 = **1320 blocks**. This harness relies
on that path itself. A future importer or duplicate-region feature could
therefore put well past 800 blocks on one sheet, and nothing measured here
speaks to that.

Removing the two redundant redraws takes the 400-block parameter edit from
205 ms to about 90 ms, and dropping htmx's settle delay takes it to about
65 ms — projections from the measured 58.8 ms redraw and 24.3 ms wait, not
measurements, and subject to the layout caveat above. The harness exists so
the next task can confirm them.

Out-of-band swapping the inspector, the dock and the tab strip, or patching
block cards in place, would address the 24 ms of parse-and-swap and leave the
141 ms of re-apply exactly where it is. It is the more invasive change and the
smaller prize. That option was measured and rejected on the numbers, not
deferred out of caution.

### The follow-up work, in priority order

1. **Stop calling `redrawEdges` on the server-rendered paths.** Drop it from
   the `onReapply` list in `main.js`, and from the initial-load sequence
   below it; keep the `moveDrag`, `nudgeSelection` and window-resize callers,
   which are the ones that move blocks without a round trip. Worth ~118 ms
   per swap and ~59 ms of page load at 400 blocks. Largest win, smallest diff.

   **This is safe only while every swap re-renders the whole `#workbench`
   fragment**, because that is what guarantees the SVG layer and the block
   cards always arrive from the same render and agree. Write that invariant
   down where the call used to be. If item 5 ever introduces a partial or
   out-of-band swap that replaces cards without the connections layer, or the
   layer without the cards, the dropped redraw becomes silently wrong and the
   two changes must not be adopted independently.

   Re-run the gate before landing:
   `node docs/swap-scaling-bench.mjs --sizes 50 --swap-reps 2 --load-reps 2`.
   Its `numeric differs` column must stay 0 for all nine interactions, its
   `raw differs` column must stay 100% — a 0 there means the probe stopped
   reading the server's bytes and the zeros below it are meaningless — and
   `negativeControl` must stay non-zero.
2. **Hoist `geometry()` out of `redrawEdges`'s loop, and index the block
   nodes.** Read the geometry once per call, and build one `Map` from
   `data-block-id` to node instead of two `querySelector` scans per path
   element. This makes the redraw linear in edges instead of O(blocks × edges).
   It is what makes dragging at 400 blocks viable, and unlike item 1 it helps
   the drag, which item 1 does not touch. The redraw is the bulk of the
   58.3 ms drag frame; how much of it survives is for the harness to say, not
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
   the same tables, so the comparison is direct; 640 is as far as its layout
   reaches.

   **If that decision is to swap anything less than the whole fragment, item 1
   has to be revisited in the same change.** Dropping the redraw traded on
   every swap delivering cards and wires together. A partial swap breaks that
   trade, and the redraw has to come back — indexed and hoisted per item 2, so
   it is affordable — for whatever the partial swap leaves stale.

## What dependent tasks must know

**Block-count budget.** Design for **400 blocks and 400 signals** on one
flowsheet. As the code stands today, editing is comfortable to 150 and merely
usable at 400 (205 ms); dragging is comfortable to 150 and poor at 400
(17 fps). Items 1 and 2 above are what buy the full 400.

Read "205 ms per mutation" wider than it sounds. `selectBlock` in
`selection.js` answers a click on a block with `GET /flows/{id}/workbench` into
the same full swap, so **clicking a block to inspect it costs the same 205 ms
as editing it** — the gate table lists it as one of the nine, and it is by far
the most frequent of them. The budget is set by the cheapest thing a user
does, not the rarest, which makes the case for item 1 stronger rather than
weaker.

Auto-placement stops at 800 blocks, so 400 is half of what the Add button can
reach; explicit placement fits about 1320. Nothing has been measured above
400 — measure rather than extrapolate, because both the redraw and the drag
grow faster than linearly.

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
