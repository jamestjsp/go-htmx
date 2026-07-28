package studio

import (
	"context"
	"time"
)

type ResultsExport struct {
	SchemaVersion  int               `json:"schemaVersion"`
	FlowID         int64             `json:"flowId"`
	FlowName       string            `json:"flowName"`
	ModelUpdatedAt time.Time         `json:"modelUpdatedAt"`
	Simulation     *Simulation       `json:"simulation,omitempty"`
	Analysis       AnalysisWorkspace `json:"analysis"`
}

func (s *Studio) ExportResults(ctx context.Context, flowID int64) (ResultsExport, error) {
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return ResultsExport{}, err
	}
	records, err := s.loadAnalysisRecords(ctx, flowID)
	if err != nil {
		return ResultsExport{}, err
	}
	return ResultsExport{
		SchemaVersion:  1,
		FlowID:         flowID,
		FlowName:       snapshot.Flow.Name,
		ModelUpdatedAt: snapshot.Flow.ModelUpdatedAt,
		Simulation:     snapshot.LastRun,
		Analysis:       analysisWorkspace(snapshot, records),
	}, nil
}
