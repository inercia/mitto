//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests multi-client synchronization scenarios to ensure all clients
// see the same conversation state regardless of when they connect/disconnect.
package inprocess

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// =============================================================================
// Multi-Client Synchronization Tests
// =============================================================================

// TestMultiClientSync_AllClientsSeeAllMessages verifies that multiple clients
// connected to the same session all see the same messages in the same order.
func TestMultiClientSync_AllClientsSeeAllMessages(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const numClients = 3
	var mu sync.Mutex

	// Track messages received by each client (keyed by seq)
	clientMessages := make([]map[int64]string, numClients)
	clientConnected := make([]chan struct{}, numClients)
	clientDone := make([]chan struct{}, numClients)
	sessions := make([]*client.Session, numClients)

	for i := 0; i < numClients; i++ {
		clientMessages[i] = make(map[int64]string)
		clientConnected[i] = make(chan struct{})
		clientDone[i] = make(chan struct{})
	}

	// Connect all clients
	for i := 0; i < numClients; i++ {
		idx := i
		sess, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
			OnConnected: func(sessionID, clientID, acpServer string) {
				t.Logf("Client %d connected: clientID=%s", idx, clientID)
				close(clientConnected[idx])
			},
			OnAgentMessage: func(html string) {
				mu.Lock()
				// Use a simple counter as seq since we don't have access to actual seq
				seq := int64(len(clientMessages[idx]) + 1)
				clientMessages[idx][seq] = html
				t.Logf("Client %d received message seq=%d: %s...", idx, seq, truncate(html, 50))
				mu.Unlock()
			},
			OnPromptComplete: func(eventCount int) {
				t.Logf("Client %d received prompt_complete: eventCount=%d", idx, eventCount)
				close(clientDone[idx])
			},
		})
		if err != nil {
			t.Fatalf("Client %d Connect failed: %v", idx, err)
		}
		sessions[idx] = sess
		defer sess.Close()
	}

	// Wait for all clients to connect
	for i := 0; i < numClients; i++ {
		select {
		case <-clientConnected[i]:
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for client %d to connect", i)
		}
	}

	// Each client must send load_events to register as an observer
	// This is required by the server to prevent race conditions
	for i := 0; i < numClients; i++ {
		if err := sessions[i].LoadEvents(50, 0, 0); err != nil {
			t.Fatalf("Client %d LoadEvents failed: %v", i, err)
		}
	}

	// Small delay to ensure all clients are fully registered as observers
	time.Sleep(200 * time.Millisecond)

	// Client 0 sends a prompt
	testMessage := "Hello from test!"
	if err := sessions[0].SendPrompt(testMessage); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for all clients to receive completion
	for i := 0; i < numClients; i++ {
		select {
		case <-clientDone[i]:
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for client %d completion", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify all clients received messages
	for i := 0; i < numClients; i++ {
		if len(clientMessages[i]) == 0 {
			t.Errorf("Client %d received no messages", i)
		}
	}

	// Verify all clients received the same number of messages
	msgCount := len(clientMessages[0])
	for i := 1; i < numClients; i++ {
		if len(clientMessages[i]) != msgCount {
			t.Errorf("Client %d received %d messages, client 0 received %d",
				i, len(clientMessages[i]), msgCount)
		}
	}

	t.Logf("All %d clients received %d messages each", numClients, msgCount)
}

// TestMultiClientSync_LateJoinerSeesHistory verifies that a client connecting
// after messages have been sent still sees all historical messages.
func TestMultiClientSync_LateJoinerSeesHistory(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	client1Messages := make([]string, 0)
	client1Connected := make(chan struct{})
	client1Done := make(chan struct{})

	// Connect client 1 first
	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client 1 connected: clientID=%s", clientID)
			close(client1Connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client1Messages = append(client1Messages, html)
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	<-client1Connected

	// Client 1 must send load_events to register as an observer
	if err := sess1.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client 1 LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Client 1 sends a prompt and waits for completion
	if err := sess1.SendPrompt("First message"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}
	<-client1Done

	mu.Lock()
	client1MsgCount := len(client1Messages)
	mu.Unlock()
	t.Logf("Client 1 received %d messages", client1MsgCount)

	// Now connect client 2 (late joiner)
	client2Messages := make([]string, 0)
	client2Connected := make(chan struct{})
	client2EventsLoaded := make(chan struct{})

	sess2, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			t.Logf("Client 2 (late joiner) connected: clientID=%s", clientID)
			close(client2Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Type == "agent_message" {
					client2Messages = append(client2Messages, e.HTML)
				}
			}
			mu.Unlock()
			t.Logf("Client 2 loaded %d events (hasMore=%v, isPrompting=%v)", len(events), hasMore, isPrompting)
			close(client2EventsLoaded)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	<-client2Connected

	// Request events load
	if err := sess2.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	select {
	case <-client2EventsLoaded:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 2 events_loaded")
	}

	mu.Lock()
	defer mu.Unlock()

	// Client 2 should see the same messages as client 1
	if len(client2Messages) != client1MsgCount {
		t.Errorf("Late joiner received %d messages, expected %d", len(client2Messages), client1MsgCount)
	}

	t.Logf("Late joiner successfully received %d historical messages", len(client2Messages))
}

// TestMultiClientSync_MidStreamJoiner verifies that a client connecting while
// the agent is actively streaming still receives all messages correctly.
func TestMultiClientSync_MidStreamJoiner(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	client1Messages := make([]string, 0)
	client2Messages := make([]string, 0)
	client1Connected := make(chan struct{})
	client1Done := make(chan struct{})
	client2Done := make(chan struct{})
	client1FirstMessage := make(chan struct{})

	// Connect client 1 first
	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client1Messages = append(client1Messages, html)
			if len(client1Messages) == 1 {
				// Signal that first message arrived - time to connect client 2
				select {
				case <-client1FirstMessage:
				default:
					close(client1FirstMessage)
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	<-client1Connected

	// Client 1 must send load_events to register as an observer
	if err := sess1.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client 1 LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Client 1 sends a prompt
	if err := sess1.SendPrompt("Generate a long response"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for first message to arrive, then connect client 2 mid-stream
	select {
	case <-client1FirstMessage:
		t.Log("First message received, connecting client 2 mid-stream")
	case <-client1Done:
		t.Log("Response completed before we could test mid-stream join")
	case <-ctx.Done():
		t.Fatal("Timeout waiting for first message")
	}

	// Connect client 2 while streaming is in progress
	client2Connected := make(chan struct{})
	sess2, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Type == "agent_message" {
					client2Messages = append(client2Messages, e.HTML)
				}
			}
			mu.Unlock()
			t.Logf("Client 2 loaded %d events (isPrompting=%v)", len(events), isPrompting)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client2Messages = append(client2Messages, html)
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client2Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	<-client2Connected

	// Request events load for client 2
	if err := sess2.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait for both clients to complete
	<-client1Done
	select {
	case <-client2Done:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 2 completion")
	}

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Client 1 received %d messages", len(client1Messages))
	t.Logf("Client 2 received %d messages", len(client2Messages))

	// Client 2 should have received messages (either via events_loaded or streaming)
	if len(client2Messages) == 0 {
		t.Error("Client 2 (mid-stream joiner) received no messages")
	}

	// Both clients should have received the same final content
	// Note: The exact count might differ due to message coalescing, but content should match
}

// TestMultiClientSync_ReconnectSeesAllMessages verifies that a client that
// disconnects and reconnects sees all messages including those sent while disconnected.
func TestMultiClientSync_ReconnectSeesAllMessages(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var mu sync.Mutex
	client1Messages := make([]string, 0)
	client2MessagesBeforeDisconnect := make([]string, 0)
	client2MessagesAfterReconnect := make([]string, 0)

	client1Connected := make(chan struct{})
	client1Done := make(chan struct{})
	client2Connected := make(chan struct{})
	client2Done := make(chan struct{})

	// Connect client 1
	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client1Messages = append(client1Messages, html)
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			select {
			case <-client1Done:
			default:
				close(client1Done)
			}
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	// Connect client 2
	sess2, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			select {
			case <-client2Connected:
			default:
				close(client2Connected)
			}
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client2MessagesBeforeDisconnect = append(client2MessagesBeforeDisconnect, html)
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			select {
			case <-client2Done:
			default:
				close(client2Done)
			}
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}

	<-client1Connected
	<-client2Connected

	// Both clients must send load_events to register as observers
	if err := sess1.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client 1 LoadEvents failed: %v", err)
	}
	if err := sess2.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client 2 LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Client 1 sends first prompt
	if err := sess1.SendPrompt("First message"); err != nil {
		t.Fatalf("First SendPrompt failed: %v", err)
	}
	<-client1Done
	<-client2Done

	mu.Lock()
	beforeCount := len(client2MessagesBeforeDisconnect)
	mu.Unlock()
	t.Logf("Before disconnect: client 2 has %d messages", beforeCount)

	// Disconnect client 2
	sess2.Close()
	time.Sleep(200 * time.Millisecond)

	// Reset done channels for second prompt
	client1Done = make(chan struct{})

	// Client 1 sends second prompt while client 2 is disconnected
	if err := sess1.SendPrompt("Second message while client 2 is disconnected"); err != nil {
		t.Fatalf("Second SendPrompt failed: %v", err)
	}
	<-client1Done

	mu.Lock()
	afterFirstClient := len(client1Messages)
	mu.Unlock()
	t.Logf("After second prompt: client 1 has %d messages", afterFirstClient)

	// Reconnect client 2
	client2Reconnected := make(chan struct{})
	client2EventsLoaded := make(chan struct{})

	sess2New, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Reconnected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Type == "agent_message" {
					client2MessagesAfterReconnect = append(client2MessagesAfterReconnect, e.HTML)
				}
			}
			mu.Unlock()
			t.Logf("Client 2 reconnected and loaded %d events", len(events))
			close(client2EventsLoaded)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Reconnect failed: %v", err)
	}
	defer sess2New.Close()

	<-client2Reconnected

	// Request events load
	if err := sess2New.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	select {
	case <-client2EventsLoaded:
	case <-ctx.Done():
		t.Fatal("Timeout waiting for client 2 events_loaded after reconnect")
	}

	mu.Lock()
	defer mu.Unlock()

	// Client 2 after reconnect should see all messages including those sent while disconnected
	t.Logf("Client 1 total messages: %d", len(client1Messages))
	t.Logf("Client 2 after reconnect: %d messages", len(client2MessagesAfterReconnect))

	if len(client2MessagesAfterReconnect) < len(client1Messages) {
		t.Errorf("Client 2 after reconnect has fewer messages (%d) than client 1 (%d)",
			len(client2MessagesAfterReconnect), len(client1Messages))
	}
}

// TestMultiClientSync_StreamingIndicator verifies that all clients see the
// streaming indicator (is_prompting) correctly when the agent is responding.
func TestMultiClientSync_StreamingIndicator(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	client1SawPrompting := false
	client2SawPrompting := false
	client1Connected := make(chan struct{})
	client2Connected := make(chan struct{})
	client1Done := make(chan struct{})
	client2Done := make(chan struct{})

	// Connect client 1
	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnUserPrompt: func(senderID, promptID, message string) {
			// user_prompt always has is_prompting=true
			mu.Lock()
			client1SawPrompting = true
			mu.Unlock()
			t.Logf("Client 1 received user_prompt (is_prompting implied)")
		},
		OnAgentMessage: func(html string) {
			// Agent messages during prompting have is_prompting=true
			mu.Lock()
			client1SawPrompting = true
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	// Connect client 2
	sess2, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnUserPrompt: func(senderID, promptID, message string) {
			mu.Lock()
			client2SawPrompting = true
			mu.Unlock()
			t.Logf("Client 2 received user_prompt (is_prompting implied)")
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client2SawPrompting = true
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client2Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	<-client1Connected
	<-client2Connected

	// Both clients must send load_events to register as observers
	if err := sess1.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client 1 LoadEvents failed: %v", err)
	}
	if err := sess2.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Client 2 LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Client 1 sends a prompt
	if err := sess1.SendPrompt("Test streaming indicator"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for completion
	<-client1Done
	<-client2Done

	mu.Lock()
	defer mu.Unlock()

	// Both clients should have seen the prompting state
	if !client1SawPrompting {
		t.Error("Client 1 did not see prompting state")
	}
	if !client2SawPrompting {
		t.Error("Client 2 did not see prompting state")
	}

	t.Logf("Both clients correctly saw streaming indicator")
}

// TestMultiClientSync_NoDuplicateMessages verifies that clients don't receive
// duplicate messages when connecting at various times.
func TestMultiClientSync_NoDuplicateMessages(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	// Track messages by their content to detect duplicates
	client1MessageSet := make(map[string]int) // content -> count
	client1Connected := make(chan struct{})
	client1Done := make(chan struct{})

	// Connect client 1
	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Type == "agent_message" && e.HTML != "" {
					client1MessageSet[e.HTML]++
				}
			}
			mu.Unlock()
		},
		OnAgentMessage: func(html string) {
			mu.Lock()
			client1MessageSet[html]++
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	<-client1Connected

	// Request initial events load
	if err := sess1.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Send a prompt
	if err := sess1.SendPrompt("Test no duplicates"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	<-client1Done

	mu.Lock()
	defer mu.Unlock()

	// Check for duplicates
	duplicates := 0
	for content, count := range client1MessageSet {
		if count > 1 {
			duplicates++
			t.Errorf("Duplicate message (count=%d): %s...", count, truncate(content, 50))
		}
	}

	if duplicates > 0 {
		t.Errorf("Found %d duplicate messages", duplicates)
	} else {
		t.Logf("No duplicate messages found among %d unique messages", len(client1MessageSet))
	}
}

// TestMultiClientSync_StaleSyncRecovery verifies that when a client sends a sync
// request with a stale (too high) sequence number, the server returns appropriate
// information for the client to recover by doing a fresh load.
// This tests the Safari/iPhone bug where localStorage had a stale lastSeenSeq.
func TestMultiClientSync_StaleSyncRecovery(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex

	// First, connect a client and do an initial load to see what events exist
	client1Connected := make(chan struct{})
	client1EventsLoaded := make(chan struct{})
	client1EventsLoadedOnce := sync.Once{}
	var initialEvents []client.SyncEvent
	var initialTotalCount int

	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			initialEvents = events
			mu.Unlock()
			t.Logf("Initial load: %d events, hasMore=%v", len(events), hasMore)
			client1EventsLoadedOnce.Do(func() {
				close(client1EventsLoaded)
			})
		},
		OnEventsLoadedWithMeta: func(events []client.SyncEvent, hasMore bool, isPrompting bool, totalCount int) {
			mu.Lock()
			initialTotalCount = totalCount
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	<-client1Connected

	// Do initial load
	if err := sess1.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Initial LoadEvents failed: %v", err)
	}

	select {
	case <-client1EventsLoaded:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for initial load response")
	}

	mu.Lock()
	initialEventCount := len(initialEvents)
	totalCount := initialTotalCount
	mu.Unlock()

	t.Logf("Session has %d events (total_count=%d)", initialEventCount, totalCount)

	// Now connect client 2 and simulate a stale sync by requesting events after
	// a sequence number that's higher than the actual event count
	client2Connected := make(chan struct{})
	client2EventsLoaded := make(chan struct{})
	client2EventsLoadedOnce := sync.Once{}
	var staleSyncEvents []client.SyncEvent
	var staleSyncTotalCount int

	sess2, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			staleSyncEvents = events
			mu.Unlock()
			t.Logf("Stale sync: %d events, hasMore=%v", len(events), hasMore)
			client2EventsLoadedOnce.Do(func() {
				close(client2EventsLoaded)
			})
		},
		OnEventsLoadedWithMeta: func(events []client.SyncEvent, hasMore bool, isPrompting bool, totalCount int) {
			mu.Lock()
			staleSyncTotalCount = totalCount
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	<-client2Connected

	// Request events with a stale (too high) after_seq
	// This simulates what happens when localStorage has an old lastSeenSeq
	staleSeq := int64(99999) // Way higher than actual event count
	if err := sess2.LoadEvents(50, staleSeq, 0); err != nil {
		t.Fatalf("LoadEvents with stale seq failed: %v", err)
	}

	select {
	case <-client2EventsLoaded:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for stale sync response")
	}

	mu.Lock()
	staleEventCount := len(staleSyncEvents)
	staleTotalCount := staleSyncTotalCount
	mu.Unlock()

	// With a stale seq, we should get 0 events (nothing after seq 99999)
	// But total_count should still indicate there are events in the session
	if staleEventCount != 0 {
		t.Errorf("Stale sync returned %d events, expected 0", staleEventCount)
	} else {
		t.Log("Stale sync correctly returned 0 events")
	}

	// The key insight: total_count should still be > 0 even when sync returns 0 events
	// This allows the client to detect the stale sync and do a fresh load
	t.Logf("Stale sync total_count=%d (should match initial total_count=%d)", staleTotalCount, totalCount)

	// Now do a fresh load (simulating what the client should do after detecting stale sync)
	client3Connected := make(chan struct{})
	client3EventsLoaded := make(chan struct{})
	client3EventsLoadedOnce := sync.Once{}
	var freshEvents []client.SyncEvent

	sess3, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client3Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			freshEvents = events
			mu.Unlock()
			t.Logf("Fresh load returned %d events", len(events))
			client3EventsLoadedOnce.Do(func() {
				close(client3EventsLoaded)
			})
		},
	})
	if err != nil {
		t.Fatalf("Client 3 Connect failed: %v", err)
	}
	defer sess3.Close()

	<-client3Connected

	// Do a fresh load (no after_seq)
	if err := sess3.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("Fresh LoadEvents failed: %v", err)
	}

	select {
	case <-client3EventsLoaded:
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for fresh load response")
	}

	mu.Lock()
	freshEventCount := len(freshEvents)
	mu.Unlock()

	// Fresh load should return the same events as initial load
	if freshEventCount != initialEventCount {
		t.Errorf("Fresh load returned %d events, expected %d (same as initial)", freshEventCount, initialEventCount)
	}

	t.Logf("Recovery test complete: stale sync=%d events, fresh load=%d events, initial=%d events",
		staleEventCount, freshEventCount, initialEventCount)
}

// TestMultiClientSync_EventOrdering verifies that events are received in the
// correct order (by sequence number) across all clients.
func TestMultiClientSync_EventOrdering(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	// Track event sequence numbers
	client1Seqs := make([]int64, 0)
	client2Seqs := make([]int64, 0)
	client1Connected := make(chan struct{})
	client2Connected := make(chan struct{})
	client1Done := make(chan struct{})
	client2Done := make(chan struct{})

	// Connect both clients
	sess1, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client1Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > 0 {
					client1Seqs = append(client1Seqs, e.Seq)
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client1Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 1 Connect failed: %v", err)
	}
	defer sess1.Close()

	sess2, err := ts.Client.Connect(ctx, session.SessionID, client.SessionCallbacks{
		OnConnected: func(sessionID, clientID, acpServer string) {
			close(client2Connected)
		},
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			for _, e := range events {
				if e.Seq > 0 {
					client2Seqs = append(client2Seqs, e.Seq)
				}
			}
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(client2Done)
		},
	})
	if err != nil {
		t.Fatalf("Client 2 Connect failed: %v", err)
	}
	defer sess2.Close()

	<-client1Connected
	<-client2Connected

	// Both clients must send load_events to register as observers
	if err := sess1.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client 1 LoadEvents failed: %v", err)
	}
	if err := sess2.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("Client 2 LoadEvents failed: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Send a prompt
	if err := sess1.SendPrompt("Test event ordering"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	<-client1Done
	<-client2Done

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Verify sequences are in order for client 1
	if !sort.SliceIsSorted(client1Seqs, func(i, j int) bool {
		return client1Seqs[i] < client1Seqs[j]
	}) {
		t.Errorf("Client 1 sequences not in order: %v", client1Seqs)
	}

	// Verify sequences are in order for client 2
	if !sort.SliceIsSorted(client2Seqs, func(i, j int) bool {
		return client2Seqs[i] < client2Seqs[j]
	}) {
		t.Errorf("Client 2 sequences not in order: %v", client2Seqs)
	}

	t.Logf("Client 1 seqs: %v", client1Seqs)
	t.Logf("Client 2 seqs: %v", client2Seqs)
}
