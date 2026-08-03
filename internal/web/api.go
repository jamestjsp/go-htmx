package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/jamestjsp/process-lab/internal/studio"
)

const maxAPIRequestBytes = 1 << 20

type apiResponse struct {
	Status int
	Value  any
}

type apiOperation func(*http.Request) (apiResponse, error)

type apiConflictError struct {
	message string
}

func (e *apiConflictError) Error() string {
	return e.message
}

func apiConflict(message string) error {
	return &apiConflictError{message: message}
}

type apiErrorResponse struct {
	Error apiErrorDetail `json:"error"`
}

type apiErrorDetail struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (s *Server) api(operation apiOperation) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := operation(r)
		if err != nil {
			s.writeAPIError(w, r, err)
			return
		}
		status := result.Status
		if status == 0 {
			status = http.StatusOK
		}
		writeAPIJSON(w, status, result.Value)
	})
}

func (s *Server) writeAPIError(w http.ResponseWriter, r *http.Request, err error) {
	status, detail := apiErrorDetailFor(err)
	if status >= http.StatusInternalServerError && s.logger != nil {
		s.logger.Printf("api %s %s: %v", r.Method, r.URL.Path, err)
	}
	writeAPIJSON(w, status, apiErrorResponse{Error: detail})
}

func apiErrorDetailFor(err error) (int, apiErrorDetail) {
	var validation *studio.ValidationError
	var conflict *apiConflictError
	switch {
	case errors.As(err, &validation):
		return http.StatusBadRequest, apiErrorDetail{
			Kind:    "usage",
			Message: validation.Message,
		}
	case errors.Is(err, studio.ErrNotFound):
		return http.StatusNotFound, apiErrorDetail{
			Kind:    "not_found",
			Message: studio.ValidationMessage(err),
		}
	case errors.As(err, &conflict):
		return http.StatusConflict, apiErrorDetail{
			Kind:    "conflict",
			Message: conflict.message,
		}
	default:
		return http.StatusInternalServerError, apiErrorDetail{
			Kind:    "internal",
			Message: studio.ValidationMessage(err),
		}
	}
}

func writeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

func decodeAPIJSON(r *http.Request, destination any) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return &studio.ValidationError{Message: "Content-Type must be application/json."}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxAPIRequestBytes+1))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(body) > maxAPIRequestBytes {
		return &studio.ValidationError{
			Message: fmt.Sprintf("request body exceeds the %d-byte limit.", maxAPIRequestBytes),
		}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return &studio.ValidationError{Message: "request body is required."}
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return &studio.ValidationError{Message: "request body must contain valid JSON."}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &studio.ValidationError{Message: "request body must contain one JSON value."}
	}
	return nil
}
