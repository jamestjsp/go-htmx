package studio

import (
	"context"
	"encoding/json"
	"math"
	"math/cmplx"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jamestjsp/controlsys"
)

func TestDesignPIDControllerSupportsEveryControlsysPIDTypeWithoutMutation(t *testing.T) {
	service, flowID, _, controllerID := pidDesignStudio(t, BlockLag, BlockPID)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, pidType := range []controlsys.PidtuneType{
		controlsys.PidtuneP,
		controlsys.PidtuneI,
		controlsys.PidtunePI,
		controlsys.PidtunePD,
		controlsys.PidtunePID,
		controlsys.PidtunePIDF,
	} {
		t.Run(string(pidType), func(t *testing.T) {
			candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
				Type: pidType, CrossoverFrequency: 1, PhaseMargin: 60,
				StepHorizon: 8,
			})
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Controller == nil || candidate.ClosedLoop == nil ||
				candidate.CandidateMargin == nil ||
				len(candidate.Frequency.Omega) != 160 {
				t.Fatalf("incomplete candidate = %#v", candidate)
			}
			if candidate.Gains.SampleTime != 0 {
				t.Fatalf("continuous PID Dt = %g", candidate.Gains.SampleTime)
			}
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatalf("candidate is not JSON-safe: %v", err)
			}
			if candidate.Gains.FilterCoefficient <= 0 ||
				!strings.Contains(string(encoded), `"filterCoefficient"`) ||
				strings.Contains(string(encoded), `"filterTime"`) {
				t.Fatalf("candidate filter boundary = %#v, JSON %s",
					candidate.Gains, encoded)
			}
		})
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Flow.ModelUpdatedAt != before.Flow.ModelUpdatedAt {
		t.Fatal("read-only PID design changed the model revision")
	}
	if parameters := findBlock(t, after.Blocks, controllerID).Parameters; parameters.Proportional != 1 ||
		parameters.Integral != 1 {
		t.Fatalf("read-only PID design changed controller parameters: %#v", parameters)
	}
}

func TestPIDDesignTargetAndMarginMatchIndependentFrequencyOracle(t *testing.T) {
	service, flowID, _, _ := pidDesignStudio(t, BlockLag, BlockPID)
	candidate, err := service.DesignPIDController(
		context.Background(),
		flowID,
		PIDDesignRequest{
			Type: controlsys.PidtunePI, CrossoverFrequency: 2,
			PhaseMargin: 55, Omega: []float64{0.2, 2, 20},
			StepHorizon: 10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := service.BuildControlModels(
		context.Background(), flowID, ControlModelBuildRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plantResponse, err := models.Plant.FreqResponse([]float64{2})
	if err != nil {
		t.Fatal(err)
	}
	controllerResponse, err := candidate.Controller.FreqResponse([]float64{2})
	if err != nil {
		t.Fatal(err)
	}
	loop := plantResponse.At(0, 0, 0) * controllerResponse.At(0, 0, 0)
	if difference := math.Abs(cmplx.Abs(loop) - 1); difference > 1e-9 {
		t.Fatalf("|L(j2)| = %.12g, want 1", cmplx.Abs(loop))
	}
	phaseMargin := 180 + cmplx.Phase(loop)*180/math.Pi
	if difference := math.Abs(phaseMargin - 55); difference > 1e-8 {
		t.Fatalf("independent phase margin = %.12g, want 55", phaseMargin)
	}
	if candidate.CandidateMargin == nil ||
		candidate.CandidateMargin.PhaseMarginDegrees == nil ||
		math.Abs(*candidate.CandidateMargin.PhaseMarginDegrees-phaseMargin) > 0.5 {
		t.Fatalf("reported margin = %#v, independent %.12g", candidate.CandidateMargin, phaseMargin)
	}
	closedResponse, err := candidate.ClosedLoop.FreqResponse([]float64{2})
	if err != nil {
		t.Fatal(err)
	}
	wantClosed := loop / (1 + loop)
	if difference := cmplx.Abs(closedResponse.At(0, 0, 0) - wantClosed); difference > 1e-9 {
		t.Fatalf(
			"closed loop at target = %v, want L/(1+L) = %v",
			closedResponse.At(0, 0, 0), wantClosed,
		)
	}
	if candidate.Step == nil ||
		len(candidate.Step.Times) != len(candidate.Step.CurrentValues) ||
		len(candidate.Step.Times) != len(candidate.Step.CandidateValues) {
		t.Fatalf("common-grid step evidence = %#v", candidate.Step)
	}
	if final := candidate.Step.CandidateValues[len(candidate.Step.CandidateValues)-1]; math.Abs(final-1) > 0.02 {
		t.Fatalf("candidate PI step final value = %.6g, want 1", final)
	}
}

func TestPIDDesignAppliesAtomicallyAndRejectsStaleCandidate(t *testing.T) {
	service, flowID, _, controllerID := pidDesignStudio(t, BlockLag, BlockPID)
	ctx := context.Background()
	candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
		Type: controlsys.PidtunePIDF, CrossoverFrequency: 1, PhaseMargin: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := service.ApplyPIDDesignCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	applied := application.Snapshot
	parameters := findBlock(t, applied.Blocks, controllerID).Parameters
	if parameters.Proportional != candidate.Gains.Proportional ||
		parameters.Integral != candidate.Gains.Integral ||
		parameters.Derivative != candidate.Gains.Derivative ||
		parameters.FilterCoefficient != candidate.Gains.FilterCoefficient {
		t.Fatalf("applied parameters = %#v, candidate = %#v", parameters, candidate.Gains)
	}
	if _, err := service.ApplyPIDDesignCandidate(ctx, candidate); err == nil ||
		!strings.Contains(err.Error(), "stale") {
		t.Fatalf("second apply error = %v", err)
	}
}

func TestPID2NamedRolesPreserveFeedbackSignAndReferenceEquivalence(t *testing.T) {
	service, flowID, _, controllerID := pidDesignStudio(t, BlockLag, BlockPID2)
	ctx := context.Background()
	models, err := service.BuildControlModels(ctx, flowID, ControlModelBuildRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := models.ReferenceController.InputName; len(got) != 2 ||
		got[0] != "reference" || got[1] != "measurement" {
		t.Fatalf("PID2 reference inputs = %v", got)
	}
	omega := []float64{0.2, 1, 5}
	full, err := models.ReferenceController.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := models.Controller.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	for i := range omega {
		if cmplx.Abs(full.At(i, 0, 0)-feedback.At(i, 0, 0)) > 1e-12 ||
			cmplx.Abs(full.At(i, 0, 1)+feedback.At(i, 0, 0)) > 1e-12 {
			t.Fatalf(
				"PID2 B=C=1 at %g: reference=%v measurement=%v feedback=%v",
				omega[i], full.At(i, 0, 0), full.At(i, 0, 1), feedback.At(i, 0, 0),
			)
		}
	}
	duplicated, err := service.DuplicateFlow(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	copiedRoles, err := service.ControlRoles(ctx, duplicated.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copiedRoles.Controller.ReferenceInputs[0].BlockID == controllerID {
		t.Fatal("duplicated PID2 reference role retained the source controller ID")
	}
	if _, err := service.BuildControlModels(
		ctx, duplicated.Snapshot.Flow.ID, ControlModelBuildRequest{},
	); err != nil {
		t.Fatalf("duplicated PID2 roles do not compile: %v", err)
	}
	referenceClosed, err := pid2ReferenceClosedLoop(
		models.Plant, models.ReferenceController,
	)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryOpen, err := controlsys.Series(models.Controller, models.Plant)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryClosed, err := controlsys.Feedback(ordinaryOpen, nil, -1)
	if err != nil {
		t.Fatal(err)
	}
	referenceResponse, err := referenceClosed.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryResponse, err := ordinaryClosed.FreqResponse(omega)
	if err != nil {
		t.Fatal(err)
	}
	for i := range omega {
		if difference := cmplx.Abs(
			referenceResponse.At(i, 0, 0) - ordinaryResponse.At(i, 0, 0),
		); difference > 1e-11 {
			t.Fatalf("PID2 B=C=1 closed-loop difference at %g = %g", omega[i], difference)
		}
	}
	b, c := 0.7, 0.15
	candidate, err := service.DesignPIDController(ctx, flowID, PIDDesignRequest{
		Type: controlsys.PidtunePIDF, CrossoverFrequency: 1,
		PhaseMargin: 60, SetpointWeight: &b, DerivativeWeight: &c,
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Gains.SetpointWeight != b || candidate.Gains.DerivativeWeight != c {
		t.Fatalf("PID2 weights = %#v", candidate.Gains)
	}
	application, err := service.ApplyPIDDesignCandidate(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	applied := application.Snapshot
	parameters := findBlock(t, applied.Blocks, controllerID).Parameters
	if parameters.SetpointWeight != b || parameters.DerivativeWeight != c {
		t.Fatalf("applied PID2 weights = %#v", parameters)
	}
}

func TestPIDDesignGatesIntegratorDelayUnstableAndDiscretePlants(t *testing.T) {
	tests := []struct {
		name      string
		plantKind BlockKind
		configure map[string]string
		discrete  bool
		delay     bool
	}{
		{
			name: "integrating", plantKind: BlockTransfer,
			configure: map[string]string{"numerator": "1", "denominator": "1, 0"},
		},
		{name: "delayed", plantKind: BlockDelay, delay: true},
		{
			name: "unstable", plantKind: BlockTransfer,
			configure: map[string]string{"numerator": "1", "denominator": "1, -1"},
		},
		{name: "discrete", plantKind: BlockDiscreteTransfer, discrete: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, flowID, plantID, _ := pidDesignStudio(
				t, test.plantKind, BlockPID,
			)
			if len(test.configure) != 0 {
				if _, err := service.UpdateBlock(
					context.Background(), plantID,
					BlockUpdate{Name: "Plant", Parameters: test.configure},
				); err != nil {
					t.Fatal(err)
				}
			}
			if test.discrete {
				snapshot, err := service.Snapshot(context.Background(), flowID)
				if err != nil {
					t.Fatal(err)
				}
				var controllerID int64
				for _, block := range snapshot.Blocks {
					if block.Kind == BlockPID {
						controllerID = block.ID
					}
				}
				if _, err := service.UpdateBlock(
					context.Background(), controllerID,
					BlockUpdate{
						Name: "Controller",
						Parameters: map[string]string{
							"proportional": "1", "integral": "0.5",
							"derivative": "0", "filter_coefficient": "10",
							"time_domain": "discrete", "sample_time": "0.1",
						},
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			candidate, err := service.DesignPIDController(
				context.Background(), flowID,
				PIDDesignRequest{
					Type: controlsys.PidtunePI, CrossoverFrequency: 0.5,
					PhaseMargin: 50,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Controller == nil || len(candidate.Frequency.Omega) == 0 {
				t.Fatalf("incomplete candidate = %#v", candidate)
			}
			if test.discrete && candidate.Gains.SampleTime != 0.1 {
				t.Fatalf("discrete PID Dt = %g, want 0.1", candidate.Gains.SampleTime)
			}
			if test.discrete {
				application, err := service.ApplyPIDDesignCandidate(
					context.Background(), candidate,
				)
				if err != nil {
					t.Fatal(err)
				}
				applied := application.Snapshot
				controller := findBlock(t, applied.Blocks, candidate.ControllerBlockID)
				if normalizedModelDomain(controller.Parameters) != modelDomainDiscrete ||
					controller.Parameters.SampleTime != 0.1 {
					t.Fatalf(
						"applied discrete controller domain = %q at %g",
						normalizedModelDomain(controller.Parameters),
						controller.Parameters.SampleTime,
					)
				}
			}
			if test.delay {
				found := false
				for _, warning := range candidate.Warnings {
					found = found || strings.Contains(warning, "exact delay")
				}
				if !found {
					t.Fatalf("delay candidate warnings = %v", candidate.Warnings)
				}
			}
		})
	}
}

func pidDesignStudio(
	t *testing.T,
	plantKind, controllerKind BlockKind,
) (*Studio, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "pid-design.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "PID design")
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	_, plantID, err := service.AddBlock(
		ctx, flowID, plantKind, Point{X: 100, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, controllerID, err := service.AddBlock(
		ctx, flowID, controllerKind, Point{X: 400, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	controllerTargetPort := 0
	if controllerKind == BlockPID2 {
		controllerTargetPort = 1
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: controllerID, TargetID: plantID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: plantID, TargetID: controllerID, TargetPort: controllerTargetPort,
	}); err != nil {
		t.Fatal(err)
	}
	spec := sisoRoleSpec(plantID, controllerID)
	if controllerKind == BlockPID2 {
		reference := NamedChannelRef{
			BlockID: controllerID, Direction: ChannelInput,
			Port: 0, ChannelName: "reference",
		}
		measurement := NamedChannelRef{
			BlockID: controllerID, Direction: ChannelInput,
			Port: 1, ChannelName: "measurement",
		}
		control := NamedChannelRef{
			BlockID: controllerID, Direction: ChannelOutput,
			Port: 0, ChannelName: "control",
		}
		spec.Controller.ReferenceInputs = []NamedChannelRef{reference}
		spec.Controller.MeasurementInputs = []NamedChannelRef{measurement}
		spec.Controller.ControlOutputs = []NamedChannelRef{control}
		spec.AnalysisPoints[0].Pairs[0].Output = control
		spec.AnalysisPoints[1].Pairs[0].Input = measurement
	}
	if _, err := service.AssignControlRoles(ctx, flowID, spec); err != nil {
		t.Fatal(err)
	}
	return service, flowID, plantID, controllerID
}
