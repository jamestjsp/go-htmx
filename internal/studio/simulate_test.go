package studio

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimulateSeededBranchedFlow(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err = studio.Run(ctx, snapshot.Flow.ID, SimulationRequest{
		Duration: 30, SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	run := snapshot.LastRun
	if run == nil {
		t.Fatal("LastRun is nil")
	}
	if len(run.Times) != 301 {
		t.Fatalf("sample count = %d, want 301", len(run.Times))
	}
	if len(run.Series) != 1 {
		t.Fatalf("series count = %d, want 1", len(run.Series))
	}
	wantFinal := 1.8 + 0.3*-0.7
	gotFinal := run.Series[0].Values[len(run.Series[0].Values)-1]
	if math.Abs(gotFinal-wantFinal) > 0.001 {
		t.Fatalf("final value = %g, want %g", gotFinal, wantFinal)
	}
	if len(run.Metrics) != 1 || !run.Metrics[0].Settled {
		t.Fatalf("metrics = %#v", run.Metrics)
	}
}

func TestSimulateStaticFlow(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 2}},
		{ID: 2, Kind: BlockGain, Name: "Gain", Parameters: Parameters{Gain: 3}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
	}

	run, err := simulate(blocks, connections, SimulationRequest{Duration: 2, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	for i, value := range run.Series[0].Values {
		if value != 6 {
			t.Fatalf("sample %d = %g, want 6", i, value)
		}
	}
}

func TestSimulationRoundTripsThroughSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := first.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = first.Run(ctx, snapshot.Flow.ID, SimulationRequest{
		Duration: 12, SampleTime: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSamples := len(snapshot.LastRun.Times)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	snapshot, err = second.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil || len(snapshot.LastRun.Times) != wantSamples {
		t.Fatalf("persisted run = %#v", snapshot.LastRun)
	}
}

func TestModelChangeHidesStaleSimulationButMoveDoesNot(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = studio.Run(ctx, snapshot.Flow.ID, SimulationRequest{
		Duration: 12, SampleTime: 0.2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil {
		t.Fatal("run was not saved")
	}
	if err := studio.MoveBlock(ctx, snapshot.Blocks[0].ID, Point{X: 44, Y: 55}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil {
		t.Fatal("layout move hid a still-valid simulation")
	}
	snapshot, _, err = studio.AddBlock(ctx, snapshot.Flow.ID, BlockGain, Point{X: 40, Y: 40})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun != nil {
		t.Fatal("model change kept a stale simulation visible")
	}
}

func TestCompileRejectsMissingScope(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockGain, Name: "Gain", Parameters: Parameters{Gain: 2}},
	}
	_, err := compileFlow(blocks, []Connection{{SourceID: 1, TargetID: 2}})
	var validation *ValidationError
	if !errors.As(err, &validation) || !strings.Contains(err.Error(), "Scope") {
		t.Fatalf("error = %v, want missing Scope validation", err)
	}
}

func TestCompileRejectsCycle(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSum, Name: "Sum A"},
		{ID: 3, Kind: BlockSum, Name: "Sum B"},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
		{SourceID: 3, TargetID: 2},
		{SourceID: 3, TargetID: 4},
	}
	_, err := compileFlow(blocks, connections)
	var validation *ValidationError
	if !errors.As(err, &validation) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want cycle validation", err)
	}
}

func TestCompileRejectsInvalidLag(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockLag, Name: "Bad lag", Parameters: Parameters{TimeConstant: 0}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	_, err := compileFlow(blocks, []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
	})
	if err == nil || !strings.Contains(err.Error(), "time constant") {
		t.Fatalf("error = %v, want time constant validation", err)
	}
}
