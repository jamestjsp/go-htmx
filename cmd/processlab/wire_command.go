package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type wireRecordClient struct {
	ID             int64    `json:"id"`
	FlowID         int64    `json:"flowId"`
	SourceID       int64    `json:"sourceId"`
	SourceName     string   `json:"sourceName"`
	SourcePort     int      `json:"sourcePort"`
	SourceWidth    int      `json:"sourceWidth"`
	SourceChannels []string `json:"sourceChannels"`
	TargetID       int64    `json:"targetId"`
	TargetName     string   `json:"targetName"`
	TargetPort     int      `json:"targetPort"`
	TargetWidth    int      `json:"targetWidth"`
	TargetChannels []string `json:"targetChannels"`
}

type wireMutationClient struct {
	FlowID  int64 `json:"flowId"`
	Removed int   `json:"removed"`
}

func newWireCommand() *command {
	return &command{
		name:      "wire",
		summary:   "Connect and disconnect flowsheet signals",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "wiring operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runWire(ctx, options, args, stdout, stderr)
		},
	}
}

func runWire(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab wire: choose list, connect, or rm")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		return runWireList(ctx, client, args[1:], options, stdout)
	case "connect":
		return runWireConnect(ctx, client, args[1:], options, stdout)
	case "rm":
		return runWireRemove(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab wire: unknown operation %q; choose list, connect, or rm", args[0])
	}
}

func runWireList(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("wire list", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab wire list --flow <id> [--json]")
			return nil
		}
		return usagef("processlab wire list: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab wire list: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab wire list: unexpected argument %q", set.Arg(0))
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/connections", nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var wires []wireRecordClient
	if err := json.Unmarshal(raw, &wires); err != nil {
		return fmt.Errorf("decode wires: %w", err)
	}
	for _, wire := range wires {
		fmt.Fprintf(stdout, "%d\t%s:%d -> %s:%d\t%d -> %d channels\n",
			wire.ID, wire.SourceName, wire.SourcePort, wire.TargetName, wire.TargetPort,
			wire.SourceWidth, wire.TargetWidth)
	}
	return nil
}

func runWireConnect(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("wire connect", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		return usagef("processlab wire connect: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab wire connect: --flow is required")
	}
	if set.NArg() != 2 {
		return usagef("processlab wire connect: expected source and target endpoints")
	}
	sourceID, sourcePort, err := parseWireEndpoint(set.Arg(0))
	if err != nil {
		return err
	}
	targetID, targetPort, err := parseWireEndpoint(set.Arg(1))
	if err != nil {
		return err
	}
	input := struct {
		SourceID   int64 `json:"sourceId"`
		SourcePort int   `json:"sourcePort"`
		TargetID   int64 `json:"targetId"`
		TargetPort int   `json:"targetPort"`
	}{SourceID: sourceID, SourcePort: sourcePort, TargetID: targetID, TargetPort: targetPort}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/flows/"+strconv.FormatInt(flowID, 10)+"/connections", input, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var wire wireRecordClient
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("decode connected wire: %w", err)
	}
	fmt.Fprintln(stdout, wire.ID)
	return nil
}

func runWireRemove(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--block", "-block"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("wire rm", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var blockID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&blockID, "block", 0, "remove every wire connected to this block")
	if err := set.Parse(args); err != nil {
		return usagef("processlab wire rm: %v", err)
	}
	if blockID != 0 {
		if set.NArg() != 0 {
			return usagef("processlab wire rm: --block cannot be combined with a connection id")
		}
		return removeWire(ctx, client, http.MethodDelete, "/blocks/"+strconv.FormatInt(blockID, 10)+"/connections", jsonOutput, stdout)
	}
	if set.NArg() != 1 {
		return usagef("processlab wire rm: expected a connection id or --block <id>")
	}
	connectionID, err := commandID(set.Arg(0), "connection id")
	if err != nil {
		return err
	}
	return removeWire(ctx, client, http.MethodDelete, "/connections/"+strconv.FormatInt(connectionID, 10), jsonOutput, stdout)
}

func removeWire(ctx context.Context, client *apiClient, method, path string, jsonOutput bool, stdout io.Writer) error {
	var raw json.RawMessage
	if err := client.request(ctx, method, path, nil, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var mutation wireMutationClient
	if err := json.Unmarshal(raw, &mutation); err != nil {
		return fmt.Errorf("decode removed wires: %w", err)
	}
	fmt.Fprintf(stdout, "removed %d connections\n", mutation.Removed)
	return nil
}

func parseWireEndpoint(value string) (int64, int, error) {
	blockPart, portPart, hasPort := strings.Cut(value, ":")
	blockID, err := commandID(blockPart, "block id")
	if err != nil {
		return 0, 0, err
	}
	if !hasPort || portPart == "" {
		return blockID, 0, nil
	}
	if strings.Contains(portPart, ":") {
		return 0, 0, usagef("endpoint %q must use blockID[:port]", value)
	}
	port, err := strconv.Atoi(portPart)
	if err != nil || port < 0 {
		return 0, 0, usagef("port in endpoint %q must be a non-negative integer", value)
	}
	return blockID, port, nil
}
