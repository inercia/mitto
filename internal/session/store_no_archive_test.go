package session

import (
	"os"
	"strings"
	"testing"
)

// TestStore_NoArchive_RoundTrip pins mitto-yvel.2: Metadata.NoArchive
// persists through Create -> UpdateMetadata -> GetMetadata (the same path
// session_create.go and tools_conversation_new.go use to set it at create
// time), and survives a store reload (simulating a Mitto restart).
func TestStore_NoArchive_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-no-archive"
	meta := Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Default is false for a plain session.
	gotMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if gotMeta.NoArchive {
		t.Error("NoArchive should default to false")
	}

	// Set it via UpdateMetadata, mirroring how the create paths persist it.
	if err := store.UpdateMetadata(sessionID, func(m *Metadata) {
		m.NoArchive = true
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	gotMeta, err = store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata after update failed: %v", err)
	}
	if !gotMeta.NoArchive {
		t.Error("NoArchive should be true after UpdateMetadata")
	}

	// Simulate a restart: close and reopen the store on the same baseDir.
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	store2, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore (reopen) failed: %v", err)
	}
	defer store2.Close()

	reloaded, err := store2.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata (reload) failed: %v", err)
	}
	if !reloaded.NoArchive {
		t.Error("NoArchive should survive a store reload (restart)")
	}
}

// TestStore_NoArchive_OmitEmptyWhenFalse pins the json:"no_archive,omitempty"
// tag on Metadata.NoArchive: a session created before this field existed (or
// any session where the flag was never set) must not gain a stray
// "no_archive" key in its on-disk metadata.json.
func TestStore_NoArchive_OmitEmptyWhenFalse(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-no-archive-omitempty"
	if err := store.Create(Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/test/dir",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	raw, err := os.ReadFile(store.metadataPath(sessionID))
	if err != nil {
		t.Fatalf("ReadFile(metadata.json) failed: %v", err)
	}
	if strings.Contains(string(raw), "no_archive") {
		t.Errorf("metadata.json contains \"no_archive\" for a session with NoArchive=false: %s", raw)
	}
}
