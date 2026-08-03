package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
