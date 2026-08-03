package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestParameterSweepAPIReportsWorstCaseSummaries(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plant := findKindBlock(t, snapshot.Blocks, "lag")
	response := requestJSONAPI(t, server, http.MethodPost, "/api/v1/flows/1/parameter-sweeps", parameterSweepAPIRequest{
		BlockID: plant.ID,
		Sweep: studio.SweepSpec{
			Axes: []studio.SweepAxis{{Parameter: "time_constant", Unit: "s", Values: []float64{1, 2}}},
		},
		Analysis: studio.SweepAnalysisSpec{Omega: []float64{0.1, 1}, StepFinal: 1},
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("parameter sweep status = %d: %s", response.Code, response.Body.String())
	}
	var result studio.ParameterSweepAnalysis
	decodeJSONResponse(t, response, &result)
	if len(result.Frequency.Models) != 2 || len(result.Time.Models) != 2 ||
		result.Frequency.WorstCase.Name == "" || result.Time.WorstCase.Name == "" {
		t.Fatalf("parameter sweep result = %#v", result)
	}
}

func TestParameterSweepAPIRejectsUnknownParameter(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	plant := findKindBlock(t, snapshot.Blocks, "lag")
	response := requestJSONAPI(t, server, http.MethodPost, "/api/v1/flows/1/parameter-sweeps", parameterSweepAPIRequest{
		BlockID: plant.ID,
		Sweep: studio.SweepSpec{
			Axes: []studio.SweepAxis{{Parameter: "missing", Unit: "1", Values: []float64{1}}},
		},
		Analysis: studio.SweepAnalysisSpec{Omega: []float64{0.1, 1}, StepFinal: 1},
	})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "is not defined for block") {
		t.Fatalf("unknown parameter response = %d: %s", response.Code, response.Body.String())
	}
}
