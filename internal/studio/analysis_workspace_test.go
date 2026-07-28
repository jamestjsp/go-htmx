package studio

import (
	"context"
	"path/filepath"
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

func TestFrequencyWorkspaceUsesAllNamedChannelsAndPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "analysis.db")
	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inputs, outputs := analysisChannels(snapshot.Blocks)
	analysis, err := service.RunAnalysis(ctx, snapshot.Flow.ID, AnalysisWorkspaceRequest{
		Intent:               AnalysisIntentFrequency,
		Input:                inputs[0].ChannelRef,
		Output:               outputs[len(outputs)-1].ChannelRef,
		FrequencyAllChannels: true,
		Points:               30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Frequency == nil ||
		len(analysis.Frequency.Result.Inputs) != len(inputs) ||
		len(analysis.Frequency.Result.Outputs) != len(outputs) ||
		len(analysis.Frequency.Result.Bode) != len(inputs)*len(outputs) {
		t.Fatalf("all-channel frequency result = %#v", analysis.Frequency)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	workspace, err := reopened.Workspace(ctx, snapshot.Flow.ProjectID, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Analysis.Frequency == nil ||
		len(workspace.Analysis.Frequency.Result.Bode) != len(inputs)*len(outputs) ||
		workspace.Analysis.Frequency.Stale {
		t.Fatalf("reloaded frequency analysis = %#v", workspace.Analysis.Frequency)
	}

	workspace.Analysis.Frequency.Result.Grid.Omega[0] = 999
	reloaded, err := reopened.Workspace(ctx, snapshot.Flow.ProjectID, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Analysis.Frequency.Result.Grid.Omega[0] == 999 {
		t.Fatal("analysis workspace leaked mutable cached slices")
	}

	duplicated, err := reopened.DuplicateFlow(ctx, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if duplicated.Analysis.Dynamics != nil ||
		duplicated.Analysis.Frequency != nil ||
		duplicated.Analysis.Loop != nil {
		t.Fatalf("duplicated flow copied analysis results: %#v", duplicated.Analysis)
	}
	var copiedResults int
	if err := reopened.db.QueryRowContext(
		ctx, "SELECT COUNT(*) FROM analysis_runs WHERE flow_id = ?",
		duplicated.Snapshot.Flow.ID,
	).Scan(&copiedResults); err != nil {
		t.Fatal(err)
	}
	if copiedResults != 0 {
		t.Fatalf("duplicated analysis rows = %d", copiedResults)
	}
}
