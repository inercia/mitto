package conversation

// Title generation cluster for BackgroundSession.

import "strings"

// NeedsTitle returns true if the session has no title yet and needs auto-title generation.
// Returns false if the session already has a title (either auto-generated or user-set).
func (bs *BackgroundSession) NeedsTitle() bool {
	if bs.store == nil || bs.persistedID == "" {
		return false
	}
	meta, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		return false
	}
	return meta.Name == ""
}

// retryTitleGenerationIfNeeded checks if the session still needs a title and
// triggers async title generation. This is called after prompt completion to catch:
// (1) failed initial title generation attempts (e.g., context deadline exceeded)
// (2) prompts that arrived via paths that don't trigger title generation
//
//	(queue processing, MCP send_prompt, periodic prompts)
func (bs *BackgroundSession) retryTitleGenerationIfNeeded(message string) {
	if !bs.NeedsTitle() {
		return
	}

	if bs.logger != nil {
		bs.logger.Info("Session still has no title after prompt completion, retrying title generation",
			"session_id", bs.persistedID)
	}

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

// TriggerTitleGeneration triggers async title generation if the session has no title yet.
// This is the public interface used by MCP tools and API handlers to generate titles
// for sessions that received prompts via paths that don't normally trigger title generation
// (e.g., periodic prompt configuration, queue processing).
func (bs *BackgroundSession) TriggerTitleGeneration(message string) {
	bs.retryTitleGenerationIfNeeded(message)
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
	inline := strings.TrimSpace(prompt)
	if inline != "" && inline != "(pending)" {
		bs.retryTitleGenerationIfNeeded(inline)
		return
	}
	name := strings.TrimSpace(promptName)
	if name == "" {
		return
	}
	if bs.promptResolver != nil {
		if resolved, err := bs.promptResolver(name, bs.workingDir); err == nil && strings.TrimSpace(resolved) != "" {
			bs.retryTitleGenerationIfNeeded(strings.TrimSpace(resolved))
			return
		} else if err != nil && bs.logger != nil {
			bs.logger.Warn("Could not resolve periodic prompt name for title generation; falling back to name",
				"prompt_name", name, "error", err)
		}
	}
	bs.retryTitleGenerationIfNeeded(name)
}
