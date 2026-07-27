package studio

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProjectWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")
	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Project.ID != current.Snapshot.Flow.ProjectID {
		t.Fatalf("project = %d, flow project = %d", current.Project.ID, current.Snapshot.Flow.ProjectID)
	}
	if len(current.Projects) != 1 || len(current.Flows) != 1 {
		t.Fatalf("initial workspace has %d projects and %d flows", len(current.Projects), len(current.Flows))
	}

	run, err := service.Run(ctx, current.Snapshot.Flow.ID, SimulationRequest{
		Duration:   5,
		SampleTime: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.LastRun == nil {
		t.Fatal("seeded flow did not simulate")
	}
	renamedSeed, err := service.RenameFlow(ctx, current.Snapshot.Flow.ID, "  Main control loop  ")
	if err != nil {
		t.Fatal(err)
	}
	if renamedSeed.Snapshot.Flow.Name != "Main control loop" {
		t.Fatalf("renamed flow = %q", renamedSeed.Snapshot.Flow.Name)
	}
	if renamedSeed.Snapshot.LastRun == nil {
		t.Fatal("renaming invalidated the last simulation")
	}

	createdProject, err := service.CreateProject(ctx, "  Batch studies  ")
	if err != nil {
		t.Fatal(err)
	}
	if createdProject.Project.Name != "Batch studies" {
		t.Fatalf("project name = %q", createdProject.Project.Name)
	}
	if len(createdProject.Projects) != 2 || len(createdProject.Flows) != 1 {
		t.Fatalf("created workspace has %d projects and %d flows", len(createdProject.Projects), len(createdProject.Flows))
	}
	if createdProject.Snapshot.Flow.Name != defaultFlowName {
		t.Fatalf("default flow = %q", createdProject.Snapshot.Flow.Name)
	}

	createdFlow, err := service.CreateFlow(ctx, createdProject.Project.ID, " Startup ")
	if err != nil {
		t.Fatal(err)
	}
	if createdFlow.Snapshot.Flow.Name != "Startup" || len(createdFlow.Flows) != 2 {
		t.Fatalf("created flow = %q, flow count = %d", createdFlow.Snapshot.Flow.Name, len(createdFlow.Flows))
	}
	flowID := createdFlow.Snapshot.Flow.ID

	renamedFlow, err := service.RenameFlow(ctx, flowID, "Warm startup")
	if err != nil {
		t.Fatal(err)
	}
	if renamedFlow.Snapshot.Flow.Name != "Warm startup" {
		t.Fatalf("renamed flow = %q", renamedFlow.Snapshot.Flow.Name)
	}
	if len(renamedFlow.Snapshot.Events) == 0 ||
		renamedFlow.Snapshot.Events[0].Message != "Renamed flowsheet to Warm startup" {
		t.Fatalf("rename event = %#v", renamedFlow.Snapshot.Events)
	}

	if _, err := service.Workspace(ctx, current.Project.ID, flowID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mismatched workspace error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	persisted, err := reopened.Workspace(ctx, createdProject.Project.ID, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Project.Name != "Batch studies" || persisted.Snapshot.Flow.Name != "Warm startup" {
		t.Fatalf("persisted workspace = %q / %q", persisted.Project.Name, persisted.Snapshot.Flow.Name)
	}
}

func TestFlowListsFollowPositionRatherThanName(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "order.db"))
	seeded, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID := seeded.Project.ID
	seededName := seeded.Snapshot.Flow.Name

	// Names chosen so alphabetical order disagrees with creation order.
	if _, err := service.CreateFlow(ctx, projectID, "Zulu"); err != nil {
		t.Fatal(err)
	}
	appended, err := service.CreateFlow(ctx, projectID, "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{seededName, "Zulu", "Alpha"}
	if got := flowNames(appended.Flows); !slices.Equal(got, want) {
		t.Fatalf("flow order = %v, want %v", got, want)
	}
	if got, want := flowPositions(t, service, projectID), []int{0, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}

	// A reordered strip, as a drag will later write it, decides both the list
	// order and which flowsheet the project opens on.
	if _, err := service.db.ExecContext(ctx,
		"UPDATE flows SET position = 3 - position WHERE project_id = ?", projectID,
	); err != nil {
		t.Fatal(err)
	}
	reordered, err := service.ProjectWorkspace(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"Alpha", "Zulu", seededName}
	if got := flowNames(reordered.Flows); !slices.Equal(got, want) {
		t.Fatalf("reordered flows = %v, want %v", got, want)
	}
	if reordered.Snapshot.Flow.Name != "Alpha" {
		t.Fatalf("project opened %q, want the first tab", reordered.Snapshot.Flow.Name)
	}
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.Snapshot.Flow.Name != "Alpha" {
		t.Fatalf("current workspace opened %q, want the first tab", current.Snapshot.Flow.Name)
	}

	// Each project counts its own tabs from zero.
	other, err := service.CreateProject(ctx, "Batch studies")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := flowPositions(t, service, other.Project.ID), []int{0}; !slices.Equal(got, want) {
		t.Fatalf("new project positions = %v, want %v", got, want)
	}
}

func TestFlowNeedsRunFollowsModelEditsAndRuns(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	workspace, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := workspace.Snapshot.Flow.ID
	if !workspace.Flows[0].NeedsRun {
		t.Fatal("a flowsheet that has never been simulated does not need a run")
	}

	if _, err := service.Run(ctx, flowID, SimulationRequest{Duration: 5, SampleTime: 0.1}); err != nil {
		t.Fatal(err)
	}
	workspace, err = service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Flows[0].NeedsRun {
		t.Fatal("a freshly simulated flowsheet still needs a run")
	}
	if workspace.Snapshot.Flow.NeedsRun != (workspace.Snapshot.LastRun == nil) {
		t.Fatalf("tab dot and chart disagree: NeedsRun = %t, LastRun = %#v",
			workspace.Snapshot.Flow.NeedsRun, workspace.Snapshot.LastRun)
	}

	lag := findKind(t, workspace.Snapshot.Blocks, BlockLag)
	if _, err := service.UpdateBlock(ctx, lag.ID, BlockUpdate{
		Name:       lag.Name,
		Parameters: map[string]string{"time_constant": "6.5"},
	}); err != nil {
		t.Fatal(err)
	}
	workspace, err = service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.Flows[0].NeedsRun {
		t.Fatal("an edited flowsheet does not need a run")
	}
	if workspace.Snapshot.Flow.NeedsRun != (workspace.Snapshot.LastRun == nil) {
		t.Fatalf("tab dot and chart disagree after an edit: NeedsRun = %t, LastRun = %#v",
			workspace.Snapshot.Flow.NeedsRun, workspace.Snapshot.LastRun)
	}
}

func TestProjectWorkspaceRejectsInvalidIntent(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, filepath.Join(t.TempDir(), "invalid-workspace.db"))
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "blank project",
			run: func() error {
				_, err := service.CreateProject(ctx, " ")
				return err
			},
		},
		{
			name: "long flowsheet",
			run: func() error {
				_, err := service.CreateFlow(ctx, current.Project.ID, strings.Repeat("x", maxWorkspaceNameLength+1))
				return err
			},
		},
		{
			name: "missing project",
			run: func() error {
				_, err := service.CreateFlow(ctx, 9999, "Flow")
				return err
			},
		},
		{
			name: "missing flow",
			run: func() error {
				_, err := service.RenameFlow(ctx, 9999, "Flow")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil {
				t.Fatal("operation succeeded")
			}
		})
	}
}
