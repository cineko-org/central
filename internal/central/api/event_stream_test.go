package api

import (
	"net/http/httptest"
	"testing"
)

func TestClientEventCursorUsesLastEventID(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), "GET", "/v1/events/stream", nil)
	request.Header.Set("Last-Event-ID", "17")
	cursor, err := clientEventCursor(request)
	if err != nil || cursor != 17 {
		t.Fatalf("cursor = %d, %v", cursor, err)
	}
	request.URL.RawQuery = "after=18"
	if _, err := clientEventCursor(request); err == nil {
		t.Fatal("conflicting event cursors accepted")
	}
}
