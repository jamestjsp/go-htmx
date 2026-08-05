package web

import (
	"context"
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

type apiPortRecord struct {
	Width    int      `json:"width"`
	Channels []string `json:"channels"`
}

type apiBlockRecord struct {
	ID              int64             `json:"id"`
	FlowID          int64             `json:"flowId"`
	Kind            studio.BlockKind  `json:"kind"`
	Name            string            `json:"name"`
	Position        apiPoint          `json:"position"`
	Parameters      studio.Parameters `json:"parameters"`
	ParameterValues map[string]string `json:"parameterValues"`
	Inputs          []apiPortRecord   `json:"inputs"`
	Outputs         []apiPortRecord   `json:"outputs"`
	Summary         string            `json:"summary"`
}

type blockUpdateAPIRequest struct {
	Name       string            `json:"name"`
	Parameters map[string]string `json:"parameters"`
}

type blockPositionAPIRequest struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type blockMoveAPIRequest struct {
	BlockID int64 `json:"blockId"`
	X       int   `json:"x"`
	Y       int   `json:"y"`
}

type blockMovesAPIRequest struct {
	Moves []blockMoveAPIRequest `json:"moves"`
}

type blockIDsAPIRequest struct {
	BlockIDs []int64 `json:"blockIds"`
}

type blockBatchAPIRecord struct {
	FlowID int64            `json:"flowId"`
	Blocks []apiBlockRecord `json:"blocks"`
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
	position := studio.Point{X: input.X, Y: input.Y}
	var snapshot studio.Snapshot
	var blockID int64
	if len(input.Parameters) == 0 {
		snapshot, blockID, err = s.studio.AddBlock(r.Context(), flowID, kind, position)
	} else {
		snapshot, blockID, err = s.studio.AddConfiguredBlock(
			r.Context(), flowID, kind, position, input.Parameters,
		)
	}
	if err != nil {
		return apiResponse{}, err
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

func (s *Server) blockListAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.Snapshot(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: blockRecords(snapshot.Blocks)}, nil
}

func (s *Server) blockDetailAPI(r *http.Request, blockID int64) (apiResponse, error) {
	_, block, err := s.snapshotBlockByID(r.Context(), blockID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: newAPIBlockRecord(block)}, nil
}

func (s *Server) updateBlockAPI(r *http.Request) (apiResponse, error) {
	blockID, err := parsePathInt(r, "blockID")
	if err != nil {
		return apiResponse{}, err
	}
	var input blockUpdateAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.UpdateBlock(r.Context(), blockID, studio.BlockUpdate{
		Name: input.Name, Parameters: input.Parameters,
	})
	if err != nil {
		return apiResponse{}, err
	}
	for _, block := range snapshot.Blocks {
		if block.ID == blockID {
			return apiResponse{Value: newAPIBlockRecord(block)}, nil
		}
	}
	return apiResponse{}, studio.ErrNotFound
}

func (s *Server) moveBlockAPI(r *http.Request) (apiResponse, error) {
	blockID, err := parsePathInt(r, "blockID")
	if err != nil {
		return apiResponse{}, err
	}
	before, block, err := s.snapshotBlockByID(r.Context(), blockID)
	if err != nil {
		return apiResponse{}, err
	}
	var input blockPositionAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	if err := s.studio.MoveBlock(r.Context(), blockID, studio.Point{X: input.X, Y: input.Y}); err != nil {
		return apiResponse{}, err
	}
	after, err := s.studio.Snapshot(r.Context(), before.Flow.ID)
	if err != nil {
		return apiResponse{}, err
	}
	for _, moved := range after.Blocks {
		if moved.ID == block.ID {
			return apiResponse{Value: newAPIBlockRecord(moved)}, nil
		}
	}
	return apiResponse{}, studio.ErrNotFound
}

func (s *Server) moveBlocksAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input blockMovesAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	moves := make([]studio.BlockMove, len(input.Moves))
	for index, move := range input.Moves {
		moves[index] = studio.BlockMove{
			BlockID: move.BlockID, Position: studio.Point{X: move.X, Y: move.Y},
		}
	}
	if err := s.studio.MoveBlocks(r.Context(), flowID, moves); err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.Snapshot(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: blockBatchAPIRecord{FlowID: flowID, Blocks: blockRecords(snapshot.Blocks)}}, nil
}

func (s *Server) deleteBlockAPI(r *http.Request) (apiResponse, error) {
	blockID, err := parsePathInt(r, "blockID")
	if err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.DeleteBlock(r.Context(), blockID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: blockBatchAPIRecord{
		FlowID: snapshot.Flow.ID, Blocks: blockRecords(snapshot.Blocks),
	}}, nil
}

func (s *Server) deleteBlocksAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input blockIDsAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.DeleteBlocks(r.Context(), flowID, input.BlockIDs)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: blockBatchAPIRecord{
		FlowID: snapshot.Flow.ID, Blocks: blockRecords(snapshot.Blocks),
	}}, nil
}

func (s *Server) duplicateBlocksAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input blockIDsAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	before, err := s.studio.Snapshot(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.DuplicateBlocks(r.Context(), flowID, input.BlockIDs)
	if err != nil {
		return apiResponse{}, err
	}
	known := make(map[int64]bool, len(before.Blocks))
	for _, block := range before.Blocks {
		known[block.ID] = true
	}
	copies := make([]studio.Block, 0, len(snapshot.Blocks)-len(before.Blocks))
	for _, block := range snapshot.Blocks {
		if !known[block.ID] {
			copies = append(copies, block)
		}
	}
	return apiResponse{Value: blockBatchAPIRecord{
		FlowID: flowID, Blocks: blockRecords(copies),
	}}, nil
}

func (s *Server) snapshotBlockByID(ctx context.Context, blockID int64) (studio.Snapshot, studio.Block, error) {
	register, err := s.studio.Register(ctx)
	if err != nil {
		return studio.Snapshot{}, studio.Block{}, err
	}
	for _, entry := range register.Projects {
		for _, flow := range entry.Flows {
			snapshot, err := s.studio.Snapshot(ctx, flow.ID)
			if err != nil {
				return studio.Snapshot{}, studio.Block{}, err
			}
			for _, block := range snapshot.Blocks {
				if block.ID == blockID {
					return snapshot, block, nil
				}
			}
		}
	}
	return studio.Snapshot{}, studio.Block{}, studio.ErrNotFound
}

func blockRecords(blocks []studio.Block) []apiBlockRecord {
	records := make([]apiBlockRecord, 0, len(blocks))
	for _, block := range blocks {
		records = append(records, newAPIBlockRecord(block))
	}
	return records
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

func newAPIBlockRecord(block studio.Block) apiBlockRecord {
	values := make(map[string]string)
	for _, field := range block.EditorFields() {
		values[field.Name] = field.Value
	}
	inputs := make([]apiPortRecord, 0)
	for index := 0; ; index++ {
		port, ok := block.InputPort(index)
		if !ok {
			break
		}
		inputs = append(inputs, apiPortRecord{
			Width: port.Width, Channels: append([]string(nil), port.Channels...),
		})
	}
	outputs := make([]apiPortRecord, 0)
	for index := 0; ; index++ {
		port, ok := block.OutputPort(index)
		if !ok {
			break
		}
		outputs = append(outputs, apiPortRecord{
			Width: port.Width, Channels: append([]string(nil), port.Channels...),
		})
	}
	return apiBlockRecord{
		ID: block.ID, FlowID: block.FlowID, Kind: block.Kind, Name: block.Name,
		Position:   apiPoint{X: block.Position.X, Y: block.Position.Y},
		Parameters: block.Parameters, ParameterValues: values,
		Inputs: inputs, Outputs: outputs, Summary: block.Summary(),
	}
}
