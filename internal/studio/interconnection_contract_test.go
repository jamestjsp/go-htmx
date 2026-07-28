package studio

import (
	"errors"
	"math/cmplx"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

func TestCompiledSeriesMatchesSpecializedControlsysAlgebra(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockGain, Name: "Gain", Parameters: Parameters{Gain: 2}},
		{ID: 3, Kind: BlockLag, Name: "Lag", Parameters: Parameters{TimeConstant: 0.5}},
		{ID: 4, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 4},
	}
	model, err := compileModel(blocks, connections)
	if err != nil {
		t.Fatal(err)
	}

	gain, err := realizeBlock(blocks[1], []int{0})
	if err != nil {
		t.Fatal(err)
	}
	lag, err := realizeBlock(blocks[2], []int{0})
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := controlsys.Series(gain, lag)
	if err != nil {
		t.Fatal(err)
	}
	oracle.InputName = []string{"block_1_source"}
	oracle.OutputName = []string{"block_4_output_0"}

	times, input := contractInput(81, 0.05, 1)
	for sample := range times {
		input.Set(sample, 0, 0.5+0.25*times[sample])
	}
	assertSystemsEquivalent(t, model.systemCopy(), oracle, input, times, 1e-11)
}

func TestCompiledSignedParallelMIMOMatchesDirectFeedthroughOracle(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Left", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSource, Name: "Right", Parameters: Parameters{Amplitude: 1}},
		{ID: 3, Kind: BlockGain, Name: "Twice", Parameters: Parameters{Gain: 2}},
		{ID: 4, Kind: BlockGain, Name: "Triple", Parameters: Parameters{Gain: 3}},
		{ID: 5, Kind: BlockSum, Name: "Difference", Parameters: Parameters{Signs: "+-"}},
		{ID: 6, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 3},
		{ID: 2, SourceID: 2, TargetID: 4},
		{ID: 3, SourceID: 3, TargetID: 5, TargetPort: 0},
		{ID: 4, SourceID: 4, TargetID: 5, TargetPort: 1},
		{ID: 5, SourceID: 5, TargetID: 6},
	}
	model, err := compileRequestedModel(blocks, connections, modelCompileRequest{
		includeSinks: true,
		probes: []modelProbe{
			{BlockID: 3}, {BlockID: 4}, {BlockID: 5}, {BlockID: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	oracle, err := controlsys.NewGain(mat.NewDense(4, 2, []float64{
		2, -3,
		2, 0,
		0, 3,
		2, -3,
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	oracle.InputName = []string{"block_1_source", "block_2_source"}
	oracle.OutputName = []string{
		"block_6_output_0",
		"block_3_output_0",
		"block_4_output_0",
		"block_5_output_0",
	}

	times, input := contractInput(61, 0.1, 2)
	for sample, time := range times {
		input.Set(sample, 0, 1+time)
		input.Set(sample, 1, 0.25-0.5*time)
	}
	assertSystemsEquivalent(t, model.systemCopy(), oracle, input, times, 1e-12)
}

func TestInterconnectionFailuresAreDeterministic(t *testing.T) {
	t.Run("duplicate generated names", func(t *testing.T) {
		_, err := compileRequestedModel([]Block{
			{ID: 1, Kind: BlockSource, Name: "First", Parameters: Parameters{Amplitude: 1}},
			{ID: 1, Kind: BlockSource, Name: "Second", Parameters: Parameters{Amplitude: 1}},
		}, nil, modelCompileRequest{probes: []modelProbe{{BlockID: 1}}})
		var validation *ValidationError
		if !errors.As(err, &validation) || !strings.Contains(err.Error(), "share block id 1") {
			t.Fatalf("error = %v, want duplicate block identity validation", err)
		}
	})

	t.Run("dimension mismatch", func(t *testing.T) {
		left, err := controlsys.NewGain(mat.NewDense(1, 2, nil), 0)
		if err != nil {
			t.Fatal(err)
		}
		right, err := controlsys.NewGain(mat.NewDense(1, 3, nil), 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = controlsys.Series(left, right)
		if !errors.Is(err, controlsys.ErrDimensionMismatch) {
			t.Fatalf("error = %v, want ErrDimensionMismatch", err)
		}
	})

	t.Run("mixed time domains", func(t *testing.T) {
		continuous, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0)
		if err != nil {
			t.Fatal(err)
		}
		discrete, err := controlsys.NewGain(mat.NewDense(1, 1, []float64{1}), 0.1)
		if err != nil {
			t.Fatal(err)
		}
		_, err = controlsys.Series(continuous, discrete)
		if !errors.Is(err, controlsys.ErrDomainMismatch) {
			t.Fatalf("error = %v, want ErrDomainMismatch", err)
		}
	})
}

func assertSystemsEquivalent(
	t *testing.T,
	got, want *controlsys.System,
	input *mat.Dense,
	times []float64,
	tolerance float64,
) {
	t.Helper()
	if gotDims, wantDims := systemDimensions(got), systemDimensions(want); gotDims != wantDims {
		t.Fatalf("dimensions = %+v, want %+v", gotDims, wantDims)
	}
	if !reflect.DeepEqual(got.InputName, want.InputName) ||
		!reflect.DeepEqual(got.OutputName, want.OutputName) {
		t.Fatalf("names = %v/%v, want %v/%v",
			got.InputName, got.OutputName, want.InputName, want.OutputName)
	}

	gotPoles, err := got.Poles()
	if err != nil {
		t.Fatal(err)
	}
	wantPoles, err := want.Poles()
	if err != nil {
		t.Fatal(err)
	}
	sortComplex(gotPoles)
	sortComplex(wantPoles)
	if len(gotPoles) != len(wantPoles) {
		t.Fatalf("poles = %v, want %v", gotPoles, wantPoles)
	}
	for i := range gotPoles {
		if cmplx.Abs(gotPoles[i]-wantPoles[i]) > tolerance {
			t.Fatalf("pole %d = %v, want %v", i, gotPoles[i], wantPoles[i])
		}
	}

	gotGain, err := got.DCGain()
	if err != nil {
		t.Fatal(err)
	}
	wantGain, err := want.DCGain()
	if err != nil {
		t.Fatal(err)
	}
	if !mat.EqualApprox(gotGain, wantGain, tolerance) {
		t.Fatalf("DC gain =\n%v\nwant\n%v", mat.Formatted(gotGain), mat.Formatted(wantGain))
	}

	gotResponse, err := controlsys.Lsim(got, input, times, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantResponse, err := controlsys.Lsim(want, input, times, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotResponse.OutputName, wantResponse.OutputName) {
		t.Fatalf("response names = %v, want %v", gotResponse.OutputName, wantResponse.OutputName)
	}
	if !mat.EqualApprox(gotResponse.Y, wantResponse.Y, tolerance) {
		t.Fatalf("sampled responses differ beyond %.3g", tolerance)
	}
}

func systemDimensions(system *controlsys.System) compiledModelDimensions {
	states, inputs, outputs := system.Dims()
	return compiledModelDimensions{States: states, Inputs: inputs, Outputs: outputs}
}

func sortComplex(values []complex128) {
	sort.Slice(values, func(i, j int) bool {
		if real(values[i]) != real(values[j]) {
			return real(values[i]) < real(values[j])
		}
		return imag(values[i]) < imag(values[j])
	})
}

func contractInput(samples int, sampleTime float64, inputs int) ([]float64, *mat.Dense) {
	times := make([]float64, samples)
	for sample := range samples {
		times[sample] = float64(sample) * sampleTime
	}
	return times, mat.NewDense(samples, inputs, nil)
}

func BenchmarkCompileModel(b *testing.B) {
	for _, states := range []int{1, 8, 32, 128} {
		blocks := make([]Block, 0, states+2)
		connections := make([]Connection, 0, states+1)
		blocks = append(blocks, Block{
			ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1},
		})
		for state := 0; state < states; state++ {
			id := int64(state + 2)
			blocks = append(blocks, Block{
				ID: id, Kind: BlockLag, Name: "Lag",
				Parameters: Parameters{TimeConstant: 1 + float64(state)/10},
			})
			connections = append(connections, Connection{
				ID: int64(state + 1), SourceID: id - 1, TargetID: id,
			})
		}
		sinkID := int64(states + 2)
		blocks = append(blocks, Block{ID: sinkID, Kind: BlockScope, Name: "Output"})
		connections = append(connections, Connection{
			ID: int64(states + 1), SourceID: sinkID - 1, TargetID: sinkID,
		})

		b.Run("states="+strconv.Itoa(states), func(b *testing.B) {
			b.ReportMetric(float64(len(blocks)), "blocks")
			b.ReportMetric(float64(states), "states")
			b.ReportMetric(float64(len(connections)), "connections")
			for range b.N {
				if _, err := compileModel(blocks, connections); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
