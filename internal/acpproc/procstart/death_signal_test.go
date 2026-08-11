package procstart

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestSignalName_CanonicalNames verifies the mapper produces canonical
// "SIG<NAME>" strings for the signals that matter for ACP-process triage
// (mitto-7vq): OOM-killer (SIGKILL), internal crashes (SIGSEGV/SIGBUS/SIGABRT),
// user cancel (SIGINT), and clean shutdown (SIGTERM). The bead's acceptance
// criterion is that a human can grep the log and immediately tell OOM from
// panic from cancel.
func TestSignalName_CanonicalNames(t *testing.T) {
	cases := []struct {
		sig  syscall.Signal
		want string
	}{
		{syscall.SIGHUP, "SIGHUP"},
		{syscall.SIGINT, "SIGINT"},
		{syscall.SIGQUIT, "SIGQUIT"},
		{syscall.SIGILL, "SIGILL"},
		{syscall.SIGABRT, "SIGABRT"},
		{syscall.SIGFPE, "SIGFPE"},
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGSEGV, "SIGSEGV"},
		{syscall.SIGPIPE, "SIGPIPE"},
		{syscall.SIGALRM, "SIGALRM"},
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGBUS, "SIGBUS"},
		{syscall.SIGTRAP, "SIGTRAP"},
		{syscall.SIGUSR1, "SIGUSR1"},
		{syscall.SIGUSR2, "SIGUSR2"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			got := SignalName(tc.sig)
			if got != tc.want {
				t.Errorf("SignalName(%v) = %q, want %q", tc.sig, got, tc.want)
			}
		})
	}
}

// TestSignalName_NilReturnsEmpty locks down the nil-input contract callers
// depend on to drop the death_signal log field cleanly when a wait error
// isn't signaled.
func TestSignalName_NilReturnsEmpty(t *testing.T) {
	if got := SignalName(nil); got != "" {
		t.Errorf("SignalName(nil) = %q, want empty", got)
	}
}

// TestSignalName_UnknownSignalFallback verifies unmapped syscall.Signal values
// still produce a non-empty, parseable string so the log line never carries a
// silent zero. A real-user grepping "Signal(" gets a hit and can look up the
// number.
func TestSignalName_UnknownSignalFallback(t *testing.T) {
	// Signal 99 is not defined on macOS/Linux; syscall.Signal accepts arbitrary ints.
	got := SignalName(syscall.Signal(99))
	if !strings.HasPrefix(got, "Signal(") || !strings.Contains(got, "99") {
		t.Errorf("SignalName(Signal(99)) = %q, want prefix Signal(99)", got)
	}
}

// TestSignalName_NonSyscallSignalFallsBackToString covers the defensive
// non-syscall.Signal branch — callers passing an os.Signal from a mock or a
// future custom type should still get a usable string.
func TestSignalName_NonSyscallSignalFallsBackToString(t *testing.T) {
	var custom os.Signal = fakeSignal("custom-sig")
	if got := SignalName(custom); got != "custom-sig" {
		t.Errorf("SignalName(fakeSignal) = %q, want %q", got, "custom-sig")
	}
}

type fakeSignal string

func (f fakeSignal) String() string { return string(f) }
func (f fakeSignal) Signal()        {}

// TestStderrTail_NilAndEmpty locks down the two "drop the field cleanly"
// contracts — nil collector and an empty (whitespace-only) buffer both return
// "" so AbnormalExitAttrs skips the tail attribute.
func TestStderrTail_NilAndEmpty(t *testing.T) {
	if got := StderrTail(nil, 4096); got != "" {
		t.Errorf("StderrTail(nil) = %q, want empty", got)
	}
	c := NewStderrCollector(8192, nil)
	if got := StderrTail(c, 4096); got != "" {
		t.Errorf("StderrTail(empty collector) = %q, want empty", got)
	}
	// Whitespace-only content also trims to empty.
	_, _ = c.Write([]byte("   \n\r\n  \t \n"))
	if got := StderrTail(c, 4096); got != "" {
		t.Errorf("StderrTail(whitespace-only) = %q, want empty", got)
	}
}

// TestStderrTail_MaxBytesNonPositive verifies the maxBytes<=0 short-circuit —
// callers passing 0 or a negative cap get "" instead of the full buffer,
// preventing an accidental "log everything" leak.
func TestStderrTail_MaxBytesNonPositive(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	_, _ = c.Write([]byte("some content"))
	if got := StderrTail(c, 0); got != "" {
		t.Errorf("StderrTail(maxBytes=0) = %q, want empty", got)
	}
	if got := StderrTail(c, -1); got != "" {
		t.Errorf("StderrTail(maxBytes=-1) = %q, want empty", got)
	}
}

// TestStderrTail_CRLFNormalizedAndTrimmed verifies Windows-style CRLF line
// endings are normalized to LF and leading/trailing whitespace is stripped so
// the log field is one clean multi-line string, not a mix of \r\n and \n.
func TestStderrTail_CRLFNormalizedAndTrimmed(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	_, _ = c.Write([]byte("\r\n  line1\r\nline2\r\n  \n"))
	got := StderrTail(c, 4096)
	if strings.Contains(got, "\r") {
		t.Errorf("StderrTail retained CR: %q", got)
	}
	if got != "line1\nline2" {
		t.Errorf("StderrTail = %q, want %q", got, "line1\nline2")
	}
}

// TestStderrTail_TruncatesToMaxBytes verifies the head+tail excerpt semantics
// (mitto-zq6a) when the collector is fuller than the cap: the result contains
// both the head and the tail of the buffer, joined by an explicit elision
// marker, and stays within maxBytes plus the marker.
func TestStderrTail_TruncatesToMaxBytes(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	// Write 5 KB; ask for 1 KB total.
	payload := strings.Repeat("A", 4000) + strings.Repeat("B", 1000)
	_, _ = c.Write([]byte(payload))
	got := StderrTail(c, 1024)
	if len(got) > 1024+64 {
		t.Fatalf("StderrTail len = %d, want <= 1024+marker", len(got))
	}
	if !strings.HasPrefix(got, strings.Repeat("A", 10)) {
		t.Errorf("StderrTail does not start with the A run (head); got first 32=%q", got[:32])
	}
	if !strings.HasSuffix(got, strings.Repeat("B", 10)) {
		t.Errorf("StderrTail does not end with the B run (tail); got last 32=%q", got[len(got)-32:])
	}
	if !strings.Contains(got, "bytes elided") {
		t.Errorf("StderrTail missing elision marker: %q", got)
	}
}

// TestStderrTail_NoMarkerWhenNotTruncated verifies the elision marker is
// absent when the buffer fits within maxBytes — head+tail excerpting only
// kicks in on overflow.
func TestStderrTail_NoMarkerWhenNotTruncated(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	_, _ = c.Write([]byte("short content"))
	got := StderrTail(c, 4096)
	if strings.Contains(got, "elided") {
		t.Errorf("StderrTail added an elision marker despite fitting: %q", got)
	}
	if got != "short content" {
		t.Errorf("StderrTail = %q, want %q", got, "short content")
	}
}

// TestStderrTail_PreservesV8FatalHeader is the acceptance-criterion
// regression test for mitto-zq6a: a simulated V8 fatal-error dump whose
// header would previously be discarded by tail-only truncation must survive
// in the head half of the excerpt, alongside the last backtrace frame in the
// tail half.
func TestStderrTail_PreservesV8FatalHeader(t *testing.T) {
	c := NewStderrCollector(1<<20, nil)
	var b strings.Builder
	b.WriteString("FATAL ERROR: Ineffective mark-compacts near heap limit Allocation failed - JavaScript heap out of memory\n")
	b.WriteString("----- Native stack trace -----\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, " %d: 0x%x some_frame_symbol(args) [/opt/homebrew/lib/libnode.dylib]\n", i, 0x1000000+i)
	}
	_, _ = c.Write([]byte(b.String()))

	got := StderrTail(c, DefaultStderrTailBytes)
	if !strings.Contains(got, "FATAL ERROR") {
		t.Errorf("StderrTail dropped the fatal header; got head+tail: %q", got[:200])
	}
	if !strings.Contains(got, "199:") {
		t.Errorf("StderrTail dropped the last backtrace frame")
	}
	if !strings.Contains(got, "bytes elided") {
		t.Errorf("StderrTail missing elision marker for an overflowing dump")
	}
	if len(got) > DefaultStderrTailBytes+64 {
		t.Errorf("StderrTail len = %d, want <= %d+marker", len(got), DefaultStderrTailBytes)
	}
}
