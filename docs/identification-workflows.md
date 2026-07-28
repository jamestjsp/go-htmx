# Identification workflows

Studio treats frequency-response estimation, ERA identification, and FRD algebra
as reviewable analysis workflows. They do not mutate a flowsheet or silently
replace an authored model.

## Sampled-data contract

`IdentificationDataset` stores inputs and outputs as channels by samples. Every
channel has a unique name and a non-empty unit. The dataset also records:

- a finite positive sample time and its time unit;
- non-overlapping `[start,end)` training and validation ranges; and
- one explicit preprocessing choice: `none`, `remove_mean`, or
  `linear_detrend`.

Mean and trend coefficients are fitted only on the training range and then
applied to both ranges. Validation data therefore cannot influence
preprocessing or the estimated model.

The default workflow bounds datasets to 32 input and 32 output channels,
1,048,576 samples, and an NFFT of 16,384. NFFT must fit in both partitions.

## FRD import

`ImportFRD` validates a named, persistable `FRDModel` and records its external
source. The model must include an ordered frequency grid, complex response
matrices, sample time, time unit, and complete input/output names and units.
Import supplies provenance only; it does not invent coherence or validation-fit
evidence.

## Frequency-response estimation

`EstimateFrequencyResponse` accepts H1 or H2, a persisted window enum, NFFT,
overlap, and a minimum coherence threshold. The enum is translated internally
to the callback expected by controlsys, so persisted requests contain no Go
function values. H2 is deliberately restricted to SISO data.

The result is a named `FRDCandidate` containing:

- a persistable `FRDModel` with frequency samples expressed in radians per
  declared time unit, complex responses, sample time, and exact input/output
  names and units;
- the estimator request and complete dataset provenance;
- time-domain input rank and condition diagnostics;
- per-frequency coherence summary; and
- a held-out relative RMS comparison between independently estimated training
  and validation FRDs.

MIMO estimation refuses rank-deficient input data before calling controlsys.
This is required because unresolved spectral inversions in the dependency are
otherwise represented by zero-valued response bins.

The persistent MIMO oracle uses two independent white-noise inputs, a known
full-rank static 2×2 plant, measurement noise, and held-out data. Every
identified channel is compared with its analytic complex gain; a separate
fixture proves that collinear excitation is refused.

Coherence and held-out fit are evidence, not uncertainty bounds. Low-coherence
bins are excluded from the held-out comparison and counted in the diagnostics.

## ERA identification

`IdentifyERA` accepts a sequence of output-by-input Markov matrices. The first
matrix is direct feedthrough. `TrainingCount` divides the initial identification
sequence from the held-out tail, and the requested order is always explicit.
The training count must be odd so both block Hankel matrices contain only
measured parameters; an even count would make the dependency zero-fill its last
shifted block.

The result is a named `ERACandidate` with:

- the complete Hankel singular-value sequence returned by controlsys;
- a persistable named discrete state-space model; and
- held-out impulse evidence computed by comparing each unused Markov parameter
  with `C A^(k-1) B`.

The workflow bounds the sequence to 4,096 matrices and the requested order to
256. ERA does not infer order or provide confidence intervals.

## FRD algebra

`InterconnectFRD` routes series, parallel, feedback, and SISO margin operations
through controlsys. Before delegation it requires:

- exactly equal sample times and frequency values;
- exactly equal time units;
- complete names on every channel;
- compatible connected signal units;
- matching connected names for series and feedback;
- matching input and output names for parallel; and
- an explicit feedback sign of `-1` or `1`.

FRD algebra remains frequency-domain-only. It does not make an FRD suitable for
time simulation; ERA or another explicit realization workflow is required for
that transition.
