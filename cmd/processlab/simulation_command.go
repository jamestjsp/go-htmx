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
)

type simulationSeriesClient struct {
	Name   string    `json:"name"`
	Values []float64 `json:"values"`
}

type simulationClient struct {
	ID         int64                    `json:"id"`
	Duration   float64                  `json:"duration"`
	SampleTime float64                  `json:"sampleTime"`
	Times      []float64                `json:"times"`
	Series     []simulationSeriesClient `json:"series"`
	Stale      bool                     `json:"stale"`
}

func newSimulationCommand() *command {
	return &command{
		name:      "sim",
		summary:   "Run and inspect flowsheet simulations",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "simulation operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runSimulationCommand(ctx, options, args, stdout, stderr)
		},
	}
}

func runSimulationCommand(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab sim: choose run or show")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "run":
		return runSimulationRun(ctx, client, args[1:], options, stdout)
	case "show":
		return runSimulationShow(ctx, client, args[1:], options, stdout, stderr)
	default:
		return usagef("processlab sim: unknown operation %q; choose run or show", args[0])
	}
}

func runSimulationRun(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow", "--duration", "-duration", "--sample-time", "-sample-time"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("sim run", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	duration := 10.0
	sampleTime := 0.1
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.Float64Var(&duration, "duration", duration, "simulation duration in seconds")
	set.Float64Var(&sampleTime, "sample-time", sampleTime, "sample interval in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab sim run --flow <id> [--duration <seconds>] [--sample-time <seconds>] [--json]")
			return nil
		}
		return usagef("processlab sim run: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab sim run: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab sim run: unexpected argument %q", set.Arg(0))
	}
	input := struct {
		Duration   float64 `json:"duration"`
		SampleTime float64 `json:"sampleTime"`
	}{Duration: duration, SampleTime: sampleTime}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/flows/"+strconv.FormatInt(flowID, 10)+"/simulations", input, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var simulation simulationClient
	if err := json.Unmarshal(raw, &simulation); err != nil {
		return fmt.Errorf("decode simulation: %w", err)
	}
	printSimulationSeries(stdout, simulation)
	return nil
}

func runSimulationShow(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("sim show", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab sim show --flow <id> [--json]")
			return nil
		}
		return usagef("processlab sim show: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab sim show: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab sim show: unexpected argument %q", set.Arg(0))
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodGet, "/flows/"+strconv.FormatInt(flowID, 10)+"/simulations/latest", nil, &raw); err != nil {
		return err
	}
	var simulation simulationClient
	if err := json.Unmarshal(raw, &simulation); err != nil {
		return fmt.Errorf("decode stored simulation: %w", err)
	}
	if simulation.Stale {
		fmt.Fprintf(stderr, "warning: simulation %d is stale because the flowsheet changed after it ran\n", simulation.ID)
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	printSimulationSeries(stdout, simulation)
	return nil
}

func printSimulationSeries(w io.Writer, simulation simulationClient) {
	fmt.Fprint(w, "time")
	for _, series := range simulation.Series {
		fmt.Fprintf(w, "\t%s", series.Name)
	}
	fmt.Fprintln(w)
	for index, timeValue := range simulation.Times {
		fmt.Fprintf(w, "%g", timeValue)
		for _, series := range simulation.Series {
			if index < len(series.Values) {
				fmt.Fprintf(w, "\t%g", series.Values[index])
			} else {
				fmt.Fprint(w, "\t")
			}
		}
		fmt.Fprintln(w)
	}
}
