package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// =============================================================================
// findSingletonCandidate (mitto-4mb.3) — scan/decision logic in isolation
// =============================================================================

func TestFindSingletonCandidate_NoExistingSession(t *testing.T) {
	if _, found := findSingletonCandidate(nil, "/work", "my-prompt"); found {
		t.Error("expected no candidate for empty metadata list")
	}
}

func TestFindSingletonCandidate_OneMatchingNonArchivedSession(t *testing.T) {
	metas := []session.Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "my-prompt"},
	}
	id, found := findSingletonCandidate(metas, "/work", "my-prompt")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "s1" {
		t.Errorf("SessionID = %q, want %q", id, "s1")
	}
}

func TestFindSingletonCandidate_ArchivedMatchIgnored(t *testing.T) {
	metas := []session.Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "my-prompt", Archived: true},
	}
	if _, found := findSingletonCandidate(metas, "/work", "my-prompt"); found {
		t.Error("archived session should not be a candidate")
	}
}

func TestFindSingletonCandidate_DifferentWorkingDirIgnored(t *testing.T) {
	metas := []session.Metadata{
		{SessionID: "s1", WorkingDir: "/other", OriginPromptName: "my-prompt"},
	}
	if _, found := findSingletonCandidate(metas, "/work", "my-prompt"); found {
		t.Error("session in a different working dir should not be a candidate")
	}
}

func TestFindSingletonCandidate_DifferentOriginPromptNameIgnored(t *testing.T) {
	metas := []session.Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "other-prompt"},
	}
	if _, found := findSingletonCandidate(metas, "/work", "my-prompt"); found {
		t.Error("session from a different prompt should not be a candidate")
	}
}

func TestFindSingletonCandidate_CaseInsensitivePromptMatch(t *testing.T) {
	metas := []session.Metadata{
		{SessionID: "s1", WorkingDir: "/work", OriginPromptName: "My-Prompt"},
	}
	id, found := findSingletonCandidate(metas, "/work", "my-prompt")
	if !found || id != "s1" {
		t.Errorf("expected case-insensitive match, got found=%v id=%q", found, id)
	}
}

func TestFindSingletonCandidate_MultipleMatches_MostRecentlyUpdatedWins(t *testing.T) {
	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	metas := []session.Metadata{
		{SessionID: "old", WorkingDir: "/work", OriginPromptName: "my-prompt", UpdatedAt: older},
		{SessionID: "new", WorkingDir: "/work", OriginPromptName: "my-prompt", UpdatedAt: newer},
	}
	id, found := findSingletonCandidate(metas, "/work", "my-prompt")
	if !found {
		t.Fatal("expected a candidate")
	}
	if id != "new" {
		t.Errorf("SessionID = %q, want %q (most recently updated)", id, "new")
	}
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
