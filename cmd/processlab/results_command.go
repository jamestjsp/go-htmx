package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const resultsSchemaVersion = 1

type resultsExportClient struct {
	SchemaVersion int `json:"schemaVersion"`
}

type eventClient struct {
	ID        int64  `json:"id"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}

func newExportCommand() *command {
	var flowID int64
	var jsonOutput bool
	return &command{
		name:    "export",
		summary: "Export complete flowsheet results",
		flags: []commandFlag{
			{name: "flow", typeName: "id", defaultValue: "0", usage: "flowsheet id", register: func(set *flag.FlagSet) {
				set.Int64Var(&flowID, "flow", 0, "flowsheet id")
			}},
			{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) {
				set.BoolVar(&jsonOutput, "json", false, "write machine-readable output")
			}},
		},
		run: func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
			if len(args) != 0 {
				return usagef("processlab export: unexpected argument %q", args[0])
			}
			if flowID <= 0 {
				return usagef("processlab export: --flow is required")
			}
			return exportResults(ctx, options, flowID, stdout)
		},
	}
}

func exportResults(ctx context.Context, options globalOptions, flowID int64, stdout io.Writer) error {
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.requestRoot(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/results.json", nil, &raw); err != nil {
		return err
	}
	var document resultsExportClient
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Errorf("decode results export: %w", err)
	}
	if document.SchemaVersion != resultsSchemaVersion {
		return fmt.Errorf("processlab export: unsupported results schema version %d", document.SchemaVersion)
	}
	return writeRawJSON(stdout, raw)
}

func newLogCommand() *command {
	var flowID int64
	var limit int
	var jsonOutput bool
	return &command{
		name:    "log",
		summary: "Show recent flowsheet activity",
		flags: []commandFlag{
			{name: "flow", typeName: "id", defaultValue: "0", usage: "flowsheet id", register: func(set *flag.FlagSet) {
				set.Int64Var(&flowID, "flow", 0, "flowsheet id")
			}},
			{name: "limit", typeName: "count", defaultValue: "8", usage: "maximum number of events", register: func(set *flag.FlagSet) {
				set.IntVar(&limit, "limit", 8, "maximum number of events")
			}},
			{name: "json", typeName: "bool", defaultValue: "false", usage: "write machine-readable output", register: func(set *flag.FlagSet) {
				set.BoolVar(&jsonOutput, "json", false, "write machine-readable output")
			}},
		},
		run: func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
			if len(args) != 0 {
				return usagef("processlab log: unexpected argument %q", args[0])
			}
			if flowID <= 0 {
				return usagef("processlab log: --flow is required")
			}
			if limit <= 0 {
				return usagef("processlab log: --limit must be positive")
			}
			return logEvents(ctx, options, flowID, limit, jsonOutput || options.json, stdout)
		},
	}
}

func logEvents(ctx context.Context, options globalOptions, flowID int64, limit int, jsonOutput bool, stdout io.Writer) error {
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/events?limit=" + strconv.Itoa(limit)
	if err := client.request(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var events []eventClient
	if err := json.Unmarshal(raw, &events); err != nil {
		return fmt.Errorf("decode activity events: %w", err)
	}
	for _, event := range events {
		createdAt, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
		if err != nil {
			return fmt.Errorf("decode activity event %d timestamp: %w", event.ID, err)
		}
		fmt.Fprintf(stdout, "%s\t%s\n", createdAt.Format(time.RFC3339Nano), event.Message)
	}
	return nil
}
