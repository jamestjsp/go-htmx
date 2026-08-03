package web

import (
	"net/http"
	"strings"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type blockLibraryEntry struct {
	Kind        studio.BlockKind `json:"kind"`
	Label       string           `json:"label"`
	Category    string           `json:"category"`
	Description string           `json:"description"`
	Tag         string           `json:"tag"`
	HasInput    bool             `json:"hasInput"`
	HasOutput   bool             `json:"hasOutput"`
}

func (s *Server) blockLibraryAPI(_ *http.Request) (apiResponse, error) {
	definitions := studio.BlockLibrary()
	entries := make([]blockLibraryEntry, 0, len(definitions))
	for _, definition := range definitions {
		entries = append(entries, blockLibraryEntry{
			Kind:        definition.Kind,
			Label:       definition.Label,
			Category:    definition.Category,
			Description: definition.Description,
			Tag:         definition.Tag,
			HasInput:    definition.HasInput(),
			HasOutput:   definition.HasOutput(),
		})
	}
	return apiResponse{Value: entries}, nil
}

func (s *Server) blockSchemaAPI(r *http.Request) (apiResponse, error) {
	if blockID, err := parseInt64(r.PathValue("kind")); err == nil && blockID > 0 {
		return s.blockDetailAPI(r, blockID)
	}
	blockKind := studio.BlockKind(strings.ToLower(strings.TrimSpace(r.PathValue("kind"))))
	schema, ok := blockKind.Schema()
	if !ok {
		return apiResponse{}, studio.ErrNotFound
	}
	return apiResponse{Value: schema}, nil
}
