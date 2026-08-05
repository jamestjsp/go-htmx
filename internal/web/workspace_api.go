package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type workspaceNameAPIRequest struct {
	Name string `json:"name"`
}

type workspaceDeleteAPIRequest struct {
	Force bool `json:"force"`
}

type flowReorderAPIRequest struct {
	FlowIDs []int64 `json:"flowIds"`
}

type projectAPIRecord struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type projectListAPIRecord struct {
	projectAPIRecord
	FlowCount int `json:"flowCount"`
}

type flowAPIRecord struct {
	ID             int64  `json:"id"`
	ProjectID      int64  `json:"projectId"`
	ProjectName    string `json:"projectName,omitempty"`
	Name           string `json:"name"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	ModelUpdatedAt string `json:"modelUpdatedAt"`
	NeedsRun       bool   `json:"needsRun"`
	BlockCount     int    `json:"blockCount,omitempty"`
}

type workspaceAPIRecord struct {
	Project    projectAPIRecord `json:"project"`
	Flows      []flowAPIRecord  `json:"flows"`
	Snapshot   flowAPIRecord    `json:"snapshot"`
	BlockCount int              `json:"blockCount"`
}

func (s *Server) projectListAPI(r *http.Request) (apiResponse, error) {
	register, err := s.studio.Register(r.Context())
	if err != nil {
		return apiResponse{}, err
	}
	projects := make([]projectListAPIRecord, 0, len(register.Projects))
	for _, entry := range register.Projects {
		projects = append(projects, projectListAPIRecord{
			projectAPIRecord: projectRecord(entry.Project),
			FlowCount:        len(entry.Flows),
		})
	}
	return apiResponse{Value: projects}, nil
}

func (s *Server) projectDetailAPI(r *http.Request) (apiResponse, error) {
	projectID, err := parsePathInt(r, "projectID")
	if err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.ProjectWorkspace(r.Context(), projectID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func (s *Server) projectCreateAPI(r *http.Request) (apiResponse, error) {
	var input workspaceNameAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.CreateProject(r.Context(), input.Name)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: workspaceRecord(workspace)}, nil
}

func (s *Server) projectRenameAPI(r *http.Request) (apiResponse, error) {
	projectID, err := parsePathInt(r, "projectID")
	if err != nil {
		return apiResponse{}, err
	}
	var input workspaceNameAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.RenameProject(r.Context(), projectID, input.Name)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func (s *Server) projectDeleteAPI(r *http.Request) (apiResponse, error) {
	projectID, err := parsePathInt(r, "projectID")
	if err != nil {
		return apiResponse{}, err
	}
	if r.ContentLength == 0 {
		return apiResponse{}, &studio.ValidationError{Message: "project deletion requires --force."}
	}
	var input workspaceDeleteAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	if !input.Force {
		return apiResponse{}, &studio.ValidationError{Message: "project deletion requires --force."}
	}
	workspace, err := s.studio.DeleteProject(r.Context(), projectID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func (s *Server) flowListAPI(r *http.Request) (apiResponse, error) {
	register, err := s.studio.Register(r.Context())
	if err != nil {
		return apiResponse{}, err
	}
	projectFilter, err := optionalQueryInt(r, "project")
	if err != nil {
		return apiResponse{}, err
	}
	flows := make([]flowAPIRecord, 0)
	for _, entry := range register.Projects {
		if projectFilter != nil && entry.Project.ID != *projectFilter {
			continue
		}
		for _, flow := range entry.Flows {
			record := flowRecord(flow)
			record.ProjectName = entry.Project.Name
			flows = append(flows, record)
		}
	}
	return apiResponse{Value: flows}, nil
}

func (s *Server) flowDetailAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.workspaceForFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func (s *Server) flowCreateAPI(r *http.Request) (apiResponse, error) {
	projectID, err := parsePathInt(r, "projectID")
	if err != nil {
		return apiResponse{}, err
	}
	var input workspaceNameAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.CreateFlow(r.Context(), projectID, input.Name)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: workspaceRecord(workspace)}, nil
}

func (s *Server) flowRenameAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input workspaceNameAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.RenameFlow(r.Context(), flowID, input.Name)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func (s *Server) flowDuplicateAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.DuplicateFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Status: http.StatusCreated, Value: workspaceRecord(workspace)}, nil
}

func (s *Server) flowDeleteAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input workspaceDeleteAPIRequest
	if r.ContentLength != 0 {
		if err := decodeAPIJSON(r, &input); err != nil {
			return apiResponse{}, err
		}
	}
	before, err := s.workspaceForFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	blockCount := len(before.Snapshot.Blocks)
	if blockCount > 0 && !input.Force {
		return apiResponse{}, &studio.ValidationError{Message: fmt.Sprintf(
			"flowsheet %d contains %d blocks; use --force to delete it.", flowID, blockCount,
		)}
	}
	workspace, err := s.studio.DeleteFlow(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func (s *Server) workspaceForFlow(ctx context.Context, flowID int64) (studio.Workspace, error) {
	register, err := s.studio.Register(ctx)
	if err != nil {
		return studio.Workspace{}, err
	}
	for _, entry := range register.Projects {
		for _, flow := range entry.Flows {
			if flow.ID == flowID {
				return s.studio.Workspace(ctx, entry.Project.ID, flowID)
			}
		}
	}
	return studio.Workspace{}, studio.ErrNotFound
}

func (s *Server) flowReorderAPI(r *http.Request) (apiResponse, error) {
	projectID, err := parsePathInt(r, "projectID")
	if err != nil {
		return apiResponse{}, err
	}
	var input flowReorderAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	workspace, err := s.studio.ReorderFlows(r.Context(), projectID, input.FlowIDs)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: workspaceRecord(workspace)}, nil
}

func optionalQueryInt(r *http.Request, name string) (*int64, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return nil, &studio.ValidationError{Message: name + " must be a positive integer."}
	}
	return &parsed, nil
}

func projectRecord(project studio.Project) projectAPIRecord {
	return projectAPIRecord{
		ID: project.ID, Name: project.Name,
		CreatedAt: project.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: project.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func flowRecord(flow studio.Flow) flowAPIRecord {
	return flowAPIRecord{
		ID: flow.ID, ProjectID: flow.ProjectID, Name: flow.Name,
		CreatedAt:      flow.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      flow.UpdatedAt.Format(time.RFC3339Nano),
		ModelUpdatedAt: flow.ModelUpdatedAt.Format(time.RFC3339Nano),
		NeedsRun:       flow.NeedsRun,
	}
}

func workspaceRecord(workspace studio.Workspace) workspaceAPIRecord {
	snapshot := flowRecord(workspace.Snapshot.Flow)
	snapshot.BlockCount = len(workspace.Snapshot.Blocks)
	flows := make([]flowAPIRecord, 0, len(workspace.Flows))
	for _, flow := range workspace.Flows {
		flows = append(flows, flowRecord(flow))
	}
	return workspaceAPIRecord{
		Project:    projectRecord(workspace.Project),
		Flows:      flows,
		Snapshot:   snapshot,
		BlockCount: len(workspace.Snapshot.Blocks),
	}
}
