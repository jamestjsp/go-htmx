package studio

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRecentEventsRejectsExcessiveLimitBeforeDatabaseAccess(t *testing.T) {
	service := openTestStudio(t, ":memory:")
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := service.RecentEvents(context.Background(), 1, maxEventLimit+1)
	var validation *ValidationError
	if !errors.As(err, &validation) || !strings.Contains(validation.Message, "event limit") {
		t.Fatalf("excessive event limit error = %v, want validation before database access", err)
	}
}
