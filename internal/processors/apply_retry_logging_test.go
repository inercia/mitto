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
	m.dispatchWithRetry("", "proc", "prompt", time.Second, "skip", "give up")

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

func ordinaryRetryRecords(records []capturedLogRecord) (warns, debugs []capturedLogRecord) {
	for _, rec := range records {
		if rec.Message != ordinaryRetryLogMessage {
			continue
		}
		if rec.Level == slog.LevelWarn {
			warns = append(warns, rec)
		} else if rec.Level == slog.LevelDebug {
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
