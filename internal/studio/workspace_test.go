package studio

import (
	"context"
	"errors"
	"path/filepath"
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
