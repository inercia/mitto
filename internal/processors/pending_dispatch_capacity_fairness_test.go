package processors

import (
	"testing"
	"time"
)

// TestFilePendingDispatchStore_CapacityEvictionDropsOldestRegardlessOfProcessor
// reproduces mitto-8ynr: pending-dispatch spool capacity eviction
// (boundPendingDispatchEntries, triggered once a workspace's spool exceeds
// pendingDispatchMaxEntries) is pure FIFO drop-oldest-unclaimed, with no
// fairness across processor names. A workspace whose spool already holds
// older, high-value close-phase memory batches (extract-memories-on-close,
// curate-memories-on-close) loses them to a flood of newer, low-value
// prompt-mode identify-user-data dispatches once the spool saturates —
// exactly the production evidence in the bug report (12x identify-user-data,
// 3x extract-memories-on-close, 1x curate-memories-on-close dropped on one
// workspace).
func TestFilePendingDispatchStore_CapacityEvictionDropsOldestRegardlessOfProcessor(t *testing.T) {
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	const wsUUID = "ws-capacity-fairness"

	// Prime the spool with the oldest entries being high-value close-phase
	// memory batches, then fill the rest of the cap with low-value
	// identify-user-data filler — mirroring the production ordering where
	// memory batches were enqueued first and the identify-user-data flood
	// arrived later.
	memoryNames := []string{
		"extract-memories-on-close",
		"extract-memories-on-close",
		"extract-memories-on-close",
		"curate-memories-on-close",
	}
	base := time.Now().Add(-time.Hour)
	for i, name := range memoryNames {
		if _, err := store.Append(PendingDispatchEntry{
			WorkspaceUUID: wsUUID,
			Name:          name,
			Prompt:        "memory batch " + name,
			SavedAt:       base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Append(%s) error = %v", name, err)
		}
	}
	for i := len(memoryNames); i < pendingDispatchMaxEntries; i++ {
		if _, err := store.Append(PendingDispatchEntry{
			WorkspaceUUID: wsUUID,
			Name:          "identify-user-data",
			Prompt:        "filler",
			SavedAt:       base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Append(filler-%d) error = %v", i, err)
		}
	}

	// Spool is now exactly at capacity. One more identify-user-data dispatch
	// (the flood in the bug report) must not evict the older, high-value
	// memory-processor entries.
	appendResult, err := store.Append(PendingDispatchEntry{
		WorkspaceUUID: wsUUID,
		Name:          "identify-user-data",
		Prompt:        "flood entry",
		SavedAt:       time.Now(),
	})
	if err != nil {
		t.Fatalf("Append(flood) error = %v", err)
	}
	if len(appendResult.Dropped) != 1 {
		t.Fatalf("Dropped count = %d, want 1", len(appendResult.Dropped))
	}
	dropped := appendResult.Dropped[0]
	if dropped.Name == "extract-memories-on-close" || dropped.Name == "curate-memories-on-close" {
		t.Fatalf("mitto-8ynr: capacity eviction dropped memory-processor entry %q to make room for an identify-user-data flood entry; want a non-memory entry evicted instead", dropped.Name)
	}

	// The memory-processor entries must all still be present in the spool.
	remaining, err := store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	counts := map[string]int{}
	for _, entry := range remaining {
		counts[entry.Name]++
	}
	if got := counts["extract-memories-on-close"]; got != 3 {
		t.Errorf("mitto-8ynr: expected 3 surviving extract-memories-on-close entries, got %d", got)
	}
	if got := counts["curate-memories-on-close"]; got != 1 {
		t.Errorf("mitto-8ynr: expected 1 surviving curate-memories-on-close entry, got %d", got)
	}
}
