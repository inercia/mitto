package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// newGetSessionHandlers creates a temp store and a Handlers for exercising
// HandleGetSession.
func newGetSessionHandlers(t *testing.T) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, New(Deps{Store: store})
}

func TestHandleGetSession_NotFound(t *testing.T) {
	_, h := newGetSessionHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/20260131-120000-abcd1234", nil)
	w := httptest.NewRecorder()

	h.HandleGetSession(w, req, "20260131-120000-abcd1234", false)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleGetSession_Found(t *testing.T) {
	store, h := newGetSessionHandlers(t)

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-get",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-session-get", nil)
	w := httptest.NewRecorder()

	h.HandleGetSession(w, req, "test-session-get", false)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleGetSession_Events(t *testing.T) {
	store, h := newGetSessionHandlers(t)

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-events",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/test-session-events/events", nil)
	w := httptest.NewRecorder()

	h.HandleGetSession(w, req, "test-session-events", true)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestHandleGetSession_ParentSessionID verifies that ParentSessionID is included when getting a single session.
func TestHandleGetSession_ParentSessionID(t *testing.T) {
	store, h := newGetSessionHandlers(t)

	// Create a child session with ParentSessionID set
	childMeta := session.Metadata{
		SessionID:       "child-session-1",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child Session",
		ParentSessionID: "parent-session-1",
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/child-session-1", nil)
	w := httptest.NewRecorder()

	// Call HandleGetSession with sessionID and isEventsRequest=false
	h.HandleGetSession(w, req, "child-session-1", false)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Parse response
	var response session.Metadata
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Verify ParentSessionID is present and correct
	if response.ParentSessionID != "parent-session-1" {
		t.Errorf("ParentSessionID = %q, want %q", response.ParentSessionID, "parent-session-1")
	}
}
