package studio

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

const r2026aVectorRoutingFixturePath = "testdata/simulink/r2026a/sum_and_direct_vectors.json"

type vectorRoutingCompatibilityFixture struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Release       string `json:"release"`
	Mappings      []struct {
		ProcessLabBlock       BlockKind `json:"processLabBlock"`
		MathWorksBlock        string    `json:"mathWorksBlock"`
		Compatibility         string    `json:"compatibility"`
		SupportedSubset       string    `json:"supportedSubset"`
		IntentionalDeviations []string  `json:"intentionalDeviations"`
	} `json:"mappings"`
	Cases []struct {
		ID        string `json:"id"`
		Reference struct {
			Title    string `json:"title"`
			URL      string `json:"url"`
			Section  string `json:"section"`
			Accessed string `json:"accessed"`
		} `json:"reference"`
		Oracle struct {
			Kind        string `json:"kind"`
			Description string `json:"description"`
		} `json:"oracle"`
	} `json:"cases"`
	Sum struct {
		DefaultSigns  string      `json:"defaultSigns"`
		ScalarInputs  []float64   `json:"scalarInputs"`
		ScalarSigns   string      `json:"scalarSigns"`
		VectorInputs  [][]float64 `json:"vectorInputs"`
		VectorSigns   string      `json:"vectorSigns"`
		CollapseInput []float64   `json:"collapseInput"`
	} `json:"sum"`
	UnitDelay struct {
		Input            []float64 `json:"input"`
		InitialCondition []float64 `json:"initialCondition"`
		SampleTime       float64   `json:"sampleTime"`
		Duration         float64   `json:"duration"`
	} `json:"unitDelay"`
}

func loadR2026aVectorRoutingFixture(t *testing.T) vectorRoutingCompatibilityFixture {
	t.Helper()
	encoded, err := os.ReadFile(r2026aVectorRoutingFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture vectorRoutingCompatibilityFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestR2026aVectorRoutingFixtureCarriesTraceableProvenance(t *testing.T) {
	fixture := loadR2026aVectorRoutingFixture(t)
	if fixture.SchemaVersion != 1 || fixture.ID == "" || fixture.Release != "R2026a" {
		t.Fatalf("fixture identity = version %d id %q release %q",
			fixture.SchemaVersion, fixture.ID, fixture.Release)
	}
	if len(fixture.Mappings) != 10 || len(fixture.Cases) != 3 {
		t.Fatalf("fixture mappings/cases = %d/%d, want 10/3",
			len(fixture.Mappings), len(fixture.Cases))
	}
	compatibilityKinds := map[string]bool{
		"direct-counterpart":         true,
		"process-lab-specialization": true,
	}
	for _, mapping := range fixture.Mappings {
		if !mapping.ProcessLabBlock.Valid() || mapping.MathWorksBlock == "" ||
			!compatibilityKinds[mapping.Compatibility] ||
			mapping.SupportedSubset == "" || len(mapping.IntentionalDeviations) == 0 {
			t.Fatalf("incomplete mapping = %#v", mapping)
		}
	}
	for _, compatibilityCase := range fixture.Cases {
		if compatibilityCase.ID == "" || compatibilityCase.Reference.Title == "" ||
			!strings.HasPrefix(compatibilityCase.Reference.URL, "https://www.mathworks.com/help/") ||
			compatibilityCase.Reference.Section == "" || compatibilityCase.Reference.Accessed == "" {
			t.Fatalf("incomplete compatibility case = %#v", compatibilityCase)
		}
		if compatibilityCase.Oracle.Kind != "mathworks-formula-analytic" ||
			compatibilityCase.Oracle.Description == "" ||
			!strings.Contains(
				strings.ToLower(compatibilityCase.Oracle.Description),
				"not matlab or simulink output",
			) {
			t.Fatalf("case oracle provenance = %#v", compatibilityCase.Oracle)
		}
	}
}

func TestR2026aSumDefaultsAndNumericInputCountThroughStudio(t *testing.T) {
	fixture := loadR2026aVectorRoutingFixture(t)
	studio, ctx, flowID := newCompatibilityFlow(t, fixture.ID+"-sum-default")
	snapshot, sumID, err := studio.AddBlock(
		ctx, flowID, BlockSum, Point{X: 400, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	sum := findBlock(t, snapshot.Blocks, sumID)
	if sum.Parameters.Signs != fixture.Sum.DefaultSigns ||
		normalizedDirectSignalWidth(sum.Parameters) != 1 ||
		sum.InputPortCount() != 1 {
		t.Fatalf("new Sum = %#v with %d ports", sum.Parameters, sum.InputPortCount())
	}
	snapshot, err = studio.UpdateBlock(ctx, sumID, BlockUpdate{
		Name: "Three-input adder",
		Parameters: map[string]string{
			"signs": "3", "signal_width": "1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum = findBlock(t, snapshot.Blocks, sumID)
	if sum.Parameters.Signs != "+++" || sum.InputPortCount() != 3 {
		t.Fatalf("numeric input count = %#v with %d ports",
			sum.Parameters, sum.InputPortCount())
	}
}

func TestR2026aScalarSumRunsThroughPublicStudio(t *testing.T) {
	fixture := loadR2026aVectorRoutingFixture(t)
	studio, ctx, flowID := newCompatibilityFlow(t, fixture.ID+"-scalar-sum")
	sourceIDs := make([]int64, len(fixture.Sum.ScalarInputs))
	for index, value := range fixture.Sum.ScalarInputs {
		_, sourceIDs[index] = addScalarConstant(
			t, studio, ctx, flowID, value, 100, 100+index*140,
		)
	}
	_, sumID, err := studio.AddBlock(ctx, flowID, BlockSum, Point{X: 400, Y: 220})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 700, Y: 220})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sumID, BlockUpdate{
		Name: "Scalar sum",
		Parameters: map[string]string{
			"signs": fixture.Sum.ScalarSigns, "signal_width": "1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	for port, sourceID := range sourceIDs {
		if _, err := studio.Connect(ctx, flowID, Wire{
			SourceID: sourceID, TargetID: sumID, TargetPort: port,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := studio.Connect(ctx, flowID, Wire{
		SourceID: sumID, TargetID: scopeID,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := studio.Run(ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	want := fixture.Sum.ScalarInputs[0] -
		fixture.Sum.ScalarInputs[1] +
		fixture.Sum.ScalarInputs[2]
	for sample, got := range run.LastRun.Series[0].Values {
		if got != want {
			t.Fatalf("scalar Sum sample %d = %g, want %g", sample, got, want)
		}
	}
}

func TestR2026aVectorSumAndReductionRunThroughPublicStudio(t *testing.T) {
	fixture := loadR2026aVectorRoutingFixture(t)
	t.Run("elementwise multi-input", func(t *testing.T) {
		studio, ctx, flowID := newCompatibilityFlow(t, fixture.ID+"-vector-sum")
		sourceIDs := make([]int64, len(fixture.Sum.VectorInputs))
		for index, values := range fixture.Sum.VectorInputs {
			sourceIDs[index] = addVectorConstant(
				t, studio, ctx, flowID, values, 100, 100+index*180,
			)
		}
		_, sumID, err := studio.AddBlock(ctx, flowID, BlockSum, Point{X: 400, Y: 180})
		if err != nil {
			t.Fatal(err)
		}
		_, scopeID, err := studio.AddBlock(
			ctx, flowID, BlockVectorScope, Point{X: 700, Y: 180},
		)
		if err != nil {
			t.Fatal(err)
		}
		width := len(fixture.Sum.VectorInputs[0])
		if _, err := studio.UpdateBlock(ctx, sumID, BlockUpdate{
			Name: "Direct vector sum",
			Parameters: map[string]string{
				"signs":        fixture.Sum.VectorSigns,
				"signal_width": strconv.Itoa(width),
			},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := studio.UpdateBlock(ctx, scopeID, BlockUpdate{
			Name: "Vector output",
			Parameters: map[string]string{
				"input_names": channelNamesForWidth(t, width).Text(),
			},
		}); err != nil {
			t.Fatal(err)
		}
		for port, sourceID := range sourceIDs {
			if _, err := studio.Connect(ctx, flowID, Wire{
				SourceID: sourceID, TargetID: sumID, TargetPort: port,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := studio.Connect(ctx, flowID, Wire{
			SourceID: sumID, TargetID: scopeID,
		}); err != nil {
			t.Fatal(err)
		}
		run, err := studio.Run(
			ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1},
		)
		if err != nil {
			t.Fatal(err)
		}
		for channel := range width {
			want := fixture.Sum.VectorInputs[0][channel] -
				fixture.Sum.VectorInputs[1][channel]
			for sample, got := range run.LastRun.Series[channel].Values {
				if got != want {
					t.Fatalf("channel %d sample %d = %g, want %g",
						channel, sample, got, want)
				}
			}
		}
	})

	t.Run("one-port all-elements reduction", func(t *testing.T) {
		studio, ctx, flowID := newCompatibilityFlow(t, fixture.ID+"-vector-collapse")
		sourceID := addVectorConstant(
			t, studio, ctx, flowID, fixture.Sum.CollapseInput, 100, 100,
		)
		_, sumID, err := studio.AddBlock(ctx, flowID, BlockSum, Point{X: 400, Y: 100})
		if err != nil {
			t.Fatal(err)
		}
		_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 700, Y: 100})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := studio.UpdateBlock(ctx, sumID, BlockUpdate{
			Name: "Sum of elements",
			Parameters: map[string]string{
				"signs":        "+",
				"signal_width": strconv.Itoa(len(fixture.Sum.CollapseInput)),
			},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := studio.Connect(ctx, flowID, Wire{
			SourceID: sourceID, TargetID: sumID,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := studio.Connect(ctx, flowID, Wire{
			SourceID: sumID, TargetID: scopeID,
		}); err != nil {
			t.Fatal(err)
		}
		run, err := studio.Run(
			ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1},
		)
		if err != nil {
			t.Fatal(err)
		}
		want := 0.0
		for _, value := range fixture.Sum.CollapseInput {
			want += value
		}
		for sample, got := range run.LastRun.Series[0].Values {
			if got != want {
				t.Fatalf("reduction sample %d = %g, want %g", sample, got, want)
			}
		}
	})
}

func TestR2026aVectorUnitDelayRunsAtInheritedRateThroughPublicStudio(t *testing.T) {
	fixture := loadR2026aVectorRoutingFixture(t)
	studio, ctx, flowID := newCompatibilityFlow(t, fixture.ID+"-vector-unit-delay")
	sourceID := addVectorConstant(
		t, studio, ctx, flowID, fixture.UnitDelay.Input, 100, 100,
	)
	_, delayID, err := studio.AddBlock(
		ctx, flowID, BlockUnitDelay, Point{X: 400, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(
		ctx, flowID, BlockVectorScope, Point{X: 700, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	width := len(fixture.UnitDelay.Input)
	initial, err := NewVectorValue(fixture.UnitDelay.InitialCondition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, delayID, BlockUpdate{
		Name: "Vector memory",
		Parameters: map[string]string{
			"initial_condition": initial.Text(),
			"signal_width":      strconv.Itoa(width),
			"sample_time_mode":  string(sampleTimeInherited),
			"sample_time":       formatFloat(fixture.UnitDelay.SampleTime),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, scopeID, BlockUpdate{
		Name: "Delayed vector",
		Parameters: map[string]string{
			"input_names": channelNamesForWidth(t, width).Text(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{
		SourceID: sourceID, TargetID: delayID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{
		SourceID: delayID, TargetID: scopeID,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := studio.Run(ctx, flowID, SimulationRequest{
		Duration:   fixture.UnitDelay.Duration,
		SampleTime: fixture.UnitDelay.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.LastRun == nil || len(run.LastRun.Series) != width {
		t.Fatalf("vector Unit Delay run = %#v", run.LastRun)
	}
	for channel := range width {
		if got := run.LastRun.Series[channel].Values[0]; got != fixture.UnitDelay.InitialCondition[channel] {
			t.Fatalf("channel %d initial output = %g, want %g",
				channel, got, fixture.UnitDelay.InitialCondition[channel])
		}
		for sample := 1; sample < len(run.LastRun.Times); sample++ {
			if got := run.LastRun.Series[channel].Values[sample]; got != fixture.UnitDelay.Input[channel] {
				t.Fatalf("channel %d sample %d = %g, want %g",
					channel, sample, got, fixture.UnitDelay.Input[channel])
			}
		}
	}
}

func TestLegacyDirectVectorParametersRemainScalar(t *testing.T) {
	sum, err := decodeParameters(
		BlockSum,
		`{"parameterSchemaVersion":1,"signs":"+-"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	delay, err := decodeParameters(
		BlockUnitDelay,
		`{"parameterSchemaVersion":1,"initialCondition":4,"sampleTime":0.1,"sampleTimeMode":"explicit"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if normalizedDirectSignalWidth(sum) != 1 ||
		normalizedDirectSignalWidth(delay) != 1 ||
		!equalFloatValues(unitDelayInitialState(delay), []float64{4}) {
		t.Fatalf("legacy direct-vector defaults = Sum %#v Unit Delay %#v",
			sum, delay)
	}
}

func TestAuthoredDirectVectorParametersRoundTrip(t *testing.T) {
	initial, err := NewVectorValue([]float64{9, -2, 4})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		kind       BlockKind
		parameters Parameters
		check      func(Parameters) bool
	}{
		{
			kind: BlockSum,
			parameters: Parameters{
				Signs: "+-", SignalWidth: 3,
			},
			check: func(parameters Parameters) bool {
				return parameters.Signs == "+-" &&
					normalizedDirectSignalWidth(parameters) == 3
			},
		},
		{
			kind: BlockUnitDelay,
			parameters: Parameters{
				SignalWidth: 3, InitialState: &initial,
				SampleTimeMode: string(sampleTimeInherited),
			},
			check: func(parameters Parameters) bool {
				return normalizedDirectSignalWidth(parameters) == 3 &&
					equalFloatValues(
						unitDelayInitialState(parameters),
						[]float64{9, -2, 4},
					) &&
					normalizedSampleTimeMode(parameters) == sampleTimeInherited
			},
		},
	}
	for _, test := range tests {
		encoded, err := encodeParameters(test.parameters)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeParameters(test.kind, encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !test.check(decoded) {
			t.Fatalf("%s round trip = %#v from %s", test.kind, decoded, encoded)
		}
	}
}

func addScalarConstant(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	flowID int64,
	value float64,
	x, y int,
) (Snapshot, int64) {
	t.Helper()
	snapshot, blockID, err := studio.AddBlock(
		ctx, flowID, BlockConstant, Point{X: x, Y: y},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = studio.UpdateBlock(ctx, blockID, BlockUpdate{
		Name:       "Scalar input",
		Parameters: map[string]string{"value": formatFloat(value)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, blockID
}

func addVectorConstant(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	flowID int64,
	values []float64,
	x, y int,
) int64 {
	t.Helper()
	_, blockID, err := studio.AddBlock(
		ctx, flowID, BlockVectorConstant, Point{X: x, Y: y},
	)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := NewVectorValue(values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, blockID, BlockUpdate{
		Name: "Vector input",
		Parameters: map[string]string{
			"vector":       vector.Text(),
			"output_names": channelNamesForWidth(t, len(values)).Text(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	return blockID
}

func channelNamesForWidth(t *testing.T, width int) ChannelNames {
	t.Helper()
	names := make([]string, width)
	for channel := range width {
		names[channel] = defaultChannelName(channel, width)
	}
	result, err := NewChannelNames(names)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDirectVectorValidationRejectsIncompatibleAuthoredShapes(t *testing.T) {
	if err := updateWithOverride(t, BlockSum, "signal_width", "0"); err == nil ||
		!strings.Contains(err.Error(), "signal width must be between") {
		t.Fatalf("Sum zero width error = %v", err)
	}
	if err := updateWithOverride(t, BlockSum, "signal_width", "1.5"); err == nil ||
		!strings.Contains(err.Error(), "whole number") {
		t.Fatalf("Sum fractional width error = %v", err)
	}
	parameters := defaultParameters(BlockUnitDelay)
	parameters.SignalWidth = 3
	initial, err := NewVectorValue([]float64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	parameters.InitialState = &initial
	if err := validateParameters(BlockUnitDelay, parameters); err == nil ||
		!strings.Contains(err.Error(), "2 values for signal width 3") {
		t.Fatalf("Unit Delay mismatched initial vector error = %v", err)
	}
	for _, value := range unitDelayInitialState(Parameters{
		SignalWidth: 3, InitialCondition: math.Pi,
	}) {
		if value != math.Pi {
			t.Fatalf("scalar initial-condition broadcast = %v", value)
		}
	}
}
