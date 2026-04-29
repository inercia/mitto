package web

import (
	"fmt"
	"math/rand"
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

// classifyACPError examines an error message and stderr output to determine
// whether the failure is permanent (should not retry) or transient (may succeed on retry).
// Returns nil if err is nil.
func classifyACPError(err error, stderr string) *ACPClassifiedError {
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

// isACPConnectionError reports whether err is a recoverable ACP pipe/connection
// error that can be resolved by restarting the underlying OS process.
// Used to detect the post-sleep/resume race condition where the OS has killed
// the ACP subprocess but the Go connection object still appears alive.
func isACPConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "shared ACP process has exited") ||
		strings.Contains(msg, "shared ACP process is not running")
}

// backoffDelay calculates an exponential backoff delay with jitter.
// attempt is 0-indexed (0 = first retry). The delay is capped at maxDelay.
// Jitter adds random variation of ±jitterRatio to prevent thundering herd.
func backoffDelay(attempt int, baseDelay, maxDelay time.Duration, jitterRatio float64) time.Duration {
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
