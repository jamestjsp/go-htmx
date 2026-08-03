package studio

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunParameterSweepUsesCatalogBlockAndSnapshotRevision(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plant := findKind(t, snapshot.Blocks, BlockLag)
	analysis, err := service.RunParameterSweep(
		ctx, snapshot.Flow.ID, plant.ID,
		SweepSpec{
			SourceModelRevision: snapshot.Flow.ModelUpdatedAt.Add(time.Hour),
			Axes:                []SweepAxis{{Parameter: "time_constant", Unit: "s", Values: []float64{1, 2}}},
		},
		SweepAnalysisSpec{Omega: []float64{0.1, 1}, StepFinal: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.SourceModelRevision.Equal(snapshot.Flow.ModelUpdatedAt) ||
		len(analysis.Shape) != 1 || analysis.Shape[0] != 2 ||
		len(analysis.Frequency.Models) != 2 || len(analysis.Time.Models) != 2 {
		t.Fatalf("parameter sweep analysis = %#v", analysis)
	}
	if analysis.Frequency.WorstCase.Name == "" {
		t.Fatalf("frequency worst case = %#v", analysis.Frequency.WorstCase)
	}
}

func TestRunParameterSweepRejectsUnknownCatalogParameterBeforeCompilation(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plant := findKind(t, snapshot.Blocks, BlockLag)
	_, err = service.RunParameterSweep(
		ctx, snapshot.Flow.ID, plant.ID,
		SweepSpec{Axes: []SweepAxis{{Parameter: "not_a_parameter", Unit: "1", Values: []float64{1}}}},
		SweepAnalysisSpec{Omega: []float64{0.1, 1}, StepFinal: 1},
	)
	if err == nil || !strings.Contains(err.Error(), "is not defined for block") {
		t.Fatalf("unknown parameter error = %v", err)
	}
}
