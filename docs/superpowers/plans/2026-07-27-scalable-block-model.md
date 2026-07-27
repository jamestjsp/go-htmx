# Scalable block model — Process Lab

Source of truth for the task graph is `.ergo/plans.jsonl`; this file is the
flattened, dependency-ordered rendering of it used to brief implementers.
Each task below names its ergo ID. Close the ergo task when its work lands.

## Global Constraints

- Go 1.26, module `github.com/jamestjsp/go-htmx`. Stack is Go + HTMX + SQLite,
  no frontend framework, no bundler, no TypeScript. Templates and static
  assets are `go:embed`-ed; a JS/CSS change needs `go build` before the
  server serves it.
- Validation gates for every task that touches Go:
  `gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab`
- Existing test files must keep passing. Where a task says "passes unmodified",
  do not edit that test file to make it pass.
- Databases written by earlier versions must still open. Migrations run at
  startup in `internal/studio/store.go`, are idempotent, and never lose user data.
- Domain refusals are explicit `*studio.ValidationError` messages in the
  existing voice; message text is asserted by tests and shown in the UI.
- One authority per decision: never duplicate a format, default, ordering rule,
  or validation across modules.
- The seeded reactor model's steady state is 1.59 and is asserted by tests;
  no refactor may change simulation numerics.
- Browser verification, where a task requires it: launch the built binary on a
  scratch database and a non-default port, start Chrome with
  `--headless=new --remote-debugging-port=<port>`, and drive CDP from a Node
  script using node 24's global `WebSocket` and `fetch` (no npm installs —
  puppeteer and playwright are NOT available). Report honestly if a check
  could not be run; never claim a verification you did not perform.

## Task 1: Centralize model-edit bookkeeping in Studio

_ergo: MODHX4_


## Goal
- State the rule "a model edit touches model_updated_at, a layout edit does not, every edit logs an event and stamps updated_at" once, instead of ten times.

## Context
- Every mutation in internal/studio/studio.go repeats the same coda: format now, UPDATE flows SET updated_at (plus model_updated_at for model edits), insertEvent, re-snapshot — AddBlock, UpdateBlock, DeleteBlock(s), DuplicateBlocks, Connect, Disconnect(s), MoveBlock(s). Which operations count as model edits is the invariant behind the amber staleness dot, and it currently lives in the copies.
- Shape: an unexported helper (for example s.mutateFlow(ctx, flowID, modelEdit bool, event string, fn)) or two flavors (modelEdit / layoutEdit) if that reads better — MoveBlocks stamps updated_at only and logs no event, and the helper must express that without a flag explosion. Pick the smallest shape that removes the copies while each operation keeps owning its SQL.

## Acceptance Criteria
- The updated_at / model_updated_at / event coda appears once per flavor; every mutation goes through it.
- studio_test.go and lifecycle_test.go pass unmodified; staleness-dot behavior is untouched (register_test.go).

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab


## Task 2: Retire the legacy scalar block columns

_ergo: ZONM7S_



## Goal
- Make `parameters_json` the only stored parameter representation, ending the era where every block insert and scan carries `amplitude`, `gain`, `time_constant` plus a JSON fallback.

## Context
- Add a startup migration in internal/studio/store.go that backfills parameters_json for rows where it is '' using the legacy scalars (the mapping is decodeParameters' switch, store.go:605-622). Afterward drop: decodeParameters' legacy branch, the three extra bind parameters in AddBlock (studio.go:65), DuplicateBlocks (studio.go:317), and seed (store.go:395), and the legacy scan columns (store.go:471, store.go:576).
- Keep the physical columns for old databases (SQLite column drops need table rebuilds for no upside); simply stop reading and writing them. New databases may omit them from CREATE TABLE — both a fresh db and a legacy one must open.
- register_test.go exercises legacy migration; extend it with a pre-JSON fixture whose scalar parameters must survive the backfill intact through a Snapshot.

## Acceptance Criteria
- No SQL outside the migration mentions amplitude, gain, or time_constant.
- A database from before parameters_json opens, backfills once, and shows identical block parameters; a second open does not rewrite rows.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab


## Task 3: Give parameter fields their own parse and format

_ergo: 23HT4T_



## Goal
- Kill the two ~60-line name switches `setParameter` and `parameterText` in internal/studio/catalog.go by making each parameter field definition own how its value is read from and written to `Parameters`.

## Context
- `parameterDefinition` (catalog.go:34) already carries name/label/type/step/min/max/unit/help. Add per-field behavior: `set(*Parameters, string) error` and `text(Parameters) string`. The `numberField` helper can build both from a `*Parameters -> *float64` field selector, so scalar fields stay one line each.
- Decision (recorded in the container): keep the flat `Parameters` union struct in model.go and the existing JSON wire format unchanged. Per-kind parameter structs behind an interface were considered and rejected for now: much larger churn across store and tests without removing caller knowledge. Revisit only if the union grows past ~30 fields.
- Special fields become per-field closures: signs (space strip), numerator/denominator (parseCoefficients), approximation (int).
- `validateBlockUpdate` (catalog.go:291) iterates definition fields; it calls the field's own set afterward.

## Acceptance Criteria
- `setParameter` and `parameterText` are gone; no switch on parameter name remains in the package.
- Adding a scalar parameter to a block touches only the Parameters struct and that block's definition entry.
- Behavior identical: same validation messages for malformed numbers, coefficients, signs, and Padé order; catalog_test.go passes unmodified.

## Validation Gates
- gofmt -w cmd internal && go vet ./...
- go test -race ./...
- go build ./cmd/processlab

## Dependencies
- blocks `OOTX6Y`: Move block validation and summaries into the catalog


## Task 4: Move block validation and summaries into the catalog

_ergo: OOTX6Y_



## Goal
- Replace the per-kind switches `validateParameters` (catalog.go:322) and `Block.Summary` (catalog.go:256) with per-definition hooks so a block kind's rules live in its one definition entry.

## Context
- Extend `blockDefinition` with `validate func(Parameters) error` and `summary func(Parameters) string`. Per-field numeric bounds should move onto the field definitions: today `bounded("gain", -10000, 10000)` restates the field's Min/Max editor strings — make the numeric bound the one authority and derive the editor attributes from it. The validate hook then carries only cross-field rules (transfer proper-ness, sign alphabet, Padé range).
- `compileFlow` (simulate.go:133) and `validateBlockUpdate` both call validateParameters today; both go through the same definition method afterward.
- Validation message text is asserted by tests and shown in the interface; preserve it exactly.

## Acceptance Criteria
- No switch over BlockKind remains for validation or summaries.
- Editor min/max/step attributes derive from the same bounds validation enforces.
- Existing tests pass; add one test proving a cross-field rule (transfer proper-ness) still refuses through both the editor path and the compile path.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab

## Dependencies
- depends on `23HT4T`: Give parameter fields their own parse and format
- blocks `VBPV5F`: Move realization and waveforms into the catalog


## Task 5: Move realization and waveforms into the catalog

_ergo: VBPV5F_



## Goal
- Replace the `realizeBlock` switch (simulate.go:264), the `sourceValue` switch (simulate.go:347), and the `isSource`/`isSink` kind lists (simulate.go:339) with definition-owned behavior, so adding a dynamics block or a source touches only its catalog entry.

## Context
- Add to `blockDefinition`: a role (source | dynamic | sink), `realize func(Block, inputs int) (*controlsys.System, error)`, and for sources `waveform func(Parameters, t float64) float64`.
- Sources and sinks realize as unit gains today (simulate.go:268-269); make that the default when realize is nil.
- Signal naming (sourceSignalName/inputSignalName/outputSignalName) stays in simulate.go — it is the compiler's concern, not the block's.
- The realize hook returns *controlsys.System because that is what this compiler consumes. Do not generalize the signature for hypothetical nonlinear engines; the engine spike (root task) owns that question.
- Consider splitting catalog.go into per-category files (sources.go, continuous.go, ...) if it passes ~700 lines; each block entry should read as one self-contained declaration.

## Acceptance Criteria
- simulate.go contains no switch over BlockKind.
- Each block's definition entry is self-contained: metadata, fields, defaults, validation, summary, realization or waveform in one place.
- simulate_test.go analytic-response tests pass unmodified (identical numerics).

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab

## Dependencies
- depends on `OOTX6Y`: Move block validation and summaries into the catalog
- blocks `KBLEZJ`: Make the catalog the one authority for input arity


## Task 6: Make the catalog the one authority for input arity

_ergo: KBLEZJ_



## Goal
- State each block's input arity (none / exactly one / variadic) once, in its definition, and have both `Studio.Connect` (studio.go:383) and `compileFlow` (simulate.go:190) consult it.

## Context
- The rule "non-Sum blocks accept one input" is written twice today: Connect's incoming-count check (studio.go:418-429) and compileFlow's arity walk, which also special-cases BlockSum by name. A new variadic block (Product, Mux) would require editing both.
- Templates and view code read `Definition.HasInput`/`HasOutput` (view.go, workbench.html); keep those as accessors derived from arity/role.
- Sum's sign-count rule (signs must match connected inputs) moves into Sum's own hooks, not compileFlow's generic walk.

## Acceptance Criteria
- The one-input rule and the source/sink no-input/no-output rules each exist in exactly one place.
- Connect and compileFlow reject the same situations with the same messages as before; studio_test.go and simulate_test.go pass unmodified.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab

## Dependencies
- depends on `VBPV5F`: Move realization and waveforms into the catalog
- blocks `V2IZ2T`: Model ports in the domain and schema


## Task 7: spike: chart a discrete and nonlinear engine path

_ergo: LIOGYV_


## Goal
- Decide, on paper, how Process Lab grows past the linear boundary — Unit Delay, discrete filters, Product, Saturation, Switch, Relay — so block work after the catalog container can be scheduled with real dependencies.

## Context
- Today the compiler composes LTI realizations via controlsys.ConnectByName + Lsim (internal/studio/simulate.go); README states the linear boundary deliberately.
- Questions to answer: (1) what controlsys v1.2.0 offers beyond Lsim (discrete systems, nonlinear stepping); (2) a mixed sample-time policy for discrete blocks; (3) whether nonlinear blocks need a per-step graph evaluator (fixed-step integration over the topological order compileFlow already computes) beside the LTI path, and how the two coexist on one sheet; (4) which block families each investment unlocks, in dependency order.
- Integration point: the catalog realize hook (after "Move realization and waveforms into the catalog") is where a second engine would attach a different realization; propose that seam without building it.

## Acceptance Criteria
- docs/simulation-engine-roadmap.md: options with tradeoffs, one recommendation, and a dependency-ordered block/engine task list ready to file into ergo.
- What dependent tasks must learn from this spike: the chosen engine strategy and the order block families become possible. File the recommended follow-up tasks into ergo, or state explicitly that none are filed yet.


## Task 8: Share one design-token stylesheet

_ergo: 5YVU3I_



## Goal
- Move the :root token block that app.css and register.css each carry into one tokens.css both pages link, so the palette has one authority instead of a copied block and a comment promising the copies match.

## Context
- register.css:1-18 documents the copy as deliberate isolation from the 3,000-line canvas stylesheet. The isolation is right; the copying is not. tokens.css holds exactly the shared :root block (and the shared dark-scheme override if both shells carry one); each page adds one link tag before its own stylesheet.
- app.css keeps workbench-only tokens. The register's rule that --signal orange never appears on it holds by the register never using the variable; it does not need a separate palette file.
- Templates: page.html and register.html gain the link tag; CSP stays 'self'; assets stay embedded.

## Acceptance Criteria
- One :root token definition in the repository; both pages render identically (spot-check register and workbench at 1280px, light and dark scheme, via CDP screenshots).
- register.css's header comment states the new arrangement.

## Validation Gates
- go build ./cmd/processlab && go test -race ./...
- CDP screenshot spot-check.


## Task 9: Split app.js into ES modules

_ergo: BZAXSA_



## Goal
- Break the 1,551-line IIFE in internal/web/static/app.js into native ES modules with explicit state, so the next canvas features (ports, subsystems) land in a file of their own instead of growing one closure.

## Context
- Modules, by the state they own: viewport (pan/zoom/fit/readouts), selection (set, marquee, selection bar), dragging (block drag, guides, nudge), wiring (ports, draft edge), shell (rails, dock, shortcut sheet, persistence), plus a small shared canvas-geometry module (geometry(), screenToSheet, edgePath — edgePath must keep matching its server-side twin in view.go).
- No bundler: script type="module" in page.html; files under static/js/ ride the existing go:embed and the CSP 'self' rule. menu.js, tabs.js, and register.js already load separately — leave them.
- The delicate part is the htmx re-apply contract: after every #workbench swap, viewport, selection, and shell state re-apply from localStorage. Keep one explicit re-apply entry point modules register with, replacing scattered htmx:afterSettle listeners.
- Pure reorganization: no behavior, key-map, or persistence-key changes.

## Acceptance Criteria
- No module reaches into another's mutable state except through exported functions; each file under ~400 lines.
- The documented gesture set works unchanged — verified with the CDP interaction pass at 25%, 100%, and 400% zoom (viewport, snapping, marquee, keyboard, context menus, wiring).

## Validation Gates
- go build ./cmd/processlab (embed picks up the new files)
- go test -race ./... (embedded-asset tests)
- CDP pass over the workbench gestures.

## Dependencies
- blocks `KE3PPL`: Render and wire ports in the workbench


## Task 10: spike: measure workbench swap cost at scale

_ergo: YY5WPU_



## Goal
- Learn where the full-#workbench-swap architecture actually stops scaling, so any move to finer-grained swaps is decided on numbers, not fear.

## Context
- Every mutation re-renders and swaps the whole workbench fragment, then re-applies client state. At the seeded 8 blocks this is instant; nobody has measured 100 or 400.
- Method: script databases with 50-, 150-, and 400-block flowsheets (grid-placed, chained); measure server render time, fragment size, and browser swap+settle+re-apply time over CDP for a block move and a parameter edit at each size, at 25% and 100% zoom.

## Acceptance Criteria
- docs/swap-scaling.md with the numbers and one recommendation: either "full swap holds to N blocks; revisit at N" or the concrete shape of follow-up work (for example: oob-swap the inspector, dock, and tabs; patch block cards in place). What dependent tasks must learn: the block-count budget and the recommended swap strategy.
- The measurement script is checked in beside the doc and runnable by another agent.

## Validation Gates
- Doc and script exist; script runs against a scratch database without touching processlab.db.


## Task 11: Model ports in the domain and schema

_ergo: V2IZ2T_



## Goal
- Give connections port identity — source_port and target_port — and give each block kind a port list, so multi-input blocks stop being a BlockSum special case and MIMO or subsystem blocks become expressible.

## Context
- Ports derive from the definition and the block's parameters: Sum exposes one input port per sign character; every other current block exposes at most one input and one output (port 0). Expose e.g. `Definition.InputPorts(parameters)`.
- Schema: connections gains source_port and target_port INTEGER NOT NULL DEFAULT 0. UNIQUE(flow_id, source_id, target_id) must become per-port; SQLite constraint changes need a create-copy-drop-rename table rebuild inside one transaction (same discipline as ensureProjects in store.go).
- Backfill: existing inbound Sum connections are numbered onto ports by connection id ascending — exactly the order compileFlow assigns signs today, so no stored model changes meaning.
- Domain rules, each an explicit refusal in the existing style: an input port holds at most one wire; a connection must name a port the block has; shrinking Sum's signs below its highest connected port is refused (no silent wire dropping).
- Connect (studio.go:383) gains port arguments; the HTTP handler reads optional source_port/target_port form values defaulting to 0, so the existing client keeps working until the workbench task lands.

## Acceptance Criteria
- Old databases migrate: every connection lands on port 0 except Sum inputs, which occupy ports 0..n-1 in connection-id order; re-opening does not renumber.
- Editing Sum signs refuses to orphan a wired port, with a message naming the port.
- Two wires into one input port are refused; wires into different ports of one block succeed.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab

## Dependencies
- depends on `KBLEZJ`: Make the catalog the one authority for input arity
- blocks `3NXUEA`: spike: design hierarchical subsystems
- blocks `KE3PPL`: Render and wire ports in the workbench
- blocks `VJKI5N`: Compile wiring by port


## Task 12: Compile wiring by port

_ergo: VJKI5N_



## Goal
- Make the compiler wire signals by (block, port) instead of bare block id, so Sum signs bind to ports rather than to connection-id order.

## Context
- compileFlow (simulate.go) currently sorts Sum's incoming connections by connection id and matches signs positionally — deleting and redrawing a wire silently changes which sign it gets. After this task, sign i belongs to port i whatever order the wires were drawn.
- inputSignalName becomes port-qualified (block_N_input_P); realize hooks receive the connected ports; controlsys.Connection entries map source (id, port) to target (id, port).
- The seeded reactor model and every analytic simulate_test.go result must be numerically identical — the previous task's backfill guarantees sign assignments carry over.

## Acceptance Criteria
- A Sum with signs "+-" applies + to the wire on port 0 and - to port 1 regardless of creation order (add a test drawing them in reverse).
- Existing analytic response tests pass unmodified.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab

## Dependencies
- depends on `V2IZ2T`: Model ports in the domain and schema
- blocks `YWZAD7`: Verify and document the port model


## Task 13: Render and wire ports in the workbench

_ergo: KE3PPL_



## Goal
- Draw each block's real ports and let wiring gestures name the port they touch, so a Sum shows one input pip per sign and a wire lands on the port the user chose.

## Context
- Template: workbench.html renders one input and one output button today (data-input-port keyed by block id). Ports become a per-block list on the view model; attributes carry block id plus port index. Edge paths (edgePath in view.go and its client twin) anchor at the port's y-offset instead of the block's vertical midline — the two implementations must stay geometrically identical.
- Design: stay inside the established instrument language (app.css tokens). Input pips distribute evenly along the block's left edge, outputs along the right; --signal orange remains outputs-only, inputs stay gray. The Sum block draws its sign character beside each pip — the sign on the sheet is the port label, exactly as the paper notation writes it. No new colors; aria-labels name block and port.
- JS: the wiring gestures (beginConnection/finishConnection in the gesture layer) and their htmx form values gain the two port fields; the draft edge snaps to the hovered pip.
- Inspector: the per-block connection list names the port (for Sum, the sign) so a model is legible without counting wires.

## Acceptance Criteria
- A Sum with signs "+-" renders two labeled input pips; dragging a wire onto each lands on that port; the persisted connection carries it; a full reload draws identical geometry (server path and client redraw agree).
- Single-port blocks look and behave exactly as before at 25%, 100%, and 400% zoom.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab
- Real pointer gestures over CDP against headless Chrome: wire to each Sum port, reload, verify persisted geometry (docs/workbench-ergonomics.md's pass is the model).

## Dependencies
- depends on `BZAXSA`: Split app.js into ES modules
- depends on `V2IZ2T`: Model ports in the domain and schema
- blocks `YWZAD7`: Verify and document the port model


## Task 14: spike: design hierarchical subsystems

_ergo: 3NXUEA_


## Goal
- Produce the design for subsystem blocks — a flowsheet inside a block, the signature Simulink structure README currently rules out — so implementation can be planned as ordinary tasks.

## Context
- Questions: data model (a subsystem block referencing a child flow, versus flows nesting via a parent block id; which project the child sheet belongs to; cascade rules on delete and duplicate), port surface (child Inport/Outport blocks becoming the parent block ports — requires port identity, delivered by the ports container), navigation (tab strip versus breadcrumb descent; canonical URLs), and compilation (inline the child graph into the parent before compileFlow, with namespaced signal names).
- Constraints worth honoring from this codebase: every list the UI draws has one query authority; refusals are explicit domain messages; migrations open old databases unchanged.

## Acceptance Criteria
- docs/subsystem-design.md answering the questions with one recommendation and a task breakdown ready for ergo.
- Explicitly lists what stays out of a first subsystem release (masked parameters, library links, and similar).

## Dependencies
- depends on `V2IZ2T`: Model ports in the domain and schema


## Task 15: Verify and document the port model

_ergo: YWZAD7_



## Goal
- One end-to-end pass proving ports across the stack, then bring README.md and docs/workbench-ergonomics.md up to date.

## Context
- The pass: open a legacy database → migration lands Sum wires on ports → edit signs (grow allowed, orphaning shrink refused) → rewire between ports in the browser → simulate → numerics identical to the pre-port run for the seeded model.
- Document the port model in README (Supported blocks and SQLite data sections) and the gesture changes in docs/workbench-ergonomics.md.

## Acceptance Criteria
- A written verification section records the checks run (browser and migration), in the repo's existing verification style.
- README no longer describes Sum signs as connection-order-dependent.

## Validation Gates
- gofmt -w cmd internal && go vet ./... && go test -race ./... && go build ./cmd/processlab
- The CDP browser pass above, recorded in the doc.

## Dependencies
- depends on `KE3PPL`: Render and wire ports in the workbench
- depends on `VJKI5N`: Compile wiring by port

