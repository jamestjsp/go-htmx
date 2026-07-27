package studio

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenSeedsUsableExample(t *testing.T) {
	studio := openTestStudio(t, filepath.Join(t.TempDir(), "seed.db"))

	snapshot, err := studio.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Flow.Name != "Reactor temperature loop" {
		t.Fatalf("flow name = %q", snapshot.Flow.Name)
	}
	if len(snapshot.Blocks) != 8 {
		t.Fatalf("block count = %d, want 8", len(snapshot.Blocks))
	}
	if len(snapshot.Connections) != 7 {
		t.Fatalf("connection count = %d, want 7", len(snapshot.Connections))
	}
	if len(snapshot.Events) == 0 || snapshot.Events[0].Message != "Example flowsheet created" {
		t.Fatalf("unexpected events: %#v", snapshot.Events)
	}
}

func TestBlockChangesRoundTripThroughSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "roundtrip.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := first.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, blockID, err := first.AddBlock(ctx, snapshot.Flow.ID, BlockLag, Point{X: -20, Y: 900})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.MoveBlock(ctx, blockID, Point{X: 333, Y: 222}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpdateBlock(ctx, blockID, BlockUpdate{
		Name: "Separator vessel", Parameter: 6.5,
	}); err != nil {
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
	snapshot, err = second.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	block := findBlock(t, snapshot.Blocks, blockID)
	if block.Name != "Separator vessel" {
		t.Fatalf("name = %q", block.Name)
	}
	if block.Position != (Point{X: 333, Y: 222}) {
		t.Fatalf("position = %#v", block.Position)
	}
	if block.Parameters.TimeConstant != 6.5 {
		t.Fatalf("time constant = %g", block.Parameters.TimeConstant)
	}
}

func TestUpdateRejectsInvalidParameters(t *testing.T) {
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lag := findKind(t, snapshot.Blocks, BlockLag)

	_, err = studio.UpdateBlock(context.Background(), lag.ID, BlockUpdate{
		Name: lag.Name, Parameter: 0,
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
}

func TestConnectRejectsDuplicateInvalidPortsAndCycle(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	existing := snapshot.Connections[0]
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, existing.SourceID, existing.TargetID); err == nil {
		t.Fatal("duplicate connection succeeded")
	}

	scope := findKind(t, snapshot.Blocks, BlockScope)
	gain := findKind(t, snapshot.Blocks, BlockGain)
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, scope.ID, gain.ID); err == nil {
		t.Fatal("scope output connection succeeded")
	}

	var sums []int64
	for range 3 {
		_, id, err := studio.AddBlock(ctx, snapshot.Flow.ID, BlockSum, Point{X: 100, Y: 100})
		if err != nil {
			t.Fatal(err)
		}
		sums = append(sums, id)
	}
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, sums[0], sums[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, sums[1], sums[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, sums[2], sums[0]); err == nil {
		t.Fatal("cyclic connection succeeded")
	}
}

func TestDeleteBlockCascadesConnections(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gain := findKind(t, snapshot.Blocks, BlockGain)

	snapshot, err = studio.DeleteBlock(ctx, gain.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, connection := range snapshot.Connections {
		if connection.SourceID == gain.ID || connection.TargetID == gain.ID {
			t.Fatalf("connection %#v still references deleted block", connection)
		}
	}
}

func openTestStudio(t *testing.T, path string) *Studio {
	t.Helper()
	studio, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = studio.Close() })
	return studio
}

func findKind(t *testing.T, blocks []Block, kind BlockKind) Block {
	t.Helper()
	for _, block := range blocks {
		if block.Kind == kind {
			return block
		}
	}
	t.Fatalf("no block of kind %q", kind)
	return Block{}
}

func findBlock(t *testing.T, blocks []Block, id int64) Block {
	t.Helper()
	for _, block := range blocks {
		if block.ID == id {
			return block
		}
	}
	t.Fatalf("no block with id %d", id)
	return Block{}
}
