package agentbackend

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestFakeHost_LoadSessionHappyPath covers the LoadSession success path
// (only the unknown-ref error path was previously exercised): a session
// created via NewSession must be loadable again by its SessionRef and
// resolve to the same underlying session (same capability view).
func TestFakeHost_LoadSessionHappyPath(t *testing.T) {
	h := newConnectedFakeHost(t, "p1")
	created, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	loaded, err := h.LoadSession(context.Background(), created.Ref())
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.Ref() != created.Ref() {
		t.Fatalf("LoadSession ref = %+v, want %+v", loaded.Ref(), created.Ref())
	}
	// Mutating capability state via the created handle must be visible
	// through the loaded handle too — both must resolve to the same
	// underlying session, not a detached copy.
	if state := loaded.Capabilities().Query(FeatureModelSelection); state != CapabilitySupported {
		t.Fatalf("loaded session FeatureModelSelection = %v, want Supported", state)
	}
}

// TestFakeHost_HostWideSubscriptionReceivesAllSessions exercises the
// documented Subscribe contract: "fn to receive events for ref (or all
// sessions on the host, when ref is the zero value)". A host-wide
// subscriber (zero-value SessionRef) must observe events from every
// session, not just one.
func TestFakeHost_HostWideSubscriptionReceivesAllSessions(t *testing.T) {
	h := newConnectedFakeHost(t, "p1", "p2")
	s1, err := h.NewSession(context.Background(), "p1")
	if err != nil {
		t.Fatalf("NewSession p1: %v", err)
	}
	s2, err := h.NewSession(context.Background(), "p2")
	if err != nil {
		t.Fatalf("NewSession p2: %v", err)
	}

	var mu sync.Mutex
	seen := map[SessionRef]int{}
	sub, err := h.Subscribe(context.Background(), SessionRef{}, func(ev Event) {
		mu.Lock()
		seen[ev.Session]++
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Subscribe (host-wide): %v", err)
	}
	defer sub.Close()

	if _, err := h.Prompt(context.Background(), s1.Ref(), nil); err != nil {
		t.Fatalf("Prompt s1: %v", err)
	}
	if _, err := h.Prompt(context.Background(), s2.Ref(), nil); err != nil {
		t.Fatalf("Prompt s2: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen[s1.Ref()] != 1 || seen[s2.Ref()] != 1 {
		t.Fatalf("host-wide subscriber saw %v, want exactly one event for each of s1=%v, s2=%v", seen, s1.Ref(), s2.Ref())
	}
}

// TestFakeHost_ConcurrentSessionsAreRaceSafe exercises the SessionOps doc
// contract: "implementations must be safe for concurrent use by multiple
// goroutines across different SessionRef values." Many goroutines each own
// a distinct session and concurrently create it, subscribe, prompt, and
// cancel — run under `go test -race` this must complete without a data race
// or panic, and each goroutine's own outcome content must round-trip
// correctly (proving no cross-session state corruption under concurrency).
func TestFakeHost_ConcurrentSessionsAreRaceSafe(t *testing.T) {
	const n = 32
	h := newConnectedFakeHost(t, "p1")

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			sess, err := h.NewSession(ctx, "p1")
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: NewSession: %w", i, err)
				return
			}

			var got []ContentBlock
			sub, err := h.Subscribe(ctx, sess.Ref(), func(ev Event) {
				if ev.Kind == EventAgentMessage {
					got = ev.Content
				}
			})
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: Subscribe: %w", i, err)
				return
			}
			defer sub.Close()

			want := fmt.Sprintf("payload-%d", i)
			outcome, err := h.Prompt(ctx, sess.Ref(), []ContentBlock{{Text: &TextBlock{Text: want}}})
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: Prompt: %w", i, err)
				return
			}
			if outcome.StopReason != StopReasonEndTurn {
				errs <- fmt.Errorf("goroutine %d: StopReason = %v, want EndTurn", i, outcome.StopReason)
				return
			}
			if len(got) != 1 || got[0].Text == nil || got[0].Text.Text != want {
				errs <- fmt.Errorf("goroutine %d: subscriber saw %+v, want text %q (no cross-session leakage)", i, got, want)
				return
			}
			if err := h.Cancel(ctx, sess.Ref()); err != nil {
				errs <- fmt.Errorf("goroutine %d: Cancel: %w", i, err)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
