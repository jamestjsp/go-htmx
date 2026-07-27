package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jamestjsp/go-htmx/internal/studio"
)

func TestPageRendersSeededHTMXWorkbench(t *testing.T) {
	server, _ := openTestServer(t)
	response := request(t, server, http.MethodGet, "/", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{
		"Process Lab",
		"Reactor temperature loop",
		"Feed setpoint",
		`hx-post="/flows/1/blocks"`,
		"htmx.org@2.0.10",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

func TestAddUpdateAndMoveBlockThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	add := request(t, server, http.MethodPost, "/flows/1/blocks", url.Values{
		"kind": {"lag"},
		"x":    {"170"},
		"y":    {"280"},
	})
	if add.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", add.Code, add.Body.String())
	}
	if strings.Contains(add.Body.String(), "<!doctype html>") {
		t.Fatal("mutation returned full page instead of workbench fragment")
	}

	afterAdd, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(afterAdd.Blocks) != len(snapshot.Blocks)+1 {
		t.Fatalf("block count = %d", len(afterAdd.Blocks))
	}
	block := afterAdd.Blocks[len(afterAdd.Blocks)-1]

	update := request(t, server, http.MethodPut, "/blocks/"+strconv.FormatInt(block.ID, 10), url.Values{
		"name":          {"Heat exchanger"},
		"time_constant": {"3.5"},
	})
	if update.Code != http.StatusOK || !strings.Contains(update.Body.String(), "Heat exchanger") {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}

	move := request(t, server, http.MethodPatch, "/blocks/"+strconv.FormatInt(block.ID, 10)+"/position", url.Values{
		"x": {"410"},
		"y": {"190"},
	})
	if move.Code != http.StatusNoContent {
		t.Fatalf("move status = %d, body = %s", move.Code, move.Body.String())
	}
	afterMove, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range afterMove.Blocks {
		if candidate.ID == block.ID && candidate.Position != (studio.Point{X: 410, Y: 190}) {
			t.Fatalf("position = %#v", candidate.Position)
		}
	}
}

func TestCatalogPaletteAndTransferFunctionEditor(t *testing.T) {
	server, service := openTestServer(t)
	page := request(t, server, http.MethodGet, "/", nil)
	for _, expected := range []string{
		"Constant", "Sine Wave", "Integrator", "Transfer Function",
		"PID Controller", "Transport Delay", "Spectrum Analyzer",
	} {
		if !strings.Contains(page.Body.String(), expected) {
			t.Errorf("palette does not contain %q", expected)
		}
	}

	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	add := request(t, server, http.MethodPost, "/flows/1/blocks", url.Values{
		"kind": {"transfer"},
		"x":    {"170"},
		"y":    {"280"},
	})
	if add.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", add.Code, add.Body.String())
	}
	afterAdd, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(afterAdd.Blocks) != len(snapshot.Blocks)+1 {
		t.Fatalf("block count = %d", len(afterAdd.Blocks))
	}
	block := afterAdd.Blocks[len(afterAdd.Blocks)-1]
	body := add.Body.String()
	for _, expected := range []string{
		`name="numerator"`, `value="1"`,
		`name="denominator"`, `value="1, 1"`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("editor does not contain %q", expected)
		}
	}

	update := request(t, server, http.MethodPut, "/blocks/"+strconv.FormatInt(block.ID, 10), url.Values{
		"name":        {"Plant"},
		"numerator":   {"2, 1"},
		"denominator": {"1, 3, 2"},
	})
	if update.Code != http.StatusOK || !strings.Contains(update.Body.String(), "[2, 1] / [1, 3, 2]") {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}
}

func TestSpectrumAnalyzerThroughHTMXFlow(t *testing.T) {
	server, service := openTestServer(t)
	addSine := request(t, server, http.MethodPost, "/flows/1/blocks", url.Values{
		"kind": {"sine"},
		"x":    {"30"},
		"y":    {"470"},
	})
	if addSine.Code != http.StatusOK {
		t.Fatalf("add sine status = %d, body = %s", addSine.Code, addSine.Body.String())
	}
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sine := snapshot.Blocks[len(snapshot.Blocks)-1]
	update := request(t, server, http.MethodPut, "/blocks/"+strconv.FormatInt(sine.ID, 10), url.Values{
		"name":      {"Two hertz"},
		"amplitude": {"1.25"},
		"bias":      {"0"},
		"frequency": {"12.566370614359172"},
		"phase":     {"0"},
	})
	if update.Code != http.StatusOK {
		t.Fatalf("update sine status = %d, body = %s", update.Code, update.Body.String())
	}

	addSpectrum := request(t, server, http.MethodPost, "/flows/1/blocks", url.Values{
		"kind": {"spectrum"},
		"x":    {"750"},
		"y":    {"470"},
	})
	if addSpectrum.Code != http.StatusOK {
		t.Fatalf("add spectrum status = %d, body = %s", addSpectrum.Code, addSpectrum.Body.String())
	}
	snapshot, err = service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spectrum := snapshot.Blocks[len(snapshot.Blocks)-1]
	connect := request(t, server, http.MethodPost, "/flows/1/connections", url.Values{
		"source_id": {strconv.FormatInt(sine.ID, 10)},
		"target_id": {strconv.FormatInt(spectrum.ID, 10)},
	})
	if connect.Code != http.StatusOK {
		t.Fatalf("connect status = %d, body = %s", connect.Code, connect.Body.String())
	}

	run := request(t, server, http.MethodPost, "/flows/1/simulations", url.Values{
		"duration":    {"3.99"},
		"sample_time": {"0.01"},
	})
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d, body = %s", run.Code, run.Body.String())
	}
	for _, expected := range []string{
		"frequency spectrum", "Peak 2 Hz", "controlsys + Gonum FFT",
	} {
		if !strings.Contains(run.Body.String(), expected) {
			t.Errorf("spectrum result does not contain %q", expected)
		}
	}
}

func TestConnectionErrorRendersInline(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	connection := snapshot.Connections[0]

	response := request(t, server, http.MethodPost, "/flows/1/connections", url.Values{
		"source_id": {strconv.FormatInt(connection.SourceID, 10)},
		"target_id": {strconv.FormatInt(connection.TargetID, 10)},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "already connected") {
		t.Fatalf("body does not contain validation: %s", response.Body.String())
	}
}

func TestSimulationReturnsSVGTrendAndMetrics(t *testing.T) {
	server, _ := openTestServer(t)
	response := request(t, server, http.MethodPost, "/flows/1/simulations", url.Values{
		"duration":    {"20"},
		"sample_time": {"0.1"},
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{"trend-chart", "Temperature", "controlsys", "Settling"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

func TestStaticAssetsAreEmbedded(t *testing.T) {
	server, _ := openTestServer(t)
	for _, path := range []string{"/assets/app.css", "/assets/app.js"} {
		response := request(t, server, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if response.Body.Len() < 1000 {
			t.Fatalf("%s unexpectedly small", path)
		}
	}
}

func openTestServer(t *testing.T) (*Server, *studio.Studio) {
	t.Helper()
	service, err := studio.Open(context.Background(), filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	server, err := New(service)
	if err != nil {
		t.Fatal(err)
	}
	return server, service
}

func request(t *testing.T, server *Server, method, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	return response
}
