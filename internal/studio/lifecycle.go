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

// CreateFlow adds a flowsheet to a project. An empty name is not a mistake
// here: the tab strip's + button creates a sheet and opens inline rename on it
// with no dialog, so the project names the sheet itself. A submitted name is
// held to the same rules as everywhere else.
func (s *Studio) CreateFlow(ctx context.Context, projectID int64, name string) (Workspace, error) {
	generated := strings.TrimSpace(name) == ""
	if !generated {
		var err error
		if name, err = workspaceName("flowsheet", name); err != nil {
			return Workspace{}, err
		}
	}
	var flowID int64
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
		if generated {
			var err error
			if name, err = generatedFlowName(ctx, tx, projectID); err != nil {
				return err
			}
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		created, err := insertFlow(ctx, tx, projectID, name, now)
		if err != nil {
			return err
		}
		flowID = created
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

// DuplicateFlow copies a flowsheet whole: every block with its position and
// parameters, and every connection rewired to the copied blocks, so the copy
// simulates identically to its source instead of arriving as loose blocks.
//
// This is deliberately not what DuplicateBlocks does. Duplicating a selection
// inside one sheet leaves the copies unwired, because a sub-diagram that
// silently rewired itself is harder to reason about; duplicating a whole sheet
// has no such ambiguity, since the copy contains both ends of every wire.
//
// Simulation runs are not copied — the copy has never been run, and the amber
// tab dot should say so — and neither is the source's history. The copy opens
// with one event of its own, naming where it came from.
func (s *Studio) DuplicateFlow(ctx context.Context, flowID int64) (Workspace, error) {
	var projectID, copyID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var name string
		var position int
		if err := tx.QueryRowContext(ctx,
			"SELECT project_id, name, position FROM flows WHERE id = ?", flowID,
		).Scan(&projectID, &name, &position); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		// The copy belongs immediately right of the tab it came from, so
		// everything after that tab shifts one place along first.
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET position = position + 1 WHERE project_id = ? AND position > ?",
			projectID, position,
		); err != nil {
			return fmt.Errorf("make room for the copy: %w", err)
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO flows(project_id, name, created_at, updated_at, model_updated_at, position)
			VALUES(?, ?, ?, ?, ?, ?)`,
			projectID, copyName(name), now, now, now, position+1,
		)
		if err != nil {
			return fmt.Errorf("duplicate flowsheet: %w", err)
		}
		copyID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read copied flowsheet id: %w", err)
		}
		moved, err := copyBlocks(ctx, tx, flowID, copyID)
		if err != nil {
			return err
		}
		if err := copyConnections(ctx, tx, flowID, copyID, moved); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE projects SET updated_at = ? WHERE id = ?", now, projectID,
		); err != nil {
			return fmt.Errorf("touch project: %w", err)
		}
		return insertEvent(ctx, tx, copyID, now, "Duplicated from "+name)
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.Workspace(ctx, projectID, copyID)
}

// copyName is the source's name with " copy" appended, shortened when that
// would push it past the limit workspaceName enforces, so a duplicate never
// carries a name the user could not have typed themselves.
func copyName(name string) string {
	const suffix = " copy"
	limit := maxWorkspaceNameLength - utf8.RuneCountInString(suffix)
	if runes := []rune(name); len(runes) > limit {
		name = strings.TrimRight(string(runes[:limit]), " ")
	}
	return name + suffix
}

// copyBlocks copies every block of one flowsheet into another and reports how
// the ids moved, so the connections can follow the blocks they join.
//
// The rows are copied column for column, carrying both parameters_json and the
// legacy amplitude, gain and time_constant columns. decodeParameters prefers
// the JSON and falls back to those columns for blocks written before the JSON
// column existed, so a copy that carried only one of the two would decode
// differently from the block it claims to be a copy of.
func copyBlocks(ctx context.Context, tx *sql.Tx, sourceFlowID, targetFlowID int64) (map[int64]int64, error) {
	type row struct {
		id                            int64
		kind, name, parameters        string
		x, y                          int
		amplitude, gain, timeConstant float64
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, kind, name, x, y, amplitude, gain, time_constant, parameters_json
		FROM blocks WHERE flow_id = ? ORDER BY id`, sourceFlowID)
	if err != nil {
		return nil, fmt.Errorf("load blocks to copy: %w", err)
	}
	// The whole selection is read before anything is written: the transaction
	// holds one connection, so an insert cannot run while this cursor is open.
	var source []row
	for rows.Next() {
		var block row
		if err := rows.Scan(
			&block.id, &block.kind, &block.name, &block.x, &block.y,
			&block.amplitude, &block.gain, &block.timeConstant, &block.parameters,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan block to copy: %w", err)
		}
		source = append(source, block)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close blocks to copy: %w", err)
	}

	moved := make(map[int64]int64, len(source))
	for _, block := range source {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO blocks(
				flow_id, kind, name, x, y, amplitude, gain, time_constant, parameters_json
			)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			targetFlowID, block.kind, block.name, block.x, block.y,
			block.amplitude, block.gain, block.timeConstant, block.parameters,
		)
		if err != nil {
			return nil, fmt.Errorf("copy block %q: %w", block.name, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("read copied block id: %w", err)
		}
		moved[block.id] = id
	}
	return moved, nil
}

// copyConnections rewrites each wire of one flowsheet onto the copied blocks.
// Both endpoints are remapped, so no connection can point back at the source's
// blocks. Distinct ids map to distinct ids, so a set of wires that satisfied
// UNIQUE(flow_id, source_id, target_id) in the source satisfies it in the copy.
func copyConnections(ctx context.Context, tx *sql.Tx, sourceFlowID, targetFlowID int64, moved map[int64]int64) error {
	type edge struct {
		source, target int64
	}
	rows, err := tx.QueryContext(ctx,
		"SELECT source_id, target_id FROM connections WHERE flow_id = ? ORDER BY id",
		sourceFlowID,
	)
	if err != nil {
		return fmt.Errorf("load connections to copy: %w", err)
	}
	var edges []edge
	for rows.Next() {
		var wire edge
		if err := rows.Scan(&wire.source, &wire.target); err != nil {
			rows.Close()
			return fmt.Errorf("scan connection to copy: %w", err)
		}
		edges = append(edges, wire)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close connections to copy: %w", err)
	}

	for _, wire := range edges {
		source, ok := moved[wire.source]
		if !ok {
			return fmt.Errorf("connection source %d is not a block of flowsheet %d", wire.source, sourceFlowID)
		}
		target, ok := moved[wire.target]
		if !ok {
			return fmt.Errorf("connection target %d is not a block of flowsheet %d", wire.target, sourceFlowID)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO connections(flow_id, source_id, target_id) VALUES(?, ?, ?)",
			targetFlowID, source, target,
		); err != nil {
			return fmt.Errorf("copy connection: %w", err)
		}
	}
	return nil
}

// DeleteFlow removes a flowsheet and, through ON DELETE CASCADE, its blocks,
// connections, events and simulation runs, for the same reason DeleteProject
// leans on the cascade: deleting the children by hand would be a second,
// divergent statement of what a flowsheet contains.
//
// It refuses the last flowsheet in a project. A project therefore always holds
// at least one sheet, which is what keeps the tab strip from ever rendering
// empty — and what guarantees there is a flowsheet to hand back here: the
// deleted tab's left neighbour, or its right neighbour when it was the first.
func (s *Studio) DeleteFlow(ctx context.Context, flowID int64) (Workspace, error) {
	var projectID, landingID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var position int
		if err := tx.QueryRowContext(ctx,
			"SELECT project_id, position FROM flows WHERE id = ?", flowID,
		).Scan(&projectID, &position); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var survivors int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM flows WHERE project_id = ? AND id <> ?", projectID, flowID,
		).Scan(&survivors); err != nil {
			return fmt.Errorf("count remaining flowsheets: %w", err)
		}
		if survivors == 0 {
			return invalid("a project must keep at least one flowsheet")
		}
		// (position, id) is the tab strip's own order, so the flowsheet found
		// here is the tab that visually takes the deleted one's place.
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM flows
			WHERE project_id = ? AND (position < ? OR (position = ? AND id < ?))
			ORDER BY position DESC, id DESC LIMIT 1`,
			projectID, position, position, flowID,
		).Scan(&landingID)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, `
				SELECT id FROM flows
				WHERE project_id = ? AND (position > ? OR (position = ? AND id > ?))
				ORDER BY position, id LIMIT 1`,
				projectID, position, position, flowID,
			).Scan(&landingID)
		}
		if err != nil {
			return fmt.Errorf("choose the next flowsheet: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM flows WHERE id = ?", flowID); err != nil {
			return fmt.Errorf("delete flowsheet: %w", err)
		}
		// Closing the gap keeps positions dense, which is what lets insertFlow
		// append at MAX(position) + 1 without leaving holes behind.
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET position = position - 1 WHERE project_id = ? AND position > ?",
			projectID, position,
		); err != nil {
			return fmt.Errorf("close the tab strip gap: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE projects SET updated_at = ? WHERE id = ?",
			s.now().UTC().Format(time.RFC3339Nano), projectID,
		); err != nil {
			return fmt.Errorf("touch project: %w", err)
		}
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.Workspace(ctx, projectID, landingID)
}

// ReorderFlows rewrites a project's tab order from the full ordered id list the
// strip is showing.
//
// It takes the whole list rather than one moved id and an index because the
// client already knows the order it drew, and because comparing that list
// against the project's own flowsheets is what makes an omitted, repeated or
// foreign id detectable at all. A list that is not a permutation of this
// project's flowsheets is rejected whole, inside the transaction, so a rejected
// reorder leaves every position exactly as it was.
//
// The returned workspace opens the project's first tab, as ProjectWorkspace
// does. Reordering does not change which sheet the user is on, so a caller
// holding an open flowsheet should re-render the strip from Flows rather than
// follow Snapshot.
func (s *Studio) ReorderFlows(ctx context.Context, projectID int64, flowIDs []int64) (Workspace, error) {
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
		rows, err := tx.QueryContext(ctx, "SELECT id FROM flows WHERE project_id = ?", projectID)
		if err != nil {
			return fmt.Errorf("load flowsheet order: %w", err)
		}
		belongs := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("scan flowsheet id: %w", err)
			}
			belongs[id] = true
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close flowsheet order: %w", err)
		}

		refusal := invalid(
			"the new order must list each of this project's %d flowsheets exactly once",
			len(belongs),
		)
		if len(flowIDs) != len(belongs) {
			return refusal
		}
		placed := make(map[int64]bool, len(flowIDs))
		for _, flowID := range flowIDs {
			if !belongs[flowID] || placed[flowID] {
				return refusal
			}
			placed[flowID] = true
		}

		for position, flowID := range flowIDs {
			result, err := tx.ExecContext(ctx,
				"UPDATE flows SET position = ? WHERE id = ? AND project_id = ?",
				position, flowID, projectID,
			)
			if err != nil {
				return fmt.Errorf("reorder flowsheets: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected == 0 {
				return ErrNotFound
			}
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE projects SET updated_at = ? WHERE id = ?",
			s.now().UTC().Format(time.RFC3339Nano), projectID,
		); err != nil {
			return fmt.Errorf("touch project: %w", err)
		}
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return s.ProjectWorkspace(ctx, projectID)
}

// generatedFlowName names a sheet the user did not name. It fills the lowest
// free number: with Flowsheet 2 present and Flowsheet 1 deleted, the next sheet
// is Flowsheet 1, not Flowsheet 3. Reusing a freed number keeps the numbering as
// short as the project is, where counting past the highest ever used would
// leave a one-sheet project whose only sheet is Flowsheet 9. The loop always
// terminates: at most len(taken) numbers can be in use.
func generatedFlowName(ctx context.Context, tx *sql.Tx, projectID int64) (string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM flows WHERE project_id = ?", projectID)
	if err != nil {
		return "", fmt.Errorf("load flowsheet names: %w", err)
	}
	taken := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan flowsheet name: %w", err)
		}
		taken[name] = true
	}
	if err := rows.Close(); err != nil {
		return "", fmt.Errorf("close flowsheet names: %w", err)
	}
	for number := 1; ; number++ {
		candidate := fmt.Sprintf("Flowsheet %d", number)
		if !taken[candidate] {
			return candidate, nil
		}
	}
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
