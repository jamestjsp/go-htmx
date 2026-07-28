# Named robust synthesis

Process Lab treats H2 and H-infinity synthesis as a named generalized-plant
workflow, not as positional matrix form fields.

The assigned plant roles define the partition order:

- exogenous inputs followed by control inputs;
- regulated outputs followed by measurement outputs.

Studio verifies that every partition is non-empty, builds that exact
generalized plant, and passes only the derived measurement and control counts
to `controlsys.H2Syn` or `controlsys.HinfSyn`. Synthesis is limited to
continuous, standard, delay-free generalized plants. Descriptor and delayed
models are refused before the dependency can discard their semantics.

Each candidate retains the source model revision, normalized role fingerprint,
partition names, Riccati solutions and conditioning, closed-loop poles,
achieved norm, solver gamma where applicable, and warnings. The synthesized
controller is independently reconnected with a lower LFT and checked for
dimensions, channel names, and stability. H-infinity candidates retain both the
solver gamma and the measured closed-loop norm when numerical tolerances differ.

The controller follows the signed-control-law convention returned by robust
synthesis. Review normalizes that sign for the common current-versus-candidate
loop comparison. Application is available only when the authored controller is
one continuous State-Space block; it uses the shared atomic, role- and
revision-checked apply/undo authority.

The Studio boundary converts dependency panics into attributable synthesis
errors. Tests cover that containment, the published two-state generalized
plant, an independent continuous Lyapunov H2 oracle, a dense complex
singular-value H-infinity oracle, non-mutation, apply/undo, and explicit
descriptor, discrete, and delay refusals.
