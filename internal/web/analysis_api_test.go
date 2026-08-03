package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestAnalysisAPIListsChannelsRunsAnalysisAndMarksStale(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := findKindBlock(t, snapshot.Blocks, "source")
	plant := findKindBlock(t, snapshot.Blocks, "lag")

	response := requestJSONAPI(t, server, http.MethodGet, "/api/v1/flows/1/analyses", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("analysis workspace status = %d: %s", response.Code, response.Body.String())
	}
	var workspace studio.AnalysisWorkspace
	decodeJSONResponse(t, response, &workspace)
	if len(workspace.Inputs) == 0 || len(workspace.Outputs) == 0 {
		t.Fatalf("analysis channels = %#v", workspace)
	}
	if workspace.Inputs[0].BlockID != source.ID {
		t.Fatalf("first analysis input = %#v, want source %d", workspace.Inputs[0], source.ID)
	}

	response = requestJSONAPI(t, server, http.MethodPost, "/api/v1/flows/1/analyses", studio.AnalysisWorkspaceRequest{
		Intent:      studio.AnalysisIntentDynamics,
		Input:       studio.ChannelRef{BlockID: source.ID, Port: 0, Channel: 0},
		Output:      studio.ChannelRef{BlockID: plant.ID, Port: 0, Channel: 0},
		StepHorizon: 2,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("analysis run status = %d: %s", response.Code, response.Body.String())
	}
	decodeJSONResponse(t, response, &workspace)
	if workspace.Dynamics == nil || workspace.Dynamics.Stale || workspace.Dynamics.Result.StepExperiment == nil {
		t.Fatalf("dynamics workspace = %#v", workspace.Dynamics)
	}

	_, err = service.UpdateBlock(ctx, plant.ID, studio.BlockUpdate{
		Name: plant.Name,
		Parameters: map[string]string{
			"time_constant": "9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response = requestJSONAPI(t, server, http.MethodGet, "/api/v1/flows/1/analyses", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("stale analysis workspace status = %d: %s", response.Code, response.Body.String())
	}
	decodeJSONResponse(t, response, &workspace)
	if workspace.Dynamics == nil || !workspace.Dynamics.Stale {
		t.Fatalf("stale dynamics workspace = %#v", workspace.Dynamics)
	}
}

func TestAnalysisAPIRejectsMalformedChannelRequest(t *testing.T) {
	server, _ := openTestServer(t)
	response := requestJSONAPI(t, server, http.MethodPost, "/api/v1/flows/1/analyses", map[string]any{
		"intent": "dynamics",
		"input":  map[string]any{"blockId": "bad", "port": 0, "channel": 0},
	})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "valid JSON") {
		t.Fatalf("malformed analysis request = %d: %s", response.Code, response.Body.String())
	}
}
