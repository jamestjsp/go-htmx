package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// renderedTab is one tab as the browser receives it, so the assertions below
// read the markup rather than the view model that produced it.
type renderedTab struct {
	id       string
	name     string
	href     string
	fragment string
	pushURL  string
	active   bool
	needsRun bool
}

var (
	tabPattern     = regexp.MustCompile(`(?s)<a\b([^>]*\bdata-flow-tab="(\d+)"[^>]*)>(.*?)</a>`)
	jumpPattern    = regexp.MustCompile(`<a\b([^>]*\bdata-flow-jump="\d+"[^>]*)>`)
	tabNamePattern = regexp.MustCompile(`<span class="flow-tab-name">([^<]*)</span>`)
)

func parseTabs(t *testing.T, body string) []renderedTab {
	t.Helper()
	var tabs []renderedTab
	for _, match := range tabPattern.FindAllStringSubmatch(body, -1) {
		attributes, inner := match[1], match[3]
		name := tabNamePattern.FindStringSubmatch(inner)
		if name == nil {
			t.Fatalf("tab %s renders no name: %s", match[2], inner)
		}
		tabs = append(tabs, renderedTab{
			id:       match[2],
			name:     name[1],
			href:     attributeValue(attributes, "href"),
			fragment: attributeValue(attributes, "hx-get"),
			pushURL:  attributeValue(attributes, "hx-push-url"),
			active:   strings.Contains(attributes, `aria-current="page"`),
			needsRun: strings.Contains(inner, "flow-tab-dot"),
		})
	}
	if len(tabs) == 0 {
		t.Fatal("no flowsheet tabs in the rendered workbench")
	}
	return tabs
}

func attributeValue(attributes, name string) string {
	match := regexp.MustCompile(name + `="([^"]*)"`).FindStringSubmatch(attributes)
	if match == nil {
		return ""
	}
	return match[1]
}

func tabNames(tabs []renderedTab) []string {
	names := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		names = append(names, tab.name)
	}
	return names
}

// TestTabStripRendersEverySheetInPositionOrder covers the strip end to end:
// the order it draws, the single active tab, the amber dot, and the two
// addresses each tab carries.
func TestTabStripRendersEverySheetInPositionOrder(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()

	if _, err := service.CreateFlow(ctx, 1, "Column"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateFlow(ctx, 1, "Dryer"); err != nil {
		t.Fatal(err)
	}
	// The + button submits no name at all, which is how a sheet gets a
	// generated one.
	created := request(t, server, http.MethodPost, "/projects/1/flows", url.Values{})
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	opened := created.Header().Get("Location")

	workspace, err := service.ProjectWorkspace(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Flows) != 4 {
		t.Fatalf("project holds %d flowsheets, want 4", len(workspace.Flows))
	}
	if opened != fmt.Sprintf("/projects/1/flows/%d", workspace.Flows[3].ID) {
		t.Fatalf("+ opened %q, want the sheet it created", opened)
	}
	if workspace.Flows[3].Name != "Flowsheet 1" {
		t.Fatalf("generated name = %q, want %q", workspace.Flows[3].Name, "Flowsheet 1")
	}

	// Positions, not ids and not names: reorder to an order neither would
	// produce, so a strip sorted by anything else fails here.
	seeded, column, dryer, generated := workspace.Flows[0], workspace.Flows[1], workspace.Flows[2], workspace.Flows[3]
	if _, err := service.ReorderFlows(ctx, 1, []int64{
		dryer.ID, seeded.ID, generated.ID, column.ID,
	}); err != nil {
		t.Fatal(err)
	}

	// The seeded sheet is the only one with a simulation behind it, so it is
	// the only one that should lose its amber dot.
	ran := request(t, server, http.MethodPost, fmt.Sprintf("/flows/%d/simulations", seeded.ID), url.Values{
		"duration":    {"5"},
		"sample_time": {"0.1"},
	})
	if ran.Code != http.StatusOK {
		t.Fatalf("run status = %d, body = %s", ran.Code, ran.Body.String())
	}

	response := request(t, server, http.MethodGet, fmt.Sprintf("/projects/1/flows/%d", column.ID), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	tabs := parseTabs(t, body)

	want := []string{"Dryer", "Reactor temperature loop", "Flowsheet 1", "Column"}
	if got := tabNames(tabs); !equalStrings(got, want) {
		t.Fatalf("tab order = %v, want %v", got, want)
	}

	var active []string
	for _, tab := range tabs {
		if tab.active {
			active = append(active, tab.name)
		}
	}
	if len(active) != 1 || active[0] != "Column" {
		t.Fatalf("tabs marked aria-current = %v, want exactly [Column]", active)
	}
	// The jump list marks the open sheet the same way and just as singly.
	lit := 0
	for _, match := range jumpPattern.FindAllStringSubmatch(body, -1) {
		if strings.Contains(match[1], `aria-current="page"`) {
			lit++
		}
	}
	if lit != 1 {
		t.Fatalf("jump list marks %d sheets as current, want exactly 1", lit)
	}

	for _, tab := range tabs {
		wantDot := tab.name != "Reactor temperature loop"
		if tab.needsRun != wantDot {
			t.Errorf("%q amber dot = %t, want %t", tab.name, tab.needsRun, wantDot)
		}
		// The pushed address is the canonical page, never the fragment: a
		// pushed fragment URL reloads as a bare <main> with no stylesheet.
		wantPush := fmt.Sprintf("/projects/1/flows/%s", tab.id)
		if tab.pushURL != wantPush {
			t.Errorf("%q pushes %q, want %q", tab.name, tab.pushURL, wantPush)
		}
		if tab.href != wantPush {
			t.Errorf("%q links to %q, want %q", tab.name, tab.href, wantPush)
		}
		wantFragment := fmt.Sprintf("/flows/%s/workbench", tab.id)
		if tab.fragment != wantFragment {
			t.Errorf("%q fetches %q, want %q", tab.name, tab.fragment, wantFragment)
		}
	}
	if strings.Contains(body, `hx-push-url="true"`) {
		t.Error(`the strip pushes hx-push-url="true", which would push the fragment URL`)
	}

	// The right end counts the sheets and lists every one of them.
	tools := body[strings.Index(body, `<details class="tab-jump">`):]
	if !strings.Contains(tools[:200], "<b>4</b>") {
		t.Errorf("sheet count missing from the jump control: %s", tools[:200])
	}
	if listed := strings.Count(body, "data-flow-jump="); listed != len(tabs) {
		t.Errorf("jump list holds %d sheets, want %d", listed, len(tabs))
	}
}

// TestSwappedFragmentCarriesTheOpenProjectsTabs pins what a tab click swaps
// in: the fragment brings its own project's strip, and only that project's.
func TestSwappedFragmentCarriesTheOpenProjectsTabs(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()

	second, err := service.CreateProject(ctx, "Compressor station")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateFlow(ctx, second.Project.ID, "Anti-surge"); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.ProjectWorkspace(ctx, second.Project.ID)
	if err != nil {
		t.Fatal(err)
	}

	response := requestHX(t, server, http.MethodGet,
		fmt.Sprintf("/flows/%d/workbench", workspace.Flows[1].ID), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("the tab target returned a full page instead of the workbench fragment")
	}
	tabs := parseTabs(t, body)
	if got := tabNames(tabs); !equalStrings(got, []string{workspace.Flows[0].Name, "Anti-surge"}) {
		t.Fatalf("fragment tabs = %v, want the second project's own sheets", got)
	}
	if !tabs[1].active || tabs[0].active {
		t.Fatalf("active tab = %v, want only Anti-surge", tabNames(tabs))
	}
	if strings.Contains(body, "Reactor temperature loop") {
		t.Error("the fragment leaks the other project's flowsheets into the strip")
	}
}

func equalStrings(got, want []string) bool {
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
