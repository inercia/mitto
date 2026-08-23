package processors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSweepPendingDispatchDir_DrainsOrphanedWorkspaceSpool reproduces
// mitto-lak: a pending-dispatch spool for a workspace with no live process
// (and which will never again fire a conversation-close event, i.e. it is
// "orphaned") is never drained or aged out today, because the only
// entrypoints able to prune it — FlushPendingDispatches(ctx, workspaceUUID),
// and the pendingDispatchMaxAge cap enforced inside Load/Claim — require a
// caller who already knows the workspace UUID in advance. Nothing in
// production enumerates PendingProcessorDispatchDir() independent of a live
// or closing session; the only two callers of FlushPendingDispatches are
// BackgroundSession.wireProcessorPendingDispatch (fires only when a live
// session for that workspace is constructed/resumed) and the
// conversation-close pipeline (SessionManager.ApplyOnCloseProcessors) — see
// the mitto-lak investigation comment for the full trace.
//
// SweepPendingDispatchDir is the missing capability that closes this gap: given
// a directory of per-workspace spool files, it must discover every
// workspace's spool purely from the files present on disk (no advance
// knowledge of the UUID required), then either flush it (when the workspace
// is currently dispatchable) or otherwise let the existing age cap prune
// expired entries and remove an empty spool file. It does not exist yet, so
// this test fails to build until the fix phase adds it — the undefined
// symbol below is the precise, intentional reproduction of the bug (a
// missing directory-wide sweep), not an unrelated setup error.
func TestSweepPendingDispatchDir_DrainsOrphanedWorkspaceSpool(t *testing.T) {
	spoolDir := t.TempDir()
	const orphanUUID = "ws-orphaned-forever"
	store := &FilePendingDispatchStore{BaseDir: spoolDir}

	aged := PendingDispatchEntry{
		WorkspaceUUID: orphanUUID,
		Name:          "extract-memories-on-close",
		Prompt:        "persist memories",
		SavedAt:       time.Now().Add(-(pendingDispatchMaxAge + time.Hour)),
		Attempts:      1,
	}
	if err := store.Replace(orphanUUID, []PendingDispatchEntry{aged}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		t.Fatal("orphaned (non-dispatchable) workspace must not be flushed via promptFunc; its aged entry should be pruned by the age cap instead")
		return nil
	})

	// The orphaned workspace is never dispatchable: no live process today,
	// and by definition of "orphaned" (per the bug) it never will be again.
	isDispatchable := func(workspaceUUID string) bool { return false }

	swept, err := SweepPendingDispatchDir(m, spoolDir, isDispatchable)
	if err != nil {
		t.Fatalf("SweepPendingDispatchDir() error = %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1 (the orphaned workspace's on-disk spool should have been discovered without any prior knowledge of its UUID)", swept)
	}

	spoolPath := filepath.Join(spoolDir, orphanUUID+".json")
	if _, statErr := os.Stat(spoolPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected the orphaned workspace's aged spool file to be pruned/removed by the sweep, stat err = %v", statErr)
	}
}
