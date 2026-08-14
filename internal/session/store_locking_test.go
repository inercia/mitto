package session

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newLockingTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, id := range []string{"session-a", "session-b"} {
		if err := store.Create(Metadata{SessionID: id, ACPServer: "test", WorkingDir: "/test"}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}
	return store
}

func waitForSessionLockRefs(t *testing.T, store *Store, sessionID string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		store.sessionLocksMu.Lock()
		entry := store.sessionLocks[sessionID]
		got := 0
		if entry != nil {
			got = entry.refs
		}
		store.sessionLocksMu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session lock %q did not reach %d refs", sessionID, want)
}

func waitForLifecycleWriter(t *testing.T, store *Store) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !store.mu.TryRLock() {
			return
		}
		store.mu.RUnlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("global operation did not queue for the lifecycle write lock")
}

func TestStoreSessionLocksShardBySession(t *testing.T) {
	store := newLockingTestStore(t)
	releaseA, err := store.lockSessionWrite("session-a")
	if err != nil {
		t.Fatalf("lock session A: %v", err)
	}

	aDone := make(chan error, 1)
	go func() {
		aDone <- store.UpdateMetadata("session-a", func(meta *Metadata) { meta.Name = "updated-a" })
	}()
	waitForSessionLockRefs(t, store, "session-a", 2)

	bDone := make(chan error, 1)
	go func() {
		if err := store.UpdateMetadata("session-b", func(meta *Metadata) { meta.Name = "updated-b" }); err != nil {
			bDone <- err
			return
		}
		bDone <- store.AppendEvent("session-b", Event{
			Type: EventTypeUserPrompt,
			Data: UserPromptData{Message: "unrelated event"},
		})
	}()
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatalf("unrelated session metadata/event I/O: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session B blocked behind session A")
	}
	select {
	case err := <-aDone:
		t.Fatalf("same-session update completed while lock held: %v", err)
	default:
	}

	releaseA()
	if err := <-aDone; err != nil {
		t.Fatalf("same-session update after release: %v", err)
	}
	waitForSessionLockRefs(t, store, "session-a", 0)
}

func TestStoreGlobalOperationsAreLifecycleBarriers(t *testing.T) {
	tests := []struct {
		name   string
		action func(*Store) error
		verify func(*testing.T, *Store)
	}{
		{"list", func(s *Store) error { _, err := s.List(); return err }, nil},
		{"delete", func(s *Store) error { return s.Delete("session-a") }, func(t *testing.T, s *Store) {
			if s.Exists("session-a") {
				t.Fatal("session A still exists after Delete")
			}
		}},
		{"close", func(s *Store) error { return s.Close() }, func(t *testing.T, s *Store) {
			if _, err := s.GetMetadata("session-b"); !errors.Is(err, ErrStoreClosed) {
				t.Fatalf("GetMetadata after Close: got %v, want ErrStoreClosed", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newLockingTestStore(t)
			release, err := store.lockSessionRead("session-a")
			if err != nil {
				t.Fatalf("lock session A: %v", err)
			}
			done := make(chan error, 1)
			go func() { done <- tc.action(store) }()
			waitForLifecycleWriter(t, store)
			select {
			case err := <-done:
				t.Fatalf("global operation crossed active session lock: %v", err)
			default:
			}
			release()
			if err := <-done; err != nil {
				t.Fatalf("global operation after release: %v", err)
			}
			if tc.verify != nil {
				tc.verify(t, store)
			}
		})
	}
}

func TestStoreConcurrentAppendAndPrunePreservesSequenceAndMetadata(t *testing.T) {
	store := newLockingTestStore(t)
	const writers, eventsEach, keepLast = 4, 40, 50
	start := make(chan struct{})
	errCh := make(chan error, writers+1)
	var wg sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			<-start
			for i := 0; i < eventsEach; i++ {
				err := store.AppendEvent("session-a", Event{Type: EventTypeUserPrompt, Data: UserPromptData{Message: fmt.Sprintf("%d-%d", writer, i)}})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(writer)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < writers*eventsEach; i++ {
			if _, err := store.PruneKeepLast("session-a", keepLast); err != nil {
				errCh <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent append/prune: %v", err)
	}
	if _, err := store.PruneKeepLast("session-a", keepLast); err != nil {
		t.Fatalf("final prune: %v", err)
	}
	events, err := store.ReadEvents("session-a")
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	if len(events) != keepLast {
		t.Fatalf("event count = %d, want %d", len(events), keepLast)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq != events[i-1].Seq+1 {
			t.Fatalf("non-contiguous seq at %d: %d after %d", i, events[i].Seq, events[i-1].Seq)
		}
	}
	meta, err := store.GetMetadata("session-a")
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta.EventCount != keepLast || meta.MaxSeq != writers*eventsEach {
		t.Fatalf("metadata count/max_seq = %d/%d, want %d/%d", meta.EventCount, meta.MaxSeq, keepLast, writers*eventsEach)
	}
}
