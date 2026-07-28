package studio

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
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

func TestPIDFeedbackLoopMatchesControlsysFeedback(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Setpoint", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSum, Name: "Error", Parameters: Parameters{Signs: "+-"}},
		{ID: 3, Kind: BlockPID, Name: "Controller", Parameters: Parameters{
			Proportional: 2, Integral: 1, Derivative: 0.2, FilterTime: 0.051,
		}},
		{ID: 4, Kind: BlockTransfer, Name: "Plant", Parameters: Parameters{
			Numerator: []float64{1}, Denominator: []float64{1, 1},
		}},
		{ID: 5, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 4},
		{ID: 4, SourceID: 4, TargetID: 2, TargetPort: 1},
		{ID: 5, SourceID: 4, TargetID: 5},
	}

	run, err := simulate(blocks, connections, SimulationRequest{
		Duration: 20, SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}

	controller, err := realizeBlock(blocks[2], []int{0})
	if err != nil {
		t.Fatal(err)
	}
	plant, err := realizeBlock(blocks[3], []int{0})
	if err != nil {
		t.Fatal(err)
	}
	openLoop, err := controlsys.Series(controller, plant)
	if err != nil {
		t.Fatal(err)
	}
	closedLoop, err := controlsys.Feedback(openLoop, nil, -1)
	if err != nil {
		t.Fatal(err)
	}
	input := mat.NewDense(len(run.Times), 1, make([]float64, len(run.Times)))
	for sample := range run.Times {
		input.Set(sample, 0, 1)
	}
	want, err := controlsys.Lsim(closedLoop, input, run.Times, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(run.Series) != 1 {
		t.Fatalf("series = %d, want 1", len(run.Series))
	}
	for sample, got := range run.Series[0].Values {
		if diff := math.Abs(got - want.Y.At(0, sample)); diff > 1e-10 {
			t.Fatalf("sample %d = %.12g, controlsys.Feedback = %.12g (diff %.3g)",
				sample, got, want.Y.At(0, sample), diff)
		}
	}
}

func TestWellPosedStaticFeedbackLoopSimulates(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSum, Name: "Error", Parameters: Parameters{Signs: "+-"}},
		{ID: 3, Kind: BlockGain, Name: "Gain", Parameters: Parameters{Gain: 1}},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 2, TargetPort: 1},
		{ID: 4, SourceID: 3, TargetID: 4},
	}

	run, err := simulate(blocks, connections, SimulationRequest{
		Duration: 1, SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 1 {
		t.Fatalf("series = %d, want 1", len(run.Series))
	}
	for sample, got := range run.Series[0].Values {
		if diff := math.Abs(got - 0.5); diff > 1e-12 {
			t.Fatalf("sample %d = %.12g, want 0.5 (diff %.3g)", sample, got, diff)
		}
	}
}

func TestCompileTranslatesUnsolvableAlgebraicLoop(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSum, Name: "Sum", Parameters: Parameters{Signs: "++"}},
		{ID: 3, Kind: BlockGain, Name: "Gain", Parameters: Parameters{Gain: 1}},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 2, TargetPort: 1},
		{ID: 4, SourceID: 3, TargetID: 4},
	}

	_, err := compileFlow(blocks, connections)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	const want = "flowsheet contains an unsolvable algebraic loop; add dynamics or change a direct-feedthrough gain"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
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
	// The Sum's two wires name the two ports its signs declare. Before
	// connections carried ports these read as the first and second wire drawn,
	// which is the same pair in the same order — port 0 takes +, port 1 takes -.
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 3, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3, TargetPort: 1},
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

// A Sum's sign belongs to the port a wire landed on, not to the wire's place
// in the drawing order. These two are drawn back to front — the wire onto the
// second port is made first — so a compiler that matched signs to connection
// order would give + to the subtrahend and - to the minuend, and the response
// would come out negated.
func TestSumSignsFollowInputPortsNotWiringOrder(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Subtrahend", Parameters: Parameters{Value: 3}},
		{ID: 2, Kind: BlockConstant, Name: "Minuend", Parameters: Parameters{Value: 10}},
		{ID: 3, Kind: BlockSum, Name: "Difference", Parameters: Parameters{Signs: "+-"}},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 3, TargetPort: 1},
		{ID: 2, SourceID: 2, TargetID: 3, TargetPort: 0},
		{ID: 3, SourceID: 3, TargetID: 4},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	for i, got := range run.Series[0].Values {
		if want := 10.0 - 3.0; math.Abs(got-want) > 1e-12 {
			t.Fatalf("sample %d = %.12g, want %.12g", i, got, want)
		}
	}
}

// Per-port uniqueness lets one output fan into two ports of the same Sum.
// Naming an input by its port is what makes both wires arrive: keyed by the
// source block, as they were, the two shared one signal name and the second
// silently vanished into the first's place, halving the sum.
func TestSumCountsBothWiresWhenOneOutputFansIntoTwoPorts(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Feed", Parameters: Parameters{Value: 4}},
		{ID: 2, Kind: BlockSum, Name: "Doubler", Parameters: Parameters{Signs: "++"}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: 0},
		{ID: 2, SourceID: 1, TargetID: 2, TargetPort: 1},
		{ID: 3, SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	for i, got := range run.Series[0].Values {
		if want := 8.0; math.Abs(got-want) > 1e-12 {
			t.Fatalf("sample %d = %.12g, want %.12g", i, got, want)
		}
	}
}

// Connect refuses a second wire onto an occupied port, but the connections
// table cannot express that rule, so a model an older version wrote can still
// arrive with two wires on one terminal. Both would compile to the same signal
// name, so compiling one is a guess; the compiler says so instead.
func TestCompileRejectsTwoWiresOnOneInputPort(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Bias", Parameters: Parameters{Value: 2}},
		{ID: 2, Kind: BlockConstant, Name: "Load", Parameters: Parameters{Value: 5}},
		{ID: 3, Kind: BlockSum, Name: "Difference", Parameters: Parameters{Signs: "+-"}},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	_, err := compileFlow(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 3, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3, TargetPort: 0},
		{ID: 3, SourceID: 3, TargetID: 4},
	})
	var validation *ValidationError
	want := "Difference has more than one input on port 0"
	if !errors.As(err, &validation) || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// A negative port is the one bad index that cannot compile into something
// harmless — Sum would read its sign at that index and panic — so it is
// refused in the same voice Connect refuses it in, and for the same reach: the
// column has no CHECK, and copying a flowsheet carries the value over.
func TestCompileRejectsANegativeInputPort(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Feed", Parameters: Parameters{Value: 2}},
		{ID: 2, Kind: BlockSum, Name: "Total", Parameters: Parameters{Signs: "+"}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	_, err := compileFlow(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: -1},
		{ID: 2, SourceID: 2, TargetID: 3},
	})
	var validation *ValidationError
	want := "Total has no input port -1"
	if !errors.As(err, &validation) || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// A Sum can hold more wires than it has signs: an older version could wire one
// past maxInputSigns, where no sign string could name every port, so the port
// migration leaves the lone sign broadcasting rather than change what the
// flowsheet computes. Three wires stand in for that shape — the clamp that
// reads the last sign for a port past the end is the same one — and a minus
// makes it visible, since a clamp that missed would leave the extra ports on
// the +1 a zero gain slot would also show.
func TestSumBroadcastsALoneSignAcrossEveryWiredPort(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "A", Parameters: Parameters{Value: 2}},
		{ID: 2, Kind: BlockConstant, Name: "B", Parameters: Parameters{Value: 7}},
		{ID: 3, Kind: BlockConstant, Name: "C", Parameters: Parameters{Value: 11}},
		{ID: 4, Kind: BlockSum, Name: "Total", Parameters: Parameters{Signs: "-"}},
		{ID: 5, Kind: BlockScope, Name: "Output"},
	}
	run, err := simulate(blocks, []Connection{
		{ID: 1, SourceID: 1, TargetID: 4, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 4, TargetPort: 1},
		{ID: 3, SourceID: 3, TargetID: 4, TargetPort: 2},
		{ID: 4, SourceID: 4, TargetID: 5},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	for i, got := range run.Series[0].Values {
		if want := -(2.0 + 7.0 + 11.0); math.Abs(got-want) > 1e-12 {
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
