package studio

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gonum.org/v1/gonum/mat"
)

func TestTuneControllerFindsBoundaryCandidateWithoutMutatingThenAppliesAtomically(t *testing.T) {
	ctx := context.Background()
	service, flowID, plantID, controllerID := tuningStudio(t)
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	controllerBefore := findBlock(t, before.Blocks, controllerID)

	candidate, err := service.TuneController(ctx, flowID, ControllerTuningRequest{
		Algorithm: TuningGrid,
		Parameters: []TunableParameterSpec{{
			Ref: TunableParameterRef{
				BlockID: controllerID, Field: TunableGain,
			},
			Lower: 0.5, Upper: 2,
		}},
		Goals: []TuningGoalRequest{{
			Name: "track", Kind: TuningGoalTracking, Maximum: 0.34,
			AnalysisPoint: "actuator",
		}},
		GridPoints: 4, MaxEvaluations: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.Pass || candidate.Iterations != 4 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if len(candidate.Values) != 1 ||
		candidate.Values[0].Previous != 1 ||
		candidate.Values[0].Value != 2 {
		t.Fatalf("tuned values = %#v", candidate.Values)
	}
	if len(candidate.Goals) != 1 || !candidate.Goals[0].Pass {
		t.Fatalf("goal evidence = %#v", candidate.Goals)
	}
	unmodified, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if gain := findBlock(t, unmodified.Blocks, controllerID).Parameters.Gain; gain != 1 {
		t.Fatalf("candidate generation changed gain to %g", gain)
	}
	if unmodified.Flow.ModelUpdatedAt != before.Flow.ModelUpdatedAt {
		t.Fatal("candidate generation changed model revision")
	}

	applied, err := service.ApplyTuningCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if gain := findBlock(t, applied.Blocks, controllerID).Parameters.Gain; gain != 2 {
		t.Fatalf("applied gain = %g, want 2", gain)
	}
	if applied.Flow.ModelUpdatedAt == before.Flow.ModelUpdatedAt {
		t.Fatal("candidate application did not change model revision")
	}
	if _, err := service.ApplyTuningCandidate(ctx, candidate); err == nil ||
		!strings.Contains(err.Error(), "stale") {
		t.Fatalf("second apply error = %v", err)
	}
	if controllerBefore.Parameters.Gain != 1 {
		t.Fatal("test fixture controller changed through shared parameter ownership")
	}
	_ = plantID
}

func TestTuneControllerReturnsConflictingGoalEvidenceAndTruthfulMethodWarning(t *testing.T) {
	service, flowID, _, controllerID := tuningStudio(t)
	candidate, err := service.TuneController(
		context.Background(),
		flowID,
		ControllerTuningRequest{
			Algorithm: TuningSystune,
			Parameters: []TunableParameterSpec{{
				Ref: TunableParameterRef{
					BlockID: controllerID, Field: TunableGain,
				},
				Lower: 0.5, Upper: 2,
			}},
			Goals: []TuningGoalRequest{
				{
					Name: "impossible tracking", Kind: TuningGoalTracking,
					Maximum: 0.05, AnalysisPoint: "actuator",
				},
				{
					Name: "small loop", Kind: TuningGoalLoopShape,
					Minimum: 0, Maximum: 0.55,
					AnalysisPoint: "actuator", Omega: []float64{0.01},
				},
			},
			GridPoints: 4, MaxEvaluations: 100,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Pass {
		t.Fatalf("conflicting goals unexpectedly passed: %#v", candidate.Goals)
	}
	if candidate.SearchMethod != "cartesian-grid" ||
		len(candidate.Warnings) != 1 ||
		!strings.Contains(candidate.Warnings[0], "not a continuous optimizer") {
		t.Fatalf(
			"method/warnings = %q %#v",
			candidate.SearchMethod, candidate.Warnings,
		)
	}
	failed := 0
	for _, goal := range candidate.Goals {
		if !goal.Pass {
			failed++
			if goal.Message == "" || goal.Violation <= 0 {
				t.Fatalf("failed goal lacks evidence: %#v", goal)
			}
		}
	}
	if failed == 0 {
		t.Fatalf("goal evidence = %#v", candidate.Goals)
	}
}

func TestTunableMatrixGainKeepsOneBoundAuthorityAndMIMODimensions(t *testing.T) {
	matrix, err := NewMatrixValue(2, 2, []float64{
		1, 0,
		0, 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	inputs, _ := NewChannelNames([]string{"y1", "y2"})
	outputs, _ := NewChannelNames([]string{"u1", "u2"})
	block := Block{
		ID: 42, Kind: BlockMatrixGain, Name: "MIMO controller",
		Parameters: Parameters{
			D: &matrix, InputNames: &inputs, OutputNames: &outputs,
		},
	}
	ref := TunableParameterRef{
		BlockID: 42, Field: TunableMatrixGain, Row: 0, Column: 0,
	}
	tunable, authorities, err := tunableControllerBlock(
		block,
		[]TunableParameterSpec{{Ref: ref, Lower: 0.5, Upper: 3}},
		0.1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(tunable.FreeParameters()) != 1 {
		t.Fatalf("free parameters = %d, want 1", len(tunable.FreeParameters()))
	}
	authority := authorities[parameterKey(ref)]
	if authority.current != 1 || authority.lower != 0.5 || authority.upper != 3 {
		t.Fatalf("authority = %#v", authority)
	}
	sampled, err := tunable.SampleBlock(map[string]float64{
		parameterKey(ref): 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	system, err := sampled.CurrentSystem()
	if err != nil {
		t.Fatal(err)
	}
	if _, inputsN, outputsN := system.Dims(); inputsN != 2 || outputsN != 2 {
		t.Fatalf("sampled dimensions = %d×%d", outputsN, inputsN)
	}
	if system.Dt != 0.1 {
		t.Fatalf("sampled controller Dt = %g, want 0.1", system.Dt)
	}
	want := mat.NewDense(2, 2, []float64{
		3, 0,
		0, 2,
	})
	if !mat.EqualApprox(system.D, want, 1e-14) {
		t.Fatalf(
			"sampled matrix =\n%v\nwant\n%v",
			mat.Formatted(system.D), mat.Formatted(want),
		)
	}
}

func TestTunableTransferAndStateSpaceMapBackToAuthoredParameters(t *testing.T) {
	transfer := Block{
		ID: 51, Kind: BlockTransfer, Name: "Lead controller",
		Parameters: Parameters{
			Numerator: []float64{1, 2}, Denominator: []float64{1, 3},
		},
	}
	transferRef := TunableParameterRef{
		BlockID: 51, Field: TunableNumerator, Coefficient: 1,
	}
	tunableTransfer, _, err := tunableControllerBlock(
		transfer,
		[]TunableParameterSpec{{Ref: transferRef, Lower: 1, Upper: 4}},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	sampledTransfer, err := tunableTransfer.SampleBlock(map[string]float64{
		parameterKey(transferRef): 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	transferSystem, err := sampledTransfer.CurrentSystem()
	if err != nil {
		t.Fatal(err)
	}
	dc, err := transferSystem.DCGain()
	if err != nil {
		t.Fatal(err)
	}
	if got := dc.At(0, 0); got != 4.0/3.0 {
		t.Fatalf("sampled transfer DC gain = %g, want 4/3", got)
	}
	transferParameters := cloneParameters(transfer.Parameters)
	if err := setTunedParameter(&transferParameters, transferRef, 4); err != nil {
		t.Fatal(err)
	}
	if transferParameters.Numerator[1] != 4 ||
		transfer.Parameters.Numerator[1] != 2 {
		t.Fatalf(
			"authored transfer mapping = %v; original = %v",
			transferParameters.Numerator, transfer.Parameters.Numerator,
		)
	}

	a, _ := NewMatrixValue(1, 1, []float64{-1})
	b, _ := NewMatrixValue(1, 1, []float64{1})
	c, _ := NewMatrixValue(1, 1, []float64{1})
	d, _ := NewMatrixValue(1, 1, []float64{0})
	stateSpace := Block{
		ID: 52, Kind: BlockStateSpace, Name: "State controller",
		Parameters: Parameters{A: &a, B: &b, C: &c, D: &d},
	}
	stateRef := TunableParameterRef{
		BlockID: 52, Field: TunableStateA,
	}
	tunableState, _, err := tunableControllerBlock(
		stateSpace,
		[]TunableParameterSpec{{Ref: stateRef, Lower: -3, Upper: -0.5}},
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	sampledState, err := tunableState.SampleBlock(map[string]float64{
		parameterKey(stateRef): -2,
	})
	if err != nil {
		t.Fatal(err)
	}
	stateSystem, err := sampledState.CurrentSystem()
	if err != nil {
		t.Fatal(err)
	}
	if stateSystem.A.At(0, 0) != -2 {
		t.Fatalf("sampled state A = %g", stateSystem.A.At(0, 0))
	}
	stateParameters := cloneParameters(stateSpace.Parameters)
	if err := setTunedParameter(&stateParameters, stateRef, -2); err != nil {
		t.Fatal(err)
	}
	if stateParameters.A.At(0, 0) != -2 || stateSpace.Parameters.A.At(0, 0) != -1 {
		t.Fatal("state-space candidate did not preserve authored parameter ownership")
	}
}

func TestTuningGoalsMapEveryControlsysGoalFamily(t *testing.T) {
	requests := []TuningGoalRequest{
		{Name: "tracking", Kind: TuningGoalTracking, Maximum: 1},
		{
			Name: "rejection", Kind: TuningGoalRejection, Maximum: 1,
			Omega: []float64{0.1, 1},
		},
		{
			Name: "sensitivity", Kind: TuningGoalSensitivity, Maximum: 1,
			Omega: []float64{0.1, 1},
		},
		{
			Name: "weighted", Kind: TuningGoalWeightedGain, Maximum: 1,
			Omega: []float64{0.1, 1},
		},
		{
			Name: "shape", Kind: TuningGoalLoopShape, Minimum: 0.1, Maximum: 2,
			Omega: []float64{0.1, 1},
		},
		{Name: "margin", Kind: TuningGoalMargin, Minimum: 3, Maximum: 30},
		{Name: "pole", Kind: TuningGoalPole, Maximum: -0.1},
		{Name: "overshoot", Kind: TuningGoalOvershoot, Maximum: 10},
	}
	for i := range requests {
		requests[i].AnalysisPoint = "actuator"
	}
	goals, kinds, err := tuningGoals(
		requests,
		[]AnalysisPointRole{{
			Name: "actuator", Location: AnalysisPointPlantInput,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != len(requests) || len(kinds) != len(requests) {
		t.Fatalf("goals = %d, kinds = %d", len(goals), len(kinds))
	}
	for _, request := range requests {
		if kinds[request.Name] != request.Kind {
			t.Fatalf("goal %q kind = %q", request.Name, kinds[request.Name])
		}
	}
}

func TestTuningRequestRejectsUnboundedAndOversizedSearches(t *testing.T) {
	base := ControllerTuningRequest{
		Algorithm: TuningGrid,
		Parameters: []TunableParameterSpec{{
			Ref:   TunableParameterRef{BlockID: 1, Field: TunableGain},
			Lower: 1, Upper: 1,
		}},
		Goals:          []TuningGoalRequest{{Name: "track", Kind: TuningGoalTracking}},
		GridPoints:     5,
		MaxEvaluations: 100,
	}
	if err := validateControllerTuningRequest(base); err == nil ||
		!strings.Contains(err.Error(), "lower < upper") {
		t.Fatalf("equal bounds error = %v", err)
	}
	base.Parameters[0].Lower = 0
	base.Parameters[0].Upper = 1
	base.Parameters = append(base.Parameters, TunableParameterSpec{
		Ref:   TunableParameterRef{BlockID: 1, Field: TunableIntegral},
		Lower: 0, Upper: 1,
	}, TunableParameterSpec{
		Ref:   TunableParameterRef{BlockID: 1, Field: TunableDerivative},
		Lower: 0, Upper: 1,
	})
	base.GridPoints = 5
	base.MaxEvaluations = 100
	if err := validateControllerTuningRequest(base); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized grid error = %v", err)
	}
}

func tuningStudio(t *testing.T) (*Studio, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "tuning.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "Tuning")
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	_, plantID, err := service.AddBlock(
		ctx, flowID, BlockLag, Point{X: 100, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, controllerID, err := service.AddBlock(
		ctx, flowID, BlockGain, Point{X: 400, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: controllerID, TargetID: plantID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: plantID, TargetID: controllerID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignControlRoles(
		ctx, flowID, sisoRoleSpec(plantID, controllerID),
	); err != nil {
		t.Fatal(err)
	}
	return service, flowID, plantID, controllerID
}
