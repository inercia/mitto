package conversation

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestCompileStderrPatterns_EmptySpecReturnsNil verifies the compile helper
// returns nil for an empty spec so hot paths can cheaply short-circuit
// (mitto-k6h).
func TestCompileStderrPatterns_EmptySpecReturnsNil(t *testing.T) {
	if got := CompileStderrPatterns(StderrPatternsSpec{}, nil); got != nil {
		t.Fatalf("expected nil for empty spec, got %+v", got)
	}
}

// TestCompileStderrPatterns_InvalidPatternSkippedWithWarn verifies that an
// invalid regex is dropped (never fatal) and a warn line is emitted.
func TestCompileStderrPatterns_InvalidPatternSkippedWithWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	spec := StderrPatternsSpec{
		Crash: []string{
			`valid pattern`,
			`(broken`, // unclosed group
			`another valid`,
		},
	}
	got := CompileStderrPatterns(spec, logger)
	if got == nil {
		t.Fatal("expected non-nil compiled patterns")
	}
	if len(got.Crash) != 2 {
		t.Fatalf("expected 2 valid crash patterns, got %d", len(got.Crash))
	}
	if !strings.Contains(buf.String(), "skipping invalid stderr pattern") {
		t.Fatalf("expected warn log for invalid pattern, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "class=crash") {
		t.Fatalf("expected warn log to include class=crash, got: %s", buf.String())
	}
}

// TestCompileStderrPatterns_AllClassesCompiled verifies each action class is
// separately populated so downstream wiring can pick them up independently.
func TestCompileStderrPatterns_AllClassesCompiled(t *testing.T) {
	spec := StderrPatternsSpec{
		Crash:    []string{`crash-pat`},
		Ignore:   []string{`ignore-pat`},
		Degraded: []string{`degraded-pat`},
	}
	got := CompileStderrPatterns(spec, nil)
	if got == nil {
		t.Fatal("expected non-nil compiled patterns")
	}
	if len(got.Crash) != 1 || len(got.Ignore) != 1 || len(got.Degraded) != 1 {
		t.Fatalf("expected 1 pattern per class, got crash=%d ignore=%d degraded=%d",
			len(got.Crash), len(got.Ignore), len(got.Degraded))
	}
	// Sanity-check the Degraded regex is compiled and matches — even though its
	// behavioural wiring is deferred, callers can inspect it end-to-end.
	if !got.Degraded[0].MatchString("agent hit degraded-pat threshold") {
		t.Fatal("degraded regex compiled but does not match expected input")
	}
}

// TestStartStderrMonitor_PerAgentCrashPatternTriggersCallback verifies a
// per-agent Crash regex fires onCrashDetected even when the chunk does NOT
// match the hardcoded baseline (mitto-k6h).
func TestStartStderrMonitor_PerAgentCrashPatternTriggersCallback(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	crashDetected := make(chan struct{}, 1)
	onCrashDetected := func() {
		select {
		case crashDetected <- struct{}{}:
		default:
		}
	}

	perAgent := CompileStderrPatterns(StderrPatternsSpec{
		Crash: []string{`(?i)per-agent-only fatal`},
	}, nil)
	if perAgent == nil {
		t.Fatal("expected non-nil compiled patterns")
	}

	StartStderrMonitor(pr, collector, onCrashDetected, nil, nil, nil, nil, perAgent)

	go func() {
		_, _ = pw.Write([]byte("Per-Agent-Only Fatal encountered\n"))
		_ = pw.Close()
	}()

	select {
	case <-crashDetected:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("expected onCrashDetected for per-agent crash pattern")
	}
}

// TestStartStderrMonitor_BaselineStillFiresWithPerAgent verifies that when
// per-agent patterns are supplied, the hardcoded baseline is NOT replaced.
func TestStartStderrMonitor_BaselineStillFiresWithPerAgent(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	crashDetected := make(chan struct{}, 1)
	onCrashDetected := func() {
		select {
		case crashDetected <- struct{}{}:
		default:
		}
	}

	// Per-agent patterns that WILL NOT match — the baseline "broken pipe"
	// must still fire the callback.
	perAgent := CompileStderrPatterns(StderrPatternsSpec{
		Crash: []string{`per-agent-only-never-matches-baseline-input`},
	}, nil)

	StartStderrMonitor(pr, collector, onCrashDetected, nil, nil, nil, nil, perAgent)

	go func() {
		_, _ = pw.Write([]byte("error: broken pipe on write\n"))
		_ = pw.Close()
	}()

	select {
	case <-crashDetected:
		// expected: baseline fired
	case <-time.After(2 * time.Second):
		t.Fatal("expected baseline crash pattern to still fire alongside per-agent patterns")
	}
}

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
