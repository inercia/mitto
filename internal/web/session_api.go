package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/inercia/mitto/internal/session"
)

// handleListSessions handles GET /api/sessions
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to access session store", "error", err)
		}
		http.Error(w, "Failed to access session store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	sessions, err := store.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list sessions", "error", err)
		}
		http.Error(w, "Failed to list sessions", http.StatusInternalServerError)
		return
	}

	// Sort by creation time, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(sessions); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to encode sessions", "error", err)
		}
	}
}

// handleSessionDetail handles GET, PATCH, DELETE /api/sessions/{id} and GET /api/sessions/{id}/events
func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path: /api/sessions/{id} or /api/sessions/{id}/events
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Session ID required", http.StatusBadRequest)
		return
	}

	sessionID := parts[0]
	isEventsRequest := len(parts) > 1 && parts[1] == "events"

	switch r.Method {
	case http.MethodGet:
		s.handleGetSession(w, sessionID, isEventsRequest)
	case http.MethodPatch:
		s.handleUpdateSession(w, r, sessionID)
	case http.MethodDelete:
		s.handleDeleteSession(w, sessionID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetSession handles GET /api/sessions/{id} and GET /api/sessions/{id}/events
func (s *Server) handleGetSession(w http.ResponseWriter, sessionID string, isEventsRequest bool) {
	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to access session store", "error", err)
		}
		http.Error(w, "Failed to access session store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	if isEventsRequest {
		// Return session events
		events, err := store.ReadEvents(sessionID)
		if err != nil {
			if err == session.ErrSessionNotFound {
				http.Error(w, "Session not found", http.StatusNotFound)
				return
			}
			if s.logger != nil {
				s.logger.Error("Failed to read session events", "error", err, "session_id", sessionID)
			}
			http.Error(w, "Failed to read session events", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(events); err != nil {
			if s.logger != nil {
				s.logger.Error("Failed to encode events", "error", err)
			}
		}
	} else {
		// Return session metadata
		meta, err := store.GetMetadata(sessionID)
		if err != nil {
			if err == session.ErrSessionNotFound {
				http.Error(w, "Session not found", http.StatusNotFound)
				return
			}
			if s.logger != nil {
				s.logger.Error("Failed to get session metadata", "error", err, "session_id", sessionID)
			}
			http.Error(w, "Failed to get session metadata", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(meta); err != nil {
			if s.logger != nil {
				s.logger.Error("Failed to encode metadata", "error", err)
			}
		}
	}
}

// SessionUpdateRequest represents a request to update session metadata.
type SessionUpdateRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

// handleUpdateSession handles PATCH /api/sessions/{id}
func (s *Server) handleUpdateSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req SessionUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to access session store", "error", err)
		}
		http.Error(w, "Failed to access session store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	err = store.UpdateMetadata(sessionID, func(meta *session.Metadata) {
		if req.Name != nil {
			meta.Name = *req.Name
		}
		if req.Description != nil {
			meta.Description = *req.Description
		}
	})
	if err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to update session", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to update session", http.StatusInternalServerError)
		return
	}

	// Return updated metadata
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		http.Error(w, "Failed to get updated metadata", http.StatusInternalServerError)
		return
	}

	// Broadcast the rename to all connected WebSocket clients
	if req.Name != nil {
		s.BroadcastSessionRenamed(sessionID, *req.Name)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// handleDeleteSession handles DELETE /api/sessions/{id}
func (s *Server) handleDeleteSession(w http.ResponseWriter, sessionID string) {
	// First, close any running background session for this ID
	// This stops the ACP process and cleans up resources
	if s.sessionManager != nil {
		s.sessionManager.CloseSession(sessionID, "deleted")
	}

	store, err := session.DefaultStore()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to access session store", "error", err)
		}
		http.Error(w, "Failed to access session store", http.StatusInternalServerError)
		return
	}
	defer store.Close()

	if err := store.Delete(sessionID); err != nil {
		if err == session.ErrSessionNotFound {
			http.Error(w, "Session not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to delete session", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to delete session", http.StatusInternalServerError)
		return
	}

	// Broadcast the deletion to all connected WebSocket clients
	s.BroadcastSessionDeleted(sessionID)

	w.WriteHeader(http.StatusNoContent)
}
