package web

import (
	"errors"
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type simulationRunAPIRequest struct {
	Duration   float64 `json:"duration"`
	SampleTime float64 `json:"sampleTime"`
}

type latestSimulationAPIRecord struct {
	studio.Simulation
	Stale bool `json:"stale"`
}

func (s *Server) simulationRunAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input simulationRunAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.Run(r.Context(), flowID, studio.SimulationRequest{
		Duration: input.Duration, SampleTime: input.SampleTime,
	})
	if err != nil {
		return apiResponse{}, err
	}
	if snapshot.LastRun == nil {
		return apiResponse{}, errors.New("simulation completed without a stored result")
	}
	return apiResponse{Status: http.StatusCreated, Value: snapshot.LastRun}, nil
}

func (s *Server) simulationShowAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	run, err := s.studio.LatestSimulation(r.Context(), flowID)
	if errors.Is(err, studio.ErrNotFound) {
		return apiResponse{}, &studio.ValidationError{Message: "no simulation run is stored; run one first."}
	}
	if err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.workspaceForFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: latestSimulationAPIRecord{
		Simulation: run, Stale: workspace.Snapshot.Flow.NeedsRun,
	}}, nil
}
