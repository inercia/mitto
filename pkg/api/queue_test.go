package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_MoveQueueMessage_HappyPath(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/queue/msg-1/move", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":[{"id":"msg-2","message":"a","queued_at":"2026-01-01T00:00:00Z"},{"id":"msg-1","message":"b","queued_at":"2026-01-01T00:00:01Z"}],"count":2}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	result, err := c.MoveQueueMessage("sess-1", "msg-1", "up")
	if err != nil {
		t.Fatalf("MoveQueueMessage: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/mitto/api/sessions/sess-1/queue/msg-1/move" {
		t.Errorf("path = %q, unexpected", gotPath)
	}
	if gotBody["direction"] != "up" {
		t.Errorf("request body direction = %v, want up", gotBody["direction"])
	}
	if result.Count != 2 || len(result.Messages) != 2 || result.Messages[0].ID != "msg-2" {
		t.Errorf("QueueListResponse = %+v, unexpected", result)
	}
}

func TestClient_MoveQueueMessage_404_ReturnsTypedNotFoundError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/mitto/api/sessions/sess-1/queue/missing/move", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL)
	_, err := c.MoveQueueMessage("sess-1", "missing", "down")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false, want true; err = %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("errors.As failed to extract *APIError")
	}
	if apiErr.Details["message_id"] != "missing" {
		t.Errorf("Details[message_id] = %v, want %q", apiErr.Details["message_id"], "missing")
	}
}
