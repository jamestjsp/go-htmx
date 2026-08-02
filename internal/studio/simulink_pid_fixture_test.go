package studio

import (
	"context"
	"encoding/json"
	"math"
	"math/cmplx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const r2026aPIDFixturePath = "testdata/simulink/r2026a/pid_parallel_form.json"

type pidCompatibilityFixture struct {
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
	} `json:"cases"`
	Defaults struct {
		ControllerType    string  `json:"controllerType"`
		Form              string  `json:"form"`
		Proportional      float64 `json:"proportional"`
		Integral          float64 `json:"integral"`
		Derivative        float64 `json:"derivative"`
		FilterCoefficient float64 `json:"filterCoefficient"`
		SetpointWeight    float64 `json:"setpointWeight"`
		DerivativeWeight  float64 `json:"derivativeWeight"`
	} `json:"defaults"`
	Scenario struct {
		Proportional      float64   `json:"proportional"`
		Integral          float64   `json:"integral"`
		Derivative        float64   `json:"derivative"`
		FilterCoefficient float64   `json:"filterCoefficient"`
		SetpointWeight    float64   `json:"setpointWeight"`
		DerivativeWeight  float64   `json:"derivativeWeight"`
		SampleTime        float64   `json:"sampleTime"`
		Omega             []float64 `json:"omega"`
	} `json:"scenario"`
}

func loadR2026aPIDFixture(t *testing.T) pidCompatibilityFixture {
	t.Helper()
	encoded, err := os.ReadFile(r2026aPIDFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture pidCompatibilityFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestR2026aPIDFixtureCarriesTraceableProvenance(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	if fixture.SchemaVersion != 1 || fixture.ID == "" || fixture.Release != "R2026a" {
		t.Fatalf("fixture identity = version %d id %q release %q",
			fixture.SchemaVersion, fixture.ID, fixture.Release)
	}
	if len(fixture.Mappings) != 2 || len(fixture.Cases) != 4 {
		t.Fatalf("fixture mappings/cases = %d/%d, want 2/4",
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

func TestR2026aPIDDirectDefaultsAndPublicFilterCoefficient(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	if fixture.Defaults.ControllerType != "PID" || fixture.Defaults.Form != "Parallel" {
		t.Fatalf("fixture controller/form = %q/%q",
			fixture.Defaults.ControllerType, fixture.Defaults.Form)
	}
	for _, kind := range []BlockKind{BlockPID, BlockPID2} {
		if !strings.Contains(kind.Definition().Description, "Parallel-form") {
			t.Fatalf("%s does not state its supported form: %q",
				kind, kind.Definition().Description)
		}
		parameters := defaultParameters(kind)
		if parameters.Proportional != fixture.Defaults.Proportional ||
			parameters.Integral != fixture.Defaults.Integral ||
			parameters.Derivative != fixture.Defaults.Derivative ||
			parameters.FilterCoefficient != fixture.Defaults.FilterCoefficient {
			t.Fatalf("%s defaults = %#v", kind, parameters)
		}
		if kind == BlockPID2 &&
			(parameters.SetpointWeight != fixture.Defaults.SetpointWeight ||
				parameters.DerivativeWeight != fixture.Defaults.DerivativeWeight) {
			t.Fatalf("%s weights = %#v", kind, parameters)
		}
		fields := Block{Kind: kind, Parameters: parameters}.EditorFields()
		names := make(map[string]ParameterField, len(fields))
		for _, field := range fields {
			names[field.Name] = field
		}
		if names["filter_coefficient"].Label != "Filter coefficient N" {
			t.Fatalf("%s filter coefficient field = %#v", kind, names["filter_coefficient"])
		}
		if _, found := names["filter_time"]; found {
			t.Fatalf("%s still exposes filter_time", kind)
		}
	}
}

func TestLegacyPIDFilterTimeMigratesToCoefficient(t *testing.T) {
	for _, kind := range []BlockKind{BlockPID, BlockPID2} {
		parameters, err := decodeParameters(kind,
			`{"parameterSchemaVersion":1,"proportional":2,"integral":1,"derivative":0.2,"filterTime":0.05}`)
		if err != nil {
			t.Fatal(err)
		}
		if parameters.FilterCoefficient != 20 || parameters.FilterTime != 0 {
			t.Fatalf("%s migrated filter = %#v", kind, parameters)
		}
		encoded, err := encodeParameters(parameters)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(encoded, `"filterCoefficient":20`) ||
			strings.Contains(encoded, `"filterTime"`) {
			t.Fatalf("%s canonical parameters = %s", kind, encoded)
		}
	}
}

func TestR2026aParallelPIDFrequencyThroughPublicStudio(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	flow := newPIDCompatibilityFlow(t, BlockPID, modelDomainContinuous, fixture)
	result, err := flow.studio.AnalyzeFrequency(flow.ctx, flow.flowID, FrequencyAnalysisRequest{
		Inputs:  []ChannelRef{{BlockID: flow.sourceIDs[0]}},
		Outputs: []ChannelRef{{BlockID: flow.controllerID}},
		Omega:   fixture.Scenario.Omega,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, omega := range fixture.Scenario.Omega {
		s := complex(0, omega)
		want := complex(fixture.Scenario.Proportional, 0) +
			complex(fixture.Scenario.Integral, 0)/s +
			complex(fixture.Scenario.Derivative*fixture.Scenario.FilterCoefficient, 0)*
				s/(s+complex(fixture.Scenario.FilterCoefficient, 0))
		assertBodeSample(t, result, index, want)
	}
}

func TestR2026aParallelPITimeResponseThroughPublicStudio(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	flow := newPIDCompatibilityFlow(t, BlockPID, modelDomainContinuous, fixture)
	if _, err := flow.studio.UpdateBlock(flow.ctx, flow.controllerID, BlockUpdate{
		Name: "PI Controller",
		Parameters: map[string]string{
			"proportional":       formatFloat(fixture.Scenario.Proportional),
			"integral":           formatFloat(fixture.Scenario.Integral),
			"derivative":         "0",
			"filter_coefficient": formatFloat(fixture.Scenario.FilterCoefficient),
			"time_domain":        modelDomainContinuous,
			"sample_time":        formatFloat(fixture.Scenario.SampleTime),
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := flow.studio.AddBlock(
		flow.ctx, flow.flowID, BlockScope, Point{X: 700, Y: 160},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := flow.studio.Connect(flow.ctx, flow.flowID, Wire{
		SourceID: flow.controllerID, TargetID: scopeID,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := flow.studio.Run(flow.ctx, flow.flowID, SimulationRequest{
		Duration: 1, SampleTime: 0.05,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil || len(snapshot.LastRun.Series) != 1 {
		t.Fatalf("public PI run = %#v", snapshot.LastRun)
	}
	for index, got := range snapshot.LastRun.Series[0].Values {
		time := snapshot.LastRun.Times[index]
		want := fixture.Scenario.Proportional + fixture.Scenario.Integral*time
		if math.Abs(got-want) > 1e-10 {
			t.Fatalf("PI sample %d at %g = %.12g, want %.12g",
				index, time, got, want)
		}
	}
}

func TestR2026aParallelPIDTypesUseActiveGainTerms(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	tests := []struct {
		name string
		p    float64
		i    float64
		d    float64
	}{
		{name: "P", p: fixture.Scenario.Proportional},
		{name: "I", i: fixture.Scenario.Integral},
		{name: "PI", p: fixture.Scenario.Proportional, i: fixture.Scenario.Integral},
		{name: "PD", p: fixture.Scenario.Proportional, d: fixture.Scenario.Derivative},
		{name: "PID", p: fixture.Scenario.Proportional, i: fixture.Scenario.Integral, d: fixture.Scenario.Derivative},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flow := newPIDCompatibilityFlow(
				t, BlockPID, modelDomainContinuous, fixture,
			)
			if _, err := flow.studio.UpdateBlock(
				flow.ctx,
				flow.controllerID,
				BlockUpdate{
					Name: test.name + " Controller",
					Parameters: map[string]string{
						"proportional":       formatFloat(test.p),
						"integral":           formatFloat(test.i),
						"derivative":         formatFloat(test.d),
						"filter_coefficient": formatFloat(fixture.Scenario.FilterCoefficient),
						"time_domain":        modelDomainContinuous,
						"sample_time":        formatFloat(fixture.Scenario.SampleTime),
					},
				},
			); err != nil {
				t.Fatal(err)
			}
			result, err := flow.studio.AnalyzeFrequency(
				flow.ctx,
				flow.flowID,
				FrequencyAnalysisRequest{
					Inputs:  []ChannelRef{{BlockID: flow.sourceIDs[0]}},
					Outputs: []ChannelRef{{BlockID: flow.controllerID}},
					Omega:   fixture.Scenario.Omega,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			for index, omega := range fixture.Scenario.Omega {
				s := complex(0, omega)
				want := complex(test.p, 0)
				if test.i != 0 {
					want += complex(test.i, 0) / s
				}
				if test.d != 0 {
					want += complex(test.d*fixture.Scenario.FilterCoefficient, 0) *
						s / (s + complex(fixture.Scenario.FilterCoefficient, 0))
				}
				assertBodeSample(t, result, index, want)
			}
		})
	}
}

func TestR2026aDiscretePIDUsesForwardEulerNMapping(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	flow := newPIDCompatibilityFlow(t, BlockPID, modelDomainDiscrete, fixture)
	result, err := flow.studio.AnalyzeFrequency(flow.ctx, flow.flowID, FrequencyAnalysisRequest{
		Inputs:   []ChannelRef{{BlockID: flow.sourceIDs[0]}},
		Outputs:  []ChannelRef{{BlockID: flow.controllerID}},
		BaseStep: fixture.Scenario.SampleTime,
		Omega:    fixture.Scenario.Omega,
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, omega := range fixture.Scenario.Omega {
		z := cmplx.Exp(complex(0, omega*fixture.Scenario.SampleTime))
		forwardEulerIntegrator := complex(fixture.Scenario.SampleTime, 0) / (z - 1)
		forwardEulerDerivative := complex(fixture.Scenario.FilterCoefficient, 0) *
			(z - 1) /
			(z - complex(1-fixture.Scenario.FilterCoefficient*fixture.Scenario.SampleTime, 0))
		want := complex(fixture.Scenario.Proportional, 0) +
			complex(fixture.Scenario.Integral, 0)*forwardEulerIntegrator +
			complex(fixture.Scenario.Derivative, 0)*forwardEulerDerivative
		assertBodeSample(t, result, index, want)
	}
}

func TestR2026aParallelPID2WeightsThroughPublicStudio(t *testing.T) {
	fixture := loadR2026aPIDFixture(t)
	flow := newPIDCompatibilityFlow(t, BlockPID2, modelDomainContinuous, fixture)
	for input, sourceID := range flow.sourceIDs {
		result, err := flow.studio.AnalyzeFrequency(flow.ctx, flow.flowID, FrequencyAnalysisRequest{
			Inputs:  []ChannelRef{{BlockID: sourceID}},
			Outputs: []ChannelRef{{BlockID: flow.controllerID}},
			Omega:   fixture.Scenario.Omega,
		})
		if err != nil {
			t.Fatal(err)
		}
		for index, omega := range fixture.Scenario.Omega {
			s := complex(0, omega)
			filteredD := complex(
				fixture.Scenario.Derivative*fixture.Scenario.FilterCoefficient, 0,
			) * s / (s + complex(fixture.Scenario.FilterCoefficient, 0))
			var want complex128
			if input == 0 {
				want = complex(fixture.Scenario.Proportional*fixture.Scenario.SetpointWeight, 0) +
					complex(fixture.Scenario.Integral, 0)/s +
					complex(fixture.Scenario.DerivativeWeight, 0)*filteredD
			} else {
				want = -complex(fixture.Scenario.Proportional, 0) -
					complex(fixture.Scenario.Integral, 0)/s - filteredD
			}
			assertBodeSample(t, result, index, want)
		}
	}
}

type pidCompatibilityFlow struct {
	studio       *Studio
	ctx          context.Context
	flowID       int64
	sourceIDs    []int64
	controllerID int64
}

func newPIDCompatibilityFlow(
	t *testing.T,
	kind BlockKind,
	timeDomain string,
	fixture pidCompatibilityFixture,
) pidCompatibilityFlow {
	t.Helper()
	studio := openTestStudio(t, filepath.Join(t.TempDir(), "r2026a-pid.db"))
	ctx := context.Background()
	seeded, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := studio.CreateFlow(ctx, seeded.Flow.ProjectID, fixture.ID+"-"+string(kind))
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	sourceCount := 1
	if kind == BlockPID2 {
		sourceCount = 2
	}
	sourceIDs := make([]int64, sourceCount)
	for index := range sourceCount {
		_, sourceIDs[index], err = studio.AddBlock(
			ctx, flowID, BlockConstant, Point{X: 100, Y: 100 + index*160},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, controllerID, err := studio.AddBlock(ctx, flowID, kind, Point{X: 400, Y: 160})
	if err != nil {
		t.Fatal(err)
	}
	parameters := map[string]string{
		"proportional":       formatFloat(fixture.Scenario.Proportional),
		"integral":           formatFloat(fixture.Scenario.Integral),
		"derivative":         formatFloat(fixture.Scenario.Derivative),
		"filter_coefficient": formatFloat(fixture.Scenario.FilterCoefficient),
		"time_domain":        timeDomain,
		"sample_time":        formatFloat(fixture.Scenario.SampleTime),
	}
	if kind == BlockPID2 {
		parameters["setpoint_weight"] = formatFloat(fixture.Scenario.SetpointWeight)
		parameters["derivative_weight"] = formatFloat(fixture.Scenario.DerivativeWeight)
	}
	if _, err := studio.UpdateBlock(ctx, controllerID, BlockUpdate{
		Name: "Controller", Parameters: parameters,
	}); err != nil {
		t.Fatal(err)
	}
	for index, sourceID := range sourceIDs {
		if _, err := studio.Connect(ctx, flowID, Wire{
			SourceID: sourceID, TargetID: controllerID, TargetPort: index,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return pidCompatibilityFlow{
		studio: studio, ctx: ctx, flowID: flowID,
		sourceIDs: sourceIDs, controllerID: controllerID,
	}
}

func assertBodeSample(
	t *testing.T,
	result FrequencyAnalysis,
	index int,
	want complex128,
) {
	t.Helper()
	if len(result.Bode) != 1 ||
		result.Bode[0].MagnitudeDB[index] == nil ||
		result.Bode[0].PhaseDegrees[index] == nil {
		t.Fatalf("missing Bode sample %d in %#v", index, result)
	}
	wantMagnitude := 20 * math.Log10(cmplx.Abs(want))
	wantPhase := cmplx.Phase(want) * 180 / math.Pi
	phaseDifference := math.Mod(
		*result.Bode[0].PhaseDegrees[index]-wantPhase+180,
		360,
	) - 180
	if phaseDifference < -180 {
		phaseDifference += 360
	}
	if math.Abs(*result.Bode[0].MagnitudeDB[index]-wantMagnitude) > 1e-9 ||
		math.Abs(phaseDifference) > 1e-9 {
		t.Fatalf("Bode[%d] = %.12g dB, %.12g deg; want %.12g dB, %.12g deg",
			index,
			*result.Bode[0].MagnitudeDB[index],
			*result.Bode[0].PhaseDegrees[index],
			wantMagnitude,
			wantPhase,
		)
	}
}
