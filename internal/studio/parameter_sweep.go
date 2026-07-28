package studio

import (
	"fmt"
	"math"
	"math/cmplx"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jamestjsp/controlsys"
)

const (
	maxParameterSweepAxes         = 4
	maxParameterSweepModels       = 64
	maxParameterSweepFrequencies  = 256
	maxParameterSweepTimeSamples  = 2000
	maxParameterSweepStates       = 64
	maxParameterSweepChannels     = 8
	maxParameterSweepResponseData = 1_000_000
)

type SweepAxis struct {
	Parameter string    `json:"parameter"`
	Unit      string    `json:"unit"`
	Values    []float64 `json:"values"`
}

type SweepSpec struct {
	SourceModelRevision time.Time   `json:"sourceModelRevision"`
	Axes                []SweepAxis `json:"axes"`
}

type SweepModelSource struct {
	Name          string
	ModelRevision time.Time
	Parameters    Parameters
	SetParameter  func(*Parameters, string, float64) error
	Compile       func(string, Parameters) (*controlsys.System, error)
}

type SweepVariant struct {
	FlatIndex   int        `json:"flatIndex"`
	Name        string     `json:"name"`
	Coordinates []int      `json:"coordinates"`
	Values      []float64  `json:"values"`
	Parameters  Parameters `json:"parameters"`
}

type ParameterSweep struct {
	SourceModelRevision time.Time              `json:"sourceModelRevision"`
	Axes                []SweepAxis            `json:"axes"`
	Shape               []int                  `json:"shape"`
	Variants            []SweepVariant         `json:"variants"`
	Models              *controlsys.ModelArray `json:"-"`
}

type SweepAnalysisSpec struct {
	Omega     []float64 `json:"omega"`
	StepFinal float64   `json:"stepFinal"`
}

type SweepFrequencyModelSummary struct {
	FlatIndex   int     `json:"flatIndex"`
	Name        string  `json:"name"`
	Coordinates []int   `json:"coordinates"`
	PeakGain    float64 `json:"peakGain"`
	PeakOmega   float64 `json:"peakOmega"`
}

type SweepTimeModelSummary struct {
	FlatIndex    int     `json:"flatIndex"`
	Name         string  `json:"name"`
	Coordinates  []int   `json:"coordinates"`
	PeakAbsolute float64 `json:"peakAbsolute"`
	PeakTime     float64 `json:"peakTime"`
	InputIndex   int     `json:"inputIndex"`
	OutputIndex  int     `json:"outputIndex"`
	SampleCount  int     `json:"sampleCount"`
}

type SweepFrequencyAnalysis struct {
	Models    []SweepFrequencyModelSummary       `json:"models"`
	WorstCase SweepFrequencyModelSummary         `json:"worstCase"`
	Responses *controlsys.ModelArrayFreqResponse `json:"-"`
}

type SweepTimeAnalysis struct {
	Models    []SweepTimeModelSummary            `json:"models"`
	WorstCase SweepTimeModelSummary              `json:"worstCase"`
	Responses *controlsys.ModelArrayTimeResponse `json:"-"`
}

type ParameterSweepAnalysis struct {
	SourceModelRevision time.Time              `json:"sourceModelRevision"`
	Axes                []SweepAxis            `json:"axes"`
	Shape               []int                  `json:"shape"`
	Frequency           SweepFrequencyAnalysis `json:"frequency"`
	Time                SweepTimeAnalysis      `json:"time"`
}

func MaterializeParameterSweep(source SweepModelSource, spec SweepSpec) (*ParameterSweep, error) {
	shape, modelCount, err := validateSweepDefinition(source, spec)
	if err != nil {
		return nil, err
	}
	axes := cloneSweepAxes(spec.Axes)
	variants := make([]SweepVariant, modelCount)
	models := make([]*controlsys.System, modelCount)
	for flat := range modelCount {
		coordinates := sweepCoordinates(flat, shape)
		parameters := cloneParameters(source.Parameters)
		values := make([]float64, len(axes))
		for axisIndex, axis := range axes {
			value := axis.Values[coordinates[axisIndex]]
			values[axisIndex] = value
			if err := source.SetParameter(&parameters, axis.Parameter, value); err != nil {
				return nil, invalid(
					"parameter sweep model %d cannot set %s: %v",
					flat+1, axis.Parameter, err,
				)
			}
		}
		name := sweepVariantName(source.Name, axes, values)
		variantParameters := cloneParameters(parameters)
		system, err := source.Compile(name, cloneParameters(parameters))
		if err != nil {
			return nil, fmt.Errorf("compile parameter sweep model %q: %w", name, err)
		}
		variants[flat] = SweepVariant{
			FlatIndex: flat, Name: name,
			Coordinates: coordinates, Values: values,
			Parameters: variantParameters,
		}
		models[flat] = system
	}
	if err := validateSweepModelFamily(models); err != nil {
		return nil, err
	}
	array, err := controlsys.NewModelArray(shape, models)
	if err != nil {
		return nil, fmt.Errorf("construct parameter sweep model array: %w", err)
	}
	return &ParameterSweep{
		SourceModelRevision: spec.SourceModelRevision,
		Axes:                axes, Shape: append([]int(nil), shape...),
		Variants: variants, Models: array,
	}, nil
}

func AnalyzeParameterSweep(
	sweep *ParameterSweep,
	spec SweepAnalysisSpec,
) (*ParameterSweepAnalysis, error) {
	if sweep == nil || sweep.Models == nil || len(sweep.Variants) == 0 {
		return nil, invalid("parameter sweep is empty")
	}
	if sweep.Models.Len() != len(sweep.Variants) ||
		!slices.Equal(sweep.Models.Shape(), sweep.Shape) {
		return nil, invalid("parameter sweep metadata does not match its model array")
	}
	reference, ok, err := sweep.Models.ModelFlat(0)
	if err != nil || !ok {
		return nil, invalid("parameter sweep has no reference model")
	}
	if err := validateSweepAnalysisSpec(sweep, reference, spec); err != nil {
		return nil, err
	}

	frequencyResponses, err := sweep.Models.FreqResponse(spec.Omega)
	if err != nil {
		return nil, fmt.Errorf("evaluate parameter sweep frequency responses: %w", err)
	}
	timeResponses, err := sweep.Models.Step(spec.StepFinal)
	if err != nil {
		return nil, fmt.Errorf("evaluate parameter sweep step responses: %w", err)
	}
	frequency, err := summarizeSweepFrequency(sweep, frequencyResponses, reference.Dt)
	if err != nil {
		return nil, err
	}
	timeSummary, err := summarizeSweepTime(sweep, timeResponses)
	if err != nil {
		return nil, err
	}
	return &ParameterSweepAnalysis{
		SourceModelRevision: sweep.SourceModelRevision,
		Axes:                cloneSweepAxes(sweep.Axes),
		Shape:               append([]int(nil), sweep.Shape...),
		Frequency:           frequency,
		Time:                timeSummary,
	}, nil
}

func validateSweepDefinition(source SweepModelSource, spec SweepSpec) ([]int, int, error) {
	if strings.TrimSpace(source.Name) == "" {
		return nil, 0, invalid("parameter sweep source name is required")
	}
	if source.ModelRevision.IsZero() || spec.SourceModelRevision.IsZero() {
		return nil, 0, invalid("parameter sweep source model revision is required")
	}
	if !source.ModelRevision.Equal(spec.SourceModelRevision) {
		return nil, 0, invalid(
			"parameter sweep source revision %s does not match requested revision %s",
			source.ModelRevision.Format(time.RFC3339Nano),
			spec.SourceModelRevision.Format(time.RFC3339Nano),
		)
	}
	if source.SetParameter == nil || source.Compile == nil {
		return nil, 0, invalid("parameter sweep source requires parameter and compile functions")
	}
	if len(spec.Axes) == 0 || len(spec.Axes) > maxParameterSweepAxes {
		return nil, 0, invalid(
			"parameter sweep requires between 1 and %d axes",
			maxParameterSweepAxes,
		)
	}
	seen := make(map[string]struct{}, len(spec.Axes))
	shape := make([]int, len(spec.Axes))
	modelCount := 1
	for axisIndex, axis := range spec.Axes {
		if strings.TrimSpace(axis.Parameter) == "" {
			return nil, 0, invalid("parameter sweep axis %d requires a parameter name", axisIndex+1)
		}
		if strings.TrimSpace(axis.Unit) == "" {
			return nil, 0, invalid("parameter sweep axis %s requires an explicit unit", axis.Parameter)
		}
		if _, exists := seen[axis.Parameter]; exists {
			return nil, 0, invalid("parameter sweep axis %s is repeated", axis.Parameter)
		}
		seen[axis.Parameter] = struct{}{}
		if len(axis.Values) == 0 {
			return nil, 0, invalid("parameter sweep axis %s has no values", axis.Parameter)
		}
		axisValues := make(map[uint64]struct{}, len(axis.Values))
		for valueIndex, value := range axis.Values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, 0, invalid(
					"parameter sweep axis %s value %d must be finite",
					axis.Parameter, valueIndex+1,
				)
			}
			if value == 0 {
				value = 0
			}
			bits := math.Float64bits(value)
			if _, exists := axisValues[bits]; exists {
				return nil, 0, invalid(
					"parameter sweep axis %s repeats value %.12g",
					axis.Parameter, value,
				)
			}
			axisValues[bits] = struct{}{}
		}
		if modelCount > maxParameterSweepModels/len(axis.Values) {
			return nil, 0, invalid(
				"parameter sweep is limited to %d models",
				maxParameterSweepModels,
			)
		}
		modelCount *= len(axis.Values)
		shape[axisIndex] = len(axis.Values)
	}
	return shape, modelCount, nil
}

func validateSweepModelFamily(models []*controlsys.System) error {
	if len(models) == 0 {
		return invalid("parameter sweep compiled no models")
	}
	var reference *controlsys.System
	var referenceInputs, referenceOutputs, referenceStates []string
	for modelIndex, system := range models {
		if system == nil {
			return invalid("parameter sweep model %d compiled to nil", modelIndex+1)
		}
		if err := system.Validate(); err != nil {
			return fmt.Errorf("validate parameter sweep model %d: %w", modelIndex+1, err)
		}
		if system.IsDescriptor() {
			return invalid(
				"parameter sweep model %d is descriptor; bounded step analysis requires explicit state space",
				modelIndex+1,
			)
		}
		n, m, p := system.Dims()
		if n > maxParameterSweepStates {
			return invalid(
				"parameter sweep model %d is limited to %d states",
				modelIndex+1, maxParameterSweepStates,
			)
		}
		if m == 0 || p == 0 ||
			m > maxParameterSweepChannels || p > maxParameterSweepChannels {
			return invalid(
				"parameter sweep model %d requires between 1 and %d input and output channels",
				modelIndex+1, maxParameterSweepChannels,
			)
		}
		if err := validateCompleteSweepNames("input", modelIndex, system.InputName, m); err != nil {
			return err
		}
		if err := validateCompleteSweepNames("output", modelIndex, system.OutputName, p); err != nil {
			return err
		}
		if err := validateCompleteSweepNames("state", modelIndex, system.StateName, n); err != nil {
			return err
		}
		if reference == nil {
			reference = system
			referenceInputs = append([]string(nil), system.InputName...)
			referenceOutputs = append([]string(nil), system.OutputName...)
			referenceStates = append([]string(nil), system.StateName...)
			continue
		}
		rn, rm, rp := reference.Dims()
		if n != rn || m != rm || p != rp {
			return invalid(
				"parameter sweep model %d dimensions (%d,%d,%d) do not match (%d,%d,%d)",
				modelIndex+1, n, m, p, rn, rm, rp,
			)
		}
		if system.Dt != reference.Dt ||
			system.IsContinuous() != reference.IsContinuous() {
			return invalid(
				"parameter sweep model %d time domain/sample time %.12g does not match %.12g",
				modelIndex+1, system.Dt, reference.Dt,
			)
		}
		if !slices.Equal(system.InputName, referenceInputs) ||
			!slices.Equal(system.OutputName, referenceOutputs) ||
			!slices.Equal(system.StateName, referenceStates) {
			return invalid("parameter sweep model %d channel or state names differ", modelIndex+1)
		}
	}
	return nil
}

func validateCompleteSweepNames(kind string, modelIndex int, names []string, count int) error {
	if len(names) != count {
		return invalid(
			"parameter sweep model %d requires %d complete %s names, got %d",
			modelIndex+1, count, kind, len(names),
		)
	}
	seen := make(map[string]struct{}, len(names))
	for nameIndex, name := range names {
		if strings.TrimSpace(name) == "" {
			return invalid(
				"parameter sweep model %d %s name %d is empty",
				modelIndex+1, kind, nameIndex+1,
			)
		}
		if _, exists := seen[name]; exists {
			return invalid(
				"parameter sweep model %d repeats %s name %q",
				modelIndex+1, kind, name,
			)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateSweepAnalysisSpec(
	sweep *ParameterSweep,
	reference *controlsys.System,
	spec SweepAnalysisSpec,
) error {
	if len(spec.Omega) < 2 || len(spec.Omega) > maxParameterSweepFrequencies {
		return invalid(
			"parameter sweep frequency analysis requires between 2 and %d points",
			maxParameterSweepFrequencies,
		)
	}
	for frequencyIndex, frequency := range spec.Omega {
		if math.IsNaN(frequency) || math.IsInf(frequency, 0) || frequency <= 0 {
			return invalid(
				"parameter sweep frequency %d must be positive and finite",
				frequencyIndex+1,
			)
		}
		if frequencyIndex > 0 && frequency <= spec.Omega[frequencyIndex-1] {
			return invalid("parameter sweep frequencies must be strictly increasing")
		}
	}
	if reference.IsDiscrete() {
		nyquist := math.Pi / reference.Dt
		if spec.Omega[len(spec.Omega)-1] > nyquist*(1+1e-12) {
			return invalid(
				"parameter sweep frequency %.12g exceeds discrete Nyquist %.12g",
				spec.Omega[len(spec.Omega)-1], nyquist,
			)
		}
	}
	if math.IsNaN(spec.StepFinal) || math.IsInf(spec.StepFinal, 0) ||
		spec.StepFinal <= 0 {
		return invalid("parameter sweep step final time must be positive and finite")
	}
	_, inputs, outputs := reference.Dims()
	responseValues := sweep.Models.Len() * len(spec.Omega) * inputs * outputs
	if responseValues > maxParameterSweepResponseData {
		return invalid(
			"parameter sweep frequency response is limited to %d complex values",
			maxParameterSweepResponseData,
		)
	}
	for modelIndex := range sweep.Models.Len() {
		system, ok, err := sweep.Models.ModelFlat(modelIndex)
		if err != nil || !ok {
			return invalid("parameter sweep model %d is unavailable", modelIndex+1)
		}
		samples, err := parameterSweepStepSamples(system, spec.StepFinal)
		if err != nil {
			return fmt.Errorf("bound parameter sweep model %d step response: %w", modelIndex+1, err)
		}
		if samples > maxParameterSweepTimeSamples {
			return invalid(
				"parameter sweep step response is limited to %d samples per model; model %d requires %d",
				maxParameterSweepTimeSamples, modelIndex+1, samples,
			)
		}
	}
	return nil
}

func parameterSweepStepSamples(system *controlsys.System, finalTime float64) (int, error) {
	if system.IsDiscrete() {
		return boundedSweepSampleCount(finalTime, system.Dt), nil
	}
	n, _, _ := system.Dims()
	if n == 0 {
		return boundedSweepSampleCount(finalTime, 0.01), nil
	}
	poles, err := system.Poles()
	if err != nil {
		return 0, err
	}
	var minFrequency, maxFrequency float64
	for _, pole := range poles {
		frequency := cmplx.Abs(pole)
		if frequency <= 1e-10 {
			continue
		}
		if minFrequency == 0 || frequency < minFrequency {
			minFrequency = frequency
		}
		if frequency > maxFrequency {
			maxFrequency = frequency
		}
	}
	if minFrequency == 0 {
		return boundedSweepSampleCount(finalTime, 0.01), nil
	}
	automaticFinal := math.Min(7/minFrequency, 1e4)
	step := 1 / (20 * maxFrequency)
	if maximumStep := automaticFinal / 100; step > maximumStep {
		step = maximumStep
	}
	return boundedSweepSampleCount(finalTime, step), nil
}

func boundedSweepSampleCount(finalTime, step float64) int {
	ratio := finalTime / step
	if ratio >= float64(maxParameterSweepTimeSamples) {
		return maxParameterSweepTimeSamples + 1
	}
	return int(ratio) + 1
}

func summarizeSweepFrequency(
	sweep *ParameterSweep,
	responses *controlsys.ModelArrayFreqResponse,
	dt float64,
) (SweepFrequencyAnalysis, error) {
	result := SweepFrequencyAnalysis{
		Models:    make([]SweepFrequencyModelSummary, len(sweep.Variants)),
		Responses: responses,
	}
	worstIndex := -1
	for modelIndex, variant := range sweep.Variants {
		response := responses.Responses[modelIndex]
		if response == nil || responses.Void[modelIndex] {
			return SweepFrequencyAnalysis{}, invalid(
				"parameter sweep frequency response %d is unavailable",
				modelIndex+1,
			)
		}
		sigma, err := sweepResponseSigma(response, dt)
		if err != nil {
			return SweepFrequencyAnalysis{}, fmt.Errorf(
				"parameter sweep frequency singular values for model %d: %w",
				modelIndex+1, err,
			)
		}
		summary := SweepFrequencyModelSummary{
			FlatIndex: modelIndex, Name: variant.Name,
			Coordinates: append([]int(nil), variant.Coordinates...),
			PeakGain:    -1,
		}
		for frequencyIndex, frequency := range response.Omega {
			gain := sigma.At(frequencyIndex, 0)
			if gain > summary.PeakGain {
				summary.PeakGain = gain
				summary.PeakOmega = frequency
			}
		}
		result.Models[modelIndex] = summary
		if worstIndex < 0 || summary.PeakGain > result.WorstCase.PeakGain {
			worstIndex = modelIndex
			result.WorstCase = summary
		}
	}
	return result, nil
}

func sweepResponseSigma(
	response *controlsys.FreqResponseMatrix,
	dt float64,
) (*controlsys.SigmaResult, error) {
	values := make([][][]complex128, response.NFreq)
	for frequency := range response.NFreq {
		values[frequency] = make([][]complex128, response.P)
		for output := range response.P {
			values[frequency][output] = make([]complex128, response.M)
			for input := range response.M {
				values[frequency][output][input] = response.At(frequency, output, input)
			}
		}
	}
	frd, err := controlsys.NewFRD(values, response.Omega, dt)
	if err != nil {
		return nil, err
	}
	return frd.Sigma()
}

func summarizeSweepTime(
	sweep *ParameterSweep,
	responses *controlsys.ModelArrayTimeResponse,
) (SweepTimeAnalysis, error) {
	result := SweepTimeAnalysis{
		Models:    make([]SweepTimeModelSummary, len(sweep.Variants)),
		Responses: responses,
	}
	_, _, outputs := sweep.Models.Dims()
	worstIndex := -1
	for modelIndex, variant := range sweep.Variants {
		response := responses.Responses[modelIndex]
		if response == nil || response.Y == nil || responses.Void[modelIndex] {
			return SweepTimeAnalysis{}, invalid(
				"parameter sweep step response %d is unavailable",
				modelIndex+1,
			)
		}
		rows, samples := response.Y.Dims()
		summary := SweepTimeModelSummary{
			FlatIndex: modelIndex, Name: variant.Name,
			Coordinates: append([]int(nil), variant.Coordinates...),
			SampleCount: samples,
		}
		for row := range rows {
			for sample := range samples {
				value := math.Abs(response.Y.At(row, sample))
				if value > summary.PeakAbsolute {
					summary.PeakAbsolute = value
					summary.PeakTime = response.T[sample]
					summary.InputIndex = row / outputs
					summary.OutputIndex = row % outputs
				}
			}
		}
		result.Models[modelIndex] = summary
		if worstIndex < 0 || summary.PeakAbsolute > result.WorstCase.PeakAbsolute {
			worstIndex = modelIndex
			result.WorstCase = summary
		}
	}
	return result, nil
}

func sweepCoordinates(flat int, shape []int) []int {
	coordinates := make([]int, len(shape))
	for axis := len(shape) - 1; axis >= 0; axis-- {
		coordinates[axis] = flat % shape[axis]
		flat /= shape[axis]
	}
	return coordinates
}

func sweepVariantName(sourceName string, axes []SweepAxis, values []float64) string {
	parts := make([]string, len(axes))
	for axisIndex, axis := range axes {
		parts[axisIndex] = axis.Parameter + "=" +
			strconv.FormatFloat(values[axisIndex], 'g', -1, 64) + " " + axis.Unit
	}
	return sourceName + " [" + strings.Join(parts, ", ") + "]"
}

func cloneSweepAxes(axes []SweepAxis) []SweepAxis {
	cloned := make([]SweepAxis, len(axes))
	for axisIndex, axis := range axes {
		cloned[axisIndex] = axis
		cloned[axisIndex].Values = append([]float64(nil), axis.Values...)
	}
	return cloned
}
