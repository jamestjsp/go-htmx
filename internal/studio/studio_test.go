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
	if snapshot.Flow.ProjectID == 0 {
		t.Fatal("seeded flow has no project")
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
		Name: "Separator vessel",
		Parameters: map[string]string{
			"time_constant": "6.5",
		},
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
	if block.Position != (Point{X: 340, Y: 220}) {
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
		Name: lag.Name,
		Parameters: map[string]string{
			"time_constant": "0",
		},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
}

func TestConnectRejectsDuplicateAndInvalidPortsButAllowsCycle(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	existing := snapshot.Connections[0]
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, Wire{SourceID: existing.SourceID, TargetID: existing.TargetID}); err == nil {
		t.Fatal("duplicate connection succeeded")
	}

	scope := findKind(t, snapshot.Blocks, BlockScope)
	gain := findKind(t, snapshot.Blocks, BlockGain)
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, Wire{SourceID: scope.ID, TargetID: gain.ID}); err == nil {
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
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, Wire{SourceID: sums[0], TargetID: sums[1]}); err != nil {
		t.Fatal(err)
	}
	if _, err := studio.Connect(ctx, snapshot.Flow.ID, Wire{SourceID: sums[1], TargetID: sums[2]}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = studio.Connect(ctx, snapshot.Flow.ID, Wire{SourceID: sums[2], TargetID: sums[0]})
	if err != nil {
		t.Fatal(err)
	}
	var feedback bool
	for _, connection := range snapshot.Connections {
		feedback = feedback || connection.SourceID == sums[2] && connection.TargetID == sums[0]
	}
	if !feedback {
		t.Fatal("feedback connection was not persisted")
	}
}

func TestConnectAllowsVariadicSumButRejectsSecondWireOnArityOneBlock(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := snapshot.Flow.ID

	_, sourceA, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 700, Y: 700})
	if err != nil {
		t.Fatal(err)
	}
	_, sourceB, err := studio.AddBlock(ctx, flowID, BlockConstant, Point{X: 700, Y: 800})
	if err != nil {
		t.Fatal(err)
	}
	_, sumID, err := studio.AddBlock(ctx, flowID, BlockSum, Point{X: 900, Y: 700})
	if err != nil {
		t.Fatal(err)
	}
	_, gainID, err := studio.AddBlock(ctx, flowID, BlockGain, Point{X: 900, Y: 800})
	if err != nil {
		t.Fatal(err)
	}

	// A Sum's signs are its input ports, so a second terminal to wire is what
	// a second sign creates. The default one sign leaves it with port 0 only.
	snapshot, err = studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sum := findBlock(t, snapshot.Blocks, sumID)
	if _, err := studio.UpdateBlock(ctx, sumID, BlockUpdate{
		Name:       sum.Name,
		Parameters: map[string]string{"signs": "++"},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := studio.Connect(ctx, flowID, Wire{SourceID: sourceA, TargetID: sumID}); err != nil {
		t.Fatalf("first wire into Sum: %v", err)
	}
	if _, err := studio.Connect(ctx, flowID, Wire{
		SourceID: sourceB, TargetID: sumID, TargetPort: 1,
	}); err != nil {
		t.Fatalf("second wire into variadic Sum: %v", err)
	}

	if _, err := studio.Connect(ctx, flowID, Wire{SourceID: sourceA, TargetID: gainID}); err != nil {
		t.Fatalf("first wire into Gain: %v", err)
	}
	snapshot, err = studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gain := findBlock(t, snapshot.Blocks, gainID)

	_, err = studio.Connect(ctx, flowID, Wire{SourceID: sourceB, TargetID: gainID})
	if err == nil {
		t.Fatal("second wire into an arityOne block succeeded")
	}
	if want := gain.Name + " already has an input"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
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

func TestAddBlockChoosesOpenPosition(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	occupied := snapshot.Blocks[0].Position

	snapshot, blockID, err := studio.AddBlock(ctx, snapshot.Flow.ID, BlockGain, occupied)
	if err != nil {
		t.Fatal(err)
	}
	added := findBlock(t, snapshot.Blocks, blockID)
	if added.Position == occupied {
		t.Fatalf("new block was placed on occupied position %#v", occupied)
	}
}

func TestClampPositionSnapsAndBounds(t *testing.T) {
	cases := []struct {
		name  string
		point Point
		want  Point
	}{
		{"rounds down", Point{X: 333, Y: 222}, Point{X: 340, Y: 220}},
		{"rounds half up", Point{X: 410, Y: 190}, Point{X: 420, Y: 200}},
		{"already on grid", Point{X: 240, Y: 120}, Point{X: 240, Y: 120}},
		{"negative clamps to origin", Point{X: -500, Y: -1}, Point{X: 0, Y: 0}},
		{"beyond the sheet keeps the block inside", Point{X: 99999, Y: 99999},
			Point{X: 5820, Y: 3900}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := clampPosition(testCase.point)
			if got != testCase.want {
				t.Fatalf("clampPosition(%#v) = %#v, want %#v", testCase.point, got, testCase.want)
			}
			if got.X%GridPitch != 0 || got.Y%GridPitch != 0 {
				t.Fatalf("position %#v is off the %dpx grid", got, GridPitch)
			}
			if got.X+BlockWidth > SheetWidth || got.Y+BlockHeight > SheetHeight {
				t.Fatalf("position %#v puts the block outside the sheet", got)
			}
		})
	}
}

func TestMoveBlockSnapsToGridAcrossTheWidenedSheet(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockID := snapshot.Blocks[0].ID

	// A coordinate far outside the old 1040x500 world must now be accepted.
	if err := studio.MoveBlock(ctx, blockID, Point{X: 3007, Y: 2503}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if moved := findBlock(t, snapshot.Blocks, blockID); moved.Position != (Point{X: 3000, Y: 2500}) {
		t.Fatalf("position = %#v, want {3000 2500}", moved.Position)
	}
}

func TestAddBlockFillsTheLatticeWithoutOverlapping(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	snapshot, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	desired := snapshot.Blocks[0].Position

	// Every request names the same occupied point, so each block must fall
	// through to the next free lattice cell.
	for range 8 {
		snapshot, _, err = studio.AddBlock(ctx, snapshot.Flow.ID, BlockGain, desired)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, block := range snapshot.Blocks {
		if block.Position.X%GridPitch != 0 || block.Position.Y%GridPitch != 0 {
			t.Fatalf("block %d at %#v is off the grid", block.ID, block.Position)
		}
		for _, other := range snapshot.Blocks[i+1:] {
			if abs(block.Position.X-other.Position.X) < BlockWidth &&
				abs(block.Position.Y-other.Position.Y) < BlockHeight {
				t.Fatalf("blocks %d and %d overlap at %#v and %#v",
					block.ID, other.ID, block.Position, other.Position)
			}
		}
	}
}

func TestDuplicateBlocksPlacesAtTheOffsetBeforeUsingTheLattice(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	initial, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial, sourceID, err := studio.AddBlock(ctx, initial.Flow.ID, BlockGain, Point{X: 4000, Y: 3000})
	if err != nil {
		t.Fatal(err)
	}
	source := findBlock(t, initial.Blocks, sourceID)

	duplicated, err := studio.DuplicateBlocks(ctx, initial.Flow.ID, []int64{sourceID})
	if err != nil {
		t.Fatal(err)
	}
	copy := findBlockByName(t, duplicated.Blocks, source.Name+" copy")
	want := clampPosition(Point{X: source.Position.X + GridPitch, Y: source.Position.Y + GridPitch})
	if copy.Position != want {
		t.Fatalf("duplicate position = %#v, want source offset %#v", copy.Position, want)
	}
}

func TestDuplicateBlocksFallbackNeverOverlapsTheSource(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	initial, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	initial, sourceID, err := studio.AddBlock(ctx, initial.Flow.ID, BlockGain, Point{X: 4000, Y: 3000})
	if err != nil {
		t.Fatal(err)
	}
	source := findBlock(t, initial.Blocks, sourceID)
	initial, blockerID, err := studio.AddBlock(ctx, initial.Flow.ID, BlockGain, Point{X: 1000, Y: 3000})
	if err != nil {
		t.Fatal(err)
	}
	desired := clampPosition(Point{X: source.Position.X + GridPitch, Y: source.Position.Y + GridPitch})
	if err := studio.MoveBlock(ctx, blockerID, desired); err != nil {
		t.Fatal(err)
	}
	duplicated, err := studio.DuplicateBlocks(ctx, initial.Flow.ID, []int64{sourceID})
	if err != nil {
		t.Fatal(err)
	}
	copy := findBlockByName(t, duplicated.Blocks, source.Name+" copy")
	if copy.Position == desired {
		t.Fatalf("duplicate used occupied desired position %#v", desired)
	}
	assertBlockDoesNotOverlap(t, copy, duplicated.Blocks)
}

func TestDuplicateBlocksFullSelectionDoesNotOverlap(t *testing.T) {
	ctx := context.Background()
	studio := openTestStudio(t, ":memory:")
	initial, err := studio.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockIDs := make([]int64, len(initial.Blocks))
	for index, block := range initial.Blocks {
		blockIDs[index] = block.ID
	}
	duplicated, err := studio.DuplicateBlocks(ctx, initial.Flow.ID, blockIDs)
	if err != nil {
		t.Fatal(err)
	}
	for index, block := range duplicated.Blocks {
		for _, other := range duplicated.Blocks[index+1:] {
			if abs(block.Position.X-other.Position.X) < BlockWidth && abs(block.Position.Y-other.Position.Y) < BlockHeight {
				t.Fatalf("blocks %d and %d overlap at %#v and %#v", block.ID, other.ID, block.Position, other.Position)
			}
		}
	}
}

func assertBlockDoesNotOverlap(t *testing.T, block Block, blocks []Block) {
	t.Helper()
	for _, other := range blocks {
		if block.ID == other.ID {
			continue
		}
		if abs(block.Position.X-other.Position.X) < BlockWidth && abs(block.Position.Y-other.Position.Y) < BlockHeight {
			t.Fatalf("blocks %d and %d overlap at %#v and %#v", block.ID, other.ID, block.Position, other.Position)
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

func findBlockByName(t *testing.T, blocks []Block, name string) Block {
	t.Helper()
	for _, block := range blocks {
		if block.Name == name {
			return block
		}
	}
	t.Fatalf("block %q not found", name)
	return Block{}
}
