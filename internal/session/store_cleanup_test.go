package session

import (
	"os"
	"testing"
	"time"
)

// TestCleanupArchivedSessions_DeletesOldArchivedSession is the baseline: an
// ordinary (non-protected) session archived well past the retention period
// is swept.
func TestCleanupArchivedSessions_DeletesOldArchivedSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "old-archived-session"
	if err := store.Create(Metadata{SessionID: sid, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := store.UpdateMetadata(sid, func(m *Metadata) {
		m.Archived = true
		m.ArchivedAt = old
		m.ArchiveReason = ArchiveReasonManual
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	deleted, err := store.CleanupArchivedSessions("1d")
	if err != nil {
		t.Fatalf("CleanupArchivedSessions error: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if _, err := os.Stat(store.SessionDir(sid)); !os.IsNotExist(err) {
		t.Error("expected session directory to be removed")
	}
}

// TestCleanupArchivedSessions_NeverSweepsNoArchiveSession pins mitto-yvel.3:
// CleanupArchivedSessions only sweeps meta.Archived==true sessions. A
// NoArchive conversation can never reach Archived==true through any archive
// entry point (every one of them checks Metadata.IsArchivable() first), so
// even a very old, never-touched NoArchive session must never be swept —
// unreachable by construction, pinned here so a future change to this
// function's Archived-only gate is caught.
func TestCleanupArchivedSessions_NeverSweepsNoArchiveSession(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "old-no-archive-session"
	if err := store.Create(Metadata{
		SessionID:  sid,
		ACPServer:  "test",
		WorkingDir: "/tmp",
		NoArchive:  true,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	deleted, err := store.CleanupArchivedSessions("1d")
	if err != nil {
		t.Fatalf("CleanupArchivedSessions error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 (NoArchive, Archived=false session must never be swept)", deleted)
	}
	if _, err := os.Stat(store.SessionDir(sid)); os.IsNotExist(err) {
		t.Error("NoArchive session directory must not be removed by cleanup")
	}
}
