package studio

import (
	"context"
	"math"
	"time"

	"github.com/jamestjsp/controlsys"
)

type LoopAnalysisRequest struct {
	Input              ChannelRef `json:"input"`
	Output             ChannelRef `json:"output"`
	BaseStep           float64    `json:"baseStep,omitempty"`
	BandwidthDropDB    float64    `json:"bandwidthDropDb,omitempty"`
	RootLocusGains     []float64  `json:"rootLocusGains,omitempty"`
	PassivityOmega     []float64  `json:"passivityOmega,omitempty"`
	PassivityTolerance float64    `json:"passivityTolerance,omitempty"`
}

type LoopAnalysis struct {
	FlowID         int64                      `json:"flowId"`
	ModelUpdatedAt time.Time                  `json:"modelUpdatedAt"`
	Input          AnalyzedChannel            `json:"input"`
	Output         AnalyzedChannel            `json:"output"`
	Basis          string                     `json:"basis"`
	Domain         string                     `json:"domain"`
	Margins        *ClassicalMarginAnalysis   `json:"margins,omitempty"`
	AllMargins     *AllMarginAnalysis         `json:"allMargins,omitempty"`
	Bandwidth      *BandwidthAnalysis         `json:"bandwidth,omitempty"`
	DiskMargin     *SensitivityMarginAnalysis `json:"diskMargin,omitempty"`
	RootLocus      *RootLocusAnalysis         `json:"rootLocus,omitempty"`
	Passivity      *SampledPassivityEvidence  `json:"passivity,omitempty"`
	Applicability  []AnalysisApplicability    `json:"applicability"`
}

type AnalysisApplicability struct {
	Operation string `json:"operation"`
	Status    string `json:"status"`
	Detail    string `json:"detail"`
}

type ClassicalMarginAnalysis struct {
	GainMarginDB               *float64 `json:"gainMarginDb,omitempty"`
	PhaseMarginDegrees         *float64 `json:"phaseMarginDegrees,omitempty"`
	GainCrossoverRadPerSecond  *float64 `json:"gainCrossoverRadPerSecond,omitempty"`
	PhaseCrossoverRadPerSecond *float64 `json:"phaseCrossoverRadPerSecond,omitempty"`
	NoFiniteGainMargin         bool     `json:"noFiniteGainMargin"`
	NoFinitePhaseMargin        bool     `json:"noFinitePhaseMargin"`
}

type AllMarginAnalysis struct {
	GainMarginsDB               []float64 `json:"gainMarginsDb"`
	PhaseMarginsDegrees         []float64 `json:"phaseMarginsDegrees"`
	GainCrossoversRadPerSecond  []float64 `json:"gainCrossoversRadPerSecond"`
	PhaseCrossoversRadPerSecond []float64 `json:"phaseCrossoversRadPerSecond"`
}

type BandwidthAnalysis struct {
	DropDB       float64  `json:"dropDb"`
	RadPerSecond *float64 `json:"radPerSecond,omitempty"`
	Unbounded    bool     `json:"unbounded"`
}

type SensitivityMarginAnalysis struct {
	Method                    string      `json:"method"`
	Alpha                     *float64    `json:"alpha,omitempty"`
	LinearGainRange           [2]*float64 `json:"linearGainRange"`
	GainRangeDB               [2]*float64 `json:"gainRangeDb"`
	SymmetricPhaseDegrees     *float64    `json:"symmetricPhaseDegrees,omitempty"`
	PeakSensitivity           *float64    `json:"peakSensitivity,omitempty"`
	PeakFrequencyRadPerSecond *float64    `json:"peakFrequencyRadPerSecond,omitempty"`
}

type RootLocusAnalysis struct {
	Plane              string           `json:"plane"`
	Gains              []float64        `json:"gains"`
	Branches           [][]ComplexValue `json:"branches"`
	Breakaway          []ComplexValue   `json:"breakaway,omitempty"`
	AsymptoteAnglesRad []float64        `json:"asymptoteAnglesRad,omitempty"`
	AsymptoteCentroid  *float64         `json:"asymptoteCentroid,omitempty"`
	DepartureAnglesRad []float64        `json:"departureAnglesRad,omitempty"`
	ArrivalAnglesRad   []float64        `json:"arrivalAnglesRad,omitempty"`
}

type SampledPassivityEvidence struct {
	Guarantee                  string    `json:"guarantee"`
	Status                     string    `json:"status"`
	PassiveOnSampledGrid       bool      `json:"passiveOnSampledGrid"`
	MinimumHermitianPart       *float64  `json:"minimumHermitianPart,omitempty"`
	WorstFrequencyRadPerSecond *float64  `json:"worstFrequencyRadPerSecond,omitempty"`
	Omega                      []float64 `json:"omega"`
	Tolerance                  float64   `json:"tolerance"`
}

func (s *Studio) AnalyzeLoop(
	ctx context.Context,
	flowID int64,
	request LoopAnalysisRequest,
) (LoopAnalysis, error) {
	if err := validateLoopAnalysisRequest(request); err != nil {
		return LoopAnalysis{}, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	if err != nil {
		return LoopAnalysis{}, err
	}
	result, err := analyzeLoop(snapshot.Blocks, snapshot.Connections, request)
	if err != nil {
		return LoopAnalysis{}, err
	}
	result.FlowID = flowID
	result.ModelUpdatedAt = snapshot.Flow.ModelUpdatedAt
	return result, nil
}

func analyzeLoop(
	blocks []Block,
	connections []Connection,
	request LoopAnalysisRequest,
) (LoopAnalysis, error) {
	if err := validateLoopAnalysisRequest(request); err != nil {
		return LoopAnalysis{}, err
	}
	model, err := compileRequestedModel(blocks, connections, modelCompileRequest{
		probes: []modelProbe{{
			BlockID: request.Output.BlockID, OutputPort: request.Output.Port,
		}},
		baseStep: request.BaseStep,
	})
	if err != nil {
		return LoopAnalysis{}, err
	}
	hasInternalDelay := model.system.HasInternalDelay()
	system, inputs, outputs, err := model.selectChannels(
		[]ChannelRef{request.Input},
		[]ChannelRef{request.Output},
	)
	if err != nil {
		return LoopAnalysis{}, err
	}
	result := LoopAnalysis{
		Input:  analyzedChannel(request.Input, inputs[0]),
		Output: analyzedChannel(request.Output, outputs[0]),
		Basis:  "explicit-selected-siso-channel",
		Domain: "continuous-s-plane",
	}
	if system.IsDiscrete() {
		result.Domain = "discrete-z-plane"
	}

	result.computeMargins(system)
	result.computeAllMargins(system)

	rationalUnavailable := hasInternalDelay || system.HasInternalDelay()
	if rationalUnavailable {
		detail := "unavailable because an internal exact delay has no finite rational realization; use the frequency response"
		result.unavailable("bandwidth", detail)
		result.unavailable("modulus-margin", detail)
		result.unavailable("root-locus", detail)
	} else {
		result.computeBandwidth(system, request.BandwidthDropDB)
		result.computeSensitivityMargin(system)
		result.computeRootLocus(system, request.RootLocusGains)
	}
	result.computePassivity(system, request.PassivityOmega, request.PassivityTolerance)
	return result, nil
}

func validateLoopAnalysisRequest(request LoopAnalysisRequest) error {
	if math.IsNaN(request.BandwidthDropDB) || math.IsInf(request.BandwidthDropDB, 0) ||
		request.BandwidthDropDB > 0 {
		return invalid("bandwidth drop must be zero for the -3 dB default or a negative finite dB value")
	}
	if len(request.RootLocusGains) > 2000 {
		return invalid("root-locus gain grid must contain at most 2,000 points")
	}
	for i, gain := range request.RootLocusGains {
		if math.IsNaN(gain) || math.IsInf(gain, 0) || gain < 0 {
			return invalid("root-locus gain %d must be a non-negative finite value", i+1)
		}
	}
	if len(request.PassivityOmega) == 1 {
		return invalid("passivity frequency grid requires at least two points")
	}
	for i, frequency := range request.PassivityOmega {
		if math.IsNaN(frequency) || math.IsInf(frequency, 0) || frequency <= 0 {
			return invalid("passivity frequency %d must be a positive finite rad/s value", i+1)
		}
		if i > 0 && frequency <= request.PassivityOmega[i-1] {
			return invalid("passivity frequency grid must be strictly increasing at point %d", i+1)
		}
	}
	if math.IsNaN(request.PassivityTolerance) ||
		math.IsInf(request.PassivityTolerance, 0) ||
		request.PassivityTolerance < 0 {
		return invalid("passivity tolerance must be a non-negative finite value")
	}
	return nil
}

func (result *LoopAnalysis) computeMargins(system *controlsys.System) {
	margin, err := controlsys.Margin(system)
	if err != nil {
		result.unavailable("margin", err.Error())
		return
	}
	result.Margins = &ClassicalMarginAnalysis{
		GainMarginDB:               finitePointer(margin.GainMargin),
		PhaseMarginDegrees:         finitePointer(margin.PhaseMargin),
		GainCrossoverRadPerSecond:  finitePointer(margin.WgFreq),
		PhaseCrossoverRadPerSecond: finitePointer(margin.WpFreq),
		NoFiniteGainMargin:         !finite(margin.GainMargin),
		NoFinitePhaseMargin:        !finite(margin.PhaseMargin),
	}
	result.available("margin", "classical SISO gain and phase margins")
}

func (result *LoopAnalysis) computeAllMargins(system *controlsys.System) {
	all, err := controlsys.AllMargin(system)
	if err != nil {
		result.unavailable("all-margin", err.Error())
		return
	}
	result.AllMargins = &AllMarginAnalysis{
		GainMarginsDB:               append([]float64(nil), all.GainMargins...),
		PhaseMarginsDegrees:         append([]float64(nil), all.PhaseMargins...),
		GainCrossoversRadPerSecond:  append([]float64(nil), all.GainCrossFreqs...),
		PhaseCrossoversRadPerSecond: append([]float64(nil), all.PhaseCrossFreqs...),
	}
	result.available("all-margin", "all sampled and refined SISO crossovers")
}

func (result *LoopAnalysis) computeBandwidth(system *controlsys.System, dropDB float64) {
	effectiveDrop := dropDB
	if effectiveDrop == 0 {
		effectiveDrop = -3
	}
	bandwidth, err := controlsys.Bandwidth(system, effectiveDrop)
	if err != nil {
		result.unavailable("bandwidth", err.Error())
		return
	}
	result.Bandwidth = &BandwidthAnalysis{
		DropDB:       effectiveDrop,
		RadPerSecond: finitePointer(bandwidth),
		Unbounded:    math.IsInf(bandwidth, 1),
	}
	result.available("bandwidth", "selected-channel magnitude bandwidth relative to DC gain")
}

func (result *LoopAnalysis) computeSensitivityMargin(system *controlsys.System) {
	margin, err := controlsys.DiskMargin(system)
	if err != nil {
		result.unavailable("modulus-margin", err.Error())
		return
	}
	result.DiskMargin = &SensitivityMarginAnalysis{
		Method:                    "modulus margin derived from peak sensitivity; not a general disk-margin certificate",
		Alpha:                     finitePointer(margin.Alpha),
		LinearGainRange:           finitePair(margin.GainMargin),
		GainRangeDB:               finitePair(margin.GainMarginDB),
		SymmetricPhaseDegrees:     finitePointer(margin.PhaseMargin),
		PeakSensitivity:           finitePointer(margin.PeakSensitivity),
		PeakFrequencyRadPerSecond: finitePointer(margin.PeakFreq),
	}
	result.available("modulus-margin", result.DiskMargin.Method)
}

func (result *LoopAnalysis) computeRootLocus(system *controlsys.System, gains []float64) {
	if system.HasDelay() {
		result.unavailable("root-locus", "unavailable for delayed systems because root locus requires a finite rational realization")
		return
	}
	locus, err := controlsys.RootLocus(system, gains)
	if err != nil {
		result.unavailable("root-locus", err.Error())
		return
	}
	branches := make([][]ComplexValue, len(locus.Branches))
	for i, branch := range locus.Branches {
		branches[i] = complexValues(branch)
	}
	plane := "s"
	if system.IsDiscrete() {
		plane = "z"
	}
	result.RootLocus = &RootLocusAnalysis{
		Plane:              plane,
		Gains:              append([]float64(nil), locus.Gains...),
		Branches:           branches,
		Breakaway:          complexValues(locus.Breakaway),
		AsymptoteAnglesRad: append([]float64(nil), locus.AsymptoteAngles...),
		AsymptoteCentroid:  finitePointer(locus.AsymptoteCentroid),
		DepartureAnglesRad: append([]float64(nil), locus.DepartureAngles...),
		ArrivalAnglesRad:   append([]float64(nil), locus.ArrivalAngles...),
	}
	result.available("root-locus", "closed-loop branches for non-negative scalar gain in the "+plane+"-plane")
}

func (result *LoopAnalysis) computePassivity(
	system *controlsys.System,
	omega []float64,
	tolerance float64,
) {
	if err := validateFrequencyGridForSystemIfPresent(omega, system); err != nil {
		result.unavailable("sampled-passivity", err.Error())
		return
	}
	evidence, err := controlsys.SampledPassive(system, &controlsys.PassivityOptions{
		Omega: append([]float64(nil), omega...),
		Tol:   tolerance,
	})
	if err != nil {
		result.unavailable("sampled-passivity", err.Error())
		return
	}
	status := string(evidence.Status)
	if evidence.Status == controlsys.PassivityCertified {
		status = string(controlsys.PassivitySampled)
	}
	result.Passivity = &SampledPassivityEvidence{
		Guarantee:                  "sampled-frequency-grid evidence, not an analytic certificate",
		Status:                     status,
		PassiveOnSampledGrid:       evidence.Passive,
		MinimumHermitianPart:       finitePointer(evidence.MinHermitianPart),
		WorstFrequencyRadPerSecond: finitePointer(evidence.Frequency),
		Omega:                      append([]float64(nil), evidence.Omega...),
		Tolerance:                  evidence.Tolerance,
	}
	result.Applicability = append(result.Applicability, AnalysisApplicability{
		Operation: "sampled-passivity",
		Status:    status,
		Detail:    result.Passivity.Guarantee,
	})
}

func validateFrequencyGridForSystemIfPresent(omega []float64, system *controlsys.System) error {
	if len(omega) == 0 {
		return nil
	}
	return validateFrequencyGridForSystem(omega, system)
}

func (result *LoopAnalysis) available(operation, detail string) {
	result.Applicability = append(result.Applicability, AnalysisApplicability{
		Operation: operation, Status: "available", Detail: detail,
	})
}

func (result *LoopAnalysis) unavailable(operation, detail string) {
	result.Applicability = append(result.Applicability, AnalysisApplicability{
		Operation: operation, Status: "unavailable", Detail: detail,
	})
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finitePair(values [2]float64) [2]*float64 {
	return [2]*float64{finitePointer(values[0]), finitePointer(values[1])}
}
