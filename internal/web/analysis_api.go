package web

import (
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func (s *Server) analysisShowAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.workspaceForFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspace.Analysis}, nil
}

func (s *Server) analysisRunAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input studio.AnalysisWorkspaceRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.RunAnalysis(r.Context(), flowID, input)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: workspace}, nil
}
