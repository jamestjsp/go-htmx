package studio

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestScalarPortsKeepWidthOneAndExistingPortIDs(t *testing.T) {
	for _, kind := range blockOrder {
		if kind == BlockVectorConstant || kind == BlockMatrixGain ||
			kind == BlockVectorSum || kind == BlockVectorScope ||
			kind == BlockDiscreteStateSpace ||
			kind == BlockMux || kind == BlockDemux ||
			kind == BlockSelector || kind == BlockPermutation {
			continue
		}
		block := Block{Kind: kind, Parameters: defaultParameters(kind)}
		for port := 0; port < block.InputPortCount(); port++ {
			schema, ok := block.InputPort(port)
			if !ok || schema.Width != 1 || !reflect.DeepEqual(schema.Channels, []string{"value"}) {
				t.Fatalf("%s input port %d = %#v", kind, port, schema)
			}
		}
		for port := 0; port < block.OutputPortCount(); port++ {
			schema, ok := block.OutputPort(port)
			if !ok || schema.Width != 1 || !reflect.DeepEqual(schema.Channels, []string{"value"}) {
				t.Fatalf("%s output port %d = %#v", kind, port, schema)
			}
		}
	}
}

func TestMatrixGainDerivesNamedPortWidthsFromValidatedParameters(t *testing.T) {
	block := Block{Kind: BlockMatrixGain, Parameters: defaultParameters(BlockMatrixGain)}
	input, ok := block.InputPort(0)
	if !ok || input.Width != 2 || !reflect.DeepEqual(input.Channels, []string{"u1", "u2"}) {
		t.Fatalf("input port = %#v", input)
	}
	output, ok := block.OutputPort(0)
	if !ok || output.Width != 2 || !reflect.DeepEqual(output.Channels, []string{"y1", "y2"}) {
		t.Fatalf("output port = %#v", output)
	}
	input.Channels[0] = "changed"
	again, _ := block.InputPort(0)
	if again.Channels[0] != "u1" {
		t.Fatal("port schema exposed mutable channel-name storage")
	}
}

func TestConnectRejectsScalarVectorMismatchBeforePersistence(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceID, err := service.AddBlock(ctx, snapshot.Flow.ID, BlockConstant, Point{X: 1000, Y: 200})
	if err != nil {
		t.Fatal(err)
	}
	_, matrixID, err := service.AddBlock(ctx, snapshot.Flow.ID, BlockMatrixGain, Point{X: 1200, Y: 200})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Connect(ctx, snapshot.Flow.ID, Wire{SourceID: sourceID, TargetID: matrixID})
	if err == nil || !strings.Contains(err.Error(), "(1 channels)") ||
		!strings.Contains(err.Error(), "(2 channels)") {
		t.Fatalf("error = %v, want scalar/vector width mismatch", err)
	}
	var count int
	if queryErr := service.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM connections WHERE source_id = ? AND target_id = ?",
		sourceID, matrixID,
	).Scan(&count); queryErr != nil {
		t.Fatal(queryErr)
	}
	if count != 0 {
		t.Fatalf("stored %d incompatible connections", count)
	}
}

func TestVectorFanoutAndChannelRenamePreserveConnections(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for index := range 3 {
		_, id, err := service.AddBlock(
			ctx, snapshot.Flow.ID, BlockMatrixGain,
			Point{X: 1000 + 200*index, Y: 500},
		)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, target := range ids[1:] {
		if _, err := service.Connect(ctx, snapshot.Flow.ID, Wire{
			SourceID: ids[0], TargetID: target,
		}); err != nil {
			t.Fatal(err)
		}
	}

	update := matrixGainUpdate(t, "Renamed source", "1, 0\n0, 1", "u1, u2", "product, recycle")
	if _, err := service.UpdateBlock(ctx, ids[0], update); err != nil {
		t.Fatalf("same-width channel rename failed: %v", err)
	}
	current, err := service.Snapshot(ctx, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Connections) < 2 {
		t.Fatalf("fanout connections = %d, want at least 2", len(current.Connections))
	}

	wider := matrixGainUpdate(
		t, "Wider source", "1, 0\n0, 1\n1, 1", "u1, u2", "a, b, c",
	)
	if _, err := service.UpdateBlock(ctx, ids[0], wider); err == nil ||
		!strings.Contains(err.Error(), "(3 channels)") ||
		!strings.Contains(err.Error(), "(2 channels)") {
		t.Fatalf("width-changing edit error = %v", err)
	}
}

func matrixGainUpdate(
	t *testing.T,
	name, matrix, inputs, outputs string,
) BlockUpdate {
	t.Helper()
	return BlockUpdate{
		Name: name,
		Parameters: map[string]string{
			"d":            matrix,
			"input_names":  inputs,
			"output_names": outputs,
		},
	}
}

func TestNamedMIMOFeedbackCompilesAndSimulatesByPortSchema(t *testing.T) {
	sourceParameters := defaultParameters(BlockVectorConstant)
	sourceValues, _ := NewVectorValue([]float64{1, 2})
	sourceParameters.Vector = &sourceValues

	sumParameters := defaultParameters(BlockVectorSum)
	sumInputs, _ := NewChannelNames([]string{"setpoint", "feedback"})
	sumOutputs, _ := NewChannelNames([]string{"error a", "error b"})
	sumParameters.InputNames = &sumInputs
	sumParameters.OutputNames = &sumOutputs

	plantParameters := defaultParameters(BlockMatrixGain)
	plantMatrix, _ := NewMatrixValue(2, 2, []float64{0.5, 0, 0, 0.5})
	plantInputs, _ := NewChannelNames([]string{"error a", "error b"})
	plantOutputs, _ := NewChannelNames([]string{"product", "recycle"})
	plantParameters.D = &plantMatrix
	plantParameters.InputNames = &plantInputs
	plantParameters.OutputNames = &plantOutputs

	scopeParameters := defaultParameters(BlockVectorScope)
	scopeInputs, _ := NewChannelNames([]string{"measured product", "measured recycle"})
	scopeParameters.InputNames = &scopeInputs

	blocks := []Block{
		{ID: 1, Kind: BlockVectorConstant, Name: "Setpoint", Parameters: sourceParameters},
		{ID: 2, Kind: BlockVectorSum, Name: "Error", Parameters: sumParameters},
		{ID: 3, Kind: BlockMatrixGain, Name: "Plant", Parameters: plantParameters},
		{ID: 4, Kind: BlockVectorScope, Name: "Output", Parameters: scopeParameters},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2, TargetPort: 0},
		{ID: 2, SourceID: 2, TargetID: 3},
		{ID: 3, SourceID: 3, TargetID: 2, TargetPort: 1},
		{ID: 4, SourceID: 3, TargetID: 4},
	}
	model, err := compileModel(blocks, connections)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := model.dimensions(), (compiledModelDimensions{Inputs: 2, Outputs: 2}); got != want {
		t.Fatalf("MIMO dimensions = %#v, want %#v", got, want)
	}
	channels := model.outputChannels()
	if got := []string{channels[0].ChannelName, channels[1].ChannelName}; !reflect.DeepEqual(
		got, []string{"measured product", "measured recycle"},
	) {
		t.Fatalf("compiled output channel names = %v", got)
	}

	run, err := model.run(SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Series) != 2 {
		t.Fatalf("MIMO series = %d, want 2", len(run.Series))
	}
	for sample := range run.Times {
		if diff := math.Abs(run.Series[0].Values[sample] - 1.0/3); diff > 1e-12 {
			t.Fatalf("channel 0 sample %d diff = %g", sample, diff)
		}
		if diff := math.Abs(run.Series[1].Values[sample] - 2.0/3); diff > 1e-12 {
			t.Fatalf("channel 1 sample %d diff = %g", sample, diff)
		}
	}
}

func TestLegacyPortMigrationDerivesScalarWidthOne(t *testing.T) {
	path := openLegacyPortsDatabase(t)
	service := openTestStudio(t, path)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range snapshot.Blocks {
		for port := 0; port < block.InputPortCount(); port++ {
			schema, _ := block.InputPort(port)
			if schema.Width != 1 {
				t.Fatalf("legacy %s input port %d width = %d", block.Name, port, schema.Width)
			}
		}
		for port := 0; port < block.OutputPortCount(); port++ {
			schema, _ := block.OutputPort(port)
			if schema.Width != 1 {
				t.Fatalf("legacy %s output port %d width = %d", block.Name, port, schema.Width)
			}
		}
	}
}
