package studio

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegisterListsEveryProjectWithItsSheetsInTabOrder(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "register.db"))
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seededProjectID := seeded.Project.ID
	seededFlowName := seeded.Snapshot.Flow.Name

	// Names chosen so the register's order is neither the order the projects
	// were created in nor a case-sensitive sort.
	if _, err := service.CreateProject(ctx, "Batch studies"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject(ctx, "alpha campaign"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Zulu", "Alpha"} {
		if _, err := service.CreateFlow(ctx, seededProjectID, name); err != nil {
			t.Fatal(err)
		}
	}
	// A dragged tab strip, which disagrees with both name and id order.
	if _, err := service.db.ExecContext(ctx,
		"UPDATE flows SET position = 3 - position WHERE project_id = ?", seededProjectID,
	); err != nil {
		t.Fatal(err)
	}

	register, err := service.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantProjects := []string{"alpha campaign", "Batch studies", defaultProjectName}
	if got := registerNames(register); !slices.Equal(got, wantProjects) {
		t.Fatalf("register projects = %v, want %v", got, wantProjects)
	}
	wantCounts := []int{1, 1, 3}
	for i, entry := range register.Projects {
		if entry.FlowCount() != wantCounts[i] {
			t.Fatalf("%q holds %d sheets, want %d", entry.Project.Name, entry.FlowCount(), wantCounts[i])
		}
		if entry.FlowCount() != len(entry.Flows) {
			t.Fatalf("%q counts %d sheets but lists %d", entry.Project.Name, entry.FlowCount(), len(entry.Flows))
		}
		for _, flow := range entry.Flows {
			if flow.ProjectID != entry.Project.ID {
				t.Fatalf("flow %q of project %d filed under project %d",
					flow.Name, flow.ProjectID, entry.Project.ID)
			}
		}
	}

	seededEntry := registerEntry(t, register, defaultProjectName)
	wantFlows := []string{"Alpha", "Zulu", seededFlowName}
	if got := flowNames(seededEntry.Flows); !slices.Equal(got, wantFlows) {
		t.Fatalf("register sheets = %v, want the tab order %v", got, wantFlows)
	}
	tabs, err := service.ProjectWorkspace(ctx, seededProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if got := flowNames(tabs.Flows); !slices.Equal(got, wantFlows) {
		t.Fatalf("tab strip = %v, register = %v", got, wantFlows)
	}
}

func TestRegisterEditedAtFollowsTheLatestFlowsheetEdit(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "register-edited.db"))
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := seeded.Project.ID
	// A second project, created last, so it is the newer of the two.
	quiet, err := service.CreateProject(ctx, "Batch studies")
	if err != nil {
		t.Fatal(err)
	}

	// Editing a block touches the flowsheet and nothing else, so a row that
	// reported projects.updated_at would sit still while the user worked.
	lag := findKind(t, seeded.Snapshot.Blocks, BlockLag)
	if _, err := service.UpdateBlock(ctx, lag.ID, BlockUpdate{
		Name:       lag.Name,
		Parameters: map[string]string{"time_constant": "6.5"},
	}); err != nil {
		t.Fatal(err)
	}

	register, err := service.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	edited := registerEntry(t, register, defaultProjectName)
	untouched := registerEntry(t, register, "Batch studies")

	workspace, err := service.ProjectWorkspace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if !edited.EditedAt.Equal(workspace.Snapshot.Flow.UpdatedAt) {
		t.Fatalf("EditedAt = %s, want the edited flowsheet's %s",
			edited.EditedAt, workspace.Snapshot.Flow.UpdatedAt)
	}
	if !edited.EditedAt.After(edited.Project.UpdatedAt) {
		t.Fatalf("EditedAt = %s did not move past the project's own %s after a block edit",
			edited.EditedAt, edited.Project.UpdatedAt)
	}
	if !edited.EditedAt.After(untouched.EditedAt) {
		t.Fatalf("the edited project reports %s, the untouched one %s",
			edited.EditedAt, untouched.EditedAt)
	}
	if !untouched.EditedAt.Equal(quiet.Project.UpdatedAt) {
		t.Fatalf("untouched EditedAt = %s, want the project's own %s",
			untouched.EditedAt, quiet.Project.UpdatedAt)
	}
}

// TestRegisterNeedsRunAgreesWithTheFlowQueries drives four flowsheets into
// different staleness states through the public operations only, then asks
// every read model the interface uses. The register's amber dot, the tab
// strip's, and the simulation dock's chart must be the same answer.
func TestRegisterNeedsRunAgreesWithTheFlowQueries(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "register-needs-run.db"))
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := SimulationRequest{Duration: 5, SampleTime: 0.1}

	// Simulated and current.
	if _, err := service.Run(ctx, seeded.Snapshot.Flow.ID, request); err != nil {
		t.Fatal(err)
	}
	// Never simulated.
	if _, err := service.CreateFlow(ctx, seeded.Project.ID, "Zulu"); err != nil {
		t.Fatal(err)
	}

	other, err := service.CreateProject(ctx, "Batch studies")
	if err != nil {
		t.Fatal(err)
	}
	// Simulated, then edited, so its run is stale.
	staleID := other.Snapshot.Flow.ID
	wireRunnableFlow(t, service, staleID)
	if _, err := service.Run(ctx, staleID, request); err != nil {
		t.Fatal(err)
	}
	stale, err := service.Snapshot(ctx, staleID)
	if err != nil {
		t.Fatal(err)
	}
	lag := findKind(t, stale.Blocks, BlockLag)
	if _, err := service.UpdateBlock(ctx, lag.ID, BlockUpdate{
		Name:       lag.Name,
		Parameters: map[string]string{"time_constant": "6.5"},
	}); err != nil {
		t.Fatal(err)
	}
	// Simulated after every edit.
	fresh, err := service.CreateFlow(ctx, other.Project.ID, "Startup")
	if err != nil {
		t.Fatal(err)
	}
	wireRunnableFlow(t, service, fresh.Snapshot.Flow.ID)
	if _, err := service.Run(ctx, fresh.Snapshot.Flow.ID, request); err != nil {
		t.Fatal(err)
	}

	register, err := service.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var needing, current int
	for _, entry := range register.Projects {
		tabs, err := service.ProjectWorkspace(ctx, entry.Project.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(tabs.Flows) != len(entry.Flows) {
			t.Fatalf("%q lists %d sheets in the register and %d in the tab strip",
				entry.Project.Name, len(entry.Flows), len(tabs.Flows))
		}
		for i, flow := range entry.Flows {
			if flow.ID != tabs.Flows[i].ID {
				t.Fatalf("register sheet %d of %q is %d, tab strip has %d",
					i, entry.Project.Name, flow.ID, tabs.Flows[i].ID)
			}
			if flow.NeedsRun != tabs.Flows[i].NeedsRun {
				t.Fatalf("%q: register NeedsRun = %t, tab strip = %t",
					flow.Name, flow.NeedsRun, tabs.Flows[i].NeedsRun)
			}
			snapshot, err := service.Snapshot(ctx, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if flow.NeedsRun != (snapshot.LastRun == nil) {
				t.Fatalf("%q: register NeedsRun = %t but the dock has LastRun = %#v",
					flow.Name, flow.NeedsRun, snapshot.LastRun)
			}
			if flow.NeedsRun {
				needing++
			} else {
				current++
			}
		}
	}
	// Without both answers present the comparison above proves nothing.
	if needing != 2 || current != 2 {
		t.Fatalf("register reported %d stale and %d current sheets, want 2 and 2", needing, current)
	}
}

// TestRegisterAgreesWithTheDockAtTheStalenessBoundary pins the comparison
// itself. A run recorded at exactly the model's own timestamp is the run the
// dock shows, so a register asking `>` rather than `>=` would light an amber
// dot on a flowsheet whose chart is current — the two drifting apart in the
// one case where the difference is invisible to every other test.
func TestRegisterAgreesWithTheDockAtTheStalenessBoundary(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "register-boundary.db"))
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := seeded.Snapshot.Flow.ID
	if _, err := service.Run(ctx, flowID, SimulationRequest{Duration: 5, SampleTime: 0.1}); err != nil {
		t.Fatal(err)
	}
	// Two clock readings agreeing to the nanosecond is out of a test's reach
	// through the public operations, so the boundary is set from underneath.
	if _, err := service.db.ExecContext(ctx, `
		UPDATE flows SET model_updated_at = (
			SELECT created_at FROM simulation_runs
			WHERE simulation_runs.flow_id = flows.id
			ORDER BY simulation_runs.id DESC LIMIT 1
		)
		WHERE id = ?`, flowID,
	); err != nil {
		t.Fatal(err)
	}

	register, err := service.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flow := registerFlow(t, register, flowID)
	snapshot, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastRun == nil {
		t.Fatal("the dock dropped a run recorded at the model's own timestamp")
	}
	if flow.NeedsRun {
		t.Fatal("the register calls a flowsheet stale while the dock is showing its chart")
	}
	tabs, err := service.ProjectWorkspace(ctx, seeded.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if flow.NeedsRun != tabs.Flows[0].NeedsRun {
		t.Fatalf("register NeedsRun = %t, tab strip = %t", flow.NeedsRun, tabs.Flows[0].NeedsRun)
	}
}

func TestRegisterOnAnEmptyDatabaseIsEmpty(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "register-empty.db"))
	// `seed` and the last-project rule keep this state out of reach through the
	// public API, so it is reached the only way it can be: from underneath.
	if _, err := service.db.ExecContext(ctx, "DELETE FROM projects"); err != nil {
		t.Fatal(err)
	}

	register, err := service.Register(ctx)
	if err != nil {
		t.Fatalf("empty register returned an error: %v", err)
	}
	if len(register.Projects) != 0 {
		t.Fatalf("empty register holds %d projects", len(register.Projects))
	}
}

func TestRegisterAsksTheDatabaseTwiceWhateverTheProjectCount(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "register-cost.db")
	service := openTestStudio(t, path)
	counted, queries := countingStudio(t, service, path)

	queries.Store(0)
	if _, err := counted.Register(ctx); err != nil {
		t.Fatal(err)
	}
	one := queries.Load()

	for project := range 5 {
		created, err := service.CreateProject(ctx, fmt.Sprintf("Campaign %d", project))
		if err != nil {
			t.Fatal(err)
		}
		for sheet := range 3 {
			if _, err := service.CreateFlow(ctx, created.Project.ID, fmt.Sprintf("Sheet %d", sheet)); err != nil {
				t.Fatal(err)
			}
		}
	}
	queries.Store(0)
	register, err := counted.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	six := queries.Load()

	if len(register.Projects) != 6 {
		t.Fatalf("register holds %d projects, want 6", len(register.Projects))
	}
	if one != 2 || six != 2 {
		t.Fatalf("register issued %d queries for 1 project and %d for 6, want 2 each", one, six)
	}
}

func registerNames(register Register) []string {
	names := make([]string, 0, len(register.Projects))
	for _, entry := range register.Projects {
		names = append(names, entry.Project.Name)
	}
	return names
}

func registerEntry(t *testing.T, register Register, name string) RegisterEntry {
	t.Helper()
	for _, entry := range register.Projects {
		if entry.Project.Name == name {
			return entry
		}
	}
	t.Fatalf("no project %q in the register", name)
	return RegisterEntry{}
}

func registerFlow(t *testing.T, register Register, flowID int64) Flow {
	t.Helper()
	for _, entry := range register.Projects {
		for _, flow := range entry.Flows {
			if flow.ID == flowID {
				return flow
			}
		}
	}
	t.Fatalf("no flowsheet %d in the register", flowID)
	return Flow{}
}

// wireRunnableFlow gives an empty flowsheet enough to simulate: a source
// through a lag into a scope. The lag is what a later edit changes.
func wireRunnableFlow(t *testing.T, service *Studio, flowID int64) {
	t.Helper()
	ctx := context.Background()
	_, sourceID, err := service.AddBlock(ctx, flowID, BlockSource, Point{X: 60, Y: 80})
	if err != nil {
		t.Fatal(err)
	}
	_, lagID, err := service.AddBlock(ctx, flowID, BlockLag, Point{X: 400, Y: 80})
	if err != nil {
		t.Fatal(err)
	}
	_, scopeID, err := service.AddBlock(ctx, flowID, BlockScope, Point{X: 740, Y: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{SourceID: sourceID, TargetID: lagID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{SourceID: lagID, TargetID: scopeID}); err != nil {
		t.Fatal(err)
	}
}

var countingDrivers atomic.Int64

// countingStudio reopens an existing database through a driver that counts
// every query, so a test can prove a read model's cost does not grow with the
// data. Writes still go through the original studio; only reads are counted.
func countingStudio(t *testing.T, service *Studio, path string) (*Studio, *atomic.Int64) {
	t.Helper()
	queries := &atomic.Int64{}
	name := fmt.Sprintf("counting-sqlite-%d", countingDrivers.Add(1))
	sql.Register(name, countingDriver{inner: service.db.Driver(), queries: queries})
	db, err := sql.Open(name, dataSourceName(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return &Studio{db: db, now: time.Now}, queries
}

type countingDriver struct {
	inner   driver.Driver
	queries *atomic.Int64
}

func (d countingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return countingConn{Conn: conn, queries: d.queries}, nil
}

// countingConn implements neither driver.QueryerContext nor
// driver.ExecerContext, so database/sql has to route every statement through
// PrepareContext and the counting statement below sees all of them.
type countingConn struct {
	driver.Conn
	queries *atomic.Int64
}

func (c countingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return countingStmt{Stmt: stmt, queries: c.queries}, nil
}

func (c countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	preparer, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return c.Prepare(query)
	}
	stmt, err := preparer.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return countingStmt{Stmt: stmt, queries: c.queries}, nil
}

func (c countingConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return c.Conn.Begin()
	}
	return beginner.BeginTx(ctx, options)
}

type countingStmt struct {
	driver.Stmt
	queries *atomic.Int64
}

func (s countingStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.queries.Add(1)
	return s.Stmt.Query(args)
}

func (s countingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.queries.Add(1)
	querier, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		values := make([]driver.Value, 0, len(args))
		for _, arg := range args {
			values = append(values, arg.Value)
		}
		return s.Stmt.Query(values)
	}
	return querier.QueryContext(ctx, args)
}
