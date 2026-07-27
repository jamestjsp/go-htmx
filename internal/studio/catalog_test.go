package studio

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBlockLibraryDefinitionsOwnDefaultsAndEditors(t *testing.T) {
	library := BlockLibrary()
	if len(library) != len(blockOrder) {
		t.Fatalf("library size = %d, want %d", len(library), len(blockOrder))
	}
	for _, definition := range library {
		if !definition.Kind.Valid() {
			t.Fatalf("%q is not valid", definition.Kind)
		}
		block := Block{Kind: definition.Kind, Parameters: defaultParameters(definition.Kind)}
		for _, field := range block.EditorFields() {
			if field.Name == "" || field.Label == "" || field.Value == "" {
				t.Fatalf("%s has incomplete editor field %#v", definition.Kind, field)
			}
		}
		if block.Summary() == "" {
			t.Fatalf("%s has no summary", definition.Kind)
		}
	}
}

func TestTransferFunctionUpdateParsesAndValidatesCoefficients(t *testing.T) {
	block := Block{
		Kind:       BlockTransfer,
		Name:       "Plant",
		Parameters: defaultParameters(BlockTransfer),
	}
	updated, err := validateBlockUpdate(block, BlockUpdate{
		Name: "Second-order plant",
		Parameters: map[string]string{
			"numerator":   "2, 1",
			"denominator": "1 3 2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := coefficientsText(updated.Parameters.Numerator); got != "2, 1" {
		t.Fatalf("numerator = %q", got)
	}

	_, err = validateBlockUpdate(block, BlockUpdate{
		Name: "Improper",
		Parameters: map[string]string{
			"numerator":   "1, 2, 3",
			"denominator": "1, 2",
		},
	})
	if err == nil {
		t.Fatal("improper transfer function succeeded")
	}
}

func TestOpenMigratesLegacyBlockParameters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE flows (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE blocks (
			id INTEGER PRIMARY KEY,
			flow_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			x INTEGER NOT NULL,
			y INTEGER NOT NULL,
			amplitude REAL NOT NULL DEFAULT 0,
			gain REAL NOT NULL DEFAULT 0,
			time_constant REAL NOT NULL DEFAULT 0
		);
		INSERT INTO flows(id, name, created_at, updated_at)
		VALUES(1, 'Legacy', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO blocks(flow_id, kind, name, x, y, amplitude, gain, time_constant)
		VALUES(1, 'lag', 'Legacy lag', 20, 20, 0, 0, 4.5);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Blocks[0].Parameters.TimeConstant; got != 4.5 {
		t.Fatalf("time constant = %g, want 4.5", got)
	}
	if snapshot.Flow.ProjectID == 0 {
		t.Fatal("legacy flow has no project")
	}
	var projectCount int
	var projectName string
	if err := service.db.QueryRowContext(ctx,
		"SELECT COUNT(*), MIN(name) FROM projects",
	).Scan(&projectCount, &projectName); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 || projectName != defaultProjectName {
		t.Fatalf("projects = %d, %q", projectCount, projectName)
	}
	projectID := snapshot.Flow.ProjectID
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	snapshot, err = reopened.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Flow.ProjectID != projectID {
		t.Fatalf("project id after reopen = %d, want %d", snapshot.Flow.ProjectID, projectID)
	}
	if err := reopened.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM projects",
	).Scan(&projectCount); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 {
		t.Fatalf("project count after reopen = %d, want 1", projectCount)
	}
}

// A database written before projects, model revisions, and tab order existed
// takes every migration in one Open. A fresh database gets `position` from
// CREATE TABLE and never runs the backfill, so this is its only coverage.
func TestOpenBackfillsLegacyFlowPositions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-order.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE flows (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		INSERT INTO flows(id, name, created_at, updated_at) VALUES
			(1, 'zeta loop', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(2, 'Alpha loop', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z'),
			(3, 'beta loop', '2026-01-03T00:00:00Z', '2026-01-03T00:00:00Z');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flowNames(workspace.Flows), []string{"Alpha loop", "beta loop", "zeta loop"}; !slices.Equal(got, want) {
		t.Fatalf("migrated order = %v, want %v", got, want)
	}
	if got, want := flowPositions(t, service, workspace.Project.ID), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}

	// A hand-ordered tab strip must survive the next Open: the migration
	// short-circuits once the column exists rather than re-sorting by name.
	if _, err := service.db.ExecContext(ctx,
		"UPDATE flows SET position = CASE id WHEN 1 THEN 0 WHEN 2 THEN 1 ELSE 2 END",
	); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	workspace, err = reopened.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flowNames(workspace.Flows), []string{"zeta loop", "Alpha loop", "beta loop"}; !slices.Equal(got, want) {
		t.Fatalf("order after reopen = %v, want %v", got, want)
	}
}

// The backfill windows per project, so two projects each start at zero rather
// than sharing one running count.
func TestOpenBackfillsFlowPositionsPerProject(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "project-order.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE flows (
			id INTEGER PRIMARY KEY,
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			model_updated_at TEXT NOT NULL
		);
		INSERT INTO projects(id, name, created_at, updated_at) VALUES
			(1, 'Loops', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(2, 'Studies', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO flows(id, project_id, name, created_at, updated_at, model_updated_at) VALUES
			(1, 1, 'zeta', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(2, 2, 'Yankee', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(3, 1, 'Alpha', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
			(4, 2, 'alpha', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	tests := []struct {
		projectID int64
		want      []string
	}{
		{projectID: 1, want: []string{"Alpha", "zeta"}},
		{projectID: 2, want: []string{"alpha", "Yankee"}},
	}
	for _, test := range tests {
		workspace, err := service.ProjectWorkspace(ctx, test.projectID)
		if err != nil {
			t.Fatal(err)
		}
		if got := flowNames(workspace.Flows); !slices.Equal(got, test.want) {
			t.Fatalf("project %d order = %v, want %v", test.projectID, got, test.want)
		}
		if got, want := flowPositions(t, service, test.projectID), []int{0, 1}; !slices.Equal(got, want) {
			t.Fatalf("project %d positions = %v, want %v", test.projectID, got, want)
		}
	}
}

// Deleting a project will lean on ON DELETE CASCADE, so foreign keys must be
// on for every connection, not only the one the schema statement ran on.
func TestOpenEnforcesForeignKeys(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "cascade.db"))
	var enforced int
	if err := service.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enforced); err != nil {
		t.Fatal(err)
	}
	if enforced != 1 {
		t.Fatalf("foreign_keys = %d, want 1", enforced)
	}
}

func flowNames(flows []Flow) []string {
	names := make([]string, 0, len(flows))
	for _, flow := range flows {
		names = append(names, flow.Name)
	}
	return names
}

func flowPositions(t *testing.T, service *Studio, projectID int64) []int {
	t.Helper()
	rows, err := service.db.QueryContext(context.Background(),
		"SELECT position FROM flows WHERE project_id = ? ORDER BY position, id", projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var positions []int
	for rows.Next() {
		var position int
		if err := rows.Scan(&position); err != nil {
			t.Fatal(err)
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return positions
}
