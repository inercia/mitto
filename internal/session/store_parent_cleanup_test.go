package session

import (
	"testing"
)

// TestDelete_ClearsParentReferences verifies that deleting a parent session
// clears the ParentSessionID field in all child sessions.
func TestDelete_ClearsParentReferences(t *testing.T) {
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

	// Create multiple child sessions (children inherit parent's flags)
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

	// Verify initial state - children have parent references
	child1, err := store.GetMetadata("child-session-1")
	if err != nil {
		t.Fatalf("GetMetadata child1 failed: %v", err)
	}
	if child1.ParentSessionID != "parent-session-1" {
		t.Errorf("child1.ParentSessionID = %q, want %q", child1.ParentSessionID, "parent-session-1")
	}

	child2, err := store.GetMetadata("child-session-2")
	if err != nil {
		t.Fatalf("GetMetadata child2 failed: %v", err)
	}
	if child2.ParentSessionID != "parent-session-1" {
		t.Errorf("child2.ParentSessionID = %q, want %q", child2.ParentSessionID, "parent-session-1")
	}

	// Delete the parent session
	if err := store.Delete("parent-session-1"); err != nil {
		t.Fatalf("Delete parent failed: %v", err)
	}

	// Verify parent is deleted
	if store.Exists("parent-session-1") {
		t.Error("Parent session still exists after deletion")
	}

	// Verify child sessions have their parent references cleared
	child1After, err := store.GetMetadata("child-session-1")
	if err != nil {
		t.Fatalf("GetMetadata child1 after delete failed: %v", err)
	}
	if child1After.ParentSessionID != "" {
		t.Errorf("child1.ParentSessionID after delete = %q, want empty string", child1After.ParentSessionID)
	}

	child2After, err := store.GetMetadata("child-session-2")
	if err != nil {
		t.Fatalf("GetMetadata child2 after delete failed: %v", err)
	}
	if child2After.ParentSessionID != "" {
		t.Errorf("child2.ParentSessionID after delete = %q, want empty string", child2After.ParentSessionID)
	}

	// Verify unrelated session is unchanged
	unrelatedAfter, err := store.GetMetadata("unrelated-session")
	if err != nil {
		t.Fatalf("GetMetadata unrelated after delete failed: %v", err)
	}
	if unrelatedAfter.ParentSessionID != "" {
		t.Errorf("unrelated.ParentSessionID = %q, want empty string", unrelatedAfter.ParentSessionID)
	}

	// Verify UpdatedAt was updated for child sessions
	if !child1After.UpdatedAt.After(child1.UpdatedAt) {
		t.Error("child1.UpdatedAt was not updated after parent deletion")
	}
	if !child2After.UpdatedAt.After(child2.UpdatedAt) {
		t.Error("child2.UpdatedAt was not updated after parent deletion")
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
// in a three-level hierarchy only clears references in direct children.
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

	// Verify child's parent reference is cleared
	childAfter, err := store.GetMetadata("child")
	if err != nil {
		t.Fatalf("GetMetadata child after delete failed: %v", err)
	}
	if childAfter.ParentSessionID != "" {
		t.Errorf("child.ParentSessionID after delete = %q, want empty string", childAfter.ParentSessionID)
	}

	// Verify grandparent is unchanged
	grandparentAfter, err := store.GetMetadata("grandparent")
	if err != nil {
		t.Fatalf("GetMetadata grandparent after delete failed: %v", err)
	}
	if grandparentAfter.ParentSessionID != "" {
		t.Errorf("grandparent.ParentSessionID = %q, want empty string", grandparentAfter.ParentSessionID)
	}
}
