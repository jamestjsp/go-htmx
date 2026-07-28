package studio

import (
	"fmt"
	"math"
	"math/cmplx"
	"strings"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/dsp/window"
	"gonum.org/v1/gonum/mat"
)

const (
	defaultIdentificationMaxChannels = 32
	defaultIdentificationMaxSamples  = 1 << 20
	defaultIdentificationMaxNFFT     = 1 << 14
	defaultIdentificationMaxMarkov   = 4096
	defaultIdentificationMaxERAOrder = 256
)

type IdentificationWorkflow struct {
	maxChannels int
	maxSamples  int
	maxNFFT     int
	maxMarkov   int
	maxERAOrder int
}

func NewIdentificationWorkflow() *IdentificationWorkflow {
	return &IdentificationWorkflow{
		maxChannels: defaultIdentificationMaxChannels,
		maxSamples:  defaultIdentificationMaxSamples,
		maxNFFT:     defaultIdentificationMaxNFFT,
		maxMarkov:   defaultIdentificationMaxMarkov,
		maxERAOrder: defaultIdentificationMaxERAOrder,
	}
}

type IdentificationDataset struct {
	Inputs        MatrixValue                 `json:"inputs"`
	Outputs       MatrixValue                 `json:"outputs"`
	InputNames    []string                    `json:"inputNames"`
	OutputNames   []string                    `json:"outputNames"`
	InputUnits    []string                    `json:"inputUnits"`
	OutputUnits   []string                    `json:"outputUnits"`
	SampleTime    float64                     `json:"sampleTime"`
	TimeUnit      string                      `json:"timeUnit"`
	Split         IdentificationSplit         `json:"split"`
	Preprocessing IdentificationPreprocessing `json:"preprocessing"`
}

type IdentificationSplit struct {
	Training   SampleRange `json:"training"`
	Validation SampleRange `json:"validation"`
}

type SampleRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (r SampleRange) Len() int {
	return r.End - r.Start
}

type IdentificationPreprocessing string

const (
	PreprocessingNone          IdentificationPreprocessing = "none"
	PreprocessingRemoveMean    IdentificationPreprocessing = "remove_mean"
	PreprocessingLinearDetrend IdentificationPreprocessing = "linear_detrend"
)

type FrequencyEstimationMethod string

const (
	FrequencyEstimationH1 FrequencyEstimationMethod = "h1"
	FrequencyEstimationH2 FrequencyEstimationMethod = "h2"
)

type IdentificationWindow string

const (
	IdentificationWindowRectangular IdentificationWindow = "rectangular"
	IdentificationWindowHann        IdentificationWindow = "hann"
	IdentificationWindowHamming     IdentificationWindow = "hamming"
	IdentificationWindowBlackman    IdentificationWindow = "blackman"
)

type FrequencyEstimationOptions struct {
	Method       FrequencyEstimationMethod `json:"method"`
	Window       IdentificationWindow      `json:"window"`
	NFFT         int                       `json:"nfft"`
	Overlap      int                       `json:"overlap"`
	MinCoherence float64                   `json:"minCoherence"`
}

type FrequencyIdentificationRequest struct {
	Name    string                     `json:"name"`
	Dataset IdentificationDataset      `json:"dataset"`
	Options FrequencyEstimationOptions `json:"options"`
}

type IdentificationProvenance struct {
	Source        string                      `json:"source"`
	SampleTime    float64                     `json:"sampleTime"`
	TimeUnit      string                      `json:"timeUnit"`
	InputNames    []string                    `json:"inputNames"`
	OutputNames   []string                    `json:"outputNames"`
	InputUnits    []string                    `json:"inputUnits"`
	OutputUnits   []string                    `json:"outputUnits"`
	TotalSamples  int                         `json:"totalSamples"`
	Split         IdentificationSplit         `json:"split"`
	Preprocessing IdentificationPreprocessing `json:"preprocessing"`
	Estimator     *FrequencyEstimationOptions `json:"estimator,omitempty"`
}

type ExcitationDiagnostics struct {
	InputRank          int     `json:"inputRank"`
	InputChannels      int     `json:"inputChannels"`
	SmallestSingular   float64 `json:"smallestSingular"`
	LargestSingular    float64 `json:"largestSingular"`
	ConditionNumber    float64 `json:"conditionNumber"`
	MeanCoherence      float64 `json:"meanCoherence"`
	MinimumCoherence   float64 `json:"minimumCoherence"`
	LowCoherenceBins   int     `json:"lowCoherenceBins"`
	TotalCoherenceBins int     `json:"totalCoherenceBins"`
}

type FrequencyValidationFit struct {
	ComparedBins int     `json:"comparedBins"`
	RelativeRMS  float64 `json:"relativeRms"`
	FitPercent   float64 `json:"fitPercent"`
}

type FRDModel struct {
	Frequencies VectorValue          `json:"frequencies"`
	Response    ComplexResponseValue `json:"response"`
	SampleTime  float64              `json:"sampleTime"`
	TimeUnit    string               `json:"timeUnit"`
	InputNames  []string             `json:"inputNames"`
	OutputNames []string             `json:"outputNames"`
	InputUnits  []string             `json:"inputUnits"`
	OutputUnits []string             `json:"outputUnits"`
}

type FRDCandidate struct {
	Name        string                   `json:"name"`
	Model       FRDModel                 `json:"model"`
	Coherence   []float64                `json:"coherence"`
	Diagnostics *ExcitationDiagnostics   `json:"diagnostics,omitempty"`
	Fit         *FrequencyValidationFit  `json:"fit,omitempty"`
	Provenance  IdentificationProvenance `json:"provenance"`
}

type FRDImportRequest struct {
	Name   string   `json:"name"`
	Source string   `json:"source"`
	Model  FRDModel `json:"model"`
}

func (c FRDCandidate) FRD() (*controlsys.FRD, error) {
	return c.Model.controlsys()
}

func (w *IdentificationWorkflow) ImportFRD(
	request FRDImportRequest,
) (FRDCandidate, error) {
	if w == nil {
		return FRDCandidate{}, invalid("identification workflow is nil")
	}
	if request.Name == "" {
		return FRDCandidate{}, invalid("imported FRD candidate name is required")
	}
	if request.Source == "" {
		return FRDCandidate{}, invalid("imported FRD source is required")
	}
	frd, err := request.Model.controlsys()
	if err != nil {
		return FRDCandidate{}, invalid("imported FRD: %s", err)
	}
	model, err := newFRDModel(
		frd,
		request.Model.TimeUnit,
		request.Model.InputUnits,
		request.Model.OutputUnits,
	)
	if err != nil {
		return FRDCandidate{}, err
	}
	return FRDCandidate{
		Name:  request.Name,
		Model: model,
		Provenance: IdentificationProvenance{
			Source:        request.Source,
			SampleTime:    model.SampleTime,
			TimeUnit:      model.TimeUnit,
			InputNames:    append([]string(nil), model.InputNames...),
			OutputNames:   append([]string(nil), model.OutputNames...),
			InputUnits:    append([]string(nil), model.InputUnits...),
			OutputUnits:   append([]string(nil), model.OutputUnits...),
			TotalSamples:  model.Frequencies.Len(),
			Preprocessing: PreprocessingNone,
		},
	}, nil
}

func (w *IdentificationWorkflow) EstimateFrequencyResponse(
	request FrequencyIdentificationRequest,
) (FRDCandidate, error) {
	if w == nil {
		return FRDCandidate{}, invalid("identification workflow is nil")
	}
	if request.Name == "" {
		return FRDCandidate{}, invalid("frequency-identification candidate name is required")
	}
	plan, err := w.frequencyPlan(request.Dataset, request.Options)
	if err != nil {
		return FRDCandidate{}, err
	}
	training, validation := plan.partition()
	trainingResult, err := controlsys.FreqRespEst(
		training.inputs, training.outputs, request.Dataset.SampleTime, plan.controlsysOptions(),
	)
	if err != nil {
		return FRDCandidate{}, fmt.Errorf("estimate training frequency response: %w", err)
	}
	validationResult, err := controlsys.FreqRespEst(
		validation.inputs, validation.outputs, request.Dataset.SampleTime, plan.controlsysOptions(),
	)
	if err != nil {
		return FRDCandidate{}, fmt.Errorf("estimate validation frequency response: %w", err)
	}
	excitation, err := excitationDiagnostics(training.inputs)
	if err != nil {
		return FRDCandidate{}, err
	}
	if excitation.InputRank != excitation.InputChannels {
		return FRDCandidate{}, invalid(
			"frequency identification requires independent input excitation; input rank is %d for %d channels",
			excitation.InputRank, excitation.InputChannels,
		)
	}
	coherenceDiagnostics(trainingResult.Coherence, request.Options.MinCoherence, &excitation)
	fit, err := compareFrequencyEstimates(
		trainingResult, validationResult, request.Options.MinCoherence,
	)
	if err != nil {
		return FRDCandidate{}, err
	}
	trainingResult.H.InputName = append([]string(nil), request.Dataset.InputNames...)
	trainingResult.H.OutputName = append([]string(nil), request.Dataset.OutputNames...)
	frd, err := trainingResult.FRD()
	if err != nil {
		return FRDCandidate{}, fmt.Errorf("construct estimated FRD: %w", err)
	}
	model, err := newFRDModel(
		frd,
		request.Dataset.TimeUnit,
		request.Dataset.InputUnits,
		request.Dataset.OutputUnits,
	)
	if err != nil {
		return FRDCandidate{}, err
	}
	options := request.Options
	return FRDCandidate{
		Name: request.Name, Model: model,
		Coherence:   append([]float64(nil), trainingResult.Coherence...),
		Diagnostics: &excitation, Fit: &fit,
		Provenance: IdentificationProvenance{
			Source:       "sampled_data",
			SampleTime:   request.Dataset.SampleTime,
			TimeUnit:     request.Dataset.TimeUnit,
			InputNames:   append([]string(nil), request.Dataset.InputNames...),
			OutputNames:  append([]string(nil), request.Dataset.OutputNames...),
			InputUnits:   append([]string(nil), request.Dataset.InputUnits...),
			OutputUnits:  append([]string(nil), request.Dataset.OutputUnits...),
			TotalSamples: plan.samples, Split: request.Dataset.Split,
			Preprocessing: request.Dataset.Preprocessing, Estimator: &options,
		},
	}, nil
}

type frequencyIdentificationPlan struct {
	dataset IdentificationDataset
	options FrequencyEstimationOptions
	inputs  *mat.Dense
	outputs *mat.Dense
	samples int
}

type sampledPartition struct {
	inputs  *mat.Dense
	outputs *mat.Dense
}

func (w *IdentificationWorkflow) frequencyPlan(
	dataset IdentificationDataset,
	options FrequencyEstimationOptions,
) (frequencyIdentificationPlan, error) {
	inputChannels, samples := dataset.Inputs.Dims()
	outputChannels, outputSamples := dataset.Outputs.Dims()
	if inputChannels == 0 || outputChannels == 0 || samples == 0 {
		return frequencyIdentificationPlan{}, invalid("identification data must be channels by samples")
	}
	if samples != outputSamples {
		return frequencyIdentificationPlan{}, invalid(
			"input and output data have %d and %d samples", samples, outputSamples,
		)
	}
	if inputChannels > w.maxChannels || outputChannels > w.maxChannels {
		return frequencyIdentificationPlan{}, invalid(
			"identification data exceeds the %d-channel limit", w.maxChannels,
		)
	}
	if samples > w.maxSamples {
		return frequencyIdentificationPlan{}, invalid(
			"identification data exceeds the %d-sample limit", w.maxSamples,
		)
	}
	if !finitePositive(dataset.SampleTime) {
		return frequencyIdentificationPlan{}, invalid("identification sample time must be finite and positive")
	}
	if dataset.TimeUnit == "" {
		return frequencyIdentificationPlan{}, invalid("identification time unit is required")
	}
	if err := validateSignalMetadata(
		dataset.InputNames, dataset.InputUnits, inputChannels, "input",
	); err != nil {
		return frequencyIdentificationPlan{}, err
	}
	if err := validateSignalMetadata(
		dataset.OutputNames, dataset.OutputUnits, outputChannels, "output",
	); err != nil {
		return frequencyIdentificationPlan{}, err
	}
	if err := validateIdentificationSplit(dataset.Split, samples); err != nil {
		return frequencyIdentificationPlan{}, err
	}
	if err := validatePreprocessing(dataset.Preprocessing); err != nil {
		return frequencyIdentificationPlan{}, err
	}
	if err := w.validateFrequencyOptions(options, inputChannels, outputChannels, dataset.Split); err != nil {
		return frequencyIdentificationPlan{}, err
	}
	inputs := mat.NewDense(inputChannels, samples, dataset.Inputs.Values())
	outputs := mat.NewDense(outputChannels, samples, dataset.Outputs.Values())
	inputs = preprocessIdentificationMatrix(inputs, dataset.Split.Training, dataset.Preprocessing)
	outputs = preprocessIdentificationMatrix(outputs, dataset.Split.Training, dataset.Preprocessing)
	return frequencyIdentificationPlan{
		dataset: dataset, options: options, inputs: inputs, outputs: outputs, samples: samples,
	}, nil
}

func (w *IdentificationWorkflow) validateFrequencyOptions(
	options FrequencyEstimationOptions,
	inputs, outputs int,
	split IdentificationSplit,
) error {
	if options.Method != FrequencyEstimationH1 && options.Method != FrequencyEstimationH2 {
		return invalid("frequency estimator must be h1 or h2")
	}
	if options.Method == FrequencyEstimationH2 && (inputs != 1 || outputs != 1) {
		return invalid("h2 frequency estimation is only available for SISO data")
	}
	if _, err := identificationWindow(options.Window); err != nil {
		return err
	}
	if options.NFFT < 8 || options.NFFT > w.maxNFFT {
		return invalid("frequency-estimation nfft must be between 8 and %d", w.maxNFFT)
	}
	if options.NFFT > split.Training.Len() || options.NFFT > split.Validation.Len() {
		return invalid(
			"frequency-estimation nfft %d exceeds the training or validation partition",
			options.NFFT,
		)
	}
	if options.Overlap < 0 || options.Overlap >= options.NFFT {
		return invalid("frequency-estimation overlap must be at least 0 and less than nfft")
	}
	if math.IsNaN(options.MinCoherence) || math.IsInf(options.MinCoherence, 0) ||
		options.MinCoherence < 0 || options.MinCoherence > 1 {
		return invalid("minimum coherence must be between 0 and 1")
	}
	return nil
}

func (p frequencyIdentificationPlan) partition() (sampledPartition, sampledPartition) {
	return sampledPartition{
			inputs:  sliceSamples(p.inputs, p.dataset.Split.Training),
			outputs: sliceSamples(p.outputs, p.dataset.Split.Training),
		}, sampledPartition{
			inputs:  sliceSamples(p.inputs, p.dataset.Split.Validation),
			outputs: sliceSamples(p.outputs, p.dataset.Split.Validation),
		}
}

func (p frequencyIdentificationPlan) controlsysOptions() *controlsys.FreqRespEstOpts {
	win, _ := identificationWindow(p.options.Window)
	return &controlsys.FreqRespEstOpts{
		NFFT: p.options.NFFT, Window: win, NOverlap: p.options.Overlap,
		Method: controlsys.FreqRespEstMethod(p.options.Method),
	}
}

func identificationWindow(
	name IdentificationWindow,
) (func([]float64) []float64, error) {
	switch name {
	case IdentificationWindowRectangular:
		return func(values []float64) []float64 { return values }, nil
	case IdentificationWindowHann:
		return window.Hann, nil
	case IdentificationWindowHamming:
		return window.Hamming, nil
	case IdentificationWindowBlackman:
		return window.Blackman, nil
	default:
		return nil, invalid("identification window must be rectangular, hann, hamming, or blackman")
	}
}

func validateSignalMetadata(names, units []string, channels int, kind string) error {
	if err := validateSignalNames(names, channels, kind); err != nil {
		return err
	}
	return validateUnits(units, channels, kind)
}

func validateIdentificationSplit(split IdentificationSplit, samples int) error {
	if err := validateSampleRange(split.Training, samples, "training"); err != nil {
		return err
	}
	if err := validateSampleRange(split.Validation, samples, "validation"); err != nil {
		return err
	}
	if split.Training.Start < split.Validation.End &&
		split.Validation.Start < split.Training.End {
		return invalid("training and validation sample ranges overlap")
	}
	return nil
}

func validateSampleRange(sampleRange SampleRange, samples int, name string) error {
	if sampleRange.Start < 0 || sampleRange.End <= sampleRange.Start || sampleRange.End > samples {
		return invalid(
			"%s sample range [%d,%d) is outside %d samples",
			name, sampleRange.Start, sampleRange.End, samples,
		)
	}
	return nil
}

func validatePreprocessing(method IdentificationPreprocessing) error {
	switch method {
	case PreprocessingNone, PreprocessingRemoveMean, PreprocessingLinearDetrend:
		return nil
	default:
		return invalid("preprocessing must be none, remove_mean, or linear_detrend")
	}
}

func preprocessIdentificationMatrix(
	source *mat.Dense,
	training SampleRange,
	method IdentificationPreprocessing,
) *mat.Dense {
	result := mat.DenseCopyOf(source)
	if method == PreprocessingNone {
		return result
	}
	channels, samples := result.Dims()
	raw := result.RawMatrix()
	for channel := range channels {
		row := raw.Data[channel*raw.Stride : channel*raw.Stride+samples]
		mean, slope := fittedTrend(row, training, method)
		for sample := range row {
			row[sample] -= mean + slope*float64(sample)
		}
	}
	return result
}

func fittedTrend(
	values []float64,
	training SampleRange,
	method IdentificationPreprocessing,
) (intercept, slope float64) {
	count := float64(training.Len())
	var sumX, sumY float64
	for index := training.Start; index < training.End; index++ {
		sumX += float64(index)
		sumY += values[index]
	}
	meanX, meanY := sumX/count, sumY/count
	if method == PreprocessingRemoveMean {
		return meanY, 0
	}
	var xx, xy float64
	for index := training.Start; index < training.End; index++ {
		centeredX := float64(index) - meanX
		xx += centeredX * centeredX
		xy += centeredX * (values[index] - meanY)
	}
	if xx != 0 {
		slope = xy / xx
	}
	return meanY - slope*meanX, slope
}

func sliceSamples(source *mat.Dense, sampleRange SampleRange) *mat.Dense {
	channels, _ := source.Dims()
	result := mat.NewDense(channels, sampleRange.Len(), nil)
	for channel := range channels {
		for sample := sampleRange.Start; sample < sampleRange.End; sample++ {
			result.Set(channel, sample-sampleRange.Start, source.At(channel, sample))
		}
	}
	return result
}

func excitationDiagnostics(inputs *mat.Dense) (ExcitationDiagnostics, error) {
	channels, _ := inputs.Dims()
	var decomposition mat.SVD
	if !decomposition.Factorize(inputs, mat.SVDThin) {
		return ExcitationDiagnostics{}, invalid("input excitation SVD did not converge")
	}
	singular := decomposition.Values(nil)
	result := ExcitationDiagnostics{InputChannels: channels}
	if len(singular) == 0 {
		return result, invalid("input excitation has no singular values")
	}
	result.LargestSingular = singular[0]
	result.SmallestSingular = singular[len(singular)-1]
	tolerance := math.Nextafter(1, 2) - 1
	tolerance *= float64(max(inputs.RawMatrix().Rows, inputs.RawMatrix().Cols))
	tolerance *= result.LargestSingular
	for _, value := range singular {
		if value > tolerance {
			result.InputRank++
		}
	}
	if result.SmallestSingular == 0 {
		result.ConditionNumber = math.Inf(1)
	} else {
		result.ConditionNumber = result.LargestSingular / result.SmallestSingular
	}
	return result, nil
}

func coherenceDiagnostics(
	coherence []float64,
	minimum float64,
	result *ExcitationDiagnostics,
) {
	result.TotalCoherenceBins = len(coherence)
	result.MinimumCoherence = 1
	for _, value := range coherence {
		result.MeanCoherence += value
		if value < result.MinimumCoherence {
			result.MinimumCoherence = value
		}
		if value < minimum {
			result.LowCoherenceBins++
		}
	}
	if len(coherence) > 0 {
		result.MeanCoherence /= float64(len(coherence))
	} else {
		result.MinimumCoherence = 0
	}
}

func compareFrequencyEstimates(
	training, validation *controlsys.FreqRespEstResult,
	minimumCoherence float64,
) (FrequencyValidationFit, error) {
	if training == nil || validation == nil || training.H == nil || validation.H == nil {
		return FrequencyValidationFit{}, invalid("frequency estimates are incomplete")
	}
	if training.H.NFreq != validation.H.NFreq ||
		training.H.P != validation.H.P || training.H.M != validation.H.M {
		return FrequencyValidationFit{}, invalid("training and validation frequency grids differ")
	}
	var errorPower, referencePower float64
	compared := 0
	for index := range training.H.Data {
		if len(training.Coherence) > 0 &&
			(training.Coherence[index] < minimumCoherence ||
				validation.Coherence[index] < minimumCoherence) {
			continue
		}
		difference := training.H.Data[index] - validation.H.Data[index]
		errorPower += cmplx.Abs(difference) * cmplx.Abs(difference)
		referencePower += cmplx.Abs(validation.H.Data[index]) * cmplx.Abs(validation.H.Data[index])
		compared++
	}
	if compared == 0 || referencePower == 0 {
		return FrequencyValidationFit{}, invalid(
			"training and validation data have no sufficiently coherent nonzero frequency bins",
		)
	}
	relative := math.Sqrt(errorPower / referencePower)
	return FrequencyValidationFit{
		ComparedBins: compared,
		RelativeRMS:  relative,
		FitPercent:   100 * math.Max(0, 1-relative),
	}, nil
}

type MarkovParameterDataset struct {
	Parameters    []MatrixValue `json:"parameters"`
	TrainingCount int           `json:"trainingCount"`
	InputNames    []string      `json:"inputNames"`
	OutputNames   []string      `json:"outputNames"`
	InputUnits    []string      `json:"inputUnits"`
	OutputUnits   []string      `json:"outputUnits"`
	SampleTime    float64       `json:"sampleTime"`
	TimeUnit      string        `json:"timeUnit"`
}

type ERAIdentificationRequest struct {
	Name    string                 `json:"name"`
	Dataset MarkovParameterDataset `json:"dataset"`
	Order   int                    `json:"order"`
}

type ERAValidationFit struct {
	HeldOutParameters int     `json:"heldOutParameters"`
	RelativeRMS       float64 `json:"relativeRms"`
	FitPercent        float64 `json:"fitPercent"`
}

type IdentifiedStateSpace struct {
	A           MatrixValue `json:"a"`
	B           MatrixValue `json:"b"`
	C           MatrixValue `json:"c"`
	D           MatrixValue `json:"d"`
	SampleTime  float64     `json:"sampleTime"`
	InputNames  []string    `json:"inputNames"`
	OutputNames []string    `json:"outputNames"`
	StateNames  []string    `json:"stateNames"`
}

type ERACandidate struct {
	Name                 string                   `json:"name"`
	Order                int                      `json:"order"`
	HankelSingularValues []float64                `json:"hankelSingularValues"`
	Model                IdentifiedStateSpace     `json:"model"`
	Fit                  ERAValidationFit         `json:"fit"`
	Provenance           IdentificationProvenance `json:"provenance"`
}

func (w *IdentificationWorkflow) IdentifyERA(
	request ERAIdentificationRequest,
) (ERACandidate, error) {
	if w == nil {
		return ERACandidate{}, invalid("identification workflow is nil")
	}
	if request.Name == "" {
		return ERACandidate{}, invalid("ERA candidate name is required")
	}
	dataset := request.Dataset
	if !finitePositive(dataset.SampleTime) {
		return ERACandidate{}, invalid("ERA sample time must be finite and positive")
	}
	if dataset.TimeUnit == "" {
		return ERACandidate{}, invalid("ERA time unit is required")
	}
	if len(dataset.Parameters) > w.maxMarkov {
		return ERACandidate{}, invalid("ERA data exceeds the %d-parameter limit", w.maxMarkov)
	}
	if dataset.TrainingCount < 3 || dataset.TrainingCount >= len(dataset.Parameters) {
		return ERACandidate{}, invalid(
			"ERA training count must leave at least one held-out Markov parameter",
		)
	}
	if dataset.TrainingCount%2 == 0 {
		return ERACandidate{}, invalid(
			"ERA training count must be odd so the shifted Hankel matrix uses measured parameters",
		)
	}
	if request.Order <= 0 || request.Order > w.maxERAOrder {
		return ERACandidate{}, invalid("ERA order must be between 1 and %d", w.maxERAOrder)
	}
	parameters, outputs, inputs, err := validateMarkovDataset(dataset.Parameters)
	if err != nil {
		return ERACandidate{}, err
	}
	if err := validateSignalMetadata(dataset.InputNames, dataset.InputUnits, inputs, "input"); err != nil {
		return ERACandidate{}, err
	}
	if err := validateSignalMetadata(dataset.OutputNames, dataset.OutputUnits, outputs, "output"); err != nil {
		return ERACandidate{}, err
	}
	result, err := controlsys.ERA(
		parameters[:dataset.TrainingCount], request.Order, dataset.SampleTime,
	)
	if err != nil {
		return ERACandidate{}, fmt.Errorf("identify ERA model: %w", err)
	}
	result.Sys.InputName = append([]string(nil), dataset.InputNames...)
	result.Sys.OutputName = append([]string(nil), dataset.OutputNames...)
	result.Sys.StateName = indexedNames("x", request.Order)
	fit, err := compareHeldOutMarkov(
		result.Sys, parameters, dataset.TrainingCount,
	)
	if err != nil {
		return ERACandidate{}, err
	}
	model, err := identifiedStateSpace(result.Sys)
	if err != nil {
		return ERACandidate{}, err
	}
	return ERACandidate{
		Name: request.Name, Order: request.Order,
		HankelSingularValues: append([]float64(nil), result.HSV...),
		Model:                model, Fit: fit,
		Provenance: IdentificationProvenance{
			Source:     "markov_parameters",
			SampleTime: dataset.SampleTime, TimeUnit: dataset.TimeUnit,
			InputNames:   append([]string(nil), dataset.InputNames...),
			OutputNames:  append([]string(nil), dataset.OutputNames...),
			InputUnits:   append([]string(nil), dataset.InputUnits...),
			OutputUnits:  append([]string(nil), dataset.OutputUnits...),
			TotalSamples: len(dataset.Parameters),
			Split: IdentificationSplit{
				Training:   SampleRange{Start: 0, End: dataset.TrainingCount},
				Validation: SampleRange{Start: dataset.TrainingCount, End: len(dataset.Parameters)},
			},
			Preprocessing: PreprocessingNone,
		},
	}, nil
}

func validateMarkovDataset(
	values []MatrixValue,
) ([]*mat.Dense, int, int, error) {
	if len(values) == 0 {
		return nil, 0, 0, invalid("Markov-parameter data is empty")
	}
	outputs, inputs := values[0].Dims()
	if outputs == 0 || inputs == 0 {
		return nil, 0, 0, invalid("Markov parameters must be nonempty output by input matrices")
	}
	result := make([]*mat.Dense, len(values))
	for index, value := range values {
		rows, columns := value.Dims()
		if rows != outputs || columns != inputs {
			return nil, 0, 0, invalid(
				"Markov parameter %d is %dx%d; expected %dx%d",
				index, rows, columns, outputs, inputs,
			)
		}
		result[index] = mat.NewDense(rows, columns, value.Values())
	}
	return result, outputs, inputs, nil
}

func compareHeldOutMarkov(
	system *controlsys.System,
	parameters []*mat.Dense,
	start int,
) (ERAValidationFit, error) {
	var errorPower, referencePower float64
	for index := start; index < len(parameters); index++ {
		predicted := systemMarkovParameter(system, index)
		rows, columns := predicted.Dims()
		for row := range rows {
			for column := range columns {
				difference := predicted.At(row, column) - parameters[index].At(row, column)
				errorPower += difference * difference
				reference := parameters[index].At(row, column)
				referencePower += reference * reference
			}
		}
	}
	if referencePower == 0 {
		return ERAValidationFit{}, invalid("held-out Markov parameters have zero energy")
	}
	relative := math.Sqrt(errorPower / referencePower)
	return ERAValidationFit{
		HeldOutParameters: len(parameters) - start,
		RelativeRMS:       relative,
		FitPercent:        100 * math.Max(0, 1-relative),
	}, nil
}

func systemMarkovParameter(system *controlsys.System, index int) *mat.Dense {
	if index == 0 {
		return mat.DenseCopyOf(system.D)
	}
	_, inputs, outputs := system.Dims()
	power := identificationIdentityDense(system.A.RawMatrix().Rows)
	for count := 1; count < index; count++ {
		var next mat.Dense
		next.Mul(power, system.A)
		power = &next
	}
	var powerB, result mat.Dense
	powerB.Mul(power, system.B)
	result.Mul(system.C, &powerB)
	return mat.NewDense(outputs, inputs, append([]float64(nil), result.RawMatrix().Data...))
}

func identificationIdentityDense(size int) *mat.Dense {
	result := mat.NewDense(size, size, nil)
	for index := range size {
		result.Set(index, index, 1)
	}
	return result
}

func identifiedStateSpace(system *controlsys.System) (IdentifiedStateSpace, error) {
	a, err := matrixValuePointer(system.A)
	if err != nil {
		return IdentifiedStateSpace{}, err
	}
	b, err := matrixValuePointer(system.B)
	if err != nil {
		return IdentifiedStateSpace{}, err
	}
	c, err := matrixValuePointer(system.C)
	if err != nil {
		return IdentifiedStateSpace{}, err
	}
	d, err := matrixValuePointer(system.D)
	if err != nil {
		return IdentifiedStateSpace{}, err
	}
	return IdentifiedStateSpace{
		A: *a, B: *b, C: *c, D: *d, SampleTime: system.Dt,
		InputNames:  append([]string(nil), system.InputName...),
		OutputNames: append([]string(nil), system.OutputName...),
		StateNames:  append([]string(nil), system.StateName...),
	}, nil
}

type FRDInterconnectionOperation string

const (
	FRDOperationSeries   FRDInterconnectionOperation = "series"
	FRDOperationParallel FRDInterconnectionOperation = "parallel"
	FRDOperationFeedback FRDInterconnectionOperation = "feedback"
	FRDOperationMargin   FRDInterconnectionOperation = "margin"
)

type FRDInterconnectionRequest struct {
	Operation FRDInterconnectionOperation `json:"operation"`
	Left      FRDModel                    `json:"left"`
	Right     *FRDModel                   `json:"right,omitempty"`
	Sign      float64                     `json:"sign,omitempty"`
}

type FRDInterconnectionResult struct {
	Model  *FRDModel          `json:"model,omitempty"`
	Margin *FRDMarginEvidence `json:"margin,omitempty"`
}

type FRDMarginEvidence struct {
	GainMarginDB            *float64 `json:"gainMarginDb,omitempty"`
	PhaseMarginDegrees      *float64 `json:"phaseMarginDegrees,omitempty"`
	GainCrossoverFrequency  *float64 `json:"gainCrossoverFrequency,omitempty"`
	PhaseCrossoverFrequency *float64 `json:"phaseCrossoverFrequency,omitempty"`
}

func (w *IdentificationWorkflow) InterconnectFRD(
	request FRDInterconnectionRequest,
) (FRDInterconnectionResult, error) {
	if w == nil {
		return FRDInterconnectionResult{}, invalid("identification workflow is nil")
	}
	left, err := request.Left.controlsys()
	if err != nil {
		return FRDInterconnectionResult{}, invalid("left FRD: %s", err)
	}
	if request.Operation == FRDOperationMargin {
		margin, err := controlsys.FRDMargin(left)
		if err != nil {
			return FRDInterconnectionResult{}, fmt.Errorf("FRD margin: %w", err)
		}
		return FRDInterconnectionResult{Margin: finiteMarginEvidence(margin)}, nil
	}
	if request.Right == nil {
		return FRDInterconnectionResult{}, invalid("%s FRD operation requires a right model", request.Operation)
	}
	right, err := request.Right.controlsys()
	if err != nil {
		return FRDInterconnectionResult{}, invalid("right FRD: %s", err)
	}
	if request.Left.TimeUnit != request.Right.TimeUnit {
		return FRDInterconnectionResult{}, invalid(
			"FRD time units %q and %q differ",
			request.Left.TimeUnit, request.Right.TimeUnit,
		)
	}
	if err := validateExactFRDGrid(left, right); err != nil {
		return FRDInterconnectionResult{}, err
	}
	var connected *controlsys.FRD
	var inputUnits, outputUnits []string
	switch request.Operation {
	case FRDOperationSeries:
		if err := requireExactStrings(left.OutputName, right.InputName, "series connected channels"); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(
			request.Left.OutputUnits,
			request.Right.InputUnits,
			"series connected units",
		); err != nil {
			return FRDInterconnectionResult{}, err
		}
		connected, err = controlsys.FRDSeries(left, right)
		inputUnits = request.Left.InputUnits
		outputUnits = request.Right.OutputUnits
	case FRDOperationParallel:
		if err := requireExactStrings(left.InputName, right.InputName, "parallel inputs"); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(left.OutputName, right.OutputName, "parallel outputs"); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(
			request.Left.InputUnits,
			request.Right.InputUnits,
			"parallel input units",
		); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(
			request.Left.OutputUnits,
			request.Right.OutputUnits,
			"parallel output units",
		); err != nil {
			return FRDInterconnectionResult{}, err
		}
		connected, err = controlsys.FRDParallel(left, right)
		inputUnits = request.Left.InputUnits
		outputUnits = request.Left.OutputUnits
	case FRDOperationFeedback:
		if request.Sign != -1 && request.Sign != 1 {
			return FRDInterconnectionResult{}, invalid("FRD feedback sign must be -1 or 1")
		}
		if err := requireExactStrings(left.OutputName, right.InputName, "feedback measurements"); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(left.InputName, right.OutputName, "feedback controls"); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(
			request.Left.OutputUnits,
			request.Right.InputUnits,
			"feedback measurement units",
		); err != nil {
			return FRDInterconnectionResult{}, err
		}
		if err := requireExactStrings(
			request.Left.InputUnits,
			request.Right.OutputUnits,
			"feedback control units",
		); err != nil {
			return FRDInterconnectionResult{}, err
		}
		connected, err = controlsys.FRDFeedback(left, right, request.Sign)
		inputUnits = request.Left.InputUnits
		outputUnits = request.Left.OutputUnits
	default:
		return FRDInterconnectionResult{}, invalid("unknown FRD operation %q", request.Operation)
	}
	if err != nil {
		return FRDInterconnectionResult{}, fmt.Errorf("%s FRD operation: %w", request.Operation, err)
	}
	model, err := newFRDModel(
		connected,
		request.Left.TimeUnit,
		inputUnits,
		outputUnits,
	)
	if err != nil {
		return FRDInterconnectionResult{}, err
	}
	return FRDInterconnectionResult{Model: &model}, nil
}

func validateExactFRDGrid(left, right *controlsys.FRD) error {
	if left.Dt != right.Dt {
		return invalid("FRD sample times %g and %g differ", left.Dt, right.Dt)
	}
	if len(left.Omega) != len(right.Omega) {
		return invalid("FRD frequency-grid lengths %d and %d differ", len(left.Omega), len(right.Omega))
	}
	for index := range left.Omega {
		if left.Omega[index] != right.Omega[index] {
			return invalid(
				"FRD frequency grids differ at index %d: %g and %g",
				index, left.Omega[index], right.Omega[index],
			)
		}
	}
	return nil
}

func requireExactStrings(left, right []string, role string) error {
	if len(left) == 0 || len(right) == 0 {
		return invalid("%s require complete channel names", role)
	}
	if len(left) != len(right) {
		return invalid("%s have %d and %d channels", role, len(left), len(right))
	}
	for index := range left {
		if left[index] == "" || right[index] == "" || left[index] != right[index] {
			return invalid(
				"%s differ at channel %d: %q and %q",
				role, index+1, left[index], right[index],
			)
		}
	}
	return nil
}

func newFRDModel(
	frd *controlsys.FRD,
	timeUnit string,
	inputUnits, outputUnits []string,
) (FRDModel, error) {
	if frd == nil {
		return FRDModel{}, invalid("FRD model is nil")
	}
	if timeUnit == "" {
		return FRDModel{}, invalid("FRD time unit is required")
	}
	if err := validateFRDNames(frd); err != nil {
		return FRDModel{}, err
	}
	outputs, inputs := frd.Dims()
	if err := validateUnits(inputUnits, inputs, "FRD input"); err != nil {
		return FRDModel{}, err
	}
	if err := validateUnits(outputUnits, outputs, "FRD output"); err != nil {
		return FRDModel{}, err
	}
	frequencies, err := NewVectorValue(frd.Omega)
	if err != nil {
		return FRDModel{}, err
	}
	values := make([]complex128, 0, len(frd.Omega)*outputs*inputs)
	for frequency := range frd.Omega {
		for output := range outputs {
			values = append(values, frd.Response[frequency][output]...)
		}
	}
	response, err := NewComplexResponseValue(len(frd.Omega), outputs, inputs, values)
	if err != nil {
		return FRDModel{}, err
	}
	return FRDModel{
		Frequencies: frequencies, Response: response, SampleTime: frd.Dt,
		TimeUnit:    timeUnit,
		InputNames:  append([]string(nil), frd.InputName...),
		OutputNames: append([]string(nil), frd.OutputName...),
		InputUnits:  append([]string(nil), inputUnits...),
		OutputUnits: append([]string(nil), outputUnits...),
	}, nil
}

func (model FRDModel) controlsys() (*controlsys.FRD, error) {
	if model.SampleTime < 0 || math.IsNaN(model.SampleTime) || math.IsInf(model.SampleTime, 0) {
		return nil, invalid("FRD sample time must be finite and nonnegative")
	}
	if model.TimeUnit == "" {
		return nil, invalid("FRD time unit is required")
	}
	frequencies := model.Frequencies.Values()
	samples, outputs, inputs := model.Response.Dims()
	if samples != len(frequencies) {
		return nil, invalid("FRD response count does not match the frequency grid")
	}
	if err := validateSignalNames(model.InputNames, inputs, "FRD input"); err != nil {
		return nil, err
	}
	if err := validateSignalNames(model.OutputNames, outputs, "FRD output"); err != nil {
		return nil, err
	}
	if err := validateUnits(model.InputUnits, inputs, "FRD input"); err != nil {
		return nil, err
	}
	if err := validateUnits(model.OutputUnits, outputs, "FRD output"); err != nil {
		return nil, err
	}
	frd, err := controlsys.NewFRD(model.Response.Tensor(), frequencies, model.SampleTime)
	if err != nil {
		return nil, err
	}
	frd.InputName = append([]string(nil), model.InputNames...)
	frd.OutputName = append([]string(nil), model.OutputNames...)
	return frd, nil
}

func validateFRDNames(frd *controlsys.FRD) error {
	outputs, inputs := frd.Dims()
	if err := validateSignalNames(frd.InputName, inputs, "FRD input"); err != nil {
		return err
	}
	return validateSignalNames(frd.OutputName, outputs, "FRD output")
}

func validateSignalNames(names []string, channels int, role string) error {
	validated, err := NewChannelNames(names)
	if err != nil {
		return invalid("%s names: %s", role, err)
	}
	if validated.Len() != channels {
		return invalid("%s has %d channels but %d names", role, channels, validated.Len())
	}
	canonical := validated.Names()
	for index := range names {
		if names[index] != canonical[index] {
			return invalid("%s name %d has surrounding whitespace", role, index+1)
		}
	}
	return nil
}

func validateUnits(units []string, channels int, role string) error {
	if len(units) != channels {
		return invalid("%s has %d channels but %d units", role, channels, len(units))
	}
	for index, unit := range units {
		if strings.TrimSpace(unit) == "" {
			return invalid("%s unit %d is empty", role, index+1)
		}
		if unit != strings.TrimSpace(unit) {
			return invalid("%s unit %d has surrounding whitespace", role, index+1)
		}
	}
	return nil
}

func finiteMarginEvidence(margin *controlsys.MarginResult) *FRDMarginEvidence {
	if margin == nil {
		return nil
	}
	return &FRDMarginEvidence{
		GainMarginDB:            finiteFloatPointer(margin.GainMargin),
		PhaseMarginDegrees:      finiteFloatPointer(margin.PhaseMargin),
		GainCrossoverFrequency:  finiteFloatPointer(margin.WgFreq),
		PhaseCrossoverFrequency: finiteFloatPointer(margin.WpFreq),
	}
}

func finiteFloatPointer(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
