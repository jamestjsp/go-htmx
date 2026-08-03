package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestBlockLibraryAPIUsesCatalogOrderAndStableBytes(t *testing.T) {
	server, _ := openTestServer(t)
	first := requestAPI(t, server, http.MethodGet, "/api/v1/blocks")
	second := requestAPI(t, server, http.MethodGet, "/api/v1/blocks")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d/%d, want 200", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatal("repeated library responses differ")
	}

	var entries []blockLibraryEntry
	if err := json.Unmarshal(first.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	definitions := studio.BlockLibrary()
	if len(entries) != len(definitions) {
		t.Fatalf("library length = %d, want %d", len(entries), len(definitions))
	}
	for index, definition := range definitions {
		if entries[index].Kind != definition.Kind ||
			entries[index].Label != definition.Label ||
			entries[index].Category != definition.Category ||
			entries[index].Description != definition.Description ||
			entries[index].Tag != definition.Tag ||
			entries[index].HasInput != definition.HasInput() ||
			entries[index].HasOutput != definition.HasOutput() {
			t.Fatalf("entry %d = %#v, want catalog definition %#v", index, entries[index], definition)
		}
	}
}

func TestBlockSchemaAPIUsesCatalogSchemaAndNotFoundEnvelope(t *testing.T) {
	server, _ := openTestServer(t)
	response := requestAPI(t, server, http.MethodGet, "/api/v1/blocks/pid")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var schema studio.BlockSchema
	if err := json.Unmarshal(response.Body.Bytes(), &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	want, ok := studio.BlockKind("pid").Schema()
	if !ok || schema.Kind != want.Kind || len(schema.Parameters) != len(want.Parameters) || len(schema.Inputs) != len(want.Inputs) || len(schema.Outputs) != len(want.Outputs) {
		t.Fatalf("schema = %#v, want %#v", schema, want)
	}

	notFound := requestAPI(t, server, http.MethodGet, "/api/v1/blocks/no-such-kind")
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown kind status = %d, want 404: %s", notFound.Code, notFound.Body.String())
	}
	var envelope apiErrorResponse
	if err := json.Unmarshal(notFound.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode not-found envelope: %v", err)
	}
	if envelope.Error.Kind != "not_found" {
		t.Fatalf("not-found kind = %q, want not_found", envelope.Error.Kind)
	}
}

func requestAPI(t *testing.T, server *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
