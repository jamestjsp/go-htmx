package web

import (
	"net/http"
)

func (s *Server) modelStudyAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	provenance, err := s.studio.ControlModelStudy(
		r.Context(), flowID, r.URL.Query().Get("role"),
	)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: provenance}, nil
}
