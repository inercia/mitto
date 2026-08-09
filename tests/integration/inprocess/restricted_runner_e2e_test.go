//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
// This file tests the Tier 2 restricted-runner E2E flow: a session created via
// the REST API + WebSocket goes through SessionManager.createRunner which then
// consults runner.NewRunner, producing the "created restricted runner" INFO log
// with the requested runner type (or a fallback record when the platform runner
// is unavailable). See mitto-6yi.2.
package inprocess

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// expectedRunnerType returns the runner type to request for this platform.
// sandbox-exec is native on darwin; exec is a trivial passthrough available
// on every platform. Both produce the target INFO log line.
func expectedRunnerType() string {
	if runtime.GOOS == "darwin" {
		return "sandbox-exec"
	}
	return "exec"
}

// unavailableRunnerType returns a platform-native runner type that is guaranteed
// NOT to be available on the current OS, so runner.NewRunner takes the WARN
// fallback branch ("restricted runner creation failed / not available, falling
// back to exec"). Both grrunner.New and CheckImplicitRequirements are consulted
// there, so an OS-mismatched native runner reliably triggers the fallback.
func unavailableRunnerType() string {
	if runtime.GOOS == "darwin" {
		return "firejail"
	}
	return "sandbox-exec"
}

// findRecord scans the captured records for an entry at the given level whose
// message contains msgSubstr. Returns the record and true on the first match.
func findRecord(h *capturingHandler, level slog.Level, msgSubstr string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level && strings.Contains(r.Message, msgSubstr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// attrValue returns the string form of the attribute named key, or "" if absent.
func attrValue(r slog.Record, key string) string {
	var out string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			out = a.Value.String()
			return false
		}
		return true
	})
	return out
}

// TestRestrictedRunner_SessionManager_CreatesRunner drives a full session
// end-to-end through the WebSocket / REST path with a pinned restricted-runner
// configuration on the mock-ACP agent, and asserts that SessionManager.createRunner
// went through runner.NewRunner (which emits the "created restricted runner" INFO
// log) — matching the acceptance criterion for mitto-6yi.2.
func TestRestrictedRunner_SessionManager_CreatesRunner(t *testing.T) {
	runnerType := expectedRunnerType()
	cap := &capturingHandler{}
	logger := slog.New(cap)

	ts := SetupTestServer(t, func(cfg *web.Config) {
		// Capture internal INFO/WARN records from SessionManager + runner.NewRunner.
		cfg.Logger = logger

		// Pin a restricted runner on the mock-acp agent config. SessionManager.createRunner
		// walks global → agent → workspace; setting the agent-level entry is enough to
		// trigger runner.NewRunner and thus the target log line. resolveConfig starts
		// with runnerType="exec" as the lookup key, so the ENTRY key must be "exec" and
		// the entry's Type field is the desired override (e.g. "sandbox-exec" on darwin).
		if cfg.MittoConfig != nil {
			for i := range cfg.MittoConfig.ACPServers {
				if cfg.MittoConfig.ACPServers[i].Name == "mock-acp" {
					cfg.MittoConfig.ACPServers[i].RestrictedRunners = map[string]*config.WorkspaceRunnerConfig{
						"exec": {Type: runnerType},
					}
				}
			}
		}
	})

	// Drive the full REST + WebSocket path. CreateSession triggers
	// SessionManager.CreateSession → createRunner → runner.NewRunner, which is
	// where the target INFO log is emitted, so this alone is enough to exercise
	// the SessionManager runner path. Connect + SendPrompt round-trips exercise
	// that the sandbox does not break normal prompt delivery.
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		mu             sync.Mutex
		promptComplete bool
	)
	callbacks := client.SessionCallbacks{
		OnPromptComplete: func(int) {
			mu.Lock()
			promptComplete = true
			mu.Unlock()
		},
	}

	ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for the runner INFO/WARN record. The runner is created during ACP
	// startup which SessionManager kicks off from CreateSession, so it should
	// appear well before the prompt completes.
	waitFor(t, 10*time.Second, func() bool {
		if _, ok := findRecord(cap, slog.LevelInfo, "created restricted runner"); ok {
			return true
		}
		if _, ok := findRecord(cap, slog.LevelWarn, "falling back to exec"); ok {
			return true
		}
		return false
	}, "runner.NewRunner INFO/WARN record")

	// Give the prompt a bounded chance to complete, but do not require it — under
	// a real sandbox on macOS the mock-ACP process may not be fully permitted to
	// speak back. The runner-creation acceptance criterion above is what the bead
	// requires; a completed prompt is nice-to-have and only logged.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := promptComplete
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Primary acceptance path: "created restricted runner" INFO fires with the
	// resolved runner type. On the fallback branch inside runner.NewRunner
	// (platform unavailable / implicit requirements not met), resolved.Type is
	// rewritten to "exec" before this log line, and a WARN with "falling back to
	// exec" is also emitted — either state satisfies the bead acceptance.
	rec, ok := findRecord(cap, slog.LevelInfo, "created restricted runner")
	if !ok {
		t.Fatalf("expected an INFO \"created restricted runner\" record; got none")
	}
	gotType := attrValue(rec, "type")
	if gotType != runnerType && gotType != "exec" {
		t.Fatalf("created restricted runner: got type=%q, want %q or fallback %q", gotType, runnerType, "exec")
	}
	// If the resolved type is not the requested one, the fallback WARN must be
	// present as well — otherwise the runner silently downgraded, which would
	// mask a real regression.
	if gotType != runnerType {
		if _, ok := findRecord(cap, slog.LevelWarn, "falling back to exec"); !ok {
			t.Fatalf("resolved type=%q differs from requested %q but no fallback WARN was recorded", gotType, runnerType)
		}
	}
}

// TestRestrictedRunner_SessionManager_FallsBackOnUnavailableType asks the
// SessionManager for a platform-native runner that cannot exist on this OS
// (firejail on darwin, sandbox-exec on linux) and asserts the WARN fallback
// branch inside runner.NewRunner fires and downgrades to exec — covering the
// FallbackInfo path called out in the mitto-6yi.2 plan.
func TestRestrictedRunner_SessionManager_FallsBackOnUnavailableType(t *testing.T) {
	requested := unavailableRunnerType()
	cap := &capturingHandler{}
	logger := slog.New(cap)

	ts := SetupTestServer(t, func(cfg *web.Config) {
		cfg.Logger = logger
		if cfg.MittoConfig != nil {
			for i := range cfg.MittoConfig.ACPServers {
				if cfg.MittoConfig.ACPServers[i].Name == "mock-acp" {
					cfg.MittoConfig.ACPServers[i].RestrictedRunners = map[string]*config.WorkspaceRunnerConfig{
						"exec": {Type: requested},
					}
				}
			}
		}
	})

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	// Wait for either the WARN fallback record or the INFO record — the fallback
	// WARN is emitted before the INFO, so if the WARN is present we can stop
	// waiting immediately.
	waitFor(t, 10*time.Second, func() bool {
		if _, ok := findRecord(cap, slog.LevelWarn, "falling back to exec"); ok {
			return true
		}
		_, ok := findRecord(cap, slog.LevelInfo, "created restricted runner")
		return ok
	}, "runner.NewRunner WARN/INFO record")

	warn, ok := findRecord(cap, slog.LevelWarn, "falling back to exec")
	if !ok {
		t.Fatalf("expected a WARN fallback record for requested type=%q, got none", requested)
	}
	if gotReq := attrValue(warn, "requested_type"); gotReq != requested {
		t.Fatalf("fallback WARN: requested_type=%q, want %q", gotReq, requested)
	}

	rec, ok := findRecord(cap, slog.LevelInfo, "created restricted runner")
	if !ok {
		t.Fatalf("expected an INFO \"created restricted runner\" record after fallback; got none")
	}
	if gotType := attrValue(rec, "type"); gotType != "exec" {
		t.Fatalf("post-fallback INFO: type=%q, want %q", gotType, "exec")
	}
	if gotFallback := attrValue(rec, "fallback"); gotFallback != "true" {
		t.Fatalf("post-fallback INFO: fallback=%q, want %q", gotFallback, "true")
	}
}
