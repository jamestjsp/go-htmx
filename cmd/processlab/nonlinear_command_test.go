package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIHarnessRunsNonlinearWorkflowsAcrossRestart(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	definition := map[string]any{
		"ref":  map[string]any{"key": "cli/decay", "version": 1},
		"name": "CLI decay", "stateNames": []string{"x"},
		"inputNames": []string{"u"}, "outputNames": []string{"y"},
		"dynamics": []string{"-0.1*x + u"}, "outputs": []string{"x"},
		"sampleTime": 0.1, "integrationSteps": 2,
	}
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	registered := harness.RunInput(string(definitionJSON), "--server", harness.URL(), "nonlinear", "register")
	if registered.code != 0 || registered.stderr != "" || strings.TrimSpace(registered.stdout) != "cli/decay@1" {
		t.Fatalf("nonlinear register = %s", registered)
	}

	operatingPointPath := filepath.Join(t.TempDir(), "origin.json")
	operatingPoint := map[string]any{
		"operatingPoint": map[string]any{
			"name": "origin", "state": []float64{0}, "input": []float64{0},
		},
		"directions": []map[string]any{{
			"name": "state", "stateDelta": []float64{1},
			"inputDelta": []float64{0}, "radius": 0.1,
		}},
	}
	operatingPointJSON, err := json.Marshal(operatingPoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(operatingPointPath, operatingPointJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	linearized := harness.Run(
		"--server", harness.URL(), "nonlinear", "linearize",
		"--definition", "cli/decay@1", "--operating-point", operatingPointPath,
	)
	if linearized.code != 0 || linearized.stderr != "" ||
		!strings.Contains(linearized.stdout, "equilibrium residual norm:") ||
		!strings.Contains(linearized.stdout, "state\t0.1") {
		t.Fatalf("nonlinear linearize = %s", linearized)
	}

	restartCLIHarness(t, harness)
	linearizedAfterRestart := harness.Run(
		"--server", harness.URL(), "nonlinear", "linearize", "--json",
		"--definition", "cli/decay@1", "--operating-point", operatingPointPath,
	)
	if linearizedAfterRestart.code != 0 || linearizedAfterRestart.stderr != "" ||
		!strings.Contains(linearizedAfterRestart.stdout, `"candidateOnly":true`) ||
		!strings.Contains(linearizedAfterRestart.stdout, `"dynamics":["-0.1*x + u"]`) {
		t.Fatalf("nonlinear linearize after restart = %s", linearizedAfterRestart)
	}

	estimate := harness.RunInput(
		"time\tu\ty\n0\t1\t0.1\n0.1\t0\t0\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if estimate.code != 0 || estimate.stderr != "" || !strings.Contains(estimate.stdout, "time\tx\n") {
		t.Fatalf("nonlinear EKF = %s", estimate)
	}

	mismatched := harness.RunInput(
		"time\tu\ty\n0\t1\n", "--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if mismatched.code != 1 || !strings.Contains(mismatched.stderr, "EKF measurement 1 has length 0; want 1") {
		t.Fatalf("mismatched nonlinear EKF = %s", mismatched)
	}

	malformed := harness.RunInput(
		"time\tu\ty\nnot-a-number\t1\t0\n", "--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if malformed.code != 2 || !strings.Contains(malformed.stderr, "TSV row 1 column 1") {
		t.Fatalf("malformed nonlinear TSV = %s", malformed)
	}
}

func TestNonlinearCLIRejectsMalformedStdinBeforeContactingServer(t *testing.T) {
	result := runCLIWithInput(
		t, "{", "--server", "http://127.0.0.1:1", "nonlinear", "register",
	)
	if result.code != 2 || !strings.Contains(result.stderr, "stdin must contain valid JSON") ||
		strings.Contains(result.stderr, "could not reach Process Lab server") {
		t.Fatalf("malformed register stdin = %s", result)
	}
}

func restartCLIHarness(t *testing.T, harness *cliHarness) {
	t.Helper()
	harness.Close()
	port := freeTestPort(t)
	harness.url = "http://127.0.0.1:" + port
	process := exec.Command(
		harness.binary, "serve", "--addr", "127.0.0.1:"+port, "--db", harness.database,
	)
	process.Dir = harness.root
	var output bytes.Buffer
	process.Stdout = &output
	process.Stderr = &output
	if err := process.Start(); err != nil {
		t.Fatalf("restart processlab: %v", err)
	}
	harness.process = process
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(harness.url + "/")
		if err == nil {
			response.Body.Close()
			return
		}
		if process.ProcessState != nil {
			t.Fatalf("processlab exited while restarting: %v\n%s", process.ProcessState, output.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("processlab did not restart\n%s", output.String())
}

func runCLIWithInput(t *testing.T, input string, args ...string) cliResult {
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
	command := exec.Command(binary, args...)
	command.Dir = root
	command.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	return cliResult{args: args, code: code, stdout: stdout.String(), stderr: stderr.String()}
}
