package studio

import (
	"context"
	"testing"
)

func TestAnalysisWorkspaceCachesIntentsAndMarksModelEditsStale(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := findKind(t, snapshot.Blocks, BlockSource)
	plant := findKind(t, snapshot.Blocks, BlockLag)
	request := AnalysisWorkspaceRequest{
		Intent:      AnalysisIntentDynamics,
		Input:       ChannelRef{BlockID: source.ID},
		Output:      ChannelRef{BlockID: plant.ID},
		StepHorizon: 5,
	}
	analysis, err := service.RunAnalysis(ctx, snapshot.Flow.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Dynamics == nil ||
		analysis.Dynamics.Stale ||
		analysis.Dynamics.Result.StepExperiment == nil {
		t.Fatalf("dynamics record = %#v", analysis.Dynamics)
	}

	request.Intent = AnalysisIntentFrequency
	request.Points = 30
	analysis, err = service.RunAnalysis(ctx, snapshot.Flow.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Dynamics == nil ||
		analysis.Frequency == nil ||
		len(analysis.Frequency.Result.Grid.Omega) != 30 {
		t.Fatalf("retained analysis records = %#v", analysis)
	}

	if err := service.MoveBlock(ctx, plant.ID, Point{X: 1000, Y: 1000}); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.Workspace(ctx, snapshot.Flow.ProjectID, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Analysis.Dynamics.Stale || workspace.Analysis.Frequency.Stale {
		t.Fatalf("layout-only edit marked analysis stale: %#v", workspace.Analysis)
	}

	plant = findBlock(t, workspace.Snapshot.Blocks, plant.ID)
	_, err = service.UpdateBlock(ctx, plant.ID, BlockUpdate{
		Name: plant.Name,
		Parameters: map[string]string{
			"time_constant": "8",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = service.Workspace(ctx, snapshot.Flow.ProjectID, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.Analysis.Dynamics.Stale ||
		!workspace.Analysis.Frequency.Stale {
		t.Fatalf("model edit did not mark cached analysis stale: %#v", workspace.Analysis)
	}
}

func TestAnalysisWorkspaceExposesNamedVectorChannels(t *testing.T) {
	sourceParameters := defaultParameters(BlockVectorConstant)
	values, err := NewVectorValue([]float64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	sourceNames, err := NewChannelNames([]string{"feed", "recycle"})
	if err != nil {
		t.Fatal(err)
	}
	inputNames, err := NewChannelNames([]string{"feed", "recycle"})
	if err != nil {
		t.Fatal(err)
	}
	outputNames, err := NewChannelNames([]string{"temperature", "pressure"})
	if err != nil {
		t.Fatal(err)
	}
	sourceParameters.Vector = &values
	sourceParameters.OutputNames = &sourceNames
	gainParameters := defaultParameters(BlockMatrixGain)
	gainParameters.InputNames = &inputNames
	gainParameters.OutputNames = &outputNames

	inputs, outputs := analysisChannels([]Block{
		{ID: 1, Kind: BlockVectorConstant, Name: "Inputs", Parameters: sourceParameters},
		{ID: 2, Kind: BlockMatrixGain, Name: "Plant", Parameters: gainParameters},
	})
	if len(inputs) != 2 ||
		inputs[0].Name != "Inputs · output 1 · feed" ||
		inputs[1].Name != "Inputs · output 1 · recycle" {
		t.Fatalf("named inputs = %#v", inputs)
	}
	if len(outputs) != 4 ||
		outputs[2].Name != "Plant · output 1 · temperature" ||
		outputs[3].Name != "Plant · output 1 · pressure" {
		t.Fatalf("named outputs = %#v", outputs)
	}
}

func TestAnalysisWorkspaceRejectsNegativeStepHorizon(t *testing.T) {
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.RunAnalysis(
		context.Background(),
		snapshot.Flow.ID,
		AnalysisWorkspaceRequest{
			Intent: AnalysisIntentDynamics, StepHorizon: -1,
		},
	)
	if err == nil {
		t.Fatal("negative step horizon succeeded")
	}
}
