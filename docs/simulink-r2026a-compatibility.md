# Simulink R2026a compatibility

Process Lab compares block behavior with the official MathWorks online
documentation for Simulink R2026a. The target is the documented simulation
contract, not every code-generation, data-type, tuning, or user-interface
feature.

Compatibility work is additive. It must preserve the existing workbench,
saved-diagram schema, and delay-free simulation path unless a later migration
explicitly changes one of those contracts.

## Reference authority

Every compatibility fixture records:

- `release`: `R2026a`;
- the MathWorks page title, stable online URL, and relevant section;
- the Process Lab block or execution behavior being compared;
- the supported subset and intentional deviations;
- the origin of its expected values.

The initial delay fixtures use these R2026a references:

- [Time Delays in Linear Systems](https://www.mathworks.com/help/control/ug/time-delays-in-linear-systems.html),
  especially “Transport Delay in MIMO Transfer Function” and
  “Discrete-Time Transfer Function with Time Delay”;
- [Transport Delay](https://www.mathworks.com/help/simulink/slref/transportdelay.html);
- [lsim](https://www.mathworks.com/help/control/ref/dynamicsystem.lsim.html).

The online pages do not carry the release in their stable URLs. The fixture
therefore pins `R2026a` explicitly and records the date on which the page was
checked.

## Oracle provenance

Fixtures label expected results with one of these values:

- `mathworks-example-data`: numeric values published by the referenced example;
- `mathworks-formula-analytic`: values derived from a documented equation or
  behavior using an analytic calculation;
- `mathworks-semantics-controlsys`: values computed independently by the pinned
  `controlsys` backend for semantics defined by the referenced documentation.

Analytic and `controlsys` values are generated reference values. They are not
MATLAB or Simulink output and must not be described as such.

## Initial MIMO delay subset

For a continuous MIMO transfer model, MathWorks defines `IODelay` as an
output-by-input matrix: entry `(i,j)` delays the contribution from input `j` to
output `i`. Process Lab maps the MIMO Transfer Function block’s Pairwise delays
matrix to that contract.

The first executable fixture uses a two-input, two-output transfer matrix with
one denominator per output row, distinct delays on all four paths, constant
named inputs, and named vector outputs. Its response is checked through the
public Studio run and persisted result series against independently calculated
shifted step responses.

This slice intentionally retains Process Lab’s current requirements that exact
continuous delays align with the run sample time and discrete delays are integer
sample counts. It does not add vector Transport Delay parameters, initial
history, interpolation, new editor fields, or schema migrations.

## Direct scalar state subset

The next fixture group covers direct Step, Integrator, and Unit Delay block
semantics that do not change block topology:

- Step uses the documented scalar defaults: step time 1 second, initial value
  0, and final value 1.
- Integrator supports a finite scalar internal initial condition and otherwise
  retains Process Lab's existing continuous LTI execution.
- Unit Delay supports a finite scalar initial output for the first sample
  period.

This subset does not add reset, saturation, external initial-condition ports,
state ports, vector state, or solver configuration. Unit Delay also retains
Process Lab's explicit 0.1 second new-block sample-time default until analysis
requests can resolve an inherited rate from an explicit model-level base step.

Transfer Function remains a zero-initial-state block. This matches the
MathWorks contract, which directs nonzero transfer-function initial conditions
to a State-Space realization rather than assigning physical meaning to an
arbitrary transfer-function state.

New parameter JSON carries a schema version so an authored zero remains
distinct from an absent legacy field. Unversioned saved blocks continue to
receive their historical defaults, including a zero-time Step, while newly
created Step blocks receive the R2026a one-second default.

## State-space and transport-history subset

Continuous and discrete State-Space blocks use the documented direct-block
matrix defaults `A = B = C = D = 1` and an internal initial condition of zero.
For multi-state models, Process Lab accepts either one finite value, broadcast
to every state, or one finite value per row of `A`. Initial states are assembled
by the compiler in the same realization order used for scalar stateful blocks.

The Discrete State-Space block retains Process Lab's explicit 0.1 second
new-block sample time. Simulink defaults this parameter to inherited (`-1`),
which Process Lab cannot represent until model-level inherited-rate analysis is
available. Named input, output, and state channels also remain a
Process-Lab-specific extension.

Exact scalar Transport Delay supports the documented finite Initial output
value. The value supplies constant pre-simulation history until the aligned
delayed input becomes available. The default remains zero, so existing models
keep the same realization, delay-aware driver selection, and no-delay fast
path. Padé and Thiran are explicit approximation modes and do not reinterpret
Initial output as approximation-state coordinates; they reject a nonzero value.

The executable fixture derives continuous and discrete State-Space responses
from the documented equations and Transport Delay values from its documented
history behavior. These analytic values are not MATLAB or Simulink output.

## PID and PID2 parallel-form subset

PID Controller and 2-DOF PID Controller blocks expose the R2026a parallel-form
parameters `P`, `I`, `D`, and derivative filter coefficient `N`. Process Lab
maps them to controlsys's equivalent filter time at the adapter boundary with
`Tf = 1/N`; saved models and controller-design candidates retain `N` as the
public value. Existing saved blocks authored with `filterTime` are migrated to
the equivalent coefficient when loaded.

The supported continuous controller is
`P + I/s + D*N*s/(s+N)`. The discrete controller uses the documented Forward
Euler integration and filter methods at Process Lab's explicit sample time.
PID2 adds the documented proportional and derivative setpoint weights:
`P*(b*r-y) + I/s*(r-y) + D*N*s/(s+N)*(c*r-y)`.

New blocks use the documented direct defaults `P=1`, `I=1`, `D=0`, and
`N=100`; PID2 also uses `b=c=1`. Process Lab's single parallel-form editor
represents P, I, PI, PD, and PID controller types by setting unused gains to
zero instead of adding a separate type menu. It does not yet expose Ideal form,
unfiltered derivatives, inherited discrete sample time, saturation,
anti-windup, reset, tracking, or external parameter ports.

The executable PID fixture checks direct defaults, continuous public time and
frequency responses, discrete Forward Euler frequency response, and both PID2
input paths against equations in the official R2026a block documentation.
These analytic values are not MATLAB or Simulink output.

## Sum, direct vectors, and named routing

The direct Sum counterpart uses the documented default `+` sign and accepts
either a `+`/`-` list or a positive numeric input count. Multi-input vectors of
one authored width are added or subtracted elementwise. A one-port vector uses
the documented Sum-of-elements behavior and produces a scalar. Process Lab
authors width explicitly because it does not yet have diagram-wide inherited
dimension propagation. Mixed scalar/vector expansion, matrices, sign-list
spacers, and specified-dimension reduction remain outside this subset.

Unit Delay accepts the same explicit scalar or vector width. Its scalar initial
condition broadcasts across the vector unless a one-value or width-matched
vector initial condition is authored. The existing inherited-rate mode uses the
public run step and is unchanged. Saved Sum and Unit Delay blocks without a
width remain scalar, so opening an existing diagram does not change its ports
or response.

Vector Sum, Vector Constant, Matrix Gain, Mux, Demux, Selector, Permutation, and
Vector Scope remain intentional Process-Lab-specific named-channel
specializations. Their channel labels are authored routing metadata rather than
an assertion of direct Simulink block parity. Vector Sum keeps elementwise
behavior even with one input; users choose direct Sum when they need documented
one-input reduction.

The executable fixture checks scalar Sum, numeric input-count shorthand,
elementwise vector Sum, one-input vector reduction, and vector Unit Delay
history through public Studio operations. Expected values come from the
documented Sum and Unit Delay equations and are not MATLAB or Simulink output.

## Fixture layout

Versioned fixtures live under
`internal/studio/testdata/simulink/r2026a`. Each JSON file contains the
traceability record and the model inputs needed by its test. Tests reject
fixtures with a different release, an unapproved oracle label, incomplete
source attribution, or language that presents generated values as MATLAB
output.
