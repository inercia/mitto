package acp

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
	{
		// The agent's internal MCP-init wait budget elapsed before every configured MCP
		// server finished handshake, so the pending session/new was aborted by the
		// stderr-signal watch (mitto-8ul.1). Retrying with the same MCP configuration
		// will produce the same failure until the underlying MCP server is fixed.
		substrings:   []string{"mcp initialization timed out"},
		userMessage:  "MCP server initialization timed out",
		userGuidance: "Check that every configured MCP server is reachable and starts within the agent's MCP-init budget. Fix the failing MCP server or remove it from the workspace configuration.",
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

// FormatClassifiedError returns a user-friendly string combining the message and guidance.
// Used for observer notifications.
func FormatClassifiedError(classified *ACPClassifiedError) string {
	if classified == nil {
		return ""
	}
	if classified.UserGuidance != "" {
		return fmt.Sprintf("%s. %s", classified.UserMessage, classified.UserGuidance)
	}
	return classified.UserMessage
}

// MCPInitTimeoutPattern detects the agent's "MCP initialization timed out after Ns"
// line, emitted when its internal MCP wait budget elapses without all servers being
// ready. Matched tolerantly so we don't couple to the exact suffix (mitto-8ul.1).
var MCPInitTimeoutPattern = regexp.MustCompile(`(?i)mcp initialization timed out`)

// mcpTimedOutServerLinePattern matches a per-server status line the agent emits
// on the same stderr chunk as the generic MCP-init-timeout message, e.g.
// "   ⏳ yahoo-finance (timed out)". Captures the server name (mitto-m8nx AC2).
var mcpTimedOutServerLinePattern = regexp.MustCompile(`⏳\s*(\S+)\s*\(timed out\)`)

// ExtractMCPTimedOutServers scans a stderr chunk for per-server "⏳ <name> (timed
// out)" lines and returns the names of every server that failed to initialize in
// time, in the order they appear. Returns nil if no such line is present — this is
// the common case for older agents (or a stderr read-buffer boundary split) that
// only emit the generic "MCP initialization timed out" tail matched by
// MCPInitTimeoutPattern; callers must fall back to a workspace-only message in
// that case (mitto-m8nx AC2).
func ExtractMCPTimedOutServers(chunk string) []string {
	matches := mcpTimedOutServerLinePattern.FindAllStringSubmatch(chunk, -1)
	if len(matches) == 0 {
		return nil
	}
	servers := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 {
			servers = append(servers, m[1])
		}
	}
	return servers
}

// IsMCPInitTimeout reports whether err (possibly wrapped in *ACPClassifiedError)
// carries the agent's "MCP initialization timed out" signal. This is TRANSIENT
// on a cold shared ACP process — once the process warms (mitto-54k.3 warm-once
// barrier) a retry succeeds. It is used by auto-resume paths (mitto-54k.6) to
// avoid counting a cold-start MCP-init timeout toward the hard ACP-start failure
// threshold (which would otherwise auto-archive an otherwise-resumable session).
//
// Note: the classification in permanentErrorPatterns intentionally stays as
// Permanent to avoid interactive retry storms on genuinely broken MCP servers;
// this predicate is a targeted carve-out for the background auto-resume counter
// only. Since *ACPClassifiedError.Error() forwards to the underlying original
// error, a substring/regex match on err.Error() works through the wrapper.
func IsMCPInitTimeout(err error) bool {
	if err == nil {
		return false
	}
	return MCPInitTimeoutPattern.MatchString(err.Error())
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

// status413Regex matches a 413 status code anchored to a recognizable status
// keyword or HTTP-response prefix (mitto-3rs). Unlike a bare `strings.Contains(s,
// "413")`, this does not false-positive on the digits "413" appearing incidentally
// elsewhere in the error string (e.g. inside a request-id UUID segment such as
// "f24b-4130-..."). It deliberately does NOT use a plain `\b413\b` word-boundary
// match either: this codebase's error strings routinely carry duration-style
// fields (e.g. "duration_ms=413", "elapsed_ms=413") that would reintroduce the
// same class of false positive. Kept separate from httpStatusRegex (which is
// shared with extractHTTPStatus and feeds unrelated -32603 message formatting)
// so this fix does not alter other call sites.
var status413Regex = regexp.MustCompile(`(?i)(?:HTTP error:\s*|"?(?:http)?status"?\s*:\s*|HTTP/[12](?:\.[01])?\s+)413\b`)

// IsContextTooLargeError returns true if the error indicates the AI model
// rejected the prompt because the conversation context is too large (HTTP 413
// or an equivalent model-specific error phrase).
//
// The ACP server forwards HTTP 413 responses as JSON-RPC -32603 "Internal error"
// messages, so the numeric status code or the model-specific phrase may appear
// anywhere in the error string.  We keep the list of patterns here (rather than
// inlining them in FormatACPError) so that the prompt dispatcher's queue-advancement
// logic and the loop runner's auto-pause guard (via internal/web) can reuse the
// same predicate without duplicating strings.
//
// mitto-k4x: Augment's chat-stream endpoint returns HTTP 400 with
// apiStatus="invalidArgument" for oversized/malformed context-flush payloads
// (not HTTP 413). Both substrings are required so unrelated 400s do not match.
//
// mitto-2efc: that pair-match alone is too broad — ANY upstream 400
// invalidArgument (e.g. a deferred model-switch race, or any other
// malformed-request 400 unrelated to context size) was being classified as
// context-too-large. The 400/invalidArgument pair must now ALSO be
// corroborated by an actual token/length overflow signal somewhere in the
// payload before it is treated as context-too-large; otherwise the error is
// reported verbatim (via the generic delivery-failure path) instead of
// masquerading as contextWindowExceeded.
func IsContextTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	errMsgLower := strings.ToLower(errMsg)
	if status413Regex.MatchString(errMsg) ||
		strings.Contains(errMsgLower, "context too large") ||
		strings.Contains(errMsgLower, "context_too_long") ||
		strings.Contains(errMsgLower, "context_length_exceeded") ||
		strings.Contains(errMsgLower, "context window is full") ||
		strings.Contains(errMsgLower, "prompt is too long") ||
		strings.Contains(errMsgLower, "maximum context length") ||
		strings.Contains(errMsgLower, "context too large for model") {
		return true
	}
	if strings.Contains(errMsgLower, `"httpstatus":400`) &&
		strings.Contains(errMsgLower, `"apistatus":"invalidargument"`) {
		// Require corroborating evidence of a token/length overflow before
		// treating a generic 400/invalidArgument as context-too-large
		// (mitto-2efc).
		return strings.Contains(errMsgLower, "token") ||
			strings.Contains(errMsgLower, "too long") ||
			strings.Contains(errMsgLower, "maximum length") ||
			strings.Contains(errMsgLower, "length exceeds") ||
			strings.Contains(errMsgLower, "too large") ||
			strings.Contains(errMsgLower, "context")
	}
	return false
}

// IsChatStreamOversizedArgumentError returns true if err carries the bare
// Augment chat-stream `httpStatus:400` / `apiStatus:"invalidArgument"` pair,
// WITHOUT requiring the token/length corroboration that IsContextTooLargeError
// demands (mitto-2efc). It is deliberately narrower in scope than a general
// context-size predicate: callers MUST additionally corroborate with their own
// independent oversized-context signal before treating a match as
// context-too-large — using this alone would reintroduce the mitto-2efc false
// positive (a deferred model-switch race or any other malformed-request 400
// unrelated to context size also matches this pair).
//
// mitto-5se: the loop runner's handleDeliveryFailure uses this predicate
// together with the session's own measured acpContextTurnsSinceReset() (an
// independent, loop-scoped size proxy) to reclassify a bare 400 as
// oversized_context only when the loop's own context was already large at
// dispatch — leaving acp.IsContextTooLargeError itself untouched so its
// anti-false-positive guarantee for all other callers is preserved.
func IsChatStreamOversizedArgumentError(err error) bool {
	if err == nil {
		return false
	}
	errMsgLower := strings.ToLower(err.Error())
	return strings.Contains(errMsgLower, `"httpstatus":400`) &&
		strings.Contains(errMsgLower, `"apistatus":"invalidargument"`)
}

// isAgentBusyError reports whether err is a saturated/overloaded shared ACP
// process fail-fast error (mitto-13ck.2). These errors wrap context.DeadlineExceeded
// but represent a BUSY agent, not a cancellation, so they must be classified
// before the generic context-cancelled branch in FormatACPError.
func isAgentBusyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "saturated")
}

// IsRateLimitError returns true if the error indicates the upstream API is
// rate-limiting the session.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errMsgLower := strings.ToLower(err.Error())
	return strings.Contains(errMsgLower, "rate limit") || strings.Contains(errMsgLower, "too many requests")
}

// IsAuthError returns true if the error indicates the upstream CLI's
// authentication has expired or the user is not logged in. Claude Code
// surfaces this as JSON-RPC `-32000 "Authentication required"` when its
// Anthropic OAuth token has expired mid-conversation; the ACP process is
// still alive, so a restart won't help — the user must re-authenticate the
// CLI. Callers (handlePromptError) use this to stop queue advancement so
// every queued message doesn't cascade the same failure (mitto-r5o).
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "authentication required")
}

// IsHandshakeQueryClosedError reports whether err is the agent's "query closed
// before response received" wedge signature on a session/new handshake — JSON-RPC
// -32603 ("Internal error") whose data carries "query closed before response
// received" (case-insensitive). This is NOT necessarily an auth failure
// (mitto-biu, correcting the mitto-bov assumption): the SDK's async iterator is
// torn down before writing a response both on cold-start auth failure AND on a
// long-lived shared process whose internal query loop has wedged (mitto-aoo,
// which now auto-recycles this exact signature via
// internal/acpproc.isAgentQueryClosedErr -> recordRPCWedgeFailure -> saturation
// -> GC Tier 5/6 recycle).
//
// internal/acp cannot import internal/acpproc/acperrors here (acperrors imports
// this package, an import cycle), so this is a string-based twin of the
// structured classifier acperrors.IsAgentQueryClosedErr /
// internal/acpproc/shared_acp_process.go:isAgentQueryClosedErr — keep the three
// implementations in sync.
func IsHandshakeQueryClosedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, "-32603") &&
		strings.Contains(strings.ToLower(errMsg), "query closed before response received")
}

// ProcessHistory is a tri-state signal describing whether the shared ACP
// process that produced an error had previously completed at least one
// successful session RPC (session/new or session/load) before this failure.
// Kept tri-state (rather than a plain bool) so FormatACPError's existing
// unhedged behavior is preserved by default: only a caller that explicitly
// passes ProcessHistoryWarm gets the auth-free wording (mitto-azk).
type ProcessHistory int

const (
	// ProcessHistoryUnknown is the zero value: the caller has no corroborating
	// signal about the process's prior health. Formatting falls back to the
	// original cause-neutral-but-hedged wording (mitto-biu).
	ProcessHistoryUnknown ProcessHistory = iota
	// ProcessHistoryCold indicates this is a first-contact failure: the shared
	// process has never completed a session RPC. Auth is a plausible cause, so
	// the secondary "check the CLI is authenticated" hint is retained.
	ProcessHistoryCold
	// ProcessHistoryWarm indicates the shared process previously completed at
	// least one session/new or session/load successfully. Auth cannot be the
	// cause of a later handshake failure on the same process, so the message
	// drops the authentication hint entirely (mitto-azk).
	ProcessHistoryWarm
)

// IsUpstreamUnavailableError reports whether err indicates the agent's
// upstream API (e.g. xlb.api.augmentcode.com) was unavailable — either at the
// network level (a connect-timeout brownout) or at the application level (an
// HTTP 5xx chat-stream response) — rather than an agent-side internal error.
//
// Node's undici HTTP client surfaces the network-level case as
// `UND_ERR_CONNECT_TIMEOUT` / "Connect Timeout Error" wrapped in a "fetch
// failed" JSON-RPC -32603 envelope; that envelope carries no HTTP status code,
// so extractHTTPStatus finds nothing and the generic -32603 branch would
// otherwise misreport it as an opaque "internal error" (mitto-gbf5).
//
// The application-level case is an HTTP 5xx from the backend (e.g. 500 during a
// provider outage), delivered as a -32603 envelope whose `data.apiStatus` is
// "unavailable" (mitto-bfu). In both cases Augment's authoritative
// provider-outage marker is `apiStatus == "unavailable"`, so we match that
// marker directly (rather than requiring a specific companion substring such as
// "fetch failed"). A bare 5xx that lacks the marker is intentionally NOT
// treated as an upstream outage here — it keeps flowing through the generic
// -32603 HTTP-status branch of FormatACPError unchanged.
func IsUpstreamUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	errMsgLower := strings.ToLower(err.Error())
	if strings.Contains(errMsgLower, "und_err_connect_timeout") ||
		strings.Contains(errMsgLower, "connect timeout error") {
		return true
	}
	return strings.Contains(errMsgLower, `"apistatus":"unavailable"`)
}

// IsAgentInternalError reports whether err is an agent-internal (not
// Mitto-side) JavaScript runtime error surfaced through a JSON-RPC -32603
// "Internal error" envelope — e.g. a minified TypeError such as "n.map is
// not a function" (mitto-3sc). Auggie's turn-finalization code can throw
// this when an internal value it expects to be an array is not; the
// exception was observed as the RESULT of the session/prompt RPC at turn
// completion, AFTER a full turn had already streamed successfully — not as
// a rejection of a malformed prompt payload on delivery. Because the
// offending code is agent-internal (minified, external to Mitto), there is
// no local call site to guard; the corresponding fix is purely a
// classification/wording change so the user is not told to "simplify their
// request", which cannot fix an agent-side defect.
//
// Checked in FormatACPErrorWithContext before the generic -32603 catch-all,
// but after IsUpstreamUnavailableError and IsHandshakeQueryClosedError so it
// only claims -32603 envelopes those more specific classifiers don't.
func IsAgentInternalError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "-32603") || !strings.Contains(errMsg, "Internal error") {
		return false
	}
	errMsgLower := strings.ToLower(errMsg)
	return strings.Contains(errMsgLower, "is not a function") ||
		strings.Contains(errMsgLower, "cannot read propert") ||
		strings.Contains(errMsgLower, "is not iterable") ||
		strings.Contains(errMsgLower, "undefined is not")
}

// FormatErrorHints carries optional corroborating context that lets
// FormatACPErrorWithContext refine its message beyond what the raw error
// string alone can tell. Zero value means "no additional context" and
// FormatACPErrorWithContext(err, FormatErrorHints{}) is byte-identical to
// FormatACPError(err). Designed so future hints can be added as new fields
// without another functions signature change (mitto-azk).
type FormatErrorHints struct {
	// ProcessHistory corroborates a handshake failure's likely cause using the
	// shared process's prior session-RPC history. See ProcessHistory.
	ProcessHistory ProcessHistory
}

// FormatACPError transforms ACP errors into user-friendly messages.
// It detects common error patterns and provides actionable guidance.
// This is a convenience wrapper around FormatACPErrorWithContext with no
// additional hints (ProcessHistoryUnknown) — see that function for the
// context-aware variant.
func FormatACPError(err error) string {
	return FormatACPErrorWithContext(err, FormatErrorHints{})
}

// FormatACPErrorWithContext transforms ACP errors into user-friendly messages,
// optionally refining the wording using corroborating hints the caller already
// has (e.g. whether the shared process had previously completed a session RPC).
// With the zero-value FormatErrorHints{}, behavior is identical to
// FormatACPError.
func FormatACPErrorWithContext(err error, hints FormatErrorHints) string {
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
	if IsContextTooLargeError(err) {
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

	// Upstream provider unavailable — either a network-level connect-timeout
	// brownout (UND_ERR_CONNECT_TIMEOUT to xlb.api.augmentcode.com, mitto-gbf5)
	// or an application-level HTTP 5xx whose data.apiStatus is "unavailable"
	// (mitto-bfu). Checked before the generic context-cancelled and -32603
	// branches so this transient provider outage is named for the user —
	// distinct from an opaque agent "internal error", a "request was
	// cancelled", or an auth/config problem — and framed as self-healing.
	if IsUpstreamUnavailableError(err) {
		return "The AI agent's upstream API is temporarily unavailable (provider " +
			"outage). This is transient and Mitto will retry automatically — " +
			"please try again in a moment."
	}

	// Context cancelled (user cancelled or session closed)
	if strings.Contains(errMsg, "context canceled") ||
		strings.Contains(errMsg, "context deadline exceeded") {
		return "The request was cancelled. Please try again."
	}

	// Rate limiting
	if IsRateLimitError(err) {
		return "Rate limit reached. Please wait a moment before sending another message."
	}

	// Authentication required (mitto-r5o) — upstream CLI's session token has
	// expired or the user is not logged in. The ACP agent process is still
	// alive; restarting won't help. User needs to re-authenticate the CLI.
	if IsAuthError(err) {
		return "🔐 The AI agent's authentication has expired. " +
			"Please re-authenticate the CLI in a terminal (e.g. `claude auth login` " +
			"for Claude Code, or `auggie auth login` for Auggie), then send your message again."
	}

	// Handshake "query closed before response received" (mitto-biu, correcting
	// mitto-bov). The SDK's session/new async iterator is torn down before
	// writing a response and surfaces as JSON-RPC -32603 with data.details
	// "Query closed before response received" — a different code path from the
	// -32000 case above. mitto-bov assumed this always means expired CLI auth;
	// it does not — it's also the symptom of a wedged shared process whose
	// internal query loop has torn down (mitto-aoo), which Mitto now detects
	// and recycles automatically. The message is therefore cause-neutral and
	// remedy-first: Restart ACP (with a note that Mitto also auto-recycles this
	// case) leads, and re-authenticating is only a hedged secondary hint.
	// Placed before the generic -32603 catch-all so this friendly message wins
	// over "AI service returned an error". Match is case-insensitive on the
	// details substring in case the SDK wording drifts.
	if IsHandshakeQueryClosedError(err) {
		if hints.ProcessHistory == ProcessHistoryWarm {
			// The shared process previously completed at least one session RPC
			// successfully, so an auth problem cannot explain this failure —
			// omit the hint entirely (mitto-azk).
			return "The agent process could not start a new session (handshake failed). " +
				"The agent had been working normally and its internal query loop has " +
				"since wedged. Click Restart ACP for this workspace — Mitto also " +
				"recycles a wedged agent process automatically."
		}
		return "The agent process could not start a new session (handshake failed). " +
			"Click Restart ACP for this workspace — Mitto also recycles a wedged agent " +
			"process automatically. If it keeps happening, check that the CLI is " +
			"authenticated (e.g. `claude auth login` for Claude Code, or `auggie auth login` " +
			"for Auggie)."
	}

	// Agent-internal JS runtime error (mitto-3sc) — a minified TypeError (e.g.
	// "n.map is not a function") raised inside the agent's own turn-finalization
	// code, surfaced as a -32603 envelope at turn completion. This is neither an
	// upstream provider outage nor an HTTP-status error, so it must be checked
	// before the generic -32603 catch-all below; simplifying the prompt cannot
	// fix an agent-side defect, so that advice is deliberately omitted here.
	if IsAgentInternalError(err) {
		return "The AI agent hit an internal error while finishing its response. " +
			"This is a defect in the agent itself, not your request — it is usually " +
			"transient. Please try again."
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
