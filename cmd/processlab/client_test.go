package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAPIClientDecodesSuccessAndEnvelopeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/ok" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"name":"reactor"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"kind":"usage","message":"name is required"}}`)
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var success struct {
		Name string `json:"name"`
	}
	if err := client.request(context.Background(), http.MethodGet, "ok", nil, &success); err != nil {
		t.Fatalf("success request: %v", err)
	}
	if success.Name != "reactor" {
		t.Fatalf("success = %#v", success)
	}

	err = client.request(context.Background(), http.MethodPost, "bad", map[string]string{"name": ""}, nil)
	var clientErr *clientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("error = %v, want clientError", err)
	}
	if clientErr.kind != "usage" || clientErr.message != "name is required" || clientErrorKind(err) != "usage" {
		t.Fatalf("client error = %#v", clientErr)
	}
}

func TestAPIClientUnreachableUsesExitThree(t *testing.T) {
	client, err := newAPIClient("http://127.0.0.1:1", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})
	err = client.request(context.Background(), http.MethodGet, "status", nil, nil)
	var clientErr *clientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("error = %v, want clientError", err)
	}
	if clientErr.ExitCode() != 3 || !strings.Contains(clientErr.Error(), "127.0.0.1:1") ||
		!strings.Contains(clientErr.Error(), "processlab serve") {
		t.Fatalf("unreachable error = %#v", clientErr)
	}
}

func TestCLIHarnessRunsRealBinaryAndCleansUp(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	result := harness.Run("--server", harness.URL(), "--help")
	if result.code != 0 {
		t.Fatalf("command failed: %s", result)
	}
	if result.stderr != "" || !strings.Contains(result.stdout, "Commands:") {
		t.Fatalf("help result = %s", result)
	}

	blockHelp := harness.Run("--server", harness.URL(), "block", "add", "pid", "--help")
	if blockHelp.code != 0 || !strings.Contains(blockHelp.stdout, "--proportional") || blockHelp.stderr != "" {
		t.Fatalf("block help result = %s", blockHelp)
	}

	blockList := harness.Run("--server", harness.URL(), "block", "list")
	if blockList.code != 0 || !strings.Contains(blockList.stdout, "Sources:") || !strings.Contains(blockList.stdout, "pid") {
		t.Fatalf("block list result = %s", blockList)
	}

	created := harness.Run(
		"--server", harness.URL(), "block", "add", "pid", "--flow", "1",
		"--proportional", "2.5", "--json",
	)
	if created.code != 0 || created.stderr != "" {
		t.Fatalf("block add result = %s", created)
	}
	var record struct {
		ID         int64 `json:"id"`
		Parameters struct {
			Proportional float64 `json:"proportional"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(created.stdout), &record); err != nil {
		t.Fatalf("decode created block: %v\n%s", err, created.stdout)
	}
	if record.ID == 0 || record.Parameters.Proportional != 2.5 {
		t.Fatalf("created block = %#v", record)
	}

	for _, test := range []struct {
		kind, flag, value, wantJSON string
	}{
		{kind: "constant", flag: "value", value: "3.5", wantJSON: "3.5"},
		{kind: "gain", flag: "gain", value: "2.5", wantJSON: "2.5"},
		{kind: "mux", flag: "output-names", value: "u1, u2", wantJSON: `["u1","u2"]`},
		{kind: "lag", flag: "time-constant", value: "2", wantJSON: "2"},
		{kind: "unit_delay", flag: "initial-condition", value: "0.5", wantJSON: "0.5"},
		{kind: "state_space", flag: "a", value: "2", wantJSON: `{"rows":1,"columns":1,"values":[2]}`},
		{kind: "vector_scope", flag: "input-names", value: "y", wantJSON: `["y"]`},
	} {
		created := harness.Run(
			"--server", harness.URL(), "block", "add", test.kind, "--flow", "1",
			"--"+test.flag, test.value, "--json",
		)
		if created.code != 0 || created.stderr != "" {
			t.Fatalf("%s add result = %s", test.kind, created)
		}
		var payload struct {
			Parameters map[string]json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal([]byte(created.stdout), &payload); err != nil {
			t.Fatalf("decode %s block: %v\n%s", test.kind, err, created.stdout)
		}
		var got, want any
		if err := json.Unmarshal(payload.Parameters[parameterJSONName(test.flag)], &got); err != nil {
			t.Fatalf("decode %s parameter: %v", test.kind, err)
		}
		if err := json.Unmarshal([]byte(test.wantJSON), &want); err != nil {
			t.Fatalf("decode expected %s parameter: %v", test.kind, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s.%s = %#v, want %#v", test.kind, test.flag, got, want)
		}
	}

	noServer := harness.Run("--server", "http://127.0.0.1:1", "block", "add", "--help")
	if noServer.code != 3 || noServer.stdout != "" || !strings.Contains(noServer.stderr, "processlab serve") {
		t.Fatalf("unreachable block command = %s", noServer)
	}
}

func TestCLIHarnessRunsAnalysisCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	channels := harness.Run("--server", harness.URL(), "analyze", "channels", "--flow", "1", "--json")
	if channels.code != 0 || channels.stderr != "" {
		t.Fatalf("analysis channels result = %s", channels)
	}
	var workspace struct {
		Inputs  []analysisChannelClient `json:"inputs"`
		Outputs []analysisChannelClient `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(channels.stdout), &workspace); err != nil {
		t.Fatalf("decode analysis channels: %v\n%s", err, channels.stdout)
	}
	if len(workspace.Inputs) == 0 || len(workspace.Outputs) == 0 {
		t.Fatalf("analysis channels = %#v", workspace)
	}
	ref := func(channel analysisChannelClient) string {
		return fmt.Sprintf("%d:%d:%d", channel.BlockID, channel.Port, channel.Channel)
	}

	malformed := harness.Run("--server", "http://127.0.0.1:1", "analyze", "dynamics", "--flow", "1", "--input", "bad", "--output", "bad")
	if malformed.code != 2 || !strings.Contains(malformed.stderr, "want block:port:channel") {
		t.Fatalf("malformed channel result = %s", malformed)
	}

	dynamics := harness.Run(
		"--server", harness.URL(), "analyze", "dynamics", "--flow", "1",
		"--input", ref(workspace.Inputs[0]), "--output", ref(workspace.Outputs[len(workspace.Outputs)-1]),
		"--horizon", "2", "--json",
	)
	if dynamics.code != 0 || dynamics.stderr != "" || !strings.Contains(dynamics.stdout, `"dynamics"`) {
		t.Fatalf("analysis dynamics result = %s", dynamics)
	}

	frequency := harness.Run(
		"--server", harness.URL(), "analyze", "frequency", "--flow", "1",
		"--all-channels", "--points", "10", "--json",
	)
	if frequency.code != 0 || frequency.stderr != "" || !strings.Contains(frequency.stdout, `"frequency"`) {
		t.Fatalf("analysis frequency result = %s", frequency)
	}

	added := harness.Run("--server", harness.URL(), "block", "add", "constant", "--flow", "1")
	if added.code != 0 || added.stderr != "" {
		t.Fatalf("model edit result = %s", added)
	}
	show := harness.Run("--server", harness.URL(), "analyze", "show", "--flow", "1")
	if show.code != 0 || !strings.Contains(show.stderr, "cached analysis is stale") {
		t.Fatalf("analysis show result = %s", show)
	}
}

func TestCLIHarnessRunsControlRoleCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	plant := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "lag", "--flow", "1"))
	controller := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "gain", "--flow", "1"))
	for _, endpoints := range [][2]int64{{controller, plant}, {plant, controller}} {
		result := harness.Run(
			"--server", harness.URL(), "wire", "connect", "--flow", "1",
			secondString(endpoints[0]), secondString(endpoints[1]),
		)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("role boundary wire result = %s", result)
		}
	}

	set := harness.Run(
		"--server", harness.URL(), "roles", "set", "--flow", "1",
		"--plant", secondString(plant), "--controller", secondString(controller), "--json",
	)
	if set.code != 0 || set.stderr != "" {
		t.Fatalf("roles set result = %s", set)
	}
	var roles rolesOutput
	if err := json.Unmarshal([]byte(set.stdout), &roles); err != nil {
		t.Fatalf("decode roles set: %v\n%s", err, set.stdout)
	}
	if roles.Version != 1 || roles.Fingerprint == "" || len(roles.Spec.Plant.Blocks) != 1 || len(roles.Spec.Controller.Blocks) != 1 {
		t.Fatalf("roles set = %#v", roles)
	}

	show := harness.Run("--server", harness.URL(), "roles", "show", "--flow", "1")
	if show.code != 0 || show.stderr != "" || !strings.Contains(show.stdout, "plant:") || !strings.Contains(show.stdout, "controller:") || strings.Contains(show.stdout, "<missing>") {
		t.Fatalf("roles show result = %s", show)
	}

	foreignFlow := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "create", "--project", "1", "Foreign"))
	foreignBlock := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "lag", "--flow", secondString(foreignFlow)))
	foreign := harness.Run(
		"--server", harness.URL(), "roles", "set", "--flow", "1",
		"--plant", secondString(foreignBlock), "--controller", secondString(controller),
	)
	if foreign.code != 1 || !strings.Contains(foreign.stderr, "not in the selected flowsheet") {
		t.Fatalf("foreign role result = %s", foreign)
	}

	deleted := harness.Run("--server", harness.URL(), "block", "rm", "--flow", "1", secondString(plant))
	if deleted.code != 0 || deleted.stderr != "" {
		t.Fatalf("referenced block delete result = %s", deleted)
	}
	cleared := harness.Run("--server", harness.URL(), "roles", "show", "--flow", "1")
	if cleared.code != 0 || cleared.stderr != "" || !strings.Contains(cleared.stdout, "roles: cleared") {
		t.Fatalf("cleared roles result = %s", cleared)
	}
}

func TestCLIHarnessRunsPIDControllerCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	plant := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "lag", "--flow", "1"))
	controller := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "pid", "--flow", "1"))
	for _, endpoints := range [][2]int64{{controller, plant}, {plant, controller}} {
		result := harness.Run(
			"--server", harness.URL(), "wire", "connect", "--flow", "1",
			secondString(endpoints[0]), secondString(endpoints[1]),
		)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("controller boundary wire result = %s", result)
		}
	}
	roles := harness.Run(
		"--server", harness.URL(), "roles", "set", "--flow", "1",
		"--plant", secondString(plant), "--controller", secondString(controller), "--json",
	)
	if roles.code != 0 || roles.stderr != "" {
		t.Fatalf("controller roles result = %s", roles)
	}

	designed := harness.Run(
		"--server", harness.URL(), "controller", "pid", "--flow", "1",
		"--type", "PI", "--crossover", "1", "--phase-margin", "55", "--review-horizon", "2", "--json",
	)
	if designed.code != 0 || designed.stderr != "" {
		t.Fatalf("controller PID result = %s", designed)
	}
	var candidate controllerCandidateRecordClient
	if err := json.Unmarshal([]byte(designed.stdout), &candidate); err != nil {
		t.Fatalf("decode controller candidate: %v\n%s", err, designed.stdout)
	}
	if candidate.ID == "" || candidate.Kind != "pid" || candidate.Review.Kind != "pid" {
		t.Fatalf("controller candidate = %#v", candidate)
	}

	review := harness.Run(
		"--server", harness.URL(), "controller", "review", "--flow", "1", candidate.ID, "--json",
	)
	if review.code != 0 || review.stderr != "" || !strings.Contains(review.stdout, `"kind":"pid"`) {
		t.Fatalf("controller review result = %s", review)
	}

	tuned := harness.Run(
		"--server", harness.URL(), "controller", "tune", "--flow", "1",
		"--parameter", "proportional=0.1:5", "--goal", "tracking:tracking:10",
		"--grid-points", "2", "--max-evaluations", "2", "--json",
	)
	if tuned.code != 0 || tuned.stderr != "" || !strings.Contains(tuned.stdout, `"kind":"tuning"`) {
		t.Fatalf("controller tuning result = %s", tuned)
	}
}

func TestCLIHarnessRunsStateControllerCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	flowID := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "create", "--project", "1", "State loop"))
	plant := requireCLIID(t, harness.Run(
		"--server", harness.URL(), "block", "add", "state_space", "--flow", secondString(flowID),
		"--a", "1", "--b", "1", "--c", "1", "--d", "0",
		"--input-names", "u", "--output-names", "y", "--state-names", "x",
	))
	controller := requireCLIID(t, harness.Run(
		"--server", harness.URL(), "block", "add", "matrix_gain", "--flow", secondString(flowID),
		"--d", "0", "--input-names", "y", "--output-names", "u",
	))
	for _, endpoints := range [][2]int64{{controller, plant}, {plant, controller}} {
		result := harness.Run(
			"--server", harness.URL(), "wire", "connect", "--flow", secondString(flowID),
			secondString(endpoints[0]), secondString(endpoints[1]),
		)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("state boundary wire result = %s", result)
		}
	}
	roles := harness.Run(
		"--server", harness.URL(), "roles", "set", "--flow", secondString(flowID),
		"--plant", secondString(plant), "--controller", secondString(controller), "--json",
	)
	if roles.code != 0 || roles.stderr != "" {
		t.Fatalf("state roles result = %s", roles)
	}

	feedback := harness.Run(
		"--server", harness.URL(), "controller", "state", "feedback", "--flow", secondString(flowID),
		"--method", "lqr", "--q", "1", "--r", "1", "--json",
	)
	if feedback.code != 0 || feedback.stderr != "" || !strings.Contains(feedback.stdout, `"kind":"state-space"`) {
		t.Fatalf("state feedback result = %s", feedback)
	}

	estimator := harness.Run(
		"--server", harness.URL(), "controller", "state", "estimator", "--flow", secondString(flowID),
		"--method", "kalman", "--qn", "1", "--rn", "1", "--json",
	)
	if estimator.code != 0 || estimator.stderr != "" || !strings.Contains(estimator.stdout, `"kind":"state-estimator"`) {
		t.Fatalf("state estimator result = %s", estimator)
	}

	observer := harness.Run(
		"--server", harness.URL(), "controller", "state", "observer", "--flow", secondString(flowID),
		"--method", "lqg", "--q", "1", "--r", "1", "--qn", "1", "--rn", "1", "--json",
	)
	if observer.code != 0 || observer.stderr != "" || !strings.Contains(observer.stdout, `"kind":"state-space"`) {
		t.Fatalf("state observer result = %s", observer)
	}
}

func TestCLIHarnessRunsIdentificationCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	jsonInput := identificationDatasetJSON(64, 1)
	jsonResult := harness.RunInput(
		jsonInput,
		"--server", harness.URL(), "ident", "estimate", "--sample-time", "0.1",
		"--nfft", "8", "--overlap", "4", "--json",
	)
	if jsonResult.code != 0 || jsonResult.stderr != "" {
		t.Fatalf("JSON identification result = %s", jsonResult)
	}
	var jsonCandidate struct {
		Name string `json:"name"`
		Fit  struct {
			ComparedBins int `json:"comparedBins"`
		} `json:"fit"`
	}
	if err := json.Unmarshal([]byte(jsonResult.stdout), &jsonCandidate); err != nil {
		t.Fatalf("decode JSON identification result: %v\n%s", err, jsonResult.stdout)
	}
	if jsonCandidate.Name != "identification" || jsonCandidate.Fit.ComparedBins == 0 {
		t.Fatalf("JSON identification candidate = %#v", jsonCandidate)
	}

	var csvInput strings.Builder
	csvInput.WriteString("u,y\n")
	for sample := 0; sample < 64; sample++ {
		input := float64(sample%7 - 3)
		output := 0.5*input + float64((sample+2)%5-2)
		fmt.Fprintf(&csvInput, "%g,%g\n", input, output)
	}
	csvResult := harness.RunInput(
		csvInput.String(),
		"--server", harness.URL(), "ident", "estimate", "--format", "csv",
		"--input-columns", "u", "--output-columns", "y", "--sample-time", "0.1",
		"--training-start", "0", "--training-end", "32", "--validation-start", "32", "--validation-end", "64",
		"--nfft", "8", "--overlap", "4",
	)
	if csvResult.code != 0 || csvResult.stderr != "" || !strings.Contains(csvResult.stdout, "validation fit:") {
		t.Fatalf("CSV identification result = %s", csvResult)
	}

	overlap := harness.RunInput(
		jsonInput, "--server", harness.URL(), "ident", "estimate", "--sample-time", "0.1",
		"--training-start", "0", "--training-end", "40", "--validation-start", "20", "--validation-end", "64",
		"--nfft", "8", "--overlap", "4",
	)
	if overlap.code != 1 || !strings.Contains(overlap.stderr, "sample ranges overlap") {
		t.Fatalf("overlapping identification split = %s", overlap)
	}

	fftLimit := harness.RunInput(
		jsonInput, "--server", harness.URL(), "ident", "estimate", "--sample-time", "0.1",
		"--nfft", "100", "--overlap", "4",
	)
	if fftLimit.code != 1 || !strings.Contains(fftLimit.stderr, "nfft 100 exceeds") {
		t.Fatalf("identification FFT limit = %s", fftLimit)
	}

	channelLimit := harness.RunInput(
		identificationDatasetJSON(8, 33), "--server", harness.URL(), "ident", "estimate",
		"--sample-time", "0.1", "--nfft", "8", "--overlap", "4",
	)
	if channelLimit.code != 1 || !strings.Contains(channelLimit.stderr, "32-channel limit") {
		t.Fatalf("identification channel limit = %s", channelLimit)
	}

	era := harness.RunInput(
		markovDatasetJSON(4), "--server", harness.URL(), "ident", "era", "--order", "1", "--json",
	)
	if era.code != 0 || era.stderr != "" || !strings.Contains(era.stdout, `"order":1`) {
		t.Fatalf("ERA identification result = %s", era)
	}

	eraOrderLimit := harness.RunInput(
		markovDatasetJSON(4), "--server", harness.URL(), "ident", "era", "--order", "257",
	)
	if eraOrderLimit.code != 1 || !strings.Contains(eraOrderLimit.stderr, "order must be between 1 and 256") {
		t.Fatalf("ERA order limit = %s", eraOrderLimit)
	}

	markovLimit := harness.RunInput(
		markovDatasetJSON(4097), "--server", harness.URL(), "ident", "era", "--order", "1",
	)
	if markovLimit.code != 1 || !strings.Contains(markovLimit.stderr, "4096-parameter limit") {
		t.Fatalf("Markov limit = %s", markovLimit)
	}
}

func identificationDatasetJSON(samples, channels int) string {
	inputValues := make([]float64, channels*samples)
	inputNames := make([]string, channels)
	inputUnits := make([]string, channels)
	for channel := range channels {
		inputNames[channel] = fmt.Sprintf("u%d", channel)
		inputUnits[channel] = "1"
		for sample := range samples {
			inputValues[channel*samples+sample] = float64((channel+sample)%7 - 3)
		}
	}
	outputValues := make([]float64, samples)
	for sample := range samples {
		outputValues[sample] = 0.5*inputValues[sample] + float64((sample+2)%5-2)
	}
	value := map[string]any{
		"inputs":     map[string]any{"rows": channels, "columns": samples, "values": inputValues},
		"outputs":    map[string]any{"rows": 1, "columns": samples, "values": outputValues},
		"inputNames": inputNames, "outputNames": []string{"y"},
		"inputUnits": inputUnits, "outputUnits": []string{"1"},
		"sampleTime": 0.1, "timeUnit": "s",
		"split": map[string]any{
			"training":   map[string]int{"start": 0, "end": samples / 2},
			"validation": map[string]int{"start": samples / 2, "end": samples},
		},
		"preprocessing": "none",
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func markovDatasetJSON(count int) string {
	parameters := make([]map[string]any, count)
	for index := range parameters {
		value := 1.0
		if index > 0 {
			value = 1 / float64(index+1)
		}
		parameters[index] = map[string]any{"rows": 1, "columns": 1, "values": []float64{value}}
	}
	value := map[string]any{
		"parameters": parameters, "trainingCount": 3,
		"inputNames": []string{"u"}, "outputNames": []string{"y"},
		"inputUnits": []string{"1"}, "outputUnits": []string{"1"},
		"sampleTime": 0.1, "timeUnit": "s",
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestCLIHarnessRunsParameterSweepCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	listed := harness.Run("--server", harness.URL(), "block", "list", "--flow", "1", "--json")
	if listed.code != 0 || listed.stderr != "" {
		t.Fatalf("list sweep blocks result = %s", listed)
	}
	var blocks []blockRecordClient
	if err := json.Unmarshal([]byte(listed.stdout), &blocks); err != nil {
		t.Fatalf("decode sweep blocks: %v", err)
	}
	var lag int64
	for _, block := range blocks {
		if block.Kind == "lag" {
			lag = block.ID
			break
		}
	}
	if lag == 0 {
		t.Fatal("seeded flow has no lag block")
	}
	result := harness.Run(
		"--server", harness.URL(), "sweep", "run", "--flow", "1", "--block", secondString(lag),
		"--axis", "time_constant=0.5:2:4",
	)
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("sweep run result = %s", result)
	}
	lines := strings.Split(strings.TrimSpace(result.stdout), "\n")
	if len(lines) != 5 || !strings.Contains(result.stdout, "coordinates") || !strings.Contains(result.stdout, "*") {
		t.Fatalf("sweep table = %s", result)
	}

	jsonResult := harness.Run(
		"--server", harness.URL(), "sweep", "run", "--flow", "1", "--block", secondString(lag),
		"--axis", "time_constant=0.5,1", "--json",
	)
	if jsonResult.code != 0 || jsonResult.stderr != "" || !strings.Contains(jsonResult.stdout, `"frequency"`) || !strings.Contains(jsonResult.stdout, `"time"`) {
		t.Fatalf("sweep JSON result = %s", jsonResult)
	}

	unknown := harness.Run(
		"--server", harness.URL(), "sweep", "run", "--flow", "1", "--block", secondString(lag),
		"--axis", "missing=1,2",
	)
	if unknown.code != 1 || !strings.Contains(unknown.stderr, "available parameters: time_constant") {
		t.Fatalf("unknown sweep parameter = %s", unknown)
	}

	tooMany := harness.Run(
		"--server", harness.URL(), "sweep", "run", "--flow", "1", "--block", secondString(lag),
		"--axis", "time_constant=1,2", "--axis", "time_constant=3,4",
		"--axis", "time_constant=5,6", "--axis", "time_constant=7,8", "--axis", "time_constant=9,10",
	)
	if tooMany.code != 1 || !strings.Contains(tooMany.stderr, "between 1 and 4 axes") {
		t.Fatalf("too many sweep axes = %s", tooMany)
	}
}

func TestCLIHarnessRunsWorkspaceCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	created := harness.Run("--server", harness.URL(), "project", "create", "Boiler")
	projectID := requireCLIID(t, created)

	listed := harness.Run("--server", harness.URL(), "project", "list", "--json")
	if listed.code != 0 || listed.stderr != "" {
		t.Fatalf("project list result = %s", listed)
	}
	var projects []projectClientRecord
	if err := json.Unmarshal([]byte(listed.stdout), &projects); err != nil {
		t.Fatalf("decode project list: %v\n%s", err, listed.stdout)
	}
	var boiler projectClientRecord
	for _, project := range projects {
		if project.ID == projectID {
			boiler = project
		}
	}
	if boiler.Name != "Boiler" || boiler.FlowCount != 1 {
		t.Fatalf("created project = %#v, want Boiler with its default flowsheet", boiler)
	}

	createdFlow := harness.Run("--server", harness.URL(), "flow", "create", "--project", strconv.FormatInt(projectID, 10), "Level loop")
	flowID := requireCLIID(t, createdFlow)
	flowList := harness.Run("--server", harness.URL(), "flow", "list", "--project", strconv.FormatInt(projectID, 10), "--json")
	if flowList.code != 0 || flowList.stderr != "" {
		t.Fatalf("flow list result = %s", flowList)
	}
	var flows []flowClientRecord
	if err := json.Unmarshal([]byte(flowList.stdout), &flows); err != nil {
		t.Fatalf("decode flow list: %v\n%s", err, flowList.stdout)
	}
	if len(flows) != 2 {
		t.Fatalf("flow list = %#v, want default and created flowsheet", flows)
	}
	var defaultFlowID int64
	for _, flow := range flows {
		if flow.ID == flowID && !flow.NeedsRun {
			t.Fatalf("new flow stale flag = %#v, want true", flow)
		}
		if flow.ID != flowID {
			defaultFlowID = flow.ID
		}
	}

	added := harness.Run("--server", harness.URL(), "block", "add", "constant", "--flow", strconv.FormatInt(flowID, 10))
	if added.code != 0 || added.stderr != "" {
		t.Fatalf("add block to flow result = %s", added)
	}
	withoutForce := harness.Run("--server", harness.URL(), "flow", "delete", strconv.FormatInt(flowID, 10))
	if withoutForce.code != 1 || !strings.Contains(withoutForce.stderr, "use --force") {
		t.Fatalf("delete without force result = %s", withoutForce)
	}
	withForce := harness.Run("--server", harness.URL(), "flow", "delete", strconv.FormatInt(flowID, 10), "--force")
	if withForce.code != 0 || withForce.stderr != "" {
		t.Fatalf("delete with force result = %s", withForce)
	}

	first := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "create", "--project", strconv.FormatInt(projectID, 10), "First"))
	second := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "create", "--project", strconv.FormatInt(projectID, 10), "Second"))
	if result := harness.Run("--server", harness.URL(), "flow", "reorder", secondString(second), secondString(first), secondString(defaultFlowID), "--project", strconv.FormatInt(projectID, 10)); result.code != 0 || result.stderr != "" {
		t.Fatalf("flow reorder result = %s", result)
	}

	otherProject := requireCLIID(t, harness.Run("--server", harness.URL(), "project", "create", "Other"))
	otherFlows := harness.Run("--server", harness.URL(), "flow", "list", "--project", strconv.FormatInt(otherProject, 10), "--json")
	if otherFlows.code != 0 {
		t.Fatalf("other flow list result = %s", otherFlows)
	}
	var foreign []flowClientRecord
	if err := json.Unmarshal([]byte(otherFlows.stdout), &foreign); err != nil || len(foreign) != 1 {
		t.Fatalf("decode other flows = %v, %#v", err, foreign)
	}
	if result := harness.Run("--server", harness.URL(), "flow", "reorder", secondString(second), secondString(first), secondString(defaultFlowID), secondString(foreign[0].ID), "--project", strconv.FormatInt(projectID, 10)); result.code != 1 || !strings.Contains(result.stderr, "each of this project's") {
		t.Fatalf("foreign flow reorder result = %s", result)
	}

	if result := harness.Run("--server", harness.URL(), "project", "rename", secondString(projectID), "Boiler renamed"); result.code != 0 || result.stderr != "" {
		t.Fatalf("project rename result = %s", result)
	}
	shown := harness.Run("--server", harness.URL(), "project", "show", secondString(projectID), "--json")
	var shownWorkspace workspaceClientRecord
	if shown.code != 0 || json.Unmarshal([]byte(shown.stdout), &shownWorkspace) != nil || shownWorkspace.Project.Name != "Boiler renamed" {
		t.Fatalf("project show result = %s", shown)
	}

	duplicate := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "duplicate", secondString(first)))
	if result := harness.Run("--server", harness.URL(), "flow", "rename", secondString(first), "Renamed first"); result.code != 0 || result.stderr != "" {
		t.Fatalf("flow rename result = %s", result)
	}
	if result := harness.Run("--server", harness.URL(), "flow", "delete", secondString(duplicate), "--force"); result.code != 0 || result.stderr != "" {
		t.Fatalf("duplicate delete result = %s", result)
	}

	lastFlow := harness.Run("--server", harness.URL(), "flow", "list", "--project", strconv.FormatInt(otherProject, 10), "--json")
	if lastFlow.code != 0 {
		t.Fatalf("last flow list result = %s", lastFlow)
	}
	if err := json.Unmarshal([]byte(lastFlow.stdout), &foreign); err != nil || len(foreign) != 1 {
		t.Fatalf("decode last flow = %v, %#v", err, foreign)
	}
	lastDelete := harness.Run("--server", harness.URL(), "flow", "delete", secondString(foreign[0].ID), "--force")
	if lastDelete.code != 1 || !strings.Contains(lastDelete.stderr, "at least one flowsheet") {
		t.Fatalf("last flow delete result = %s", lastDelete)
	}
	if result := harness.Run("--server", harness.URL(), "project", "delete", secondString(otherProject), "--force"); result.code != 0 || result.stderr != "" {
		t.Fatalf("project delete result = %s", result)
	}
}

func TestCLIHarnessRunsBlockAuthoringCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	listed := harness.Run("--server", harness.URL(), "block", "list", "--flow", "1", "--json")
	if listed.code != 0 || listed.stderr != "" {
		t.Fatalf("block list result = %s", listed)
	}
	var blocks []blockRecordClient
	if err := json.Unmarshal([]byte(listed.stdout), &blocks); err != nil {
		t.Fatalf("decode block list: %v\n%s", err, listed.stdout)
	}
	var gain, sum blockRecordClient
	for _, block := range blocks {
		switch block.Kind {
		case "gain":
			gain = block
		case "sum":
			sum = block
		}
	}
	if gain.ID == 0 || sum.ID == 0 {
		t.Fatalf("seeded gain/sum not found: gain=%#v sum=%#v", gain, sum)
	}

	shown := harness.Run("--server", harness.URL(), "block", "show", secondString(gain.ID), "--json")
	if shown.code != 0 || shown.stderr != "" {
		t.Fatalf("block show result = %s", shown)
	}
	var shownBlock blockRecordClient
	if err := json.Unmarshal([]byte(shown.stdout), &shownBlock); err != nil || shownBlock.ID != gain.ID {
		t.Fatalf("block show = %s", shown)
	}

	updated := harness.Run("--server", harness.URL(), "block", "set", secondString(gain.ID), "--gain", "3", "--name", "Updated valve", "--json")
	if updated.code != 0 || updated.stderr != "" {
		t.Fatalf("block set result = %s", updated)
	}
	var updatedBlock blockRecordClient
	if err := json.Unmarshal([]byte(updated.stdout), &updatedBlock); err != nil {
		t.Fatalf("decode block set: %v", err)
	}
	if updatedBlock.Name != "Updated valve" || updatedBlock.Parameters["gain"] != float64(3) {
		t.Fatalf("updated block = %#v", updatedBlock)
	}

	sumUpdate := harness.Run("--server", harness.URL(), "block", "set", secondString(sum.ID), "--signs", "+")
	if sumUpdate.code != 1 || !strings.Contains(sumUpdate.stderr, "wire on input port 1") {
		t.Fatalf("wired sum edit result = %s", sumUpdate)
	}

	moved := harness.Run("--server", harness.URL(), "block", "mv", "--flow", "1", secondString(gain.ID)+":1400,1000", secondString(sum.ID)+":1600,1000", "--json")
	if moved.code != 0 || moved.stderr != "" {
		t.Fatalf("block mv result = %s", moved)
	}
	var movedBatch blockBatchClient
	if err := json.Unmarshal([]byte(moved.stdout), &movedBatch); err != nil || len(movedBatch.Blocks) == 0 {
		t.Fatalf("decode block mv: %v\n%s", err, moved.stdout)
	}

	invalidDelete := harness.Run("--server", harness.URL(), "block", "rm", "--flow", "1", secondString(gain.ID), secondString(sum.ID), "999999")
	if invalidDelete.code != 1 || !strings.Contains(invalidDelete.stderr, "requested item") {
		t.Fatalf("atomic block rm result = %s", invalidDelete)
	}
	afterInvalid := harness.Run("--server", harness.URL(), "block", "list", "--flow", "1", "--json")
	if afterInvalid.code != 0 {
		t.Fatalf("block list after invalid rm = %s", afterInvalid)
	}
	var remaining []blockRecordClient
	if err := json.Unmarshal([]byte(afterInvalid.stdout), &remaining); err != nil {
		t.Fatalf("decode block list after invalid rm: %v", err)
	}
	if !containsClientBlock(remaining, gain.ID) || !containsClientBlock(remaining, sum.ID) {
		t.Fatal("invalid block rm removed a block before rejecting the batch")
	}

	duplicated := harness.Run("--server", harness.URL(), "block", "cp", "--flow", "1", secondString(gain.ID), "--json")
	if duplicated.code != 0 || duplicated.stderr != "" {
		t.Fatalf("block cp result = %s", duplicated)
	}
	var copies blockBatchClient
	if err := json.Unmarshal([]byte(duplicated.stdout), &copies); err != nil || len(copies.Blocks) != 1 {
		t.Fatalf("decode block cp: %v\n%s", err, duplicated.stdout)
	}
	if copies.Blocks[0].Name == "" || !strings.Contains(copies.Blocks[0].Name, "copy") {
		t.Fatalf("copy record = %#v", copies.Blocks[0])
	}
}

func TestCLIHarnessRunsWireCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	source := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "constant", "--flow", "1"))
	target := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "gain", "--flow", "1"))
	connection := harness.Run("--server", harness.URL(), "wire", "connect", "--flow", "1", secondString(source)+":0", secondString(target)+":0")
	connectionID := requireCLIID(t, connection)

	listed := harness.Run("--server", harness.URL(), "wire", "list", "--flow", "1", "--json")
	if listed.code != 0 || listed.stderr != "" {
		t.Fatalf("wire list result = %s", listed)
	}
	var wires []wireRecordClient
	if err := json.Unmarshal([]byte(listed.stdout), &wires); err != nil {
		t.Fatalf("decode wire list: %v\n%s", err, listed.stdout)
	}
	var found wireRecordClient
	for _, wire := range wires {
		if wire.ID == connectionID {
			found = wire
		}
	}
	if found.SourceWidth != 1 || found.TargetWidth != 1 || found.SourcePort != 0 || found.TargetPort != 0 {
		t.Fatalf("wire record = %#v", found)
	}

	secondSource := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "constant", "--flow", "1"))
	occupied := harness.Run("--server", harness.URL(), "wire", "connect", "--flow", "1", secondString(secondSource), secondString(target))
	if occupied.code != 1 || !strings.Contains(occupied.stderr, "already has an input") {
		t.Fatalf("occupied wire result = %s", occupied)
	}
	vector := requireCLIID(t, harness.Run("--server", harness.URL(), "block", "add", "vector_constant", "--flow", "1"))
	mismatch := harness.Run("--server", harness.URL(), "wire", "connect", "--flow", "1", secondString(vector), secondString(target))
	if mismatch.code != 1 || !strings.Contains(mismatch.stderr, "cannot connect") {
		t.Fatalf("width mismatch result = %s", mismatch)
	}

	removed := harness.Run("--server", harness.URL(), "wire", "rm", "--block", secondString(source), "--json")
	if removed.code != 0 || removed.stderr != "" {
		t.Fatalf("block wire removal result = %s", removed)
	}
	var mutation wireMutationClient
	if err := json.Unmarshal([]byte(removed.stdout), &mutation); err != nil || mutation.Removed != 1 {
		t.Fatalf("removed wire response = %v: %s", err, removed.stdout)
	}

	reconnected := requireCLIID(t, harness.Run("--server", harness.URL(), "wire", "connect", "--flow", "1", secondString(source), secondString(target)))
	if result := harness.Run("--server", harness.URL(), "wire", "rm", secondString(reconnected)); result.code != 0 || !strings.Contains(result.stdout, "removed 1 connections") || result.stderr != "" {
		t.Fatalf("connection wire removal result = %s", result)
	}
}

func TestCLIHarnessRunsFlowDumpAndApply(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	dump := harness.Run("--server", harness.URL(), "flow", "dump", "--flow", "1")
	if dump.code != 0 || dump.stderr != "" {
		t.Fatalf("flow dump result = %s", dump)
	}
	var dumped map[string]any
	if err := json.Unmarshal([]byte(dump.stdout), &dumped); err != nil {
		t.Fatalf("decode flow dump: %v", err)
	}
	if dumped["version"] != float64(1) {
		t.Fatalf("flow dump version = %#v", dumped["version"])
	}

	roundTrip := harness.RunInput(dump.stdout, "--server", harness.URL(), "flow", "apply", "--flow", "1")
	if roundTrip.code != 0 || roundTrip.stderr != "" || !strings.Contains(roundTrip.stdout, "No changes.") {
		t.Fatalf("flow round-trip result = %s", roundTrip)
	}

	var changed map[string]any
	if err := json.Unmarshal([]byte(dump.stdout), &changed); err != nil {
		t.Fatal(err)
	}
	blocks := changed["blocks"].([]any)
	blocks = append(blocks, map[string]any{
		"kind": "constant", "name": "Preview", "position": map[string]any{"x": 800, "y": 100},
		"parameters": map[string]any{"value": "4"},
	})
	changed["blocks"] = blocks
	changedJSON, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	dryRun := harness.RunInput(string(changedJSON), "--server", harness.URL(), "flow", "apply", "--flow", "1", "--dry-run")
	if dryRun.code != 0 || dryRun.stderr != "" || !strings.Contains(dryRun.stdout, "Added:") || !strings.Contains(dryRun.stdout, "Preview") || !strings.Contains(dryRun.stdout, "Dry run") {
		t.Fatalf("flow dry-run result = %s", dryRun)
	}

	projectID := requireCLIID(t, harness.Run("--server", harness.URL(), "project", "create", "Declarative"))
	flowID := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "create", "--project", secondString(projectID), "Empty target"))
	emptyDocument := map[string]any{
		"version": 1,
		"blocks": []any{
			map[string]any{"kind": "constant", "name": "Feed", "position": map[string]any{"x": 100, "y": 100}, "parameters": map[string]any{"value": "2"}},
			map[string]any{"kind": "gain", "name": "Valve", "position": map[string]any{"x": 400, "y": 100}, "parameters": map[string]any{"gain": "3"}},
		},
		"wires": []any{map[string]any{"source": "Feed", "sourcePort": 0, "target": "Valve", "targetPort": 0}},
	}
	emptyJSON, err := json.Marshal(emptyDocument)
	if err != nil {
		t.Fatal(err)
	}
	apply := harness.RunInput(string(emptyJSON), "--server", harness.URL(), "flow", "apply", "--flow", secondString(flowID))
	if apply.code != 0 || apply.stderr != "" || !strings.Contains(apply.stdout, "Feed") || !strings.Contains(apply.stdout, "Valve") {
		t.Fatalf("empty flow apply result = %s", apply)
	}
	finalDump := harness.Run("--server", harness.URL(), "flow", "dump", "--flow", secondString(flowID), "--json")
	if finalDump.code != 0 || !strings.Contains(finalDump.stdout, "Feed") || !strings.Contains(finalDump.stdout, "Valve") {
		t.Fatalf("final flow dump result = %s", finalDump)
	}
}

func TestCLIHarnessRunsSimulationCommands(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	run := harness.Run("--server", harness.URL(), "sim", "run", "--flow", "1", "--duration", "1", "--sample-time", "0.1")
	if run.code != 0 || run.stderr != "" {
		t.Fatalf("simulation run result = %s", run)
	}
	lines := strings.Split(strings.TrimSpace(run.stdout), "\n")
	if len(lines) != 12 || !strings.HasPrefix(lines[0], "time\t") {
		t.Fatalf("simulation series has %d lines: %s", len(lines), run.stdout)
	}

	jsonRun := harness.Run("--server", harness.URL(), "sim", "run", "--flow", "1", "--duration", "1", "--sample-time", "0.1", "--json")
	if jsonRun.code != 0 || jsonRun.stderr != "" {
		t.Fatalf("JSON simulation run result = %s", jsonRun)
	}
	var simulation map[string]any
	if err := json.Unmarshal([]byte(jsonRun.stdout), &simulation); err != nil || len(simulation["times"].([]any)) != 11 {
		t.Fatalf("JSON simulation = %v: %s", err, jsonRun.stdout)
	}

	bad := harness.Run("--server", harness.URL(), "sim", "run", "--flow", "1", "--duration", "not-a-number")
	if bad.code != 2 || bad.stdout != "" || !strings.Contains(bad.stderr, "invalid value") {
		t.Fatalf("invalid simulation request = %s", bad)
	}

	if result := harness.Run("--server", harness.URL(), "block", "add", "constant", "--flow", "1"); result.code != 0 {
		t.Fatalf("model edit after simulation = %s", result)
	}
	show := harness.Run("--server", harness.URL(), "sim", "show", "--flow", "1")
	if show.code != 0 || !strings.Contains(show.stdout, "time\t") || !strings.Contains(show.stderr, "is stale") {
		t.Fatalf("stale simulation show = %s", show)
	}

	projectID := requireCLIID(t, harness.Run("--server", harness.URL(), "project", "create", "No runs"))
	flowID := requireCLIID(t, harness.Run("--server", harness.URL(), "flow", "create", "--project", secondString(projectID), "Empty"))
	missing := harness.Run("--server", harness.URL(), "sim", "show", "--flow", secondString(flowID))
	if missing.code != 1 || !strings.Contains(missing.stderr, "run one first") {
		t.Fatalf("missing simulation show = %s", missing)
	}
}

func containsClientBlock(blocks []blockRecordClient, id int64) bool {
	for _, block := range blocks {
		if block.ID == id {
			return true
		}
	}
	return false
}

func requireCLIID(t *testing.T, result cliResult) int64 {
	t.Helper()
	if result.code != 0 || result.stderr != "" {
		t.Fatalf("expected successful id command: %s", result)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(result.stdout), 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("expected positive id in %s: %v", result.stdout, err)
	}
	return id
}

func secondString(value int64) string {
	return strconv.FormatInt(value, 10)
}

func parameterJSONName(flag string) string {
	name := strings.ReplaceAll(flag, "-", "_")
	for index := strings.IndexByte(name, '_'); index >= 0 && index+1 < len(name); index = strings.IndexByte(name, '_') {
		name = name[:index] + strings.ToUpper(name[index+1:index+2]) + name[index+2:]
	}
	return strings.ReplaceAll(name, "_", "")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type cliResult struct {
	args           []string
	code           int
	stdout, stderr string
}

func (result cliResult) String() string {
	return "command=" + strings.Join(result.args, " ") +
		" exit=" + strconv.Itoa(result.code) +
		" stdout=" + result.stdout + " stderr=" + result.stderr
}

type cliHarness struct {
	binary   string
	root     string
	database string
	process  *exec.Cmd
	url      string
}

func newCLIHarness(t *testing.T) *cliHarness {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	binary := filepath.Join(work, "processlab")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	build.Env = append(os.Environ(), "GOCACHE="+filepath.Join(work, "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build processlab: %v\n%s", err, output)
	}
	port := freeTestPort(t)
	serverURL := "http://127.0.0.1:" + port
	process := exec.Command(binary, "serve", "--addr", "127.0.0.1:"+port, "--db", filepath.Join(work, "processlab.db"))
	process.Dir = root
	var output bytes.Buffer
	process.Stdout = &output
	process.Stderr = &output
	if err := process.Start(); err != nil {
		t.Fatalf("start processlab: %v", err)
	}
	harness := &cliHarness{
		binary: binary, root: root, database: filepath.Join(work, "processlab.db"),
		process: process, url: serverURL,
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(serverURL + "/")
		if err == nil {
			response.Body.Close()
			return harness
		}
		if process.ProcessState != nil {
			t.Fatalf("processlab exited while starting: %v\n%s", process.ProcessState, output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("processlab did not start\n%s", output.String())
	return nil
}

func (harness *cliHarness) URL() string {
	return harness.url
}

func (harness *cliHarness) Run(args ...string) cliResult {
	return harness.runWithInput("", args...)
}

func (harness *cliHarness) RunInput(input string, args ...string) cliResult {
	return harness.runWithInput(input, args...)
}

func (harness *cliHarness) runWithInput(input string, args ...string) cliResult {
	command := exec.Command(harness.binary, args...)
	command.Dir = harness.root
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return cliResult{args: args, code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (harness *cliHarness) Close() {
	if harness == nil || harness.process == nil || harness.process.Process == nil {
		return
	}
	if harness.process.ProcessState == nil {
		_ = harness.process.Process.Signal(syscall.SIGTERM)
	}
	_ = harness.process.Wait()
}

func freeTestPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return strconv.Itoa(port)
}
