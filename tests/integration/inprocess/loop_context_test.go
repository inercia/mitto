//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"testing"

	"github.com/inercia/mitto/internal/client"
)

// TestLoopContextSemantics verifies the three context-aware loop send cases:
//
// (a) NEW: PUT loop with prompt_name + frequency + max_iterations → GET shows enabled config.
// (b) REGULAR→loop: PUT loop (enabled), then run-now → loop configured.
// (c) LOOP one-shot: with a config already set, POST /queue with a DIFFERENT prompt_name →
//
//	loop config is UNCHANGED (same prompt_name, frequency, max_iterations, enabled).
func TestLoopContextSemantics(t *testing.T) {
	ts := SetupTestServer(t)

	// -------------------------------------------------------------------------
	// Case (a): Configure loop on a fresh session — verify GET reflects it.
	// -------------------------------------------------------------------------
	t.Run("new_loop_config", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "loop-new"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		req := client.SetLoopRequest{
			PromptName:    "daily-standup",
			Frequency:     client.LoopFrequency{Value: 2, Unit: "hours"},
			Enabled:       true,
			MaxIterations: 5,
		}
		cfg, err := ts.Client.SetLoop(sess.SessionID, req)
		if err != nil {
			t.Fatalf("SetLoop failed: %v", err)
		}
		if !cfg.Enabled {
			t.Errorf("expected enabled=true, got false")
		}
		if cfg.PromptName != "daily-standup" {
			t.Errorf("expected prompt_name=%q, got %q", "daily-standup", cfg.PromptName)
		}
		if cfg.Frequency.Value != 2 || cfg.Frequency.Unit != "hours" {
			t.Errorf("unexpected frequency: %+v", cfg.Frequency)
		}
		if cfg.MaxIterations != 5 {
			t.Errorf("expected max_iterations=5, got %d", cfg.MaxIterations)
		}

		// Verify GET returns the same config.
		got, err := ts.Client.GetLoop(sess.SessionID)
		if err != nil {
			t.Fatalf("GetLoop failed: %v", err)
		}
		if got.PromptName != "daily-standup" {
			t.Errorf("GET: expected prompt_name=%q, got %q", "daily-standup", got.PromptName)
		}
		if got.MaxIterations != 5 {
			t.Errorf("GET: expected max_iterations=5, got %d", got.MaxIterations)
		}
		if !got.Enabled {
			t.Errorf("GET: expected enabled=true")
		}

		t.Logf("Case (a): loop config confirmed via GET ✓")
	})

	// -------------------------------------------------------------------------
	// Case (b): Regular → loop: PUT loop then run-now succeeds.
	// Use a raw prompt text (not prompt_name) so run-now doesn't fail trying
	// to resolve a named prompt that doesn't exist in the test workspace.
	// -------------------------------------------------------------------------
	t.Run("regular_to_loop_run_now", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "regular-to-loop"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		// PUT loop config with raw prompt text (simulates makeLoopNow step 1).
		req := client.SetLoopRequest{
			Prompt:        "Perform the weekly review tasks.",
			Frequency:     client.LoopFrequency{Value: 1, Unit: "hours"},
			Enabled:       true,
			MaxIterations: 3,
		}
		cfg, err := ts.Client.SetLoop(sess.SessionID, req)
		if err != nil {
			t.Fatalf("SetLoop failed: %v", err)
		}
		if !cfg.Enabled {
			t.Errorf("expected enabled=true after PUT")
		}
		if cfg.MaxIterations != 3 {
			t.Errorf("expected max_iterations=3, got %d", cfg.MaxIterations)
		}

		// POST run-now (simulates makeLoopNow step 2).
		// The mock ACP server can receive and respond to a raw prompt.
		if err := ts.Client.RunLoopNow(sess.SessionID, true); err != nil {
			t.Fatalf("RunLoopNow failed: %v", err)
		}

		// Verify the loop config is still set after run-now.
		got, err := ts.Client.GetLoop(sess.SessionID)
		if err != nil {
			t.Fatalf("GetLoop after run-now failed: %v", err)
		}
		if !got.Enabled {
			t.Errorf("expected enabled=true after run-now")
		}
		if got.MaxIterations != 3 {
			t.Errorf("expected max_iterations=3 after run-now, got %d", got.MaxIterations)
		}

		t.Logf("Case (b): regular→loop configured (max_iterations=%d) and run-now accepted ✓", got.MaxIterations)
	})

	// -------------------------------------------------------------------------
	// Case (c): Loop one-shot — POST /queue with different prompt_name;
	//           loop config must be UNCHANGED.
	// -------------------------------------------------------------------------
	t.Run("loop_one_shot_leaves_config_unchanged", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "loop-oneshot"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		// Set up the existing loop config.
		original := client.SetLoopRequest{
			PromptName:    "nightly-build",
			Frequency:     client.LoopFrequency{Value: 24, Unit: "hours"},
			Enabled:       true,
			MaxIterations: 10,
		}
		if _, err := ts.Client.SetLoop(sess.SessionID, original); err != nil {
			t.Fatalf("SetLoop (setup) failed: %v", err)
		}

		// POST /queue with a DIFFERENT prompt_name (one-shot send).
		if _, err := ts.Client.AddToQueueNamed(sess.SessionID, "hotfix-check"); err != nil {
			t.Fatalf("AddToQueueNamed failed: %v", err)
		}

		// GET loop config — must be unchanged.
		got, err := ts.Client.GetLoop(sess.SessionID)
		if err != nil {
			t.Fatalf("GetLoop after one-shot failed: %v", err)
		}
		if got.PromptName != "nightly-build" {
			t.Errorf("loop config mutated: expected prompt_name=%q, got %q", "nightly-build", got.PromptName)
		}
		if got.Frequency.Value != 24 || got.Frequency.Unit != "hours" {
			t.Errorf("loop config mutated: frequency=%+v", got.Frequency)
		}
		if got.MaxIterations != 10 {
			t.Errorf("loop config mutated: expected max_iterations=10, got %d", got.MaxIterations)
		}
		if !got.Enabled {
			t.Errorf("loop config mutated: expected enabled=true, got false")
		}

		t.Logf("Case (c): loop config unchanged after one-shot queue POST ✓")
	})
}
