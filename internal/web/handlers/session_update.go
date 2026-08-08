package handlers

import (
	"net/http"
	"time"

	"github.com/inercia/mitto/internal/session"
)

// SessionUpdateRequest represents a request to update session metadata.
type SessionUpdateRequest struct {
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
	Pinned          *bool   `json:"pinned,omitempty"`           // Deprecated: use Archived instead
	Archived        *bool   `json:"archived,omitempty"`         // If true, session is archived
	BeadsIssue      *string `json:"beads_issue,omitempty"`      // Linked beads issue ID (empty string clears it)
	BackgroundColor *string `json:"background_color,omitempty"` // Conversation accent color, hex (empty string clears it); mitto-8sk
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

	// When archiving a child session, delete it instead (children should never be archived).
	// Reject archiving a NoArchive conversation (mitto-yvel.3) — checked after the child
	// redirect so a protected child remains deletable via that path (deletion is always
	// allowed per epic decision 3; only archiving is gated).
	if req.Archived != nil && *req.Archived {
		meta, err := store.GetMetadata(sessionID)
		if err == nil {
			if meta.ParentSessionID != "" {
				if h.deps.Logger != nil {
					h.deps.Logger.Info("Converting child archive to delete",
						"session_id", sessionID,
						"parent_session_id", meta.ParentSessionID)
				}
				h.HandleDeleteSession(w, sessionID)
				return
			}
			if !meta.IsArchivable() {
				writeErrorJSON(w, http.StatusConflict, "conflict",
					"Conversation is marked non-archivable and cannot be archived; delete it instead")
				return
			}
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
		if req.BackgroundColor != nil {
			meta.BackgroundColor = *req.BackgroundColor
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
				meta.AutoUnarchiveLastAttemptAt = time.Time{}
				// mitto-wub (Defect 3): ACPStartFailureCount is only reset on a
				// SUCCESSFUL ACP start (session_manager.go). Without also resetting it
				// here, a session unarchived while the counter is at/near
				// ACPStartFailureThreshold gets re-archived by the very next transient
				// failure, before it ever gets a chance at the successful start that
				// would normally clear it — turning a one-shot saturation hiccup into a
				// permanent archive/unarchive flap.
				meta.ACPStartFailureCount = 0
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

	// Broadcast the beads_issue link change so the conversation header's
	// linked-issue button/badge updates immediately (mitto: stale beads link
	// indicator). Fires for both set-to-new-id and clear-to-empty transitions.
	if req.BeadsIssue != nil && h.deps.BroadcastSessionBeadsIssueUpdated != nil {
		h.deps.BroadcastSessionBeadsIssueUpdated(sessionID, *req.BeadsIssue)
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

	// Fire the conversationClosed processor pipeline (fire-and-forget). The call
	// itself schedules a goroutine, so it does not block the archive request.
	if req.Archived != nil && *req.Archived && h.deps.ApplyOnCloseProcessors != nil {
		h.deps.ApplyOnCloseProcessors(sessionID, string(session.ArchiveReasonManual))
	}

	// Delete all child sessions when parent is archived
	if req.Archived != nil && *req.Archived {
		// Authoritatively stop the loop on archive so it can never schedule a
		// new run or spawn new children, and the UI badge clears (mitto-efnb).
		if h.deps.StopLoopForArchive != nil {
			h.deps.StopLoopForArchive(sessionID)
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

		// Restore/re-surface any loop configuration that was left disabled by
		// the archive (mitto-vmp): auto-resume archive-related stops, keep
		// other pauses paused, and always re-broadcast the current config.
		h.RestoreLoopOnUnarchive(sessionID)
	}

	writeJSONOK(w, meta)
}

// RestoreLoopOnUnarchive re-surfaces a session's loop configuration after
// unarchive. Loop config (prompt/arguments/trigger/etc.) survives archive in
// loop.json, but MarkStopped(StoppedReasonArchived/ResumeFailures) leaves it
// disabled with no broadcast, so clients never learn it's still there
// (mitto-vmp). This:
//  1. Does nothing when the session has no loop configured.
//  2. Auto-re-enables the loop when it was stopped for an archive-related
//     reason (manual archive or ACP resume-failure auto-archive), kicking off
//     BootstrapOnCompletion for onCompletion loops.
//  3. Leaves other pause reasons (user-paused, max iterations, etc.) alone.
//  4. Always re-broadcasts the current loop state so the UI can re-render it.
//
// Exported so the web package's auto-unarchive recovery scheduler
// (LoopRunner.onAutoUnarchive, wired in server.go) can reuse the same
// restore logic as the manual HTTP unarchive path.
func (h *Handlers) RestoreLoopOnUnarchive(sessionID string) {
	store := h.deps.Store
	if store == nil {
		return
	}

	loopStore := store.Loop(sessionID)
	loop, err := loopStore.Get()
	if err != nil {
		if err != session.ErrLoopNotFound {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to read loop config on unarchive",
					"session_id", sessionID, "error", err)
			}
		}
		return
	}
	if loop == nil {
		return
	}

	archiveRelated := loop.StoppedReason == session.StoppedReasonArchived ||
		loop.StoppedReason == session.StoppedReasonResumeFailures

	if archiveRelated && !loop.Enabled {
		enabled := true
		if err := loopStore.Update(session.LoopUpdate{Enabled: &enabled}); err != nil {
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("Failed to re-enable loop on unarchive",
					"session_id", sessionID, "error", err)
			}
		} else if updated, gErr := loopStore.Get(); gErr == nil && updated != nil && updated.IsOnCompletion() {
			if h.deps.BootstrapOnCompletion != nil {
				h.deps.BootstrapOnCompletion(sessionID)
			}
		}
	}

	// Always re-broadcast the latest loop state so the editor/pill render,
	// whether the loop was just re-enabled or is still paused.
	if final, gErr := loopStore.Get(); gErr == nil && h.deps.BroadcastLoopUpdated != nil {
		h.deps.BroadcastLoopUpdated(sessionID, final)
	}
}
