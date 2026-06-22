package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// setupQueueTestHandlers creates a test Handlers backed by a session store with a
// single test session, for exercising the queue REST handlers.
func setupQueueTestHandlers(t *testing.T) (*session.Store, *Handlers, string) {
	t.Helper()

	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "20260201-120000-test1234"
	if err := store.Create(session.Metadata{SessionID: sessionID, Status: "active"}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	h := New(Deps{Store: store, APIPrefix: "/mitto"})
	return store, h, sessionID
}

func TestHandleSessionQueue_List_Empty(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodGet, "/mitto/api/sessions/"+sessionID+"/queue", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp QueueListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("Count = %d, want 0", resp.Count)
	}
	if len(resp.Messages) != 0 {
		t.Errorf("Messages = %d, want 0", len(resp.Messages))
	}

	queue.Delete()
}

func TestHandleSessionQueue_Add(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	body := `{"message": "Test message", "image_ids": ["img1", "img2"]}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusCreated {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusCreated)
	}

	var msg session.QueuedMessage
	if err := json.NewDecoder(w.Body).Decode(&msg); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if msg.ID == "" {
		t.Error("Message ID should not be empty")
	}
	if msg.Message != "Test message" {
		t.Errorf("Message = %q, want %q", msg.Message, "Test message")
	}
	if len(msg.ImageIDs) != 2 {
		t.Errorf("ImageIDs = %v, want 2 items", msg.ImageIDs)
	}

	queue.Delete()
}

func TestHandleSessionQueue_Add_EmptyMessage(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	body := `{"message": ""}`
	req := httptest.NewRequest(http.MethodPost, "/mitto/api/sessions/"+sessionID+"/queue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	queue.Delete()
}

func TestHandleSessionQueue_Delete_Message(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	msg, _ := queue.Add("Test", nil, nil, "", nil, 0, nil, "")

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue/"+msg.ID, nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "/"+msg.ID)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	if _, err := queue.Get(msg.ID); err != session.ErrMessageNotFound {
		t.Error("Message should have been deleted")
	}

	queue.Delete()
}

func TestHandleSessionQueue_Delete_NotFound(t *testing.T) {
	store, h, sessionID := setupQueueTestHandlers(t)
	queue := store.Queue(sessionID)

	req := httptest.NewRequest(http.MethodDelete, "/mitto/api/sessions/"+sessionID+"/queue/nonexistent", nil)
	w := httptest.NewRecorder()

	h.HandleSessionQueue(w, req, sessionID, "/nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}

	queue.Delete()
}
