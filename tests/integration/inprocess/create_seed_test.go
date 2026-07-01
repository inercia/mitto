//go:build integration

package inprocess

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/conversation"
)

// TestAtomicCreateSeed verifies that a single POST /api/sessions with
// initial_prompt_name creates AND seeds the conversation in one call.
//
// Approach: We write a named prompt file to the MITTO_DIR/prompts/ directory
// (which is tmpDir/prompts/ in tests because SetupTestServer sets MITTO_DIR=tmpDir).
// The PromptsCache reads from disk on demand, so the file is found when the
// background session calls resolvePromptByName during TryProcessQueuedMessage.
//
// We verify end-to-end by waiting for OnPromptComplete (proving the named prompt
// was dispatched to the mock ACP and the agent responded), then asserting that a
// user_prompt event with prompt_name set appears in the session's event log.
func TestAtomicCreateSeed(t *testing.T) {
	ts := SetupTestServer(t)

	// Write the named prompt file to the workspace .mitto/prompts/ directory so
	// resolvePromptByName finds it via the workspace directory prompts path.
	// Note: The test server's webConfig does not configure PromptsCache (so MITTO_DIR/prompts/
	// is not searched). Workspace directory prompts (workspaceDir/.mitto/prompts/) are always
	// loaded by resolvePromptByName via loadPromptsFromDirs and do NOT require PromptsCache.
	// The file must exist before TryProcessQueuedMessage runs (it fires asynchronously
	// after CreateSession returns, so writing here is safe).
	// Uses the canonical .prompt.yaml format: loadPromptsFromDirs/LoadPromptsFromDir only
	// loads ".prompt.yaml" files (legacy ".md" front-matter files require an explicit
	// migration step that resolvePromptByName does not perform).
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	promptsDir := filepath.Join(workspaceDir, ".mitto", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	promptContent := `name: "atomic-seed-test-prompt"
description: "Integration test prompt for atomic create+seed"
prompt: |
  Say hello from the atomic seed test.
`
	promptPath := filepath.Join(promptsDir, "atomic-seed-test-prompt.prompt.yaml")
	if err := os.WriteFile(promptPath, []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to write prompt file: %v", err)
	}
	t.Logf("Wrote prompt file: %s", promptPath)

	// Single call: create session AND seed with named prompt + arguments.
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{
		InitialPromptName: "atomic-seed-test-prompt",
		Arguments:         map[string]string{"TEST_KEY": "test-value"},
	})
	if err != nil {
		t.Fatalf("CreateSession with InitialPromptName failed: %v", err)
	}
	defer ts.Client.DeleteSession(sess.SessionID)
	t.Logf("Created session: %s", sess.SessionID)

	// Connect via WebSocket and wait for the seeded prompt to be dispatched and completed.
	var (
		mu             sync.Mutex
		promptComplete bool
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			promptComplete = true
			t.Logf("Prompt complete: %d events", eventCount)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	// Load events to register as observer — required for OnPromptComplete to fire.
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait for the agent to finish responding to the seeded prompt.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return promptComplete
	}, "prompt complete from seeded named prompt")

	// Load all events and verify the user_prompt event carries prompt_name.
	events, err := ts.Store.ReadEvents(sess.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Event.Data is a map[string]interface{} after JSON round-trip through the store.
	var found bool
	for _, ev := range events {
		if ev.Type != "user_prompt" {
			continue
		}
		dataMap, ok := ev.Data.(map[string]interface{})
		if !ok {
			continue
		}
		promptName, _ := dataMap["prompt_name"].(string)
		if promptName == "atomic-seed-test-prompt" {
			found = true
			t.Logf("✓ user_prompt event (seq=%d) has prompt_name=%q — atomic create+seed verified", ev.Seq, promptName)
			break
		}
	}

	if !found {
		// Log all events for debugging.
		t.Logf("Events received (%d total):", len(events))
		for _, ev := range events {
			t.Logf("  seq=%d type=%s data=%+v", ev.Seq, ev.Type, ev.Data)
		}
		t.Errorf("No user_prompt event with prompt_name=%q found — atomic create+seed failed", "atomic-seed-test-prompt")
	}
}

// TestSingletonPromptFindOrRoute verifies the find-or-route logic for
// singleton-declared prompts (mitto-4mb.3): creating a conversation from a
// singleton prompt twice in the same working dir routes the second call to
// the SAME conversation (reused:true) instead of creating a duplicate, and
// (idle case) re-seeds the prompt into the existing queue for dispatch.
func TestSingletonPromptFindOrRoute(t *testing.T) {
	ts := SetupTestServer(t)

	// Declare a singleton prompt via the canonical .prompt.yaml format (loaded
	// by loadPromptsFromDirs without needing the legacy .md migration step).
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	promptsDir := filepath.Join(workspaceDir, ".mitto", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	promptContent := `name: "singleton-test-prompt"
description: "Integration test prompt for singleton find-or-route"
singleton: true
prompt: |
  Say hello from the singleton find-or-route test.
`
	promptPath := filepath.Join(promptsDir, "singleton-test-prompt.prompt.yaml")
	if err := os.WriteFile(promptPath, []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to write prompt file: %v", err)
	}

	// First call: creates a brand-new conversation and seeds it.
	first, err := ts.Client.CreateSession(client.CreateSessionRequest{
		InitialPromptName: "singleton-test-prompt",
	})
	if err != nil {
		t.Fatalf("First CreateSession failed: %v", err)
	}
	if first.Reused {
		t.Fatalf("First CreateSession should not be reused, got Reused=true")
	}
	t.Logf("Created first session: %s", first.SessionID)

	var (
		mu              sync.Mutex
		completionCount int
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, first.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			completionCount++
			t.Logf("Prompt complete #%d: %d events", completionCount, eventCount)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait for the first seeded prompt to complete so the session goes idle
	// (empty queue, not prompting) before issuing the second create call.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return completionCount >= 1
	}, "first prompt complete")

	// Second call: same prompt, same (default) working dir — must route to the
	// SAME conversation instead of creating a duplicate, and re-seed it (idle).
	second, err := ts.Client.CreateSession(client.CreateSessionRequest{
		InitialPromptName: "singleton-test-prompt",
	})
	if err != nil {
		t.Fatalf("Second CreateSession failed: %v", err)
	}
	if !second.Reused {
		t.Errorf("Second CreateSession should be reused, got Reused=false")
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("Second SessionID = %q, want same as first %q", second.SessionID, first.SessionID)
	}

	// Wait for the re-seeded (second) prompt to complete on the same conversation.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return completionCount >= 2
	}, "second (reused) prompt complete")

	// Verify the event log has two user_prompt events for the named prompt.
	events, err := ts.Store.ReadEvents(first.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	var promptCount int
	for _, ev := range events {
		if ev.Type != "user_prompt" {
			continue
		}
		dataMap, ok := ev.Data.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := dataMap["prompt_name"].(string); name == "singleton-test-prompt" {
			promptCount++
		}
	}
	if promptCount != 2 {
		t.Errorf("user_prompt events with prompt_name=%q = %d, want 2 (initial create + reused seed)",
			"singleton-test-prompt", promptCount)
	}
}

// TestSingletonPromptFindOrRoute_BusyReuseDoesNotDuplicate verifies the busy
// branch of singleton find-or-route (mitto-4mb.3/.5): creating a conversation
// from a singleton prompt a second time while the EXISTING conversation is
// still prompting (busy) routes to the SAME conversation (reused:true, same
// session id) WITHOUT enqueuing/dispatching a duplicate prompt — contrast the
// idle case above, which re-seeds and expects two user_prompt events.
func TestSingletonPromptFindOrRoute_BusyReuseDoesNotDuplicate(t *testing.T) {
	ts := SetupTestServer(t)

	// Singleton prompt with a SLOW body (mock ACP delays 3s on this pattern —
	// see tests/fixtures/responses/lazy-session-slow-prompt.json) so the first
	// dispatch stays busy long enough for the second create to land mid-turn.
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	promptsDir := filepath.Join(workspaceDir, ".mitto", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	promptContent := `name: "singleton-busy-prompt"
description: "Integration test prompt for singleton busy-reuse"
singleton: true
prompt: |
  LAZY_SESSION_SLOW_PROMPT please respond slowly for the busy-reuse test.
`
	promptPath := filepath.Join(promptsDir, "singleton-busy-prompt.prompt.yaml")
	if err := os.WriteFile(promptPath, []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to write prompt file: %v", err)
	}

	// First call: creates a brand-new conversation and seeds it with the slow prompt.
	first, err := ts.Client.CreateSession(client.CreateSessionRequest{
		InitialPromptName: "singleton-busy-prompt",
	})
	if err != nil {
		t.Fatalf("First CreateSession failed: %v", err)
	}
	if first.Reused {
		t.Fatalf("First CreateSession should not be reused, got Reused=true")
	}
	t.Logf("Created first session: %s", first.SessionID)

	var (
		mu              sync.Mutex
		completionCount int
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, first.SessionID, client.SessionCallbacks{
		OnPromptComplete: func(eventCount int) {
			mu.Lock()
			defer mu.Unlock()
			completionCount++
			t.Logf("Prompt complete #%d: %d events", completionCount, eventCount)
		},
	})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}

	// Wait until the first (slow) prompt is actually prompting — the busy window.
	sm := ts.Server.GetSessionManager()
	var bs *conversation.BackgroundSession
	waitFor(t, 10*time.Second, func() bool {
		bs = sm.GetSession(first.SessionID)
		return bs != nil && bs.IsPrompting()
	}, "first (slow) prompt is prompting")

	// Second call, issued WHILE busy: same prompt, same working dir — must route
	// to the SAME conversation, and must NOT enqueue a duplicate (busy = focus-only).
	second, err := ts.Client.CreateSession(client.CreateSessionRequest{
		InitialPromptName: "singleton-busy-prompt",
	})
	if err != nil {
		t.Fatalf("Second CreateSession failed: %v", err)
	}
	if !second.Reused {
		t.Errorf("Second CreateSession should be reused, got Reused=false")
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("Second SessionID = %q, want same as first %q", second.SessionID, first.SessionID)
	}

	// The busy path does not enqueue — the queue should not have gained a
	// pending duplicate from the second (busy) create call.
	if qlen, err := ts.Store.Queue(first.SessionID).Len(); err == nil && qlen != 0 {
		t.Errorf("Queue length after busy reuse = %d, want 0 (busy reuse must not enqueue)", qlen)
	}

	// Wait for the slow first prompt to finish, then settle briefly to catch any
	// late re-dispatch the busy path might have incorrectly triggered.
	waitFor(t, 20*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return completionCount >= 1
	}, "first prompt complete")
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	finalCompletionCount := completionCount
	mu.Unlock()
	if finalCompletionCount != 1 {
		t.Errorf("completionCount = %d after settle, want 1 (busy reuse must not dispatch a second prompt)", finalCompletionCount)
	}

	// KEY ASSERTION: exactly ONE user_prompt event for this prompt — the busy
	// second create must not have produced a duplicate dispatch.
	events, err := ts.Store.ReadEvents(first.SessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	var promptCount int
	for _, ev := range events {
		if ev.Type != "user_prompt" {
			continue
		}
		dataMap, ok := ev.Data.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := dataMap["prompt_name"].(string); name == "singleton-busy-prompt" {
			promptCount++
		}
	}
	if promptCount != 1 {
		t.Errorf("user_prompt events with prompt_name=%q = %d, want 1 (busy reuse must NOT duplicate)",
			"singleton-busy-prompt", promptCount)
	}
}
