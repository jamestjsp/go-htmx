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
Initial output as approximation-state coordinates.

The executable fixture derives continuous and discrete State-Space responses
from the documented equations and Transport Delay values from its documented
history behavior. These analytic values are not MATLAB or Simulink output.

## Fixture layout

Versioned fixtures live under
`internal/studio/testdata/simulink/r2026a`. Each JSON file contains the
traceability record and the model inputs needed by its test. Tests reject
fixtures with a different release, an unapproved oracle label, incomplete
source attribution, or language that presents generated values as MATLAB
output.
