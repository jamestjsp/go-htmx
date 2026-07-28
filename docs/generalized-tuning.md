# Generalized controller tuning

`Studio.TuneController` is the design boundary around controlsys generalized
models and bounded tunable blocks. It does not edit the flowsheet.

## Parameter authority

Each `TunableParameterSpec` identifies one authored value by block ID and
field, plus matrix or coefficient indices where required. The current value
comes only from the stored block. The request supplies finite lower and upper
bounds; it cannot provide a second initial value.

Stable parameter paths cover:

- scalar Gain;
- Matrix Gain entries;
- PID proportional, integral, and derivative gains;
- continuous and discrete transfer numerator coefficients;
- MIMO transfer numerator coefficients;
- continuous and discrete state-space `A`, `B`, `C`, and `D` entries.

Unselected values become fixed controlsys `TunableReal` values. Selected
values retain the exact reverse mapping needed to update the authored block.

## Goals and search

Requests can combine controlsys goals for:

- tracking;
- rejection;
- sensitivity;
- weighted gain;
- loop shape;
- gain/phase margin;
- pole location;
- overshoot.

Frequency goals own an explicit angular-frequency grid. Every goal can name a
stored analysis point. Candidate evidence includes the measured value, limit,
normalized violation, controlsys diagnostics, and an explicit failure
message.

Grid size is bounded before controlsys is called: 2–50 samples per parameter,
at most eight parameters, and at most 100,000 Cartesian evaluations.

Controlsys 1.2.0 implements `Systune` and `Looptune` by delegating to
`GridTune`. Process Lab preserves the requested algorithm label but records
the actual `cartesian-grid` method and a warning. It does not call the result a
continuous optimum or the best of all feasible zero-score candidates.

## Immutable candidates and atomic apply

A candidate records:

- flowsheet and source model revision;
- requested algorithm and actual search method;
- pass, score, and iteration count;
- previous, candidate, and bound values;
- per-goal evidence;
- candidate controller and closed-loop systems.

The diagram remains untouched until `Studio.ApplyTuningCandidate`. Apply
compares the exact source revision, loads every target block, writes cloned
parameters, revalidates block and wired-port dimensions, and commits all
changes with one model event. Any error rolls the transaction back. Applying
the same candidate after a model edit is refused as stale.

For a discrete plant, a neutral static controller inherits the plant sample
time before generalized-loop assembly, and the tunable controlsys block uses
that same `Dt`.
