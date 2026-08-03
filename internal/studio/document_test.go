package studio

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFlowDocumentRoundTripIsANoop(t *testing.T) {
	service := openTestStudio(t, filepath.Join(t.TempDir(), "document-roundtrip.db"))
	ctx := context.Background()
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	document, err := service.DumpFlow(ctx, current.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := current.Snapshot.Flow.ModelUpdatedAt
	result, applied, err := service.ApplyFlow(ctx, current.Snapshot.Flow.ID, document, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.WiresAdded != 0 || result.WiresRemoved != 0 {
		t.Fatalf("round trip result = %#v, want no changes", result)
	}
	if !reflect.DeepEqual(applied, current.Snapshot) {
		t.Fatal("round trip changed the snapshot")
	}
	if !applied.Flow.ModelUpdatedAt.Equal(before) {
		t.Fatalf("model_updated_at = %s, want %s", applied.Flow.ModelUpdatedAt, before)
	}
}

func TestApplyFlowRejectsDuplicateDocumentNames(t *testing.T) {
	service := openTestStudio(t, ":memory:")
	ctx := context.Background()
	snapshot, err := service.Current(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.ApplyFlow(ctx, snapshot.Flow.ID, FlowDocument{
		Version: 1,
		Blocks: []DocumentBlock{
			{Name: "duplicate", Kind: BlockConstant, Position: DocumentPosition{X: 100, Y: 100}, Parameters: map[string]string{"value": "1"}},
			{Name: "duplicate", Kind: BlockGain, Position: DocumentPosition{X: 400, Y: 100}, Parameters: map[string]string{"gain": "1"}},
		},
	}, false)
	if err == nil || !strings.Contains(err.Error(), `flowsheet document contains duplicate block name "duplicate"`) {
		t.Fatalf("duplicate document error = %v", err)
	}
}

func TestApplyFlowReconcilesGraphDryRunsAndRejectsAtomically(t *testing.T) {
	service := openTestStudio(t, filepath.Join(t.TempDir(), "document-apply.db"))
	ctx := context.Background()
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := service.CreateFlow(ctx, current.Project.ID, "Declarative")
	if err != nil {
		t.Fatal(err)
	}
	flowID := empty.Snapshot.Flow.ID
	document := FlowDocument{
		Version: 1,
		Blocks: []DocumentBlock{
			{ID: 999, Kind: BlockConstant, Name: "Feed", Position: DocumentPosition{X: 100, Y: 100}, Parameters: map[string]string{"value": "2"}},
			{Kind: BlockGain, Name: "Valve", Position: DocumentPosition{X: 400, Y: 100}, Parameters: map[string]string{"gain": "3"}},
		},
		Wires: []DocumentWire{{ID: 1000, Source: "Feed", Target: "Valve"}},
	}
	result, applied, err := service.ApplyFlow(ctx, flowID, document, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 2 || result.WiresAdded != 1 || !result.Changed {
		t.Fatalf("initial apply result = %#v", result)
	}
	if len(applied.Blocks) != 2 || len(applied.Connections) != 1 {
		t.Fatalf("applied graph has %d blocks and %d wires", len(applied.Blocks), len(applied.Connections))
	}

	dryRun := document
	dryRun.Blocks = append(dryRun.Blocks, DocumentBlock{
		Kind: BlockConstant, Name: "Dry run only", Position: DocumentPosition{X: 800, Y: 100}, Parameters: map[string]string{"value": "4"},
	})
	dryResult, drySnapshot, err := service.ApplyFlow(ctx, flowID, dryRun, true)
	if err != nil {
		t.Fatal(err)
	}
	if !dryResult.DryRun || !dryResult.Changed || len(dryResult.Added) != 1 {
		t.Fatalf("dry-run result = %#v", dryResult)
	}
	if len(drySnapshot.Blocks) != 2 {
		t.Fatalf("dry-run mutated block count to %d", len(drySnapshot.Blocks))
	}

	partial := document
	partial.Blocks = partial.Blocks[:1]
	partial.Wires = nil
	removed, _, err := service.ApplyFlow(ctx, flowID, partial, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Removed) != 1 || removed.WiresRemoved != 1 {
		t.Fatalf("partial apply result = %#v", removed)
	}

	beforeInvalid, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	invalidDocument, err := service.DumpFlow(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	invalidDocument.Blocks[0].Parameters["value"] = "not-a-number"
	_, _, err = service.ApplyFlow(ctx, flowID, invalidDocument, false)
	if err == nil || !strings.Contains(err.Error(), "must be a number") {
		t.Fatalf("invalid apply error = %v", err)
	}
	afterInvalid, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeInvalid, afterInvalid) {
		t.Fatal("invalid document changed the flowsheet")
	}
}
