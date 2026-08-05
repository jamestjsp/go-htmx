package web

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestBlockAuthoringAPIRoundTripsCatalogValuesAndAtomicBatches(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flowID := current.Snapshot.Flow.ID

	list := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/blocks", flowID))
	if list.Code != http.StatusOK {
		t.Fatalf("block list status = %d: %s", list.Code, list.Body.String())
	}
	var blocks []apiBlockRecord
	decodeJSONResponse(t, list, &blocks)
	if len(blocks) != len(current.Snapshot.Blocks) {
		t.Fatalf("block list length = %d, want %d", len(blocks), len(current.Snapshot.Blocks))
	}

	var gain, sum apiBlockRecord
	for _, block := range blocks {
		switch block.Kind {
		case studio.BlockGain:
			gain = block
		case studio.BlockSum:
			sum = block
		}
	}
	if gain.ID == 0 || sum.ID == 0 {
		t.Fatalf("seeded gain/sum not found: gain=%#v sum=%#v", gain, sum)
	}

	show := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/blocks/%d", gain.ID))
	if show.Code != http.StatusOK {
		t.Fatalf("block show status = %d: %s", show.Code, show.Body.String())
	}
	var shown apiBlockRecord
	decodeJSONResponse(t, show, &shown)
	if shown.ID != gain.ID || shown.ParameterValues["gain"] != gain.ParameterValues["gain"] {
		t.Fatalf("shown block = %#v, listed block = %#v", shown, gain)
	}

	updatedValues := cloneStringMap(gain.ParameterValues)
	updatedValues["gain"] = "3"
	updated := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/blocks/%d", gain.ID), blockUpdateAPIRequest{
		Name: "Updated valve", Parameters: updatedValues,
	})
	if updated.Code != http.StatusOK {
		t.Fatalf("block update status = %d: %s", updated.Code, updated.Body.String())
	}
	var updatedBlock apiBlockRecord
	decodeJSONResponse(t, updated, &updatedBlock)
	if updatedBlock.Name != "Updated valve" || updatedBlock.Parameters.Gain != 3 {
		t.Fatalf("updated block = %#v", updatedBlock)
	}

	sumValues := cloneStringMap(sum.ParameterValues)
	sumValues["signs"] = "+"
	sumUpdate := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/blocks/%d", sum.ID), blockUpdateAPIRequest{
		Name: sum.Name, Parameters: sumValues,
	})
	if sumUpdate.Code != http.StatusBadRequest || !strings.Contains(sumUpdate.Body.String(), "wire on input port 1") {
		t.Fatalf("wired sum shrink = %d: %s", sumUpdate.Code, sumUpdate.Body.String())
	}

	moved := requestJSONAPI(t, server, http.MethodPatch, fmt.Sprintf("/api/v1/flows/%d/blocks/positions", flowID), blockMovesAPIRequest{
		Moves: []blockMoveAPIRequest{
			{BlockID: gain.ID, X: 1400, Y: 1000},
			{BlockID: sum.ID, X: 1600, Y: 1000},
		},
	})
	if moved.Code != http.StatusOK {
		t.Fatalf("batch move status = %d: %s", moved.Code, moved.Body.String())
	}
	var movedBatch blockBatchAPIRecord
	decodeJSONResponse(t, moved, &movedBatch)
	if len(movedBatch.Blocks) != len(blocks) {
		t.Fatalf("moved response blocks = %d, want %d", len(movedBatch.Blocks), len(blocks))
	}

	invalidDelete := requestJSONAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/flows/%d/blocks", flowID), blockIDsAPIRequest{
		BlockIDs: []int64{gain.ID, sum.ID, 999999},
	})
	if invalidDelete.Code != http.StatusNotFound {
		t.Fatalf("invalid batch delete status = %d: %s", invalidDelete.Code, invalidDelete.Body.String())
	}
	stillThere := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/blocks", flowID))
	var afterInvalid []apiBlockRecord
	decodeJSONResponse(t, stillThere, &afterInvalid)
	if !containsBlock(afterInvalid, gain.ID) || !containsBlock(afterInvalid, sum.ID) {
		t.Fatal("atomic delete removed a block before rejecting the invalid id")
	}

	_, copiedID, err := service.AddBlock(context.Background(), flowID, studio.BlockConstant, studio.Point{X: 2400, Y: 1000})
	if err != nil {
		t.Fatal(err)
	}
	beforeCopy := currentBlock(t, service, copiedID)
	duplicated := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/blocks/duplicate", flowID), blockIDsAPIRequest{
		BlockIDs: []int64{copiedID},
	})
	if duplicated.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d: %s", duplicated.Code, duplicated.Body.String())
	}
	var duplicateBatch blockBatchAPIRecord
	decodeJSONResponse(t, duplicated, &duplicateBatch)
	if len(duplicateBatch.Blocks) != 1 {
		t.Fatalf("duplicate response = %#v", duplicateBatch)
	}
	copyBlock := duplicateBatch.Blocks[0]
	if copyBlock.Position.X != beforeCopy.Position.X+studio.GridPitch || copyBlock.Position.Y != beforeCopy.Position.Y+studio.GridPitch || !strings.Contains(copyBlock.Name, "copy") {
		t.Fatalf("copy = %#v, source = %#v", copyBlock, beforeCopy)
	}
}

func TestBlockAuthoringAPICreatesConfiguredBlockAtomically(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	current, err := service.CurrentWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	flowID := current.Snapshot.Flow.ID
	before, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}

	invalid := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/blocks", flowID), blockAddAPIRequest{
		Kind: "lag", X: 1400, Y: 1200,
		Parameters: map[string]string{"time_constant": "0"},
	})
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "time constant") {
		t.Fatalf("invalid configured add = %d: %s", invalid.Code, invalid.Body.String())
	}
	afterInvalid, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterInvalid, before) {
		t.Fatalf("invalid configured add changed snapshot:\nbefore=%#v\nafter=%#v", before, afterInvalid)
	}

	created := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/blocks", flowID), blockAddAPIRequest{
		Kind: "lag", X: 1400, Y: 1200,
		Parameters: map[string]string{"time_constant": "6.5"},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("configured add status = %d: %s", created.Code, created.Body.String())
	}
	var record apiBlockRecord
	decodeJSONResponse(t, created, &record)
	if record.ID == 0 || record.Parameters.TimeConstant != 6.5 {
		t.Fatalf("configured block = %#v", record)
	}
	afterCreate, err := service.Snapshot(ctx, flowID)
	if err != nil {
		t.Fatal(err)
	}
	if afterCreate.Flow.ModelUpdatedAt == before.Flow.ModelUpdatedAt {
		t.Fatal("configured API add did not update the model revision")
	}
	if len(afterCreate.Events) != len(before.Events)+1 {
		t.Fatalf("events grew from %d to %d, want one event", len(before.Events), len(afterCreate.Events))
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func containsBlock(blocks []apiBlockRecord, blockID int64) bool {
	for _, block := range blocks {
		if block.ID == blockID {
			return true
		}
	}
	return false
}

func currentBlock(t *testing.T, service *studio.Studio, blockID int64) studio.Block {
	t.Helper()
	snapshot, err := service.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range snapshot.Blocks {
		if block.ID == blockID {
			return block
		}
	}
	t.Fatalf("block %d not found", blockID)
	return studio.Block{}
}
