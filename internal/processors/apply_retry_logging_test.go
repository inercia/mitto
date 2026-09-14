package processors

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
)

const ordinaryRetryLogMessage = "prompt-mode processor dispatch attempt failed; will retry"

func TestRunDispatchRetryLoop_OrdinaryLogging_FirstWarnThenDebug(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	attempts := 0
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("transient backpressure %d", attempts)
		}
		return nil
	})

	totalAttempts, _, err := m.runDispatchRetryLoop("ws", "proc", "prompt", time.Second, "skip")
	if err != nil || totalAttempts != 3 {
		t.Fatalf("runDispatchRetryLoop() = attempts %d, err %v; want 3, nil", totalAttempts, err)
	}

	warns, debugs := ordinaryRetryRecords(handler.snapshot())
	if len(warns) != 1 || len(debugs) != 1 {
		t.Fatalf("ordinary retry logs = %d WARN, %d DEBUG; want 1, 1: %+v", len(warns), len(debugs), handler.snapshot())
	}
	assertRetryAttrs(t, warns[0], 1)
	assertRetryAttrs(t, debugs[0], 2)
}

func TestDispatchWithRetry_OrdinaryExhaustionPreservesTerminalErrorContext(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		return fmt.Errorf("persistent transient backpressure")
	})
	m.dispatchWithRetry("", "proc", "prompt", time.Second, "skip", "give up", false)

	var terminal []capturedLogRecord
	for _, rec := range handler.snapshot() {
		if rec.Level == slog.LevelError && rec.Message == "give up; batch not persisted, work is lost" {
			terminal = append(terminal, rec)
		}
	}
	if len(terminal) != 1 {
		t.Fatalf("terminal ERROR records = %d, want 1: %+v", len(terminal), handler.snapshot())
	}
	if got := terminal[0].Attrs["attempts"]; got != int64(dispatchPromptMaxRetries+1) {
		t.Errorf("terminal attempts = %v, want %d", got, dispatchPromptMaxRetries+1)
	}
	if waited, ok := terminal[0].Attrs["waited"].(time.Duration); !ok || waited <= 0 {
		t.Errorf("terminal waited = %#v, want positive time.Duration", terminal[0].Attrs["waited"])
	}
}

func TestFlushPendingDispatches_OrdinaryLogging_OneWarnPerFlushWindow(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })
	origBusyInterval := pendingDispatchBusyRetryInterval
	pendingDispatchBusyRetryInterval = time.Millisecond
	t.Cleanup(func() { pendingDispatchBusyRetryInterval = origBusyInterval })

	const workspaceUUID = "ws-retry-log-window"
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	if err := store.Replace(workspaceUUID, []PendingDispatchEntry{{
		WorkspaceUUID: workspaceUUID, Name: "batch", Prompt: "prompt",
		TimeoutSeconds: 1, SavedAt: time.Now(), Attempts: 1,
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPendingDispatchStore(store)
	attempts := 0
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		attempts++
		if attempts <= 4 {
			return fmt.Errorf("ordinary host backpressure: %w", acperrors.ErrProcessBusy)
		}
		return nil
	})
	m.FlushPendingDispatches(context.Background(), workspaceUUID)

	remaining, err := store.Load(workspaceUUID)
	if err != nil || len(remaining) != 0 || attempts != 5 {
		t.Fatalf("flush result: attempts=%d remaining=%+v err=%v; want 5, empty, nil", attempts, remaining, err)
	}
	warns, debugs := ordinaryRetryRecords(handler.snapshot())
	if len(warns) != 1 || len(debugs) != 2 {
		t.Fatalf("flush retry logs = %d WARN, %d DEBUG; want 1, 2: %+v", len(warns), len(debugs), handler.snapshot())
	}
	assertRetryAttrs(t, warns[0], 1)
	assertRetryAttrs(t, debugs[0], 2)
	assertRetryAttrs(t, debugs[1], 1) // New nested loop, same flush log window.
}

// TestDispatchWithRetry_PersistedForRetryLogsWarnNotError covers mitto-c6j.2:
// once ordinary retries are exhausted and the undelivered batch is
// successfully spooled for later delivery (the self-healing path exercised
// by FlushPendingDispatches), the "batch persisted for later retry" line
// must log at WARN, not ERROR — it is an expected, recovered condition, not
// a genuine failure, and logging it at ERROR caused alert fatigue in
// scheduled log-analysis (see bead evidence).
func TestDispatchWithRetry_PersistedForRetryLogsWarnNotError(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	store := &FilePendingDispatchStore{BaseDir: t.TempDir()}
	m.SetPendingDispatchStore(store)
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		return fmt.Errorf("persistent transient backpressure")
	})

	const workspaceUUID = "ws-persisted-warn"
	m.dispatchWithRetry(workspaceUUID, "proc", "prompt", time.Second, "skip",
		"prompt-mode processor dispatch failed", false)

	entries, err := store.Load(workspaceUUID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Load() = %+v, err %v; want exactly 1 spooled entry", entries, err)
	}

	const wantMessage = "prompt-mode processor dispatch failed; batch persisted for later retry"
	var warnCount, errorCount int
	for _, rec := range handler.snapshot() {
		if rec.Message != wantMessage {
			continue
		}
		switch rec.Level {
		case slog.LevelWarn:
			warnCount++
		case slog.LevelError:
			errorCount++
		}
	}
	if warnCount != 1 || errorCount != 0 {
		t.Fatalf("persisted-for-retry logs = %d WARN, %d ERROR; want 1, 0 (mitto-c6j.2 acceptance criteria: a "+
			"successfully-spooled-and-later-delivered dispatch must not produce an ERROR-level log line): %+v",
			warnCount, errorCount, handler.snapshot())
	}
}

// failingPendingDispatchStore's Append/AppendClaimed always fail, letting
// TestDispatchWithRetry_SpoolAppendFailureStillLogsError exercise the
// genuine-failure ERROR path ("failed to persist undelivered batch, work is
// lost") — distinct from the ordinary give-up-without-any-store ERROR path
// already covered by TestDispatchWithRetry_OrdinaryExhaustionPreservesTerminalErrorContext,
// and distinct from the successfully-spooled WARN path above. Reserving
// ERROR for spooling itself failing (true, unrecovered work loss) is the
// other half of the mitto-c6j.2 acceptance criteria.
type failingPendingDispatchStore struct{}

func (failingPendingDispatchStore) Append(PendingDispatchEntry) (PendingDispatchAppendResult, error) {
	return PendingDispatchAppendResult{}, fmt.Errorf("disk full")
}

func (failingPendingDispatchStore) AppendClaimed(PendingDispatchEntry) (PendingDispatchAppendResult, error) {
	return PendingDispatchAppendResult{}, fmt.Errorf("disk full")
}

func (failingPendingDispatchStore) Claim(string) (PendingDispatchClaim, error) {
	return PendingDispatchClaim{}, nil
}

func (failingPendingDispatchStore) Requeue(string, []PendingDispatchEntry) ([]PendingDispatchEntry, error) {
	return nil, nil
}

func (failingPendingDispatchStore) Acknowledge(string, []string) error { return nil }

func TestDispatchWithRetry_SpoolAppendFailureStillLogsError(t *testing.T) {
	origDelay := dispatchPromptRetryBaseDelay
	dispatchPromptRetryBaseDelay = time.Millisecond
	t.Cleanup(func() { dispatchPromptRetryBaseDelay = origDelay })

	handler := &recordingLogHandler{}
	m := NewManager("", slog.New(handler))
	m.SetPendingDispatchStore(failingPendingDispatchStore{})
	m.SetPromptFunc(func(context.Context, string, string, string) error {
		return fmt.Errorf("persistent transient backpressure")
	})

	m.dispatchWithRetry("ws-spool-fails", "proc", "prompt", time.Second, "skip",
		"prompt-mode processor dispatch failed", false)

	const wantMessage = "prompt-mode processor dispatch failed; failed to persist undelivered batch, work is lost"
	var errorCount int
	for _, rec := range handler.snapshot() {
		if rec.Level == slog.LevelError && rec.Message == wantMessage {
			errorCount++
		}
	}
	if errorCount != 1 {
		t.Fatalf("spool-append-failure ERROR records = %d, want 1 (spooling itself failing is a genuine, "+
			"unrecovered failure and must stay ERROR): %+v", errorCount, handler.snapshot())
	}
}

func ordinaryRetryRecords(records []capturedLogRecord) (warns, debugs []capturedLogRecord) {
	for _, rec := range records {
		if rec.Message != ordinaryRetryLogMessage {
			continue
		}
		switch rec.Level {
		case slog.LevelWarn:
			warns = append(warns, rec)
		case slog.LevelDebug:
			debugs = append(debugs, rec)
		}
	}
	return warns, debugs
}

func assertRetryAttrs(t *testing.T, rec capturedLogRecord, wantAttempt int64) {
	t.Helper()
	if got := rec.Attrs["attempt"]; got != wantAttempt {
		t.Errorf("attempt = %v, want %d", got, wantAttempt)
	}
	if got := rec.Attrs["max_attempts"]; got != int64(dispatchPromptMaxRetries+1) {
		t.Errorf("max_attempts = %v, want %d", got, dispatchPromptMaxRetries+1)
	}
}
