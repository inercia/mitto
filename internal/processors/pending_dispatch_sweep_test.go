package processors

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// TestSweepPendingDispatchDir_OrphanedWorkspaceDropIsAudited reproduces
// mitto-f81: draining (age-pruning) a close-phase memory batch for an
// orphaned workspace — one that is permanently non-dispatchable, e.g.
// removed from folders.json with no live shared ACP process — is
// completely silent today. TestSweepPendingDispatchDir_DrainsOrphanedWorkspaceSpool
// above already pins that the entry IS dropped (mitto-lak); this test pins
// that the drop must also be *audited*: a WARN naming the workspace and the
// processor(s) whose batch was lost, since extract-memories-on-close /
// claude-update-memory / memorize-preferences / auggie-update-rules /
// curate-memories-on-close batches represent memory/preference/rule work
// that is otherwise lost with no observable trace (mitto-f81 investigation).
//
// The LastError recorded on the entry is the exact string produced by the
// orphaned-workspace path (getOrCreateAuxiliarySession, matched by
// isNonRetryableDispatchErr's "no shared process for workspace" check) —
// deliberately NOT one of isTransientAuxUnavailableDispatchErr's classes, so
// this entry gets no extended grace and expires at the ordinary
// pendingDispatchMaxAge, exactly as in the reported incident.
//
// This test fails today because SweepPendingDispatchDir's non-dispatchable
// branch calls FilePendingDispatchStore.Load directly, which discards its
// `expired` set with no logging of any kind — there is no WARN to find.
func TestSweepPendingDispatchDir_OrphanedWorkspaceDropIsAudited(t *testing.T) {
	spoolDir := t.TempDir()
	const orphanUUID = "ws-orphaned-memory-batch"
	const processorName = "extract-memories-on-close+claude-update-memory+memorize-preferences+auggie-update-rules+curate-memories-on-close"
	const auxUnavailableErr = "failed to get auxiliary session: no shared process for workspace " + orphanUUID + " (auxiliary sessions require an active workspace)"

	store := &FilePendingDispatchStore{BaseDir: spoolDir}
	aged := PendingDispatchEntry{
		WorkspaceUUID: orphanUUID,
		Name:          processorName,
		Prompt:        "persist memories",
		SavedAt:       time.Now().Add(-(pendingDispatchMaxAge + time.Hour)),
		Attempts:      1,
		LastError:     auxUnavailableErr,
	}
	if err := store.Replace(orphanUUID, []PendingDispatchEntry{aged}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPendingDispatchStore(store)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		t.Fatal("orphaned (non-dispatchable) workspace must not be flushed via promptFunc; its aged entry should be pruned by the age cap instead")
		return nil
	})

	isDispatchable := func(workspaceUUID string) bool { return false }

	if _, err := SweepPendingDispatchDir(m, spoolDir, isDispatchable); err != nil {
		t.Fatalf("SweepPendingDispatchDir() error = %v", err)
	}

	// Sanity: the batch really was dropped (unchanged mitto-lak behavior) —
	// otherwise the missing-WARN assertion below would be trivially true for
	// the wrong reason (nothing was dropped at all).
	remaining, err := store.Load(orphanUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining entries = %d, want 0 (the orphaned close-phase batch should have aged out)", len(remaining))
	}

	for _, rec := range handler.snapshot() {
		if rec.Level != slog.LevelWarn {
			continue
		}
		if !strings.Contains(rec.Message, "pending-dispatch") {
			continue
		}
		if rec.Attrs["workspace_uuid"] == orphanUUID {
			return // audited — found the expected WARN
		}
	}
	t.Fatalf("SweepPendingDispatchDir() silently dropped an undeliverable close-phase memory batch "+
		"for orphaned workspace %s (processor %q) with no audit WARN (mitto-f81); captured records: %+v",
		orphanUUID, processorName, handler.snapshot())
}
