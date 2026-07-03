package conversation

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// discardLogger returns an slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const testSessionID = "test-session-001"

func TestACPProcessController_CanRestart_InitiallyTrue(t *testing.T) {
	c := acpProcessController{}
	if !c.canRestart(discardLogger(), testSessionID) {
		t.Error("expected canRestart to return true for a fresh controller")
	}
}

func TestACPProcessController_CanRestart_PermanentlyFailed(t *testing.T) {
	c := acpProcessController{}
	c.markPermanentlyFailed()
	if c.canRestart(discardLogger(), testSessionID) {
		t.Error("expected canRestart to return false after markPermanentlyFailed")
	}
}

func TestACPProcessController_CanRestart_LifetimeCap(t *testing.T) {
	c := acpProcessController{}
	logger := discardLogger()
	// Record MaxACPTotalRestarts restarts
	for i := 0; i < MaxACPTotalRestarts; i++ {
		c.recordRestart(RestartReasonCrashDuringPrompt, logger, testSessionID)
	}
	// The next canRestart should hit the cap and return false
	if c.canRestart(logger, testSessionID) {
		t.Errorf("expected canRestart to return false after %d total restarts", MaxACPTotalRestarts)
	}
	// permanentlyFailed should now be set
	c.mu.Lock()
	failed := c.permanentlyFailed
	c.mu.Unlock()
	if !failed {
		t.Error("expected permanentlyFailed to be true after lifetime cap")
	}
}

func TestACPProcessController_RecordRestart_IncrementsCounts(t *testing.T) {
	c := acpProcessController{}
	logger := discardLogger()

	c.recordRestart(RestartReasonCrashDuringPrompt, logger, testSessionID)
	c.recordRestart(RestartReasonUnexpectedExit, logger, testSessionID)

	if c.totalRestarts() != 2 {
		t.Errorf("expected totalRestarts=2, got %d", c.totalRestarts())
	}
	if c.recentRestartCount() != 2 {
		t.Errorf("expected recentRestartCount=2, got %d", c.recentRestartCount())
	}
}

func TestACPProcessController_Stats_ReasonCounts(t *testing.T) {
	c := acpProcessController{}
	logger := discardLogger()

	c.recordRestart(RestartReasonCrashDuringPrompt, logger, testSessionID)
	c.recordRestart(RestartReasonCrashDuringPrompt, logger, testSessionID)
	c.recordRestart(RestartReasonUnexpectedExit, logger, testSessionID)

	s := c.stats()
	if s.TotalRestarts != 3 {
		t.Errorf("expected TotalRestarts=3, got %d", s.TotalRestarts)
	}
	if s.ReasonCounts[RestartReasonCrashDuringPrompt] != 2 {
		t.Errorf("expected CrashDuringPrompt count=2, got %d", s.ReasonCounts[RestartReasonCrashDuringPrompt])
	}
	if s.ReasonCounts[RestartReasonUnexpectedExit] != 1 {
		t.Errorf("expected UnexpectedExit count=1, got %d", s.ReasonCounts[RestartReasonUnexpectedExit])
	}
	if s.LastReason != RestartReasonUnexpectedExit {
		t.Errorf("expected LastReason=UnexpectedExit, got %v", s.LastReason)
	}
	if s.LastRestartTime.IsZero() {
		t.Error("expected LastRestartTime to be non-zero")
	}
}

func TestACPProcessController_GetRestartInfo_Format(t *testing.T) {
	c := acpProcessController{}
	logger := discardLogger()

	// Before any restarts: attempt 1 of MaxACPRestarts
	info := c.getRestartInfo()
	expected := "(attempt 1 of 3)"
	if info != expected {
		t.Errorf("expected %q, got %q", expected, info)
	}

	c.recordRestart(RestartReasonCrashDuringPrompt, logger, testSessionID)
	info = c.getRestartInfo()
	expected = "(attempt 2 of 3)"
	if info != expected {
		t.Errorf("expected %q, got %q", expected, info)
	}
}

func TestACPProcessController_SlidingWindow(t *testing.T) {
	c := acpProcessController{}

	// Inject old restarts directly (outside the window)
	oldTime := time.Now().Add(-(ACPRestartWindow + time.Minute))
	c.mu.Lock()
	c.restartTimes = []time.Time{oldTime, oldTime}
	c.restartReasons = []RestartReason{RestartReasonCrashDuringPrompt, RestartReasonCrashDuringPrompt}
	c.restartCount = 2
	c.mu.Unlock()

	logger := discardLogger()
	// canRestart should prune old entries and allow a restart
	if !c.canRestart(logger, testSessionID) {
		t.Error("expected canRestart to return true after old restarts are outside the window")
	}
	// After pruning, recentRestartCount should be 0
	if c.recentRestartCount() != 0 {
		t.Errorf("expected recentRestartCount=0 after window prune, got %d", c.recentRestartCount())
	}
	// RecentRestarts in stats should be 0
	s := c.stats()
	if s.RecentRestarts != 0 {
		t.Errorf("expected RecentRestarts=0 after window prune, got %d", s.RecentRestarts)
	}
}

func TestACPProcessController_RecentRestartCount_AfterRecords(t *testing.T) {
	c := acpProcessController{}
	logger := discardLogger()

	for i := 0; i < 3; i++ {
		c.recordRestart(RestartReasonCrashDuringStream, logger, testSessionID)
	}

	if got := c.recentRestartCount(); got != 3 {
		t.Errorf("expected recentRestartCount=3, got %d", got)
	}
	if got := c.totalRestarts(); got != 3 {
		t.Errorf("expected totalRestarts=3, got %d", got)
	}
}

func TestACPProcessController_GetRestartInfo_ContainsAttempt(t *testing.T) {
	c := acpProcessController{}
	info := c.getRestartInfo()
	if !strings.HasPrefix(info, "(attempt ") {
		t.Errorf("expected getRestartInfo to start with '(attempt ', got %q", info)
	}
}
