package web

import (
	"net/http"
	"strconv"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type flowDocumentApplyAPIResponse struct {
	Result studio.FlowApplyResult `json:"result"`
}

func (s *Server) flowDocumentDumpAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	document, err := s.studio.DumpFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: document}, nil
}

func (s *Server) flowDocumentApplyAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	dryRun, err := optionalBoolQuery(r, "dry-run")
	if err != nil {
		return apiResponse{}, err
	}
	var document studio.FlowDocument
	if err := decodeAPIJSON(r, &document); err != nil {
		return apiResponse{}, err
	}
	result, _, err := s.studio.ApplyFlow(r.Context(), flowID, document, dryRun)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: flowDocumentApplyAPIResponse{Result: result}}, nil
}

func optionalBoolQuery(r *http.Request, name string) (bool, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, &studio.ValidationError{Message: name + " must be true or false."}
	}
	return parsed, nil
}
