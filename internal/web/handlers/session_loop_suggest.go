// Package handlers read-only endpoint that suggests a loop draft pre-filled
// from the most recent named prompt in the session's event log (mitto-qff).
// Walks the last N events for a user_prompt with a non-empty PromptName,
// resolves that name against the workspace's merged prompt list, and — when
// the resolved prompt carries a loop: frontmatter block — returns the merged
// LoopPrompt draft (Enabled=false) that the caller can PUT verbatim to /loop.
package handlers

import (
	"net/http"
	"strings"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// suggestLoopEventScanLimit caps the number of events we look back for the
// most-recent named user prompt. Larger windows waste I/O and rarely surface
// a fresh suggestion — a named prompt older than ~50 events is unlikely to
// still reflect the user's current intent.
const suggestLoopEventScanLimit = 50

// handleSuggestLoopFromRecent handles GET /api/sessions/{id}/loop/suggest-from-recent.
// Returns 200 + a validated LoopPrompt draft when the most recent user prompt
// in the session's event log is a named prompt whose YAML frontmatter carries
// a loop: block; returns 404 in every other case so the frontend can fall
// back to today's blank-draft behaviour.
//
// This endpoint is read-only: it never writes session state.
func (h *Handlers) handleSuggestLoopFromRecent(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	if h.deps.Store == nil || h.deps.GetWorkspacePromptsAll == nil {
		h.debugSuggestNotFound(sessionID, "deps unavailable")
		writeErrorJSON(w, http.StatusNotFound, "", "No loop suggestion available")
		return
	}

	meta, err := h.deps.Store.GetMetadata(sessionID)
	if err != nil || meta.WorkingDir == "" {
		h.debugSuggestNotFound(sessionID, "no metadata or empty working_dir")
		writeErrorJSON(w, http.StatusNotFound, "", "No loop suggestion available")
		return
	}

	events, err := h.deps.Store.ReadEventsLastReverse(sessionID, suggestLoopEventScanLimit, 0)
	if err != nil {
		h.debugSuggestNotFound(sessionID, "ReadEventsLastReverse failed")
		writeErrorJSON(w, http.StatusNotFound, "", "No loop suggestion available")
		return
	}

	// Events are already newest-first. Find the first user_prompt with a
	// non-empty PromptName.
	var promptName string
	for _, ev := range events {
		if ev.Type != session.EventTypeUserPrompt {
			continue
		}
		decoded, derr := session.DecodeEventData(ev)
		if derr != nil {
			continue
		}
		up, ok := decoded.(session.UserPromptData)
		if !ok {
			continue
		}
		if up.PromptName != "" {
			promptName = up.PromptName
			break
		}
	}
	if promptName == "" {
		h.debugSuggestNotFound(sessionID, "no recent named prompt in window")
		writeErrorJSON(w, http.StatusNotFound, "", "No loop suggestion available")
		return
	}

	// Resolve the prompt against the workspace's merged prompt list.
	// Match is case-insensitive to mirror MCP / REST prompt resolution.
	prompts := h.deps.GetWorkspacePromptsAll(meta.WorkingDir)
	var (
		resolvedName string
		resolvedLoop *configPkg.PromptLoop
	)
	for i := range prompts {
		wp := &prompts[i]
		if strings.EqualFold(wp.Name, promptName) {
			resolvedName = wp.Name
			resolvedLoop = wp.Loop
			break
		}
	}
	if resolvedLoop == nil {
		h.debugSuggestNotFound(sessionID, "prompt not in workspace list or has no loop: block")
		writeErrorJSON(w, http.StatusNotFound, "", "No loop suggestion available")
		return
	}

	p := &session.LoopPrompt{PromptName: resolvedName, Enabled: false}
	applyPromptLoopDefaultsToLoopPrompt(p, resolvedLoop, nil)

	if err := p.Validate(); err != nil {
		h.debugSuggestNotFound(sessionID, "merged scaffold failed Validate()")
		writeErrorJSON(w, http.StatusNotFound, "", "No loop suggestion available")
		return
	}

	writeJSONOK(w, p)
}

// debugSuggestNotFound logs a suggest-from-recent 404 branch at Debug so
// operators can trace why a caller got the blank-draft fallback.
func (h *Handlers) debugSuggestNotFound(sessionID, reason string) {
	if h.deps.Logger != nil {
		h.deps.Logger.Debug("suggest-loop-from-recent: no suggestion",
			"session_id", sessionID, "reason", reason)
	}
}
