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

// TestReuseSingletonSession_NotLoadedIdle_EnqueuesWithoutDispatch verifies that
// reusing a singleton session that is not currently loaded in memory (no live
// BackgroundSession) and has an empty queue enqueues the prompt directly
// (no SessionManager → no dispatch attempted) and responds with reused:true.
func TestReuseSingletonSession_NotLoadedIdle_EnqueuesWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "20260201-130000-singleton1"
	if err := store.Create(session.Metadata{SessionID: sessionID, Status: "active"}); err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	h := New(Deps{Store: store})

	w := httptest.NewRecorder()
	h.reuseSingletonSession(w, sessionID, "my-prompt", map[string]string{"X": "y"})

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if resp["session_id"] != sessionID {
		t.Errorf("session_id = %v, want %q", resp["session_id"], sessionID)
	}
	if resp["reused"] != true {
		t.Errorf("reused = %v, want true", resp["reused"])
	}

	queue := store.Queue(sessionID)
	messages, err := queue.List()
	if err != nil {
		t.Fatalf("Queue.List failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	if messages[0].PromptName != "my-prompt" {
		t.Errorf("PromptName = %q, want %q", messages[0].PromptName, "my-prompt")
	}
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
