package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/dsp/fourier"
	"gonum.org/v1/gonum/dsp/window"
)

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
	model, err := compileModel(blocks, connections)
	if err != nil {
		return nil, err
	}
	return model.run(request)
}

func compileModel(blocks []Block, connections []Connection) (*compiledModel, error) {
	return compileRequestedModel(blocks, connections, modelCompileRequest{includeSinks: true})
}

func compileRequestedModel(
	blocks []Block,
	connections []Connection,
	request modelCompileRequest,
) (*compiledModel, error) {
	if len(blocks) == 0 {
		return nil, invalid("add blocks before running the simulation")
	}

	blockByID := make(map[int64]Block, len(blocks))
	incoming := make(map[int64][]Connection, len(blocks))
	var sources, sinks []Block
	for _, block := range blocks {
		if !block.Kind.Valid() {
			return nil, invalid("%s has an unknown block type", block.Name)
		}
		if err := validateParameters(block.Kind, block.Parameters); err != nil {
			return nil, invalid("%s: %s", block.Name, err)
		}
		block.Parameters = cloneParameters(block.Parameters)
		blockByID[block.ID] = block
		switch {
		case block.Kind.isSource():
			sources = append(sources, block)
		case block.Kind.isSink():
			sinks = append(sinks, block)
		}
	}
	if len(sources) == 0 {
		return nil, invalid("add at least one source block before simulating")
	}
	if request.includeSinks && len(sinks) == 0 {
		return nil, invalid("add at least one Scope or Spectrum Analyzer before simulating")
	}
	if !request.includeSinks && len(request.probes) == 0 {
		return nil, invalid("select at least one output signal before compiling")
	}

	for _, connection := range connections {
		source, sourceOK := blockByID[connection.SourceID]
		target, targetOK := blockByID[connection.TargetID]
		if !sourceOK || !targetOK {
			return nil, invalid("a connection references a missing block")
		}
		if !source.Kind.HasOutput() || !target.Kind.HasInput() {
			return nil, invalid("a connection uses an incompatible port")
		}
		incoming[target.ID] = append(incoming[target.ID], connection)
	}

	orderedBlocks := make([]Block, 0, len(blockByID))
	for _, block := range blockByID {
		orderedBlocks = append(orderedBlocks, block)
	}
	sort.Slice(orderedBlocks, func(i, j int) bool {
		return orderedBlocks[i].ID < orderedBlocks[j].ID
	})

	wiredPorts := make(map[int64][]int, len(incoming))
	for _, block := range orderedBlocks {
		inputs := incoming[block.ID]
		switch block.Kind.arity() {
		case arityNone:
			if len(inputs) != 0 {
				return nil, invalid("%s cannot accept an input", block.Name)
			}
		case arityVariadic:
			if len(inputs) == 0 {
				return nil, invalid("%s needs at least one input", block.Name)
			}
		default: // arityOne
			if len(inputs) == 0 {
				return nil, invalid("%s is not connected", block.Name)
			}
			if len(inputs) > 1 {
				return nil, invalid("%s accepts only one input", block.Name)
			}
		}
		// checkInputs is a kind's own rule tying its parameters to the
		// connected input count (Sum's signs must match), layered on top of
		// the generic arity check above rather than folded into it.
		if check := blockDefinitions[block.Kind].checkInputs; check != nil {
			if err := check(block, len(inputs)); err != nil {
				return nil, err
			}
		}

		ports, err := wiredInputPorts(block, inputs)
		if err != nil {
			return nil, err
		}
		wiredPorts[block.ID] = ports
	}

	sort.Slice(sources, func(i, j int) bool { return sources[i].ID < sources[j].ID })
	sort.Slice(sinks, func(i, j int) bool { return sinks[i].ID < sinks[j].ID })

	systems := make([]*controlsys.System, 0, len(blocks))
	sourceSignals := make(map[int64]compiledSignal, len(sources))
	inputSignals := make(map[compiledPort]compiledSignal, len(connections))
	outputSignals := make(map[compiledPort]compiledSignal, len(blocks))
	signals := make([]compiledSignal, 0, len(blocks)+len(connections)+len(sources))
	for _, block := range orderedBlocks {
		system, err := realizeBlock(block, wiredPorts[block.ID])
		if err != nil {
			return nil, err
		}
		systems = append(systems, system)

		if block.Kind.isSource() {
			signal := compiledSignal{
				Name: system.InputName[0], BlockID: block.ID,
				Port: 0, Role: compiledExternalInput,
			}
			sourceSignals[block.ID] = signal
			signals = append(signals, signal)
		} else {
			for i, port := range wiredPorts[block.ID] {
				signal := compiledSignal{
					Name: system.InputName[i], BlockID: block.ID,
					Port: port, Role: compiledBlockInput,
				}
				inputSignals[compiledPort{blockID: block.ID, port: port}] = signal
				signals = append(signals, signal)
			}
		}
		for port, name := range system.OutputName {
			signal := compiledSignal{
				Name: name, BlockID: block.ID,
				Port: port, Role: compiledBlockOutput,
			}
			outputSignals[compiledPort{blockID: block.ID, port: port}] = signal
			signals = append(signals, signal)
		}
	}

	namedConnections := make([]controlsys.Connection, 0, len(connections))
	for _, connection := range connections {
		from, ok := outputSignals[compiledPort{
			blockID: connection.SourceID,
			port:    connection.SourcePort,
		}]
		if !ok {
			return nil, invalid("%s has no output port %d",
				blockByID[connection.SourceID].Name, connection.SourcePort)
		}
		to, ok := inputSignals[compiledPort{
			blockID: connection.TargetID,
			port:    connection.TargetPort,
		}]
		if !ok {
			return nil, invalid("%s has no input port %d",
				blockByID[connection.TargetID].Name, connection.TargetPort)
		}
		namedConnections = append(namedConnections, controlsys.Connection{
			From: from.Name,
			To:   to.Name,
			Gain: 1,
		})
	}
	inputs := make([]string, len(sources))
	compiledInputs := make([]compiledInput, len(sources))
	for i, source := range sources {
		signal := sourceSignals[source.ID]
		inputs[i] = signal.Name
		compiledInputs[i] = compiledInput{signal: signal, source: source}
	}
	requestedProbes := make([]modelProbe, 0, len(sinks)+len(request.probes))
	if request.includeSinks {
		for _, sink := range sinks {
			requestedProbes = append(requestedProbes, modelProbe{
				BlockID: sink.ID, OutputPort: 0,
			})
		}
	}
	requestedProbes = append(requestedProbes, request.probes...)
	requestedProbes = uniqueModelProbes(requestedProbes)

	outputs := make([]string, len(requestedProbes))
	compiledOutputs := make([]compiledOutput, len(requestedProbes))
	for i, probe := range requestedProbes {
		block, ok := blockByID[probe.BlockID]
		if !ok {
			return nil, invalid("an analysis probe references missing block %d", probe.BlockID)
		}
		signal, ok := outputSignals[compiledPort{
			blockID: probe.BlockID,
			port:    probe.OutputPort,
		}]
		if !ok {
			return nil, invalid("%s has no output port %d", block.Name, probe.OutputPort)
		}
		outputs[i] = signal.Name
		compiledOutputs[i] = compiledOutput{signal: signal, block: block}
	}
	system, err := controlsys.ConnectByName(systems, namedConnections, inputs, outputs)
	if err != nil {
		if errors.Is(err, controlsys.ErrAlgebraicLoop) {
			return nil, invalid(
				"flowsheet contains an unsolvable algebraic loop; add dynamics or change a direct-feedthrough gain",
			)
		}
		return nil, fmt.Errorf("compile flowsheet: %w", err)
	}

	provenanceConnections := append([]Connection(nil), connections...)
	sort.Slice(provenanceConnections, func(i, j int) bool {
		left, right := provenanceConnections[i], provenanceConnections[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		if left.SourceID != right.SourceID {
			return left.SourceID < right.SourceID
		}
		if left.SourcePort != right.SourcePort {
			return left.SourcePort < right.SourcePort
		}
		if left.TargetID != right.TargetID {
			return left.TargetID < right.TargetID
		}
		return left.TargetPort < right.TargetPort
	})
	return &compiledModel{
		system:  system,
		inputs:  compiledInputs,
		outputs: compiledOutputs,
		signals: signals,
		provenance: compiledModelProvenance{
			Blocks:      orderedBlocks,
			Connections: provenanceConnections,
		},
	}, nil
}

// wiredInputPorts is the block's input terminals that carry a wire, in
// ascending port order. It is the shape everything downstream reads a block's
// inputs through — the realization's gains, its signal names, and so the
// column each wire drives — which is what puts a wire's sign under its port
// instead of under the order the wires happened to be drawn in.
func wiredInputPorts(block Block, inputs []Connection) ([]int, error) {
	ports := make([]int, len(inputs))
	for i, connection := range inputs {
		ports[i] = connection.TargetPort
	}
	sort.Ints(ports)
	// A negative index is not a terminal on any block, and it is the one bad
	// port that cannot be compiled into something harmless: Sum reads its sign
	// at that index, so the wire would panic mid-request instead of being
	// refused. Connect turns such a wire away, but the column carries no CHECK
	// to stop one being stored and copying a flowsheet reproduces it verbatim
	// — the same reach as the duplicate below, and it gets the same wording
	// Connect uses so a bad port reads the same whenever it surfaces.
	if len(ports) > 0 && ports[0] < 0 {
		return nil, invalid("%s has no input port %d", block.Name, ports[0])
	}
	for i := 1; i < len(ports); i++ {
		// One terminal, one signal. Connect refuses a second wire onto an
		// occupied port, but the schema cannot, so a model written by an older
		// version or edited by hand can still arrive holding two. Both would
		// compile to the same signal name and one would vanish into the
		// other's place, silently — hence a refusal rather than a guess at
		// which the user meant.
		if ports[i] == ports[i-1] {
			return nil, invalid("%s has more than one input on port %d", block.Name, ports[i])
		}
	}
	return ports, nil
}

// realizeBlock defers to the block's own definition for the controlsys
// realization (blockDefinition.realizeSystem), keeping only what is the
// compiler's concern here: naming the realized system's ports so
// controlsys.ConnectByName can wire it to the rest of the flowsheet.
func realizeBlock(block Block, ports []int) (*controlsys.System, error) {
	definition, ok := blockDefinitions[block.Kind]
	if !ok {
		return nil, invalid("%s has an unsupported block type", block.Name)
	}
	system, err := definition.realizeSystem(block, ports)
	if err != nil {
		return nil, fmt.Errorf("realize %s: %w", block.Name, err)
	}

	// A source's one input is the flowsheet's own input, driven by the sampled
	// waveform rather than by a wire, so it is the one input a port does not
	// name. Every other kind names exactly the terminals its wires arrived on,
	// in the same order realize built its inputs, so the two agree column for
	// column.
	if block.Kind.isSource() {
		system.InputName = []string{sourceSignalName(block.ID)}
	} else {
		system.InputName = make([]string, len(ports))
		for i, port := range ports {
			system.InputName[i] = inputSignalName(block.ID, port)
		}
	}
	// Every kind realizes a single output, so it is port 0 — including a sink,
	// whose output the compiler reads even though the canvas draws no terminal
	// there. A kind that one day drives more will name each of them here; the
	// wires already say which one they leave from.
	system.OutputName = []string{outputSignalName(block.ID, 0)}
	return system, nil
}

// sourceValue defers to the source's own waveform hook. A roleSource kind
// with no waveform set (which registering a new source without one would
// produce) is silent rather than a panic here, matching the old switch's
// default case.
func sourceValue(source Block, t float64) float64 {
	waveform := blockDefinitions[source.Kind].waveform
	if waveform == nil {
		return 0
	}
	return waveform(source.Parameters, t)
}

func sourceSignalName(id int64) string {
	return fmt.Sprintf("block_%d_source", id)
}

// inputSignalName and outputSignalName name a terminal, not a block: the port
// is part of the name, so the two ends of a wire can be spelled from the wire
// alone. That is what binds a signal to the port it landed on — a Sum's second
// input is block_7_input_1 whether it was the first wire drawn or the last.
func inputSignalName(id int64, port int) string {
	return fmt.Sprintf("block_%d_input_%d", id, port)
}

func outputSignalName(id int64, port int) string {
	return fmt.Sprintf("block_%d_output_%d", id, port)
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
