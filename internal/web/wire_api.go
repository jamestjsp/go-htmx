package web

import (
	"context"
	"net/http"

	"github.com/jamestjsp/process-lab/internal/studio"
)

type wireConnectAPIRequest struct {
	SourceID   int64 `json:"sourceId"`
	SourcePort int   `json:"sourcePort"`
	TargetID   int64 `json:"targetId"`
	TargetPort int   `json:"targetPort"`
}

type apiWireRecord struct {
	ID             int64    `json:"id"`
	FlowID         int64    `json:"flowId"`
	SourceID       int64    `json:"sourceId"`
	SourceName     string   `json:"sourceName"`
	SourcePort     int      `json:"sourcePort"`
	SourceWidth    int      `json:"sourceWidth"`
	SourceChannels []string `json:"sourceChannels"`
	TargetID       int64    `json:"targetId"`
	TargetName     string   `json:"targetName"`
	TargetPort     int      `json:"targetPort"`
	TargetWidth    int      `json:"targetWidth"`
	TargetChannels []string `json:"targetChannels"`
}

type wireMutationAPIRecord struct {
	FlowID  int64 `json:"flowId"`
	Removed int   `json:"removed"`
}

func (s *Server) wireListAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.Snapshot(r.Context(), flowID)
	if err != nil {
		return apiResponse{}, err
	}
	records, err := wireRecords(snapshot)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: records}, nil
}

func (s *Server) wireConnectAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	var input wireConnectAPIRequest
	if err := decodeAPIJSON(r, &input); err != nil {
		return apiResponse{}, err
	}
	snapshot, err := s.studio.Connect(r.Context(), flowID, studio.Wire{
		SourceID: input.SourceID, SourcePort: input.SourcePort,
		TargetID: input.TargetID, TargetPort: input.TargetPort,
	})
	if err != nil {
		return apiResponse{}, err
	}
	for _, connection := range snapshot.Connections {
		if connection.SourceID == input.SourceID && connection.SourcePort == input.SourcePort &&
			connection.TargetID == input.TargetID && connection.TargetPort == input.TargetPort {
			record, err := newAPIWireRecord(snapshot, connection)
			if err != nil {
				return apiResponse{}, err
			}
			return apiResponse{Status: http.StatusCreated, Value: record}, nil
		}
	}
	return apiResponse{}, studio.ErrNotFound
}

func (s *Server) wireDisconnectAPI(r *http.Request) (apiResponse, error) {
	connectionID, err := parsePathInt(r, "connectionID")
	if err != nil {
		return apiResponse{}, err
	}
	before, _, err := s.snapshotConnectionByID(r.Context(), connectionID)
	if err != nil {
		return apiResponse{}, err
	}
	if _, err := s.studio.Disconnect(r.Context(), connectionID); err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: wireMutationAPIRecord{FlowID: before.Flow.ID, Removed: 1}}, nil
}

func (s *Server) wireDisconnectBlockAPI(r *http.Request) (apiResponse, error) {
	blockID, err := parsePathInt(r, "blockID")
	if err != nil {
		return apiResponse{}, err
	}
	before, _, err := s.snapshotBlockByID(r.Context(), blockID)
	if err != nil {
		return apiResponse{}, err
	}
	after, err := s.studio.DisconnectBlock(r.Context(), blockID)
	if err != nil {
		return apiResponse{}, err
	}
	return apiResponse{Value: wireMutationAPIRecord{
		FlowID: before.Flow.ID, Removed: len(before.Connections) - len(after.Connections),
	}}, nil
}

func (s *Server) snapshotConnectionByID(ctx context.Context, connectionID int64) (studio.Snapshot, studio.Connection, error) {
	register, err := s.studio.Register(ctx)
	if err != nil {
		return studio.Snapshot{}, studio.Connection{}, err
	}
	for _, entry := range register.Projects {
		for _, flow := range entry.Flows {
			snapshot, err := s.studio.Snapshot(ctx, flow.ID)
			if err != nil {
				return studio.Snapshot{}, studio.Connection{}, err
			}
			for _, connection := range snapshot.Connections {
				if connection.ID == connectionID {
					return snapshot, connection, nil
				}
			}
		}
	}
	return studio.Snapshot{}, studio.Connection{}, studio.ErrNotFound
}

func wireRecords(snapshot studio.Snapshot) ([]apiWireRecord, error) {
	records := make([]apiWireRecord, 0, len(snapshot.Connections))
	for _, connection := range snapshot.Connections {
		record, err := newAPIWireRecord(snapshot, connection)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func newAPIWireRecord(snapshot studio.Snapshot, connection studio.Connection) (apiWireRecord, error) {
	blocks := make(map[int64]studio.Block, len(snapshot.Blocks))
	for _, block := range snapshot.Blocks {
		blocks[block.ID] = block
	}
	source, sourceOK := blocks[connection.SourceID]
	target, targetOK := blocks[connection.TargetID]
	if !sourceOK || !targetOK {
		return apiWireRecord{}, studio.ErrNotFound
	}
	sourcePort, sourcePortOK := source.OutputPort(connection.SourcePort)
	targetPort, targetPortOK := target.InputPort(connection.TargetPort)
	if !sourcePortOK || !targetPortOK {
		return apiWireRecord{}, &studio.ValidationError{Message: "stored connection refers to a missing port."}
	}
	return apiWireRecord{
		ID: connection.ID, FlowID: connection.FlowID,
		SourceID: connection.SourceID, SourceName: source.Name, SourcePort: connection.SourcePort,
		SourceWidth: sourcePort.Width, SourceChannels: append([]string(nil), sourcePort.Channels...),
		TargetID: connection.TargetID, TargetName: target.Name, TargetPort: connection.TargetPort,
		TargetWidth: targetPort.Width, TargetChannels: append([]string(nil), targetPort.Channels...),
	}, nil
}
