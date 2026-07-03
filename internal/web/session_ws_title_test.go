package web

import (
	"github.com/inercia/mitto/internal/session"
	"testing"
)

// Tests for SessionWSClient.sessionNeedsTitle

func TestSessionWSClient_SessionNeedsTitle_NoStore(t *testing.T) {
	client := &SessionWSClient{
		sessionID: "test-session",
		store:     nil, // No store
	}

	if client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return false when store is nil")
	}
}

func TestSessionWSClient_SessionNeedsTitle_EmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	client := &SessionWSClient{
		sessionID: "", // Empty session ID
		store:     store,
	}

	if client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return false when sessionID is empty")
	}
}

func TestSessionWSClient_SessionNeedsTitle_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with empty name
	meta := session.Metadata{
		SessionID:  "test-session-ws-empty",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // Empty name - needs title
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	client := &SessionWSClient{
		sessionID: "test-session-ws-empty",
		store:     store,
	}

	if !client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return true when session name is empty")
	}
}

func TestSessionWSClient_SessionNeedsTitle_HasName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a name
	meta := session.Metadata{
		SessionID:  "test-session-ws-named",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Named Session", // Has a name
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	client := &SessionWSClient{
		sessionID: "test-session-ws-named",
		store:     store,
	}

	if client.sessionNeedsTitle() {
		t.Error("sessionNeedsTitle should return false when session already has a name")
	}
}
