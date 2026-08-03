package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDocumentationExamplesAndGeneratedReference(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	examples := [][]string{
		{"help", "--json"},
		{"serve", "--help"},
		{"project", "list"},
		{"flow", "list"},
		{"block", "list", "--flow", "1"},
		{"wire", "list", "--flow", "1"},
		{"sim", "run", "--flow", "1", "--duration", "1", "--sample-time", "0.1"},
		{"analyze", "channels", "--flow", "1"},
		{"roles", "show", "--flow", "1"},
		{"sweep", "run", "--help"},
		{"controller", "pid", "--help"},
		{"ident", "estimate", "--help"},
		{"study", "show", "--help"},
		{"nonlinear", "register", "--help"},
		{"export", "--flow", "1"},
		{"log", "--flow", "1"},
	}
	for _, example := range examples {
		result := append([]string{"--server", harness.URL()}, example...)
		got := harness.Run(result...)
		if got.code != 0 || got.stderr != "" {
			t.Fatalf("CLI example %q = %s", strings.Join(example, " "), got)
		}
	}

	if result := harness.Run("--server", harness.URL(), "--timeout", "30s", "project", "list"); result.code != 0 || result.stderr != "" {
		t.Fatalf("server resolution example = %s", result)
	}
	if result := harness.Run("--server", harness.URL(), "project", "list", "--json"); result.code != 0 || result.stderr != "" {
		t.Fatalf("JSON project example = %s", result)
	}
	if result := harness.Run("--server", harness.URL(), "sim", "show", "--flow", "1", "--json"); result.code != 0 || result.stderr != "" {
		t.Fatalf("JSON simulation example = %s", result)
	}

	definition := map[string]any{
		"ref":  map[string]any{"key": "docs/decay", "version": 1},
		"name": "Documentation decay", "stateNames": []string{"x"},
		"inputNames": []string{"u"}, "outputNames": []string{"y"},
		"dynamics": []string{"-0.1*x + u"}, "outputs": []string{"x"},
		"sampleTime": 0.1,
	}
	definitionJSON, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if result := harness.RunInput(string(definitionJSON), "--server", harness.URL(), "nonlinear", "register"); result.code != 0 || result.stderr != "" {
		t.Fatalf("documentation nonlinear register example = %s", result)
	}
	operatingPointPath := filepath.Join(t.TempDir(), "origin.json")
	operatingPointJSON, err := json.Marshal(map[string]any{
		"operatingPoint": map[string]any{"name": "origin", "state": []float64{0}, "input": []float64{0}},
		"directions":     []map[string]any{{"name": "state", "stateDelta": []float64{1}, "inputDelta": []float64{0}, "radius": 0.1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(operatingPointPath, operatingPointJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if result := harness.Run("--server", harness.URL(), "nonlinear", "linearize", "--definition", "docs/decay@1", "--operating-point", operatingPointPath); result.code != 0 || result.stderr != "" {
		t.Fatalf("documentation nonlinear linearize example = %s", result)
	}
	if result := harness.RunInput("time\tu\ty\n0\t1\t0.1\n", "--server", harness.URL(), "nonlinear", "ekf", "--definition", "docs/decay@1"); result.code != 0 || result.stderr != "" {
		t.Fatalf("documentation nonlinear EKF example = %s", result)
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Dir(filepath.Dir(root))
	generator := filepath.Join(t.TempDir(), "processlab-docs")
	build := exec.Command("go", "build", "-o", generator, "./cmd/processlab-docs")
	build.Dir = repositoryRoot
	build.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build documentation generator: %v\n%s", err, output)
	}
	check := exec.Command(
		generator, "--binary", harness.binary, "--server", harness.URL(),
		"--document", filepath.Join(repositoryRoot, "docs/cli.md"), "--check",
	)
	check.Dir = repositoryRoot
	var stdout, stderr bytes.Buffer
	check.Stdout = &stdout
	check.Stderr = &stderr
	if err := check.Run(); err != nil {
		t.Fatalf("generated CLI reference check: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var help commandHelpJSON
	result := harness.Run("--server", harness.URL(), "help", "--json")
	if err := json.Unmarshal([]byte(result.stdout), &help); err != nil {
		t.Fatalf("decode command help for docs: %v", err)
	}
	if len(help.Commands) != 16 {
		t.Fatalf("documented command tree has %d commands, want 16", len(help.Commands))
	}
}
