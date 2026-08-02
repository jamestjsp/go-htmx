package studio

import (
	"errors"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
)

func TestAlgebraicLoopMessageFallsBackWithoutTypedMetadata(t *testing.T) {
	if got := algebraicLoopMessage(controlsys.ErrAlgebraicLoop, nil, nil); got != algebraicLoopFallback {
		t.Fatalf("message = %q, want %q", got, algebraicLoopFallback)
	}
}

func TestAlgebraicLoopMessageNamesOnlyCriticalMIMOChannels(t *testing.T) {
	signals := []compiledSignal{
		{Name: "stable.input", BlockID: 7, Port: 0, Channel: 0, ChannelName: "stable", Width: 3, Role: compiledBlockInput},
		{Name: "critical1.input", BlockID: 7, Port: 0, Channel: 1, ChannelName: "pressure", Width: 3, Role: compiledBlockInput},
		{Name: "critical2.input", BlockID: 7, Port: 0, Channel: 2, ChannelName: "temperature", Width: 3, Role: compiledBlockInput},
		{Name: "critical1.output", BlockID: 7, Port: 0, Channel: 1, ChannelName: "recycle", Width: 3, Role: compiledBlockOutput},
		{Name: "critical2.output", BlockID: 7, Port: 0, Channel: 2, ChannelName: "product", Width: 3, Role: compiledBlockOutput},
	}
	message := algebraicLoopMessage(
		&controlsys.AlgebraicLoopError{
			Signals: []string{
				"critical1.input", "critical2.input",
				"critical1.output", "critical2.output",
			},
			Condition: 5e15,
		},
		signals,
		map[int64]Block{7: {ID: 7, Name: "Recycle mixer"}},
	)

	for _, expected := range []string{
		`"Recycle mixer" input port 1 channel "pressure"`,
		`"Recycle mixer" input port 1 channel "temperature"`,
		`"Recycle mixer" output port 1 channel "recycle"`,
		`"Recycle mixer" output port 1 channel "product"`,
		"condition number 5e+15",
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("message does not contain %q: %s", expected, message)
		}
	}
	if strings.Contains(message, "stable") {
		t.Fatalf("message implicated a stable channel: %s", message)
	}
}

func TestCompileTranslatesNearSingularMIMOLoop(t *testing.T) {
	inputs, _ := NewChannelNames([]string{"stable", "pressure", "temperature"})
	outputs, _ := NewChannelNames([]string{"bypass", "recycle", "product"})
	gain, _ := NewMatrixValue(3, 3, []float64{
		0, 0, 0,
		0, 0, -1,
		0, -1, -2e-16,
	})

	_, err := compileModel(
		[]Block{
			{ID: 1, Kind: BlockSource, Name: "Clock"},
			{ID: 2, Kind: BlockScope, Name: "Clock trend"},
			{ID: 7, Kind: BlockMatrixGain, Name: "Recycle mixer", Parameters: Parameters{
				D: &gain, InputNames: &inputs, OutputNames: &outputs,
			}},
			{ID: 8, Kind: BlockVectorScope, Name: "Process trend", Parameters: Parameters{
				InputNames: &outputs,
			}},
		},
		[]Connection{
			{SourceID: 1, TargetID: 2},
			{SourceID: 7, TargetID: 7},
			{SourceID: 7, TargetID: 8},
		},
	)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	for _, expected := range []string{
		`"Recycle mixer" input port 1 channel "pressure"`,
		`"Recycle mixer" input port 1 channel "temperature"`,
		`"Recycle mixer" output port 1 channel "recycle"`,
		`"Recycle mixer" output port 1 channel "product"`,
		"condition number",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error does not contain %q: %v", expected, err)
		}
	}
	if strings.Contains(err.Error(), "stable") || strings.Contains(err.Error(), "block_") {
		t.Fatalf("error contains an irrelevant or internal signal: %v", err)
	}
}
