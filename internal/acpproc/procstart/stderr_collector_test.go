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
