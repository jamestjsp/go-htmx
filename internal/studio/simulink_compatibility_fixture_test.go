package studio

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const r2026aMIMODelayFixturePath = "testdata/simulink/r2026a/mimo_transfer_pairwise_delay.json"

type simulinkCompatibilityFixture struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Release       string `json:"release"`
	Reference     struct {
		Title    string `json:"title"`
		URL      string `json:"url"`
		Section  string `json:"section"`
		Accessed string `json:"accessed"`
	} `json:"reference"`
	Mapping struct {
		ProcessLabBlock       BlockKind `json:"processLabBlock"`
		MathWorksConcept      string    `json:"mathWorksConcept"`
		SupportedSubset       string    `json:"supportedSubset"`
		IntentionalDeviations []string  `json:"intentionalDeviations"`
	} `json:"mapping"`
	Oracle struct {
		Kind        string `json:"kind"`
		Description string `json:"description"`
	} `json:"oracle"`
	Simulation struct {
		SampleTime float64 `json:"sampleTime"`
		Duration   float64 `json:"duration"`
	} `json:"simulation"`
	Model struct {
		InputNames   []string      `json:"inputNames"`
		OutputNames  []string      `json:"outputNames"`
		InputValues  []float64     `json:"inputValues"`
		Numerators   [][][]float64 `json:"numerators"`
		Denominators [][]float64   `json:"denominators"`
		Delays       [][]float64   `json:"delays"`
	} `json:"model"`
}

func loadR2026aMIMODelayFixture(t *testing.T) simulinkCompatibilityFixture {
	t.Helper()
	encoded, err := os.ReadFile(r2026aMIMODelayFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture simulinkCompatibilityFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestR2026aCompatibilityFixtureCarriesTraceableProvenance(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	if fixture.SchemaVersion != 1 || fixture.ID == "" {
		t.Fatalf("fixture identity = version %d id %q", fixture.SchemaVersion, fixture.ID)
	}
	if fixture.Release != "R2026a" {
		t.Fatalf("release = %q, want R2026a", fixture.Release)
	}
	if fixture.Reference.Title == "" ||
		!strings.HasPrefix(fixture.Reference.URL, "https://www.mathworks.com/help/") ||
		fixture.Reference.Section == "" ||
		fixture.Reference.Accessed == "" {
		t.Fatalf("incomplete MathWorks reference = %#v", fixture.Reference)
	}
	if !fixture.Mapping.ProcessLabBlock.Valid() ||
		fixture.Mapping.MathWorksConcept == "" ||
		fixture.Mapping.SupportedSubset == "" ||
		len(fixture.Mapping.IntentionalDeviations) == 0 {
		t.Fatalf("incomplete Process Lab mapping = %#v", fixture.Mapping)
	}
	allowedOracles := map[string]bool{
		"mathworks-example-data":         true,
		"mathworks-formula-analytic":     true,
		"mathworks-semantics-controlsys": true,
	}
	if !allowedOracles[fixture.Oracle.Kind] || fixture.Oracle.Description == "" {
		t.Fatalf("oracle provenance = %#v", fixture.Oracle)
	}
	generatedDescription := strings.ToLower(strings.Join(
		append(fixture.Mapping.IntentionalDeviations, fixture.Oracle.Description),
		" ",
	))
	if strings.Contains(generatedDescription, "matlab output") &&
		!strings.Contains(generatedDescription, "not matlab") {
		t.Fatalf("generated oracle is mislabeled as MATLAB output: %q", generatedDescription)
	}
}
