package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestControllerCandidateAPIUsesOpaqueRegistryHandlesAndReportsStaleApply(t *testing.T) {
	server, service := openTestServer(t)
	flowID, _, controllerID := webPIDDesignFlow(t, service)
	path := fmt.Sprintf("/api/v1/flows/%d/controller-candidates/pid", flowID)
	input := pidCandidateAPIRequest{
		PIDDesignRequest: studio.PIDDesignRequest{
			Type:               "PI",
			CrossoverFrequency: 2,
			PhaseMargin:        55,
			StepHorizon:        5,
		},
		ReviewHorizon: 5,
	}

	firstResponse := requestJSONAPI(t, server, http.MethodPost, path, input)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("first candidate status = %d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	var first controllerCandidateAPIRecord
	decodeJSONResponse(t, firstResponse, &first)
	if first.ID == "" || first.Kind != "pid" || !first.Review.ApplyAvailable {
		t.Fatalf("first candidate = %#v", first)
	}

	show := requestJSONAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/controller-candidates/%s", flowID, first.ID), nil)
	if show.Code != http.StatusOK {
		t.Fatalf("candidate show status = %d: %s", show.Code, show.Body.String())
	}
	var shown controllerCandidateAPIRecord
	decodeJSONResponse(t, show, &shown)
	if shown.ID != first.ID || shown.Review.Kind != "pid" {
		t.Fatalf("shown candidate = %#v", shown)
	}

	secondResponse := requestJSONAPI(t, server, http.MethodPost, path, input)
	if secondResponse.Code != http.StatusCreated {
		t.Fatalf("second candidate status = %d: %s", secondResponse.Code, secondResponse.Body.String())
	}
	var second controllerCandidateAPIRecord
	decodeJSONResponse(t, secondResponse, &second)
	if second.ID == first.ID {
		t.Fatal("candidate replacement reused the first opaque ID")
	}
	expired := requestJSONAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/controller-candidates/%s", flowID, first.ID), nil)
	if expired.Code != http.StatusNotFound {
		t.Fatalf("replaced candidate status = %d: %s", expired.Code, expired.Body.String())
	}

	busy, release := server.controllerCandidates.beginApply(second.ID, flowID)
	if busy == nil {
		t.Fatal("begin API conflict apply")
	}
	conflict := requestJSONAPI(t, server, http.MethodPost, path, input)
	release()
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "action is in progress") {
		t.Fatalf("busy design response = %d: %s", conflict.Code, conflict.Body.String())
	}

	before, err := service.Snapshot(context.Background(), flowID)
	if err != nil {
		t.Fatal(err)
	}
	var controller studio.Block
	for _, block := range before.Blocks {
		if block.ID == controllerID {
			controller = block
		}
	}
	parameters := make(map[string]string)
	for _, field := range controller.EditorFields() {
		parameters[field.Name] = field.Value
	}
	parameters["proportional"] = "2"
	if _, err := service.UpdateBlock(context.Background(), controllerID, studio.BlockUpdate{
		Name:       controller.Name,
		Parameters: parameters,
	}); err != nil {
		t.Fatal(err)
	}
	stale := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/controller-candidates/%s/apply", flowID, second.ID), nil)
	if stale.Code != http.StatusBadRequest || !strings.Contains(stale.Body.String(), "stale") {
		t.Fatalf("stale apply response = %d: %s", stale.Code, stale.Body.String())
	}
}
