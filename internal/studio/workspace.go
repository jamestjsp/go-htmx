package studio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultFlowName        = "Untitled flowsheet"
	maxWorkspaceNameLength = 80
)

func (s *Studio) CurrentWorkspace(ctx context.Context) (Workspace, error) {
	var projectID, flowID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT projects.id, flows.id
		FROM projects
		JOIN flows ON flows.project_id = projects.id
		ORDER BY projects.id, flows.id
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

func (s *Studio) CreateProject(ctx context.Context, name string) (Workspace, error) {
	name, err := workspaceName("project", name)
	if err != nil {
		return Workspace{}, err
	}
	var projectID, flowID int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.now().UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx,
			"INSERT INTO projects(name, created_at, updated_at) VALUES(?, ?, ?)",
			name, now, now,
		)
		if err != nil {
			return fmt.Errorf("create project: %w", err)
		}
		projectID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read project id: %w", err)
		}
		flowID, err = insertFlow(ctx, tx, projectID, defaultFlowName, now)
		return err
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.Workspace(ctx, projectID, flowID)
}

func (s *Studio) CreateFlow(ctx context.Context, projectID int64, name string) (Workspace, error) {
	name, err := workspaceName("flowsheet", name)
	if err != nil {
		return Workspace{}, err
	}
	var flowID int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM projects WHERE id = ?", projectID,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		flowID, err = insertFlow(ctx, tx, projectID, name, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE projects SET updated_at = ? WHERE id = ?", now, projectID,
		); err != nil {
			return fmt.Errorf("touch project: %w", err)
		}
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.Workspace(ctx, projectID, flowID)
}

func (s *Studio) RenameFlow(ctx context.Context, flowID int64, name string) (Workspace, error) {
	name, err := workspaceName("flowsheet", name)
	if err != nil {
		return Workspace{}, err
	}
	var projectID int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			"SELECT project_id FROM flows WHERE id = ?", flowID,
		).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET name = ?, updated_at = ? WHERE id = ?",
			name, now, flowID,
		); err != nil {
			return fmt.Errorf("rename flowsheet: %w", err)
		}
		return insertEvent(ctx, tx, flowID, now, "Renamed flowsheet to "+name)
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.Workspace(ctx, projectID, flowID)
}

func insertFlow(ctx context.Context, tx *sql.Tx, projectID int64, name, now string) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO flows(project_id, name, created_at, updated_at, model_updated_at)
		VALUES(?, ?, ?, ?, ?)`,
		projectID, name, now, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("create flowsheet: %w", err)
	}
	flowID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read flowsheet id: %w", err)
	}
	if err := insertEvent(ctx, tx, flowID, now, "Flowsheet created"); err != nil {
		return 0, err
	}
	return flowID, nil
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

func (s *Studio) projectFlows(ctx context.Context, projectID int64) ([]Flow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, name, created_at, updated_at, model_updated_at
		FROM flows
		WHERE project_id = ?
		ORDER BY name COLLATE NOCASE, id`,
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

func workspaceName(subject, raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", invalid("%s name is required", subject)
	}
	if utf8.RuneCountInString(name) > maxWorkspaceNameLength {
		return "", invalid("%s name must be %d characters or fewer", subject, maxWorkspaceNameLength)
	}
	return name, nil
}
