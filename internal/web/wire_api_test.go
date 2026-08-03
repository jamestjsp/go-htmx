package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestWireAPIListsPortsAndPreservesDomainRefusals(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flowID := current.Snapshot.Flow.ID

	sourceSnapshot, sourceID, err := service.AddBlock(context.Background(), flowID, studio.BlockConstant, studio.Point{X: 2000, Y: 1000})
	if err != nil {
		t.Fatal(err)
	}
	targetSnapshot, targetID, err := service.AddBlock(context.Background(), flowID, studio.BlockGain, studio.Point{X: 2400, Y: 1000})
	if err != nil {
		t.Fatal(err)
	}
	vectorSnapshot, vectorID, err := service.AddBlock(context.Background(), flowID, studio.BlockVectorConstant, studio.Point{X: 2000, Y: 1400})
	if err != nil {
		t.Fatal(err)
	}
	_, secondSourceID, err := service.AddBlock(context.Background(), flowID, studio.BlockConstant, studio.Point{X: 2000, Y: 1800})
	if err != nil {
		t.Fatal(err)
	}

	connected := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/connections", flowID), wireConnectAPIRequest{
		SourceID: sourceID, TargetID: targetID,
	})
	if connected.Code != http.StatusCreated {
		t.Fatalf("connect status = %d: %s", connected.Code, connected.Body.String())
	}
	var wire apiWireRecord
	decodeJSONResponse(t, connected, &wire)
	if wire.SourceID != sourceID || wire.TargetID != targetID || wire.SourcePort != 0 || wire.TargetPort != 0 || wire.SourceWidth != 1 || wire.TargetWidth != 1 {
		t.Fatalf("connected wire = %#v", wire)
	}

	list := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/connections", flowID))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "sourceName") {
		t.Fatalf("wire list = %d: %s", list.Code, list.Body.String())
	}

	occupied := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/connections", flowID), wireConnectAPIRequest{
		SourceID: secondSourceID, TargetID: targetID,
	})
	if occupied.Code != http.StatusBadRequest || !strings.Contains(occupied.Body.String(), "already has an input") {
		t.Fatalf("occupied input = %d: %s", occupied.Code, occupied.Body.String())
	}

	mismatch := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/connections", flowID), wireConnectAPIRequest{
		SourceID: vectorID, TargetID: targetID,
	})
	if mismatch.Code != http.StatusBadRequest || !strings.Contains(mismatch.Body.String(), "cannot connect") {
		t.Fatalf("width mismatch = %d: %s", mismatch.Code, mismatch.Body.String())
	}
	if len(sourceSnapshot.Blocks) == 0 || len(targetSnapshot.Blocks) == 0 || len(vectorSnapshot.Blocks) == 0 {
		t.Fatal("created block snapshots unexpectedly empty")
	}

	removed := requestAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/blocks/%d/connections", sourceID))
	if removed.Code != http.StatusOK || !strings.Contains(removed.Body.String(), `"removed":1`) {
		t.Fatalf("block disconnect = %d: %s", removed.Code, removed.Body.String())
	}

	reconnected := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/flows/%d/connections", flowID), wireConnectAPIRequest{
		SourceID: sourceID, TargetID: targetID,
	})
	if reconnected.Code != http.StatusCreated {
		t.Fatalf("reconnect status = %d: %s", reconnected.Code, reconnected.Body.String())
	}
	var reconnectedWire apiWireRecord
	decodeJSONResponse(t, reconnected, &reconnectedWire)
	deleted := requestAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/connections/%d", reconnectedWire.ID))
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"removed":1`) {
		t.Fatalf("connection delete = %d: %s", deleted.Code, deleted.Body.String())
	}
}
