package studio

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jamestjsp/controlsys"
)

const (
	defaultFrequencyPoints     = 200
	maxAnalysisChannelsPerAxis = 16
	maxFrequencyResponseTraces = 64
	maxFrequencyPoints         = 2000
)

type FrequencyAnalysisRequest struct {
	Inputs   []ChannelRef `json:"inputs"`
	Outputs  []ChannelRef `json:"outputs"`
	BaseStep float64      `json:"baseStep,omitempty"`
	Omega    []float64    `json:"omega,omitempty"`
	Points   int          `json:"points,omitempty"`
}

type FrequencyAnalysis struct {
	FlowID         int64                  `json:"flowId"`
	ModelUpdatedAt time.Time              `json:"modelUpdatedAt"`
	Inputs         []AnalyzedChannel      `json:"inputs"`
	Outputs        []AnalyzedChannel      `json:"outputs"`
	Mode           string                 `json:"mode"`
	Grid           FrequencyGrid          `json:"grid"`
	Units          FrequencyUnits         `json:"units"`
	Bode           []BodeTrace            `json:"bode,omitempty"`
	Nyquist        *NyquistAnalysis       `json:"nyquist,omitempty"`
	Nichols        *NicholsAnalysis       `json:"nichols,omitempty"`
	SingularValues *SingularValueAnalysis `json:"singularValues,omitempty"`
	Issues         []AnalysisIssue        `json:"issues,omitempty"`
}

type FrequencyGrid struct {
	Source          string    `json:"source"`
	Omega           []float64 `json:"omega"`
	DiscreteNyquist *float64  `json:"discreteNyquist,omitempty"`
}

type FrequencyUnits struct {
	Frequency   string `json:"frequency"`
	Magnitude   string `json:"magnitude"`
	Phase       string `json:"phase"`
	PhasePolicy string `json:"phasePolicy"`
	Singular    string `json:"singular"`
}

type BodeTrace struct {
	InputIndex   int        `json:"inputIndex"`
	OutputIndex  int        `json:"outputIndex"`
	MagnitudeDB  []*float64 `json:"magnitudeDb"`
	PhaseDegrees []*float64 `json:"phaseDegrees"`
}

type ComplexSample struct {
	Real *float64 `json:"real,omitempty"`
	Imag *float64 `json:"imag,omitempty"`
}

type NyquistAnalysis struct {
	Omega    []float64       `json:"omega"`
	Positive []ComplexSample `json:"positive"`
	Negative []ComplexSample `json:"negative"`
}

type NicholsAnalysis struct {
	MagnitudeDB  []*float64 `json:"magnitudeDb"`
	PhaseDegrees []*float64 `json:"phaseDegrees"`
}

type SingularValueAnalysis struct {
	Values [][]*float64 `json:"values"`
}

func (s *Studio) AnalyzeFrequency(
	ctx context.Context,
	flowID int64,
	request FrequencyAnalysisRequest,
) (FrequencyAnalysis, error) {
	if err := validateFrequencyRequest(request); err != nil {
		return FrequencyAnalysis{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return FrequencyAnalysis{}, err
	}
	result, err := analyzeFrequency(snapshot.Blocks, snapshot.Connections, request)
	if err != nil {
		return FrequencyAnalysis{}, err
	}
	result.FlowID = flowID
	result.ModelUpdatedAt = snapshot.Flow.ModelUpdatedAt
	return result, nil
}

func analyzeFrequency(
	blocks []Block,
	connections []Connection,
	request FrequencyAnalysisRequest,
) (FrequencyAnalysis, error) {
	if err := validateFrequencyRequest(request); err != nil {
		return FrequencyAnalysis{}, err
	}
	probes := make([]modelProbe, len(request.Outputs))
	for i, output := range request.Outputs {
		probes[i] = modelProbe{BlockID: output.BlockID, OutputPort: output.Port}
	}
	model, err := compileRequestedModel(blocks, connections, modelCompileRequest{
		probes: probes, baseStep: request.BaseStep,
	})
	if err != nil {
		return FrequencyAnalysis{}, err
	}
	system, inputSignals, outputSignals, err := model.selectChannels(
		request.Inputs, request.Outputs,
	)
	if err != nil {
		return FrequencyAnalysis{}, err
	}
	result := FrequencyAnalysis{
		Mode: "mimo",
		Units: FrequencyUnits{
			Frequency:   "rad/s",
			Magnitude:   "dB",
			Phase:       "degrees",
			PhasePolicy: "unwrapped between adjacent frequency samples",
			Singular:    "absolute gain",
		},
	}
	if len(request.Inputs) == 1 && len(request.Outputs) == 1 {
		result.Mode = "siso"
	}
	for i, ref := range request.Inputs {
		result.Inputs = append(result.Inputs, analyzedChannel(ref, inputSignals[i]))
	}
	for i, ref := range request.Outputs {
		result.Outputs = append(result.Outputs, analyzedChannel(ref, outputSignals[i]))
	}

	points := request.Points
	if points == 0 {
		points = defaultFrequencyPoints
	}
	omega := append([]float64(nil), request.Omega...)
	gridSource := "explicit"
	if len(omega) == 0 {
		gridSource = "automatic"
		if system.IsDiscrete() {
			omega = logFrequencyGrid(
				math.Pi/system.Dt*1e-4,
				math.Pi/system.Dt,
				points,
			)
		}
	} else if err := validateFrequencyGridForSystem(omega, system); err != nil {
		return FrequencyAnalysis{}, err
	}
	if len(omega) == 0 {
		bode, bodeErr := system.Bode(nil, points)
		if bodeErr != nil {
			return FrequencyAnalysis{}, fmt.Errorf("automatic frequency grid: %w", bodeErr)
		}
		omega = append([]float64(nil), bode.Omega...)
	}
	if err := validateFrequencyGridForSystem(omega, system); err != nil {
		return FrequencyAnalysis{}, err
	}
	frd, err := system.FRD(omega)
	if err != nil {
		return FrequencyAnalysis{}, fmt.Errorf("frequency response data: %w", err)
	}
	bode := frd.Bode()
	result.Grid = FrequencyGrid{Source: gridSource, Omega: omega}
	if system.IsDiscrete() {
		result.Grid.DiscreteNyquist = floatPointer(math.Pi / system.Dt)
	}
	result.Bode = bodeTraces(bode, len(request.Outputs), len(request.Inputs))

	sigma, err := frd.Sigma()
	if err != nil {
		result.Issues = append(result.Issues, AnalysisIssue{
			Operation: "singular-values", Message: err.Error(),
		})
	} else {
		result.SingularValues = singularValueAnalysis(sigma)
	}
	if result.Mode == "mimo" {
		return result, nil
	}

	nichols, err := system.Nichols(omega, 0)
	if err != nil {
		result.Issues = append(result.Issues, AnalysisIssue{
			Operation: "nichols", Message: err.Error(),
		})
	} else {
		result.Nichols = &NicholsAnalysis{
			MagnitudeDB:  scalarSamples(nichols.Omega, func(i int) float64 { return nichols.MagDBAt(i, 0, 0) }),
			PhaseDegrees: scalarSamples(nichols.Omega, func(i int) float64 { return nichols.PhaseAt(i, 0, 0) }),
		}
	}
	nyquist, err := frd.Nyquist()
	if err != nil {
		result.Issues = append(result.Issues, AnalysisIssue{
			Operation: "nyquist", Message: err.Error(),
		})
	} else {
		result.Nyquist = &NyquistAnalysis{
			Omega:    append([]float64(nil), nyquist.Omega...),
			Positive: complexSamples(nyquist.Contour),
			Negative: complexSamples(nyquist.ContourN),
		}
	}
	return result, nil
}

func validateFrequencyRequest(request FrequencyAnalysisRequest) error {
	if len(request.Inputs) == 0 || len(request.Outputs) == 0 {
		return invalid("select at least one input and one output channel")
	}
	if len(request.Inputs) > maxAnalysisChannelsPerAxis ||
		len(request.Outputs) > maxAnalysisChannelsPerAxis {
		return invalid(
			"frequency analysis is limited to %d input and %d output channels; selected %d inputs and %d outputs",
			maxAnalysisChannelsPerAxis, maxAnalysisChannelsPerAxis,
			len(request.Inputs), len(request.Outputs),
		)
	}
	if len(request.Inputs)*len(request.Outputs) > maxFrequencyResponseTraces {
		return invalid(
			"frequency analysis is limited to %d input-output traces; selected %d",
			maxFrequencyResponseTraces, len(request.Inputs)*len(request.Outputs),
		)
	}
	if request.Points < 0 || request.Points == 1 || request.Points > maxFrequencyPoints {
		return invalid("automatic frequency points must be 0 for the default or between 2 and 2,000")
	}
	if len(request.Omega) == 1 {
		return invalid("explicit frequency grid requires at least two points")
	}
	for i, frequency := range request.Omega {
		if math.IsNaN(frequency) || math.IsInf(frequency, 0) || frequency <= 0 {
			return invalid("frequency %d must be a positive finite rad/s value", i+1)
		}
		if i > 0 && frequency <= request.Omega[i-1] {
			return invalid("frequency grid must be strictly increasing at point %d", i+1)
		}
	}
	return nil
}

func validateFrequencyGridForSystem(omega []float64, system *controlsys.System) error {
	if len(omega) < 2 {
		return invalid("frequency analysis requires at least two frequency points")
	}
	if !system.IsDiscrete() {
		return nil
	}
	nyquist := math.Pi / system.Dt
	for i, frequency := range omega {
		if frequency > nyquist*(1+1e-12) {
			return invalid(
				"frequency %.12g rad/s at point %d exceeds the discrete Nyquist limit %.12g rad/s for sample time %.12g s",
				frequency, i+1, nyquist, system.Dt,
			)
		}
	}
	return nil
}

func bodeTraces(bode *controlsys.BodeResult, outputs, inputs int) []BodeTrace {
	traces := make([]BodeTrace, 0, outputs*inputs)
	for output := range outputs {
		for input := range inputs {
			traces = append(traces, BodeTrace{
				InputIndex:  input,
				OutputIndex: output,
				MagnitudeDB: scalarSamples(bode.Omega, func(i int) float64 {
					return bode.MagDBAt(i, output, input)
				}),
				PhaseDegrees: scalarSamples(bode.Omega, func(i int) float64 {
					return bode.PhaseAt(i, output, input)
				}),
			})
		}
	}
	return traces
}

func singularValueAnalysis(result *controlsys.SigmaResult) *SingularValueAnalysis {
	values := make([][]*float64, result.NSV())
	for singular := range result.NSV() {
		values[singular] = scalarSamples(result.Omega, func(frequency int) float64 {
			return result.At(frequency, singular)
		})
	}
	return &SingularValueAnalysis{Values: values}
}

func scalarSamples(omega []float64, value func(int) float64) []*float64 {
	result := make([]*float64, len(omega))
	for i := range omega {
		result[i] = finitePointer(value(i))
	}
	return result
}

func complexSamples(values []complex128) []ComplexSample {
	result := make([]ComplexSample, len(values))
	for i, value := range values {
		result[i] = ComplexSample{
			Real: finitePointer(real(value)),
			Imag: finitePointer(imag(value)),
		}
	}
	return result
}

func logFrequencyGrid(minimum, maximum float64, points int) []float64 {
	result := make([]float64, points)
	if points == 1 {
		result[0] = minimum
		return result
	}
	logMinimum := math.Log(minimum)
	logSpan := math.Log(maximum) - logMinimum
	for i := range points {
		result[i] = math.Exp(logMinimum + float64(i)*logSpan/float64(points-1))
	}
	result[len(result)-1] = maximum
	return result
}
