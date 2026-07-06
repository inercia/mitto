package conversation

import (
	"io"
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

	StartStderrMonitor(pr, collector, onCrashDetected, nil)

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

	StartStderrMonitor(pr, collector, onCrashDetected, nil)

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
