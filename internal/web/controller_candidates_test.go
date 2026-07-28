package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamestjsp/go-htmx/internal/studio"
)

func TestControllerCandidateRegistryTransitionsAreAtomic(t *testing.T) {
	registry := newControllerCandidateRegistry()
	candidate := &pendingControllerCandidate{ID: "candidate", FlowID: 7}
	if conflict := registry.put(candidate); conflict != controllerCandidatePutOK {
		t.Fatalf("put candidate: %v", conflict)
	}
	if registry.beginApply(candidate.ID, candidate.FlowID) == nil {
		t.Fatal("begin apply")
	}
	if registry.beginApply(candidate.ID, candidate.FlowID) != nil {
		t.Fatal("second apply began while the first was active")
	}
	if conflict := registry.put(
		&pendingControllerCandidate{ID: "replacement", FlowID: 7},
	); conflict != controllerCandidatePutBusy {
		t.Fatalf("active replacement conflict = %v", conflict)
	}
	if registry.finishApply(candidate.ID, nil) == nil {
		t.Fatal("release failed apply")
	}
	if registry.beginApply(candidate.ID, candidate.FlowID) == nil {
		t.Fatal("retry apply")
	}
	undo := studio.ControllerUndoCandidate{FlowID: candidate.FlowID}
	if applied := registry.finishApply(candidate.ID, &undo); applied == nil ||
		!applied.Applied || applied.Undo == nil {
		t.Fatalf("finish apply = %#v", applied)
	}
	if conflict := registry.put(
		&pendingControllerCandidate{ID: "replacement", FlowID: 7},
	); conflict != controllerCandidatePutUndoAvailable {
		t.Fatalf("undo replacement conflict = %v", conflict)
	}
	if registry.beginUndo(candidate.ID, candidate.FlowID) == nil {
		t.Fatal("begin undo")
	}
	if registry.beginUndo(candidate.ID, candidate.FlowID) != nil {
		t.Fatal("second undo began while the first was active")
	}
	if registry.finishUndo(candidate.ID, false) == nil {
		t.Fatal("release failed undo")
	}
	if registry.beginUndo(candidate.ID, candidate.FlowID) == nil {
		t.Fatal("retry undo")
	}
	registry.finishUndo(candidate.ID, true)
	if pending := registry.forFlow(candidate.FlowID); pending != nil {
		t.Fatalf("candidate remains after undo: %#v", pending)
	}

	for _, test := range []struct {
		name        string
		flowID      int64
		revision    time.Time
		fingerprint string
	}{
		{
			name: "model change", flowID: 8,
			revision:    time.Date(2026, 7, 28, 9, 1, 0, 0, time.UTC),
			fingerprint: "roles-a",
		},
		{
			name: "role change", flowID: 9,
			revision:    time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC),
			fingerprint: "roles-b",
		},
	} {
		t.Run(test.name+" expires undo", func(t *testing.T) {
			appliedRevision := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
			appliedRoles := studio.ControlRoleSnapshot{Fingerprint: "roles-a"}
			original := &pendingControllerCandidate{
				ID: "applied-" + test.name, FlowID: test.flowID,
			}
			if conflict := registry.put(original); conflict != controllerCandidatePutOK {
				t.Fatalf("put original: %v", conflict)
			}
			if registry.beginApply(original.ID, original.FlowID) == nil {
				t.Fatal("begin original apply")
			}
			registry.finishApply(
				original.ID,
				&studio.ControllerUndoCandidate{
					FlowID: original.FlowID, SourceModelRevision: appliedRevision,
					SourceControlRoles: appliedRoles,
				},
			)
			replacement := &pendingControllerCandidate{
				ID: "replacement-" + test.name, FlowID: test.flowID,
				Review: studio.ControllerCandidateReview{
					SourceModelRevision: test.revision,
					SourceControlRoles: studio.ControlRoleSnapshot{
						Fingerprint: test.fingerprint,
					},
				},
			}
			if conflict := registry.put(replacement); conflict != controllerCandidatePutOK {
				t.Fatalf("stale undo blocked replacement: %v", conflict)
			}
			if current := registry.forFlow(test.flowID); current == nil ||
				current.ID != replacement.ID {
				t.Fatalf("replacement = %#v", current)
			}
		})
	}
}

func TestControlRoleHTTPRoundTripAndValidation(t *testing.T) {
	server, service := openTestServer(t)
	flowID, _, _ := webPIDDesignFlow(t, service)
	path := "/flows/" + strconv.FormatInt(flowID, 10) + "/control-roles"
	current := request(t, server, http.MethodGet, path, nil)
	if current.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", current.Code, current.Body.String())
	}
	if contentType := current.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content type = %q", contentType)
	}

	put := httptest.NewRequest(http.MethodPut, path, strings.NewReader(current.Body.String()))
	put.Header.Set("Content-Type", "application/json")
	putResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", putResponse.Code, putResponse.Body.String())
	}
	if !strings.Contains(putResponse.Body.String(), `"version":1`) {
		t.Fatalf("assigned roles = %s", putResponse.Body.String())
	}

	invalid := httptest.NewRequest(
		http.MethodPut, path, strings.NewReader(`{"version":1,"unknown":true}`),
	)
	invalid.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d", invalidResponse.Code)
	}
}

func TestPIDCandidateReviewApplyAndUndoThroughHTMX(t *testing.T) {
	server, service := openTestServer(t)
	flowID, _, controllerID := webPIDDesignFlow(t, service)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	beforeController := webBlockByID(t, before.Blocks, controllerID)
	response := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/controller-candidates/pid",
		url.Values{
			"pid_type": {"PI"}, "crossover_frequency": {"2"},
			"phase_margin": {"55"}, "review_horizon": {"5"},
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("design status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{
		"PI candidate", "Current", "Candidate", "Phase margin",
		"Apply candidate atomically", "READ ONLY", "Named roles",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("PID candidate fragment does not contain %q", expected)
		}
	}
	unmodified, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !unmodified.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) ||
		webBlockByID(t, unmodified.Blocks, controllerID).Parameters.Proportional !=
			beforeController.Parameters.Proportional {
		t.Fatal("PID candidate generation mutated the controller")
	}
	pending := server.controllerCandidates.forFlow(flowID)
	if pending == nil || pending.ID == "" {
		t.Fatal("PID candidate was not stored behind an opaque ID")
	}
	applied := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/apply",
		nil,
	)
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", applied.Code, applied.Body.String())
	}
	if !strings.Contains(applied.Body.String(), "Undo candidate") {
		t.Fatalf("applied workbench has no undo action: %s", applied.Body.String())
	}
	afterApply, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if afterApply.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) ||
		webBlockByID(t, afterApply.Blocks, controllerID).Parameters.Proportional ==
			beforeController.Parameters.Proportional {
		t.Fatal("PID candidate apply did not change the controller")
	}
	replacement := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/controller-candidates/pid",
		url.Values{
			"pid_type": {"PI"}, "crossover_frequency": {"1"},
			"phase_margin": {"60"},
		},
	)
	if !strings.Contains(replacement.Body.String(), "Undo or change the model") ||
		!strings.Contains(replacement.Body.String(), "Undo candidate") {
		t.Fatalf("replacement response = %s", replacement.Body.String())
	}
	stillApplied := server.controllerCandidates.forFlow(flowID)
	if stillApplied == nil || stillApplied.ID != pending.ID || !stillApplied.Applied {
		t.Fatalf("replacement discarded undo candidate: %#v", stillApplied)
	}
	undone := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/undo",
		nil,
	)
	if undone.Code != http.StatusOK {
		t.Fatalf("undo status = %d: %s", undone.Code, undone.Body.String())
	}
	afterUndo, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	restored := webBlockByID(t, afterUndo.Blocks, controllerID)
	if restored.Parameters.Proportional != beforeController.Parameters.Proportional ||
		restored.Parameters.Integral != beforeController.Parameters.Integral {
		t.Fatalf("PID undo restored %#v, want %#v", restored.Parameters, beforeController.Parameters)
	}
}

func TestPID2CandidateWeightsApplyAndUndoThroughHTMX(t *testing.T) {
	server, service := openTestServer(t)
	flowID, _, controllerID := webPID2DesignFlow(t, service)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	beforeParameters := webBlockByID(t, before.Blocks, controllerID).Parameters
	response := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/controller-candidates/pid",
		url.Values{
			"pid_type": {"PIDF"}, "crossover_frequency": {"1"},
			"phase_margin": {"60"}, "setpoint_weight": {"0.35"},
			"derivative_weight": {"0.1"}, "review_horizon": {"5"},
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("design status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{
		"PIDF candidate", "Setpoint weight", "0.35",
		"Derivative weight", "0.1", "Closed-loop step",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("PID2 candidate fragment does not contain %q", expected)
		}
	}
	pending := server.controllerCandidates.forFlow(flowID)
	if pending == nil || pending.PID == nil ||
		!pending.PID.TwoDegreeOfFreedom ||
		pending.PID.Gains.SetpointWeight != 0.35 ||
		pending.PID.Gains.DerivativeWeight != 0.1 {
		t.Fatalf("PID2 candidate = %#v", pending)
	}
	applied := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/apply",
		nil,
	)
	if applied.Code != http.StatusOK {
		t.Fatalf("apply status = %d: %s", applied.Code, applied.Body.String())
	}
	afterApply, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	appliedParameters := webBlockByID(t, afterApply.Blocks, controllerID).Parameters
	if appliedParameters.SetpointWeight != 0.35 ||
		appliedParameters.DerivativeWeight != 0.1 {
		t.Fatalf("applied PID2 weights = %#v", appliedParameters)
	}
	undone := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/undo",
		nil,
	)
	if undone.Code != http.StatusOK {
		t.Fatalf("undo status = %d: %s", undone.Code, undone.Body.String())
	}
	restored, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if got := webBlockByID(t, restored.Blocks, controllerID).Parameters; !reflect.DeepEqual(got, beforeParameters) {
		t.Fatalf("PID2 undo restored %#v, want %#v", got, beforeParameters)
	}
}

func TestLQGCandidateReviewAndApplyThroughHTMX(t *testing.T) {
	server, service := openTestServer(t)
	flowID, _, controllerID := webStateDesignFlow(t, service)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	beforeController := webBlockByID(t, before.Blocks, controllerID)
	response := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/controller-candidates/state-space",
		url.Values{
			"q": {"1 0; 0 1"}, "r": {"1 0; 0 1"},
			"qn": {"1 0; 0 1"}, "rn": {"1 0; 0 1"},
			"review_horizon": {"4"},
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("LQG design status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{
		"lqg candidate", "Controllability", "Observability",
		"Current", "Candidate", "Apply candidate atomically",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("LQG candidate fragment does not contain %q", expected)
		}
	}
	unmodified, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	unmodifiedController := webBlockByID(t, unmodified.Blocks, controllerID)
	if !unmodified.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) ||
		unmodifiedController.Parameters.A.At(0, 0) !=
			beforeController.Parameters.A.At(0, 0) {
		t.Fatal("LQG candidate generation mutated the controller")
	}
	pending := server.controllerCandidates.forFlow(flowID)
	applied := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/apply",
		nil,
	)
	if applied.Code != http.StatusOK {
		t.Fatalf("LQG apply status = %d: %s", applied.Code, applied.Body.String())
	}
	after, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) ||
		reflect.DeepEqual(
			webBlockByID(t, after.Blocks, controllerID).Parameters,
			beforeController.Parameters,
		) {
		t.Fatal("LQG candidate apply did not replace the state-space controller")
	}
	undone := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/undo",
		nil,
	)
	if undone.Code != http.StatusOK {
		t.Fatalf("LQG undo status = %d: %s", undone.Code, undone.Body.String())
	}
	restored, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if got := webBlockByID(t, restored.Blocks, controllerID).Parameters; !reflect.DeepEqual(got, beforeController.Parameters) {
		t.Fatalf("LQG undo restored %#v, want %#v", got, beforeController.Parameters)
	}
}

func TestControllerCandidateRejectsWrongFlowAndStaleModel(t *testing.T) {
	server, service := openTestServer(t)
	flowID, _, _ := webPIDDesignFlow(t, service)
	response := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/controller-candidates/pid",
		url.Values{
			"pid_type": {"PI"}, "crossover_frequency": {"1"},
			"phase_margin": {"60"},
		},
	)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	pending := server.controllerCandidates.forFlow(flowID)
	wrongFlow := requestHX(
		t, server, http.MethodPost,
		"/flows/999/controller-candidates/"+pending.ID+"/apply", nil,
	)
	if !strings.Contains(wrongFlow.Body.String(), "expired or was replaced") {
		t.Fatalf("wrong-flow response = %s", wrongFlow.Body.String())
	}
	if _, _, err := service.AddBlock(
		context.Background(), flowID, studio.BlockConstant,
		studio.Point{X: 700, Y: 100},
	); err != nil {
		t.Fatal(err)
	}
	stale := requestHX(
		t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+
			"/controller-candidates/"+pending.ID+"/apply", nil,
	)
	staleBody := stale.Body.String()
	if !strings.Contains(staleBody, "stale") ||
		!strings.Contains(staleBody, "Generate fresh candidate") {
		t.Fatalf("stale response = %s", staleBody)
	}
	if stale.Header().Get("HX-Retarget") != "#controller-candidate" ||
		stale.Header().Get("HX-Reswap") != "innerHTML" {
		t.Fatalf(
			"stale HTMX swap = target %q, swap %q",
			stale.Header().Get("HX-Retarget"),
			stale.Header().Get("HX-Reswap"),
		)
	}
}

func webPIDDesignFlow(
	t *testing.T,
	service *studio.Studio,
) (flowID, plantID, controllerID int64) {
	return webPIDDesignFlowKind(t, service, studio.BlockPID)
}

func webPID2DesignFlow(
	t *testing.T,
	service *studio.Studio,
) (flowID, plantID, controllerID int64) {
	return webPIDDesignFlowKind(t, service, studio.BlockPID2)
}

func webPIDDesignFlowKind(
	t *testing.T,
	service *studio.Studio,
	controllerKind studio.BlockKind,
) (flowID, plantID, controllerID int64) {
	t.Helper()
	ctx := context.Background()
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "Web PID design")
	if err != nil {
		t.Fatal(err)
	}
	flowID = created.Snapshot.Flow.ID
	if _, plantID, err = service.AddBlock(
		ctx, flowID, studio.BlockLag, studio.Point{X: 100, Y: 100},
	); err != nil {
		t.Fatal(err)
	}
	if _, controllerID, err = service.AddBlock(
		ctx, flowID, controllerKind, studio.Point{X: 400, Y: 100},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, studio.Wire{
		SourceID: controllerID, TargetID: plantID,
	}); err != nil {
		t.Fatal(err)
	}
	controllerTargetPort := 0
	if controllerKind == studio.BlockPID2 {
		controllerTargetPort = 1
	}
	if _, err := service.Connect(ctx, flowID, studio.Wire{
		SourceID: plantID, TargetID: controllerID, TargetPort: controllerTargetPort,
	}); err != nil {
		t.Fatal(err)
	}
	if controllerKind == studio.BlockPID2 {
		assignWebPID2Roles(t, service, flowID, plantID, controllerID)
		return flowID, plantID, controllerID
	}
	assignWebControlRoles(
		t, service, flowID, plantID, controllerID,
		[]string{"value"}, []string{"value"}, studio.FeedbackExternalNegative,
	)
	return flowID, plantID, controllerID
}

func assignWebPID2Roles(
	t *testing.T,
	service *studio.Studio,
	flowID, plantID, controllerID int64,
) {
	t.Helper()
	plantInput := studio.NamedChannelRef{
		BlockID: plantID, Direction: studio.ChannelInput, Port: 0,
		ChannelName: "value",
	}
	plantOutput := studio.NamedChannelRef{
		BlockID: plantID, Direction: studio.ChannelOutput, Port: 0,
		ChannelName: "value",
	}
	reference := studio.NamedChannelRef{
		BlockID: controllerID, Direction: studio.ChannelInput, Port: 0,
		ChannelName: "reference",
	}
	measurement := studio.NamedChannelRef{
		BlockID: controllerID, Direction: studio.ChannelInput, Port: 1,
		ChannelName: "measurement",
	}
	control := studio.NamedChannelRef{
		BlockID: controllerID, Direction: studio.ChannelOutput, Port: 0,
		ChannelName: "control",
	}
	_, err := service.AssignControlRoles(
		context.Background(), flowID,
		studio.ControlRoleSpec{
			Version: 1,
			Plant: studio.PlantRole{
				Blocks:             []int64{plantID},
				ControlInputs:      []studio.NamedChannelRef{plantInput},
				MeasurementOutputs: []studio.NamedChannelRef{plantOutput},
			},
			Controller: studio.ControllerRole{
				Blocks:             []int64{controllerID},
				ReferenceInputs:    []studio.NamedChannelRef{reference},
				MeasurementInputs:  []studio.NamedChannelRef{measurement},
				ControlOutputs:     []studio.NamedChannelRef{control},
				FeedbackConvention: studio.FeedbackExternalNegative,
			},
			AnalysisPoints: []studio.AnalysisPointRole{
				{
					Name: "actuator", Location: studio.AnalysisPointPlantInput,
					Pairs: []studio.LoopBreakPair{{
						Output: control, Input: plantInput,
					}},
				},
				{
					Name: "sensor", Location: studio.AnalysisPointPlantOutput,
					Pairs: []studio.LoopBreakPair{{
						Output: plantOutput, Input: measurement,
					}},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func webStateDesignFlow(
	t *testing.T,
	service *studio.Studio,
) (flowID, plantID, controllerID int64) {
	t.Helper()
	ctx := context.Background()
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "Web LQG design")
	if err != nil {
		t.Fatal(err)
	}
	flowID = created.Snapshot.Flow.ID
	if _, plantID, err = service.AddBlock(
		ctx, flowID, studio.BlockStateSpace, studio.Point{X: 100, Y: 100},
	); err != nil {
		t.Fatal(err)
	}
	if _, controllerID, err = service.AddBlock(
		ctx, flowID, studio.BlockStateSpace, studio.Point{X: 400, Y: 100},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, controllerID, studio.BlockUpdate{
		Name: "LQG controller",
		Parameters: map[string]string{
			"a": "0.8 0; 0 0.5", "b": "1 0; 0 1",
			"c": "1 0; 0 1", "d": "0 0; 0 0",
			"input_names": "y1, y2", "output_names": "u1, u2",
			"state_names": "xc1, xc2", "time_domain": "continuous",
			"sample_time": "0.1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, studio.Wire{
		SourceID: controllerID, TargetID: plantID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, studio.Wire{
		SourceID: plantID, TargetID: controllerID,
	}); err != nil {
		t.Fatal(err)
	}
	assignWebControlRoles(
		t, service, flowID, plantID, controllerID,
		[]string{"u1", "u2"}, []string{"y1", "y2"},
		studio.FeedbackSignedControlLaw,
	)
	return flowID, plantID, controllerID
}

func assignWebControlRoles(
	t *testing.T,
	service *studio.Studio,
	flowID, plantID, controllerID int64,
	controls, measurements []string,
	convention studio.FeedbackConvention,
) {
	t.Helper()
	plantInputs := webNamedRefs(plantID, studio.ChannelInput, controls)
	plantOutputs := webNamedRefs(plantID, studio.ChannelOutput, measurements)
	controllerInputs := webNamedRefs(controllerID, studio.ChannelInput, measurements)
	controllerOutputs := webNamedRefs(controllerID, studio.ChannelOutput, controls)
	plantPairs := make([]studio.LoopBreakPair, len(controls))
	for i := range controls {
		plantPairs[i] = studio.LoopBreakPair{
			Output: controllerOutputs[i], Input: plantInputs[i],
		}
	}
	outputPairs := make([]studio.LoopBreakPair, len(measurements))
	for i := range measurements {
		outputPairs[i] = studio.LoopBreakPair{
			Output: plantOutputs[i], Input: controllerInputs[i],
		}
	}
	_, err := service.AssignControlRoles(context.Background(), flowID, studio.ControlRoleSpec{
		Version: 1,
		Plant: studio.PlantRole{
			Blocks: []int64{plantID}, ControlInputs: plantInputs,
			MeasurementOutputs: plantOutputs,
		},
		Controller: studio.ControllerRole{
			Blocks: []int64{controllerID}, FeedbackConvention: convention,
			MeasurementInputs: controllerInputs, ControlOutputs: controllerOutputs,
		},
		AnalysisPoints: []studio.AnalysisPointRole{
			{
				Name: "actuator", Location: studio.AnalysisPointPlantInput,
				Pairs: plantPairs,
			},
			{
				Name: "sensor", Location: studio.AnalysisPointPlantOutput,
				Pairs: outputPairs,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func webNamedRefs(
	blockID int64,
	direction studio.ChannelDirection,
	names []string,
) []studio.NamedChannelRef {
	refs := make([]studio.NamedChannelRef, len(names))
	for i, name := range names {
		refs[i] = studio.NamedChannelRef{
			BlockID: blockID, Direction: direction, Port: 0, ChannelName: name,
		}
	}
	return refs
}

func webBlockByID(
	t *testing.T,
	blocks []studio.Block,
	blockID int64,
) studio.Block {
	t.Helper()
	for _, block := range blocks {
		if block.ID == blockID {
			return block
		}
	}
	t.Fatalf("block %d not found", blockID)
	return studio.Block{}
}
