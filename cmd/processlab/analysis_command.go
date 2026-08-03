package main

import (
	"bytes"
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
		name:      "analyze",
		summary:   "Discover channels and run control analyses",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "analysis operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runAnalyze(ctx, options, args, stdout, stderr)
		},
	}
}

func runAnalyze(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab analyze: choose channels, dynamics, frequency, loop, or show")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "channels":
		return runAnalyzeChannels(ctx, client, args[1:], options, stdout)
	case "dynamics":
		return runAnalyzeRun(ctx, client, args[1:], options, stdout, "dynamics")
	case "frequency":
		return runAnalyzeRun(ctx, client, args[1:], options, stdout, "frequency")
	case "loop":
		return runAnalyzeRun(ctx, client, args[1:], options, stdout, "loop")
	case "show":
		return runAnalyzeShow(ctx, client, args[1:], options, stdout, stderr)
	default:
		return usagef("processlab analyze: unknown operation %q; choose channels, dynamics, frequency, loop, or show", args[0])
	}
}

func runAnalyzeChannels(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("analyze channels", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab analyze channels --flow <id> [--json]")
			return nil
		}
		return usagef("processlab analyze channels: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab analyze channels: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab analyze channels: unexpected argument %q", set.Arg(0))
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
	valueFlags := []string{"--flow", "-flow", "--input", "-input", "--output", "-output", "--base-step", "-base-step", "--step-horizon", "-step-horizon", "--horizon", "-horizon", "--points", "-points"}
	boolFlags := []string{"--json", "-json", "--all-channels", "-all-channels"}
	args = moveCommandFlags(args, valueFlags, boolFlags)
	set := flag.NewFlagSet("analyze "+intent, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var inputText, outputText string
	var baseStep, stepHorizon float64
	var points int
	allChannels := false
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&inputText, "input", "", "input channel (block:port:channel)")
	set.StringVar(&outputText, "output", "", "output channel (block:port:channel)")
	set.Float64Var(&baseStep, "base-step", 0, "base simulation step in seconds")
	set.Float64Var(&stepHorizon, "step-horizon", 0, "step experiment horizon in seconds")
	set.Float64Var(&stepHorizon, "horizon", 0, "step experiment horizon in seconds")
	set.IntVar(&points, "points", 0, "frequency grid points")
	set.BoolVar(&allChannels, "all-channels", false, "analyze every selectable input and output")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if intent == "frequency" {
				fmt.Fprintln(stdout, "Usage: processlab analyze frequency --flow <id> (--all-channels | --input <ref> --output <ref>) [--points <n>] [--json]")
			} else {
				fmt.Fprintf(stdout, "Usage: processlab analyze %s --flow <id> --input <ref> --output <ref> [--base-step <seconds>] [--horizon <seconds>] [--json]\n", intent)
			}
			return nil
		}
		return usagef("processlab analyze %s: %v", intent, err)
	}
	if flowID <= 0 {
		return usagef("processlab analyze %s: --flow is required", intent)
	}
	if set.NArg() != 0 {
		return usagef("processlab analyze %s: unexpected argument %q", intent, set.Arg(0))
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
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("analyze show", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab analyze show --flow <id> [--json]")
			return nil
		}
		return usagef("processlab analyze show: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab analyze show: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab analyze show: unexpected argument %q", set.Arg(0))
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
