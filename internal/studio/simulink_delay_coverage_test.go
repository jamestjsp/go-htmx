package studio

import (
	"context"
	"math"
	"math/cmplx"
	"path/filepath"
	"strconv"
	"testing"
)

func TestR2026aDiscreteMIMOPairwiseDelaysUseIntegerSampleCounts(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	requireR2026aCompatibilityCase(t, fixture, "discrete-integer-pairwise-delay")
	fixture.Model.Denominators = [][]float64{{1, -0.5}, {1, -0.25}}
	fixture.Model.Delays = [][]float64{{1, 4}, {3, 2}}
	flow := createMIMOCompatibilityFlow(t, fixture, modelDomainDiscrete)

	snapshot, err := flow.studio.Run(flow.ctx, flow.flowID, SimulationRequest{
		Duration: fixture.Simulation.Duration, SampleTime: fixture.Simulation.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil {
		t.Fatal("public discrete run was not persisted")
	}
	if snapshot.LastRun.Fidelity.Driver != "delay-aware-simulate" {
		t.Fatalf("simulation driver = %q, want delay-aware-simulate",
			snapshot.LastRun.Fidelity.Driver)
	}
	series := resultSeriesByChannel(t, snapshot.LastRun)
	for output, name := range fixture.Model.OutputNames {
		for sample := range snapshot.LastRun.Times {
			var want float64
			for input := range fixture.Model.InputNames {
				elapsed := sample - int(fixture.Model.Delays[output][input])
				if elapsed <= 0 {
					continue
				}
				pole := -fixture.Model.Denominators[output][1]
				want += fixture.Model.Numerators[output][input][0] *
					fixture.Model.InputValues[input] *
					(1 - math.Pow(pole, float64(elapsed))) /
					(1 - pole)
			}
			if got := series[name].Values[sample]; math.Abs(got-want) > 1e-12 {
				t.Fatalf("%s sample %d = %g, integer-delay oracle %g", name, sample, got, want)
			}
		}
	}
}

func TestR2026aMixedEmbeddedAndTransportDelaysCompose(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	requireR2026aCompatibilityCase(t, fixture, "mixed-embedded-and-transport-delay")
	studio, ctx, flowID := newCompatibilityFlow(t, "mixed-delay")

	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockVectorConstant, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, transferID, err := studio.AddBlock(ctx, flowID, BlockMIMOTransfer, Point{X: 350, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, delayID, err := studio.AddBlock(ctx, flowID, BlockDelay, Point{X: 600, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 850, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sourceID, BlockUpdate{
		Name: "Input", Parameters: map[string]string{
			"vector": "1", "output_names": "value",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, transferID, BlockUpdate{
		Name: "Embedded delay", Parameters: map[string]string{
			"transfer_numerators":   "1",
			"transfer_denominators": "1, 1",
			"transfer_delays":       "0.2",
			"input_names":           "value",
			"output_names":          "value",
			"time_domain":           modelDomainContinuous,
			"sample_time":           "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	updateExactTransportDelay(t, studio, ctx, delayID, "Transport delay", 0.3)
	for _, wire := range []Wire{
		{SourceID: sourceID, TargetID: transferID},
		{SourceID: transferID, TargetID: delayID},
		{SourceID: delayID, TargetID: scopeID},
	} {
		if _, err := studio.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil || snapshot.LastRun.Fidelity.Driver != "delay-aware-simulate" {
		t.Fatalf("mixed-delay run = %#v", snapshot.LastRun)
	}
	for sample, currentTime := range snapshot.LastRun.Times {
		shifted := currentTime - 0.5
		var want float64
		if shifted >= 0 {
			want = 1 - math.Exp(-shifted)
		}
		if got := snapshot.LastRun.Series[0].Values[sample]; math.Abs(got-want) > 1e-10 {
			t.Fatalf("t=%g = %g, combined-delay oracle %g", currentTime, got, want)
		}
	}
}

func TestR2026aDelayedFeedbackRunsWithInternalDelay(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	requireR2026aCompatibilityCase(t, fixture, "delayed-feedback")
	studio, ctx, flowID := newCompatibilityFlow(t, "delayed-feedback")

	_, sourceID, err := studio.AddBlock(ctx, flowID, BlockSource, Point{X: 100, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, sumID, err := studio.AddBlock(ctx, flowID, BlockSum, Point{X: 300, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, gainID, err := studio.AddBlock(ctx, flowID, BlockGain, Point{X: 500, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, plantID, err := studio.AddBlock(ctx, flowID, BlockTransfer, Point{X: 700, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, delayID, err := studio.AddBlock(ctx, flowID, BlockDelay, Point{X: 700, Y: 300})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := studio.AddBlock(ctx, flowID, BlockScope, Point{X: 900, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, sumID, BlockUpdate{
		Name: "Error", Parameters: map[string]string{"signs": "+-"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.UpdateBlock(ctx, gainID, BlockUpdate{
		Name: "Controller", Parameters: map[string]string{"gain": "2"},
	}); err != nil {
		t.Fatal(err)
	}
	updateExactTransportDelay(t, studio, ctx, delayID, "Feedback delay", 0.2)
	for _, wire := range []Wire{
		{SourceID: sourceID, TargetID: sumID, TargetPort: 0},
		{SourceID: sumID, TargetID: gainID},
		{SourceID: gainID, TargetID: plantID},
		{SourceID: plantID, TargetID: delayID},
		{SourceID: delayID, TargetID: sumID, TargetPort: 1},
		{SourceID: plantID, TargetID: scopeID},
	} {
		if _, err := studio.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := studio.Run(ctx, flowID, SimulationRequest{Duration: 1, SampleTime: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil || snapshot.LastRun.Fidelity.Driver != "delay-aware-simulate" {
		t.Fatalf("delayed-feedback run = %#v", snapshot.LastRun)
	}
	for sample := 0; sample <= 2; sample++ {
		currentTime := snapshot.LastRun.Times[sample]
		want := 2 * (1 - math.Exp(-currentTime))
		if got := snapshot.LastRun.Series[0].Values[sample]; math.Abs(got-want) > 1e-10 {
			t.Fatalf("pre-feedback t=%g = %g, open-loop oracle %g", currentTime, got, want)
		}
	}
}

func TestR2026aMIMODelayFrequencyResponseKeepsPathPhase(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	requireR2026aCompatibilityCase(t, fixture, "mimo-delay-frequency-response")
	flow := createMIMOCompatibilityFlow(t, fixture, modelDomainContinuous)
	const omega = 1.0
	result, err := flow.studio.AnalyzeFrequency(flow.ctx, flow.flowID, FrequencyAnalysisRequest{
		Inputs: []ChannelRef{
			{BlockID: flow.sourceID, Channel: 0},
			{BlockID: flow.sourceID, Channel: 1},
		},
		Outputs: []ChannelRef{
			{BlockID: flow.transferID, Channel: 0},
			{BlockID: flow.transferID, Channel: 1},
		},
		Omega: []float64{omega, 2 * omega},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "mimo" || len(result.Bode) != 4 {
		t.Fatalf("MIMO frequency result = %#v", result)
	}
	for _, trace := range result.Bode {
		output, input := trace.OutputIndex, trace.InputIndex
		rate := fixture.Model.Denominators[output][1]
		gain := fixture.Model.Numerators[output][input][0]
		delay := fixture.Model.Delays[output][input]
		want := complex(gain, 0) /
			complex(rate, omega) *
			cmplx.Exp(complex(0, -omega*delay))
		wantMagnitude := 20 * math.Log10(cmplx.Abs(want))
		wantPhase := cmplx.Phase(want) * 180 / math.Pi
		if got := *trace.MagnitudeDB[0]; math.Abs(got-wantMagnitude) > 1e-10 {
			t.Fatalf("magnitude[%d,%d] = %g, oracle %g", output, input, got, wantMagnitude)
		}
		if got := *trace.PhaseDegrees[0]; math.Abs(got-wantPhase) > 1e-10 {
			t.Fatalf("phase[%d,%d] = %g, oracle %g", output, input, got, wantPhase)
		}
	}
}

func TestR2026aDelayFreeMIMOTransferKeepsBatchFastPath(t *testing.T) {
	fixture := loadR2026aMIMODelayFixture(t)
	requireR2026aCompatibilityCase(t, fixture, "delay-free-fast-path")
	fixture.Model.Delays = [][]float64{{0, 0}, {0, 0}}
	flow := createMIMOCompatibilityFlow(t, fixture, modelDomainContinuous)
	snapshot, err := flow.studio.Run(flow.ctx, flow.flowID, SimulationRequest{
		Duration: fixture.Simulation.Duration, SampleTime: fixture.Simulation.SampleTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil || snapshot.LastRun.Fidelity.Driver != "batch-lsim" {
		t.Fatalf("delay-free run driver = %#v", snapshot.LastRun)
	}
	assertMIMOFirstOrderDelayFixture(t, fixture, snapshot.LastRun)
}

func newCompatibilityFlow(t *testing.T, name string) (*Studio, context.Context, int64) {
	t.Helper()
	ctx := context.Background()
	studio := openTestStudio(t, filepath.Join(t.TempDir(), name+".db"))
	seeded, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := studio.CreateFlow(ctx, seeded.Flow.ProjectID, name)
	if err != nil {
		t.Fatal(err)
	}
	return studio, ctx, created.Snapshot.Flow.ID
}

func updateExactTransportDelay(
	t *testing.T,
	studio *Studio,
	ctx context.Context,
	blockID int64,
	name string,
	delay float64,
) {
	t.Helper()
	if _, err := studio.UpdateBlock(ctx, blockID, BlockUpdate{
		Name: name,
		Parameters: map[string]string{
			"delay":            strconv.FormatFloat(delay, 'g', -1, 64),
			"delay_mode":       delayModeExact,
			"approximation":    "3",
			"sample_time_mode": string(sampleTimeExplicit),
			"sample_time":      "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func resultSeriesByChannel(t *testing.T, run *Simulation) map[string]Series {
	t.Helper()
	series := make(map[string]Series, len(run.Series))
	for _, values := range run.Series {
		series[values.ChannelName] = values
	}
	return series
}
