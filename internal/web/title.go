package web

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/session"
)

// Precompiled regexps for GenerateQuickTitle.
var (
	reFencedCode    = regexp.MustCompile("(?s)```[^`]*```")
	reInlineCode    = regexp.MustCompile("`[^`]+`")
	reMarkdownLink  = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reURL           = regexp.MustCompile(`https?://\S+`)
	reMarkdownHead  = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reMarkdownEmph  = regexp.MustCompile(`\*{1,2}([^*]+)\*{1,2}`)
	reMarkdownUnder = regexp.MustCompile(`_{1,2}([^_]+)_{1,2}`)
	reWhitespace    = regexp.MustCompile(`\s+`)
)

const (
	quickTitleMaxWords  = 6
	quickTitleMaxChars  = 50
	quickTitleMinLength = 4 // if result is shorter than this, return ""
)

// GenerateQuickTitle generates a quick fallback title from the message text
// without needing the auxiliary session. It extracts the first few meaningful
// words from the message, stripping markdown formatting and noise.
// Returns empty string if no meaningful title can be extracted.
func GenerateQuickTitle(message string) string {
	s := message

	// Strip fenced code blocks first (multi-line)
	s = reFencedCode.ReplaceAllString(s, " ")
	// Strip inline code
	s = reInlineCode.ReplaceAllString(s, " ")
	// Strip markdown links, keeping link text
	s = reMarkdownLink.ReplaceAllString(s, "$1")
	// Strip bare URLs
	s = reURL.ReplaceAllString(s, " ")
	// Strip markdown headings (leading #)
	s = reMarkdownHead.ReplaceAllString(s, "")
	// Strip bold/italic markers, keeping inner text
	s = reMarkdownEmph.ReplaceAllString(s, "$1")
	s = reMarkdownUnder.ReplaceAllString(s, "$1")

	// Collapse whitespace
	s = reWhitespace.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	if s == "" {
		return ""
	}

	// Take first quickTitleMaxWords words
	words := strings.Fields(s)
	if len(words) > quickTitleMaxWords {
		words = words[:quickTitleMaxWords]
	}
	title := strings.Join(words, " ")

	// Strip leading/trailing punctuation
	title = strings.TrimFunc(title, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	if len(title) < quickTitleMinLength {
		return ""
	}

	// Cap at quickTitleMaxChars, breaking at word boundary
	if len(title) > quickTitleMaxChars {
		cut := title[:quickTitleMaxChars]
		// Find last space within limit
		if idx := strings.LastIndex(cut, " "); idx > 0 {
			cut = cut[:idx]
		}
		title = strings.TrimRight(cut, " ") + "..."
	}

	// Capitalize first letter
	if len(title) > 0 {
		runes := []rune(title)
		runes[0] = unicode.ToUpper(runes[0])
		title = string(runes)
	}

	return title
}

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
	// titleSessionCreateTimeout is the timeout for a single title generation attempt.
	// This covers the full round-trip: auxiliary session creation + the title prompt itself.
	// 20 minutes per attempt is generous. With titleMaxRetries=3 the total worst-case
	// wall time is ≈ 83 minutes.
	titleSessionCreateTimeout = 20 * time.Minute
)

// GenerateAndSetTitle generates a title for a session using the workspace-scoped auxiliary session.
// This runs asynchronously and doesn't block the caller.
// It retries up to titleMaxRetries times with exponential backoff on transient failures.
// The OnTitleGenerated callback is called when the title is successfully generated and saved.
//
// Before launching the async goroutine, it synchronously sets a quick fallback title extracted
// from the message text so the UI shows something immediately without waiting for the auxiliary.
func GenerateAndSetTitle(cfg TitleGenerationConfig) {
	// Immediately set a quick fallback title from the message text.
	// This gives the conversation a title right away without waiting for the
	// auxiliary session.
	quickTitle := GenerateQuickTitle(cfg.Message)
	if quickTitle != "" && cfg.Store != nil {
		if err := cfg.Store.UpdateMetadata(cfg.SessionID, func(m *session.Metadata) {
			if m.Name == "" { // Only set if no title yet
				m.Name = quickTitle
			}
		}); err == nil {
			if cfg.Logger != nil {
				cfg.Logger.Debug("Set quick fallback title", "session_id", cfg.SessionID, "title", quickTitle)
			}
			// Notify immediately so UI updates
			if cfg.OnTitleGenerated != nil {
				cfg.OnTitleGenerated(cfg.SessionID, quickTitle)
			}
		}
	}

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
				// A quick title may already be set, but we still try auxiliary to get a
				// better (more descriptive) title. Only the retry delay applies here.
				delay := titleRetryBaseDelay * time.Duration(1<<(attempt-1)) // exponential: 30s, 60s, 120s
				if cfg.Logger != nil {
					cfg.Logger.Info("Retrying title generation",
						"session_id", cfg.SessionID,
						"attempt", attempt+1,
						"delay", delay)
				}
				time.Sleep(delay)
			}

			// The 20-minute budget covers auxiliary session setup and the prompt itself.
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
