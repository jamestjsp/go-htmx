# Guided PID and PID2 design

Process Lab exposes `controlsys.Pidtune` through one Studio operation:

```go
candidate, err := studio.DesignPIDController(ctx, flowID, studio.PIDDesignRequest{
    Type:               controlsys.PidtunePIDF,
    CrossoverFrequency: 2,
    PhaseMargin:        60,
    StepHorizon:        10,
})
```

The operation uses the persisted plant and controller roles. It requires one
SISO plant and one authored PID or PID2 controller block. The supported
controller choices are P, I, PI, PD, PID, and PIDF.

Candidate generation is read-only. The result retains the exact source model
revision, tuned parallel gains, derivative filter, sample time, achieved
classical margins, and current-versus-candidate loop responses on common
frequency and time grids. Applying the result is a separate atomic operation:

```go
snapshot, err := studio.ApplyPIDDesignCandidate(ctx, candidate)
```

Apply refuses candidates whose originating model revision is no longer
current.

## Two-degree-of-freedom PID

The PID2 block has separate named `reference` and `measurement` inputs and a
named `control` output. Its setpoint weights implement

`u = Kp(b r - y) + Ki/s(r - y) + Kd s/(Tf s + 1)(c r - y)`.

Controller roles therefore assign the reference input separately from the
measurement input. Process Lab retains the full `(r,y) -> u` controller for
reference-response evidence and derives the positive `y -> u` controller
expected by `controlsys` feedback operations. With `b=c=1`, the reference
response is equivalent to the ordinary one-degree-of-freedom PID loop.

## Time domain and delay policy

PID and PID2 blocks explicitly author continuous or discrete time. Discrete
`Pidtune` candidates preserve the plant sample time, and apply writes the gains
without changing the controller's time-domain settings.

For delayed plants, tuning and frequency evidence use the exact delay carried
by `controlsys`. The workflow does not silently introduce Padé or Thiran
approximations. If a delayed or unstable closed loop cannot provide stable step
evidence, the candidate remains reviewable with frequency and margin evidence
and reports the missing step result as a warning.
