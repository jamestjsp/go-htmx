package web

import (
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type parameterSweepAPIRequest struct {
	BlockID  int64                    `json:"blockId"`
	Sweep    studio.SweepSpec         `json:"sweep"`
	Analysis studio.SweepAnalysisSpec `json:"analysis"`
}

func (s *Server) parameterSweepRunAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input parameterSweepAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	result, err := s.studio.RunParameterSweep(
		r.Context(), flowID, input.BlockID, input.Sweep, input.Analysis,
	)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: result}, nil
}
