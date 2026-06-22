package handlers

import (
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// HandleDeleteSession handles DELETE /api/sessions/{id}
func (h *Handlers) HandleDeleteSession(w http.ResponseWriter, sessionID string) {
	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Find ALL children recursively BEFORE deletion (they will be cascade-deleted by store.Delete)
	// We need their IDs to close their ACP processes and broadcast deletions
	allChildIDs, err := store.FindAllChildrenRecursive(sessionID)
	if err != nil && h.deps.Logger != nil {
		h.deps.Logger.Warn("Failed to find children for deletion",
			"session_id", sessionID,
			"error", err)
	}

	// Clean up callback index entries for this session and all children
	if h.deps.CallbackIndex != nil {
		h.deps.CallbackIndex.RemoveBySessionID(sessionID)
		for _, childID := range allChildIDs {
			h.deps.CallbackIndex.RemoveBySessionID(childID)
		}
	}

	// Close ACP processes for parent and all children
	if h.deps.SessionManager != nil {
		h.deps.SessionManager.CloseSession(sessionID, "deleted")
		for _, childID := range allChildIDs {
			h.deps.SessionManager.CloseSession(childID, "parent_deleted")
		}
	}

	// Delete from store (cascade-deletes all children recursively)
	if err := store.Delete(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to delete session", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to delete session", http.StatusInternalServerError)
		return
	}

	// Broadcast deletions to all connected WebSocket clients
	if h.deps.BroadcastSessionDeleted != nil {
		h.deps.BroadcastSessionDeleted(sessionID)
		for _, childID := range allChildIDs {
			h.deps.BroadcastSessionDeleted(childID)
		}
	}

	if h.deps.Logger != nil && len(allChildIDs) > 0 {
		h.deps.Logger.Info("Deleted session with children",
			"session_id", sessionID,
			"children_deleted", len(allChildIDs))
	}

	writeNoContent(w)
}
