package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestEventsAPIRejectsExcessiveLimit(t *testing.T) {
	server, _ := openTestServer(t)
	response := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/1/events?limit=%d", 1_000_001))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "event limit") {
		t.Fatalf("excessive event limit = %d: %s", response.Code, response.Body.String())
	}
}
