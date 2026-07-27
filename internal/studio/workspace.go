package studio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Studio) CurrentWorkspace(ctx context.Context) (Workspace, error) {
	var projectID, flowID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT projects.id, flows.id
		FROM projects
		JOIN flows ON flows.project_id = projects.id
		ORDER BY projects.id, flows.position, flows.id
		LIMIT 1`,
	).Scan(&projectID, &flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("load current workspace: %w", err)
	}
	return s.Workspace(ctx, projectID, flowID)
}

func (s *Studio) ProjectWorkspace(ctx context.Context, projectID int64) (Workspace, error) {
	var flowID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM flows
		WHERE project_id = ?
		ORDER BY position, id
		LIMIT 1`,
		projectID,
	).Scan(&flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("open project workspace: %w", err)
	}
	return s.Workspace(ctx, projectID, flowID)
}

func (s *Studio) Workspace(ctx context.Context, projectID, flowID int64) (Workspace, error) {
	var workspace Workspace
	var created, updated string
	err := s.db.QueryRowContext(ctx, `
		SELECT projects.id, projects.name, projects.created_at, projects.updated_at
		FROM projects
		JOIN flows ON flows.project_id = projects.id
		WHERE projects.id = ? AND flows.id = ?`,
		projectID, flowID,
	).Scan(
		&workspace.Project.ID, &workspace.Project.Name, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("load workspace project: %w", err)
	}
	workspace.Project.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	workspace.Project.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)

	workspace.Projects, err = s.projects(ctx)
	if err != nil {
		return Workspace{}, err
	}
	workspace.Flows, err = s.projectFlows(ctx, projectID)
	if err != nil {
		return Workspace{}, err
	}
	workspace.Snapshot, err = s.snapshot(ctx, flowID)
	if err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (s *Studio) projects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, created_at, updated_at
		FROM projects
		ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		var created, updated string
		if err := rows.Scan(
			&project.ID, &project.Name, &created, &updated,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		project.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		project.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}
	return projects, nil
}

// projectFlows lists one project's tab strip. NeedsRun compares the two
// RFC3339Nano timestamps as raw text, exactly as `snapshot` does when it picks
// the run behind the chart; converting either side to a datetime here is how
// the amber dot and the dock would start to disagree.
func (s *Studio) projectFlows(ctx context.Context, projectID int64) ([]Flow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			flows.id, flows.project_id, flows.name,
			flows.created_at, flows.updated_at, flows.model_updated_at,
			NOT EXISTS (
				SELECT 1 FROM simulation_runs
				WHERE simulation_runs.flow_id = flows.id
					AND simulation_runs.created_at >= flows.model_updated_at
			) AS needs_run
		FROM flows
		WHERE flows.project_id = ?
		ORDER BY flows.position, flows.id`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list project flows: %w", err)
	}
	defer rows.Close()
	var flows []Flow
	for rows.Next() {
		var flow Flow
		var created, updated, modelUpdated string
		if err := rows.Scan(
			&flow.ID, &flow.ProjectID, &flow.Name, &created, &updated, &modelUpdated,
			&flow.NeedsRun,
		); err != nil {
			return nil, fmt.Errorf("scan project flow: %w", err)
		}
		flow.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		flow.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		flow.ModelUpdatedAt, _ = time.Parse(time.RFC3339Nano, modelUpdated)
		flows = append(flows, flow)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read project flows: %w", err)
	}
	return flows, nil
}
