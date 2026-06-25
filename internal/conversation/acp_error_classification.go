package conversation

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Shared ACP process restart constants.
// These are used by both SharedACPProcess and BackgroundSession to ensure
// consistent restart behavior across both code paths.
const (
	// MaxACPRestarts is the maximum number of automatic restarts allowed within ACPRestartWindow.
	// If this limit is exceeded, the user must manually restart the session.
	MaxACPRestarts = 3

	// MaxACPTotalRestarts is the absolute lifetime cap on ACP restart attempts for a single
	// BackgroundSession. Once this many restarts have been recorded (across all sliding windows),
	// the session is marked as permanently failed and no further restart attempts are made.
	// This prevents dead sessions from retrying indefinitely after the sliding window resets.
	// Value: 10 (~3 windows × 3 restarts per window + 1 spare).
	MaxACPTotalRestarts = 10

	// ACPRestartWindow is the time window for counting restart attempts.
	// Restarts older than this are not counted toward the limit.
	ACPRestartWindow = 5 * time.Minute

	// ACPRestartBaseDelay is the initial delay between runtime restart attempts.
	// This is intentionally longer than the start-retry delay (500ms) to give the system
	// time to recover from transient conditions (e.g., notification queue overflow
	// due to backpressure from slow WebSocket clients).
	// With exponential backoff: 3s → 6s → 12s → 24s → 30s (capped).
	ACPRestartBaseDelay = 3 * time.Second

	// ACPRestartMaxDelay is the maximum delay between runtime restart attempts.
	// This prevents rapid crash loops that burn resources without letting the underlying
	// condition (e.g., client backpressure) resolve.
	ACPRestartMaxDelay = 30 * time.Second

	// MaxGlobalRestarts is the maximum number of ACP process restarts across ALL workspaces
	// within GlobalRestartWindow. When exceeded, ALL restarts are paused for GlobalCooldownDuration.
	// This prevents cross-workspace restart cascades under systemic memory pressure.
	MaxGlobalRestarts = 5

	// GlobalRestartWindow is the time window for counting global restarts across all workspaces.
	GlobalRestartWindow = 2 * time.Minute

	// GlobalCooldownDuration is how long ALL restarts are paused after the global restart
	// limit is exceeded. This gives the system time to recover from memory pressure.
	GlobalCooldownDuration = 60 * time.Second
)

// RestartReason represents the reason why an ACP process was restarted.
type RestartReason string

const (
	// RestartReasonCrashDuringPrompt indicates the process crashed while handling a prompt.
	RestartReasonCrashDuringPrompt RestartReason = "crash_during_prompt"

	// RestartReasonCrashDuringStream indicates the process crashed while streaming a response.
	RestartReasonCrashDuringStream RestartReason = "crash_during_stream"

	// RestartReasonUnexpectedExit indicates the process exited unexpectedly outside of prompt handling.
	RestartReasonUnexpectedExit RestartReason = "unexpected_exit"

	// RestartReasonResumeFailure indicates the process was found dead when a session tried to resume
	// after the app was backgrounded or the system slept (e.g. broken pipe, file already closed).
	RestartReasonResumeFailure RestartReason = "resume_failure"

	// RestartReasonUnknown indicates the restart reason could not be determined.
	RestartReasonUnknown RestartReason = "unknown"
)

// ACPErrorClass represents the severity classification of an ACP process error.
type ACPErrorClass int

const (
	// ACPErrorTransient indicates a temporary failure that may succeed on retry.
	// Examples: network timeouts, port conflicts, transient crashes.
	ACPErrorTransient ACPErrorClass = iota

	// ACPErrorPermanent indicates a failure that will not resolve by retrying.
	// Examples: missing binary, missing npm module, permission denied, syntax errors.
	ACPErrorPermanent
)

// String returns a human-readable representation of the error class.
func (c ACPErrorClass) String() string {
	switch c {
	case ACPErrorTransient:
		return "transient"
	case ACPErrorPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// ACPClassifiedError holds the result of classifying an ACP process error.
// It implements the error interface so it can be returned where error is expected.
// Callers that need the classification details can use type assertion:
//
//	if classified, ok := err.(*ACPClassifiedError); ok { ... }
type ACPClassifiedError struct {
	// Class is the error classification (transient or permanent).
	Class ACPErrorClass
	// OriginalError is the underlying error.
	OriginalError error
	// Stderr is the captured stderr output from the process (may be empty).
	Stderr string
	// UserMessage is a user-friendly description of what went wrong.
	UserMessage string
	// UserGuidance is actionable advice for the user to fix the problem.
	// Empty for transient errors where retry is the correct action.
	UserGuidance string
}

// Error returns the original error message, satisfying the error interface.
func (e *ACPClassifiedError) Error() string {
	return e.OriginalError.Error()
}

// Unwrap returns the original error for use with errors.Is/errors.As.
func (e *ACPClassifiedError) Unwrap() error {
	return e.OriginalError
}

// IsRetryable returns true if the error is transient and the operation should be retried.
func (e *ACPClassifiedError) IsRetryable() bool {
	return e.Class == ACPErrorTransient
}

// errorPattern defines a known error pattern with associated user-facing messages.
type errorPattern struct {
	// substrings are case-insensitive substrings to match against the combined error+stderr text.
	substrings []string
	// userMessage is a short, user-friendly description of the error.
	userMessage string
	// userGuidance is actionable advice for the user to fix the problem.
	userGuidance string
}

// matches returns true if any of the pattern's substrings appear in the combined text.
func (p errorPattern) matches(combined string) bool {
	lower := strings.ToLower(combined)
	for _, sub := range p.substrings {
		if strings.Contains(lower, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// permanentErrorPatterns defines known permanent error patterns in priority order.
// The first matching pattern wins.
var permanentErrorPatterns = []errorPattern{
	{
		substrings:   []string{"Cannot find module", "MODULE_NOT_FOUND", "Cannot resolve module"},
		userMessage:  "A required Node.js module is missing",
		userGuidance: "Install the missing module or check the ACP command in workspace settings.",
	},
	{
		substrings:   []string{"command not found", "no such file or directory", "not found in PATH", "executable file not found"},
		userMessage:  "The ACP command was not found",
		userGuidance: "Check that the ACP command is installed and the path is correct in settings.",
	},
	{
		substrings:   []string{"EACCES", "permission denied", "Operation not permitted"},
		userMessage:  "Permission denied when starting the ACP process",
		userGuidance: "Check file permissions for the ACP command and its working directory.",
	},
	{
		substrings:   []string{"SyntaxError", "Unexpected token", "Parse error"},
		userMessage:  "The ACP server script contains a syntax error",
		userGuidance: "Fix the syntax error in the ACP server script before retrying.",
	},
	{
		substrings:   []string{"ENOENT"},
		userMessage:  "A required file or directory was not found",
		userGuidance: "Verify that the ACP command path and working directory exist.",
	},
	{
		substrings:   []string{"empty ACP command"},
		userMessage:  "No ACP command configured",
		userGuidance: "Configure an ACP command in workspace settings.",
	},
	{
		// "write |1: file already closed" — the OS-level write end of the ACP stdin pipe
		// has been closed (e.g. the subprocess exited and cleanup ran). This is a permanent
		// OS-level condition: the pipe descriptor cannot be reopened. Retrying the same
		// process start will keep hitting this error until the session is re-created.
		substrings:   []string{"file already closed"},
		userMessage:  "The ACP process pipe was permanently closed",
		userGuidance: "Archive and re-open this conversation to get a fresh ACP connection.",
	},
}

// ClassifyACPError examines an error message and stderr output to determine
// whether the failure is permanent (should not retry) or transient (may succeed on retry).
// Returns nil if err is nil.
func ClassifyACPError(err error, stderr string) *ACPClassifiedError {
	if err == nil {
		return nil
	}

	combined := err.Error() + "\n" + stderr

	// Check permanent error patterns first (order matters — most specific first).
	for _, pattern := range permanentErrorPatterns {
		if pattern.matches(combined) {
			return &ACPClassifiedError{
				Class:         ACPErrorPermanent,
				OriginalError: err,
				Stderr:        stderr,
				UserMessage:   pattern.userMessage,
				UserGuidance:  pattern.userGuidance,
			}
		}
	}

	// Default: transient (retryable).
	return &ACPClassifiedError{
		Class:         ACPErrorTransient,
		OriginalError: err,
		Stderr:        stderr,
		UserMessage:   "The ACP process failed to start",
		UserGuidance:  "",
	}
}

// formatClassifiedError returns a user-friendly string combining the message and guidance.
// Used for observer notifications.
func formatClassifiedError(classified *ACPClassifiedError) string {
	if classified == nil {
		return ""
	}
	if classified.UserGuidance != "" {
		return fmt.Sprintf("%s. %s", classified.UserMessage, classified.UserGuidance)
	}
	return classified.UserMessage
}

// IsACPConnectionError reports whether err is a recoverable ACP pipe/connection
// error that can be resolved by restarting the underlying OS process.
// Used to detect the post-sleep/resume race condition where the OS has killed
// the ACP subprocess but the Go connection object still appears alive.
func IsACPConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "peer disconnected") ||
		strings.Contains(msg, "shared ACP process has exited") ||
		strings.Contains(msg, "shared ACP process is not running")
}

// BackoffDelay calculates an exponential backoff delay with jitter.
// attempt is 0-indexed (0 = first retry). The delay is capped at maxDelay.
// Jitter adds random variation of ±jitterRatio to prevent thundering herd.
func BackoffDelay(attempt int, baseDelay, maxDelay time.Duration, jitterRatio float64) time.Duration {
	delay := baseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
			break
		}
	}

	// Add jitter: random variation within ±jitterRatio of the delay.
	if jitterRatio > 0 {
		jitter := time.Duration(float64(delay) * jitterRatio * (2*rand.Float64() - 1))
		delay += jitter
		if delay < 0 {
			delay = baseDelay // Safety floor.
		}
	}

	return delay
}

// httpStatusRegex matches HTTP status codes in ACP error strings.
// It looks for patterns like "HTTP error: NNN", `"httpStatus":NNN`, or "HTTP/1.1 NNN".
var httpStatusRegex = regexp.MustCompile(`(?:HTTP error:\s*|"httpStatus"\s*:\s*|HTTP/[12](?:\.[01])?\s+)(\d{3})`)

// isContextTooLargeError returns true if the error indicates the AI model
// rejected the prompt because the conversation context is too large (HTTP 413
// or an equivalent model-specific error phrase).
//
// The ACP server forwards HTTP 413 responses as JSON-RPC -32603 "Internal error"
// messages, so the numeric status code or the model-specific phrase may appear
// anywhere in the error string.  We keep the list of patterns here (rather than
// inlining them in formatACPError) so that the queue-advancement logic can reuse
// the same predicate without duplicating strings.
func isContextTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	errMsgLower := strings.ToLower(errMsg)
	return strings.Contains(errMsg, "413") ||
		strings.Contains(errMsgLower, "context too large") ||
		strings.Contains(errMsgLower, "context_too_long") ||
		strings.Contains(errMsgLower, "context_length_exceeded") ||
		strings.Contains(errMsgLower, "context window is full") ||
		strings.Contains(errMsgLower, "prompt is too long") ||
		strings.Contains(errMsgLower, "maximum context length") ||
		strings.Contains(errMsgLower, "context too large for model")
}

// isAgentBusyError reports whether err is a saturated/overloaded shared ACP
// process fail-fast error (mitto-13ck.2). These errors wrap context.DeadlineExceeded
// but represent a BUSY agent, not a cancellation, so they must be classified
// before the generic context-cancelled branch in formatACPError.
func isAgentBusyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "saturated")
}

// isRateLimitError returns true if the error indicates the upstream API is
// rate-limiting the session.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errMsgLower := strings.ToLower(err.Error())
	return strings.Contains(errMsgLower, "rate limit") || strings.Contains(errMsgLower, "too many requests")
}

// formatACPError transforms ACP errors into user-friendly messages.
// It detects common error patterns and provides actionable guidance.
func formatACPError(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	// SDK control request timeout (CLI subprocess died, ACP tried to reconnect and timed out)
	// This is the 60s DEFAULT_CONTROL_REQUEST_TIMEOUT in claude-code-agent-sdk
	if strings.Contains(errMsg, "Control request timed out") ||
		strings.Contains(errMsg, "control request timed out") {
		return "The AI agent's internal connection to the CLI timed out. " +
			"This usually means the CLI subprocess crashed. The agent will attempt to restart automatically."
	}

	// HTTP 413 / context-too-large errors from the AI model.
	// Checked before the generic -32603 catch-all so users get an actionable message.
	if isContextTooLargeError(err) {
		return "⚠️ The conversation context is too large for the model. " +
			"Please start a new conversation. You can ask the agent to summarize the key points first if needed."
	}

	// Timeout errors from ACP server (tool execution took too long)
	if strings.Contains(errMsg, "aborted due to timeout") {
		return "A tool operation timed out. The AI agent's tool call took too long to complete. " +
			"Try breaking your request into smaller steps, or ask for a more specific task."
	}

	// Connection/transport errors
	if strings.Contains(errMsg, "peer disconnected") ||
		strings.Contains(errMsg, "connection reset") ||
		strings.Contains(errMsg, "broken pipe") ||
		strings.Contains(errMsg, "stream ended unexpectedly") {
		return "Lost connection to the AI agent. The agent process may have crashed or been restarted. " +
			"Please try sending your message again."
	}

	// Saturated/overloaded shared ACP process (mitto-13ck.2): start/resume failed fast
	// because the shared agent process is busy. This wraps context.DeadlineExceeded, so
	// it MUST be checked before the generic context-cancelled branch below to avoid the
	// misleading "request was cancelled" message.
	if isAgentBusyError(err) {
		return "The agent is busy — please try again in a moment."
	}

	// Context cancelled (user cancelled or session closed)
	if strings.Contains(errMsg, "context canceled") ||
		strings.Contains(errMsg, "context deadline exceeded") {
		return "The request was cancelled. Please try again."
	}

	// Rate limiting
	if isRateLimitError(err) {
		return "Rate limit reached. Please wait a moment before sending another message."
	}

	// JSON-RPC internal error (-32603) — try to extract HTTP status for better messages.
	// Previously this required "details" to be present in the message; without it the
	// raw JSON-RPC error string was shown to the user. Now we always return a
	// user-friendly message whenever the -32603 code is detected.
	if strings.Contains(errMsg, "-32603") && strings.Contains(errMsg, "Internal error") {
		if httpStatus := extractHTTPStatus(errMsg); httpStatus > 0 {
			switch httpStatus {
			case 408:
				return fmt.Sprintf("The AI service request timed out (HTTP %d). The service may be overloaded — please try again in a moment.", httpStatus)
			case 500:
				return fmt.Sprintf("The AI service encountered a server error (HTTP %d). Please try again.", httpStatus)
			case 502, 503:
				return fmt.Sprintf("The AI service is temporarily unavailable (HTTP %d). Please try again shortly.", httpStatus)
			case 504:
				return fmt.Sprintf("The AI service gateway timed out (HTTP %d). Please try again.", httpStatus)
			default:
				return fmt.Sprintf("The AI service returned an error (HTTP %d). Please try again, or simplify your request if the problem persists.", httpStatus)
			}
		}
		return "The AI agent encountered an internal error. Please try again, " +
			"or simplify your request if the problem persists."
	}

	// Default: return original error with prefix
	return "Prompt failed: " + errMsg
}

// extractHTTPStatus tries to extract an HTTP status code from an error string.
// It searches for common patterns like "HTTP error: NNN", `"httpStatus":NNN`, or "HTTP/1.1 NNN".
// Returns 0 if no HTTP status code is found or the extracted value is outside the 4xx–5xx range.
func extractHTTPStatus(errMsg string) int {
	matches := httpStatusRegex.FindStringSubmatch(errMsg)
	if len(matches) >= 2 {
		status, err := strconv.Atoi(matches[1])
		if err == nil && status >= 400 && status < 600 {
			return status
		}
	}
	return 0
}
