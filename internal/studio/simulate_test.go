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
		{ID: 2, Kind: BlockSum, Name: "Sum A", Parameters: defaultParameters(BlockSum)},
		{ID: 3, Kind: BlockSum, Name: "Sum B", Parameters: defaultParameters(BlockSum)},
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

func TestContinuousBlockResponsesAgainstAnalyticModels(t *testing.T) {
	tests := []struct {
		name       string
		kind       BlockKind
		parameters Parameters
		want       func(float64) float64
		tolerance  float64
	}{
		{
			name:       "integrator",
			kind:       BlockIntegrator,
			parameters: Parameters{},
			want:       func(time float64) float64 { return 2 * time },
			tolerance:  1e-10,
		},
		{
			name: "first order transfer function",
			kind: BlockTransfer,
			parameters: Parameters{
				Numerator: []float64{2}, Denominator: []float64{1, 2},
			},
			want:      func(time float64) float64 { return 2 * (1 - math.Exp(-2*time)) },
			tolerance: 1e-10,
		},
		{
			name: "parallel PI controller",
			kind: BlockPID,
			parameters: Parameters{
				Proportional: 2, Integral: 0.5, FilterTime: 0.1,
			},
			want:      func(time float64) float64 { return 2 + 0.5*time },
			tolerance: 1e-10,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			blocks := []Block{
				{ID: 1, Kind: BlockConstant, Name: "Input", Parameters: Parameters{Value: 2}},
				{ID: 2, Kind: test.kind, Name: "Model", Parameters: test.parameters},
				{ID: 3, Kind: BlockScope, Name: "Output"},
			}
			if test.kind == BlockPID {
				blocks[0] = Block{
					ID: 1, Kind: BlockSource, Name: "Input",
					Parameters: Parameters{Amplitude: 1},
				}
			}
			run, err := simulate(blocks, []Connection{
				{ID: 1, SourceID: 1, TargetID: 2},
				{ID: 2, SourceID: 2, TargetID: 3},
			}, SimulationRequest{Duration: 3, SampleTime: 0.1})
			if err != nil {
				t.Fatal(err)
			}
			for i, got := range run.Series[0].Values {
				want := test.want(run.Times[i])
				if math.Abs(got-want) > test.tolerance {
					t.Fatalf("sample %d at %g s = %.12g, want %.12g",
						i, run.Times[i], got, want)
				}
			}
		})
	}
}

func TestIndependentSourceWaveformsAndSignedSum(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Bias", Parameters: Parameters{Value: 2}},
		{ID: 2, Kind: BlockSine, Name: "Wave", Parameters: Parameters{
			Amplitude: 0.5, Bias: 0.25, Frequency: 2, Phase: 0.3,
		}},
		{ID: 3, Kind: BlockSum, Name: "Difference", Parameters: Parameters{Signs: "+-"}},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 3},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 4},
	}, SimulationRequest{Duration: 2, SampleTime: 0.05})
	if err != nil {
		t.Fatal(err)
	}
	for i, got := range run.Series[0].Values {
		time := run.Times[i]
		want := 2 - (0.25 + 0.5*math.Sin(2*time+0.3))
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("sample %d = %.12g, want %.12g", i, got, want)
		}
	}
}

func TestPadeDelayConvergesToStepSteadyState(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockDelay, Name: "Delay", Parameters: Parameters{
			Delay: 0.5, Approximation: 3,
		}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 5, SampleTime: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	final := run.Series[0].Values[len(run.Series[0].Values)-1]
	if math.Abs(final-1) > 1e-8 {
		t.Fatalf("final value = %.12g, want 1", final)
	}
}

func TestSpectrumAnalyzerFindsSineFrequency(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSine, Name: "Two hertz", Parameters: Parameters{
			Amplitude: 1.25,
			Frequency: 4 * math.Pi,
		}},
		{ID: 2, Kind: BlockSpectrum, Name: "Spectrum"},
	}
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
	}, SimulationRequest{Duration: 3.99, SampleTime: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 0 || len(run.Spectra) != 1 {
		t.Fatalf("series = %d, spectra = %d", len(run.Series), len(run.Spectra))
	}
	spectrum := run.Spectra[0]
	if math.Abs(spectrum.PeakFrequency-2) > 1e-12 {
		t.Fatalf("peak frequency = %.12g Hz, want 2", spectrum.PeakFrequency)
	}
	if math.Abs(spectrum.PeakMagnitude-1.25) > 1e-3 {
		t.Fatalf("peak magnitude = %.12g, want 1.25", spectrum.PeakMagnitude)
	}
}
