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

func TestR2026aMIMOTransferPairwiseDelaysRunThroughStudio(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	studio := openTestStudio(t, filepath.Join(t.TempDir(), "r2026a.db"))
	ctx := context.Background()
	seeded, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := studio.CreateFlow(ctx, seeded.Flow.ProjectID, fixture.ID)
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID

	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockVectorConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, transferID, err := studio.AddBlock(ctx, flowID, BlockMIMOTransfer, Point{X: 400, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockVectorScope, Point{X: 700, Y: 100})
	if err != nil {
		t.Fatal(err)
	}

	inputNames, err := NewChannelNames(fixture.Model.InputNames)
	if err != nil {
		t.Fatal(err)
	}
	outputNames, err := NewChannelNames(fixture.Model.OutputNames)
	if err != nil {
		t.Fatal(err)
	}
	inputValues, err := NewVectorValue(fixture.Model.InputValues)
	if err != nil {
		t.Fatal(err)
	}
	numerators, err := NewPolynomialMatrixValue(fixture.Model.Numerators)
	if err != nil {
		t.Fatal(err)
	}
	denominatorValues := make([][][]float64, len(fixture.Model.Denominators))
	for output, denominator := range fixture.Model.Denominators {
		denominatorValues[output] = [][]float64{append([]float64(nil), denominator...)}
	}
	denominators, err := NewPolynomialMatrixValue(denominatorValues)
	if err != nil {
		t.Fatal(err)
	}
	delays, err := matrixValueFromRows(fixture.Model.Delays)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := studio.UpdateBlock(ctx, sourceID, BlockUpdate{
		Name: "Inputs",
		Parameters: map[string]string{
			"vector":       inputValues.Text(),
			"output_names": inputNames.Text(),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, transferID, BlockUpdate{
		Name: "Delayed plant",
		Parameters: map[string]string{
			"transfer_numerators":   numerators.Text(),
			"transfer_denominators": denominators.Text(),
			"transfer_delays":       delays.Text(),
			"input_names":           inputNames.Text(),
			"output_names":          outputNames.Text(),
			"time_domain":           modelDomainContinuous,
			"sample_time":           strconv.FormatFloat(fixture.Simulation.SampleTime, 'g', -1, 64),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, scopeID, BlockUpdate{
		Name:       "Outputs",
		Parameters: map[string]string{"input_names": outputNames.Text()},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{SourceID: sourceID, TargetID: transferID}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{SourceID: transferID, TargetID: scopeID}); err != nil {
		t.Fatal(err)
	}

	runSnapshot, err := studio.Run(ctx, flowID, SimulationRequest{
		Duration: fixture.Simulation.Duration, SampleTime: fixture.Simulation.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := studio.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if runSnapshot.LastRun == nil || persisted.LastRun == nil ||
		runSnapshot.LastRun.ID != persisted.LastRun.ID {
		t.Fatalf("public run was not persisted: returned=%#v stored=%#v",
			runSnapshot.LastRun, persisted.LastRun)
	}
	if persisted.LastRun.Fidelity.Driver != "delay-aware-simulate" {
		t.Fatalf("simulation driver = %q, want delay-aware-simulate",
			persisted.LastRun.Fidelity.Driver)
	}
	assertMIMOFirstOrderDelayFixture(t, fixture, persisted.LastRun)
}

func matrixValueFromRows(rows [][]float64) (MatrixValue, error) {
	if len(rows) == 0 {
		return MatrixValue{}, invalid("matrix fixture must contain at least one row")
	}
	values := make([]float64, 0, len(rows)*len(rows[0]))
	for _, row := range rows {
		if len(row) != len(rows[0]) {
			return MatrixValue{}, invalid("matrix fixture rows must have equal widths")
		}
		values = append(values, row...)
	}
	return NewMatrixValue(len(rows), len(rows[0]), values)
}

func assertMIMOFirstOrderDelayFixture(
	t *testing.T,
	fixture simulinkCompatibilityFixture,
	run *Simulation,
) {
	t.Helper()
	if len(run.Series) != len(fixture.Model.OutputNames) {
		t.Fatalf("series count = %d, want %d", len(run.Series), len(fixture.Model.OutputNames))
	}
	seriesByName := make(map[string]Series, len(run.Series))
	for _, series := range run.Series {
		seriesByName[series.ChannelName] = series
	}
	for output, name := range fixture.Model.OutputNames {
		series, ok := seriesByName[name]
		if !ok {
			t.Fatalf("missing persisted output series %q in %#v", name, run.Series)
		}
		denominator := fixture.Model.Denominators[output]
		if len(denominator) != 2 || denominator[0] != 1 {
			t.Fatalf("output %q denominator = %v, want first-order monic", name, denominator)
		}
		rate := denominator[1]
		for sample, currentTime := range run.Times {
			var want float64
			for input := range fixture.Model.InputNames {
				shiftedTime := currentTime - fixture.Model.Delays[output][input]
				if shiftedTime < 0 {
					continue
				}
				numerator := fixture.Model.Numerators[output][input]
				if len(numerator) != 1 {
					t.Fatalf("path [%d,%d] numerator = %v, want scalar", output, input, numerator)
				}
				want += fixture.Model.InputValues[input] *
					numerator[0] / rate *
					(1 - math.Exp(-rate*shiftedTime))
			}
			if difference := math.Abs(series.Values[sample] - want); difference > 1e-10 {
				t.Fatalf(
					"%s at t=%.3g = %.12g, analytic R2026a IODelay oracle %.12g (difference %.3g)",
					name, currentTime, series.Values[sample], want, difference,
				)
			}
		}
	}
}
