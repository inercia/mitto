// tools_children_transient_retry_test.go: tests for the shared bounded async
// resume-retry helper (mitto-nf6) — scheduleBoundedResumeRetry and its
// classifier isTransientChildResumeError — used by both the
// mitto_conversation_new transient branch (tools_conversation_new.go) and the
// auto-resume-on-send-prompt path (tools_prompt_dispatch.go).
package mcpserver

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

func TestIsTransientChildResumeError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		mcpTimeout    bool
		wantTransient bool
	}{
		{name: "nil error", err: nil, wantTransient: false},
		{name: "MCP init timeout", err: fmt.Errorf("MCP initialization timed out after 240s"), mcpTimeout: true, wantTransient: true},
		{name: "shared process saturated", err: acperrors.ErrSharedProcessSaturated, wantTransient: true},
		{name: "wrapped shared process saturated", err: fmt.Errorf("resume: %w", acperrors.ErrSharedProcessSaturated), wantTransient: true},
		{name: "other error", err: errors.New("boom"), wantTransient: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := &mockSessionManagerForAutoResume{resumeErrIsMCPInitTimeout: tt.mcpTimeout}
			srv, err := NewServer(Config{Port: 0}, Dependencies{SessionManager: sm})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			if got := srv.isTransientChildResumeError(tt.err); got != tt.wantTransient {
				t.Errorf("isTransientChildResumeError(%v) = %v, want %v", tt.err, got, tt.wantTransient)
			}
		})
	}
}

// TestScheduleBoundedResumeRetry_StopsEarly_WhenSessionAlreadyExists verifies
// that the retry loop does not call ResumeSession at all once the session has
// already attached (e.g. a concurrent foreground resume won the race) —
// instead it just kicks the existing session's queue.
func TestScheduleBoundedResumeRetry_StopsEarly_WhenSessionAlreadyExists(t *testing.T) {
	oldDelays := childResumeRetryDelays
	childResumeRetryDelays = []time.Duration{0}
	t.Cleanup(func() { childResumeRetryDelays = oldDelays })

	existingBS := &mockBackgroundSessionForAutoResume{}
	sm := &mockSessionManagerForAutoResume{
		sessions: map[string]BackgroundSession{"conv-1": existingBS},
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	srv.scheduleBoundedResumeRetry("conv-1", "Conv One", "/test/dir")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !existingBS.tryProcessCalled.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	if !existingBS.tryProcessCalled.Load() {
		t.Fatal("expected the already-running session's queue to be kicked")
	}
	sm.mu.Lock()
	resumeCalls := len(sm.resumeCalls)
	sm.mu.Unlock()
	if resumeCalls != 0 {
		t.Errorf("expected 0 ResumeSession calls when the session already exists, got %d", resumeCalls)
	}
}

// TestScheduleBoundedResumeRetry_AbortsOnNonTransientError verifies the retry
// loop gives up (WARN-only, no further attempts) as soon as ResumeSession
// returns a non-transient error, instead of exhausting all bounded attempts.
func TestScheduleBoundedResumeRetry_AbortsOnNonTransientError(t *testing.T) {
	oldDelays := childResumeRetryDelays
	childResumeRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { childResumeRetryDelays = oldDelays })

	permanentErr := errors.New("permanent failure")
	sm := &mockSessionManagerForAutoResume{
		sessions:   map[string]BackgroundSession{},
		resumeErrs: []error{permanentErr, permanentErr, permanentErr},
	}
	srv, err := NewServer(Config{Port: 0}, Dependencies{SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	srv.scheduleBoundedResumeRetry("conv-2", "Conv Two", "/test/dir")

	// Give the goroutine time to run; it should stop after exactly one
	// ResumeSession call since the error is non-transient.
	time.Sleep(200 * time.Millisecond)

	sm.mu.Lock()
	resumeCalls := len(sm.resumeCalls)
	sm.mu.Unlock()
	if resumeCalls != 1 {
		t.Errorf("expected exactly 1 ResumeSession call before aborting on non-transient error, got %d", resumeCalls)
	}
}

// TestScheduleBoundedResumeRetry_NilSessionManager_NoOp verifies the helper is
// a safe no-op when no session manager is wired (defensive guard).
func TestScheduleBoundedResumeRetry_NilSessionManager_NoOp(t *testing.T) {
	srv, err := NewServer(Config{Port: 0}, Dependencies{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Must not panic.
	srv.scheduleBoundedResumeRetry("conv-3", "Conv Three", "/test/dir")
}
