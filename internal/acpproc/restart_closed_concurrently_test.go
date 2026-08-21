package acpproc

import (
	"context"
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/conversation"
)

// TestRestart_AbortsWhenProcessClosedConcurrently is the mitto-ei81 regression
// test.
//
// Root cause (pre-fix): a shared ACP process's process-lifetime context
// (p.ctx) is created once in NewSharedACPProcess and cancelled exactly once,
// in Close() — it can never be un-cancelled. Close() is called by the process
// manager's StopProcess (used by every GC recycle tier, including the Tier 5
// saturated-idle recycle). Production evidence showed a "User focused
// conversation, ensuring ACP is resumed" request racing a concurrent Tier 5
// recycle for the SAME workspace: the resume path had already obtained a
// reference to the (still valid, not-yet-closed) SharedACPProcess via
// GetOrCreateProcess; the recycle then called Close() on that exact instance
// while the resume's LoadSession RPC was in flight, and the resume's
// subsequent Restart() call on the now-permanently-cancelled instance failed
// immediately with a generic "context canceled" — indistinguishable from a
// genuine transient startup failure, and counted toward failure_count.
//
// Fix: Restart() now detects (via a non-blocking check on p.ctx.Done()) that
// the instance was already closed by a concurrent Close() and returns the
// distinguishable acperrors.ErrProcessClosedConcurrently sentinel immediately,
// without attempting a doomed startProcess() call. Callers (see
// internal/conversation/session_manager.go's resumeSessionWithConstraint) use
// this to retry with a freshly-created process instead of hard-failing.
func TestRestart_AbortsWhenProcessClosedConcurrently(t *testing.T) {
	mockCmd := findMockACPServerBinaryForRestartTest(t)

	p, err := NewSharedACPProcess(context.Background(), SharedACPProcessConfig{
		ACPCommand: mockCmd,
		ACPServer:  "mock-acp",
	})
	if err != nil {
		t.Fatalf("NewSharedACPProcess() error = %v", err)
	}

	// Snapshot the generation as production callers do (before observing the
	// death / racing the recycle), then simulate a concurrent GC recycle
	// (Tier 5 saturated-idle, or any other tier) closing the process out from
	// under a resume that is about to call Restart() on the same instance.
	observedGen := p.Generation()
	p.Close()

	restartErr := p.Restart(observedGen)
	if restartErr == nil {
		t.Fatalf("mitto-ei81: Restart() on a concurrently-closed process returned nil error, " +
			"want acperrors.ErrProcessClosedConcurrently — a permanently-cancelled process " +
			"context can never successfully start a new OS process")
	}
	if !errors.Is(restartErr, acperrors.ErrProcessClosedConcurrently) {
		t.Fatalf("mitto-ei81: Restart() on a concurrently-closed process returned %v (type %T), "+
			"want an error wrapping acperrors.ErrProcessClosedConcurrently so callers can "+
			"distinguish this benign race from a genuine transient startup failure",
			restartErr, restartErr)
	}

	// The unconditional-restart form (RestartAnyGeneration) used by the manual
	// "Restart ACP" UI action must hit the same guard.
	restartErr2 := p.Restart(conversation.RestartAnyGeneration)
	if !errors.Is(restartErr2, acperrors.ErrProcessClosedConcurrently) {
		t.Fatalf("mitto-ei81: Restart(RestartAnyGeneration) on a concurrently-closed process "+
			"returned %v, want acperrors.ErrProcessClosedConcurrently", restartErr2)
	}
}
