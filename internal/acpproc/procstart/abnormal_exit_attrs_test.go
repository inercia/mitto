package procstart

import (
	"syscall"
	"testing"
)

// findAttr returns the value paired with key in an slog-style key/value slice,
// or nil if key is absent. attrs is expected to be pairs: k,v,k,v,...
func findAttr(attrs []any, key string) any {
	for i := 0; i+1 < len(attrs); i += 2 {
		if k, ok := attrs[i].(string); ok && k == key {
			return attrs[i+1]
		}
	}
	return nil
}

// TestAbnormalExitAttrs_SignaledWithStderr covers the primary "crash with
// diagnostic" case (mitto-7vq acceptance criterion): a signaled exit MUST emit
// death_signal AND, when the collector has content, stderr_tail plus
// stderr_tail_len. Together these let a human answer OOM vs panic vs
// disconnect from one log line.
func TestAbnormalExitAttrs_SignaledWithStderr(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	_, _ = c.Write([]byte("panic: runtime error\n\tat foo.go:42\n"))

	attrs := AbnormalExitAttrs(syscall.SIGSEGV, true, c, DefaultStderrTailBytes)

	if got := findAttr(attrs, "death_signal"); got != "SIGSEGV" {
		t.Errorf("death_signal = %v, want SIGSEGV", got)
	}
	tail, ok := findAttr(attrs, "stderr_tail").(string)
	if !ok || tail == "" {
		t.Fatalf("stderr_tail missing or wrong type: %v", findAttr(attrs, "stderr_tail"))
	}
	if got := findAttr(attrs, "stderr_tail_len"); got != len(tail) {
		t.Errorf("stderr_tail_len = %v, want %d", got, len(tail))
	}
}

// TestAbnormalExitAttrs_NotSignaledOmitsDeathSignal covers the "wait error
// wasn't a signal" branch — no death_signal attribute is emitted, so
// downstream log-grep patterns don't see a spurious field.
func TestAbnormalExitAttrs_NotSignaledOmitsDeathSignal(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	_, _ = c.Write([]byte("some stderr\n"))

	attrs := AbnormalExitAttrs(nil, false, c, DefaultStderrTailBytes)

	if got := findAttr(attrs, "death_signal"); got != nil {
		t.Errorf("death_signal should be absent when not signaled, got %v", got)
	}
	// stderr_tail is still emitted (independent of signaled flag).
	if got := findAttr(attrs, "stderr_tail"); got == nil {
		t.Errorf("stderr_tail should still be emitted when collector has content")
	}
}

// TestAbnormalExitAttrs_NilCollectorDropsTail covers the intentional-shutdown
// symmetry case documented in death_signal.go: callers pass a nil collector
// so the DEBUG branch gets death_signal (for symmetry) but no stderr tail
// (intentional kills are not diagnostic).
func TestAbnormalExitAttrs_NilCollectorDropsTail(t *testing.T) {
	attrs := AbnormalExitAttrs(syscall.SIGKILL, true, nil, DefaultStderrTailBytes)

	if got := findAttr(attrs, "death_signal"); got != "SIGKILL" {
		t.Errorf("death_signal = %v, want SIGKILL", got)
	}
	if got := findAttr(attrs, "stderr_tail"); got != nil {
		t.Errorf("stderr_tail should be dropped when collector is nil, got %v", got)
	}
	if got := findAttr(attrs, "stderr_tail_len"); got != nil {
		t.Errorf("stderr_tail_len should be dropped when collector is nil, got %v", got)
	}
}

// TestAbnormalExitAttrs_EmptyCollectorDropsTail — a signaled exit whose
// collector never saw output emits death_signal but no stderr_tail. This
// keeps the log line free of empty-string noise fields.
func TestAbnormalExitAttrs_EmptyCollectorDropsTail(t *testing.T) {
	c := NewStderrCollector(8192, nil)

	attrs := AbnormalExitAttrs(syscall.SIGKILL, true, c, DefaultStderrTailBytes)

	if got := findAttr(attrs, "death_signal"); got != "SIGKILL" {
		t.Errorf("death_signal = %v, want SIGKILL", got)
	}
	if got := findAttr(attrs, "stderr_tail"); got != nil {
		t.Errorf("stderr_tail should be dropped when buffer is empty, got %v", got)
	}
}

// TestAbnormalExitAttrs_PairsAreSlogParseable ensures the returned slice has
// even length and every even index is a string key — this is what
// slog.Log(...) requires to render structured fields correctly at the
// two callsites (p.wait / bs.acpWait).
func TestAbnormalExitAttrs_PairsAreSlogParseable(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	_, _ = c.Write([]byte("crash detail"))

	attrs := AbnormalExitAttrs(syscall.SIGKILL, true, c, DefaultStderrTailBytes)

	if len(attrs)%2 != 0 {
		t.Fatalf("attrs length is odd (%d) — not a valid slog kv sequence: %+v", len(attrs), attrs)
	}
	for i := 0; i < len(attrs); i += 2 {
		if _, ok := attrs[i].(string); !ok {
			t.Errorf("attrs[%d] key is not a string: %T (%v)", i, attrs[i], attrs[i])
		}
	}
}

// TestAbnormalExitAttrs_TailIsCapped is a small end-to-end guard: the
// abnormal-exit log line must never carry substantially more than
// DefaultStderrTailBytes bytes of stderr, even if the collector has been
// filled to its full 8-KB capacity. Since mitto-zq6a, StderrTail emits a
// bounded head+tail excerpt plus a short elision marker on overflow, so the
// cap is enforced as maxBytes + a small marker allowance rather than an exact
// byte ceiling (see StderrTail's doc comment for the head+tail rationale).
func TestAbnormalExitAttrs_TailIsCapped(t *testing.T) {
	c := NewStderrCollector(8192, nil)
	// Fill the full 8-KB ring.
	big := make([]byte, 8192)
	for i := range big {
		big[i] = 'X'
	}
	_, _ = c.Write(big)

	attrs := AbnormalExitAttrs(syscall.SIGKILL, true, c, DefaultStderrTailBytes)

	tail, _ := findAttr(attrs, "stderr_tail").(string)
	if len(tail) > DefaultStderrTailBytes+64 {
		t.Errorf("stderr_tail len=%d exceeds cap %d+marker", len(tail), DefaultStderrTailBytes)
	}
	tlen, _ := findAttr(attrs, "stderr_tail_len").(int)
	if tlen != len(tail) {
		t.Errorf("stderr_tail_len=%d does not match tail len=%d", tlen, len(tail))
	}
}
