# Process Lab

Process Lab is a runnable demonstration of a Go + HTMX + SQLite application with a frontend that behaves like a small engineering desktop tool. Users build a dynamic process flowsheet, connect signal blocks, tune parameters, drag blocks around the canvas, and simulate the result without a frontend framework or JSON API.

The seeded example models two first-order paths feeding an energy balance:

- a setpoint through valve gain and reactor dynamics;
- a disturbance through jacket dynamics and heat loss;
- a summed temperature output rendered as an SVG trend.

The expected steady-state value is `1.8 + (0.3 × -0.7) = 1.59`, which is also asserted by the simulation tests.

## Run it

Requirements:

- Go 1.26.3 or newer
- an internet connection for the pinned HTMX 2.0.10 CDN script

```bash
go run ./cmd/processlab
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). The first run creates `processlab.db` and seeds the reactor example. The address serves the drawing register; open the seeded project from there.

To use another address or database:

```bash
go run ./cmd/processlab -addr 127.0.0.1:9090 -db ./demo.db
```

All CSS, application JavaScript, and HTML templates are embedded in the Go binary. Only HTMX itself is loaded from the pinned CDN URL.

## Run with Docker Compose

With Docker Desktop or another Docker Engine running:

```bash
docker compose up --build --detach
docker compose ps
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080). Compose builds a small
non-root image and stores `processlab.db` in the named
`processlab-data` volume, so projects and flowsheets survive container
replacement and restarts.

If port 8080 is already in use, choose another host port:

```bash
PROCESSLAB_PORT=9090 docker compose up --build --detach
```

Follow logs or stop the deployment with:

```bash
docker compose logs --follow
docker compose down
```

`docker compose down` preserves the database volume. Run
`docker compose down --volumes` only when you intentionally want to delete all
containerized Process Lab data.

## Projects, flowsheets, and persistence

Process Lab organizes top-level flowsheets inside projects.

`/` is the drawing register: one line per project, carrying its sheet count and
when it was last edited, and a row expands to reveal that project's flowsheets.
It replaces the old redirect into a flowsheet, so the application opens by
showing what exists rather than dropping you into whichever sheet it saw last.

- The last line of the register creates a project. It starts with one empty
  flowsheet named `Untitled flowsheet`.
- A project name opens that project's first sheet. A sheet chip in an expanded
  row opens that sheet directly, so the home screen reaches a sheet in one
  click.
- Double-click a project name, or use **Rename** on its row, to rename it in
  place. **Delete** removes the project and everything under it after a
  confirmation naming the project and its sheet count. The last remaining
  project offers no Delete.

Inside a project the flowsheets are a tab strip across the bottom of the
workbench, below the simulation dock and above the readout rail, so it stays
visible whatever the rails and the dock are doing:

| Gesture or control | Action |
| --- | --- |
| Click a tab | Open that sheet; only the workbench is swapped, so the page does not flash |
| Double-click a tab | Rename it in place; `Enter` commits, `Esc` puts the old name back |
| Right-click a tab | Rename, Duplicate, Delete |
| Drag a tab | Move it along the strip; an insertion marker shows where it lands |
| `Ctrl`/`Cmd` + `Shift` + `←` / `→` | Move the open sheet one place |
| `+` | Add a sheet named `Flowsheet N` and open its tab in rename, with no dialog |
| `‹ ›` | Scroll the strip one tab when there are more tabs than fit |
| `N sheets` | A jump list of every sheet in the project |

Duplicating a sheet copies its blocks, their parameters and positions, and the
wiring between them; run history is not copied. The copy is named `‹name› copy`
and lands immediately right of the sheet it came from. Deleting the open sheet
opens its left neighbour, or its right neighbour when it was the first tab, and
deleting the project you are inside returns you to the register. A project
always keeps at least one flowsheet, which is what guarantees the tab strip is
never empty.

A tab carries an amber dot when its model changed since its last simulation.
That is the same condition the simulation dock uses to call a chart stale, so
the tab and the chart cannot disagree.

The topbar carries **Projects**, a link back to the register, and a switcher
naming the open project that lists the others and creates a new one. The
flowsheets of the open project are the tab strip's subject and are not repeated
there.

Every edit is saved to the SQLite file passed with `-db`; there is no separate
save command. Stop the server with `Ctrl+C`, then run it again with the same
database path to reopen the workspace:

```bash
go run ./cmd/processlab -db ./demo.db
```

Each sheet has a canonical URL that stays valid across restarts, for example
`/projects/2/flows/5`, and switching tabs pushes it, so Back walks the sheets
you visited. Use a stable `-db` path when starting the server from different
working directories.

Databases from versions without projects are migrated at startup. Existing
flowsheets are retained inside a default `Process Lab project`, and their tabs
open in the order the old flowsheet menu listed them — by name, ignoring case —
after which the order is yours to change and is stored per project.

Projects currently contain independent top-level flowsheets. Hierarchical
subflowsheet or subsystem blocks inside a flowsheet are not yet supported.

## Try the workbench

1. Run the seeded model and inspect the temperature response and settling metric.
2. Right-click empty canvas and add a block; it lands where you clicked.
3. Drag from an orange output port to a gray input port to wire a signal.
   A Sum draws one labeled input port per `+`/`-` sign.
4. Click a block to edit its name or numerical parameter in the inspector.
5. Drag a block. It snaps to the grid and shows guides when it lines up with a neighbour.
6. Drag a box around several blocks and move them together.
7. Collapse the side rails and drag the dock down to give the sheet the whole window.

Press `?` for the full shortcut sheet.

## Workbench interaction

On desktop, the window is a fixed application shell: the canvas is the only
region that grows. At 860px and below, the interface stacks and the page
scrolls so every control remains reachable without horizontal overflow. Both
side rails collapse to a 46px icon strip — the collapsed library still adds
blocks — and the simulation dock at the bottom drags between a header-only
state and 70% of the window. Those choices persist across reloads. The
flowsheet tab strip spans the full width below the dock, outside the rails, so
collapsing anything never takes the other sheets with it.

The sheet is a 6000×4000 world on a 20px grid, viewed through a pan/zoom
viewport at 25%–400%.

| Gesture | Action |
| --- | --- |
| Wheel | Pan |
| `Cmd`/`Ctrl` + wheel, or pinch | Zoom about the pointer |
| Space + drag, or middle-drag | Pan |
| Drag empty canvas | Select blocks with a marquee (`Shift` extends) |
| Drag a block | Move it, snapped to the grid; moves the whole selection |
| `Alt` + drag | Suspend alignment magnetism (still snaps to the grid) |
| Drag a specific output port to a specific input port | Wire a signal to that terminal |
| Click output, then input | Wire without dragging; Enter or Space works on focused ports |
| Right-click | Context menu on a block or on the canvas |

| Keys | Action |
| --- | --- |
| `Delete` / `Backspace` | Delete the selection |
| Arrows / `Shift` + arrows | Nudge one / five grid steps |
| `Cmd`/`Ctrl` + `A` | Select every block |
| `Cmd`/`Ctrl` + `D` | Duplicate (wiring between blocks is not copied) |
| `Cmd`/`Ctrl` + `=` / `−` / `0` | Zoom in / out / reset |
| `Shift` + `1` | Fit the flowsheet to the window |
| `Esc` | Cancel wiring, or clear the selection |
| `Cmd`/`Ctrl` + `Enter` | Run the model |
| `Cmd`/`Ctrl` + `Shift` + `←` / `→` | Move the open sheet along the tab strip |
| `?` | Shortcut sheet |

The status bar is a live readout rail: cursor position in sheet
coordinates, zoom, grid pitch, selection count, block and signal counts,
and solver state.

`docs/workbench-ergonomics.md` records the interaction model, the state
that lives on the client, and the constraints behind these choices.

## Stack and request flow

```mermaid
flowchart LR
    Browser["Browser<br/>HTMX + small gesture layer"]
    HTTP["Go HTTP handlers<br/>HTML fragments"]
    Studio["Studio service<br/>domain operations"]
    SQLite[("SQLite<br/>projects, flows, events, runs")]
    Compiler["Flow compiler<br/>graph to state space"]
    Controlsys["controlsys v1.2.0<br/>named composition, simulation, analysis"]

    Browser -- "HTML requests" --> HTTP
    HTTP -- "add, connect, tune, run" --> Studio
    Studio <--> SQLite
    Studio --> Compiler
    Compiler --> Controlsys
    HTTP -- "server-rendered components" --> Browser
```

HTMX performs every server mutation and swaps the returned `#workbench` fragment. A small framework-free JavaScript layer handles only interactions that must stay in the browser: the pan/zoom viewport, pointer dragging and grid snapping, marquee selection, provisional signal lines, port gestures, context menus, and keyboard shortcuts. Every mutation still persists through `htmx.ajax`.

Because the swap replaces the whole fragment, all client-held state — viewport, selection, rail and dock sizing — is re-applied after each swap and stored in `localStorage` rather than in the flow record. Multi-selection is deliberately client-side, so the server keeps its single `selected` parameter for the inspector and a marquee costs no round trips.

The Go handlers state user intent and call one cohesive service operation. They do not coordinate SQL transactions or simulation steps. The `studio` package owns block defaults, validation, placement, interconnection validation, persistence, graph compilation, simulation, and stale-result rules.

## Supported blocks

| Library | Block | Behavior | Input rule |
| --- | --- | --- | --- |
| Sources | Step | Configurable initial value, final value, and step time | No input |
| Sources | Constant | Constant signal | No input |
| Sources | Vector Constant | Named constant vector | No input |
| Sources | Sine Wave | Biased sinusoid with amplitude, angular frequency, and phase | No input |
| Math | Gain | Multiplies its input by `K` | Exactly one |
| Math | Matrix Gain | Named vector relation `y = Du` | One vector input |
| Math | Mux / Demux | Assemble scalar channels into a named vector, or decompose one | Named scalar ports / one named vector |
| Math | Selector / Permutation | Select a named subset, or reorder a complete named channel set | One named vector |
| Math | Sum | Adds or subtracts inputs using a `+`/`-` sign pattern | One input port per sign |
| Math | Vector Sum | Adds or subtracts named vectors | One vector input port per sign |
| Continuous | First-order Lag | `1 / (τs + 1)` | Exactly one |
| Continuous | Integrator | `1 / s` with zero initial condition | Exactly one |
| Continuous | Transfer Function | Proper continuous SISO numerator/denominator model | Exactly one |
| Continuous | PID Controller | Parallel PID with a required derivative filter time | Exactly one |
| Continuous | Transport Delay | Exact delay metadata by default, or explicit Padé/Thiran approximation | Exactly one |
| Models | State-Space | Named continuous or discrete MIMO `A,B,C,D` realization | One named vector input |
| Models | MIMO Transfer Function | Output-row denominators, per-channel numerators and delays | One named vector input |
| Models | Zero-Pole-Gain | Per-channel zeros, poles, and finite gain matrix | One named vector input |
| Models | Frequency Response Data | Named complex MIMO samples on an explicit rad/s grid | Frequency-domain workflows only |
| Discrete | Unit Delay | Exact one-sample state at an explicit or inherited rate | Exactly one |
| Discrete | Transfer Function | Proper SISO numerator/denominator model in `z` | Exactly one |
| Discrete | State-Space | Named MIMO `x[k+1]=Ax[k]+Bu[k]`, `y[k]=Cx[k]+Du[k]` | One named vector input |
| Discrete | Discretized Transfer | Explicit ZOH, FOH, matched pole-zero, or impulse-invariant conversion | Exactly one |
| Sinks | Scope | Plots the time-domain signal and response metrics | Exactly one |
| Sinks | Vector Scope | Plots named vector channels | One vector input |
| Sinks | Spectrum Analyzer | Hann-windowed one-sided amplitude spectrum using Gonum FFT | Exactly one |

Flows may branch, merge, and close feedback loops. Named interconnections are
passed to `controlsys.ConnectByName`, which resolves dynamic feedback and
rejects only an unsolvable algebraic loop. Every source owns a separate
external input channel, so Step, Constant, and Sine Wave blocks remain
independent when a model branches or merges.

Each terminal has one catalog-derived width and ordered channel-name list.
Scalar diagrams retain width one and their existing port numbers. A vector is
one connection, not several unrelated wires: connections reject unequal
widths before persistence, then the compiler expands compatible vector
channels into deterministic `ConnectByName` pairs. Matrix Gain, Vector Sum,
Vector Constant, Vector Scope, State-Space, MIMO Transfer Function,
Zero-Pole-Gain, and the routing blocks exercise the same named MIMO feedback
path. Representation dimensions and channel names are validated together, so
a stored model cannot claim a port width that differs from its realization.

State-Space, MIMO Transfer Function, and Zero-Pole-Gain preserve their
authored parameters while delegating realization and conversion to
`controlsys`. Their explicit time-domain choice determines whether `Dt` is
zero or a positive sample time. MIMO transfer functions use the package's
native shape: one denominator per output row, one numerator and delay per
output/input channel. Frequency Response Data owns a strictly increasing
rad/s grid and finite row-major complex response samples. Because controlsys
FRD has no state-space conversion, an FRD block is deliberately
frequency-domain-only until an identification or fitting workflow creates a
time realization.

Transport Delay preserves exact delay metadata through named series and
feedback composition. Exact time simulation requires the delay to be an
integer multiple of the run sample time; otherwise the run reports the nearest
aligned value and asks for an explicit Padé or Thiran approximation. Padé is a
continuous rational model, while Thiran is a discrete all-pass model with its
own sample time. Stored delays created before these choices existed retain
their historical Padé behavior.

Discrete blocks declare an explicit sample time or inherit the run step. Unit
Delay carries its state exactly between samples. Discrete Transfer Function
and State-Space blocks are realized directly at their declared `Dt`.
Discretized Transfer makes conversion a visible model choice—ZOH, FOH,
matched pole-zero, or impulse invariant—rather than silently choosing a
method during compilation.

Every stored run includes a fidelity record: base step, model domain, driver,
segment count, source hold, discrete block rates, rate transitions, and delay
provenance. The simulation dock renders the same record, naming exact,
Padé-order, and Thiran-order delay behavior. Unsupported fractional delay
alignment or unresolved mixed rates fail before simulation rather than
falling back to a hidden approximation.

Connections identify both endpoint ports. For a Sum, sign character `i`
belongs to input port `i`, so deleting and redrawing another wire cannot change
which inputs are added or subtracted. Editing the sign pattern adds ports;
removing a sign is refused while that port still carries a wire.

Each linear block becomes a locally named `controlsys.System`.
`controlsys.ConnectByName` composes compatible realizations into one
state-space model. Continuous delay-free systems use `controlsys.Lsim`;
discrete systems and delay-aware conversions use `System.Simulate` while
carrying `XFinal` between segments. Spectrum Analyzer sinks then apply
Gonum's Hann window and real FFT to their selected response.

Plain continuous `Lsim` is used only when the model is delay-free. A connected
exact delay must first be internalized by named composition and aligned to the
run grid; the engine then takes the explicit delay-aware
`DiscretizeWithOpts` + `Simulate` path so controlsys owns the delay buffers.

Compilation returns one owned model artifact containing the composed system,
stable block/port channel identities, source excitations, selected outputs,
time-domain metadata, dimensions, and a snapshot of the diagram provenance.
Simulation and analysis consume that artifact instead of reconstructing
controlsys channel order or exposing its mutable matrices.

Analysis probes identify a block and output port rather than spelling an
internal controlsys name. The compiler coalesces duplicates in first-request
order and exposes those signals while composing the graph; a later subset is
selected with `controlsys.SelectByName`. Scope and Spectrum Analyzer blocks
remain simulation consumers, not the authority on which signals analysis may
inspect.

`Studio.AnalyzeDynamics` selects one compiled input/output channel pair and
exposes controlsys stability, poles, zeros, DC gain, and damping. A standard
step response is calculated only when the caller declares a step experiment;
its rise, settling, overshoot, undershoot, peak, peak-time, and steady-state
metrics are separate from the arbitrary-source metrics stored on normal
simulation runs. Undefined operations return named issues beside any valid
partial results rather than non-finite JSON values.

`Studio.AnalyzeFrequency` selects one or more named input/output channels.
It reports Bode paths in dB and unwrapped degrees, SISO Nyquist and Nichols
data, and linear singular values for rectangular MIMO models. Frequency grids
are always angular frequency in rad/s; callers may provide a strictly
increasing grid or request an automatic one. Discrete grids end at `π/Dt`.
This model frequency response is distinct from the Spectrum Analyzer, which
is an FFT of one sampled simulation signal.

`Studio.AnalyzeLoop` requires one explicit named input/output channel pair. It
does not infer a loop from diagram topology. The report uses controlsys for
classical and all-crossing margins, bandwidth, peak-sensitivity modulus
margin, root locus, and sampled passivity evidence. Every operation carries
applicability metadata: exact internal delays retain frequency-crossing
margins but do not claim finite-order bandwidth or root-locus results, and a
sampled passivity pass is never presented as an analytic certificate.

`Studio.RunAnalysis` is the workbench boundary for those analysis intents. It
owns the snapshot, named-channel selection, calculation, ephemeral per-flow
cache, and revision comparison. Dynamics, frequency, and loop results remain
side by side for comparison; a model edit keeps them visible but marks them
stale, while a layout-only move leaves their model revision current. The dock
renders step, pole-zero, Bode, Nyquist, Nichols, singular-value, and root-locus
views from that shared channel and revision metadata.

Named vector routing is explicit diagram algebra. Mux assembles scalar ports
into one named vector; Demux decomposes it; Selector emits a validated named
subset; Permutation requires and reorders the complete channel set. Each is a
static `controlsys.NewGain` selection matrix, so vector fan-out, MIMO sums,
feedback, simulation, and analysis all use the same named interconnection
compiler. Missing or duplicate channel names are rejected before compilation.

The linear boundary is deliberate. Continuous and discrete state-space,
transfer-function, delay, and named MIMO models stay within controlsys.
Continuous/discrete mixtures and unresolved multirate execution are refused
with the required conversion or scheduling action. Product, Saturation,
Switch, Relay, and logic blocks require a nonlinear or hybrid solver; this
compiler does not silently linearize them.

The module pins `github.com/jamestjsp/controlsys` to `v1.2.0` and includes the Gonum fork replacement required by that package.

## SQLite data

The database stores:

- projects and their top-level flowsheets;
- flows, their place in the project's tab strip, and separate layout/model
  update timestamps;
- blocks, positions, and version-tolerant JSON parameters;
- signal connections with source and target port indices, foreign keys, tuple
  uniqueness, and a domain rule that each target port accepts one wire;
- recent activity events;
- complete simulation runs as JSON time series.

Model edits invalidate the displayed result, while layout-only moves and
flowsheet renames do not. Historical runs remain in SQLite. Schema startup
migrates databases created before projects, tab order, model timestamps, JSON
block parameters, or connection ports were introduced. During the port
migration, every source endpoint becomes port 0 and target endpoints are
numbered per target by their old connection order. Non-Sum blocks could carry
only one input and therefore remain on target port 0; Sum inputs retain the
positions the old compiler gave their signs. Reopening the migrated database
does not renumber it. Deleting a project reaches its flowsheets, blocks,
connections, events, and runs through `ON DELETE CASCADE`, so foreign keys are
turned on in the connection string rather than left to a pragma on whichever
connection happens to run it.

## Project structure

```text
cmd/processlab/           executable and graceful HTTP shutdown
internal/studio/          domain, SQLite repository, compiler, simulation
internal/web/             handlers, view models, embedded templates and assets
docs/                     block library and workbench interaction notes
.ergo/plans.jsonl         dependency-ordered implementation history
```

## Validate it

```bash
gofmt -w cmd internal
go vet ./...
go test -race ./...
go build ./cmd/processlab
```

The persistent tests cover project and flowsheet lifecycle operations —
including the refusal to delete the last project or the last flowsheet,
duplicate fidelity down to block parameters and remapped connection ids, and
reorder rejecting ids from another project — SQLite round trips, legacy
migration and the per-project tab-order backfill, the register query's counts
and stale-run flag, grid snapping and the sheet bounds, collision-free block
placement, connection constraints, feedback and algebraic-loop handling, analytic control-block
responses, FFT peak detection, HTML fragment behavior, embedded assets,
multi-field HTTP editing flows, and the batch move, delete, and duplicate
endpoints including rejection of ids from another flow.

Interaction behavior cannot be covered by Go tests. It was verified by driving
real pointer and key gestures against headless Chrome over CDP — 88 checks
across viewport, snapping, selection, keyboard, context menus, and wiring — at
25%, 100%, and 400% zoom.

The port-model pass additionally migrated and reopened a pre-port connection
fixture, grew a Sum's sign list and refused a shrink that would orphan a wire,
then disconnected and pointer-wired the seeded model to Sum ports 0 and 1.
The draft snapped to each labeled port, both port indices survived an HTMX
swap and a reload, and server-rendered and client-redrawn curve coordinates
matched. Focused-port keyboard wiring, cancellation, history restore, dense
16-port hit testing, and unchanged single-port geometry at 25%, 100%, and 400%
also passed. Restoring the seeded `++` signs and running the rewired model
produced the expected displayed final value of `1.591`. The persistent
regression verifies 301 samples, the final-value tolerance, and the settled
metric; the compiler pass also compared every sample and metric bit-for-bit
before and after port-based wiring.

The navigation redesign was verified the same way, end to end in one browser
session: create a project from the register, add sheets with `+`, rename a tab
by double-click, duplicate it, reorder by drag and by keyboard, delete a sheet
and land on the neighbour the domain chose, switch projects from the register
and from the topbar switcher, rename and delete a project, then restart the
server and confirm the register, the tab names, and the tab order came back
unchanged — 77 checks. The same pass was repeated against a database written
before this branch, whose `flows` table had no `position`, `project_id`, or
`model_updated_at` column: it migrated on open, its tabs appeared in the old
by-name order, every new operation worked on it, and a second open did not
re-sort the strip — 46 checks. Rendering was confirmed at 1440, 1280, 860, and
620px on both pages.

Note that templates and static assets are `go:embed`-ed into the binary, so a change to `static/js/*.js` or `app.css` needs a rebuild before the server serves it.
