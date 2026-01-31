package web

import (
	"testing"

	"github.com/inercia/mitto/internal/session"
)

func TestSessionNeedsTitle(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session without a name
	sessionID := "test-session-1"
	err = store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // No name
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Session without name should need title
	if !SessionNeedsTitle(store, sessionID) {
		t.Error("SessionNeedsTitle should return true for session without name")
	}

	// Update session with a name
	err = store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.Name = "My Session"
	})
	if err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	// Session with name should not need title
	if SessionNeedsTitle(store, sessionID) {
		t.Error("SessionNeedsTitle should return false for session with name")
	}
}

func TestSessionNeedsTitle_NilStore(t *testing.T) {
	if SessionNeedsTitle(nil, "some-id") {
		t.Error("SessionNeedsTitle should return false for nil store")
	}
}

func TestSessionNeedsTitle_EmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if SessionNeedsTitle(store, "") {
		t.Error("SessionNeedsTitle should return false for empty session ID")
	}
}

func TestSessionNeedsTitle_NonExistentSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if SessionNeedsTitle(store, "non-existent") {
		t.Error("SessionNeedsTitle should return false for non-existent session")
	}
}

func TestGenerateAndSetTitle_NilStore(t *testing.T) {
	// Should not panic with nil store
	GenerateAndSetTitle(TitleGenerationConfig{
		Store:     nil,
		SessionID: "test",
		Message:   "Hello",
		OnTitleGenerated: func(sessionID, title string) {
			// This won't be called since auxiliary isn't initialized
		},
	})

	// Give goroutine time to run (it should exit early due to auxiliary not being initialized)
	// This test mainly verifies no panic occurs
}

func TestTitleGenerationConfig_Fields(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	cfg := TitleGenerationConfig{
		Store:     store,
		SessionID: "test-session",
		Message:   "Test message",
		Logger:    nil,
		OnTitleGenerated: func(sessionID, title string) {
			// callback
		},
	}

	if cfg.Store != store {
		t.Error("Store field not set correctly")
	}
	if cfg.SessionID != "test-session" {
		t.Error("SessionID field not set correctly")
	}
	if cfg.Message != "Test message" {
		t.Error("Message field not set correctly")
	}
	if cfg.OnTitleGenerated == nil {
		t.Error("OnTitleGenerated callback not set")
	}
}
