package session

import (
	"errors"
	"sync"
	"testing"
)

// TestDelete_CascadeDeletesChildren verifies that deleting a parent session
// cascade-deletes all child sessions.
func TestDelete_CascadeDeletesChildren(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a parent session
	parentMeta := Metadata{
		SessionID:  "parent-session-1",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Parent Session",
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	// Create multiple child sessions
	child1Meta := Metadata{
		SessionID:       "child-session-1",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child Session 1",
		ParentSessionID: "parent-session-1",
	}
	if err := store.Create(child1Meta); err != nil {
		t.Fatalf("Create child1 failed: %v", err)
	}

	child2Meta := Metadata{
		SessionID:       "child-session-2",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child Session 2",
		ParentSessionID: "parent-session-1",
	}
	if err := store.Create(child2Meta); err != nil {
		t.Fatalf("Create child2 failed: %v", err)
	}

	// Create an unrelated session (no parent)
	unrelatedMeta := Metadata{
		SessionID:  "unrelated-session",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Unrelated Session",
	}
	if err := store.Create(unrelatedMeta); err != nil {
		t.Fatalf("Create unrelated failed: %v", err)
	}

	// Delete the parent session
	if err := store.Delete("parent-session-1"); err != nil {
		t.Fatalf("Delete parent failed: %v", err)
	}

	// Verify parent is deleted
	if store.Exists("parent-session-1") {
		t.Error("Parent session still exists after deletion")
	}

	// Verify child sessions are cascade-deleted
	if store.Exists("child-session-1") {
		t.Error("Child 1 still exists after parent deletion — expected cascade delete")
	}
	if store.Exists("child-session-2") {
		t.Error("Child 2 still exists after parent deletion — expected cascade delete")
	}

	// Verify unrelated session is unchanged
	if !store.Exists("unrelated-session") {
		t.Error("Unrelated session was deleted — should not have been affected")
	}
}

// TestDelete_NoChildSessions verifies that deleting a session without children
// works correctly and doesn't cause errors.
func TestDelete_NoChildSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session without children
	meta := Metadata{
		SessionID:  "standalone-session",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Standalone Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Delete the session
	if err := store.Delete("standalone-session"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's deleted
	if store.Exists("standalone-session") {
		t.Error("Session still exists after deletion")
	}
}

// TestDelete_NestedParentChild verifies that deleting a middle-level parent
// in a three-level hierarchy cascade-deletes its child (grandchild of root).
func TestDelete_NestedParentChild(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a three-level hierarchy:
	// grandparent -> parent -> child

	grandparentMeta := Metadata{
		SessionID:  "grandparent",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Grandparent",
	}
	if err := store.Create(grandparentMeta); err != nil {
		t.Fatalf("Create grandparent failed: %v", err)
	}

	parentMeta := Metadata{
		SessionID:       "parent",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Parent",
		ParentSessionID: "grandparent",
	}
	if err := store.Create(parentMeta); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}

	childMeta := Metadata{
		SessionID:       "child",
		ACPServer:       "test-server",
		WorkingDir:      "/tmp",
		Name:            "Child",
		ParentSessionID: "parent",
	}
	if err := store.Create(childMeta); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	// Delete the middle parent
	if err := store.Delete("parent"); err != nil {
		t.Fatalf("Delete parent failed: %v", err)
	}

	// Verify parent is deleted
	if store.Exists("parent") {
		t.Error("Parent session still exists after deletion")
	}

	// Verify child is cascade-deleted along with parent
	if store.Exists("child") {
		t.Error("Child still exists after parent deletion — expected cascade delete")
	}

	// Verify grandparent is unchanged
	if !store.Exists("grandparent") {
		t.Error("Grandparent was deleted — should not have been affected")
	}
}

// observedDelete records a single delete-observer invocation for assertions below.
type observedDelete struct {
	ID       string
	ParentID string
}

// TestDelete_ObserverFiresForPlainDelete verifies that deleting a top-level
// session (no parent, no children) fires the delete observer exactly once
// with the session's ID and an empty parent ID.
func TestDelete_ObserverFiresForPlainDelete(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	meta := Metadata{
		SessionID:  "standalone-session",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Standalone Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var mu sync.Mutex
	var observed []observedDelete
	store.SetDeleteObserver(func(sessionID, parentSessionID string) {
		mu.Lock()
		defer mu.Unlock()
		observed = append(observed, observedDelete{ID: sessionID, ParentID: parentSessionID})
	})

	if err := store.Delete("standalone-session"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 {
		t.Fatalf("expected exactly 1 observer invocation, got %d: %+v", len(observed), observed)
	}
	if observed[0] != (observedDelete{ID: "standalone-session", ParentID: ""}) {
		t.Errorf("unexpected observed delete: %+v", observed[0])
	}
}

// TestDelete_ObserverFiresForCascade verifies that deleting a middle-level
// parent in a three-level hierarchy fires the observer once per removed
// session (cascade descendants first, then the target), each paired with its
// own immediate parent ID at the time of removal.
func TestDelete_ObserverFiresForCascade(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// grandparent -> parent -> child
	if err := store.Create(Metadata{SessionID: "grandparent", ACPServer: "test-server", WorkingDir: "/tmp", Name: "Grandparent"}); err != nil {
		t.Fatalf("Create grandparent failed: %v", err)
	}
	if err := store.Create(Metadata{SessionID: "parent", ACPServer: "test-server", WorkingDir: "/tmp", Name: "Parent", ParentSessionID: "grandparent"}); err != nil {
		t.Fatalf("Create parent failed: %v", err)
	}
	if err := store.Create(Metadata{SessionID: "child", ACPServer: "test-server", WorkingDir: "/tmp", Name: "Child", ParentSessionID: "parent"}); err != nil {
		t.Fatalf("Create child failed: %v", err)
	}

	var mu sync.Mutex
	var observed []observedDelete
	store.SetDeleteObserver(func(sessionID, parentSessionID string) {
		mu.Lock()
		defer mu.Unlock()
		observed = append(observed, observedDelete{ID: sessionID, ParentID: parentSessionID})
	})

	if err := store.Delete("parent"); err != nil {
		t.Fatalf("Delete parent failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []observedDelete{
		{ID: "child", ParentID: "parent"},
		{ID: "parent", ParentID: "grandparent"},
	}
	if len(observed) != len(want) {
		t.Fatalf("expected %d observer invocations, got %d: %+v", len(want), len(observed), observed)
	}
	for i, w := range want {
		if observed[i] != w {
			t.Errorf("observed[%d] = %+v, want %+v (full: %+v)", i, observed[i], w, observed)
		}
	}
}

// TestDelete_ObserverNotInvokedOnSessionNotFound verifies that a failed
// Delete (session does not exist) does not invoke the delete observer.
func TestDelete_ObserverNotInvokedOnSessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	called := false
	store.SetDeleteObserver(func(sessionID, parentSessionID string) {
		called = true
	})

	err = store.Delete("does-not-exist")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
	if called {
		t.Error("observer should not be invoked when Delete fails with ErrSessionNotFound")
	}
}

// TestDelete_ObserverNotInvokedOnStoreClosed verifies that a failed Delete
// (store closed) does not invoke the delete observer.
func TestDelete_ObserverNotInvokedOnStoreClosed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	if err := store.Create(Metadata{SessionID: "session-a", ACPServer: "test-server", WorkingDir: "/tmp", Name: "A"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	called := false
	store.SetDeleteObserver(func(sessionID, parentSessionID string) {
		called = true
	})

	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	err = store.Delete("session-a")
	if !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("expected ErrStoreClosed, got %v", err)
	}
	if called {
		t.Error("observer should not be invoked when Delete fails with ErrStoreClosed")
	}
}

// TestDelete_NilObserverIsNoop verifies that clearing the observer (passing
// nil to SetDeleteObserver) makes Delete a silent no-op with respect to
// notification, and does not panic.
func TestDelete_NilObserverIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(Metadata{SessionID: "session-a", ACPServer: "test-server", WorkingDir: "/tmp", Name: "A"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Set then clear, to prove clearing actually takes effect (not just
	// never having been set).
	store.SetDeleteObserver(func(sessionID, parentSessionID string) {
		t.Error("observer should have been cleared and must not fire")
	})
	store.SetDeleteObserver(nil)

	if err := store.Delete("session-a"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

// TestDelete_ObserverReentrantCallDoesNotDeadlock verifies that an observer
// which calls back into other Store methods (which take s.mu) does not
// deadlock, proving the observer is invoked after Delete releases the lock.
func TestDelete_ObserverReentrantCallDoesNotDeadlock(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(Metadata{SessionID: "session-a", ACPServer: "test-server", WorkingDir: "/tmp", Name: "A"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := store.Create(Metadata{SessionID: "session-b", ACPServer: "test-server", WorkingDir: "/tmp", Name: "B"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	done := make(chan struct{})
	store.SetDeleteObserver(func(sessionID, parentSessionID string) {
		// Re-entrant calls into the Store from within the observer must not
		// deadlock, since Delete invokes the observer after releasing s.mu.
		if store.Exists("session-b") == false {
			t.Error("re-entrant Exists call returned unexpected result")
		}
		if _, err := store.GetMetadata("session-b"); err != nil {
			t.Errorf("re-entrant GetMetadata call failed: %v", err)
		}
		if _, err := store.ListChildSessions("session-a"); err != nil {
			t.Errorf("re-entrant ListChildSessions call failed: %v", err)
		}
		close(done)
	})

	if err := store.Delete("session-a"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	select {
	case <-done:
		// observer ran to completion without deadlocking
	default:
		t.Fatal("observer did not run (or Delete returned before the observer finished)")
	}
}
