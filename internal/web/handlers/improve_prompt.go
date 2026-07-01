package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// HandleImprovePrompt improves a user prompt via the workspace-scoped auxiliary
// session. POST /api/aux/improve-prompt.
func (h *Handlers) HandleImprovePrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Parse request body
	var req struct {
		Prompt        string `json:"prompt"`
		WorkspaceUUID string `json:"workspace_uuid"` // Required for workspace-scoped auxiliary
	}
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.Prompt == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Prompt is required")
		return
	}

	if req.WorkspaceUUID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Workspace UUID is required")
		return
	}

	// Check if auxiliary manager is initialized
	if h.deps.ImprovePrompt == nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Auxiliary manager not initialized")
		}
		writeErrorJSON(w, http.StatusServiceUnavailable, "", "Service unavailable")
		return
	}

	// Create a context with timeout for the auxiliary request; stay below the
	// 30s middleware cap so we can write a clear error before it fires.
	ctx, cancel := context.WithTimeout(r.Context(), auxBackedRequestTimeout)
	defer cancel()

	// Call the workspace-scoped auxiliary manager to improve the prompt
	improved, err := h.deps.ImprovePrompt(ctx, req.WorkspaceUUID, req.Prompt)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeRetryableUnavailable(w, "The AI helper is starting up. Please try again in a few seconds.", 5)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to improve prompt",
				"error", err,
				"workspace_uuid", req.WorkspaceUUID)
		}
		errMsg := err.Error()
		var userMsg string
		if strings.Contains(errMsg, "broken pipe") ||
			strings.Contains(errMsg, "peer disconnected") ||
			strings.Contains(errMsg, "connection reset") ||
			strings.Contains(errMsg, "process has exited") {
			userMsg = "The AI agent process crashed. Please try again in a moment."
		} else {
			userMsg = "Failed to improve prompt"
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", userMsg)
		return
	}

	// Return the improved prompt
	writeJSONOK(w, map[string]string{
		"improved_prompt": improved,
	})
}
