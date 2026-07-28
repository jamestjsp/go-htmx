package studio

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jamestjsp/controlsys"
	"gonum.org/v1/gonum/mat"
)

type PIDDesignRequest struct {
	Type               controlsys.PidtuneType `json:"type"`
	CrossoverFrequency float64                `json:"crossoverFrequency,omitempty"`
	PhaseMargin        float64                `json:"phaseMargin,omitempty"`
	SetpointWeight     *float64               `json:"setpointWeight,omitempty"`
	DerivativeWeight   *float64               `json:"derivativeWeight,omitempty"`
	Omega              []float64              `json:"omega,omitempty"`
	StepHorizon        float64                `json:"stepHorizon,omitempty"`
	BaseStep           float64                `json:"baseStep,omitempty"`
}

type PIDDesignGains struct {
	Proportional     float64 `json:"proportional"`
	Integral         float64 `json:"integral"`
	Derivative       float64 `json:"derivative"`
	FilterTime       float64 `json:"filterTime"`
	SetpointWeight   float64 `json:"setpointWeight,omitempty"`
	DerivativeWeight float64 `json:"derivativeWeight,omitempty"`
	SampleTime       float64 `json:"sampleTime,omitempty"`
}

type PIDFrequencyEvidence struct {
	Omega                 []float64  `json:"omega"`
	CurrentMagnitudeDB    []*float64 `json:"currentMagnitudeDb"`
	CurrentPhaseDegrees   []*float64 `json:"currentPhaseDegrees"`
	CandidateMagnitudeDB  []*float64 `json:"candidateMagnitudeDb"`
	CandidatePhaseDegrees []*float64 `json:"candidatePhaseDegrees"`
}

type PIDStepEvidence struct {
	Times           []float64 `json:"times"`
	CurrentValues   []float64 `json:"currentValues"`
	CandidateValues []float64 `json:"candidateValues"`
}

type PIDDesignCandidate struct {
	FlowID              int64                    `json:"flowId"`
	SourceModelRevision time.Time                `json:"sourceModelRevision"`
	SourceControlRoles  ControlRoleSnapshot      `json:"sourceControlRoles"`
	ControllerBlockID   int64                    `json:"controllerBlockId"`
	Type                controlsys.PidtuneType   `json:"type"`
	TargetCrossover     float64                  `json:"targetCrossover,omitempty"`
	TargetPhaseMargin   float64                  `json:"targetPhaseMargin"`
	TwoDegreeOfFreedom  bool                     `json:"twoDegreeOfFreedom"`
	Goals               []ControllerDesignGoal   `json:"goals"`
	Gains               PIDDesignGains           `json:"gains"`
	CurrentMargin       *ClassicalMarginAnalysis `json:"currentMargin,omitempty"`
	CandidateMargin     *ClassicalMarginAnalysis `json:"candidateMargin,omitempty"`
	Frequency           PIDFrequencyEvidence     `json:"frequency"`
	Step                *PIDStepEvidence         `json:"step,omitempty"`
	Warnings            []string                 `json:"warnings,omitempty"`
	Controller          *controlsys.System       `json:"-"`
	ReferenceController *controlsys.System       `json:"-"`
	ClosedLoop          *controlsys.System       `json:"-"`
	edit                *candidateBlockEdit
}

func (s *Studio) DesignPIDController(
	ctx context.Context,
	flowID int64,
	request PIDDesignRequest,
) (PIDDesignCandidate, error) {
	request.Type = controlsys.PidtuneType(strings.ToUpper(strings.TrimSpace(string(request.Type))))
	if err := validatePIDDesignRequest(request); err != nil {
		return PIDDesignCandidate{}, err
	}
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	resolved, err := resolveControlRoleSpec(snapshot.Blocks, snapshot.Connections, spec)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	if len(resolved.Controller.Blocks) != 1 {
		return PIDDesignCandidate{}, invalid(
			"PID design requires one explicit controller block; role has %d blocks",
			len(resolved.Controller.Blocks),
		)
	}
	block := blockWithID(snapshot.Blocks, resolved.Controller.Blocks[0])
	if block.Kind != BlockPID && block.Kind != BlockPID2 {
		return PIDDesignCandidate{}, invalid(
			"PID design requires a PID or PID2 controller block; selected %s",
			block.Kind.Label(),
		)
	}
	if block.Kind == BlockPID2 && len(resolved.Controller.ReferenceInputs) == 0 {
		return PIDDesignCandidate{}, invalid(
			"PID2 design requires an explicit named controller reference input",
		)
	}
	if block.Kind == BlockPID && len(resolved.Controller.ReferenceInputs) != 0 {
		return PIDDesignCandidate{}, invalid(
			"one-degree-of-freedom PID design cannot use controller reference inputs",
		)
	}
	if block.Kind == BlockPID &&
		(request.SetpointWeight != nil || request.DerivativeWeight != nil) {
		return PIDDesignCandidate{}, invalid(
			"setpoint and derivative weights require a PID2 controller block",
		)
	}
	models, err := buildControlModels(
		snapshot,
		resolved,
		ControlModelBuildRequest{BaseStep: request.BaseStep},
	)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	tuned, err := controlsys.Pidtune(
		models.Plant,
		request.Type,
		controlsys.PidtuneOptions{
			CrossoverFrequency: request.CrossoverFrequency,
			PhaseMargin:        request.PhaseMargin,
		},
	)
	if err != nil {
		return PIDDesignCandidate{}, invalid("controlsys pidtune: %s", err)
	}
	designed := tuned.Copy()
	if designed.Kd == 0 && designed.Tf == 0 {
		designed.Tf = block.Parameters.FilterTime
	}
	controller, err := designed.System()
	if err != nil {
		return PIDDesignCandidate{}, invalid("realize PID candidate: %s", err)
	}
	if err := controller.SetInputName(models.Controller.InputName...); err != nil {
		return PIDDesignCandidate{}, err
	}
	if err := controller.SetOutputName(models.Controller.OutputName...); err != nil {
		return PIDDesignCandidate{}, err
	}

	b := block.Parameters.SetpointWeight
	c := block.Parameters.DerivativeWeight
	referenceController := controller.Copy()
	if block.Kind == BlockPID2 {
		if request.SetpointWeight != nil {
			b = *request.SetpointWeight
		}
		if request.DerivativeWeight != nil {
			c = *request.DerivativeWeight
		}
		referenceController, err = controlsys.NewPID2(
			designed.Kp, designed.Ki, designed.Kd, designed.Tf, b, c,
			controlsys.WithTs(designed.Dt),
		).System()
		if err != nil {
			return PIDDesignCandidate{}, invalid("realize PID2 candidate: %s", err)
		}
		if err := referenceController.SetInputName(models.ReferenceController.InputName...); err != nil {
			return PIDDesignCandidate{}, err
		}
		if err := referenceController.SetOutputName(models.ReferenceController.OutputName...); err != nil {
			return PIDDesignCandidate{}, err
		}
	}
	candidateOpen, err := controlsys.Series(models.Plant, controller)
	if err != nil {
		return PIDDesignCandidate{}, fmt.Errorf("assemble candidate open loop: %w", err)
	}
	candidateClosed, err := controlsys.Feedback(candidateOpen, nil, -1)
	if err != nil {
		return PIDDesignCandidate{}, fmt.Errorf("assemble candidate closed loop: %w", err)
	}
	currentReferenceClosed := models.Points[0].ClosedLoop
	candidateReferenceClosed := candidateClosed
	if block.Kind == BlockPID2 {
		currentReferenceClosed, err = pid2ReferenceClosedLoop(
			models.Plant, models.ReferenceController,
		)
		if err != nil {
			return PIDDesignCandidate{}, fmt.Errorf("assemble current PID2 reference loop: %w", err)
		}
		candidateReferenceClosed, err = pid2ReferenceClosedLoop(
			models.Plant, referenceController,
		)
		if err != nil {
			return PIDDesignCandidate{}, fmt.Errorf("assemble candidate PID2 reference loop: %w", err)
		}
	}
	omega, err := pidEvidenceGrid(request.Omega, candidateOpen)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	frequency, err := comparePIDFrequency(
		models.Points[0].OpenLoop, candidateOpen, omega,
	)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	targetPM := request.PhaseMargin
	if targetPM == 0 {
		targetPM = 60
	}
	candidate := PIDDesignCandidate{
		FlowID: flowID, SourceModelRevision: snapshot.Flow.ModelUpdatedAt,
		SourceControlRoles: newControlRoleSnapshot(spec),
		ControllerBlockID:  block.ID, Type: request.Type,
		TargetCrossover: request.CrossoverFrequency, TargetPhaseMargin: targetPM,
		TwoDegreeOfFreedom: block.Kind == BlockPID2,
		Goals:              pidControllerDesignGoals(request.CrossoverFrequency, targetPM),
		Gains: PIDDesignGains{
			Proportional: designed.Kp, Integral: designed.Ki, Derivative: designed.Kd,
			FilterTime: designed.Tf, SetpointWeight: b, DerivativeWeight: c,
			SampleTime: designed.Dt,
		},
		Frequency: frequency, Controller: controller.Copy(),
		ReferenceController: referenceController.Copy(),
		ClosedLoop:          candidateReferenceClosed.Copy(),
	}
	if margin, marginErr := controlsys.Margin(models.Points[0].OpenLoop); marginErr == nil {
		candidate.CurrentMargin = pidMarginEvidence(margin)
	} else {
		candidate.Warnings = append(candidate.Warnings, "Current margins unavailable: "+marginErr.Error())
	}
	if margin, marginErr := controlsys.Margin(candidateOpen); marginErr == nil {
		candidate.CandidateMargin = pidMarginEvidence(margin)
	} else {
		candidate.Warnings = append(candidate.Warnings, "Candidate margins unavailable: "+marginErr.Error())
	}
	if models.Plant.HasDelay() {
		candidate.Warnings = append(candidate.Warnings,
			"Pidtune uses the plant's exact delay frequency response; no hidden Padé or Thiran approximation was introduced.",
		)
	}
	step, warnings := comparePIDStep(
		currentReferenceClosed, candidateReferenceClosed, request.StepHorizon,
	)
	candidate.Step = step
	candidate.Warnings = append(candidate.Warnings, warnings...)
	candidate.edit, err = editBlockWithTunedChanges(
		block, pidCandidateChanges(block, designed, b, c),
	)
	if err != nil {
		return PIDDesignCandidate{}, err
	}
	return candidate, nil
}

func pidControllerDesignGoals(
	crossoverFrequency, phaseMargin float64,
) []ControllerDesignGoal {
	goals := []ControllerDesignGoal{{
		Name: "phase margin", Target: fmt.Sprintf("%.6g degrees", phaseMargin),
	}}
	if crossoverFrequency > 0 {
		goals = append(goals, ControllerDesignGoal{
			Name:   "crossover frequency",
			Target: fmt.Sprintf("%.6g rad/s", crossoverFrequency),
		})
	}
	return goals
}

func validatePIDDesignRequest(request PIDDesignRequest) error {
	switch request.Type {
	case controlsys.PidtuneP, controlsys.PidtuneI, controlsys.PidtunePI,
		controlsys.PidtunePD, controlsys.PidtunePID, controlsys.PidtunePIDF:
	default:
		return invalid("unknown PID design type %q", request.Type)
	}
	for label, value := range map[string]float64{
		"crossover frequency": request.CrossoverFrequency,
		"phase margin":        request.PhaseMargin,
		"step horizon":        request.StepHorizon,
		"base step":           request.BaseStep,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return invalid("%s must be a non-negative finite number", label)
		}
	}
	if request.PhaseMargin >= 180 {
		return invalid("phase margin must be less than 180 degrees")
	}
	if request.StepHorizon > 120 {
		return invalid("step horizon must be at most 120 seconds")
	}
	for label, value := range map[string]*float64{
		"setpoint weight":   request.SetpointWeight,
		"derivative weight": request.DerivativeWeight,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < -10 || *value > 10) {
			return invalid("%s must be finite and between -10 and 10", label)
		}
	}
	if len(request.Omega) != 0 {
		if len(request.Omega) < 2 {
			return invalid("PID evidence frequency grid requires at least two points")
		}
		for i, value := range request.Omega {
			if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return invalid("PID evidence frequency %d must be positive and finite", i+1)
			}
			if i > 0 && value <= request.Omega[i-1] {
				return invalid("PID evidence frequency grid must be strictly increasing")
			}
		}
	}
	return nil
}

func pidEvidenceGrid(requested []float64, system *controlsys.System) ([]float64, error) {
	if len(requested) != 0 {
		if err := validateFrequencyGridForSystem(requested, system); err != nil {
			return nil, err
		}
		return append([]float64(nil), requested...), nil
	}
	bode, err := system.Bode(nil, 160)
	if err != nil {
		return nil, fmt.Errorf("build PID evidence frequency grid: %w", err)
	}
	return append([]float64(nil), bode.Omega...), nil
}

func comparePIDFrequency(
	current, candidate *controlsys.System,
	omega []float64,
) (PIDFrequencyEvidence, error) {
	currentBode, err := current.Bode(omega, len(omega))
	if err != nil {
		return PIDFrequencyEvidence{}, fmt.Errorf("current loop frequency evidence: %w", err)
	}
	candidateBode, err := candidate.Bode(omega, len(omega))
	if err != nil {
		return PIDFrequencyEvidence{}, fmt.Errorf("candidate loop frequency evidence: %w", err)
	}
	result := PIDFrequencyEvidence{Omega: append([]float64(nil), omega...)}
	for i := range omega {
		result.CurrentMagnitudeDB = append(result.CurrentMagnitudeDB, finitePointer(currentBode.MagDBAt(i, 0, 0)))
		result.CurrentPhaseDegrees = append(result.CurrentPhaseDegrees, finitePointer(currentBode.PhaseAt(i, 0, 0)))
		result.CandidateMagnitudeDB = append(result.CandidateMagnitudeDB, finitePointer(candidateBode.MagDBAt(i, 0, 0)))
		result.CandidatePhaseDegrees = append(result.CandidatePhaseDegrees, finitePointer(candidateBode.PhaseAt(i, 0, 0)))
	}
	return result, nil
}

func pidMarginEvidence(result *controlsys.MarginResult) *ClassicalMarginAnalysis {
	return &ClassicalMarginAnalysis{
		GainMarginDB:               finitePointer(result.GainMargin),
		PhaseMarginDegrees:         finitePointer(result.PhaseMargin),
		GainCrossoverRadPerSecond:  finitePointer(result.WgFreq),
		PhaseCrossoverRadPerSecond: finitePointer(result.WpFreq),
		NoFiniteGainMargin:         !finite(result.GainMargin),
		NoFinitePhaseMargin:        !finite(result.PhaseMargin),
	}
}

func comparePIDStep(
	current, candidate *controlsys.System,
	horizon float64,
) (*PIDStepEvidence, []string) {
	currentStable, currentErr := current.IsStable()
	candidateStable, candidateErr := candidate.IsStable()
	var warnings []string
	if currentErr != nil || !currentStable {
		warnings = append(warnings, "Current reference-loop step evidence is unavailable because the loop is unstable or its stability could not be determined.")
	}
	if candidateErr != nil || !candidateStable {
		warnings = append(warnings, "Candidate reference-loop step evidence is unavailable because the loop is unstable or its stability could not be determined.")
	}
	if currentErr != nil || candidateErr != nil || !currentStable || !candidateStable {
		return nil, warnings
	}
	if horizon == 0 {
		horizon = 10
		if current.IsDiscrete() {
			horizon = 100 * current.Dt
		}
	}
	var steps int
	var dt float64
	if current.IsDiscrete() {
		dt = current.Dt
		steps = int(math.Floor(horizon/dt)) + 1
		if steps < 2 {
			steps = 2
		}
	} else {
		steps = 501
		dt = horizon / float64(steps-1)
	}
	times := make([]float64, steps)
	input := mat.NewDense(steps, 1, nil)
	for i := range steps {
		times[i] = float64(i) * dt
		input.Set(i, 0, 1)
	}
	currentResponse, err := controlsys.Lsim(current, input, times, nil)
	if err != nil {
		return nil, append(warnings, "Current step response unavailable: "+err.Error())
	}
	candidateResponse, err := controlsys.Lsim(candidate, input, times, nil)
	if err != nil {
		return nil, append(warnings, "Candidate step response unavailable: "+err.Error())
	}
	result := &PIDStepEvidence{Times: append([]float64(nil), currentResponse.T...)}
	_, samples := currentResponse.Y.Dims()
	for i := range samples {
		result.CurrentValues = append(result.CurrentValues, currentResponse.Y.At(0, i))
		result.CandidateValues = append(result.CandidateValues, candidateResponse.Y.At(0, i))
	}
	return result, warnings
}

func pid2ReferenceClosedLoop(
	plant, controller *controlsys.System,
) (*controlsys.System, error) {
	plant = plant.Copy()
	controller = controller.Copy()
	if err := plant.SetInputName("pid-design.plant.u"); err != nil {
		return nil, err
	}
	if err := plant.SetOutputName("pid-design.plant.y"); err != nil {
		return nil, err
	}
	if err := controller.SetInputName(
		"pid-design.controller.r", "pid-design.controller.y",
	); err != nil {
		return nil, err
	}
	if err := controller.SetOutputName("pid-design.controller.u"); err != nil {
		return nil, err
	}
	result, err := controlsys.ConnectByName(
		[]*controlsys.System{plant, controller},
		[]controlsys.Connection{
			{From: "pid-design.controller.u", To: "pid-design.plant.u"},
			{From: "pid-design.plant.y", To: "pid-design.controller.y"},
		},
		[]string{"pid-design.controller.r"},
		[]string{"pid-design.plant.y"},
	)
	if err != nil {
		return nil, err
	}
	if err := result.SetInputName("reference"); err != nil {
		return nil, err
	}
	if err := result.SetOutputName("measurement"); err != nil {
		return nil, err
	}
	return result, nil
}

func pidCandidateChanges(
	block Block,
	pid *controlsys.PID,
	b, c float64,
) []tunedParameterChange {
	type fieldValue struct {
		field TunableField
		value float64
	}
	values := []fieldValue{
		{TunableProportional, pid.Kp},
		{TunableIntegral, pid.Ki},
		{TunableDerivative, pid.Kd},
		{TunableFilterTime, pid.Tf},
	}
	if block.Kind == BlockPID2 {
		values = append(values,
			fieldValue{TunableSetpointWeight, b},
			fieldValue{TunableDerivativeWeight, c},
		)
	}
	changes := make([]tunedParameterChange, len(values))
	for i, value := range values {
		changes[i] = tunedParameterChange{
			ref:   TunableParameterRef{BlockID: block.ID, Field: value.field},
			value: value.value,
		}
	}
	return changes
}

func (s *Studio) ApplyPIDDesignCandidate(
	ctx context.Context,
	candidate PIDDesignCandidate,
) (Snapshot, error) {
	result, err := s.ApplyPIDDesignCandidateWithUndo(ctx, candidate)
	return result.Snapshot, err
}

func (s *Studio) ApplyPIDDesignCandidateWithUndo(
	ctx context.Context,
	candidate PIDDesignCandidate,
) (ControllerCandidateApplication, error) {
	if candidate.ControllerBlockID <= 0 || candidate.edit == nil {
		return ControllerCandidateApplication{}, invalid(
			"PID controller candidate is incomplete; design it again",
		)
	}
	return s.applyCandidateBlockEditWithUndo(ctx, candidateApplyRequest{
		flowID: candidate.FlowID, modelRevision: candidate.SourceModelRevision,
		controlRoles: candidate.SourceControlRoles, edit: candidate.edit,
		event: "Applied pidtune controller candidate",
	})
}
