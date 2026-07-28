# controlsys capability coverage

Process Lab pins `github.com/jamestjsp/controlsys v1.2.0`. This matrix records
where the application uses each practical package family and, just as
importantly, where it deliberately does not expose a package primitive as a
product operation.

Status meanings:

- **Browser** — available in the Docker-served application.
- **Studio** — a bounded Go workflow with persisted, runnable tests and
  serializable evidence; it is intended for an API or future UI rather than a
  thin form today.
- **Deferred** — evaluated and rejected for now, with the limiting contract
  stated.
- **Non-goal** — a low-level numerical or developer primitive that should stay
  behind a higher-level workflow.

## Modeling, algebra, delay, and simulation

| controlsys family | Status | Process Lab workflow and evidence |
| --- | --- | --- |
| State-space, SISO transfer function, MIMO transfer function, ZPK, gain, and FRD construction | **Browser** | Library blocks compile to named systems in `catalog.go`; analytic MIMO equivalence is covered by `rich_lti_test.go`. FRD remains frequency-domain-only. |
| Named inputs, outputs, and states | **Browser** | Every rich model and vector-routing block owns complete names. Port schemas reject width/name mismatches before compilation. |
| Series, parallel, sums, branches, selections, permutations, and feedback | **Browser** | The flowsheet compiler assembles one arbitrary named graph with `ConnectByName`. This is the product form of block-diagram algebra; users are not limited to positional `Series` calls. |
| `Append`, `BlkDiag`, connection matrices, and `SumBlk` | **Non-goal** | These are alternate programmatic encodings of the named graph. Exposing both would create two authorities for wiring and signal order. |
| Lower LFT | **Browser/Studio** | Exact-delay feedback and generalized-plant validation use LFT semantics. H2/H∞ candidates independently rebuild their generalized closed loop with `LFT`. Upper-LFT authoring is deferred until an uncertainty-block model exists. |
| Exact input/output and internal delays | **Browser** | Transport Delay and per-channel MIMO delays retain exact metadata. Static paths use exact shifts; dynamic feedback uses delay-preserving LFT assembly. |
| Padé and Thiran approximations | **Browser** | The delay block makes approximation choice and order explicit. Thiran is restricted to valid sampled-data configurations. |
| Smith predictor and delay-safe feedback helpers | **Deferred** | Exact delayed plants already simulate and analyze, but a Smith candidate needs a named plant-model mismatch contract and the shared controller review/apply lifecycle. It is not silently substituted for ordinary feedback. |
| C2D, ZOH, FOH, matched, impulse-invariant, and delay conversion policy | **Browser** | Discretized Transfer and sampled execution route explicit conversion choices through controlsys. |
| D2C and D2D | **Deferred** | Process Lab never silently changes an authored discrete model or its rate. A future conversion candidate must retain aliasing and resampling evidence. |
| `Lsim`, step response, and sampled execution | **Browser** | Continuous, discrete, MIMO, and exact-delay execution preserve names, sample plans, and result metadata. |
| Impulse and initial-condition response | **Deferred** | The result schema is ready for multichannel traces, but the current source-block model has no authored initial-state contract. |
| Random models and generic signal generators | **Non-goal** | Deterministic source blocks and explicit test fixtures own reproducibility. |

## Analysis and control design

| controlsys family | Status | Process Lab workflow and evidence |
| --- | --- | --- |
| Poles, zeros, DC gain, damping, stability, and step information | **Browser** | Dynamics analysis returns named evidence and refuses finite-order claims that an exact internal delay makes incomplete. |
| Bode, Nyquist, Nichols, singular values, and FRD frequency response | **Browser** | Frequency analysis supports named SISO and MIMO selections and enforces discrete Nyquist bounds. |
| Classical, all, and disk margins; bandwidth; root locus | **Browser** | Loop analysis uses the selected named loop. Root locus is SISO-only and reports that limitation. |
| Loop sensitivity (`Si`, `So`, `Ti`, `To`) and candidate comparison | **Browser through controller review** | PID, LQG, and robust candidates are compared on a common named grid before apply. The deeper `AnalyzeLoopRobustness` Studio operation remains reusable without duplicating another UI. |
| PID/PID2 and `Pidtune` | **Browser** | All controlsys PID types are candidate-only until reviewed; PID2 retains explicit reference, measurement, and control roles. |
| LQR/DLQR/LQI/LQRD, pole placement, LQE/Kalman/KALMD, LQG, estimator, and regulator assembly | **Browser/Studio** | LQG is exposed in Controller design; the full state-design API retains controllability, observability, stabilizability, detectability, gains, and pole evidence. |
| Tunable gains, PID, transfer functions, state space, tuning goals, `GridTune`, `Systune`, and `Looptune` | **Studio** | `TuneController` binds bounds to exact authored fields, caps the Cartesian search, records truthful method diagnostics, and uses atomic candidate apply. A generic browser form is deferred because positional matrix fields would undermine the named-authority model. |
| H2 and H∞ synthesis | **Browser** | Named exogenous, regulated, measurement, and control partitions derive from control roles. The browser uses the shared read-only review, atomic apply, and one-use undo lifecycle. |
| CARE/DARE, Lyapunov equations, Gramians, staircase forms, canonical transforms, balancing transforms, and state permutations | **Non-goal** | These are numerical building blocks. Product workflows expose the control-design, diagnosis, or reduction intent and retain the relevant evidence instead of exposing raw coordinate operations. |

## Identification, nonlinear, reduction, and model families

| controlsys family | Status | Process Lab workflow and evidence |
| --- | --- | --- |
| Operating-point linearization | **Studio** | Persisted nonlinear definitions bind versioned runtime callbacks. Linearization requires equilibrium and returns local directional-error evidence without mutating a flow. |
| EKF | **Studio** | The batch workflow validates dimensions and finite symmetric PSD covariance inputs and retains state/covariance evidence. Browser authoring is deferred because executable nonlinear callbacks cannot be safely uploaded as form data. |
| Frequency-response estimation H1/H2 | **Studio** | Explicit channel units, sample time, train/validation ranges, training-only preprocessing, window enum, excitation rank, coherence, and held-out fit are serializable with the candidate. |
| ERA | **Studio** | Explicit order and training split produce a named discrete realization with the complete HSV sequence and held-out Markov evidence. |
| FRD import, series, parallel, feedback, margins, and passivity | **Studio** | Exact grids, sample time, time unit, channel names, and connected units are checked before controlsys algebra. |
| Minimal realization, staircase reduction, balancing, balanced/state/modal reduction | **Studio** | `ModelStudy.Reduce` returns a non-mutating candidate with names, poles, stability, dense frequency error, HSVs, and the balanced-truncation discarded-HSV bound. |
| Stability decomposition | **Studio** | Stable and unstable parts retain names and independently verify `G = Gs + Gu`. |
| H2/H∞ norms, covariance, and sampled passivity | **Studio** | Independent Lyapunov, dense-frequency, covariance-residual, and Hermitian-eigenvalue checks support the evidence. A sampled pass is never labeled an analytic certificate. |
| Model arrays and parameter sweeps | **Studio** | `MaterializeParameterSweep` and `AnalyzeParameterSweep` enforce explicit revision, ordered unit-bearing axes, strict model compatibility, deterministic coordinates, and hard web-process bounds before `ModelArray`. |
| Spectral factorization | **Deferred** | It requires a passivity/positive-real certification and factor-acceptance workflow; sampled passivity alone is intentionally insufficient. |
| Descriptor systems and physical assembly | **Deferred** | The physical-assembly spike verifies equations, poles, frequency response, binding order, and current singular/delay limitations. Production authoring waits for descriptor-aware simulation and an explicit across/through vocabulary. |

## Persistence boundary

Authored blocks, connections, control roles, nonlinear definitions, simulations,
and the three browser analysis intents persist in SQLite. Controller candidates
are intentionally short-lived revision capabilities: applying one persists the
authored controller, while unapplied candidates and their one-use undo tokens
expire with the process or model revision.

Identification, reduction, and sweep candidates are JSON-safe Studio values,
but Process Lab does not yet claim them as durable project artifacts. A future
artifact table must version payload schemas, retain source revisions, and
define stale/replay semantics before a browser upload UI is added.

## Runnable representative fixtures

The following commands run the same named project fixtures used as acceptance
oracles. They create isolated SQLite projects where the workflow is
flow-backed; no external service or network access is required.

| Representative project | Command |
| --- | --- |
| SISO PID-controlled plant | `go test ./internal/studio -run '^TestPIDFeedbackLoopMatchesControlsysFeedback$' -count=1` |
| Named MIMO models and algebra | `go test ./internal/studio -run '^(TestRichLTIRepresentationsMatchIndependentMIMOOracle|TestBuildControlModelsPreservesNamedMIMOOrdering)$' -count=1` |
| Exact-delay plant and delayed feedback | `go test ./internal/studio -run '^(TestExactTransportDelayShiftsStepAndSineOnAlignedGrid|TestExactTransportDelayUsesControlsysLFTInFeedback)$' -count=1` |
| Discrete MIMO and explicit sampled conversion | `go test ./internal/studio -run '^(TestDiscreteStateSpaceMatchesMIMODifferenceEquation|TestExplicitC2DMethodsMatchDirectControlsysConversions)$' -count=1` |
| Bounded generalized tuning | `go test ./internal/studio -run '^TestTuneControllerFindsBoundaryCandidateWithoutMutatingThenAppliesAtomically$' -count=1` |
| Identified FRD and ERA models | `go test ./internal/studio -run '^(TestIdentificationWorkflowRecoversNoisySISOFrequencyResponse|TestIdentificationWorkflowERAUsesHeldOutMarkovParameters)$' -count=1` |
| Reduced and decomposed models | `go test ./internal/studio -run '^(TestModelStudyBalancedTruncationCarriesDiscardedHSVBound|TestModelStudyStabilitySeparationPreservesPolesAndTransfer)$' -count=1` |
| H2/H∞ generalized plant | `go test ./internal/studio -run '^TestRobustSynthesisUsesNamedPartitionsAndIndependentNormOracles$' -count=1` |
| Named model-array sweep | `go test ./internal/studio -run '^(TestParameterSweepTwoAxisOrderingAndAnalyticResponses|TestParameterSweepMIMOStaticWorstCaseUsesLargestSingularValue)$' -count=1` |

The Docker browser acceptance additionally creates and leaves a
`PID controlled plant` flowsheet in the persistent Compose volume, runs it,
and verifies that vendored HTMX drives controller candidate generation without
a CDN.

## Numerical and performance truth

No integration test uses `t.Skip`, `Skipf`, or conditional success masking.
Analytic and independent numerical oracles are preferred to comparing one
controlsys headline result with another. Dense frequency sweeps used for H∞
checks are explicitly lower-bound evidence, not certified global norms.

Model-study and parameter-sweep benchmarks are measurements for the pinned
platform. Hard resource caps—rather than unstable wall-clock assertions—are
the production performance gate: 64 models, 64 states per model, eight inputs
or outputs, 256 frequencies, 2,000 step samples per model, and one million
complex family-response values.
