package studio

import (
	"errors"
	"math"
	"math/cmplx"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestControlsysContinuousDelayMatchesShiftedStepOracle(t *testing.T) {
	system, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SetDelay(mat.NewDense(1, 1, []float64{0.35})); err != nil {
		t.Fatal(err)
	}

	times := delayTimes(21, 0.05)
	discrete, err := system.DiscretizeWithOpts(0.05, controlsys.C2DOptions{
		Method:        controlsys.C2DMethodZOH,
		DelayModeling: controlsys.C2DDelayModelingInternal,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := mat.NewDense(1, len(times), nil)
	for sample := range times {
		input.Set(0, sample, 1)
	}
	response, err := discrete.Simulate(input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for sample, time := range times {
		want := 0.0
		if time+1e-12 >= 0.35 {
			want = 1
		}
		if got := response.Y.At(0, sample); math.Abs(got-want) > 1e-12 {
			t.Fatalf("t=%g: y=%g, want %g", time, got, want)
		}
	}
}

func TestControlsysMIMODelayMatrixMatchesPerPathShiftOracle(t *testing.T) {
	system, err := controlsys.NewGain(mat.NewDense(2, 2, []float64{
		1, 2,
		3, 4,
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	delays := mat.NewDense(2, 2, []float64{
		0.1, 0.2,
		0.3, 0.4,
	})
	if err := system.SetDelay(delays); err != nil {
		t.Fatal(err)
	}

	times := delayTimes(11, 0.1)
	discrete, err := system.DiscretizeWithOpts(0.1, controlsys.C2DOptions{
		Method:        controlsys.C2DMethodZOH,
		DelayModeling: controlsys.C2DDelayModelingInternal,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := mat.NewDense(2, len(times), nil)
	for sample := range times {
		input.Set(0, sample, 1)
		input.Set(1, sample, 1)
	}
	response, err := discrete.Simulate(input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gains := [2][2]float64{{1, 2}, {3, 4}}
	for sample, time := range times {
		for output := range 2 {
			var want float64
			for inputIndex := range 2 {
				if time+1e-12 >= delays.At(output, inputIndex) {
					want += gains[output][inputIndex]
				}
			}
			if got := response.Y.At(output, sample); math.Abs(got-want) > 1e-12 {
				t.Fatalf("t=%g output=%d: y=%g, want %g", time, output, got, want)
			}
		}
	}
}

func TestControlsysLsimRequiresExplicitContinuousDelayConversion(t *testing.T) {
	system, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SetInputDelay([]float64{0.2}); err != nil {
		t.Fatal(err)
	}
	times := delayTimes(6, 0.1)
	input := mat.NewDense(len(times), 1, nil)
	for sample := range times {
		input.Set(sample, 0, 1)
	}
	response, err := controlsys.Lsim(system, input, times, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Y.At(0, 0); got != 1 {
		t.Fatalf("Lsim t=0 = %g, want current undelayed behavior 1", got)
	}

	discrete, err := system.DiscretizeWithOpts(0.1, controlsys.C2DOptions{
		Method: controlsys.C2DMethodZOH,
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitInput := mat.NewDense(1, len(times), nil)
	for sample := range times {
		explicitInput.Set(0, sample, 1)
	}
	explicit, err := discrete.Simulate(explicitInput, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Y.At(0, 0) != 0 || explicit.Y.At(0, 1) != 0 || explicit.Y.At(0, 2) != 1 {
		t.Fatalf("explicit delay samples = [%g %g %g], want [0 0 1]",
			explicit.Y.At(0, 0), explicit.Y.At(0, 1), explicit.Y.At(0, 2))
	}
}

func TestControlsysDiscreteDelayUsesIntegerSamples(t *testing.T) {
	system, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0.1)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SetDelay(mat.NewDense(1, 1, []float64{3})); err != nil {
		t.Fatal(err)
	}
	input := mat.NewDense(1, 8, nil)
	for sample := range 8 {
		input.Set(0, sample, 1)
	}
	response, err := system.Simulate(input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for sample := range 8 {
		want := 0.0
		if sample >= 3 {
			want = 1
		}
		if got := response.Y.At(0, sample); got != want {
			t.Fatalf("sample %d: y=%g, want %g", sample, got, want)
		}
	}
	if err := system.SetDelay(mat.NewDense(1, 1, []float64{2.5})); !errors.Is(err, controlsys.ErrFractionalDelay) {
		t.Fatalf("fractional discrete delay error = %v, want ErrFractionalDelay", err)
	}
}

func TestControlsysDelayApproximationChoicesRemainExplicit(t *testing.T) {
	continuous, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := continuous.SetInputDelay([]float64{0.35}); err != nil {
		t.Fatal(err)
	}

	pade, err := continuous.Pade(3)
	if err != nil {
		t.Fatal(err)
	}
	if continuous.InputDelay[0] != 0.35 || !continuous.HasDelay() {
		t.Fatal("Padé mutated the exact-delay model")
	}
	if pade.HasDelay() {
		t.Fatal("Padé model retained exact delay metadata")
	}
	if states, _, _ := pade.Dims(); states != 3 {
		t.Fatalf("Padé states = %d, want 3", states)
	}

	if _, err := continuous.DiscretizeWithOpts(0.1, controlsys.C2DOptions{
		Method: controlsys.C2DMethodZOH,
	}); !errors.Is(err, controlsys.ErrFractionalDelay) {
		t.Fatalf("fractional c2d error = %v, want ErrFractionalDelay", err)
	}
	thiran, err := continuous.DiscretizeWithOpts(0.1, controlsys.C2DOptions{
		Method: controlsys.C2DMethodZOH, ThiranOrder: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if thiran.HasDelay() {
		t.Fatal("Thiran conversion retained external delay metadata")
	}
	if !thiran.IsDiscrete() || thiran.Dt != 0.1 {
		t.Fatalf("Thiran domain = discrete %v, dt %g", thiran.IsDiscrete(), thiran.Dt)
	}

	omega := []float64{0.2}
	exactResponse, err := continuous.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	padeResponse, err := pade.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	thiranResponse, err := thiran.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	want := cmplx.Exp(complex(0, -omega[0]*0.35))
	if diff := cmplx.Abs(exactResponse.At(0, 0, 0) - want); diff > 1e-12 {
		t.Fatalf("exact delay frequency response diff = %g", diff)
	}
	if diff := cmplx.Abs(padeResponse.At(0, 0, 0) - want); diff > 1e-8 {
		t.Fatalf("Padé frequency response diff = %g", diff)
	}
	if diff := cmplx.Abs(thiranResponse.At(0, 0, 0) - want); diff > 1e-5 {
		t.Fatalf("Thiran frequency response diff = %g", diff)
	}
}

func TestControlsysNamedFeedbackPromotesLoopDelayToInternalLFT(t *testing.T) {
	plant, err := controlsys.New(
		mat.NewDense(1, 1, []float64{-1}),
		mat.NewDense(1, 1, []float64{1}),
		mat.NewDense(1, 1, []float64{1}),
		mat.NewDense(1, 1, nil),
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	plant.InputName = []string{"error"}
	plant.OutputName = []string{"output"}
	if err := plant.SetOutputDelay([]float64{0.3}); err != nil {
		t.Fatal(err)
	}

	observer, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
	if err != nil {
		t.Fatal(err)
	}
	observer.InputName = []string{"observed_input"}
	observer.OutputName = []string{"observed_output"}

	closed, err := controlsys.ConnectByName(
		[]*controlsys.System{plant, observer},
		[]controlsys.Connection{
			{From: "output", To: "error", Gain: -1},
			{From: "output", To: "observed_input", Gain: 1},
		},
		[]string{"error"},
		[]string{"observed_output"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.HasInternalDelay() || closed.LFT == nil {
		t.Fatal("named feedback did not promote loop delay to internal LFT")
	}
	if len(closed.LFT.Tau) != 1 || math.Abs(closed.LFT.Tau[0]-0.3) > 1e-12 {
		t.Fatalf("internal delays = %v, want [0.3]", closed.LFT.Tau)
	}
	if len(closed.InputName) != 1 || closed.InputName[0] != "error" ||
		len(closed.OutputName) != 1 || closed.OutputName[0] != "observed_output" {
		t.Fatalf("closed-loop names = %v/%v", closed.InputName, closed.OutputName)
	}
}

func delayTimes(samples int, sampleTime float64) []float64 {
	times := make([]float64, samples)
	for sample := range samples {
		times[sample] = float64(sample) * sampleTime
	}
	return times
}
