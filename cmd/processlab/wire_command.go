package main

import (
	"context"
	"encoding/json"
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
	clientRun := func(ctx context.Context, options globalOptions, action func(*apiClient) error) error {
		client, err := newAPIClient(options.server, options.timeout)
		if err != nil {
			return err
		}
		return action(client)
	}
	return &command{
		name: "wire", summary: "Connect and disconnect flowsheet signals", children: []*command{
			newCommand("list", "List signal connections", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error { return runWireList(ctx, client, args, options, stdout) })
			}),
			newCommand("connect", "Connect two signal endpoints", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "source", description: "source endpoint", required: true}, {name: "target", description: "target endpoint", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error { return runWireConnect(ctx, client, args, options, stdout) })
			}),
			newCommand("rm", "Remove signal connections", []commandFlag{documentedInt64Flag("block", "id", 0, "remove every wire connected to this block"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "connection id", description: "connection identifier"}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return clientRun(ctx, options, func(client *apiClient) error { return runWireRemove(ctx, client, args, options, stdout) })
			}),
		},
	}
}

func runWireList(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab wire list: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab wire list: unexpected argument %q", args[0])
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
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab wire connect: --flow is required")
	}
	if len(args) != 2 {
		return usagef("processlab wire connect: expected source and target endpoints")
	}
	sourceID, sourcePort, err := parseWireEndpoint(args[0])
	if err != nil {
		return err
	}
	targetID, targetPort, err := parseWireEndpoint(args[1])
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
	jsonOutput := options.json || options.commandBool("json")
	blockID := options.commandInt64("block")
	if blockID != 0 {
		if len(args) != 0 {
			return usagef("processlab wire rm: --block cannot be combined with a connection id")
		}
		return removeWire(ctx, client, http.MethodDelete, "/blocks/"+strconv.FormatInt(blockID, 10)+"/connections", jsonOutput, stdout)
	}
	if len(args) != 1 {
		return usagef("processlab wire rm: expected a connection id or --block <id>")
	}
	connectionID, err := commandID(args[0], "connection id")
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
