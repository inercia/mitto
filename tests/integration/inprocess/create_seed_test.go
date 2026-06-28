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
	workspaceDir := filepath.Join(ts.TempDir, "workspace")
	promptsDir := filepath.Join(workspaceDir, ".mitto", "prompts")
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace prompts dir: %v", err)
	}
	promptContent := `---
name: "atomic-seed-test-prompt"
description: "Integration test prompt for atomic create+seed"
---
Say hello from the atomic seed test.
`
	promptPath := filepath.Join(promptsDir, "atomic-seed-test-prompt.md")
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
