package studio

import (
	"fmt"
	"math"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

type compiledSignalRole uint8

const (
	compiledExternalInput compiledSignalRole = iota
	compiledBlockInput
	compiledBlockOutput
)

type compiledSignal struct {
	Name        string
	BlockID     int64
	Port        int
	Channel     int
	ChannelName string
	Width       int
	Role        compiledSignalRole
}

type compiledPort struct {
	blockID int64
	port    int
}

type modelProbe struct {
	BlockID    int64
	OutputPort int
}

type modelCompileRequest struct {
	includeSinks bool
	probes       []modelProbe
	baseStep     float64
}

type compiledInput struct {
	signal compiledSignal
	source Block
}

type compiledOutput struct {
	signal compiledSignal
	block  Block
}

type compiledModelDimensions struct {
	States  int
	Inputs  int
	Outputs int
}

type compiledModelTimeDomain struct {
	Domain     timeDomainKind
	SampleTime float64
}

type compiledModelProvenance struct {
	Blocks      []Block
	Connections []Connection
}

type compiledModel struct {
	system     *controlsys.System
	inputs     []compiledInput
	outputs    []compiledOutput
	signals    []compiledSignal
	provenance compiledModelProvenance
	execution  executionPartition
}

func (m *compiledModel) dimensions() compiledModelDimensions {
	states, inputs, outputs := m.system.Dims()
	return compiledModelDimensions{
		States:  states,
		Inputs:  inputs,
		Outputs: outputs,
	}
}

func (m *compiledModel) timeDomain() compiledModelTimeDomain {
	domain := timeDomainContinuous
	if m.system.IsDiscrete() {
		domain = timeDomainDiscrete
	}
	return compiledModelTimeDomain{Domain: domain, SampleTime: m.system.Dt}
}

func (m *compiledModel) inputChannels() []compiledSignal {
	channels := make([]compiledSignal, len(m.inputs))
	for i, input := range m.inputs {
		channels[i] = input.signal
	}
	return channels
}

func (m *compiledModel) outputChannels() []compiledSignal {
	channels := make([]compiledSignal, len(m.outputs))
	for i, output := range m.outputs {
		channels[i] = output.signal
	}
	return channels
}

func (m *compiledModel) signalChannels() []compiledSignal {
	return append([]compiledSignal(nil), m.signals...)
}

func (m *compiledModel) modelProvenance() compiledModelProvenance {
	blocks := make([]Block, len(m.provenance.Blocks))
	for i, block := range m.provenance.Blocks {
		blocks[i] = block
		blocks[i].Parameters = cloneParameters(block.Parameters)
	}
	return compiledModelProvenance{
		Blocks:      blocks,
		Connections: append([]Connection(nil), m.provenance.Connections...),
	}
}

func (m *compiledModel) systemCopy() *controlsys.System {
	return m.system.Copy()
}

func (m *compiledModel) selectOutputs(probes []modelProbe) (*compiledModel, error) {
	if len(probes) == 0 {
		return nil, invalid("select at least one output signal")
	}
	outputByPort := make(map[compiledPort][]compiledOutput, len(m.outputs))
	for _, output := range m.outputs {
		port := compiledPort{blockID: output.signal.BlockID, port: output.signal.Port}
		outputByPort[port] = append(outputByPort[port], output)
	}

	unique := uniqueModelProbes(probes)
	var outputs []compiledOutput
	var names []string
	for _, probe := range unique {
		portOutputs, ok := outputByPort[compiledPort{blockID: probe.BlockID, port: probe.OutputPort}]
		if !ok {
			return nil, invalid(
				"block %d output port %d was not exposed during compilation",
				probe.BlockID, probe.OutputPort,
			)
		}
		for _, output := range portOutputs {
			outputs = append(outputs, output)
			names = append(names, output.signal.Name)
		}
	}

	system, err := m.system.SelectByName(m.system.InputName, names)
	if err != nil {
		return nil, fmt.Errorf("select compiled outputs: %w", err)
	}
	return &compiledModel{
		system:     system,
		inputs:     m.inputs,
		outputs:    outputs,
		signals:    m.signals,
		provenance: m.provenance,
		execution:  m.execution,
	}, nil
}

func uniqueModelProbes(probes []modelProbe) []modelProbe {
	unique := make([]modelProbe, 0, len(probes))
	seen := make(map[modelProbe]struct{}, len(probes))
	for _, probe := range probes {
		if _, ok := seen[probe]; ok {
			continue
		}
		seen[probe] = struct{}{}
		unique = append(unique, probe)
	}
	return unique
}

func (m *compiledModel) response(request SimulationRequest) (*controlsys.TimeResponse, error) {
	if m.system.IsDiscrete() &&
		math.Abs(request.SampleTime-m.system.Dt) > 1e-9 {
		return nil, invalid(
			"run sample time %.12g s does not match the discrete model sample time %.12g s",
			request.SampleTime, m.system.Dt,
		)
	}
	steps := int(math.Round(request.Duration/request.SampleTime)) + 1
	times := make([]float64, steps)
	for i := range steps {
		times[i] = float64(i) * request.SampleTime
	}

	if m.hasExactDelay() {
		if err := m.validateExactDelaySampling(request.SampleTime); err != nil {
			return nil, err
		}
		discrete, err := m.system.DiscretizeWithOpts(request.SampleTime, controlsys.C2DOptions{
			Method:        controlsys.C2DMethodZOH,
			DelayModeling: controlsys.C2DDelayModelingInternal,
		})
		if err != nil {
			return nil, fmt.Errorf("prepare exact-delay simulation: %w", err)
		}
		inputData := make([]float64, steps*len(m.inputs))
		for sample := range steps {
			for inputIndex, input := range m.inputs {
				inputData[inputIndex*steps+sample] = sourceValue(
					input.source, input.signal.Channel, times[sample],
				)
			}
		}
		response, err := discrete.Simulate(
			mat.NewDense(len(m.inputs), steps, inputData),
			nil,
			nil,
		)
		if err != nil {
			return nil, fmt.Errorf("simulate exact-delay flowsheet: %w", err)
		}
		return &controlsys.TimeResponse{
			T:          times,
			Y:          response.Y,
			OutputName: append([]string(nil), discrete.OutputName...),
		}, nil
	}

	if m.system.IsDiscrete() {
		inputData := make([]float64, steps*len(m.inputs))
		for sample := range steps {
			for inputIndex, input := range m.inputs {
				inputData[inputIndex*steps+sample] = sourceValue(
					input.source, input.signal.Channel, times[sample],
				)
			}
		}
		response, err := simulateSystemByStep(
			m.system,
			mat.NewDense(len(m.inputs), steps, inputData),
		)
		if err != nil {
			return nil, fmt.Errorf("step discrete flowsheet: %w", err)
		}
		return &controlsys.TimeResponse{
			T:          times,
			Y:          response,
			OutputName: append([]string(nil), m.system.OutputName...),
		}, nil
	}

	inputData := make([]float64, steps*len(m.inputs))
	for i := range steps {
		for inputIndex, input := range m.inputs {
			inputData[i*len(m.inputs)+inputIndex] = sourceValue(
				input.source, input.signal.Channel, times[i],
			)
		}
	}
	input := mat.NewDense(steps, len(m.inputs), inputData)
	response, err := controlsys.Lsim(m.system, input, times, nil)
	if err != nil {
		return nil, fmt.Errorf("simulate flowsheet: %w", err)
	}
	return response, nil
}

func simulateSystemByStep(system *controlsys.System, input *mat.Dense) (*mat.Dense, error) {
	inputs, steps := input.Dims()
	_, systemInputs, outputs := system.Dims()
	if inputs != systemInputs {
		return nil, fmt.Errorf(
			"step input rows %d do not match system inputs %d",
			inputs, systemInputs,
		)
	}
	values := mat.NewDense(outputs, steps, nil)
	var state *mat.VecDense
	for sample := range steps {
		column := mat.NewDense(inputs, 1, nil)
		for inputIndex := range inputs {
			column.Set(inputIndex, 0, input.At(inputIndex, sample))
		}
		response, err := system.Simulate(column, state, nil)
		if err != nil {
			return nil, err
		}
		for output := range outputs {
			values.Set(output, sample, response.Y.At(output, 0))
		}
		state = response.XFinal
	}
	return values, nil
}

func (m *compiledModel) hasExactDelay() bool {
	for _, block := range m.provenance.Blocks {
		if block.Kind == BlockDelay &&
			normalizedDelayMode(block.Parameters) == delayModeExact &&
			block.Parameters.Delay > 0 {
			return true
		}
	}
	return false
}

func (m *compiledModel) validateExactDelaySampling(sampleTime float64) error {
	for _, block := range m.provenance.Blocks {
		if block.Kind != BlockDelay || normalizedDelayMode(block.Parameters) != delayModeExact {
			continue
		}
		samples := block.Parameters.Delay / sampleTime
		nearestSamples := math.Round(samples)
		if math.Abs(samples-nearestSamples) <= 1e-9 {
			continue
		}
		nearestDelay := nearestSamples * sampleTime
		if nearestDelay == 0 {
			nearestDelay = 0
		}
		return invalid(
			"%s exact delay %.12g s is not aligned to sample time %.12g s; nearest aligned delay is %.12g s, or select Padé or Thiran",
			block.Name, block.Parameters.Delay, sampleTime, nearestDelay,
		)
	}
	return nil
}

func (m *compiledModel) run(request SimulationRequest) (*Simulation, error) {
	response, err := m.response(request)
	if err != nil {
		return nil, err
	}
	run := &Simulation{
		Duration:   request.Duration,
		SampleTime: request.SampleTime,
		Fidelity:   m.fidelity(),
		Times:      response.T,
	}
	for outputIndex, output := range m.outputs {
		if !output.block.Kind.isSink() {
			continue
		}
		values := make([]float64, len(response.T))
		for sample := range response.T {
			values[sample] = response.Y.At(outputIndex, sample)
		}
		if output.block.Kind.isSpectrumSink() {
			run.Spectra = append(run.Spectra, spectrumFor(output.block, values, request.SampleTime))
		} else {
			run.Series = append(run.Series, Series{
				BlockID: output.block.ID,
				Name:    output.block.Name,
				Values:  values,
			})
			run.Metrics = append(run.Metrics, metricFor(output.block.Name, response.T, values))
		}
	}
	return run, nil
}

func (m *compiledModel) fidelity() Fidelity {
	fidelity := Fidelity{
		Driver:       "batch-lsim",
		SourceHold:   "piecewise-constant",
		SegmentCount: len(m.execution.segments),
	}
	if m.hasExactDelay() {
		fidelity.Driver = "delay-aware-simulate"
		fidelity.ExactDelayAligned = true
	} else if m.system.IsDiscrete() {
		fidelity.Driver = "per-sample-simulate"
	}
	for _, input := range m.inputs {
		if input.source.Kind == BlockSine {
			fidelity.SourceHold = "sampled-zero-order-hold"
			break
		}
	}
	seenDelayModels := make(map[string]struct{})
	for _, block := range m.provenance.Blocks {
		if block.Kind != BlockDelay {
			continue
		}
		mode := normalizedDelayMode(block.Parameters)
		if _, exists := seenDelayModels[mode]; exists {
			continue
		}
		seenDelayModels[mode] = struct{}{}
		fidelity.DelayModels = append(fidelity.DelayModels, mode)
	}
	return fidelity
}
