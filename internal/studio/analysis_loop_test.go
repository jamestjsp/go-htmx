package studio

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestLoopAnalysisReturnsStableFirstOrderDiagnostics(t *testing.T) {
	result, err := analyzeLoop(
		analysisSISOBlocks([]float64{1}, []float64{1, 1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		LoopAnalysisRequest{
			Input:          ChannelRef{BlockID: 1},
			Output:         ChannelRef{BlockID: 2},
			RootLocusGains: []float64{0, 1, 4},
			PassivityOmega: []float64{0.01, 0.1, 1, 10},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Basis != "explicit-selected-siso-channel" ||
		result.Domain != "continuous-s-plane" ||
		result.Margins == nil ||
		result.AllMargins == nil {
		t.Fatalf("loop identity and margins = %#v", result)
	}
	if !result.Margins.NoFiniteGainMargin ||
		!result.Margins.NoFinitePhaseMargin ||
		result.Margins.GainMarginDB != nil ||
		result.Margins.PhaseMarginDegrees != nil {
		t.Fatalf("no-crossover margins = %#v", result.Margins)
	}
	wantBandwidth := math.Sqrt(math.Pow(10, 0.3) - 1)
	if result.Bandwidth == nil ||
		result.Bandwidth.RadPerSecond == nil ||
		math.Abs(*result.Bandwidth.RadPerSecond-wantBandwidth) > 1e-6 {
		t.Fatalf("bandwidth = %#v, want %g", result.Bandwidth, wantBandwidth)
	}
	if result.RootLocus == nil ||
		result.RootLocus.Plane != "s" ||
		len(result.RootLocus.Branches) != 1 {
		t.Fatalf("root locus = %#v", result.RootLocus)
	}
	for i, want := range []float64{-1, -2, -5} {
		if got := result.RootLocus.Branches[0][i]; math.Abs(got.Real-want) > 1e-10 || math.Abs(got.Imag) > 1e-10 {
			t.Fatalf("root locus branch[%d] = %#v, want %g", i, got, want)
		}
	}
	if result.Passivity == nil ||
		result.Passivity.Status == "certified" ||
		!result.Passivity.PassiveOnSampledGrid ||
		!strings.Contains(result.Passivity.Guarantee, "not an analytic certificate") {
		t.Fatalf("passivity evidence = %#v", result.Passivity)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("marshal loop result: %v", err)
	}
}

func TestLoopAnalysisReturnsEveryGainCrossover(t *testing.T) {
	result, err := analyzeLoop(
		analysisSISOBlocks(
			[]float64{2, 0.08, 2},
			[]float64{1, 0.4, 1},
		),
		[]Connection{{SourceID: 1, TargetID: 2}},
		LoopAnalysisRequest{
			Input: ChannelRef{BlockID: 1}, Output: ChannelRef{BlockID: 2},
			RootLocusGains: []float64{0, 1},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.AllMargins == nil ||
		len(result.AllMargins.GainCrossoversRadPerSecond) != 2 ||
		len(result.AllMargins.PhaseMarginsDegrees) != 2 {
		t.Fatalf("all margins = %#v, want two gain crossovers", result.AllMargins)
	}
}

func TestLoopAnalysisStatesUnstablePassivityLimitation(t *testing.T) {
	result, err := analyzeLoop(
		analysisSISOBlocks([]float64{1}, []float64{1, -1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		LoopAnalysisRequest{
			Input: ChannelRef{BlockID: 1}, Output: ChannelRef{BlockID: 2},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passivity != nil ||
		analysisApplicability(result.Applicability, "sampled-passivity").Status != "unavailable" ||
		!strings.Contains(
			analysisApplicability(result.Applicability, "sampled-passivity").Detail,
			"unstable",
		) {
		t.Fatalf("unstable passivity applicability = %#v", result.Applicability)
	}
}

func TestLoopAnalysisReportsSampledPassivityCounterexample(t *testing.T) {
	result, err := analyzeLoop(
		analysisSISOBlocks([]float64{-1}, []float64{1, 1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		LoopAnalysisRequest{
			Input:          ChannelRef{BlockID: 1},
			Output:         ChannelRef{BlockID: 2},
			RootLocusGains: []float64{0, 1},
			PassivityOmega: []float64{0.01, 0.1, 1, 10},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passivity == nil ||
		result.Passivity.Status != "violated" ||
		result.Passivity.PassiveOnSampledGrid ||
		result.Passivity.MinimumHermitianPart == nil ||
		*result.Passivity.MinimumHermitianPart >= 0 {
		t.Fatalf("passivity counterexample = %#v", result.Passivity)
	}
}

func TestLoopAnalysisUsesDiscreteZPlaneAndNyquistBound(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockUnitDelay, Name: "Memory", Parameters: Parameters{
			SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
		}},
	}
	result, err := analyzeLoop(
		blocks,
		[]Connection{{SourceID: 1, TargetID: 2}},
		LoopAnalysisRequest{
			Input:          ChannelRef{BlockID: 1},
			Output:         ChannelRef{BlockID: 2},
			BaseStep:       0.1,
			RootLocusGains: []float64{0, 1},
			PassivityOmega: []float64{1, 10},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Domain != "discrete-z-plane" ||
		result.RootLocus == nil ||
		result.RootLocus.Plane != "z" {
		t.Fatalf("discrete applicability = %#v", result)
	}
	aboveNyquist, err := analyzeLoop(
		blocks,
		[]Connection{{SourceID: 1, TargetID: 2}},
		LoopAnalysisRequest{
			Input: ChannelRef{BlockID: 1}, Output: ChannelRef{BlockID: 2},
			BaseStep:       0.1,
			PassivityOmega: []float64{1, 40},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	applicability := analysisApplicability(aboveNyquist.Applicability, "sampled-passivity")
	if applicability.Status != "unavailable" ||
		!strings.Contains(applicability.Detail, "Nyquist limit") {
		t.Fatalf("above-Nyquist passivity applicability = %#v", applicability)
	}
}

func TestLoopAnalysisLimitsFiniteOrderOperationsForInternalDelay(t *testing.T) {
	result, err := analyzeLoop(
		[]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockSum, Name: "Error", Parameters: Parameters{Signs: "+-"}},
			{ID: 3, Kind: BlockLag, Name: "Plant", Parameters: Parameters{TimeConstant: 1}},
			{ID: 4, Kind: BlockDelay, Name: "Feedback delay", Parameters: Parameters{
				Delay: 0.2, DelayMode: delayModeExact,
			}},
		},
		[]Connection{
			{SourceID: 1, TargetID: 2, TargetPort: 0},
			{SourceID: 4, TargetID: 2, TargetPort: 1},
			{SourceID: 2, TargetID: 3},
			{SourceID: 3, TargetID: 4},
		},
		LoopAnalysisRequest{
			Input: ChannelRef{BlockID: 1}, Output: ChannelRef{BlockID: 4},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Margins == nil || result.AllMargins == nil {
		t.Fatalf("exact-delay frequency margins = %#v", result)
	}
	for _, operation := range []string{"bandwidth", "modulus-margin", "root-locus"} {
		applicability := analysisApplicability(result.Applicability, operation)
		if applicability.Status != "unavailable" ||
			!strings.Contains(applicability.Detail, "internal exact delay") {
			t.Fatalf("%s applicability = %#v", operation, applicability)
		}
	}
	if result.Bandwidth != nil || result.DiskMargin != nil || result.RootLocus != nil {
		t.Fatalf("finite-order exact-delay diagnostics = %#v", result)
	}
}

func TestLoopAnalysisValidatesGainGrid(t *testing.T) {
	err := validateLoopAnalysisRequest(LoopAnalysisRequest{RootLocusGains: []float64{-1}})
	if err == nil || !strings.Contains(err.Error(), "non-negative") {
		t.Fatalf("gain validation error = %v", err)
	}
}

func analysisApplicability(
	values []AnalysisApplicability,
	operation string,
) AnalysisApplicability {
	for _, value := range values {
		if value.Operation == operation {
			return value
		}
	}
	return AnalysisApplicability{}
}
