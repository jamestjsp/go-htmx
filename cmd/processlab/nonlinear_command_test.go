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
	if estimate.code != 0 ||
		estimate.stderr != "Using identity Q, R, and P0 covariances with a zero initial state; pass --estimator to configure the EKF.\n" ||
		!strings.Contains(estimate.stdout, "time\tx\n") {
		t.Fatalf("nonlinear EKF = %s", estimate)
	}

	reordered := harness.RunInput(
		"time\ty\tu\n0\t0.1\t1\n0.1\t0\t0\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if reordered.code != 0 || reordered.stderr != estimate.stderr || reordered.stdout != estimate.stdout {
		t.Fatalf("reordered nonlinear EKF = %s; want output %s", reordered, estimate.stdout)
	}

	extra := harness.RunInput(
		"time\textra\tu\ty\n0\t99\t1\t0.1\n0.1\t99\t0\t0\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if extra.code != 0 || extra.stderr != estimate.stderr || extra.stdout != estimate.stdout {
		t.Fatalf("extra-column nonlinear EKF = %s; want output %s", extra, estimate.stdout)
	}

	missing := harness.RunInput(
		"time\tu\n0\t1\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if missing.code != 1 || !strings.Contains(missing.stderr, `missing signal "y"`) ||
		!strings.Contains(missing.stderr, "columns: time, u") {
		t.Fatalf("missing-column nonlinear EKF = %s", missing)
	}

	badRow := harness.RunInput(
		"time\tu\ty\n0\t1\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if badRow.code != 2 || !strings.Contains(badRow.stderr, "TSV row 1 has 2 columns; want 3") {
		t.Fatalf("short-row nonlinear EKF = %s", badRow)
	}

	estimatorPath := filepath.Join(t.TempDir(), "estimator.json")
	estimator := map[string]any{
		"name":              "configured CLI estimator",
		"initialState":      []float64{0.5},
		"processNoise":      map[string]any{"rows": 1, "columns": 1, "values": []float64{0.01}},
		"measurementNoise":  map[string]any{"rows": 1, "columns": 1, "values": []float64{0.1}},
		"initialCovariance": map[string]any{"rows": 1, "columns": 1, "values": []float64{0.2}},
	}
	estimatorJSON, err := json.Marshal(estimator)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(estimatorPath, estimatorJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	configured := harness.RunInput(
		"time\tu\ty\n0\t1\t0.1\n0.1\t0\t0\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1", "--estimator", estimatorPath, "--json",
	)
	if configured.code != 0 || configured.stderr != "" || !strings.Contains(configured.stdout, `"estimatorName":"configured CLI estimator"`) {
		t.Fatalf("configured nonlinear EKF = %s", configured)
	}

	estimator["name"] = "high process noise estimator"
	estimator["processNoise"] = map[string]any{"rows": 1, "columns": 1, "values": []float64{1}}
	highNoiseJSON, err := json.Marshal(estimator)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(estimatorPath, highNoiseJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	highNoise := harness.RunInput(
		"time\tu\ty\n0\t1\t0.1\n0.1\t0\t0\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1", "--estimator", estimatorPath, "--json",
	)
	if highNoise.code != 0 || highNoise.stderr != "" || highNoise.stdout == configured.stdout {
		t.Fatalf("high-noise nonlinear EKF = %s; want different estimate from %s", highNoise, configured.stdout)
	}

	estimator["processNoise"] = map[string]any{"rows": 1, "columns": 1, "values": []float64{-1}}
	invalidNoiseJSON, err := json.Marshal(estimator)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(estimatorPath, invalidNoiseJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	invalidNoise := harness.RunInput(
		"time\tu\ty\n0\t1\t0.1\n",
		"--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1", "--estimator", estimatorPath,
	)
	if invalidNoise.code != 1 || !strings.Contains(invalidNoise.stderr, "EKF process noise Q must be positive semidefinite") {
		t.Fatalf("invalid-noise nonlinear EKF = %s", invalidNoise)
	}

	mismatched := harness.RunInput(
		"time\tu\ty\n0\t1\t0\n", "--server", harness.URL(), "nonlinear", "ekf", "--definition", "cli/decay@1",
	)
	if mismatched.code != 0 || !strings.Contains(mismatched.stdout, "time\tx\n") {
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
