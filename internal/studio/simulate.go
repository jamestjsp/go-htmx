package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/dsp/fourier"
	"gonum.org/v1/gonum/dsp/window"
	"gonum.org/v1/gonum/mat"
)

type compiledFlow struct {
	system  *controlsys.System
	sources []Block
	sinks   []Block
}

func (s *Studio) Run(ctx context.Context, flowID int64, request SimulationRequest) (Snapshot, error) {
	if request.Duration < 1 || request.Duration > 120 {
		return Snapshot{}, invalid("duration must be between 1 and 120 seconds")
	}
	if request.SampleTime < 0.01 || request.SampleTime > 2 {
		return Snapshot{}, invalid("sample time must be between 0.01 and 2 seconds")
	}
	if request.Duration/request.SampleTime > 5000 {
		return Snapshot{}, invalid("simulation is limited to 5,000 samples")
	}

	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return Snapshot{}, err
	}
	run, err := simulate(snapshot.Blocks, snapshot.Connections, request)
	if err != nil {
		return Snapshot{}, err
	}
	run.CreatedAt = s.now().UTC()

	err = s.inTx(ctx, func(tx *sql.Tx) error {
		encoded, err := json.Marshal(run)
		if err != nil {
			return fmt.Errorf("encode simulation: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO simulation_runs(flow_id, created_at, duration, sample_time, result_json)
			VALUES(?, ?, ?, ?, ?)`,
			flowID, run.CreatedAt.Format(time.RFC3339Nano),
			run.Duration, run.SampleTime, string(encoded),
		)
		if err != nil {
			return fmt.Errorf("save simulation: %w", err)
		}
		run.ID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read simulation id: %w", err)
		}
		return insertEvent(ctx, tx, flowID, run.CreatedAt.Format(time.RFC3339Nano),
			fmt.Sprintf("Simulated %.1f seconds at %.3f s/sample", request.Duration, request.SampleTime),
		)
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

func simulate(blocks []Block, connections []Connection, request SimulationRequest) (*Simulation, error) {
	compiled, err := compileFlow(blocks, connections)
	if err != nil {
		return nil, err
	}

	steps := int(math.Round(request.Duration/request.SampleTime)) + 1
	times := make([]float64, steps)
	inputData := make([]float64, steps*len(compiled.sources))
	for i := range steps {
		times[i] = float64(i) * request.SampleTime
		for sourceIndex, source := range compiled.sources {
			inputData[i*len(compiled.sources)+sourceIndex] = sourceValue(source, times[i])
		}
	}
	input := mat.NewDense(steps, len(compiled.sources), inputData)
	response, err := controlsys.Lsim(compiled.system, input, times, nil)
	if err != nil {
		return nil, fmt.Errorf("simulate flowsheet: %w", err)
	}

	run := &Simulation{
		Duration:   request.Duration,
		SampleTime: request.SampleTime,
		Times:      times,
	}
	for output, sink := range compiled.sinks {
		values := make([]float64, steps)
		for sample := range steps {
			values[sample] = response.Y.At(output, sample)
		}
		switch sink.Kind {
		case BlockScope:
			run.Series = append(run.Series, Series{
				BlockID: sink.ID,
				Name:    sink.Name,
				Values:  values,
			})
			run.Metrics = append(run.Metrics, metricFor(sink.Name, times, values))
		case BlockSpectrum:
			run.Spectra = append(run.Spectra, spectrumFor(sink, values, request.SampleTime))
		}
	}
	return run, nil
}

func compileFlow(blocks []Block, connections []Connection) (compiledFlow, error) {
	if len(blocks) == 0 {
		return compiledFlow{}, invalid("add blocks before running the simulation")
	}

	blockByID := make(map[int64]Block, len(blocks))
	indegree := make(map[int64]int, len(blocks))
	incoming := make(map[int64][]Connection, len(blocks))
	outgoing := make(map[int64][]int64, len(blocks))
	var sources, sinks []Block
	for _, block := range blocks {
		if !block.Kind.Valid() {
			return compiledFlow{}, invalid("%s has an unknown block type", block.Name)
		}
		if err := validateParameters(block.Kind, block.Parameters); err != nil {
			return compiledFlow{}, invalid("%s: %s", block.Name, err)
		}
		blockByID[block.ID] = block
		indegree[block.ID] = 0
		switch {
		case isSource(block.Kind):
			sources = append(sources, block)
		case isSink(block.Kind):
			sinks = append(sinks, block)
		}
	}
	if len(sources) == 0 {
		return compiledFlow{}, invalid("add at least one source block before simulating")
	}
	if len(sinks) == 0 {
		return compiledFlow{}, invalid("add at least one Scope or Spectrum Analyzer before simulating")
	}

	for _, connection := range connections {
		source, sourceOK := blockByID[connection.SourceID]
		target, targetOK := blockByID[connection.TargetID]
		if !sourceOK || !targetOK {
			return compiledFlow{}, invalid("a connection references a missing block")
		}
		if !source.Kind.HasOutput() || !target.Kind.HasInput() {
			return compiledFlow{}, invalid("a connection uses an incompatible port")
		}
		outgoing[source.ID] = append(outgoing[source.ID], target.ID)
		incoming[target.ID] = append(incoming[target.ID], connection)
		indegree[target.ID]++
	}

	ready := make([]int64, 0, len(blocks))
	for _, block := range blocks {
		if indegree[block.ID] == 0 {
			ready = append(ready, block.ID)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	order := make([]int64, 0, len(blocks))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)
		for _, target := range outgoing[current] {
			indegree[target]--
			if indegree[target] == 0 {
				ready = append(ready, target)
				sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
			}
		}
	}
	if len(order) != len(blocks) {
		return compiledFlow{}, invalid("flowsheet contains a cycle; remove a feedback connection")
	}

	for _, block := range blocks {
		inputs := incoming[block.ID]
		switch {
		case isSource(block.Kind):
			if len(inputs) != 0 {
				return compiledFlow{}, invalid("%s cannot accept an input", block.Name)
			}
		case block.Kind == BlockSum:
			if len(inputs) == 0 {
				return compiledFlow{}, invalid("%s needs at least one input", block.Name)
			}
			if len(block.Parameters.Signs) != 1 && len(block.Parameters.Signs) != len(inputs) {
				return compiledFlow{}, invalid(
					"%s has %d input signs for %d connections",
					block.Name, len(block.Parameters.Signs), len(inputs),
				)
			}
		default:
			if len(inputs) == 0 {
				return compiledFlow{}, invalid("%s is not connected", block.Name)
			}
			if len(inputs) > 1 {
				return compiledFlow{}, invalid("%s accepts only one input", block.Name)
			}
		}
	}

	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	sort.Slice(sinks, func(i, j int) bool { return sinks[i].ID < sinks[j].ID })
	for targetID := range incoming {
		sort.Slice(incoming[targetID], func(i, j int) bool {
			left := incoming[targetID][i]
			right := incoming[targetID][j]
			if left.ID != right.ID {
				return left.ID < right.ID
			}
			return left.SourceID < right.SourceID
		})
	}

	systems := make([]*controlsys.System, 0, len(blocks))
	for _, id := range order {
		block := blockByID[id]
		system, err := realizeBlock(block, incoming[id])
		if err != nil {
			return compiledFlow{}, err
		}
		systems = append(systems, system)
	}

	namedConnections := make([]controlsys.Connection, 0, len(connections))
	for _, connection := range connections {
		target := blockByID[connection.TargetID]
		namedConnections = append(namedConnections, controlsys.Connection{
			From: outputSignalName(connection.SourceID),
			To:   inputSignalName(target, connection),
			Gain: 1,
		})
	}
	inputs := make([]string, len(sources))
	for i, source := range sources {
		inputs[i] = sourceSignalName(source.ID)
	}
	outputs := make([]string, len(sinks))
	for i, sink := range sinks {
		outputs[i] = outputSignalName(sink.ID)
	}
	system, err := controlsys.ConnectByName(systems, namedConnections, inputs, outputs)
	if err != nil {
		return compiledFlow{}, fmt.Errorf("compile flowsheet: %w", err)
	}
	return compiledFlow{system: system, sources: sources, sinks: sinks}, nil
}

func realizeBlock(block Block, incoming []Connection) (*controlsys.System, error) {
	var system *controlsys.System
	var err error
	switch block.Kind {
	case BlockSource, BlockConstant, BlockSine, BlockScope, BlockSpectrum:
		system, err = controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
	case BlockGain:
		system, err = controlsys.NewGain(mat.NewDense(1, 1, []float64{block.Parameters.Gain}), 0)
	case BlockSum:
		gains := make([]float64, len(incoming))
		for i := range gains {
			signIndex := min(i, len(block.Parameters.Signs)-1)
			gains[i] = 1
			if block.Parameters.Signs[signIndex] == '-' {
				gains[i] = -1
			}
		}
		system, err = controlsys.NewGain(mat.NewDense(1, len(gains), gains), 0)
	case BlockLag:
		tau := block.Parameters.TimeConstant
		system, err = controlsys.New(
			mat.NewDense(1, 1, []float64{-1 / tau}),
			mat.NewDense(1, 1, []float64{1 / tau}),
			mat.NewDense(1, 1, []float64{1}),
			mat.NewDense(1, 1, []float64{0}),
			0,
		)
	case BlockIntegrator:
		system, err = controlsys.New(
			mat.NewDense(1, 1, []float64{0}),
			mat.NewDense(1, 1, []float64{1}),
			mat.NewDense(1, 1, []float64{1}),
			mat.NewDense(1, 1, []float64{0}),
			0,
		)
	case BlockTransfer:
		result, transferErr := (&controlsys.TransferFunc{
			Num: [][][]float64{{append([]float64(nil), block.Parameters.Numerator...)}},
			Den: [][]float64{append([]float64(nil), block.Parameters.Denominator...)},
		}).StateSpace(nil)
		if transferErr != nil {
			err = transferErr
		} else {
			system = result.Sys
		}
	case BlockPID:
		system, err = controlsys.NewPID(
			block.Parameters.Proportional,
			block.Parameters.Integral,
			block.Parameters.Derivative,
			controlsys.WithFilter(block.Parameters.FilterTime),
		).System()
	case BlockDelay:
		system, err = controlsys.PadeDelay(block.Parameters.Delay, block.Parameters.Approximation)
	default:
		return nil, invalid("%s has an unsupported block type", block.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("realize %s: %w", block.Name, err)
	}

	if isSource(block.Kind) {
		system.InputName = []string{sourceSignalName(block.ID)}
	} else if block.Kind == BlockSum {
		system.InputName = make([]string, len(incoming))
		for i, connection := range incoming {
			system.InputName[i] = inputSignalName(block, connection)
		}
	} else {
		system.InputName = []string{inputSignalName(block, Connection{})}
	}
	system.OutputName = []string{outputSignalName(block.ID)}
	return system, nil
}

func isSource(kind BlockKind) bool {
	return kind == BlockSource || kind == BlockConstant || kind == BlockSine
}

func isSink(kind BlockKind) bool {
	return kind == BlockScope || kind == BlockSpectrum
}

func sourceValue(source Block, t float64) float64 {
	switch source.Kind {
	case BlockSource:
		if t < source.Parameters.StepTime {
			return source.Parameters.InitialValue
		}
		return source.Parameters.Amplitude
	case BlockConstant:
		return source.Parameters.Value
	case BlockSine:
		return source.Parameters.Bias +
			source.Parameters.Amplitude*math.Sin(source.Parameters.Frequency*t+source.Parameters.Phase)
	default:
		return 0
	}
}

func sourceSignalName(id int64) string {
	return fmt.Sprintf("block_%d_source", id)
}

func inputSignalName(block Block, connection Connection) string {
	if block.Kind == BlockSum {
		return fmt.Sprintf("block_%d_input_from_%d", block.ID, connection.SourceID)
	}
	return fmt.Sprintf("block_%d_input", block.ID)
}

func outputSignalName(id int64) string {
	return fmt.Sprintf("block_%d_output", id)
}

func spectrumFor(block Block, values []float64, sampleTime float64) Spectrum {
	spectrum := Spectrum{BlockID: block.ID, Name: block.Name}
	if len(values) < 2 {
		return spectrum
	}
	windowed := append([]float64(nil), values...)
	window.Hann(windowed)
	var windowSum float64
	for i := range values {
		windowSum += 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(len(values)-1)))
	}

	transform := fourier.NewFFT(len(windowed))
	coefficients := transform.Coefficients(nil, windowed)
	spectrum.Frequencies = make([]float64, len(coefficients))
	spectrum.Magnitudes = make([]float64, len(coefficients))
	for i, coefficient := range coefficients {
		scale := 2 / windowSum
		if i == 0 || len(values)%2 == 0 && i == len(coefficients)-1 {
			scale = 1 / windowSum
		}
		frequency := transform.Freq(i) / sampleTime
		magnitude := math.Hypot(real(coefficient), imag(coefficient)) * scale
		spectrum.Frequencies[i] = frequency
		spectrum.Magnitudes[i] = magnitude
		if magnitude > spectrum.PeakMagnitude {
			spectrum.PeakFrequency = frequency
			spectrum.PeakMagnitude = magnitude
		}
	}
	return spectrum
}

func metricFor(name string, times, values []float64) Metric {
	metric := Metric{Name: name}
	if len(values) == 0 {
		return metric
	}
	metric.Final = values[len(values)-1]
	metric.Peak = values[0]
	for _, value := range values[1:] {
		if math.Abs(value) > math.Abs(metric.Peak) {
			metric.Peak = value
		}
	}

	tolerance := math.Max(0.02*math.Abs(metric.Final), 0.002)
	for i := range values {
		settled := true
		for _, value := range values[i:] {
			if math.Abs(value-metric.Final) > tolerance {
				settled = false
				break
			}
		}
		if settled {
			metric.Settled = true
			metric.SettleTime = times[i]
			break
		}
	}
	return metric
}
