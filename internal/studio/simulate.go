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
	"gonum.org/v1/gonum/mat"
)

type signalExpression struct {
	state []float64
	input float64
}

type compiledFlow struct {
	system *controlsys.System
	scopes []Block
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
	inputData := make([]float64, steps)
	for i := range steps {
		times[i] = float64(i) * request.SampleTime
		inputData[i] = 1
	}
	input := mat.NewDense(steps, 1, inputData)
	response, err := controlsys.Lsim(compiled.system, input, times, nil)
	if err != nil {
		return nil, fmt.Errorf("simulate flowsheet: %w", err)
	}

	run := &Simulation{
		Duration:   request.Duration,
		SampleTime: request.SampleTime,
		Times:      times,
	}
	for output, scope := range compiled.scopes {
		values := make([]float64, steps)
		for sample := range steps {
			values[sample] = response.Y.At(output, sample)
		}
		run.Series = append(run.Series, Series{
			BlockID: scope.ID,
			Name:    scope.Name,
			Values:  values,
		})
		run.Metrics = append(run.Metrics, metricFor(scope.Name, times, values))
	}
	return run, nil
}

func compileFlow(blocks []Block, connections []Connection) (compiledFlow, error) {
	if len(blocks) == 0 {
		return compiledFlow{}, invalid("add blocks before running the simulation")
	}

	blockByID := make(map[int64]Block, len(blocks))
	indegree := make(map[int64]int, len(blocks))
	incoming := make(map[int64][]int64, len(blocks))
	outgoing := make(map[int64][]int64, len(blocks))
	var sources, scopes []Block
	stateIndex := make(map[int64]int)
	for _, block := range blocks {
		blockByID[block.ID] = block
		indegree[block.ID] = 0
		switch block.Kind {
		case BlockSource:
			sources = append(sources, block)
		case BlockScope:
			scopes = append(scopes, block)
		case BlockLag:
			if block.Parameters.TimeConstant < 0.05 {
				return compiledFlow{}, invalid("%s needs a positive time constant", block.Name)
			}
			stateIndex[block.ID] = len(stateIndex)
		}
	}
	if len(sources) == 0 {
		return compiledFlow{}, invalid("add at least one Source block before simulating")
	}
	if len(scopes) == 0 {
		return compiledFlow{}, invalid("add at least one Scope block before simulating")
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
		incoming[target.ID] = append(incoming[target.ID], source.ID)
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

	stateCount := len(stateIndex)
	expressions := make(map[int64]signalExpression, len(blocks))
	a := make([]float64, stateCount*stateCount)
	b := make([]float64, stateCount)
	for _, id := range order {
		block := blockByID[id]
		inputs := incoming[id]
		switch block.Kind {
		case BlockSource:
			if len(inputs) != 0 {
				return compiledFlow{}, invalid("%s cannot accept an input", block.Name)
			}
			expressions[id] = signalExpression{
				state: make([]float64, stateCount),
				input: block.Parameters.Amplitude,
			}
		case BlockGain:
			input, err := singleInput(block, inputs, expressions)
			if err != nil {
				return compiledFlow{}, err
			}
			expressions[id] = scaleExpression(input, block.Parameters.Gain)
		case BlockLag:
			input, err := singleInput(block, inputs, expressions)
			if err != nil {
				return compiledFlow{}, err
			}
			index := stateIndex[id]
			tau := block.Parameters.TimeConstant
			for column, coefficient := range input.state {
				a[index*stateCount+column] = coefficient / tau
			}
			a[index*stateCount+index] -= 1 / tau
			b[index] = input.input / tau
			state := make([]float64, stateCount)
			state[index] = 1
			expressions[id] = signalExpression{state: state}
		case BlockSum:
			if len(inputs) == 0 {
				return compiledFlow{}, invalid("%s needs at least one input", block.Name)
			}
			sum := signalExpression{state: make([]float64, stateCount)}
			for _, sourceID := range inputs {
				sum = addExpression(sum, expressions[sourceID])
			}
			expressions[id] = sum
		case BlockScope:
			input, err := singleInput(block, inputs, expressions)
			if err != nil {
				return compiledFlow{}, err
			}
			expressions[id] = input
		}
	}

	c := make([]float64, len(scopes)*stateCount)
	d := make([]float64, len(scopes))
	sort.Slice(scopes, func(i, j int) bool { return scopes[i].ID < scopes[j].ID })
	for row, scope := range scopes {
		expression := expressions[scope.ID]
		copy(c[row*stateCount:(row+1)*stateCount], expression.state)
		d[row] = expression.input
	}

	var system *controlsys.System
	var err error
	if stateCount == 0 {
		system, err = controlsys.NewGain(mat.NewDense(len(scopes), 1, d), 0)
	} else {
		system, err = controlsys.New(
			mat.NewDense(stateCount, stateCount, a),
			mat.NewDense(stateCount, 1, b),
			mat.NewDense(len(scopes), stateCount, c),
			mat.NewDense(len(scopes), 1, d),
			0,
		)
	}
	if err != nil {
		return compiledFlow{}, fmt.Errorf("compile flowsheet: %w", err)
	}
	return compiledFlow{system: system, scopes: scopes}, nil
}

func singleInput(block Block, inputIDs []int64, expressions map[int64]signalExpression) (signalExpression, error) {
	if len(inputIDs) == 0 {
		return signalExpression{}, invalid("%s is not connected", block.Name)
	}
	if len(inputIDs) > 1 {
		return signalExpression{}, invalid("%s accepts only one input", block.Name)
	}
	return expressions[inputIDs[0]], nil
}

func scaleExpression(expression signalExpression, scale float64) signalExpression {
	result := signalExpression{
		state: make([]float64, len(expression.state)),
		input: expression.input * scale,
	}
	for i, coefficient := range expression.state {
		result.state[i] = coefficient * scale
	}
	return result
}

func addExpression(left, right signalExpression) signalExpression {
	result := signalExpression{
		state: make([]float64, len(left.state)),
		input: left.input + right.input,
	}
	for i := range result.state {
		result.state[i] = left.state[i] + right.state[i]
	}
	return result
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
