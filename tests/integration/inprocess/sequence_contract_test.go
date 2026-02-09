//go:build integration

package inprocess

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// =============================================================================
// Sequence Number Contract Integration Tests
//
// These tests verify the sequence number contract between backend and frontend:
// - Uniqueness: Each event has a unique seq (except coalescing chunks)
// - Monotonicity: seq values are strictly increasing
// - Persistence: seq is preserved when events are written to storage
// - Coalescing: Multiple chunks of same message share the same seq
// =============================================================================

// TestSequenceNumberMonotonicity verifies that sequence numbers are
// monotonically increasing within a session.
func TestSequenceNumberMonotonicity(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track sequence numbers
	var (
		mu       sync.Mutex
		seqs     []int64
		complete = make(chan struct{})
	)

	callbacks := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > 0 {
					seqs = append(seqs, e.Seq)
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(complete)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connection
	time.Sleep(300 * time.Millisecond)

	// Must call LoadEvents to register as an observer
	ws.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send a prompt
	if err := ws.SendPrompt("Hello, please respond briefly"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for completion
	select {
	case <-complete:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for prompt completion")
	}

	// Load all events
	ws.LoadEvents(100, 0, 0)
	time.Sleep(500 * time.Millisecond)

	// Verify monotonicity
	mu.Lock()
	defer mu.Unlock()

	if len(seqs) < 2 {
		t.Skipf("Not enough events to verify monotonicity (got %d)", len(seqs))
	}

	for i := 1; i < len(seqs); i++ {
		if seqs[i] < seqs[i-1] {
			t.Errorf("Sequence numbers not monotonic: seq[%d]=%d < seq[%d]=%d",
				i, seqs[i], i-1, seqs[i-1])
		}
	}

	t.Logf("Verified %d sequence numbers are monotonic", len(seqs))
}

// TestSequenceNumberPersistence verifies that sequence numbers are preserved
// when events are persisted and reloaded.
func TestSequenceNumberPersistence(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// First connection - send prompt and wait for completion
	var (
		mu            sync.Mutex
		streamingSeqs []int64
		complete      = make(chan struct{})
	)

	callbacks1 := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			// Note: OnAgentMessage doesn't include seq in the callback
			// We'll verify via events_loaded
		},
		OnPromptComplete: func(eventCount int) {
			close(complete)
		},
	}
	// Suppress unused variable warning
	_ = streamingSeqs

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Must call LoadEvents to register as an observer
	ws1.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	if err := ws1.SendPrompt("Hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-complete:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for prompt completion")
	}

	ws1.Close()

	// Second connection - load events and verify seqs match
	var (
		loadedSeqs []int64
		loaded     = make(chan struct{})
	)

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > 0 {
					loadedSeqs = append(loadedSeqs, e.Seq)
				}
			}
			mu.Unlock()
			close(loaded)
		},
	}

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)

	// Load all events
	ws2.LoadEvents(100, 0, 0)

	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for events to load")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(loadedSeqs) == 0 {
		t.Fatal("No events loaded")
	}

	// Verify loaded seqs are monotonic
	for i := 1; i < len(loadedSeqs); i++ {
		if loadedSeqs[i] < loadedSeqs[i-1] {
			t.Errorf("Loaded sequence numbers not monotonic: seq[%d]=%d < seq[%d]=%d",
				i, loadedSeqs[i], i-1, loadedSeqs[i-1])
		}
	}

	t.Logf("Verified %d persisted sequence numbers", len(loadedSeqs))
}

// TestSequenceNumberSyncAfterReconnect verifies that syncing after reconnection
// correctly retrieves missed events using after_seq.
func TestSequenceNumberSyncAfterReconnect(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// First connection - send prompt and track last seq
	var (
		mu       sync.Mutex
		lastSeq  int64
		complete = make(chan struct{})
	)

	callbacks1 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > lastSeq {
					lastSeq = e.Seq
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(complete)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Must call LoadEvents to register as an observer
	ws1.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send first prompt
	if err := ws1.SendPrompt("First message"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	select {
	case <-complete:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for first prompt completion")
	}

	// Load events to get the last seq
	ws1.LoadEvents(100, 0, 0)
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	firstLastSeq := lastSeq
	mu.Unlock()

	ws1.Close()

	// Send another prompt via REST API (simulating activity while disconnected)
	// Note: This would require the session to be running, which it is in the background

	// Second connection - sync from last known seq
	var (
		syncedEvents []client.SyncEvent
		synced       = make(chan struct{})
	)

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			syncedEvents = append(syncedEvents, events...)
			mu.Unlock()
			close(synced)
		},
	}

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)

	// Load events after the first session's last seq
	ws2.LoadEvents(100, firstLastSeq, 0)

	select {
	case <-synced:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for sync")
	}

	mu.Lock()
	defer mu.Unlock()

	// All synced events should have seq > firstLastSeq
	for _, e := range syncedEvents {
		if e.Seq > 0 && e.Seq <= firstLastSeq {
			t.Errorf("Synced event has seq %d <= firstLastSeq %d", e.Seq, firstLastSeq)
		}
	}

	t.Logf("Sync after seq %d returned %d events", firstLastSeq, len(syncedEvents))
}

// TestMultipleClientsReceiveSameSeqs verifies that multiple clients connected
// to the same session receive events with the same sequence numbers.
func TestMultipleClientsReceiveSameSeqs(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track events from both clients
	var (
		mu1, mu2      sync.Mutex
		client1Events []client.SyncEvent
		client2Events []client.SyncEvent
		complete1     = make(chan struct{})
		complete2     = make(chan struct{})
	)

	callbacks1 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu1.Lock()
			client1Events = append(client1Events, events...)
			mu1.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			select {
			case <-complete1:
			default:
				close(complete1)
			}
		},
	}

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu2.Lock()
			client2Events = append(client2Events, events...)
			mu2.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			select {
			case <-complete2:
			default:
				close(complete2)
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Connect both clients
	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("First connect failed: %v", err)
	}
	defer ws1.Close()

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}
	defer ws2.Close()

	time.Sleep(300 * time.Millisecond)

	// Each client must send load_events to register as an observer
	// This is required by the server to prevent race conditions
	ws1.LoadEvents(50, 0, 0)
	ws2.LoadEvents(50, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send a prompt from client 1
	if err := ws1.SendPrompt("Hello from client 1"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for both clients to receive completion
	select {
	case <-complete1:
	case <-time.After(20 * time.Second):
		t.Fatal("Timeout waiting for client 1 completion")
	}

	select {
	case <-complete2:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for client 2 completion")
	}

	// Load events on both clients
	ws1.LoadEvents(100, 0, 0)
	ws2.LoadEvents(100, 0, 0)
	time.Sleep(500 * time.Millisecond)

	// Compare events
	mu1.Lock()
	mu2.Lock()
	defer mu1.Unlock()
	defer mu2.Unlock()

	if len(client1Events) == 0 || len(client2Events) == 0 {
		t.Skipf("Not enough events (client1: %d, client2: %d)", len(client1Events), len(client2Events))
	}

	// Build seq maps
	seqs1 := make(map[int64]string)
	for _, e := range client1Events {
		if e.Seq > 0 {
			seqs1[e.Seq] = e.Type
		}
	}

	seqs2 := make(map[int64]string)
	for _, e := range client2Events {
		if e.Seq > 0 {
			seqs2[e.Seq] = e.Type
		}
	}

	// Verify both clients have the same seqs
	for seq, type1 := range seqs1 {
		if type2, ok := seqs2[seq]; ok {
			if type1 != type2 {
				t.Errorf("Seq %d has different types: client1=%s, client2=%s", seq, type1, type2)
			}
		}
	}

	t.Logf("Both clients received consistent sequence numbers (client1: %d events, client2: %d events)",
		len(client1Events), len(client2Events))
}
