package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Foreign keys are enabled by the DSN, not here. A PRAGMA in this string would
// run on whichever connection executes it, which makes enforcement depend on
// the pool staying pinned to one connection — and would mask a broken DSN, so
// TestOpenEnforcesForeignKeys would pass while proving nothing.
//
// connectionColumns and connectionsFlowIndex are spelled once and reused by
// the rebuild in ensureConnectionPorts, so a database created fresh and one
// migrated up from an earlier version cannot end up carrying different
// constraints under the same name.
const connectionColumns = `(
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	source_id INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
	source_port INTEGER NOT NULL DEFAULT 0,
	target_id INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
	target_port INTEGER NOT NULL DEFAULT 0,
	UNIQUE(flow_id, source_id, source_port, target_id, target_port)
)`

const connectionsFlowIndex = "CREATE INDEX IF NOT EXISTS connections_flow_id_idx ON connections(flow_id)"

const schema = `
CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS flows (
	id INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	model_updated_at TEXT NOT NULL,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS blocks (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	x INTEGER NOT NULL,
	y INTEGER NOT NULL,
	parameters_json TEXT NOT NULL DEFAULT ''
);
` + "CREATE TABLE IF NOT EXISTS connections " + connectionColumns + `;
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
CREATE TABLE IF NOT EXISTS analysis_runs (
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	intent TEXT NOT NULL,
	created_at TEXT NOT NULL,
	model_updated_at TEXT NOT NULL,
	request_json TEXT NOT NULL,
	result_json TEXT NOT NULL,
	PRIMARY KEY(flow_id, intent)
);
CREATE TABLE IF NOT EXISTS control_model_specs (
	flow_id INTEGER PRIMARY KEY REFERENCES flows(id) ON DELETE CASCADE,
	version INTEGER NOT NULL,
	spec_json TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS blocks_flow_id_idx ON blocks(flow_id);
` + connectionsFlowIndex + `;
CREATE INDEX IF NOT EXISTS events_flow_id_id_idx ON events(flow_id, id DESC);
CREATE INDEX IF NOT EXISTS simulation_runs_flow_id_created_at_idx
	ON simulation_runs(flow_id, created_at);
CREATE INDEX IF NOT EXISTS analysis_runs_flow_id_idx
	ON analysis_runs(flow_id);
`

const defaultProjectName = "Process Lab project"

func Open(ctx context.Context, path string) (*Studio, error) {
	db, err := sql.Open("sqlite", dataSourceName(path))
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
	// After ensureParametersJSON, so the column this reads from is guaranteed
	// to exist whether it was just added or has been there all along.
	if err := ensureLegacyBlockParameters(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureConnectionPorts(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	// After both ensureLegacyBlockParameters and ensureConnectionPorts: it
	// reads the parameters the first backfilled and the ports the second
	// numbered, and reconciles them.
	if err := ensureDeclaredInputPorts(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureProjects(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	// After ensureProjects, so every flow already belongs to a project and the
	// backfill can number each project's tabs separately.
	if err := ensureFlowPositions(ctx, db); err != nil {
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

// dataSourceName asks the driver for foreign key enforcement on every
// connection it opens. Deleting a project destroys rows in five tables through
// ON DELETE CASCADE, and leaving that to the PRAGMA in the schema statement
// would make it depend on the pool staying pinned to one connection.
func dataSourceName(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=foreign_keys(1)"
}

func ensureParametersJSON(ctx context.Context, db *sql.DB) error {
	found, err := tableHasColumn(ctx, db, "blocks", "parameters_json")
	if err != nil {
		return err
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

// ensureLegacyBlockParameters fills parameters_json for rows written before
// that column existed, applying once, at migration time, the same
// kind-to-field mapping decodeParameters used to apply on every read. A fresh
// database never had the amplitude, gain and time_constant columns to begin
// with, so there is nothing here for it to do. An already-migrated row has a
// non-empty parameters_json and will not match the WHERE clause below, which
// is what makes re-running this on every Open safe rather than merely
// harmless.
func ensureLegacyBlockParameters(ctx context.Context, db *sql.DB) error {
	hasLegacyColumns, err := tableHasColumn(ctx, db, "blocks", "amplitude")
	if err != nil {
		return err
	}
	if !hasLegacyColumns {
		return nil
	}
	return inDBTx(ctx, db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			"SELECT id, kind, amplitude, gain, time_constant FROM blocks WHERE parameters_json = ''",
		)
		if err != nil {
			return fmt.Errorf("find legacy block parameters: %w", err)
		}
		type legacyBlock struct {
			id                            int64
			kind                          BlockKind
			amplitude, gain, timeConstant float64
		}
		var legacy []legacyBlock
		for rows.Next() {
			var block legacyBlock
			if err := rows.Scan(
				&block.id, &block.kind, &block.amplitude, &block.gain, &block.timeConstant,
			); err != nil {
				rows.Close()
				return fmt.Errorf("scan legacy block parameters: %w", err)
			}
			legacy = append(legacy, block)
		}
		// rows.Close alone would miss this: it reports the driver's own
		// close-time error, not an iteration failure that ended Next early.
		// Only rows.Err reports that, and a truncated read here would commit
		// a partial backfill — the rest of legacy's rows silently keep
		// decoding to catalog defaults for the life of the process, since
		// decodeParameters no longer has a fallback to catch them.
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read legacy block parameters: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close legacy block parameters: %w", err)
		}
		for _, block := range legacy {
			parameters := defaultParameters(block.kind)
			switch block.kind {
			case BlockSource:
				parameters.Amplitude = block.amplitude
				parameters.StepTime = 0
			case BlockGain:
				parameters.Gain = block.gain
			case BlockLag:
				parameters.TimeConstant = block.timeConstant
			}
			encoded, err := encodeParameters(parameters)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE blocks SET parameters_json = ? WHERE id = ?", encoded, block.id,
			); err != nil {
				return fmt.Errorf("backfill block %d parameters: %w", block.id, err)
			}
		}
		return nil
	})
}

// ensureConnectionPorts gives every stored wire the terminal identity the
// domain now carries. Adding the two columns is only half of it: the old
// UNIQUE(flow_id, source_id, target_id) has to become per-port, and SQLite
// cannot alter a constraint in place, so the table is rebuilt
// create-copy-drop-rename inside one transaction. A fresh database is created
// with the ports already there, so the column check below is what keeps this
// from running twice and renumbering a flowsheet a user has since rewired.
//
// Ports are numbered per target in connection-id order — exactly the order
// compileModel hands a Sum's signs to its inbound wires today — so no stored
// flowsheet changes what it computes. Nothing here names a block kind: every
// target is numbered the same way, and a target that has only ever held one
// wire lands on port 0 by arithmetic rather than by a rule about Sum. That
// also means a flowsheet an older version left over-wired keeps one wire per
// port instead of stacking several onto port 0.
func ensureConnectionPorts(ctx context.Context, db *sql.DB) error {
	found, err := tableHasColumn(ctx, db, "connections", "source_port")
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	// The rebuild is pinned to one connection because PRAGMA foreign_keys is
	// per-connection state, and it is turned off for the duration because
	// DROP TABLE performs an implicit delete of every row while they are
	// enforced, and because the copy would otherwise reject rows an older
	// version could have orphaned — compileModel's "a connection references a
	// missing block" exists precisely because such rows are possible. Those
	// rows open today, so refusing to migrate them would take a database the
	// user can still repair and make it unopenable.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve connection for port migration: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("suspend foreign keys: %w", err)
	}
	rebuild := rebuildConnectionsWithPorts(ctx, conn)
	// Restoring enforcement is not optional: every later write on this pooled
	// connection depends on it, and TestOpenEnforcesForeignKeys reads it back.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil && rebuild == nil {
		rebuild = fmt.Errorf("restore foreign keys: %w", err)
	}
	return rebuild
}

func rebuildConnectionsWithPorts(ctx context.Context, conn *sql.Conn) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin port migration: %w", err)
	}
	if err := copyConnectionsOntoPorts(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit port migration: %w", err)
	}
	return nil
}

func copyConnectionsOntoPorts(ctx context.Context, tx *sql.Tx) error {
	var before int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM connections").Scan(&before); err != nil {
		return fmt.Errorf("count connections to migrate: %w", err)
	}
	statements := []string{
		// An interrupted run cannot leave this table behind — the whole
		// rebuild is one transaction — but a database touched by some older
		// or hand-run attempt can, and a stale copy would make the CREATE
		// below fail for a reason that has nothing to do with the user.
		"DROP TABLE IF EXISTS connections_ported",
		"CREATE TABLE connections_ported " + connectionColumns,
		`INSERT INTO connections_ported(id, flow_id, source_id, source_port, target_id, target_port)
			SELECT id, flow_id, source_id, 0, target_id,
				ROW_NUMBER() OVER (PARTITION BY flow_id, target_id ORDER BY id) - 1
			FROM connections`,
		"DROP TABLE connections",
		"ALTER TABLE connections_ported RENAME TO connections",
		connectionsFlowIndex,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate connections to ports: %w", err)
		}
	}

	// The row count is the gate rather than PRAGMA foreign_key_check: the
	// copy changes no key column, so the check could only report damage that
	// predates this migration, while a short copy is the one failure that
	// would cost a user wiring. Rolling back on a mismatch leaves the old
	// table exactly as it was.
	var after int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM connections").Scan(&after); err != nil {
		return fmt.Errorf("count migrated connections: %w", err)
	}
	if after != before {
		return fmt.Errorf("migrate connections to ports: %d of %d wires survived", after, before)
	}
	return nil
}

// ensureDeclaredInputPorts widens a variadic block's parameters to cover the
// ports its wires occupy. A Sum written before ports carried a single sign
// broadcast across however many wires reached it; now the signs are the port
// list, so that same Sum has to name each one. Repeating the sign is
// numerically identical to the broadcast it replaces, which is why this is a
// restatement rather than an edit.
//
// It runs on every Open, unguarded by a column check, so that it also repairs
// a database whose connections were renumbered by a run that stopped before
// getting here. Once every wired port is declared, the loop below finds
// nothing to write and re-opening leaves the parameters alone.
func ensureDeclaredInputPorts(ctx context.Context, db *sql.DB) error {
	return inDBTx(ctx, db, func(tx *sql.Tx) error {
		type wiredBlock struct {
			id         int64
			kind       BlockKind
			parameters string
			ports      int
		}
		// Only a block wired beyond port 0 can be under-declared: every kind
		// that accepts a wire at all has at least one port.
		rows, err := tx.QueryContext(ctx, `
			SELECT blocks.id, blocks.kind, blocks.parameters_json, MAX(connections.target_port) + 1
			FROM blocks
			JOIN connections ON connections.target_id = blocks.id
			GROUP BY blocks.id
			HAVING MAX(connections.target_port) > 0`)
		if err != nil {
			return fmt.Errorf("find wired input ports: %w", err)
		}
		var wired []wiredBlock
		for rows.Next() {
			var block wiredBlock
			if err := rows.Scan(&block.id, &block.kind, &block.parameters, &block.ports); err != nil {
				rows.Close()
				return fmt.Errorf("scan wired input ports: %w", err)
			}
			wired = append(wired, block)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("read wired input ports: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close wired input ports: %w", err)
		}

		for _, block := range wired {
			definition := blockDefinitions[block.kind]
			parameters, err := decodeParameters(block.kind, block.parameters)
			if err != nil {
				return fmt.Errorf("decode parameters for block %d: %w", block.id, err)
			}
			if len(definition.ports(parameters).inputs) >= block.ports ||
				definition.declareWiredPorts == nil {
				continue
			}
			widened, ok := definition.declareWiredPorts(parameters, block.ports)
			if !ok {
				continue
			}
			encoded, err := encodeParameters(widened)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE blocks SET parameters_json = ? WHERE id = ?", encoded, block.id,
			); err != nil {
				return fmt.Errorf("declare input ports for block %d: %w", block.id, err)
			}
		}
		return nil
	})
}

func ensureModelUpdatedAt(ctx context.Context, db *sql.DB) error {
	found, err := tableHasColumn(ctx, db, "flows", "model_updated_at")
	if err != nil {
		return err
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

func ensureProjects(ctx context.Context, db *sql.DB) error {
	return inDBTx(ctx, db, func(tx *sql.Tx) error {
		hasProjectID, err := tableHasColumn(ctx, tx, "flows", "project_id")
		if err != nil {
			return err
		}
		if !hasProjectID {
			if _, err := tx.ExecContext(ctx,
				"ALTER TABLE flows ADD COLUMN project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE",
			); err != nil {
				return fmt.Errorf("add flow project: %w", err)
			}
		}

		var unassigned int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM flows WHERE project_id IS NULL",
		).Scan(&unassigned); err != nil {
			return fmt.Errorf("count unassigned flows: %w", err)
		}
		if unassigned > 0 {
			projectID, err := emptyDefaultProject(ctx, tx)
			if err != nil {
				return err
			}
			if projectID == 0 {
				now := time.Now().UTC().Format(time.RFC3339Nano)
				result, err := tx.ExecContext(ctx,
					"INSERT INTO projects(name, created_at, updated_at) VALUES(?, ?, ?)",
					defaultProjectName, now, now,
				)
				if err != nil {
					return fmt.Errorf("create legacy project: %w", err)
				}
				projectID, err = result.LastInsertId()
				if err != nil {
					return fmt.Errorf("read legacy project id: %w", err)
				}
			}
			if _, err := tx.ExecContext(ctx,
				"UPDATE flows SET project_id = ? WHERE project_id IS NULL", projectID,
			); err != nil {
				return fmt.Errorf("assign legacy flows: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			"CREATE INDEX IF NOT EXISTS flows_project_id_idx ON flows(project_id)",
		); err != nil {
			return fmt.Errorf("index flows by project: %w", err)
		}
		return nil
	})
}

func emptyDefaultProject(ctx context.Context, tx *sql.Tx) (int64, error) {
	var projectID int64
	err := tx.QueryRowContext(ctx, `
		SELECT projects.id
		FROM projects
		LEFT JOIN flows ON flows.project_id = projects.id
		WHERE projects.name = ?
		GROUP BY projects.id
		HAVING COUNT(flows.id) = 0
		ORDER BY projects.id
		LIMIT 1`,
		defaultProjectName,
	).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find interrupted legacy project: %w", err)
	}
	return projectID, nil
}

// ensureFlowPositions gives flowsheets an order a user can change. Databases
// written before the column open with their tabs in the order the old menu
// showed them, numbered from zero within each project. It also repairs the
// duplicate default positions left by an interrupted older migration while
// preserving every valid user-defined order.
func ensureFlowPositions(ctx context.Context, db *sql.DB) error {
	return inDBTx(ctx, db, func(tx *sql.Tx) error {
		found, err := tableHasColumn(ctx, tx, "flows", "position")
		if err != nil {
			return err
		}
		if !found {
			if _, err := tx.ExecContext(ctx,
				"ALTER TABLE flows ADD COLUMN position INTEGER NOT NULL DEFAULT 0",
			); err != nil {
				return fmt.Errorf("add flow position: %w", err)
			}
		}

		invalid, err := hasInvalidFlowPositions(ctx, tx)
		if err != nil {
			return err
		}
		if found && !invalid {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE flows SET position = (
				SELECT ordered.position
				FROM (
					SELECT id, ROW_NUMBER() OVER (
						PARTITION BY project_id
						ORDER BY position, name COLLATE NOCASE, id
					) - 1 AS position
					FROM flows
				) AS ordered
				WHERE ordered.id = flows.id
			)`,
		); err != nil {
			return fmt.Errorf("order legacy flows: %w", err)
		}
		return nil
	})
}

func hasInvalidFlowPositions(ctx context.Context, tx *sql.Tx) (bool, error) {
	var invalid int
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM flows
			GROUP BY project_id
			HAVING MIN(position) <> 0
				OR MAX(position) <> COUNT(*) - 1
				OR COUNT(DISTINCT position) <> COUNT(*)
		)`,
	).Scan(&invalid); err != nil {
		return false, fmt.Errorf("validate flow positions: %w", err)
	}
	return invalid != 0, nil
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func tableHasColumn(ctx context.Context, db schemaQueryer, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read %s schema: %w", table, err)
	}
	return false, nil
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
		projectResult, err := tx.ExecContext(ctx,
			"INSERT INTO projects(name, created_at, updated_at) VALUES(?, ?, ?)",
			defaultProjectName, now, now,
		)
		if err != nil {
			return fmt.Errorf("seed project: %w", err)
		}
		projectID, err := projectResult.LastInsertId()
		if err != nil {
			return fmt.Errorf("seed project id: %w", err)
		}
		result, err := tx.ExecContext(ctx,
			"INSERT INTO flows(project_id, name, created_at, updated_at, model_updated_at) VALUES(?, ?, ?, ?, ?)",
			projectID, "Reactor temperature loop", now, now, now,
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
		// Laid out on the sheet lattice so the starter flowsheet opens on-grid.
		seeds := []seedBlock{
			{BlockSource, "Feed setpoint", 60, 80, Parameters{Amplitude: 1}},
			{BlockGain, "Valve gain", 300, 80, Parameters{Gain: 1.8}},
			{BlockLag, "Reactor", 540, 80, Parameters{TimeConstant: 2.2}},
			{BlockSource, "Disturbance", 60, 320, Parameters{Amplitude: 0.3}},
			{BlockLag, "Jacket lag", 300, 320, Parameters{TimeConstant: 4}},
			{BlockGain, "Heat loss", 540, 320, Parameters{Gain: -0.7}},
			// Two signs because two wires arrive: the Sum's signs are its
			// input ports, one per wire, in the order the edges below add
			// them.
			{BlockSum, "Energy balance", 780, 200, Parameters{Signs: "++"}},
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
				INSERT INTO blocks(flow_id, kind, name, x, y, parameters_json)
				VALUES(?, ?, ?, ?, ?, ?)`,
				flowID, seed.kind, seed.name, placed.X, placed.Y, encoded,
			)
			if err != nil {
				return fmt.Errorf("seed block %q: %w", seed.name, err)
			}
			ids[i], err = result.LastInsertId()
			if err != nil {
				return fmt.Errorf("seed block id: %w", err)
			}
		}

		// Source block, target block, target input port. Every source block
		// here drives its only output, so no edge needs to name a source
		// port; only the Sum has a second input to land on.
		edges := [][3]int{{0, 1, 0}, {1, 2, 0}, {2, 6, 0}, {3, 4, 0}, {4, 5, 0}, {5, 6, 1}, {6, 7, 0}}
		for _, edge := range edges {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO connections(flow_id, source_id, source_port, target_id, target_port)
				VALUES(?, ?, ?, ?, ?)`,
				flowID, ids[edge[0]], 0, ids[edge[1]], edge[2],
			); err != nil {
				return fmt.Errorf("seed connection: %w", err)
			}
		}
		return insertEvent(ctx, tx, flowID, now, "Example flowsheet created")
	})
}

func (s *Studio) inTx(ctx context.Context, action func(*sql.Tx) error) error {
	return inDBTx(ctx, s.db, action)
}

func inDBTx(ctx context.Context, db *sql.DB, action func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
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
		"SELECT id, project_id, name, created_at, updated_at, model_updated_at FROM flows WHERE id = ?", flowID,
	).Scan(
		&snapshot.Flow.ID, &snapshot.Flow.ProjectID, &snapshot.Flow.Name,
		&created, &updated, &modelUpdated,
	)
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
		SELECT id, flow_id, kind, name, x, y, parameters_json
		FROM blocks WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load blocks: %w", err)
	}
	for rows.Next() {
		var block Block
		var encoded string
		if err := rows.Scan(
			&block.ID, &block.FlowID, &block.Kind, &block.Name,
			&block.Position.X, &block.Position.Y, &encoded,
		); err != nil {
			rows.Close()
			return Snapshot{}, fmt.Errorf("scan block: %w", err)
		}
		block.Parameters, err = decodeParameters(block.Kind, encoded)
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
		SELECT id, flow_id, source_id, source_port, target_id, target_port
		FROM connections WHERE flow_id = ? ORDER BY id`, flowID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load connections: %w", err)
	}
	for rows.Next() {
		var connection Connection
		if err := rows.Scan(
			&connection.ID, &connection.FlowID,
			&connection.SourceID, &connection.SourcePort,
			&connection.TargetID, &connection.TargetPort,
		); err != nil {
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
	// The dock and the tab dot answer the same question from the same query:
	// a flowsheet needs a run exactly when no run survived the filter above.
	snapshot.Flow.NeedsRun = snapshot.LastRun == nil
	return snapshot, nil
}

func blockByID(ctx context.Context, tx *sql.Tx, id int64) (Block, error) {
	var block Block
	var encoded string
	err := tx.QueryRowContext(ctx, `
		SELECT id, flow_id, kind, name, x, y, parameters_json
		FROM blocks WHERE id = ?`, id,
	).Scan(
		&block.ID, &block.FlowID, &block.Kind, &block.Name,
		&block.Position.X, &block.Position.Y, &encoded,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Block{}, ErrNotFound
	}
	if err != nil {
		return Block{}, fmt.Errorf("load block: %w", err)
	}
	block.Parameters, err = decodeParameters(block.Kind, encoded)
	if err != nil {
		return Block{}, fmt.Errorf("decode parameters for %s: %w", block.Name, err)
	}
	return block, nil
}

const parameterSchemaVersion = 1

func encodeParameters(parameters Parameters) (string, error) {
	encoded, err := json.Marshal(struct {
		ParameterSchemaVersion int `json:"parameterSchemaVersion"`
		Parameters
	}{
		ParameterSchemaVersion: parameterSchemaVersion,
		Parameters:             parameters,
	})
	if err != nil {
		return "", fmt.Errorf("encode block parameters: %w", err)
	}
	return string(encoded), nil
}

func decodeParameters(kind BlockKind, encoded string) (Parameters, error) {
	if encoded == "" {
		parameters := defaultParameters(kind)
		if kind == BlockSource {
			parameters.StepTime = 0
		}
		return parameters, nil
	}
	var metadata struct {
		ParameterSchemaVersion int `json:"parameterSchemaVersion"`
	}
	if err := json.Unmarshal([]byte(encoded), &metadata); err != nil {
		return Parameters{}, err
	}
	legacy := metadata.ParameterSchemaVersion == 0
	if !legacy && metadata.ParameterSchemaVersion != parameterSchemaVersion {
		return Parameters{}, fmt.Errorf(
			"unsupported block parameter schema version %d",
			metadata.ParameterSchemaVersion,
		)
	}
	base := Parameters{}
	if legacy {
		switch kind {
		case BlockPID, BlockPID2:
			base = Parameters{}
		case BlockStateSpace:
			base = legacyStateSpaceParameters()
		case BlockDiscreteStateSpace:
			base = legacyDiscreteStateSpaceParameters()
		default:
			base = defaultParameters(kind)
		}
	}
	stored := struct {
		Parameters
		DelayMode         *string  `json:"delayMode"`
		StepTime          *float64 `json:"stepTime"`
		FilterCoefficient *float64 `json:"filterCoefficient"`
		FilterTime        *float64 `json:"filterTime"`
	}{
		Parameters: base,
	}
	if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
		return Parameters{}, err
	}
	parameters := stored.Parameters
	if stored.DelayMode != nil {
		parameters.DelayMode = *stored.DelayMode
	}
	if stored.StepTime != nil {
		parameters.StepTime = *stored.StepTime
	} else if kind == BlockSource && legacy {
		parameters.StepTime = 0
	}
	if kind == BlockPID || kind == BlockPID2 {
		switch {
		case stored.FilterCoefficient != nil:
			parameters.FilterCoefficient = *stored.FilterCoefficient
		case stored.FilterTime != nil && *stored.FilterTime > 0:
			parameters.FilterCoefficient = 1 / *stored.FilterTime
		default:
			parameters.FilterCoefficient = defaultPIDFilterCoefficient
		}
		parameters.FilterTime = 0
	}
	if kind == BlockDelay && legacy {
		if stored.DelayMode == nil {
			parameters.DelayMode = delayModePade
		}
	}
	return parameters, nil
}
