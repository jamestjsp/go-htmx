package studio

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestRoutingBlocksUseStaticSelectionMatrices(t *testing.T) {
	inputNames, _ := NewChannelNames([]string{"feed", "recycle", "purge"})
	selectedNames, _ := NewChannelNames([]string{"purge", "feed"})
	selector := Block{
		Kind: BlockSelector,
		Parameters: Parameters{
			InputNames: &inputNames, OutputNames: &selectedNames,
		},
	}
	system, err := realizeBlock(selector, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if states, inputs, outputs := system.Dims(); states != 0 || inputs != 3 || outputs != 2 {
		t.Fatalf("selector dimensions = %d states, %d inputs, %d outputs", states, inputs, outputs)
	}
	want := []float64{0, 0, 1, 1, 0, 0}
	if got := append([]float64(nil), system.D.RawMatrix().Data...); !reflect.DeepEqual(got, want) {
		t.Fatalf("selector D = %v, want %v", got, want)
	}
	if got := system.InputName; !reflect.DeepEqual(
		got,
		[]string{"block_0_input_0_channel_0", "block_0_input_0_channel_1", "block_0_input_0_channel_2"},
	) {
		t.Fatalf("selector input names = %v", got)
	}
}

func TestRoutingValidationRejectsMissingAndIncompleteChannels(t *testing.T) {
	inputNames, _ := NewChannelNames([]string{"a", "b", "c"})
	missing, _ := NewChannelNames([]string{"a", "missing"})
	err := blockDefinitions[BlockSelector].validate(Parameters{
		InputNames: &inputNames, OutputNames: &missing,
	})
	if err == nil || !strings.Contains(err.Error(), `"missing"`) {
		t.Fatalf("selector error = %v, want missing-channel context", err)
	}

	incomplete, _ := NewChannelNames([]string{"c", "a"})
	err = blockDefinitions[BlockPermutation].validate(Parameters{
		InputNames: &inputNames, OutputNames: &incomplete,
	})
	if err == nil || !strings.Contains(err.Error(), "every input channel") {
		t.Fatalf("permutation error = %v, want complete-set context", err)
	}
}

func TestMuxPermutationDemuxPreserveNamesAndValues(t *testing.T) {
	muxNames, _ := NewChannelNames([]string{"hot", "cold"})
	permutationInput, _ := NewChannelNames([]string{"hot", "cold"})
	permutationOutput, _ := NewChannelNames([]string{"cold", "hot"})
	demuxNames, _ := NewChannelNames([]string{"cold", "hot"})
	vectorScopeNames, _ := NewChannelNames([]string{"hot", "cold"})

	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Hot", Parameters: Parameters{Value: 10}},
		{ID: 2, Kind: BlockConstant, Name: "Cold", Parameters: Parameters{Value: 20}},
		{ID: 3, Kind: BlockMux, Name: "Assemble", Parameters: Parameters{OutputNames: &muxNames}},
		{ID: 4, Kind: BlockPermutation, Name: "Swap", Parameters: Parameters{
			InputNames: &permutationInput, OutputNames: &permutationOutput,
		}},
		{ID: 5, Kind: BlockDemux, Name: "Split", Parameters: Parameters{InputNames: &demuxNames}},
		{ID: 6, Kind: BlockScope, Name: "Cold output"},
		{ID: 7, Kind: BlockScope, Name: "Hot output"},
		{ID: 8, Kind: BlockVectorScope, Name: "Vector branch", Parameters: Parameters{
			InputNames: &vectorScopeNames,
		}},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 3, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3, TargetPort: 1},
		{ID: 3, SourceID: 3, TargetID: 4},
		{ID: 4, SourceID: 4, TargetID: 5},
		{ID: 5, SourceID: 5, SourcePort: 0, TargetID: 6},
		{ID: 6, SourceID: 5, SourcePort: 1, TargetID: 7},
		{ID: 7, SourceID: 3, TargetID: 8},
	}
	run, err := simulate(blocks, connections, SimulationRequest{
		Duration: 1, SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 4 {
		t.Fatalf("routing series = %d, want two scalar and two vector-branch channels", len(run.Series))
	}
	wantValues := []float64{20, 10, 10, 20}
	for series, want := range wantValues {
		for sample, got := range run.Series[series].Values {
			if got != want {
				t.Fatalf("series %d sample %d = %g, want %g", series, sample, got, want)
			}
		}
	}
}

func TestSelectorSupportsNamedSubset(t *testing.T) {
	values, _ := NewVectorValue([]float64{1, 2, 3})
	sourceNames, _ := NewChannelNames([]string{"a", "b", "c"})
	selectedNames, _ := NewChannelNames([]string{"c", "a"})
	scopeNames, _ := NewChannelNames([]string{"c", "a"})
	run, err := simulate(
		[]Block{
			{ID: 1, Kind: BlockVectorConstant, Name: "Input", Parameters: Parameters{
				Vector: &values, OutputNames: &sourceNames,
			}},
			{ID: 2, Kind: BlockSelector, Name: "Subset", Parameters: Parameters{
				InputNames: &sourceNames, OutputNames: &selectedNames,
			}},
			{ID: 3, Kind: BlockVectorScope, Name: "Output", Parameters: Parameters{
				InputNames: &scopeNames,
			}},
		},
		[]Connection{
			{SourceID: 1, TargetID: 2},
			{SourceID: 2, TargetID: 3},
		},
		SimulationRequest{Duration: 1, SampleTime: 0.1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 2 {
		t.Fatalf("subset series = %d, want 2", len(run.Series))
	}
	for sample := range run.Times {
		if math.Abs(run.Series[0].Values[sample]-3) > 1e-12 ||
			math.Abs(run.Series[1].Values[sample]-1) > 1e-12 {
			t.Fatalf(
				"subset sample %d = [%g %g], want [3 1]",
				sample,
				run.Series[0].Values[sample],
				run.Series[1].Values[sample],
			)
		}
	}
}

func TestMuxRequiresEveryDeclaredScalarInput(t *testing.T) {
	names, _ := NewChannelNames([]string{"a", "b"})
	_, err := compileModel(
		[]Block{
			{ID: 1, Kind: BlockConstant, Name: "A", Parameters: Parameters{Value: 1}},
			{ID: 2, Kind: BlockMux, Name: "Mux", Parameters: Parameters{OutputNames: &names}},
			{ID: 3, Kind: BlockVectorScope, Name: "Output", Parameters: Parameters{InputNames: &names}},
		},
		[]Connection{
			{SourceID: 1, TargetID: 2, TargetPort: 0},
			{SourceID: 2, TargetID: 3},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "one scalar input for each") {
		t.Fatalf("partial mux error = %v", err)
	}
}

func TestPermutationCompilesInsideMIMOFeedback(t *testing.T) {
	setpointValues, _ := NewVectorValue([]float64{2, 1})
	setpointNames, _ := NewChannelNames([]string{"r1", "r2"})
	sumInputs, _ := NewChannelNames([]string{"r1", "r2"})
	sumOutputs, _ := NewChannelNames([]string{"e1", "e2"})
	plantMatrix, _ := NewMatrixValue(2, 2, []float64{0.5, 0, 0, 0.5})
	plantInputs, _ := NewChannelNames([]string{"e1", "e2"})
	plantOutputs, _ := NewChannelNames([]string{"y1", "y2"})
	feedbackInputs, _ := NewChannelNames([]string{"y1", "y2"})
	feedbackOutputs, _ := NewChannelNames([]string{"y2", "y1"})
	scopeNames, _ := NewChannelNames([]string{"y1", "y2"})

	run, err := simulate(
		[]Block{
			{ID: 1, Kind: BlockVectorConstant, Name: "Setpoint", Parameters: Parameters{
				Vector: &setpointValues, OutputNames: &setpointNames,
			}},
			{ID: 2, Kind: BlockVectorSum, Name: "Error", Parameters: Parameters{
				Signs: "+-", InputNames: &sumInputs, OutputNames: &sumOutputs,
			}},
			{ID: 3, Kind: BlockMatrixGain, Name: "Plant", Parameters: Parameters{
				D: &plantMatrix, InputNames: &plantInputs, OutputNames: &plantOutputs,
			}},
			{ID: 4, Kind: BlockPermutation, Name: "Cross feedback", Parameters: Parameters{
				InputNames: &feedbackInputs, OutputNames: &feedbackOutputs,
			}},
			{ID: 5, Kind: BlockVectorScope, Name: "Output", Parameters: Parameters{
				InputNames: &scopeNames,
			}},
		},
		[]Connection{
			{SourceID: 1, TargetID: 2, TargetPort: 0},
			{SourceID: 2, TargetID: 3},
			{SourceID: 3, TargetID: 4},
			{SourceID: 4, TargetID: 2, TargetPort: 1},
			{SourceID: 3, TargetID: 5},
		},
		SimulationRequest{Duration: 1, SampleTime: 0.1},
	)
	if err != nil {
		t.Fatal(err)
	}
	for sample := range run.Times {
		if math.Abs(run.Series[0].Values[sample]-1) > 1e-12 ||
			math.Abs(run.Series[1].Values[sample]) > 1e-12 {
			t.Fatalf(
				"closed-loop sample %d = [%g %g], want [1 0]",
				sample,
				run.Series[0].Values[sample],
				run.Series[1].Values[sample],
			)
		}
	}
}
