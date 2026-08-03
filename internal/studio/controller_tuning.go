package studio

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jamestjsp/controlsys"
)

type TuningAlgorithm string

const (
	TuningGrid     TuningAlgorithm = "grid"
	TuningSystune  TuningAlgorithm = "systune"
	TuningLooptune TuningAlgorithm = "looptune"
)

type TunableField string

const (
	TunableGain              TunableField = "gain"
	TunableProportional      TunableField = "proportional"
	TunableIntegral          TunableField = "integral"
	TunableDerivative        TunableField = "derivative"
	TunableFilterCoefficient TunableField = "filter_coefficient"
	TunableFilterTime        TunableField = "filter_time"
	TunableSetpointWeight    TunableField = "setpoint_weight"
	TunableDerivativeWeight  TunableField = "derivative_weight"
	TunableMatrixGain        TunableField = "matrix_gain"
	TunableNumerator         TunableField = "numerator"
	TunableTransferNum       TunableField = "transfer_numerator"
	TunableStateA            TunableField = "state_a"
	TunableStateB            TunableField = "state_b"
	TunableStateC            TunableField = "state_c"
	TunableStateD            TunableField = "state_d"
)

type TunableParameterRef struct {
	BlockID     int64        `json:"blockId"`
	Field       TunableField `json:"field"`
	Row         int          `json:"row,omitempty"`
	Column      int          `json:"column,omitempty"`
	Coefficient int          `json:"coefficient,omitempty"`
}

type TunableParameterSpec struct {
	Ref   TunableParameterRef `json:"ref"`
	Lower float64             `json:"lower"`
	Upper float64             `json:"upper"`
}

type TuningGoalKind string

const (
	TuningGoalTracking     TuningGoalKind = "tracking"
	TuningGoalRejection    TuningGoalKind = "rejection"
	TuningGoalSensitivity  TuningGoalKind = "sensitivity"
	TuningGoalWeightedGain TuningGoalKind = "weighted_gain"
	TuningGoalLoopShape    TuningGoalKind = "loop_shape"
	TuningGoalMargin       TuningGoalKind = "margin"
	TuningGoalPole         TuningGoalKind = "pole"
	TuningGoalOvershoot    TuningGoalKind = "overshoot"
)

type TuningGoalRequest struct {
	Name          string         `json:"name"`
	Kind          TuningGoalKind `json:"kind"`
	Maximum       float64        `json:"maximum"`
	Minimum       float64        `json:"minimum,omitempty"`
	AnalysisPoint string         `json:"analysisPoint,omitempty"`
	Omega         []float64      `json:"omega,omitempty"`
	InputWeight   *controlsys.System
	OutputWeight  *controlsys.System
}

type ControllerTuningRequest struct {
	Algorithm      TuningAlgorithm        `json:"algorithm"`
	Parameters     []TunableParameterSpec `json:"parameters"`
	Goals          []TuningGoalRequest    `json:"goals"`
	GridPoints     int                    `json:"gridPoints"`
	MaxEvaluations int                    `json:"maxEvaluations"`
	BaseStep       float64                `json:"baseStep,omitempty"`
}

type TunedValue struct {
	Ref      TunableParameterRef `json:"ref"`
	Previous float64             `json:"previous"`
	Value    float64             `json:"value"`
	Lower    float64             `json:"lower"`
	Upper    float64             `json:"upper"`
}

type TuningGoalEvidence struct {
	Name        string             `json:"name"`
	Kind        TuningGoalKind     `json:"kind"`
	Pass        bool               `json:"pass"`
	Value       float64            `json:"value"`
	Limit       float64            `json:"limit"`
	Violation   float64            `json:"violation"`
	Diagnostics map[string]float64 `json:"diagnostics,omitempty"`
	Message     string             `json:"message,omitempty"`
}

type ControllerTuningCandidate struct {
	FlowID              int64                `json:"flowId"`
	SourceModelRevision time.Time            `json:"sourceModelRevision"`
	SourceControlRoles  ControlRoleSnapshot  `json:"sourceControlRoles"`
	Algorithm           TuningAlgorithm      `json:"algorithm"`
	SearchMethod        string               `json:"searchMethod"`
	Pass                bool                 `json:"pass"`
	Score               float64              `json:"score"`
	Iterations          int                  `json:"iterations"`
	Values              []TunedValue         `json:"values"`
	Goals               []TuningGoalEvidence `json:"goals"`
	Warnings            []string             `json:"warnings,omitempty"`
	Controller          *controlsys.System   `json:"-"`
	ClosedLoop          *controlsys.System   `json:"-"`
	edit                *candidateBlockEdit
}

type tunedParameterChange struct {
	ref   TunableParameterRef
	value float64
}

func (s *Studio) TuneController(
	ctx context.Context,
	flowID int64,
	request ControllerTuningRequest,
) (ControllerTuningCandidate, error) {
	if err := validateControllerTuningRequest(request); err != nil {
		return ControllerTuningCandidate{}, err
	}
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	resolved, err := resolveControlRoleSpec(
		snapshot.Blocks, snapshot.Connections, spec,
	)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	if len(resolved.Controller.Blocks) != 1 {
		return ControllerTuningCandidate{}, invalid(
			"tuning requires one explicit controller block; role has %d blocks",
			len(resolved.Controller.Blocks),
		)
	}
	models, err := buildControlModels(
		snapshot,
		resolved,
		ControlModelBuildRequest{BaseStep: request.BaseStep},
	)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	if err := validateTuningGoalApplicability(models.Plant, request.Goals); err != nil {
		return ControllerTuningCandidate{}, err
	}
	controllerBlock := blockWithID(snapshot.Blocks, resolved.Controller.Blocks[0])
	tunable, authorities, err := tunableControllerBlock(
		controllerBlock, request.Parameters, models.Controller.Dt,
	)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	loop, err := generalizedTuningLoop(models.Plant, tunable, resolved.AnalysisPoints)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	goals, goalKinds, err := tuningGoals(request.Goals, resolved.AnalysisPoints)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	options := &controlsys.SystuneOptions{
		GridPoints: request.GridPoints, MaxEvaluations: request.MaxEvaluations,
	}
	var tuned *controlsys.SystuneResult
	switch request.Algorithm {
	case TuningGrid:
		tuned, err = controlsys.GridTune(loop, goals, options)
	case TuningSystune:
		tuned, err = controlsys.Systune(loop, goals, options)
	case TuningLooptune:
		tuned, err = controlsys.Looptune(loop, goals, options)
	}
	if err != nil {
		return ControllerTuningCandidate{}, fmt.Errorf(
			"%s controller tuning: %w", request.Algorithm, err,
		)
	}
	if err := tuned.Controller.SetInputName(models.Controller.InputName...); err != nil {
		return ControllerTuningCandidate{}, err
	}
	if err := tuned.Controller.SetOutputName(models.Controller.OutputName...); err != nil {
		return ControllerTuningCandidate{}, err
	}
	candidate := ControllerTuningCandidate{
		FlowID:              flowID,
		SourceModelRevision: snapshot.Flow.ModelUpdatedAt,
		SourceControlRoles:  newControlRoleSnapshot(spec),
		Algorithm:           request.Algorithm,
		SearchMethod:        tuned.Method,
		Pass:                tuned.Pass,
		Score:               tuned.Score,
		Iterations:          tuned.Iterations,
		Controller:          tuned.Controller.Copy(),
		ClosedLoop:          tuned.ClosedLoop.Copy(),
	}
	if request.Algorithm != TuningGrid {
		candidate.Warnings = append(candidate.Warnings,
			fmt.Sprintf(
				"%s currently uses controlsys bounded Cartesian-grid search; it is not a continuous optimizer",
				request.Algorithm,
			),
		)
	}
	for _, result := range tuned.Goals {
		kind := goalKinds[result.GoalName]
		evidence := TuningGoalEvidence{
			Name: result.GoalName, Kind: kind,
			Pass: result.Pass, Value: result.Value, Limit: result.Limit,
			Violation:   result.Violation,
			Diagnostics: cloneStringFloatMap(result.Diagnostics),
		}
		if !result.Pass {
			evidence.Message = fmt.Sprintf(
				"goal missed by normalized violation %.4g", result.Violation,
			)
		}
		candidate.Goals = append(candidate.Goals, evidence)
	}
	keys := make([]string, 0, len(tuned.Parameters))
	for key := range tuned.Parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	changes := make([]tunedParameterChange, 0, len(keys))
	for _, key := range keys {
		authority, ok := authorities[key]
		if !ok {
			return ControllerTuningCandidate{}, fmt.Errorf(
				"controlsys returned unknown tunable parameter %q", key,
			)
		}
		value := tuned.Parameters[key]
		candidate.Values = append(candidate.Values, TunedValue{
			Ref: authority.ref, Previous: authority.current, Value: value,
			Lower: authority.lower, Upper: authority.upper,
		})
		changes = append(changes, tunedParameterChange{
			ref: authority.ref, value: value,
		})
	}
	candidate.edit, err = editBlockWithTunedChanges(controllerBlock, changes)
	if err != nil {
		return ControllerTuningCandidate{}, err
	}
	return candidate, nil
}

func validateTuningGoalApplicability(
	plant *controlsys.System,
	goals []TuningGoalRequest,
) error {
	_, inputs, outputs := plant.Dims()
	for _, goal := range goals {
		if goal.Kind == TuningGoalPole && plant.IsDiscrete() {
			return invalid(
				"pole tuning goal %q uses continuous real-part semantics and is unavailable for a discrete plant",
				goal.Name,
			)
		}
		if goal.Kind == TuningGoalMargin && (inputs != 1 || outputs != 1) {
			return invalid(
				"margin tuning goal %q requires SISO plant/controller roles; selected plant is %d×%d",
				goal.Name, outputs, inputs,
			)
		}
	}
	return nil
}

func validateControllerTuningRequest(request ControllerTuningRequest) error {
	switch request.Algorithm {
	case TuningGrid, TuningSystune, TuningLooptune:
	default:
		return invalid("unknown tuning algorithm %q", request.Algorithm)
	}
	if len(request.Parameters) == 0 {
		return invalid("select at least one tunable controller parameter")
	}
	if len(request.Parameters) > 8 {
		return invalid("tune at most 8 parameters at once")
	}
	if len(request.Goals) == 0 {
		return invalid("select at least one tuning goal")
	}
	if request.GridPoints < 2 || request.GridPoints > 50 {
		return invalid("tuning grid points must be between 2 and 50")
	}
	if request.MaxEvaluations <= 0 || request.MaxEvaluations > 100_000 {
		return invalid("maximum tuning evaluations must be between 1 and 100,000")
	}
	if request.BaseStep < 0 || math.IsNaN(request.BaseStep) ||
		math.IsInf(request.BaseStep, 0) {
		return invalid("tuning base step must be a non-negative finite value")
	}
	seen := make(map[TunableParameterRef]struct{}, len(request.Parameters))
	evaluations := 1
	for i, parameter := range request.Parameters {
		if parameter.Ref.BlockID <= 0 {
			return invalid("tunable parameter %d must name a block", i+1)
		}
		if math.IsNaN(parameter.Lower) || math.IsInf(parameter.Lower, 0) ||
			math.IsNaN(parameter.Upper) || math.IsInf(parameter.Upper, 0) ||
			parameter.Lower >= parameter.Upper {
			return invalid(
				"tunable parameter %d requires finite lower < upper bounds", i+1,
			)
		}
		if _, duplicate := seen[parameter.Ref]; duplicate {
			return invalid("tunable parameter %s is selected more than once", parameterKey(parameter.Ref))
		}
		seen[parameter.Ref] = struct{}{}
		if evaluations > request.MaxEvaluations/request.GridPoints {
			return invalid(
				"tuning grid exceeds the %d evaluation limit", request.MaxEvaluations,
			)
		}
		evaluations *= request.GridPoints
	}
	return nil
}

type tunableAuthority struct {
	ref                   TunableParameterRef
	current, lower, upper float64
}

func tunableControllerBlock(
	block Block,
	specs []TunableParameterSpec,
	effectiveSampleTime float64,
) (controlsys.TunableBlock, map[string]tunableAuthority, error) {
	requested := make(map[TunableParameterRef]TunableParameterSpec, len(specs))
	for _, spec := range specs {
		if spec.Ref.BlockID != block.ID {
			return nil, nil, invalid(
				"tunable parameter %s is outside controller block %s",
				parameterKey(spec.Ref), block.Name,
			)
		}
		requested[spec.Ref] = spec
	}
	authorities := make(map[string]tunableAuthority, len(specs))
	makeReal := func(ref TunableParameterRef, current float64) (*controlsys.TunableReal, error) {
		key := parameterKey(ref)
		spec, selected := requested[ref]
		bounds := controlsys.TunableBounds{}
		if selected {
			if current < spec.Lower || current > spec.Upper {
				return nil, invalid(
					"current value %.12g for %s is outside [%.12g, %.12g]",
					current, key, spec.Lower, spec.Upper,
				)
			}
			bounds = controlsys.TunableBounds{Lower: spec.Lower, Upper: spec.Upper}
			authorities[key] = tunableAuthority{
				ref: ref, current: current, lower: spec.Lower, upper: spec.Upper,
			}
		}
		parameter, err := controlsys.NewTunableReal(key, current, bounds)
		if err != nil {
			return nil, err
		}
		parameter.SetFixed(!selected)
		return parameter, nil
	}

	var tunable controlsys.TunableBlock
	switch block.Kind {
	case BlockGain:
		ref := TunableParameterRef{BlockID: block.ID, Field: TunableGain}
		value, err := makeReal(ref, block.Parameters.Gain)
		if err != nil {
			return nil, nil, err
		}
		tunable = controlsys.NewTunableGain(
			fmt.Sprintf("block-%d-gain", block.ID),
			[][]*controlsys.TunableReal{{value}},
			effectiveSampleTime,
		)
	case BlockMatrixGain:
		if block.Parameters.D == nil {
			return nil, nil, invalid("%s has no gain matrix", block.Name)
		}
		rows, columns := block.Parameters.D.Dims()
		matrix := make([][]*controlsys.TunableReal, rows)
		for row := range rows {
			matrix[row] = make([]*controlsys.TunableReal, columns)
			for column := range columns {
				ref := TunableParameterRef{
					BlockID: block.ID, Field: TunableMatrixGain,
					Row: row, Column: column,
				}
				value, err := makeReal(ref, block.Parameters.D.At(row, column))
				if err != nil {
					return nil, nil, err
				}
				matrix[row][column] = value
			}
		}
		tunable = controlsys.NewTunableGain(
			fmt.Sprintf("block-%d-matrix-gain", block.ID), matrix, effectiveSampleTime,
		)
	case BlockPID:
		kp, err := makeReal(
			TunableParameterRef{BlockID: block.ID, Field: TunableProportional},
			block.Parameters.Proportional,
		)
		if err != nil {
			return nil, nil, err
		}
		ki, err := makeReal(
			TunableParameterRef{BlockID: block.ID, Field: TunableIntegral},
			block.Parameters.Integral,
		)
		if err != nil {
			return nil, nil, err
		}
		kd, err := makeReal(
			TunableParameterRef{BlockID: block.ID, Field: TunableDerivative},
			block.Parameters.Derivative,
		)
		if err != nil {
			return nil, nil, err
		}
		tunable = controlsys.NewTunablePID(
			fmt.Sprintf("block-%d-pid", block.ID),
			kp, ki, kd, pidFilterTime(block.Parameters), effectiveSampleTime,
		)
	case BlockTransfer, BlockDiscreteTransfer:
		numerator := make([]*controlsys.TunableReal, len(block.Parameters.Numerator))
		for coefficient, current := range block.Parameters.Numerator {
			ref := TunableParameterRef{
				BlockID: block.ID, Field: TunableNumerator, Coefficient: coefficient,
			}
			value, err := makeReal(ref, current)
			if err != nil {
				return nil, nil, err
			}
			numerator[coefficient] = value
		}
		tunable = controlsys.NewTunableTF(
			fmt.Sprintf("block-%d-transfer", block.ID),
			[][][]*controlsys.TunableReal{{numerator}},
			[][]float64{append([]float64(nil), block.Parameters.Denominator...)},
			effectiveSampleTime,
		)
	case BlockMIMOTransfer:
		if block.Parameters.TransferNumerators == nil ||
			block.Parameters.TransferDenominators == nil {
			return nil, nil, invalid("%s has no transfer matrix", block.Name)
		}
		numerators := block.Parameters.TransferNumerators.Values()
		tunableNumerators := make([][][]*controlsys.TunableReal, len(numerators))
		for row := range numerators {
			tunableNumerators[row] = make([][]*controlsys.TunableReal, len(numerators[row]))
			for column := range numerators[row] {
				tunableNumerators[row][column] = make(
					[]*controlsys.TunableReal, len(numerators[row][column]),
				)
				for coefficient, current := range numerators[row][column] {
					ref := TunableParameterRef{
						BlockID: block.ID, Field: TunableTransferNum,
						Row: row, Column: column, Coefficient: coefficient,
					}
					value, err := makeReal(ref, current)
					if err != nil {
						return nil, nil, err
					}
					tunableNumerators[row][column][coefficient] = value
				}
			}
		}
		storedDenominators := block.Parameters.TransferDenominators.Values()
		denominators := make([][]float64, len(storedDenominators))
		for row := range storedDenominators {
			if len(storedDenominators[row]) != 1 {
				return nil, nil, invalid(
					"%s denominator matrix must have one column", block.Name,
				)
			}
			denominators[row] = append([]float64(nil), storedDenominators[row][0]...)
		}
		tunable = controlsys.NewTunableTF(
			fmt.Sprintf("block-%d-mimo-transfer", block.ID),
			tunableNumerators, denominators, effectiveSampleTime,
		)
	case BlockStateSpace, BlockDiscreteStateSpace:
		if block.Parameters.A == nil || block.Parameters.B == nil ||
			block.Parameters.C == nil || block.Parameters.D == nil {
			return nil, nil, invalid("%s has incomplete state matrices", block.Name)
		}
		makeMatrix := func(
			field TunableField,
			matrix *MatrixValue,
		) ([][]*controlsys.TunableReal, error) {
			rows, columns := matrix.Dims()
			result := make([][]*controlsys.TunableReal, rows)
			for row := range rows {
				result[row] = make([]*controlsys.TunableReal, columns)
				for column := range columns {
					ref := TunableParameterRef{
						BlockID: block.ID, Field: field, Row: row, Column: column,
					}
					value, err := makeReal(ref, matrix.At(row, column))
					if err != nil {
						return nil, err
					}
					result[row][column] = value
				}
			}
			return result, nil
		}
		a, err := makeMatrix(TunableStateA, block.Parameters.A)
		if err != nil {
			return nil, nil, err
		}
		b, err := makeMatrix(TunableStateB, block.Parameters.B)
		if err != nil {
			return nil, nil, err
		}
		c, err := makeMatrix(TunableStateC, block.Parameters.C)
		if err != nil {
			return nil, nil, err
		}
		d, err := makeMatrix(TunableStateD, block.Parameters.D)
		if err != nil {
			return nil, nil, err
		}
		tunable = controlsys.NewTunableSS(
			fmt.Sprintf("block-%d-state-space", block.ID),
			a, b, c, d, effectiveSampleTime,
		)
	default:
		return nil, nil, invalid(
			"%s (%s) is not a supported tunable Gain, PID, transfer-function, or state-space block",
			block.Name, block.Kind.Label(),
		)
	}
	if len(authorities) != len(specs) {
		return nil, nil, invalid(
			"one or more selected parameters do not belong to %s", block.Name,
		)
	}
	return tunable, authorities, nil
}

func generalizedTuningLoop(
	plant *controlsys.System,
	controller controlsys.TunableBlock,
	points []AnalysisPointRole,
) (*controlsys.GeneralizedClosedLoop, error) {
	primary := points[0]
	loop, err := controlsys.NewGeneralizedClosedLoop(
		"tunable-flowsheet-loop", plant, controller, primary.Name,
	)
	if err != nil {
		return nil, err
	}
	if primary.Location == AnalysisPointPlantInput {
		if err := loop.InsertAnalysisPoint(
			primary.Name, controlsys.AnalysisPointPlantInput,
		); err != nil {
			return nil, err
		}
	}
	for _, point := range points[1:] {
		location := controlsys.AnalysisPointPlantOutput
		if point.Location == AnalysisPointPlantInput {
			location = controlsys.AnalysisPointPlantInput
		}
		if err := loop.InsertAnalysisPoint(point.Name, location); err != nil {
			return nil, err
		}
	}
	return loop, nil
}

func tuningGoals(
	requests []TuningGoalRequest,
	points []AnalysisPointRole,
) ([]controlsys.TuningGoal, map[string]TuningGoalKind, error) {
	pointNames := make(map[string]struct{}, len(points))
	for _, point := range points {
		pointNames[point.Name] = struct{}{}
	}
	goals := make([]controlsys.TuningGoal, len(requests))
	kinds := make(map[string]TuningGoalKind, len(requests))
	for i, request := range requests {
		request.Name = strings.TrimSpace(request.Name)
		if request.Name == "" {
			return nil, nil, invalid("tuning goal %d must have a name", i+1)
		}
		if _, duplicate := kinds[request.Name]; duplicate {
			return nil, nil, invalid("tuning goal %q is named more than once", request.Name)
		}
		if request.AnalysisPoint != "" {
			if _, ok := pointNames[request.AnalysisPoint]; !ok {
				return nil, nil, invalid(
					"tuning goal %q references unknown analysis point %q",
					request.Name, request.AnalysisPoint,
				)
			}
		}
		goalType, err := controlsysTuningGoalType(request.Kind)
		if err != nil {
			return nil, nil, err
		}
		goal, err := controlsys.NewTuningGoal(controlsys.TuningGoalSpec{
			Name: request.Name, Type: goalType,
			Max: request.Maximum, Min: request.Minimum,
			AnalysisPoint: request.AnalysisPoint,
			Omega:         append([]float64(nil), request.Omega...),
			InputWeight:   request.InputWeight, OutputWeight: request.OutputWeight,
		})
		if err != nil {
			return nil, nil, invalid("tuning goal %q: %s", request.Name, err)
		}
		goals[i] = goal
		kinds[request.Name] = request.Kind
	}
	return goals, kinds, nil
}

func controlsysTuningGoalType(kind TuningGoalKind) (controlsys.TuningGoalType, error) {
	switch kind {
	case TuningGoalTracking:
		return controlsys.TuningGoalTracking, nil
	case TuningGoalRejection:
		return controlsys.TuningGoalRejection, nil
	case TuningGoalSensitivity:
		return controlsys.TuningGoalSensitivity, nil
	case TuningGoalWeightedGain:
		return controlsys.TuningGoalWeightedGain, nil
	case TuningGoalLoopShape:
		return controlsys.TuningGoalLoopShape, nil
	case TuningGoalMargin:
		return controlsys.TuningGoalMargin, nil
	case TuningGoalPole:
		return controlsys.TuningGoalPole, nil
	case TuningGoalOvershoot:
		return controlsys.TuningGoalOvershoot, nil
	default:
		return 0, invalid("unknown tuning goal kind %q", kind)
	}
}

func (s *Studio) ApplyTuningCandidate(
	ctx context.Context,
	candidate ControllerTuningCandidate,
) (ControllerCandidateApplication, error) {
	if candidate.FlowID <= 0 || candidate.SourceModelRevision.IsZero() ||
		candidate.edit == nil {
		return ControllerCandidateApplication{}, invalid(
			"controller candidate is incomplete; tune it again",
		)
	}
	return s.applyCandidateBlockEditWithUndo(ctx, candidateApplyRequest{
		flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
		controlRoles: candidate.SourceControlRoles, edit: candidate.edit,
		event: fmt.Sprintf("Applied %s controller candidate", candidate.Algorithm),
	})
}

func setTunedParameter(
	parameters *Parameters,
	ref TunableParameterRef,
	value float64,
) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return invalid("tuned parameter %s is not finite", parameterKey(ref))
	}
	switch ref.Field {
	case TunableGain:
		parameters.Gain = value
	case TunableProportional:
		parameters.Proportional = value
	case TunableIntegral:
		parameters.Integral = value
	case TunableDerivative:
		parameters.Derivative = value
	case TunableFilterCoefficient:
		parameters.FilterCoefficient = value
		parameters.FilterTime = 0
	case TunableFilterTime:
		if value <= 0 {
			return invalid("tuned filter time must be positive")
		}
		parameters.FilterCoefficient = 1 / value
		parameters.FilterTime = 0
	case TunableSetpointWeight:
		parameters.SetpointWeight = value
	case TunableDerivativeWeight:
		parameters.DerivativeWeight = value
	case TunableMatrixGain:
		if parameters.D == nil {
			return invalid("tuned matrix gain is missing")
		}
		rows, columns := parameters.D.Dims()
		if ref.Row < 0 || ref.Row >= rows || ref.Column < 0 || ref.Column >= columns {
			return invalid("tuned matrix index %d,%d is out of range", ref.Row, ref.Column)
		}
		values := parameters.D.Values()
		values[ref.Row*columns+ref.Column] = value
		matrix, err := NewMatrixValue(rows, columns, values)
		if err != nil {
			return err
		}
		parameters.D = &matrix
	case TunableNumerator:
		if ref.Coefficient < 0 || ref.Coefficient >= len(parameters.Numerator) {
			return invalid(
				"tuned numerator coefficient %d is out of range", ref.Coefficient,
			)
		}
		parameters.Numerator = append([]float64(nil), parameters.Numerator...)
		parameters.Numerator[ref.Coefficient] = value
	case TunableTransferNum:
		if parameters.TransferNumerators == nil {
			return invalid("tuned transfer numerator matrix is missing")
		}
		values := parameters.TransferNumerators.Values()
		if ref.Row < 0 || ref.Row >= len(values) ||
			ref.Column < 0 || ref.Column >= len(values[ref.Row]) ||
			ref.Coefficient < 0 ||
			ref.Coefficient >= len(values[ref.Row][ref.Column]) {
			return invalid(
				"tuned transfer numerator index %d,%d,%d is out of range",
				ref.Row, ref.Column, ref.Coefficient,
			)
		}
		values[ref.Row][ref.Column][ref.Coefficient] = value
		matrix, err := NewPolynomialMatrixValue(values)
		if err != nil {
			return err
		}
		parameters.TransferNumerators = &matrix
	case TunableStateA, TunableStateB, TunableStateC, TunableStateD:
		var matrix **MatrixValue
		switch ref.Field {
		case TunableStateA:
			matrix = &parameters.A
		case TunableStateB:
			matrix = &parameters.B
		case TunableStateC:
			matrix = &parameters.C
		case TunableStateD:
			matrix = &parameters.D
		}
		if *matrix == nil {
			return invalid("tuned state-space matrix %s is missing", ref.Field)
		}
		rows, columns := (*matrix).Dims()
		if ref.Row < 0 || ref.Row >= rows || ref.Column < 0 || ref.Column >= columns {
			return invalid(
				"tuned state-space matrix index %d,%d is out of range",
				ref.Row, ref.Column,
			)
		}
		values := (*matrix).Values()
		values[ref.Row*columns+ref.Column] = value
		updated, err := NewMatrixValue(rows, columns, values)
		if err != nil {
			return err
		}
		*matrix = &updated
	default:
		return invalid("unknown tuned parameter field %q", ref.Field)
	}
	return nil
}

func parameterKey(ref TunableParameterRef) string {
	switch ref.Field {
	case TunableMatrixGain, TunableStateA, TunableStateB, TunableStateC, TunableStateD:
		return fmt.Sprintf(
			"block.%d.%s.%d.%d", ref.BlockID, ref.Field, ref.Row, ref.Column,
		)
	case TunableNumerator:
		return fmt.Sprintf(
			"block.%d.%s.%d", ref.BlockID, ref.Field, ref.Coefficient,
		)
	case TunableTransferNum:
		return fmt.Sprintf(
			"block.%d.%s.%d.%d.%d",
			ref.BlockID, ref.Field, ref.Row, ref.Column, ref.Coefficient,
		)
	default:
		return fmt.Sprintf("block.%d.%s", ref.BlockID, ref.Field)
	}
}

func blockWithID(blocks []Block, blockID int64) Block {
	for _, block := range blocks {
		if block.ID == blockID {
			return block
		}
	}
	return Block{}
}

func cloneStringFloatMap(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	cloned := make(map[string]float64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
