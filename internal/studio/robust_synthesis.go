package studio

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

type RobustSynthesisMethod string

const (
	RobustSynthesisH2   RobustSynthesisMethod = "h2"
	RobustSynthesisHinf RobustSynthesisMethod = "hinf"
)

type RobustSynthesisRequest struct {
	Method   RobustSynthesisMethod `json:"method"`
	BaseStep float64               `json:"baseStep,omitempty"`
}

type RobustSynthesisPartitions struct {
	Exogenous   []string `json:"exogenous"`
	Regulated   []string `json:"regulated"`
	Measurement []string `json:"measurement"`
	Control     []string `json:"control"`
}

type RobustSynthesisEvidence struct {
	StableClosedLoop bool           `json:"stableClosedLoop"`
	AchievedNorm     float64        `json:"achievedNorm"`
	PeakFrequency    float64        `json:"peakFrequency,omitempty"`
	GammaBound       float64        `json:"gammaBound,omitempty"`
	ClosedLoopPoles  []ComplexValue `json:"closedLoopPoles"`
	XCondition       float64        `json:"xCondition"`
	YCondition       float64        `json:"yCondition"`
}

type RobustSynthesisCandidate struct {
	FlowID              int64                     `json:"flowId"`
	SourceModelRevision time.Time                 `json:"sourceModelRevision"`
	SourceControlRoles  ControlRoleSnapshot       `json:"sourceControlRoles"`
	Method              RobustSynthesisMethod     `json:"method"`
	Partitions          RobustSynthesisPartitions `json:"partitions"`
	Goals               []ControllerDesignGoal    `json:"goals"`
	Evidence            RobustSynthesisEvidence   `json:"evidence"`
	X                   *MatrixValue              `json:"x,omitempty"`
	Y                   *MatrixValue              `json:"y,omitempty"`
	Warnings            []string                  `json:"warnings,omitempty"`
	Controller          *controlsys.System        `json:"-"`
	ClosedLoop          *controlsys.System        `json:"-"`
	edit                *candidateBlockEdit
}

type robustSynthesisContext struct {
	snapshot Snapshot
	spec     ControlRoleSpec
	resolved resolvedControlRoleSpec
	models   ControlModelSet
}

func (s *Studio) DesignRobustController(
	ctx context.Context,
	flowID int64,
	request RobustSynthesisRequest,
) (RobustSynthesisCandidate, error) {
	design, err := s.robustSynthesisContext(ctx, flowID, request)
	if err != nil {
		return RobustSynthesisCandidate{}, err
	}
	generalized := design.models.GeneralizedPlant
	exogenous := namedChannelNames(design.resolved.Plant.ExogenousInputs)
	control := namedChannelNames(design.resolved.Plant.ControlInputs)
	regulated := namedChannelNames(design.resolved.Plant.PerformanceOutputs)
	measurement := namedChannelNames(design.resolved.Plant.MeasurementOutputs)
	nmeas, ncont := len(measurement), len(control)

	var (
		controller *controlsys.System
		x, y       *mat.Dense
		gamma      float64
		poles      []complex128
	)
	switch request.Method {
	case RobustSynthesisH2:
		result, synthErr := safeH2Synthesis(generalized, nmeas, ncont)
		if synthErr != nil {
			return RobustSynthesisCandidate{}, robustSynthesisError(
				request.Method, exogenous, regulated, measurement, control, synthErr,
			)
		}
		controller, x, y, poles = result.K, result.X, result.Y, result.CLPoles
	case RobustSynthesisHinf:
		result, synthErr := safeHinfSynthesis(generalized, nmeas, ncont)
		if synthErr != nil {
			return RobustSynthesisCandidate{}, robustSynthesisError(
				request.Method, exogenous, regulated, measurement, control, synthErr,
			)
		}
		controller, x, y, gamma, poles = result.K, result.X, result.Y,
			result.GammaOpt, result.CLPoles
	default:
		return RobustSynthesisCandidate{}, invalid(
			"robust synthesis method must be %q or %q",
			RobustSynthesisH2, RobustSynthesisHinf,
		)
	}
	if err := nameRobustController(controller, request.Method, measurement, control); err != nil {
		return RobustSynthesisCandidate{}, err
	}
	closedLoop, err := controlsys.LFT(
		generalized, controller, len(exogenous), len(regulated),
	)
	if err != nil {
		return RobustSynthesisCandidate{}, fmt.Errorf(
			"validate %s generalized closed loop: %w", request.Method, err,
		)
	}
	if err := validateRobustClosedLoop(
		closedLoop, exogenous, regulated, poles,
	); err != nil {
		return RobustSynthesisCandidate{}, err
	}
	stable, _ := closedLoop.IsStable()
	evidence := RobustSynthesisEvidence{
		StableClosedLoop: stable,
		GammaBound:       gamma,
		ClosedLoopPoles:  complexValues(poles),
		XCondition:       denseConditionNumber(x),
		YCondition:       denseConditionNumber(y),
	}
	gammaRelativeExcess := 0.0
	switch request.Method {
	case RobustSynthesisH2:
		evidence.AchievedNorm, err = controlsys.H2Norm(closedLoop)
	case RobustSynthesisHinf:
		evidence.AchievedNorm, evidence.PeakFrequency, err =
			controlsys.HinfNorm(closedLoop)
		if err == nil && evidence.AchievedNorm > gamma {
			gammaRelativeExcess = (evidence.AchievedNorm - gamma) /
				math.Max(gamma, 1e-12)
			if gammaRelativeExcess > 0.01 {
				err = fmt.Errorf(
					"measured H-infinity norm %.6g exceeds synthesis bound %.6g by %.3g%%",
					evidence.AchievedNorm, gamma, 100*gammaRelativeExcess,
				)
			}
		}
	}
	if err != nil {
		return RobustSynthesisCandidate{}, fmt.Errorf(
			"validate %s achieved norm: %w", request.Method, err,
		)
	}
	xValue, err := matrixValuePointer(x)
	if err != nil {
		return RobustSynthesisCandidate{}, err
	}
	yValue, err := matrixValuePointer(y)
	if err != nil {
		return RobustSynthesisCandidate{}, err
	}
	candidate := RobustSynthesisCandidate{
		FlowID:              design.snapshot.Flow.ID,
		SourceModelRevision: design.snapshot.Flow.ModelUpdatedAt,
		SourceControlRoles:  newControlRoleSnapshot(design.spec),
		Method:              request.Method,
		Partitions: RobustSynthesisPartitions{
			Exogenous: exogenous, Regulated: regulated,
			Measurement: measurement, Control: control,
		},
		Goals: []ControllerDesignGoal{{
			Name: "generalized closed-loop norm",
			Target: fmt.Sprintf("minimize %s from [%s] to [%s]",
				strings.ToUpper(string(request.Method)),
				strings.Join(exogenous, ", "), strings.Join(regulated, ", ")),
		}},
		Evidence: evidence, X: xValue, Y: yValue,
		Controller: controller, ClosedLoop: closedLoop,
	}
	for _, diagnostic := range []struct {
		label     string
		condition float64
	}{
		{label: "X Riccati solution", condition: evidence.XCondition},
		{label: "Y Riccati solution", condition: evidence.YCondition},
	} {
		if math.IsInf(diagnostic.condition, 1) || diagnostic.condition > 1e10 {
			candidate.Warnings = append(
				candidate.Warnings,
				fmt.Sprintf(
					"%s is ill-conditioned (condition %.6g)",
					diagnostic.label,
					diagnostic.condition,
				),
			)
		}
	}
	if gammaRelativeExcess > 0 {
		candidate.Warnings = append(
			candidate.Warnings,
			fmt.Sprintf(
				"Measured H-infinity norm exceeds the solver gamma by %.3g%%; both values are retained as numerical evidence.",
				100*gammaRelativeExcess,
			),
		)
	}
	editor := stateDesignPlant{snapshot: design.snapshot, spec: design.spec}
	candidate.edit, err = editor.stateSpaceControllerEdit(controller)
	if err != nil {
		return RobustSynthesisCandidate{}, err
	}
	if candidate.edit == nil {
		candidate.Warnings = append(
			candidate.Warnings,
			"Apply requires exactly one authored continuous State-Space controller block.",
		)
	}
	return candidate, nil
}

func safeH2Synthesis(
	generalized *controlsys.System,
	measurements, controls int,
) (result *controlsys.H2SynResult, err error) {
	return containSynthesisPanic("controlsys H2Syn", func() (*controlsys.H2SynResult, error) {
		return controlsys.H2Syn(generalized, measurements, controls)
	})
}

func safeHinfSynthesis(
	generalized *controlsys.System,
	measurements, controls int,
) (result *controlsys.HinfSynResult, err error) {
	return containSynthesisPanic("controlsys HinfSyn", func() (*controlsys.HinfSynResult, error) {
		return controlsys.HinfSyn(generalized, measurements, controls)
	})
}

func containSynthesisPanic[T any](
	label string,
	operation func() (T, error),
) (result T, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%s panicked: %v", label, recovered)
		}
	}()
	return operation()
}

func (s *Studio) robustSynthesisContext(
	ctx context.Context,
	flowID int64,
	request RobustSynthesisRequest,
) (robustSynthesisContext, error) {
	if request.Method != RobustSynthesisH2 &&
		request.Method != RobustSynthesisHinf {
		return robustSynthesisContext{}, invalid(
			"robust synthesis method must be %q or %q",
			RobustSynthesisH2, RobustSynthesisHinf,
		)
	}
	if request.BaseStep < 0 || !finite(request.BaseStep) {
		return robustSynthesisContext{}, invalid(
			"robust synthesis base step must be non-negative and finite",
		)
	}
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return robustSynthesisContext{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return robustSynthesisContext{}, err
	}
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		return robustSynthesisContext{}, err
	}
	if len(resolved.Plant.ExogenousInputs) == 0 ||
		len(resolved.Plant.ControlInputs) == 0 ||
		len(resolved.Plant.PerformanceOutputs) == 0 ||
		len(resolved.Plant.MeasurementOutputs) == 0 {
		return robustSynthesisContext{}, invalid(
			"robust synthesis requires named exogenous, regulated, measurement, and control partitions",
		)
	}
	models, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{BaseStep: request.BaseStep},
	)
	if err != nil {
		return robustSynthesisContext{}, err
	}
	generalized := models.GeneralizedPlant
	if err := validateRobustGeneralizedPlant(generalized); err != nil {
		return robustSynthesisContext{}, err
	}
	return robustSynthesisContext{
		snapshot: snapshot, spec: spec, resolved: resolved, models: models,
	}, nil
}

func validateRobustGeneralizedPlant(generalized *controlsys.System) error {
	if generalized == nil {
		return invalid("robust synthesis requires a generalized plant")
	}
	n, _, _ := generalized.Dims()
	switch {
	case n == 0:
		return invalid("robust synthesis requires a generalized plant with states")
	case !generalized.IsContinuous():
		return invalid("robust synthesis requires a continuous generalized plant")
	case generalized.IsDescriptor():
		return invalid("robust synthesis does not support descriptor generalized plants")
	case generalized.HasDelay() || generalized.HasInternalDelay():
		return invalid(
			"robust synthesis refuses delayed generalized plants because H2Syn and HinfSyn do not preserve delay semantics",
		)
	}
	return nil
}

func robustSynthesisError(
	method RobustSynthesisMethod,
	exogenous, regulated, measurement, control []string,
	err error,
) error {
	return fmt.Errorf(
		"controlsys %s synthesis for w=[%s], z=[%s], y=[%s], u=[%s]: %w",
		method, strings.Join(exogenous, ", "), strings.Join(regulated, ", "),
		strings.Join(measurement, ", "), strings.Join(control, ", "), err,
	)
}

func nameRobustController(
	controller *controlsys.System,
	method RobustSynthesisMethod,
	measurement, control []string,
) error {
	if controller == nil {
		return invalid("controlsys robust synthesis returned no controller")
	}
	states, inputs, outputs := controller.Dims()
	if inputs != len(measurement) || outputs != len(control) {
		return invalid(
			"robust controller dimensions are %d×%d; named roles require %d×%d",
			outputs, inputs, len(control), len(measurement),
		)
	}
	if err := controller.SetInputName(measurement...); err != nil {
		return err
	}
	if err := controller.SetOutputName(control...); err != nil {
		return err
	}
	if err := controller.SetStateName(
		indexedNames(string(method)+".state.", states)...,
	); err != nil {
		return err
	}
	return nil
}

func validateRobustClosedLoop(
	closedLoop *controlsys.System,
	exogenous, regulated []string,
	synthesisPoles []complex128,
) error {
	if closedLoop == nil {
		return invalid("robust synthesis returned no generalized closed loop")
	}
	_, inputs, outputs := closedLoop.Dims()
	if inputs != len(exogenous) || outputs != len(regulated) {
		return invalid(
			"generalized closed loop dimensions are %d×%d; named roles require %d×%d",
			outputs, inputs, len(regulated), len(exogenous),
		)
	}
	if !slices.Equal(closedLoop.InputName, exogenous) ||
		!slices.Equal(closedLoop.OutputName, regulated) {
		return invalid(
			"generalized closed loop did not preserve named exogenous and regulated channels",
		)
	}
	stable, err := closedLoop.IsStable()
	if err != nil {
		return fmt.Errorf("evaluate robust closed-loop stability: %w", err)
	}
	if !stable {
		return invalid("synthesized generalized closed loop is unstable")
	}
	actualPoles, err := closedLoop.Poles()
	if err != nil {
		return err
	}
	if len(actualPoles) != len(synthesisPoles) {
		return invalid(
			"synthesis reported %d closed-loop poles but assembled loop has %d",
			len(synthesisPoles), len(actualPoles),
		)
	}
	if !complexMultisetsClose(actualPoles, synthesisPoles, 1e-7) {
		return invalid(
			"synthesis-reported closed-loop poles do not match the independently assembled loop",
		)
	}
	return nil
}

func complexMultisetsClose(actual, expected []complex128, relativeTolerance float64) bool {
	if len(actual) != len(expected) || relativeTolerance < 0 {
		return false
	}
	used := make([]bool, len(expected))
	for _, actualValue := range actual {
		matched := false
		for index, expectedValue := range expected {
			if used[index] {
				continue
			}
			scale := math.Max(1, math.Max(
				cmplxAbs(actualValue),
				cmplxAbs(expectedValue),
			))
			if cmplxAbs(actualValue-expectedValue) <= relativeTolerance*scale {
				used[index] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func cmplxAbs(value complex128) float64 {
	return math.Hypot(real(value), imag(value))
}

func denseConditionNumber(matrix *mat.Dense) float64 {
	if matrix == nil {
		return math.Inf(1)
	}
	var decomposition mat.SVD
	if !decomposition.Factorize(matrix, mat.SVDThin) {
		return math.Inf(1)
	}
	values := decomposition.Values(nil)
	if len(values) == 0 || values[len(values)-1] <= 0 {
		return math.Inf(1)
	}
	return values[0] / values[len(values)-1]
}

func (s *Studio) ReviewRobustSynthesisCandidate(
	ctx context.Context,
	candidate RobustSynthesisCandidate,
	horizon float64,
) (ControllerCandidateReview, error) {
	return s.reviewControllerCandidate(
		ctx,
		controllerReviewRequest{
			flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
			controlRoles: candidate.SourceControlRoles, kind: "robust-synthesis",
			algorithm: string(candidate.Method), goals: candidate.Goals,
			warnings: candidate.Warnings, controller: candidate.Controller,
			controllerIsSigned: true, horizon: horizon,
			applyAvailable: candidate.edit != nil,
		},
	)
}

func (s *Studio) ApplyRobustSynthesisCandidate(
	ctx context.Context,
	candidate RobustSynthesisCandidate,
) (ControllerCandidateApplication, error) {
	if candidate.FlowID <= 0 || candidate.SourceModelRevision.IsZero() ||
		candidate.edit == nil {
		return ControllerCandidateApplication{}, invalid(
			"robust-synthesis candidate cannot be applied to the authored controller block",
		)
	}
	return s.applyCandidateBlockEditWithUndo(ctx, candidateApplyRequest{
		flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
		controlRoles: candidate.SourceControlRoles, edit: candidate.edit,
		event: fmt.Sprintf(
			"Applied %s robust-synthesis candidate", candidate.Method,
		),
	})
}
