package studio

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestBlockLibraryDefinitionsOwnDefaultsAndEditors(t *testing.T) {
	library := BlockLibrary()
	if len(library) != len(blockOrder) {
		t.Fatalf("library size = %d, want %d", len(library), len(blockOrder))
	}
	for _, definition := range library {
		if !definition.Kind.Valid() {
			t.Fatalf("%q is not valid", definition.Kind)
		}
		block := Block{Kind: definition.Kind, Parameters: defaultParameters(definition.Kind)}
		for _, field := range block.EditorFields() {
			if field.Name == "" || field.Label == "" || field.Value == "" {
				t.Fatalf("%s has incomplete editor field %#v", definition.Kind, field)
			}
		}
		if block.Summary() == "" {
			t.Fatalf("%s has no summary", definition.Kind)
		}
	}
}

func TestTransferFunctionUpdateParsesAndValidatesCoefficients(t *testing.T) {
	block := Block{
		Kind:       BlockTransfer,
		Name:       "Plant",
		Parameters: defaultParameters(BlockTransfer),
	}
	updated, err := validateBlockUpdate(block, BlockUpdate{
		Name: "Second-order plant",
		Parameters: map[string]string{
			"numerator":   "2, 1",
			"denominator": "1 3 2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := coefficientsText(updated.Parameters.Numerator); got != "2, 1" {
		t.Fatalf("numerator = %q", got)
	}

	_, err = validateBlockUpdate(block, BlockUpdate{
		Name: "Improper",
		Parameters: map[string]string{
			"numerator":   "1, 2, 3",
			"denominator": "1, 2",
		},
	})
	if err == nil {
		t.Fatal("improper transfer function succeeded")
	}
}

func TestOpenMigratesLegacyBlockParameters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE flows (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE blocks (
			id INTEGER PRIMARY KEY,
			flow_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			x INTEGER NOT NULL,
			y INTEGER NOT NULL,
			amplitude REAL NOT NULL DEFAULT 0,
			gain REAL NOT NULL DEFAULT 0,
			time_constant REAL NOT NULL DEFAULT 0
		);
		INSERT INTO flows(id, name, created_at, updated_at)
		VALUES(1, 'Legacy', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO blocks(flow_id, kind, name, x, y, amplitude, gain, time_constant)
		VALUES(1, 'lag', 'Legacy lag', 20, 20, 0, 0, 4.5);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	service, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Blocks[0].Parameters.TimeConstant; got != 4.5 {
		t.Fatalf("time constant = %g, want 4.5", got)
	}
}
