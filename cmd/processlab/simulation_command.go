package main

import (
	"context"
	"encoding/json"
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
		name: "sim", summary: "Run and inspect flowsheet simulations", children: []*command{
			newCommand("run", "Run a flowsheet simulation", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedFloat64Flag("duration", "seconds", 10, "simulation duration in seconds"), documentedFloat64Flag("sample-time", "seconds", 0.1, "sample interval in seconds"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runSimulationRun(ctx, client, args, options, stdout)
			}),
			newCommand("show", "Show the latest simulation", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runSimulationShow(ctx, client, args, options, stdout, stderr)
			}),
		},
	}
}

func runSimulationRun(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	duration := options.commandFloat64("duration")
	sampleTime := options.commandFloat64("sample-time")
	if flowID <= 0 {
		return usagef("processlab sim run: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab sim run: unexpected argument %q", args[0])
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
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab sim show: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab sim show: unexpected argument %q", args[0])
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
