package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestAPIErrorEnvelopeMapsDomainErrors(t *testing.T) {
	server, _ := openTestServer(t)
	var logOutput bytes.Buffer
	server.logger = log.New(&logOutput, "", 0)

	tests := []struct {
		name       string
		err        error
		status     int
		kind       string
		message    string
		loggedText string
	}{
		{
			name:    "validation",
			err:     &studio.ValidationError{Message: "gain must be positive"},
			status:  http.StatusBadRequest,
			kind:    "usage",
			message: "gain must be positive",
		},
		{
			name:    "not found",
			err:     studio.ErrNotFound,
			status:  http.StatusNotFound,
			kind:    "not_found",
			message: "The requested item no longer exists.",
		},
		{
			name:       "conflict",
			err:        apiConflict("Another controller candidate action is in progress. Try again after it finishes."),
			status:     http.StatusConflict,
			kind:       "conflict",
			message:    "Another controller candidate action is in progress. Try again after it finishes.",
			loggedText: "",
		},
		{
			name:       "internal",
			err:        errors.New("database password leaked only to logs"),
			status:     http.StatusInternalServerError,
			kind:       "internal",
			message:    "The operation could not be completed.",
			loggedText: "database password leaked only to logs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := invokeAPI(t, server, func(*http.Request) (apiResponse, error) {
				return apiResponse{}, test.err
			})
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want JSON", contentType)
			}
			var payload apiErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error.Kind != test.kind || payload.Error.Message != test.message {
				t.Fatalf("error = %#v, want kind %q and message %q", payload.Error, test.kind, test.message)
			}
			if strings.Contains(response.Body.String(), "database password") {
				t.Fatal("internal error leaked its implementation detail")
			}
			if test.loggedText != "" && !strings.Contains(logOutput.String(), test.loggedText) {
				t.Fatalf("log = %q, want original error", logOutput.String())
			}
		})
	}
}

func TestAPIDecoderRejectsNonJSONAndOversizedBodies(t *testing.T) {
	server, _ := openTestServer(t)
	operation := func(r *http.Request) (apiResponse, error) {
		var input struct {
			Name string `json:"name"`
		}
		if err := decodeAPIJSON(r, &input); err != nil {
			return apiResponse{}, err
		}
		return apiResponse{Value: input}, nil
	}

	tests := []struct {
		name        string
		contentType string
		body        string
		message     string
	}{
		{
			name:        "form body",
			contentType: "application/x-www-form-urlencoded",
			body:        "name=processlab",
			message:     "Content-Type must be application/json.",
		},
		{
			name:        "oversized body",
			contentType: "application/json",
			body:        fmt.Sprintf(`{"name":"%s"}`, strings.Repeat("x", maxAPIRequestBytes)),
			message:     fmt.Sprintf("request body exceeds the %d-byte limit.", maxAPIRequestBytes),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			server.api(operation).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}
			var payload apiErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error.Kind != "usage" || payload.Error.Message != test.message {
				t.Fatalf("error = %#v, want usage %q", payload.Error, test.message)
			}
		})
	}
}

func TestAPIRouterReturnsJSONForUnknownRoute(t *testing.T) {
	server, _ := openTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/no-such-resource", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want JSON", contentType)
	}
	var payload apiErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error.Kind != "not_found" || !strings.Contains(payload.Error.Message, "GET /api/v1/no-such-resource") {
		t.Fatalf("unknown route error = %#v", payload.Error)
	}
	if strings.Contains(payload.Error.Message, "requested item no longer exists") {
		t.Fatal("unknown route used the missing-record message")
	}
}

func invokeAPI(t *testing.T, server *Server, operation apiOperation) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	response := httptest.NewRecorder()
	server.api(operation).ServeHTTP(response, request)
	return response
}
