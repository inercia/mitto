package handlers

import (
	"net/http"
	"net/http/httptest"
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
