package web

import (
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func (s *Server) identificationEstimateAPI(r *http.Request) (apiResponse, error) {
	var input studio.FrequencyIdentificationRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := studio.NewIdentificationWorkflow().EstimateFrequencyResponse(input)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: candidate}, nil
}

func (s *Server) identificationERAAPI(r *http.Request) (apiResponse, error) {
	var input studio.ERAIdentificationRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	candidate, err := studio.NewIdentificationWorkflow().IdentifyERA(input)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: candidate}, nil
}
