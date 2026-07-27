package studio

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Studio struct {
	db  *sql.DB
	now func() time.Time
}

func (s *Studio) Current(ctx context.Context) (Snapshot, error) {
	var flowID int64
	err := s.db.QueryRowContext(ctx, "SELECT id FROM flows ORDER BY id LIMIT 1").Scan(&flowID)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load current flow: %w", err)
	}
	return s.snapshot(ctx, flowID)
}

func (s *Studio) Snapshot(ctx context.Context, flowID int64) (Snapshot, error) {
	return s.snapshot(ctx, flowID)
}

func (s *Studio) AddBlock(ctx context.Context, flowID int64, kind BlockKind, position Point) (Snapshot, int64, error) {
	if !kind.Valid() {
		return Snapshot{}, 0, invalid("unknown block type %q", kind)
	}
	position = clampPosition(position)
	parameters := defaultParameters(kind)
	var blockID int64

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT 1 FROM flows WHERE id = ?", flowID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		var count int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM blocks WHERE flow_id = ? AND kind = ?", flowID, kind,
		).Scan(&count); err != nil {
			return err
		}
		placed, err := openPosition(ctx, tx, flowID, position)
		if err != nil {
			return err
		}
		name := fmt.Sprintf("%s %d", kind.Label(), count+1)
		result, err := tx.ExecContext(ctx, `
			INSERT INTO blocks(flow_id, kind, name, x, y, amplitude, gain, time_constant)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			flowID, kind, name, placed.X, placed.Y,
			parameters.Amplitude, parameters.Gain, parameters.TimeConstant,
		)
		if err != nil {
			return fmt.Errorf("add block: %w", err)
		}
		blockID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read block id: %w", err)
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET updated_at = ?, model_updated_at = ? WHERE id = ?",
			now, now, flowID,
		); err != nil {
			return err
		}
		return insertEvent(ctx, tx, flowID, now, fmt.Sprintf("Added %s", name))
	})
	if err != nil {
		return Snapshot{}, 0, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	return snapshot, blockID, err
}

func (s *Studio) MoveBlock(ctx context.Context, blockID int64, position Point) error {
	position = clampPosition(position)
	return s.inTx(ctx, func(tx *sql.Tx) error {
		block, err := blockByID(ctx, tx, blockID)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			"UPDATE blocks SET x = ?, y = ? WHERE id = ?",
			position.X, position.Y, blockID,
		)
		if err != nil {
			return fmt.Errorf("move block: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, "UPDATE flows SET updated_at = ? WHERE id = ?",
			s.now().UTC().Format(time.RFC3339Nano), block.FlowID)
		return err
	})
}

func (s *Studio) UpdateBlock(ctx context.Context, blockID int64, update BlockUpdate) (Snapshot, error) {
	var flowID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		block, err := blockByID(ctx, tx, blockID)
		if err != nil {
			return err
		}
		block, err = validateBlockUpdate(block, update)
		if err != nil {
			return err
		}
		flowID = block.FlowID
		_, err = tx.ExecContext(ctx, `
			UPDATE blocks SET name = ?, amplitude = ?, gain = ?, time_constant = ?
			WHERE id = ?`,
			block.Name, block.Parameters.Amplitude, block.Parameters.Gain,
			block.Parameters.TimeConstant, block.ID,
		)
		if err != nil {
			return fmt.Errorf("update block: %w", err)
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET updated_at = ?, model_updated_at = ? WHERE id = ?",
			now, now, flowID,
		); err != nil {
			return err
		}
		return insertEvent(ctx, tx, flowID, now, fmt.Sprintf("Updated %s", block.Name))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

func (s *Studio) DeleteBlock(ctx context.Context, blockID int64) (Snapshot, error) {
	var flowID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		block, err := blockByID(ctx, tx, blockID)
		if err != nil {
			return err
		}
		flowID = block.FlowID
		if _, err := tx.ExecContext(ctx, "DELETE FROM blocks WHERE id = ?", blockID); err != nil {
			return fmt.Errorf("delete block: %w", err)
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET updated_at = ?, model_updated_at = ? WHERE id = ?",
			now, now, flowID,
		); err != nil {
			return err
		}
		return insertEvent(ctx, tx, flowID, now, fmt.Sprintf("Deleted %s", block.Name))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

func (s *Studio) Connect(ctx context.Context, flowID, sourceID, targetID int64) (Snapshot, error) {
	if sourceID == targetID {
		return Snapshot{}, invalid("a block cannot connect to itself")
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		source, err := blockByID(ctx, tx, sourceID)
		if err != nil {
			return err
		}
		target, err := blockByID(ctx, tx, targetID)
		if err != nil {
			return err
		}
		if source.FlowID != flowID || target.FlowID != flowID {
			return invalid("both blocks must belong to the active flowsheet")
		}
		if !source.Kind.HasOutput() {
			return invalid("%s does not have an output port", source.Name)
		}
		if !target.Kind.HasInput() {
			return invalid("%s does not have an input port", target.Name)
		}

		var duplicate int
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM connections
			WHERE flow_id = ? AND source_id = ? AND target_id = ?`,
			flowID, sourceID, targetID,
		).Scan(&duplicate)
		if err != nil {
			return err
		}
		if duplicate > 0 {
			return invalid("those blocks are already connected")
		}
		if target.Kind != BlockSum {
			var incoming int
			if err := tx.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM connections WHERE flow_id = ? AND target_id = ?",
				flowID, targetID,
			).Scan(&incoming); err != nil {
				return err
			}
			if incoming > 0 {
				return invalid("%s already has an input", target.Name)
			}
		}

		connections, err := connectionsInTx(ctx, tx, flowID)
		if err != nil {
			return err
		}
		if pathExists(connections, targetID, sourceID) {
			return invalid("that connection would create a cycle")
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO connections(flow_id, source_id, target_id) VALUES(?, ?, ?)`,
			flowID, sourceID, targetID,
		); err != nil {
			return fmt.Errorf("connect blocks: %w", err)
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET updated_at = ?, model_updated_at = ? WHERE id = ?",
			now, now, flowID,
		); err != nil {
			return err
		}
		return insertEvent(ctx, tx, flowID, now, fmt.Sprintf("Connected %s → %s", source.Name, target.Name))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

func (s *Studio) Disconnect(ctx context.Context, connectionID int64) (Snapshot, error) {
	var flowID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var sourceName, targetName string
		err := tx.QueryRowContext(ctx, `
			SELECT c.flow_id, source.name, target.name
			FROM connections c
			JOIN blocks source ON source.id = c.source_id
			JOIN blocks target ON target.id = c.target_id
			WHERE c.id = ?`, connectionID,
		).Scan(&flowID, &sourceName, &targetName)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM connections WHERE id = ?", connectionID); err != nil {
			return fmt.Errorf("disconnect blocks: %w", err)
		}
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx,
			"UPDATE flows SET updated_at = ?, model_updated_at = ? WHERE id = ?",
			now, now, flowID,
		); err != nil {
			return err
		}
		return insertEvent(ctx, tx, flowID, now, fmt.Sprintf("Disconnected %s → %s", sourceName, targetName))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

func openPosition(ctx context.Context, tx *sql.Tx, flowID int64, desired Point) (Point, error) {
	rows, err := tx.QueryContext(ctx, "SELECT x, y FROM blocks WHERE flow_id = ?", flowID)
	if err != nil {
		return Point{}, fmt.Errorf("load block positions: %w", err)
	}
	var occupied []Point
	for rows.Next() {
		var point Point
		if err := rows.Scan(&point.X, &point.Y); err != nil {
			rows.Close()
			return Point{}, fmt.Errorf("scan block position: %w", err)
		}
		occupied = append(occupied, point)
	}
	if err := rows.Close(); err != nil {
		return Point{}, fmt.Errorf("close block positions: %w", err)
	}

	available := func(candidate Point) bool {
		for _, point := range occupied {
			if abs(candidate.X-point.X) < 172 && abs(candidate.Y-point.Y) < 84 {
				return false
			}
		}
		return true
	}
	if available(desired) {
		return desired, nil
	}
	for _, y := range []int{90, 220, 350, 470} {
		for _, x := range []int{30, 210, 390, 570, 750, 930} {
			candidate := Point{X: x, Y: y}
			if available(candidate) {
				return candidate, nil
			}
		}
	}
	return desired, invalid("the flowsheet is full; move a block to make room")
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func connectionsInTx(ctx context.Context, tx *sql.Tx, flowID int64) ([]Connection, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT id, flow_id, source_id, target_id FROM connections WHERE flow_id = ?",
		flowID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var connections []Connection
	for rows.Next() {
		var connection Connection
		if err := rows.Scan(&connection.ID, &connection.FlowID, &connection.SourceID, &connection.TargetID); err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func pathExists(connections []Connection, start, goal int64) bool {
	adjacency := make(map[int64][]int64)
	for _, connection := range connections {
		adjacency[connection.SourceID] = append(adjacency[connection.SourceID], connection.TargetID)
	}
	seen := map[int64]bool{start: true}
	queue := []int64{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == goal {
			return true
		}
		for _, next := range adjacency[current] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func ValidationMessage(err error) string {
	var validation *ValidationError
	if errors.As(err, &validation) {
		return validation.Message
	}
	if errors.Is(err, ErrNotFound) {
		return "The requested item no longer exists."
	}
	return "The operation could not be completed."
}

func ParseBlockKind(value string) (BlockKind, error) {
	kind := BlockKind(strings.ToLower(strings.TrimSpace(value)))
	if !kind.Valid() {
		return "", invalid("unknown block type %q", value)
	}
	return kind, nil
}
