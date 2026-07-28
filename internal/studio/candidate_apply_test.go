package studio

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
)

func TestControlRoleSnapshotFingerprintNormalizesEquivalentSpecifications(t *testing.T) {
	left := sisoRoleSpec(1, 2)
	left.Plant.Blocks = []int64{7, 1}
	left.Controller.Blocks = []int64{9, 2}
	left.AnalysisPoints[0].Name = " actuator "
	left.Plant.ExogenousInputs = []NamedChannelRef{}

	right := cloneControlRoleSpec(left)
	right.Plant.Blocks = []int64{1, 7}
	right.Controller.Blocks = []int64{2, 9}
	right.AnalysisPoints[0].Name = "actuator"
	right.Plant.ExogenousInputs = nil
	right.Controller.FeedbackConvention = FeedbackExternalNegative

	leftSnapshot := newControlRoleSnapshot(left)
	rightSnapshot := newControlRoleSnapshot(right)
	if leftSnapshot.Fingerprint != rightSnapshot.Fingerprint {
		t.Fatalf(
			"equivalent role fingerprints differ: %q != %q",
			leftSnapshot.Fingerprint, rightSnapshot.Fingerprint,
		)
	}
	if !leftSnapshot.valid() || !rightSnapshot.valid() {
		t.Fatal("normalized control role snapshot does not validate itself")
	}

	changed := cloneControlRoleSpec(right)
	changed.AnalysisPoints[0].Name = "different break"
	if newControlRoleSnapshot(changed).Fingerprint == rightSnapshot.Fingerprint {
		t.Fatal("semantically changed roles retained the old fingerprint")
	}
}

func TestControllerUndoCandidateCloneOwnsNestedState(t *testing.T) {
	parameters := defaultParameters(BlockTransfer)
	parameters.Numerator = []float64{1, 2}
	roles := newControlRoleSnapshot(sisoRoleSpec(1, 2))
	original := ControllerUndoCandidate{
		FlowID:             7,
		SourceControlRoles: roles,
		edit: &candidateBlockEdit{
			blockID: 3, expectedKind: BlockTransfer, parameters: parameters,
		},
	}

	cloned := original.Clone()
	original.SourceControlRoles.Spec.Plant.Blocks[0] = 99
	original.edit.parameters.Numerator[0] = 99

	if cloned.SourceControlRoles.Spec.Plant.Blocks[0] == 99 {
		t.Fatal("cloned undo aliases control-role slices")
	}
	if cloned.edit.parameters.Numerator[0] == 99 {
		t.Fatal("cloned undo aliases parameter slices")
	}
}

func TestTuningCandidateRejectsChangedControlRolesAtSameModelRevision(t *testing.T) {
	service, flowID, _, controllerID := tuningStudio(t)
	ctx := context.Background()
	candidate, err := service.TuneController(ctx, flowID, ControllerTuningRequest{
		Algorithm: TuningGrid,
		Parameters: []TunableParameterSpec{{
			Ref:   TunableParameterRef{BlockID: controllerID, Field: TunableGain},
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
	assertCandidateRoleSnapshot(t, candidate.SourceControlRoles)
	before := snapshotAndChangeControlRoles(t, service, flowID)

	if _, err := service.ApplyTuningCandidate(ctx, candidate); err == nil ||
		!strings.Contains(err.Error(), "control roles changed") ||
		!strings.Contains(err.Error(), "refresh") {
		t.Fatalf("changed-role tuning apply error = %v", err)
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		findBlock(t, after.Blocks, controllerID).Parameters,
		findBlock(t, before.Blocks, controllerID).Parameters,
	) {
		t.Fatal("rejected tuning candidate partially changed the controller")
	}
}

func TestPIDCandidateRejectsChangedControlRolesAtSameModelRevision(t *testing.T) {
	service, flowID, _, controllerID := pidDesignStudio(t, BlockLag, BlockPID)
	ctx := context.Background()
	candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
		Type: controlsys.PidtunePIDF, CrossoverFrequency: 1, PhaseMargin: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateRoleSnapshot(t, candidate.SourceControlRoles)
	before := snapshotAndChangeControlRoles(t, service, flowID)

	if _, err := service.ApplyPIDDesignCandidate(ctx, candidate); err == nil ||
		!strings.Contains(err.Error(), "control roles changed") ||
		!strings.Contains(err.Error(), "refresh") {
		t.Fatalf("changed-role PID apply error = %v", err)
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		findBlock(t, after.Blocks, controllerID).Parameters,
		findBlock(t, before.Blocks, controllerID).Parameters,
	) {
		t.Fatal("rejected PID candidate partially changed the controller")
	}
}

func TestStateCandidateRejectsChangedControlRolesAtSameModelRevision(t *testing.T) {
	service, flowID, _, controllerID := stateDesignStudio(
		t, modelDomainContinuous, BlockMatrixGain,
	)
	ctx := context.Background()
	q := testMatrix(t, 2, 2, []float64{2, 0, 0, 1})
	r := testMatrix(t, 1, 1, []float64{0.5})
	candidate, err := service.DesignStateFeedback(ctx, flowID, StateFeedbackRequest{
		Method: StateFeedbackLQR, Q: &q, R: &r,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateRoleSnapshot(t, candidate.SourceControlRoles)
	before := snapshotAndChangeControlRoles(t, service, flowID)

	if _, err := service.ApplyStateDesignCandidate(ctx, candidate); err == nil ||
		!strings.Contains(err.Error(), "control roles changed") ||
		!strings.Contains(err.Error(), "refresh") {
		t.Fatalf("changed-role state apply error = %v", err)
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		findBlock(t, after.Blocks, controllerID).Parameters,
		findBlock(t, before.Blocks, controllerID).Parameters,
	) {
		t.Fatal("rejected state candidate partially changed the controller")
	}
}

func assertCandidateRoleSnapshot(t *testing.T, snapshot ControlRoleSnapshot) {
	t.Helper()
	if !snapshot.valid() || snapshot.Fingerprint == "" ||
		len(snapshot.Spec.AnalysisPoints) == 0 {
		t.Fatalf("candidate control role snapshot = %#v", snapshot)
	}
}

func snapshotAndChangeControlRoles(
	t *testing.T,
	service *Studio,
	flowID int64,
) Snapshot {
	t.Helper()
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.ControlRoles(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	spec.AnalysisPoints[0].Name += " changed"
	if _, err := service.AssignControlRoles(ctx, flowID, spec); err != nil {
		t.Fatal(err)
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) {
		t.Fatal("changing control roles unexpectedly changed the model revision")
	}
	return before
}
