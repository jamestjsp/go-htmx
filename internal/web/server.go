package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/jamestjsp/go-htmx/internal/studio"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	studio    *studio.Studio
	templates *template.Template
	handler   http.Handler
}

func New(studioService *studio.Studio) (*Server, error) {
	templates, err := template.New("processlab").ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("load static assets: %w", err)
	}

	server := &Server{studio: studioService, templates: templates}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(static)))
	mux.HandleFunc("GET /", server.page)
	mux.HandleFunc("GET /projects/{projectID}", server.projectPage)
	mux.HandleFunc("GET /projects/{projectID}/flows/{flowID}", server.projectFlowPage)
	mux.HandleFunc("GET /flows/{flowID}/workbench", server.workbench)
	mux.HandleFunc("POST /projects", server.createProject)
	mux.HandleFunc("PUT /projects/{projectID}/name", server.renameProject)
	mux.HandleFunc("DELETE /projects/{projectID}", server.deleteProject)
	mux.HandleFunc("POST /projects/{projectID}/flows", server.createFlow)
	mux.HandleFunc("PATCH /projects/{projectID}/flows/order", server.reorderFlows)
	mux.HandleFunc("PUT /flows/{flowID}/name", server.renameFlow)
	mux.HandleFunc("POST /flows/{flowID}/duplicate", server.duplicateFlow)
	mux.HandleFunc("DELETE /flows/{flowID}", server.deleteFlow)
	mux.HandleFunc("POST /flows/{flowID}/blocks", server.addBlock)
	mux.HandleFunc("PATCH /blocks/{blockID}/position", server.moveBlock)
	mux.HandleFunc("PATCH /flows/{flowID}/blocks/positions", server.moveBlocks)
	mux.HandleFunc("PUT /blocks/{blockID}", server.updateBlock)
	mux.HandleFunc("DELETE /blocks/{blockID}", server.deleteBlock)
	mux.HandleFunc("DELETE /flows/{flowID}/blocks", server.deleteBlocks)
	mux.HandleFunc("POST /flows/{flowID}/blocks/duplicate", server.duplicateBlocks)
	mux.HandleFunc("POST /flows/{flowID}/connections", server.connect)
	mux.HandleFunc("DELETE /connections/{connectionID}", server.disconnect)
	mux.HandleFunc("DELETE /blocks/{blockID}/connections", server.disconnectBlock)
	mux.HandleFunc("POST /flows/{flowID}/simulations", server.runSimulation)
	server.handler = securityHeaders(mux)
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// page renders the register: every project the database holds, each row
// carrying the flowsheets it expands to reveal. It replaces a redirect into a
// flowsheet, which left the application with no screen that showed what
// projects existed.
//
// `GET /` is also the mux's catch-all, so an address nothing else matches
// arrives here. That is a miss, not the home page, and answering it with the
// register would dress every typo as a 200.
func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	register, err := s.studio.Register(r.Context())
	if err != nil {
		http.Error(w, "Process Lab could not load the register.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "register", newRegisterView(register)); err != nil {
		http.Error(w, "Process Lab could not render the register.", http.StatusInternalServerError)
	}
}

func (s *Server) projectFlowPage(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	workspace, err := s.studio.Workspace(r.Context(), projectID, flowID)
	if err != nil {
		if errors.Is(err, studio.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Process Lab could not load the flowsheet.", http.StatusInternalServerError)
		return
	}
	view := pageView{Workbench: newWorkbenchView(workspace, selectedID(r), "")}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "page", view); err != nil {
		http.Error(w, "Process Lab could not render the page.", http.StatusInternalServerError)
	}
}

func (s *Server) projectPage(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	workspace, err := s.studio.ProjectWorkspace(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, studio.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Process Lab could not load the project.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, workspacePath(workspace), http.StatusSeeOther)
}

func (s *Server) workbench(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	snapshot, err := s.studio.Snapshot(r.Context(), flowID)
	if err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, selectedID(r), "")
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid project.", http.StatusBadRequest)
		return
	}
	workspace, err := s.studio.CreateProject(r.Context(), r.FormValue("name"))
	if err != nil {
		http.Error(w, studio.ValidationMessage(err), http.StatusBadRequest)
		return
	}
	s.redirectWorkspace(w, r, workspace)
}

func (s *Server) createFlow(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid flowsheet.", http.StatusBadRequest)
		return
	}
	workspace, err := s.studio.CreateFlow(r.Context(), projectID, r.FormValue("name"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, studio.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, studio.ValidationMessage(err), status)
		return
	}
	s.redirectWorkspace(w, r, workspace)
}

func (s *Server) renameFlow(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	workspace, err := s.studio.RenameFlow(r.Context(), flowID, r.FormValue("name"))
	if err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "workbench", newWorkbenchView(
		workspace, selectedID(r), "",
	)); err != nil {
		http.Error(w, "Process Lab could not render the workbench.", http.StatusInternalServerError)
	}
}

func (s *Server) addBlock(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, flowID, 0, studio.ValidationMessage(err))
		return
	}
	kind, err := studio.ParseBlockKind(r.FormValue("kind"))
	if err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	position := studio.Point{
		X: formInt(r, "x", 90),
		Y: formInt(r, "y", 120),
	}
	snapshot, blockID, err := s.studio.AddBlock(r.Context(), flowID, kind, position)
	if err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, blockID, "")
}

func (s *Server) moveBlock(w http.ResponseWriter, r *http.Request) {
	blockID, ok := pathID(w, r, "blockID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid position.", http.StatusBadRequest)
		return
	}
	x, errX := strconv.Atoi(r.FormValue("x"))
	y, errY := strconv.Atoi(r.FormValue("y"))
	if errX != nil || errY != nil {
		http.Error(w, "Invalid position.", http.StatusBadRequest)
		return
	}
	if err := s.studio.MoveBlock(r.Context(), blockID, studio.Point{X: x, Y: y}); err != nil {
		http.Error(w, studio.ValidationMessage(err), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// moveBlocks repositions a selection in one request. The id, x and y form
// values are parallel arrays; sending N separate requests instead would be
// both slower and non-atomic, so a partial arrangement could survive.
func (s *Server) moveBlocks(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid positions.", http.StatusBadRequest)
		return
	}
	ids, xs, ys := r.PostForm["id"], r.PostForm["x"], r.PostForm["y"]
	if len(ids) == 0 || len(ids) != len(xs) || len(ids) != len(ys) {
		http.Error(w, "Every block needs an id, an x and a y.", http.StatusBadRequest)
		return
	}
	moves := make([]studio.BlockMove, 0, len(ids))
	for i := range ids {
		blockID, errID := strconv.ParseInt(ids[i], 10, 64)
		x, errX := strconv.Atoi(xs[i])
		y, errY := strconv.Atoi(ys[i])
		if errID != nil || errX != nil || errY != nil {
			http.Error(w, "Invalid position.", http.StatusBadRequest)
			return
		}
		moves = append(moves, studio.BlockMove{
			BlockID:  blockID,
			Position: studio.Point{X: x, Y: y},
		})
	}
	if err := s.studio.MoveBlocks(r.Context(), flowID, moves); err != nil {
		http.Error(w, studio.ValidationMessage(err), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateBlock(w http.ResponseWriter, r *http.Request) {
	blockID, ok := pathID(w, r, "blockID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, 0, blockID, err)
		return
	}
	parameters := make(map[string]string, len(r.PostForm))
	for name, values := range r.PostForm {
		if name != "name" && len(values) != 0 {
			parameters[name] = values[0]
		}
	}
	snapshot, err := s.studio.UpdateBlock(r.Context(), blockID, studio.BlockUpdate{
		Name:       r.FormValue("name"),
		Parameters: parameters,
	})
	if err != nil {
		s.renderFailure(w, r, 0, blockID, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, blockID, "")
}

func (s *Server) deleteBlock(w http.ResponseWriter, r *http.Request) {
	blockID, ok := pathID(w, r, "blockID")
	if !ok {
		return
	}
	snapshot, err := s.studio.DeleteBlock(r.Context(), blockID)
	if err != nil {
		s.renderFailure(w, r, 0, 0, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, 0, "")
}

// deleteBlocks removes a selection. Ids arrive as repeated query values,
// which is what a DELETE carries.
func (s *Server) deleteBlocks(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	blockIDs, ok := s.formIDs(w, r, flowID)
	if !ok {
		return
	}
	snapshot, err := s.studio.DeleteBlocks(r.Context(), flowID, blockIDs)
	if err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, 0, "")
}

func (s *Server) duplicateBlocks(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	blockIDs, ok := s.formIDs(w, r, flowID)
	if !ok {
		return
	}
	snapshot, err := s.studio.DuplicateBlocks(r.Context(), flowID, blockIDs)
	if err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, 0, "")
}

// formIDs reads the repeated `id` values shared by the batch endpoints.
func (s *Server) formIDs(w http.ResponseWriter, r *http.Request, flowID int64) ([]int64, bool) {
	raw := r.Form["id"]
	if len(raw) == 0 {
		s.renderFailure(w, r, flowID, 0, &studio.ValidationError{
			Message: "select at least one block first",
		})
		return nil, false
	}
	blockIDs := make([]int64, 0, len(raw))
	for _, value := range raw {
		blockID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || blockID <= 0 {
			http.Error(w, "Invalid identifier.", http.StatusBadRequest)
			return nil, false
		}
		blockIDs = append(blockIDs, blockID)
	}
	return blockIDs, true
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, flowID, 0, err)
		return
	}
	sourceID, errSource := strconv.ParseInt(r.FormValue("source_id"), 10, 64)
	targetID, errTarget := strconv.ParseInt(r.FormValue("target_id"), 10, 64)
	sourcePort, sourcePortOK := portValue(r, "source_port")
	targetPort, targetPortOK := portValue(r, "target_port")
	if errSource != nil || errTarget != nil || !sourcePortOK || !targetPortOK {
		s.renderFailure(w, r, flowID, 0, &studio.ValidationError{Message: "choose an output and an input to connect"})
		return
	}
	snapshot, err := s.studio.Connect(r.Context(), flowID, studio.Wire{
		SourceID: sourceID, SourcePort: sourcePort,
		TargetID: targetID, TargetPort: targetPort,
	})
	if err != nil {
		s.renderFailure(w, r, flowID, targetID, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, targetID, "")
}

// portValue reads an optional terminal index. An absent field means the
// block's first port, so a client written before ports keeps connecting
// exactly what it always did. A present but unreadable one is a malformed
// request rather than a default, and says so. Whether the index names a port
// the block actually has is the domain's answer, not this one's.
func portValue(r *http.Request, name string) (int, bool) {
	raw := r.FormValue(name)
	if raw == "" {
		return 0, true
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return port, true
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	connectionID, ok := pathID(w, r, "connectionID")
	if !ok {
		return
	}
	snapshot, err := s.studio.Disconnect(r.Context(), connectionID)
	if err != nil {
		s.renderFailure(w, r, 0, 0, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, selectedID(r), "")
}

func (s *Server) disconnectBlock(w http.ResponseWriter, r *http.Request) {
	blockID, ok := pathID(w, r, "blockID")
	if !ok {
		return
	}
	snapshot, err := s.studio.DisconnectBlock(r.Context(), blockID)
	if err != nil {
		s.renderFailure(w, r, 0, blockID, err)
		return
	}
	s.renderWorkbench(w, r, snapshot, blockID, "")
}

func (s *Server) runSimulation(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderFailure(w, r, flowID, selectedID(r), err)
		return
	}
	duration, durationErr := strconv.ParseFloat(r.FormValue("duration"), 64)
	sampleTime, sampleErr := strconv.ParseFloat(r.FormValue("sample_time"), 64)
	if durationErr != nil || sampleErr != nil {
		s.renderFailure(w, r, flowID, selectedID(r), &studio.ValidationError{
			Message: "duration and sample time must be numbers",
		})
		return
	}
	snapshot, err := s.studio.Run(r.Context(), flowID, studio.SimulationRequest{
		Duration: duration, SampleTime: sampleTime,
	})
	if err != nil {
		s.renderFailure(w, r, flowID, selectedID(r), err)
		return
	}
	s.renderWorkbench(w, r, snapshot, selectedID(r), "")
}

func (s *Server) renderFailure(w http.ResponseWriter, r *http.Request, flowID, selected int64, failure any) {
	var message string
	switch value := failure.(type) {
	case error:
		message = studio.ValidationMessage(value)
	case string:
		message = value
	default:
		message = "The operation could not be completed."
	}
	var snapshot studio.Snapshot
	var err error
	if flowID != 0 {
		snapshot, err = s.studio.Snapshot(r.Context(), flowID)
	} else {
		snapshot, err = s.studio.Current(r.Context())
	}
	if err != nil {
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	s.renderWorkbench(w, r, snapshot, selected, message)
}

func (s *Server) renderWorkbench(
	w http.ResponseWriter,
	r *http.Request,
	snapshot studio.Snapshot,
	selected int64,
	message string,
) {
	workspace, err := s.studio.Workspace(
		r.Context(), snapshot.Flow.ProjectID, snapshot.Flow.ID,
	)
	if err != nil {
		http.Error(w, "Process Lab could not load the project.", http.StatusInternalServerError)
		return
	}
	workspace.Snapshot = snapshot
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(
		w, "workbench", newWorkbenchView(workspace, selected, message),
	); err != nil {
		http.Error(w, "Process Lab could not render the workbench.", http.StatusInternalServerError)
	}
}

func (s *Server) redirectWorkspace(w http.ResponseWriter, r *http.Request, workspace studio.Workspace) {
	location := workspacePath(workspace)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", location)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func workspacePath(workspace studio.Workspace) string {
	return "/projects/" + strconv.FormatInt(workspace.Project.ID, 10) +
		"/flows/" + strconv.FormatInt(workspace.Snapshot.Flow.ID, 10)
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid identifier.", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func formInt(r *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(r.FormValue(name))
	if err != nil {
		return fallback
	}
	return value
}

func selectedID(r *http.Request) int64 {
	value := r.URL.Query().Get("selected")
	if value == "" {
		value = r.FormValue("selected_id")
	}
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"script-src 'self' https://cdn.jsdelivr.net",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data:",
			"connect-src 'self'",
			"font-src 'self'",
		}, "; "))
		next.ServeHTTP(w, r)
	})
}
