package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestFlowDocumentAPIIsAStableRoundTripAndAtomic(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flowID := current.Snapshot.Flow.ID

	dump := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d/document", flowID))
	if dump.Code != http.StatusOK {
		t.Fatalf("dump status = %d: %s", dump.Code, dump.Body.String())
	}
	var document studio.FlowDocument
	decodeJSONResponse(t, dump, &document)
	if document.Version != 1 || len(document.Blocks) != len(current.Snapshot.Blocks) || len(document.Wires) != len(current.Snapshot.Connections) {
		t.Fatalf("dump = version %d, %d blocks, %d wires", document.Version, len(document.Blocks), len(document.Wires))
	}

	unchanged := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/flows/%d/document", flowID), document)
	if unchanged.Code != http.StatusOK {
		t.Fatalf("round-trip status = %d: %s", unchanged.Code, unchanged.Body.String())
	}
	var unchangedResult flowDocumentApplyAPIResponse
	decodeJSONResponse(t, unchanged, &unchangedResult)
	if unchangedResult.Result.Changed {
		t.Fatalf("round-trip result = %#v", unchangedResult.Result)
	}

	invalid := document
	for index := range invalid.Blocks {
		if invalid.Blocks[index].Kind == studio.BlockGain {
			invalid.Blocks[index].Parameters["gain"] = "not-a-number"
		}
	}
	rejected := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/flows/%d/document", flowID), invalid)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "must be a number") {
		t.Fatalf("invalid apply = %d: %s", rejected.Code, rejected.Body.String())
	}
	stillCurrent, err := service.Snapshot(context.Background(), flowID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stillCurrent.Blocks) != len(current.Snapshot.Blocks) || len(stillCurrent.Connections) != len(current.Snapshot.Connections) {
		t.Fatal("rejected document changed the flowsheet")
	}
}

func TestFlowDocumentAPIAppliesEmptyGraphAndDryRuns(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := service.CreateFlow(context.Background(), current.Project.ID, "Declarative API")
	if err != nil {
		t.Fatal(err)
	}
	document := studio.FlowDocument{
		Version: 1,
		Blocks: []studio.DocumentBlock{
			{Kind: studio.BlockConstant, Name: "Feed", Position: studio.DocumentPosition{X: 100, Y: 100}, Parameters: map[string]string{"value": "2"}},
			{Kind: studio.BlockGain, Name: "Valve", Position: studio.DocumentPosition{X: 400, Y: 100}, Parameters: map[string]string{"gain": "3"}},
		},
		Wires: []studio.DocumentWire{{Source: "Feed", Target: "Valve"}},
	}
	created := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/flows/%d/document", empty.Snapshot.Flow.ID), document)
	if created.Code != http.StatusOK {
		t.Fatalf("empty apply status = %d: %s", created.Code, created.Body.String())
	}
	var createdResult flowDocumentApplyAPIResponse
	decodeJSONResponse(t, created, &createdResult)
	if len(createdResult.Result.Added) != 2 || createdResult.Result.WiresAdded != 1 {
		t.Fatalf("empty apply result = %#v", createdResult.Result)
	}

	dry := document
	dry.Blocks = append(dry.Blocks, studio.DocumentBlock{
		Kind: studio.BlockConstant, Name: "Preview", Position: studio.DocumentPosition{X: 800, Y: 100}, Parameters: map[string]string{"value": "4"},
	})
	dryResponse := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/flows/%d/document?dry-run=true", empty.Snapshot.Flow.ID), dry)
	if dryResponse.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d: %s", dryResponse.Code, dryResponse.Body.String())
	}
	var dryResult flowDocumentApplyAPIResponse
	decodeJSONResponse(t, dryResponse, &dryResult)
	if !dryResult.Result.DryRun || len(dryResult.Result.Added) != 1 {
		t.Fatalf("dry-run result = %#v", dryResult.Result)
	}
	afterDry, err := service.Snapshot(context.Background(), empty.Snapshot.Flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterDry.Blocks) != 2 {
		t.Fatalf("dry-run changed block count to %d", len(afterDry.Blocks))
	}
}
