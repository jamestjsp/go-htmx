package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestFlowsheetBuildingSkillBuildsAndImprovesClosedLoop(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	project := requireSkillCommand(t, harness, "project", "create", "Skill loop")
	projectID := parseSkillID(t, project.stdout)
	flows := requireSkillCommand(t, harness, "flow", "list", "--project", strconv.FormatInt(projectID, 10), "--json")
	var flowRecords []flowClientRecord
	decodeSkillJSON(t, flows.stdout, &flowRecords)
	if len(flowRecords) != 1 {
		t.Fatalf("new project flows = %#v", flowRecords)
	}
	flowID := flowRecords[0].ID

	empty := requireSkillCommand(t, harness, "flow", "dump", "--flow", strconv.FormatInt(flowID, 10))
	var emptyDocument map[string]any
	decodeSkillJSON(t, empty.stdout, &emptyDocument)
	if len(emptyDocument["blocks"].([]any)) != 0 {
		t.Fatalf("new flow is not empty: %s", empty.stdout)
	}

	document := map[string]any{
		"version": 1,
		"blocks": []map[string]any{
			{"kind": "constant", "name": "Reference", "position": map[string]int{"x": 0, "y": 0}, "parameters": map[string]string{"value": "1"}},
			{"kind": "lag", "name": "Plant", "position": map[string]int{"x": 400, "y": 0}, "parameters": map[string]string{"time_constant": "1"}},
			{"kind": "pid2", "name": "Controller", "position": map[string]int{"x": 200, "y": 300}, "parameters": map[string]string{
				"proportional": "1", "integral": "1", "derivative": "0", "filter_coefficient": "100",
				"setpoint_weight": "1", "derivative_weight": "1",
				"time_domain": "continuous", "sample_time": "0.1",
			}},
			{"kind": "scope", "name": "Output", "position": map[string]int{"x": 800, "y": 0}, "parameters": map[string]string{}},
		},
		"wires": []map[string]any{
			{"source": "Reference", "sourcePort": 0, "target": "Controller", "targetPort": 0},
			{"source": "Controller", "sourcePort": 0, "target": "Plant", "targetPort": 0},
			{"source": "Plant", "sourcePort": 0, "target": "Controller", "targetPort": 1},
			{"source": "Plant", "sourcePort": 0, "target": "Output", "targetPort": 0},
		},
	}
	documentJSON, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	requireSkillCommandInput(t, harness, string(documentJSON), "flow", "apply", "--flow", strconv.FormatInt(flowID, 10), "--dry-run")
	requireSkillCommandInput(t, harness, string(documentJSON), "flow", "apply", "--flow", strconv.FormatInt(flowID, 10))

	blocks := requireSkillCommand(t, harness, "block", "list", "--flow", strconv.FormatInt(flowID, 10), "--json")
	var blockRecords []blockRecordClient
	decodeSkillJSON(t, blocks.stdout, &blockRecords)
	ids := make(map[string]int64, len(blockRecords))
	for _, block := range blockRecords {
		ids[block.Name] = block.ID
	}
	for _, name := range []string{"Plant", "Controller", "Reference", "Output"} {
		if ids[name] == 0 {
			t.Fatalf("missing skill block %q: %#v", name, ids)
		}
	}

	baseline := requireSkillCommand(t, harness, "sim", "run", "--flow", strconv.FormatInt(flowID, 10), "--duration", "5", "--sample-time", "0.1", "--json")
	var baselineSimulation simulationClient
	decodeSkillJSON(t, baseline.stdout, &baselineSimulation)
	if baselineSimulation.ID == 0 || len(baselineSimulation.Times) == 0 {
		t.Fatalf("baseline simulation = %#v", baselineSimulation)
	}

	channels := requireSkillCommand(t, harness, "analyze", "channels", "--flow", strconv.FormatInt(flowID, 10), "--json")
	var analysis analysisWorkspaceClient
	decodeSkillJSON(t, channels.stdout, &analysis)
	if len(analysis.Inputs) == 0 || len(analysis.Outputs) == 0 {
		t.Fatalf("analysis channels = %#v", analysis)
	}
	inputRef := fmt.Sprintf("%d:%d:%d", analysis.Inputs[0].BlockID, analysis.Inputs[0].Port, analysis.Inputs[0].Channel)
	outputRef := fmt.Sprintf("%d:%d:%d", analysis.Outputs[0].BlockID, analysis.Outputs[0].Port, analysis.Outputs[0].Channel)
	requireSkillCommand(t, harness, "analyze", "loop", "--flow", strconv.FormatInt(flowID, 10), "--input", inputRef, "--output", outputRef, "--points", "16", "--json")

	roles := requireSkillCommand(t, harness, "roles", "set", "--flow", strconv.FormatInt(flowID, 10), "--plant", strconv.FormatInt(ids["Plant"], 10), "--controller", strconv.FormatInt(ids["Controller"], 10), "--json")
	var roleResult rolesOutput
	decodeSkillJSON(t, roles.stdout, &roleResult)
	if roleResult.Fingerprint == "" || len(roleResult.Spec.Plant.Blocks) != 1 || len(roleResult.Spec.Controller.Blocks) != 1 || len(roleResult.Spec.Controller.ReferenceInputs) != 1 {
		t.Fatalf("skill roles = %#v", roleResult)
	}

	candidate := requireSkillCommand(t, harness, "controller", "pid", "--flow", strconv.FormatInt(flowID, 10), "--type", "PI", "--crossover", "1", "--phase-margin", "55", "--review-horizon", "5", "--json")
	var candidateRecord controllerCandidateRecordClient
	decodeSkillJSON(t, candidate.stdout, &candidateRecord)
	if candidateRecord.ID == "" || candidateRecord.Kind != "pid" {
		t.Fatalf("skill PID candidate = %#v", candidateRecord)
	}

	review := requireSkillCommand(t, harness, "controller", "review", "--flow", strconv.FormatInt(flowID, 10), candidateRecord.ID, "--json")
	var reviewRecord controllerCandidateRecordClient
	decodeSkillJSON(t, review.stdout, &reviewRecord)
	if len(reviewRecord.Review.Time.Traces) != 1 {
		t.Fatalf("skill PID review = %#v", reviewRecord.Review)
	}
	trace := reviewRecord.Review.Time.Traces[0]
	if stepError(trace.CurrentValues, 1) <= stepError(trace.CandidateValues, 1) {
		t.Fatalf("PID candidate did not improve review step error: current=%v candidate=%v", trace.CurrentValues, trace.CandidateValues)
	}

	requireSkillCommand(t, harness, "controller", "apply", "--flow", strconv.FormatInt(flowID, 10), candidateRecord.ID)
	after := requireSkillCommand(t, harness, "sim", "run", "--flow", strconv.FormatInt(flowID, 10), "--duration", "5", "--sample-time", "0.1", "--json")
	var afterSimulation simulationClient
	decodeSkillJSON(t, after.stdout, &afterSimulation)
	if afterSimulation.ID == baselineSimulation.ID || len(afterSimulation.Times) == 0 {
		t.Fatalf("post-apply simulation = %#v", afterSimulation)
	}
	verified := requireSkillCommand(t, harness, "analyze", "loop", "--flow", strconv.FormatInt(flowID, 10), "--input", inputRef, "--output", outputRef, "--points", "16", "--json")
	if !strings.Contains(verified.stdout, `"loop"`) {
		t.Fatalf("post-apply loop verification = %s", verified.stdout)
	}
}

func requireSkillCommand(t *testing.T, harness *cliHarness, args ...string) cliResult {
	t.Helper()
	return requireSkillResult(t, harness.Run(append([]string{"--server", harness.URL()}, args...)...))
}

func requireSkillCommandInput(t *testing.T, harness *cliHarness, input string, args ...string) cliResult {
	t.Helper()
	return requireSkillResult(t, harness.RunInput(input, append([]string{"--server", harness.URL()}, args...)...))
}

func requireSkillResult(t *testing.T, result cliResult) cliResult {
	t.Helper()
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("skill command failed: %s", result)
	}
	return result
}

func parseSkillID(t *testing.T, text string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("parse id %q: %v", text, err)
	}
	return id
}

func decodeSkillJSON(t *testing.T, text string, destination any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), destination); err != nil {
		t.Fatalf("decode skill JSON: %v\n%s", err, text)
	}
}

func stepError(values []float64, target float64) float64 {
	if len(values) == 0 {
		return math.Inf(1)
	}
	return math.Abs(target - values[len(values)-1])
}
