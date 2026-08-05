package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestCLIProjectDeleteRequiresForce(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	projectID := requireCLIID(t, harness.Run("--server", harness.URL(), "project", "create", "Operations"))
	withoutForce := harness.Run("--server", harness.URL(), "project", "delete", strconv.FormatInt(projectID, 10))
	if withoutForce.code != 1 || !strings.Contains(withoutForce.stderr, "--force") {
		t.Fatalf("project delete without force = %s", withoutForce)
	}
	if result := harness.Run("--server", harness.URL(), "project", "show", strconv.FormatInt(projectID, 10)); result.code != 0 {
		t.Fatalf("project disappeared after refused delete = %s", result)
	}

	withForce := harness.Run("--server", harness.URL(), "project", "delete", strconv.FormatInt(projectID, 10), "--force")
	if withForce.code != 0 || withForce.stderr != "" {
		t.Fatalf("project delete with force = %s", withForce)
	}
	if result := harness.Run("--server", harness.URL(), "project", "show", strconv.FormatInt(projectID, 10)); result.code != 1 {
		t.Fatalf("deleted project show = %s", result)
	}
}

func TestCLIEventLimitIsBounded(t *testing.T) {
	harness := newCLIHarness(t)
	defer harness.Close()

	result := harness.Run(
		"--server", harness.URL(), "log", "--flow", "1", "--limit", "1000001", "--json",
	)
	if result.code != 1 || !strings.Contains(result.stderr, "event limit") {
		t.Fatalf("excessive log limit = %s", result)
	}
}
