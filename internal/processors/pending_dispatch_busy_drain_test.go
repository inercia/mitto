package processors

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

// TestFlushPendingDispatches_DrainsAfterAsyncPromptReleasesSlot reproduces
// mitto-e3ut.1. PromptProcessorAsync returns as soon as it starts a background
// prompt, while that prompt keeps the threshold-1 ActiveRPC slot occupied.
// One flush opportunity must eventually deliver every distinct-purpose entry
// sequentially instead of stopping after the first ErrProcessBusy.
func TestFlushPendingDispatches_DrainsAfterAsyncPromptReleasesSlot(t *testing.T) {
	origMaxRetries := dispatchPromptMaxRetries
	dispatchPromptMaxRetries = 0
	t.Cleanup(func() { dispatchPromptMaxRetries = origMaxRetries })
	origBusyInterval := pendingDispatchBusyRetryInterval
	pendingDispatchBusyRetryInterval = 5 * time.Millisecond
	t.Cleanup(func() { pendingDispatchBusyRetryInterval = origBusyInterval })

	const wsUUID = "ws-async-busy-drain"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	entries := []PendingDispatchEntry{
		{WorkspaceUUID: wsUUID, Name: "batch-a", Prompt: "prompt-a", SavedAt: time.Now(), Attempts: 1},
		{WorkspaceUUID: wsUUID, Name: "batch-b", Prompt: "prompt-b", SavedAt: time.Now(), Attempts: 1},
		{WorkspaceUUID: wsUUID, Name: "batch-c", Prompt: "prompt-c", SavedAt: time.Now(), Attempts: 1},
	}
	if err := store.Replace(wsUUID, entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	var active atomic.Bool
	var mu sync.Mutex
	var delivered []string
	m := NewManager("", nil)
	m.SetPendingDispatchStore(store)
	m.SetPromptFunc(func(_ context.Context, _, name, _ string) error {
		if !active.CompareAndSwap(false, true) {
			return fmt.Errorf("failed to get auxiliary session: %w", acperrors.ErrProcessBusy)
		}
		mu.Lock()
		delivered = append(delivered, name)
		mu.Unlock()
		go func() {
			time.Sleep(25 * time.Millisecond)
			active.Store(false)
		}()
		return nil
	})

	m.FlushPendingDispatches(context.Background(), wsUUID)

	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(delivered)
		mu.Unlock()
		if count == len(entries) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), delivered...)
	mu.Unlock()
	remaining, err := store.Load(wsUUID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 3 || got[0] != "batch-a" || got[1] != "batch-b" || got[2] != "batch-c" || len(remaining) != 0 {
		t.Fatalf("delivered = %v, remaining = %v; want [batch-a batch-b batch-c] and an empty spool", got, pendingDispatchNames(remaining))
	}
}

func pendingDispatchNames(entries []PendingDispatchEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
