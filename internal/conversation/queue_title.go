package conversation

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// QueueTitleRequest represents a request to generate a title for a queued message.
type QueueTitleRequest struct {
	SessionID string
	MessageID string
	Message   string
}

type queueTitleJobKey struct {
	sessionID string
	messageID string
}

// QueueTitleWorker processes title generation requests sequentially.
// This prevents overwhelming the auxiliary conversation with concurrent requests.
type QueueTitleWorker struct {
	store            *session.Store
	logger           *slog.Logger
	requests         chan QueueTitleRequest
	wg               sync.WaitGroup
	ctx              context.Context
	cancel           context.CancelFunc
	sessionManager   *SessionManager                      // Session manager for workspace lookup
	auxiliaryManager *auxiliary.WorkspaceAuxiliaryManager // Auxiliary manager for title generation
	jobsMu           sync.Mutex
	jobs             map[queueTitleJobKey]struct{}

	// OnTitleGenerated is called when a title is successfully generated.
	// It receives the session ID, message ID, and the generated title.
	OnTitleGenerated func(sessionID, messageID, title string)
}

// NewQueueTitleWorker creates a new title generation worker.
// The worker processes requests sequentially in a background goroutine.
func NewQueueTitleWorker(store *session.Store, sessionManager *SessionManager, auxiliaryManager *auxiliary.WorkspaceAuxiliaryManager, logger *slog.Logger) *QueueTitleWorker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &QueueTitleWorker{
		store:            store,
		sessionManager:   sessionManager,
		auxiliaryManager: auxiliaryManager,
		logger:           logger,
		requests:         make(chan QueueTitleRequest, 100), // Buffer up to 100 requests
		ctx:              ctx,
		cancel:           cancel,
		jobs:             make(map[queueTitleJobKey]struct{}),
	}
	w.wg.Add(1)
	go w.run()
	return w
}

// Enqueue adds a title generation request to the queue.
// This method is non-blocking; the request will be processed asynchronously.
func (w *QueueTitleWorker) Enqueue(req QueueTitleRequest) {
	if !w.claimJob(req) {
		if w.logger != nil {
			w.logger.Debug("Coalescing duplicate queue title generation request",
				"session_id", req.SessionID,
				"message_id", req.MessageID)
		}
		return
	}
	select {
	case w.requests <- req:
		if w.logger != nil {
			w.logger.Debug("Enqueued title generation request",
				"session_id", req.SessionID,
				"message_id", req.MessageID)
		}
	default:
		w.releaseJob(req)
		// Channel full, drop the request
		if w.logger != nil {
			w.logger.Warn("Title generation queue full, dropping request",
				"session_id", req.SessionID,
				"message_id", req.MessageID)
		}
	}
}

// Close stops the worker and waits for it to finish.
func (w *QueueTitleWorker) Close() {
	w.cancel()
	close(w.requests)
	w.wg.Wait()
	w.jobsMu.Lock()
	clear(w.jobs)
	w.jobsMu.Unlock()
}

func (w *QueueTitleWorker) claimJob(req QueueTitleRequest) bool {
	key := queueTitleJobKey{sessionID: req.SessionID, messageID: req.MessageID}
	w.jobsMu.Lock()
	defer w.jobsMu.Unlock()
	if w.jobs == nil {
		w.jobs = make(map[queueTitleJobKey]struct{})
	}
	if _, exists := w.jobs[key]; exists {
		return false
	}
	w.jobs[key] = struct{}{}
	return true
}

func (w *QueueTitleWorker) releaseJob(req QueueTitleRequest) {
	key := queueTitleJobKey{sessionID: req.SessionID, messageID: req.MessageID}
	w.jobsMu.Lock()
	delete(w.jobs, key)
	w.jobsMu.Unlock()
}

// run processes title generation requests sequentially.
func (w *QueueTitleWorker) run() {
	defer w.wg.Done()

	for req := range w.requests {
		select {
		case <-w.ctx.Done():
			w.releaseJob(req)
			return
		default:
			w.processRequest(req)
			w.releaseJob(req)
		}
	}
}

// processRequest generates a title for a single queued message.
func (w *QueueTitleWorker) processRequest(req QueueTitleRequest) {
	// Get workspace UUID for this session
	workspaceUUID := w.sessionManager.GetWorkspaceUUIDForSession(req.SessionID)
	if workspaceUUID == "" {
		if w.logger != nil {
			w.logger.Warn("Cannot generate queue title: session has no workspace",
				"session_id", req.SessionID,
				"message_id", req.MessageID)
		}
		return
	}

	var queue *session.Queue
	if w.store != nil {
		queue = w.store.Queue(req.SessionID)
		queued, err := queue.Get(req.MessageID)
		if err != nil {
			if !errors.Is(err, session.ErrMessageNotFound) && w.logger != nil {
				w.logger.Error("Failed to read queue message before title generation", "error", err,
					"session_id", req.SessionID, "message_id", req.MessageID)
			}
			return
		}
		if queued.Title == "" {
			fallback := GenerateQuickTitle(req.Message)
			if fallback != "" {
				if err := queue.UpdateTitle(req.MessageID, fallback); err != nil {
					if !errors.Is(err, session.ErrMessageNotFound) && w.logger != nil {
						w.logger.Error("Failed to set queue message fallback title", "error", err,
							"session_id", req.SessionID, "message_id", req.MessageID)
					}
					return
				}
				if w.OnTitleGenerated != nil {
					w.OnTitleGenerated(req.SessionID, req.MessageID, fallback)
				}
			}
		}
	}

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			delay := titleRetryDelay(attempt)
			timer := time.NewTimer(delay)
			select {
			case <-w.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if queue != nil {
				if _, err := queue.Get(req.MessageID); err != nil {
					if !errors.Is(err, session.ErrMessageNotFound) && w.logger != nil {
						w.logger.Error("Failed to check queue message before title retry", "error", err,
							"session_id", req.SessionID, "message_id", req.MessageID)
					}
					return
				}
			}
		}

		ctx, cancel := context.WithTimeout(w.ctx, 5*time.Minute)
		title, err := w.auxiliaryManager.GenerateQueuedMessageTitle(ctx, workspaceUUID, req.Message)
		cancel()
		if err != nil {
			if errors.Is(err, acperrors.ErrProcessBusy) {
				if w.logger != nil {
					w.logger.Info("Queue title generation waiting for process quiescence",
						"session_id", req.SessionID, "message_id", req.MessageID,
						"workspace_uuid", workspaceUUID, "attempt", attempt+1,
						"retry_delay", titleRetryDelay(attempt+1))
				}
				continue
			}
			if w.logger != nil {
				w.logger.Error("Failed to generate queue message title", "error", err,
					"session_id", req.SessionID, "message_id", req.MessageID,
					"workspace_uuid", workspaceUUID)
			}
			return
		}
		if title == "" {
			return
		}

		if queue != nil {
			if err := queue.UpdateTitle(req.MessageID, title); err != nil {
				if errors.Is(err, session.ErrMessageNotFound) {
					if w.logger != nil {
						w.logger.Debug("Queue message no longer exists, skipping title update",
							"session_id", req.SessionID, "message_id", req.MessageID, "title", title)
					}
				} else if w.logger != nil {
					w.logger.Error("Failed to update queue message title", "error", err,
						"session_id", req.SessionID, "message_id", req.MessageID)
				}
				return
			}
		}

		if w.logger != nil {
			w.logger.Info("Generated queue message title", "session_id", req.SessionID,
				"message_id", req.MessageID, "title", title)
		}
		if w.OnTitleGenerated != nil {
			w.OnTitleGenerated(req.SessionID, req.MessageID, title)
		}
		return
	}
}
