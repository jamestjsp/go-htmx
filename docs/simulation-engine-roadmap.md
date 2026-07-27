# Simulation engine roadmap

How Process Lab grows past the linear boundary that `README.md` states and
`docs/simulink-block-expansion.md` explains. This is a decision document: it
picks an engine strategy and orders the block families that strategy unlocks.
No engine code is written here.

Read this before adding any block that is not a continuous LTI realization —
Unit Delay, discrete filters, Product, Saturation, Switch, Relay, or logic.

## What controlsys v1.2.0 actually provides

Verified against the pinned module source, not the README. The package was
read at `$(go env GOMODCACHE)/github.com/jamestjsp/controlsys@v1.2.0/` and
exercised from a throwaway module pinned to the same version and the same
gonum fork.

It is a large library — roughly 1,600 lines of exported API covering
synthesis, reduction, identification, frequency response, and delays. Almost
none of that is relevant here. Four facts decide the whole design.

**1. A `System` has exactly one `Dt`, and compositions refuse to mix.**
`ConnectByName` (`names.go:341`) delegates to `BlkDiag` (`connect.go:1078`),
which compares every system's `Dt` against the first and fails otherwise:

```
connectbyname: blkdiag: system 1 Dt=0.05 != Dt=0:
controlsys: systems must share the same time domain
```

There is no multirate composition and no continuous-plus-discrete
composition. A sheet holding a continuous Lag and a discrete Unit Delay
cannot be expressed as one `controlsys.System` at all.

**2. `Lsim` is a batch call over a uniform grid, and it discretizes.**
`Lsim` (`response.go:627`) validates that the grid is uniform, then — for a
continuous system — ZOH-discretizes the *composed* model at the grid spacing
and calls `System.Simulate`. For an already-discrete system it requires the
grid spacing to equal `sys.Dt` within 1e-6 relative and refuses otherwise:

```
Lsim: time grid spacing 0.02 does not match system Dt 0.05
Lsim: non-uniform time grid at index 2 (dt=0.06, expected 0.05); uniform grid required
```

An integer-multiple grid is not accepted either. Rate transitions are not a
thing this library does.

**3. There is no nonlinear time simulation and no ODE solver.** A search of
the non-test sources for Runge-Kutta, adaptive or variable step, and any
integrator type returns nothing. `System.Simulate` (`simulate.go:24`) is a
discrete recurrence and refuses continuous systems outright with
`controlsys: wrong time domain for operation`. `NonlinearModel`
(`linearize.go:9`) is not a simulation type: its only consumers are
`Linearize` and `EKF`. There is no Saturation, Dead Zone, Relay, or any
other nonlinearity anywhere in the package.

`controlsys`'s own `docs/lpv-ltv-sparse-scope.md` confirms this is
deliberate: LTV models are listed as "Later", explicitly needing
"time-varying state update callbacks or sampled trajectories,
simulation-only semantics".

**4. `Linearize` exists and is exactly the wrong tool.** It takes a nonlinear
model and returns a continuous `System` by finite differences at an operating
point. Linearizing a saturation at a point *inside* its limit returns
`C = [0]`, `D = [0]` and a nil error — a model that reports the signal as
identically zero, with no diagnostic. That is precisely the silent
linearization this repository forbids, offered by the library as a
success. Nothing in the compiler may call it.

What *is* usable, verified working:

| Capability | Call | Use here |
| --- | --- | --- |
| Per-sample stepping with carried state | `System.Simulate(u, x0, opts)` returning `XFinal` | Drives an LTI segment one sample at a time |
| Continuous to discrete | `DiscretizeZOH`, `DiscretizeFOH`, `DiscretizeMatched`, `DiscretizeImpulse` | Prepares a segment for stepping |
| Discrete to discrete resample | `D2D(newDt, opts)` | Not used; see the sample-time policy below |
| Discrete realization | `New(A, B, C, D, dt)` with `dt > 0` | Discrete filters, if they are ever composed |
| Fractional discrete delay | `ThiranDelay(tau, order, dt)` | A later discrete Transport Delay |

`DiscretizeZOH` was checked against every realization the catalog produces
today — static gain (`n = 0`), first-order lag, integrator (pole at the
origin), `PadeDelay(1, 3)`, and a PID with `Tf = 0.001` — at 0.01, 0.1, 1 and
2 seconds. All succeeded. The `n = 0` case returns a nil `XFinal` from
`Simulate`, which a stepping driver must handle rather than dereference.

`D2C` is not a way out of fact 1. Converting a unit delay back to continuous
by ZOH fails on its pole at the origin
(`matLog: eigenvalue[0]=(0+0i) on non-positive real axis (branch cut)`), and
the Tustin route succeeds while producing a model that is not the delay.

## The measurement that decides the design

Two first-order lags in series, unit step, `dt = 0.1`, compared against the
analytic response of `1 / ((s+1)(2s+1))`:

| Path | Max absolute error |
| --- | --- |
| Connect continuous, then discretize (today's `Lsim`) | 5.551e-16 |
| Discretize each block, then connect | 2.629e-02 |

ZOH is exact for a piecewise-constant input, and today the only
piecewise-constant signals are the external source channels — every internal
signal stays inside one continuous composition, so the answer is exact to
machine precision. That is why
`TestContinuousBlockResponsesAgainstAnalyticModels` can assert 1e-10 against
closed-form step responses.

Cutting a continuous chain and holding the signal at the cut over each step
is what costs 2.6e-2. **Every cut costs this.** Any proposal is measured
against whether it cuts sheets that do not need cutting.

A second measurement fixes the seam. Stepping a discretized system one
sample at a time with `Simulate`, feeding `XFinal` back as the next `x0`,
reproduces the whole-batch `Lsim` result with a maximum difference of
**0.000e+00**. Per-step and batch are the same arithmetic in the same order.

## Options

### A. Stay linear

Cost nothing, unlock nothing. Not a plan; it is the status quo this document
exists to chart past.

### B. Discretize the whole sheet at the base step

Give every `realize` hook the simulation's sample time, return discrete
systems at that `Dt`, compose with `ConnectByName`, keep `Lsim`. Structurally
this is the smallest change: one extra argument, nothing else moves.

**Rejected.** It converts every existing sheet from the 5.551e-16 row to the
2.629e-02 row of the table above, and would break the 1e-10 assertions in
`simulate_test.go`. It also buys nothing it was chosen for: a Saturation has
no LTI realization at any `Dt`, so the whole numerical price is paid and the
nonlinear boundary has not moved.

### C. Replace `Lsim` with a per-step evaluator everywhere

One engine, one code path, uniform semantics. Walk `compileFlow`'s
topological order once per step, evaluating each block from its own state.

**Rejected**, for B's numeric reason plus a maintenance one. Whichever
integrator is chosen — per-block ZOH, or a Runge-Kutta pass over a
hand-assembled `A`/`B` — it re-derives what `ConnectByName` and `Lsim`
already do exactly, and the numerics become this repository's problem
forever. The existing path is fast, exact, and asserted by tests. Discarding
it to gain Saturation is a bad trade.

### D. Segmented hybrid (recommended)

Partition the sheet into maximal LTI segments separated by **step blocks** —
blocks that have no continuous LTI realization. Compile each segment exactly
as today. Drive the sheet one sample at a time: step blocks are evaluated
algebraically between segments, in the topological order `compileFlow`
already computes.

The property that makes this the right answer: **a sheet with no step blocks
partitions into exactly one segment, and one segment is compiled and run by
today's code, unchanged.** The linear path is not a fast path bolted beside
the new engine; it is the degenerate case of the partition. It cannot drift,
because there is nothing to keep in sync.

## Recommendation

Build D.

Ship the segmenter and the per-step driver **with no step blocks registered**
first. At that point every sheet in existence is one segment, the entire
existing test suite must pass untouched, and the engine change is provably
inert. Only then register the first nonlinear block.

## How a Gain and a Saturation coexist on one sheet

Take the sheet the constraint asks about:

```
Step -> Gain(2) -> Saturation(±0.5) -> Lag(1) -> Scope
```

Compilation:

1. `compileFlow` runs unchanged: validation, arity, cycle rejection, and the
   topological order.
2. Delete the step-block vertices (Saturation) from the block graph. The
   remaining weakly-connected components are the segments: `{Step, Gain}` and
   `{Lag, Scope}`. Each is acyclic because the whole graph is, so each
   compiles through `ConnectByName` exactly as the whole sheet does today.
3. A segment's inputs are the source channels inside it plus one channel per
   edge entering it from a step block. Its outputs are the sink outputs
   inside it plus one output per edge leaving it to a step block.
4. Segments and step blocks form their own DAG; topologically order it.

Each sample `k` at time `t_k`:

1. Evaluate every source `waveform` at `t_k`. Unchanged.
2. Walk the segment/step order. For a segment, assemble its one-column input
   from the source values and the step-block outputs already computed this
   sample, call `Simulate` with the segment's carried state, read its
   outputs, keep `XFinal`. For a step block, read its input samples and call
   its step closure.
3. Record the sink outputs.

Remove the Saturation and step 2 has one segment and nothing else, so the
driver falls back to a single batch `Lsim` over the whole grid — today's
call, today's allocations, today's numbers.

**What the user pays, and must be told.** The signal entering the Saturation
is held over each step; the second segment sees a piecewise-constant input
that the true continuous signal is not. That is the 2.629e-02 row. It is not
a defect to be fixed — it is the inherent cost of fixed-step hybrid
simulation, and it is only charged where the user placed a nonlinear block.
Two consequences for the interface:

- On a multi-segment sheet the sample time stops being "how often an exact
  answer is sampled" and becomes the integration step. The simulation dock
  has to say so.
- The stored run should record the segment count, so a user can see when a
  sheet left the exact path and why.

## The realize seam

`realize` keeps its signature. Its meaning is tightened to one sentence, and
one sibling hook is added beside it in `blockDefinition`:

```go
// realize builds the block's continuous LTI realization (Dt = 0). A
// definition sets realize or step, never both.
realize func(Block, int) (*controlsys.System, error)

// step builds the block's per-sample evaluator: a closure owning whatever
// state the block carries between samples — a Unit Delay's one register, a
// Relay's latched side — returning its output for the inputs at this
// sample. Setting step is what makes a kind a step block: it is cut out of
// the LTI partition and driven by the hybrid loop instead. nil for every
// kind realize covers.
step func(block Block, baseStep float64) (func(inputs []float64, t float64) float64, error)
```

Plus one derived predicate, in the shape of the `isSource`/`isSink`/
`isSpectrumSink` accessors the catalog already has:

```go
func (k BlockKind) isStepBlock() bool { return blockDefinitions[k].step != nil }
```

**Cost to existing blocks: zero.** All twelve registered kinds keep their
current `realize` (or nil, and `realizeSystem`'s unit-gain default). No
existing entry gains a field, and `realizeBlock` in `simulate.go` is
unchanged for every block it handles today. The catalog remains the single
authority for which engine handles a kind, in the same way it is already the
single authority for role, arity, and validation.

Two rules the container must enforce, checked once over `blockDefinitions`
rather than at compile time:

- A definition sets exactly one of `realize` and `step`. Both is a
  contradiction; neither means the unit-gain default, which is right for
  sources and sinks and wrong for anything else.
- A step block may not be `roleSource`. A source is an external input
  channel, not a graph vertex the driver evaluates.

`baseStep` is passed to the step hook so a block with its own sample time can
check it (below). A block whose step closure wants library machinery — a
discrete transfer function, say — realizes a discrete `controlsys.System` at
its own `Dt` inside the closure and steps it with `Simulate`. That is the
block's business, not a seam change.

## Mixed sample-time policy

Fact 1 forecloses the obvious approach: two sample times cannot coexist
inside one `controlsys` composition, so a rate transition can only live in a
step closure. That makes the policy short.

**The simulation's sample time is the base rate.** It already exists, is
already user-visible, and is already bounded (0.01 to 2 seconds, at most
5,000 samples).

**A discrete block declares its own sample time as a parameter**, defaulting
to `0` meaning "inherit the base rate". `0` rather than Simulink's `-1`
because it matches this codebase's `omitempty` `Parameters` and reads as
unset. The UI question is exactly one new numeric field, "Sample time", with
`0 = inherit` in its help text — no new editor machinery.

**A declared sample time must be a positive integer multiple of the base
step.** `Ts / baseStep` is rounded; a relative error above 1e-9 is a refusal.
The tolerance matches `controlsys`'s own `discreteDelaySamples` rule.

**When they disagree, the compiler refuses.** A domain message names both
numbers and the nearest legal value:

> Unit Delay sample time 0.03 s is not a multiple of the simulation step
> 0.02 s; use 0.02 s or 0.04 s

It does not resample and it does not round. Rounding would silently run a
block the user did not configure, which is the same failure as silently
linearizing one.

**A legal multiple `N > 1` updates on samples where `k mod N == 0` and holds
between updates.** This is exact for a discrete block, and it is not an LTI
system at the base rate — it is periodically time-varying. That is the
concrete reason the rate transition lives in a step closure rather than a
`controlsys.System`, and it is why `D2D` is listed above as available but
unused: resampling a block to the base rate would change the block.

Sample-time *offset* (a block that updates on `k mod N == 1`) is out of scope
for the first cut. Say so in the field's help text rather than accepting a
value that is ignored.

## Block families, in dependency order

Each row needs everything above it.

| # | Family | Needs | Why it is gated there |
| --- | --- | --- | --- |
| 1 | Segmenter and hybrid driver, no new blocks | catalog `realize` seam (done) | Everything else is a step closure, and there is nowhere to put one until the driver exists |
| 2 | Memoryless nonlinear: Saturation, Dead Zone, Abs, Sign, Product, Divide, Min/Max, trigonometric | 1 | Pure functions of the current sample. No state, no rate, no new arity — Product and Min/Max reuse the existing `variadic` flag |
| 3 | Sample-time policy and Unit Delay | 1 | Unit Delay is the smallest block that carries state and needs a rate, so it is the right one to land the policy on |
| 4 | Discrete Transfer Function, Discrete State-Space, discrete filters | 3 | Need the rate policy for their `Ts` and the driver for stepping. Realized as a discrete `controlsys.System` and stepped with `Simulate` |
| 5 | Switch, Multiport Switch, Relay | 2 | Relay's hysteresis is state a step closure already owns. Switch is the first block whose arity is neither one nor variadic — see below |
| 6 | Logical Operator, Relational Operator, Compare To Constant | 5 | Trivial closures once 5's arity work exists |
| 7 | State-Space block, MIMO ports | none of the above | Orthogonal. It needs matrix-aware editor fields and multi-port block semantics, which is a catalog and canvas problem, not an engine one. Keep it off this critical path |

**Row 5 stresses the arity model.** `inputArity` is `none | one | variadic`
today, and a Switch takes exactly three inputs where the second is the
control, not a data input. That is neither value. Whoever files row 5 should
expect to extend `arity` into a small port-list description rather than add a
fourth enum value, and should check that `Connect`, `compileFlow`, and the
palette's port glyphs all still read from the one derivation.

**Row 6 has a type question with no type system.** Every signal here is a
`float64`. Booleans are 0 and 1 by convention, and the docs must say so
rather than pretend a signal type exists.

**Feedback stays refused, and is the largest thing this roadmap does not
solve.** `compileFlow` rejects cycles today. The most-wanted nonlinear
sheet — a PID with output saturation and anti-windup — is a cycle, so
Saturation lands useful but not yet useful for the thing people want it for.
The hybrid driver does make cycles more tractable than the LTI composer does:
a step block without direct feedthrough breaks an algebraic loop, so a loop
containing a Unit Delay could be ordered around. That is a separate decision
with its own numerics, and it should be spiked on its own once rows 1 to 3
exist.

## Task list, ready to file

None of these are filed in ergo yet. The list below is the intended shape;
`.ergo/plans.jsonl` was being edited by concurrent work when this spike ran,
so nothing was written to it.

| Task | Depends on | Deliverable |
| --- | --- | --- |
| T1 Add the `step` hook and `isStepBlock` to the catalog | — | Two fields and one predicate in `catalog.go`; the exactly-one-of and not-a-source rules checked over `blockDefinitions`; no kind sets `step` yet |
| T2 Partition `compileFlow` into segments | T1 | `compiledFlow` grows a segment list; with no step blocks it holds exactly one segment. Every existing test passes untouched |
| T3 Per-step driver behind the single-segment fast path | T2 | Multi-segment sheets step through `Simulate` carrying `XFinal`; single-segment sheets keep the batch `Lsim` call. Test asserts the two paths agree bit-for-bit on a single-segment sheet (measured max diff 0.000e+00) |
| T4 Surface segment count in the run record and dock | T3 | Sheet says when it left the exact path and what the sample time now controls |
| T5 Saturation block | T3 | First step block. Numeric check against the analytic response of a saturating first-order loop; refusal test that nothing linearizes |
| T6 Dead Zone, Abs, Sign, Product, Min/Max | T5 | Remaining memoryless nonlinearities; Product exercises `variadic` through the step path |
| T7 Sample-time parameter and the integer-multiple rule | T3 | The `Ts` field, the rounding tolerance, the refusal message naming both numbers and the nearest legal value |
| T8 Unit Delay | T7 | Smallest stateful discrete block; exercises hold-between-updates at `N > 1` |
| T9 Discrete Transfer Function and Discrete State-Space | T8 | Realized as a discrete `controlsys.System` at `Ts`, stepped with `Simulate` |
| T10 Port-list arity, then Switch and Relay | T6 | Extends `inputArity` past none/one/variadic without adding a fourth enum value |
| T11 Logic and comparison blocks | T10 | States the 0/1 boolean convention in the docs |
| T12 Spike: nonlinear feedback | T8 | Whether and how a cycle containing a delay-free-loop-breaking block can be ordered and run |

## What dependent tasks must know

- **The linear path is not being changed.** Any task touching `simulate.go`
  keeps `ConnectByName` plus a batch `Lsim` for a single-segment sheet. If a
  change makes a purely linear sheet take a different arithmetic path, it is
  wrong regardless of how close the numbers come out.
- **The catalog stays the single authority.** A kind is a step block because
  its definition sets `step`, and nowhere else states it.
- **Nothing calls `controlsys.Linearize`.** It returns a zero model for a
  saturated operating point with a nil error.
- **Nonlinear blocks do not arrive before the driver.** Rows 2 through 6 of
  the family table are all blocked on T3, and the ordering of rows 3 to 6 is
  the ordering their tasks must be scheduled in.
- **State-Space and MIMO are not blocked by any of this** and can be
  scheduled independently whenever the editor gains matrix fields.

Unresolved questions: whether a cycle containing a delay can be ordered and
run (T12); whether sample-time offset is ever wanted; whether the segment
count belongs in the stored run JSON or is recomputed for display.
