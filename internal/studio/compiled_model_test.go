package studio

import (
	"reflect"
	"testing"
)

func TestCompiledModelOwnsNamedChannelsAndProvenance(t *testing.T) {
	blocks := []Block{
		{ID: 20, Kind: BlockScope, Name: "Trend"},
		{ID: 10, Kind: BlockConstant, Name: "Bias", Parameters: Parameters{Value: 2}},
		{ID: 7, Kind: BlockLag, Name: "Plant", Parameters: Parameters{TimeConstant: 1}},
		{ID: 2, Kind: BlockSource, Name: "Setpoint", Parameters: Parameters{Amplitude: 1}},
		{ID: 15, Kind: BlockSpectrum, Name: "Spectrum"},
		{ID: 5, Kind: BlockSum, Name: "Demand", Parameters: Parameters{Signs: "++"}},
	}
	connections := []Connection{
		{ID: 6, SourceID: 7, TargetID: 20},
		{ID: 2, SourceID: 10, TargetID: 5, TargetPort: 1},
		{ID: 4, SourceID: 7, TargetID: 15},
		{ID: 3, SourceID: 5, TargetID: 7},
		{ID: 1, SourceID: 2, TargetID: 5, TargetPort: 0},
	}

	model, err := compileModel(blocks, connections)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := model.dimensions(), (compiledModelDimensions{States: 1, Inputs: 2, Outputs: 2}); got != want {
		t.Fatalf("dimensions = %+v, want %+v", got, want)
	}
	if got, want := model.timeDomain(), (compiledModelTimeDomain{Domain: compiledContinuous}); got != want {
		t.Fatalf("time domain = %+v, want %+v", got, want)
	}

	wantInputs := []compiledSignal{
		{Name: "block_2_source", BlockID: 2, Port: 0, Role: compiledExternalInput},
		{Name: "block_10_source", BlockID: 10, Port: 0, Role: compiledExternalInput},
	}
	if got := model.inputChannels(); !reflect.DeepEqual(got, wantInputs) {
		t.Fatalf("input channels = %#v, want %#v", got, wantInputs)
	}
	wantOutputs := []compiledSignal{
		{Name: "block_15_output_0", BlockID: 15, Port: 0, Role: compiledBlockOutput},
		{Name: "block_20_output_0", BlockID: 20, Port: 0, Role: compiledBlockOutput},
	}
	if got := model.outputChannels(); !reflect.DeepEqual(got, wantOutputs) {
		t.Fatalf("output channels = %#v, want %#v", got, wantOutputs)
	}

	assertCompiledSignal(t, model.signalChannels(), compiledSignal{
		Name: "block_5_input_1", BlockID: 5, Port: 1, Role: compiledBlockInput,
	})
	assertCompiledSignal(t, model.signalChannels(), compiledSignal{
		Name: "block_7_output_0", BlockID: 7, Port: 0, Role: compiledBlockOutput,
	})

	provenance := model.modelProvenance()
	if got, want := blockIDs(provenance.Blocks), []int64{2, 5, 7, 10, 15, 20}; !reflect.DeepEqual(got, want) {
		t.Fatalf("block provenance = %v, want %v", got, want)
	}
	if got := connectionIDs(provenance.Connections); !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 6}) {
		t.Fatalf("connection provenance = %v", got)
	}
}

func TestCompiledModelMetadataAndSystemCopiesCannotMutateArtifact(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockTransfer, Name: "Plant", Parameters: Parameters{
			Numerator: []float64{1}, Denominator: []float64{1, 1},
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

	inputs := model.inputChannels()
	inputs[0].Name = "mutated"
	blocks[1].Parameters.Numerator[0] = 99
	provenance := model.modelProvenance()
	provenance.Blocks[0].ID = 99
	provenance.Blocks[1].Parameters.Denominator[0] = 99
	provenance.Connections[0].SourceID = 99
	system := model.systemCopy()
	system.InputName[0] = "mutated"
	system.A.Set(0, 0, 99)

	if got := model.inputChannels()[0].Name; got != "block_1_source" {
		t.Fatalf("input name after caller mutation = %q", got)
	}
	freshProvenance := model.modelProvenance()
	if freshProvenance.Blocks[0].ID != 1 ||
		freshProvenance.Blocks[1].Parameters.Numerator[0] != 1 ||
		freshProvenance.Blocks[1].Parameters.Denominator[0] != 1 ||
		freshProvenance.Connections[0].SourceID != 1 {
		t.Fatalf("provenance was mutated: %#v", freshProvenance)
	}
	freshSystem := model.systemCopy()
	if freshSystem.InputName[0] != "block_1_source" || freshSystem.A.At(0, 0) == 99 {
		t.Fatal("controlsys system copy mutated the compiled model")
	}
}

func assertCompiledSignal(t *testing.T, signals []compiledSignal, want compiledSignal) {
	t.Helper()
	for _, signal := range signals {
		if signal == want {
			return
		}
	}
	t.Fatalf("signals %#v do not contain %#v", signals, want)
}

func connectionIDs(connections []Connection) []int64 {
	ids := make([]int64, len(connections))
	for i, connection := range connections {
		ids[i] = connection.ID
	}
	return ids
}

func blockIDs(blocks []Block) []int64 {
	ids := make([]int64, len(blocks))
	for i, block := range blocks {
		ids[i] = block.ID
	}
	return ids
}
