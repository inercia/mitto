//go:build integration

package inprocess

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/web"
)

// safeErrorCollector is a thread-safe error message collector for tests.
type safeErrorCollector struct {
	mu     sync.Mutex
	errors []string
}

func (c *safeErrorCollector) add(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, msg)
}

func (c *safeErrorCollector) contains(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.errors {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// containsSince checks if any message added since the given index contains the substring.
// Returns true if found, and the current length of the errors slice.
func (c *safeErrorCollector) containsSince(since int, substr string) (bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := since; i < len(c.errors); i++ {
		if strings.Contains(c.errors[i], substr) {
			return true, len(c.errors)
		}
	}
	return false, len(c.errors)
}

func (c *safeErrorCollector) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.errors)
}

func (c *safeErrorCollector) copy() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]string, len(c.errors))
	copy(result, c.errors)
	return result
}

// TestACPRestart_SingleCrash tests that the ACP process restarts after a single crash.
func TestACPRestart_SingleCrash(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Connect WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errorCollector := &safeErrorCollector{}
	callbacks := client.SessionCallbacks{
		OnError: func(msg string) {
			errorCollector.add(msg)
			t.Logf("Error: %s", msg)
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Load events to register as observer
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Send a prompt that triggers a crash in the mock ACP server
	// The mock server will exit with code 1 when it sees "CRASH"
	if err := ws.SendPrompt("CRASH"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for restart notification
	waitFor(t, 10*time.Second, func() bool {
		return errorCollector.contains("Restarting") && errorCollector.contains("attempt 1 of 3")
	}, "restart notification")

	// Verify we got the restart message
	if !errorCollector.contains("AI agent restarted") {
		t.Error("Expected 'AI agent restarted' message")
	}
}

// TestACPRestart_RateLimiting tests that restarts are rate-limited to MaxACPRestarts per window.
// After exceeding the limit, the user sees a "keeps crashing" message instead of another restart.
func TestACPRestart_RateLimiting(t *testing.T) {
	ts := SetupTestServer(t)

	// Create a session
	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Connect WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	errorCollector := &safeErrorCollector{}
	callbacks := client.SessionCallbacks{
		OnError: func(msg string) {
			errorCollector.add(msg)
			t.Logf("Error: %s", msg)
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Trigger 4 crashes, waiting for each restart to complete before the next.
	// The backoff delay (3s base with exponential increase) means we must wait
	// for the full cycle, not just a fixed sleep.
	for i := 1; i <= 4; i++ {
		t.Logf("Triggering crash %d", i)

		// Mark the current position in the error log before sending the crash
		startIdx := errorCollector.len()

		if err := ws.SendPrompt(fmt.Sprintf("CRASH_%d", i)); err != nil {
			t.Logf("SendPrompt %d failed (expected): %v", i, err)
		}

		if i <= 3 {
			// First 3 should restart. Wait for the "Restarting" notification.
			attemptMsg := fmt.Sprintf("attempt %d of 3", i)
			waitFor(t, 15*time.Second, func() bool {
				found, _ := errorCollector.containsSince(startIdx, "Restarting")
				foundAttempt, _ := errorCollector.containsSince(startIdx, attemptMsg)
				return found && foundAttempt
			}, fmt.Sprintf("crash %d: restart notification with 'attempt %d of 3'", i, i))

			// Wait for the restart to complete before triggering the next crash.
			// This is critical because restartACPProcess applies exponential backoff
			// (3s, 6s, 12s) before actually starting the new process.
			waitFor(t, 30*time.Second, func() bool {
				found, _ := errorCollector.containsSince(startIdx, "AI agent restarted")
				return found
			}, fmt.Sprintf("crash %d: restart completion", i))
		} else {
			// 4th should hit the limit
			waitFor(t, 15*time.Second, func() bool {
				found, _ := errorCollector.containsSince(startIdx, "keeps crashing")
				return found
			}, "crash 4: 'keeps crashing' message after hitting restart limit")
		}
	}
}

// TestACPRestart_BackoffDelays tests that exponential backoff is applied between restarts.
// The backoff happens inside restartACPProcess, AFTER the "Restarting" notification is sent.
// So we measure the full crash-to-restart-complete cycle time, which includes the backoff.
func TestACPRestart_BackoffDelays(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	errorCollector := &safeErrorCollector{}
	callbacks := client.SessionCallbacks{
		OnError: func(msg string) {
			errorCollector.add(msg)
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Trigger 3 crashes and measure the full crash→restart-complete cycle time.
	// The backoff delay is applied inside restartACPProcess:
	//   Restart 1: no delay (recentCount=0)
	//   Restart 2: ~3s delay (base delay, recentCount=1)
	//   Restart 3: ~6s delay (2x base, recentCount=2)
	var cycleDurations []time.Duration

	for i := 1; i <= 3; i++ {
		// Mark the current position in the error log before sending the crash
		startIdx := errorCollector.len()

		start := time.Now()
		if err := ws.SendPrompt(fmt.Sprintf("CRASH_%d", i)); err != nil {
			t.Logf("SendPrompt %d failed (expected): %v", i, err)
		}

		// Wait for restart to complete (includes backoff delay)
		waitFor(t, 20*time.Second, func() bool {
			found, _ := errorCollector.containsSince(startIdx, "AI agent restarted")
			return found
		}, fmt.Sprintf("restart %d completion", i))

		duration := time.Since(start)
		cycleDurations = append(cycleDurations, duration)
		t.Logf("Restart %d full cycle took %v", i, duration)
	}

	// Verify backoff: restart 2 should take noticeably longer than restart 1
	// because restart 2 has a ~3s backoff delay.
	if len(cycleDurations) >= 2 {
		t.Logf("Cycle 1: %v, Cycle 2: %v", cycleDurations[0], cycleDurations[1])

		// Restart 2 should take at least 2s longer than restart 1 (base delay is 3s, minus jitter)
		if cycleDurations[1] < cycleDurations[0]+2*time.Second {
			t.Errorf("Expected restart 2 cycle (%v) to be at least 2s longer than restart 1 cycle (%v) due to backoff",
				cycleDurations[1], cycleDurations[0])
		}
	}
}

// TestACPRestart_ReasonTracking tests that restart reasons are tracked correctly.
func TestACPRestart_ReasonTracking(t *testing.T) {
	ts := SetupTestServer(t)

	session, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	defer ts.Client.DeleteSession(session.SessionID)

	// Get the BackgroundSession to check restart stats
	sm := ts.Server.GetSessionManager()
	bs := sm.GetSession(session.SessionID)
	if bs == nil {
		t.Fatalf("GetSession returned nil")
	}

	// Connect WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errorCollector := &safeErrorCollector{}
	callbacks := client.SessionCallbacks{
		OnError: func(msg string) {
			errorCollector.add(msg)
			t.Logf("Error: %s", msg)
		},
	}

	ws, err := ts.Client.Connect(ctx, session.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Trigger a crash during prompt.
	// The mock server sends an AgentMessageChunk before os.Exit(1),
	// so the crash is detected during streaming (not prompt send).
	if err := ws.SendPrompt("CRASH"); err != nil {
		t.Logf("SendPrompt failed (expected): %v", err)
	}

	// Wait for restart to complete
	waitFor(t, 10*time.Second, func() bool {
		return errorCollector.contains("AI agent restarted")
	}, "restart completion")

	// Check restart stats
	stats := bs.GetRestartStats()

	if stats.TotalRestarts != 1 {
		t.Errorf("TotalRestarts = %d, want 1", stats.TotalRestarts)
	}

	if stats.RecentRestarts != 1 {
		t.Errorf("RecentRestarts = %d, want 1", stats.RecentRestarts)
	}

	// Verify reason was tracked.
	// The mock sends an AgentMessageChunk before crashing, so the crash is detected
	// during streaming, resulting in CrashDuringStream (not CrashDuringPrompt).
	if stats.LastReason != web.RestartReasonCrashDuringStream {
		t.Errorf("LastReason = %q, want %q", stats.LastReason, web.RestartReasonCrashDuringStream)
	}

	// Verify reason count
	if count := stats.ReasonCounts[web.RestartReasonCrashDuringStream]; count != 1 {
		t.Errorf("ReasonCounts[CrashDuringStream] = %d, want 1", count)
	}
}
