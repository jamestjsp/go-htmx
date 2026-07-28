package studio

import (
	"encoding/json"
	"math"
	"testing"
)

func TestDynamicsAnalysisMatchesFirstOrderOracle(t *testing.T) {
	result, err := analyzeDynamics(
		analysisSISOBlocks([]float64{1}, []float64{1, 1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		DynamicsAnalysisRequest{
			Input:  ChannelRef{BlockID: 1},
			Output: ChannelRef{BlockID: 2},
			Step:   &StepExperiment{Horizon: 10},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("issues = %#v", result.Issues)
	}
	if result.Stable == nil || !*result.Stable {
		t.Fatalf("stable = %v", result.Stable)
	}
	if len(result.Poles) != 1 ||
		math.Abs(result.Poles[0].Real+1) > 1e-12 ||
		math.Abs(result.Poles[0].Imag) > 1e-12 {
		t.Fatalf("poles = %#v", result.Poles)
	}
	if len(result.Zeros) != 0 {
		t.Fatalf("zeros = %#v, want none", result.Zeros)
	}
	if result.DCGain == nil || math.Abs(*result.DCGain-1) > 1e-12 {
		t.Fatalf("dc gain = %v", result.DCGain)
	}
	if len(result.Damping) != 1 ||
		math.Abs(result.Damping[0].NaturalFrequency-1) > 1e-12 ||
		math.Abs(result.Damping[0].DampingRatio-1) > 1e-12 ||
		result.Damping[0].TimeConstant == nil ||
		math.Abs(*result.Damping[0].TimeConstant-1) > 1e-12 {
		t.Fatalf("damping = %#v", result.Damping)
	}
	if result.StepExperiment == nil {
		t.Fatal("step experiment is nil")
	}
	metrics := result.StepExperiment.Metrics
	if metrics.RiseTime == nil || math.Abs(*metrics.RiseTime-math.Log(9)) > 0.03 {
		t.Fatalf("rise time = %v, want ln(9)", metrics.RiseTime)
	}
	if metrics.SettlingTime == nil || math.Abs(*metrics.SettlingTime-math.Log(50)) > 0.05 {
		t.Fatalf("settling time = %v, want ln(50)", metrics.SettlingTime)
	}
	if metrics.Overshoot == nil || math.Abs(*metrics.Overshoot) > 1e-9 ||
		metrics.SteadyStateValue == nil || math.Abs(*metrics.SteadyStateValue-1) > 1e-12 {
		t.Fatalf("step metrics = %#v", metrics)
	}
}

func TestDynamicsAnalysisMatchesUnderdampedSecondOrderOracle(t *testing.T) {
	const (
		naturalFrequency = 2.0
		dampingRatio     = 0.25
	)
	result, err := analyzeDynamics(
		analysisSISOBlocks(
			[]float64{naturalFrequency * naturalFrequency},
			[]float64{1, 2 * dampingRatio * naturalFrequency, naturalFrequency * naturalFrequency},
		),
		[]Connection{{SourceID: 1, TargetID: 2}},
		DynamicsAnalysisRequest{
			Input:  ChannelRef{BlockID: 1},
			Output: ChannelRef{BlockID: 2},
			Step:   &StepExperiment{Horizon: 15},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Damping) != 2 {
		t.Fatalf("damping = %#v", result.Damping)
	}
	for _, mode := range result.Damping {
		if math.Abs(mode.NaturalFrequency-naturalFrequency) > 1e-10 ||
			math.Abs(mode.DampingRatio-dampingRatio) > 1e-10 {
			t.Fatalf("mode = %#v", mode)
		}
	}
	wantOvershoot := 100 * math.Exp(
		-math.Pi*dampingRatio/math.Sqrt(1-dampingRatio*dampingRatio),
	)
	metrics := result.StepExperiment.Metrics
	if metrics.Overshoot == nil || math.Abs(*metrics.Overshoot-wantOvershoot) > 0.5 {
		t.Fatalf("overshoot = %v, want %g", metrics.Overshoot, wantOvershoot)
	}
	wantPeakTime := math.Pi / (naturalFrequency * math.Sqrt(1-dampingRatio*dampingRatio))
	if metrics.PeakTime == nil || math.Abs(*metrics.PeakTime-wantPeakTime) > 0.05 {
		t.Fatalf("peak time = %v, want %g", metrics.PeakTime, wantPeakTime)
	}
}

func TestDynamicsAnalysisReturnsPartialResultsWithoutNonFiniteJSON(t *testing.T) {
	tests := []struct {
		name        string
		denominator []float64
		wantIssue   string
	}{
		{name: "unstable", denominator: []float64{1, -1}, wantIssue: "step"},
		{name: "integrator", denominator: []float64{1, 0}, wantIssue: "dc-gain"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := analyzeDynamics(
				analysisSISOBlocks([]float64{1}, test.denominator),
				[]Connection{{SourceID: 1, TargetID: 2}},
				DynamicsAnalysisRequest{
					Input:  ChannelRef{BlockID: 1},
					Output: ChannelRef{BlockID: 2},
					Step:   &StepExperiment{Horizon: 5},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Stable == nil || *result.Stable {
				t.Fatalf("stable = %v, want false", result.Stable)
			}
			if len(result.Poles) != 1 {
				t.Fatalf("poles = %#v", result.Poles)
			}
			if !hasAnalysisIssue(result.Issues, test.wantIssue) {
				t.Fatalf("issues = %#v, want %q", result.Issues, test.wantIssue)
			}
			if result.StepExperiment != nil {
				t.Fatalf("unstable step result = %#v", result.StepExperiment)
			}
			if _, err := json.Marshal(result); err != nil {
				t.Fatalf("marshal partial result: %v", err)
			}
		})
	}
}

func TestDynamicsAnalysisDoesNotRunAnImplicitStepExperiment(t *testing.T) {
	result, err := analyzeDynamics(
		analysisSISOBlocks([]float64{1}, []float64{1, 1}),
		[]Connection{{SourceID: 1, TargetID: 2}},
		DynamicsAnalysisRequest{
			Input: ChannelRef{BlockID: 1}, Output: ChannelRef{BlockID: 2},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.StepExperiment != nil || hasAnalysisIssue(result.Issues, "step") {
		t.Fatalf("implicit step result = %#v, issues = %#v", result.StepExperiment, result.Issues)
	}
}

func analysisSISOBlocks(numerator, denominator []float64) []Block {
	return []Block{
		{ID: 1, Kind: BlockSource, Name: "Excitation", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockTransfer, Name: "Plant", Parameters: Parameters{
			Numerator: numerator, Denominator: denominator,
		}},
	}
}

func hasAnalysisIssue(issues []AnalysisIssue, operation string) bool {
	for _, issue := range issues {
		if issue.Operation == operation {
			return true
		}
	}
	return false
}
