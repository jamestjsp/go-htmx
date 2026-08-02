package studio

import (
	"encoding/json"
	"os"
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
