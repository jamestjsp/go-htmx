package studio

import (
	"context"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
)

func TestPIDCandidateReviewComparesCommonGridsWithoutMutation(t *testing.T) {
	service, flowID, _, _ := pidDesignStudio(t, BlockLag, BlockPID)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
		Type: controlsys.PidtunePI, CrossoverFrequency: 1,
		PhaseMargin: 60, StepHorizon: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewPIDDesignCandidate(ctx, candidate, 8)
	if err != nil {
		t.Fatal(err)
	}
	if review.Kind != "pid" || review.Algorithm != "PI" ||
		!review.ApplyAvailable || review.Robustness.Candidate == nil {
		t.Fatalf("PID review = %#v", review)
	}
	if len(review.Robustness.Grid.Omega) < 2 ||
		len(review.Time.Times) < 2 || len(review.Time.Traces) != 1 {
		t.Fatalf(
			"PID comparison grids = frequency %d, time %d, traces %d",
			len(review.Robustness.Grid.Omega),
			len(review.Time.Times),
			len(review.Time.Traces),
		)
	}
	trace := review.Time.Traces[0]
	if len(trace.CurrentValues) != len(review.Time.Times) ||
		len(trace.CandidateValues) != len(review.Time.Times) {
		t.Fatalf("PID time trace does not use the shared grid: %#v", trace)
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) {
		t.Fatal("candidate review mutated the model")
	}
}

func TestStateCandidateReviewNormalizesSignedControlLaw(t *testing.T) {
	service, flowID, _, _ := stateDesignStudio(
		t, modelDomainContinuous, BlockMatrixGain,
	)
	q := testMatrix(t, 2, 2, []float64{2, 0, 0, 1})
	r := testMatrix(t, 1, 1, []float64{0.5})
	candidate, err := service.DesignStateFeedback(
		context.Background(),
		flowID,
		StateFeedbackRequest{Method: StateFeedbackLQR, Q: &q, R: &r},
	)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewStateDesignCandidate(
		context.Background(), candidate, 6,
	)
	if err != nil {
		t.Fatal(err)
	}
	if review.Kind != "state-space" || review.Algorithm != "lqr" ||
		review.Robustness.Candidate == nil || len(review.Time.Traces) != 4 {
		t.Fatalf(
			"state-design review kind=%q algorithm=%q candidate=%t traces=%d",
			review.Kind,
			review.Algorithm,
			review.Robustness.Candidate != nil,
			len(review.Time.Traces),
		)
	}
	if review.SourceControlRoles.Fingerprint == "" ||
		len(review.SourceControlRoles.Spec.Controller.MeasurementInputs) != 2 {
		t.Fatalf("state-design source roles = %#v", review.SourceControlRoles)
	}
}

func TestPID2ReviewUsesReferenceLoopEvidence(t *testing.T) {
	service, flowID, _, _ := pidDesignStudio(t, BlockLag, BlockPID2)
	setpointWeight := 0.35
	derivativeWeight := 0.1
	candidate, err := service.DesignPIDController(
		context.Background(),
		flowID,
		PIDDesignRequest{
			Type: controlsys.PidtunePIDF, CrossoverFrequency: 1,
			PhaseMargin: 60, StepHorizon: 5,
			SetpointWeight:   &setpointWeight,
			DerivativeWeight: &derivativeWeight,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	review, err := service.ReviewPIDDesignCandidate(
		context.Background(), candidate, 5,
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Step == nil || len(review.Time.Traces) != 1 {
		t.Fatalf("PID2 reference evidence = %#v", review.Time)
	}
	trace := review.Time.Traces[0]
	if trace.InputName != "reference" ||
		len(trace.CandidateValues) != len(candidate.Step.CandidateValues) {
		t.Fatalf("PID2 review trace = %#v", trace)
	}
	for i := range trace.CandidateValues {
		if trace.CandidateValues[i] != candidate.Step.CandidateValues[i] {
			t.Fatalf(
				"PID2 candidate sample %d = %g, want reference-loop %g",
				i, trace.CandidateValues[i], candidate.Step.CandidateValues[i],
			)
		}
	}
}

func TestCandidateReviewRejectsChangedNamedRoles(t *testing.T) {
	service, flowID, plantID, controllerID := pidDesignStudio(
		t, BlockLag, BlockPID,
	)
	ctx := context.Background()
	candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
		Type: controlsys.PidtunePI, CrossoverFrequency: 1, PhaseMargin: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := sisoRoleSpec(plantID, controllerID)
	spec.AnalysisPoints[0].Name = "renamed actuator break"
	if _, err := service.AssignControlRoles(ctx, flowID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReviewPIDDesignCandidate(ctx, candidate, 4); err == nil ||
		!strings.Contains(err.Error(), "control roles changed") ||
		!strings.Contains(err.Error(), "refresh") {
		t.Fatalf("stale-role review error = %v", err)
	}
}

func TestControllerCandidateApplyReturnsRevisionCheckedUndo(t *testing.T) {
	service, flowID, _, controllerID := pidDesignStudio(
		t, BlockLag, BlockPID,
	)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
		Type: controlsys.PidtunePI, CrossoverFrequency: 2, PhaseMargin: 55,
	})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.ApplyPIDDesignCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Undo.SourceModelRevision.IsZero() ||
		!applied.Undo.SourceModelRevision.Equal(
			applied.Snapshot.Flow.ModelUpdatedAt,
		) ||
		applied.Undo.edit == nil ||
		applied.Snapshot.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) {
		t.Fatalf("apply result is missing undo authority: %#v", applied.Undo)
	}
	restored, err := service.UndoControllerCandidate(ctx, applied.Undo)
	if err != nil {
		t.Fatal(err)
	}
	beforeController := findBlock(t, before.Blocks, controllerID)
	restoredController := findBlock(t, restored.Blocks, controllerID)
	if restoredController.Parameters.Proportional !=
		beforeController.Parameters.Proportional ||
		restoredController.Parameters.Integral !=
			beforeController.Parameters.Integral {
		t.Fatalf(
			"undo restored Kp=%g Ki=%g, want Kp=%g Ki=%g",
			restoredController.Parameters.Proportional,
			restoredController.Parameters.Integral,
			beforeController.Parameters.Proportional,
			beforeController.Parameters.Integral,
		)
	}
	if _, err := service.UndoControllerCandidate(ctx, applied.Undo); err == nil ||
		!strings.Contains(err.Error(), "stale") {
		t.Fatalf("second undo error = %v", err)
	}
}
