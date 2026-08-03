package main

import (
	"context"
	"encoding/json"
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

func documentedStringListFlag(name, typeName, usage string) commandFlag {
	var value repeatedStringFlag
	return commandFlag{
		name: name, typeName: typeName, usage: usage,
		register: func(set *flag.FlagSet) { set.Var(&value, name, usage) },
		value:    func() any { return []string(value) },
	}
}

type parameterSweepRequestClient struct {
	BlockID  int64                    `json:"blockId"`
	Sweep    studio.SweepSpec         `json:"sweep"`
	Analysis studio.SweepAnalysisSpec `json:"analysis"`
}

func newSweepCommand() *command {
	return &command{
		name: "sweep", summary: "Run catalog-backed parameter sweeps", children: []*command{
			newCommand("run", "Run catalog-backed parameter sweeps", []commandFlag{
				documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedInt64Flag("block", "id", 0, "block id whose parameter is swept"), documentedStringListFlag("axis", "string", "parameter=start:stop:count or parameter=value,value,...; repeatable"), documentedStringFlag("omega", "list", "0.1,1", "comma-separated positive frequency points"), documentedFloat64Flag("step-final", "seconds", 1, "step response final time in seconds"), documentedBoolFlag("json", "write machine-readable output"),
			}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runSweepRun(ctx, client, args, options, stdout)
			}),
		},
	}
}

func runSweepRun(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	blockID := options.commandInt64("block")
	axisValues := options.commandStrings("axis")
	omegaText := options.commandString("omega")
	if omegaText == "" {
		omegaText = "0.1,1"
	}
	stepFinal := options.commandFloat64("step-final")
	if flowID <= 0 || blockID <= 0 || len(axisValues) == 0 {
		return usagef("processlab sweep run: --flow, --block, and at least one --axis are required")
	}
	if len(args) != 0 {
		return usagef("processlab sweep run: unexpected argument %q", args[0])
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
