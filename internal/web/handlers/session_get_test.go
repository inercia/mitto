package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// ---- HandlePromptArgCache tests ----

// newPromptArgCacheHandlers creates a temp store and a Handlers (no SessionManager)
// for exercising HandlePromptArgCache.
func newPromptArgCacheHandlers(t *testing.T) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, New(Deps{Store: store})
}

func TestHandlePromptArgCache_MissingPromptParam(t *testing.T) {
	_, h := newPromptArgCacheHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/any-id/prompt-arg-cache", nil)
	w := httptest.NewRecorder()

	h.HandlePromptArgCache(w, req, "any-id")

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePromptArgCache_SessionNotFound(t *testing.T) {
	_, h := newPromptArgCacheHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/no-such-id/prompt-arg-cache?prompt=MyPrompt", nil)
	w := httptest.NewRecorder()

	h.HandlePromptArgCache(w, req, "no-such-id")

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v (body=%q)", err, w.Body.String())
	}
	if env.Error.Code != "not_found" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "not_found")
	}
}

func TestHandlePromptArgCache_ExistsNoRunningSession(t *testing.T) {
	store, h := newPromptArgCacheHandlers(t)

	// Create the session in the store (no running BackgroundSession).
	meta := session.Metadata{
		SessionID:  "pac-test-session",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/pac-test-session/prompt-arg-cache?prompt=MyPrompt", nil)
	w := httptest.NewRecorder()

	h.HandlePromptArgCache(w, req, "pac-test-session")

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp promptArgCacheResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%q)", err, w.Body.String())
	}
	if resp.Prompt != "MyPrompt" {
		t.Errorf("prompt = %q, want %q", resp.Prompt, "MyPrompt")
	}
	// cached must be an empty array, not null
	if resp.Cached == nil {
		t.Error("cached = null, want [] (empty array)")
	}
	if len(resp.Cached) != 0 {
		t.Errorf("cached = %v, want empty", resp.Cached)
	}

	// Confirm JSON encodes cached as [] not null.
	bodyStr := w.Body.String()
	const wantCachedJSON = `"cached":[]`
	if !contains(bodyStr, wantCachedJSON) {
		t.Errorf("JSON body %q does not contain %q (want array not null)", bodyStr, wantCachedJSON)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

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

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("Failed to unmarshal error envelope: %v (body=%q)", err, w.Body.String())
	}
	if env.Error.Code != "not_found" {
		t.Errorf("error.code = %q, want %q", env.Error.Code, "not_found")
	}
	if env.Error.Message != "Session not found" {
		t.Errorf("error.message = %q, want %q", env.Error.Message, "Session not found")
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
