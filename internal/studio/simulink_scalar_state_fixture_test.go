package studio

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const r2026aScalarStateFixturePath = "testdata/simulink/r2026a/scalar_initial_conditions.json"

type scalarStateCompatibilityFixture struct {
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
			StepTime     float64 `json:"stepTime"`
			InitialValue float64 `json:"initialValue"`
			FinalValue   float64 `json:"finalValue"`
		} `json:"defaults"`
		Scenario struct {
			Input            float64 `json:"input"`
			InitialCondition float64 `json:"initialCondition"`
			SampleTime       float64 `json:"sampleTime"`
			Duration         float64 `json:"duration"`
		} `json:"scenario"`
	} `json:"cases"`
}

func loadR2026aScalarStateFixture(t *testing.T) scalarStateCompatibilityFixture {
	t.Helper()
	encoded, err := os.ReadFile(r2026aScalarStateFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture scalarStateCompatibilityFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestR2026aScalarStateFixtureCarriesTraceableProvenance(t *testing.T) {
	fixture := loadR2026aScalarStateFixture(t)
	if fixture.SchemaVersion != 1 || fixture.ID == "" || fixture.Release != "R2026a" {
		t.Fatalf("fixture identity = version %d id %q release %q",
			fixture.SchemaVersion, fixture.ID, fixture.Release)
	}
	if len(fixture.Mappings) != 3 {
		t.Fatalf("mapping count = %d, want 3", len(fixture.Mappings))
	}
	for _, mapping := range fixture.Mappings {
		if !mapping.ProcessLabBlock.Valid() || mapping.MathWorksBlock == "" ||
			mapping.SupportedSubset == "" || len(mapping.IntentionalDeviations) == 0 {
			t.Fatalf("incomplete block mapping = %#v", mapping)
		}
	}
	allowedOracles := map[string]bool{
		"mathworks-example-data":         true,
		"mathworks-formula-analytic":     true,
		"mathworks-semantics-controlsys": true,
	}
	if len(fixture.Cases) != 3 {
		t.Fatalf("case count = %d, want 3", len(fixture.Cases))
	}
	for _, compatibilityCase := range fixture.Cases {
		if compatibilityCase.ID == "" ||
			compatibilityCase.Reference.Title == "" ||
			!strings.HasPrefix(compatibilityCase.Reference.URL, "https://www.mathworks.com/help/") ||
			compatibilityCase.Reference.Section == "" ||
			compatibilityCase.Reference.Accessed == "" {
			t.Fatalf("incomplete compatibility case = %#v", compatibilityCase)
		}
		if !allowedOracles[compatibilityCase.Oracle.Kind] ||
			compatibilityCase.Oracle.Description == "" {
			t.Fatalf("case oracle provenance = %#v", compatibilityCase)
		}
		description := strings.ToLower(compatibilityCase.Oracle.Description)
		if strings.Contains(description, "matlab output") &&
			!strings.Contains(description, "not matlab output") {
			t.Fatalf("generated oracle is mislabeled as MATLAB output: %q", description)
		}
	}
}

func requireScalarStateCompatibilityCase(
	t *testing.T,
	fixture scalarStateCompatibilityFixture,
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

func TestR2026aStepDefaultsRunThroughStudio(t *testing.T) {
	fixture := loadR2026aScalarStateFixture(t)
	index := requireScalarStateCompatibilityCase(t, fixture, "step-defaults")
	compatibilityCase := fixture.Cases[index]
	studio, ctx, flowID := newCompatibilityFlow(t, "r2026a-step-default")

	snapshot, sourceID, err := studio.AddBlock(
		ctx, flowID, BlockSource, Point{X: 100, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	source := findBlock(t, snapshot.Blocks, sourceID)
	if source.Parameters.StepTime != compatibilityCase.Defaults.StepTime ||
		source.Parameters.InitialValue != compatibilityCase.Defaults.InitialValue ||
		source.Parameters.Amplitude != compatibilityCase.Defaults.FinalValue {
		t.Fatalf("Step defaults = %#v, fixture = %#v",
			source.Parameters, compatibilityCase.Defaults)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 500, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{
		SourceID: sourceID, TargetID: scopeID,
	}); err != nil {
		t.Fatal(err)
	}
	run, err := studio.Run(ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.25})
	if err != nil {
		t.Fatal(err)
	}
	for sample, currentTime := range run.LastRun.Times {
		want := compatibilityCase.Defaults.InitialValue
		if currentTime >= compatibilityCase.Defaults.StepTime {
			want = compatibilityCase.Defaults.FinalValue
		}
		if got := run.LastRun.Series[0].Values[sample]; got != want {
			t.Fatalf("Step at t=%g = %g, R2026a default oracle %g", currentTime, got, want)
		}
	}
}

func TestR2026aIntegratorInternalInitialConditionRunsThroughStudio(t *testing.T) {
	fixture := loadR2026aScalarStateFixture(t)
	index := requireScalarStateCompatibilityCase(
		t, fixture, "integrator-internal-initial-condition",
	)
	scenario := fixture.Cases[index].Scenario
	studio, ctx, flowID, sourceID, dynamicID, scopeID := newScalarStateFlow(
		t, "r2026a-integrator-initial", BlockIntegrator,
	)
	updateScalarConstant(t, studio, ctx, sourceID, scenario.Input)
	updateScalarInitialCondition(
		t, studio, ctx, dynamicID, "Integrator", scenario.InitialCondition, nil,
	)
	connectScalarStateFlow(t, studio, ctx, flowID, sourceID, dynamicID, scopeID)

	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{
		Duration: scenario.Duration, SampleTime: scenario.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	for sample, currentTime := range snapshot.LastRun.Times {
		want := scenario.InitialCondition + scenario.Input*currentTime
		if got := snapshot.LastRun.Series[0].Values[sample]; math.Abs(got-want) > 1e-12 {
			t.Fatalf("Integrator at t=%g = %g, R2026a initial-state oracle %g",
				currentTime, got, want)
		}
	}
}

func TestR2026aUnitDelayInitialConditionRunsThroughStudio(t *testing.T) {
	fixture := loadR2026aScalarStateFixture(t)
	index := requireScalarStateCompatibilityCase(
		t, fixture, "unit-delay-internal-initial-condition",
	)
	scenario := fixture.Cases[index].Scenario
	studio, ctx, flowID, sourceID, dynamicID, scopeID := newScalarStateFlow(
		t, "r2026a-unit-delay-initial", BlockUnitDelay,
	)
	updateScalarConstant(t, studio, ctx, sourceID, scenario.Input)
	updateScalarInitialCondition(
		t,
		studio,
		ctx,
		dynamicID,
		"Unit Delay",
		scenario.InitialCondition,
		map[string]string{
			"sample_time_mode": string(sampleTimeExplicit),
			"sample_time":      strconv.FormatFloat(scenario.SampleTime, 'g', -1, 64),
		},
	)
	connectScalarStateFlow(t, studio, ctx, flowID, sourceID, dynamicID, scopeID)

	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{
		Duration: scenario.Duration, SampleTime: scenario.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	for sample, got := range snapshot.LastRun.Series[0].Values {
		want := scenario.Input
		if sample == 0 {
			want = scenario.InitialCondition
		}
		if got != want {
			t.Fatalf("Unit Delay sample %d = %g, R2026a initial-output oracle %g",
				sample, got, want)
		}
	}
}

func TestR2026aScalarInitialConditionDefaultsAndValidation(t *testing.T) {
	for _, kind := range []BlockKind{BlockIntegrator, BlockUnitDelay} {
		parameters := defaultParameters(kind)
		if parameters.InitialCondition != 0 {
			t.Fatalf("%s initial condition = %g, want documented zero default",
				kind, parameters.InitialCondition)
		}
		block := Block{Kind: kind, Parameters: parameters}
		found := false
		for _, field := range block.EditorFields() {
			if field.Name != "initial_condition" {
				continue
			}
			found = true
			if field.Label != "Initial condition" || field.Value != "0" {
				t.Fatalf("%s initial-condition editor = %#v", kind, field)
			}
		}
		if !found {
			t.Fatalf("%s has no initial-condition editor field", kind)
		}
		for _, value := range []string{"NaN", "+Inf", "-Inf"} {
			err := updateWithOverride(t, kind, "initial_condition", value)
			if err == nil || !strings.Contains(err.Error(), "must be finite") {
				t.Fatalf("%s initial condition %q error = %v, want finite validation",
					kind, value, err)
			}
		}
	}
}

func TestCompiledInitialStateKeepsBlockRealizationOrder(t *testing.T) {
	fixture := loadR2026aScalarStateFixture(t)
	requireScalarStateCompatibilityCase(
		t, fixture, "integrator-internal-initial-condition",
	)
	studio, ctx, flowID := newCompatibilityFlow(t, "ordered-initial-state")
	_, sourceID, err := studio.AddBlock(
		ctx, flowID, BlockConstant, Point{X: 100, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, firstID, err := studio.AddBlock(
		ctx, flowID, BlockIntegrator, Point{X: 300, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, secondID, err := studio.AddBlock(
		ctx, flowID, BlockIntegrator, Point{X: 500, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(
		ctx, flowID, BlockScope, Point{X: 700, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	updateScalarConstant(t, studio, ctx, sourceID, 0)
	updateScalarInitialCondition(t, studio, ctx, firstID, "First", 2, nil)
	updateScalarInitialCondition(t, studio, ctx, secondID, "Second", 3, nil)
	for _, wire := range []Wire{
		{SourceID: sourceID, TargetID: firstID},
		{SourceID: firstID, TargetID: secondID},
		{SourceID: secondID, TargetID: scopeID},
	} {
		if _, err := studio.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := studio.Run(
		ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1},
	)
	if err != nil {
		t.Fatal(err)
	}
	for sample, currentTime := range snapshot.LastRun.Times {
		want := 3 + 2*currentTime
		if got := snapshot.LastRun.Series[0].Values[sample]; math.Abs(got-want) > 1e-12 {
			t.Fatalf("ordered initial state at t=%g = %g, want %g", currentTime, got, want)
		}
	}
}

func newScalarStateFlow(
	t *testing.T,
	name string,
	kind BlockKind,
) (*Studio, context.Context, int64, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	studio := openTestStudio(t, filepath.Join(t.TempDir(), name+".db"))
	seeded, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := studio.CreateFlow(ctx, seeded.Flow.ProjectID, name)
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, dynamicID, err := studio.AddBlock(ctx, flowID, kind, Point{X: 400, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 700, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	return studio, ctx, flowID, sourceID, dynamicID, scopeID
}

func updateScalarConstant(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	blockID int64,
	value float64,
) {
	t.Helper()
	if _, err := studio.UpdateBlock(ctx, blockID, BlockUpdate{
		Name: "Input",
		Parameters: map[string]string{
			"value": strconv.FormatFloat(value, 'g', -1, 64),
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func updateScalarInitialCondition(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	blockID int64,
	name string,
	initialCondition float64,
	extra map[string]string,
) {
	t.Helper()
	parameters := map[string]string{
		"initial_condition": strconv.FormatFloat(initialCondition, 'g', -1, 64),
	}
	for key, value := range extra {
		parameters[key] = value
	}
	if _, err := studio.UpdateBlock(ctx, blockID, BlockUpdate{
		Name: name, Parameters: parameters,
	}); err != nil {
		t.Fatal(err)
	}
}

func connectScalarStateFlow(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	flowID int64,
	sourceID int64,
	dynamicID int64,
	scopeID int64,
) {
	t.Helper()
	for _, wire := range []Wire{
		{SourceID: sourceID, TargetID: dynamicID},
		{SourceID: dynamicID, TargetID: scopeID},
	} {
		if _, err := studio.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}
}
