package handlers

import (
	"net/http"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// SessionUpdateRequest represents a request to update session metadata.
type SessionUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Pinned      *bool   `json:"pinned,omitempty"`      // Deprecated: use Archived instead
	Archived    *bool   `json:"archived,omitempty"`    // If true, session is archived
	BeadsIssue  *string `json:"beads_issue,omitempty"` // Linked beads issue ID (empty string clears it)
}

// archiveWaitTimeout is the maximum time to wait for a response to complete when archiving.
const archiveWaitTimeout = 5 * time.Minute

// HandleUpdateSession handles PATCH /api/sessions/{id}
func (h *Handlers) HandleUpdateSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req SessionUpdateRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	// Use the server's session store (owned by the server, not closed by this handler)
	store := h.deps.Store
	if store == nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Session store not available")
		return
	}

	// When archiving a child session, delete it instead (children should never be archived)
	if req.Archived != nil && *req.Archived {
		meta, err := store.GetMetadata(sessionID)
		if err == nil && meta.ParentSessionID != "" {
			if h.deps.Logger != nil {
				h.deps.Logger.Info("Converting child archive to delete",
					"session_id", sessionID,
					"parent_session_id", meta.ParentSessionID)
			}
			h.HandleDeleteSession(w, sessionID)
			return
		}
	}

	// Handle archive lifecycle: wait for response and stop ACP
	if req.Archived != nil && *req.Archived {
		if h.deps.SessionManager != nil {
			// Wait for any active response to complete before archiving
			// This ensures we don't interrupt an in-progress agent response
			reason := "archived"
			if !h.deps.SessionManager.CloseSessionGracefully(sessionID, reason, archiveWaitTimeout) {
				// Timeout waiting for response - still proceed with archive but log warning
				if h.deps.Logger != nil {
					h.deps.Logger.Warn("Timeout waiting for response before archiving, proceeding anyway",
						"session_id", sessionID)
				}
				// Force close the session
				reason = "archived_timeout"
				h.deps.SessionManager.CloseSession(sessionID, reason)
			}
			// Broadcast that ACP was stopped
			if h.deps.BroadcastACPStopped != nil {
				h.deps.BroadcastACPStopped(sessionID, reason)
			}
		}
	}

	err := store.UpdateMetadata(sessionID, func(meta *session.Metadata) {
		if req.Name != nil {
			meta.Name = *req.Name
		}
		if req.Description != nil {
			meta.Description = *req.Description
		}
		if req.BeadsIssue != nil {
			meta.BeadsIssue = *req.BeadsIssue
		}
		if req.Pinned != nil {
			meta.Pinned = *req.Pinned
		}
		if req.Archived != nil {
			meta.Archived = *req.Archived
			if *req.Archived {
				// Set archived timestamp and reason when archiving
				meta.ArchivedAt = time.Now()
				meta.ArchiveReason = session.ArchiveReasonManual
			} else {
				// Clear archived timestamp and reason when unarchiving
				meta.ArchivedAt = time.Time{}
				meta.ArchiveReason = ""
			}
		}
	})
	if err != nil {
		if err == session.ErrSessionNotFound {
			writeErrorJSON(w, http.StatusNotFound, "", "Session not found")
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to update session", "error", err, "session_id", sessionID)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to update session")
		return
	}

	// Return updated metadata
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to get updated metadata")
		return
	}

	// Broadcast the rename to all connected WebSocket clients
	if req.Name != nil && h.deps.BroadcastSessionRenamed != nil {
		h.deps.BroadcastSessionRenamed(sessionID, *req.Name)
	}

	// Broadcast the pinned state change to all connected WebSocket clients
	if req.Pinned != nil && h.deps.BroadcastSessionPinned != nil {
		h.deps.BroadcastSessionPinned(sessionID, *req.Pinned)
	}

	// Broadcast the archived state change to all connected WebSocket clients.
	// For archive: broadcast immediately so clients know to disconnect.
	// For unarchive: broadcast AFTER ResumeSession so the session is already in
	// sm.sessions when clients reconnect (prevents pendingResumes race).
	if req.Archived != nil && *req.Archived && h.deps.BroadcastSessionArchived != nil {
		h.deps.BroadcastSessionArchived(sessionID, true, session.ArchiveReasonManual)
	}

	// Delete all child sessions when parent is archived
	if req.Archived != nil && *req.Archived {
		// Authoritatively stop the periodic loop on archive so it can never schedule a
		// new run or spawn new children, and the UI badge clears (mitto-efnb).
		if h.deps.StopPeriodicForArchive != nil {
			h.deps.StopPeriodicForArchive(sessionID)
		}
		if h.deps.SessionManager != nil {
			go h.deps.SessionManager.DeleteChildSessions(sessionID)
		}
	}

	// Handle unarchive lifecycle: restart ACP session FIRST, then broadcast
	if req.Archived != nil && !*req.Archived {
		if h.deps.SessionManager != nil {
			// Resume the session to restart the ACP connection
			_, err := h.deps.SessionManager.ResumeSession(sessionID, meta.Name, meta.WorkingDir)
			if err != nil {
				// Log the error but don't fail the request - the session is unarchived
				// The ACP will be started when the user sends a message
				if h.deps.Logger != nil {
					h.deps.Logger.Warn("Failed to resume ACP session after unarchive",
						"session_id", sessionID,
						"error", err)
				}
				// Broadcast ACP start failure to all clients
				if h.deps.BroadcastACPStartFailed != nil {
					h.deps.BroadcastACPStartFailed(sessionID, meta.Name, err, "")
				}
			} else {
				if h.deps.Logger != nil {
					h.deps.Logger.Info("Resumed ACP session after unarchive",
						"session_id", sessionID)
				}
				// Broadcast that ACP was started
				if h.deps.BroadcastACPStarted != nil {
					h.deps.BroadcastACPStarted(sessionID)
				}
			}
		}
		// Broadcast AFTER resume — session is now in sm.sessions
		if h.deps.BroadcastSessionArchived != nil {
			h.deps.BroadcastSessionArchived(sessionID, false)
		}
	}

	writeJSONOK(w, meta)
}
