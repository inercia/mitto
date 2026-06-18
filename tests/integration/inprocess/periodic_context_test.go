//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"testing"

	"github.com/inercia/mitto/internal/client"
)

// TestPeriodicContextSemantics verifies the three context-aware periodic send cases:
//
// (a) NEW: PUT periodic with prompt_name + frequency + max_iterations → GET shows enabled config.
// (b) REGULAR→periodic: PUT periodic (enabled), then run-now → periodic configured.
// (c) PERIODIC one-shot: with a config already set, POST /queue with a DIFFERENT prompt_name →
//
//	periodic config is UNCHANGED (same prompt_name, frequency, max_iterations, enabled).
func TestPeriodicContextSemantics(t *testing.T) {
	ts := SetupTestServer(t)

	// -------------------------------------------------------------------------
	// Case (a): Configure periodic on a fresh session — verify GET reflects it.
	// -------------------------------------------------------------------------
	t.Run("new_periodic_config", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "periodic-new"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		req := client.SetPeriodicRequest{
			PromptName: "daily-standup",
			Frequency:  client.PeriodicFrequency{Value: 2, Unit: "hours"},
			Enabled:    true,
			MaxIterations: 5,
		}
		cfg, err := ts.Client.SetPeriodic(sess.SessionID, req)
		if err != nil {
			t.Fatalf("SetPeriodic failed: %v", err)
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
		got, err := ts.Client.GetPeriodic(sess.SessionID)
		if err != nil {
			t.Fatalf("GetPeriodic failed: %v", err)
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

		t.Logf("Case (a): periodic config confirmed via GET ✓")
	})

	// -------------------------------------------------------------------------
	// Case (b): Regular → periodic: PUT periodic then run-now succeeds.
	// Use a raw prompt text (not prompt_name) so run-now doesn't fail trying
	// to resolve a named prompt that doesn't exist in the test workspace.
	// -------------------------------------------------------------------------
	t.Run("regular_to_periodic_run_now", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "regular-to-periodic"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		// PUT periodic config with raw prompt text (simulates makePeriodicNow step 1).
		req := client.SetPeriodicRequest{
			Prompt:        "Perform the weekly review tasks.",
			Frequency:     client.PeriodicFrequency{Value: 1, Unit: "hours"},
			Enabled:       true,
			MaxIterations: 3,
		}
		cfg, err := ts.Client.SetPeriodic(sess.SessionID, req)
		if err != nil {
			t.Fatalf("SetPeriodic failed: %v", err)
		}
		if !cfg.Enabled {
			t.Errorf("expected enabled=true after PUT")
		}
		if cfg.MaxIterations != 3 {
			t.Errorf("expected max_iterations=3, got %d", cfg.MaxIterations)
		}

		// POST run-now (simulates makePeriodicNow step 2).
		// The mock ACP server can receive and respond to a raw prompt.
		if err := ts.Client.RunPeriodicNow(sess.SessionID, true); err != nil {
			t.Fatalf("RunPeriodicNow failed: %v", err)
		}

		// Verify the periodic config is still set after run-now.
		got, err := ts.Client.GetPeriodic(sess.SessionID)
		if err != nil {
			t.Fatalf("GetPeriodic after run-now failed: %v", err)
		}
		if !got.Enabled {
			t.Errorf("expected enabled=true after run-now")
		}
		if got.MaxIterations != 3 {
			t.Errorf("expected max_iterations=3 after run-now, got %d", got.MaxIterations)
		}

		t.Logf("Case (b): regular→periodic configured (max_iterations=%d) and run-now accepted ✓", got.MaxIterations)
	})

	// -------------------------------------------------------------------------
	// Case (c): Periodic one-shot — POST /queue with different prompt_name;
	//           periodic config must be UNCHANGED.
	// -------------------------------------------------------------------------
	t.Run("periodic_one_shot_leaves_config_unchanged", func(t *testing.T) {
		sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "periodic-oneshot"})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer ts.Client.DeleteSession(sess.SessionID)

		// Set up the existing periodic config.
		original := client.SetPeriodicRequest{
			PromptName:    "nightly-build",
			Frequency:     client.PeriodicFrequency{Value: 24, Unit: "hours"},
			Enabled:       true,
			MaxIterations: 10,
		}
		if _, err := ts.Client.SetPeriodic(sess.SessionID, original); err != nil {
			t.Fatalf("SetPeriodic (setup) failed: %v", err)
		}

		// POST /queue with a DIFFERENT prompt_name (one-shot send).
		if _, err := ts.Client.AddToQueueNamed(sess.SessionID, "hotfix-check"); err != nil {
			t.Fatalf("AddToQueueNamed failed: %v", err)
		}

		// GET periodic config — must be unchanged.
		got, err := ts.Client.GetPeriodic(sess.SessionID)
		if err != nil {
			t.Fatalf("GetPeriodic after one-shot failed: %v", err)
		}
		if got.PromptName != "nightly-build" {
			t.Errorf("periodic config mutated: expected prompt_name=%q, got %q", "nightly-build", got.PromptName)
		}
		if got.Frequency.Value != 24 || got.Frequency.Unit != "hours" {
			t.Errorf("periodic config mutated: frequency=%+v", got.Frequency)
		}
		if got.MaxIterations != 10 {
			t.Errorf("periodic config mutated: expected max_iterations=10, got %d", got.MaxIterations)
		}
		if !got.Enabled {
			t.Errorf("periodic config mutated: expected enabled=true, got false")
		}

		t.Logf("Case (c): periodic config unchanged after one-shot queue POST ✓")
	})
}
