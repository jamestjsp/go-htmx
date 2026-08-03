# Nonlinear linearization and EKF workflows

Process Lab treats nonlinear definitions as analysis and estimation models,
not as block-diagram simulation engines. The normal flowsheet compiler remains
LTI. Registering a nonlinear definition cannot add or replace a block.

## Stable definitions and expressions

`Studio.RegisterNonlinearDefinition` persists one complete, immutable definition:

- a stable key and positive version;
- ordered state, input, and output names, all valid Go identifiers;
- one expression per state derivative in `dynamics`;
- one expression per output in `outputs`;
- an optional positive `sampleTime` and optional `integrationSteps` for EKF.

For example:

```json
{
  "ref": {"key": "models/pendulum", "version": 1},
  "name": "Pendulum",
  "stateNames": ["angle", "rate"],
  "inputNames": ["torque"],
  "outputNames": ["angle"],
  "dynamics": ["rate", "-sin(angle) - 0.1*rate + torque"],
  "outputs": ["angle"],
  "sampleTime": 0.01,
  "integrationSteps": 4
}
```

Expressions permit numeric literals, declared signal names, `pi`, `e`, unary
`+`/`-`, binary `+`, `-`, `*`, `/`, and the functions `sin`, `cos`, `tan`,
`asin`, `acos`, `atan`, `atan2`, `sinh`, `cosh`, `tanh`, `exp`, `log`, `log10`,
`sqrt`, `abs`, `pow`, `min`, and `max`. Use `pow(x, 2)` for powers; `^` is
rejected because Go parses it as bitwise XOR. Selectors, indexing, strings, and
other syntax are rejected at registration.

A key/version cannot later acquire a different definition; publish the changed
definition under a new version. The stored expression document is executable
after a process restart: RK4 derives the discrete transition, and central finite
differences derive both EKF Jacobians. No callback registration or process-local
runtime state is required.

`integrationSteps` defaults to 1 when omitted. `sampleTime` is optional for
linearization and required by EKF. An output that
references an input is valid for linearization, where it contributes direct
feedthrough to `D`, but is refused as an EKF measurement because EKF measurement
functions depend on state alone.

## Operating-point linearization

`Studio.LinearizeNonlinear` accepts a named operating point, an equilibrium
tolerance, and explicit state/input perturbation directions. Studio evaluates
and reports the continuous-dynamics equilibrium residual before invoking
`controlsys.Linearize`. A point outside its tolerance is refused.

The returned `NonlinearLinearizationCandidate` owns:

- the definition and operating-point snapshot;
- equilibrium residual, norm, and operating output;
- the named `controlsys.System`;
- stored definition-creation and candidate-creation provenance;
- full-radius and half-radius directional errors, including quadratic ratios.

It is candidate-only. There is deliberately no apply operation and no implicit
conversion into a flowsheet block.

## Extended Kalman filtering

`Studio.RunNonlinearEKF` owns a complete batch predict/update run. Its estimator
definition includes a name, the initial named state, and the `Q`, `R`, and `P0`
covariances. The CLI accepts that JSON document with `--estimator`; the
definition reference from `--definition` supplies its model. For example:

```json
{
  "name": "pendulum EKF",
  "initialState": [0, 0],
  "processNoise": {"rows": 2, "columns": 2, "values": [0.001, 0, 0, 0.001]},
  "measurementNoise": {"rows": 1, "columns": 1, "values": [0.01]},
  "initialCovariance": {"rows": 2, "columns": 2, "values": [1, 0, 0, 1]}
}
```

Pass it beside the sample stream:

```bash
processlab nonlinear ekf --definition models/pendulum@1 --estimator estimator.json < samples.tsv
```

If `--estimator` is omitted, the CLI uses identity `Q`, `R`, and `P0` with a
zero initial state and reports that default on stderr. Studio validates
dimensions, finite values, symmetry, and positive semidefiniteness before
constructing `controlsys.EKF`.

Every batch row contains one input and one measurement for each declared signal.
The CLI binds TSV columns by their `time`, input, and output header names, so
column order may differ from the definition. Extra columns are ignored, which
allows direct piping from `sim run`; missing declared signals are refused. The
result records the predicted and updated state and covariance for every row,
plus final state, names, and definition provenance. Batches are limited to
10,000 rows.

This workflow estimates state from supplied samples. It does not generate a
nonlinear trajectory or claim that the flowsheet can simulate nonlinear
feedback.
