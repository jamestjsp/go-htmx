package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/jamestjsp/controlsys"
	"github.com/jamestjsp/process-lab/internal/studio"
)

type controllerCandidateRecordClient struct {
	ID            string                           `json:"id"`
	FlowID        int64                            `json:"flowId"`
	Kind          string                           `json:"kind"`
	Review        studio.ControllerCandidateReview `json:"review"`
	Applied       bool                             `json:"applied"`
	UndoAvailable bool                             `json:"undoAvailable"`
}

type controllerActionRecordClient struct {
	ID     string `json:"id"`
	FlowID int64  `json:"flowId"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
}

type controllerBlockChangeClient struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters"`
}

type controllerActionOutputClient struct {
	controllerActionRecordClient
	Changes []controllerBlockChangeClient `json:"changes"`
}

type pidCandidateRequestClient struct {
	studio.PIDDesignRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type stateFeedbackCandidateRequestClient struct {
	studio.StateFeedbackRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type estimatorCandidateRequestClient struct {
	studio.EstimatorDesignRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type observerCandidateRequestClient struct {
	studio.ObserverRegulatorRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type robustCandidateRequestClient struct {
	studio.RobustSynthesisRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

type tuningCandidateRequestClient struct {
	studio.ControllerTuningRequest
	ReviewHorizon float64 `json:"reviewHorizon,omitempty"`
}

func newControllerCommand() *command {
	return &command{
		name: "controller", summary: "Design, tune, and review controller candidates", children: []*command{
			newCommand("pid", "Design a PID controller", controllerPIDFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runControllerPID(ctx, client, args, options, stdout, stderr)
			}),
			{
				name: "state", summary: "Design state-space controllers", children: []*command{
					newCommand("feedback", "Design state feedback", controllerStateFeedbackFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
						client, err := newAPIClient(options.server, options.timeout)
						if err != nil {
							return err
						}
						return runControllerStateFeedback(ctx, client, args, options, stdout, stderr)
					}),
					newCommand("estimator", "Design a state estimator", controllerEstimatorFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
						client, err := newAPIClient(options.server, options.timeout)
						if err != nil {
							return err
						}
						return runControllerEstimator(ctx, client, args, options, stdout, stderr)
					}),
					newCommand("observer", "Design an observer regulator", controllerObserverFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
						client, err := newAPIClient(options.server, options.timeout)
						if err != nil {
							return err
						}
						return runControllerObserver(ctx, client, args, options, stdout, stderr)
					}),
				},
			},
			newCommand("robust", "Design a robust controller", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("method", "string", string(studio.RobustSynthesisH2), "robust method: h2 or hinf"), documentedFloat64Flag("base-step", "seconds", 0, "simulation base step in seconds"), documentedFloat64Flag("review-horizon", "seconds", 0, "review horizon in seconds"), documentedBoolFlag("json", "write machine-readable output")}, nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runControllerRobust(ctx, client, args, options, stdout, stderr)
			}),
			newCommand("tune", "Tune a controller", controllerTuneFlags(), nil, func(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runControllerTune(ctx, client, args, options, stdout, stderr)
			}),
			newCommand("review", "Review a controller candidate", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "candidate id", description: "candidate identifier", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runControllerReview(ctx, client, args, options, stdout)
			}),
			newCommand("apply", "Apply a controller candidate", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "candidate id", description: "candidate identifier", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runControllerAction(ctx, client, args, options, stdout, "apply")
			}),
			newCommand("undo", "Undo a controller candidate", []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedBoolFlag("json", "write machine-readable output")}, []commandArgument{{name: "candidate id", description: "candidate identifier", required: true}}, func(ctx context.Context, options globalOptions, args []string, stdout, _ io.Writer) error {
				client, err := newAPIClient(options.server, options.timeout)
				if err != nil {
					return err
				}
				return runControllerAction(ctx, client, args, options, stdout, "undo")
			}),
		},
	}
}

func controllerPIDFlags() []commandFlag {
	return []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("type", "string", "PID", "PID tuning type: P, PI, PD, or PID"), documentedFloat64Flag("crossover", "hz", 0, "target crossover frequency"), documentedFloat64Flag("phase-margin", "degrees", 0, "target phase margin in degrees"), documentedFloat64Flag("review-horizon", "seconds", 0, "review horizon in seconds"), documentedFloat64Flag("base-step", "seconds", 0, "simulation base step in seconds"), documentedStringFlag("setpoint-weight", "float", "", "PID2 setpoint weight"), documentedStringFlag("derivative-weight", "float", "", "PID2 derivative weight"), documentedBoolFlag("json", "write machine-readable output")}
}

func controllerStateFeedbackFlags() []commandFlag {
	return []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("method", "string", string(studio.StateFeedbackLQR), "state feedback method"), documentedStringFlag("q", "matrix", "", "state cost matrix"), documentedStringFlag("r", "matrix", "", "control cost matrix"), documentedStringFlag("regulated-output", "matrix", "", "regulated output matrix"), documentedStringFlag("poles", "list", "", "comma-separated real or complex poles"), documentedFloat64Flag("sample-time", "seconds", 0, "sample time in seconds"), documentedFloat64Flag("base-step", "seconds", 0, "simulation base step in seconds"), documentedFloat64Flag("review-horizon", "seconds", 0, "review horizon in seconds"), documentedBoolFlag("json", "write machine-readable output")}
}

func controllerEstimatorFlags() []commandFlag {
	return []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("method", "string", string(studio.EstimatorLQE), "estimator method"), documentedStringFlag("qn", "matrix", "", "process noise matrix"), documentedStringFlag("rn", "matrix", "", "measurement noise matrix"), documentedStringFlag("g", "matrix", "", "process noise input matrix"), documentedStringFlag("poles", "list", "", "comma-separated real or complex poles"), documentedFloat64Flag("sample-time", "seconds", 0, "sample time in seconds"), documentedFloat64Flag("base-step", "seconds", 0, "simulation base step in seconds"), documentedFloat64Flag("review-horizon", "seconds", 0, "review horizon in seconds"), documentedBoolFlag("json", "write machine-readable output")}
}

func controllerObserverFlags() []commandFlag {
	return []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("method", "string", string(studio.ObserverRegulatorLQG), "observer regulator method"), documentedStringFlag("q", "matrix", "", "state cost matrix"), documentedStringFlag("r", "matrix", "", "control cost matrix"), documentedStringFlag("qn", "matrix", "", "process noise matrix"), documentedStringFlag("rn", "matrix", "", "measurement noise matrix"), documentedStringFlag("k", "matrix", "", "state feedback gain matrix"), documentedStringFlag("l", "matrix", "", "observer gain matrix"), documentedFloat64Flag("base-step", "seconds", 0, "simulation base step in seconds"), documentedFloat64Flag("review-horizon", "seconds", 0, "review horizon in seconds"), documentedBoolFlag("json", "write machine-readable output")}
}

func controllerTuneFlags() []commandFlag {
	return []commandFlag{documentedInt64Flag("flow", "id", 0, "flowsheet id"), documentedStringFlag("algorithm", "string", string(studio.TuningGrid), "tuning algorithm"), documentedStringListFlag("parameter", "string", "field=lower:upper; repeatable"), documentedStringListFlag("goal", "string", "name:kind:maximum[:minimum]; repeatable"), documentedStringFlag("goals", "path", "", "JSON tuning goals file, or - for stdin"), documentedIntFlag("grid-points", "count", 3, "grid points per parameter"), documentedIntFlag("max-evaluations", "count", 100, "maximum tuning evaluations"), documentedFloat64Flag("base-step", "seconds", 0, "base simulation step in seconds"), documentedFloat64Flag("review-horizon", "seconds", 0, "review horizon in seconds"), documentedBoolFlag("json", "write machine-readable output")}
}

func runControllerAction(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer, action string) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if len(args) != 1 {
		return usagef("processlab controller %s: exactly one candidate id is required", action)
	}
	id := args[0]
	record, err := findControllerCandidate(ctx, client, id, flowID)
	if err != nil {
		return err
	}
	before, err := getControllerBlocks(ctx, client, record.FlowID)
	if err != nil {
		return err
	}
	path := "/flows/" + strconv.FormatInt(record.FlowID, 10) + "/controller-candidates/" + id + "/" + action
	var result controllerActionRecordClient
	if err := client.request(ctx, http.MethodPost, path, nil, &result); err != nil {
		return err
	}
	after, err := getControllerBlocks(ctx, client, record.FlowID)
	if err != nil {
		return err
	}
	output := controllerActionOutputClient{controllerActionRecordClient: result, Changes: controllerBlockChanges(before, after)}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(output)
	}
	fmt.Fprintf(stdout, "%s candidate %s (%s)\n", result.Action, result.ID, result.Kind)
	if len(output.Changes) == 0 {
		fmt.Fprintln(stdout, "blocks changed: none")
		return nil
	}
	for _, change := range output.Changes {
		parameters, _ := json.Marshal(change.Parameters)
		fmt.Fprintf(stdout, "block %d %s: %s\n", change.ID, change.Name, parameters)
	}
	return nil
}

func findControllerCandidate(ctx context.Context, client *apiClient, id string, flowID int64) (controllerCandidateRecordClient, error) {
	if flowID > 0 {
		return requestControllerCandidate(ctx, client, flowID, id)
	}
	var flows []flowClientRecord
	if err := client.request(ctx, http.MethodGet, "/flows", nil, &flows); err != nil {
		return controllerCandidateRecordClient{}, err
	}
	for _, flow := range flows {
		record, err := requestControllerCandidate(ctx, client, flow.ID, id)
		if err == nil {
			return record, nil
		}
		var clientErr *clientError
		if !errors.As(err, &clientErr) || clientErr.code != 1 || clientErr.kind != "not_found" {
			return controllerCandidateRecordClient{}, err
		}
	}
	return controllerCandidateRecordClient{}, fmt.Errorf("controller candidate %q was not found", id)
}

func requestControllerCandidate(ctx context.Context, client *apiClient, flowID int64, id string) (controllerCandidateRecordClient, error) {
	var record controllerCandidateRecordClient
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/controller-candidates/" + id
	if err := client.request(ctx, http.MethodGet, path, nil, &record); err != nil {
		return controllerCandidateRecordClient{}, err
	}
	return record, nil
}

func getControllerBlocks(ctx context.Context, client *apiClient, flowID int64) ([]blockRecordClient, error) {
	var blocks []blockRecordClient
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/blocks"
	if err := client.request(ctx, http.MethodGet, path, nil, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func controllerBlockChanges(before, after []blockRecordClient) []controllerBlockChangeClient {
	previous := make(map[int64]blockRecordClient, len(before))
	for _, block := range before {
		previous[block.ID] = block
	}
	changes := make([]controllerBlockChangeClient, 0)
	for _, block := range after {
		old, ok := previous[block.ID]
		if !ok || reflect.DeepEqual(old.Parameters, block.Parameters) {
			continue
		}
		changes = append(changes, controllerBlockChangeClient{
			ID: block.ID, Name: block.Name, Parameters: block.Parameters,
		})
	}
	return changes
}

func runControllerPID(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	typeText := options.commandString("type")
	crossover := options.commandFloat64("crossover")
	phaseMargin := options.commandFloat64("phase-margin")
	reviewHorizon := options.commandFloat64("review-horizon")
	baseStep := options.commandFloat64("base-step")
	setpointWeight := options.commandString("setpoint-weight")
	derivativeWeight := options.commandString("derivative-weight")
	if typeText == "" {
		typeText = "PID"
	}
	if flowID <= 0 {
		return usagef("processlab controller pid: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab controller pid: unexpected argument %q", args[0])
	}
	request := pidCandidateRequestClient{
		PIDDesignRequest: studio.PIDDesignRequest{
			Type:               controlsys.PidtuneType(typeText),
			CrossoverFrequency: crossover,
			PhaseMargin:        phaseMargin,
			StepHorizon:        reviewHorizon,
			BaseStep:           baseStep,
		},
		ReviewHorizon: reviewHorizon,
	}
	var err error
	request.SetpointWeight, err = optionalControllerFloat(setpointWeight, "setpoint-weight")
	if err != nil {
		return usagef("processlab controller pid: %v", err)
	}
	request.DerivativeWeight, err = optionalControllerFloat(derivativeWeight, "derivative-weight")
	if err != nil {
		return usagef("processlab controller pid: %v", err)
	}
	return postControllerCandidate(ctx, client, flowID, "pid", request, jsonOutput, stdout, stderr)
}

func runControllerStateFeedback(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	method := options.commandString("method")
	qText := options.commandString("q")
	rText := options.commandString("r")
	regulatedOutputText := options.commandString("regulated-output")
	polesText := options.commandString("poles")
	sampleTime := options.commandFloat64("sample-time")
	baseStep := options.commandFloat64("base-step")
	reviewHorizon := options.commandFloat64("review-horizon")
	if method == "" {
		method = string(studio.StateFeedbackLQR)
	}
	if flowID <= 0 {
		return usagef("processlab controller state feedback: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab controller state feedback: unexpected argument %q", args[0])
	}
	request, err := stateFeedbackRequest(method, qText, rText, regulatedOutputText, polesText, sampleTime, baseStep)
	if err != nil {
		return usagef("processlab controller state feedback: %v", err)
	}
	requestClient := stateFeedbackCandidateRequestClient{StateFeedbackRequest: request, ReviewHorizon: reviewHorizon}
	return postControllerCandidate(ctx, client, flowID, "state/feedback", requestClient, jsonOutput, stdout, stderr)
}

func runControllerEstimator(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	method := options.commandString("method")
	qnText := options.commandString("qn")
	rnText := options.commandString("rn")
	gText := options.commandString("g")
	polesText := options.commandString("poles")
	sampleTime := options.commandFloat64("sample-time")
	baseStep := options.commandFloat64("base-step")
	reviewHorizon := options.commandFloat64("review-horizon")
	if method == "" {
		method = string(studio.EstimatorLQE)
	}
	if flowID <= 0 {
		return usagef("processlab controller state estimator: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab controller state estimator: unexpected argument %q", args[0])
	}
	qn, err := optionalControllerMatrix(qnText, "qn")
	if err != nil {
		return usagef("processlab controller state estimator: %v", err)
	}
	rn, err := optionalControllerMatrix(rnText, "rn")
	if err != nil {
		return usagef("processlab controller state estimator: %v", err)
	}
	g, err := optionalControllerMatrix(gText, "g")
	if err != nil {
		return usagef("processlab controller state estimator: %v", err)
	}
	poles, err := parseControllerPoles(polesText)
	if err != nil {
		return usagef("processlab controller state estimator: %v", err)
	}
	request := estimatorCandidateRequestClient{
		EstimatorDesignRequest: studio.EstimatorDesignRequest{
			Method: studio.EstimatorMethod(method), Qn: qn, Rn: rn, G: g, Poles: poles,
			SampleTime: sampleTime, BaseStep: baseStep,
		},
		ReviewHorizon: reviewHorizon,
	}
	return postControllerCandidate(ctx, client, flowID, "state/estimator", request, jsonOutput, stdout, stderr)
}

func runControllerObserver(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	method := options.commandString("method")
	qText := options.commandString("q")
	rText := options.commandString("r")
	qnText := options.commandString("qn")
	rnText := options.commandString("rn")
	kText := options.commandString("k")
	lText := options.commandString("l")
	baseStep := options.commandFloat64("base-step")
	reviewHorizon := options.commandFloat64("review-horizon")
	if method == "" {
		method = string(studio.ObserverRegulatorLQG)
	}
	if flowID <= 0 {
		return usagef("processlab controller state observer: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab controller state observer: unexpected argument %q", args[0])
	}
	q, err := optionalControllerMatrix(qText, "q")
	if err != nil {
		return usagef("processlab controller state observer: %v", err)
	}
	r, err := optionalControllerMatrix(rText, "r")
	if err != nil {
		return usagef("processlab controller state observer: %v", err)
	}
	qn, err := optionalControllerMatrix(qnText, "qn")
	if err != nil {
		return usagef("processlab controller state observer: %v", err)
	}
	rn, err := optionalControllerMatrix(rnText, "rn")
	if err != nil {
		return usagef("processlab controller state observer: %v", err)
	}
	k, err := optionalControllerMatrix(kText, "k")
	if err != nil {
		return usagef("processlab controller state observer: %v", err)
	}
	l, err := optionalControllerMatrix(lText, "l")
	if err != nil {
		return usagef("processlab controller state observer: %v", err)
	}
	request := observerCandidateRequestClient{
		ObserverRegulatorRequest: studio.ObserverRegulatorRequest{
			Method: studio.ObserverRegulatorMethod(method), Q: q, R: r, Qn: qn, Rn: rn, K: k, L: l, BaseStep: baseStep,
		},
		ReviewHorizon: reviewHorizon,
	}
	return postControllerCandidate(ctx, client, flowID, "state-space", request, jsonOutput, stdout, stderr)
}

func runControllerRobust(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	method := options.commandString("method")
	baseStep := options.commandFloat64("base-step")
	reviewHorizon := options.commandFloat64("review-horizon")
	if method == "" {
		method = string(studio.RobustSynthesisH2)
	}
	if flowID <= 0 {
		return usagef("processlab controller robust: --flow is required")
	}
	if len(args) != 0 {
		return usagef("processlab controller robust: unexpected argument %q", args[0])
	}
	request := robustCandidateRequestClient{
		RobustSynthesisRequest: studio.RobustSynthesisRequest{Method: studio.RobustSynthesisMethod(method), BaseStep: baseStep},
		ReviewHorizon:          reviewHorizon,
	}
	return postControllerCandidate(ctx, client, flowID, "robust", request, jsonOutput, stdout, stderr)
}

func runControllerTune(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	algorithm := options.commandString("algorithm")
	parameters := options.commandStrings("parameter")
	goals := options.commandStrings("goal")
	goalsFile := options.commandString("goals")
	gridPoints := options.commandInt("grid-points")
	maxEvaluations := options.commandInt("max-evaluations")
	baseStep := options.commandFloat64("base-step")
	reviewHorizon := options.commandFloat64("review-horizon")
	if algorithm == "" {
		algorithm = string(studio.TuningGrid)
	}
	if flowID <= 0 || len(parameters) == 0 {
		return usagef("processlab controller tune: --flow and at least one --parameter are required")
	}
	if len(args) != 0 {
		return usagef("processlab controller tune: unexpected argument %q", args[0])
	}
	roles, err := getControlRoles(ctx, client, flowID)
	if err != nil {
		return err
	}
	if len(roles.Controller.Blocks) != 1 {
		return fmt.Errorf("controller tune requires exactly one controller role block; found %d", len(roles.Controller.Blocks))
	}
	parameterSpecs, err := parseControllerParameters(parameters, roles.Controller.Blocks[0])
	if err != nil {
		return usagef("processlab controller tune: %v", err)
	}
	tuningGoals, err := loadControllerTuningGoals(goals, goalsFile)
	if err != nil {
		return usagef("processlab controller tune: %v", err)
	}
	request := tuningCandidateRequestClient{
		ControllerTuningRequest: studio.ControllerTuningRequest{
			Algorithm: studio.TuningAlgorithm(algorithm), Parameters: parameterSpecs,
			Goals: tuningGoals, GridPoints: gridPoints, MaxEvaluations: maxEvaluations,
			BaseStep: baseStep,
		},
		ReviewHorizon: reviewHorizon,
	}
	return postControllerCandidate(ctx, client, flowID, "tune", request, jsonOutput, stdout, stderr)
}

func runControllerReview(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer) error {
	jsonOutput := options.json || options.commandBool("json")
	flowID := options.commandInt64("flow")
	if flowID <= 0 {
		return usagef("processlab controller review: --flow is required")
	}
	if len(args) != 1 {
		return usagef("processlab controller review: exactly one candidate id is required")
	}
	var record controllerCandidateRecordClient
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/controller-candidates/" + args[0]
	if err := client.request(ctx, http.MethodGet, path, nil, &record); err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(record)
	}
	printControllerReview(stdout, record)
	return nil
}

func postControllerCandidate(ctx context.Context, client *apiClient, flowID int64, operation string, request any, jsonOutput bool, stdout, stderr io.Writer) error {
	var record controllerCandidateRecordClient
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/controller-candidates/" + operation
	if err := client.request(ctx, http.MethodPost, path, request, &record); err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(record)
	}
	fmt.Fprintln(stdout, record.ID)
	fmt.Fprintf(stderr, "controller candidate %s (%s)\n", record.ID, record.Kind)
	printControllerReview(stderr, record)
	return nil
}

func printControllerReview(w io.Writer, record controllerCandidateRecordClient) {
	fmt.Fprintf(w, "kind: %s\nalgorithm: %s\napply available: %t\nundo available: %t\n", record.Review.Kind, record.Review.Algorithm, record.Review.ApplyAvailable, record.Review.UndoAvailable)
	for _, goal := range record.Review.Goals {
		fmt.Fprintf(w, "goal: %s (%s)\n", goal.Name, goal.Target)
	}
	for _, warning := range record.Review.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

func optionalControllerFloat(raw, label string) (*float64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return nil, fmt.Errorf("--%s must be a number", label)
	}
	return &value, nil
}

func optionalControllerMatrix(raw, label string) (*studio.MatrixValue, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := studio.ParseMatrixValue(raw)
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", label, err)
	}
	return &value, nil
}

func parseControllerPoles(raw string) ([]studio.ComplexValue, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	values := make([]studio.ComplexValue, len(parts))
	for index, part := range parts {
		value, err := strconv.ParseComplex(strings.Trim(strings.TrimSpace(part), "()"), 128)
		if err != nil {
			return nil, fmt.Errorf("pole %d must be a real or complex number", index+1)
		}
		values[index] = studio.ComplexValue{Real: real(value), Imag: imag(value)}
	}
	return values, nil
}

func stateFeedbackRequest(method, qText, rText, regulatedOutputText, polesText string, sampleTime, baseStep float64) (studio.StateFeedbackRequest, error) {
	q, err := optionalControllerMatrix(qText, "q")
	if err != nil {
		return studio.StateFeedbackRequest{}, err
	}
	r, err := optionalControllerMatrix(rText, "r")
	if err != nil {
		return studio.StateFeedbackRequest{}, err
	}
	regulatedOutput, err := optionalControllerMatrix(regulatedOutputText, "regulated-output")
	if err != nil {
		return studio.StateFeedbackRequest{}, err
	}
	poles, err := parseControllerPoles(polesText)
	if err != nil {
		return studio.StateFeedbackRequest{}, fmt.Errorf("--poles: %w", err)
	}
	return studio.StateFeedbackRequest{
		Method: studio.StateFeedbackMethod(method), Q: q, R: r,
		RegulatedOutput: regulatedOutput, Poles: poles,
		SampleTime: sampleTime, BaseStep: baseStep,
	}, nil
}

func parseControllerParameters(values []string, blockID int64) ([]studio.TunableParameterSpec, error) {
	parameters := make([]studio.TunableParameterSpec, 0, len(values))
	for _, raw := range values {
		name, bounds, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("parameter %q must use field=lower:upper", raw)
		}
		limits := strings.Split(bounds, ":")
		if len(limits) != 2 {
			return nil, fmt.Errorf("parameter %q must use field=lower:upper", raw)
		}
		lower, err := strconv.ParseFloat(strings.TrimSpace(limits[0]), 64)
		if err != nil {
			return nil, fmt.Errorf("parameter %q has an invalid lower bound", raw)
		}
		upper, err := strconv.ParseFloat(strings.TrimSpace(limits[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("parameter %q has an invalid upper bound", raw)
		}
		parameters = append(parameters, studio.TunableParameterSpec{
			Ref:   studio.TunableParameterRef{BlockID: blockID, Field: studio.TunableField(strings.TrimSpace(name))},
			Lower: lower, Upper: upper,
		})
	}
	return parameters, nil
}

func loadControllerTuningGoals(values []string, source string) ([]studio.TuningGoalRequest, error) {
	if source != "" {
		var encoded []byte
		var err error
		if source == "-" {
			encoded, err = io.ReadAll(os.Stdin)
		} else {
			encoded, err = os.ReadFile(source)
		}
		if err != nil {
			return nil, fmt.Errorf("read goals: %w", err)
		}
		var goals []studio.TuningGoalRequest
		if err := json.Unmarshal(encoded, &goals); err != nil {
			return nil, fmt.Errorf("decode goals JSON: %w", err)
		}
		return goals, nil
	}
	goals := make([]studio.TuningGoalRequest, 0, len(values))
	for _, raw := range values {
		parts := strings.Split(raw, ":")
		if len(parts) != 3 && len(parts) != 4 {
			return nil, fmt.Errorf("goal %q must use name:kind:maximum[:minimum]", raw)
		}
		maximum, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("goal %q has an invalid maximum", raw)
		}
		var minimum float64
		if len(parts) == 4 {
			minimum, err = strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			if err != nil {
				return nil, fmt.Errorf("goal %q has an invalid minimum", raw)
			}
		}
		goals = append(goals, studio.TuningGoalRequest{
			Name: strings.TrimSpace(parts[0]), Kind: studio.TuningGoalKind(strings.TrimSpace(parts[1])),
			Maximum: maximum, Minimum: minimum,
		})
	}
	return goals, nil
}
