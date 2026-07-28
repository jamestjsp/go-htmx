# Model studies

`ModelStudy` is the review boundary for analysis and reduction of a state-space
or frequency-response model. It owns a copy of the source, records provenance,
and returns candidate systems without replacing or mutating that source.

## Capabilities and limits

| Study operation | controlsys path | Evidence returned | Applicability |
| --- | --- | --- | --- |
| Minimal realization | `MinimalRealization` | retained order, poles, stability, channel names, dense frequency error | Standard, delay-free state space |
| Staircase reduction | `Reduce` | retained order and frequency error | Standard, delay-free state space |
| Balanced realization and energy analysis | `Balreal`, `H2Norm`, `HinfNorm`, `HSV` | both HSV paths, H2, Hinf and peak frequency | Stable, standard, delay-free state space |
| Balanced truncation or residualization | `MinimalRealization`, `Balred` | HSV, discarded HSV, dense frequency error; truncation also reports the `2 sum(discarded HSV)` Hinf bound | Stable, standard, delay-free state space |
| Selected-state truncation or residualization | `Modred` | retained order, poles, stability, frequency error | Standard, delay-free state space |
| Modal truncation | `ModalTruncate` | retained modes through candidate poles and frequency error | Standard, delay-free state space |
| Stability decomposition | `Stabsep` | stable and unstable components with poles and named channels | Standard, delay-free state space |
| White-noise response | `Covar` | output covariance | Stable, standard, delay-free state space |
| State-space passivity screen | `SampledPassive` | minimum sampled Hermitian-part eigenvalue and violating frequency | Stable, square, standard, delay-free state space |
| FRD passivity screen | `FRDPassive` | minimum sampled Hermitian-part eigenvalue and violating frequency | Square FRD with a valid frequency grid |

Descriptor and exact-delay limitations are checked by `ModelStudy` before a
controlsys algorithm is called. Convert a descriptor model to an explicit
realization when mathematically valid. Approximate or absorb a delay explicitly
when the approximation is acceptable, and keep that converted model as a new
provenance source rather than disguising it as the original.

Passivity results are deliberately labeled `sampled-pass`. A sampled pass is
finite-grid evidence, not an analytic certificate. A negative sampled
Hermitian-part eigenvalue is a concrete counterexample.

## Review workflow

Create a study from a named source:

```go
study, err := studio.NewStateSpaceModelStudy("validated plant", plant)
```

The constructor copies `plant`. `SourceSystem` also returns a defensive copy, so
subsequent caller changes cannot alter study provenance.

Request a candidate without applying it:

```go
candidate, err := study.Reduce(studio.ModelReductionRequest{
    Method: studio.ModelBalancedTruncation,
    Order:  4,
})
```

The candidate contains:

- source name, original order, sample time, poles, stability, and names;
- retained order, candidate poles, stability, and channel/state names;
- the owned candidate `System`;
- a fixed representative-grid frequency error;
- Hankel singular values and, for balanced truncation, the discarded-HSV Hinf
  bound.

The caller must separately decide whether to apply a candidate. `ModelStudy`
does not write the block diagram, replace a model, or claim acceptance.

Energy and covariance evidence can be requested together:

```go
evidence, err := study.Energy(studio.ModelEnergyRequest{
    InputNoiseCovariance: noiseCovariance,
})
```

Stability separation preserves the identity `G = Gs + Gu`, including
feedthrough allocation performed by controlsys:

```go
parts, err := study.SeparateStability()
```

For measured response data, use `NewFRDModelStudy` and `Passivity`. The result
always distinguishes sampled evidence from a certificate.

## Verification

The persistent tests use independent checks rather than accepting one
controlsys operation as the oracle for another:

- analytic frequency response for minimal-realization equivalence;
- a dense frequency sweep against the balanced-truncation discarded-HSV bound;
- independent continuous Lyapunov formulas for H2 and covariance;
- a dense independent Hinf sweep;
- pole classification and direct frequency verification of `G = Gs + Gu`;
- analytic Hermitian eigenvalues for MIMO passivity evidence.

Representative reduction and evidence benchmarks are in
`internal/studio/model_study_test.go`. Run them with:

```sh
go test ./internal/studio -run '^TestModelStudy' -count=1
go test -race ./internal/studio -run '^TestModelStudy' -count=1
go test ./internal/studio -run '^$' -bench '^BenchmarkModelStudyRepresentative' -benchtime=3x
```
