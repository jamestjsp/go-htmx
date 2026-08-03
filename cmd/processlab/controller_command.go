package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
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
		name:      "controller",
		summary:   "Design, tune, and review controller candidates",
		freeform:  true,
		arguments: []commandArgument{{name: "operation", description: "controller operation"}},
		run: func(ctx context.Context, options globalOptions, args []string, stdout io.Writer, stderr io.Writer) error {
			return runController(ctx, options, args, stdout, stderr)
		},
	}
}

func runController(ctx context.Context, options globalOptions, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab controller: choose pid, state, robust, tune, or review")
	}
	client, err := newAPIClient(options.server, options.timeout)
	if err != nil {
		return err
	}
	switch args[0] {
	case "pid":
		return runControllerPID(ctx, client, args[1:], options, stdout, stderr)
	case "state":
		return runControllerState(ctx, client, args[1:], options, stdout, stderr)
	case "robust":
		return runControllerRobust(ctx, client, args[1:], options, stdout, stderr)
	case "tune":
		return runControllerTune(ctx, client, args[1:], options, stdout, stderr)
	case "review":
		return runControllerReview(ctx, client, args[1:], options, stdout)
	case "apply":
		return runControllerAction(ctx, client, args[1:], options, stdout, "apply")
	case "undo":
		return runControllerAction(ctx, client, args[1:], options, stdout, "undo")
	default:
		return usagef("processlab controller: unknown operation %q; choose pid, state, robust, tune, review, apply, or undo", args[0])
	}
}

func runControllerAction(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout io.Writer, action string) error {
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("controller "+action, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id; omit to search all flows")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stdout, "Usage: processlab controller %s <candidate-id> [--flow <id>] [--json]\n", action)
			if action == "apply" {
				fmt.Fprintln(stdout, "Applying invalidates stored simulation and analysis records for the flowsheet.")
			}
			return nil
		}
		return usagef("processlab controller %s: %v", action, err)
	}
	if set.NArg() != 1 {
		return usagef("processlab controller %s: exactly one candidate id is required", action)
	}
	id := set.Arg(0)
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
	args = moveCommandFlags(args,
		[]string{"--flow", "-flow", "--type", "-type", "--crossover", "-crossover", "--phase-margin", "-phase-margin", "--review-horizon", "-review-horizon", "--base-step", "-base-step", "--setpoint-weight", "-setpoint-weight", "--derivative-weight", "-derivative-weight"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("controller pid", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var typeText string
	var crossover, phaseMargin, reviewHorizon, baseStep float64
	var setpointWeight, derivativeWeight string
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&typeText, "type", "PID", "PID tuning type: P, PI, PD, or PID")
	set.Float64Var(&crossover, "crossover", 0, "target crossover frequency")
	set.Float64Var(&phaseMargin, "phase-margin", 0, "target phase margin in degrees")
	set.Float64Var(&reviewHorizon, "review-horizon", 0, "review horizon in seconds")
	set.Float64Var(&baseStep, "base-step", 0, "simulation base step in seconds")
	set.StringVar(&setpointWeight, "setpoint-weight", "", "PID2 setpoint weight")
	set.StringVar(&derivativeWeight, "derivative-weight", "", "PID2 derivative weight")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller pid --flow <id> [--type PID] [--crossover <hz>] [--phase-margin <degrees>] [--review-horizon <seconds>] [--json]")
			return nil
		}
		return usagef("processlab controller pid: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab controller pid: --flow is required")
	}
	if set.NArg() != 0 {
		return usagef("processlab controller pid: unexpected argument %q", set.Arg(0))
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

func runControllerState(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usagef("processlab controller state: choose feedback, estimator, or observer")
	}
	switch args[0] {
	case "feedback":
		return runControllerStateFeedback(ctx, client, args[1:], options, stdout, stderr)
	case "estimator":
		return runControllerEstimator(ctx, client, args[1:], options, stdout, stderr)
	case "observer":
		return runControllerObserver(ctx, client, args[1:], options, stdout, stderr)
	default:
		return usagef("processlab controller state: unknown operation %q; choose feedback, estimator, or observer", args[0])
	}
}

func runControllerStateFeedback(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	args = moveCommandFlags(args,
		[]string{"--flow", "-flow", "--method", "-method", "--q", "-q", "--r", "-r", "--regulated-output", "-regulated-output", "--poles", "-poles", "--sample-time", "-sample-time", "--base-step", "-base-step", "--review-horizon", "-review-horizon"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("controller state feedback", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var method, qText, rText, regulatedOutputText, polesText string
	var sampleTime, baseStep, reviewHorizon float64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&method, "method", string(studio.StateFeedbackLQR), "state feedback method")
	set.StringVar(&qText, "q", "", "state cost matrix, for example 1 or 1,0;0,1")
	set.StringVar(&rText, "r", "", "control cost matrix")
	set.StringVar(&regulatedOutputText, "regulated-output", "", "regulated output matrix")
	set.StringVar(&polesText, "poles", "", "comma-separated real or complex poles")
	set.Float64Var(&sampleTime, "sample-time", 0, "sample time in seconds")
	set.Float64Var(&baseStep, "base-step", 0, "simulation base step in seconds")
	set.Float64Var(&reviewHorizon, "review-horizon", 0, "review horizon in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller state feedback --flow <id> --method <lqr|lqi|lqrd|acker|place> [--q <matrix>] [--r <matrix>] [--poles <list>] [--json]")
			return nil
		}
		return usagef("processlab controller state feedback: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab controller state feedback: --flow is required")
	}
	request, err := stateFeedbackRequest(method, qText, rText, regulatedOutputText, polesText, sampleTime, baseStep)
	if err != nil {
		return usagef("processlab controller state feedback: %v", err)
	}
	requestClient := stateFeedbackCandidateRequestClient{StateFeedbackRequest: request, ReviewHorizon: reviewHorizon}
	return postControllerCandidate(ctx, client, flowID, "state/feedback", requestClient, jsonOutput, stdout, stderr)
}

func runControllerEstimator(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	args = moveCommandFlags(args,
		[]string{"--flow", "-flow", "--method", "-method", "--qn", "-qn", "--rn", "-rn", "--g", "-g", "--poles", "-poles", "--sample-time", "-sample-time", "--base-step", "-base-step", "--review-horizon", "-review-horizon"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("controller state estimator", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var method, qnText, rnText, gText, polesText string
	var sampleTime, baseStep, reviewHorizon float64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&method, "method", string(studio.EstimatorLQE), "estimator method")
	set.StringVar(&qnText, "qn", "", "process noise matrix")
	set.StringVar(&rnText, "rn", "", "measurement noise matrix")
	set.StringVar(&gText, "g", "", "process noise input matrix")
	set.StringVar(&polesText, "poles", "", "comma-separated real or complex poles")
	set.Float64Var(&sampleTime, "sample-time", 0, "sample time in seconds")
	set.Float64Var(&baseStep, "base-step", 0, "simulation base step in seconds")
	set.Float64Var(&reviewHorizon, "review-horizon", 0, "review horizon in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller state estimator --flow <id> --method <lqe|kalman|kalmd|place> [--qn <matrix>] [--rn <matrix>] [--json]")
			return nil
		}
		return usagef("processlab controller state estimator: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab controller state estimator: --flow is required")
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
	args = moveCommandFlags(args,
		[]string{"--flow", "-flow", "--method", "-method", "--q", "-q", "--r", "-r", "--qn", "-qn", "--rn", "-rn", "--k", "-k", "--l", "-l", "--base-step", "-base-step", "--review-horizon", "-review-horizon"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("controller state observer", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var method, qText, rText, qnText, rnText, kText, lText string
	var baseStep, reviewHorizon float64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&method, "method", string(studio.ObserverRegulatorLQG), "observer regulator method")
	set.StringVar(&qText, "q", "", "state cost matrix")
	set.StringVar(&rText, "r", "", "control cost matrix")
	set.StringVar(&qnText, "qn", "", "process noise matrix")
	set.StringVar(&rnText, "rn", "", "measurement noise matrix")
	set.StringVar(&kText, "k", "", "state feedback gain matrix")
	set.StringVar(&lText, "l", "", "observer gain matrix")
	set.Float64Var(&baseStep, "base-step", 0, "simulation base step in seconds")
	set.Float64Var(&reviewHorizon, "review-horizon", 0, "review horizon in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller state observer --flow <id> --method <lqg|reg> [--q <matrix>] [--r <matrix>] [--qn <matrix>] [--rn <matrix>] [--json]")
			return nil
		}
		return usagef("processlab controller state observer: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab controller state observer: --flow is required")
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
	args = moveCommandFlags(args,
		[]string{"--flow", "-flow", "--method", "-method", "--base-step", "-base-step", "--review-horizon", "-review-horizon"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("controller robust", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var method string
	var baseStep, reviewHorizon float64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&method, "method", string(studio.RobustSynthesisH2), "robust method: h2 or hinf")
	set.Float64Var(&baseStep, "base-step", 0, "simulation base step in seconds")
	set.Float64Var(&reviewHorizon, "review-horizon", 0, "review horizon in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller robust --flow <id> --method <h2|hinf> [--json]")
			return nil
		}
		return usagef("processlab controller robust: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab controller robust: --flow is required")
	}
	request := robustCandidateRequestClient{
		RobustSynthesisRequest: studio.RobustSynthesisRequest{Method: studio.RobustSynthesisMethod(method), BaseStep: baseStep},
		ReviewHorizon:          reviewHorizon,
	}
	return postControllerCandidate(ctx, client, flowID, "robust", request, jsonOutput, stdout, stderr)
}

func runControllerTune(ctx context.Context, client *apiClient, args []string, options globalOptions, stdout, stderr io.Writer) error {
	args = moveCommandFlags(args,
		[]string{"--flow", "-flow", "--algorithm", "-algorithm", "--parameter", "-parameter", "--goal", "-goal", "--goals", "-goals", "--grid-points", "-grid-points", "--max-evaluations", "-max-evaluations", "--base-step", "-base-step", "--review-horizon", "-review-horizon"},
		[]string{"--json", "-json"},
	)
	set := flag.NewFlagSet("controller tune", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	var algorithm, goalsFile string
	var parameters, goals repeatedStringFlag
	var gridPoints, maxEvaluations int
	var baseStep, reviewHorizon float64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	set.StringVar(&algorithm, "algorithm", string(studio.TuningGrid), "tuning algorithm")
	set.Var(&parameters, "parameter", "field=lower:upper; repeatable")
	set.Var(&goals, "goal", "name:kind:maximum[:minimum]; repeatable")
	set.StringVar(&goalsFile, "goals", "", "JSON tuning goals file, or - for stdin")
	set.IntVar(&gridPoints, "grid-points", 3, "grid points per parameter")
	set.IntVar(&maxEvaluations, "max-evaluations", 100, "maximum tuning evaluations")
	set.Float64Var(&baseStep, "base-step", 0, "simulation base step in seconds")
	set.Float64Var(&reviewHorizon, "review-horizon", 0, "review horizon in seconds")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller tune --flow <id> --parameter <field=lower:upper> --goal <name:kind:maximum[:minimum]> [--json]")
			return nil
		}
		return usagef("processlab controller tune: %v", err)
	}
	if flowID <= 0 || len(parameters) == 0 {
		return usagef("processlab controller tune: --flow and at least one --parameter are required")
	}
	if set.NArg() != 0 {
		return usagef("processlab controller tune: unexpected argument %q", set.Arg(0))
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
	args = moveCommandFlags(args, []string{"--flow", "-flow"}, []string{"--json", "-json"})
	set := flag.NewFlagSet("controller review", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	jsonOutput := options.json
	var flowID int64
	set.BoolVar(&jsonOutput, "json", jsonOutput, "write machine-readable output")
	set.Int64Var(&flowID, "flow", 0, "flowsheet id")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: processlab controller review --flow <id> <candidate-id> [--json]")
			return nil
		}
		return usagef("processlab controller review: %v", err)
	}
	if flowID <= 0 {
		return usagef("processlab controller review: --flow is required")
	}
	if set.NArg() != 1 {
		return usagef("processlab controller review: exactly one candidate id is required")
	}
	var record controllerCandidateRecordClient
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/controller-candidates/" + set.Arg(0)
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
