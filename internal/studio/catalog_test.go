package studio

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
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

// Transfer-function properness is a cross-field rule — it compares numerator
// and denominator length, so it cannot live on either field's own bound. It
// belongs to BlockTransfer's validate hook, and validateParameters is the one
// place both validateBlockUpdate (the editor path) and compileFlow (the
// compile path) call to reach it. This test proves the move to per-definition
// hooks kept both callers refusing the same improper model, not just one.
func TestImproperTransferFunctionRefusedByBothEditorAndCompilePaths(t *testing.T) {
	numerator, denominator := "1, 2, 3", "1, 2"
	const wantMessage = "transfer function must be proper"

	block := Block{Kind: BlockTransfer, Name: "Plant", Parameters: defaultParameters(BlockTransfer)}
	_, err := validateBlockUpdate(block, BlockUpdate{
		Name: "Plant",
		Parameters: map[string]string{
			"numerator":   numerator,
			"denominator": denominator,
		},
	})
	if err == nil || err.Error() != wantMessage {
		t.Fatalf("validateBlockUpdate error = %v, want %q", err, wantMessage)
	}

	improperNumerator, parseErr := parseCoefficients(numerator)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	improperDenominator, parseErr := parseCoefficients(denominator)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	blocks := []Block{
		{ID: 1, Kind: BlockConstant, Name: "Input", Parameters: Parameters{Value: 1}},
		{ID: 2, Kind: BlockTransfer, Name: "Plant", Parameters: Parameters{
			Numerator: improperNumerator, Denominator: improperDenominator,
		}},
		{ID: 3, Kind: BlockScope, Name: "Output"},
	}
	connections := []Connection{
		{ID: 1, SourceID: 1, TargetID: 2},
		{ID: 2, SourceID: 2, TargetID: 3},
	}
	if _, err := compileFlow(blocks, connections); err == nil ||
		err.Error() != "Plant: "+wantMessage {
		t.Fatalf("compileFlow error = %v, want %q", err, "Plant: "+wantMessage)
	}
}

// updateWithOverride starts from a kind's own defaults, rendered through
// EditorFields the way the UI would echo them back, then swaps in one bad
// value. Every other field stays valid, so the returned error can only have
// come from the field under test.
func updateWithOverride(t *testing.T, kind BlockKind, field, raw string) error {
	t.Helper()
	block := Block{Kind: kind, Name: "Block", Parameters: defaultParameters(kind)}
	values := make(map[string]string)
	for _, editorField := range block.EditorFields() {
		values[editorField.Name] = editorField.Value
	}
	values[field] = raw
	_, err := validateBlockUpdate(block, BlockUpdate{Name: "Block", Parameters: values})
	return err
}

// These wordings moved from the setParameter/parameterText switches into
// per-field closures; this test is the guard that the move didn't paraphrase
// them along the way.
func TestValidateBlockUpdateFieldErrorWordingIsUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		kind    BlockKind
		field   string
		raw     string
		wantErr string
	}{
		{"malformed number keeps underscores as spaces", BlockSource, "initial_value", "abc", "initial value must be a number"},
		{"malformed Padé order", BlockDelay, "approximation", "abc", "Padé order must be a whole number"},
		{"malformed numerator coefficients", BlockTransfer, "numerator", "one, two", "numerator coefficients must be comma or space separated numbers"},
		{"malformed denominator coefficients", BlockTransfer, "denominator", "?", "denominator coefficients must be comma or space separated numbers"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := updateWithOverride(t, test.kind, test.field, test.raw)
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// The Sine block's phase field used to state its range twice and
// disagree with itself: the editor's Min/Max attributes said -1000..1000,
// but the old validateParameters switch enforced -10000..10000 against it.
// Unifying the bound onto the field (task 4) kept the editor's frozen
// attribute value and tightened enforcement to match it, since a value the
// UI never let you type could still reach the server as -10000..10000 was
// no tighter. This pins that resolved number down: if it ever needs to
// widen again, this test forces that to be a deliberate edit, not a silent
// drift back to the old inconsistency.
func TestValidateBlockUpdateRejectsPhaseAboveItsBound(t *testing.T) {
	err := updateWithOverride(t, BlockSine, "phase", "5000")
	if want := "phase must be between -1000 and 1000"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestValidateBlockUpdateRequiresEveryDefinedField(t *testing.T) {
	block := Block{Kind: BlockGain, Name: "Valve", Parameters: defaultParameters(BlockGain)}
	_, err := validateBlockUpdate(block, BlockUpdate{Name: "Valve", Parameters: map[string]string{}})
	if want := "gain is required"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestValidateBlockUpdateStripsSpacesFromSigns(t *testing.T) {
	block := Block{Kind: BlockSum, Name: "Balance", Parameters: defaultParameters(BlockSum)}
	updated, err := validateBlockUpdate(block, BlockUpdate{
		Name:       "Balance",
		Parameters: map[string]string{"signs": " + - + "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := updated.Parameters.Signs, "+-+"; got != want {
		t.Fatalf("signs = %q, want %q", got, want)
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

// ensureLegacyBlockParameters must run after ensureParametersJSON has
// guaranteed parameters_json exists, backfilling it from the scalar columns
// for rows the column's own DEFAULT ” left empty. This fixture's blocks
// table already carries parameters_json — unlike
// TestOpenMigratesLegacyBlockParameters's, where ensureParametersJSON has to
// add the column first — so this is the ordering constraint's own coverage,
// not the same path exercised a second time.
func TestOpenBackfillsBlockParametersFromLegacyColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre-json.db")
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
			time_constant REAL NOT NULL DEFAULT 0,
			parameters_json TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO flows(id, name, created_at, updated_at)
		VALUES(1, 'Legacy', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO blocks(flow_id, kind, name, x, y, amplitude, gain, time_constant) VALUES
			(1, 'source', 'Feed', 60, 80, 1.75, 0, 0),
			(1, 'gain', 'Valve', 300, 80, 0, 2.4, 0),
			(1, 'lag', 'Reactor', 540, 80, 0, 0, 4.5),
			(1, 'sum', 'Balance', 780, 80, 0, 0, 0);
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
	if got := blockNamed(t, snapshot.Blocks, "Feed").Parameters.Amplitude; got != 1.75 {
		t.Fatalf("amplitude = %v, want 1.75", got)
	}
	if got := blockNamed(t, snapshot.Blocks, "Valve").Parameters.Gain; got != 2.4 {
		t.Fatalf("gain = %v, want 2.4", got)
	}
	if got := blockNamed(t, snapshot.Blocks, "Reactor").Parameters.TimeConstant; got != 4.5 {
		t.Fatalf("time constant = %v, want 4.5", got)
	}
	// A kind outside decodeParameters' old switch — nothing in the legacy
	// columns ever described a sum block — backfills to its catalog defaults,
	// the same value a fresh sum block gets today.
	if got, want := blockNamed(t, snapshot.Blocks, "Balance").Parameters, defaultParameters(BlockSum); !reflect.DeepEqual(got, want) {
		t.Fatalf("sum defaults = %#v, want %#v", got, want)
	}

	encoded := blockParametersJSON(t, service, "Feed")
	if encoded == "" {
		t.Fatal("parameters_json was not backfilled")
	}
	// The legacy columns are kept, not dropped: rebuilding the table for no
	// runtime benefit is exactly what this migration avoids.
	if got := legacyColumn(t, service, "amplitude", "Feed"); got != 1.75 {
		t.Fatalf("legacy amplitude column = %v, want 1.75 to survive untouched", got)
	}

	// Diverge parameters_json from what the legacy columns would regenerate,
	// simulating a user editing this block after the first Open already
	// backfilled it. amplitude stays 1.75 — only the JSON changes, the way
	// UpdateBlock leaves it after a real edit. A backfill that re-derives from
	// the legacy columns on every Open, instead of skipping rows its own
	// WHERE clause says are already done, would silently revert this edit
	// and lose it — encodeParameters is deterministic, so comparing against
	// the unedited `encoded` from before could never catch that regression.
	const edited = `{"amplitude":9.5}`
	if _, err := service.db.ExecContext(ctx,
		"UPDATE blocks SET parameters_json = ? WHERE name = ?", edited, "Feed",
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
	if got := blockParametersJSON(t, reopened, "Feed"); got != edited {
		t.Fatalf("parameters_json after reopen = %q, want the edit %q preserved untouched", got, edited)
	}
	snapshotAfterReopen, err := reopened.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := blockNamed(t, snapshotAfterReopen.Blocks, "Feed").Parameters.Amplitude; got != 9.5 {
		t.Fatalf("amplitude after reopen = %v, want the edited 9.5, not the stale legacy 1.75", got)
	}
}

func blockParametersJSON(t *testing.T, service *Studio, name string) string {
	t.Helper()
	var encoded string
	if err := service.db.QueryRowContext(context.Background(),
		"SELECT parameters_json FROM blocks WHERE name = ?", name,
	).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	return encoded
}

func legacyColumn(t *testing.T, service *Studio, column, name string) float64 {
	t.Helper()
	var value float64
	if err := service.db.QueryRowContext(context.Background(),
		"SELECT "+column+" FROM blocks WHERE name = ?", name,
	).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
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

// A process can stop after an older migration added its columns but before it
// assigned the legacy project or numbered the tabs. Open must finish that
// partial state rather than creating a second default project or accepting
// duplicate zero positions as a user-defined order.
func TestOpenResumesInterruptedProjectAndPositionMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "interrupted.db")
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
			project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			model_updated_at TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO projects(id, name, created_at, updated_at)
		VALUES(7, 'Process Lab project', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO flows(
			id, project_id, name, created_at, updated_at, model_updated_at, position
		) VALUES
			(1, NULL, 'zeta loop', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0),
			(2, NULL, 'Alpha loop', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', 0),
			(3, NULL, 'beta loop', '2026-01-03T00:00:00Z', '2026-01-03T00:00:00Z', '2026-01-03T00:00:00Z', 0);
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

	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Project.ID != 7 {
		t.Fatalf("legacy project id = %d, want reused project 7", workspace.Project.ID)
	}
	if got, want := flowNames(workspace.Flows), []string{"Alpha loop", "beta loop", "zeta loop"}; !slices.Equal(got, want) {
		t.Fatalf("resumed order = %v, want %v", got, want)
	}
	if got, want := flowPositions(t, service, 7), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("resumed positions = %v, want %v", got, want)
	}
	var projects, unassigned int
	if err := service.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if err := service.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM flows WHERE project_id IS NULL",
	).Scan(&unassigned); err != nil {
		t.Fatal(err)
	}
	if projects != 1 || unassigned != 0 {
		t.Fatalf("projects = %d, unassigned flows = %d; want 1, 0", projects, unassigned)
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

// A fresh database has no reason to carry columns nothing reads or writes
// anymore; only a database opened from before this migration keeps them, for
// compatibility rather than any ongoing use.
func TestOpenFreshDatabaseOmitsLegacyBlockColumns(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "fresh.db"))
	for _, column := range []string{"amplitude", "gain", "time_constant"} {
		found, err := tableHasColumn(ctx, service.db, "blocks", column)
		if err != nil {
			t.Fatal(err)
		}
		if found {
			t.Fatalf("fresh database has the legacy %s column", column)
		}
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
