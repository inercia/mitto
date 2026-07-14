package conversation

import (
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// TestStartStderrMonitor_HeapOOM_TriggersCrashDetection is a regression test for
// mitto-5q8: when the agent subprocess (Node/V8) dies with a JS heap-OOM fatal
// error, StartStderrMonitor must recognize the pattern and invoke onCrashDetected
// immediately, rather than waiting for the SDK's control-request timeout.
func TestStartStderrMonitor_HeapOOM_TriggersCrashDetection(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	crashDetected := make(chan struct{}, 1)
	onCrashDetected := func() {
		select {
		case crashDetected <- struct{}{}:
		default:
		}
	}

	StartStderrMonitor(pr, collector, onCrashDetected, nil, nil, nil, nil, nil)

	chunk := "FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory"
	go func() {
		_, _ = pw.Write([]byte(chunk))
		_ = pw.Close()
	}()

	select {
	case <-crashDetected:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("expected onCrashDetected to be invoked for heap-OOM stderr output")
	}
}

// TestStartStderrMonitor_MCPInitProgress_TriggersCallback is a regression test for
// mitto-8ul.1 / mitto-29q: when the agent writes a "Waiting for N MCP server(s) to
// initialize" line the stderr monitor must invoke onMCPInitProgress on each matching
// chunk. Dedup is now the callback's responsibility (edge-detected CompareAndSwap on
// mcpInitInProgress) so a per-session re-handshake after the first success re-fires
// the callback and re-grants the extended MCP-init budget.
func TestStartStderrMonitor_MCPInitProgress_TriggersCallback(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	var progressCalls atomic.Int32
	onProgress := func() { progressCalls.Add(1) }
	onTimeout := func() { t.Fatal("MCP timeout should not fire on progress lines") }

	StartStderrMonitor(pr, collector, nil, nil, onProgress, onTimeout, nil, nil)

	go func() {
		// Two matching lines: the monitor must invoke the callback for each.
		_, _ = pw.Write([]byte("Waiting for 3 MCP servers to initialize\n"))
		_, _ = pw.Write([]byte("Waiting for 3 MCP servers to initialize (still)\n"))
		_ = pw.Close()
	}()

	// Give the goroutine a moment to consume the stream.
	time.Sleep(200 * time.Millisecond)
	if got := progressCalls.Load(); got < 1 {
		t.Fatalf("expected onMCPInitProgress to be called at least once, got %d", got)
	}
}

// TestStartStderrMonitor_MCPInitTimeout_TriggersCallback is a regression test for
// mitto-8ul.1: the "MCP initialization timed out" signal must invoke onMCPInitTimeout
// (at most once) so the pending session/new can be aborted promptly.
func TestStartStderrMonitor_MCPInitTimeout_TriggersCallback(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	var timeoutCalls atomic.Int32
	onTimeout := func() { timeoutCalls.Add(1) }

	StartStderrMonitor(pr, collector, nil, nil, nil, onTimeout, nil, nil)

	go func() {
		_, _ = pw.Write([]byte("MCP initialization timed out after 225s\n"))
		_, _ = pw.Write([]byte("MCP initialization timed out again\n"))
		_ = pw.Close()
	}()

	time.Sleep(200 * time.Millisecond)
	if got := timeoutCalls.Load(); got != 1 {
		t.Fatalf("expected onMCPInitTimeout to be called exactly once, got %d", got)
	}
}

// TestStartStderrMonitor_MCPPatternsDoNotTriggerCrash guards against future regex
// churn accidentally overlapping MCP-init phrasing with crash detection patterns.
func TestStartStderrMonitor_MCPPatternsDoNotTriggerCrash(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	crashDetected := make(chan struct{}, 1)
	onCrashDetected := func() { crashDetected <- struct{}{} }

	StartStderrMonitor(pr, collector, onCrashDetected, nil, nil, nil, nil, nil)

	go func() {
		_, _ = pw.Write([]byte("Waiting for 3 MCP servers to initialize\n"))
		_, _ = pw.Write([]byte("MCP initialization timed out after 225s\n"))
		_ = pw.Close()
	}()

	select {
	case <-crashDetected:
		t.Fatal("MCP-init lines must not trigger crash detection")
	case <-time.After(300 * time.Millisecond):
		// expected: no crash signal
	}
}

// TestStartStderrMonitor_NormalOutput_DoesNotTriggerCrashDetection is a sanity
// check that ordinary stderr output does not falsely trigger crash detection.
func TestStartStderrMonitor_NormalOutput_DoesNotTriggerCrashDetection(t *testing.T) {
	pr, pw := io.Pipe()
	collector := NewStderrCollector(8192, nil)

	crashDetected := make(chan struct{}, 1)
	onCrashDetected := func() {
		select {
		case crashDetected <- struct{}{}:
		default:
		}
	}

	StartStderrMonitor(pr, collector, onCrashDetected, nil, nil, nil, nil, nil)

	go func() {
		_, _ = pw.Write([]byte("some normal debug output\n"))
		_ = pw.Close()
	}()

	select {
	case <-crashDetected:
		t.Fatal("did not expect onCrashDetected for normal stderr output")
	case <-time.After(300 * time.Millisecond):
		// expected: no crash signal
	}
}
