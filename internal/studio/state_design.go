package studio

import (
	"context"
	"fmt"
	"math"
	"math/cmplx"
	"strings"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

type StateFeedbackMethod string

const (
	StateFeedbackLQR   StateFeedbackMethod = "lqr"
	StateFeedbackLQI   StateFeedbackMethod = "lqi"
	StateFeedbackLQRD  StateFeedbackMethod = "lqrd"
	StateFeedbackAcker StateFeedbackMethod = "acker"
	StateFeedbackPlace StateFeedbackMethod = "place"
)

type EstimatorMethod string

const (
	EstimatorLQE    EstimatorMethod = "lqe"
	EstimatorKalman EstimatorMethod = "kalman"
	EstimatorKalmd  EstimatorMethod = "kalmd"
	EstimatorPlace  EstimatorMethod = "place"
)

type ObserverRegulatorMethod string

const (
	ObserverRegulatorLQG ObserverRegulatorMethod = "lqg"
	ObserverRegulatorReg ObserverRegulatorMethod = "reg"
)

type StateFeedbackRequest struct {
	Method          StateFeedbackMethod `json:"method"`
	Q               *MatrixValue        `json:"q,omitempty"`
	R               *MatrixValue        `json:"r,omitempty"`
	RegulatedOutput *MatrixValue        `json:"regulatedOutput,omitempty"`
	Poles           []ComplexValue      `json:"poles,omitempty"`
	SampleTime      float64             `json:"sampleTime,omitempty"`
	BaseStep        float64             `json:"baseStep,omitempty"`
}

type EstimatorDesignRequest struct {
	Method     EstimatorMethod `json:"method"`
	Qn         *MatrixValue    `json:"qn,omitempty"`
	Rn         *MatrixValue    `json:"rn,omitempty"`
	G          *MatrixValue    `json:"g,omitempty"`
	Poles      []ComplexValue  `json:"poles,omitempty"`
	SampleTime float64         `json:"sampleTime,omitempty"`
	BaseStep   float64         `json:"baseStep,omitempty"`
}

type ObserverRegulatorRequest struct {
	Method   ObserverRegulatorMethod `json:"method"`
	Q        *MatrixValue            `json:"q,omitempty"`
	R        *MatrixValue            `json:"r,omitempty"`
	Qn       *MatrixValue            `json:"qn,omitempty"`
	Rn       *MatrixValue            `json:"rn,omitempty"`
	K        *MatrixValue            `json:"k,omitempty"`
	L        *MatrixValue            `json:"l,omitempty"`
	BaseStep float64                 `json:"baseStep,omitempty"`
}

type StateDesignDiagnostics struct {
	States           int  `json:"states"`
	Controls         int  `json:"controls"`
	Measurements     int  `json:"measurements"`
	ControllableRank int  `json:"controllableRank"`
	ObservableRank   int  `json:"observableRank"`
	Controllable     bool `json:"controllable"`
	Observable       bool `json:"observable"`
	Stabilizable     bool `json:"stabilizable"`
	Detectable       bool `json:"detectable"`
}

type StateDesignCandidate struct {
	FlowID              int64                  `json:"flowId"`
	SourceModelRevision time.Time              `json:"sourceModelRevision"`
	SourceControlRoles  ControlRoleSnapshot    `json:"sourceControlRoles"`
	Method              string                 `json:"method"`
	Goals               []ControllerDesignGoal `json:"goals"`
	Diagnostics         StateDesignDiagnostics `json:"diagnostics"`
	StateNames          []string               `json:"stateNames"`
	MeasurementNames    []string               `json:"measurementNames"`
	ControlNames        []string               `json:"controlNames"`
	GainK               *MatrixValue           `json:"gainK,omitempty"`
	GainL               *MatrixValue           `json:"gainL,omitempty"`
	RiccatiX            *MatrixValue           `json:"riccatiX,omitempty"`
	ClosedLoopPoles     []ComplexValue         `json:"closedLoopPoles,omitempty"`
	EstimatorPoles      []ComplexValue         `json:"estimatorPoles,omitempty"`
	ReciprocalCondition *float64               `json:"reciprocalCondition,omitempty"`
	Warnings            []string               `json:"warnings,omitempty"`
	Controller          *controlsys.System     `json:"-"`
	Estimator           *controlsys.System     `json:"-"`
	edit                *candidateBlockEdit
}

type stateDesignPlant struct {
	snapshot Snapshot
	spec     ControlRoleSpec
	models   ControlModelSet
	plant    *controlsys.System
	diag     StateDesignDiagnostics
}

func (s *Studio) DesignStateFeedback(
	ctx context.Context,
	flowID int64,
	request StateFeedbackRequest,
) (StateDesignCandidate, error) {
	design, err := s.stateDesignPlant(ctx, flowID, request.BaseStep)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	plant := design.plant
	n, m, _ := plant.Dims()
	candidate := design.candidate(string(request.Method))
	candidate.Goals = stateFeedbackDesignGoals(request)
	var result *controlsys.RiccatiResult
	var gain *mat.Dense
	controllerPlant := plant
	if request.Method != StateFeedbackLQI {
		if _, err := fullStateMeasurementPermutation(plant); err != nil {
			return StateDesignCandidate{}, err
		}
	}
	switch request.Method {
	case StateFeedbackLQR:
		if !design.diag.Stabilizable {
			return StateDesignCandidate{}, invalid("LQR requires a stabilizable selected plant")
		}
		q, r, err := stateCostMatrices(request.Q, request.R, n, m)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		if plant.IsContinuous() {
			result, err = controlsys.Lqr(plant.A, plant.B, q, r, nil)
		} else {
			result, err = controlsys.Dlqr(plant.A, plant.B, q, r, nil)
		}
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys %s: %w", request.Method, err)
		}
		gain = result.K
	case StateFeedbackLQRD:
		if !design.diag.Stabilizable {
			return StateDesignCandidate{}, invalid("LQRD requires a stabilizable selected plant")
		}
		if !plant.IsContinuous() {
			return StateDesignCandidate{}, invalid("LQRD requires a continuous plant role")
		}
		if request.SampleTime <= 0 || !finite(request.SampleTime) {
			return StateDesignCandidate{}, invalid("LQRD sample time must be positive and finite")
		}
		q, r, err := stateCostMatrices(request.Q, request.R, n, m)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		result, err = controlsys.Lqrd(
			plant.A, plant.B, q, r, request.SampleTime, nil,
		)
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys lqrd: %w", err)
		}
		gain = result.K
		controllerPlant, err = plant.DiscretizeZOH(request.SampleTime)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		candidate.Warnings = append(candidate.Warnings,
			"LQRD discretizes A and B with zero-order hold but does not integrate continuous Q/R costs; sampled-loop apply is unavailable.",
		)
	case StateFeedbackLQI:
		if !design.diag.Stabilizable {
			return StateDesignCandidate{}, invalid("LQI requires a stabilizable selected plant")
		}
		regulatedC := plant.C
		if request.RegulatedOutput != nil {
			regulatedC = denseMatrix(request.RegulatedOutput)
			outputs, columns := regulatedC.Dims()
			if outputs == 0 || columns != n {
				return StateDesignCandidate{}, invalid(
					"LQI regulated-output matrix must have %d columns", n,
				)
			}
		} else if !matrixIsZero(plant.D, 1e-13) {
			return StateDesignCandidate{}, invalid("LQI requires regulated outputs with zero direct feedthrough")
		}
		outputs, _ := regulatedC.Dims()
		q, r, err := stateCostMatrices(request.Q, request.R, n+outputs, m)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		if plant.IsContinuous() {
			result, err = controlsys.Lqi(plant.A, plant.B, regulatedC, q, r, nil)
		} else {
			aaug, baug := discreteLQIAugmentation(plant, regulatedC)
			result, err = controlsys.Dlqr(aaug, baug, q, r, nil)
		}
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys lqi: %w", err)
		}
		gain = result.K
	case StateFeedbackAcker, StateFeedbackPlace:
		poles, err := validateRequestedPoles(request.Poles, n, plant.IsContinuous())
		if err != nil {
			return StateDesignCandidate{}, err
		}
		if !design.diag.Controllable {
			return StateDesignCandidate{}, invalid("%s requires a fully controllable selected plant", request.Method)
		}
		if request.Method == StateFeedbackAcker && n > 10 {
			candidate.Warnings = append(candidate.Warnings,
				"Acker is numerically fragile above order ten; prefer Place.",
			)
		}
		if request.Method == StateFeedbackAcker {
			gain, err = controlsys.Acker(plant.A, plant.B, poles)
		} else {
			gain, err = controlsys.Place(plant.A, plant.B, poles)
		}
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys %s: %w", request.Method, err)
		}
	default:
		return StateDesignCandidate{}, invalid("unknown state-feedback method %q", request.Method)
	}
	candidate.GainK, err = matrixValuePointer(gain)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	if result != nil {
		candidate.RiccatiX, err = matrixValuePointer(result.X)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		candidate.ReciprocalCondition = finitePointer(result.Rcnd)
	}
	if request.Method == StateFeedbackLQI {
		regulatedC := plant.C
		regulatedNames := append([]string(nil), plant.OutputName...)
		if request.RegulatedOutput != nil {
			regulatedC = denseMatrix(request.RegulatedOutput)
			outputs, _ := regulatedC.Dims()
			regulatedNames = indexedNames("regulated", outputs)
		}
		controller, err := lqiControllerSystem(
			plant, gain, regulatedC, regulatedNames,
		)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		candidate.Controller = controller
		candidate.ClosedLoopPoles = complexValues(result.Eig)
		candidate.edit, err = design.stateSpaceControllerEdit(controller)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		return candidate, nil
	}
	closedA := mat.NewDense(n, n, nil)
	closedA.Mul(controllerPlant.B, gain)
	closedA.Sub(controllerPlant.A, closedA)
	eigenvalues, err := denseEigenvalues(closedA)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	candidate.ClosedLoopPoles = complexValues(eigenvalues)
	controller, err := stateFeedbackController(controllerPlant, gain)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	candidate.Controller = controller
	if request.Method != StateFeedbackLQRD {
		candidate.edit, err = design.matrixGainControllerEdit(controller)
		if err != nil {
			return StateDesignCandidate{}, err
		}
	}
	return candidate, nil
}

func (s *Studio) DesignEstimator(
	ctx context.Context,
	flowID int64,
	request EstimatorDesignRequest,
) (StateDesignCandidate, error) {
	design, err := s.stateDesignPlant(ctx, flowID, request.BaseStep)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	plant := design.plant
	n, m, p := plant.Dims()
	candidate := design.candidate(string(request.Method))
	candidate.Goals = estimatorDesignGoals(request)
	var result *controlsys.RiccatiResult
	var gain *mat.Dense
	estimatorPlant := plant
	switch request.Method {
	case EstimatorLQE:
		if !design.diag.Detectable {
			return StateDesignCandidate{}, invalid("LQE requires a detectable selected plant")
		}
		if !plant.IsContinuous() {
			return StateDesignCandidate{}, invalid("LQE with explicit G requires a continuous plant")
		}
		if request.G == nil {
			return StateDesignCandidate{}, invalid("LQE requires an explicit process-noise matrix G")
		}
		g := denseMatrix(request.G)
		gRows, noises := g.Dims()
		if gRows != n {
			return StateDesignCandidate{}, invalid(
				"LQE process-noise matrix G must have %d rows; got %d", n, gRows,
			)
		}
		qn, rn, err := covarianceMatrices(request.Qn, request.Rn, noises, p)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		result, err = controlsys.Lqe(plant.A, g, plant.C, qn, rn, nil)
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys lqe: %w", err)
		}
		gain = result.K
	case EstimatorKalman:
		if !design.diag.Detectable {
			return StateDesignCandidate{}, invalid("Kalman requires a detectable selected plant")
		}
		qn, rn, err := covarianceMatrices(request.Qn, request.Rn, m, p)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		result, err = controlsys.Kalman(plant, qn, rn, nil)
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys kalman: %w", err)
		}
		gain = result.K
	case EstimatorKalmd:
		if !design.diag.Detectable {
			return StateDesignCandidate{}, invalid("Kalmd requires a detectable selected plant")
		}
		if !plant.IsContinuous() {
			return StateDesignCandidate{}, invalid("Kalmd requires a continuous plant")
		}
		if request.SampleTime <= 0 || !finite(request.SampleTime) {
			return StateDesignCandidate{}, invalid("Kalmd sample time must be positive and finite")
		}
		qn, rn, err := covarianceMatrices(request.Qn, request.Rn, m, p)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		result, err = controlsys.Kalmd(
			plant, qn, rn, request.SampleTime, nil,
		)
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys kalmd: %w", err)
		}
		gain = result.K
		estimatorPlant, err = plant.DiscretizeZOH(request.SampleTime)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		candidate.Warnings = append(candidate.Warnings,
			"Kalmd is realized against the ZOH-discretized plant; applying it inside the current continuous loop is unavailable.",
		)
	case EstimatorPlace:
		poles, err := validateRequestedPoles(request.Poles, n, plant.IsContinuous())
		if err != nil {
			return StateDesignCandidate{}, err
		}
		if !design.diag.Observable {
			return StateDesignCandidate{}, invalid("observer placement requires a fully observable selected plant")
		}
		dual, err := controlsys.Place(
			mat.DenseCopyOf(plant.A.T()),
			mat.DenseCopyOf(plant.C.T()),
			poles,
		)
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys observer place: %w", err)
		}
		gain = mat.DenseCopyOf(dual.T())
	default:
		return StateDesignCandidate{}, invalid("unknown estimator method %q", request.Method)
	}
	candidate.GainL, err = matrixValuePointer(gain)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	if result != nil {
		candidate.RiccatiX, err = matrixValuePointer(result.X)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		candidate.ReciprocalCondition = finitePointer(result.Rcnd)
	}
	estimatorA := mat.NewDense(n, n, nil)
	estimatorA.Mul(gain, estimatorPlant.C)
	estimatorA.Sub(estimatorPlant.A, estimatorA)
	eigenvalues, err := denseEigenvalues(estimatorA)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	candidate.EstimatorPoles = complexValues(eigenvalues)
	estimator, err := controlsys.Estim(estimatorPlant, gain)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	nameEstimator(estimator, estimatorPlant)
	candidate.Estimator = estimator
	return candidate, nil
}

func (s *Studio) DesignObserverRegulator(
	ctx context.Context,
	flowID int64,
	request ObserverRegulatorRequest,
) (StateDesignCandidate, error) {
	design, err := s.stateDesignPlant(ctx, flowID, request.BaseStep)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	plant := design.plant
	n, m, p := plant.Dims()
	candidate := design.candidate(string(request.Method))
	candidate.Goals = observerRegulatorDesignGoals(request)
	var k, l *mat.Dense
	switch request.Method {
	case ObserverRegulatorLQG:
		if !design.diag.Stabilizable || !design.diag.Detectable {
			return StateDesignCandidate{}, invalid(
				"LQG requires stabilizable and detectable selected plant roles",
			)
		}
		q, r, err := stateCostMatrices(request.Q, request.R, n, m)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		qn, rn, err := covarianceMatrices(request.Qn, request.Rn, m, p)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		result, err := controlsys.Lqg(plant, q, r, qn, rn, nil)
		if err != nil {
			return StateDesignCandidate{}, fmt.Errorf("controlsys lqg: %w", err)
		}
		k, l = result.K, result.L
		candidate.RiccatiX, err = matrixValuePointer(result.Xc)
		if err != nil {
			return StateDesignCandidate{}, err
		}
		candidate.Controller = result.Controller.Copy()
	case ObserverRegulatorReg:
		if request.K == nil || request.L == nil {
			return StateDesignCandidate{}, invalid("observer regulator requires K and L matrices")
		}
		k, l = denseMatrix(request.K), denseMatrix(request.L)
		controller, err := controlsys.Reg(plant, k, l)
		if err != nil {
			return StateDesignCandidate{}, invalid("controlsys regulator: %s", err)
		}
		candidate.Controller = controller
	default:
		return StateDesignCandidate{}, invalid("unknown observer-regulator method %q", request.Method)
	}
	candidate.GainK, err = matrixValuePointer(k)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	candidate.GainL, err = matrixValuePointer(l)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	if err := candidate.Controller.SetInputName(plant.OutputName...); err != nil {
		return StateDesignCandidate{}, err
	}
	if err := candidate.Controller.SetOutputName(plant.InputName...); err != nil {
		return StateDesignCandidate{}, err
	}
	_ = candidate.Controller.SetStateName(prefixedNames("estimate-state.", plant.StateName)...)
	estimatorA := mat.NewDense(n, n, nil)
	estimatorA.Mul(l, plant.C)
	estimatorA.Sub(plant.A, estimatorA)
	estimatorPoles, err := denseEigenvalues(estimatorA)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	candidate.EstimatorPoles = complexValues(estimatorPoles)
	closedA := mat.NewDense(n, n, nil)
	closedA.Mul(plant.B, k)
	closedA.Sub(plant.A, closedA)
	closedPoles, err := denseEigenvalues(closedA)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	candidate.ClosedLoopPoles = complexValues(closedPoles)
	candidate.edit, err = design.stateSpaceControllerEdit(candidate.Controller)
	if err != nil {
		return StateDesignCandidate{}, err
	}
	return candidate, nil
}

func (s *Studio) stateDesignPlant(
	ctx context.Context,
	flowID int64,
	baseStep float64,
) (stateDesignPlant, error) {
	if baseStep < 0 || !finite(baseStep) {
		return stateDesignPlant{}, invalid("state-design base step must be non-negative and finite")
	}
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return stateDesignPlant{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return stateDesignPlant{}, err
	}
	resolved, err := resolveControlRoleSpec(snapshot.Blocks, snapshot.Connections, spec)
	if err != nil {
		return stateDesignPlant{}, err
	}
	models, err := buildControlModels(
		snapshot, resolved, ControlModelBuildRequest{BaseStep: baseStep},
	)
	if err != nil {
		return stateDesignPlant{}, err
	}
	plant := models.EstimatorPlant
	n, m, p := plant.Dims()
	if n == 0 {
		return stateDesignPlant{}, invalid("state synthesis requires a plant with states")
	}
	if plant.IsDescriptor() {
		return stateDesignPlant{}, invalid("state synthesis does not support descriptor plants")
	}
	if plant.HasDelay() || plant.HasInternalDelay() {
		return stateDesignPlant{}, invalid("state synthesis does not support delayed plants without explicit augmentation")
	}
	if err := validateStateNames(plant.StateName, n); err != nil {
		return stateDesignPlant{}, err
	}
	continuous := plant.IsContinuous()
	ctrb, err := controlsys.Ctrb(plant.A, plant.B)
	if err != nil {
		return stateDesignPlant{}, err
	}
	obsv, err := controlsys.Obsv(plant.A, plant.C)
	if err != nil {
		return stateDesignPlant{}, err
	}
	ctrbRank := denseRank(ctrb)
	obsvRank := denseRank(obsv)
	stabilizable, err := controlsys.IsStabilizable(plant.A, plant.B, continuous)
	if err != nil {
		return stateDesignPlant{}, err
	}
	detectable, err := controlsys.IsDetectable(plant.A, plant.C, continuous)
	if err != nil {
		return stateDesignPlant{}, err
	}
	return stateDesignPlant{
		snapshot: snapshot, spec: spec, models: models, plant: plant,
		diag: StateDesignDiagnostics{
			States: n, Controls: m, Measurements: p,
			ControllableRank: ctrbRank, ObservableRank: obsvRank,
			Controllable: ctrbRank == n, Observable: obsvRank == n,
			Stabilizable: stabilizable, Detectable: detectable,
		},
	}, nil
}

func (design stateDesignPlant) candidate(method string) StateDesignCandidate {
	return StateDesignCandidate{
		FlowID:              design.snapshot.Flow.ID,
		SourceModelRevision: design.snapshot.Flow.ModelUpdatedAt,
		SourceControlRoles:  newControlRoleSnapshot(design.spec),
		Method:              method, Diagnostics: design.diag,
		StateNames:       append([]string(nil), design.plant.StateName...),
		MeasurementNames: append([]string(nil), design.plant.OutputName...),
		ControlNames:     append([]string(nil), design.plant.InputName...),
	}
}

func stateFeedbackDesignGoals(
	request StateFeedbackRequest,
) []ControllerDesignGoal {
	switch request.Method {
	case StateFeedbackLQR, StateFeedbackLQRD:
		return []ControllerDesignGoal{{
			Name:   "optimal state feedback",
			Target: matrixCostGoal(request.Q, request.R),
		}}
	case StateFeedbackLQI:
		return []ControllerDesignGoal{{
			Name:   "integral state feedback",
			Target: matrixCostGoal(request.Q, request.R),
		}}
	case StateFeedbackAcker, StateFeedbackPlace:
		return []ControllerDesignGoal{{
			Name:   "closed-loop pole placement",
			Target: complexGoalText(request.Poles),
		}}
	default:
		return []ControllerDesignGoal{{
			Name: "state feedback", Target: string(request.Method),
		}}
	}
}

func estimatorDesignGoals(
	request EstimatorDesignRequest,
) []ControllerDesignGoal {
	if request.Method == EstimatorPlace {
		return []ControllerDesignGoal{{
			Name:   "estimator pole placement",
			Target: complexGoalText(request.Poles),
		}}
	}
	return []ControllerDesignGoal{{
		Name: "state estimation",
		Target: fmt.Sprintf(
			"%s with Qn=%s and Rn=%s",
			request.Method, matrixGoalText(request.Qn), matrixGoalText(request.Rn),
		),
	}}
}

func observerRegulatorDesignGoals(
	request ObserverRegulatorRequest,
) []ControllerDesignGoal {
	if request.Method == ObserverRegulatorReg {
		return []ControllerDesignGoal{{
			Name: "observer regulator",
			Target: fmt.Sprintf(
				"explicit K=%s and L=%s",
				matrixGoalText(request.K), matrixGoalText(request.L),
			),
		}}
	}
	return []ControllerDesignGoal{{
		Name: "LQG output feedback",
		Target: fmt.Sprintf(
			"Q=%s; R=%s; Qn=%s; Rn=%s",
			matrixGoalText(request.Q), matrixGoalText(request.R),
			matrixGoalText(request.Qn), matrixGoalText(request.Rn),
		),
	}}
}

func matrixCostGoal(q, r *MatrixValue) string {
	return fmt.Sprintf("Q=%s; R=%s", matrixGoalText(q), matrixGoalText(r))
}

func matrixGoalText(value *MatrixValue) string {
	if value == nil {
		return "unspecified"
	}
	return strings.ReplaceAll(value.Text(), "\n", "; ")
}

func complexGoalText(values []ComplexValue) string {
	if len(values) == 0 {
		return "unspecified"
	}
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = fmt.Sprintf("%.6g%+.6gi", value.Real, value.Imag)
	}
	return strings.Join(parts, ", ")
}

func (design stateDesignPlant) controllerBlock() (Block, bool) {
	if len(design.spec.Controller.Blocks) != 1 {
		return Block{}, false
	}
	return blockWithID(
		design.snapshot.Blocks, design.spec.Controller.Blocks[0],
	), true
}

func (design stateDesignPlant) matrixGainControllerEdit(
	controller *controlsys.System,
) (*candidateBlockEdit, error) {
	block, ok := design.controllerBlock()
	if !ok || block.Kind != BlockMatrixGain {
		return nil, nil
	}
	parameters := cloneParameters(block.Parameters)
	var err error
	parameters.D, err = matrixValuePointer(controller.D)
	if err != nil {
		return nil, err
	}
	inputs, _ := NewChannelNames(controller.InputName)
	outputs, _ := NewChannelNames(controller.OutputName)
	parameters.InputNames, parameters.OutputNames = &inputs, &outputs
	return &candidateBlockEdit{
		blockID: block.ID, expectedKind: block.Kind, parameters: parameters,
	}, nil
}

func (design stateDesignPlant) stateSpaceControllerEdit(
	controller *controlsys.System,
) (*candidateBlockEdit, error) {
	block, ok := design.controllerBlock()
	if !ok || (block.Kind != BlockStateSpace && block.Kind != BlockDiscreteStateSpace) {
		return nil, nil
	}
	parameters := cloneParameters(block.Parameters)
	var err error
	parameters.A, err = matrixValuePointer(controller.A)
	if err != nil {
		return nil, err
	}
	parameters.B, err = matrixValuePointer(controller.B)
	if err != nil {
		return nil, err
	}
	parameters.C, err = matrixValuePointer(controller.C)
	if err != nil {
		return nil, err
	}
	parameters.D, err = matrixValuePointer(controller.D)
	if err != nil {
		return nil, err
	}
	inputs, _ := NewChannelNames(controller.InputName)
	outputs, _ := NewChannelNames(controller.OutputName)
	states, _ := NewChannelNames(controller.StateName)
	parameters.InputNames, parameters.OutputNames, parameters.StateNames = &inputs, &outputs, &states
	parameters.TimeDomain = modelDomainContinuous
	parameters.SampleTime = controller.Dt
	if controller.IsDiscrete() {
		parameters.TimeDomain = modelDomainDiscrete
	}
	return &candidateBlockEdit{
		blockID: block.ID, expectedKind: block.Kind, parameters: parameters,
	}, nil
}

func (s *Studio) ApplyStateDesignCandidate(
	ctx context.Context,
	candidate StateDesignCandidate,
) (ControllerCandidateApplication, error) {
	if candidate.FlowID <= 0 || candidate.SourceModelRevision.IsZero() ||
		candidate.edit == nil {
		return ControllerCandidateApplication{}, invalid(
			"state-design candidate cannot be applied to the authored controller block",
		)
	}
	return s.applyCandidateBlockEditWithUndo(ctx, candidateApplyRequest{
		flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
		controlRoles: candidate.SourceControlRoles, edit: candidate.edit,
		event: fmt.Sprintf("Applied %s state-design candidate", candidate.Method),
	})
}

func stateCostMatrices(
	qValue, rValue *MatrixValue,
	states, controls int,
) (*mat.Dense, *mat.Dense, error) {
	if qValue == nil || rValue == nil {
		return nil, nil, invalid("state and control cost matrices Q and R are required")
	}
	q, r := denseMatrix(qValue), denseMatrix(rValue)
	if err := validateSymmetricMatrix("Q", q, states, false); err != nil {
		return nil, nil, err
	}
	if err := validateSymmetricMatrix("R", r, controls, true); err != nil {
		return nil, nil, err
	}
	return q, r, nil
}

func covarianceMatrices(
	qValue, rValue *MatrixValue,
	processNoises, measurements int,
) (*mat.Dense, *mat.Dense, error) {
	if qValue == nil || rValue == nil {
		return nil, nil, invalid("process and measurement covariance matrices Qn and Rn are required")
	}
	q, r := denseMatrix(qValue), denseMatrix(rValue)
	if err := validateSymmetricMatrix("Qn", q, processNoises, false); err != nil {
		return nil, nil, err
	}
	if err := validateSymmetricMatrix("Rn", r, measurements, true); err != nil {
		return nil, nil, err
	}
	return q, r, nil
}

func validateSymmetricMatrix(
	name string,
	matrix *mat.Dense,
	size int,
	positiveDefinite bool,
) error {
	rows, columns := matrix.Dims()
	if rows != size || columns != size {
		return invalid("%s must be %d×%d; got %d×%d", name, size, size, rows, columns)
	}
	symmetric := mat.NewSymDense(size, nil)
	for row := range size {
		for column := range row + 1 {
			if math.Abs(matrix.At(row, column)-matrix.At(column, row)) > 1e-12 {
				return invalid("%s must be symmetric", name)
			}
			symmetric.SetSym(row, column, matrix.At(row, column))
		}
	}
	var eigen mat.EigenSym
	if !eigen.Factorize(symmetric, false) {
		return invalid("%s eigenvalue check failed", name)
	}
	for _, value := range eigen.Values(nil) {
		if positiveDefinite && value <= 1e-12 {
			return invalid("%s must be positive definite", name)
		}
		if !positiveDefinite && value < -1e-12 {
			return invalid("%s must be positive semidefinite", name)
		}
	}
	return nil
}

func validateRequestedPoles(
	values []ComplexValue,
	states int,
	continuous bool,
) ([]complex128, error) {
	if len(values) != states {
		return nil, invalid("pole placement requires %d poles; got %d", states, len(values))
	}
	poles := make([]complex128, states)
	for i, value := range values {
		pole := complex(value.Real, value.Imag)
		if continuous && real(pole) >= 0 {
			return nil, invalid("continuous pole %d must lie in the open left half-plane", i+1)
		}
		if !continuous && cmplx.Abs(pole) >= 1 {
			return nil, invalid("discrete pole %d must lie inside the unit circle", i+1)
		}
		poles[i] = pole
	}
	for i, pole := range poles {
		if imag(pole) == 0 {
			continue
		}
		found := false
		for _, other := range poles {
			if cmplx.Abs(other-cmplx.Conj(pole)) < 1e-12 {
				found = true
				break
			}
		}
		if !found {
			return nil, invalid("pole %d requires its complex conjugate", i+1)
		}
	}
	return poles, nil
}

func stateFeedbackController(
	plant *controlsys.System,
	gain *mat.Dense,
) (*controlsys.System, error) {
	permutation, err := fullStateMeasurementPermutation(plant)
	if err != nil {
		return nil, err
	}
	controls, states := gain.Dims()
	data := make([]float64, controls*states)
	for measurement, state := range permutation {
		for control := range controls {
			data[control*states+measurement] = -gain.At(control, state)
		}
	}
	controller, err := controlsys.NewGain(
		mat.NewDense(controls, states, data), plant.Dt,
	)
	if err != nil {
		return nil, err
	}
	_ = controller.SetInputName(plant.OutputName...)
	_ = controller.SetOutputName(plant.InputName...)
	return controller, nil
}

func fullStateMeasurementPermutation(
	plant *controlsys.System,
) ([]int, error) {
	n, _, outputs := plant.Dims()
	if outputs != n || !matrixIsZero(plant.D, 1e-13) {
		return nil, invalid("direct state feedback requires full-state measurements and zero direct feedthrough")
	}
	permutation := make([]int, n)
	seen := make(map[int]struct{}, n)
	for output := range n {
		state := -1
		for column := range n {
			value := plant.C.At(output, column)
			if math.Abs(value-1) <= 1e-13 && state == -1 {
				state = column
			} else if math.Abs(value) > 1e-13 {
				return nil, invalid("direct state feedback requires measurement C to be a state permutation")
			}
		}
		if state < 0 {
			return nil, invalid("direct state feedback requires measurement C to be a state permutation")
		}
		if _, duplicate := seen[state]; duplicate {
			return nil, invalid("direct state feedback repeats state %d", state+1)
		}
		seen[state] = struct{}{}
		permutation[output] = state
	}
	return permutation, nil
}

func lqiControllerSystem(
	plant *controlsys.System,
	gain *mat.Dense,
	regulatedC *mat.Dense,
	regulatedNames []string,
) (*controlsys.System, error) {
	permutation, err := fullStateMeasurementPermutation(plant)
	if err != nil {
		return nil, err
	}
	n, m, _ := plant.Dims()
	p, _ := regulatedC.Dims()
	rows, columns := gain.Dims()
	if rows != m || columns != n+p {
		return nil, invalid("LQI returned %d×%d gain for %d states and %d outputs", rows, columns, n, p)
	}
	a := mat.NewDense(p, p, nil)
	if plant.IsDiscrete() {
		for i := range p {
			a.Set(i, i, 1)
		}
	}
	b := mat.NewDense(p, p+n, nil)
	for i := range p {
		b.Set(i, i, 1)
		for measurement, state := range permutation {
			b.Set(i, p+measurement, -regulatedC.At(i, state))
		}
	}
	c := mat.NewDense(m, p, nil)
	d := mat.NewDense(m, p+n, nil)
	for control := range m {
		for integral := range p {
			c.Set(control, integral, -gain.At(control, n+integral))
		}
		for measurement, state := range permutation {
			d.Set(control, p+measurement, -gain.At(control, state))
		}
	}
	controller, err := controlsys.New(a, b, c, d, plant.Dt)
	if err != nil {
		return nil, err
	}
	references := prefixedNames("reference.", regulatedNames)
	inputs := append(references, plant.OutputName...)
	_ = controller.SetInputName(inputs...)
	_ = controller.SetOutputName(plant.InputName...)
	_ = controller.SetStateName(prefixedNames("integral.", regulatedNames)...)
	return controller, nil
}

func discreteLQIAugmentation(
	plant *controlsys.System,
	regulatedC *mat.Dense,
) (*mat.Dense, *mat.Dense) {
	n, m, _ := plant.Dims()
	p, _ := regulatedC.Dims()
	a := mat.NewDense(n+p, n+p, nil)
	b := mat.NewDense(n+p, m, nil)
	for row := range n {
		for column := range n {
			a.Set(row, column, plant.A.At(row, column))
		}
		for column := range m {
			b.Set(row, column, plant.B.At(row, column))
		}
	}
	for row := range p {
		for column := range n {
			a.Set(n+row, column, -regulatedC.At(row, column))
		}
		a.Set(n+row, n+row, 1)
	}
	return a, b
}

func nameEstimator(estimator, plant *controlsys.System) {
	inputs := append(
		prefixedNames("command.", plant.InputName),
		prefixedNames("measurement.", plant.OutputName)...,
	)
	outputs := append(
		prefixedNames("estimate-output.", plant.OutputName),
		prefixedNames("estimate-state.", plant.StateName)...,
	)
	_ = estimator.SetInputName(inputs...)
	_ = estimator.SetOutputName(outputs...)
	_ = estimator.SetStateName(prefixedNames("estimate-state.", plant.StateName)...)
}

func prefixedNames(prefix string, names []string) []string {
	result := make([]string, len(names))
	for i, name := range names {
		result[i] = prefix + name
	}
	return result
}

func indexedNames(prefix string, count int) []string {
	result := make([]string, count)
	for i := range count {
		result[i] = fmt.Sprintf("%s%d", prefix, i+1)
	}
	return result
}

func validateStateNames(names []string, states int) error {
	if len(names) != states {
		return invalid("selected plant has %d states but %d state names", states, len(names))
	}
	seen := make(map[string]struct{}, states)
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return invalid("selected plant state names must be non-empty")
		}
		if _, duplicate := seen[name]; duplicate {
			return invalid("selected plant state name %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func denseMatrix(value *MatrixValue) *mat.Dense {
	rows, columns := value.Dims()
	return mat.NewDense(rows, columns, value.Values())
}

func matrixValuePointer(matrix *mat.Dense) (*MatrixValue, error) {
	if matrix == nil {
		return nil, nil
	}
	rows, columns := matrix.Dims()
	value, err := NewMatrixValue(rows, columns, append([]float64(nil), matrix.RawMatrix().Data...))
	if err != nil {
		return nil, fmt.Errorf("synthesized matrix: %w", err)
	}
	return &value, nil
}

func denseRank(matrix *mat.Dense) int {
	var svd mat.SVD
	if !svd.Factorize(matrix, mat.SVDThin) {
		return 0
	}
	values := svd.Values(nil)
	if len(values) == 0 {
		return 0
	}
	tolerance := float64(max(matrix.RawMatrix().Rows, matrix.RawMatrix().Cols)) *
		values[0] * 1e-12
	rank := 0
	for _, value := range values {
		if value > tolerance {
			rank++
		}
	}
	return rank
}

func denseEigenvalues(matrix *mat.Dense) ([]complex128, error) {
	var eigen mat.Eigen
	if !eigen.Factorize(matrix, mat.EigenNone) {
		return nil, fmt.Errorf("eigenvalue factorization failed")
	}
	return eigen.Values(nil), nil
}

func matrixIsZero(matrix *mat.Dense, tolerance float64) bool {
	rows, columns := matrix.Dims()
	for row := range rows {
		for column := range columns {
			if math.Abs(matrix.At(row, column)) > tolerance {
				return false
			}
		}
	}
	return true
}
