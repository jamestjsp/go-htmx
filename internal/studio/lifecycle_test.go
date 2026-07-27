package studio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameProjectRoundTrips(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "rename-project.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := current.Project.ID
	edited := current.Project.UpdatedAt

	renamed, err := service.RenameProject(ctx, projectID, "  Batch studies  ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Project.Name != "Batch studies" {
		t.Fatalf("renamed project = %q", renamed.Project.Name)
	}
	if !renamed.Project.UpdatedAt.After(edited) {
		t.Fatalf("updated_at = %s, want later than %s", renamed.Project.UpdatedAt, edited)
	}
	if len(renamed.Projects) != 1 || renamed.Projects[0].Name != "Batch studies" {
		t.Fatalf("project list = %#v", renamed.Projects)
	}
	// The rename opens the project's first flowsheet, as ProjectWorkspace does.
	if renamed.Snapshot.Flow.ID != current.Snapshot.Flow.ID {
		t.Fatalf("landed on flow %d, want %d", renamed.Snapshot.Flow.ID, current.Snapshot.Flow.ID)
	}

	reread, err := service.ProjectWorkspace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.Project.Name != "Batch studies" {
		t.Fatalf("reread project = %q", reread.Project.Name)
	}
}

func TestRenameProjectRejectsInvalidNames(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		id   int64
		to   string
		want error
	}{
		{name: "blank", id: current.Project.ID, to: "   "},
		{name: "too long", id: current.Project.ID, to: strings.Repeat("x", maxWorkspaceNameLength+1)},
		{name: "unknown project", id: 9999, to: "Batch studies", want: ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.RenameProject(ctx, test.id, test.to)
			if err == nil {
				t.Fatal("rename succeeded")
			}
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("error = %v, want %v", err, test.want)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
		})
	}

	// The longest accepted name proves the limit is inclusive.
	if _, err := service.RenameProject(ctx, current.Project.ID,
		strings.Repeat("x", maxWorkspaceNameLength)); err != nil {
		t.Fatal(err)
	}
	unchanged, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Project.Name != strings.Repeat("x", maxWorkspaceNameLength) {
		t.Fatalf("project name = %q", unchanged.Project.Name)
	}
}

func TestDeleteProjectCascadesToEveryDependentRow(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "delete-project.db"))
	doomed, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The seeded project is the one with blocks and connections; give it a
	// simulation run too, so all five child tables have rows to lose.
	if _, err := service.Run(ctx, doomed.Snapshot.Flow.ID, SimulationRequest{
		Duration:   5,
		SampleTime: 0.1,
	}); err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateFlow(ctx, doomed.Project.ID, "Startup")
	if err != nil {
		t.Fatal(err)
	}
	flowIDs := []int64{doomed.Snapshot.Flow.ID, second.Snapshot.Flow.ID}
	for _, table := range []string{"blocks", "connections", "events", "simulation_runs"} {
		if got := countRows(t, service,
			"SELECT COUNT(*) FROM "+table+" WHERE flow_id = ?", doomed.Snapshot.Flow.ID,
		); got == 0 {
			t.Fatalf("%s is already empty; the cascade would prove nothing", table)
		}
	}

	keeper, err := service.CreateProject(ctx, "Batch studies")
	if err != nil {
		t.Fatal(err)
	}

	remaining, err := service.DeleteProject(ctx, doomed.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Deleting the project you were inside cannot return you to it, so it
	// returns the workspace the application now falls back to.
	if remaining.Project.ID != keeper.Project.ID {
		t.Fatalf("landed on project %d, want the surviving project %d",
			remaining.Project.ID, keeper.Project.ID)
	}
	if remaining.Snapshot.Flow.ProjectID != keeper.Project.ID {
		t.Fatalf("landed on flow of project %d", remaining.Snapshot.Flow.ProjectID)
	}
	if len(remaining.Projects) != 1 {
		t.Fatalf("workspace lists %d projects, want 1", len(remaining.Projects))
	}

	if got := countRows(t, service,
		"SELECT COUNT(*) FROM projects WHERE id = ?", doomed.Project.ID); got != 0 {
		t.Fatalf("project rows = %d, want 0", got)
	}
	if got := countRows(t, service,
		"SELECT COUNT(*) FROM flows WHERE project_id = ?", doomed.Project.ID); got != 0 {
		t.Fatalf("flow rows = %d, want 0", got)
	}
	for _, flowID := range flowIDs {
		for _, table := range []string{"blocks", "connections", "events", "simulation_runs"} {
			if got := countRows(t, service,
				"SELECT COUNT(*) FROM "+table+" WHERE flow_id = ?", flowID,
			); got != 0 {
				t.Fatalf("%s rows for flow %d = %d, want 0", table, flowID, got)
			}
		}
	}
	// Nothing anywhere in the database outlived its parent.
	for _, table := range []string{"blocks", "connections", "events", "simulation_runs"} {
		if got := countRows(t, service,
			"SELECT COUNT(*) FROM "+table+" WHERE flow_id NOT IN (SELECT id FROM flows)",
		); got != 0 {
			t.Fatalf("orphaned %s rows = %d, want 0", table, got)
		}
	}
	if got := countRows(t, service,
		"SELECT COUNT(*) FROM connections WHERE source_id NOT IN (SELECT id FROM blocks) "+
			"OR target_id NOT IN (SELECT id FROM blocks)",
	); got != 0 {
		t.Fatalf("orphaned connection endpoints = %d, want 0", got)
	}

	if _, err := service.ProjectWorkspace(ctx, doomed.Project.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted project workspace error = %v", err)
	}
}

func TestDeleteProjectRefusesTheLastProject(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "last-project.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flows := countRows(t, service, "SELECT COUNT(*) FROM flows")
	blocks := countRows(t, service, "SELECT COUNT(*) FROM blocks")
	connections := countRows(t, service, "SELECT COUNT(*) FROM connections")

	_, err = service.DeleteProject(ctx, current.Project.ID)
	if err == nil {
		t.Fatal("deleted the last project")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want a ValidationError", err)
	}
	if validation.Message == "" {
		t.Fatal("refusal carries no message for the user")
	}

	if got := countRows(t, service, "SELECT COUNT(*) FROM projects"); got != 1 {
		t.Fatalf("projects = %d, want 1", got)
	}
	if got := countRows(t, service, "SELECT COUNT(*) FROM flows"); got != flows {
		t.Fatalf("flows = %d, want %d", got, flows)
	}
	if got := countRows(t, service, "SELECT COUNT(*) FROM blocks"); got != blocks {
		t.Fatalf("blocks = %d, want %d", got, blocks)
	}
	if got := countRows(t, service, "SELECT COUNT(*) FROM connections"); got != connections {
		t.Fatalf("connections = %d, want %d", got, connections)
	}
	after, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Project.ID != current.Project.ID || after.Project.Name != current.Project.Name {
		t.Fatalf("workspace = %#v, want %#v", after.Project, current.Project)
	}
}

func TestDeleteProjectRejectsUnknownID(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	if _, err := service.CreateProject(ctx, "Batch studies"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteProject(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrNotFound)
	}
	if got := countRows(t, service, "SELECT COUNT(*) FROM projects"); got != 2 {
		t.Fatalf("projects = %d, want 2", got)
	}
}

func countRows(t *testing.T, service *Studio, query string, args ...any) int {
	t.Helper()
	var count int
	if err := service.db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
