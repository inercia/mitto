package session

import (
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
