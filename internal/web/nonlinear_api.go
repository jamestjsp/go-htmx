package web

import (
	"net/http"
	"strconv"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func (s *Server) nonlinearDefinitionRegisterAPI(r *http.Request) (apiResponse, error) {
	var definition studio.NonlinearDefinition
	if err := decodeAPIJSON(r, &definition); err != nil {
		return apiResponse{}, err
	}
	registered, err := s.studio.RegisterNonlinearDefinition(r.Context(), definition)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: registered}, nil
}

func (s *Server) nonlinearDefinitionShowAPI(r *http.Request) (apiResponse, error) {
	key := r.URL.Query().Get("key")
	if key == "" {
		return apiResponse{}, &studio.ValidationError{Message: "definition key is required."}
	}
	version, err := strconv.Atoi(r.URL.Query().Get("version"))
	if err != nil || version <= 0 {
		return apiResponse{}, &studio.ValidationError{Message: "definition version must be a positive integer."}
	}
	definition, err := s.studio.NonlinearDefinition(
		r.Context(), studio.NonlinearDefinitionRef{Key: key, Version: version},
	)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: definition}, nil
}

func (s *Server) nonlinearLinearizeAPI(r *http.Request) (apiResponse, error) {
	var request studio.NonlinearLinearizationRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		return apiResponse{}, err
	}
	candidate, err := s.studio.LinearizeNonlinear(r.Context(), request)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: candidate}, nil
}

func (s *Server) nonlinearEKFAPI(r *http.Request) (apiResponse, error) {
	var request studio.NonlinearEKFRunRequest
	if err := decodeAPIJSON(r, &request); err != nil {
		return apiResponse{}, err
	}
	run, err := s.studio.RunNonlinearEKF(r.Context(), request)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: run}, nil
}
