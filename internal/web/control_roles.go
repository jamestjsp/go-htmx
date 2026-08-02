package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

const maxControlRoleRequestBytes = 1 << 20

func (s *Server) getControlRoles(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	spec, err := s.studio.ControlRoles(r.Context(), flowID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, studio.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, studio.ValidationMessage(err), status)
		return
	}
	writeJSON(w, spec)
}

func (s *Server) assignControlRoles(w http.ResponseWriter, r *http.Request) {
	flowID, ok := pathID(w, r, "flowID")
	if !ok {
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxControlRoleRequestBytes)
	defer body.Close()
	var spec studio.ControlRoleSpec
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		http.Error(w, "Invalid control-role specification.", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "Control-role request must contain one JSON value.", http.StatusBadRequest)
		return
	}
	assigned, err := s.studio.AssignControlRoles(r.Context(), flowID, spec)
	if err != nil {
		http.Error(w, studio.ValidationMessage(err), http.StatusBadRequest)
		return
	}
	writeJSON(w, assigned)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
