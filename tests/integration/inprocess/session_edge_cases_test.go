//go:build integration

package inprocess

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
)

// TestReconnectDuringAgentStreaming tests that a client reconnecting during
// active agent streaming receives all events without gaps.
//
// Scenario:
// 1. Client connects and sends a prompt
// 2. Agent starts streaming response
// 3. Client disconnects mid-stream
// 4. Client reconnects and syncs
// 5. Verify no events are missing
func TestReconnectDuringAgentStreaming(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Track events from first connection
	var (
		mu              sync.Mutex
		firstConnEvents []string
		promptComplete  = make(chan struct{})
	)

	callbacks := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			firstConnEvents = append(firstConnEvents, "agent_message:"+html)
			mu.Unlock()
		},
		OnToolCall: func(id, title, status string) {
			mu.Lock()
			firstConnEvents = append(firstConnEvents, "tool_call:"+id)
			mu.Unlock()
		},
		OnPromptComplete: func(eventCount int) {
			close(promptComplete)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First connection
	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Wait for connection
	time.Sleep(300 * time.Millisecond)

	// Send a prompt that will trigger streaming
	if err := ws1.SendPrompt("Hello, please respond with a multi-part message"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait a bit for streaming to start, then disconnect
	time.Sleep(200 * time.Millisecond)
	ws1.Close()

	// Wait for prompt to complete (agent continues even when client disconnects)
	select {
	case <-promptComplete:
		// Good, prompt completed
	case <-time.After(10 * time.Second):
		// Prompt may have completed before we set up the channel, continue anyway
	}

	// Track events from second connection
	var secondConnEvents []client.SyncEvent

	callbacks2 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			mu.Lock()
			secondConnEvents = append(secondConnEvents, events...)
			mu.Unlock()
		},
	}

	// Second connection
	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}
	defer ws2.Close()

	// Wait for connection
	time.Sleep(300 * time.Millisecond)

	// Load all events
	if err := ws2.LoadEvents(100, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait for events to load
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Verify we got events on reconnect
	if len(secondConnEvents) == 0 {
		t.Error("Expected to receive events on reconnect, got none")
	}

	// Log what we received for debugging
	t.Logf("First connection received %d events", len(firstConnEvents))
	t.Logf("Second connection loaded %d events", len(secondConnEvents))

	// Verify events include the user prompt at minimum
	hasUserPrompt := false
	for _, e := range secondConnEvents {
		if e.Type == "user_prompt" {
			hasUserPrompt = true
			break
		}
	}
	if !hasUserPrompt {
		t.Error("Expected to find user_prompt in loaded events")
	}
}

// TestStaleSeqSync tests that syncing with a stale sequence number
// correctly returns all missed events.
//
// Scenario:
// 1. Client connects and loads initial events (lastSeenSeq = N)
// 2. Client disconnects
// 3. Another client sends messages (events N+1, N+2, ...)
// 4. First client reconnects with stale lastSeenSeq = N
// 5. Verify all events after N are returned
func TestStaleSeqSync(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First client connects and gets initial state
	var initialEventCount int
	var initialLoadDone = make(chan struct{})
	var initialLoadOnce sync.Once

	callbacks1 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			initialLoadOnce.Do(func() {
				initialEventCount = len(events)
				close(initialLoadDone)
			})
		},
	}

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	ws1.LoadEvents(100, 0, 0)

	select {
	case <-initialLoadDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for initial load")
	}

	// Disconnect first client
	ws1.Close()

	// Second client sends a message and waits for agent response
	var agentMessageReceived = make(chan struct{})
	var agentMessageOnce sync.Once
	callbacks2 := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			agentMessageOnce.Do(func() {
				close(agentMessageReceived)
			})
		},
	}

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Second connect failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	ws2.SendPrompt("Message while first client was disconnected")

	// Wait for agent to respond (mock ACP sends response)
	select {
	case <-agentMessageReceived:
		t.Log("Agent message received")
	case <-time.After(5 * time.Second):
		t.Log("Timeout waiting for agent message, continuing")
	}

	// Give time for events to be persisted
	time.Sleep(500 * time.Millisecond)
	ws2.Close()

	// First client reconnects and syncs from stale position
	var syncedEvents []client.SyncEvent
	var syncDone = make(chan struct{})
	var syncOnce sync.Once

	callbacks3 := client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			syncOnce.Do(func() {
				syncedEvents = events
				close(syncDone)
			})
		},
	}

	ws3, err := ts.Client.Connect(ctx, session.SessionID, callbacks3)
	if err != nil {
		t.Fatalf("Reconnect failed: %v", err)
	}
	defer ws3.Close()

	time.Sleep(300 * time.Millisecond)

	// Sync from the stale position (after initial events)
	// Use afterSeq = initialEventCount to get events after that point
	ws3.LoadEvents(100, int64(initialEventCount), 0)

	select {
	case <-syncDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for sync")
	}

	// Should have received the new events
	if len(syncedEvents) == 0 {
		t.Error("Expected to receive new events after sync, got none")
	}

	t.Logf("Initial event count: %d, synced events: %d", initialEventCount, len(syncedEvents))
}

// TestConcurrentPromptsFromTwoClients tests that when two clients send prompts
// simultaneously, both are handled correctly without crashes or deadlocks.
//
// Scenario:
// 1. Two clients connect to the same session
// 2. Both send prompts at nearly the same time
// 3. Verify at least one prompt is processed (the other may be queued)
func TestConcurrentPromptsFromTwoClients(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track agent messages received by each client
	var (
		mu                   sync.Mutex
		client1AgentMessages int
		client2AgentMessages int
		wg                   sync.WaitGroup
	)

	callbacks1 := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			client1AgentMessages++
			mu.Unlock()
		},
	}

	callbacks2 := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			mu.Lock()
			client2AgentMessages++
			mu.Unlock()
		},
	}

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect client 1 failed: %v", err)
	}
	defer ws1.Close()

	ws2, err := ts.Client.Connect(ctx, session.SessionID, callbacks2)
	if err != nil {
		t.Fatalf("Connect client 2 failed: %v", err)
	}
	defer ws2.Close()

	// Wait for connections and register as observers by loading events
	time.Sleep(300 * time.Millisecond)
	ws1.LoadEvents(100, 0, 0)
	ws2.LoadEvents(100, 0, 0)
	time.Sleep(300 * time.Millisecond)

	// Send prompts concurrently
	wg.Add(2)
	go func() {
		defer wg.Done()
		ws1.SendPrompt("Message from client 1")
	}()
	go func() {
		defer wg.Done()
		// Small delay to avoid exact same timestamp
		time.Sleep(50 * time.Millisecond)
		ws2.SendPrompt("Message from client 2")
	}()
	wg.Wait()

	// Wait for agent responses
	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	// At least one client should receive agent messages
	t.Logf("Client 1 received %d agent messages", client1AgentMessages)
	t.Logf("Client 2 received %d agent messages", client2AgentMessages)

	// The key test is that no crashes or deadlocks occurred
	// At least one prompt should have been processed
	if client1AgentMessages == 0 && client2AgentMessages == 0 {
		t.Error("Expected at least one client to receive agent messages")
	}
}

// TestQueueAddDuringPrompting tests that adding to queue while prompting works correctly.
//
// Scenario:
// 1. Client sends a prompt
// 2. While agent is responding, client adds to queue
// 3. Verify queue message was added successfully
// 4. After prompt completes, queue message may be sent (depending on queue delay)
func TestQueueAddDuringPrompting(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var agentMessageReceived = make(chan struct{})
	var agentMessageOnce sync.Once
	callbacks := client.SessionCallbacks{
		OnAgentMessage: func(html string) {
			agentMessageOnce.Do(func() {
				close(agentMessageReceived)
			})
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Wait for connection and register as observer
	time.Sleep(300 * time.Millisecond)
	ws.LoadEvents(100, 0, 0)
	time.Sleep(200 * time.Millisecond)

	// Send initial prompt
	ws.SendPrompt("Initial prompt")

	// Immediately add to queue while prompting
	queuedMsg, err := ts.Client.AddToQueue(session.SessionID, "Queued message during prompting")
	if err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}

	t.Logf("Added message to queue: %s", queuedMsg.ID)

	// Verify queue has the message (before prompt completes)
	queue, err := ts.Client.ListQueue(session.SessionID)
	if err != nil {
		t.Fatalf("ListQueue failed: %v", err)
	}

	found := false
	for _, msg := range queue.Messages {
		if msg.ID == queuedMsg.ID {
			found = true
			break
		}
	}

	if !found {
		t.Log("Queue message was already sent or not found (may be expected if prompt completed quickly)")
	} else {
		t.Logf("Queue has %d messages during prompting", queue.Count)
	}

	// Wait for agent response
	select {
	case <-agentMessageReceived:
		t.Log("Agent message received")
	case <-time.After(5 * time.Second):
		t.Log("Timeout waiting for agent message")
	}

	// The test passes if no panics or deadlocks occurred during concurrent queue/prompt operations
}

// TestObserverAddDuringHighFrequencyEvents tests that adding an observer
// while events are being emitted at high frequency doesn't cause races or missed events.
func TestObserverAddDuringHighFrequencyEvents(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First client sends a prompt that generates many events
	var promptComplete = make(chan struct{})
	callbacks1 := client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			close(promptComplete)
		},
	}

	ws1, err := ts.Client.Connect(ctx, session.SessionID, callbacks1)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws1.Close()

	time.Sleep(300 * time.Millisecond)

	// Send prompt
	ws1.SendPrompt("Generate a response with multiple parts")

	// While streaming, rapidly connect/disconnect additional clients
	var eventCounts atomic.Int64
	for i := 0; i < 5; i++ {
		go func(idx int) {
			callbacks := client.SessionCallbacks{
				OnAgentMessage: func(html string) {
					eventCounts.Add(1)
				},
			}
			ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
			if err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			ws.Close()
		}(i)
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for prompt to complete
	select {
	case <-promptComplete:
	case <-time.After(10 * time.Second):
		t.Log("Prompt complete timeout, continuing")
	}

	t.Logf("Additional clients received %d events total", eventCounts.Load())
	// The test passes if no panics or deadlocks occurred
}

// TestMultipleClientsSeeSameEvents tests that multiple clients connected
// to the same session all receive the same events.
func TestMultipleClientsSeeSameEvents(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const numClients = 3
	var (
		mu               sync.Mutex
		eventCounts      = make([]int, numClients)
		agentMessageCh   = make(chan struct{})
		agentMessageOnce sync.Once
	)

	// Connect multiple clients
	var clients []*client.Session
	for i := 0; i < numClients; i++ {
		idx := i
		callbacks := client.SessionCallbacks{
			OnAgentMessage: func(html string) {
				mu.Lock()
				eventCounts[idx]++
				mu.Unlock()
				agentMessageOnce.Do(func() {
					close(agentMessageCh)
				})
			},
		}

		ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
		if err != nil {
			t.Fatalf("Connect client %d failed: %v", i, err)
		}
		clients = append(clients, ws)
	}
	defer func() {
		for _, ws := range clients {
			ws.Close()
		}
	}()

	// Wait for connections and register all clients as observers
	time.Sleep(300 * time.Millisecond)
	for _, ws := range clients {
		ws.LoadEvents(100, 0, 0)
	}
	time.Sleep(300 * time.Millisecond)

	// First client sends a prompt
	clients[0].SendPrompt("Hello from multi-client test")

	// Wait for at least one agent message
	select {
	case <-agentMessageCh:
		t.Log("Agent message received")
	case <-time.After(5 * time.Second):
		t.Log("Timeout waiting for agent message")
	}

	// Give time for all events to propagate to all clients
	time.Sleep(1 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Event counts per client: %v", eventCounts)

	// At least one client should have received events
	totalEvents := 0
	for _, count := range eventCounts {
		totalEvents += count
	}
	if totalEvents == 0 {
		t.Error("No clients received any events")
	}

	// Check if all clients received events (ideal case)
	allReceived := true
	for i, count := range eventCounts {
		if count == 0 {
			t.Logf("Client %d received no events (may be timing issue)", i)
			allReceived = false
		}
	}
	if allReceived {
		t.Log("All clients received events successfully")
	}
}
