package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNewStore_CleanupOrphanSessionDirs pins mitto-32ef's startup sweep:
// any immediate subdirectory of baseDir with neither metadata.json nor
// events.jsonl is removed by NewStore, while directories carrying either
// file (a real, readable, or partially-written session) are left alone.
func TestNewStore_CleanupOrphanSessionDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Orphan: only processor_state.json (the dominant production case —
	// 144/145 orphans observed in the field).
	processorOnlyDir := filepath.Join(tmpDir, "orphan-processor-only")
	mustMkdirAndWrite(t, processorOnlyDir, "processor_state.json", `{"agent_response_count":1}`)

	// Orphan: only queue.json (the remaining 1/145 case).
	queueOnlyDir := filepath.Join(tmpDir, "orphan-queue-only")
	mustMkdirAndWrite(t, queueOnlyDir, "queue.json", `{"messages":[]}`)

	// Orphan: completely empty directory.
	emptyDir := filepath.Join(tmpDir, "orphan-empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("failed to create empty dir: %v", err)
	}

	// Not an orphan: has metadata.json only (e.g. a session whose events
	// file write raced a crash) — must be preserved, not judged unsafe.
	metadataOnlyDir := filepath.Join(tmpDir, "metadata-only-session")
	mustMkdirAndWrite(t, metadataOnlyDir, metadataFileName, `{"session_id":"metadata-only-session"}`)

	// Not an orphan: has events.jsonl only.
	eventsOnlyDir := filepath.Join(tmpDir, "events-only-session")
	mustMkdirAndWrite(t, eventsOnlyDir, eventsFileName, "")

	// Not an orphan: a real, fully-formed session with both files.
	realSessionDir := filepath.Join(tmpDir, "real-session")
	mustMkdirAndWrite(t, realSessionDir, metadataFileName, `{"session_id":"real-session"}`)
	mustMkdirAndWrite(t, realSessionDir, eventsFileName, "")

	// NewStore's own MkdirAll(baseDir) is a no-op here since tmpDir already
	// exists; the cleanup sweep runs regardless.
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	assertRemoved := func(dir string) {
		t.Helper()
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("expected orphan %q to be removed by NewStore, but it still exists", dir)
		}
	}
	assertPreserved := func(dir string) {
		t.Helper()
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Errorf("expected %q to be preserved by NewStore, but stat failed: %v", dir, statErr)
		}
	}

	assertRemoved(processorOnlyDir)
	assertRemoved(queueOnlyDir)
	assertRemoved(emptyDir)
	assertPreserved(metadataOnlyDir)
	assertPreserved(eventsOnlyDir)
	assertPreserved(realSessionDir)
}

// TestNewStore_CleanupOrphanSessionDirs_EmptyBaseDir confirms the sweep is
// a no-op (does not error or panic) when baseDir has no subdirectories at
// all — the common case for a fresh install.
func TestNewStore_CleanupOrphanSessionDirs_EmptyBaseDir(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed on empty base dir: %v", err)
	}
	defer store.Close()
}

// mustMkdirAndWrite creates dir (and parents) and writes content to
// filepath.Join(dir, name), failing the test on any error.
func mustMkdirAndWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create dir %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %q: %v", filepath.Join(dir, name), err)
	}
}

// TestDelete_RemovesAllSidecarsNoResidue is the mitto-32ef acceptance-
// criterion regression test: deleting a session that has accumulated every
// known sidecar file leaves absolutely nothing behind — the whole
// directory is gone, not just metadata.json/events.jsonl. Store.Delete
// already used os.RemoveAll (this was never the bug), but this pins that
// invariant explicitly so a future refactor to Delete cannot silently
// regress to a partial (metadata+events-only) removal.
func TestDelete_RemovesAllSidecarsNoResidue(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "session-with-sidecars"
	if err := store.Create(Metadata{SessionID: sid, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sessionDir := store.SessionDir(sid)
	for _, sidecar := range []string{
		"processor_state.json",
		"queue.json",
		"loop.json",
		"action_buttons.json",
		"child-reports.json",
	} {
		if err := os.WriteFile(filepath.Join(sessionDir, sidecar), []byte(`{}`), 0644); err != nil {
			t.Fatalf("failed to write sidecar %q: %v", sidecar, err)
		}
	}

	if err := store.Delete(sid); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Errorf("expected session directory %q to be fully removed, stat err: %v", sessionDir, err)
	}
}

// TestDelete_TransientBlockDuringRemoveAll_ShouldRetryNotOrphan pins mitto-s5d:
// today, Store.Delete's deleteLocked calls os.RemoveAll exactly once. In
// production, a background writer (events.jsonl append, processor_state.json
// save on AgentResponded, log flush) racing the delete can create a new file
// in the session directory in the narrow window between os.RemoveAll's final
// directory listing and its rmdir call, causing the rmdir to fail with
// "directory not empty" (ENOTEMPTY / unlinkat ...) even though RemoveAll
// already removed every OTHER file, including metadata.json/events.jsonl.
// Store.Delete then returns that raw error with no retry, leaving the
// session directory orphaned on disk while GetMetadata already reports the
// session as gone (mitto_conversation_get: "conversation not found") -
// exactly the reported symptom.
//
// This test simulates that transient race deterministically instead of
// relying on hitting the real microsecond-scale ENOTEMPTY window: a
// subdirectory is blocked (unreadable) at the moment Delete is called, and a
// background goroutine clears the block ~30ms later - mirroring how the real
// racing writer eventually stops touching the directory once its own flush
// finishes. Store.Delete's current single-attempt os.RemoveAll fails
// immediately (well before the 30ms elapses), so this test FAILS today. A
// fix that retries the directory removal for a reasonably short bounded
// window (comfortably more than 30ms - e.g. a handful of attempts with short
// backoff) will absorb the block and let Delete succeed with no orphan left
// behind, which is what this test asserts.
func TestDelete_TransientBlockDuringRemoveAll_ShouldRetryNotOrphan(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sid := "session-transient-removeall-block"
	if err := store.Create(Metadata{SessionID: sid, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sessionDir := store.SessionDir(sid)

	// Simulate the racing background writer: a subdirectory with content
	// that is present (blocking removal) right as Delete starts, then
	// clears itself shortly after - just like a real writer that stops
	// touching the directory once its own turn/flush finishes.
	racingDir := filepath.Join(sessionDir, "racing-writer")
	if err := os.MkdirAll(racingDir, 0755); err != nil {
		t.Fatalf("failed to create racing-writer dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(racingDir, "inflight.tmp"), []byte("x"), 0644); err != nil {
		t.Fatalf("failed to write racing file: %v", err)
	}
	if err := os.Chmod(racingDir, 0); err != nil {
		t.Fatalf("failed to chmod racing-writer dir: %v", err)
	}

	unblocked := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.Chmod(racingDir, 0755)
		_ = os.RemoveAll(racingDir)
		close(unblocked)
	}()
	t.Cleanup(func() {
		<-unblocked
		_ = os.Chmod(racingDir, 0755) // safety net in case Delete already removed the parent
	})

	deleteErr := store.Delete(sid)

	<-unblocked // ensure the racing writer has finished before inspecting final state

	if deleteErr != nil {
		t.Fatalf("expected Store.Delete to absorb the transient block and succeed (mitto-s5d: retry the directory removal instead of failing on the first os.RemoveAll attempt), got error: %v", deleteErr)
	}
	if _, statErr := os.Stat(sessionDir); !os.IsNotExist(statErr) {
		t.Errorf("expected session directory %q to be fully removed with no orphan left behind, stat err: %v", sessionDir, statErr)
	}
}
