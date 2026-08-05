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
	return s.addBlock(ctx, flowID, kind, position, nil)
}

// AddConfiguredBlock validates the supplied editor values and creates the
// block in the same transaction as its model revision and event. The caller
// only states the add intent; generated names, defaults, validation, and
// persistence remain owned by Studio.
func (s *Studio) AddConfiguredBlock(
	ctx context.Context,
	flowID int64,
	kind BlockKind,
	position Point,
	parameters map[string]string,
) (Snapshot, int64, error) {
	return s.addBlock(ctx, flowID, kind, position, parameters)
}

func (s *Studio) addBlock(
	ctx context.Context,
	flowID int64,
	kind BlockKind,
	position Point,
	parameters map[string]string,
) (Snapshot, int64, error) {
	if !kind.Valid() {
		return Snapshot{}, 0, invalid("unknown block type %q", kind)
	}
	position = clampPosition(position)
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
		name := fmt.Sprintf("%s %d", kind.Label(), count+1)
		block := Block{
			FlowID: flowID, Kind: kind, Name: name, Position: position,
			Parameters: defaultParameters(kind),
		}
		if parameters != nil {
			validated, err := validateBlockUpdate(block, BlockUpdate{
				Name: name, Parameters: parameters,
			})
			if err != nil {
				return err
			}
			block = validated
		}
		placed, err := openPosition(ctx, tx, flowID, position)
		if err != nil {
			return err
		}
		encoded, err := encodeParameters(block.Parameters)
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
		message := fmt.Sprintf("Added %s", name)
		if parameters != nil {
			// Configured adds used to leave an Update event newest; keep that
			// activity vocabulary while committing only one event.
			message = fmt.Sprintf("Updated %s", name)
		}
		return s.touchModel(ctx, tx, flowID, message)
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
		if err := checkWiredInputPorts(ctx, tx, block); err != nil {
			return err
		}
		if err := checkWiredPortCompatibility(ctx, tx, block); err != nil {
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

// checkWiredInputPorts refuses a parameter edit that would take away an input
// port a wire is already sitting on. Shrinking a Sum's signs is the case that
// makes this real: dropping the wire to fit would throw away a signal the
// user drew, silently, while they were editing something else. Refusing
// instead leaves them to disconnect it deliberately, or to keep the port.
func checkWiredInputPorts(ctx context.Context, tx *sql.Tx, block Block) error {
	var highest sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		"SELECT MAX(target_port) FROM connections WHERE target_id = ?", block.ID,
	).Scan(&highest); err != nil {
		return fmt.Errorf("read wired input ports: %w", err)
	}
	if !highest.Valid {
		return nil
	}
	port := int(highest.Int64)
	if port < block.InputPortCount() {
		return nil
	}
	// The highest wired port is the one named, not the first that would be
	// orphaned: it is the port that sets how far the edit can shrink.
	return invalid("%s has a wire on input port %d; disconnect it first", block.Name, port)
}

func checkWiredPortCompatibility(ctx context.Context, tx *sql.Tx, changed Block) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT source_id, source_port, target_id, target_port
		FROM connections
		WHERE source_id = ? OR target_id = ?
		ORDER BY id`,
		changed.ID, changed.ID,
	)
	if err != nil {
		return fmt.Errorf("read connected port widths: %w", err)
	}
	defer rows.Close()

	var wires []Wire
	for rows.Next() {
		var wire Wire
		if err := rows.Scan(
			&wire.SourceID, &wire.SourcePort, &wire.TargetID, &wire.TargetPort,
		); err != nil {
			return fmt.Errorf("scan connected port widths: %w", err)
		}
		wires = append(wires, wire)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate connected port widths: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close connected port widths: %w", err)
	}
	connected, err := blocksConnectedTo(ctx, tx, changed.ID)
	if err != nil {
		return err
	}
	for _, wire := range wires {
		source := changed
		if wire.SourceID != changed.ID {
			var ok bool
			source, ok = connected[wire.SourceID]
			if !ok {
				return fmt.Errorf("load connected source block %d: %w", wire.SourceID, ErrNotFound)
			}
		}
		target := changed
		if wire.TargetID != changed.ID {
			var ok bool
			target, ok = connected[wire.TargetID]
			if !ok {
				return fmt.Errorf("load connected target block %d: %w", wire.TargetID, ErrNotFound)
			}
		}
		if err := validateConnectionWidth(
			source, wire.SourcePort, target, wire.TargetPort,
		); err != nil {
			return err
		}
	}
	return nil
}

func blocksConnectedTo(
	ctx context.Context,
	tx *sql.Tx,
	blockID int64,
) (map[int64]Block, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, parameters_json
		FROM blocks
		WHERE id IN (
			SELECT target_id FROM connections WHERE source_id = ?
			UNION
			SELECT source_id FROM connections WHERE target_id = ?
		)
		ORDER BY id`,
		blockID, blockID,
	)
	if err != nil {
		return nil, fmt.Errorf("load connected blocks: %w", err)
	}
	defer rows.Close()

	blocks := make(map[int64]Block)
	for rows.Next() {
		var block Block
		var encoded string
		if err := rows.Scan(
			&block.ID, &block.FlowID, &block.Kind, &block.Name,
			&block.Position.X, &block.Position.Y, &encoded,
		); err != nil {
			return nil, fmt.Errorf("scan connected block: %w", err)
		}
		block.Parameters, err = decodeParameters(block.Kind, encoded)
		if err != nil {
			return nil, fmt.Errorf("decode parameters for %s: %w", block.Name, err)
		}
		blocks[block.ID] = block
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read connected blocks: %w", err)
	}
	return blocks, nil
}

func (s *Studio) DeleteBlock(ctx context.Context, blockID int64) (Snapshot, error) {
	var flowID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		block, err := blockByID(ctx, tx, blockID)
		if err != nil {
			return err
		}
		flowID = block.FlowID
		if err := clearControlRoleSpecForBlocks(
			ctx, tx, flowID, []int64{blockID},
		); err != nil {
			return err
		}
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
		if err := clearControlRoleSpecForBlocks(
			ctx, tx, flowID, blockIDs,
		); err != nil {
			return err
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
			excluded := map[int64]bool{block.ID: true}
			if len(blockIDs) > 1 {
				// A selection is duplicated as a group. Its originals remain
				// occupied during fallback so the copies cannot land on them.
				excluded = nil
			}
			placed, err := openPositionExcluding(ctx, tx, flowID, clampPosition(Point{
				X: block.Position.X + GridPitch,
				Y: block.Position.Y + GridPitch,
			}), excluded)
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

func (s *Studio) Connect(ctx context.Context, flowID int64, wire Wire) (Snapshot, error) {
	if wire.SourceID == wire.TargetID {
		return Snapshot{}, invalid("a block cannot connect to itself")
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		source, err := blockByID(ctx, tx, wire.SourceID)
		if err != nil {
			return err
		}
		target, err := blockByID(ctx, tx, wire.TargetID)
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
		// The two checks above answer "has terminals in that direction at
		// all"; these answer "has that one". A wire onto a port the block
		// does not expose would be invisible on the canvas and unreachable
		// from the inspector, so it is refused rather than stored.
		if !source.hasOutputPort(wire.SourcePort) {
			return invalid("%s has no output port %d", source.Name, wire.SourcePort)
		}
		if !target.hasInputPort(wire.TargetPort) {
			return invalid("%s has no input port %d", target.Name, wire.TargetPort)
		}
		if err := validateConnectionWidth(
			source, wire.SourcePort, target, wire.TargetPort,
		); err != nil {
			return err
		}

		var duplicate int
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM connections
			WHERE flow_id = ? AND source_id = ? AND source_port = ?
				AND target_id = ? AND target_port = ?`,
			flowID, wire.SourceID, wire.SourcePort, wire.TargetID, wire.TargetPort,
		).Scan(&duplicate)
		if err != nil {
			return err
		}
		if duplicate > 0 {
			return invalid("those blocks are already connected")
		}
		// An input port carries one signal. A second wire onto it would be a
		// junction nobody drew, so it is refused here rather than left for
		// compileModel to discover. This is the whole of the old "everything
		// but Sum accepts one input" rule: a Sum has a port per sign, so its
		// wires land on different ports and never meet this check.
		var occupied int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM connections WHERE flow_id = ? AND target_id = ? AND target_port = ?",
			flowID, wire.TargetID, wire.TargetPort,
		).Scan(&occupied); err != nil {
			return err
		}
		if occupied > 0 {
			// A block with a single input port has no port worth naming, and
			// that is the wording every such block has always shown.
			if target.InputPortCount() == 1 {
				return invalid("%s already has an input", target.Name)
			}
			return invalid("%s already has an input on port %d", target.Name, wire.TargetPort)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO connections(flow_id, source_id, source_port, target_id, target_port)
			VALUES(?, ?, ?, ?, ?)`,
			flowID, wire.SourceID, wire.SourcePort, wire.TargetID, wire.TargetPort,
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
	return openPositionExcluding(ctx, tx, flowID, desired, nil)
}

func openPositionExcluding(ctx context.Context, tx *sql.Tx, flowID int64, desired Point, excluded map[int64]bool) (Point, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, x, y FROM blocks WHERE flow_id = ?", flowID)
	if err != nil {
		return Point{}, fmt.Errorf("load block positions: %w", err)
	}
	var occupied []Point
	var desiredOccupied []Point
	for rows.Next() {
		var id int64
		var point Point
		if err := rows.Scan(&id, &point.X, &point.Y); err != nil {
			rows.Close()
			return Point{}, fmt.Errorf("scan block position: %w", err)
		}
		occupied = append(occupied, point)
		if !excluded[id] {
			desiredOccupied = append(desiredOccupied, point)
		}
	}
	if err := rows.Close(); err != nil {
		return Point{}, fmt.Errorf("close block positions: %w", err)
	}

	available := func(candidate Point, points []Point) bool {
		for _, point := range points {
			if abs(candidate.X-point.X) < BlockWidth && abs(candidate.Y-point.Y) < BlockHeight {
				return false
			}
		}
		return true
	}
	if available(desired, desiredOccupied) {
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
			if available(candidate, occupied) {
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
