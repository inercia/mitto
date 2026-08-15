package acpproc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"syscall"
	"testing"
)

type lockedLogBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func (b *lockedLogBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.b.Reset()
}

func newExitLoggingProcess(t *testing.T) (*SharedACPProcess, *lockedLogBuffer) {
	t.Helper()

	logs := &lockedLogBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p, err := NewSharedACPProcess(context.Background(), SharedACPProcessConfig{
		ACPCommand: findMockACPServerBinaryForRestartTest(t),
		ACPServer:  "mock-acp",
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("NewSharedACPProcess() error = %v", err)
	}
	logs.Reset()
	return p, logs
}

func waitForForcedProcessExit(t *testing.T, p *SharedACPProcess) {
	t.Helper()
	if err := syscall.Kill(p.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL process: %v", err)
	}
	if err := p.wait(); err == nil {
		t.Fatal("wait() error = nil after SIGKILL")
	}
	p.wait = nil
}

func requireLogContains(t *testing.T, logs, want string) {
	t.Helper()
	if !strings.Contains(logs, want) {
		t.Fatalf("logs missing %q:\n%s", want, logs)
	}
}

func TestSharedACPProcess_RestartExitIsIntentional(t *testing.T) {
	p, logs := newExitLoggingProcess(t)
	defer p.Close()

	if err := p.Restart(p.Generation()); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	out := logs.String()
	requireLogContains(t, out, `msg="ACP process exited (intentional shutdown)"`)
	requireLogContains(t, out, "shutdown_reason=restart")
	if strings.Contains(out, `msg="ACP process exited abnormally"`) {
		t.Fatalf("restart exit logged as abnormal:\n%s", out)
	}
}

func TestSharedACPProcess_CloseExitIsIntentional(t *testing.T) {
	p, logs := newExitLoggingProcess(t)

	p.Close()
	out := logs.String()
	requireLogContains(t, out, `msg="ACP process exited (intentional shutdown)"`)
	requireLogContains(t, out, "shutdown_reason=close")
	if strings.Contains(out, `msg="ACP process exited abnormally"`) {
		t.Fatalf("close exit logged as abnormal:\n%s", out)
	}
}

func TestSharedACPProcess_SpontaneousSIGKILLIsAbnormal(t *testing.T) {
	p, logs := newExitLoggingProcess(t)
	defer p.Close()

	waitForForcedProcessExit(t, p)
	out := logs.String()
	requireLogContains(t, out, `msg="ACP process exited abnormally"`)
	requireLogContains(t, out, "death_signal=SIGKILL")
	if strings.Contains(out, "shutdown_reason=") {
		t.Fatalf("spontaneous exit inherited shutdown reason:\n%s", out)
	}
}

func TestSharedACPProcess_ReplacementDoesNotInheritRestartIntent(t *testing.T) {
	p, logs := newExitLoggingProcess(t)
	defer p.Close()

	if err := p.Restart(p.Generation()); err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	logs.Reset()
	waitForForcedProcessExit(t, p)
	out := logs.String()
	requireLogContains(t, out, `msg="ACP process exited abnormally"`)
	if strings.Contains(out, "shutdown_reason=") {
		t.Fatalf("replacement inherited prior generation restart intent:\n%s", out)
	}
}
