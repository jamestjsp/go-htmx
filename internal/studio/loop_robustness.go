package studio

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jamestjsp/controlsys"
)

type LoopRobustnessRequest struct {
	BaseStep            float64            `json:"baseStep,omitempty"`
	Omega               []float64          `json:"omega,omitempty"`
	Points              int                `json:"points,omitempty"`
	CandidateController *controlsys.System `json:"-"`
}

type LoopRobustnessAnalysis struct {
	FlowID              int64                    `json:"flowId"`
	SourceModelRevision time.Time                `json:"sourceModelRevision"`
	Grid                FrequencyGrid            `json:"grid"`
	Units               FrequencyUnits           `json:"units"`
	Current             LoopSensitivityAnalysis  `json:"current"`
	Candidate           *LoopSensitivityAnalysis `json:"candidate,omitempty"`
}

type LoopSensitivityAnalysis struct {
	OutputSensitivity              LoopResponseAnalysis       `json:"outputSensitivity"`
	OutputComplementarySensitivity LoopResponseAnalysis       `json:"outputComplementarySensitivity"`
	InputSensitivity               LoopResponseAnalysis       `json:"inputSensitivity"`
	InputComplementarySensitivity  LoopResponseAnalysis       `json:"inputComplementarySensitivity"`
	ClassicalMargin                *ClassicalMarginAnalysis   `json:"classicalMargin,omitempty"`
	DiskMargin                     *SensitivityMarginAnalysis `json:"diskMargin,omitempty"`
	ClosedLoopBandwidth            *float64                   `json:"closedLoopBandwidth,omitempty"`
	Issues                         []AnalysisIssue            `json:"issues,omitempty"`
	models                         *controlsys.LoopsensResult
}

type LoopResponseAnalysis struct {
	Side           string                 `json:"side"`
	InputNames     []string               `json:"inputNames"`
	OutputNames    []string               `json:"outputNames"`
	Bode           []BodeTrace            `json:"bode"`
	SingularValues *SingularValueAnalysis `json:"singularValues,omitempty"`
	HInfinityNorm  *float64               `json:"hInfinityNorm,omitempty"`
	HInfinityOmega *float64               `json:"hInfinityOmega,omitempty"`
	Issues         []AnalysisIssue        `json:"issues,omitempty"`
}

func (s *Studio) AnalyzeLoopRobustness(
	ctx context.Context,
	flowID int64,
	request LoopRobustnessRequest,
) (LoopRobustnessAnalysis, error) {
	if err := validateLoopRobustnessRequest(request); err != nil {
		return LoopRobustnessAnalysis{}, err
	}
	spec, err := loadControlRoleSpec(ctx, s.db, flowID)
	if err != nil {
		return LoopRobustnessAnalysis{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return LoopRobustnessAnalysis{}, err
	}
	resolved, err := resolveControlRoleSpec(snapshot.Blocks, snapshot.Connections, spec)
	if err != nil {
		return LoopRobustnessAnalysis{}, err
	}
	models, err := buildControlModels(
		snapshot,
		resolved,
		ControlModelBuildRequest{BaseStep: request.BaseStep},
	)
	if err != nil {
		return LoopRobustnessAnalysis{}, fmt.Errorf(
			"selected plant/controller roles: %w", err,
		)
	}
	omega, gridSource, err := loopRobustnessGrid(
		request.Omega, request.Points, models.Points[0].OpenLoop,
	)
	if err != nil {
		return LoopRobustnessAnalysis{}, err
	}
	current, err := analyzeLoopController(models.Plant, models.Controller, omega)
	if err != nil {
		return LoopRobustnessAnalysis{}, fmt.Errorf(
			"current controller from selected roles: %w", err,
		)
	}
	result := LoopRobustnessAnalysis{
		FlowID: flowID, SourceModelRevision: snapshot.Flow.ModelUpdatedAt,
		Grid: FrequencyGrid{Source: gridSource, Omega: append([]float64(nil), omega...)},
		Units: FrequencyUnits{
			Frequency: "rad/s", Magnitude: "dB", Phase: "degrees",
			PhasePolicy: "unwrapped between adjacent frequency samples",
			Singular:    "absolute gain",
		},
		Current: current,
	}
	if models.Plant.IsDiscrete() {
		result.Grid.DiscreteNyquist = floatPointer(math.Pi / models.Plant.Dt)
	}
	if request.CandidateController == nil {
		return result, nil
	}
	candidateController := request.CandidateController.Copy()
	if err := validateControlModelDomains(models.Plant, candidateController); err != nil {
		return LoopRobustnessAnalysis{}, invalid(
			"candidate controller is incompatible with selected plant/controller roles: %s",
			err,
		)
	}
	if err := candidateController.SetInputName(models.Controller.InputName...); err != nil {
		return LoopRobustnessAnalysis{}, invalid(
			"candidate controller measurement names: %s", err,
		)
	}
	if err := candidateController.SetOutputName(models.Controller.OutputName...); err != nil {
		return LoopRobustnessAnalysis{}, invalid(
			"candidate controller control names: %s", err,
		)
	}
	candidate, err := analyzeLoopController(
		models.Plant, candidateController, omega,
	)
	if err != nil {
		return LoopRobustnessAnalysis{}, fmt.Errorf(
			"candidate controller for selected roles: %w", err,
		)
	}
	result.Candidate = &candidate
	return result, nil
}

func validateLoopRobustnessRequest(request LoopRobustnessRequest) error {
	if request.BaseStep < 0 || math.IsNaN(request.BaseStep) ||
		math.IsInf(request.BaseStep, 0) {
		return invalid("robustness base step must be a non-negative finite value")
	}
	if request.Points != 0 &&
		(request.Points < 2 || request.Points > maxFrequencyPoints) {
		return invalid(
			"robustness frequency points must be between 2 and %d",
			maxFrequencyPoints,
		)
	}
	if len(request.Omega) != 0 {
		if request.Points != 0 {
			return invalid("choose an explicit robustness grid or a point count, not both")
		}
		if len(request.Omega) < 2 || len(request.Omega) > maxFrequencyPoints {
			return invalid(
				"robustness frequency grid must contain between 2 and %d points",
				maxFrequencyPoints,
			)
		}
		for i, frequency := range request.Omega {
			if frequency <= 0 || math.IsNaN(frequency) || math.IsInf(frequency, 0) {
				return invalid("robustness frequency %d must be positive and finite", i+1)
			}
			if i > 0 && frequency <= request.Omega[i-1] {
				return invalid("robustness frequency grid must be strictly increasing")
			}
		}
	}
	return nil
}

func loopRobustnessGrid(
	requested []float64,
	points int,
	openLoop *controlsys.System,
) ([]float64, string, error) {
	if len(requested) != 0 {
		if err := validateFrequencyGridForSystem(requested, openLoop); err != nil {
			return nil, "", err
		}
		return append([]float64(nil), requested...), "explicit", nil
	}
	if points == 0 {
		points = defaultFrequencyPoints
	}
	bode, err := openLoop.Bode(nil, points)
	if err != nil {
		return nil, "", fmt.Errorf("automatic robustness frequency grid: %w", err)
	}
	return append([]float64(nil), bode.Omega...), "automatic", nil
}

func analyzeLoopController(
	plant, controller *controlsys.System,
	omega []float64,
) (LoopSensitivityAnalysis, error) {
	if err := validateControlModelDomains(plant, controller); err != nil {
		return LoopSensitivityAnalysis{}, err
	}
	_, inputs, outputs := plant.Dims()
	if inputs > maxAnalysisChannelsPerAxis ||
		outputs > maxAnalysisChannelsPerAxis ||
		inputs*inputs > maxFrequencyResponseTraces ||
		outputs*outputs > maxFrequencyResponseTraces {
		return LoopSensitivityAnalysis{}, invalid(
			"loop sensitivity is limited to %d channels per side and %d traces per view; selected plant has %d controls and %d measurements",
			maxAnalysisChannelsPerAxis, maxFrequencyResponseTraces, inputs, outputs,
		)
	}
	models, err := controlsys.Loopsens(plant, controller)
	if err != nil {
		return LoopSensitivityAnalysis{}, err
	}
	if err := nameLoopSensitivityModels(plant, models); err != nil {
		return LoopSensitivityAnalysis{}, err
	}
	so, err := loopResponseAnalysis("output", models.So, omega)
	if err != nil {
		return LoopSensitivityAnalysis{}, fmt.Errorf("output sensitivity: %w", err)
	}
	to, err := loopResponseAnalysis("output", models.To, omega)
	if err != nil {
		return LoopSensitivityAnalysis{}, fmt.Errorf("output complementary sensitivity: %w", err)
	}
	si, err := loopResponseAnalysis("input", models.Si, omega)
	if err != nil {
		return LoopSensitivityAnalysis{}, fmt.Errorf("input sensitivity: %w", err)
	}
	ti, err := loopResponseAnalysis("input", models.Ti, omega)
	if err != nil {
		return LoopSensitivityAnalysis{}, fmt.Errorf("input complementary sensitivity: %w", err)
	}
	result := LoopSensitivityAnalysis{
		OutputSensitivity: so, OutputComplementarySensitivity: to,
		InputSensitivity: si, InputComplementarySensitivity: ti,
		models: models,
	}
	outputLoop, loopErr := controlsys.Series(controller, plant)
	if loopErr != nil {
		result.Issues = append(result.Issues, AnalysisIssue{
			Operation: "output-loop", Message: loopErr.Error(),
		})
		return result, nil
	}
	if inputs == 1 && outputs == 1 {
		if margin, marginErr := controlsys.Margin(outputLoop); marginErr == nil {
			result.ClassicalMargin = pidMarginEvidence(margin)
		} else {
			result.Issues = append(result.Issues, AnalysisIssue{
				Operation: "classical-margin", Message: marginErr.Error(),
			})
		}
		if margin, marginErr := controlsys.DiskMargin(outputLoop); marginErr == nil {
			result.DiskMargin = loopDiskMarginEvidence(margin)
		} else {
			result.Issues = append(result.Issues, AnalysisIssue{
				Operation: "disk-margin", Message: marginErr.Error(),
			})
		}
		if bandwidth, bandwidthErr := controlsys.Bandwidth(models.To, -3); bandwidthErr == nil {
			result.ClosedLoopBandwidth = finitePointer(bandwidth)
		} else {
			result.Issues = append(result.Issues, AnalysisIssue{
				Operation: "closed-loop-bandwidth", Message: bandwidthErr.Error(),
			})
		}
	}
	return result, nil
}

func nameLoopSensitivityModels(
	plant *controlsys.System,
	models *controlsys.LoopsensResult,
) error {
	for _, system := range []*controlsys.System{models.So, models.To} {
		if err := system.SetInputName(plant.OutputName...); err != nil {
			return err
		}
		if err := system.SetOutputName(plant.OutputName...); err != nil {
			return err
		}
	}
	for _, system := range []*controlsys.System{models.Si, models.Ti} {
		if err := system.SetInputName(plant.InputName...); err != nil {
			return err
		}
		if err := system.SetOutputName(plant.InputName...); err != nil {
			return err
		}
	}
	return nil
}

func loopResponseAnalysis(
	side string,
	system *controlsys.System,
	omega []float64,
) (LoopResponseAnalysis, error) {
	frd, err := system.FRD(omega)
	if err != nil {
		return LoopResponseAnalysis{}, err
	}
	_, inputs, outputs := system.Dims()
	result := LoopResponseAnalysis{
		Side:        side,
		InputNames:  append([]string(nil), system.InputName...),
		OutputNames: append([]string(nil), system.OutputName...),
		Bode:        bodeTraces(frd.Bode(), outputs, inputs),
	}
	if sigma, sigmaErr := frd.Sigma(); sigmaErr == nil {
		result.SingularValues = singularValueAnalysis(sigma)
	} else {
		result.Issues = append(result.Issues, AnalysisIssue{
			Operation: "singular-values", Message: sigmaErr.Error(),
		})
	}
	if norm, peak, normErr := controlsys.HinfNorm(system); normErr == nil {
		result.HInfinityNorm = finitePointer(norm)
		result.HInfinityOmega = finitePointer(peak)
	} else {
		result.Issues = append(result.Issues, AnalysisIssue{
			Operation: "h-infinity-norm", Message: normErr.Error(),
		})
	}
	return result, nil
}

func loopDiskMarginEvidence(
	result *controlsys.DiskMarginResult,
) *SensitivityMarginAnalysis {
	return &SensitivityMarginAnalysis{
		Method:                    "modulus margin derived from peak sensitivity; not a general disk-margin certificate",
		Alpha:                     finitePointer(result.Alpha),
		LinearGainRange:           [2]*float64{finitePointer(result.GainMargin[0]), finitePointer(result.GainMargin[1])},
		GainRangeDB:               [2]*float64{finitePointer(result.GainMarginDB[0]), finitePointer(result.GainMarginDB[1])},
		SymmetricPhaseDegrees:     finitePointer(result.PhaseMargin),
		PeakSensitivity:           finitePointer(result.PeakSensitivity),
		PeakFrequencyRadPerSecond: finitePointer(result.PeakFreq),
	}
}
