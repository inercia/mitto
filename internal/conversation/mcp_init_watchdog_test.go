package conversation

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// mcpInitWatchdogProcess adds the optional MCP-init signal used by the
// inactivity watchdog without requiring a real ACP process.
type mcpInitWatchdogProcess struct {
	*alwaysFailSharedProcess
	inProgress bool
}

func (p *mcpInitWatchdogProcess) MCPInitInProgress() bool { return p.inProgress }

// TestStartPromptInactivityWatchdog_PausesDuringMCPInit reproduces mitto-6cz6:
// a prompt must not consume its inactivity budget while its shared agent process
// is known to be blocked initializing MCP servers.
func TestStartPromptInactivityWatchdog_PausesDuringMCPInit(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 20 * time.Millisecond
	SetPromptInactivityTimeout(50 * time.Millisecond)
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{
		logger:      slog.New(rec),
		persistedID: "test-mcp-init",
		sharedProcess: &mcpInitWatchdogProcess{
			alwaysFailSharedProcess: &alwaysFailSharedProcess{},
			inProgress:              true,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var fired atomic.Bool
	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)
	defer cancel()

	time.Sleep(250 * time.Millisecond)
	if fired.Load() {
		t.Fatal("watchdog fired while MCP initialization was in progress; the prompt's inactivity clock should pause")
	}
	if ctx.Err() != nil {
		t.Fatal("prompt context was cancelled while MCP initialization was in progress")
	}
	if got := len(rec.entriesAt(slog.LevelError)); got != 0 {
		t.Fatalf("expected no watchdog ERROR during MCP initialization, got %d", got)
	}
}
