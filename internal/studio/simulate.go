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
	"gonum.org/v1/gonum/mat"
)

const (
	MinSimulationDuration       float64 = 1
	MinSimulationSampleTime     float64 = 0.001
	maxSimulationSamples                = 5000
	maxSimulationResultChannels         = 16
	maxSimulationSamplesLabel           = "5,000"
)

func SimulationLimitsText() string {
	return fmt.Sprintf(
		"Up to %s samples and %d plotted channels per run.",
		maxSimulationSamplesLabel, maxSimulationResultChannels,
	)
}

func (s *Studio) Run(ctx context.Context, flowID int64, request SimulationRequest) (Snapshot, error) {
	if err := validateSimulationRequest(request); err != nil {
		return Snapshot{}, err
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

func validateSimulationRequest(request SimulationRequest) error {
	if math.IsNaN(request.Duration) || math.IsInf(request.Duration, 0) ||
		request.Duration < MinSimulationDuration {
		return invalid(
			"duration must be finite and at least %g second",
			MinSimulationDuration,
		)
	}
	if math.IsNaN(request.SampleTime) || math.IsInf(request.SampleTime, 0) ||
		request.SampleTime < MinSimulationSampleTime {
		return invalid(
			"sample time must be finite and at least %g seconds",
			MinSimulationSampleTime,
		)
	}
	samples := math.Round(request.Duration/request.SampleTime) + 1
	if samples > maxSimulationSamples {
		return invalid("simulation is limited to %s samples", maxSimulationSamplesLabel)
	}
	return nil
}

func simulate(blocks []Block, connections []Connection, request SimulationRequest) (*Simulation, error) {
	model, err := compileRequestedModel(blocks, connections, modelCompileRequest{
		includeSinks: true,
		baseStep:     request.SampleTime,
	})
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
	originalBlockByID := make(map[int64]Block, len(blocks))
	incoming := make(map[int64][]Connection, len(blocks))
	var sources, sinks []Block
	for _, block := range blocks {
		if existing, ok := blockByID[block.ID]; ok {
			return nil, invalid("%s and %s share block id %d", existing.Name, block.Name, block.ID)
		}
		if !block.Kind.Valid() {
			return nil, invalid("%s has an unknown block type", block.Name)
		}
		original := block
		original.Parameters = cloneParameters(block.Parameters)
		block, err := resolveBlockForCompilation(block, request.baseStep)
		if err != nil {
			return nil, invalid("%s: %s", original.Name, err)
		}
		if err := validateParameters(block.Kind, block.Parameters); err != nil {
			return nil, invalid("%s: %s", block.Name, err)
		}
		block.Parameters = cloneParameters(block.Parameters)
		blockByID[block.ID] = block
		originalBlockByID[block.ID] = original
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
		if err := validateConnectionWidth(
			source, connection.SourcePort, target, connection.TargetPort,
		); err != nil {
			return nil, err
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

	execution, err := buildExecutionPartition(
		orderedBlocks,
		connections,
		func(block Block) bool { return block.Kind.isStepBlock() },
	)
	if err != nil {
		return nil, err
	}

	systems := make([]*controlsys.System, 0, len(blocks))
	initialState := make([]float64, 0)
	hasAuthoredInitialState := false
	sourceSignals := make(map[int64][]compiledSignal, len(sources))
	inputSignals := make(map[compiledPort][]compiledSignal, len(connections))
	outputSignals := make(map[compiledPort][]compiledSignal, len(blocks))
	signals := make([]compiledSignal, 0, len(blocks)+len(connections)+len(sources))
	for _, block := range orderedBlocks {
		system, err := realizeBlock(block, wiredPorts[block.ID])
		if err != nil {
			return nil, err
		}
		systems = append(systems, system)
		states, _, _ := system.Dims()
		blockInitialState := make([]float64, states)
		if initial := blockDefinitions[block.Kind].initialState; initial != nil {
			authored := initial(block.Parameters)
			if len(authored) != states {
				return nil, fmt.Errorf(
					"%s initial state has %d values for %d realization states",
					block.Name, len(authored), states,
				)
			}
			copy(blockInitialState, authored)
			for _, value := range authored {
				if value != 0 {
					hasAuthoredInitialState = true
				}
			}
		}
		initialState = append(initialState, blockInitialState...)

		if block.Kind.isSource() {
			port, _ := block.OutputPort(0)
			blockSignals := make([]compiledSignal, port.Width)
			for channel := range port.Width {
				signal := compiledSignal{
					Name: system.InputName[channel], BlockID: block.ID,
					Port: 0, Channel: channel, ChannelName: port.Channels[channel],
					Width: port.Width, Role: compiledExternalInput,
				}
				blockSignals[channel] = signal
				signals = append(signals, signal)
			}
			sourceSignals[block.ID] = blockSignals
		} else {
			offset := 0
			for _, portIndex := range wiredPorts[block.ID] {
				port, _ := resolvedInputPort(block, portIndex)
				portSignals := make([]compiledSignal, port.Width)
				for channel := range port.Width {
					signal := compiledSignal{
						Name: system.InputName[offset], BlockID: block.ID,
						Port: portIndex, Channel: channel, ChannelName: port.Channels[channel],
						Width: port.Width, Role: compiledBlockInput,
					}
					offset++
					portSignals[channel] = signal
					signals = append(signals, signal)
				}
				inputSignals[compiledPort{blockID: block.ID, port: portIndex}] = portSignals
			}
		}
		outputPorts := block.portSchema().outputs
		if block.Kind.isSink() {
			outputPorts = block.portSchema().inputs
		}
		offset := 0
		for portIndex, port := range outputPorts {
			portSignals := make([]compiledSignal, port.Width)
			for channel := range port.Width {
				signal := compiledSignal{
					Name: system.OutputName[offset], BlockID: block.ID,
					Port: portIndex, Channel: channel, ChannelName: port.Channels[channel],
					Width: port.Width, Role: compiledBlockOutput,
				}
				offset++
				portSignals[channel] = signal
				signals = append(signals, signal)
			}
			outputSignals[compiledPort{blockID: block.ID, port: portIndex}] = portSignals
		}
	}
	if err := applyCompiledTimeDomains(orderedBlocks, systems, request.baseStep); err != nil {
		return nil, err
	}

	namedConnections := make([]controlsys.Connection, 0, len(connections))
	for _, connection := range connections {
		fromChannels, ok := outputSignals[compiledPort{
			blockID: connection.SourceID,
			port:    connection.SourcePort,
		}]
		if !ok {
			return nil, invalid("%s has no output port %d",
				blockByID[connection.SourceID].Name, connection.SourcePort)
		}
		toChannels, ok := inputSignals[compiledPort{
			blockID: connection.TargetID,
			port:    connection.TargetPort,
		}]
		if !ok {
			return nil, invalid("%s has no input port %d",
				blockByID[connection.TargetID].Name, connection.TargetPort)
		}
		if len(fromChannels) != len(toChannels) {
			return nil, invalid("a connection changed width during compilation")
		}
		for channel := range fromChannels {
			namedConnections = append(namedConnections, controlsys.Connection{
				From: fromChannels[channel].Name,
				To:   toChannels[channel].Name,
				Gain: 1,
			})
		}
	}
	var inputs []string
	var compiledInputs []compiledInput
	for _, source := range sources {
		for _, signal := range sourceSignals[source.ID] {
			inputs = append(inputs, signal.Name)
			compiledInputs = append(compiledInputs, compiledInput{
				signal: signal, source: originalBlockByID[source.ID],
			})
		}
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

	var outputs []string
	var compiledOutputs []compiledOutput
	for _, probe := range requestedProbes {
		block, ok := blockByID[probe.BlockID]
		if !ok {
			return nil, invalid("an analysis probe references missing block %d", probe.BlockID)
		}
		portSignals, ok := outputSignals[compiledPort{
			blockID: probe.BlockID,
			port:    probe.OutputPort,
		}]
		if !ok {
			return nil, invalid("%s has no output port %d", block.Name, probe.OutputPort)
		}
		for _, signal := range portSignals {
			outputs = append(outputs, signal.Name)
			compiledOutputs = append(compiledOutputs, compiledOutput{
				signal: signal, block: originalBlockByID[block.ID],
			})
		}
	}

	staticDelays, err := prepareStaticExactDelays(
		orderedBlocks, connections, sources, requestedProbes, systems,
	)
	if err != nil {
		return nil, err
	}
	if err := prepareBlockDelayRealizations(orderedBlocks, systems); err != nil {
		return nil, err
	}
	system, err := controlsys.ConnectByName(systems, namedConnections, inputs, outputs)
	if err != nil {
		if errors.Is(err, controlsys.ErrAlgebraicLoop) {
			return nil, invalid("%s", algebraicLoopMessage(err, signals, originalBlockByID))
		}
		if errors.Is(err, controlsys.ErrDomainMismatch) {
			return nil, invalid(
				"flowsheet mixes continuous systems with discrete systems or incompatible sample times; use one time domain and one discrete sample time",
			)
		}
		return nil, fmt.Errorf("compile flowsheet: %w", err)
	}
	if staticDelays != nil {
		if err := system.SetDelay(staticDelays); err != nil {
			return nil, fmt.Errorf("attach exact transport delays: %w", err)
		}
	}
	compiledStates, _, _ := system.Dims()
	if compiledStates != len(initialState) {
		return nil, fmt.Errorf(
			"compiled model has %d states after interconnection, want %d authored block states",
			compiledStates, len(initialState),
		)
	}
	if !hasAuthoredInitialState {
		initialState = nil
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
	provenanceBlocks := make([]Block, len(orderedBlocks))
	for i, block := range orderedBlocks {
		provenanceBlocks[i] = originalBlockByID[block.ID]
	}
	return &compiledModel{
		system:       system,
		initialState: initialState,
		inputs:       compiledInputs,
		outputs:      compiledOutputs,
		signals:      signals,
		provenance: compiledModelProvenance{
			Blocks:      provenanceBlocks,
			Connections: provenanceConnections,
		},
		execution: execution,
	}, nil
}

func resolveBlockForCompilation(block Block, baseStep float64) (Block, error) {
	domain := blockDefinitions[block.Kind].domain(block.Parameters)
	if domain.kind != timeDomainDiscrete {
		return block, nil
	}
	sampleTime, err := domain.sampleTime.resolve(baseStep)
	if err != nil {
		return Block{}, err
	}
	block.Parameters.SampleTime = sampleTime
	block.Parameters.SampleTimeMode = string(sampleTimeExplicit)
	return block, nil
}

func applyCompiledTimeDomains(blocks []Block, systems []*controlsys.System, baseStep float64) error {
	var (
		hasContinuous bool
		sampleTimes   []float64
		discreteNames []string
	)
	for i, block := range blocks {
		domain := blockDefinitions[block.Kind].domain(block.Parameters)
		switch domain.kind {
		case timeDomainContinuous:
			hasContinuous = true
		case timeDomainDiscrete:
			sampleTime, err := domain.sampleTime.resolve(0)
			if err != nil {
				return invalid("%s: %s", block.Name, err)
			}
			if !systems[i].IsDiscrete() || math.Abs(systems[i].Dt-sampleTime) > 1e-9 {
				return fmt.Errorf(
					"%s realization sample time %.12g does not match its catalog sample time %.12g",
					block.Name, systems[i].Dt, sampleTime,
				)
			}
			sampleTimes = append(sampleTimes, sampleTime)
			discreteNames = append(discreteNames, block.Name)
		}
	}
	if len(sampleTimes) == 0 {
		return nil
	}
	if hasContinuous {
		return invalid(
			"flowsheet mixes continuous dynamics with discrete dynamics; add an explicit sampled-data boundary",
		)
	}

	if baseStep > 0 {
		for i, blockSampleTime := range sampleTimes {
			schedule, err := scheduleSampleTime(blockSampleTime, baseStep)
			if err != nil {
				return invalid("%s: %s", discreteNames[i], err)
			}
			if schedule.updateEvery > 1 {
				return invalid(
					"%s sample time %.12g s updates every %d run samples and requires segmented zero-order-hold execution",
					discreteNames[i], blockSampleTime, schedule.updateEvery,
				)
			}
		}
	}

	sampleTime := sampleTimes[0]
	for _, other := range sampleTimes[1:] {
		compatibility := compareSampleTimes(sampleTime, other)
		switch compatibility.relation {
		case sampleTimesEqual:
			continue
		case sampleTimesIntegerMultiple:
			return invalid(
				"discrete sample times %.12g s and %.12g s have integer ratio %d and require segmented zero-order-hold execution",
				compatibility.fast, compatibility.slow, compatibility.ratio,
			)
		default:
			return invalid(
				"discrete sample times %.12g s and %.12g s are not integer multiples",
				compatibility.fast, compatibility.slow,
			)
		}
	}

	for i, block := range blocks {
		if blockDefinitions[block.Kind].domain(block.Parameters).kind == timeDomainNeutral {
			systems[i].Dt = sampleTime
		}
	}
	return nil
}

func prepareStaticExactDelays(
	blocks []Block,
	connections []Connection,
	sources []Block,
	probes []modelProbe,
	systems []*controlsys.System,
) (*mat.Dense, error) {
	hasExactDelay := false
	totalStates := 0
	for i, block := range blocks {
		states, _, _ := systems[i].Dims()
		totalStates += states
		if block.Kind == BlockDelay &&
			normalizedDelayMode(block.Parameters) == delayModeExact &&
			block.Parameters.Delay > 0 {
			hasExactDelay = true
		}
	}
	if !hasExactDelay || totalStates > 0 {
		return nil, nil
	}

	outgoing := make(map[int64][]int64, len(blocks))
	for _, connection := range connections {
		outgoing[connection.SourceID] = append(outgoing[connection.SourceID], connection.TargetID)
	}
	blockByID := make(map[int64]Block, len(blocks))
	for _, block := range blocks {
		blockByID[block.ID] = block
	}

	delayData := make([]float64, len(probes)*len(sources))
	for outputIndex, probe := range probes {
		output := blockByID[probe.BlockID]
		for inputIndex, source := range sources {
			delays, cycle := exactPathDelays(
				source.ID, probe.BlockID, 0, outgoing, blockByID, make(map[int64]bool),
			)
			if cycle {
				return nil, invalid(
					"%s to %s contains a static exact-delay loop that controlsys cannot realize; add plant dynamics or select Padé or Thiran",
					source.Name, output.Name,
				)
			}
			unique := uniqueDelays(delays)
			if len(unique) > 1 {
				return nil, invalid(
					"%s to %s has parallel static paths with different exact delays; add dynamics or select Padé or Thiran",
					source.Name, output.Name,
				)
			}
			if len(unique) == 1 {
				delayData[outputIndex*len(sources)+inputIndex] = unique[0]
			}
		}
	}

	for i, block := range blocks {
		if block.Kind != BlockDelay || normalizedDelayMode(block.Parameters) != delayModeExact {
			continue
		}
		systems[i].Delay = nil
		systems[i].InputDelay = nil
		systems[i].OutputDelay = nil
		systems[i].LFT = nil
	}
	return mat.NewDense(len(probes), len(sources), delayData), nil
}

func prepareBlockDelayRealizations(blocks []Block, systems []*controlsys.System) error {
	for index, system := range systems {
		if !system.HasDelay() {
			continue
		}
		prepared, err := system.PullDelaysToLFT()
		if err != nil {
			return fmt.Errorf("prepare %s delay realization: %w", blocks[index].Name, err)
		}
		systems[index] = prepared
	}
	return nil
}

func exactPathDelays(
	current, target int64,
	delay float64,
	outgoing map[int64][]int64,
	blocks map[int64]Block,
	visiting map[int64]bool,
) ([]float64, bool) {
	if block := blocks[current]; block.Kind == BlockDelay &&
		normalizedDelayMode(block.Parameters) == delayModeExact {
		delay += block.Parameters.Delay
	}
	if current == target {
		return []float64{delay}, false
	}
	if visiting[current] {
		return nil, true
	}
	visiting[current] = true
	defer delete(visiting, current)

	var delays []float64
	for _, next := range outgoing[current] {
		nextDelays, cycle := exactPathDelays(next, target, delay, outgoing, blocks, visiting)
		if cycle {
			return nil, true
		}
		delays = append(delays, nextDelays...)
	}
	return delays, false
}

func uniqueDelays(delays []float64) []float64 {
	var unique []float64
	for _, delay := range delays {
		found := false
		for _, existing := range unique {
			if math.Abs(delay-existing) <= 1e-9 {
				found = true
				break
			}
		}
		if !found {
			unique = append(unique, delay)
		}
	}
	return unique
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
	_, inputs, outputs := system.Dims()

	// A source's one input is the flowsheet's own input, driven by the sampled
	// waveform rather than by a wire, so it is the one input a port does not
	// name. Every other kind names exactly the terminals its wires arrived on,
	// in the same order realize built its inputs, so the two agree column for
	// column.
	if block.Kind.isSource() {
		schema, ok := block.OutputPort(0)
		if !ok || inputs != schema.Width {
			return nil, fmt.Errorf(
				"realize %s: source realization has %d inputs for output width %d",
				block.Name, inputs, schema.Width,
			)
		}
		system.InputName = make([]string, schema.Width)
		for channel := range schema.Width {
			system.InputName[channel] = sourceChannelSignalName(
				block.ID, channel, schema.Width,
			)
		}
	} else {
		expectedInputs := 0
		for _, port := range ports {
			schema, ok := resolvedInputPort(block, port)
			if !ok {
				return nil, invalid("%s has no input port %d", block.Name, port)
			}
			expectedInputs += schema.Width
		}
		if inputs != expectedInputs {
			return nil, fmt.Errorf(
				"realize %s: controlsys input dimension %d does not match port width %d",
				block.Name, inputs, expectedInputs,
			)
		}
		system.InputName = make([]string, 0, expectedInputs)
		for _, port := range ports {
			schema, _ := resolvedInputPort(block, port)
			for channel := range schema.Width {
				system.InputName = append(system.InputName, inputChannelSignalName(
					block.ID, port, channel, schema.Width,
				))
			}
		}
	}
	outputSchemas := block.portSchema().outputs
	if block.Kind.isSink() {
		outputSchemas = block.portSchema().inputs
	}
	expectedOutputs := 0
	for _, schema := range outputSchemas {
		expectedOutputs += schema.Width
	}
	if outputs != expectedOutputs {
		return nil, fmt.Errorf(
			"realize %s: controlsys output dimension %d does not match port width %d",
			block.Name, outputs, expectedOutputs,
		)
	}
	system.OutputName = make([]string, 0, expectedOutputs)
	for port, schema := range outputSchemas {
		for channel := range schema.Width {
			system.OutputName = append(system.OutputName, outputChannelSignalName(
				block.ID, port, channel, schema.Width,
			))
		}
	}
	return system, nil
}

// sourceValue defers to the source's own waveform hook. A roleSource kind
// with no waveform set (which registering a new source without one would
// produce) is silent rather than a panic here, matching the old switch's
// default case.
func sourceValue(source Block, channel int, t float64) float64 {
	waveform := blockDefinitions[source.Kind].waveform
	if waveform == nil {
		return 0
	}
	return waveform(source.Parameters, channel, t)
}

func sourceSignalName(id int64) string {
	return fmt.Sprintf("block_%d_source", id)
}

func sourceChannelSignalName(id int64, channel, width int) string {
	if width == 1 {
		return sourceSignalName(id)
	}
	return fmt.Sprintf("block_%d_source_channel_%d", id, channel)
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

func inputChannelSignalName(id int64, port, channel, width int) string {
	if width == 1 {
		return inputSignalName(id, port)
	}
	return fmt.Sprintf("block_%d_input_%d_channel_%d", id, port, channel)
}

func outputChannelSignalName(id int64, port, channel, width int) string {
	if width == 1 {
		return outputSignalName(id, port)
	}
	return fmt.Sprintf("block_%d_output_%d_channel_%d", id, port, channel)
}

func resultChannelLabel(output compiledOutput) string {
	if output.signal.Width <= 1 || output.signal.ChannelName == "" {
		return output.block.Name
	}
	return output.block.Name + " · " + output.signal.ChannelName
}

func resultChannel(output compiledOutput, name string) ResultChannel {
	return ResultChannel{
		BlockID: output.block.ID, Port: output.signal.Port,
		Channel: output.signal.Channel, ChannelName: output.signal.ChannelName,
		Name: name,
	}
}

func spectrumFor(output compiledOutput, values []float64, sampleTime float64) Spectrum {
	spectrum := Spectrum{
		ResultChannel: resultChannel(output, resultChannelLabel(output)),
	}
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

func metricFor(output compiledOutput, times, values []float64) Metric {
	metric := Metric{
		ResultChannel: resultChannel(output, resultChannelLabel(output)),
	}
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
