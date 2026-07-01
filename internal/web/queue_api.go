package web

import (
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

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

// notifyQueueUpdate broadcasts a queue update to all WebSocket clients for a session.
//
// The queue REST handlers live in internal/web/handlers; this server-internal
// helper stays in the web package because it is also used by session_api.go
// (seedQueueWithNamedPrompt) and is wired into the handlers sub-package via
// Deps.NotifyQueueUpdate.
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
//
// Like notifyQueueUpdate, this server-internal helper stays in the web package
// and is wired into the handlers sub-package via Deps.NotifyQueueReorder.
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
