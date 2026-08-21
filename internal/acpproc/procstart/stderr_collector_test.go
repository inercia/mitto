package procstart

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestStderrCollector_IgnorePatternsSuppressDebugLog verifies that ignore
// regexes suppress the debug-level "agent stderr" log but do NOT affect the
// captured buffer (still available for error reporting).
func TestStderrCollector_IgnorePatternsSuppressDebugLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	collector := NewStderrCollector(8192, logger)
	perAgent := CompileStderrPatterns(StderrPatternsSpec{
		Ignore: []string{`(?i)deprecationwarning`},
	}, nil)
	collector.SetIgnorePatterns(perAgent.Ignore)

	// Suppressed line.
	_, _ = collector.Write([]byte("(node:1234) DeprecationWarning: something\n"))
	// Non-suppressed line.
	_, _ = collector.Write([]byte("some other output\n"))

	logs := buf.String()
	if strings.Contains(logs, "DeprecationWarning") {
		t.Errorf("expected DeprecationWarning to be suppressed from debug log; got: %s", logs)
	}
	if !strings.Contains(logs, "some other output") {
		t.Errorf("expected non-matching write to still be logged; got: %s", logs)
	}
	// Buffer capture is unaffected — both writes present.
	captured := collector.GetOutput()
	if !strings.Contains(captured, "DeprecationWarning") || !strings.Contains(captured, "some other output") {
		t.Errorf("expected buffer to still capture both writes; got: %q", captured)
	}
}

// TestStderrCollector_CloseSuppressesSubsequentWrites verifies that after
// Close(), further Write() calls are no-ops: the return value still reports
// full-length consumed (io.Writer contract) but neither the buffer nor the
// debug log is mutated. This locks down the newly-exported Close() surface
// (mitto-iuw2) so a future change cannot silently regress the drop-on-close
// semantics.
func TestStderrCollector_CloseSuppressesSubsequentWrites(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	collector := NewStderrCollector(8192, logger)

	if _, err := collector.Write([]byte("before-close\n")); err != nil {
		t.Fatalf("pre-close write failed: %v", err)
	}
	collector.Close()

	preLen := len(collector.GetOutput())
	preLogLen := buf.Len()

	// Post-close write must report the full length consumed but not append
	// to the buffer or emit a debug log line.
	payload := []byte("after-close-should-be-dropped\n")
	n, err := collector.Write(payload)
	if err != nil {
		t.Fatalf("post-close write returned error: %v", err)
	}
	if n != len(payload) {
		t.Errorf("post-close write returned n=%d, want %d (io.Writer contract)", n, len(payload))
	}

	got := collector.GetOutput()
	if len(got) != preLen {
		t.Errorf("post-close write mutated buffer: pre-len=%d post-len=%d content=%q", preLen, len(got), got)
	}
	if strings.Contains(got, "after-close") {
		t.Errorf("post-close payload leaked into buffer: %q", got)
	}
	if buf.Len() != preLogLen {
		t.Errorf("post-close write emitted debug log; pre-len=%d post-len=%d", preLogLen, buf.Len())
	}
}

// TestStartStderrMonitor_DegradedPatternFiresOnDegradedCallback verifies the
// mitto-k6h increment-2 wiring: a per-agent Degraded regex fires onDegraded
// (feeding the shared-process saturation signal) and does NOT trigger the
// crash callback. Degraded is a saturation contributor, not a crash source.
func TestStartStderrMonitor_DegradedPatternFiresOnDegradedCallback(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	crashDetected := make(chan struct{}, 1)
	onCrashDetected := func() {
		select {
		case crashDetected <- struct{}{}:
		default:
		}
	}

	degradedDetected := make(chan struct{}, 1)
	onDegraded := func() {
		select {
		case degradedDetected <- struct{}{}:
		default:
		}
	}

	perAgent := CompileStderrPatterns(StderrPatternsSpec{
		Degraded: []string{`(?i)rate limit`},
	}, nil)
	if perAgent == nil {
		t.Fatal("expected non-nil compiled patterns")
	}

	StartStderrMonitor(pr, collector, onCrashDetected, nil, nil, nil, onDegraded, perAgent)

	go func() {
		_, _ = pw.Write([]byte("hit rate limit, backing off\n"))
		_ = pw.Close()
	}()

	select {
	case <-degradedDetected:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("expected onDegraded to be invoked for per-agent Degraded pattern")
	}

	// Crash must NOT fire for a Degraded-only match.
	select {
	case <-crashDetected:
		t.Fatal("Degraded patterns must not trigger crash detection")
	case <-time.After(100 * time.Millisecond):
		// expected: crash callback stays quiet
	}
}
