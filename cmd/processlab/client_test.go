package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	command := exec.Command(harness.binary, args...)
	command.Dir = harness.root
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
