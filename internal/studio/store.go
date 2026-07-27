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
	updated_at TEXT NOT NULL,
	model_updated_at TEXT NOT NULL
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
	time_constant REAL NOT NULL DEFAULT 0,
	parameters_json TEXT NOT NULL DEFAULT ''
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
	if err := ensureModelUpdatedAt(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureParametersJSON(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	studio := &Studio{db: db, now: time.Now}
	if err := studio.seed(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return studio, nil
}

func ensureParametersJSON(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(blocks)")
	if err != nil {
		return fmt.Errorf("inspect blocks schema: %w", err)
	}
	found := false
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan blocks schema: %w", err)
		}
		if name == "parameters_json" {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close blocks schema: %w", err)
	}
	if found {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		"ALTER TABLE blocks ADD COLUMN parameters_json TEXT NOT NULL DEFAULT ''",
	); err != nil {
		return fmt.Errorf("add block parameters: %w", err)
	}
	return nil
}

func ensureModelUpdatedAt(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(flows)")
	if err != nil {
		return fmt.Errorf("inspect flows schema: %w", err)
	}
	found := false
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan flows schema: %w", err)
		}
		if name == "model_updated_at" {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close flows schema: %w", err)
	}
	if found {
		return nil
	}
	if _, err := db.ExecContext(ctx,
		"ALTER TABLE flows ADD COLUMN model_updated_at TEXT NOT NULL DEFAULT ''",
	); err != nil {
		return fmt.Errorf("add model revision: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE flows SET model_updated_at = updated_at WHERE model_updated_at = ''",
	); err != nil {
		return fmt.Errorf("initialize model revision: %w", err)
	}
	return nil
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
			"INSERT INTO flows(name, created_at, updated_at, model_updated_at) VALUES(?, ?, ?, ?)",
			"Reactor temperature loop", now, now, now,
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
		// Laid out on the sheet lattice so the demo flowsheet opens on-grid.
		seeds := []seedBlock{
			{BlockSource, "Feed setpoint", 60, 80, Parameters{Amplitude: 1}},
			{BlockGain, "Valve gain", 300, 80, Parameters{Gain: 1.8}},
			{BlockLag, "Reactor", 540, 80, Parameters{TimeConstant: 2.2}},
			{BlockSource, "Disturbance", 60, 320, Parameters{Amplitude: 0.3}},
			{BlockLag, "Jacket lag", 300, 320, Parameters{TimeConstant: 4}},
			{BlockGain, "Heat loss", 540, 320, Parameters{Gain: -0.7}},
			{BlockSum, "Energy balance", 780, 200, Parameters{}},
			{BlockScope, "Temperature", 1020, 200, Parameters{}},
		}

		ids := make([]int64, len(seeds))
		for i, seed := range seeds {
			encoded, err := encodeParameters(seed.p)
			if err != nil {
				return err
			}
			placed := clampPosition(Point{X: seed.x, Y: seed.y})
			result, err := tx.ExecContext(ctx, `
				INSERT INTO blocks(
					flow_id, kind, name, x, y, amplitude, gain, time_constant, parameters_json
				)
				VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				flowID, seed.kind, seed.name, placed.X, placed.Y,
				seed.p.Amplitude, seed.p.Gain, seed.p.TimeConstant, encoded,
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
	var created, updated, modelUpdated string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, created_at, updated_at, model_updated_at FROM flows WHERE id = ?", flowID,
	).Scan(&snapshot.Flow.ID, &snapshot.Flow.Name, &created, &updated, &modelUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("load flow: %w", err)
	}
	snapshot.Flow.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	snapshot.Flow.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	snapshot.Flow.ModelUpdatedAt, _ = time.Parse(time.RFC3339Nano, modelUpdated)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, amplitude, gain, time_constant, parameters_json
		FROM blocks WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load blocks: %w", err)
	}
	for rows.Next() {
		var block Block
		var legacy Parameters
		var encoded string
		if err := rows.Scan(
			&block.ID, &block.FlowID, &block.Kind, &block.Name,
			&block.Position.X, &block.Position.Y,
			&legacy.Amplitude, &legacy.Gain, &legacy.TimeConstant, &encoded,
		); err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("scan block: %w", err)
		}
		block.Parameters, err = decodeParameters(block.Kind, encoded, legacy)
		if err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("decode parameters for %s: %w", block.Name, err)
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
		FROM simulation_runs
		WHERE flow_id = ? AND created_at >= ?
		ORDER BY id DESC LIMIT 1`,
		flowID, snapshot.Flow.ModelUpdatedAt.UTC().Format(time.RFC3339Nano),
	).Scan(&run.ID, &runCreated, &run.Duration, &run.SampleTime, &runJSON)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Snapshot{}, fmt.Errorf("load simulation: %w", err)
	default:
		runID := run.ID
		runTime, _ := time.Parse(time.RFC3339Nano, runCreated)
		duration := run.Duration
		sampleTime := run.SampleTime
		if err := json.Unmarshal([]byte(runJSON), &run); err != nil {
			return Snapshot{}, fmt.Errorf("decode simulation: %w", err)
		}
		run.ID = runID
		run.CreatedAt = runTime
		run.Duration = duration
		run.SampleTime = sampleTime
		snapshot.LastRun = &run
	}
	return snapshot, nil
}

func blockByID(ctx context.Context, tx *sql.Tx, id int64) (Block, error) {
	var block Block
	var legacy Parameters
	var encoded string
	err := tx.QueryRowContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, amplitude, gain, time_constant, parameters_json
		FROM blocks WHERE id = ?`, id,
	).Scan(
		&block.ID, &block.FlowID, &block.Kind, &block.Name,
		&block.Position.X, &block.Position.Y,
		&legacy.Amplitude, &legacy.Gain, &legacy.TimeConstant, &encoded,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Block{}, ErrNotFound
	}
	if err != nil {
		return Block{}, fmt.Errorf("load block: %w", err)
	}
	block.Parameters, err = decodeParameters(block.Kind, encoded, legacy)
	if err != nil {
		return Block{}, fmt.Errorf("decode parameters for %s: %w", block.Name, err)
	}
	return block, nil
}

func encodeParameters(parameters Parameters) (string, error) {
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return "", fmt.Errorf("encode block parameters: %w", err)
	}
	return string(encoded), nil
}

func decodeParameters(kind BlockKind, encoded string, legacy Parameters) (Parameters, error) {
	parameters := defaultParameters(kind)
	if encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &parameters); err != nil {
			return Parameters{}, err
		}
		return parameters, nil
	}
	switch kind {
	case BlockSource:
		parameters.Amplitude = legacy.Amplitude
	case BlockGain:
		parameters.Gain = legacy.Gain
	case BlockLag:
		parameters.TimeConstant = legacy.TimeConstant
	}
	return parameters, nil
}
