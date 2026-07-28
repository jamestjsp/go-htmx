# Named loop sensitivity and robustness

`Studio.AnalyzeLoopRobustness` builds the persisted plant and controller roles,
calls `controlsys.Loopsens`, and returns the four standard loop models:

- `So = (I + PC)^-1`, output sensitivity
- `To = PC(I + PC)^-1`, output complementary sensitivity
- `Si = (I + CP)^-1`, input sensitivity
- `Ti = CP(I + CP)^-1`, input complementary sensitivity

Output-side models retain the plant measurement names on both axes. Input-side
models retain the plant control names. Every model includes all named Bode
input-output traces, singular-value traces, and the H-infinity norm when the
model supports it.

For SISO roles, the analysis also reports classical margins, the
peak-sensitivity-derived modulus margin returned by `controlsys.DiskMargin`,
and complementary-sensitivity bandwidth. These SISO-only metrics are omitted
for MIMO roles; MIMO robustness is represented by the singular values and
norms instead.

## Candidate comparison

An optional candidate controller can be supplied without changing the
flowsheet:

```go
analysis, err := studio.AnalyzeLoopRobustness(ctx, flowID, studio.LoopRobustnessRequest{
    Omega:               []float64{0.01, 0.1, 1, 10, 100},
    CandidateController: candidate.Controller,
})
```

Current and candidate results use the exact same frequency grid. Candidate
dimensions, time domain, and sample time are checked against the selected plant
and controller roles before any robustness calculation. The operation is
read-only and records the source model revision so a later review workflow can
detect stale evidence.
