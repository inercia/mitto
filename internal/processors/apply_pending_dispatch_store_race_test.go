package processors

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestManager_PendingDispatchStore_ConcurrentSetAndFlush_Race reproduces
// mitto-d6v: Manager.pendingDispatchStore was written by
// SetPendingDispatchStore while FlushPendingDispatches read it, with no
// synchronisation between them.
//
// The two accesses are genuinely unsynchronised (the WaitGroup only orders the
// final Wait), so the race detector reports the write/read pair regardless of
// wall-clock overlap. Production hits this because SessionManager shares one
// *Manager across sessions: every new/resume re-wires the store
// (wireProcessorPendingDispatch) and then spawns a fire-and-forget flush
// goroutine that outlives the call.
//
// Fails under -race before the fix; passes once the field is guarded.
func TestManager_PendingDispatchStore_ConcurrentSetAndFlush_Race(t *testing.T) {
	const (
		iterations    = 200
		workspaceUUID = "ws-d6v-race"
	)

	storeA := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	storeB := &FilePendingDispatchStore{BaseDir: t.TempDir()}

	m := NewManager("", nil)
	m.SetPendingDispatchStore(storeA)
	// FlushPendingDispatches returns early unless a prompt executor is wired.
	m.SetPromptFunc(func(context.Context, string, string, string) error { return nil })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var flushes atomic.Int64
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				m.SetPendingDispatchStore(storeB)
			} else {
				m.SetPendingDispatchStore(storeA)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.FlushPendingDispatches(ctx, workspaceUUID)
			flushes.Add(1)
		}
	}()

	wg.Wait()

	if got := flushes.Load(); got != iterations {
		t.Fatalf("flush calls = %d, want %d", got, iterations)
	}
}
