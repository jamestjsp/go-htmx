package web

import (
	"context"
	"fmt"
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

// TestPageRendersTheRegister pins the projects home. `/` used to redirect into
// a flowsheet, which left the application with no screen that showed what
// projects existed; it now renders, and it renders its own shell rather than
// the workbench's.
func TestPageRendersTheRegister(t *testing.T) {
	server, _ := openTestServer(t)
	response := request(t, server, http.MethodGet, "/", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); location != "" {
		t.Fatalf("the register still redirects to %q", location)
	}
	body := response.Body.String()
	for _, expected := range []string{
		"Process Lab",
		"Drawing register",
		"Process Lab project",
		"Reactor temperature loop",
		`class="register-row"`,
		`href="/projects/1"`,
		`href="/projects/1/flows/1"`,
		`hx-post="/projects"`,
		`hx-put="/projects/1/name"`,
		`href="/assets/register.css"`,
		`src="/assets/register.js"`,
		"htmx.org@2.0.10",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("register does not contain %q", expected)
		}
	}
	// The register is a different shell. Pulling the workbench stylesheet or
	// its scripts onto it would undo the point of the separation.
	for _, unwanted := range []string{"/assets/app.css", "/assets/app.js", "/assets/menu.js"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the register loads %q", unwanted)
		}
	}
	// A CSP of `script-src 'self' https://cdn.jsdelivr.net` drops an inline
	// script in the browser while every test here still passes, so the absence
	// has to be asserted rather than assumed.
	if strings.Contains(body, "onclick=") || strings.Contains(body, "<script>") {
		t.Error("the register carries inline script, which the CSP drops silently")
	}
}

// TestRegisterListsEveryProjectAndFlowsheet is the register's whole promise:
// nothing is behind a menu, and a sheet can be opened without opening its
// project first.
func TestRegisterListsEveryProjectAndFlowsheet(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := service.CreateProject(ctx, "Operations")
	if err != nil {
		t.Fatal(err)
	}
	startup := addFlow(t, service, operations.Project.ID, "Startup")
	shutdown := addFlow(t, service, operations.Project.ID, "Shutdown")

	body := request(t, server, http.MethodGet, "/", nil).Body.String()
	for _, name := range []string{
		seeded.Project.Name, seeded.Snapshot.Flow.Name,
		"Operations", "Untitled flowsheet", "Startup", "Shutdown",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("register does not name %q", name)
		}
	}
	// Every sheet is reachable directly, in the project's tab order.
	for _, flowID := range []int64{operations.Snapshot.Flow.ID, startup, shutdown} {
		href := fmt.Sprintf(`href="/projects/%d/flows/%d"`, operations.Project.ID, flowID)
		if !strings.Contains(body, href) {
			t.Errorf("register does not link to %s", href)
		}
	}
	order := []string{
		fmt.Sprintf(`/flows/%d"`, operations.Snapshot.Flow.ID),
		fmt.Sprintf(`/flows/%d"`, startup),
		fmt.Sprintf(`/flows/%d"`, shutdown),
	}
	if at := indexesOf(body, order); !ascending(at) {
		t.Errorf("flowsheet chips are at %v, not in tab order", at)
	}
	if !strings.Contains(body, ">3<") {
		t.Error("register does not show the three-sheet count")
	}
}

// TestRegisterHidesDeleteForASingleProject keeps the domain's refusal to
// delete the last project out of the interface, so it cannot be reached by
// accident.
func TestRegisterHidesDeleteForASingleProject(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()

	only := request(t, server, http.MethodGet, "/", nil).Body.String()
	if strings.Contains(only, "hx-delete=") {
		t.Error("the only project offers Delete")
	}

	second, err := service.CreateProject(ctx, "Operations")
	if err != nil {
		t.Fatal(err)
	}
	both := request(t, server, http.MethodGet, "/", nil).Body.String()
	for _, expected := range []string{
		fmt.Sprintf(`hx-delete="/projects/%d"`, second.Project.ID),
		"and its 1 flowsheet?",
	} {
		if !strings.Contains(both, expected) {
			t.Errorf("register does not contain %q", expected)
		}
	}
	if !strings.Contains(both, "Delete &#34;Operations&#34;") &&
		!strings.Contains(both, "Delete “Operations”") {
		t.Errorf("the confirmation does not name the project: %s", both)
	}
}

// TestRenameProjectAnswersWithTheRegisterRow pins the seam. RenameProject
// hands back the project's FIRST flowsheet, so answering with the workbench
// fragment would move a caller on any other sheet — and would hand the
// register a whole workbench it has no place to put.
func TestRenameProjectAnswersWithTheRegisterRow(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := workspace.Project.ID
	addFlow(t, service, projectID, "Startup")

	response := request(t, server, http.MethodPut,
		fmt.Sprintf("/projects/%d/name", projectID),
		url.Values{"name": {"Cracker unit"}},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "workbench") || strings.Contains(body, "flow-canvas") {
		t.Fatalf("rename answered with the workbench: %s", body)
	}
	for _, expected := range []string{
		`<details class="register-row"`,
		"Cracker unit",
		"Startup",
		fmt.Sprintf(`hx-put="/projects/%d/name"`, projectID),
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("row does not contain %q", expected)
		}
	}
	// The row re-renders its own figures, so a renamed line cannot go stale.
	if !strings.Contains(body, ">2<") {
		t.Errorf("row does not carry the two-sheet count: %s", body)
	}
}

// TestRegisterViewCoversTheEmptyState reaches the state `Open` cannot: `seed`
// creates a project whenever no flows exist and DeleteProject refuses the last
// one, so an empty register is defensive markup, verified here rather than
// through the public API.
func TestRegisterViewCoversTheEmptyState(t *testing.T) {
	server, _ := openTestServer(t)
	view := newRegisterView(studio.Register{})
	if view.ProjectCount != 0 || view.SheetCount != 0 {
		t.Fatalf("empty register counts = %d projects, %d sheets", view.ProjectCount, view.SheetCount)
	}
	if view.ProjectLabel != "projects" || view.SheetLabel != "sheets" {
		t.Fatalf("empty register labels = %q, %q", view.ProjectLabel, view.SheetLabel)
	}
	var page strings.Builder
	if err := server.templates.ExecuteTemplate(&page, "register", view); err != nil {
		t.Fatalf("the empty register does not render: %v", err)
	}
	for _, expected := range []string{"Nothing on the register yet", `hx-post="/projects"`} {
		if !strings.Contains(page.String(), expected) {
			t.Errorf("empty register does not contain %q", expected)
		}
	}
}

// TestWorkbenchPageRendersTheShell keeps the workbench page covered now that
// `/` no longer leads to it.
func TestWorkbenchPageRendersTheShell(t *testing.T) {
	server, _ := openTestServer(t)
	response := request(t, server, http.MethodGet, "/projects/1/flows/1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{
		"Process Lab",
		"Process Lab project",
		"Reactor temperature loop",
		"Feed setpoint",
		`id="workbench"`,
		`hx-post="/flows/1/blocks"`,
		"htmx.org@2.0.10",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

// TestTopbarOffersTheRegisterAndTheProjectSwitcher pins the header's whole
// job: say where you are, lead home, and open any other project. Everything
// else it used to carry now belongs to a screen that does it better — the
// register lists projects, and the tab strip owns the sheets of this one.
func TestTopbarOffersTheRegisterAndTheProjectSwitcher(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	second, err := service.CreateProject(ctx, "Compressor station")
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}

	body := request(t, server, http.MethodGet, "/projects/1/flows/1", nil).Body.String()
	end := strings.Index(body, `<div class="studio-grid">`)
	if end < 0 {
		t.Fatalf("no studio grid in the page: %s", body)
	}
	header := body[:end]

	for _, expected := range []string{
		`class="topbar-home" href="/"`,
		`<details class="project-switcher">`,
		`<a href="/projects/1" aria-current="page"><span>Process Lab project</span></a>`,
		fmt.Sprintf(`<a href="/projects/%d"><span>Compressor station</span></a>`, second.Project.ID),
		`action="/projects" hx-post="/projects"`,
		"New project",
		// The counts and the saved lamp are the header's other job, and this
		// work does not touch them.
		fmt.Sprintf("<b>%d</b> blocks", len(snapshot.Blocks)),
		fmt.Sprintf("<b>%d</b> signals", len(snapshot.Connections)),
		`class="saved-state"`,
	} {
		if !strings.Contains(header, expected) {
			t.Errorf("topbar does not contain %q", expected)
		}
	}
	// Exactly one project is marked open, and it is the one being edited.
	if lit := strings.Count(header, `aria-current="page"`); lit != 1 {
		t.Errorf("the switcher marks %d projects as open, want 1", lit)
	}
	// The header no longer names sheets. The flowsheet popover sat directly
	// above a strip that lists every sheet, and the name field was a second
	// source of truth for a name the strip now owns.
	for _, gone := range []string{
		`hx-put="/flows/1/name"`,
		"Active flowsheet",
		"autosaved",
		"New flowsheet",
	} {
		if strings.Contains(header, gone) {
			t.Errorf("the topbar still carries %q", gone)
		}
	}
}

// TestUnknownPathIsNotTheRegister guards the mux's catch-all: `GET /` matches
// anything no other pattern claims, and answering a typo with the home page
// would dress every miss as a 200.
func TestUnknownPathIsNotTheRegister(t *testing.T) {
	server, _ := openTestServer(t)
	response := request(t, server, http.MethodGet, "/nowhere", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

// indexesOf reports where each needle first appears, or -1.
func indexesOf(body string, needles []string) []int {
	at := make([]int, 0, len(needles))
	for _, needle := range needles {
		at = append(at, strings.Index(body, needle))
	}
	return at
}

func ascending(values []int) bool {
	for i, value := range values {
		if value < 0 || (i > 0 && value <= values[i-1]) {
			return false
		}
	}
	return true
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
		if candidate.ID == block.ID && candidate.Position != (studio.Point{X: 420, Y: 200}) {
			t.Fatalf("position = %#v", candidate.Position)
		}
	}
}

func TestCatalogPaletteAndTransferFunctionEditor(t *testing.T) {
	server, service := openTestServer(t)
	// The workbench directly: `/` is the register now, and reading a Location
	// off a 200 would hand httptest.NewRequest an empty URL, which panics.
	page := request(t, server, http.MethodGet, "/projects/1/flows/1", nil)
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

func TestProjectAndFlowLifecycleThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	initial, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}

	createProject := request(t, server, http.MethodPost, "/projects", url.Values{
		"name": {"Operations"},
	})
	if createProject.Code != http.StatusSeeOther {
		t.Fatalf("create project status = %d, body = %s", createProject.Code, createProject.Body.String())
	}
	projectLocation := createProject.Header().Get("Location")
	var projectID, defaultFlowID int64
	if _, err := fmt.Sscanf(
		projectLocation, "/projects/%d/flows/%d", &projectID, &defaultFlowID,
	); err != nil {
		t.Fatalf("project location = %q: %v", projectLocation, err)
	}
	projectPage := request(t, server, http.MethodGet, projectLocation, nil)
	if projectPage.Code != http.StatusOK ||
		!strings.Contains(projectPage.Body.String(), "Operations") ||
		!strings.Contains(projectPage.Body.String(), "Untitled flowsheet") {
		t.Fatalf("project page status = %d, body = %s", projectPage.Code, projectPage.Body.String())
	}
	projectRedirect := request(t, server, http.MethodGet,
		"/projects/"+strconv.FormatInt(projectID, 10), nil,
	)
	if projectRedirect.Code != http.StatusSeeOther ||
		projectRedirect.Header().Get("Location") != projectLocation {
		t.Fatalf("project redirect = %d %q", projectRedirect.Code, projectRedirect.Header().Get("Location"))
	}

	createFlow := requestHX(t, server, http.MethodPost,
		"/projects/"+strconv.FormatInt(projectID, 10)+"/flows",
		url.Values{"name": {"Startup"}},
	)
	if createFlow.Code != http.StatusNoContent {
		t.Fatalf("create flow status = %d, body = %s", createFlow.Code, createFlow.Body.String())
	}
	flowLocation := createFlow.Header().Get("HX-Redirect")
	var createdProjectID, flowID int64
	if _, err := fmt.Sscanf(
		flowLocation, "/projects/%d/flows/%d", &createdProjectID, &flowID,
	); err != nil {
		t.Fatalf("flow location = %q: %v", flowLocation, err)
	}
	if createdProjectID != projectID {
		t.Fatalf("flow project = %d, want %d", createdProjectID, projectID)
	}

	rename := request(t, server, http.MethodPut,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/name",
		url.Values{"name": {"Warm startup"}},
	)
	if rename.Code != http.StatusOK || !strings.Contains(rename.Body.String(), "Warm startup") {
		t.Fatalf("rename status = %d, body = %s", rename.Code, rename.Body.String())
	}
	reopened := request(t, server, http.MethodGet, flowLocation, nil)
	if reopened.Code != http.StatusOK || !strings.Contains(reopened.Body.String(), "Warm startup") {
		t.Fatalf("reopen status = %d, body = %s", reopened.Code, reopened.Body.String())
	}

	mismatch := request(t, server, http.MethodGet,
		"/projects/"+strconv.FormatInt(initial.Project.ID, 10)+
			"/flows/"+strconv.FormatInt(flowID, 10),
		nil,
	)
	if mismatch.Code != http.StatusNotFound {
		t.Fatalf("mismatch status = %d", mismatch.Code)
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
	for _, path := range []string{
		"/assets/app.css", "/assets/app.js", "/assets/menu.js",
		"/assets/register.css", "/assets/register.js",
	} {
		response := request(t, server, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		if response.Body.Len() < 1000 {
			t.Fatalf("%s unexpectedly small", path)
		}
	}
}

func TestMoveBlocksBatchThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Blocks) < 3 {
		t.Fatalf("seeded flow has %d blocks", len(snapshot.Blocks))
	}
	moved := snapshot.Blocks[:3]
	path := "/flows/" + strconv.FormatInt(snapshot.Flow.ID, 10) + "/blocks/positions"

	values := url.Values{}
	want := map[int64]studio.Point{}
	for i, block := range moved {
		position := studio.Point{X: 400 + i*220, Y: 600}
		values.Add("id", strconv.FormatInt(block.ID, 10))
		values.Add("x", strconv.Itoa(position.X))
		values.Add("y", strconv.Itoa(position.Y))
		want[block.ID] = position
	}
	if response := request(t, server, http.MethodPatch, path, values); response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	after, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range after.Blocks {
		if expected, ok := want[block.ID]; ok && block.Position != expected {
			t.Fatalf("block %d position = %#v, want %#v", block.ID, block.Position, expected)
		}
	}

	t.Run("mismatched arrays are rejected", func(t *testing.T) {
		bad := url.Values{}
		bad.Add("id", strconv.FormatInt(moved[0].ID, 10))
		bad.Add("id", strconv.FormatInt(moved[1].ID, 10))
		bad.Add("x", "100")
		bad.Add("y", "100")
		if response := request(t, server, http.MethodPatch, path, bad); response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", response.Code)
		}
	})

	t.Run("a block from another flow moves nothing", func(t *testing.T) {
		before, err := service.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		foreign := url.Values{}
		foreign.Add("id", strconv.FormatInt(moved[0].ID, 10))
		foreign.Add("x", "1200")
		foreign.Add("y", "1200")
		foreign.Add("id", "999999")
		foreign.Add("x", "1200")
		foreign.Add("y", "1200")
		if response := request(t, server, http.MethodPatch, path, foreign); response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", response.Code)
		}
		after, err := service.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for i, block := range after.Blocks {
			if block.Position != before.Blocks[i].Position {
				t.Fatalf("block %d moved despite the rejected batch", block.ID)
			}
		}
	})
}

func TestDuplicateAndBatchDeleteBlocksThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flowID := strconv.FormatInt(snapshot.Flow.ID, 10)
	originals := snapshot.Blocks[:2]

	values := url.Values{}
	for _, block := range originals {
		values.Add("id", strconv.FormatInt(block.ID, 10))
	}
	response := request(t, server, http.MethodPost, "/flows/"+flowID+"/blocks/duplicate", values)
	if response.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, body = %s", response.Code, response.Body.String())
	}
	afterCopy, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(afterCopy.Blocks) != len(snapshot.Blocks)+2 {
		t.Fatalf("block count = %d, want %d", len(afterCopy.Blocks), len(snapshot.Blocks)+2)
	}
	for _, block := range originals {
		if !strings.Contains(response.Body.String(), block.Name+" copy") {
			t.Errorf("no copy rendered for %q", block.Name)
		}
	}
	// Duplicating must not invent new wiring.
	if len(afterCopy.Connections) != len(snapshot.Connections) {
		t.Fatalf("connections = %d, want %d", len(afterCopy.Connections), len(snapshot.Connections))
	}

	t.Run("a foreign block duplicates nothing", func(t *testing.T) {
		foreign := url.Values{}
		foreign.Add("id", strconv.FormatInt(originals[0].ID, 10))
		foreign.Add("id", "999999")
		before, err := service.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		request(t, server, http.MethodPost, "/flows/"+flowID+"/blocks/duplicate", foreign)
		after, err := service.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(after.Blocks) != len(before.Blocks) {
			t.Fatalf("block count changed from %d to %d", len(before.Blocks), len(after.Blocks))
		}
	})

	t.Run("batch delete removes blocks and their wires", func(t *testing.T) {
		before, err := service.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		gain := findKindBlock(t, before.Blocks, "gain")
		path := "/flows/" + flowID + "/blocks?id=" + strconv.FormatInt(gain.ID, 10)
		if response := request(t, server, http.MethodDelete, path, nil); response.Code != http.StatusOK {
			t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
		}
		after, err := service.Current(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, connection := range after.Connections {
			if connection.SourceID == gain.ID || connection.TargetID == gain.ID {
				t.Fatalf("connection %#v survived the delete", connection)
			}
		}
		if len(after.Blocks) != len(before.Blocks)-1 {
			t.Fatalf("block count = %d, want %d", len(after.Blocks), len(before.Blocks)-1)
		}
	})
}

func findKindBlock(t *testing.T, blocks []studio.Block, kind string) studio.Block {
	t.Helper()
	for _, block := range blocks {
		if string(block.Kind) == kind {
			return block
		}
	}
	t.Fatalf("no %s block in the flow", kind)
	return studio.Block{}
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

func requestHX(t *testing.T, server *Server, method, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	return response
}
