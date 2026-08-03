package studio

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RecentEvents returns the model activity feed newest first.
func (s *Studio) RecentEvents(ctx context.Context, flowID int64, limit int) ([]Event, error) {
	if limit <= 0 {
		return nil, invalid("event limit must be positive")
	}

	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM flows WHERE id = ?", flowID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, message, created_at FROM events
		WHERE flow_id = ? ORDER BY id DESC LIMIT ?`, flowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]Event, 0, limit)
	for rows.Next() {
		var event Event
		var timestamp string
		if err := rows.Scan(&event.ID, &event.Message, &timestamp); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, timestamp)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
