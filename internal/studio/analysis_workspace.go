package studio

import (
	"context"
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
	Intent      AnalysisIntent `json:"intent"`
	Input       ChannelRef     `json:"input"`
	Output      ChannelRef     `json:"output"`
	BaseStep    float64        `json:"baseStep,omitempty"`
	StepHorizon float64        `json:"stepHorizon,omitempty"`
	Points      int            `json:"points,omitempty"`
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
		result, analysisErr := analyzeFrequency(
			snapshot.Blocks,
			snapshot.Connections,
			FrequencyAnalysisRequest{
				Inputs: []ChannelRef{request.Input}, Outputs: []ChannelRef{request.Output},
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

	s.analysisMu.Lock()
	if s.analyses == nil {
		s.analyses = make(map[int64]analysisCache)
	}
	cache := s.analyses[flowID]
	cache.input = request.Input
	cache.output = request.Output
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
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}

func copyFrequencyRecord(record *FrequencyAnalysisRecord) *FrequencyAnalysisRecord {
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}

func copyLoopRecord(record *LoopAnalysisRecord) *LoopAnalysisRecord {
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}
