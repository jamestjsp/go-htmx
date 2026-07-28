# Nonlinear linearization and EKF workflows

Process Lab treats nonlinear callbacks as analysis and estimation definitions,
not as block-diagram simulation engines. The normal flowsheet compiler remains
LTI. Registering a nonlinear definition cannot add or replace a block.

## Stable definitions and runtime callbacks

`Studio.RegisterNonlinearDefinition` binds:

- a stable key and positive version;
- ordered state, input, and output names;
- continuous dynamics and output callbacks for linearization;
- discrete transition, measurement, and analytic Jacobian callbacks for EKF.

The definition metadata is persisted. A key/version cannot later acquire
different metadata; publish the changed definition under a new version.
Callbacks are process-local and must be registered again after a process
restart. Attempting to execute persisted metadata without its runtime callbacks
returns an explicit registration error.

## Operating-point linearization

`Studio.LinearizeNonlinear` accepts a named operating point, an equilibrium
tolerance, and explicit state/input perturbation directions. Studio evaluates
and reports the continuous-dynamics equilibrium residual before invoking
`controlsys.Linearize`. A point outside its tolerance is refused.

The returned `NonlinearLinearizationCandidate` owns:

- the definition and operating-point snapshot;
- equilibrium residual, norm, and operating output;
- the named `controlsys.System`;
- runtime-registration and creation provenance;
- full-radius and half-radius directional errors, including quadratic ratios.

It is candidate-only. There is deliberately no apply operation and no implicit
conversion into a flowsheet block.

## Extended Kalman filtering

`Studio.RunNonlinearEKF` owns a complete batch predict/update run. Its estimator
definition includes the initial named state and the `Q`, `R`, and `P0`
covariances. Studio validates dimensions, finite values, symmetry, and positive
semidefiniteness before constructing `controlsys.EKF`.

Every batch row contains one input and one measurement. The result records the
predicted and updated state and covariance for every row, plus final state,
names, and callback provenance. Batches are limited to 10,000 rows.

This workflow estimates state from supplied samples. It does not generate a
nonlinear trajectory or claim that the flowsheet can simulate nonlinear
feedback.
