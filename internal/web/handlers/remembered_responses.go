package handlers

import (
	"errors"
	"strings"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/prompts"
	"github.com/inercia/mitto/internal/rememberedargs"
	"github.com/inercia/mitto/internal/session"
)

// lookupWorkspacePrompt returns the workspace prompt whose name matches
// promptName case-insensitively, the same rule queue dispatch uses, or nil.
func (h *Handlers) lookupWorkspacePrompt(workingDir, promptName string) *config.WebPrompt {
	if h.deps.GetWorkspacePromptsAll == nil || workingDir == "" {
		return nil
	}
	all := h.deps.GetWorkspacePromptsAll(workingDir)
	for i := range all {
		if strings.EqualFold(all[i].Name, promptName) {
			return &all[i]
		}
	}
	return nil
}

// responsesDeclaredKeys returns the argument keys a "Remember responses"
// snapshot may hold: every declared parameter except `remember: never`, plus
// the nested "<Picker>_Args" blob of each prompts-type picker.
func responsesDeclaredKeys(params []prompts.PromptParameter) map[string]bool {
	keys := map[string]bool{}
	for _, p := range params {
		if p.Remember == prompts.RememberNever {
			continue
		}
		keys[p.Name] = true
		if p.Type == "prompts" {
			keys[p.Name+"_Args"] = true
		}
	}
	return keys
}

// loadConversationResponses returns the tri-state conversation-local state for
// the triple. Any failure yields the unset state so callers fall back to the
// legacy behavior; only unexpected failures are logged.
func (h *Handlers) loadConversationResponses(sessionID, workspaceUUID, promptName string) rememberedargs.ResponsesState {
	if sessionID == "" || workspaceUUID == "" || h.deps.ConversationResponses == nil {
		return rememberedargs.ResponsesState{}
	}
	st, err := h.deps.ConversationResponses.Get(sessionID, workspaceUUID, promptName)
	if err != nil {
		if !errors.Is(err, session.ErrSessionNotFound) && h.deps.Logger != nil {
			h.deps.Logger.Warn("Failed to load conversation remembered responses",
				"session_id", sessionID,
				"prompt", promptName,
				"error", err)
		}
		return rememberedargs.ResponsesState{}
	}
	return st
}

// applyRememberResponses records the caller's explicit "Remember responses"
// choice for a named prompt that was just enqueued in the session. enabled
// replaces the snapshot with the declared submitted values; disabled stores
// the tombstone and drops values. Undeclared keys are never persisted.
func (h *Handlers) applyRememberResponses(sessionID string, bs *conversation.BackgroundSession, promptName string, args map[string]string, enabled bool) error {
	if h.deps.ConversationResponses == nil || bs == nil || promptName == "" {
		return nil
	}
	target := h.lookupWorkspacePrompt(bs.GetWorkingDir(), promptName)
	if target == nil {
		return nil
	}
	workspaceUUID := bs.GetWorkspaceUUID()
	if !enabled {
		return h.deps.ConversationResponses.Disable(sessionID, workspaceUUID, promptName)
	}
	declared := responsesDeclaredKeys(target.Parameters)
	snapshot := make(map[string]string, len(declared))
	for k, v := range args {
		if declared[k] {
			snapshot[k] = v
		}
	}
	return h.deps.ConversationResponses.ReplaceEnabled(sessionID, workspaceUUID, promptName, snapshot)
}

// conversationResponsesDisabled reports whether the conversation has opted out
// of remembering this prompt. A disabled tombstone suppresses the legacy
// conversation and folder-scope writes for the prompt.
func (h *Handlers) conversationResponsesDisabled(sessionID, workspaceUUID, promptName string) bool {
	st := h.loadConversationResponses(sessionID, workspaceUUID, promptName)
	return st.State == rememberedargs.ResponsesDisabled
}
