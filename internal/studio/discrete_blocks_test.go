package studio

import (
	"math"
	"math/cmplx"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
)

func TestUnitDelayCarriesExactStateAtInheritedAndExplicitRates(t *testing.T) {
	tests := []struct {
		name            string
		parameters      Parameters
		duration        float64
		sampleTime      float64
		expectedSamples int
	}{
		{
			name: "inherited",
			parameters: Parameters{
				SampleTimeMode: string(sampleTimeInherited),
			},
			duration: 1, sampleTime: 0.1, expectedSamples: 11,
		},
		{
			name: "explicit five milliseconds",
			parameters: Parameters{
				SampleTime: 0.005, SampleTimeMode: string(sampleTimeExplicit),
			},
			duration: 1, sampleTime: 0.005, expectedSamples: 201,
		},
		{
			name: "explicit one minute",
			parameters: Parameters{
				SampleTime: 60, SampleTimeMode: string(sampleTimeExplicit),
			},
			duration: 7200, sampleTime: 60, expectedSamples: 121,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, err := simulate([]Block{
				{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
				{ID: 2, Kind: BlockUnitDelay, Name: "Memory", Parameters: test.parameters},
				{ID: 3, Kind: BlockScope, Name: "Output"},
			}, []Connection{
				{SourceID: 1, TargetID: 2},
				{SourceID: 2, TargetID: 3},
			}, SimulationRequest{Duration: test.duration, SampleTime: test.sampleTime})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(run.Series[0].Values); got != test.expectedSamples {
				t.Fatalf("samples = %d, want %d", got, test.expectedSamples)
			}
			for sample, got := range run.Series[0].Values {
				want := 1.0
				if sample == 0 {
					want = 0
				}
				if got != want {
					t.Fatalf("sample %d = %g, want %g", sample, got, want)
				}
			}
			if run.Fidelity.Driver != "per-sample-simulate" {
				t.Fatalf("driver = %q, want per-sample-simulate", run.Fidelity.Driver)
			}
		})
	}
}

func TestDiscreteTransferFunctionMatchesDifferenceEquation(t *testing.T) {
	parameters := defaultParameters(BlockDiscreteTransfer)
	run, err := simulate([]Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockDiscreteTransfer, Name: "Filter", Parameters: parameters},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}, []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	want := 0.0
	for sample, got := range run.Series[0].Values {
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("sample %d = %.12g, want %.12g", sample, got, want)
		}
		want = 0.9*want + 0.1
	}
}

func TestDiscreteStateSpaceMatchesMIMODifferenceEquation(t *testing.T) {
	source := defaultParameters(BlockVectorConstant)
	values, _ := NewVectorValue([]float64{1, 2})
	source.Vector = &values
	stateSpace := defaultParameters(BlockDiscreteStateSpace)
	scope := defaultParameters(BlockVectorScope)

	run, err := simulate([]Block{
		{ID: 1, Kind: BlockVectorConstant, Name: "Input", Parameters: source},
		{ID: 2, Kind: BlockDiscreteStateSpace, Name: "Plant", Parameters: stateSpace},
		{ID: 3, Kind: BlockVectorScope, Name: "Output", Parameters: scope},
	}, []Connection{
		{SourceID: 1, TargetID: 2},
		{SourceID: 2, TargetID: 3},
	}, SimulationRequest{Duration: 0.5, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	state := [2]float64{}
	for sample := range run.Times {
		if math.Abs(run.Series[0].Values[sample]-state[0]) > 1e-12 ||
			math.Abs(run.Series[1].Values[sample]-state[1]) > 1e-12 {
			t.Fatalf(
				"sample %d = [%g %g], want [%g %g]",
				sample,
				run.Series[0].Values[sample], run.Series[1].Values[sample],
				state[0], state[1],
			)
		}
		state[0] = 0.8*state[0] + 1
		state[1] = 0.5*state[1] + 2
	}
}

func TestExplicitC2DMethodsMatchDirectControlsysConversions(t *testing.T) {
	continuousBlock := Block{
		ID: 99, Kind: BlockTransfer, Name: "Continuous",
		Parameters: Parameters{
			Numerator: []float64{1}, Denominator: []float64{1, 1},
		},
	}
	continuous, err := realizeBlock(continuousBlock, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method string
		direct func() (*controlsys.System, error)
	}{
		{string(controlsys.C2DMethodZOH), func() (*controlsys.System, error) {
			return continuous.DiscretizeZOH(0.1)
		}},
		{string(controlsys.C2DMethodFOH), func() (*controlsys.System, error) {
			return continuous.DiscretizeFOH(0.1)
		}},
		{string(controlsys.C2DMethodMatched), func() (*controlsys.System, error) {
			return continuous.DiscretizeMatched(0.1)
		}},
		{string(controlsys.C2DMethodImpulse), func() (*controlsys.System, error) {
			return continuous.DiscretizeImpulse(0.1)
		}},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			parameters := defaultParameters(BlockDiscretizedTransfer)
			parameters.ConversionMethod = test.method
			got, err := realizeBlock(Block{
				ID: 1, Kind: BlockDiscretizedTransfer,
				Name: "Converted", Parameters: parameters,
			}, []int{0})
			if err != nil {
				t.Fatal(err)
			}
			want, err := test.direct()
			if err != nil {
				t.Fatal(err)
			}
			gotResponse, err := got.FreqResponse([]float64{0.1, 0.5, 1})
			if err != nil {
				t.Fatal(err)
			}
			wantResponse, err := want.FreqResponse([]float64{0.1, 0.5, 1})
			if err != nil {
				t.Fatal(err)
			}
			for frequency := range 3 {
				if diff := cmplx.Abs(
					gotResponse.At(frequency, 0, 0) -
						wantResponse.At(frequency, 0, 0),
				); diff > 1e-12 {
					t.Fatalf("frequency %d diff = %g", frequency, diff)
				}
			}
		})
	}
}

func TestExplicitC2DRejectsInapplicableModelAndUnknownMethod(t *testing.T) {
	parameters := defaultParameters(BlockDiscretizedTransfer)
	parameters.Numerator = []float64{1, 2, 3}
	parameters.Denominator = []float64{1, 2}
	if err := validateParameters(BlockDiscretizedTransfer, parameters); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "proper") {
		t.Fatalf("improper-model error = %v, want properness context", err)
	}

	parameters = defaultParameters(BlockDiscretizedTransfer)
	parameters.ConversionMethod = "forward-euler"
	if err := validateParameters(BlockDiscretizedTransfer, parameters); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "zoh, foh, matched, or impulse") {
		t.Fatalf("method error = %v, want supported-method context", err)
	}
}

func TestThiranSummaryReportsOrderRateAndPhaseBehavior(t *testing.T) {
	parameters := Parameters{
		Delay: 0.35, DelayMode: delayModeThiran,
		Approximation: 3, SampleTime: 0.1,
		SampleTimeMode: string(sampleTimeExplicit),
	}
	block := Block{Kind: BlockDelay, Parameters: parameters}
	if summary := block.Summary(); !strings.Contains(summary, "Thiran 3 @ 0.1 s") {
		t.Fatalf("summary = %q", summary)
	}
	system, err := realizeBlock(Block{
		ID: 1, Kind: BlockDelay, Name: "Thiran", Parameters: parameters,
	}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	response, err := system.FreqResponse([]float64{0.2})
	if err != nil {
		t.Fatal(err)
	}
	want := cmplx.Exp(complex(0, -0.2*0.35))
	if diff := cmplx.Abs(response.At(0, 0, 0) - want); diff > 1e-5 {
		t.Fatalf("Thiran phase response diff = %g", diff)
	}
}
