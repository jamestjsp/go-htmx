package studio

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSampleTimeSpecResolvesExplicitAndInheritedValues(t *testing.T) {
	tests := []struct {
		name     string
		spec     sampleTimeSpec
		baseStep float64
		want     float64
		wantErr  string
	}{
		{
			name: "explicit",
			spec: sampleTimeSpec{mode: sampleTimeExplicit, seconds: 0.2},
			want: 0.2,
		},
		{
			name:     "inherited",
			spec:     sampleTimeSpec{mode: sampleTimeInherited},
			baseStep: 0.05,
			want:     0.05,
		},
		{
			name:    "zero explicit",
			spec:    sampleTimeSpec{mode: sampleTimeExplicit},
			wantErr: "positive finite",
		},
		{
			name: "negative explicit",
			spec: sampleTimeSpec{
				mode: sampleTimeExplicit, seconds: -0.1,
			},
			wantErr: "positive finite",
		},
		{
			name:    "inherited without base",
			spec:    sampleTimeSpec{mode: sampleTimeInherited},
			wantErr: "requires a positive run sample time",
		},
		{
			name:    "unknown mode",
			spec:    sampleTimeSpec{mode: "automatic", seconds: 0.1},
			wantErr: "explicit or inherited",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.spec.resolve(test.baseStep)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want fragment %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("sample time = %g, want %g", got, test.want)
			}
		})
	}
}

func TestSampleTimeCompatibilityRecognizesOnlyIntegerRateRelations(t *testing.T) {
	tests := []struct {
		name     string
		left     float64
		right    float64
		relation sampleTimeRelation
		ratio    int
	}{
		{"equal", 0.1, 0.1, sampleTimesEqual, 1},
		{"integer multiple", 0.05, 0.2, sampleTimesIntegerMultiple, 4},
		{"reverse integer multiple", 0.3, 0.1, sampleTimesIntegerMultiple, 3},
		{"fractional", 0.1, 0.15, sampleTimesIncompatible, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := compareSampleTimes(test.left, test.right)
			if got.relation != test.relation || got.ratio != test.ratio {
				t.Fatalf("compatibility = %#v, want relation %d ratio %d", got, test.relation, test.ratio)
			}
		})
	}
}

func TestSampleScheduleUsesIntegerMultipleAndHoldPolicy(t *testing.T) {
	schedule, err := scheduleSampleTime(0.3, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.updateEvery != 3 {
		t.Fatalf("update period = %d, want 3", schedule.updateEvery)
	}
	wantUpdates := map[int]bool{0: true, 1: false, 2: false, 3: true, 4: false, 5: false, 6: true}
	for sample, want := range wantUpdates {
		if got := schedule.updatesAt(sample); got != want {
			t.Fatalf("updatesAt(%d) = %v, want %v", sample, got, want)
		}
	}

	_, err = scheduleSampleTime(0.15, 0.1)
	if err == nil ||
		!strings.Contains(err.Error(), "use 0.1 s or 0.2 s") {
		t.Fatalf("fractional schedule error = %v", err)
	}
	_, err = scheduleSampleTime(0.05, 0.1)
	if err == nil ||
		!strings.Contains(err.Error(), "use 0.1 s or 0.1 s") {
		t.Fatalf("faster-than-base error = %v", err)
	}
}

func TestInheritedThiranUsesRunSampleTimeWithoutChangingProvenance(t *testing.T) {
	blocks := []Block{
		{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockDelay, Name: "Delay", Parameters: Parameters{
			Delay: 0.35, DelayMode: delayModeThiran, Approximation: 3,
			SampleTimeMode: string(sampleTimeInherited),
		}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
	}
	model, err := compileRequestedModel(blocks, connections, modelCompileRequest{
		includeSinks: true,
		baseStep:     0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if domain := model.timeDomain(); domain.Domain != timeDomainDiscrete || domain.SampleTime != 0.1 {
		t.Fatalf("compiled domain = %#v, want discrete 0.1", domain)
	}
	provenance := model.modelProvenance()
	if got := provenance.Blocks[1].Parameters.SampleTimeMode; got != string(sampleTimeInherited) {
		t.Fatalf("provenance sample time mode = %q, want inherited", got)
	}

	if _, err := compileModel(blocks, connections); err == nil ||
		!strings.Contains(err.Error(), "inherited sample time requires a positive run sample time") {
		t.Fatalf("analysis compile error = %v, want missing base-step guidance", err)
	}
}

func TestCompileReportsMixedDomainAndRateContracts(t *testing.T) {
	t.Run("continuous and discrete", func(t *testing.T) {
		_, err := compileModel([]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockLag, Name: "Continuous", Parameters: Parameters{TimeConstant: 1}},
			{ID: 3, Kind: BlockDelay, Name: "Discrete", Parameters: Parameters{
				Delay: 0.35, DelayMode: delayModeThiran, Approximation: 3,
				SampleTime: 0.1, SampleTimeMode: string(sampleTimeExplicit),
			}},
			{ID: 4, Kind: BlockScope, Name: "Output"},
		}, []Connection{
			{ID: 1, SourceID: 1, TargetID: 2},
			{ID: 2, SourceID: 2, TargetID: 3},
			{ID: 3, SourceID: 3, TargetID: 4},
		})
		if err == nil || !strings.Contains(err.Error(), "mixes continuous dynamics with discrete dynamics") {
			t.Fatalf("error = %v, want mixed-domain boundary guidance", err)
		}
	})

	t.Run("integer-related rates", func(t *testing.T) {
		err := compileTwoRateSheet(0.1, 0.2)
		if err == nil || !strings.Contains(err.Error(), "integer ratio 2") ||
			!strings.Contains(err.Error(), "zero-order-hold") {
			t.Fatalf("error = %v, want integer-rate hold contract", err)
		}
	})

	t.Run("fractional rates", func(t *testing.T) {
		err := compileTwoRateSheet(0.1, 0.15)
		if err == nil || !strings.Contains(err.Error(), "are not integer multiples") {
			t.Fatalf("error = %v, want incompatible-rate contract", err)
		}
	})

	t.Run("explicit multiple of run step", func(t *testing.T) {
		_, err := compileRequestedModel([]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockDelay, Name: "Slow delay", Parameters: Parameters{
				Delay: 1, DelayMode: delayModeThiran, Approximation: 1,
				SampleTime: 0.2, SampleTimeMode: string(sampleTimeExplicit),
			}},
			{ID: 3, Kind: BlockScope, Name: "Output"},
		}, []Connection{
			{ID: 1, SourceID: 1, TargetID: 2},
			{ID: 2, SourceID: 2, TargetID: 3},
		}, modelCompileRequest{includeSinks: true, baseStep: 0.1})
		if err == nil || !strings.Contains(err.Error(), "updates every 2 run samples") ||
			!strings.Contains(err.Error(), "zero-order-hold") {
			t.Fatalf("error = %v, want scheduled hold contract", err)
		}
	})

	t.Run("fractional multiple of run step", func(t *testing.T) {
		_, err := compileRequestedModel([]Block{
			{ID: 1, Kind: BlockSource, Name: "Input", Parameters: Parameters{Amplitude: 1}},
			{ID: 2, Kind: BlockDelay, Name: "Fractional rate", Parameters: Parameters{
				Delay: 1, DelayMode: delayModeThiran, Approximation: 1,
				SampleTime: 0.15, SampleTimeMode: string(sampleTimeExplicit),
			}},
			{ID: 3, Kind: BlockScope, Name: "Output"},
		}, []Connection{
			{ID: 1, SourceID: 1, TargetID: 2},
			{ID: 2, SourceID: 2, TargetID: 3},
		}, modelCompileRequest{includeSinks: true, baseStep: 0.1})
		if err == nil || !strings.Contains(err.Error(), "use 0.1 s or 0.2 s") {
			t.Fatalf("error = %v, want nearest legal sample times", err)
		}
	})
}

func compileTwoRateSheet(first, second float64) error {
	_, err := compileModel([]Block{
		{ID: 1, Kind: BlockSource, Name: "Input A", Parameters: Parameters{Amplitude: 1}},
		{ID: 2, Kind: BlockSource, Name: "Input B", Parameters: Parameters{Amplitude: 1}},
		{ID: 3, Kind: BlockDelay, Name: "Rate A", Parameters: Parameters{
			Delay: 1, DelayMode: delayModeThiran, Approximation: 1,
			SampleTime: first, SampleTimeMode: string(sampleTimeExplicit),
		}},
		{ID: 4, Kind: BlockDelay, Name: "Rate B", Parameters: Parameters{
			Delay: 1, DelayMode: delayModeThiran, Approximation: 1,
			SampleTime: second, SampleTimeMode: string(sampleTimeExplicit),
		}},
		{ID: 5, Kind: BlockScope, Name: "Output A"},
		{ID: 6, Kind: BlockScope, Name: "Output B"},
	}, []Connection{
		{ID: 1, SourceID: 1, TargetID: 3},
		{ID: 2, SourceID: 2, TargetID: 4},
		{ID: 3, SourceID: 3, TargetID: 5},
		{ID: 4, SourceID: 4, TargetID: 6},
	})
	return err
}

func TestSampleTimeModeRoundTripsThroughSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sample-time.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := first.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, blockID, err := first.AddBlock(ctx, snapshot.Flow.ID, BlockDelay, Point{X: 1200, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpdateBlock(ctx, blockID, BlockUpdate{
		Name: "Inherited delay",
		Parameters: map[string]string{
			"delay":            "0.35",
			"delay_mode":       delayModeThiran,
			"approximation":    "3",
			"sample_time_mode": string(sampleTimeInherited),
			"sample_time":      "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	reread, err := second.Snapshot(ctx, snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range reread.Blocks {
		if block.ID == blockID {
			if block.Parameters.SampleTimeMode != string(sampleTimeInherited) ||
				block.Parameters.SampleTime != 0.1 {
				t.Fatalf("stored sample-time parameters = %#v", block.Parameters)
			}
			return
		}
	}
	t.Fatal("reopened flow did not contain the delay block")
}
