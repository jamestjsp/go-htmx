package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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

func runFlowDump(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("flow dump", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab flow dump --flow <id> [--json]")
			return nil
		}
		return usagef("processlab flow dump: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab flow dump: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab flow dump: unexpected argument %q", set.Arg(0))
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/document", nil, &raw); err != nil {
		return err
	}
	return writeRawJSON(stdout, raw)
}

func runFlowApply(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json", "--dry-run", "-dry-run"})
	set := flag.NewFlagSet("flow apply", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	dryRun := false
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.BoolVar(&dryRun, "dry-run", false, "show reconciliation without changing the flowsheet")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab flow apply --flow <id> [--dry-run] [--json] < document.json")
			return nil
		}
		return usagef("processlab flow apply: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab flow apply: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab flow apply: unexpected argument %q", set.Arg(0))
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
		fmt.Fprintf(w, "%s:\n", label)
		for _, name := range names {
			fmt.Fprintf(w, "  %s\n", name)
		}
	}
	printChanges("Added", result.Added)
	printChanges("Updated", result.Updated)
	printChanges("Removed", result.Removed)
	fmt.Fprintf(w, "Wires: +%d -%d\n", result.WiresAdded, result.WiresRemoved)
	if !result.Changed {
		fmt.Fprintln(w, "No changes.")
	}
	if result.DryRun {
		fmt.Fprintln(w, "Dry run; no changes applied.")
	}
}
