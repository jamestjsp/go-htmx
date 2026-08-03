package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

type flowApplyResultClient struct {
	Added        []string `json:"added"`
	Updated      []string `json:"updated"`
	Removed      []string `json:"removed"`
	WiresAdded   int      `json:"wiresAdded"`
	WiresRemoved int      `json:"wiresRemoved"`
	Changed      bool     `json:"changed"`
	DryRun       bool     `json:"dryRun"`
}

type flowApplyResponseClient struct {
	Result flowApplyResultClient `json:"result"`
}

func runFlowDump(ctx context.Context, client *apiClient, options globalOptions, flowID int64, jsonOutput bool, stdout io.Writer) error {
	if flowID <= 0 {
		return usagef("processlab flow dump: --flow is required")
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/document", nil, &raw); err != nil {
		return err
	}
	return writeRawJSON(stdout, raw)
}

func runFlowApply(ctx context.Context, client *apiClient, options globalOptions, flowID int64, jsonOutput, dryRun bool, stdout io.Writer) error {
	if flowID <= 0 {
		return usagef("processlab flow apply: --flow is required")
	}
	document, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read flowsheet document: %w", err)
	}
	if len(document) == 0 || !json.Valid(document) {
		return usagef("processlab flow apply: stdin must contain one valid JSON document")
	}
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/document"
	if dryRun {
		path += "?dry-run=true"
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPut, path, json.RawMessage(document), &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var response flowApplyResponseClient
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode flow apply result: %w", err)
	}
	printFlowApplyResult(stdout, response.Result)
	return nil
}

func printFlowApplyResult(w io.Writer, result flowApplyResultClient) {
	printChanges := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		fmt.Fprintf(w, "%s:\n", label)
		for _, name := range names {
			fmt.Fprintf(w, "  %s\n", name)
		}
	}
	printChanges("Added", result.Added)
	printChanges("Updated", result.Updated)
	printChanges("Removed", result.Removed)
	if result.WiresAdded != 0 || result.WiresRemoved != 0 {
		fmt.Fprintf(w, "Wires: +%d -%d\n", result.WiresAdded, result.WiresRemoved)
	}
	if !result.Changed {
		fmt.Fprintln(w, "No changes.")
	}
	if result.DryRun {
		fmt.Fprintln(w, "Dry run; no changes applied.")
	}
}
