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
		`href="/assets/tokens.css"`,
		`href="/assets/register.css"`,
		`src="/assets/register.js"`,
		"htmx.org@2.0.10",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("register does not contain %q", expected)
		}
	}
	// The register is a different shell. Pulling the workbench stylesheet or
	// its scripts onto it would undo the point of the separation. The canvas
	// modules are named by their directory: they assume a #workbench to act on,
	// so any one of them arriving here is the same mistake.
	for _, unwanted := range []string{"/assets/app.css", "/assets/js/", "/assets/menu.js"} {
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

// TestRenameProjectAnswersWithOrderedRegisterRows pins the seam. RenameProject
// hands back the project's FIRST flowsheet, so answering with the workbench
// fragment would move a caller on any other sheet — and would hand the
// register a whole workbench it has no place to put. Answering with only the
// renamed row would also leave a name-sorted register in its old order.
func TestRenameProjectAnswersWithOrderedRegisterRows(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := workspace.Project.ID
	addFlow(t, service, projectID, "Startup")
	if _, err := service.CreateProject(ctx, "Alpha unit"); err != nil {
		t.Fatal(err)
	}

	response := request(t, server, http.MethodPut,
		fmt.Sprintf("/projects/%d/name", projectID),
		url.Values{"name": {"Zulu unit"}},
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
		"Alpha unit",
		"Zulu unit",
		"Startup",
		fmt.Sprintf(`hx-put="/projects/%d/name"`, projectID),
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("row does not contain %q", expected)
		}
	}
	if alpha, zulu := strings.Index(body, "Alpha unit"), strings.Index(body, "Zulu unit"); alpha < 0 || zulu < 0 || alpha >= zulu {
		t.Errorf("renamed rows are not in canonical order: %s", body)
	}
	if rows := strings.Count(body, `<details class="register-row"`); rows != 2 {
		t.Errorf("rename returned %d register rows, want 2", rows)
	}
	// The collection re-renders each row's figures, so a renamed line cannot go stale.
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

func TestWorkbenchRendersValidatedParameterShapes(t *testing.T) {
	server, _ := openTestServer(t)
	workspace := studio.Workspace{
		Project: studio.Project{ID: 1, Name: "Test"},
		Flows:   []studio.Flow{{ID: 1, ProjectID: 1, Name: "Model"}},
		Snapshot: studio.Snapshot{
			Flow: studio.Flow{ID: 1, ProjectID: 1, Name: "Model"},
			Blocks: []studio.Block{{
				ID: 1, FlowID: 1, Kind: studio.BlockTransfer, Name: "Plant",
				Parameters: studio.Parameters{
					Numerator: []float64{1, 2}, Denominator: []float64{1, 3, 2},
				},
			}},
		},
	}
	view := newWorkbenchView(workspace, 1, "")
	var page strings.Builder
	if err := server.templates.ExecuteTemplate(&page, "workbench", view); err != nil {
		t.Fatal(err)
	}
	body := page.String()
	for _, want := range []string{
		`name="numerator"`,
		`name="denominator"`,
		`class="field-shape">1 × 2`,
		`class="field-shape">1 × 3`,
		"Descending powers of s",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered transfer editor does not contain %q", want)
		}
	}
}

func TestWorkbenchRendersNamedVectorPortWidth(t *testing.T) {
	server, _ := openTestServer(t)
	workspace := studio.Workspace{
		Project: studio.Project{ID: 1, Name: "Test"},
		Flows:   []studio.Flow{{ID: 1, ProjectID: 1, Name: "MIMO"}},
		Snapshot: studio.Snapshot{
			Flow: studio.Flow{ID: 1, ProjectID: 1, Name: "MIMO"},
		},
	}
	matrixSnapshot, matrixID, err := func() (studio.Snapshot, int64, error) {
		temporary, err := studio.Open(context.Background(), ":memory:")
		if err != nil {
			return studio.Snapshot{}, 0, err
		}
		defer temporary.Close()
		current, err := temporary.Current(context.Background())
		if err != nil {
			return studio.Snapshot{}, 0, err
		}
		return temporary.AddBlock(
			context.Background(), current.Flow.ID,
			studio.BlockMatrixGain, studio.Point{X: 100, Y: 100},
		)
	}()
	if err != nil {
		t.Fatal(err)
	}
	workspace.Snapshot.Blocks = []studio.Block{matrixSnapshot.Blocks[len(matrixSnapshot.Blocks)-1]}
	workspace.Snapshot.Blocks[0].ID = matrixID

	view := newWorkbenchView(workspace, matrixID, "")
	var page strings.Builder
	if err := server.templates.ExecuteTemplate(&page, "workbench", view); err != nil {
		t.Fatal(err)
	}
	body := page.String()
	for _, want := range []string{
		`input port 1 (2 channels: u1, u2)`,
		`output port 1 (2 channels: y1, y2)`,
		`class="field-shape">2 × 2`,
		`name="d"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered matrix gain does not contain %q", want)
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
		`href="/assets/tokens.css"`,
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

func TestResultsExportReturnsVersionedJSONAttachment(t *testing.T) {
	server, _ := openTestServer(t)
	run := request(t, server, http.MethodPost, "/flows/1/simulations", url.Values{
		"duration":    {"1"},
		"sample_time": {"0.1"},
	})
	if run.Code != http.StatusOK {
		t.Fatalf("run status = %d, body = %s", run.Code, run.Body.String())
	}

	response := request(t, server, http.MethodGet, "/flows/1/results.json", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("export status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content type = %q", contentType)
	}
	if disposition := response.Header().Get("Content-Disposition"); !strings.Contains(
		disposition, `process-lab-flow-1-results.json`,
	) {
		t.Fatalf("content disposition = %q", disposition)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`"schemaVersion": 1`,
		`"flowId": 1`,
		`"simulation":`,
		`"analysis":`,
		`"blockId":`,
		`"port": 0`,
		`"channel": 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("export does not contain %q", expected)
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

func TestAnalysisWorkspaceRetainsResultsAndMarksModelEditsStale(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := findKindBlock(t, snapshot.Blocks, "source")
	plant := findKindBlock(t, snapshot.Blocks, "lag")
	channel := func(block studio.Block) string {
		return fmt.Sprintf("%d:0:0", block.ID)
	}
	values := url.Values{
		"analysis_intent":    {"dynamics"},
		"analysis_input":     {channel(source)},
		"analysis_output":    {channel(plant)},
		"analysis_horizon":   {"8"},
		"analysis_points":    {"40"},
		"analysis_base_step": {"0.1"},
	}
	response := request(t, server, http.MethodPost, "/flows/1/analyses", values)
	if response.Code != http.StatusOK {
		t.Fatalf("dynamics status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		"Control analysis", "Dynamics &amp; time", "Step response",
		"Pole-zero map", "CURRENT",
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("dynamics workspace does not contain %q", expected)
		}
	}

	values.Set("analysis_intent", "frequency")
	response = request(t, server, http.MethodPost, "/flows/1/analyses", values)
	if response.Code != http.StatusOK {
		t.Fatalf("frequency status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		"Dynamics &amp; time", "Frequency response", "Bode magnitude",
		"Nyquist", "Nichols", "Singular values",
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("retained workspace does not contain %q", expected)
		}
	}

	_, err = service.UpdateBlock(ctx, plant.ID, studio.BlockUpdate{
		Name: plant.Name,
		Parameters: map[string]string{
			"time_constant": "9",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response = request(t, server, http.MethodGet, "/projects/1/flows/1", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("edited workspace status = %d", response.Code)
	}
	for _, expected := range []string{
		"Model changed · prior analysis is stale", "STALE",
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("stale workspace does not contain %q", expected)
		}
	}
}

// The connect form's port fields are optional: a client written before ports
// omits them and keeps wiring each block's first terminal, and one that sends
// them is taken at its word. A field that is present but unreadable is a
// malformed request, not a silent fall back to port 0.
func TestConnectReadsOptionalPortFields(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum := findKindBlock(t, snapshot.Blocks, "sum")
	scope := findKindBlock(t, snapshot.Blocks, "scope")

	omitted := request(t, server, http.MethodPost, "/flows/1/connections", url.Values{
		"source_id": {strconv.FormatInt(sum.ID, 10)},
		"target_id": {strconv.FormatInt(scope.ID, 10)},
	})
	if !strings.Contains(omitted.Body.String(), "already connected") {
		t.Fatalf("omitted ports did not reach port 0: %s", omitted.Body.String())
	}

	named := request(t, server, http.MethodPost, "/flows/1/connections", url.Values{
		"source_id":   {strconv.FormatInt(sum.ID, 10)},
		"target_id":   {strconv.FormatInt(scope.ID, 10)},
		"target_port": {"2"},
	})
	if !strings.Contains(named.Body.String(), "has no input port 2") {
		t.Fatalf("named port was not passed through: %s", named.Body.String())
	}

	malformed := request(t, server, http.MethodPost, "/flows/1/connections", url.Values{
		"source_id":   {strconv.FormatInt(sum.ID, 10)},
		"target_id":   {strconv.FormatInt(scope.ID, 10)},
		"target_port": {"left"},
	})
	if !strings.Contains(malformed.Body.String(), "choose an output and an input to connect") {
		t.Fatalf("malformed port was not refused: %s", malformed.Body.String())
	}
}

func TestConnectPersistsAndRendersANonzeroTargetPort(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := snapshot.Flow.ID
	_, sourceID, err := service.AddBlock(ctx, flowID, studio.BlockConstant, studio.Point{X: 120, Y: 720})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, sumID, err := service.AddBlock(ctx, flowID, studio.BlockSum, studio.Point{X: 420, Y: 720})
	if err != nil {
		t.Fatal(err)
	}
	sum := blockByID(snapshot.Blocks, sumID)
	if _, err := service.UpdateBlock(ctx, sumID, studio.BlockUpdate{
		Name:       sum.Name,
		Parameters: map[string]string{"signs": "+-"},
	}); err != nil {
		t.Fatal(err)
	}

	response := request(t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/connections",
		url.Values{
			"source_id":   {strconv.FormatInt(sourceID, 10)},
			"source_port": {"0"},
			"target_id":   {strconv.FormatInt(sumID, 10)},
			"target_port": {"1"},
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	snapshot, err = service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, connection := range snapshot.Connections {
		if connection.SourceID == sourceID && connection.TargetID == sumID {
			found = true
			if connection.SourcePort != 0 || connection.TargetPort != 1 {
				t.Fatalf("persisted ports = %d -> %d, want 0 -> 1", connection.SourcePort, connection.TargetPort)
			}
		}
	}
	if !found {
		t.Fatal("nonzero-port connection was not persisted")
	}

	body := response.Body.String()
	for _, expected := range []string{
		fmt.Sprintf(`data-edge-source="%d" data-edge-source-port="0"`, sourceID),
		fmt.Sprintf(`data-edge-target="%d" data-edge-target-port="1"`, sumID),
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("workbench does not contain %q", expected)
		}
	}

	if _, err := service.Connect(ctx, flowID, studio.Wire{
		SourceID: sourceID, TargetID: sumID, TargetPort: 0,
	}); err != nil {
		t.Fatal(err)
	}
	body = request(t, server, http.MethodGet,
		"/flows/"+strconv.FormatInt(flowID, 10)+"/workbench?selected="+strconv.FormatInt(sourceID, 10), nil,
	).Body.String()
	for _, expected := range []string{
		"input &#43; (port 1) ← output port 1",
		"input - (port 2) ← output port 1",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("source inspector does not contain %q", expected)
		}
	}
}

func TestWorkbenchRendersPortIdentityLabelsAndInspectorNames(t *testing.T) {
	server, service := openTestServer(t)
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sum := findKindBlock(t, snapshot.Blocks, "sum")
	if _, err := service.UpdateBlock(context.Background(), sum.ID, studio.BlockUpdate{
		Name:       sum.Name,
		Parameters: map[string]string{"signs": "+-"},
	}); err != nil {
		t.Fatal(err)
	}

	body := request(t, server, http.MethodGet,
		"/flows/1/workbench?selected="+strconv.FormatInt(sum.ID, 10), nil,
	).Body.String()
	for _, expected := range []string{
		fmt.Sprintf(`data-input-block="%d" data-input-port="0"`, sum.ID),
		fmt.Sprintf(`data-input-block="%d" data-input-port="1"`, sum.ID),
		`<span class="port-label" aria-hidden="true">&#43;</span>`,
		`<span class="port-label" aria-hidden="true">-</span>`,
		fmt.Sprintf(`data-edge-target="%d" data-edge-target-port="0"`, sum.ID),
		"input &#43; (port 1)",
		"input - (port 2)",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("workbench does not contain %q", expected)
		}
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
	for _, expected := range []string{
		"trend-chart", "Temperature", "controlsys", "Settling",
		"SIMULATION FIDELITY", "Batch LTI · Lsim",
		"Base step", "0.1 s", "piecewise constant",
		"Up to 5,000 samples and 16 plotted channels per run.",
		`data-series-toggle=`, `data-series-path=`,
		`href="/flows/1/results.json"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("body does not contain %q", expected)
		}
	}
}

func TestStaticAssetsAreEmbedded(t *testing.T) {
	server, _ := openTestServer(t)
	for _, path := range []string{
		"/assets/app.css", "/assets/menu.js", "/assets/tabs.js",
		"/assets/register.css", "/assets/register.js", "/assets/tokens.css",
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

// The canvas modules sit a directory down. `go:embed static/*` matches the
// directory and embeds its subtree, but nothing in the build fails if it
// stops doing so — the binary would simply serve 404s for every script the
// workbench needs, which no other test would notice.
func TestCanvasModulesAreEmbedded(t *testing.T) {
	server, _ := openTestServer(t)
	for _, path := range []string{
		"/assets/js/main.js", "/assets/js/dom.js", "/assets/js/geometry.js",
		"/assets/js/viewport.js", "/assets/js/selection.js", "/assets/js/dragging.js",
		"/assets/js/wiring.js", "/assets/js/shell.js", "/assets/js/shortcuts.js",
		"/assets/js/contextmenu.js", "/assets/js/input.js", "/assets/js/reapply.js",
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

// Every module main.js pulls in has to be served, so a page that loads only
// the entry point has to be able to reach the rest by relative path.
func TestPageLoadsTheCanvasEntryPointAsAModule(t *testing.T) {
	server, _ := openTestServer(t)
	body := request(t, server, http.MethodGet, "/flows/1/workbench", nil).Body.String()
	if strings.Contains(body, "/assets/js/") {
		t.Error("the workbench fragment loads scripts; only the page shell should")
	}
	page := request(t, server, http.MethodGet, "/projects/1/flows/1", nil).Body.String()
	if !strings.Contains(page, `<script type="module" src="/assets/js/main.js"></script>`) {
		t.Error("the page does not load the canvas entry point as a module")
	}
	// menu.js before it, tabs.js after it: the canvas registers against the
	// namespace menu.js defines, and tabs.js layers a second menu region and
	// its own arrow chord over the canvas bindings.
	menu := strings.Index(page, "/assets/menu.js")
	main := strings.Index(page, "/assets/js/main.js")
	tabs := strings.Index(page, "/assets/tabs.js")
	if menu < 0 || main < 0 || tabs < 0 || !(menu < main && main < tabs) {
		t.Errorf("script order is menu=%d main=%d tabs=%d", menu, main, tabs)
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
