package conversation

// Title generation cluster for BackgroundSession: thin delegators to the
// titleCoordinator collaborator, plus the titleDeps implementation that supplies
// it with the session's live dependencies.

import (
	"log/slog"
	"strings"
)

// NeedsTitle returns true if the session has no title yet and needs auto-title generation.
// Returns false if the session already has a title (either auto-generated or user-set).
func (bs *BackgroundSession) NeedsTitle() bool {
	return bs.titleCoord.needsTitle(bs)
}

// retryTitleGenerationIfNeeded checks if the session still needs a title and
// triggers async title generation. This is called after prompt completion to catch:
// (1) failed initial title generation attempts (e.g., context deadline exceeded)
// (2) prompts that arrived via paths that don't trigger title generation
//
//	(queue processing, MCP send_prompt, periodic prompts)
func (bs *BackgroundSession) retryTitleGenerationIfNeeded(message string) {
	bs.titleCoord.retryIfNeeded(bs, message)
}

// TriggerTitleGeneration triggers async title generation if the session has no title yet.
// This is the public interface used by MCP tools and API handlers to generate titles
// for sessions that received prompts via paths that don't normally trigger title generation
// (e.g., periodic prompt configuration, queue processing).
func (bs *BackgroundSession) TriggerTitleGeneration(message string) {
	bs.titleCoord.trigger(bs, message)
}

// TriggerTitleGenerationFromPeriodic chooses the best source text for title
// generation given a periodic-style draft. The inline `prompt` may be empty,
// whitespace, or the UI placeholder "(pending)" — all three are treated as
// "no inline prompt". When only `promptName` is meaningful, it is resolved
// to its full text via the configured prompt resolver (workingDir-scoped)
// before being passed to the auxiliary title generator. If resolution fails
// or no resolver is configured, the bare prompt name is used as a fallback.
// No-op when neither source yields any text.
func (bs *BackgroundSession) TriggerTitleGenerationFromPeriodic(prompt, promptName string) {
	bs.titleCoord.triggerFromPeriodic(bs, prompt, promptName)
}

// --- titleDeps implementation (supplies live session dependencies to titleCoordinator) ---

// sessionHasNoTitle reports whether the session currently lacks a name.
func (bs *BackgroundSession) sessionHasNoTitle() bool {
	return SessionNeedsTitle(bs.store, bs.persistedID)
}

// startTitleGeneration kicks off async title generation from the message text.
func (bs *BackgroundSession) startTitleGeneration(message string) {
	GenerateAndSetTitle(TitleGenerationConfig{
		Store:            bs.store,
		SessionID:        bs.persistedID,
		Message:          message,
		Logger:           bs.logger,
		WorkspaceUUID:    bs.workspaceUUID,
		AuxiliaryManager: bs.auxiliaryManager,
		OnTitleGenerated: bs.onTitleGenerated,
	})
}

// resolvePromptName resolves a named workspace prompt to its full text. configured
// is false when no resolver is wired.
func (bs *BackgroundSession) resolvePromptName(name string) (string, bool, error) {
	if bs.promptResolver == nil {
		return "", false, nil
	}
	resolved, err := bs.promptResolver(name, bs.workingDir)
	return strings.TrimSpace(resolved), true, err
}

// titleLogger returns the session-scoped logger.
func (bs *BackgroundSession) titleLogger() *slog.Logger { return bs.logger }

// titleSessionID returns the persisted session ID.
func (bs *BackgroundSession) titleSessionID() string { return bs.persistedID }
