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

func (s *Studio) RenameProject(ctx context.Context, projectID int64, name string) (Workspace, error) {
	name, err := workspaceName("project", name)
	if err != nil {
		return Workspace{}, err
	}
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
		if _, err := tx.ExecContext(ctx,
			"UPDATE projects SET name = ?, updated_at = ? WHERE id = ?",
			name, now, projectID,
		); err != nil {
			return fmt.Errorf("rename project: %w", err)
		}
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.ProjectWorkspace(ctx, projectID)
}

// DeleteProject removes a project and, through ON DELETE CASCADE, its
// flowsheets and every block, connection, event and simulation run beneath
// them. The cascade is the whole deletion: deleting child rows by hand here
// would be a second, divergent definition of what a project contains.
//
// It returns the workspace the caller lands on afterwards, which cannot be the
// deleted project's. That is `CurrentWorkspace` — the first flowsheet of the
// lowest-numbered surviving project — and refusing the last project is
// precisely what guarantees such a workspace still exists.
func (s *Studio) DeleteProject(ctx context.Context, projectID int64) (Workspace, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM projects WHERE id = ?", projectID,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var survivors int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM projects WHERE id <> ?", projectID,
		).Scan(&survivors); err != nil {
			return fmt.Errorf("count remaining projects: %w", err)
		}
		if survivors == 0 {
			return invalid("the last project cannot be deleted")
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM projects WHERE id = ?", projectID,
		); err != nil {
			return fmt.Errorf("delete project: %w", err)
		}
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.CurrentWorkspace(ctx)
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

// insertFlow appends the new flowsheet to the end of its project's tab strip.
func insertFlow(ctx context.Context, tx *sql.Tx, projectID int64, name, now string) (int64, error) {
	result, err := tx.ExecContext(ctx, `
		INSERT INTO flows(project_id, name, created_at, updated_at, model_updated_at, position)
		VALUES(?, ?, ?, ?, ?, (
			SELECT COALESCE(MAX(position) + 1, 0) FROM flows WHERE project_id = ?
		))`,
		projectID, name, now, now, now, projectID,
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
