# State feedback, estimation, and observer regulators

Process Lab routes state-space synthesis through three intent-level Studio
operations:

- `DesignStateFeedback` for LQR/DLQR, LQI, LQRD, Acker, and Place.
- `DesignEstimator` for LQE, continuous/discrete Kalman, Kalmd, and observer
  pole placement.
- `DesignObserverRegulator` for explicit Reg construction and LQG.

All three operations use the selected state-space plant role. Before synthesis
they reject zero-state, descriptor, or delayed models and report
controllability rank, observability rank, stabilizability, and detectability.
State, control, measurement, cost, covariance, noise, gain, and pole dimensions
are validated before calling `controlsys`. Cost and covariance matrices also
receive explicit symmetry and positive-semidefinite/positive-definite checks.

Continuous plants route to CARE-based LQR/LQI/LQE and continuous Kalman.
Discrete plants route to DARE-based DLQR and Kalman. Discrete LQI uses the
explicit augmented model with accumulating integral states. LQRD and Kalmd
return sampled candidates with a visible limitation: Process Lab does not
apply them into an authored continuous feedback cycle until sampled-loop
execution is modeled explicitly.

## Named authored controllers

Direct state feedback requires the selected measurements to be a complete
state permutation with zero direct feedthrough. The resulting Matrix Gain is
authored as `u=-Kx`, using measurement names as inputs and plant control names
as outputs.

LQI candidates contain named integral states and separate reference and
full-state measurement inputs. Estimator candidates use collision-free names:

- inputs: `command.<u>`, `measurement.<y>`
- outputs: `estimate-output.<y>`, `estimate-state.<x>`
- states: `estimate-state.<x>`

Reg and LQG candidates take named plant measurements and produce named plant
controls. Their negative control law is explicit. `ControllerRole` therefore
stores a feedback convention: `external_negative` for ordinary positive
controllers and `signed_control_law` for authored `u=-Kx` or observer
regulators. The role compiler normalizes either convention before building the
negative-feedback analysis loop.

## Candidate safety

Candidate generation does not mutate the flowsheet. Matrix Gain and
State-Space candidates can replace an existing compatible controller block
through `ApplyStateDesignCandidate`. Apply validates the exact source model
revision, target flow, target block kind, complete replacement parameters, and
wired port compatibility in one transaction. A second or stale apply is
refused.

The returned evidence includes gains, Riccati solutions, reciprocal condition
estimates, independently recomputed controller and estimator poles, named
channel/state contracts, diagnostics, and sampled-workflow warnings.
