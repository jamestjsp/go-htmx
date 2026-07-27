package studio

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
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

// The + button on the tab strip creates a sheet with no dialog, so an empty
// name is a request for a generated one rather than a mistake.
func TestCreateFlowGeneratesTheNextFreeName(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "generated-names.db"))
	project, err := service.CreateProject(ctx, "Batch studies")
	if err != nil {
		t.Fatal(err)
	}
	projectID := project.Project.ID

	first, err := service.CreateFlow(ctx, projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot.Flow.Name != "Flowsheet 1" {
		t.Fatalf("first generated name = %q, want %q", first.Snapshot.Flow.Name, "Flowsheet 1")
	}
	// Whitespace is what a submitted-but-untouched field actually contains.
	second, err := service.CreateFlow(ctx, projectID, "   ")
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot.Flow.Name != "Flowsheet 2" {
		t.Fatalf("second generated name = %q, want %q", second.Snapshot.Flow.Name, "Flowsheet 2")
	}

	// The numbering fills the lowest free slot rather than counting sheets or
	// continuing past the highest number ever used.
	if _, err := service.DeleteFlow(ctx, first.Snapshot.Flow.ID); err != nil {
		t.Fatal(err)
	}
	refilled, err := service.CreateFlow(ctx, projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if refilled.Snapshot.Flow.Name != "Flowsheet 1" {
		t.Fatalf("name after deleting Flowsheet 1 = %q, want it reused", refilled.Snapshot.Flow.Name)
	}

	// A name a user typed is never treated as taken by the generator's own
	// sequence, and numbering is per project.
	if _, err := service.CreateFlow(ctx, projectID, "Flowsheet 3"); err != nil {
		t.Fatal(err)
	}
	skipped, err := service.CreateFlow(ctx, projectID, "")
	if err != nil {
		t.Fatal(err)
	}
	if skipped.Snapshot.Flow.Name != "Flowsheet 4" {
		t.Fatalf("name = %q, want Flowsheet 4 to skip the typed one", skipped.Snapshot.Flow.Name)
	}
	elsewhere, err := service.CreateFlow(ctx, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere.Snapshot.Flow.Name != "Flowsheet 1" {
		t.Fatalf("other project's generated name = %q, want Flowsheet 1", elsewhere.Snapshot.Flow.Name)
	}
}

func TestCreateFlowStillValidatesSubmittedNames(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.CreateFlow(ctx, current.Project.ID, strings.Repeat("x", maxWorkspaceNameLength+1))
	if err == nil {
		t.Fatal("an over-long name was accepted")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want a ValidationError", err)
	}
	if _, err := service.CreateFlow(ctx, 9999, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrNotFound)
	}
	if got := countRows(t, service, "SELECT COUNT(*) FROM flows"); got != 1 {
		t.Fatalf("flows = %d, want the rejected creates to have added none", got)
	}
	named, err := service.CreateFlow(ctx, current.Project.ID, "  Startup  ")
	if err != nil {
		t.Fatal(err)
	}
	if named.Snapshot.Flow.Name != "Startup" {
		t.Fatalf("submitted name = %q", named.Snapshot.Flow.Name)
	}
}

func TestDuplicateFlowCopiesBlocksAndRewiresConnections(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "duplicate-flow.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := current.Snapshot.Flow.ID
	sourceName := current.Snapshot.Flow.Name
	// A run and a history, so the test can prove neither is copied.
	if _, err := service.Run(ctx, sourceID, SimulationRequest{Duration: 5, SampleTime: 0.1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateFlow(ctx, current.Project.ID, "Startup"); err != nil {
		t.Fatal(err)
	}
	source, err := service.Snapshot(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.Blocks) == 0 || len(source.Connections) == 0 {
		t.Fatal("the seeded flowsheet has nothing to copy")
	}

	duplicated, err := service.DuplicateFlow(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	copied := duplicated.Snapshot
	if copied.Flow.ID == sourceID {
		t.Fatal("duplicate returned the source flowsheet")
	}
	if copied.Flow.Name != sourceName+" copy" {
		t.Fatalf("copy name = %q, want %q", copied.Flow.Name, sourceName+" copy")
	}
	// The copy lands immediately right of its source and pushes the rest along.
	if got, want := flowNames(duplicated.Flows),
		[]string{sourceName, sourceName + " copy", "Startup"}; !slices.Equal(got, want) {
		t.Fatalf("tab order = %v, want %v", got, want)
	}
	if got, want := flowPositions(t, service, current.Project.ID), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}

	if len(copied.Blocks) != len(source.Blocks) {
		t.Fatalf("copied %d blocks, want %d", len(copied.Blocks), len(source.Blocks))
	}
	sourceIDs := map[int64]bool{}
	for _, block := range source.Blocks {
		sourceIDs[block.ID] = true
	}
	for i, block := range copied.Blocks {
		original := source.Blocks[i]
		if block.ID == original.ID {
			t.Fatalf("copied block %q shares the source's id", block.Name)
		}
		if block.FlowID != copied.Flow.ID {
			t.Fatalf("copied block %q belongs to flow %d", block.Name, block.FlowID)
		}
		if block.Kind != original.Kind || block.Name != original.Name || block.Position != original.Position {
			t.Fatalf("copied block = %#v, want %#v", block, original)
		}
		if !reflect.DeepEqual(block.Parameters, original.Parameters) {
			t.Fatalf("copied parameters for %q = %#v, want %#v",
				block.Name, block.Parameters, original.Parameters)
		}
	}

	// The wires must join the copy's own blocks, in the same shape.
	if len(copied.Connections) != len(source.Connections) {
		t.Fatalf("copied %d connections, want %d", len(copied.Connections), len(source.Connections))
	}
	for _, connection := range copied.Connections {
		if sourceIDs[connection.SourceID] || sourceIDs[connection.TargetID] {
			t.Fatalf("copied connection %#v still points at the source's blocks", connection)
		}
		if connection.FlowID != copied.Flow.ID {
			t.Fatalf("copied connection %#v belongs to another flowsheet", connection)
		}
	}
	if got, want := wiring(t, copied), wiring(t, source); !slices.Equal(got, want) {
		t.Fatalf("copied wiring = %v, want %v", got, want)
	}

	// Nothing of the source's history or results follows the copy.
	if copied.LastRun != nil || !copied.Flow.NeedsRun {
		t.Fatalf("the copy arrived with a simulation: %#v", copied.LastRun)
	}
	if got := countRows(t, service,
		"SELECT COUNT(*) FROM simulation_runs WHERE flow_id = ?", copied.Flow.ID); got != 0 {
		t.Fatalf("copied simulation runs = %d, want 0", got)
	}
	if got := countRows(t, service,
		"SELECT COUNT(*) FROM events WHERE flow_id = ?", copied.Flow.ID); got != 1 {
		t.Fatalf("copied events = %d, want the one event the copy records", got)
	}
	if len(copied.Events) != 1 || copied.Events[0].Message != "Duplicated from "+sourceName {
		t.Fatalf("copy events = %#v", copied.Events)
	}

	// The source is untouched: same blocks, same wires, same chart.
	reread, err := service.Snapshot(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reread.Blocks) != len(source.Blocks) || len(reread.Connections) != len(source.Connections) {
		t.Fatalf("source now has %d blocks and %d connections",
			len(reread.Blocks), len(reread.Connections))
	}
	if reread.LastRun == nil {
		t.Fatal("duplicating invalidated the source's simulation")
	}
}

// copyBlocks carries parameters_json verbatim, the one column a block's
// parameters live in now that the legacy scalar columns are retired. This
// still has to prove two distinct ways a copy could lose data: a JSON blob
// written before the catalog gained fields it now has, where decodeParameters
// fills the rest from today's defaults, and a polynomial array, which a
// column-for-column string copy cannot silently truncate the way a
// per-field copy could.
func TestDuplicateFlowPreservesBlockParameters(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFlow(ctx, current.Project.ID, "Mixed vintages")
	if err != nil {
		t.Fatal(err)
	}
	flowID := created.Snapshot.Flow.ID
	insertBlock := func(kind BlockKind, name, encoded string) int64 {
		t.Helper()
		result, err := service.db.ExecContext(ctx, `
			INSERT INTO blocks(flow_id, kind, name, x, y, parameters_json)
			VALUES(?, ?, ?, 60, 80, ?)`,
			flowID, kind, name, encoded,
		)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sourceID := insertBlock(BlockSource, "Feed", `{"amplitude":1.75}`)
	// A block written before the catalog gained the fields it now has: the JSON
	// holds one field and decoding fills the rest from today's defaults.
	partialID := insertBlock(BlockPID, "Controller", `{"derivative":0.25}`)
	insertBlock(BlockTransfer, "Valve", `{"numerator":[2,1],"denominator":[1,3,1]}`)
	if _, err := service.Connect(ctx, flowID, sourceID, partialID); err != nil {
		t.Fatal(err)
	}

	source, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	duplicated, err := service.DuplicateFlow(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	for _, original := range source.Blocks {
		copied := blockNamed(t, duplicated.Snapshot.Blocks, original.Name)
		if !reflect.DeepEqual(copied.Parameters, original.Parameters) {
			t.Fatalf("copied parameters for %q = %#v, want %#v",
				original.Name, copied.Parameters, original.Parameters)
		}
	}
	// Stated outright, because each of these is a different way to lose data.
	if got := blockNamed(t, duplicated.Snapshot.Blocks, "Feed").Parameters.Amplitude; got != 1.75 {
		t.Fatalf("copied amplitude = %v, want 1.75", got)
	}
	controller := blockNamed(t, duplicated.Snapshot.Blocks, "Controller").Parameters
	if controller.Derivative != 0.25 {
		t.Fatalf("stored JSON parameter = %v, want 0.25", controller.Derivative)
	}
	if controller.Integral != defaultParameters(BlockPID).Integral {
		t.Fatalf("catalog default = %v, want %v", controller.Integral, defaultParameters(BlockPID).Integral)
	}
	valve := blockNamed(t, duplicated.Snapshot.Blocks, "Valve").Parameters
	if !slices.Equal(valve.Numerator, []float64{2, 1}) ||
		!slices.Equal(valve.Denominator, []float64{1, 3, 1}) {
		t.Fatalf("copied polynomial = %v / %v", valve.Numerator, valve.Denominator)
	}
}

func TestDuplicateFlowCopiesAnEmptyFlowsheet(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := service.CreateFlow(ctx, current.Project.ID, "Blank")
	if err != nil {
		t.Fatal(err)
	}

	duplicated, err := service.DuplicateFlow(ctx, empty.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if duplicated.Snapshot.Flow.Name != "Blank copy" {
		t.Fatalf("copy name = %q", duplicated.Snapshot.Flow.Name)
	}
	if len(duplicated.Snapshot.Blocks) != 0 || len(duplicated.Snapshot.Connections) != 0 {
		t.Fatalf("copy of an empty sheet has %d blocks and %d connections",
			len(duplicated.Snapshot.Blocks), len(duplicated.Snapshot.Connections))
	}
	if len(duplicated.Snapshot.Events) != 1 {
		t.Fatalf("copy events = %#v", duplicated.Snapshot.Events)
	}

	// A name at the limit still yields a name a user could have typed.
	long := strings.Repeat("x", maxWorkspaceNameLength)
	renamed, err := service.RenameFlow(ctx, empty.Snapshot.Flow.ID, long)
	if err != nil {
		t.Fatal(err)
	}
	longCopy, err := service.DuplicateFlow(ctx, renamed.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := longCopy.Snapshot.Flow.Name; len([]rune(got)) > maxWorkspaceNameLength ||
		!strings.HasSuffix(got, " copy") {
		t.Fatalf("copy of a maximum-length name = %q (%d runes)", got, len([]rune(got)))
	}

	if _, err := service.DuplicateFlow(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrNotFound)
	}
}

func TestDeleteFlowOpensTheNeighbouringTab(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "delete-flow.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := current.Project.ID
	first := current.Snapshot.Flow.ID
	middle, err := service.CreateFlow(ctx, projectID, "Startup")
	if err != nil {
		t.Fatal(err)
	}
	last, err := service.CreateFlow(ctx, projectID, "Shutdown")
	if err != nil {
		t.Fatal(err)
	}
	// Give the doomed sheet rows in every dependent table.
	_, feedID, err := service.AddBlock(ctx, middle.Snapshot.Flow.ID, BlockSource, Point{X: 60, Y: 80})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := service.AddBlock(ctx, middle.Snapshot.Flow.ID, BlockScope, Point{X: 300, Y: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, middle.Snapshot.Flow.ID, feedID, scopeID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(ctx, middle.Snapshot.Flow.ID, SimulationRequest{
		Duration:   5,
		SampleTime: 0.1,
	}); err != nil {
		t.Fatal(err)
	}

	remaining, err := service.DeleteFlow(ctx, middle.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A deleted tab hands you the tab to its left.
	if remaining.Snapshot.Flow.ID != first {
		t.Fatalf("landed on flow %d, want the left neighbour %d", remaining.Snapshot.Flow.ID, first)
	}
	if got, want := flowNames(remaining.Flows),
		[]string{current.Snapshot.Flow.Name, "Shutdown"}; !slices.Equal(got, want) {
		t.Fatalf("tab strip = %v, want %v", got, want)
	}
	if got, want := flowPositions(t, service, projectID), []int{0, 1}; !slices.Equal(got, want) {
		t.Fatalf("positions = %v, want %v; deleting left a hole", got, want)
	}
	for _, table := range []string{"blocks", "connections", "events", "simulation_runs"} {
		if got := countRows(t, service,
			"SELECT COUNT(*) FROM "+table+" WHERE flow_id = ?", middle.Snapshot.Flow.ID,
		); got != 0 {
			t.Fatalf("%s rows for the deleted flowsheet = %d, want 0", table, got)
		}
	}
	if _, err := service.Snapshot(ctx, middle.Snapshot.Flow.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted flowsheet snapshot error = %v", err)
	}

	// Deleting the first tab hands you the one on its right instead.
	after, err := service.DeleteFlow(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if after.Snapshot.Flow.ID != last.Snapshot.Flow.ID {
		t.Fatalf("landed on flow %d, want the right neighbour %d",
			after.Snapshot.Flow.ID, last.Snapshot.Flow.ID)
	}
	if got, want := flowPositions(t, service, projectID), []int{0}; !slices.Equal(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
}

func TestDeleteFlowRefusesTheLastFlowsheet(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "last-flow.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A second project's sheets must not count towards this project's total.
	if _, err := service.CreateProject(ctx, "Batch studies"); err != nil {
		t.Fatal(err)
	}
	blocks := countRows(t, service, "SELECT COUNT(*) FROM blocks WHERE flow_id = ?", current.Snapshot.Flow.ID)

	_, err = service.DeleteFlow(ctx, current.Snapshot.Flow.ID)
	if err == nil {
		t.Fatal("deleted the last flowsheet in a project")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want a ValidationError", err)
	}
	if validation.Message == "" {
		t.Fatal("refusal carries no message for the user")
	}

	if got := countRows(t, service,
		"SELECT COUNT(*) FROM flows WHERE project_id = ?", current.Project.ID); got != 1 {
		t.Fatalf("flows = %d, want 1", got)
	}
	if got := countRows(t, service,
		"SELECT COUNT(*) FROM blocks WHERE flow_id = ?", current.Snapshot.Flow.ID); got != blocks {
		t.Fatalf("blocks = %d, want %d", got, blocks)
	}
	if _, err := service.DeleteFlow(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrNotFound)
	}
}

func TestReorderFlowsRewritesTheWholeStrip(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "reorder.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := current.Project.ID
	startup, err := service.CreateFlow(ctx, projectID, "Startup")
	if err != nil {
		t.Fatal(err)
	}
	shutdown, err := service.CreateFlow(ctx, projectID, "Shutdown")
	if err != nil {
		t.Fatal(err)
	}

	reordered, err := service.ReorderFlows(ctx, projectID, []int64{
		shutdown.Snapshot.Flow.ID, current.Snapshot.Flow.ID, startup.Snapshot.Flow.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Shutdown", current.Snapshot.Flow.Name, "Startup"}
	if got := flowNames(reordered.Flows); !slices.Equal(got, want) {
		t.Fatalf("tab strip = %v, want %v", got, want)
	}
	if got, want := flowPositions(t, service, projectID), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
	// The order is the project's, not one workspace's view of it.
	reread, err := service.ProjectWorkspace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if got := flowNames(reread.Flows); !slices.Equal(got, want) {
		t.Fatalf("reread tab strip = %v, want %v", got, want)
	}
	if reread.Snapshot.Flow.Name != "Shutdown" {
		t.Fatalf("project opened %q, want the new first tab", reread.Snapshot.Flow.Name)
	}
}

func TestReorderFlowsRejectsAnythingButAPermutation(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "reorder-refusals.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := current.Project.ID
	seeded := current.Snapshot.Flow.ID
	startup, err := service.CreateFlow(ctx, projectID, "Startup")
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateProject(ctx, "Batch studies")
	if err != nil {
		t.Fatal(err)
	}
	foreign := other.Snapshot.Flow.ID
	before := flowNames(current.Flows)

	tests := []struct {
		name  string
		order []int64
	}{
		{name: "omits a flowsheet", order: []int64{seeded}},
		{name: "repeats one", order: []int64{seeded, seeded}},
		{name: "another project's flowsheet", order: []int64{seeded, foreign}},
		{name: "an unknown flowsheet", order: []int64{seeded, 9999}},
		{name: "an extra id", order: []int64{seeded, startup.Snapshot.Flow.ID, foreign}},
		{name: "nothing at all", order: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ReorderFlows(ctx, projectID, test.order)
			if err == nil {
				t.Fatal("the order was accepted")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
			// A refused reorder must leave every position exactly as it was.
			if got, want := flowPositions(t, service, projectID), []int{0, 1}; !slices.Equal(got, want) {
				t.Fatalf("positions = %v, want %v", got, want)
			}
			if got, want := flowPositions(t, service, other.Project.ID), []int{0}; !slices.Equal(got, want) {
				t.Fatalf("other project's positions = %v, want %v", got, want)
			}
			workspace, err := service.ProjectWorkspace(ctx, projectID)
			if err != nil {
				t.Fatal(err)
			}
			if got := flowNames(workspace.Flows); !slices.Equal(got, append(before, "Startup")) {
				t.Fatalf("tab strip = %v", got)
			}
		})
	}

	if _, err := service.ReorderFlows(ctx, 9999, []int64{seeded}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, ErrNotFound)
	}
}

// wiring describes a snapshot's connections by block name, so two flowsheets
// can be compared for shape without sharing any ids.
func wiring(t *testing.T, snapshot Snapshot) []string {
	t.Helper()
	names := make(map[int64]string, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		names[block.ID] = block.Name
	}
	edges := make([]string, 0, len(snapshot.Connections))
	for _, connection := range snapshot.Connections {
		source, ok := names[connection.SourceID]
		if !ok {
			t.Fatalf("connection %d has no source block in its own flowsheet", connection.ID)
		}
		target, ok := names[connection.TargetID]
		if !ok {
			t.Fatalf("connection %d has no target block in its own flowsheet", connection.ID)
		}
		edges = append(edges, source+" -> "+target)
	}
	slices.Sort(edges)
	return edges
}

func blockNamed(t *testing.T, blocks []Block, name string) Block {
	t.Helper()
	for _, block := range blocks {
		if block.Name == name {
			return block
		}
	}
	t.Fatalf("no block named %q", name)
	return Block{}
}

func countRows(t *testing.T, service *Studio, query string, args ...any) int {
	t.Helper()
	var count int
	if err := service.db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
