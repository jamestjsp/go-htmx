package web

import (
	"net/http"
	"time"
)

const defaultEventLimit = 8

type eventAPIRecord struct {
	ID        int64  `json:"id"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}

func (s *Server) eventsAPI(r *http.Request) (apiResponse, error) {
	flowID, err := parsePathInt(r, "flowID")
	if err != nil {
		return apiResponse{}, err
	}
	limit := defaultEventLimit
	if requested, err := optionalQueryInt(r, "limit"); err != nil {
		return apiResponse{}, err
	} else if requested != nil {
		limit = int(*requested)
	}
	events, err := s.studio.RecentEvents(r.Context(), flowID, limit)
	if err != nil {
		return apiResponse{}, err
	}
	records := make([]eventAPIRecord, 0, len(events))
	for _, event := range events {
		records = append(records, eventAPIRecord{
			ID: event.ID, Message: event.Message,
			CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	return apiResponse{Value: records}, nil
}
