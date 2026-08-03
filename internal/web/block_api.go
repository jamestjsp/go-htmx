package web

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type blockAddAPIRequest struct {
	Kind       string            `json:"kind"`
	X          int               `json:"x"`
	Y          int               `json:"y"`
	Parameters map[string]string `json:"parameters"`
}

type apiPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type apiBlockRecord struct {
	ID         int64             `json:"id"`
	FlowID     int64             `json:"flowId"`
	Kind       studio.BlockKind  `json:"kind"`
	Name       string            `json:"name"`
	Position   apiPoint          `json:"position"`
	Parameters studio.Parameters `json:"parameters"`
	Summary    string            `json:"summary"`
}

func (s *Server) addBlockAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input blockAddAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	kind, err := studio.ParseBlockKind(input.Kind)
	if err != nil {
		return apiResponse{}, err
	}
	snapshot, blockID, err := s.studio.AddBlock(r.Context(), flowID, kind, studio.Point{X: input.X, Y: input.Y})
	if err != nil {
		return apiResponse{}, err
	}
	if len(input.Parameters) != 0 {
		snapshot, err = s.studio.UpdateBlock(r.Context(), blockID, studio.BlockUpdate{
			Name:       blockName(snapshot, blockID),
			Parameters: input.Parameters,
		})
		if err != nil {
			return apiResponse{}, err
		}
	}
	for _, block := range snapshot.Blocks {
		if block.ID == blockID {
			return apiResponse{
				Status: http.StatusCreated,
				Value:  newAPIBlockRecord(block),
			}, nil
		}
	}
	return apiResponse{}, fmt.Errorf("created block %d was not present in the snapshot", blockID)
}

func parsePathInt(r *http.Request, name string) (int64, error) {
	value, err := parseInt64(r.PathValue(name))
	if err != nil {
		return 0, &studio.ValidationError{Message: fmt.Sprintf("%s must be an integer.", name)}
	}
	return value, nil
}

func parseInt64(value string) (int64, error) {
	var result int64
	if value == "" {
		return 0, errors.New("empty integer")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, errors.New("invalid integer")
		}
		result = result*10 + int64(digit-'0')
		if result < 0 {
			return 0, errors.New("integer overflow")
		}
	}
	return result, nil
}

func blockName(snapshot studio.Snapshot, blockID int64) string {
	for _, block := range snapshot.Blocks {
		if block.ID == blockID {
			return block.Name
		}
	}
	return ""
}

func newAPIBlockRecord(block studio.Block) apiBlockRecord {
	return apiBlockRecord{
		ID: block.ID, FlowID: block.FlowID, Kind: block.Kind, Name: block.Name,
		Position:   apiPoint{X: block.Position.X, Y: block.Position.Y},
		Parameters: block.Parameters, Summary: block.Summary(),
	}
}
