package procstart

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// capturingLogHandler is a minimal slog.Handler that records emitted records for tests.
type capturingLogHandler struct {
	mu      sync.Mutex
	records []capturedLogEntry
}

type capturedLogEntry struct {
	level slog.Level
	msg   string
}

func newCapturingLogHandler() *capturingLogHandler {
	return &capturingLogHandler{}
}

func (h *capturingLogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *capturingLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, capturedLogEntry{level: r.Level, msg: r.Message})
	return nil
}

func (h *capturingLogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingLogHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *capturingLogHandler) entriesAt(level slog.Level) []capturedLogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]capturedLogEntry, 0)
	for _, r := range h.records {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

// TestStartACPStartupWatchdog_FiresWhenNoActivity verifies the watchdog emits a WARN log
// when neither stderr activity nor handshake completion is observed within the warn window.
func TestStartACPStartupWatchdog_FiresWhenNoActivity(t *testing.T) {
	// Shorten the timers for the test
	origWarn := acpStartupWatchdogWarnDelay
	origErr := acpStartupWatchdogErrorDelay
	acpStartupWatchdogWarnDelay = 30 * time.Millisecond
	acpStartupWatchdogErrorDelay = 90 * time.Millisecond
	defer func() {
		acpStartupWatchdogWarnDelay = origWarn
		acpStartupWatchdogErrorDelay = origErr
	}()

	rec := newCapturingLogHandler()
	logger := slog.New(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = StartACPStartupWatchdog(ctx, logger, "auggie", "Augment", 42)

	// Wait long enough for both timers to fire.
	time.Sleep(200 * time.Millisecond)

	warns := rec.entriesAt(slog.LevelWarn)
	errs := rec.entriesAt(slog.LevelError)

	if len(warns) == 0 {
		t.Fatalf("expected at least one WARN log entry, got none")
	}
	if !strings.Contains(warns[0].msg, "unresponsive") {
		t.Errorf("WARN log should mention 'unresponsive', got: %q", warns[0].msg)
	}
	if len(errs) == 0 {
		t.Fatalf("expected at least one ERROR log entry, got none")
	}
	if !strings.Contains(errs[0].msg, "unresponsive") {
		t.Errorf("ERROR log should mention 'unresponsive', got: %q", errs[0].msg)
	}
}

// TestStartACPStartupWatchdog_SilentWhenSignaled verifies the watchdog does NOT log when
// signalActivity is invoked before the warn window elapses.
func TestStartACPStartupWatchdog_SilentWhenSignaled(t *testing.T) {
	origWarn := acpStartupWatchdogWarnDelay
	origErr := acpStartupWatchdogErrorDelay
	acpStartupWatchdogWarnDelay = 50 * time.Millisecond
	acpStartupWatchdogErrorDelay = 150 * time.Millisecond
	defer func() {
		acpStartupWatchdogWarnDelay = origWarn
		acpStartupWatchdogErrorDelay = origErr
	}()

	rec := newCapturingLogHandler()
	logger := slog.New(rec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalActivity := StartACPStartupWatchdog(ctx, logger, "auggie", "Augment", -1)

	// Signal activity well before the warn window.
	time.Sleep(10 * time.Millisecond)
	signalActivity()

	// Wait beyond the warn window to confirm no log fires.
	time.Sleep(200 * time.Millisecond)

	if got := len(rec.entriesAt(slog.LevelWarn)); got != 0 {
		t.Errorf("expected 0 WARN entries when signaled early, got %d", got)
	}
	if got := len(rec.entriesAt(slog.LevelError)); got != 0 {
		t.Errorf("expected 0 ERROR entries when signaled early, got %d", got)
	}
}

// TestStartACPStartupWatchdog_NilLoggerNoop ensures the helper is a no-op when logger is nil.
func TestStartACPStartupWatchdog_NilLoggerNoop(t *testing.T) {
	// Should not panic, should return a callable no-op.
	signal := StartACPStartupWatchdog(context.Background(), nil, "cmd", "svr", 1)
	signal()
	signal() // Idempotent
}
