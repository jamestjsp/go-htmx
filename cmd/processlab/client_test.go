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
