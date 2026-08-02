package web

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestRobustCandidateDesignApplyAndExactUndoThroughHTMX(t *testing.T) {
	server, service := openTestServer(t)
	flowID, controllerID := robustBrowserFlow(t, service)
	ctx := context.Background()
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	beforeRoles, err := service.ControlRoles(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}

	var applyPath string
	var candidateID string
	for _, method := range []string{"h2", "hinf"} {
		response := requestHX(
			t,
			server,
			http.MethodPost,
			"/flows/"+strconv.FormatInt(flowID, 10)+"/controller-candidates/robust",
			url.Values{
				"method":         {method},
				"review_horizon": {"5"},
			},
		)
		if response.Code != http.StatusOK {
			t.Fatalf("%s design status = %d: %s", method, response.Code, response.Body.String())
		}
		if response.Header().Get("HX-Retarget") != "#controller-candidate" ||
			response.Header().Get("HX-Reswap") != "innerHTML" {
			t.Fatalf(
				"%s HTMX swap = target %q, swap %q",
				method,
				response.Header().Get("HX-Retarget"),
				response.Header().Get("HX-Reswap"),
			)
		}
		body := response.Body.String()
		for _, expected := range []string{
			method + " candidate",
			"generalized closed-loop norm",
			"w1, w2",
			"z1, z2",
			"Achieved norm",
			"Stable closed loop",
			"Closed-loop poles",
			"Named roles",
			"READ ONLY",
			"Apply candidate atomically",
		} {
			if !strings.Contains(body, expected) {
				t.Errorf("%s candidate fragment does not contain %q", method, expected)
			}
		}
		if method == "hinf" {
			for _, expected := range []string{"Gamma bound", "Peak frequency"} {
				if !strings.Contains(body, expected) {
					t.Errorf("Hinf candidate fragment does not contain %q", expected)
				}
			}
		}

		applyPath, candidateID = robustCandidateAction(t, body, flowID, "apply")
		if pending := server.controllerCandidates.forFlow(flowID); pending == nil ||
			pending.ID != candidateID || pending.Kind != "robust-synthesis" ||
			pending.Robust == nil || string(pending.Robust.Method) != method {
			t.Fatalf("%s opaque candidate registry entry = %#v", method, pending)
		}
		unmodified, err := service.Snapshot(ctx, flowID)
		if err != nil {
			t.Fatal(err)
		}
		if !unmodified.Flow.ModelUpdatedAt.Equal(before.Flow.ModelUpdatedAt) ||
			!reflect.DeepEqual(unmodified.Blocks, before.Blocks) ||
			!reflect.DeepEqual(unmodified.Connections, before.Connections) {
			t.Fatalf("%s candidate generation mutated the authored model", method)
		}
	}

	applied := requestHX(t, server, http.MethodPost, applyPath, nil)
	if applied.Code != http.StatusOK {
		t.Fatalf("robust apply status = %d: %s", applied.Code, applied.Body.String())
	}
	if !strings.Contains(applied.Body.String(), "Controller candidate applied") ||
		!strings.Contains(applied.Body.String(), "Undo candidate") {
		t.Fatalf("applied robust workbench omitted success or undo: %s", applied.Body.String())
	}
	afterApply, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(
		webBlockByID(t, afterApply.Blocks, controllerID),
		webBlockByID(t, before.Blocks, controllerID),
	) {
		t.Fatal("robust candidate apply did not replace the authored controller")
	}
	pending := server.controllerCandidates.forFlow(flowID)
	if pending == nil || pending.ID != candidateID || !pending.Applied || pending.Undo == nil {
		t.Fatalf("applied opaque candidate registry entry = %#v", pending)
	}

	undoPath, undoID := robustCandidateAction(t, applied.Body.String(), flowID, "undo")
	if undoID != candidateID {
		t.Fatalf("undo candidate ID = %q, applied candidate ID %q", undoID, candidateID)
	}
	undone := requestHX(t, server, http.MethodPost, undoPath, nil)
	if undone.Code != http.StatusOK {
		t.Fatalf("robust undo status = %d: %s", undone.Code, undone.Body.String())
	}
	if !strings.Contains(undone.Body.String(), "Controller candidate undone") {
		t.Fatalf("undo response omitted success: %s", undone.Body.String())
	}
	afterUndo, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	afterRoles, err := service.ControlRoles(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterUndo.Blocks, before.Blocks) ||
		!reflect.DeepEqual(afterUndo.Connections, before.Connections) ||
		!reflect.DeepEqual(afterRoles, beforeRoles) {
		t.Fatalf(
			"robust undo was not exact:\nblocks restored=%t\nconnections restored=%t\nroles restored=%t",
			reflect.DeepEqual(afterUndo.Blocks, before.Blocks),
			reflect.DeepEqual(afterUndo.Connections, before.Connections),
			reflect.DeepEqual(afterRoles, beforeRoles),
		)
	}
	if pending := server.controllerCandidates.forFlow(flowID); pending != nil {
		t.Fatalf("opaque candidate remains after undo: %#v", pending)
	}
}

func robustCandidateAction(
	t *testing.T,
	body string,
	flowID int64,
	action string,
) (path, candidateID string) {
	t.Helper()
	pattern := regexp.MustCompile(
		`/flows/` + strconv.FormatInt(flowID, 10) +
			`/controller-candidates/([0-9a-f]{32})/` + action,
	)
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("response has no opaque robust candidate %s path", action)
	}
	return match[0], match[1]
}

func robustBrowserFlow(
	t *testing.T,
	service *studio.Studio,
) (flowID, controllerID int64) {
	t.Helper()
	ctx := context.Background()
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "Robust browser route")
	if err != nil {
		t.Fatal(err)
	}
	flowID = created.Snapshot.Flow.ID
	_, muxID, err := service.AddBlock(
		ctx, flowID, studio.BlockMux, studio.Point{X: 100, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, plantID, err := service.AddBlock(
		ctx, flowID, studio.BlockStateSpace, studio.Point{X: 300, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, demuxID, err := service.AddBlock(
		ctx, flowID, studio.BlockDemux, studio.Point{X: 500, Y: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, controllerID, err = service.AddBlock(
		ctx, flowID, studio.BlockStateSpace, studio.Point{X: 500, Y: 300},
	)
	if err != nil {
		t.Fatal(err)
	}

	updates := []struct {
		blockID    int64
		name       string
		parameters map[string]string
	}{
		{
			blockID: muxID,
			name:    "Generalized inputs",
			parameters: map[string]string{
				"output_names": "w1, w2, u",
			},
		},
		{
			blockID: plantID,
			name:    "Named generalized plant",
			parameters: map[string]string{
				"a": "0 1; -2 -3", "b": "1 0 0; 0 1 1",
				"c": "1 0; 0 0; 1 0", "d": "0 0 0; 0 0 1; 0.1 0.1 0",
				"input_names": "w1, w2, u", "output_names": "z1, z2, y",
				"state_names": "plant.x1, plant.x2", "time_domain": "continuous",
				"sample_time": "0.1",
			},
		},
		{
			blockID: demuxID,
			name:    "Generalized outputs",
			parameters: map[string]string{
				"input_names": "z1, z2, y",
			},
		},
		{
			blockID: controllerID,
			name:    "Authored robust controller",
			parameters: map[string]string{
				"a": "-2", "b": "1", "c": "0", "d": "0",
				"input_names": "y", "output_names": "u",
				"state_names": "controller.x", "time_domain": "continuous",
				"sample_time": "0.1",
			},
		},
	}
	for _, update := range updates {
		if _, err := service.UpdateBlock(ctx, update.blockID, studio.BlockUpdate{
			Name: update.name, Parameters: update.parameters,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, wire := range []studio.Wire{
		{SourceID: controllerID, TargetID: muxID, TargetPort: 2},
		{SourceID: muxID, TargetID: plantID},
		{SourceID: plantID, TargetID: demuxID},
		{SourceID: demuxID, SourcePort: 2, TargetID: controllerID},
	} {
		if _, err := service.Connect(ctx, flowID, wire); err != nil {
			t.Fatal(err)
		}
	}

	w1 := robustBrowserChannel(muxID, studio.ChannelInput, 0, "w1")
	w2 := robustBrowserChannel(muxID, studio.ChannelInput, 1, "w2")
	plantControl := robustBrowserChannel(muxID, studio.ChannelInput, 2, "u")
	z1 := robustBrowserChannel(demuxID, studio.ChannelOutput, 0, "z1")
	z2 := robustBrowserChannel(demuxID, studio.ChannelOutput, 1, "z2")
	plantMeasurement := robustBrowserChannel(demuxID, studio.ChannelOutput, 2, "y")
	controllerMeasurement := robustBrowserChannel(controllerID, studio.ChannelInput, 0, "y")
	controllerControl := robustBrowserChannel(controllerID, studio.ChannelOutput, 0, "u")
	_, err = service.AssignControlRoles(ctx, flowID, studio.ControlRoleSpec{
		Version: 1,
		Plant: studio.PlantRole{
			Blocks:             []int64{muxID, plantID, demuxID},
			ExogenousInputs:    []studio.NamedChannelRef{w1, w2},
			ControlInputs:      []studio.NamedChannelRef{plantControl},
			PerformanceOutputs: []studio.NamedChannelRef{z1, z2},
			MeasurementOutputs: []studio.NamedChannelRef{plantMeasurement},
		},
		Controller: studio.ControllerRole{
			Blocks:             []int64{controllerID},
			MeasurementInputs:  []studio.NamedChannelRef{controllerMeasurement},
			ControlOutputs:     []studio.NamedChannelRef{controllerControl},
			FeedbackConvention: studio.FeedbackSignedControlLaw,
		},
		AnalysisPoints: []studio.AnalysisPointRole{
			{
				Name:     "actuator",
				Location: studio.AnalysisPointPlantInput,
				Pairs: []studio.LoopBreakPair{{
					Output: controllerControl,
					Input:  plantControl,
				}},
			},
			{
				Name:     "sensor",
				Location: studio.AnalysisPointPlantOutput,
				Pairs: []studio.LoopBreakPair{{
					Output: plantMeasurement,
					Input:  controllerMeasurement,
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return flowID, controllerID
}

func robustBrowserChannel(
	blockID int64,
	direction studio.ChannelDirection,
	port int,
	name string,
) studio.NamedChannelRef {
	return studio.NamedChannelRef{
		BlockID: blockID, Direction: direction, Port: port, ChannelName: name,
	}
}
