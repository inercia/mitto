package processors

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestReplayDeadLetters_RecoversEligibleEntryToMainSpool covers AC3
// (mitto-6ys1): a batch that was dead-lettered (instead of silently dropped)
// must not be a permanent loss — once past the replay cooldown, it is
// automatically moved back into the main pending spool for another attempt.
func TestReplayDeadLetters_RecoversEligibleEntryToMainSpool(t *testing.T) {
	spoolDir := t.TempDir()
	wsUUID := "ws-deadletter-replay"
	store := &FilePendingDispatchStore{BaseDir: spoolDir}

	entry := PendingDispatchEntry{
		ID:            "batch-1",
		WorkspaceUUID: wsUUID,
		Name:          "poison-batch",
		Prompt:        "do the thing",
		SavedAt:       time.Now().Add(-2 * time.Hour),
		Attempts:      pendingDispatchMaxAttempts,
		ClaimedBy:     "mitto-proc-a",
		ClaimedAt:     time.Now().Add(-2 * time.Hour),
	}

	if err := store.DeadLetter(wsUUID, []PendingDispatchEntry{entry}, "exceeded max attempts"); err != nil {
		t.Fatalf("DeadLetter failed: %v", err)
	}

	// The entry must not linger in the main spool once dead-lettered.
	mainEntries, err := store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load after DeadLetter failed: %v", err)
	}
	if len(mainEntries) != 0 {
		t.Fatalf("main spool after DeadLetter = %+v, want empty (entry should be dead-lettered, not left pending)", mainEntries)
	}

	// Use cooldown=0 so the entry is immediately eligible for replay,
	// bypassing the real 1-hour deadLetterReplayCooldown deterministically.
	replayed, err := store.ReplayDeadLetters(wsUUID, 0)
	if err != nil {
		t.Fatalf("ReplayDeadLetters failed: %v", err)
	}
	if len(replayed) != 1 || replayed[0].Name != "poison-batch" {
		t.Fatalf("ReplayDeadLetters returned %+v, want exactly one replayed entry for poison-batch", replayed)
	}
	if replayed[0].Attempts != 0 {
		t.Errorf("replayed entry Attempts = %d, want reset to 0", replayed[0].Attempts)
	}
	if replayed[0].ClaimedBy != "" {
		t.Errorf("replayed entry ClaimedBy = %q, want cleared", replayed[0].ClaimedBy)
	}
	if !replayed[0].ClaimedAt.IsZero() {
		t.Errorf("replayed entry ClaimedAt = %v, want zero value", replayed[0].ClaimedAt)
	}

	// AC3: the batch must now be recoverable — it is back in the main spool
	// for another dispatch attempt instead of being permanently lost.
	mainEntries, err = store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load after ReplayDeadLetters failed: %v", err)
	}
	if len(mainEntries) != 1 || mainEntries[0].Name != "poison-batch" {
		t.Fatalf("main spool after ReplayDeadLetters = %+v, want exactly one recovered entry for poison-batch", mainEntries)
	}
	if mainEntries[0].ID != entry.ID {
		t.Errorf("recovered entry ID = %q, want %q (same batch, not a duplicate)", mainEntries[0].ID, entry.ID)
	}

	// The dead-letter file should no longer reference the now-replayed entry:
	// since it was the only dead-lettered entry, the file itself is removed.
	deadLetterPath := filepath.Join(spoolDir, wsUUID+".deadletter.json")
	if _, statErr := os.Stat(deadLetterPath); !os.IsNotExist(statErr) {
		t.Errorf("dead-letter file at %s still exists after replay (stat err = %v), want removed since it held no remaining entries", deadLetterPath, statErr)
	}
}

// TestReplayDeadLetters_RespectsCooldown covers AC3's cooldown gate: a
// dead-lettered entry must not be replayed before deadLetterReplayCooldown
// has elapsed, so a batch that fails immediately after a previous replay
// does not spin in a tight dead-letter/replay loop.
func TestReplayDeadLetters_RespectsCooldown(t *testing.T) {
	spoolDir := t.TempDir()
	wsUUID := "ws-deadletter-cooldown"
	store := &FilePendingDispatchStore{BaseDir: spoolDir}

	entry := PendingDispatchEntry{
		ID:            "batch-2",
		WorkspaceUUID: wsUUID,
		Name:          "recent-poison-batch",
		Prompt:        "do the other thing",
		SavedAt:       time.Now(),
		Attempts:      pendingDispatchMaxAttempts,
	}

	if err := store.DeadLetter(wsUUID, []PendingDispatchEntry{entry}, "exceeded max attempts"); err != nil {
		t.Fatalf("DeadLetter failed: %v", err)
	}

	// A long cooldown means the just-dead-lettered entry is not yet eligible.
	replayed, err := store.ReplayDeadLetters(wsUUID, time.Hour)
	if err != nil {
		t.Fatalf("ReplayDeadLetters failed: %v", err)
	}
	if len(replayed) != 0 {
		t.Fatalf("ReplayDeadLetters returned %+v before cooldown elapsed, want none replayed", replayed)
	}

	// The entry must remain dead-lettered (not silently dropped) while
	// waiting out the cooldown.
	mainEntries, err := store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(mainEntries) != 0 {
		t.Fatalf("main spool = %+v before cooldown elapsed, want empty (entry should still be dead-lettered)", mainEntries)
	}

	deadLetterPath := filepath.Join(spoolDir, wsUUID+".deadletter.json")
	if _, statErr := os.Stat(deadLetterPath); statErr != nil {
		t.Fatalf("expected dead-letter file to still exist while cooldown pending, stat err = %v", statErr)
	}
}
