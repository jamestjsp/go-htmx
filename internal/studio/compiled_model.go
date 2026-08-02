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

type ChannelRef struct {
	BlockID int64 `json:"blockId"`
	Port    int   `json:"port"`
	Channel int   `json:"channel"`
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

	system, err := selectSystemChannels(m.system, m.system.InputName, names)
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

func (m *compiledModel) selectChannels(
	inputRefs []ChannelRef,
	outputRefs []ChannelRef,
) (*controlsys.System, []compiledSignal, []compiledSignal, error) {
	inputs, inputNames, err := selectCompiledChannels(
		inputRefs,
		func(ref ChannelRef) (compiledSignal, bool) {
			for _, input := range m.inputs {
				signal := input.signal
				if signal.BlockID == ref.BlockID &&
					signal.Port == ref.Port &&
					signal.Channel == ref.Channel {
					return signal, true
				}
			}
			return compiledSignal{}, false
		},
		"input",
	)
	if err != nil {
		return nil, nil, nil, err
	}
	outputs, outputNames, err := selectCompiledChannels(
		outputRefs,
		func(ref ChannelRef) (compiledSignal, bool) {
			for _, output := range m.outputs {
				signal := output.signal
				if signal.BlockID == ref.BlockID &&
					signal.Port == ref.Port &&
					signal.Channel == ref.Channel {
					return signal, true
				}
			}
			return compiledSignal{}, false
		},
		"output",
	)
	if err != nil {
		return nil, nil, nil, err
	}
	system, err := selectSystemChannels(m.system, inputNames, outputNames)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("select compiled channels: %w", err)
	}
	return system, inputs, outputs, nil
}

func selectSystemChannels(
	system *controlsys.System,
	inputNames []string,
	outputNames []string,
) (*controlsys.System, error) {
	inputs, err := namedSignalIndices(system.InputName, inputNames)
	if err != nil {
		return nil, fmt.Errorf("select inputs: %w", err)
	}
	outputs, err := namedSignalIndices(system.OutputName, outputNames)
	if err != nil {
		return nil, fmt.Errorf("select outputs: %w", err)
	}
	selected, err := system.SelectByIndex(inputs, outputs)
	if err != nil {
		return nil, err
	}
	if system.E != nil {
		selected.E = mat.DenseCopyOf(system.E)
	}
	if system.Delay != nil {
		if err := selected.SetDelay(selectDense(system.Delay, outputs, inputs)); err != nil {
			return nil, fmt.Errorf("select pairwise delays: %w", err)
		}
	}
	if system.InputDelay != nil {
		if err := selected.SetInputDelay(selectFloatValues(system.InputDelay, inputs)); err != nil {
			return nil, fmt.Errorf("select input delays: %w", err)
		}
	}
	if system.OutputDelay != nil {
		if err := selected.SetOutputDelay(selectFloatValues(system.OutputDelay, outputs)); err != nil {
			return nil, fmt.Errorf("select output delays: %w", err)
		}
	}
	if system.LFT != nil {
		if err := selected.SetInternalDelay(
			system.LFT.Tau,
			system.LFT.B2,
			system.LFT.C2,
			selectDenseRows(system.LFT.D12, outputs),
			selectDenseColumns(system.LFT.D21, inputs),
			system.LFT.D22,
		); err != nil {
			return nil, fmt.Errorf("select internal delay realization: %w", err)
		}
	}
	return selected, nil
}

func namedSignalIndices(available, selected []string) ([]int, error) {
	indices := make(map[string]int, len(available))
	for index, name := range available {
		indices[name] = index
	}
	result := make([]int, len(selected))
	for i, name := range selected {
		index, ok := indices[name]
		if !ok {
			return nil, fmt.Errorf("signal %q is not available", name)
		}
		result[i] = index
	}
	return result, nil
}

func selectDense(matrix *mat.Dense, rows, columns []int) *mat.Dense {
	result := mat.NewDense(len(rows), len(columns), nil)
	for i, row := range rows {
		for j, column := range columns {
			result.Set(i, j, matrix.At(row, column))
		}
	}
	return result
}

func selectDenseRows(matrix *mat.Dense, rows []int) *mat.Dense {
	_, columns := matrix.Dims()
	selectedColumns := make([]int, columns)
	for i := range selectedColumns {
		selectedColumns[i] = i
	}
	return selectDense(matrix, rows, selectedColumns)
}

func selectDenseColumns(matrix *mat.Dense, columns []int) *mat.Dense {
	rows, _ := matrix.Dims()
	selectedRows := make([]int, rows)
	for i := range selectedRows {
		selectedRows[i] = i
	}
	return selectDense(matrix, selectedRows, columns)
}

func selectFloatValues(values []float64, indices []int) []float64 {
	selected := make([]float64, len(indices))
	for i, index := range indices {
		selected[i] = values[index]
	}
	return selected
}

func selectCompiledChannels(
	refs []ChannelRef,
	resolve func(ChannelRef) (compiledSignal, bool),
	role string,
) ([]compiledSignal, []string, error) {
	if len(refs) == 0 {
		return nil, nil, invalid("select at least one %s channel", role)
	}
	signals := make([]compiledSignal, 0, len(refs))
	names := make([]string, 0, len(refs))
	seen := make(map[ChannelRef]struct{}, len(refs))
	for _, ref := range refs {
		if _, exists := seen[ref]; exists {
			return nil, nil, invalid(
				"%s channel block %d port %d channel %d is selected more than once",
				role, ref.BlockID, ref.Port, ref.Channel,
			)
		}
		seen[ref] = struct{}{}
		signal, ok := resolve(ref)
		if !ok {
			return nil, nil, invalid(
				"%s channel block %d port %d channel %d is not exposed by the compiled model",
				role, ref.BlockID, ref.Port, ref.Channel,
			)
		}
		signals = append(signals, signal)
		names = append(names, signal.Name)
	}
	return signals, names, nil
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

	if m.requiresDelayAwareDiscretization() {
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

func (m *compiledModel) requiresDelayAwareDiscretization() bool {
	return m.system.IsContinuous() && m.system.HasDelay()
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
			// Normalize IEEE -0 so the suggested delay is never rendered as "-0 s".
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
	resultChannels := 0
	for _, output := range m.outputs {
		if output.block.Kind.isSink() {
			resultChannels++
		}
	}
	if resultChannels > maxSimulationResultChannels {
		return nil, invalid(
			"simulation is limited to %d plotted result channels; reduce the connected Scope channels",
			maxSimulationResultChannels,
		)
	}
	response, err := m.response(request)
	if err != nil {
		return nil, err
	}
	fidelity, err := m.fidelity(request.SampleTime)
	if err != nil {
		return nil, fmt.Errorf("record simulation fidelity: %w", err)
	}
	run := &Simulation{
		Duration:   request.Duration,
		SampleTime: request.SampleTime,
		Fidelity:   fidelity,
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
			run.Spectra = append(run.Spectra, spectrumFor(output, values, request.SampleTime))
		} else {
			name := resultChannelLabel(output)
			run.Series = append(run.Series, Series{
				ResultChannel: resultChannel(output, name),
				Values:        values,
			})
			run.Metrics = append(run.Metrics, metricFor(output, response.T, values))
		}
	}
	return run, nil
}

func (m *compiledModel) fidelity(baseStep float64) (Fidelity, error) {
	fidelity := Fidelity{
		BaseStep:     baseStep,
		ModelDomain:  string(timeDomainContinuous),
		Driver:       "batch-lsim",
		SourceHold:   "piecewise-constant",
		SegmentCount: len(m.execution.segments),
	}
	if m.system.IsDiscrete() {
		fidelity.ModelDomain = string(timeDomainDiscrete)
	}
	if m.requiresDelayAwareDiscretization() {
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
		domain := blockDefinitions[block.Kind].domain(block.Parameters)
		if domain.kind == timeDomainDiscrete {
			sampleTime, err := domain.sampleTime.resolve(baseStep)
			if err != nil {
				return Fidelity{}, fmt.Errorf("%s sample time: %w", block.Name, err)
			}
			schedule, err := scheduleSampleTime(sampleTime, baseStep)
			if err != nil {
				return Fidelity{}, fmt.Errorf("%s sample schedule: %w", block.Name, err)
			}
			fidelity.BlockRates = append(fidelity.BlockRates, BlockRate{
				BlockID:     block.ID,
				BlockName:   block.Name,
				Mode:        string(domain.sampleTime.mode),
				SampleTime:  sampleTime,
				UpdateEvery: schedule.updateEvery,
			})
		}
		if block.Kind != BlockDelay {
			continue
		}
		mode := normalizedDelayMode(block.Parameters)
		delay := DelayProvenance{
			BlockID:        block.ID,
			BlockName:      block.Name,
			Representation: mode,
			Delay:          block.Parameters.Delay,
		}
		switch mode {
		case delayModeExact:
			delay.SampleTime = baseStep
			delay.Aligned = true
		case delayModePade:
			delay.ApproximationOrder = block.Parameters.Approximation
		case delayModeThiran:
			delay.ApproximationOrder = block.Parameters.Approximation
			delay.SampleTimeMode = string(normalizedSampleTimeMode(block.Parameters))
			sampleTime, resolveErr := blockDefinitions[block.Kind].
				domain(block.Parameters).sampleTime.resolve(baseStep)
			if resolveErr != nil {
				return Fidelity{}, fmt.Errorf("%s Thiran sample time: %w", block.Name, resolveErr)
			}
			delay.SampleTime = sampleTime
		}
		fidelity.Delays = append(fidelity.Delays, delay)
		if _, exists := seenDelayModels[mode]; exists {
			continue
		}
		seenDelayModels[mode] = struct{}{}
		fidelity.DelayModels = append(fidelity.DelayModels, mode)
	}
	return fidelity, nil
}
