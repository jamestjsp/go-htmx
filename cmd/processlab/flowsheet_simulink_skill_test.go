package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

const r2026aMIMODelayCLIPath = "internal/studio/testdata/simulink/r2026a/mimo_transfer_pairwise_delay.json"

type simulinkMIMODelayCLIFixture struct {
	Release    string `json:"release"`
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

func TestFlowsheetBuildingSkillRunsSimulinkR2026aMIMODelay(t *testing.T) {
	fixture := loadSimulinkMIMODelayCLIFixture(t)
	if fixture.Release != "R2026a" {
		t.Fatalf("fixture release = %q, want R2026a", fixture.Release)
	}

	inputValues := requireVectorText(t, fixture.Model.InputValues)
	inputNames := requireChannelNamesText(t, fixture.Model.InputNames)
	outputNames := requireChannelNamesText(t, fixture.Model.OutputNames)
	numerators := requirePolynomialMatrixText(t, fixture.Model.Numerators)
	denominators := make([][][]float64, len(fixture.Model.Denominators))
	for row, denominator := range fixture.Model.Denominators {
		denominators[row] = [][]float64{append([]float64(nil), denominator...)}
	}
	denominatorText := requirePolynomialMatrixText(t, denominators)
	delays := requireMatrixText(t, fixture.Model.Delays)

	harness := newCLIHarness(t)
	defer harness.Close()

	project := requireSkillCommand(t, harness, "project", "create", "Simulink R2026a MIMO delay")
	projectID := parseSkillID(t, project.stdout)
	flows := requireSkillCommand(t, harness, "flow", "list", "--project", strconv.FormatInt(projectID, 10), "--json")
	var flowRecords []flowClientRecord
	decodeSkillJSON(t, flows.stdout, &flowRecords)
	if len(flowRecords) != 1 {
		t.Fatalf("new project flows = %#v", flowRecords)
	}
	flowID := flowRecords[0].ID
	flowIDText := strconv.FormatInt(flowID, 10)

	document := map[string]any{
		"version": 1,
		"blocks": []map[string]any{
			{
				"kind": "vector_constant", "name": "Inputs",
				"position": map[string]int{"x": 0, "y": 120},
				"parameters": map[string]string{
					"vector":       inputValues,
					"output_names": inputNames,
				},
			},
			{
				"kind": "mimo_transfer", "name": "Delayed plant",
				"position": map[string]int{"x": 360, "y": 120},
				"parameters": map[string]string{
					"transfer_numerators":   numerators,
					"transfer_denominators": denominatorText,
					"transfer_delays":       delays,
					"input_names":           inputNames,
					"output_names":          outputNames,
					"time_domain":           "continuous",
					"sample_time":           strconv.FormatFloat(fixture.Simulation.SampleTime, 'g', -1, 64),
				},
			},
			{
				"kind": "vector_scope", "name": "Outputs",
				"position":   map[string]int{"x": 760, "y": 120},
				"parameters": map[string]string{"input_names": outputNames},
			},
		},
		"wires": []map[string]any{
			{"source": "Inputs", "sourcePort": 0, "target": "Delayed plant", "targetPort": 0},
			{"source": "Delayed plant", "sourcePort": 0, "target": "Outputs", "targetPort": 0},
		},
	}
	documentJSON, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	dryRun := requireSkillCommandInput(t, harness, string(documentJSON), "flow", "apply", "--flow", flowIDText, "--dry-run", "--json")
	var dryRunResponse flowApplyResponseClient
	decodeSkillJSON(t, dryRun.stdout, &dryRunResponse)
	if !dryRunResponse.Result.DryRun || len(dryRunResponse.Result.Added) != 3 || dryRunResponse.Result.WiresAdded != 2 {
		t.Fatalf("MIMO dry-run result = %#v", dryRunResponse.Result)
	}

	apply := requireSkillCommandInput(t, harness, string(documentJSON), "flow", "apply", "--flow", flowIDText, "--json")
	var applyResponse flowApplyResponseClient
	decodeSkillJSON(t, apply.stdout, &applyResponse)
	if applyResponse.Result.DryRun || len(applyResponse.Result.Added) != 3 || applyResponse.Result.WiresAdded != 2 {
		t.Fatalf("MIMO apply result = %#v", applyResponse.Result)
	}

	blocks := requireSkillCommand(t, harness, "block", "list", "--flow", flowIDText, "--json")
	var blockRecords []blockRecordClient
	decodeSkillJSON(t, blocks.stdout, &blockRecords)
	if len(blockRecords) != 3 {
		t.Fatalf("applied MIMO blocks = %#v", blockRecords)
	}
	blockNames := make(map[string]bool, len(blockRecords))
	for _, block := range blockRecords {
		if block.FlowID != flowID {
			t.Fatalf("block %q belongs to flow %d, want %d", block.Name, block.FlowID, flowID)
		}
		blockNames[block.Name] = true
	}
	for _, name := range []string{"Inputs", "Delayed plant", "Outputs"} {
		if !blockNames[name] {
			t.Fatalf("applied MIMO blocks omit %q: %#v", name, blockNames)
		}
	}

	wires := requireSkillCommand(t, harness, "wire", "list", "--flow", flowIDText, "--json")
	var wireRecords []wireRecordClient
	decodeSkillJSON(t, wires.stdout, &wireRecords)
	if len(wireRecords) != 2 {
		t.Fatalf("applied MIMO wires = %#v", wireRecords)
	}
	for _, wire := range wireRecords {
		if wire.FlowID != flowID {
			t.Fatalf("wire %d belongs to flow %d, want %d", wire.ID, wire.FlowID, flowID)
		}
	}

	channels := requireSkillCommand(t, harness, "analyze", "channels", "--flow", flowIDText, "--json")
	var analysis analysisWorkspaceClient
	decodeSkillJSON(t, channels.stdout, &analysis)
	if len(analysis.Inputs) != len(fixture.Model.InputNames) {
		t.Fatalf("MIMO analysis channels = %#v", analysis)
	}
	for _, name := range fixture.Model.OutputNames {
		found := false
		for _, channel := range analysis.Outputs {
			if strings.HasSuffix(channel.Name, "· "+name) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("MIMO analysis omitted output %q: %#v", name, analysis.Outputs)
		}
	}

	simulation := requireSkillCommand(t, harness, "sim", "run", "--flow", flowIDText,
		"--duration", strconv.FormatFloat(fixture.Simulation.Duration, 'g', -1, 64),
		"--sample-time", strconv.FormatFloat(fixture.Simulation.SampleTime, 'g', -1, 64), "--json")
	var result simulationClient
	decodeSkillJSON(t, simulation.stdout, &result)
	if result.Stale || len(result.Times) == 0 || len(result.Series) != len(fixture.Model.OutputNames) {
		t.Fatalf("MIMO simulation = %#v", result)
	}
	assertSimulinkMIMODelayCLIOracle(t, fixture, result)
}

func loadSimulinkMIMODelayCLIFixture(t *testing.T) simulinkMIMODelayCLIFixture {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixturePath := filepath.Join(filepath.Dir(sourceFile), "..", "..", r2026aMIMODelayCLIPath)
	encoded, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read Simulink fixture %s: %v", fixturePath, err)
	}
	var fixture simulinkMIMODelayCLIFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatalf("decode Simulink fixture: %v", err)
	}
	return fixture
}

func requireVectorText(t *testing.T, values []float64) string {
	t.Helper()
	value, err := studio.NewVectorValue(values)
	if err != nil {
		t.Fatal(err)
	}
	return value.Text()
}

func requireChannelNamesText(t *testing.T, names []string) string {
	t.Helper()
	value, err := studio.NewChannelNames(names)
	if err != nil {
		t.Fatal(err)
	}
	return value.Text()
}

func requirePolynomialMatrixText(t *testing.T, values [][][]float64) string {
	t.Helper()
	value, err := studio.NewPolynomialMatrixValue(values)
	if err != nil {
		t.Fatal(err)
	}
	return value.Text()
}

func requireMatrixText(t *testing.T, rows [][]float64) string {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("matrix fixture has no rows")
	}
	columns := len(rows[0])
	values := make([]float64, 0, len(rows)*columns)
	for _, row := range rows {
		if len(row) != columns {
			t.Fatalf("matrix fixture row has %d columns, want %d", len(row), columns)
		}
		values = append(values, row...)
	}
	value, err := studio.NewMatrixValue(len(rows), columns, values)
	if err != nil {
		t.Fatal(err)
	}
	return value.Text()
}

func assertSimulinkMIMODelayCLIOracle(t *testing.T, fixture simulinkMIMODelayCLIFixture, result simulationClient) {
	t.Helper()
	for output, name := range fixture.Model.OutputNames {
		var values []float64
		for _, series := range result.Series {
			if series.Name == name || strings.HasSuffix(series.Name, "· "+name) {
				values = series.Values
				break
			}
		}
		if values == nil {
			t.Fatalf("simulation omitted output %q", name)
		}
		if len(values) != len(result.Times) {
			t.Fatalf("output %q has %d samples, want %d", name, len(values), len(result.Times))
		}
		for sample, currentTime := range result.Times {
			want := 0.0
			for input := range fixture.Model.InputNames {
				shiftedTime := currentTime - fixture.Model.Delays[output][input]
				if shiftedTime < 0 {
					continue
				}
				numerator := fixture.Model.Numerators[output][input]
				if len(numerator) != 1 || len(fixture.Model.Denominators[output]) != 2 || fixture.Model.Denominators[output][0] != 1 {
					t.Fatalf("fixture output %q is not scalar first-order data", name)
				}
				rate := fixture.Model.Denominators[output][1]
				want += fixture.Model.InputValues[input] * numerator[0] / rate * (1 - math.Exp(-rate*shiftedTime))
			}
			if difference := math.Abs(values[sample] - want); difference > 1e-10 {
				t.Fatalf("%s at t=%.3g = %.12g, want %.12g (difference %.3g)", name, currentTime, values[sample], want, difference)
			}
		}
	}
}
