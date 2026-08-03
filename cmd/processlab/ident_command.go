package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type identificationEstimateRequestClient struct {
	Name    string                            `json:"name"`
	Dataset studio.IdentificationDataset      `json:"dataset"`
	Options studio.FrequencyEstimationOptions `json:"options"`
}

type identificationERARequestClient struct {
	Name    string                        `json:"name"`
	Dataset studio.MarkovParameterDataset `json:"dataset"`
	Order   int                           `json:"order"`
}

func newIdentCommand() *command {
	return &command{
		name:      "ident",
		summary:   "Estimate models from measured data",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "identification operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runIdent(ctx, options, args, stdout, stderr)
		},
	}
}

func runIdent(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab ident: choose estimate or era")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "estimate":
		return runIdentEstimate(ctx, client, args[1:], options, stdout)
	case "era":
		return runIdentERA(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab ident: unknown operation %q; choose estimate or era", args[0])
	}
}

func runIdentEstimate(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args,
		[]string{
			"--name", "-name", "--format", "-format", "--sample-time", "-sample-time", "--time-unit", "-time-unit",
			"--input-columns", "-input-columns", "--output-columns", "-output-columns", "--input-names", "-input-names", "--output-names", "-output-names",
			"--input-units", "-input-units", "--output-units", "-output-units", "--preprocessing", "-preprocessing",
			"--training-start", "-training-start", "--training-end", "-training-end", "--validation-start", "-validation-start", "--validation-end", "-validation-end",
			"--method", "-method", "--window", "-window", "--nfft", "-nfft", "--overlap", "-overlap", "--min-coherence", "-min-coherence",
		},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("ident estimate", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var name, format, timeUnit, inputColumns, outputColumns, inputNames, outputNames, inputUnits, outputUnits, preprocessing string
	var sampleTime, minCoherence float64
	var trainingStart, trainingEnd, validationStart, validationEnd int
	var method, window string
	var nfft, overlap int
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.StringVar(&name, "name", "identification", "candidate name")
	set.StringVar(&format, "format", "auto", "input format: auto, json, or csv")
	set.Float64Var(&sampleTime, "sample-time", 0, "sample time in seconds; overrides the input document")
	set.StringVar(&timeUnit, "time-unit", "", "time unit; overrides the input document")
	set.StringVar(&inputColumns, "input-columns", "", "CSV input column names, comma-separated")
	set.StringVar(&outputColumns, "output-columns", "", "CSV output column names, comma-separated")
	set.StringVar(&inputNames, "input-names", "", "input channel names, comma-separated")
	set.StringVar(&outputNames, "output-names", "", "output channel names, comma-separated")
	set.StringVar(&inputUnits, "input-units", "", "input channel units, comma-separated")
	set.StringVar(&outputUnits, "output-units", "", "output channel units, comma-separated")
	set.StringVar(&preprocessing, "preprocessing", "", "none, remove_mean, or linear_detrend")
	set.IntVar(&trainingStart, "training-start", -1, "training range start, inclusive")
	set.IntVar(&trainingEnd, "training-end", -1, "training range end, exclusive")
	set.IntVar(&validationStart, "validation-start", -1, "validation range start, inclusive")
	set.IntVar(&validationEnd, "validation-end", -1, "validation range end, exclusive")
	set.StringVar(&method, "method", string(studio.FrequencyEstimationH1), "frequency estimator: h1 or h2")
	set.StringVar(&window, "window", string(studio.IdentificationWindowHann), "window: rectangular, hann, hamming, or blackman")
	set.IntVar(&nfft, "nfft", 64, "FFT length")
	set.IntVar(&overlap, "overlap", 32, "FFT overlap")
	set.Float64Var(&minCoherence, "min-coherence", 0, "minimum coherence for validation comparisons")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab ident estimate [--sample-time <seconds>] [--format json|csv] < data.json|data.csv")
			return nil
		}
		return usagef("processlab ident estimate: %v", err)
	}
	if set.NArg() != 0 {
		return usagef("processlab ident estimate: unexpected argument %q", set.Arg(0))
	}
	encoded, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read identification data: %w", err)
	}
	dataset, err := parseIdentificationDataset(encoded, format, inputColumns, outputColumns)
	if err != nil {
		return usagef("processlab ident estimate: %v", err)
	}
	if err := applyIdentificationOverrides(&dataset, identificationOverrides{
		SampleTime: sampleTime, TimeUnit: timeUnit, InputNames: inputNames, OutputNames: outputNames,
		InputUnits: inputUnits, OutputUnits: outputUnits, Preprocessing: preprocessing,
		TrainingStart: trainingStart, TrainingEnd: trainingEnd,
		ValidationStart: validationStart, ValidationEnd: validationEnd,
	}); err != nil {
		return usagef("processlab ident estimate: %v", err)
	}
	request := identificationEstimateRequestClient{
		Name: name, Dataset: dataset,
		Options: studio.FrequencyEstimationOptions{
			Method: studio.FrequencyEstimationMethod(method), Window: studio.IdentificationWindow(window),
			NFFT: nfft, Overlap: overlap, MinCoherence: minCoherence,
		},
	}
	var candidate studio.FRDCandidate
	if err := client.request(ctx, http.MethodPost, "/identifications/estimate", request, &candidate); err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(candidate)
	}
	printFRDCandidate(stdout, candidate)
	return nil
}

func runIdentERA(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(args, []string{"--name", "-name", "--order", "-order"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("ident era", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var name string
	var order int
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.StringVar(&name, "name", "era-identification", "candidate name")
	set.IntVar(&order, "order", 1, "identified state-space order")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab ident era --order <n> <markov-data.json>")
			return nil
		}
		return usagef("processlab ident era: %v", err)
	}
	if set.NArg() > 1 {
		return usagef("processlab ident era: expected at most one JSON file")
	}
	var encoded []byte
	var err error
	if set.NArg() == 1 {
		encoded, err = os.ReadFile(set.Arg(0))
	} else {
		encoded, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		return fmt.Errorf("read ERA data: %w", err)
	}
	var dataset studio.MarkovParameterDataset
	if err := json.Unmarshal(encoded, &dataset); err != nil {
		return usagef("processlab ident era: decode JSON: %v", err)
	}
	request := identificationERARequestClient{Name: name, Dataset: dataset, Order: order}
	var candidate studio.ERACandidate
	if err := client.request(ctx, http.MethodPost, "/identifications/era", request, &candidate); err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(candidate)
	}
	fmt.Fprintf(stdout, "candidate: %s\norder: %d\nfit: %.2f%%\n", candidate.Name, candidate.Order, candidate.Fit.FitPercent)
	fmt.Fprintf(stdout, "model: %d states, %d inputs, %d outputs\n", candidate.Order, len(candidate.Model.InputNames), len(candidate.Model.OutputNames))
	return nil
}

type identificationOverrides struct {
	SampleTime                     float64
	TimeUnit                       string
	InputNames, OutputNames        string
	InputUnits, OutputUnits        string
	Preprocessing                  string
	TrainingStart, TrainingEnd     int
	ValidationStart, ValidationEnd int
}

func applyIdentificationOverrides(dataset *studio.IdentificationDataset, overrides identificationOverrides) error {
	if overrides.SampleTime != 0 {
		dataset.SampleTime = overrides.SampleTime
	}
	if overrides.TimeUnit != "" {
		dataset.TimeUnit = overrides.TimeUnit
	}
	for _, override := range []struct {
		value  string
		target *[]string
		label  string
	}{
		{overrides.InputNames, &dataset.InputNames, "input-names"},
		{overrides.OutputNames, &dataset.OutputNames, "output-names"},
		{overrides.InputUnits, &dataset.InputUnits, "input-units"},
		{overrides.OutputUnits, &dataset.OutputUnits, "output-units"},
	} {
		if override.value == "" {
			continue
		}
		values, err := parseIdentificationList(override.value, override.label)
		if err != nil {
			return err
		}
		*override.target = values
	}
	if overrides.Preprocessing != "" {
		dataset.Preprocessing = studio.IdentificationPreprocessing(overrides.Preprocessing)
	}
	if overrides.TrainingStart >= 0 {
		dataset.Split.Training.Start = overrides.TrainingStart
	}
	if overrides.TrainingEnd >= 0 {
		dataset.Split.Training.End = overrides.TrainingEnd
	}
	if overrides.ValidationStart >= 0 {
		dataset.Split.Validation.Start = overrides.ValidationStart
	}
	if overrides.ValidationEnd >= 0 {
		dataset.Split.Validation.End = overrides.ValidationEnd
	}
	_, samples := dataset.Inputs.Dims()
	if dataset.Split.Training.End == 0 && dataset.Split.Validation.End == 0 {
		trainingEnd := samples * 7 / 10
		if trainingEnd < 1 {
			trainingEnd = samples / 2
		}
		dataset.Split = studio.IdentificationSplit{
			Training:   studio.SampleRange{Start: 0, End: trainingEnd},
			Validation: studio.SampleRange{Start: trainingEnd, End: samples},
		}
	}
	if dataset.Preprocessing == "" {
		dataset.Preprocessing = studio.PreprocessingNone
	}
	if dataset.TimeUnit == "" {
		dataset.TimeUnit = "s"
	}
	return nil
}

func parseIdentificationDataset(encoded []byte, format, inputColumns, outputColumns string) (studio.IdentificationDataset, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "auto" {
		trimmed := bytes.TrimSpace(encoded)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			format = "json"
		} else {
			format = "csv"
		}
	}
	switch format {
	case "json":
		var dataset studio.IdentificationDataset
		if err := json.Unmarshal(encoded, &dataset); err != nil {
			return studio.IdentificationDataset{}, fmt.Errorf("decode JSON dataset: %w", err)
		}
		return dataset, nil
	case "csv":
		return parseIdentificationCSV(encoded, inputColumns, outputColumns)
	default:
		return studio.IdentificationDataset{}, fmt.Errorf("format must be auto, json, or csv")
	}
}

func parseIdentificationCSV(encoded []byte, inputColumns, outputColumns string) (studio.IdentificationDataset, error) {
	reader := csv.NewReader(bytes.NewReader(encoded))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return studio.IdentificationDataset{}, fmt.Errorf("decode CSV: %w", err)
	}
	if len(rows) < 2 {
		return studio.IdentificationDataset{}, fmt.Errorf("CSV must contain a header and at least one data row")
	}
	inputs, err := parseIdentificationList(inputColumns, "input-columns")
	if err != nil || len(inputs) == 0 {
		return studio.IdentificationDataset{}, fmt.Errorf("--input-columns must name at least one CSV column")
	}
	outputs, err := parseIdentificationList(outputColumns, "output-columns")
	if err != nil || len(outputs) == 0 {
		return studio.IdentificationDataset{}, fmt.Errorf("--output-columns must name at least one CSV column")
	}
	indices := make(map[string]int, len(rows[0]))
	for index, name := range rows[0] {
		name = strings.TrimSpace(name)
		if name == "" {
			return studio.IdentificationDataset{}, fmt.Errorf("CSV header %d is empty", index+1)
		}
		if _, exists := indices[name]; exists {
			return studio.IdentificationDataset{}, fmt.Errorf("CSV header %q is repeated", name)
		}
		indices[name] = index
	}
	inputData, err := csvColumns(rows[1:], inputs, indices)
	if err != nil {
		return studio.IdentificationDataset{}, fmt.Errorf("CSV inputs: %w", err)
	}
	outputData, err := csvColumns(rows[1:], outputs, indices)
	if err != nil {
		return studio.IdentificationDataset{}, fmt.Errorf("CSV outputs: %w", err)
	}
	inputMatrix, err := studio.NewMatrixValue(len(inputs), len(rows)-1, inputData)
	if err != nil {
		return studio.IdentificationDataset{}, err
	}
	outputMatrix, err := studio.NewMatrixValue(len(outputs), len(rows)-1, outputData)
	if err != nil {
		return studio.IdentificationDataset{}, err
	}
	return studio.IdentificationDataset{
		Inputs: inputMatrix, Outputs: outputMatrix,
		InputNames: inputs, OutputNames: outputs,
		InputUnits: filledIdentificationUnits(len(inputs)), OutputUnits: filledIdentificationUnits(len(outputs)),
	}, nil
}

func csvColumns(rows [][]string, names []string, indices map[string]int) ([]float64, error) {
	values := make([]float64, 0, len(rows)*len(names))
	for rowIndex, row := range rows {
		for _, name := range names {
			column, ok := indices[name]
			if !ok {
				return nil, fmt.Errorf("column %q is not in the header", name)
			}
			if column >= len(row) {
				return nil, fmt.Errorf("row %d is missing column %q", rowIndex+2, name)
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(row[column]), 64)
			if err != nil {
				return nil, fmt.Errorf("row %d column %q is not numeric", rowIndex+2, name)
			}
			values = append(values, value)
		}
	}
	// MatrixValue is channel-major; transpose the row-major CSV collection.
	transposed := make([]float64, len(values))
	for sample := range rows {
		for channel := range names {
			transposed[channel*len(rows)+sample] = values[sample*len(names)+channel]
		}
	}
	return transposed, nil
}

func parseIdentificationList(raw, label string) ([]string, error) {
	parts := strings.Split(raw, ",")
	values := make([]string, len(parts))
	for index, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("%s item %d is empty", label, index+1)
		}
		values[index] = value
	}
	if len(values) == 1 && values[0] == "" {
		return nil, nil
	}
	return values, nil
}

func filledIdentificationUnits(count int) []string {
	units := make([]string, count)
	for index := range units {
		units[index] = "1"
	}
	return units
}

func printFRDCandidate(w io.Writer, candidate studio.FRDCandidate) {
	samples, outputs, inputs := candidate.Model.Response.Dims()
	fmt.Fprintf(w, "candidate: %s\nmodel: FRD with %d frequency samples, %d inputs, %d outputs\n", candidate.Name, samples, inputs, outputs)
	if candidate.Fit != nil {
		fmt.Fprintf(w, "validation fit: %.2f%% (%.6g relative RMS, %d bins)\n", candidate.Fit.FitPercent, candidate.Fit.RelativeRMS, candidate.Fit.ComparedBins)
	}
	if candidate.Diagnostics != nil {
		fmt.Fprintf(w, "excitation: rank %d/%d, coherence %.3g..%.3g\n", candidate.Diagnostics.InputRank, candidate.Diagnostics.InputChannels, candidate.Diagnostics.MinimumCoherence, candidate.Diagnostics.MeanCoherence)
	}
}
