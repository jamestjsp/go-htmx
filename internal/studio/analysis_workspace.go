package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type AnalysisIntent string

const (
	AnalysisIntentNone      AnalysisIntent = ""
	AnalysisIntentDynamics  AnalysisIntent = "dynamics"
	AnalysisIntentFrequency AnalysisIntent = "frequency"
	AnalysisIntentLoop      AnalysisIntent = "loop"
)

type AnalysisWorkspaceRequest struct {
	Intent               AnalysisIntent `json:"intent"`
	Input                ChannelRef     `json:"input"`
	Output               ChannelRef     `json:"output"`
	Inputs               []ChannelRef   `json:"inputs,omitempty"`
	Outputs              []ChannelRef   `json:"outputs,omitempty"`
	FrequencyAllChannels bool           `json:"frequencyAllChannels,omitempty"`
	BaseStep             float64        `json:"baseStep,omitempty"`
	StepHorizon          float64        `json:"stepHorizon,omitempty"`
	Points               int            `json:"points,omitempty"`
}

type AnalysisWorkspace struct {
	FlowID         int64                    `json:"flowId"`
	ModelUpdatedAt time.Time                `json:"modelUpdatedAt"`
	Inputs         []AnalysisChannel        `json:"inputs"`
	Outputs        []AnalysisChannel        `json:"outputs"`
	SelectedInput  ChannelRef               `json:"selectedInput"`
	SelectedOutput ChannelRef               `json:"selectedOutput"`
	Dynamics       *DynamicsAnalysisRecord  `json:"dynamics,omitempty"`
	Frequency      *FrequencyAnalysisRecord `json:"frequency,omitempty"`
	Loop           *LoopAnalysisRecord      `json:"loop,omitempty"`
}

type AnalysisChannel struct {
	ChannelRef
	Name string `json:"name"`
}

type DynamicsAnalysisRecord struct {
	CreatedAt time.Time        `json:"createdAt"`
	Stale     bool             `json:"stale"`
	Result    DynamicsAnalysis `json:"result"`
}

type FrequencyAnalysisRecord struct {
	CreatedAt time.Time         `json:"createdAt"`
	Stale     bool              `json:"stale"`
	Result    FrequencyAnalysis `json:"result"`
}

type LoopAnalysisRecord struct {
	CreatedAt time.Time    `json:"createdAt"`
	Stale     bool         `json:"stale"`
	Result    LoopAnalysis `json:"result"`
}

type analysisCache struct {
	input     ChannelRef
	output    ChannelRef
	dynamics  *DynamicsAnalysisRecord
	frequency *FrequencyAnalysisRecord
	loop      *LoopAnalysisRecord
}

func analysisChannelRefs(channels []AnalysisChannel) []ChannelRef {
	refs := make([]ChannelRef, len(channels))
	for index, channel := range channels {
		refs[index] = channel.ChannelRef
	}
	return refs
}

func (s *Studio) RunAnalysis(
	ctx context.Context,
	flowID int64,
	request AnalysisWorkspaceRequest,
) (AnalysisWorkspace, error) {
	if request.Intent != AnalysisIntentDynamics &&
		request.Intent != AnalysisIntentFrequency &&
		request.Intent != AnalysisIntentLoop {
		return AnalysisWorkspace{}, invalid("choose dynamics, frequency, or loop analysis")
	}
	if request.StepHorizon < 0 {
		return AnalysisWorkspace{}, invalid("step horizon cannot be negative")
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return AnalysisWorkspace{}, err
	}
	now := s.now().UTC()
	var (
		dynamicsRecord  *DynamicsAnalysisRecord
		frequencyRecord *FrequencyAnalysisRecord
		loopRecord      *LoopAnalysisRecord
	)

	switch request.Intent {
	case AnalysisIntentDynamics:
		var step *StepExperiment
		if request.StepHorizon > 0 {
			step = &StepExperiment{Horizon: request.StepHorizon}
		}
		result, analysisErr := analyzeDynamics(
			snapshot.Blocks,
			snapshot.Connections,
			DynamicsAnalysisRequest{
				Input: request.Input, Output: request.Output,
				BaseStep: request.BaseStep, Step: step,
			},
		)
		if analysisErr != nil {
			return AnalysisWorkspace{}, analysisErr
		}
		result.FlowID = flowID
		result.ModelUpdatedAt = snapshot.Flow.ModelUpdatedAt
		dynamicsRecord = &DynamicsAnalysisRecord{CreatedAt: now, Result: result}
	case AnalysisIntentFrequency:
		inputs := append([]ChannelRef(nil), request.Inputs...)
		outputs := append([]ChannelRef(nil), request.Outputs...)
		if request.FrequencyAllChannels {
			inputChannels, outputChannels := analysisChannels(snapshot.Blocks)
			inputs = analysisChannelRefs(inputChannels)
			outputs = analysisChannelRefs(outputChannels)
		}
		if len(inputs) == 0 {
			inputs = []ChannelRef{request.Input}
		}
		if len(outputs) == 0 {
			outputs = []ChannelRef{request.Output}
		}
		request.Inputs = inputs
		request.Outputs = outputs
		result, analysisErr := analyzeFrequency(
			snapshot.Blocks,
			snapshot.Connections,
			FrequencyAnalysisRequest{
				Inputs: inputs, Outputs: outputs,
				BaseStep: request.BaseStep, Points: request.Points,
			},
		)
		if analysisErr != nil {
			return AnalysisWorkspace{}, analysisErr
		}
		result.FlowID = flowID
		result.ModelUpdatedAt = snapshot.Flow.ModelUpdatedAt
		frequencyRecord = &FrequencyAnalysisRecord{CreatedAt: now, Result: result}
	case AnalysisIntentLoop:
		result, analysisErr := analyzeLoop(
			snapshot.Blocks,
			snapshot.Connections,
			LoopAnalysisRequest{
				Input: request.Input, Output: request.Output, BaseStep: request.BaseStep,
			},
		)
		if analysisErr != nil {
			return AnalysisWorkspace{}, analysisErr
		}
		result.FlowID = flowID
		result.ModelUpdatedAt = snapshot.Flow.ModelUpdatedAt
		loopRecord = &LoopAnalysisRecord{CreatedAt: now, Result: result}
	}

	if err := s.persistAnalysisRecord(
		ctx, flowID, snapshot.Flow.ModelUpdatedAt, request,
		dynamicsRecord, frequencyRecord, loopRecord,
	); err != nil {
		return AnalysisWorkspace{}, err
	}

	s.analysisMu.Lock()
	if s.analyses == nil {
		s.analyses = make(map[int64]analysisCache)
	}
	cache := s.analyses[flowID]
	cache.input = request.Input
	cache.output = request.Output
	if len(request.Inputs) > 0 {
		cache.input = request.Inputs[0]
	}
	if len(request.Outputs) > 0 {
		cache.output = request.Outputs[0]
	}
	if dynamicsRecord != nil {
		cache.dynamics = dynamicsRecord
	}
	if frequencyRecord != nil {
		cache.frequency = frequencyRecord
	}
	if loopRecord != nil {
		cache.loop = loopRecord
	}
	s.analyses[flowID] = cache
	s.analysisMu.Unlock()
	return s.analysisWorkspace(snapshot), nil
}

func (s *Studio) persistAnalysisRecord(
	ctx context.Context,
	flowID int64,
	modelUpdatedAt time.Time,
	request AnalysisWorkspaceRequest,
	dynamics *DynamicsAnalysisRecord,
	frequency *FrequencyAnalysisRecord,
	loop *LoopAnalysisRecord,
) error {
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode analysis request: %w", err)
	}
	var record any
	switch request.Intent {
	case AnalysisIntentDynamics:
		record = dynamics
	case AnalysisIntentFrequency:
		record = frequency
	case AnalysisIntentLoop:
		record = loop
	}
	resultJSON, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode analysis result: %w", err)
	}
	createdAt := s.now().UTC()
	switch value := record.(type) {
	case *DynamicsAnalysisRecord:
		createdAt = value.CreatedAt
	case *FrequencyAnalysisRecord:
		createdAt = value.CreatedAt
	case *LoopAnalysisRecord:
		createdAt = value.CreatedAt
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO analysis_runs(
				flow_id, intent, created_at, model_updated_at, request_json, result_json
			) VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(flow_id, intent) DO UPDATE SET
				created_at = excluded.created_at,
				model_updated_at = excluded.model_updated_at,
				request_json = excluded.request_json,
				result_json = excluded.result_json`,
			flowID, request.Intent, createdAt.Format(time.RFC3339Nano),
			modelUpdatedAt.UTC().Format(time.RFC3339Nano),
			string(requestJSON), string(resultJSON),
		)
		if err != nil {
			return fmt.Errorf("save analysis result: %w", err)
		}
		return nil
	})
}

func (s *Studio) loadAnalysisCache(ctx context.Context, flowID int64) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT intent, request_json, result_json
		FROM analysis_runs
		WHERE flow_id = ?
		ORDER BY created_at, intent`,
		flowID,
	)
	if err != nil {
		return fmt.Errorf("load analysis results: %w", err)
	}
	defer rows.Close()

	var cache analysisCache
	for rows.Next() {
		var intent AnalysisIntent
		var requestJSON, resultJSON string
		if err := rows.Scan(&intent, &requestJSON, &resultJSON); err != nil {
			return fmt.Errorf("scan analysis result: %w", err)
		}
		var request AnalysisWorkspaceRequest
		if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
			return fmt.Errorf("decode %s analysis request: %w", intent, err)
		}
		cache.input = request.Input
		cache.output = request.Output
		if len(request.Inputs) > 0 {
			cache.input = request.Inputs[0]
		}
		if len(request.Outputs) > 0 {
			cache.output = request.Outputs[0]
		}
		switch intent {
		case AnalysisIntentDynamics:
			if err := json.Unmarshal([]byte(resultJSON), &cache.dynamics); err != nil {
				return fmt.Errorf("decode dynamics analysis: %w", err)
			}
		case AnalysisIntentFrequency:
			if err := json.Unmarshal([]byte(resultJSON), &cache.frequency); err != nil {
				return fmt.Errorf("decode frequency analysis: %w", err)
			}
		case AnalysisIntentLoop:
			if err := json.Unmarshal([]byte(resultJSON), &cache.loop); err != nil {
				return fmt.Errorf("decode loop analysis: %w", err)
			}
		default:
			return fmt.Errorf("decode analysis: unsupported intent %q", intent)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read analysis results: %w", err)
	}

	s.analysisMu.Lock()
	if s.analyses == nil {
		s.analyses = make(map[int64]analysisCache)
	}
	s.analyses[flowID] = cache
	s.analysisMu.Unlock()
	return nil
}

func (s *Studio) analysisWorkspace(snapshot Snapshot) AnalysisWorkspace {
	inputs, outputs := analysisChannels(snapshot.Blocks)
	s.analysisMu.RLock()
	cache := s.analyses[snapshot.Flow.ID]
	s.analysisMu.RUnlock()
	selectedInput := cache.input
	selectedOutput := cache.output
	if !containsAnalysisChannel(inputs, selectedInput) && len(inputs) > 0 {
		selectedInput = inputs[0].ChannelRef
	}
	if !containsAnalysisChannel(outputs, selectedOutput) && len(outputs) > 0 {
		selectedOutput = outputs[len(outputs)-1].ChannelRef
	}
	result := AnalysisWorkspace{
		FlowID:         snapshot.Flow.ID,
		ModelUpdatedAt: snapshot.Flow.ModelUpdatedAt,
		Inputs:         inputs,
		Outputs:        outputs,
		SelectedInput:  selectedInput,
		SelectedOutput: selectedOutput,
		Dynamics:       copyDynamicsRecord(cache.dynamics),
		Frequency:      copyFrequencyRecord(cache.frequency),
		Loop:           copyLoopRecord(cache.loop),
	}
	if result.Dynamics != nil {
		result.Dynamics.Stale = !result.Dynamics.Result.ModelUpdatedAt.Equal(snapshot.Flow.ModelUpdatedAt)
	}
	if result.Frequency != nil {
		result.Frequency.Stale = !result.Frequency.Result.ModelUpdatedAt.Equal(snapshot.Flow.ModelUpdatedAt)
	}
	if result.Loop != nil {
		result.Loop.Stale = !result.Loop.Result.ModelUpdatedAt.Equal(snapshot.Flow.ModelUpdatedAt)
	}
	return result
}

func analysisChannels(blocks []Block) ([]AnalysisChannel, []AnalysisChannel) {
	var inputs []AnalysisChannel
	var outputs []AnalysisChannel
	for _, block := range blocks {
		for port := range block.OutputPortCount() {
			schema, ok := block.OutputPort(port)
			if !ok {
				continue
			}
			for channel := range schema.Width {
				candidate := AnalysisChannel{
					ChannelRef: ChannelRef{
						BlockID: block.ID, Port: port, Channel: channel,
					},
					Name: analysisChannelName(block, port, channel, schema),
				}
				outputs = append(outputs, candidate)
				if block.InputPortCount() == 0 {
					inputs = append(inputs, candidate)
				}
			}
		}
	}
	return inputs, outputs
}

func analysisChannelName(
	block Block,
	port int,
	channel int,
	schema SignalPort,
) string {
	name := fmt.Sprintf("%s · output %d", block.Name, port+1)
	if channel < len(schema.Channels) && schema.Channels[channel] != "" {
		return name + " · " + schema.Channels[channel]
	}
	if schema.Width > 1 {
		return fmt.Sprintf("%s · channel %d", name, channel+1)
	}
	return name
}

func containsAnalysisChannel(channels []AnalysisChannel, ref ChannelRef) bool {
	for _, channel := range channels {
		if channel.ChannelRef == ref {
			return true
		}
	}
	return false
}

func copyDynamicsRecord(record *DynamicsAnalysisRecord) *DynamicsAnalysisRecord {
	return cloneAnalysisRecord(record)
}

func copyFrequencyRecord(record *FrequencyAnalysisRecord) *FrequencyAnalysisRecord {
	return cloneAnalysisRecord(record)
}

func copyLoopRecord(record *LoopAnalysisRecord) *LoopAnalysisRecord {
	return cloneAnalysisRecord(record)
}

func cloneAnalysisRecord[T any](record *T) *T {
	if record == nil {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		panic(fmt.Sprintf("clone analysis record: %v", err))
	}
	var copied T
	if err := json.Unmarshal(encoded, &copied); err != nil {
		panic(fmt.Sprintf("clone analysis record: %v", err))
	}
	return &copied
}
