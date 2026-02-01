package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// QueueAddRequest represents a request to add a message to the queue.
type QueueAddRequest struct {
	Message  string   `json:"message"`
	ImageIDs []string `json:"image_ids,omitempty"`
}

// QueueMoveRequest represents a request to move a message in the queue.
type QueueMoveRequest struct {
	Direction string `json:"direction"` // "up" or "down"
}

// QueueListResponse represents the response for listing queued messages.
type QueueListResponse struct {
	Messages []session.QueuedMessage `json:"messages"`
	Count    int                     `json:"count"`
}

// QueueConfigResponse represents the queue configuration for API responses.
// This is sent to clients so they can enforce limits client-side and display queue status.
type QueueConfigResponse struct {
	// Enabled indicates whether automatic queue processing is active.
	// When false, messages remain in queue until manually sent.
	Enabled bool `json:"enabled"`

	// MaxSize is the maximum number of messages allowed in the queue.
	// When the queue is full, new messages are rejected.
	MaxSize int `json:"max_size"`

	// DelaySeconds is the delay before sending the next queued message
	// after the agent finishes responding.
	DelaySeconds int `json:"delay_seconds"`
}

// NewQueueConfigResponse creates a QueueConfigResponse from a config.QueueConfig.
// If qc is nil, default values are used.
func NewQueueConfigResponse(qc *config.QueueConfig) QueueConfigResponse {
	return QueueConfigResponse{
		Enabled:      qc.IsEnabled(),
		MaxSize:      qc.GetMaxSize(),
		DelaySeconds: qc.GetDelaySeconds(),
	}
}

// handleSessionQueue handles queue operations for a session.
// Routes: GET/POST/DELETE {prefix}/api/sessions/{id}/queue
//
//	DELETE {prefix}/api/sessions/{id}/queue/{msg_id}
//	GET {prefix}/api/sessions/{id}/queue/{msg_id}
func (s *Server) handleSessionQueue(w http.ResponseWriter, r *http.Request, sessionID, queuePath string) {
	store := s.Store()
	if store == nil {
		http.Error(w, "Session store not available", http.StatusInternalServerError)
		return
	}

	// Check if session exists
	if !store.Exists(sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	queue := store.Queue(sessionID)

	// Parse message ID and sub-action from path if present
	// queuePath is everything after "queue", e.g., "", "/{msg_id}", or "/{msg_id}/move"
	pathPart := strings.TrimPrefix(queuePath, "/")

	if pathPart != "" {
		// Check if there's a sub-action (e.g., /move)
		parts := strings.SplitN(pathPart, "/", 2)
		messageID := parts[0]
		subAction := ""
		if len(parts) > 1 {
			subAction = parts[1]
		}

		// Operations on a specific message
		s.handleQueueMessage(w, r, queue, sessionID, messageID, subAction)
		return
	}

	// Operations on the queue itself
	switch r.Method {
	case http.MethodGet:
		s.handleListQueue(w, queue)
	case http.MethodPost:
		s.handleAddToQueue(w, r, queue, sessionID)
	case http.MethodDelete:
		s.handleClearQueue(w, queue, sessionID)
	default:
		methodNotAllowed(w)
	}
}

// handleListQueue handles GET {prefix}/api/sessions/{id}/queue
func (s *Server) handleListQueue(w http.ResponseWriter, queue *session.Queue) {
	messages, err := queue.List()
	if err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to list queue", "error", err)
		}
		http.Error(w, "Failed to list queue", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, QueueListResponse{
		Messages: messages,
		Count:    len(messages),
	})
}

// handleAddToQueue handles POST {prefix}/api/sessions/{id}/queue
func (s *Server) handleAddToQueue(w http.ResponseWriter, r *http.Request, queue *session.Queue, sessionID string) {
	var req QueueAddRequest
	if !parseJSONBody(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Message) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "empty_message", "Message cannot be empty")
		return
	}

	// Get client ID from request context if available (e.g., from auth)
	clientID := ""

	// Get queue config from session (for max size and auto-generate titles)
	var queueConfig *config.QueueConfig
	if s.sessionManager != nil {
		if bs := s.sessionManager.GetSession(sessionID); bs != nil {
			queueConfig = bs.GetQueueConfig()
		}
	}

	// Get queue max size from config (or use default)
	maxSize := config.DefaultQueueMaxSize
	if queueConfig != nil {
		maxSize = queueConfig.GetMaxSize()
	}

	msg, err := queue.Add(req.Message, req.ImageIDs, clientID, maxSize)
	if err != nil {
		if errors.Is(err, session.ErrQueueFull) {
			writeErrorJSON(w, http.StatusConflict, "queue_full",
				fmt.Sprintf("Queue is full. Maximum %d messages allowed.", maxSize))
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to add message to queue", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to add message to queue", http.StatusInternalServerError)
		return
	}

	// Notify observers about queue update
	s.notifyQueueUpdate(sessionID, "added", msg.ID)

	// Enqueue title generation if enabled
	if s.queueTitleWorker != nil && queueConfig.ShouldAutoGenerateTitles() {
		s.queueTitleWorker.Enqueue(QueueTitleRequest{
			SessionID: sessionID,
			MessageID: msg.ID,
			Message:   req.Message,
		})
	}

	// Try to process the queued message immediately if agent is idle
	if s.sessionManager != nil {
		if bs := s.sessionManager.GetSession(sessionID); bs != nil {
			go bs.TryProcessQueuedMessage()
		}
	}

	writeJSONCreated(w, msg)
}

// handleClearQueue handles DELETE {prefix}/api/sessions/{id}/queue
func (s *Server) handleClearQueue(w http.ResponseWriter, queue *session.Queue, sessionID string) {
	if err := queue.Clear(); err != nil {
		if s.logger != nil {
			s.logger.Error("Failed to clear queue", "error", err, "session_id", sessionID)
		}
		http.Error(w, "Failed to clear queue", http.StatusInternalServerError)
		return
	}

	// Notify observers about queue update
	s.notifyQueueUpdate(sessionID, "cleared", "")

	writeNoContent(w)
}

// handleQueueMessage handles operations on a specific queued message.
// Routes: GET/DELETE {prefix}/api/sessions/{id}/queue/{msg_id}
//
//	POST {prefix}/api/sessions/{id}/queue/{msg_id}/move
func (s *Server) handleQueueMessage(w http.ResponseWriter, r *http.Request, queue *session.Queue, sessionID, messageID, subAction string) {
	// Handle sub-actions first
	if subAction == "move" {
		if r.Method == http.MethodPost {
			s.handleMoveQueueMessage(w, r, queue, sessionID, messageID)
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
		s.handleGetQueueMessage(w, queue, messageID)
	case http.MethodDelete:
		s.handleDeleteQueueMessage(w, queue, sessionID, messageID)
	default:
		methodNotAllowed(w)
	}
}

// handleGetQueueMessage handles GET {prefix}/api/sessions/{id}/queue/{msg_id}
func (s *Server) handleGetQueueMessage(w http.ResponseWriter, queue *session.Queue, messageID string) {
	msg, err := queue.Get(messageID)
	if err != nil {
		if errors.Is(err, session.ErrMessageNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to get queue message", "error", err, "message_id", messageID)
		}
		http.Error(w, "Failed to get queue message", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w, msg)
}

// handleDeleteQueueMessage handles DELETE {prefix}/api/sessions/{id}/queue/{msg_id}
func (s *Server) handleDeleteQueueMessage(w http.ResponseWriter, queue *session.Queue, sessionID, messageID string) {
	if err := queue.Remove(messageID); err != nil {
		if errors.Is(err, session.ErrMessageNotFound) {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		if s.logger != nil {
			s.logger.Error("Failed to delete queue message", "error", err, "session_id", sessionID, "message_id", messageID)
		}
		http.Error(w, "Failed to delete queue message", http.StatusInternalServerError)
		return
	}

	// Notify observers about queue update
	s.notifyQueueUpdate(sessionID, "removed", messageID)

	writeNoContent(w)
}

// handleMoveQueueMessage handles POST {prefix}/api/sessions/{id}/queue/{msg_id}/move
func (s *Server) handleMoveQueueMessage(w http.ResponseWriter, r *http.Request, queue *session.Queue, sessionID, messageID string) {
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
		if s.logger != nil {
			s.logger.Error("Failed to move queue message", "error", err, "session_id", sessionID, "message_id", messageID, "direction", req.Direction)
		}
		http.Error(w, "Failed to move queue message", http.StatusInternalServerError)
		return
	}

	// Notify observers about queue reorder
	s.notifyQueueReorder(sessionID, messages)

	// Return the updated queue
	writeJSONOK(w, QueueListResponse{
		Messages: messages,
		Count:    len(messages),
	})
}

// notifyQueueUpdate broadcasts a queue update to all WebSocket clients for a session.
func (s *Server) notifyQueueUpdate(sessionID, action, messageID string) {
	// Get the background session to notify its observers
	if s.sessionManager == nil {
		return
	}
	bs := s.sessionManager.GetSession(sessionID)
	if bs == nil {
		return
	}

	// Get current queue length
	store := s.Store()
	if store == nil {
		return
	}
	queue := store.Queue(sessionID)
	length, _ := queue.Len()

	// Notify all observers
	bs.NotifyQueueUpdated(length, action, messageID)
}

// notifyQueueReorder broadcasts a queue reorder to all WebSocket clients for a session.
func (s *Server) notifyQueueReorder(sessionID string, messages []session.QueuedMessage) {
	// Get the background session to notify its observers
	if s.sessionManager == nil {
		return
	}
	bs := s.sessionManager.GetSession(sessionID)
	if bs == nil {
		return
	}

	// Notify all observers
	bs.NotifyQueueReordered(messages)
}
