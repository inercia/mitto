//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// extractPromptName extracts the prompt_name field from a SyncEvent's Data.
// Returns "" if the field is absent or Data is not a map.
func extractPromptName(e client.SyncEvent) string {
	dataMap, ok := e.Data.(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := dataMap["prompt_name"].(string)
	return name
}

// TestGapFill_UserPromptWithPromptName verifies that when a user_prompt event
// with prompt_name (simulating a periodic prompt) is missed and later recovered
// via load_events, the prompt_name field survives the store round-trip.
func TestGapFill_UserPromptWithPromptName(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	inj := NewEventInjector(t, ts, sess.SessionID)

	// Phase 1: Inject initial history and load it.
	_, initialLastSeq := inj.InjectMixed(2) // 4 events: seq 1-4

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu            sync.Mutex
		syncedEvents  []client.SyncEvent
		loadCallCount int
		initialLoaded = make(chan struct{}, 1)
		syncLoaded    = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, _ bool, _ bool) {
			mu.Lock()
			loadCallCount++
			count := loadCallCount
			if count == 2 {
				syncedEvents = append(syncedEvents, events...)
			}
			mu.Unlock()

			switch count {
			case 1:
				select {
				case initialLoaded <- struct{}{}:
				default:
				}
			case 2:
				select {
				case syncLoaded <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("initial LoadEvents failed: %v", err)
	}
	select {
	case <-initialLoaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initial events_loaded")
	}

	clientMaxSeq := initialLastSeq

	// Phase 2: Inject missed events — a periodic prompt + agent response.
	promptSeq := inj.InjectUserPromptWithName("Run daily health check", "daily-check")
	inj.InjectAgentMessages(1) // agent response

	// Phase 3: Sync to recover missed events.
	if err := ws.LoadEvents(100, clientMaxSeq, 0); err != nil {
		t.Fatalf("sync LoadEvents failed: %v", err)
	}
	select {
	case <-syncLoaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for sync events_loaded")
	}

	mu.Lock()
	recovered := make([]client.SyncEvent, len(syncedEvents))
	copy(recovered, syncedEvents)
	mu.Unlock()

	// ASSERT 1: We recovered exactly 2 events (user_prompt + agent_message).
	if len(recovered) != 2 {
		t.Fatalf("expected 2 recovered events, got %d", len(recovered))
	}

	// ASSERT 2: First event is user_prompt with prompt_name preserved.
	if recovered[0].Type != "user_prompt" {
		t.Errorf("event[0]: expected type user_prompt, got %s", recovered[0].Type)
	}
	if recovered[0].Seq != promptSeq {
		t.Errorf("event[0]: expected seq %d, got %d", promptSeq, recovered[0].Seq)
	}
	gotName := extractPromptName(recovered[0])
	if gotName != "daily-check" {
		t.Errorf("event[0]: expected prompt_name=%q, got %q", "daily-check", gotName)
	}

	// ASSERT 3: Second event is agent_message.
	if recovered[1].Type != "agent_message" {
		t.Errorf("event[1]: expected type agent_message, got %s", recovered[1].Type)
	}

	// ASSERT 4: Events are in seq order (user_prompt before agent_message).
	if recovered[0].Seq >= recovered[1].Seq {
		t.Errorf("events out of order: user_prompt seq=%d >= agent_message seq=%d",
			recovered[0].Seq, recovered[1].Seq)
	}

	t.Logf("Gap-fill delivered user_prompt with prompt_name=%q (seq=%d) ✓",
		gotName, recovered[0].Seq)
}

// TestGapFill_MultiplePeriodicPromptsWithSameText verifies that multiple periodic
// prompts with identical message text but different seqs are all delivered by the
// server (no server-side content dedup). This is the backend counterpart of the
// mergeMessagesWithSync fix in lib.js.
func TestGapFill_MultiplePeriodicPromptsWithSameText(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	inj := NewEventInjector(t, ts, sess.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu           sync.Mutex
		loadedEvents []client.SyncEvent
		loaded       = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, _ bool, _ bool) {
			mu.Lock()
			loadedEvents = append(loadedEvents, events...)
			mu.Unlock()
			select {
			case loaded <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Inject 3 periodic prompts with identical text (simulates 3 scheduled runs).
	seq1 := inj.InjectUserPromptWithName("Run scheduled task", "cron-job")
	seq2 := inj.InjectUserPromptWithName("Run scheduled task", "cron-job")
	seq3 := inj.InjectUserPromptWithName("Run scheduled task", "cron-job")

	// Load all events.
	if err := ws.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for events_loaded")
	}

	mu.Lock()
	events := make([]client.SyncEvent, len(loadedEvents))
	copy(events, loadedEvents)
	mu.Unlock()

	// Filter to only user_prompt events (CreateSession may add a session_start event).
	var prompts []client.SyncEvent
	for _, e := range events {
		if e.Type == "user_prompt" {
			prompts = append(prompts, e)
		}
	}

	// ASSERT 1: All 3 periodic prompts are returned (no server-side content dedup).
	if len(prompts) != 3 {
		t.Fatalf("expected 3 user_prompt events, got %d (total events: %d)", len(prompts), len(events))
	}

	// ASSERT 2: Each has a distinct seq.
	seqs := map[int64]bool{}
	for _, e := range prompts {
		if seqs[e.Seq] {
			t.Errorf("duplicate seq %d — server incorrectly deduplicated events", e.Seq)
		}
		seqs[e.Seq] = true
	}
	if !seqs[seq1] || !seqs[seq2] || !seqs[seq3] {
		t.Errorf("expected seqs %d, %d, %d; got %v", seq1, seq2, seq3, seqs)
	}

	// ASSERT 3: All events have prompt_name preserved.
	for i, e := range prompts {
		name := extractPromptName(e)
		if name != "cron-job" {
			t.Errorf("prompt[%d] seq=%d: expected prompt_name=%q, got %q", i, e.Seq, "cron-job", name)
		}
	}

	t.Logf("All 3 periodic prompts with identical text delivered with distinct seqs ✓")
}

// TestGapFill_PromptNameSurvivesFullReload verifies the simplest path:
// events with prompt_name are correctly returned on a fresh initial load
// (limit=50, no after_seq). This tests the store → handleLoadEvents →
// events_loaded → client serialization path.
func TestGapFill_PromptNameSurvivesFullReload(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	inj := NewEventInjector(t, ts, sess.SessionID)

	// Inject events directly — no client connected yet.
	promptSeq := inj.InjectUserPromptWithName("Nightly build check", "nightly-build")
	inj.InjectAgentMessages(1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu           sync.Mutex
		loadedEvents []client.SyncEvent
		loaded       = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, _ bool, _ bool) {
			mu.Lock()
			loadedEvents = append(loadedEvents, events...)
			mu.Unlock()
			select {
			case loaded <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Fresh load (simulates page reload).
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for events_loaded")
	}

	mu.Lock()
	events := make([]client.SyncEvent, len(loadedEvents))
	copy(events, loadedEvents)
	mu.Unlock()

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	// Find the user_prompt event.
	var found bool
	for _, e := range events {
		if e.Type == "user_prompt" && e.Seq == promptSeq {
			name := extractPromptName(e)
			if name != "nightly-build" {
				t.Errorf("prompt_name=%q, want %q", name, "nightly-build")
			} else {
				t.Logf("Full reload delivered user_prompt with prompt_name=%q (seq=%d) ✓", name, e.Seq)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("user_prompt with seq=%d not found in %d loaded events", promptSeq, len(events))
	}
}

// TestGapFill_PromptNameEmptyForAdHocPrompts verifies that ad-hoc prompts
// (without prompt_name) are distinguishable from periodic prompts in the
// same event stream.
func TestGapFill_PromptNameEmptyForAdHocPrompts(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)

	inj := NewEventInjector(t, ts, sess.SessionID)

	// Inject an ad-hoc prompt (no prompt_name) and a periodic prompt.
	inj.InjectUserPrompts(1)                                              // seq 1: ad-hoc
	inj.InjectUserPromptWithName("Weekly status report", "weekly-report") // seq 2: periodic

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu           sync.Mutex
		loadedEvents []client.SyncEvent
		loaded       = make(chan struct{}, 1)
	)

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, _ bool, _ bool) {
			mu.Lock()
			loadedEvents = append(loadedEvents, events...)
			mu.Unlock()
			select {
			case loaded <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for events_loaded")
	}

	mu.Lock()
	events := make([]client.SyncEvent, len(loadedEvents))
	copy(events, loadedEvents)
	mu.Unlock()

	// Filter to only user_prompt events (CreateSession may add a session_start event).
	var prompts []client.SyncEvent
	for _, e := range events {
		if e.Type == "user_prompt" {
			prompts = append(prompts, e)
		}
	}

	if len(prompts) != 2 {
		t.Fatalf("expected 2 user_prompt events, got %d (total events: %d)", len(prompts), len(events))
	}

	// ASSERT: First prompt (ad-hoc) has no prompt_name.
	adHocName := extractPromptName(prompts[0])
	if adHocName != "" {
		t.Errorf("ad-hoc prompt[0]: expected empty prompt_name, got %q", adHocName)
	}

	// ASSERT: Second prompt (periodic) has prompt_name.
	periodicName := extractPromptName(prompts[1])
	if periodicName != "weekly-report" {
		t.Errorf("periodic prompt[1]: expected prompt_name=%q, got %q", "weekly-report", periodicName)
	}

	t.Logf("Ad-hoc prompt_name=%q, periodic prompt_name=%q — correctly distinguished ✓",
		adHocName, periodicName)
}
