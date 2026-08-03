package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestSimulationAPIStoresAndShowsLatestRunsIncludingStaleData(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flowID := current.Snapshot.Flow.ID

	run := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/simulations", flowID), simulationRunAPIRequest{
		Duration: 1, SampleTime: 0.1,
	})
	if run.Code != http.StatusCreated {
		t.Fatalf("simulation run status = %d: %s", run.Code, run.Body.String())
	}
	var simulation studio.Simulation
	decodeJSONResponse(t, run, &simulation)
	if simulation.ID == 0 || len(simulation.Times) != 11 || len(simulation.Series) == 0 {
		t.Fatalf("simulation = id %d, %d times, %d series", simulation.ID, len(simulation.Times), len(simulation.Series))
	}

	show := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/simulations/latest", flowID))
	if show.Code != http.StatusOK {
		t.Fatalf("simulation show status = %d: %s", show.Code, show.Body.String())
	}
	var latest latestSimulationAPIRecord
	decodeJSONResponse(t, show, &latest)
	if latest.ID != simulation.ID || latest.Stale {
		t.Fatalf("latest simulation = %#v", latest)
	}

	if _, _, err := service.AddBlock(context.Background(), flowID, studio.BlockConstant, studio.Point{X: 2200, Y: 1000}); err != nil {
		t.Fatal(err)
	}
	stale := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/simulations/latest", flowID))
	if stale.Code != http.StatusOK {
		t.Fatalf("stale simulation status = %d: %s", stale.Code, stale.Body.String())
	}
	var staleLatest latestSimulationAPIRecord
	decodeJSONResponse(t, stale, &staleLatest)
	if staleLatest.ID != simulation.ID || !staleLatest.Stale {
		t.Fatalf("stale latest simulation = %#v", staleLatest)
	}
}

func TestSimulationAPIReportsMissingRunAsUsage(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(context.Background(), current.Project.ID, "No run")
	if err != nil {
		t.Fatal(err)
	}
	response := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/simulations/latest", created.Snapshot.Flow.ID))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "run one first") {
		t.Fatalf("missing run response = %d: %s", response.Code, response.Body.String())
	}
}
