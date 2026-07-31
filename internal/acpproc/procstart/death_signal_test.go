package procstart

import (
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

// TestStderrTail_TruncatesToMaxBytes verifies only the trailing maxBytes bytes
// survive when the collector is fuller than the cap — this is the ring-buffer
// guarantee the abnormal-exit log line depends on to stay grep-friendly.
func TestStderrTail_TruncatesToMaxBytes(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	// Write 5 KB; ask for the last 1 KB.
	payload := strings.Repeat("A", 4000) + strings.Repeat("B", 1000)
	_, _ = c.Write([]byte(payload))
	got := StderrTail(c, 1024)
	if len(got) != 1024 {
		t.Fatalf("StderrTail len = %d, want 1024", len(got))
	}
	// Tail must be the trailing bytes — all 'B' since we asked for 1000 B's
	// plus 24 A's before them.
	if !strings.HasSuffix(got, strings.Repeat("B", 1000)) {
		t.Errorf("StderrTail tail does not end with the B run; got last 32=%q", got[len(got)-32:])
	}
}
