package conversation

// titleCoordinator owns the auto-title generation triggers for a session. It is a
// stateless collaborator of BackgroundSession (held by composition, zero value is
// ready to use) and is unit-testable in isolation via the titleDeps seam.

import (
	"log/slog"
	"strings"
)

// titleDeps supplies the live, side-effecting primitives the titleCoordinator
// orchestrates. BackgroundSession satisfies it in production; tests use a fake.
type titleDeps interface {
	// sessionHasNoTitle reports whether the session currently lacks a name.
	sessionHasNoTitle() bool
	// startTitleGeneration kicks off async title generation from the message text.
	startTitleGeneration(message string)
	// resolvePromptName resolves a named workspace prompt to its full text
	// (workingDir-scoped). configured is false when no resolver is wired (in which
	// case resolved/err are meaningless); when configured is true, resolved is the
	// trimmed resolved text and err is the resolver error (if any).
	resolvePromptName(name string) (resolved string, configured bool, err error)
	// titleLogger returns the session-scoped logger (may be nil).
	titleLogger() *slog.Logger
	// titleSessionID returns the persisted session ID (for telemetry).
	titleSessionID() string
}

// titleCoordinator is stateless; all dependencies are passed per call.
type titleCoordinator struct{}

// needsTitle reports whether the session still needs an auto-generated title.
func (titleCoordinator) needsTitle(d titleDeps) bool {
	return d.sessionHasNoTitle()
}

// retryIfNeeded triggers async title generation if the session still has no title.
// Called after prompt completion to catch failed initial attempts and prompts that
// arrived via paths that don't trigger title generation (queue, MCP send_prompt,
// periodic prompts).
func (c titleCoordinator) retryIfNeeded(d titleDeps, message string) {
	if !d.sessionHasNoTitle() {
		return
	}
	if lg := d.titleLogger(); lg != nil {
		lg.Info("Session still has no title after prompt completion, retrying title generation",
			"session_id", d.titleSessionID())
	}
	d.startTitleGeneration(message)
}

// trigger triggers async title generation if the session has no title yet.
func (c titleCoordinator) trigger(d titleDeps, message string) {
	c.retryIfNeeded(d, message)
}

// triggerFromPeriodic chooses the best source text for title generation given a
// periodic-style draft. The inline prompt may be empty, whitespace, or the UI
// placeholder "(pending)" — all three are treated as "no inline prompt". When only
// promptName is meaningful, it is resolved to full text via the configured resolver;
// on failure or when no resolver is configured, the bare prompt name is used as a
// fallback. No-op when neither source yields any text.
func (c titleCoordinator) triggerFromPeriodic(d titleDeps, prompt, promptName string) {
	inline := strings.TrimSpace(prompt)
	if inline != "" && inline != "(pending)" {
		c.retryIfNeeded(d, inline)
		return
	}
	name := strings.TrimSpace(promptName)
	if name == "" {
		return
	}
	if resolved, configured, err := d.resolvePromptName(name); configured {
		if err == nil && resolved != "" {
			c.retryIfNeeded(d, resolved)
			return
		}
		if err != nil {
			if lg := d.titleLogger(); lg != nil {
				lg.Warn("Could not resolve periodic prompt name for title generation; falling back to name",
					"prompt_name", name, "error", err)
			}
		}
	}
	c.retryIfNeeded(d, name)
}
