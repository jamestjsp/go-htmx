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
		encoded, err := encodeParameters(parameters)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO blocks(flow_id, kind, name, x, y, parameters_json)
			VALUES(?, ?, ?, ?, ?, ?)`,
			flowID, kind, name, placed.X, placed.Y, encoded,
		)
		if err != nil {
			return fmt.Errorf("add block: %w", err)
		}
		blockID, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read block id: %w", err)
		}
		return s.touchModel(ctx, tx, flowID, fmt.Sprintf("Added %s", name))
	})
	if err != nil {
		return Snapshot{}, 0, err
	}
	snapshot, err := s.snapshot(ctx, flowID)
	return snapshot, blockID, err
}

// BlockMove is one block's new home on the sheet.
type BlockMove struct {
	BlockID  int64
	Position Point
}

// MoveBlocks repositions a whole selection. Dragging several blocks is one
// user action, so it is one transaction: either the arrangement moves or
// none of it does. A block outside flowID is rejected without moving
// anything, which keeps a crafted request from reaching another flowsheet.
func (s *Studio) MoveBlocks(ctx context.Context, flowID int64, moves []BlockMove) error {
	if len(moves) == 0 {
		return invalid("select at least one block to move")
	}
	if len(moves) > maxBlocksPerRequest {
		return invalid("move at most %d blocks at once", maxBlocksPerRequest)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, move := range moves {
			position := clampPosition(move.Position)
			result, err := tx.ExecContext(ctx,
				"UPDATE blocks SET x = ?, y = ? WHERE id = ? AND flow_id = ?",
				position.X, position.Y, move.BlockID, flowID,
			)
			if err != nil {
				return fmt.Errorf("move blocks: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected == 0 {
				return ErrNotFound
			}
		}
		return s.touchLayout(ctx, tx, flowID)
	})
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
		return s.touchLayout(ctx, tx, block.FlowID)
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
		encoded, err := encodeParameters(block.Parameters)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE blocks
			SET name = ?, parameters_json = ?
			WHERE id = ?`,
			block.Name, encoded, block.ID,
		)
		if err != nil {
			return fmt.Errorf("update block: %w", err)
		}
		return s.touchModel(ctx, tx, flowID, fmt.Sprintf("Updated %s", block.Name))
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
		return s.touchModel(ctx, tx, flowID, fmt.Sprintf("Deleted %s", block.Name))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

// DeleteBlocks removes a whole selection, and every signal wired to it, in
// one transaction. Deleting block by block would leave a half-dismantled
// flowsheet visible if any step failed.
func (s *Studio) DeleteBlocks(ctx context.Context, flowID int64, blockIDs []int64) (Snapshot, error) {
	if len(blockIDs) == 0 {
		return Snapshot{}, invalid("select at least one block to delete")
	}
	if len(blockIDs) > maxBlocksPerRequest {
		return Snapshot{}, invalid("delete at most %d blocks at once", maxBlocksPerRequest)
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		names := make([]string, 0, len(blockIDs))
		for _, blockID := range blockIDs {
			block, err := blockByID(ctx, tx, blockID)
			if err != nil {
				return err
			}
			if block.FlowID != flowID {
				return ErrNotFound
			}
			names = append(names, block.Name)
		}
		for _, blockID := range blockIDs {
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM blocks WHERE id = ? AND flow_id = ?", blockID, flowID,
			); err != nil {
				return fmt.Errorf("delete blocks: %w", err)
			}
		}
		message := "Deleted " + names[0]
		if len(names) > 1 {
			message = fmt.Sprintf("Deleted %d blocks", len(names))
		}
		return s.touchModel(ctx, tx, flowID, message)
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

// DuplicateBlocks copies a selection one grid step down and right.
//
// Wires between the originals are deliberately not copied. A duplicated
// sub-diagram that silently rewired itself is harder to reason about than
// one the user connects on purpose, and it is the behaviour the shortcut
// sheet documents.
func (s *Studio) DuplicateBlocks(ctx context.Context, flowID int64, blockIDs []int64) (Snapshot, error) {
	if len(blockIDs) == 0 {
		return Snapshot{}, invalid("select at least one block to duplicate")
	}
	if len(blockIDs) > maxBlocksPerRequest {
		return Snapshot{}, invalid("duplicate at most %d blocks at once", maxBlocksPerRequest)
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		copied := 0
		for _, blockID := range blockIDs {
			block, err := blockByID(ctx, tx, blockID)
			if err != nil {
				return err
			}
			if block.FlowID != flowID {
				return ErrNotFound
			}
			placed, err := openPosition(ctx, tx, flowID, clampPosition(Point{
				X: block.Position.X + GridPitch,
				Y: block.Position.Y + GridPitch,
			}))
			if err != nil {
				return err
			}
			name, err := availableBlockName(ctx, tx, flowID, block.Name)
			if err != nil {
				return err
			}
			encoded, err := encodeParameters(block.Parameters)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO blocks(flow_id, kind, name, x, y, parameters_json)
				VALUES(?, ?, ?, ?, ?, ?)`,
				flowID, block.Kind, name, placed.X, placed.Y, encoded,
			); err != nil {
				return fmt.Errorf("duplicate block: %w", err)
			}
			copied++
		}
		message := fmt.Sprintf("Duplicated %d blocks", copied)
		if copied == 1 {
			message = "Duplicated 1 block"
		}
		return s.touchModel(ctx, tx, flowID, message)
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

// availableBlockName finds a free "<name> copy" variant, so duplicates stay
// distinguishable in the inspector and the trend legend.
func availableBlockName(ctx context.Context, tx *sql.Tx, flowID int64, base string) (string, error) {
	taken := map[string]bool{}
	rows, err := tx.QueryContext(ctx, "SELECT name FROM blocks WHERE flow_id = ?", flowID)
	if err != nil {
		return "", fmt.Errorf("load block names: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan block name: %w", err)
		}
		taken[name] = true
	}
	if err := rows.Close(); err != nil {
		return "", fmt.Errorf("close block names: %w", err)
	}
	for attempt := 1; attempt <= maxBlocksPerRequest+1; attempt++ {
		candidate := base + " copy"
		if attempt > 1 {
			candidate = fmt.Sprintf("%s copy %d", base, attempt)
		}
		if len(candidate) > 48 {
			candidate = candidate[len(candidate)-48:]
		}
		if !taken[candidate] {
			return candidate, nil
		}
	}
	return "", invalid("too many copies of %q", base)
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
		return s.touchModel(ctx, tx, flowID, fmt.Sprintf("Connected %s → %s", source.Name, target.Name))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

// DisconnectBlock removes every signal into or out of one block, so a block
// can be isolated without hunting its wires one at a time in the inspector.
func (s *Studio) DisconnectBlock(ctx context.Context, blockID int64) (Snapshot, error) {
	var flowID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		block, err := blockByID(ctx, tx, blockID)
		if err != nil {
			return err
		}
		flowID = block.FlowID
		result, err := tx.ExecContext(ctx,
			"DELETE FROM connections WHERE source_id = ? OR target_id = ?", blockID, blockID)
		if err != nil {
			return fmt.Errorf("disconnect block: %w", err)
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if removed == 0 {
			return nil
		}
		return s.touchModel(ctx, tx, flowID, fmt.Sprintf("Disconnected %s", block.Name))
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
		return s.touchModel(ctx, tx, flowID, fmt.Sprintf("Disconnected %s → %s", sourceName, targetName))
	})
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshot(ctx, flowID)
}

// touchModel records an edit to what a flowsheet simulates: it stamps both
// updated_at and model_updated_at, then logs message as an event. Every
// mutation that adds, removes, or rewires a block goes through this, because
// which operations count as a model edit is exactly the boundary flowSelect
// (workspace.go) reads to light the amber staleness dot — moving
// model_updated_at is what makes a flowsheet's last simulation run stale.
func (s *Studio) touchModel(ctx context.Context, tx *sql.Tx, flowID int64, message string) error {
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		"UPDATE flows SET updated_at = ?, model_updated_at = ? WHERE id = ?",
		now, now, flowID,
	); err != nil {
		return err
	}
	return insertEvent(ctx, tx, flowID, now, message)
}

// touchLayout stamps updated_at only. Rearranging blocks on the sheet changes
// nothing a simulation depends on, so model_updated_at stays put and nothing
// is logged — the event feed would otherwise fill with every drag.
func (s *Studio) touchLayout(ctx context.Context, tx *sql.Tx, flowID int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE flows SET updated_at = ? WHERE id = ?",
		s.now().UTC().Format(time.RFC3339Nano), flowID)
	return err
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
			if abs(candidate.X-point.X) < BlockWidth && abs(candidate.Y-point.Y) < BlockHeight {
				return false
			}
		}
		return true
	}
	if available(desired) {
		return desired, nil
	}
	// Walk a lattice with room for a wire run between neighbours, in reading
	// order from the origin, so a cascade of new blocks stays where the user
	// is looking rather than scattering across the sheet.
	const (
		originX = 60
		originY = 80
		stepX   = BlockWidth + 68
		stepY   = BlockHeight + 36
	)
	for y := originY; y <= SheetHeight-BlockHeight; y += stepY {
		for x := originX; x <= SheetWidth-BlockWidth; x += stepX {
			candidate := clampPosition(Point{X: x, Y: y})
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
