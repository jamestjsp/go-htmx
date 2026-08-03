package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintFlowApplyResultOmitsEmptySections(t *testing.T) {
	var output bytes.Buffer
	printFlowApplyResult(&output, flowApplyResultClient{})
	if got := output.String(); got != "No changes.\n" {
		t.Fatalf("unchanged result = %q, want only No changes", got)
	}

	output.Reset()
	printFlowApplyResult(&output, flowApplyResultClient{Added: []string{"Feed"}, Changed: true})
	got := output.String()
	if !strings.Contains(got, "Added:\n  Feed\n") || strings.Contains(got, "Updated:") || strings.Contains(got, "Removed:") || strings.Contains(got, "Wires:") {
		t.Fatalf("changed result = %q, want only non-empty sections", got)
	}
}
