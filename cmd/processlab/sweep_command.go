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

	"github.com/jamestjsp/process-lab/internal/studio"
)

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *repeatedStringFlag) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type parameterSweepRequestClient struct {
	BlockID  int64                    `json:"blockId"`
	Sweep    studio.SweepSpec         `json:"sweep"`
	Analysis studio.SweepAnalysisSpec `json:"analysis"`
}

func newSweepCommand() *command {
	return &command{
		name:      "sweep",
		summary:   "Run catalog-backed parameter sweeps",
		freeform:  true,
		arguments: []commandArgument{{name: "subcommand", description: "sweep operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runSweep(ctx, options, args, stdout, stderr)
		},
	}
}

func runSweep(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab sweep: choose run")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "run":
		return runSweepRun(ctx, client, args[1:], options, stdout)
	default:
		return usagef("processlab sweep: unknown operation %q; choose run", args[0])
	}
}

func runSweepRun(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	args = moveCommandFlags(
		args,
		[]string{"--flow", "-flow", "--block", "-block", "--axis", "-axis", "--omega", "-omega", "--step-final", "-step-final"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("sweep run", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID, blockID int64
	var axisValues repeatedStringFlag
	omegaText := "0.1,1"
	stepFinal := 1.0
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.Int64Var(&blockID, "block", 0, "block id whose parameter is swept")
	set.Var(&axisValues, "axis", "parameter=start:stop:count or parameter=value,value,...; repeatable")
	set.StringVar(&omegaText, "omega", omegaText, "comma-separated positive frequency points")
	set.Float64Var(&stepFinal, "step-final", stepFinal, "step response final time in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab sweep run --flow <id> --block <id> --axis <parameter>=<start>:<stop>:<count> [--axis ...] [--omega <w1,w2,...>] [--step-final <seconds>] [--json]")
			return nil
		}
		return usagef("processlab sweep run: %v", err)
	}
	if flowID <= 0 || blockID <= 0 || len(axisValues) == 0 {
		return usagef("processlab sweep run: --flow, --block, and at least one --axis are required")
	}
	if set.NArg() != 0 {
		return usagef("processlab sweep run: unexpected argument %q", set.Arg(0))
	}
	axes, err := parseSweepAxes(ctx, client, blockID, axisValues)
	if err != nil {
		return err
	}
	omega, err := parseSweepFloatList(omegaText, "omega")
	if err != nil {
		return usagef("processlab sweep run: %v", err)
	}
	request := parameterSweepRequestClient{
		BlockID: blockID,
		Sweep:   studio.SweepSpec{Axes: axes},
		Analysis: studio.SweepAnalysisSpec{
			Omega: omega, StepFinal: stepFinal,
		},
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/flows/"+strconv.FormatInt(flowID, 10)+"/parameter-sweeps", request, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var analysis studio.ParameterSweepAnalysis
	if err := json.Unmarshal(raw, &analysis); err != nil {
		return fmt.Errorf("decode parameter sweep analysis: %w", err)
	}
	printSweepTable(stdout, analysis)
	return nil
}

func parseSweepAxes(ctx context.Context, client *apiClient, blockID int64, values []string) ([]studio.SweepAxis, error) {
	var block blockRecordClient
	if err := client.request(ctx, http.MethodGet, "/blocks/"+strconv.FormatInt(blockID, 10), nil, &block); err != nil {
		return nil, err
	}
	schema, err := requestBlockSchema(ctx, client, block.Kind)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]blockParameterSchemaClient, len(schema.Parameters))
	names := make([]string, 0, len(schema.Parameters))
	for _, field := range schema.Parameters {
		fields[field.Name] = field
		names = append(names, field.Name)
	}
	axes := make([]studio.SweepAxis, 0, len(values))
	for _, raw := range values {
		name, definition, err := parseSweepAxis(raw)
		if err != nil {
			return nil, usagef("processlab sweep run: invalid --axis %q: %v", raw, err)
		}
		field, ok := fields[name]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q for block %d; available parameters: %s", name, blockID, strings.Join(names, ", "))
		}
		if field.Type != "number" {
			return nil, fmt.Errorf("parameter %q for block %d is not a scalar numeric field", name, blockID)
		}
		axes = append(axes, studio.SweepAxis{Parameter: name, Unit: field.Unit, Values: definition})
	}
	return axes, nil
}

func parseSweepAxis(raw string) (string, []float64, error) {
	name, valuesText, ok := strings.Cut(raw, "=")
	name = strings.TrimSpace(name)
	if !ok || name == "" || strings.TrimSpace(valuesText) == "" {
		return "", nil, fmt.Errorf("want parameter=start:stop:count or parameter=value,value,...")
	}
	valuesText = strings.TrimSpace(valuesText)
	if strings.Contains(valuesText, ":") {
		parts := strings.Split(valuesText, ":")
		if len(parts) != 3 {
			return "", nil, fmt.Errorf("grid must use start:stop:count")
		}
		start, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			return "", nil, fmt.Errorf("invalid grid start")
		}
		stop, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			return "", nil, fmt.Errorf("invalid grid stop")
		}
		count, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil || count <= 0 {
			return "", nil, fmt.Errorf("grid count must be positive")
		}
		values := make([]float64, count)
		if count == 1 {
			values[0] = start
		} else {
			for index := range values {
				values[index] = start + (stop-start)*float64(index)/float64(count-1)
			}
		}
		return name, values, nil
	}
	values, err := parseSweepFloatList(valuesText, "axis values")
	return name, values, err
}

func parseSweepFloatList(value, label string) ([]float64, error) {
	parts := strings.Split(value, ",")
	values := make([]float64, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("%s value %d must be a number", label, index+1)
		}
		values[index] = parsed
	}
	return values, nil
}

func printSweepTable(w io.Writer, analysis studio.ParameterSweepAnalysis) {
	timesByIndex := make(map[int]studio.SweepTimeModelSummary, len(analysis.Time.Models))
	for _, summary := range analysis.Time.Models {
		timesByIndex[summary.FlatIndex] = summary
	}
	frequencyWorst := analysis.Frequency.WorstCase.FlatIndex
	timeWorst := analysis.Time.WorstCase.FlatIndex
	fmt.Fprintln(w, "mark\tindex\tcoordinates\tpeak_gain\tpeak_omega\tpeak_absolute\tpeak_time")
	for _, frequency := range analysis.Frequency.Models {
		timeSummary := timesByIndex[frequency.FlatIndex]
		mark := " "
		if frequency.FlatIndex == frequencyWorst && frequency.FlatIndex == timeWorst {
			mark = "*"
		} else if frequency.FlatIndex == frequencyWorst {
			mark = "F"
		} else if frequency.FlatIndex == timeWorst {
			mark = "T"
		}
		fmt.Fprintf(w, "%s\t%d\t%v\t%g\t%g\t%g\t%g\n",
			mark, frequency.FlatIndex, frequency.Coordinates,
			frequency.PeakGain, frequency.PeakOmega,
			timeSummary.PeakAbsolute, timeSummary.PeakTime,
		)
	}
}
