package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/jamestjsp/process-lab/internal/studio"
)

const maxNonlinearCLIInputBytes = 1 << 20

type nonlinearTSV struct {
	times        []float64
	inputs       [][]float64
	measurements [][]float64
}

type nonlinearTSVSchemaError struct {
	message string
}

func (e nonlinearTSVSchemaError) Error() string {
	return e.message
}

func (e nonlinearTSVSchemaError) ExitCode() int {
	return 1
}

func newNonlinearCommand() *command {
	return &command{
		name: "nonlinear", summary: "Register, linearize, and estimate nonlinear models", children: []*command{
			newCommand("register", "Register a persisted nonlinear definition", []commandFlag{documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return runNonlinearRegister(ctx, options, args, stdout)
			}),
			newCommand("linearize", "Linearize a nonlinear definition", []commandFlag{documentedStringFlag("definition", "key@version", "", "definition reference (key@version)"), documentedStringFlag("operating-point", "path", "", "JSON linearization request file"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				return runNonlinearLinearize(ctx, options, args, stdout)
			}),
			newCommand("ekf", "Estimate a nonlinear model with an EKF", []commandFlag{documentedStringFlag("definition", "key@version", "", "definition reference (key@version)"), documentedStringFlag("estimator", "path", "", "JSON EKF estimator; omitted uses identity Q, R, P0, and zero initial state"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				return runNonlinearEKF(ctx, options, args, stdout, stderr)
			}),
		},
	}
}

func runNonlinearRegister(ctx context.Context, options globalOptions, args []string, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	if len(args) != 0 {
		return usagef("processlab nonlinear register: unexpected argument %q", args[0])
	}
	document, err := readJSONDocument(os.Stdin, "nonlinear definition stdin")
	if err != nil {
		return usagef("processlab nonlinear register: %v", err)
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/nonlinear/definitions", document, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var definition studio.NonlinearDefinition
	if err := json.Unmarshal(raw, &definition); err != nil {
		return fmt.Errorf("decode nonlinear definition: %w", err)
	}
	fmt.Fprintf(stdout, "%s@%d\n", definition.Ref.Key, definition.Ref.Version)
	return nil
}

func runNonlinearLinearize(ctx context.Context, options globalOptions, args []string, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	definitionText := options.commandString("definition")
	operatingPointPath := options.commandString("operating-point")
	if len(args) != 0 {
		return usagef("processlab nonlinear linearize: unexpected argument %q", args[0])
	}
	definitionRef, err := parseNonlinearDefinitionRef(definitionText)
	if err != nil {
		return usagef("processlab nonlinear linearize: %v", err)
	}
	if operatingPointPath == "" {
		return usagef("processlab nonlinear linearize: --operating-point is required")
	}
	var request studio.NonlinearLinearizationRequest
	if err := readJSONFile(operatingPointPath, &request); err != nil {
		return usagef("processlab nonlinear linearize: %v", err)
	}
	request.OperatingPoint.Definition = definitionRef
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/nonlinear/linearizations", request, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var candidate studio.NonlinearLinearizationCandidate
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return fmt.Errorf("decode nonlinear linearization: %w", err)
	}
	printNonlinearLinearization(stdout, candidate)
	return nil
}

func runNonlinearEKF(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	definitionText := options.commandString("definition")
	estimatorPath := options.commandString("estimator")
	if len(args) != 0 {
		return usagef("processlab nonlinear ekf: unexpected argument %q", args[0])
	}
	definitionRef, err := parseNonlinearDefinitionRef(definitionText)
	if err != nil {
		return usagef("processlab nonlinear ekf: %v", err)
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	path := "/nonlinear/definitions?" + url.Values{
		"key": {definitionRef.Key}, "version": {strconv.Itoa(definitionRef.Version)},
	}.Encode()
	var definition studio.NonlinearDefinition
	if err := client.request(ctx, http.MethodGet, path, nil, &definition); err != nil {
		return err
	}
	tsv, err := readNonlinearTSV(os.Stdin, definition.InputNames, definition.OutputNames)
	if err != nil {
		var schemaError nonlinearTSVSchemaError
		if errors.As(err, &schemaError) {
			return fmt.Errorf("processlab nonlinear ekf: %w", err)
		}
		return usagef("processlab nonlinear ekf: %v", err)
	}
	var estimator *studio.NonlinearEKFDefinition
	if estimatorPath != "" {
		configured := studio.NonlinearEKFDefinition{}
		if err := readNonlinearEstimatorFile(estimatorPath, &configured); err != nil {
			return usagef("processlab nonlinear ekf: %v", err)
		}
		estimator = &configured
	}
	request, err := nonlinearEKFRequest(definition, definitionRef, tsv, estimator)
	if err != nil {
		return err
	}
	if estimator == nil {
		fmt.Fprintln(stderr, "Using identity Q, R, and P0 covariances with a zero initial state; pass --estimator to configure the EKF.")
	}
	var raw json.RawMessage
	if err := client.request(ctx, http.MethodPost, "/nonlinear/ekf", request, &raw); err != nil {
		return err
	}
	if jsonOutput {
		return writeRawJSON(stdout, raw)
	}
	var run studio.NonlinearEKFRun
	if err := json.Unmarshal(raw, &run); err != nil {
		return fmt.Errorf("decode nonlinear EKF: %w", err)
	}
	printNonlinearEKF(stdout, tsv.times, run)
	return nil
}

func parseNonlinearDefinitionRef(value string) (studio.NonlinearDefinitionRef, error) {
	trimmed := strings.TrimSpace(value)
	separator := strings.LastIndexByte(trimmed, '@')
	if separator <= 0 || separator == len(trimmed)-1 {
		return studio.NonlinearDefinitionRef{}, fmt.Errorf("--definition must be key@version")
	}
	version, err := strconv.Atoi(trimmed[separator+1:])
	if err != nil || version <= 0 {
		return studio.NonlinearDefinitionRef{}, fmt.Errorf("--definition version must be a positive integer")
	}
	return studio.NonlinearDefinitionRef{Key: trimmed[:separator], Version: version}, nil
}

func readJSONDocument(reader io.Reader, name string) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxNonlinearCLIInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(body) > maxNonlinearCLIInputBytes {
		return nil, fmt.Errorf("%s exceeds the %d-byte limit", name, maxNonlinearCLIInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var document json.RawMessage
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%s must contain valid JSON", name)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON value", name)
	}
	return document, nil
}

func readJSONFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read operating point %q: %w", path, err)
	}
	defer file.Close()
	document, err := readJSONDocument(file, "operating point")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(document, destination); err != nil {
		return fmt.Errorf("operating point must match the linearization request: %w", err)
	}
	return nil
}

func readNonlinearEstimatorFile(path string, destination *studio.NonlinearEKFDefinition) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read estimator %q: %w", path, err)
	}
	defer file.Close()
	document, err := readJSONDocument(file, "estimator")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(document, destination); err != nil {
		return fmt.Errorf("estimator must match the EKF definition: %w", err)
	}
	return nil
}

func readNonlinearTSV(reader io.Reader, inputNames, outputNames []string) (nonlinearTSV, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxNonlinearCLIInputBytes+1))
	if err != nil {
		return nonlinearTSV{}, fmt.Errorf("read TSV input: %w", err)
	}
	if len(body) > maxNonlinearCLIInputBytes {
		return nonlinearTSV{}, fmt.Errorf("TSV input exceeds the %d-byte limit", maxNonlinearCLIInputBytes)
	}
	decoder := csv.NewReader(bytes.NewReader(body))
	decoder.Comma = '\t'
	decoder.FieldsPerRecord = -1
	decoder.TrimLeadingSpace = false
	header, err := decoder.Read()
	if err == io.EOF {
		return nonlinearTSV{}, fmt.Errorf("TSV input requires a header and at least one data row")
	}
	if err != nil {
		return nonlinearTSV{}, fmt.Errorf("read TSV header: %w", err)
	}
	if len(header) < 2 || strings.TrimSpace(header[0]) != "time" {
		return nonlinearTSV{}, fmt.Errorf("TSV header must start with time and contain signal columns")
	}
	headerNames := make([]string, len(header))
	seen := make(map[string]struct{}, len(header))
	for index, name := range header {
		name = strings.TrimSpace(name)
		if name == "" {
			return nonlinearTSV{}, fmt.Errorf("TSV header column %d is empty", index+1)
		}
		if _, exists := seen[name]; exists {
			return nonlinearTSV{}, fmt.Errorf("TSV header repeats column %q", name)
		}
		seen[name] = struct{}{}
		headerNames[index] = name
	}
	columns := make(map[string]int, len(headerNames))
	for index, name := range headerNames {
		columns[name] = index
	}
	inputColumns, err := nonlinearTSVColumns(columns, inputNames, headerNames)
	if err != nil {
		return nonlinearTSV{}, err
	}
	measurementColumns, err := nonlinearTSVColumns(columns, outputNames, headerNames)
	if err != nil {
		return nonlinearTSV{}, err
	}
	result := nonlinearTSV{}
	for rowIndex := 1; ; rowIndex++ {
		row, err := decoder.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nonlinearTSV{}, fmt.Errorf("read TSV row %d: %w", rowIndex, err)
		}
		if len(row) == 0 {
			continue
		}
		if len(row) != len(header) {
			return nonlinearTSV{}, fmt.Errorf("TSV row %d has %d columns; want %d", rowIndex, len(row), len(header))
		}
		values := make([]float64, len(row))
		for column, text := range row {
			value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return nonlinearTSV{}, fmt.Errorf("TSV row %d column %d is not a finite number", rowIndex, column+1)
			}
			values[column] = value
		}
		result.times = append(result.times, values[0])
		input := make([]float64, len(inputColumns))
		for index, column := range inputColumns {
			input[index] = values[column]
		}
		measurement := make([]float64, len(measurementColumns))
		for index, column := range measurementColumns {
			measurement[index] = values[column]
		}
		result.inputs = append(result.inputs, input)
		result.measurements = append(result.measurements, measurement)
	}
	if len(result.inputs) == 0 {
		return nonlinearTSV{}, fmt.Errorf("TSV input requires at least one data row")
	}
	return result, nil
}

func nonlinearTSVColumns(columns map[string]int, names, header []string) ([]int, error) {
	result := make([]int, len(names))
	for index, name := range names {
		column, ok := columns[name]
		if !ok {
			return nil, nonlinearTSVSchemaError{message: fmt.Sprintf(
				"TSV header is missing signal %q; columns: %s", name, strings.Join(header, ", "),
			)}
		}
		result[index] = column
	}
	return result, nil
}

func nonlinearEKFRequest(
	definition studio.NonlinearDefinition,
	ref studio.NonlinearDefinitionRef,
	tsv nonlinearTSV,
	configured *studio.NonlinearEKFDefinition,
) (studio.NonlinearEKFRunRequest, error) {
	var estimator studio.NonlinearEKFDefinition
	if configured != nil {
		estimator = *configured
	} else {
		n, p := len(definition.StateNames), len(definition.OutputNames)
		q, err := nonlinearIdentityMatrix(n)
		if err != nil {
			return studio.NonlinearEKFRunRequest{}, err
		}
		r, err := nonlinearIdentityMatrix(p)
		if err != nil {
			return studio.NonlinearEKFRunRequest{}, err
		}
		p0, err := nonlinearIdentityMatrix(n)
		if err != nil {
			return studio.NonlinearEKFRunRequest{}, err
		}
		estimator = studio.NonlinearEKFDefinition{
			Name: "processlab nonlinear EKF", InitialState: make([]float64, n),
			ProcessNoise: q, MeasurementNoise: r, InitialCovariance: p0,
		}
	}
	estimator.Model = ref
	return studio.NonlinearEKFRunRequest{
		Estimator: estimator,
		Inputs:    tsv.inputs, Measurements: tsv.measurements,
	}, nil
}

func nonlinearIdentityMatrix(size int) (studio.MatrixValue, error) {
	values := make([]float64, size*size)
	for index := range size {
		values[index*size+index] = 1
	}
	return studio.NewMatrixValue(size, size, values)
}

func printNonlinearLinearization(w io.Writer, candidate studio.NonlinearLinearizationCandidate) {
	fmt.Fprintf(w, "definition: %s@%d\n", candidate.Definition.Ref.Key, candidate.Definition.Ref.Version)
	fmt.Fprintf(w, "operating point: %s\n", candidate.OperatingPoint.Name)
	fmt.Fprintf(w, "state names: %s\n", strings.Join(candidate.Definition.StateNames, ", "))
	fmt.Fprintf(w, "input names: %s\n", strings.Join(candidate.Definition.InputNames, ", "))
	fmt.Fprintf(w, "output names: %s\n", strings.Join(candidate.Definition.OutputNames, ", "))
	fmt.Fprintf(w, "equilibrium residual norm: %g\n", candidate.EquilibriumNorm)
	fmt.Fprintln(w, "validity:")
	fmt.Fprintln(w, "direction\tradius\tstate error\toutput error\tstate quadratic ratio\toutput quadratic ratio")
	for _, evidence := range candidate.Validity {
		fmt.Fprintf(w, "%s\t%g\t%g\t%g\t%s\t%s\n",
			evidence.Name, evidence.Radius, evidence.StateErrorNorm, evidence.OutputErrorNorm,
			formatNonlinearRatio(evidence.StateQuadraticRatio), formatNonlinearRatio(evidence.OutputQuadraticRatio),
		)
	}
}

func formatNonlinearRatio(value *float64) string {
	if value == nil {
		return "<n/a>"
	}
	return strconv.FormatFloat(*value, 'g', -1, 64)
}

func printNonlinearEKF(w io.Writer, times []float64, run studio.NonlinearEKFRun) {
	fmt.Fprint(w, "time")
	for _, name := range run.StateNames {
		fmt.Fprintf(w, "\t%s", name)
	}
	fmt.Fprintln(w)
	for index, step := range run.Steps {
		fmt.Fprintf(w, "%g", times[index])
		for _, value := range step.UpdatedState {
			fmt.Fprintf(w, "\t%g", value)
		}
		fmt.Fprintln(w)
	}
}
