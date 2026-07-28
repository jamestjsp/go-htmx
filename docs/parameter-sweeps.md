# Parameter sweeps

This document defines the bounded `controlsys.ModelArray` workflow implemented
in `internal/studio/parameter_sweep.go`. It is an analysis seam only: it does
not add persistence, HTTP handlers, or UI.

## Contract

A sweep begins with one `SweepModelSource`:

- a nonempty source model name;
- the current authored model revision;
- the authored `Parameters`;
- a parameter setter that recognizes full parameter names;
- a compiler that receives the generated variant name and cloned parameters.

`SweepSpec.SourceModelRevision` must exactly match the source revision. A stale
request is refused before any model is compiled.

Every ordered `SweepAxis` has:

- one unique, nonempty parameter name;
- an explicit unit, using `1` for dimensionless values;
- one or more distinct finite values in authored order.

The workflow deep-clones the authored parameters for every coordinate, applies
the axes in specification order, generates a name containing every parameter,
value, and unit, and compiles that named variant. The compiler receives a
second clone, so it cannot mutate either the source parameters or the retained
variant parameters.

For example:

```text
source: lag
gain [1]:          [1, 3]
time_constant [s]: [0.5, 2]
```

materializes:

```text
flat  coordinates  name
0     [0, 0]       lag [gain=1 1, time_constant=0.5 s]
1     [0, 1]       lag [gain=1 1, time_constant=2 s]
2     [1, 0]       lag [gain=3 1, time_constant=0.5 s]
3     [1, 1]       lag [gain=3 1, time_constant=2 s]
```

The indexing is row-major with the last axis varying fastest. For shape
`[d0, d1, ..., dn]`, the flat index is

```text
i0*(d1*...*dn) + i1*(d2*...*dn) + ... + in
```

Axis values are never sorted. Their declared order is model-grid metadata.

## Compatibility preflight

All variants are compiled before `controlsys.NewModelArray` is called, then a
stricter family preflight is applied. Every model must:

- be nonnil and pass `System.Validate`;
- use ordinary explicit state space, because the current bounded step path is
  not descriptor-aware;
- have identical state, input, and output dimensions `(n, m, p)`;
- have the same continuous/discrete domain and exactly the same `Dt`;
- provide complete, nonempty, unique input, output, and state names;
- have exactly the same input, output, and state names as the first model.

This intentionally closes two permissive behaviors in `ModelArray` itself:
the library allows different state counts, and it treats an empty channel-name
slice as compatible with a named model. Those behaviors are not safe for
attributed web results.

Only after preflight succeeds is the array constructed with the ordered axis
shape. `ModelArray.Model(indices...)` and each result slice therefore use the
same last-axis-fastest coordinate mapping.

## Bounded analysis

`AnalyzeParameterSweep` evaluates both analyses through the `ModelArray` batch
operations:

- `ModelArray.FreqResponse` evaluates every model at the explicit frequency
  grid;
- `ModelArray.Step` evaluates every model through the requested final time.

The hard limits are:

| Resource | Limit |
| --- | ---: |
| Sweep axes | 4 |
| Materialized models | 64 |
| States per model | 64 |
| Input or output channels | 8 per axis |
| Frequency points | 256 |
| Step samples per model | 2,000 |
| Complex frequency-response values across the family | 1,000,000 |

Frequency grids require 2–256 positive, finite, strictly increasing rad/s
values. A discrete family is also limited to its Nyquist frequency.

The time bound is checked before `ModelArray.Step` allocates responses. For a
discrete family it uses `floor(tFinal/Dt)+1`. For a continuous family it
reproduces the automatic grid selected by the pinned `controlsys` step
planner, including its pole-based step size. A request that would exceed
2,000 samples for any variant is refused for the whole family.

## Summaries and attribution

Raw bounded responses remain available to the internal caller. In addition,
every variant receives deterministic summaries:

- frequency: the peak largest singular value and its frequency;
- time: the maximum absolute step output, its time, input, output, and sample
  count.

For SISO models, the largest singular value is `abs(H(jw))`. For MIMO models,
it is the matrix 2-norm from the complex SVD, not the largest individual
element. The family worst case retains the complete variant name, flat index,
and axis coordinates. Equal values resolve to the first variant or frequency
in declared order.

These are screening metrics, not a claim that one scalar defines robust
performance. A future UI should label the metrics and preserve access to the
per-model responses.

## Independent verification

`internal/studio/parameter_sweep_test.go` provides persistent checks for:

- a two-axis `K/(1+s*tau)` family, including exact coordinate order, variant
  names, deep-clone isolation, analytic frequency response, and analytic step
  response;
- a static 2x2 MIMO family whose worst case is determined by singular value;
- refusal of dimension, domain/sample-time, channel-name, and missing-name
  incompatibilities before `ModelArray` construction;
- stale revision, missing unit, nonfinite value, model-count, frequency-count,
  and time-sample refusals;
- benchmark cases that independently scale model count, state count, and
  frequency count.

Unresolved questions: none
