package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// newDeleteHandlers creates a temp store and a Handlers wired with a no-op
// broadcast closure, for exercising HandleDeleteSession.
func newDeleteHandlers(t *testing.T) (*session.Store, *Handlers) {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	h := New(Deps{
		Store:                   store,
		SessionManager:          conversation.NewSessionManager("", "", false, nil),
		BroadcastSessionDeleted: func(string) {},
	})
	return store, h
}

func TestHandleDeleteSession_NotFound(t *testing.T) {
	_, h := newDeleteHandlers(t)

	w := httptest.NewRecorder()

	h.HandleDeleteSession(w, "nonexistent")

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

func TestHandleDeleteSession_Success(t *testing.T) {
	store, h := newDeleteHandlers(t)

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-delete",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	w := httptest.NewRecorder()

	h.HandleDeleteSession(w, "test-session-delete")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

// TestHandleDeleteSession_ClearsParentReferences verifies that deleting a parent session
// via the API clears the ParentSessionID field in all child sessions.
func TestHandleDeleteSession_ClearsParentReferences(t *testing.T) {
	store, h := newDeleteHandlers(t)

	// Create a parent session
	parentMeta := session.Metadata{
		SessionID:  "parent-api-test",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Parent Session",
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Create child sessions
	child1Meta := session.Metadata{
		SessionID:       "child-api-1",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child 1",
		ParentSessionID: "parent-api-test",
	}
	if err := store.Create(child1Meta); err != nil {
		t.Fatalf("Create child1 failed: %v", err)
	}

	child2Meta := session.Metadata{
		SessionID:       "child-api-2",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child 2",
		ParentSessionID: "parent-api-test",
	}
	if err := store.Create(child2Meta); err != nil {
		t.Fatalf("Create child2 failed: %v", err)
	}

	// Delete the parent session via API
	w := httptest.NewRecorder()
	h.HandleDeleteSession(w, "parent-api-test")

	if w.Code != http.StatusNoContent {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNoContent)
	}

	// Verify parent is deleted
	if store.Exists("parent-api-test") {
		t.Error("Parent session still exists after deletion")
	}

	// Verify child sessions are cascade-deleted along with the parent
	if store.Exists("child-api-1") {
		t.Error("Child 1 still exists after parent deletion — expected cascade delete")
	}
	if store.Exists("child-api-2") {
		t.Error("Child 2 still exists after parent deletion — expected cascade delete")
	}
}

// TestHandleDeleteSession_FiresApplyOnCloseProcessors verifies that a DELETE
// on a leaf session invokes the ApplyOnCloseProcessors dep with the "deleted"
// close-trigger reason (mitto-4is).
func TestHandleDeleteSession_FiresApplyOnCloseProcessors(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const sid = "sess-delete-fires-close"
	if err := store.Create(session.Metadata{
		SessionID:  sid,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var mu sync.Mutex
	var calls []struct{ id, reason string }
	h := New(Deps{
		Store:                   store,
		SessionManager:          conversation.NewSessionManager("", "", false, nil),
		BroadcastSessionDeleted: func(string) {},
		ApplyOnCloseProcessors: func(sessionID, reason string) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, struct{ id, reason string }{sessionID, reason})
		},
	})

	w := httptest.NewRecorder()
	h.HandleDeleteSession(w, sid)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Status = %d, want %d. Body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("ApplyOnCloseProcessors call count = %d, want 1 (calls=%+v)", len(calls), calls)
	}
	if calls[0].id != sid {
		t.Errorf("sessionID = %q, want %q", calls[0].id, sid)
	}
	if calls[0].reason != "deleted" {
		t.Errorf("reason = %q, want %q", calls[0].reason, "deleted")
	}
}

// TestHandleDeleteSession_FiresApplyOnCloseProcessorsForChildren verifies that
// deleting a parent session fires the close pipeline for every cascaded child
// with the "parent_deleted" reason (mitto-4is).
func TestHandleDeleteSession_FiresApplyOnCloseProcessorsForChildren(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	const parentID = "parent-delete-close"
	if err := store.Create(session.Metadata{
		SessionID:  parentID,
		ACPServer:  "test-server",
		WorkingDir: t.TempDir(),
		Name:       "Parent",
	}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}
	childIDs := []string{"child-close-1", "child-close-2"}
	for _, cid := range childIDs {
		if err := store.Create(session.Metadata{
			SessionID:       cid,
			ACPServer:       "test-server",
			WorkingDir:      "/tmp",
			ParentSessionID: parentID,
		}); err != nil {
			t.Fatalf("Create child %s failed: %v", cid, err)
		}
	}

	var mu sync.Mutex
	calls := map[string]string{}
	h := New(Deps{
		Store:                   store,
		SessionManager:          conversation.NewSessionManager("", "", false, nil),
		BroadcastSessionDeleted: func(string) {},
		ApplyOnCloseProcessors: func(sessionID, reason string) {
			mu.Lock()
			defer mu.Unlock()
			calls[sessionID] = reason
		},
	})

	w := httptest.NewRecorder()
	h.HandleDeleteSession(w, parentID)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Status = %d, want %d. Body: %s", w.Code, http.StatusNoContent, w.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1+len(childIDs) {
		t.Fatalf("ApplyOnCloseProcessors call count = %d, want %d (calls=%+v)", len(calls), 1+len(childIDs), calls)
	}
	if calls[parentID] != "deleted" {
		t.Errorf("parent reason = %q, want %q", calls[parentID], "deleted")
	}
	for _, cid := range childIDs {
		if calls[cid] != "parent_deleted" {
			t.Errorf("child %s reason = %q, want %q", cid, calls[cid], "parent_deleted")
		}
	}
}
