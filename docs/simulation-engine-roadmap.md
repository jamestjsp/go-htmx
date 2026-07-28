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
(`linearize.go:9`) is not a simulation type: its only consumer is
`Linearize`. (`EKF` is not a second consumer — it takes its own `EKFModel`,
`ekf.go:10`.) There is no Saturation, Dead Zone, Relay, or any other
nonlinearity anywhere in the package.

`controlsys`'s own `docs/lpv-ltv-sparse-scope.md` confirms this is
deliberate: LTV models are listed as "Later", explicitly needing
"time-varying state update callbacks or sampled trajectories,
simulation-only semantics".

**4. `Linearize` exists, works correctly, and is still not usable here.** It
takes a `NonlinearModel` and returns a continuous `System` by finite
differences at an operating point. Its answers for a saturation with limits
at ±0.5 are right in every construction:

| Model | Operating point | Result |
| --- | --- | --- |
| saturation as feedthrough in `H` | `u0 = 0.2`, inside the limit | `C = 0`, `D = 1` |
| saturation as feedthrough in `H` | `u0 = 0.8`, saturated | `C = 0`, `D = 0` |
| saturation in the state update `F` | `u0 = 0.2`, inside the limit | `B = 1`, `C = 1` |
| saturation in the state update `F` | `u0 = 0.8`, saturated | `B = 0`, `C = 1` |

Inside the limit it returns the unity model, which is the correct local
behaviour. `C = 0`, `D = 0` appears only at a *saturated* operating point,
where zero local gain is the mathematically correct answer, not a defect.
`finiteDifferenceLocalModel` takes a central difference with
`h = sqrt(eps) * max(|u0|, 1)` ≈ 1.5e-8 (`local_approx.go:66` for `sqrtEps`,
`:113-114` for the input-side step), which is far too small to straddle a
limit from any operating point that is not already on one.

The one place the central difference does straddle the kink is an operating
point sitting exactly on it: at `u0 = 0.5` against a limit of 0.5 the two
probes land on opposite sides and `D` comes back as **0.5** — the midpoint of
the one-sided derivatives. That is a defensible answer rather than a wrong
one, since it lies in the Clarke subdifferential, and it does not change the
conclusion below.

The reason the compiler must not call it is simpler and has nothing to do
with the library's accuracy: **a linearized saturation is not a saturation at
any operating point.** Linearization replaces the block with an LTI
approximation valid only in a neighbourhood, and the sheet is then no longer
the model the user drew. That is what the no-silent-linearization rule
forbids, and it forbids it regardless of how correctly the Jacobian is
computed. Nothing in the compiler may call `Linearize`.

What *is* usable, verified working:

| Capability | Call | Use here |
| --- | --- | --- |
| Per-sample stepping with carried state | `(*System).Simulate(u, x0, opts)` returning `XFinal` | Drives an LTI segment one sample at a time |
| Continuous to discrete | `(*System).DiscretizeZOH`, `.DiscretizeFOH`, `.DiscretizeMatched`, `.DiscretizeImpulse` | Prepares a segment for stepping |
| Discrete to discrete resample | `(*System).D2D(newDt, opts)` | Not used; see the sample-time policy below |
| Discrete realization | `New(A, B, C, D, dt)` with `dt > 0` | Discrete filters, if they are ever composed |
| Fractional discrete delay | `ThiranDelay(tau, order, dt)` | A later discrete Transport Delay |

The first three are methods on `*System`; the last two are package functions.

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

Two first-order lags in series with time constants τ = 1 and τ = 0.5 — the
system `1 / ((s+1)(0.5s+1))` — driven by a unit step and compared against its
closed-form response `1 - 2e^{-t} + e^{-2t}`. Errors are the maximum over the
run and are flat in the horizon (checked at 3, 5, 10, 20 and 50 s):

| Sample time | Connect, then discretize (today's `Lsim`) | Discretize each block, then connect |
| --- | --- | --- |
| 0.05 | 1.221e-15 | 1.282e-02 |
| 0.1 | 8.882e-16 | **2.629e-02** |
| 0.2 | 5.551e-16 | 5.482e-02 |

**Quote the system, not just the sample time, when citing these.** The error
scales with the product of the sample time and the fastest pole, so the same
number attaches to a different `dt` on a different plant. The slower pairing
τ = 1 and τ = 2 — the system `1 / ((s+1)(2s+1))` — gives 6.329e-03, 1.282e-02
and 2.629e-02 at the same three sample times, one column shifted. An earlier
draft of this document named that second system in its prose while measuring
the first, which is exactly the confusion this paragraph exists to prevent.

ZOH is exact for a piecewise-constant input. Every internal signal stays
inside one continuous composition, so nothing internal is ever held, and the
composed answer is exact to machine precision for a source that really is
piecewise constant. That is why
`TestContinuousBlockResponsesAgainstAnalyticModels` can assert 1e-10 — its
three cases all use Constant or Step inputs (`simulate_test.go:222-233`).

**The exactness does not extend to every sheet that exists today.** `Lsim`
samples each source and holds it across the step, so a Sine source is already
stair-stepped going in. One Lag driven by `sin(t)`:

| Sample time | Sine source | Step source |
| --- | --- | --- |
| 0.2 | 7.397e-02 | 6.661e-16 |
| 0.1 | 3.642e-02 | 1.887e-15 |
| 0.05 | 1.807e-02 | 2.443e-15 |
| 0.01 | 3.591e-03 | 1.166e-14 |

At `dt = 0.1` a Sine sheet is already carrying more error (3.642e-02) than a
segment cut would add (2.629e-02). Nothing in the existing tests covers this,
because none of them use a Sine source against a closed form. The
"exact for linear models" claim in this repository means *exact for
piecewise-constant sources*, and the distinction matters for what the
interface can honestly tell a user (see T4 below).

Cutting a continuous chain and holding the signal at the cut over each step
is what costs the right-hand column. **Every cut costs this.** Any proposal is
measured against whether it cuts sheets that do not need cutting.

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

**Rejected.** It moves every existing sheet from the left column of the table
above to the right one — at `dt = 0.1`, from 8.882e-16 to 2.629e-02 on the
τ = 1, τ = 0.5 chain — and would break the 1e-10 assertions in
`simulate_test.go`.
Note that it charges this to *every* block boundary, not just the ones a
nonlinear block sits on, so a long chain compounds it. It also buys nothing
it was chosen for: a Saturation has no LTI realization at any `Dt`, so the
whole numerical price is paid and the nonlinear boundary has not moved.

### C. Replace `Lsim` with a per-step evaluator everywhere

One engine, one code path, uniform semantics. Topologically order the
executable units once, then evaluate each block from its own state per step.

**Rejected**, for B's numeric reason plus a maintenance one. Whichever
integrator is chosen — per-block ZOH, or a Runge-Kutta pass over a
hand-assembled `A`/`B` — it re-derives what `ConnectByName` and `Lsim`
already do exactly, and the numerics become this repository's problem
forever. The existing path is fast, exact, and asserted by tests. Discarding
it to gain Saturation is a bad trade.

### D. Segmented hybrid (recommended)

Partition the sheet into LTI segments separated by **step blocks** — blocks
that have no continuous LTI realization. Compile each segment exactly as
today. Drive the sheet one sample at a time: step blocks are evaluated
algebraically between segments. Pure-LTI feedback must remain inside a
segment; ordering applies to the resulting segment/step graph, not to the raw
signal graph.

The property that makes this the right answer: **a sheet with no step blocks
partitions into exactly one segment, and one segment is compiled and run by
today's code, unchanged.** The linear path is not a fast path bolted beside
the new engine; it is the degenerate case of the partition. It cannot drift,
because there is nothing to keep in sync.

Getting the partition rule right is the real design work in this option, and
the obvious rule does not work. See the next section.

## Recommendation

Build D.

Ship the segmenter and the per-step driver **with no step blocks registered**
first. At that point every sheet in existence is one segment, the entire
existing test suite must pass untouched, and the engine change is provably
inert. Only then register the first nonlinear block.

## The partition rule for an acyclic signal graph

**The obvious rule is wrong.** "Delete the step-block vertices and take the
weakly-connected components of what is left" works on a chain and fails on
the most ordinary way anyone will place a Saturation — on one branch of a
Sum. This sheet is legal and acyclic, since `BlockSum` is
`arityVariadic`:

```
Step -> Gain ; Gain -> Sat ; Gain -> Sum ; Sat -> Sum ; Sum -> Scope
```

Delete `Sat` and the survivors `{Step, Gain, Sum, Scope}` are one
weakly-connected component. But `Sat` draws its input from that component and
returns its output to it, so the segment/step graph is
`segment -> Sat -> segment` — a two-cycle produced from an acyclic block
graph, with no execution order to compute. Enumerating *every* DAG shape on
2 to 6 blocks crossed with every step-block subset, this rule produces a
cyclic segment graph on **26.0% of the 2,097,152 cases at six blocks**, first
failing at three. It is not an edge case.

**For a block DAG, the rule that works** assigns each block a step depth and
cuts on that:

```
depth(b) = 0                                     if b has no incoming edges
depth(b) = max over incoming edges a -> b of
             depth(a) + (1 if a is a step block else 0)
```

A segment is the set of non-step blocks sharing one depth. Step blocks are
ordered by their own depth.

**This is acyclic for any block DAG, and it has a proof rather than an
appeal to sampling.** Give the segment at depth `k` the rank `k`, and give a
step block `b` the rank `d(b) + ½`. Every edge of the block graph that
crosses between two partition vertices strictly increases rank:

| Edge `a -> b` | Depth relation | Rank relation |
| --- | --- | --- |
| segment to segment (both non-step) | `d(b) ≥ d(a)`, and `d(b) ≠ d(a)` or they would be the same segment | `d(b) > d(a)` |
| segment to step block | `d(b) ≥ d(a)` | `d(b) + ½ > d(a)` |
| step block to segment | `d(b) ≥ d(a) + 1` | `d(b) > d(a) + ½` |
| step block to step block | `d(b) ≥ d(a) + 1` | `d(b) + ½ > d(a) + ½` |

A cycle would have to return to a rank it already left, so there are none.
The exhaustive enumeration agrees: **zero** cyclic segment graphs across
every DAG shape on 2 to 6 blocks and every step-block subset.

That proof is intentionally scoped to DAGs. Linear feedback is now a supported
`ConnectByName` interconnection, so the production segmenter cannot begin by
rejecting every cycle or by topologically sorting individual blocks. The
delay/mixed-domain spike must first establish how pure-LTI strongly connected
components are retained inside a named segment. The depth construction then
applies to the condensed graph. A cycle crossing a step-block boundary needs
its own execution and algebraic-loop contract.

Note the first row. **Segment-to-segment edges are real**, and they are the
class the connected-component rule could not produce: one input pushes a
block to a higher depth while another input stays behind at a lower one. They
occur in 44.1% of the six-block cases and first appear at three blocks
(`A -> S`, `S -> C`, `A -> C` with `S` a step block puts `A` at depth 0 and
`C` at depth 1, with `A -> C` spanning them). An earlier draft of this
document enumerated only the two step-block rows above and got the boundary
channels wrong as a result — see the compilation steps below.

It also preserves the property the whole recommendation rests on: with no
step blocks every block has depth 0, so there is exactly one segment
regardless of whether the sheet is connected — which is what today's single
`ConnectByName` call over all blocks already does. Note that segments are
depth classes, *not* connected components; refining by connectivity would
split a disconnected linear sheet into several segments and lose that
property for no gain.

On the reviewer's counterexample the rule gives depth 0 to `{Step, Gain}` and
`Sat`, and depth 1 to `{Sum, Scope}` — two segments with `Sat` between them,
correctly ordered.

## How a Gain and a Saturation coexist on one sheet

```
Step -> Gain(2) -> Saturation(±0.5) -> Lag(1) -> Scope
```

Compilation:

1. Run the existing validation and named-port derivation without rejecting
   feedback merely because it is cyclic.
2. Retain each pure-LTI feedback component inside one segment, condense those
   components, then compute step depths on the resulting acyclic graph. For
   this acyclic example that groups `{Step, Gain}` at depth 0 and
   `{Lag, Scope}` at depth 1, with `Saturation` a step block at depth 0. Each
   segment compiles through `ConnectByName` exactly as the whole sheet does
   today.
3. Cut the boundary channels. **The rule is about leaving the segment, not
   about step blocks**: a segment's ports are its edges to and from anything
   outside it, whichever kind of vertex sits on the other end.

   - **Inputs:** the source channels inside it, plus one channel per edge
     `a -> b` with `b` in the segment and `a` outside it — `a` being a step
     block *or a block in another segment*.
   - **Outputs:** the sink outputs inside it, plus one output per block in
     the segment that has at least one edge leaving it — again to a step
     block or to another segment.

   Writing this in terms of step blocks alone drops every segment-to-segment
   edge, which is wrong on 44.1% of six-block sheets. On the Sum-branch
   counterexample it would compile `segment@1` with one input for a two-input
   `Sum`, because the `Gain -> Sum` edge is neither a source channel nor an
   edge from a step block.

   The existing naming needs no extension. `inputSignalName` already keys on
   the target block and the connection, so a variadic block's two boundary
   inputs get distinct names, and `outputSignalName` keys on the source
   block, so one output serves a fan-out. Reusing them is what makes the
   boundary channels collision-free.
4. ZOH-discretize each segment at the base step. `Simulate` refuses
   continuous systems (`controlsys: wrong time domain for operation`), so
   `DiscretizeZOH(baseStep)` is a required step, not an optimisation. Do it
   once at compile time, not once per sample.

Each sample `k` at time `t_k`, for depth 0, 1, 2, … in order:

1. Evaluate every source `waveform` at `t_k`. Unchanged.
2. Run the segment at this depth: assemble its one-column input by reading,
   for each boundary input channel, the value its feeding block already
   produced this sample — a step block's closure output, or **another
   segment's output** — together with the source values. Call `Simulate` with
   the segment's carried state, read its outputs, keep `XFinal`, remembering
   that a stateless segment returns a nil `XFinal`.
3. Run every step block at this depth. They cannot feed each other, because
   an edge between two step blocks raises the depth of the target, so their
   order within a depth is free.
4. Record the sink outputs.

Both feeding cases in step 2 are always available: a step block feeding a
depth-`k` segment sits at depth `k-1` or lower and ran in a previous pass of
step 3, and a segment feeding it sits at a strictly lower depth and ran in a
previous pass of step 2. Execution *order* was never the problem — only the
channel enumeration was.

Remove the Saturation and every block sits at depth 0, so there is one
segment and no step blocks: the driver falls back to a single batch `Lsim`
over the whole grid — today's call, today's allocations, today's numbers.

**What the user pays, and must be told.** The signal entering the Saturation
is held over each step; the second segment sees a piecewise-constant input
that the true continuous signal is not. That is the right-hand column of the
table above — 2.629e-02 for the τ = 1, τ = 0.5 chain at `dt = 0.1`, and the
paragraph beside that table on why the plant matters as much as the sample
time applies to this citation too. It is not a defect
to be fixed; it is the inherent cost of fixed-step hybrid simulation, and it
is only charged where the user placed a nonlinear block.

Two consequences for the interface, and one trap:

- On a multi-segment sheet the sample time stops being "how often an exact
  answer is sampled" and becomes the integration step. The simulation dock
  has to say so.
- The stored run should record the segment count.
- **Segment count alone would mislead.** A one-segment sheet driven by a Sine
  source is already carrying 3.642e-02 at `dt = 0.1` — more than a cut costs —
  so "1 segment" must not be rendered as a promise of exactness. Whatever T4
  displays has to key off *both* the segment count and whether every source on
  the sheet is piecewise constant.

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

**The single `float64` return is a deliberate narrowing, and it forecloses
multi-output step blocks.** Every signal on a sheet is scalar today and every
block has one output port, so a scalar closure matches the model exactly and
keeps the driver's per-sample bookkeeping trivial. The cost is that a
Demux, a multi-output Switch, or any block returning a vector cannot be
expressed through this hook — it would need the return widened to
`[]float64` and the driver taught to fan out. That is a deliberate deferral,
not an oversight: widening later is a mechanical change to one signature and
one loop, whereas designing for multi-output now would put vector plumbing in
every scalar block's way. Row 7 of the family table (State-Space and MIMO
ports) is where that bill comes due.

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
step.** Compute `Ts / baseStep`, round it, and refuse if the rounding moved it
by more than 1e-9. That is an *absolute* tolerance on the sample count, not a
relative one on `Ts` — which is what `controlsys`'s own
`discreteDelaySamples` does (`time_domain.go:59-66`,
`math.Abs(samples-rounded) > 1e-9`), and adopting the same form keeps one
rule rather than two that disagree near the limits.

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
| 1 | Segmenter and hybrid driver, no new blocks | the `step` hook (T1), which does not exist yet | The catalog's `realize` seam is done, but nothing can be a step block until `step` exists, and there is nowhere to put a closure until the driver does |
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

**Linear feedback is supported; nonlinear and mixed-domain feedback remains
an engine decision.** `compileFlow` passes the full named graph to
`controlsys.ConnectByName`, which accepts well-posed LTI feedback and rejects
only an unsolvable direct-feedthrough algebraic loop. A PID with output
saturation and anti-windup crosses an LTI/step boundary, however, so it is not
covered by that contract. A stateful step block such as Unit Delay can break a
loop at a sample boundary; a memoryless step block may instead require an
algebraic solve or a precise refusal. The delay/mixed-domain spike and hybrid
driver tasks own that distinction.

## Historical task decomposition

The table below records the spike's original decomposition. The live Ergo
graph now carries the implementation work under epic `G3QP3P`, beginning with
`WWTXZL` for delay and mixed-domain contracts and `AHANTK` for the segmented
driver. Those task bodies are authoritative where this historical numbering
differs.

| Task | Depends on | Deliverable |
| --- | --- | --- |
| T1 Add the `step` hook and `isStepBlock` to the catalog | — | Two fields and one predicate in `catalog.go`; the exactly-one-of and not-a-source rules checked over `blockDefinitions`; no kind sets `step` yet |
| T2 Partition `compileFlow` into step-depth segments | T1 | Pure-LTI feedback components remain intact, then depth is computed on the condensed graph; for an acyclic graph, segments are depth classes rather than connected components. Boundary channels are enumerated by *leaving the segment*, so segment-to-segment edges are carried. The Sum-branch counterexample and exhaustive DAG cases remain regressions. With no step blocks it holds exactly one segment and every existing test passes untouched |
| T3 Per-step driver behind the single-segment fast path | T2 | Segments `DiscretizeZOH(baseStep)`-ed once at compile time, then stepped through `Simulate` carrying `XFinal` (nil for stateless segments); single-segment sheets keep the batch `Lsim` call. Test asserts the two paths agree bit-for-bit on a single-segment sheet (measured max diff 0.000e+00) |
| T4 Report simulation fidelity in the run record and dock | T3 | Segment count *and* whether every source is piecewise constant. A Sine sheet must not be shown as exact — it already carries 3.642e-02 at `dt = 0.1`. Blocks T5 because shipping a nonlinear block without the disclosure puts users on a different accuracy regime silently |
| T5 Saturation block | T4 | First step block. Numeric check against the analytic response of a saturating first-order loop; refusal test that nothing linearizes |
| T6 Dead Zone, Abs, Sign, Product, Min/Max | T5 | Remaining memoryless nonlinearities; Product exercises `variadic` through the step path |
| T7 Sample-time parameter and the integer-multiple rule | T3 | The `Ts` field, the rounding tolerance, the refusal message naming both numbers and the nearest legal value |
| T8 Unit Delay | T7, T4 | Smallest stateful discrete block; exercises hold-between-updates at `N > 1`. Depends on T4 for the same reason T5 does: it is a step block, so it creates segments |
| T9 Discrete Transfer Function and Discrete State-Space | T8 | Realized as a discrete `controlsys.System` at `Ts`, stepped with `Simulate` |
| T10 Port-list arity, then Switch and Relay | T6 | Extends `inputArity` past none/one/variadic without adding a fourth enum value |
| T11 Logic and comparison blocks | T10 | States the 0/1 boolean convention in the docs |
| T12 Spike: nonlinear feedback | T8 | Whether and how a cycle crossing a step boundary is ordered, solved, or refused without regressing supported LTI feedback |

## What dependent tasks must know

- **The linear path is not being changed.** Any task touching `simulate.go`
  keeps `ConnectByName` plus a batch `Lsim` for a single-segment sheet. If a
  change makes a purely linear sheet take a different arithmetic path, it is
  wrong regardless of how close the numbers come out.
- **The catalog stays the single authority.** A kind is a step block because
  its definition sets `step`, and nowhere else states it.
- **Nothing calls `controlsys.Linearize`**, even though it computes correct
  Jacobians. A linearized saturation is not a saturation, and swapping one for
  the other is what the no-silent-linearization rule forbids.
- **Segments are step-depth classes, not connected components.** The
  connected-component rule produces a cyclic segment graph on 26.0% of all
  six-block sheets, including a Saturation on one branch of a Sum.
- **Pure-LTI feedback stays inside a controlsys-composed segment.** The DAG
  proof applies after condensation; it is not permission to reject a cyclic
  signal graph or to split its loop across independently stepped segments.
- **A segment's boundary channels are its edges leaving the segment**, not
  its edges touching a step block. Segment-to-segment edges exist on 44.1% of
  six-block sheets, and enumerating only the step-block edges silently
  compiles a variadic block with too few inputs.
- **"Exact" means exact for piecewise-constant sources.** A Sine sheet is
  already stair-stepped by `Lsim` and is not on the exact path today. Do not
  write interface copy that promises otherwise.
- **Nonlinear blocks do not arrive before the driver or the disclosure.**
  Rows 2 through 6 of the family table are blocked on T3, and every task that
  registers a step block is additionally blocked on T4.
- **State-Space and MIMO are not blocked by any of this** and can be
  scheduled independently whenever the editor gains matrix fields.

Unresolved questions: which step-boundary cycles are ordered, solved, or
refused; whether sample-time offset is ever wanted; whether the fidelity
summary belongs in the stored run JSON or is recomputed for display; and
whether a Sine source should gain a first-order-hold option, since the
measurement above shows it, not the segment cut, is the largest error on a
sheet that mixes the two.
