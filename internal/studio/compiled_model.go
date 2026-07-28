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
	Name    string
	BlockID int64
	Port    int
	Role    compiledSignalRole
}

type compiledPort struct {
	blockID int64
	port    int
}

type compiledInput struct {
	signal compiledSignal
	source Block
}

type compiledOutput struct {
	signal compiledSignal
	sink   Block
}

type compiledModelDimensions struct {
	States  int
	Inputs  int
	Outputs int
}

type compiledModelDomain uint8

const (
	compiledContinuous compiledModelDomain = iota
	compiledDiscrete
)

type compiledModelTimeDomain struct {
	Domain     compiledModelDomain
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
	domain := compiledContinuous
	if m.system.IsDiscrete() {
		domain = compiledDiscrete
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

func (m *compiledModel) run(request SimulationRequest) (*Simulation, error) {
	steps := int(math.Round(request.Duration/request.SampleTime)) + 1
	times := make([]float64, steps)
	inputData := make([]float64, steps*len(m.inputs))
	for i := range steps {
		times[i] = float64(i) * request.SampleTime
		for inputIndex, input := range m.inputs {
			inputData[i*len(m.inputs)+inputIndex] = sourceValue(input.source, times[i])
		}
	}
	input := mat.NewDense(steps, len(m.inputs), inputData)
	response, err := controlsys.Lsim(m.system, input, times, nil)
	if err != nil {
		return nil, fmt.Errorf("simulate flowsheet: %w", err)
	}

	run := &Simulation{
		Duration:   request.Duration,
		SampleTime: request.SampleTime,
		Times:      times,
	}
	for outputIndex, output := range m.outputs {
		values := make([]float64, steps)
		for sample := range steps {
			values[sample] = response.Y.At(outputIndex, sample)
		}
		if output.sink.Kind.isSpectrumSink() {
			run.Spectra = append(run.Spectra, spectrumFor(output.sink, values, request.SampleTime))
		} else {
			run.Series = append(run.Series, Series{
				BlockID: output.sink.ID,
				Name:    output.sink.Name,
				Values:  values,
			})
			run.Metrics = append(run.Metrics, metricFor(output.sink.Name, times, values))
		}
	}
	return run, nil
}
