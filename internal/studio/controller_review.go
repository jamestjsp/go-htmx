package studio

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

const (
	defaultControllerReviewHorizon = 10.0
	maxControllerReviewSamples     = 1000
)

type ControllerDesignGoal struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type ControllerTimeTrace struct {
	InputName       string    `json:"inputName"`
	OutputName      string    `json:"outputName"`
	CurrentValues   []float64 `json:"currentValues"`
	CandidateValues []float64 `json:"candidateValues"`
}

type ControllerTimeComparison struct {
	Times  []float64             `json:"times"`
	Traces []ControllerTimeTrace `json:"traces"`
}

type ControllerCandidateReview struct {
	FlowID              int64                    `json:"flowId"`
	SourceModelRevision time.Time                `json:"sourceModelRevision"`
	SourceControlRoles  ControlRoleSnapshot      `json:"sourceControlRoles"`
	Kind                string                   `json:"kind"`
	Algorithm           string                   `json:"algorithm"`
	Goals               []ControllerDesignGoal   `json:"goals"`
	Warnings            []string                 `json:"warnings,omitempty"`
	Robustness          LoopRobustnessAnalysis   `json:"robustness"`
	Time                ControllerTimeComparison `json:"time"`
	ApplyAvailable      bool                     `json:"applyAvailable"`
	UndoAvailable       bool                     `json:"undoAvailable"`
	UndoPolicy          string                   `json:"undoPolicy"`
}

func (s *Studio) ReviewPIDDesignCandidate(
	ctx context.Context,
	candidate PIDDesignCandidate,
	horizon float64,
) (ControllerCandidateReview, error) {
	review, err := s.reviewControllerCandidate(
		ctx,
		controllerReviewRequest{
			flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
			controlRoles: candidate.SourceControlRoles, kind: "pid",
			algorithm: string(candidate.Type), goals: candidate.Goals,
			warnings: candidate.Warnings, controller: candidate.Controller,
			horizon: horizon, applyAvailable: candidate.edit != nil,
		},
	)
	if err != nil {
		return ControllerCandidateReview{}, err
	}
	if candidate.Step != nil {
		review.Time = ControllerTimeComparison{
			Times: append([]float64(nil), candidate.Step.Times...),
			Traces: []ControllerTimeTrace{{
				InputName:       "reference",
				OutputName:      "measurement",
				CurrentValues:   append([]float64(nil), candidate.Step.CurrentValues...),
				CandidateValues: append([]float64(nil), candidate.Step.CandidateValues...),
			}},
		}
	}
	return review, nil
}

func (s *Studio) ReviewTuningCandidate(
	ctx context.Context,
	candidate ControllerTuningCandidate,
	horizon float64,
) (ControllerCandidateReview, error) {
	goals := make([]ControllerDesignGoal, 0, len(candidate.Goals))
	for _, goal := range candidate.Goals {
		goals = append(goals, ControllerDesignGoal{
			Name:   goal.Name,
			Target: fmt.Sprintf("%s limit %.6g", goal.Kind, goal.Limit),
		})
	}
	return s.reviewControllerCandidate(
		ctx,
		controllerReviewRequest{
			flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
			controlRoles: candidate.SourceControlRoles, kind: "tuning",
			algorithm: string(candidate.Algorithm), goals: goals,
			warnings: candidate.Warnings, controller: candidate.Controller,
			horizon: horizon, applyAvailable: candidate.edit != nil,
		},
	)
}

func (s *Studio) ReviewStateDesignCandidate(
	ctx context.Context,
	candidate StateDesignCandidate,
	horizon float64,
) (ControllerCandidateReview, error) {
	return s.reviewControllerCandidate(
		ctx,
		controllerReviewRequest{
			flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
			controlRoles: candidate.SourceControlRoles, kind: "state-space",
			algorithm: candidate.Method,
			goals:     candidate.Goals,
			warnings:  candidate.Warnings, controller: candidate.Controller,
			controllerIsSigned: true, horizon: horizon,
			applyAvailable: candidate.edit != nil,
		},
	)
}

func (s *Studio) ReviewEstimatorCandidate(
	ctx context.Context,
	candidate StateDesignCandidate,
) (ControllerCandidateReview, error) {
	if candidate.FlowID <= 0 || candidate.SourceModelRevision.IsZero() ||
		!candidate.SourceControlRoles.valid() || candidate.Estimator == nil {
		return ControllerCandidateReview{}, invalid(
			"estimator candidate is incomplete; refresh the design",
		)
	}
	snapshot, err := s.snapshot(ctx, candidate.FlowID)
	if err != nil {
		return ControllerCandidateReview{}, err
	}
	if !snapshot.Flow.ModelUpdatedAt.Equal(candidate.SourceModelRevision) {
		return ControllerCandidateReview{}, invalid(
			"estimator candidate is stale; refresh the design from the current model",
		)
	}
	currentRoles, err := loadControlRoleSpec(ctx, s.db, candidate.FlowID)
	if err != nil {
		return ControllerCandidateReview{}, err
	}
	if newControlRoleSnapshot(currentRoles).Fingerprint !=
		candidate.SourceControlRoles.Fingerprint {
		return ControllerCandidateReview{}, invalid(
			"control roles changed; refresh the design from the current roles",
		)
	}
	return ControllerCandidateReview{
		FlowID:              candidate.FlowID,
		SourceModelRevision: candidate.SourceModelRevision,
		SourceControlRoles:  candidate.SourceControlRoles,
		Kind:                "state-estimator",
		Algorithm:           candidate.Method,
		Goals:               append([]ControllerDesignGoal(nil), candidate.Goals...),
		Warnings:            append([]string(nil), candidate.Warnings...),
		ApplyAvailable:      false,
		UndoAvailable:       false,
		UndoPolicy:          "Estimator candidates are diagnostic-only and cannot replace the authored controller.",
	}, nil
}

type controllerReviewRequest struct {
	flowID             int64
	modelRevision      time.Time
	controlRoles       ControlRoleSnapshot
	kind               string
	algorithm          string
	goals              []ControllerDesignGoal
	warnings           []string
	controller         *controlsys.System
	controllerIsSigned bool
	horizon            float64
	applyAvailable     bool
}

func (s *Studio) reviewControllerCandidate(
	ctx context.Context,
	request controllerReviewRequest,
) (ControllerCandidateReview, error) {
	if request.flowID <= 0 || request.modelRevision.IsZero() ||
		!request.controlRoles.valid() || request.controller == nil {
		return ControllerCandidateReview{}, invalid(
			"controller candidate is incomplete; refresh the design",
		)
	}
	snapshot, err := s.snapshot(ctx, request.flowID)
	if err != nil {
		return ControllerCandidateReview{}, err
	}
	if !snapshot.Flow.ModelUpdatedAt.Equal(request.modelRevision) {
		return ControllerCandidateReview{}, invalid(
			"controller candidate is stale; refresh the design from the current model",
		)
	}
	currentRoles, err := loadControlRoleSpec(ctx, s.db, request.flowID)
	if err != nil {
		return ControllerCandidateReview{}, err
	}
	if newControlRoleSnapshot(currentRoles).Fingerprint !=
		request.controlRoles.Fingerprint {
		return ControllerCandidateReview{}, invalid(
			"control roles changed; refresh the design from the current roles",
		)
	}
	controller := request.controller.Copy()
	if request.controllerIsSigned {
		controller, err = negateSystemOutputs(controller)
		if err != nil {
			return ControllerCandidateReview{}, fmt.Errorf(
				"normalize signed controller candidate: %w", err,
			)
		}
	}
	robustness, err := s.AnalyzeLoopRobustness(
		ctx,
		request.flowID,
		LoopRobustnessRequest{
			Points: defaultFrequencyPoints, CandidateController: controller,
		},
	)
	if err != nil {
		return ControllerCandidateReview{}, err
	}
	if robustness.Candidate == nil {
		return ControllerCandidateReview{}, fmt.Errorf(
			"review controller candidate: candidate robustness evidence is missing",
		)
	}
	timeComparison, err := compareControllerTimeResponses(
		robustness.Current.models.To,
		robustness.Candidate.models.To,
		request.horizon,
	)
	if err != nil {
		return ControllerCandidateReview{}, fmt.Errorf(
			"compare current and candidate time responses: %w", err,
		)
	}
	return ControllerCandidateReview{
		FlowID: request.flowID, SourceModelRevision: request.modelRevision,
		SourceControlRoles: request.controlRoles, Kind: request.kind,
		Algorithm:  request.algorithm,
		Goals:      append([]ControllerDesignGoal(nil), request.goals...),
		Warnings:   append([]string(nil), request.warnings...),
		Robustness: robustness, Time: timeComparison,
		ApplyAvailable: request.applyAvailable,
		UndoAvailable:  request.applyAvailable,
		UndoPolicy:     "The apply result carries a one-use, revision-checked controller undo candidate.",
	}, nil
}

func compareControllerTimeResponses(
	current, candidate *controlsys.System,
	horizon float64,
) (ControllerTimeComparison, error) {
	if current == nil || candidate == nil {
		return ControllerTimeComparison{}, invalid(
			"closed-loop time comparison requires current and candidate systems",
		)
	}
	_, currentInputs, currentOutputs := current.Dims()
	_, candidateInputs, candidateOutputs := candidate.Dims()
	if currentInputs != candidateInputs || currentOutputs != candidateOutputs {
		return ControllerTimeComparison{}, invalid(
			"closed-loop comparison dimensions changed from %dx%d to %dx%d",
			currentOutputs, currentInputs, candidateOutputs, candidateInputs,
		)
	}
	if horizon == 0 {
		horizon = defaultControllerReviewHorizon
	}
	if horizon <= 0 || math.IsNaN(horizon) || math.IsInf(horizon, 0) {
		return ControllerTimeComparison{}, invalid(
			"controller review horizon must be positive and finite",
		)
	}
	times, err := controllerReviewTimeGrid(current, candidate, horizon)
	if err != nil {
		return ControllerTimeComparison{}, err
	}
	result := ControllerTimeComparison{
		Times: append([]float64(nil), times...),
	}
	for input := range currentInputs {
		u := mat.NewDense(len(times), currentInputs, nil)
		for sample := range times {
			u.Set(sample, input, 1)
		}
		currentResponse, err := controlsys.Lsim(current, u, times, nil)
		if err != nil {
			return ControllerTimeComparison{}, fmt.Errorf(
				"current closed-loop input %d: %w", input+1, err,
			)
		}
		candidateResponse, err := controlsys.Lsim(candidate, u, times, nil)
		if err != nil {
			return ControllerTimeComparison{}, fmt.Errorf(
				"candidate closed-loop input %d: %w", input+1, err,
			)
		}
		for output := range currentOutputs {
			trace := ControllerTimeTrace{
				InputName:       inputName(current, input),
				OutputName:      outputName(current, output),
				CurrentValues:   make([]float64, len(times)),
				CandidateValues: make([]float64, len(times)),
			}
			for sample := range times {
				trace.CurrentValues[sample] = currentResponse.Y.At(output, sample)
				trace.CandidateValues[sample] = candidateResponse.Y.At(output, sample)
			}
			result.Traces = append(result.Traces, trace)
		}
	}
	return result, nil
}

func controllerReviewTimeGrid(
	current, candidate *controlsys.System,
	horizon float64,
) ([]float64, error) {
	dt := horizon / float64(maxControllerReviewSamples-1)
	switch {
	case current.IsDiscrete() && candidate.IsDiscrete():
		if math.Abs(current.Dt-candidate.Dt) >
			1e-12*math.Max(1, math.Max(current.Dt, candidate.Dt)) {
			return nil, invalid(
				"controller review sample times differ: current %.6g, candidate %.6g",
				current.Dt, candidate.Dt,
			)
		}
		dt = current.Dt
	case current.IsDiscrete() || candidate.IsDiscrete():
		return nil, invalid(
			"controller review cannot compare continuous and discrete closed loops",
		)
	}
	samples := int(math.Floor(horizon/dt)) + 1
	if samples > maxControllerReviewSamples {
		samples = maxControllerReviewSamples
	}
	if samples < 2 {
		samples = 2
	}
	times := make([]float64, samples)
	for sample := range times {
		times[sample] = float64(sample) * dt
	}
	return times, nil
}

func inputName(system *controlsys.System, index int) string {
	if index >= 0 && index < len(system.InputName) &&
		system.InputName[index] != "" {
		return system.InputName[index]
	}
	return fmt.Sprintf("input %d", index+1)
}

func outputName(system *controlsys.System, index int) string {
	if index >= 0 && index < len(system.OutputName) &&
		system.OutputName[index] != "" {
		return system.OutputName[index]
	}
	return fmt.Sprintf("output %d", index+1)
}
