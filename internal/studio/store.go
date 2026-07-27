package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS flows (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS blocks (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	x INTEGER NOT NULL,
	y INTEGER NOT NULL,
	amplitude REAL NOT NULL DEFAULT 0,
	gain REAL NOT NULL DEFAULT 0,
	time_constant REAL NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS connections (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	source_id INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
	target_id INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
	UNIQUE(flow_id, source_id, target_id)
);
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	message TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS simulation_runs (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	duration REAL NOT NULL,
	sample_time REAL NOT NULL,
	result_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS blocks_flow_id_idx ON blocks(flow_id);
CREATE INDEX IF NOT EXISTS connections_flow_id_idx ON connections(flow_id);
CREATE INDEX IF NOT EXISTS events_flow_id_id_idx ON events(flow_id, id DESC);
`

func Open(ctx context.Context, path string) (*Studio, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}

	studio := &Studio{db: db, now: time.Now}
	if err := studio.seed(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return studio, nil
}

func (s *Studio) Close() error {
	return s.db.Close()
}

func (s *Studio) seed(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM flows").Scan(&count); err != nil {
		return fmt.Errorf("count flows: %w", err)
	}
	if count > 0 {
		return nil
	}

	return s.inTx(ctx, func(tx *sql.Tx) error {
		now := s.now().UTC().Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx,
			"INSERT INTO flows(name, created_at, updated_at) VALUES(?, ?, ?)",
			"Reactor temperature loop", now, now,
		)
		if err != nil {
			return fmt.Errorf("seed flow: %w", err)
		}
		flowID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("seed flow id: %w", err)
		}

		type seedBlock struct {
			kind BlockKind
			name string
			x, y int
			p    Parameters
		}
		seeds := []seedBlock{
			{BlockSource, "Feed setpoint", 50, 90, Parameters{Amplitude: 1}},
			{BlockGain, "Valve gain", 260, 90, Parameters{Gain: 1.8}},
			{BlockLag, "Reactor", 470, 90, Parameters{TimeConstant: 2.2}},
			{BlockSource, "Disturbance", 50, 350, Parameters{Amplitude: 0.3}},
			{BlockLag, "Jacket lag", 260, 350, Parameters{TimeConstant: 4}},
			{BlockGain, "Heat loss", 470, 350, Parameters{Gain: -0.7}},
			{BlockSum, "Energy balance", 680, 220, Parameters{}},
			{BlockScope, "Temperature", 890, 220, Parameters{}},
		}

		ids := make([]int64, len(seeds))
		for i, seed := range seeds {
			result, err := tx.ExecContext(ctx, `
				INSERT INTO blocks(flow_id, kind, name, x, y, amplitude, gain, time_constant)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
				flowID, seed.kind, seed.name, seed.x, seed.y,
				seed.p.Amplitude, seed.p.Gain, seed.p.TimeConstant,
			)
			if err != nil {
				return fmt.Errorf("seed block %q: %w", seed.name, err)
			}
			ids[i], err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("seed block id: %w", err)
			}
		}

		edges := [][2]int{{0, 1}, {1, 2}, {2, 6}, {3, 4}, {4, 5}, {5, 6}, {6, 7}}
		for _, edge := range edges {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO connections(flow_id, source_id, target_id) VALUES(?, ?, ?)",
				flowID, ids[edge[0]], ids[edge[1]],
			); err != nil {
				return fmt.Errorf("seed connection: %w", err)
			}
		}
		return insertEvent(ctx, tx, flowID, now, "Example flowsheet created")
	})
}

func (s *Studio) inTx(ctx context.Context, action func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := action(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, flowID int64, now, message string) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO events(flow_id, message, created_at) VALUES(?, ?, ?)",
		flowID, message, now,
	)
	return err
}

func (s *Studio) snapshot(ctx context.Context, flowID int64) (Snapshot, error) {
	var snapshot Snapshot
	var created, updated string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at, updated_at FROM flows WHERE id = ?", flowID,
	).Scan(&snapshot.Flow.ID, &snapshot.Flow.Name, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load flow: %w", err)
	}
	snapshot.Flow.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	snapshot.Flow.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, amplitude, gain, time_constant
		FROM blocks WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load blocks: %w", err)
	}
	for rows.Next() {
		var block Block
		if err := rows.Scan(
			&block.ID, &block.FlowID, &block.Kind, &block.Name,
			&block.Position.X, &block.Position.Y,
			&block.Parameters.Amplitude, &block.Parameters.Gain, &block.Parameters.TimeConstant,
		); err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("scan block: %w", err)
		}
		snapshot.Blocks = append(snapshot.Blocks, block)
	}
	if err := rows.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("close blocks: %w", err)
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT id, flow_id, source_id, target_id
		FROM connections WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load connections: %w", err)
	}
	for rows.Next() {
		var connection Connection
		if err := rows.Scan(&connection.ID, &connection.FlowID, &connection.SourceID, &connection.TargetID); err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("scan connection: %w", err)
		}
		snapshot.Connections = append(snapshot.Connections, connection)
	}
	if err := rows.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("close connections: %w", err)
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT id, message, created_at FROM events
		WHERE flow_id = ? ORDER BY id DESC LIMIT 8`, flowID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load events: %w", err)
	}
	for rows.Next() {
		var event Event
		var timestamp string
		if err := rows.Scan(&event.ID, &event.Message, &timestamp); err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("scan event: %w", err)
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, timestamp)
		snapshot.Events = append(snapshot.Events, event)
	}
	if err := rows.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("close events: %w", err)
	}

	var runJSON string
	var runCreated string
	var run Simulation
	err = s.db.QueryRowContext(ctx, `
		SELECT id, created_at, duration, sample_time, result_json
		FROM simulation_runs WHERE flow_id = ? ORDER BY id DESC LIMIT 1`, flowID,
	).Scan(&run.ID, &runCreated, &run.Duration, &run.SampleTime, &runJSON)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Snapshot{}, fmt.Errorf("load simulation: %w", err)
	default:
		run.CreatedAt, _ = time.Parse(time.RFC3339Nano, runCreated)
		if err := json.Unmarshal([]byte(runJSON), &run); err != nil {
			return Snapshot{}, fmt.Errorf("decode simulation: %w", err)
		}
		snapshot.LastRun = &run
	}
	return snapshot, nil
}

func blockByID(ctx context.Context, tx *sql.Tx, id int64) (Block, error) {
	var block Block
	err := tx.QueryRowContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, amplitude, gain, time_constant
		FROM blocks WHERE id = ?`, id,
	).Scan(
		&block.ID, &block.FlowID, &block.Kind, &block.Name,
		&block.Position.X, &block.Position.Y,
		&block.Parameters.Amplitude, &block.Parameters.Gain, &block.Parameters.TimeConstant,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Block{}, ErrNotFound
	}
	if err != nil {
		return Block{}, fmt.Errorf("load block: %w", err)
	}
	return block, nil
}
