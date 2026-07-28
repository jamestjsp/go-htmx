package studio

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// legacySchema is the connections table exactly as versions before ports
// wrote it — the old UNIQUE(flow_id, source_id, target_id) and both foreign
// keys — with every other table already at today's shape, so the fixtures
// below exercise the port migration on its own rather than the whole ladder.
const legacySchema = `
CREATE TABLE projects (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE flows (
	id INTEGER PRIMARY KEY,
	project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	model_updated_at TEXT NOT NULL,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE blocks (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	x INTEGER NOT NULL,
	y INTEGER NOT NULL,
	parameters_json TEXT NOT NULL DEFAULT ''
);
CREATE TABLE connections (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	source_id INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
	target_id INTEGER NOT NULL REFERENCES blocks(id) ON DELETE CASCADE,
	UNIQUE(flow_id, source_id, target_id)
);
CREATE TABLE events (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	message TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE simulation_runs (
	id INTEGER PRIMARY KEY,
	flow_id INTEGER NOT NULL REFERENCES flows(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	duration REAL NOT NULL,
	sample_time REAL NOT NULL,
	result_json TEXT NOT NULL
);
CREATE INDEX connections_flow_id_idx ON connections(flow_id);
INSERT INTO projects(id, name, created_at, updated_at)
	VALUES(1, 'Legacy', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
INSERT INTO flows(id, project_id, name, created_at, updated_at, model_updated_at, position)
	VALUES(1, 1, 'Legacy loop', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0);
INSERT INTO blocks(id, flow_id, kind, name, x, y, parameters_json) VALUES
	(1, 1, 'constant', 'Feed A', 60, 80, '{"value":1}'),
	(2, 1, 'constant', 'Feed B', 60, 200, '{"value":2}'),
	(3, 1, 'constant', 'Feed C', 60, 320, '{"value":3}'),
	(4, 1, 'sum', 'Signed', 300, 80, '{"signs":"+-"}'),
	-- Empty parameters, exactly as the old seed wrote its Sum: decoding fills
	-- the single default sign, which is the broadcast every shipped example
	-- flowsheet is carrying.
	(5, 1, 'sum', 'Broadcast', 300, 320, '{}'),
	(6, 1, 'gain', 'Valve', 540, 200, '{"gain":2}'),
	(7, 1, 'scope', 'Trend', 780, 80, '{}');
-- Deliberately not in id order, and the two wires into Signed arrive from
-- blocks whose own ids run the other way, so numbering by connection id can
-- be told apart from numbering by insertion or by source id.
INSERT INTO connections(id, flow_id, source_id, target_id) VALUES
	(12, 1, 1, 4),
	(10, 1, 3, 4),
	(15, 1, 4, 7),
	(11, 1, 2, 6),
	(14, 1, 2, 5),
	(13, 1, 1, 5),
	(16, 1, 6, 999);
`

// openLegacyPortsDatabase writes the fixture above and returns its path. The
// database is opened without the foreign-key pragma, which is how row 16 —
// a wire whose target block no longer exists — gets in. Databases written
// before foreign keys were enforced can hold such rows, which is why
// compileFlow has a message for them.
func openLegacyPortsDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-ports.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), legacySchema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type portedWire struct {
	id                     int64
	source, target         int64
	sourcePort, targetPort int
}

func portedWires(t *testing.T, service *Studio) []portedWire {
	t.Helper()
	rows, err := service.db.QueryContext(context.Background(), `
		SELECT id, source_id, source_port, target_id, target_port
		FROM connections ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var wires []portedWire
	for rows.Next() {
		var wire portedWire
		if err := rows.Scan(&wire.id, &wire.source, &wire.sourcePort, &wire.target, &wire.targetPort); err != nil {
			t.Fatal(err)
		}
		wires = append(wires, wire)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return wires
}

// Ports are numbered per target in connection-id order because that is the
// order compileFlow hands a Sum's signs to its inbound wires. Numbering them
// any other way would quietly change which sign each stored wire carries.
func TestOpenNumbersLegacyConnectionsOntoPorts(t *testing.T) {
	ctx := context.Background()
	path := openLegacyPortsDatabase(t)

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	want := []portedWire{
		{id: 10, source: 3, target: 4, targetPort: 0},
		{id: 11, source: 2, target: 6, targetPort: 0},
		{id: 12, source: 1, target: 4, targetPort: 1},
		{id: 13, source: 1, target: 5, targetPort: 0},
		{id: 14, source: 2, target: 5, targetPort: 1},
		{id: 15, source: 4, target: 7, targetPort: 0},
		// The orphaned wire is copied across untouched: the rebuild runs
		// with foreign keys suspended precisely so a database that already
		// opens does not become one that cannot.
		{id: 16, source: 6, target: 999, targetPort: 0},
	}
	got := portedWires(t, service)
	if len(got) != len(want) {
		t.Fatalf("connections after migration = %d, want %d", len(got), len(want))
	}
	for i, wire := range got {
		if wire != want[i] {
			t.Fatalf("connection %d = %#v, want %#v", wire.id, wire, want[i])
		}
	}
}

// A Sum written before ports carried one sign broadcast across however many
// wires reached it. The signs are the port list now, so that Sum has to name
// each port — repeating the sign is what the broadcast already meant, so the
// flowsheet keeps computing exactly what it did. A Sum that already named
// every port is left alone.
func TestOpenDeclaresInputPortsForBroadcastSums(t *testing.T) {
	ctx := context.Background()
	path := openLegacyPortsDatabase(t)

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}

	broadcast := blockNamed(t, snapshot.Blocks, "Broadcast")
	if got, want := broadcast.Parameters.Signs, "++"; got != want {
		t.Fatalf("broadcast sum signs = %q, want %q", got, want)
	}
	if got := broadcast.InputPortCount(); got != 2 {
		t.Fatalf("broadcast sum input ports = %d, want 2", got)
	}
	signed := blockNamed(t, snapshot.Blocks, "Signed")
	if got, want := signed.Parameters.Signs, "+-"; got != want {
		t.Fatalf("signed sum signs = %q, want %q", got, want)
	}
}

// Re-opening must not renumber a wire or rewrite a parameter. The port
// columns are the migration's own guard, and the port declaration below them
// runs unguarded on every Open — so this is what proves the second run finds
// nothing left to do rather than doing it again.
func TestReopeningLeavesMigratedPortsAlone(t *testing.T) {
	ctx := context.Background()
	path := openLegacyPortsDatabase(t)

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	migrated := portedWires(t, first)
	// A wire the user redraws after the migration must survive the next Open
	// as they left it, not be renumbered back onto the port the backfill
	// would have chosen.
	if _, err := first.db.ExecContext(ctx,
		"UPDATE connections SET target_port = 1 WHERE id = ?", 11,
	); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	reopened := portedWires(t, second)
	if len(reopened) != len(migrated) {
		t.Fatalf("connections after reopen = %d, want %d", len(reopened), len(migrated))
	}
	for i, wire := range reopened {
		want := migrated[i]
		if wire.id == 11 {
			want.targetPort = 1
		}
		if wire != want {
			t.Fatalf("connection %d after reopen = %#v, want %#v", wire.id, wire, want)
		}
	}

	snapshot, err := second.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := blockNamed(t, snapshot.Blocks, "Broadcast").Parameters.Signs, "++"; got != want {
		t.Fatalf("broadcast sum signs after reopen = %q, want %q", got, want)
	}
}

// Rebuilding a table by create-copy-drop-rename is where a cascade can fire
// on rows nobody asked to delete, and where an enforcement pragma left off
// would stay off. This checks the far side of the rebuild: the wires are all
// still there, foreign keys are enforced again, and deleting a block still
// takes its wires with it.
func TestConnectionsSurviveTheRebuildAndStillCascade(t *testing.T) {
	ctx := context.Background()
	path := openLegacyPortsDatabase(t)

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	if got := len(portedWires(t, service)); got != 7 {
		t.Fatalf("connections after rebuild = %d, want 7", got)
	}
	var enforced int
	if err := service.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enforced); err != nil {
		t.Fatal(err)
	}
	if enforced != 1 {
		t.Fatalf("foreign_keys after rebuild = %d, want 1", enforced)
	}
	// The rebuilt table must still refuse a wire to a block that is not
	// there, which is the constraint the copy ran without.
	if _, err := service.db.ExecContext(ctx, `
		INSERT INTO connections(flow_id, source_id, source_port, target_id, target_port)
		VALUES(1, 1, 0, 998, 0)`,
	); err == nil {
		t.Fatal("the rebuilt table accepted a wire to a missing block")
	}

	// Deleting the Signed sum must take both wires into it and the one out of
	// it with it, through the cascade the rebuilt table re-declares.
	if _, err := service.DeleteBlock(ctx, 4); err != nil {
		t.Fatal(err)
	}
	for _, wire := range portedWires(t, service) {
		if wire.source == 4 || wire.target == 4 {
			t.Fatalf("connection %d survived the cascade: %#v", wire.id, wire)
		}
	}
	if got := len(portedWires(t, service)); got != 4 {
		t.Fatalf("connections after cascade = %d, want 4", got)
	}
}

// The old UNIQUE(flow_id, source_id, target_id) made a second wire between
// the same two blocks impossible, whatever port it landed on. Fanning one
// output into two ports of a Sum is the case that constraint got wrong.
func TestConnectWiresOneOutputIntoTwoPortsOfTheSameBlock(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := snapshot.Flow.ID

	_, feedID, err := service.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	sumID := twoPortSum(t, service, flowID)

	if _, err := service.Connect(ctx, flowID, Wire{SourceID: feedID, TargetID: sumID}); err != nil {
		t.Fatalf("wire onto port 0: %v", err)
	}
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: feedID, TargetID: sumID, TargetPort: 1,
	}); err != nil {
		t.Fatalf("wire onto port 1: %v", err)
	}
}

func TestConnectRefusesASecondWireOntoOneInputPort(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := snapshot.Flow.ID

	_, feedA, err := service.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	_, feedB, err := service.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 1100})
	if err != nil {
		t.Fatal(err)
	}
	sumID := twoPortSum(t, service, flowID)

	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: feedA, TargetID: sumID, TargetPort: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Connect(ctx, flowID, Wire{
		SourceID: feedB, TargetID: sumID, TargetPort: 1,
	})
	snapshot, current := service.Current(ctx)
	if current != nil {
		t.Fatal(current)
	}
	want := findBlock(t, snapshot.Blocks, sumID).Name + " already has an input on port 1"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestConnectRefusesAPortTheBlockDoesNotHave(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := snapshot.Flow.ID

	_, feedID, err := service.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	_, gainID, err := service.AddBlock(ctx, flowID, BlockGain, Point{X: 400, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	feed := findBlock(t, snapshot.Blocks, feedID)
	gain := findBlock(t, snapshot.Blocks, gainID)

	_, err = service.Connect(ctx, flowID, Wire{SourceID: feedID, TargetID: gainID, TargetPort: 1})
	if want := gain.Name + " has no input port 1"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	_, err = service.Connect(ctx, flowID, Wire{SourceID: feedID, SourcePort: 3, TargetID: gainID})
	if want := feed.Name + " has no output port 3"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	// A negative index names no port either, so a hand-made request cannot
	// slip one past the check by going the other way.
	_, err = service.Connect(ctx, flowID, Wire{SourceID: feedID, TargetID: gainID, TargetPort: -1})
	if want := gain.Name + " has no input port -1"; err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

// Shrinking a Sum's signs takes away input ports. Dropping the wires sitting
// on them to make the edit fit would throw away signals the user drew, so the
// edit is refused and the port that blocks it is named.
func TestUpdateBlockRefusesToOrphanAWiredInputPort(t *testing.T) {
	ctx := context.Background()
	service := openTestStudio(t, ":memory:")
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := snapshot.Flow.ID

	_, feedID, err := service.AddBlock(ctx, flowID, BlockConstant, Point{X: 100, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	sumID := twoPortSum(t, service, flowID)
	if _, err := service.Connect(ctx, flowID, Wire{
		SourceID: feedID, TargetID: sumID, TargetPort: 1,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum := findBlock(t, snapshot.Blocks, sumID)

	_, err = service.UpdateBlock(ctx, sumID, BlockUpdate{
		Name:       sum.Name,
		Parameters: map[string]string{"signs": "+"},
	})
	want := sum.Name + " has a wire on input port 1; disconnect it first"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	// The refusal is about orphaning a port, not about touching the field:
	// renaming, changing the remaining sign, and widening all still work.
	if _, err := service.UpdateBlock(ctx, sumID, BlockUpdate{
		Name:       "Energy balance",
		Parameters: map[string]string{"signs": "+--"},
	}); err != nil {
		t.Fatalf("widening a wired sum: %v", err)
	}
	snapshot, err = service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := findBlock(t, snapshot.Blocks, sumID).InputPortCount(); got != 3 {
		t.Fatalf("input ports after widening = %d, want 3", got)
	}
}

// Ports come from the definition and the block's parameters, in one place.
// This pins that derivation for every registered kind, and catches a variadic
// kind added without saying how its port count is derived — which would
// otherwise only show up as a nil call at runtime.
func TestEveryVariadicKindDerivesItsInputPortsFromParameters(t *testing.T) {
	for _, kind := range blockOrder {
		definition := blockDefinitions[kind]
		block := Block{Kind: kind, Name: kind.Label(), Parameters: defaultParameters(kind)}
		if definition.arity() == arityVariadic && definition.inputPorts == nil {
			t.Fatalf("%s is variadic but does not derive its input ports", kind)
		}
		if got, want := block.OutputPortCount(), boolToPorts(kind.HasOutput()); got != want {
			t.Fatalf("%s output ports = %d, want %d", kind, got, want)
		}
		switch definition.arity() {
		case arityNone:
			if got := block.InputPortCount(); got != 0 {
				t.Fatalf("%s input ports = %d, want 0", kind, got)
			}
		case arityVariadic:
			// Sum's default is one sign, so a fresh one starts with the same
			// single port every other block has and grows as it is edited.
			if got := block.InputPortCount(); got != 1 {
				t.Fatalf("%s input ports at defaults = %d, want 1", kind, got)
			}
			block.Parameters.Signs = "+-+"
			if got := block.InputPortCount(); got != 3 {
				t.Fatalf("%s input ports for three signs = %d, want 3", kind, got)
			}
		default:
			if got := block.InputPortCount(); got != 1 {
				t.Fatalf("%s input ports = %d, want 1", kind, got)
			}
		}
	}
}

func boolToPorts(present bool) int {
	if present {
		return 1
	}
	return 0
}

// twoPortSum adds a Sum and widens it to two input ports, which is the only
// way a block gets a second terminal to wire.
func twoPortSum(t *testing.T, service *Studio, flowID int64) int64 {
	t.Helper()
	ctx := context.Background()
	snapshot, sumID, err := service.AddBlock(ctx, flowID, BlockSum, Point{X: 700, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBlock(ctx, sumID, BlockUpdate{
		Name:       findBlock(t, snapshot.Blocks, sumID).Name,
		Parameters: map[string]string{"signs": "++"},
	}); err != nil {
		t.Fatal(err)
	}
	return sumID
}
