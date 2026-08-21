package procstart

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// acpStartupWatchdogWarnDelay is the delay before the startup watchdog emits a WARN log
// when no stderr activity has been observed and the ACP Initialize handshake has not completed.
// Exposed as a var so tests can override it.
var acpStartupWatchdogWarnDelay = 10 * time.Second

// acpStartupWatchdogErrorDelay is the delay before the startup watchdog emits an ERROR log
// when the process is still unresponsive.
var acpStartupWatchdogErrorDelay = 30 * time.Second

// StartACPStartupWatchdog runs a background goroutine that emits a WARN log if no stderr
// activity is observed within acpStartupWatchdogWarnDelay, and an ERROR log if the process
// is still unresponsive after acpStartupWatchdogErrorDelay. The returned signalActivity
// callback should be wired to stderr first-activity AND called when the Initialize
// handshake completes (success or failure); callers should also defer-cancel ctx so the
// watchdog is torn down when startup finishes. Returns a no-op if logger is nil.
func StartACPStartupWatchdog(ctx context.Context, logger *slog.Logger, command, acpServer string, pid int) func() {
	if logger == nil {
		return func() {}
	}
	activityCh := make(chan struct{})
	var once sync.Once
	signalActivity := func() { once.Do(func() { close(activityCh) }) }

	go func() {
		warnTimer := time.NewTimer(acpStartupWatchdogWarnDelay)
		errTimer := time.NewTimer(acpStartupWatchdogErrorDelay)
		defer warnTimer.Stop()
		defer errTimer.Stop()

		baseAttrs := []any{"command", command, "acp_server", acpServer}
		if pid > 0 {
			baseAttrs = append(baseAttrs, "pid", pid)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-activityCh:
				return
			case <-warnTimer.C:
				logger.Warn("ACP process appears unresponsive — no stderr output and no handshake observed in startup window",
					append(baseAttrs, "elapsed", acpStartupWatchdogWarnDelay.String())...)
			case <-errTimer.C:
				logger.Error("ACP process still unresponsive after extended startup window — handshake has not completed",
					append(baseAttrs, "elapsed", acpStartupWatchdogErrorDelay.String())...)
			}
		}
	}()

	return signalActivity
}
