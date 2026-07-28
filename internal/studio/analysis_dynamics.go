package studio

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jamestjsp/controlsys"
)

type DynamicsAnalysisRequest struct {
	Input    ChannelRef      `json:"input"`
	Output   ChannelRef      `json:"output"`
	BaseStep float64         `json:"baseStep,omitempty"`
	Step     *StepExperiment `json:"step,omitempty"`
}

type StepExperiment struct {
	Horizon float64 `json:"horizon"`
}

type DynamicsAnalysis struct {
	FlowID         int64                 `json:"flowId"`
	ModelUpdatedAt time.Time             `json:"modelUpdatedAt"`
	Input          AnalyzedChannel       `json:"input"`
	Output         AnalyzedChannel       `json:"output"`
	Stable         *bool                 `json:"stable,omitempty"`
	Poles          []ComplexValue        `json:"poles,omitempty"`
	Zeros          []ComplexValue        `json:"zeros,omitempty"`
	DCGain         *float64              `json:"dcGain,omitempty"`
	Damping        []DampingMode         `json:"damping,omitempty"`
	StepExperiment *StepExperimentResult `json:"stepExperiment,omitempty"`
	Issues         []AnalysisIssue       `json:"issues,omitempty"`
}

type AnalyzedChannel struct {
	ChannelRef
	Name       string `json:"name"`
	SignalName string `json:"signalName"`
}

type ComplexValue struct {
	Real float64 `json:"real"`
	Imag float64 `json:"imag"`
}

type DampingMode struct {
	Pole             ComplexValue `json:"pole"`
	NaturalFrequency float64      `json:"naturalFrequency"`
	DampingRatio     float64      `json:"dampingRatio"`
	TimeConstant     *float64     `json:"timeConstant,omitempty"`
}

type StepExperimentResult struct {
	Horizon float64     `json:"horizon"`
	Times   []float64   `json:"times"`
	Values  []float64   `json:"values"`
	Metrics StepMetrics `json:"metrics"`
}

type StepMetrics struct {
	RiseTime         *float64 `json:"riseTime,omitempty"`
	SettlingTime     *float64 `json:"settlingTime,omitempty"`
	Overshoot        *float64 `json:"overshoot,omitempty"`
	Undershoot       *float64 `json:"undershoot,omitempty"`
	Peak             *float64 `json:"peak,omitempty"`
	PeakTime         *float64 `json:"peakTime,omitempty"`
	SteadyStateValue *float64 `json:"steadyStateValue,omitempty"`
	Settled          bool     `json:"settled"`
}

type AnalysisIssue struct {
	Operation string `json:"operation"`
	Message   string `json:"message"`
}

func (s *Studio) AnalyzeDynamics(
	ctx context.Context,
	flowID int64,
	request DynamicsAnalysisRequest,
) (DynamicsAnalysis, error) {
	if request.Step != nil &&
		(math.IsNaN(request.Step.Horizon) ||
			math.IsInf(request.Step.Horizon, 0) ||
			request.Step.Horizon <= 0 ||
			request.Step.Horizon > 120) {
		return DynamicsAnalysis{}, invalid("step horizon must be greater than 0 and at most 120 seconds")
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return DynamicsAnalysis{}, err
	}
	result, err := analyzeDynamics(snapshot.Blocks, snapshot.Connections, request)
	if err != nil {
		return DynamicsAnalysis{}, err
	}
	result.FlowID = flowID
	result.ModelUpdatedAt = snapshot.Flow.ModelUpdatedAt
	return result, nil
}

func analyzeDynamics(
	blocks []Block,
	connections []Connection,
	request DynamicsAnalysisRequest,
) (DynamicsAnalysis, error) {
	model, err := compileRequestedModel(blocks, connections, modelCompileRequest{
		probes: []modelProbe{{
			BlockID: request.Output.BlockID, OutputPort: request.Output.Port,
		}},
		baseStep: request.BaseStep,
	})
	if err != nil {
		return DynamicsAnalysis{}, err
	}
	system, inputs, outputs, err := model.selectChannels(
		[]ChannelRef{request.Input},
		[]ChannelRef{request.Output},
	)
	if err != nil {
		return DynamicsAnalysis{}, err
	}
	result := DynamicsAnalysis{
		Input:  analyzedChannel(request.Input, inputs[0]),
		Output: analyzedChannel(request.Output, outputs[0]),
	}

	stable, err := system.IsStable()
	if err != nil {
		result.addIssue("stability", err)
	} else {
		result.Stable = &stable
	}
	if poles, poleErr := system.Poles(); poleErr != nil {
		result.addIssue("poles", poleErr)
	} else {
		result.Poles = complexValues(poles)
	}
	if zeros, zeroErr := system.Zeros(); zeroErr != nil {
		result.addIssue("zeros", zeroErr)
	} else {
		result.Zeros = complexValues(zeros)
	}
	if gain, gainErr := system.DCGain(); gainErr != nil {
		result.addIssue("dc-gain", gainErr)
	} else if rows, columns := gain.Dims(); rows != 1 || columns != 1 {
		result.addIssue("dc-gain", fmt.Errorf(
			"selected system returned %d×%d gain instead of SISO", rows, columns,
		))
	} else if value := gain.At(0, 0); !math.IsNaN(value) && !math.IsInf(value, 0) {
		result.DCGain = floatPointer(value)
	} else {
		result.addIssue("dc-gain", fmt.Errorf("steady-state gain is undefined"))
	}
	if damping, dampErr := controlsys.Damp(system); dampErr != nil {
		result.addIssue("damping", dampErr)
	} else {
		result.Damping = dampingModes(damping)
	}

	if request.Step == nil {
		return result, nil
	}
	if result.Stable == nil {
		result.addIssue("step", fmt.Errorf("stability must be known before a step experiment"))
		return result, nil
	}
	if !*result.Stable {
		result.addIssue("step", fmt.Errorf("step metrics require a stable selected system"))
		return result, nil
	}
	response, err := controlsys.Step(system, request.Step.Horizon)
	if err != nil {
		result.addIssue("step", err)
		return result, nil
	}
	var options *controlsys.StepInfoOptions
	if result.DCGain != nil {
		options = &controlsys.StepInfoOptions{
			SteadyStateValue: []float64{*result.DCGain},
		}
	}
	info, err := controlsys.StepInfo(response, options)
	if err != nil {
		result.addIssue("step-info", err)
		return result, nil
	}
	if len(info.Metrics) != 1 {
		result.addIssue("step-info", fmt.Errorf(
			"selected system returned %d metric rows instead of SISO", len(info.Metrics),
		))
		return result, nil
	}
	values := make([]float64, len(response.T))
	for sample := range response.T {
		values[sample] = response.Y.At(0, sample)
	}
	result.StepExperiment = &StepExperimentResult{
		Horizon: request.Step.Horizon,
		Times:   append([]float64(nil), response.T...),
		Values:  values,
		Metrics: stepMetrics(info.Metrics[0]),
	}
	return result, nil
}

func analyzedChannel(ref ChannelRef, signal compiledSignal) AnalyzedChannel {
	return AnalyzedChannel{
		ChannelRef: ref,
		Name:       signal.ChannelName,
		SignalName: signal.Name,
	}
}

func complexValues(values []complex128) []ComplexValue {
	result := make([]ComplexValue, len(values))
	for i, value := range values {
		result[i] = ComplexValue{Real: real(value), Imag: imag(value)}
	}
	return result
}

func dampingModes(values []controlsys.DampInfo) []DampingMode {
	result := make([]DampingMode, len(values))
	for i, value := range values {
		mode := DampingMode{
			Pole:             ComplexValue{Real: real(value.Pole), Imag: imag(value.Pole)},
			NaturalFrequency: value.Wn,
			DampingRatio:     value.Zeta,
			TimeConstant:     finitePointer(value.Tau),
		}
		result[i] = mode
	}
	return result
}

func stepMetrics(metric controlsys.StepMetric) StepMetrics {
	return StepMetrics{
		RiseTime:         finitePointer(metric.RiseTime),
		SettlingTime:     finitePointer(metric.SettlingTime),
		Overshoot:        finitePointer(metric.Overshoot),
		Undershoot:       finitePointer(metric.Undershoot),
		Peak:             finitePointer(metric.Peak),
		PeakTime:         finitePointer(metric.PeakTime),
		SteadyStateValue: finitePointer(metric.SteadyStateValue),
		Settled:          metric.Settled,
	}
}

func finitePointer(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return floatPointer(value)
}

func floatPointer(value float64) *float64 {
	return &value
}

func (result *DynamicsAnalysis) addIssue(operation string, err error) {
	result.Issues = append(result.Issues, AnalysisIssue{
		Operation: operation,
		Message:   err.Error(),
	})
}
