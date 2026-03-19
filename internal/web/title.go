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
	Store            *session.Store
	SessionID        string
	Message          string
	Logger           *slog.Logger
	WorkspaceUUID    string                               // Workspace UUID for auxiliary session
	AuxiliaryManager *auxiliary.WorkspaceAuxiliaryManager // Auxiliary manager for title generation
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

const (
	// titleMaxRetries is the maximum number of retry attempts for title generation.
	titleMaxRetries = 3 // 4 total attempts: delays 30s, 60s, 120s
	// titleRetryBaseDelay is the initial delay between retry attempts (exponential backoff).
	titleRetryBaseDelay = 30 * time.Second // delays: 30s, 60s, 120s
	// titleSessionCreateTimeout is the timeout for the session creation phase of title generation.
	// This is intentionally long because the ACP agent serializes RPCs — when a session/prompt
	// is in-flight, a session/new RPC will be queued until the active prompt completes.
	// With long Opus responses (5-15+ min), we must wait for the agent to become idle first.
	titleSessionCreateTimeout = 3 * time.Minute
	// titlePromptTimeout is the timeout for the actual title prompt once a session is obtained.
	// The prompt itself is fast once the agent is free.
	titlePromptTimeout = 30 * time.Second
)

// GenerateAndSetTitle generates a title for a session using the workspace-scoped auxiliary session.
// This runs asynchronously and doesn't block the caller.
// It retries up to titleMaxRetries times with exponential backoff on transient failures.
// The OnTitleGenerated callback is called when the title is successfully generated and saved.
func GenerateAndSetTitle(cfg TitleGenerationConfig) {
	go func() {
		if cfg.WorkspaceUUID == "" {
			if cfg.Logger != nil {
				cfg.Logger.Warn("Cannot generate title: session has no workspace",
					"session_id", cfg.SessionID)
			}
			return
		}

		if cfg.AuxiliaryManager == nil {
			if cfg.Logger != nil {
				cfg.Logger.Warn("Cannot generate title: no auxiliary manager (legacy mode or unsupported ACP server)",
					"session_id", cfg.SessionID)
			}
			return
		}

		var title string
		var lastErr error
		for attempt := 0; attempt <= titleMaxRetries; attempt++ {
			if attempt > 0 {
				// Check if title was set by another path while we were retrying
				if !SessionNeedsTitle(cfg.Store, cfg.SessionID) {
					if cfg.Logger != nil {
						cfg.Logger.Debug("Title already set during retry, skipping",
							"session_id", cfg.SessionID,
							"attempt", attempt)
					}
					return
				}

				delay := titleRetryBaseDelay * time.Duration(1<<(attempt-1)) // exponential: 3s, 6s
				if cfg.Logger != nil {
					cfg.Logger.Info("Retrying title generation",
						"session_id", cfg.SessionID,
						"attempt", attempt+1,
						"delay", delay)
				}
				time.Sleep(delay)
			}

			// Use titleSessionCreateTimeout for the entire call: getOrCreateAuxiliarySession
			// calls WaitForIdle internally before NewSession, so the 3-minute budget covers
			// waiting for the agent to finish any active prompt (the slow part).
			// Once the session exists, the prompt itself completes in well under 30 seconds.
			ctx, cancel := context.WithTimeout(context.Background(), titleSessionCreateTimeout)
			title, lastErr = cfg.AuxiliaryManager.GenerateTitle(ctx, cfg.WorkspaceUUID, cfg.Message)
			cancel()

			if lastErr == nil && title != "" {
				break
			}
			if lastErr != nil && cfg.Logger != nil {
				cfg.Logger.Warn("Title generation attempt failed",
					"error", lastErr,
					"session_id", cfg.SessionID,
					"attempt", attempt+1,
					"max_attempts", titleMaxRetries+1)
			}
		}

		if lastErr != nil {
			if cfg.Logger != nil {
				cfg.Logger.Error("Failed to generate title after all retries",
					"error", lastErr,
					"session_id", cfg.SessionID,
					"workspace_uuid", cfg.WorkspaceUUID,
					"attempts", titleMaxRetries+1)
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
			cfg.Logger.Debug("Auto-generated session title", "session_id", cfg.SessionID, "title", title)
		}

		// Notify via callback
		if cfg.OnTitleGenerated != nil {
			cfg.OnTitleGenerated(cfg.SessionID, title)
		}
	}()
}
