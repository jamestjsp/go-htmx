package studio

import (
	"math"
	"math/cmplx"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestTransportDelayDefaultsToExactMetadata(t *testing.T) {
	parameters := defaultParameters(BlockDelay)
	if got := normalizedDelayMode(parameters); got != delayModeExact {
		t.Fatalf("default delay mode = %q, want %q", got, delayModeExact)
	}

	system, err := realizeBlock(Block{
		ID: 1, Kind: BlockDelay, Name: "Transport", Parameters: parameters,
	}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	states, inputs, outputs := system.Dims()
	if states != 0 || inputs != 1 || outputs != 1 {
		t.Fatalf("dimensions = (%d,%d,%d), want (0,1,1)", states, inputs, outputs)
	}
	if len(system.InputDelay) != 1 || math.Abs(system.InputDelay[0]-1) > 1e-12 {
		t.Fatalf("exact delay metadata = %v, want 1 second", system.InputDelay)
	}
}

func TestTransportDelayKeepsLegacyEmptyModeAsPade(t *testing.T) {
	system, err := realizeBlock(Block{
		ID: 1, Kind: BlockDelay, Name: "Legacy",
		Parameters: Parameters{Delay: 0.5, Approximation: 3},
	}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	states, _, _ := system.Dims()
	if states != 3 {
		t.Fatalf("legacy delay states = %d, want Padé order 3", states)
	}
	if system.HasDelay() {
		t.Fatal("legacy Padé delay retained exact delay metadata")
	}
}

func TestStoredDelayWithoutModeMigratesToPade(t *testing.T) {
	parameters, err := decodeParameters(
		BlockDelay,
		`{"delay":0.5,"approximation":3}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalizedDelayMode(parameters); got != delayModePade {
		t.Fatalf("stored legacy delay mode = %q, want %q", got, delayModePade)
	}
	if parameters.SampleTime != defaultParameters(BlockDelay).SampleTime {
		t.Fatalf("migrated sample time = %g, want catalog default", parameters.SampleTime)
	}
}

func TestStoredDelayWithModeKeepsExplicitRepresentation(t *testing.T) {
	parameters, err := decodeParameters(
		BlockDelay,
		`{"delay":0.5,"delayMode":"exact","approximation":3}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := normalizedDelayMode(parameters); got != delayModeExact {
		t.Fatalf("stored explicit delay mode = %q, want %q", got, delayModeExact)
	}
}

func TestTransportDelayEditorOffersExplicitRepresentations(t *testing.T) {
	block := Block{Kind: BlockDelay, Parameters: defaultParameters(BlockDelay)}
	fields := block.EditorFields()
	if len(fields) != 5 {
		t.Fatalf("delay fields = %d, want 5", len(fields))
	}
	mode := fields[1]
	if mode.Name != "delay_mode" || len(mode.Options) != 3 {
		t.Fatalf("mode field = %#v", mode)
	}
	if !mode.Options[0].Selected || mode.Options[0].Value != delayModeExact {
		t.Fatalf("default selected option = %#v, want exact", mode.Options)
	}
}

func TestExactTransportDelayShiftsStepAndSineOnAlignedGrid(t *testing.T) {
	tests := []struct {
		name       string
		source     Block
		wantSample func(int, float64) float64
	}{
		{
			name:   "step",
			source: Block{ID: 1, Kind: BlockSource, Name: "Step", Parameters: Parameters{Amplitude: 2}},
			wantSample: func(sample int, _ float64) float64 {
				if sample < 2 {
					return 0
				}
				return 2
			},
		},
		{
			name: "sine",
			source: Block{ID: 1, Kind: BlockSine, Name: "Sine", Parameters: Parameters{
				Amplitude: 1.5, Frequency: 2,
			}},
			wantSample: func(sample int, sampleTime float64) float64 {
				if sample < 2 {
					return 0
				}
				return 1.5 * math.Sin(2*float64(sample-2)*sampleTime)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, err := simulate([]Block{
				test.source,
				{ID: 2, Kind: BlockDelay, Name: "Transport", Parameters: Parameters{
					Delay: 0.2, DelayMode: delayModeExact,
				}},
				{ID: 3, Kind: BlockScope, Name: "Output"},
			}, []Connection{
				{ID: 1, SourceID: 1, TargetID: 2},
				{ID: 2, SourceID: 2, TargetID: 3},
			}, SimulationRequest{Duration: 1, SampleTime: 0.1})
			if err != nil {
				t.Fatal(err)
			}
			for sample, got := range run.Series[0].Values {
				want := test.wantSample(sample, 0.1)
				if math.Abs(got-want) > 1e-11 {
					t.Fatalf("sample %d = %.12g, want %.12g", sample, got, want)
				}
			}
		})
	}
}

func TestExactTransportDelayRejectsFractionalSimulationGrid(t *testing.T) {
	_, err := simulate([]Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockDelay, Name: "Pipe", Parameters: Parameters{
			Delay: 0.35, DelayMode: delayModeExact,
		}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}, []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err == nil {
		t.Fatal("fractional exact delay simulation succeeded")
	}
	wantParts := []string{"Pipe exact delay 0.35 s", "sample time 0.1 s", "nearest aligned delay is 0.3 s", "Padé or Thiran"}
	for _, want := range wantParts {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want fragment %q", err, want)
		}
	}
}

func TestExplicitDelayApproximationsDelegateToControlsys(t *testing.T) {
	const (
		tau   = 0.35
		order = 3
		dt    = 0.1
		omega = 0.2
	)
	tests := []struct {
		name       string
		parameters Parameters
		wantDomain func(*controlsys.System) bool
		tolerance  float64
	}{
		{
			name: "pade",
			parameters: Parameters{
				Delay: tau, DelayMode: delayModePade, Approximation: order,
			},
			wantDomain: func(system *controlsys.System) bool { return system.IsContinuous() },
			tolerance:  1e-8,
		},
		{
			name: "thiran",
			parameters: Parameters{
				Delay: tau, DelayMode: delayModeThiran, Approximation: order, SampleTime: dt,
			},
			wantDomain: func(system *controlsys.System) bool {
				return system.IsDiscrete() && math.Abs(system.Dt-dt) < 1e-12
			},
			tolerance: 1e-5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			system, err := realizeBlock(Block{
				ID: 1, Kind: BlockDelay, Name: test.name, Parameters: test.parameters,
			}, []int{0})
			if err != nil {
				t.Fatal(err)
			}
			if !test.wantDomain(system) {
				t.Fatalf("unexpected time domain: discrete=%v dt=%g", system.IsDiscrete(), system.Dt)
			}
			response, err := system.FreqResponse([]float64{omega})
			if err != nil {
				t.Fatal(err)
			}
			want := cmplx.Exp(complex(0, -omega*tau))
			if diff := cmplx.Abs(response.At(0, 0, 0) - want); diff > test.tolerance {
				t.Fatalf("frequency response diff = %g, want <= %g", diff, test.tolerance)
			}
		})
	}
}

func TestThiranDelayRejectsUnstableOrder(t *testing.T) {
	err := validateParameters(BlockDelay, Parameters{
		Delay: 0.1, DelayMode: delayModeThiran, Approximation: 3, SampleTime: 0.1,
	})
	if err == nil || !strings.Contains(err.Error(), "at least 2.5 samples for order 3") {
		t.Fatalf("error = %v, want Thiran stability guidance", err)
	}
}

func TestThiranTransportDelayRunsAsDiscreteFlowsheet(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSine, Name: "Input", Parameters: Parameters{
			Amplitude: 1, Frequency: 0.5,
		}},
		{ID: 2, Kind: BlockDelay, Name: "Fractional delay", Parameters: Parameters{
			Delay: 0.35, DelayMode: delayModeThiran, Approximation: 3, SampleTime: 0.1,
		}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
	}
	model, err := compileModel(blocks, connections)
	if err != nil {
		t.Fatal(err)
	}
	domain := model.timeDomain()
	if domain.Domain != timeDomainDiscrete || math.Abs(domain.SampleTime-0.1) > 1e-12 {
		t.Fatalf("compiled domain = %#v, want discrete at 0.1 s", domain)
	}
	run, err := model.run(SimulationRequest{Duration: 2, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 1 || len(run.Series[0].Values) != 21 {
		t.Fatalf("simulation shape = series %d samples %d", len(run.Series), len(run.Series[0].Values))
	}

	_, err = model.run(SimulationRequest{Duration: 2, SampleTime: 0.2})
	if err == nil || !strings.Contains(err.Error(), "does not match the discrete model sample time 0.1 s") {
		t.Fatalf("mismatched-grid error = %v", err)
	}
}

func TestExactTransportDelayUsesControlsysLFTInFeedback(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Setpoint", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSum, Name: "Error", Parameters: Parameters{Signs: "+-"}},
		{ID: 3, Kind: BlockGain, Name: "Controller", Parameters: Parameters{Gain: 2}},
		{ID: 4, Kind: BlockTransfer, Name: "Plant", Parameters: Parameters{
			Numerator: []float64{1}, Denominator: []float64{1, 1},
		}},
		{ID: 5, Kind: BlockDelay, Name: "Sensor latency", Parameters: Parameters{
			Delay: 0.2, DelayMode: delayModeExact,
		}},
		{ID: 6, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 4},
		{ID: 4, SourceID: 4, TargetID: 5},
		{ID: 5, SourceID: 5, TargetID: 2, TargetPort: 1},
		{ID: 6, SourceID: 4, TargetID: 6},
	}

	model, err := compileModel(blocks, connections)
	if err != nil {
		t.Fatal(err)
	}
	got := model.systemCopy()
	if !got.HasInternalDelay() || got.LFT == nil {
		t.Fatal("feedback delay was not represented as a controlsys internal LFT")
	}
	if len(got.LFT.Tau) != 1 || math.Abs(got.LFT.Tau[0]-0.2) > 1e-12 {
		t.Fatalf("internal delays = %v, want [0.2]", got.LFT.Tau)
	}

	controller, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{2}), 0)
	if err != nil {
		t.Fatal(err)
	}
	plant, err := realizeBlock(blocks[3], []int{0})
	if err != nil {
		t.Fatal(err)
	}
	forward, err := controlsys.Series(controller, plant)
	if err != nil {
		t.Fatal(err)
	}
	delay, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := delay.SetInputDelay([]float64{0.2}); err != nil {
		t.Fatal(err)
	}
	want, err := controlsys.Feedback(forward, delay, -1)
	if err != nil {
		t.Fatal(err)
	}

	frequencies := []float64{0.1, 0.5, 1, 2}
	gotResponse, err := got.FreqResponse(frequencies)
	if err != nil {
		t.Fatal(err)
	}
	wantResponse, err := want.FreqResponse(frequencies)
	if err != nil {
		t.Fatal(err)
	}
	for frequency := range frequencies {
		if diff := cmplx.Abs(gotResponse.At(frequency, 0, 0) - wantResponse.At(frequency, 0, 0)); diff > 1e-10 {
			t.Fatalf("frequency %g response diff = %g", frequencies[frequency], diff)
		}
	}

	run, err := model.run(SimulationRequest{Duration: 2, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 1 || len(run.Series[0].Values) != 21 {
		t.Fatalf("simulation shape = series %d samples %d", len(run.Series), len(run.Series[0].Values))
	}
	discreteWant, err := want.DiscretizeWithOpts(0.1, controlsys.C2DOptions{
		Method:        controlsys.C2DMethodZOH,
		DelayModeling: controlsys.C2DDelayModelingInternal,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := mat.NewDense(1, len(run.Times), nil)
	for sample := range run.Times {
		input.Set(0, sample, 1)
	}
	timeWant, err := discreteWant.Simulate(input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for sample, got := range run.Series[0].Values {
		if diff := math.Abs(got - timeWant.Y.At(0, sample)); diff > 1e-11 {
			t.Fatalf("sample %d exact-feedback diff = %g", sample, diff)
		}
	}
}

func TestStaticParallelExactDelaysMustHaveOnePathDelay(t *testing.T) {
	_, err := compileModel([]Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockDelay, Name: "Fast path", Parameters: Parameters{
			Delay: 0.1, DelayMode: delayModeExact,
		}},
		{ID: 3, Kind: BlockDelay, Name: "Slow path", Parameters: Parameters{
			Delay: 0.2, DelayMode: delayModeExact,
		}},
		{ID: 4, Kind: BlockSum, Name: "Mixer", Parameters: Parameters{Signs: "++"}},
		{ID: 5, Kind: BlockScope, Name: "Output"},
	}, []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 1, TargetID: 3},
		{ID: 3, SourceID: 2, TargetID: 4, TargetPort: 0},
		{ID: 4, SourceID: 3, TargetID: 4, TargetPort: 1},
		{ID: 5, SourceID: 4, TargetID: 5},
	})
	if err == nil || !strings.Contains(err.Error(), "parallel static paths with different exact delays") {
		t.Fatalf("error = %v, want explicit residual-path refusal", err)
	}
}

func TestStaticExactDelaysPreserveNamedMIMOPathMatrix(t *testing.T) {
	model, err := compileModel([]Block{
		{ID: 1, Kind: BlockConstant, Name: "Feed A", Parameters: Parameters{Value: 1}},
		{ID: 2, Kind: BlockConstant, Name: "Feed B", Parameters: Parameters{Value: 2}},
		{ID: 3, Kind: BlockDelay, Name: "Pipe A", Parameters: Parameters{
			Delay: 0.1, DelayMode: delayModeExact,
		}},
		{ID: 4, Kind: BlockDelay, Name: "Pipe B", Parameters: Parameters{
			Delay: 0.3, DelayMode: delayModeExact,
		}},
		{ID: 5, Kind: BlockScope, Name: "Product A"},
		{ID: 6, Kind: BlockScope, Name: "Product B"},
	}, []Connection{
		{ID: 1, SourceID: 1, TargetID: 3},
		{ID: 2, SourceID: 2, TargetID: 4},
		{ID: 3, SourceID: 3, TargetID: 5},
		{ID: 4, SourceID: 4, TargetID: 6},
	})
	if err != nil {
		t.Fatal(err)
	}
	system := model.systemCopy()
	if system.Delay == nil {
		t.Fatal("compiled MIMO model lost exact I/O delay matrix")
	}
	want := []float64{0.1, 0, 0, 0.3}
	for output := range 2 {
		for input := range 2 {
			if got := system.Delay.At(output, input); math.Abs(got-want[output*2+input]) > 1e-12 {
				t.Fatalf("delay[%d,%d] = %g, want %g", output, input, got, want[output*2+input])
			}
		}
	}
	if len(system.InputName) != 2 || len(system.OutputName) != 2 {
		t.Fatalf("named channel dimensions = inputs %v outputs %v", system.InputName, system.OutputName)
	}
}
