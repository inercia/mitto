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

// StderrTail returns the trailing bytes of the collector's buffer, trimmed of
// surrounding whitespace and with CRLF normalized to LF, keeping at most
// maxBytes bytes of the tail. Returns "" when the collector is nil or the
// buffer is empty so callers can drop the log field cleanly.
//
// The default 8-KB StderrCollector buffer plus a maxBytes of ~4 KB keeps the
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
	if len(out) > maxBytes {
		out = out[len(out)-maxBytes:]
	}
	return out
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
