package handlers

import (
	"errors"
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// handleQueueMessage handles operations on a specific queued message.
// Routes: GET/DELETE {prefix}/api/sessions/{id}/queue/{msg_id}
//
//	POST {prefix}/api/sessions/{id}/queue/{msg_id}/move
func (h *Handlers) handleQueueMessage(w http.ResponseWriter, r *http.Request, queue *session.Queue, sessionID, messageID, subAction string) {
	// Handle sub-actions first
	if subAction == "move" {
		if r.Method == http.MethodPost {
			h.handleMoveQueueMessage(w, r, queue, sessionID, messageID)
			return
		}
		methodNotAllowed(w)
		return
	}

	// Handle direct message operations (no sub-action)
	if subAction != "" {
		http.Error(w, "Unknown action", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGetQueueMessage(w, queue, messageID)
	case http.MethodDelete:
		h.handleDeleteQueueMessage(w, queue, sessionID, messageID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetQueueMessage handles GET {prefix}/api/sessions/{id}/queue/{msg_id}
func (h *Handlers) handleGetQueueMessage(w http.ResponseWriter, queue *session.Queue, messageID string) {
	msg, err := queue.Get(messageID)
	if err != nil {
		if errors.Is(err, session.ErrMessageNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to get queue message", "error", err, "message_id", messageID)
		}
		http.Error(w, "Failed to get queue message", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, msg)
}

// handleDeleteQueueMessage handles DELETE {prefix}/api/sessions/{id}/queue/{msg_id}
func (h *Handlers) handleDeleteQueueMessage(w http.ResponseWriter, queue *session.Queue, sessionID, messageID string) {
	if err := queue.Remove(messageID); err != nil {
		if errors.Is(err, session.ErrMessageNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to delete queue message", "error", err, "session_id", sessionID, "message_id", messageID)
		}
		http.Error(w, "Failed to delete queue message", http.StatusInternalServerError)
		return
	}

	// Notify observers about queue update
	if h.deps.NotifyQueueUpdate != nil {
		h.deps.NotifyQueueUpdate(sessionID, "removed", messageID)
	}

	writeNoContent(w)
}

// handleMoveQueueMessage handles POST {prefix}/api/sessions/{id}/queue/{msg_id}/move
func (h *Handlers) handleMoveQueueMessage(w http.ResponseWriter, r *http.Request, queue *session.Queue, sessionID, messageID string) {
	var req QueueMoveRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if req.Direction != "up" && req.Direction != "down" {
		writeErrorJSON(w, http.StatusBadRequest, "invalid_direction", "Direction must be 'up' or 'down'")
		return
	}

	messages, err := queue.Move(messageID, req.Direction)
	if err != nil {
		if errors.Is(err, session.ErrMessageNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to move queue message", "error", err, "session_id", sessionID, "message_id", messageID, "direction", req.Direction)
		}
		http.Error(w, "Failed to move queue message", http.StatusInternalServerError)
		return
	}

	// Notify observers about queue reorder
	if h.deps.NotifyQueueReorder != nil {
		h.deps.NotifyQueueReorder(sessionID, messages)
	}

	// Return the updated queue
	writeJSONOK(w, QueueListResponse{
		Messages: messages,
		Count:    len(messages),
	})
}
