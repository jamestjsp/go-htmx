package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type analysisChannelClient struct {
	BlockID int64  `json:"blockId"`
	Port    int    `json:"port"`
	Channel int    `json:"channel"`
	Name    string `json:"name"`
}

type analysisRecordClient struct {
	Stale bool `json:"stale"`
}

type analysisWorkspaceClient struct {
	FlowID         int64                   `json:"flowId"`
	ModelUpdatedAt string                  `json:"modelUpdatedAt"`
	Inputs         []analysisChannelClient `json:"inputs"`
	Outputs        []analysisChannelClient `json:"outputs"`
	SelectedInput  analysisChannelClient   `json:"selectedInput"`
	SelectedOutput analysisChannelClient   `json:"selectedOutput"`
	Dynamics       *analysisRecordClient   `json:"dynamics"`
	Frequency      *analysisRecordClient   `json:"frequency"`
	Loop           *analysisRecordClient   `json:"loop"`
}

type analysisRequestClient struct {
	Intent               string               `json:"intent"`
	Input                analysisChannelRef   `json:"input,omitempty"`
	Output               analysisChannelRef   `json:"output,omitempty"`
	Inputs               []analysisChannelRef `json:"inputs,omitempty"`
	Outputs              []analysisChannelRef `json:"outputs,omitempty"`
	FrequencyAllChannels bool                 `json:"frequencyAllChannels,omitempty"`
	BaseStep             float64              `json:"baseStep,omitempty"`
	StepHorizon          float64              `json:"stepHorizon,omitempty"`
	Points               int                  `json:"points,omitempty"`
}

type analysisChannelRef struct {
	BlockID int64 `json:"blockId"`
	Port    int   `json:"port"`
	Channel int   `json:"channel"`
}

func newAnalysisCommand() *command {
	return &command{
		name: "analyze", summary: "Discover channels and run control analyses", children: []*command{
			newCommand("channels", "List selectable analysis channels", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runAnalyzeChannels(ctx, client, args, options, stdout)
			}),
			newCommand("dynamics", "Run a dynamics analysis", analysisCommandFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runAnalyzeRun(ctx, client, args, options, stdout, "dynamics")
			}),
			newCommand("frequency", "Run a frequency analysis", analysisCommandFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runAnalyzeRun(ctx, client, args, options, stdout, "frequency")
			}),
			newCommand("loop", "Run a loop analysis", analysisCommandFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runAnalyzeRun(ctx, client, args, options, stdout, "loop")
			}),
			newCommand("show", "Show cached analyses", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runAnalyzeShow(ctx, client, args, options, stdout, stderr)
			}),
		},
	}
}

func analysisCommandFlags() []commandFlag {
	return []commandFlag{
		documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("input", "ref", "", "input channel (block:port:channel)"), documentedStringFlag("output", "ref", "", "output channel (block:port:channel)"), documentedFloat64Flag("base-step", "seconds", 0, "base simulation step in seconds"), documentedFloat64Flag("step-horizon", "seconds", 0, "step experiment horizon in seconds"), documentedFloat64Flag("horizon", "seconds", 0, "step experiment horizon in seconds"), documentedIntFlag("points", "count", 0, "frequency grid points"), documentedBoolFlag("all-channels", "analyze every selectable input and output"), documentedBoolFlag("json", "write machine-readable output"),
	}
}

func runAnalyzeChannels(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab analyze channels: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab analyze channels: unexpected argument %q", args[0])
	}
	raw, err := getAnalysisWorkspace(ctx, client, flowID)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var workspace analysisWorkspaceClient
	if err := json.Unmarshal(raw, &workspace); err != nil {
		return fmt.Errorf("decode analysis channels: %w", err)
	}
	printAnalysisChannels(stdout, "Inputs", workspace.Inputs)
	printAnalysisChannels(stdout, "Outputs", workspace.Outputs)
	return nil
}

func runAnalyzeRun(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer, intent string) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	inputText := options.commandString("input")
	outputText := options.commandString("output")
	baseStep := options.commandFloat64("base-step")
	stepHorizon := options.commandFloat64("step-horizon")
	if stepHorizon == 0 {
		stepHorizon = options.commandFloat64("horizon")
	}
	points := options.commandInt("points")
	allChannels := options.commandBool("all-channels")
	if flowID <= 0 {
		return usagef("processlab analyze %s: --flow is required", intent)
	}
	if len(args) != 0 {
		return usagef("processlab analyze %s: unexpected argument %q", intent, args[0])
	}
	if intent == "frequency" && allChannels && (inputText != "" || outputText != "") {
		return usagef("processlab analyze frequency: --all-channels cannot be combined with --input or --output")
	}
	if intent != "frequency" || !allChannels {
		if inputText == "" || outputText == "" {
			return usagef("processlab analyze %s: --input and --output are required", intent)
		}
	}
	input, err := parseAnalysisChannelRef(inputText)
	if err != nil && inputText != "" {
		return usagef("processlab analyze %s: invalid --input: %v", intent, err)
	}
	output, err := parseAnalysisChannelRef(outputText)
	if err != nil && outputText != "" {
		return usagef("processlab analyze %s: invalid --output: %v", intent, err)
	}
	request := analysisRequestClient{
		Intent: intent, Input: input, Output: output,
		BaseStep: baseStep, StepHorizon: stepHorizon, Points: points,
		FrequencyAllChannels: allChannels,
	}
	raw, err := runAnalysisRequest(ctx, client, flowID, request)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	return writeIndentedJSON(stdout, raw)
}

func runAnalyzeShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab analyze show: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab analyze show: unexpected argument %q", args[0])
	}
	raw, err := getAnalysisWorkspace(ctx, client, flowID)
	if err != nil {
		return err
	}
	var workspace analysisWorkspaceClient
	if err := json.Unmarshal(raw, &workspace); err != nil {
		return fmt.Errorf("decode cached analysis: %w", err)
	}
	if workspace.Dynamics != nil && workspace.Dynamics.Stale ||
		workspace.Frequency != nil && workspace.Frequency.Stale ||
		workspace.Loop != nil && workspace.Loop.Stale {
		fmt.Fprintln(stderr, "warning: cached analysis is stale because the flowsheet changed after it ran")
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	return writeIndentedJSON(stdout, raw)
}

func getAnalysisWorkspace(ctx context.Context, client *apiClient, flowID int64) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/analyses", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func runAnalysisRequest(ctx context.Context, client *apiClient, flowID int64, request analysisRequestClient) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/flows/"+strconv.FormatInt(flowID, 10)+"/analyses", request, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func parseAnalysisChannelRef(value string) (analysisChannelRef, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return analysisChannelRef{}, fmt.Errorf("want block:port:channel")
	}
	blockID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || blockID <= 0 {
		return analysisChannelRef{}, fmt.Errorf("block id must be a positive integer")
	}
	port, err := strconv.Atoi(parts[1])
	if err != nil || port < 0 {
		return analysisChannelRef{}, fmt.Errorf("port must be a non-negative integer")
	}
	channel, err := strconv.Atoi(parts[2])
	if err != nil || channel < 0 {
		return analysisChannelRef{}, fmt.Errorf("channel must be a non-negative integer")
	}
	return analysisChannelRef{BlockID: blockID, Port: port, Channel: channel}, nil
}

func printAnalysisChannels(w io.Writer, title string, channels []analysisChannelClient) {
	fmt.Fprintln(w, title+":")
	for _, channel := range channels {
		fmt.Fprintf(w, "  %d:%d:%d\t%s\n", channel.BlockID, channel.Port, channel.Channel, channel.Name)
	}
}

func writeIndentedJSON(w io.Writer, raw json.RawMessage) error {
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return fmt.Errorf("format JSON response: %w", err)
	}
	indented.WriteByte('\n')
	_, err := w.Write(indented.Bytes())
	return err
}
