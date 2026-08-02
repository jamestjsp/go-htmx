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

const r2026aStateHistoryFixturePath = "testdata/simulink/r2026a/state_space_and_transport_history.json"

type stateHistoryCompatibilityFixture struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Release       string `json:"release"`
	Mappings      []struct {
		ProcessLabBlock       BlockKind `json:"processLabBlock"`
		MathWorksBlock        string    `json:"mathWorksBlock"`
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
		Defaults struct {
			A            [][]float64 `json:"a"`
			B            [][]float64 `json:"b"`
			C            [][]float64 `json:"c"`
			D            [][]float64 `json:"d"`
			InitialState []float64   `json:"initialState"`
		} `json:"defaults"`
		Scenario struct {
			A             [][]float64 `json:"a"`
			B             [][]float64 `json:"b"`
			C             [][]float64 `json:"c"`
			D             [][]float64 `json:"d"`
			InitialState  []float64   `json:"initialState"`
			Input         float64     `json:"input"`
			Delay         float64     `json:"delay"`
			InitialOutput float64     `json:"initialOutput"`
			SampleTime    float64     `json:"sampleTime"`
			Duration      float64     `json:"duration"`
		} `json:"scenario"`
	} `json:"cases"`
}

func loadR2026aStateHistoryFixture(t *testing.T) stateHistoryCompatibilityFixture {
	t.Helper()
	encoded, err := os.ReadFile(r2026aStateHistoryFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture stateHistoryCompatibilityFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestR2026aStateHistoryFixtureCarriesTraceableProvenance(t *testing.T) {
	fixture := loadR2026aStateHistoryFixture(t)
	if fixture.SchemaVersion != 1 || fixture.ID == "" || fixture.Release != "R2026a" {
		t.Fatalf("fixture identity = version %d id %q release %q",
			fixture.SchemaVersion, fixture.ID, fixture.Release)
	}
	if len(fixture.Mappings) != 3 || len(fixture.Cases) != 4 {
		t.Fatalf("fixture mappings/cases = %d/%d, want 3/4",
			len(fixture.Mappings), len(fixture.Cases))
	}
	for _, mapping := range fixture.Mappings {
		if !mapping.ProcessLabBlock.Valid() || mapping.MathWorksBlock == "" ||
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

func requireStateHistoryCase(
	t *testing.T,
	fixture stateHistoryCompatibilityFixture,
	id string,
) int {
	t.Helper()
	for index, compatibilityCase := range fixture.Cases {
		if compatibilityCase.ID == id {
			return index
		}
	}
	t.Fatalf("fixture does not trace compatibility case %q", id)
	return 0
}

func TestR2026aStateSpaceDirectDefaults(t *testing.T) {
	fixture := loadR2026aStateHistoryFixture(t)
	index := requireStateHistoryCase(t, fixture, "state-space-direct-defaults")
	defaults := fixture.Cases[index].Defaults
	studio, ctx, flowID := newCompatibilityFlow(t, "r2026a-state-space-defaults")
	for index, kind := range []BlockKind{BlockStateSpace, BlockDiscreteStateSpace} {
		snapshot, blockID, err := studio.AddBlock(
			ctx, flowID, kind, Point{X: 100 + index*300, Y: 100},
		)
		if err != nil {
			t.Fatal(err)
		}
		parameters := findBlock(t, snapshot.Blocks, blockID).Parameters
		assertMatrixValue(t, parameters.A, defaults.A)
		assertMatrixValue(t, parameters.B, defaults.B)
		assertMatrixValue(t, parameters.C, defaults.C)
		assertMatrixValue(t, parameters.D, defaults.D)
		if parameters.InitialState == nil ||
			!equalFloatValues(parameters.InitialState.Values(), defaults.InitialState) {
			t.Fatalf("%s initial state = %#v, want %v",
				kind, parameters.InitialState, defaults.InitialState)
		}
	}
}

func TestR2026aContinuousStateSpaceInitialVectorRunsThroughStudio(t *testing.T) {
	fixture := loadR2026aStateHistoryFixture(t)
	index := requireStateHistoryCase(t, fixture, "continuous-state-space-initial-vector")
	scenario := fixture.Cases[index].Scenario
	run := runStateSpaceInitialVectorCase(t, BlockStateSpace, scenario)

	for sample, currentTime := range run.Times {
		want := []float64{
			scenario.InitialState[0] * math.Exp(-currentTime),
			scenario.InitialState[1] * math.Exp(-2*currentTime),
		}
		for output := range want {
			if got := run.Series[output].Values[sample]; math.Abs(got-want[output]) > 1e-11 {
				t.Fatalf("continuous output %d at t=%g = %g, analytic oracle %g",
					output, currentTime, got, want[output])
			}
		}
	}
}

func TestR2026aDiscreteStateSpaceInitialVectorRunsThroughStudio(t *testing.T) {
	fixture := loadR2026aStateHistoryFixture(t)
	index := requireStateHistoryCase(t, fixture, "discrete-state-space-initial-vector")
	scenario := fixture.Cases[index].Scenario
	run := runStateSpaceInitialVectorCase(t, BlockDiscreteStateSpace, scenario)

	for sample := range run.Times {
		want := []float64{
			scenario.InitialState[0] * math.Pow(0.5, float64(sample)),
			scenario.InitialState[1] * math.Pow(0.25, float64(sample)),
		}
		for output := range want {
			if got := run.Series[output].Values[sample]; math.Abs(got-want[output]) > 1e-12 {
				t.Fatalf("discrete output %d at sample %d = %g, analytic oracle %g",
					output, sample, got, want[output])
			}
		}
	}
}

func TestR2026aStateSpaceInitialConditionValidation(t *testing.T) {
	for _, kind := range []BlockKind{BlockStateSpace, BlockDiscreteStateSpace} {
		parameters := defaultParameters(kind)
		matrix, _ := NewMatrixValue(2, 2, []float64{1, 0, 0, 1})
		parameters.A = &matrix
		parameters.B = &matrix
		parameters.C = &matrix
		parameters.D = &matrix
		inputs, _ := NewChannelNames([]string{"u1", "u2"})
		outputs, _ := NewChannelNames([]string{"y1", "y2"})
		states, _ := NewChannelNames([]string{"x1", "x2"})
		parameters.InputNames = &inputs
		parameters.OutputNames = &outputs
		parameters.StateNames = &states
		initial, _ := NewVectorValue([]float64{1, 2, 3})
		parameters.InitialState = &initial
		err := validateParameters(kind, parameters)
		if err == nil || !strings.Contains(err.Error(), "initial conditions") {
			t.Fatalf("%s mismatched initial state error = %v", kind, err)
		}

		scalar, _ := NewVectorValue([]float64{3})
		parameters.InitialState = &scalar
		if err := validateParameters(kind, parameters); err != nil {
			t.Fatalf("%s scalar initial state: %v", kind, err)
		}
		if got := stateSpaceInitialState(parameters); !equalFloatValues(got, []float64{3, 3}) {
			t.Fatalf("%s broadcast initial state = %v, want [3 3]", kind, got)
		}
	}

	block := Block{Kind: BlockStateSpace, Parameters: defaultParameters(BlockStateSpace)}
	found := false
	for _, field := range block.EditorFields() {
		if field.Name == "initial_state" && field.Value == "0" {
			found = true
		}
	}
	if !found {
		t.Fatal("State-Space editor does not expose the zero initial state")
	}
}

func TestR2026aStateAndHistoryParametersPersistWithoutChangingLegacyDefaults(t *testing.T) {
	initial, _ := NewVectorValue([]float64{2, -1})
	stateSpace := defaultParameters(BlockStateSpace)
	stateSpace.InitialState = &initial
	encoded, err := encodeParameters(stateSpace)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeParameters(BlockStateSpace, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.InitialState == nil ||
		!equalFloatValues(decoded.InitialState.Values(), initial.Values()) {
		t.Fatalf("decoded initial state = %#v, want %v", decoded.InitialState, initial.Values())
	}

	delay := defaultParameters(BlockDelay)
	delay.InitialOutput = -2
	encoded, err = encodeParameters(delay)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decodeParameters(BlockDelay, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.InitialOutput != -2 {
		t.Fatalf("decoded initial output = %g, want -2", decoded.InitialOutput)
	}

	for _, kind := range []BlockKind{BlockStateSpace, BlockDiscreteStateSpace} {
		legacy, err := decodeParameters(kind, `{"sampleTime":0.25}`)
		if err != nil {
			t.Fatal(err)
		}
		rows, columns := legacy.A.Dims()
		if rows != 2 || columns != 2 ||
			legacy.InputNames.Len() != 2 || legacy.OutputNames.Len() != 2 ||
			legacy.StateNames.Len() != 2 || legacy.InitialState != nil {
			t.Fatalf("%s partial legacy defaults changed = %#v", kind, legacy)
		}
	}
}

func TestR2026aTransportDelayInitialOutputRunsThroughStudio(t *testing.T) {
	fixture := loadR2026aStateHistoryFixture(t)
	index := requireStateHistoryCase(t, fixture, "transport-delay-initial-output")
	scenario := fixture.Cases[index].Scenario
	studio, ctx, flowID := newCompatibilityFlow(t, "r2026a-delay-history")
	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, delayID, err := studio.AddBlock(ctx, flowID, BlockDelay, Point{X: 350, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 600, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sourceID, BlockUpdate{
		Name: "Input", Parameters: map[string]string{
			"value": strconv.FormatFloat(scenario.Input, 'g', -1, 64),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, delayID, BlockUpdate{
		Name: "Transport", Parameters: map[string]string{
			"delay":            strconv.FormatFloat(scenario.Delay, 'g', -1, 64),
			"delay_mode":       delayModeExact,
			"initial_output":   strconv.FormatFloat(scenario.InitialOutput, 'g', -1, 64),
			"approximation":    "3",
			"sample_time_mode": string(sampleTimeExplicit),
			"sample_time":      "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	connectCompatibilityChain(t, studio, ctx, flowID, sourceID, delayID, scopeID)

	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{
		Duration: scenario.Duration, SampleTime: scenario.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun.Fidelity.Driver != "delay-aware-simulate" {
		t.Fatalf("driver = %q, want delay-aware-simulate", snapshot.LastRun.Fidelity.Driver)
	}
	delaySamples := int(math.Round(scenario.Delay / scenario.SampleTime))
	for sample, got := range snapshot.LastRun.Series[0].Values {
		want := scenario.Input
		if sample < delaySamples {
			want = scenario.InitialOutput
		}
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("sample %d = %g, initial-history oracle %g", sample, got, want)
		}
	}
}

func TestTransportDelayApproximationRejectsInitialOutput(t *testing.T) {
	parameters := defaultParameters(BlockDelay)
	parameters.DelayMode = delayModePade
	parameters.InitialOutput = 1
	err := validateParameters(BlockDelay, parameters)
	if err == nil || !strings.Contains(err.Error(), "only by exact transport delay") {
		t.Fatalf("Padé initial output error = %v", err)
	}
}

func TestTransportDelayInitialOutputDoesNotChangeFrequencyModel(t *testing.T) {
	studio, ctx, flowID := newCompatibilityFlow(t, "delay-history-frequency")
	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, delayID, err := studio.AddBlock(ctx, flowID, BlockDelay, Point{X: 350, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, delayID, BlockUpdate{
		Name: "History", Parameters: map[string]string{
			"delay": "0.2", "initial_output": "2", "delay_mode": delayModeExact,
			"approximation": "3", "sample_time_mode": string(sampleTimeExplicit),
			"sample_time": "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{
		SourceID: sourceID, TargetID: delayID,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := studio.AnalyzeFrequency(ctx, flowID, FrequencyAnalysisRequest{
		Inputs:   []ChannelRef{{BlockID: sourceID}},
		Outputs:  []ChannelRef{{BlockID: delayID}},
		Omega:    []float64{1, 2},
		BaseStep: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := *result.Bode[0].MagnitudeDB[0]; math.Abs(got) > 1e-12 {
		t.Fatalf("magnitude = %g dB, want 0 dB", got)
	}
	wantPhase := -0.2 * 180 / math.Pi
	if got := *result.Bode[0].PhaseDegrees[0]; math.Abs(got-wantPhase) > 1e-10 {
		t.Fatalf("phase = %g degrees, delay oracle %g", got, wantPhase)
	}
}

func TestTransportDelayInitialOutputParticipatesInFeedback(t *testing.T) {
	studio, ctx, flowID := newCompatibilityFlow(t, "delay-history-feedback")
	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, sumID, err := studio.AddBlock(ctx, flowID, BlockSum, Point{X: 300, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, delayID, err := studio.AddBlock(ctx, flowID, BlockDelay, Point{X: 500, Y: 250})
	if err != nil {
		t.Fatal(err)
	}
	_, plantID, err := studio.AddBlock(ctx, flowID, BlockTransfer, Point{X: 500, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 700, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sourceID, BlockUpdate{
		Name: "Zero", Parameters: map[string]string{"value": "0"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sumID, BlockUpdate{
		Name: "Feedback sum", Parameters: map[string]string{"signs": "+-"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, delayID, BlockUpdate{
		Name: "History", Parameters: map[string]string{
			"delay": "0.2", "initial_output": "2", "delay_mode": delayModeExact,
			"approximation": "3", "sample_time_mode": string(sampleTimeExplicit),
			"sample_time": "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, wire := range []Wire{
		{SourceID: sourceID, TargetID: sumID, TargetPort: 0},
		{SourceID: sumID, TargetID: plantID},
		{SourceID: plantID, TargetID: delayID},
		{SourceID: delayID, TargetID: sumID, TargetPort: 1},
		{SourceID: plantID, TargetID: scopeID},
	} {
		if _, err := studio.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	want := []float64{0, -2 * (1 - math.Exp(-0.1))}
	for sample, expected := range want {
		if got := snapshot.LastRun.Series[0].Values[sample]; math.Abs(got-expected) > 1e-12 {
			t.Fatalf("feedback sample %d = %g, history oracle %g", sample, got, expected)
		}
	}
}

func TestZeroTransportDelayInitialOutputKeepsNoDelayFastPath(t *testing.T) {
	studio, ctx, flowID := newCompatibilityFlow(t, "zero-delay-history")
	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, delayID, err := studio.AddBlock(ctx, flowID, BlockDelay, Point{X: 350, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 600, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sourceID, BlockUpdate{
		Name: "Input", Parameters: map[string]string{"value": "3"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, delayID, BlockUpdate{
		Name: "Transport", Parameters: map[string]string{
			"delay": "0", "delay_mode": delayModeExact, "initial_output": "-2",
			"approximation": "3", "sample_time_mode": string(sampleTimeExplicit),
			"sample_time": "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	connectCompatibilityChain(t, studio, ctx, flowID, sourceID, delayID, scopeID)

	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun.Fidelity.Driver != "batch-lsim" {
		t.Fatalf("driver = %q, want batch-lsim", snapshot.LastRun.Fidelity.Driver)
	}
	for sample, got := range snapshot.LastRun.Series[0].Values {
		if got != 3 {
			t.Fatalf("sample %d = %g, want immediate input 3", sample, got)
		}
	}
}

func runStateSpaceInitialVectorCase(
	t *testing.T,
	kind BlockKind,
	scenario struct {
		A             [][]float64 `json:"a"`
		B             [][]float64 `json:"b"`
		C             [][]float64 `json:"c"`
		D             [][]float64 `json:"d"`
		InitialState  []float64   `json:"initialState"`
		Input         float64     `json:"input"`
		Delay         float64     `json:"delay"`
		InitialOutput float64     `json:"initialOutput"`
		SampleTime    float64     `json:"sampleTime"`
		Duration      float64     `json:"duration"`
	},
) *Simulation {
	t.Helper()
	studio, ctx, flowID := newCompatibilityFlow(t, "r2026a-state-space-initial")
	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockVectorConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, stateSpaceID, err := studio.AddBlock(ctx, flowID, kind, Point{X: 350, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockVectorScope, Point{X: 650, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sourceID, BlockUpdate{
		Name: "Zero input", Parameters: map[string]string{
			"vector": "0", "output_names": "u",
		},
	}); err != nil {
		t.Fatal(err)
	}
	parameters := map[string]string{
		"a":             matrixText(scenario.A),
		"b":             matrixText(scenario.B),
		"c":             matrixText(scenario.C),
		"d":             matrixText(scenario.D),
		"initial_state": numericRowText(scenario.InitialState),
		"input_names":   "u",
		"output_names":  "y1, y2",
		"state_names":   "x1, x2",
	}
	if kind == BlockStateSpace {
		parameters["time_domain"] = modelDomainContinuous
		parameters["sample_time"] = strconv.FormatFloat(scenario.SampleTime, 'g', -1, 64)
	} else {
		parameters["sample_time_mode"] = string(sampleTimeExplicit)
		parameters["sample_time"] = strconv.FormatFloat(scenario.SampleTime, 'g', -1, 64)
	}
	if _, err := studio.UpdateBlock(ctx, stateSpaceID, BlockUpdate{
		Name: "Plant", Parameters: parameters,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, scopeID, BlockUpdate{
		Name: "Output", Parameters: map[string]string{"input_names": "y1, y2"},
	}); err != nil {
		t.Fatal(err)
	}
	connectCompatibilityChain(t, studio, ctx, flowID, sourceID, stateSpaceID, scopeID)
	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{
		Duration: scenario.Duration, SampleTime: scenario.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.LastRun
}

func connectCompatibilityChain(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	flowID int64,
	blockIDs ...int64,
) {
	t.Helper()
	for index := 1; index < len(blockIDs); index++ {
		if _, err := studio.Connect(ctx, flowID, Wire{
			SourceID: blockIDs[index-1], TargetID: blockIDs[index],
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func matrixText(values [][]float64) string {
	rows := make([]string, len(values))
	for row, entries := range values {
		rows[row] = numericRowText(entries)
	}
	return strings.Join(rows, "\n")
}

func assertMatrixValue(t *testing.T, got *MatrixValue, want [][]float64) {
	t.Helper()
	if got == nil {
		t.Fatal("matrix is nil")
	}
	rows, columns := got.Dims()
	if rows != len(want) || columns != len(want[0]) {
		t.Fatalf("matrix dimensions = %dx%d, want %dx%d",
			rows, columns, len(want), len(want[0]))
	}
	values := got.Values()
	for row := range want {
		for column := range want[row] {
			if value := values[row*columns+column]; value != want[row][column] {
				t.Fatalf("matrix[%d,%d] = %g, want %g",
					row, column, value, want[row][column])
			}
		}
	}
}

func equalFloatValues(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
