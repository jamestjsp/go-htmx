package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jamestjsp/process-lab/internal/studio"
)

func TestWorkspaceAPIListsRegisterStateAndResolvesFlowDetails(t *testing.T) {
	server, service := openTestServer(t)
	register, err := service.Register(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	projects := requestAPI(t, server, http.MethodGet, "/api/v1/projects")
	if projects.Code != http.StatusOK {
		t.Fatalf("project list status = %d: %s", projects.Code, projects.Body.String())
	}
	var projectRecords []projectListAPIRecord
	if err := json.Unmarshal(projects.Body.Bytes(), &projectRecords); err != nil {
		t.Fatalf("decode project list: %v", err)
	}
	if len(projectRecords) != len(register.Projects) || projectRecords[0].ID != current.Project.ID {
		t.Fatalf("project list = %#v, register = %#v", projectRecords, register.Projects)
	}
	if projectRecords[0].FlowCount != len(register.Projects[0].Flows) {
		t.Fatalf("flow count = %d, want %d", projectRecords[0].FlowCount, len(register.Projects[0].Flows))
	}

	flows := requestAPI(t, server, http.MethodGet, "/api/v1/flows")
	if flows.Code != http.StatusOK {
		t.Fatalf("flow list status = %d: %s", flows.Code, flows.Body.String())
	}
	var flowRecords []flowAPIRecord
	if err := json.Unmarshal(flows.Body.Bytes(), &flowRecords); err != nil {
		t.Fatalf("decode flow list: %v", err)
	}
	if len(flowRecords) != len(register.Projects[0].Flows) {
		t.Fatalf("flow list length = %d, want %d", len(flowRecords), len(register.Projects[0].Flows))
	}
	for index, flow := range register.Projects[0].Flows {
		if flowRecords[index].ID != flow.ID || flowRecords[index].NeedsRun != flow.NeedsRun {
			t.Fatalf("flow %d API record = %#v, register flow = %#v", index, flowRecords[index], flow)
		}
	}

	detail := requestAPI(t, server, http.MethodGet, fmt.Sprintf("/api/v1/flows/%d", current.Snapshot.Flow.ID))
	if detail.Code != http.StatusOK {
		t.Fatalf("flow detail status = %d: %s", detail.Code, detail.Body.String())
	}
	var workspace workspaceAPIRecord
	if err := json.Unmarshal(detail.Body.Bytes(), &workspace); err != nil {
		t.Fatalf("decode flow detail: %v", err)
	}
	if workspace.Project.ID != current.Project.ID || workspace.Snapshot.ID != current.Snapshot.Flow.ID || workspace.BlockCount != len(current.Snapshot.Blocks) {
		t.Fatalf("flow detail = %#v, current = project %d flow %d blocks %d", workspace, current.Project.ID, current.Snapshot.Flow.ID, len(current.Snapshot.Blocks))
	}
}

func TestWorkspaceAPILifecycleAndFlowDeleteForce(t *testing.T) {
	server, service := openTestServer(t)

	createdProject := requestJSONAPI(t, server, http.MethodPost, "/api/v1/projects", workspaceNameAPIRequest{Name: "Boiler"})
	if createdProject.Code != http.StatusCreated {
		t.Fatalf("create project status = %d: %s", createdProject.Code, createdProject.Body.String())
	}
	var projectWorkspace workspaceAPIRecord
	decodeJSONResponse(t, createdProject, &projectWorkspace)

	createdFlow := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/flows", projectWorkspace.Project.ID), workspaceNameAPIRequest{Name: "Level loop"})
	if createdFlow.Code != http.StatusCreated {
		t.Fatalf("create flow status = %d: %s", createdFlow.Code, createdFlow.Body.String())
	}
	var flowWorkspace workspaceAPIRecord
	decodeJSONResponse(t, createdFlow, &flowWorkspace)
	flowID := flowWorkspace.Snapshot.ID

	rename := requestJSONAPI(t, server, http.MethodPut, fmt.Sprintf("/api/v1/flows/%d/name", flowID), workspaceNameAPIRequest{Name: "Renamed loop"})
	if rename.Code != http.StatusOK {
		t.Fatalf("rename flow status = %d: %s", rename.Code, rename.Body.String())
	}

	if _, _, err := service.AddBlock(context.Background(), flowID, studio.BlockConstant, studio.Point{X: 20, Y: 20}); err != nil {
		t.Fatal(err)
	}
	withoutForce := requestAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/flows/%d", flowID))
	if withoutForce.Code != http.StatusBadRequest || !strings.Contains(withoutForce.Body.String(), "use --force") {
		t.Fatalf("delete without force = %d: %s", withoutForce.Code, withoutForce.Body.String())
	}
	withForce := requestJSONAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/flows/%d", flowID), workspaceDeleteAPIRequest{Force: true})
	if withForce.Code != http.StatusOK {
		t.Fatalf("delete with force = %d: %s", withForce.Code, withForce.Body.String())
	}

	lastFlow := requestJSONAPI(t, server, http.MethodPost, fmt.Sprintf("/api/v1/projects/%d/flows", projectWorkspace.Project.ID), workspaceNameAPIRequest{Name: "Temporary"})
	if lastFlow.Code != http.StatusCreated {
		t.Fatalf("create second flow = %d: %s", lastFlow.Code, lastFlow.Body.String())
	}
	var lastWorkspace workspaceAPIRecord
	decodeJSONResponse(t, lastFlow, &lastWorkspace)
	lastFlowID := lastWorkspace.Snapshot.ID
	if response := requestJSONAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/flows/%d", lastFlowID), workspaceDeleteAPIRequest{Force: true}); response.Code != http.StatusOK {
		t.Fatalf("delete non-last flow = %d: %s", response.Code, response.Body.String())
	}
	if response := requestJSONAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/flows/%d", flowWorkspace.Snapshot.ID), workspaceDeleteAPIRequest{Force: true}); response.Code != http.StatusNotFound {
		t.Fatalf("deleted flow lookup = %d: %s", response.Code, response.Body.String())
	}
}

func TestWorkspaceAPIProjectDeleteRequiresForceWithoutMutation(t *testing.T) {
	server, service := openTestServer(t)
	ctx := context.Background()
	created, err := service.CreateProject(ctx, "Operations")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/projects/%d", created.Project.ID)

	withoutBody := requestAPI(t, server, http.MethodDelete, path)
	if withoutBody.Code != http.StatusBadRequest || !strings.Contains(withoutBody.Body.String(), "--force") {
		t.Fatalf("bodyless delete = %d: %s", withoutBody.Code, withoutBody.Body.String())
	}
	if _, err := service.ProjectWorkspace(ctx, created.Project.ID); err != nil {
		t.Fatalf("bodyless refusal mutated project: %v", err)
	}

	withoutForce := requestJSONAPI(t, server, http.MethodDelete, path, workspaceDeleteAPIRequest{})
	if withoutForce.Code != http.StatusBadRequest || !strings.Contains(withoutForce.Body.String(), "--force") {
		t.Fatalf("false force delete = %d: %s", withoutForce.Code, withoutForce.Body.String())
	}
	if _, err := service.ProjectWorkspace(ctx, created.Project.ID); err != nil {
		t.Fatalf("false force refusal mutated project: %v", err)
	}

	withForce := requestJSONAPI(t, server, http.MethodDelete, path, workspaceDeleteAPIRequest{Force: true})
	if withForce.Code != http.StatusOK {
		t.Fatalf("forced delete = %d: %s", withForce.Code, withForce.Body.String())
	}
	if _, err := service.ProjectWorkspace(ctx, created.Project.ID); !errors.Is(err, studio.ErrNotFound) {
		t.Fatalf("forced delete project lookup = %v, want not found", err)
	}
}

func TestWorkspaceAPIRejectsForeignFlowReorderAndLastFlowDelete(t *testing.T) {
	server, service := openTestServer(t)
	current, err := service.CurrentWorkspace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateProject(context.Background(), "Other")
	if err != nil {
		t.Fatal(err)
	}
	response := requestJSONAPI(t, server, http.MethodPatch, fmt.Sprintf("/api/v1/projects/%d/flows/order", current.Project.ID), flowReorderAPIRequest{
		FlowIDs: []int64{current.Snapshot.Flow.ID, other.Snapshot.Flow.ID},
	})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "each of this project's") {
		t.Fatalf("foreign reorder = %d: %s", response.Code, response.Body.String())
	}

	last := requestJSONAPI(t, server, http.MethodDelete, fmt.Sprintf("/api/v1/flows/%d", other.Snapshot.Flow.ID), workspaceDeleteAPIRequest{Force: true})
	if last.Code != http.StatusBadRequest || !strings.Contains(last.Body.String(), "at least one flowsheet") {
		t.Fatalf("last flow delete = %d: %s", last.Code, last.Body.String())
	}
}

func requestJSONAPI(t *testing.T, server *Server, method, path string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func decodeJSONResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v\n%s", err, response.Body.String())
	}
}
