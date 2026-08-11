package procstart

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// SignalName returns the canonical POSIX signal name (e.g. "SIGKILL", "SIGSEGV")
// for sig. Falls back to `Signal(<n>)` for unknown signals so the log line still
// carries useful data. Returns "" when sig is nil.
//
// Motivation (mitto-7vq): the default os.Signal.String() renders SIGKILL as
// "killed" and SIGSEGV as "segmentation fault", which makes it impossible to
// tell OOM-killer from an internal agent crash after the fact. Emitting a
// canonical name alongside the legacy string keeps existing grep patterns
// working while unblocking triage.
func SignalName(sig os.Signal) string {
	if sig == nil {
		return ""
	}
	sysSig, ok := sig.(syscall.Signal)
	if !ok {
		return sig.String()
	}
	switch sysSig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGILL:
		return "SIGILL"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGFPE:
		return "SIGFPE"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGBUS:
		return "SIGBUS"
	case syscall.SIGTRAP:
		return "SIGTRAP"
	case syscall.SIGUSR1:
		return "SIGUSR1"
	case syscall.SIGUSR2:
		return "SIGUSR2"
	case syscall.SIGXCPU:
		return "SIGXCPU"
	case syscall.SIGXFSZ:
		return "SIGXFSZ"
	}
	return fmt.Sprintf("Signal(%d)", int(sysSig))
}

// StderrTail returns a head+tail excerpt of the collector's buffer, trimmed of
// surrounding whitespace and with CRLF normalized to LF, keeping at most
// maxBytes bytes total (plus the elision marker when truncation occurs).
// Returns "" when the collector is nil or the buffer is empty so callers can
// drop the log field cleanly.
//
// Motivation (mitto-zq6a): tail-only truncation is right for a process that
// dies with a *final* error message, but wrong for a runtime that prints a
// fatal *header* followed by a long backtrace (e.g. a Node/V8 crash) — the
// tail window lands entirely inside the backtrace and the diagnostic header
// is discarded. When the buffer exceeds maxBytes, this now keeps the first
// maxBytes/2 bytes (head), an explicit "…[N bytes elided]…" marker, and the
// last maxBytes-maxBytes/2 bytes (tail) — guaranteeing both the fatal header
// and the final frames survive. Each cut point is snapped to the nearest
// newline within a small bounded window so the excerpt does not start or end
// mid-token; if no newline is found in the window, the raw byte cut is used
// instead (the excerpt never grows past its budget).
//
// The default 32-KB StderrCollector buffer plus a maxBytes of ~4 KB keeps the
// abnormal-exit log line manageable while still surfacing enough context to
// disambiguate crash causes.
func StderrTail(collector *StderrCollector, maxBytes int) string {
	if collector == nil || maxBytes <= 0 {
		return ""
	}
	out := collector.GetOutput()
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	if len(out) <= maxBytes {
		return out
	}

	headBytes := maxBytes / 2
	tailBytes := maxBytes - headBytes

	head := snapHeadToLineBoundary(out[:headBytes])
	tail := snapTailToLineBoundary(out[len(out)-tailBytes:])

	// Count elided bytes after snapping, so the marker reports what was
	// actually dropped rather than the pre-snap estimate.
	elided := len(out) - len(head) - len(tail)

	marker := fmt.Sprintf("\n…[%d bytes elided]…\n", elided)
	return head + marker + tail
}

// stderrSnapWindow bounds how far snapHeadToLineBoundary and
// snapTailToLineBoundary will search for a newline, so a single pathological
// long line cannot shrink the head (or grow the tail) unboundedly.
const stderrSnapWindow = 256

// snapHeadToLineBoundary trims s back to the last newline found within the
// final stderrSnapWindow bytes, so a head excerpt never ends mid-token. If no
// newline is found in that window, s is returned unchanged.
func snapHeadToLineBoundary(s string) string {
	start := len(s) - stderrSnapWindow
	if start < 0 {
		start = 0
	}
	if idx := strings.LastIndexByte(s[start:], '\n'); idx >= 0 {
		return s[:start+idx]
	}
	return s
}

// snapTailToLineBoundary advances s past the first newline found within the
// first stderrSnapWindow bytes, so a tail excerpt never starts mid-token. If
// no newline is found in that window, s is returned unchanged.
func snapTailToLineBoundary(s string) string {
	end := stderrSnapWindow
	if end > len(s) {
		end = len(s)
	}
	if idx := strings.IndexByte(s[:end], '\n'); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// AbnormalExitAttrs returns structured log attributes describing an abnormal
// ACP process exit — a canonical death_signal name (when the process was
// signaled), a stderr_tail (when non-empty), and stderr_tail_len. The caller
// appends these to its own base attrs (workspace/session identifiers) before
// logging the WARN line.
//
// Passing signaled=false skips the death_signal attr (useful for the
// intentional-shutdown DEBUG branch which only wants the tail for symmetry).
// Passing a nil collector or empty buffer drops the tail cleanly.
func AbnormalExitAttrs(sig os.Signal, signaled bool, collector *StderrCollector, maxBytes int) []any {
	attrs := make([]any, 0, 4)
	if signaled {
		if name := SignalName(sig); name != "" {
			attrs = append(attrs, "death_signal", name)
		}
	}
	if tail := StderrTail(collector, maxBytes); tail != "" {
		attrs = append(attrs, "stderr_tail", tail, "stderr_tail_len", len(tail))
	}
	return attrs
}

// DefaultStderrTailBytes is the default cap applied to the stderr tail emitted
// on abnormal-exit log lines. 4 KB is small enough to keep a single WARN line
// readable while still capturing several stack frames or a Node.js fatal
// message on top of any earlier progress noise.
const DefaultStderrTailBytes = 4096

// DefaultStderrCollectorBytes is the default max buffer size for a
// StderrCollector feeding StderrTail. 32 KB is large enough to retain a full
// V8 fatal-error dump (typically 6-10 KB including the backtrace) end to end,
// so the head half of StderrTail's excerpt is the real head of the crash
// dump rather than the head of an already ring-truncated buffer (mitto-zq6a).
const DefaultStderrCollectorBytes = 32768
