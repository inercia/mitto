package web

import (
	"context"
	"log/slog"
	"time"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// TitleGenerationConfig holds configuration for title generation.
type TitleGenerationConfig struct {
	Store     *session.Store
	SessionID string
	Message   string
	Logger    *slog.Logger
	// OnTitleGenerated is called when a title is successfully generated and saved.
	// It receives the session ID and the generated title.
	OnTitleGenerated func(sessionID, title string)
}

// SessionNeedsTitle returns true if the session has no title yet and needs auto-title generation.
// Returns false if the session already has a title (either auto-generated or user-set).
func SessionNeedsTitle(store *session.Store, sessionID string) bool {
	if store == nil || sessionID == "" {
		return false
	}
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return false
	}
	return meta.Name == ""
}

// GenerateAndSetTitle generates a title for a session using the auxiliary session.
// This runs asynchronously and doesn't block the caller.
// The OnTitleGenerated callback is called when the title is successfully generated and saved.
func GenerateAndSetTitle(cfg TitleGenerationConfig) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		title, err := auxiliary.GenerateTitle(ctx, cfg.Message)
		if err != nil {
			if cfg.Logger != nil {
				cfg.Logger.Error("Failed to generate title", "error", err, "session_id", cfg.SessionID)
			}
			return
		}

		if title == "" {
			return
		}

		// Update session metadata in store
		if cfg.Store != nil {
			if err := cfg.Store.UpdateMetadata(cfg.SessionID, func(m *session.Metadata) {
				m.Name = title
			}); err != nil {
				if cfg.Logger != nil {
					cfg.Logger.Error("Failed to update session name", "error", err, "session_id", cfg.SessionID)
				}
				return
			}
		}

		if cfg.Logger != nil {
			cfg.Logger.Info("Auto-generated session title", "session_id", cfg.SessionID, "title", title)
		}

		// Notify via callback
		if cfg.OnTitleGenerated != nil {
			cfg.OnTitleGenerated(cfg.SessionID, title)
		}
	}()
}
