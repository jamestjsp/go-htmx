package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestRenameProjectThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := "/projects/" + strconv.FormatInt(workspace.Project.ID, 10) + "/name"

	response := request(t, server, http.MethodPut, path, url.Values{
		"name": {"Cracker unit"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("rename returned a full page instead of the workbench fragment")
	}
	if !strings.Contains(body, "Cracker unit") {
		t.Fatalf("fragment does not carry the new name: %s", body)
	}
	renamed, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Project.Name != "Cracker unit" {
		t.Fatalf("stored name = %q", renamed.Project.Name)
	}

	t.Run("a blank name is refused and changes nothing", func(t *testing.T) {
		refusal := request(t, server, http.MethodPut, path, url.Values{"name": {"   "}})
		if refusal.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
		}
		if !strings.Contains(refusal.Body.String(), "project name is required") {
			t.Fatalf("body = %q", refusal.Body.String())
		}
		after, err := service.CurrentWorkspace(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if after.Project.Name != "Cracker unit" {
			t.Fatalf("name changed to %q despite the refusal", after.Project.Name)
		}
	})

	t.Run("an unknown project is not found", func(t *testing.T) {
		missing := request(t, server, http.MethodPut, "/projects/999999/name", url.Values{
			"name": {"Nowhere"},
		})
		if missing.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", missing.Code, missing.Body.String())
		}
	})

	t.Run("a malformed id is rejected", func(t *testing.T) {
		malformed := request(t, server, http.MethodPut, "/projects/none/name", url.Values{
			"name": {"Nowhere"},
		})
		if malformed.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", malformed.Code)
		}
	})
}

func TestDeleteProjectThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seededPath := "/projects/" + strconv.FormatInt(seeded.Project.ID, 10)

	// Refused first, while it is the only project there is.
	refusal := request(t, server, http.MethodDelete, seededPath, nil)
	if refusal.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
	}
	if !strings.Contains(refusal.Body.String(), "the last project cannot be deleted") {
		t.Fatalf("body = %q", refusal.Body.String())
	}
	if _, err := service.ProjectWorkspace(ctx, seeded.Project.ID); err != nil {
		t.Fatalf("the refused project no longer opens: %v", err)
	}

	second, err := service.CreateProject(ctx, "Operations")
	if err != nil {
		t.Fatal(err)
	}
	secondPath := "/projects/" + strconv.FormatInt(second.Project.ID, 10)

	htmx := requestHX(t, server, http.MethodDelete, secondPath, nil)
	if htmx.Code != http.StatusNoContent {
		t.Fatalf("htmx status = %d, body = %s", htmx.Code, htmx.Body.String())
	}
	if location := htmx.Header().Get("HX-Redirect"); location != "/" {
		t.Fatalf("HX-Redirect = %q", location)
	}
	if _, err := service.ProjectWorkspace(ctx, second.Project.ID); err == nil {
		t.Fatal("the deleted project still opens")
	}

	t.Run("a plain caller is redirected to the register", func(t *testing.T) {
		third, err := service.CreateProject(ctx, "Utilities")
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, server, http.MethodDelete,
			"/projects/"+strconv.FormatInt(third.Project.ID, 10), nil,
		)
		if response.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
		if location := response.Header().Get("Location"); location != "/" {
			t.Fatalf("Location = %q", location)
		}
	})

	t.Run("an unknown project is not found", func(t *testing.T) {
		missing := request(t, server, http.MethodDelete, "/projects/999999", nil)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", missing.Code, missing.Body.String())
		}
	})
}

func TestDuplicateFlowThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := workspace.Snapshot.Flow
	blocks := len(workspace.Snapshot.Blocks)
	connections := len(workspace.Snapshot.Connections)

	response := request(t, server, http.MethodPost,
		"/flows/"+strconv.FormatInt(source.ID, 10)+"/duplicate", nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "<!doctype html>") {
		t.Fatal("duplicate returned a full page instead of the workbench fragment")
	}

	order := projectFlowIDs(t, service, source.ProjectID)
	if len(order) != 2 || order[0] != source.ID {
		t.Fatalf("tab strip = %v, want the source first and its copy second", order)
	}
	copyID := order[1]
	// The copy is what the caller is left looking at.
	if opened := openFlowID(t, response.Body.String()); opened != copyID {
		t.Fatalf("workbench opened flow %d, want the copy %d", opened, copyID)
	}
	copied, err := service.Snapshot(ctx, copyID)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Flow.Name != source.Name+" copy" {
		t.Fatalf("copy name = %q", copied.Flow.Name)
	}
	if len(copied.Blocks) != blocks || len(copied.Connections) != connections {
		t.Fatalf("copy has %d blocks and %d connections, want %d and %d",
			len(copied.Blocks), len(copied.Connections), blocks, connections)
	}

	t.Run("an unknown flowsheet is not found", func(t *testing.T) {
		missing := request(t, server, http.MethodPost, "/flows/999999/duplicate", nil)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", missing.Code, missing.Body.String())
		}
	})

	t.Run("a malformed id is rejected", func(t *testing.T) {
		malformed := request(t, server, http.MethodPost, "/flows/none/duplicate", nil)
		if malformed.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", malformed.Code)
		}
	})
}

func TestDeleteFlowThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := workspace.Project.ID
	first := workspace.Snapshot.Flow.ID

	// The seeded project holds one sheet, so deleting it is refused.
	refusal := request(t, server, http.MethodDelete, "/flows/"+strconv.FormatInt(first, 10), nil)
	if refusal.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
	}
	if !strings.Contains(refusal.Body.String(), "at least one flowsheet") {
		t.Fatalf("body = %q", refusal.Body.String())
	}
	if _, err := service.Snapshot(ctx, first); err != nil {
		t.Fatalf("the refused flowsheet is gone: %v", err)
	}

	second := addFlow(t, service, projectID, "Startup")
	third := addFlow(t, service, projectID, "Shutdown")
	if order := projectFlowIDs(t, service, projectID); len(order) != 3 {
		t.Fatalf("tab strip = %v, want three sheets", order)
	}

	// Deleting the middle tab lands on its left neighbour.
	middle := request(t, server, http.MethodDelete, "/flows/"+strconv.FormatInt(second, 10), nil)
	if middle.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", middle.Code, middle.Body.String())
	}
	if strings.Contains(middle.Body.String(), "<!doctype html>") {
		t.Fatal("delete returned a full page instead of the workbench fragment")
	}
	if opened := openFlowID(t, middle.Body.String()); opened != first {
		t.Fatalf("workbench opened flow %d, want the left neighbour %d", opened, first)
	}

	// Deleting the first tab lands on its right neighbour, there being no left.
	leading := request(t, server, http.MethodDelete, "/flows/"+strconv.FormatInt(first, 10), nil)
	if leading.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", leading.Code, leading.Body.String())
	}
	if opened := openFlowID(t, leading.Body.String()); opened != third {
		t.Fatalf("workbench opened flow %d, want the right neighbour %d", opened, third)
	}
	if order := projectFlowIDs(t, service, projectID); len(order) != 1 || order[0] != third {
		t.Fatalf("tab strip = %v, want only %d", order, third)
	}

	t.Run("an unknown flowsheet is not found", func(t *testing.T) {
		missing := request(t, server, http.MethodDelete, "/flows/999999", nil)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", missing.Code, missing.Body.String())
		}
	})

	t.Run("a malformed id is rejected", func(t *testing.T) {
		malformed := request(t, server, http.MethodDelete, "/flows/none", nil)
		if malformed.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", malformed.Code)
		}
	})
}

func TestReorderFlowsThroughHTTP(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := workspace.Project.ID
	first := workspace.Snapshot.Flow.ID
	second := addFlow(t, service, projectID, "Startup")
	third := addFlow(t, service, projectID, "Shutdown")
	path := "/projects/" + strconv.FormatInt(projectID, 10) + "/flows/order"

	values := url.Values{}
	for _, flowID := range []int64{third, first, second} {
		values.Add("id", strconv.FormatInt(flowID, 10))
	}
	response := request(t, server, http.MethodPatch, path, values)
	// A drag the client has already drawn: nothing to swap in, and nothing
	// that could move the caller onto another sheet.
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Body.Len() != 0 {
		t.Fatalf("body = %q, want an empty response", response.Body.String())
	}
	if order := projectFlowIDs(t, service, projectID); !sameOrder(order, []int64{third, first, second}) {
		t.Fatalf("tab strip = %v, want %v", order, []int64{third, first, second})
	}

	t.Run("a short list is refused and changes nothing", func(t *testing.T) {
		short := url.Values{}
		short.Add("id", strconv.FormatInt(first, 10))
		refusal := request(t, server, http.MethodPatch, path, short)
		if refusal.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
		}
		if !strings.Contains(refusal.Body.String(), "exactly once") {
			t.Fatalf("body = %q", refusal.Body.String())
		}
		if order := projectFlowIDs(t, service, projectID); !sameOrder(order, []int64{third, first, second}) {
			t.Fatalf("tab strip = %v, want the order left untouched", order)
		}
	})

	t.Run("a repeated id is refused", func(t *testing.T) {
		repeated := url.Values{}
		for _, flowID := range []int64{first, first, second} {
			repeated.Add("id", strconv.FormatInt(flowID, 10))
		}
		refusal := request(t, server, http.MethodPatch, path, repeated)
		if refusal.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
		}
		if order := projectFlowIDs(t, service, projectID); !sameOrder(order, []int64{third, first, second}) {
			t.Fatalf("tab strip = %v, want the order left untouched", order)
		}
	})

	t.Run("a sheet from another project is refused", func(t *testing.T) {
		other, err := service.CreateProject(ctx, "Operations")
		if err != nil {
			t.Fatal(err)
		}
		foreign := url.Values{}
		for _, flowID := range []int64{first, second, other.Snapshot.Flow.ID} {
			foreign.Add("id", strconv.FormatInt(flowID, 10))
		}
		refusal := request(t, server, http.MethodPatch, path, foreign)
		if refusal.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
		}
		if order := projectFlowIDs(t, service, projectID); !sameOrder(order, []int64{third, first, second}) {
			t.Fatalf("tab strip = %v, want the order left untouched", order)
		}
	})

	t.Run("a malformed id is rejected", func(t *testing.T) {
		malformed := url.Values{}
		malformed.Add("id", strconv.FormatInt(first, 10))
		malformed.Add("id", "none")
		malformed.Add("id", strconv.FormatInt(second, 10))
		refusal := request(t, server, http.MethodPatch, path, malformed)
		if refusal.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", refusal.Code, refusal.Body.String())
		}
		if order := projectFlowIDs(t, service, projectID); !sameOrder(order, []int64{third, first, second}) {
			t.Fatalf("tab strip = %v, want the order left untouched", order)
		}
	})

	t.Run("an unknown project is not found", func(t *testing.T) {
		missing := request(t, server, http.MethodPatch, "/projects/999999/flows/order", values)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", missing.Code, missing.Body.String())
		}
	})
}

// TestWorkbenchFragmentCarriesTheProject pins the behaviour the tab strip is
// swapped in by: the fragment GET /flows/{id}/workbench returns already knows
// the whole project, so switching sheets can replace #workbench alone.
func TestWorkbenchFragmentCarriesTheProject(t *testing.T) {
	server, service := openTestServer(t)
	workspace, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	projectID := workspace.Project.ID
	first := workspace.Snapshot.Flow.ID
	second := addFlow(t, service, projectID, "Startup")
	third := addFlow(t, service, projectID, "Shutdown")

	response := request(t, server, http.MethodGet,
		"/flows/"+strconv.FormatInt(second, 10)+"/workbench", nil,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("the workbench route returned a full page")
	}
	if opened := openFlowID(t, body); opened != second {
		t.Fatalf("workbench opened flow %d, want %d", opened, second)
	}
	project := strconv.FormatInt(projectID, 10)
	for _, sibling := range []int64{first, second, third} {
		link := "/projects/" + project + "/flows/" + strconv.FormatInt(sibling, 10)
		if !strings.Contains(body, link) {
			t.Errorf("fragment does not link to %s", link)
		}
	}
	for _, name := range []string{"Reactor temperature loop", "Startup", "Shutdown"} {
		if !strings.Contains(body, name) {
			t.Errorf("fragment does not name %q", name)
		}
	}
}

// addFlow appends a sheet to a project's tab strip and reports its id.
func addFlow(t *testing.T, service *studio.Studio, projectID int64, name string) int64 {
	t.Helper()
	workspace, err := service.CreateFlow(context.Background(), projectID, name)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.Snapshot.Flow.ID
}

// projectFlowIDs reads a project's tab strip in the order the strip draws it.
func projectFlowIDs(t *testing.T, service *studio.Studio, projectID int64) []int64 {
	t.Helper()
	workspace, err := service.ProjectWorkspace(context.Background(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(workspace.Flows))
	for _, flow := range workspace.Flows {
		ids = append(ids, flow.ID)
	}
	return ids
}

func sameOrder(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// openFlowID is the flowsheet a rendered workbench is showing, read from the
// data-flow-id the client itself reads to tell one sheet from another.
func openFlowID(t *testing.T, body string) int64 {
	t.Helper()
	const marker = `data-flow-id="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("no %s in the rendered workbench", marker)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatal("unterminated data-flow-id in the rendered workbench")
	}
	flowID, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		t.Fatalf("data-flow-id = %q: %v", rest[:end], err)
	}
	return flowID
}
