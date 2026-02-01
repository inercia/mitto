package web

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// QueueTitleRequest represents a request to generate a title for a queued message.
type QueueTitleRequest struct {
	SessionID string
	MessageID string
	Message   string
}

// QueueTitleWorker processes title generation requests sequentially.
// This prevents overwhelming the auxiliary conversation with concurrent requests.
type QueueTitleWorker struct {
	store    *session.Store
	logger   *slog.Logger
	requests chan QueueTitleRequest
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	// OnTitleGenerated is called when a title is successfully generated.
	// It receives the session ID, message ID, and the generated title.
	OnTitleGenerated func(sessionID, messageID, title string)
}

// NewQueueTitleWorker creates a new title generation worker.
// The worker processes requests sequentially in a background goroutine.
func NewQueueTitleWorker(store *session.Store, logger *slog.Logger) *QueueTitleWorker {
	ctx, cancel := context.WithCancel(context.Background())
	w := &QueueTitleWorker{
		store:    store,
		logger:   logger,
		requests: make(chan QueueTitleRequest, 100), // Buffer up to 100 requests
		ctx:      ctx,
		cancel:   cancel,
	}
	w.wg.Add(1)
	go w.run()
	return w
}

// Enqueue adds a title generation request to the queue.
// This method is non-blocking; the request will be processed asynchronously.
func (w *QueueTitleWorker) Enqueue(req QueueTitleRequest) {
	select {
	case w.requests <- req:
		if w.logger != nil {
			w.logger.Debug("Enqueued title generation request",
				"session_id", req.SessionID,
				"message_id", req.MessageID)
		}
	default:
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
}

// run processes title generation requests sequentially.
func (w *QueueTitleWorker) run() {
	defer w.wg.Done()

	for req := range w.requests {
		select {
		case <-w.ctx.Done():
			return
		default:
			w.processRequest(req)
		}
	}
}

// processRequest generates a title for a single queued message.
func (w *QueueTitleWorker) processRequest(req QueueTitleRequest) {
	ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
	defer cancel()

	// Generate title using auxiliary conversation
	title, err := auxiliary.GenerateQueuedMessageTitle(ctx, req.Message)
	if err != nil {
		if w.logger != nil {
			w.logger.Error("Failed to generate queue message title",
				"error", err,
				"session_id", req.SessionID,
				"message_id", req.MessageID)
		}
		return
	}

	if title == "" {
		return
	}

	// Update the message title in the queue
	if w.store != nil {
		queue := w.store.Queue(req.SessionID)
		if err := queue.UpdateTitle(req.MessageID, title); err != nil {
			if w.logger != nil {
				w.logger.Error("Failed to update queue message title",
					"error", err,
					"session_id", req.SessionID,
					"message_id", req.MessageID)
			}
			return
		}
	}

	if w.logger != nil {
		w.logger.Info("Generated queue message title",
			"session_id", req.SessionID,
			"message_id", req.MessageID,
			"title", title)
	}

	// Notify via callback
	if w.OnTitleGenerated != nil {
		w.OnTitleGenerated(req.SessionID, req.MessageID, title)
	}
}

