package web

import (
	"encoding/json"
	"net/http"

	"github.com/inercia/mitto/internal/session"
)

// PruneRequest is the request body for pruning a session's events.
type PruneRequest struct {
	// KeepLast is the number of most-recent events to keep after pruning.
	// Defaults to session.DefaultPruneKeepLast (500) if zero or not provided.
	// Minimum is session.MinPruneKeepLast (50).
	KeepLast int `json:"keep_last"`
}

// PruneResponse is the response body for a successful prune operation.
type PruneResponse struct {
	// PrunedCount is the number of events that were removed.
	PrunedCount int `json:"pruned_count"`
	// RemainingCount is the number of events remaining after pruning.
	RemainingCount int `json:"remaining_count"`
	// NewMaxSeq is the new maximum sequence number after renumbering.
	NewMaxSeq int64 `json:"new_max_seq"`
}

// handleSessionPrune handles POST /api/sessions/{id}/prune
// It prunes old events from the session, keeping the last N events.
// The session must not be actively processing a prompt when prune is called.
func (s *Server) handleSessionPrune(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Verify session exists
	if _, err := store.GetMetadata(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to get session", http.StatusInternalServerError)
		return
	}

	// Reject pruning while a prompt is in progress — pruning changes seq numbers
	// which would corrupt in-flight streaming events.
	if s.sessionManager != nil {
		if bs := s.sessionManager.GetSession(sessionID); bs != nil {
			if bs.IsPrompting() {
				http.Error(w, "Session is currently processing a prompt — wait for it to finish before pruning", http.StatusConflict)
				return
			}
		}
	}

	// Parse request body; use defaults on empty or invalid body.
	var req PruneRequest
	if r.ContentLength != 0 {
		// Ignore decode errors — we fall back to defaults below.
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	keepLast := req.KeepLast
	if keepLast <= 0 {
		keepLast = session.DefaultPruneKeepLast
	}
	if keepLast < session.MinPruneKeepLast {
		http.Error(w, "keep_last must be at least 50", http.StatusBadRequest)
		return
	}

	// Perform the prune
	result, err := store.PruneKeepLast(sessionID, keepLast)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to prune session", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to prune session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Read updated metadata to get authoritative counts
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		http.Error(w, "Failed to read updated metadata after prune", http.StatusInternalServerError)
		return
	}

	pruned := 0
	if result != nil {
		pruned = result.EventsRemoved
	}

	writeJSONOK(w, PruneResponse{
		PrunedCount:    pruned,
		RemainingCount: meta.EventCount,
		NewMaxSeq:      meta.MaxSeq,
	})
}
