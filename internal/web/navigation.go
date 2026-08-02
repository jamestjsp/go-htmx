package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jamestjsp/process-lab/internal/studio"
)

// Project and flowsheet lifecycle. These routes move whole sheets and whole
// projects; the block, connection and simulation handlers in server.go work
// inside the one sheet the caller already has open.
//
// Four response shapes appear below, and which one a handler uses follows
// from what the caller is left looking at:
//
//   - An operation that leaves the caller on a flowsheet answers with the
//     `workbench` fragment for that flowsheet, as every mutation in server.go
//     does.
//   - An operation on a project the caller is reading rather than editing —
//     renaming it from the register — answers with that project's register
//     row, because the register is not a workbench and must never be handed
//     one.
//   - An operation the client has already drawn for itself — dragging a tab
//     into a new place — answers 204, as moveBlock and moveBlocks do for the
//     same gesture on the canvas.
//   - Deleting a project answers with a redirect, because the page the caller
//     is looking at is the thing that stopped existing.
//
// A refusal here is never dressed as a workbench. renderFailure re-renders the
// sheet you are editing with a banner, which is right for an edit to that
// sheet and wrong for an operation whose subject may have just ceased to
// exist; these handlers answer the domain's own message with a status the
// client can branch on.

// renameProject renames a project and answers with the register's complete row
// collection.
//
// It used to answer with the `workbench` fragment, which was a trap. The
// workspace RenameProject returns opens the project's *first* flowsheet,
// because renaming a project says nothing about which sheet is open — so a
// caller on any other sheet who swapped that response into `#workbench` would
// find themselves moved to sheet one, and the register, which is not a
// workbench at all, had nothing it could do with the response but pick it
// apart with `hx-select`.
//
// Projects are listed by name, so replacing only the renamed row would leave
// it in its old position until reload. Answering with all rows preserves the
// canonical order while still avoiding the register shell and its totals,
// neither of which a rename changes. Nothing else calls this route.
func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid project name.", http.StatusBadRequest)
		return
	}
	if _, err := s.studio.RenameProject(r.Context(), projectID, r.FormValue("name")); err != nil {
		refuse(w, err)
		return
	}
	s.renderRegisterRows(w, r)
}

// renderRegisterRows writes the name-ordered rows inside #register-rows.
// Register is two queries whatever the number of projects, so replacing the
// ordered collection has the same database cost as selecting one row from it.
func (s *Server) renderRegisterRows(w http.ResponseWriter, r *http.Request) {
	register, err := s.studio.Register(r.Context())
	if err != nil {
		http.Error(w, "Process Lab could not load the register.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "register-rows", newRegisterView(register)); err != nil {
		http.Error(w, "Process Lab could not render the register.", http.StatusInternalServerError)
	}
}

// deleteProject removes a project and sends the caller to the register.
//
// DeleteProject hands back the workspace that survives the deletion, and this
// deliberately does not open it: the caller was inside the project that is
// gone, and the register is the one page that can show what is left rather
// than dropping them into a project they did not ask for.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if _, err := s.studio.DeleteProject(r.Context(), projectID); err != nil {
		refuse(w, err)
		return
	}
	redirectRegister(w, r)
}

// duplicateFlow copies a flowsheet and opens the copy, which is the workspace
// DuplicateFlow returns. Nothing is selected on it: the copy's blocks are new
// rows, so a selection carried over from the source names blocks that are not
// on the sheet now being rendered.
func (s *Server) duplicateFlow(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	workspace, err := s.studio.DuplicateFlow(r.Context(), flowID)
	if err != nil {
		refuse(w, err)
		return
	}
	s.renderWorkspace(w, r, workspace, 0, "")
}

// deleteFlow removes a flowsheet and opens the tab that takes its place —
// the left neighbour, or the right one when it was the first tab. DeleteFlow
// chooses that sheet, because the choice is the tab strip's own order and the
// strip's order lives in the domain.
func (s *Server) deleteFlow(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	workspace, err := s.studio.DeleteFlow(r.Context(), flowID)
	if err != nil {
		refuse(w, err)
		return
	}
	s.controllerCandidates.deleteFlow(flowID)
	s.renderWorkspace(w, r, workspace, 0, "")
}

// reorderFlows persists a dragged tab strip. The client sends the project's
// full ordered id list, as moveBlocks sends a whole moved selection, so the
// request states the order it drew rather than an edit to be replayed against
// an order the server might no longer share.
//
// It answers 204, like the two canvas drag endpoints. The client has already
// drawn the new order, so there is nothing to swap in — and the workspace
// ReorderFlows returns opens the project's first tab, which is not where the
// user is: rendering it would answer a drag by moving the user to another
// sheet.
func (s *Server) reorderFlows(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid order.", http.StatusBadRequest)
		return
	}
	raw := r.PostForm["id"]
	flowIDs := make([]int64, 0, len(raw))
	for _, value := range raw {
		flowID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || flowID <= 0 {
			http.Error(w, "Invalid identifier.", http.StatusBadRequest)
			return
		}
		flowIDs = append(flowIDs, flowID)
	}
	// A list that is not a permutation of this project's flowsheets is the
	// domain's refusal to state, not this handler's: it is the only party that
	// knows which flowsheets the project holds.
	if _, err := s.studio.ReorderFlows(r.Context(), projectID, flowIDs); err != nil {
		refuse(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renderWorkspace writes the workbench fragment for a workspace the domain
// already assembled, which is what the lifecycle operations return. It is the
// counterpart of renderWorkbench, which starts from a Snapshot and has to ask
// for the workspace around it.
func (s *Server) renderWorkspace(
	w http.ResponseWriter,
	r *http.Request,
	workspace studio.Workspace,
	selected int64,
	message string,
) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(
		w, "workbench", s.newWorkbenchView(workspace, selected, message),
	); err != nil {
		http.Error(w, "Process Lab could not render the workbench.", http.StatusInternalServerError)
	}
}

// redirectRegister sends the client home, honouring HX-Redirect so an HTMX
// caller navigates instead of swapping the response into the page it is on —
// the same branch redirectWorkspace makes for the same reason.
func redirectRegister(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// refuse answers a rejected lifecycle operation with the domain's own message
// and a status the client can branch on: 404 when the subject is gone, 400
// when the domain refused what was asked. The message is never rewritten here,
// so the last-project and last-flowsheet rules read the same in the interface
// as they do in the domain that enforces them.
func refuse(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, studio.ErrNotFound) {
		status = http.StatusNotFound
	}
	http.Error(w, studio.ValidationMessage(err), status)
}
