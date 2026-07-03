package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

func TestHandleRunningSessions_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sm := conversation.NewSessionManager("", "", false, nil)

	h := New(Deps{SessionManager: sm, Store: store})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	h.HandleRunningSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	// Response is a RunningSessionsResponse object
	var response RunningSessionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response.TotalRunning != 0 {
		t.Errorf("TotalRunning = %d, want 0", response.TotalRunning)
	}

	if len(response.Sessions) != 0 {
		t.Errorf("Sessions count = %d, want 0", len(response.Sessions))
	}
}

func TestHandleRunningSessions_StoreNil(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	h := New(Deps{SessionManager: sm}) // Store deliberately nil

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	h.HandleRunningSessions(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Failed to unmarshal envelope: %v", err)
	}
	if env.Error.Code != "server_error" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "server_error")
	}
	if env.Error.Message != "Session store not available" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "Session store not available")
	}
}

func TestHandleRunningSessions_MethodNotAllowed(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	h := New(Deps{SessionManager: sm})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	h.HandleRunningSessions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRunningSessions_WithSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "20260131-120030-abcd1234",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm := conversation.NewSessionManager("", "", false, nil)
	// Add a mock running session
	sm.AddSessionForTest(conversation.NewMinimalBackgroundSession("20260131-120030-abcd1234", "/tmp", ""))

	h := New(Deps{SessionManager: sm, Store: store})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/running", nil)
	w := httptest.NewRecorder()

	h.HandleRunningSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
}
