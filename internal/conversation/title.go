package conversation

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
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
	// Force, when true, requests an explicit user-driven title regeneration
	// (the "Auto-rename" context-menu action, mitto-yv2) rather than the
	// normal initial/upgrade auto-title flow. It bypasses the quick-fallback
	// write, the SessionNeedsTitle early-return gates (so a conversation that
	// already has a title — including an explicitly renamed one — can still
	// be regenerated), and uses Message as an extended context excerpt (see
	// BuildTitleContext) sent via GenerateTitleFromContext instead of
	// GenerateTitle. On success, NameExplicit is set (not just preserved) so
	// a later internal auto-title retry cannot clobber the fresh result.
	Force bool
}

type titleJobKey struct {
	store     *session.Store
	sessionID string
}

var titleJobs = struct {
	sync.Mutex
	active map[titleJobKey]struct{}
}{active: make(map[titleJobKey]struct{})}

// claimTitleJob coalesces title generation per persisted session. A job encountering
// proactive process-busy load shedding remains claimed until quiescence or until the
// session no longer needs a title. Sessions without a stable store key keep the
// bounded legacy behavior because they cannot persist a generated title.
func claimTitleJob(store *session.Store, sessionID string) (release func(), ok bool) {
	if store == nil || sessionID == "" {
		return func() {}, true
	}
	key := titleJobKey{store: store, sessionID: sessionID}
	titleJobs.Lock()
	if _, exists := titleJobs.active[key]; exists {
		titleJobs.Unlock()
		return nil, false
	}
	titleJobs.active[key] = struct{}{}
	titleJobs.Unlock()
	return func() {
		titleJobs.Lock()
		delete(titleJobs.active, key)
		titleJobs.Unlock()
	}, true
}

// SessionNeedsTitle returns true if the session needs an initial or upgraded auto-title.
// Returns false if the session already has a final (LLM-generated or user-set) title.
// A quick fallback title populated by GenerateAndSetTitle (marked via meta.NameIsFallback)
// is treated as still needing generation so titleCoordinator.retryIfNeeded can upgrade
// it to the real LLM-generated title on the next prompt_complete quiescence (mitto-ee3).
// An explicit human/MCP rename (meta.NameExplicit) permanently suppresses auto-title
// generation, even over a fallback title, so a rename is never clobbered (mitto-808).
func SessionNeedsTitle(store *session.Store, sessionID string) bool {
	if store == nil || sessionID == "" {
		return false
	}
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		return false
	}
	if meta.NameExplicit {
		return false
	}
	return meta.Name == "" || meta.NameIsFallback
}

// titleMaxRetries is the maximum number of retry attempts for title generation.
// var (not const) so tests can override the cadence to exercise the retry loop
// in unit time. See internal/conversation/title_wedge_repro_test.go (mitto-ammz.1).
var titleMaxRetries = 3 // 4 total attempts: delays 30s, 60s, 120s

// titleRetryBaseDelay is the initial delay between retry attempts (exponential backoff).
// var (not const) so tests can override the cadence. See titleMaxRetries.
var titleRetryBaseDelay = 30 * time.Second // delays: 30s, 60s, 120s

func titleRetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	maxAttempt := titleMaxRetries
	if maxAttempt < 1 {
		maxAttempt = 1
	}
	if attempt > maxAttempt {
		attempt = maxAttempt
	}
	return titleRetryBaseDelay * time.Duration(1<<(attempt-1))
}

const (
	// titleSessionCreateTimeout is the timeout for a single title generation attempt.
	// This covers the full round-trip: auxiliary session creation + the title prompt itself.
	// 20 minutes per attempt is generous. With titleMaxRetries=3 the total worst-case
	// wall time is ≈ 83 minutes.
	titleSessionCreateTimeout = 20 * time.Minute
)

// GenerateAndSetTitle generates a title for a session using the workspace-scoped auxiliary session.
// This runs asynchronously and doesn't block the caller.
// It retries up to titleMaxRetries times with exponential backoff on transient failures.
// Proactive ErrProcessBusy load shedding keeps one coalesced persisted job pending at
// the maximum backoff until the shared process becomes quiescent.
// The OnTitleGenerated callback is called when the title is successfully generated and saved.
//
// Before launching the async goroutine, it synchronously sets a quick fallback title extracted
// from the message text so the UI shows something immediately without waiting for the auxiliary.
func GenerateAndSetTitle(cfg TitleGenerationConfig) {
	// Immediately set a quick fallback title from the message text.
	// This gives the conversation a title right away without waiting for the
	// auxiliary session. Skipped for a forced regenerate: the conversation
	// already has a title (that's the whole point of forcing), so a fallback
	// derived from a context excerpt would be low quality and pointless.
	if !cfg.Force {
		quickTitle := GenerateQuickTitle(cfg.Message)
		if quickTitle != "" && cfg.Store != nil {
			fallbackSet := false
			if err := cfg.Store.UpdateMetadata(cfg.SessionID, func(m *session.Metadata) {
				if m.Name == "" { // Only set if no title yet
					m.Name = quickTitle
					m.NameIsFallback = true // mitto-ee3: mark so retryIfNeeded can upgrade later
					fallbackSet = true
				}
			}); err == nil && fallbackSet {
				if cfg.Logger != nil {
					cfg.Logger.Debug("Set quick fallback title", "session_id", cfg.SessionID, "title", quickTitle)
				}
				// Notify immediately so UI updates
				if cfg.OnTitleGenerated != nil {
					cfg.OnTitleGenerated(cfg.SessionID, quickTitle)
				}
			}
		}
	}

	releaseJob, claimed := claimTitleJob(cfg.Store, cfg.SessionID)
	if !claimed {
		if cfg.Logger != nil {
			cfg.Logger.Debug("Coalescing duplicate title generation job", "session_id", cfg.SessionID)
		}
		return
	}

	go func() {
		defer releaseJob()
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
		waitForQuiescence := false
		for attempt := 0; ; attempt++ {
			pendingRecovery := attempt > titleMaxRetries
			// A forced regenerate must proceed even if the session already has
			// a final/explicit title — that's the explicit user request.
			if !cfg.Force && pendingRecovery && !SessionNeedsTitle(cfg.Store, cfg.SessionID) {
				return
			}
			if attempt > 0 {
				// A quick title may already be set, but we still try auxiliary to get a
				// better (more descriptive) title. After the bounded retry schedule,
				// process-busy recovery stays capped at its maximum delay.
				delay := titleRetryDelay(attempt)
				if cfg.Logger != nil {
					if pendingRecovery {
						cfg.Logger.Debug("Polling pending title generation after process busy",
							"session_id", cfg.SessionID,
							"delay", delay)
					} else {
						cfg.Logger.Info("Retrying title generation",
							"session_id", cfg.SessionID,
							"attempt", attempt+1,
							"delay", delay)
					}
				}
				if waitForQuiescence {
					waitStart := time.Now()
					waitCtx, waitCancel := context.WithTimeout(context.Background(), delay)
					observed := cfg.AuxiliaryManager.WaitForProcessQuiescence(waitCtx, cfg.WorkspaceUUID)
					waitCancel()
					if observed && cfg.Logger != nil {
						cfg.Logger.Debug("Observed process quiescence for pending title generation",
							"session_id", cfg.SessionID)
					}
					if remaining := delay - time.Since(waitStart); !observed && remaining > 0 {
						time.Sleep(remaining)
					}
				} else {
					time.Sleep(delay)
				}
				if !cfg.Force && pendingRecovery && !SessionNeedsTitle(cfg.Store, cfg.SessionID) {
					return
				}
			}
			waitForQuiescence = false

			// The 20-minute budget covers auxiliary session setup and the prompt itself.
			ctx, cancel := context.WithTimeout(context.Background(), titleSessionCreateTimeout)
			if cfg.Force {
				// Message carries a composed multi-turn context excerpt (see
				// BuildTitleContext), not a single initial message.
				title, lastErr = cfg.AuxiliaryManager.GenerateTitleFromContext(ctx, cfg.WorkspaceUUID, cfg.Message)
			} else {
				title, lastErr = cfg.AuxiliaryManager.GenerateTitle(ctx, cfg.WorkspaceUUID, cfg.Message)
			}
			cancel()

			if lastErr == nil && title != "" {
				break
			}
			if lastErr != nil && cfg.Logger != nil {
				if pendingRecovery && errors.Is(lastErr, acperrors.ErrProcessBusy) {
					cfg.Logger.Debug("Pending title generation still waiting for process quiescence",
						"session_id", cfg.SessionID)
				} else {
					cfg.Logger.Warn("Title generation attempt failed",
						"error", lastErr,
						"session_id", cfg.SessionID,
						"attempt", attempt+1,
						"max_attempts", titleMaxRetries+1)
				}
			}

			// mitto-juzb: proactive load shedding is transient. Retain this one
			// coalesced job and use the bounded backoff so it can observe quiescence
			// without requiring another prompt-completion edge.
			if errors.Is(lastErr, acperrors.ErrProcessBusy) {
				if cfg.Store != nil && cfg.SessionID != "" {
					waitForQuiescence = true
					if attempt == titleMaxRetries && cfg.Logger != nil {
						cfg.Logger.Info("Retaining pending title generation until process quiescence",
							"session_id", cfg.SessionID,
							"delay", titleRetryDelay(attempt+1))
					}
					continue
				}
				waitForQuiescence = false
				if attempt >= titleMaxRetries {
					break
				}
				continue
			}

			// mitto-ammz.1: classify-and-abandon on wedge/saturation signals.
			// The retry cadence (30s / 60s / 120s) is failure-agnostic, and
			// each attempt burns the full 60s extended-MCP budget on a wedged
			// or saturated shared process with near-zero chance of success.
			// The next natural quiescence will re-attempt via the normal
			// auto-title path; do not amplify the storm here.
			if lastErr != nil && (acperrors.IsAgentInternalDeadlineErr(lastErr) ||
				acperrors.IsAgentQueryClosedErr(lastErr) ||
				errors.Is(lastErr, acperrors.ErrSharedProcessSaturated)) {
				if cfg.Logger != nil {
					reason := "agent_internal_deadline"
					switch {
					case errors.Is(lastErr, acperrors.ErrSharedProcessSaturated):
						reason = "shared_process_saturated"
					case acperrors.IsAgentQueryClosedErr(lastErr):
						reason = "agent_query_closed"
					}
					cfg.Logger.Info("Abandoning title generation retries on wedge signal",
						"session_id", cfg.SessionID,
						"workspace_uuid", cfg.WorkspaceUUID,
						"attempt", attempt+1,
						"reason", reason,
						"error", lastErr)
				}
				return
			}

			if attempt >= titleMaxRetries {
				break
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
				// mitto-808: an explicit rename may have landed while this
				// generation was in flight — never clobber it. A forced
				// regenerate (mitto-yv2) is itself an explicit user request,
				// so it intentionally overrides this guard.
				if !cfg.Force && m.NameExplicit {
					return
				}
				m.Name = title
				m.NameIsFallback = false // mitto-ee3: real title replaces the quick fallback
				if cfg.Force {
					// Treat the explicit regenerate like a rename so a later
					// internal auto-title retry can't clobber it.
					m.NameExplicit = true
				}
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

// titleContextMaxUserPrompts bounds how many of the most recent user prompts
// BuildTitleContext includes in the composed context excerpt.
const titleContextMaxUserPrompts = 5

// titleContextMaxChars caps the composed context excerpt length sent to the
// auxiliary session (mirrors the truncation used elsewhere, e.g.
// AnalyzeFollowUpQuestions' maxLen).
const titleContextMaxChars = 4000

// BuildTitleContext composes a short transcript excerpt from a session's
// recorded events for context-aware title regeneration (mitto-yv2): the last
// titleContextMaxUserPrompts user prompts (oldest first) plus the most recent
// agent response, HTML-stripped. If the composed text exceeds
// titleContextMaxChars, the oldest user prompts are dropped first (the
// excerpt stays most-recent-biased) before falling back to a hard cut.
// Returns "" if the session has no store, no persisted ID, or no events yet.
func BuildTitleContext(store *session.Store, sessionID string) string {
	if store == nil || sessionID == "" {
		return ""
	}
	events, err := store.ReadEvents(sessionID)
	if err != nil || len(events) == 0 {
		return ""
	}

	// Collect up to the last N user prompts, newest first, then reverse to
	// chronological order.
	var userPrompts []string
	for i := len(events) - 1; i >= 0 && len(userPrompts) < titleContextMaxUserPrompts; i-- {
		if events[i].Type != session.EventTypeUserPrompt {
			continue
		}
		data, derr := session.DecodeEventData(events[i])
		if derr != nil {
			continue
		}
		d, ok := data.(session.UserPromptData)
		if !ok {
			continue
		}
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			continue
		}
		userPrompts = append(userPrompts, msg)
	}
	for i, j := 0, len(userPrompts)-1; i < j; i, j = i+1, j-1 {
		userPrompts[i], userPrompts[j] = userPrompts[j], userPrompts[i]
	}

	lastAgentMsg := strings.TrimSpace(session.GetLastAgentMessage(events))

	compose := func(prompts []string) string {
		var b strings.Builder
		for _, p := range prompts {
			b.WriteString("User: ")
			b.WriteString(p)
			b.WriteString("\n")
		}
		if lastAgentMsg != "" {
			b.WriteString("Assistant: ")
			b.WriteString(lastAgentMsg)
			b.WriteString("\n")
		}
		return strings.TrimSpace(b.String())
	}

	text := compose(userPrompts)
	// Drop oldest user prompts first so the excerpt stays most-recent-biased.
	for len(text) > titleContextMaxChars && len(userPrompts) > 0 {
		userPrompts = userPrompts[1:]
		text = compose(userPrompts)
	}
	// Fallback hard cut (e.g. the agent response alone exceeds the cap).
	if len(text) > titleContextMaxChars {
		text = text[:titleContextMaxChars]
	}
	return text
}
