// Package procstart holds stateless helpers for starting an ACP subprocess:
// stderr collection/monitoring, per-agent pattern compilation, startup watchdog,
// and env-layering for the direct-exec and restricted-runner branches. Kept as
// a leaf under internal/acpproc so both internal/conversation and
// internal/acpproc can depend on it without introducing an import cycle
// (mitto-iuw2).
package procstart

import (
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// StderrCollector collects stderr output from the ACP process for error reporting.
type StderrCollector struct {
	mu       sync.Mutex
	buffer   []byte
	maxSize  int
	logger   *slog.Logger
	isClosed bool
	// ignorePatterns, if non-nil, causes matching writes to be suppressed from
	// the debug-level "agent stderr" log line. Crash detection is unaffected —
	// crash matching happens in StartStderrMonitor, not here (mitto-k6h).
	ignorePatterns []*regexp.Regexp
}

// NewStderrCollector creates a new stderr collector with the given max buffer size.
func NewStderrCollector(maxSize int, logger *slog.Logger) *StderrCollector {
	return &StderrCollector{
		buffer:  make([]byte, 0, maxSize),
		maxSize: maxSize,
		logger:  logger,
	}
}

// SetIgnorePatterns replaces the collector's debug-log suppression patterns
// (mitto-k6h). Safe to call before the monitor goroutine is started. Passing
// nil clears the patterns.
func (c *StderrCollector) SetIgnorePatterns(patterns []*regexp.Regexp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ignorePatterns = patterns
}

// Write implements io.Writer to collect stderr output.
func (c *StderrCollector) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return len(p), nil
	}

	// Log at debug level as it comes in, suppressing harmless protocol noise.
	// The acp-go-sdk sends $/cancel_request (JSON-RPC LSP-style) which ACP agents
	// don't support; their "Method not found" rejection written to stderr is expected
	// and can be safely ignored. The SDK-level error log for this is already suppressed
	// in logging.go; this suppresses the agent-side stderr counterpart.
	//
	// Per-agent ignore patterns (mitto-k6h) additionally suppress the debug log for
	// any write matching one of the compiled regexes. Buffer capture is unaffected —
	// error diagnostics still see the full tail.
	if c.logger != nil && len(p) > 0 {
		output := string(p)
		if !strings.Contains(output, "$/cancel_request") && !matchAnyRegex(c.ignorePatterns, output) {
			c.logger.Debug("agent stderr", "output", output)
		}
	}

	// Append to buffer, keeping only the last maxSize bytes
	c.buffer = append(c.buffer, p...)
	if len(c.buffer) > c.maxSize {
		c.buffer = c.buffer[len(c.buffer)-c.maxSize:]
	}

	return len(p), nil
}

// GetOutput returns the collected stderr output.
func (c *StderrCollector) GetOutput() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buffer)
}

// Close marks the collector as closed and logs any remaining output at warn level if non-empty.
func (c *StderrCollector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isClosed = true
}

// stderrCrashPatterns are substrings in ACP process stderr output that indicate
// the inner CLI subprocess has crashed. When detected, we proactively signal
// process death via onCrashDetected callback rather than waiting for the SDK's
// 60-second control request timeout (DEFAULT_CONTROL_REQUEST_TIMEOUT).
//
// Fix C: These patterns come from the claude-code-agent-sdk Rust layer which logs
// to stderr when the CLI subprocess dies unexpectedly.
//
// These are the hardcoded baseline. Per-agent metadata.yaml `stderrPatterns.crash`
// entries are unioned with this list at process-start time (mitto-k6h).
var stderrCrashPatterns = []string{
	"stream ended unexpectedly",
	"EOF received from CLI stdout",
	"background reader: stream ended",
	"connection reset by peer",
	"broken pipe",
	// From acp-go-sdk's JSONRPC parser when receiving malformed messages from a dying process
	"received message with neither id nor method",
	// From acp-go-sdk's notification queue overflow handler (triggers when process is overwhelmed)
	"failed to queue notification; closing connection",
	// Node/V8 fatal error when the agent subprocess exhausts its JS heap (mitto-5q8).
	// Detecting this immediately speeds proactive recycle instead of waiting for the
	// dead process to be discovered on the next RPC attempt.
	"JavaScript heap out of memory",
	"Reached heap limit",
}

// matchAnyRegex returns true if any regex in patterns matches s. Nil/empty
// slice always returns false.
func matchAnyRegex(patterns []*regexp.Regexp, s string) bool {
	for _, re := range patterns {
		if re != nil && re.MatchString(s) {
			return true
		}
	}
	return false
}
