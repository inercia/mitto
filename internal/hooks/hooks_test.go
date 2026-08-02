package hooks

import (
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

func TestProcess_Stop(t *testing.T) {
	// Test that Stop() handles nil Process gracefully
	var hp *Process
	hp.Stop() // Should not panic
}

func TestProcess_StopAlreadyDone(t *testing.T) {
	// Test that Stop() handles already-done process
	hp := &Process{
		name: "test",
		done: true,
	}
	hp.Stop() // Should not panic or do anything
}

func TestProcess_StopRunningProcess(t *testing.T) {
	// Create a long-running command that we can stop
	// Must set Setpgid to match how StartUp creates commands
	cmd := exec.Command("sleep", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start test command: %v", err)
	}

	hp := &Process{
		name: "test-sleep",
		cmd:  cmd,
	}

	// Stop should terminate the process
	hp.Stop()

	// Verify the process was stopped
	if !hp.done {
		t.Error("Process.done should be true after Stop()")
	}

	// Wait for the process to actually exit
	_ = cmd.Wait()
}

func TestStartUp_PortReplacement(t *testing.T) {
	// Test that ${PORT} is replaced correctly
	// We use echo to verify the replacement
	testPort := 12345
	command := "echo PORT=${PORT}"

	// Capture the command that would be executed
	replaced := strings.ReplaceAll(command, "${PORT}", "12345")
	if replaced != "echo PORT=12345" {
		t.Errorf("Port replacement failed: got %q, want %q", replaced, "echo PORT=12345")
	}

	// Test with actual hook (quick command that exits immediately)
	hook := config.WebHook{Command: "exit 0", Name: "test-hook"}
	hp := StartUp(hook, testPort)
	if hp == nil {
		t.Fatal("StartUp returned nil for valid command")
	}

	// Wait for the hook to complete
	time.Sleep(100 * time.Millisecond)

	hp.mu.Lock()
	done := hp.done
	hp.mu.Unlock()

	if !done {
		t.Error("Hook should have completed (exit 0)")
	}
}

func TestStartUp_ExitWithError(t *testing.T) {
	// Test that hooks that exit with error are handled
	hook := config.WebHook{Command: "exit 1", Name: "test-error-hook"}
	hp := StartUp(hook, 8080)
	if hp == nil {
		t.Fatal("StartUp returned nil for valid command")
	}

	// Wait for the hook to complete
	time.Sleep(100 * time.Millisecond)

	hp.mu.Lock()
	done := hp.done
	hp.mu.Unlock()

	if !done {
		t.Error("Hook should have completed (exit 1)")
	}
}

func TestStartUp_WithOnFailure(t *testing.T) {
	// Test that the onFailure callback is called when the hook fails
	var failureCalled bool
	var receivedFailure HookFailure

	hook := config.WebHook{Command: "exit 42", Name: "test-callback-hook"}
	onFailure := WithOnFailure(func(failure HookFailure) {
		failureCalled = true
		receivedFailure = failure
	})
	hp := StartUp(hook, 8080, onFailure)
	if hp == nil {
		t.Fatal("StartUp returned nil for valid command")
	}

	// Wait for the hook to complete
	time.Sleep(150 * time.Millisecond)

	if !failureCalled {
		t.Error("onFailure callback was not called")
	}

	if receivedFailure.Name != "test-callback-hook" {
		t.Errorf("Expected hook name 'test-callback-hook', got '%s'", receivedFailure.Name)
	}

	if receivedFailure.ExitCode != 42 {
		t.Errorf("Expected exit code 42, got %d", receivedFailure.ExitCode)
	}

	if receivedFailure.Error == "" {
		t.Error("Expected error message, got empty string")
	}
}

func TestStartUp_InvalidCommand(t *testing.T) {
	// Test with a command that fails to start
	// Note: "sh -c" will still start, but the command inside may fail
	hook := config.WebHook{Command: "nonexistent-command-12345", Name: "test-invalid"}
	hp := StartUp(hook, 8080)
	if hp == nil {
		// This is acceptable - the command may fail to start
		return
	}

	// Wait for the hook to complete
	time.Sleep(200 * time.Millisecond)

	hp.mu.Lock()
	done := hp.done
	hp.mu.Unlock()

	if !done {
		t.Error("Hook should have completed (command not found)")
	}
}

func TestStartUp_EmptyCommand(t *testing.T) {
	// Test that empty command returns nil
	hook := config.WebHook{Command: "", Name: "test-empty"}
	hp := StartUp(hook, 8080)
	if hp != nil {
		t.Error("StartUp should return nil for empty command")
	}
}

func TestProcess_ConcurrentStop(t *testing.T) {
	// Test that concurrent Stop() calls are safe
	cmd := exec.Command("sleep", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start test command: %v", err)
	}

	hp := &Process{
		name: "test-concurrent",
		cmd:  cmd,
	}

	// Call Stop() concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hp.Stop()
		}()
	}
	wg.Wait()

	// Verify the process was stopped
	if !hp.done {
		t.Error("Process.done should be true after concurrent Stop() calls")
	}

	// Wait for the process to actually exit
	_ = cmd.Wait()
}

func TestRunDown_Success(t *testing.T) {
	// Capture stdout to verify hook runs
	// We can't easily capture output, but we can verify it doesn't panic
	hook := config.WebHook{Command: "exit 0", Name: "test-down-hook"}
	RunDown(hook, 8080)
}

func TestRunDown_Error(t *testing.T) {
	// Verify error handling doesn't panic
	hook := config.WebHook{Command: "exit 1", Name: "test-down-error"}
	RunDown(hook, 8080)
}

func TestRunDown_EmptyCommand(t *testing.T) {
	// Test that empty command does nothing (doesn't panic)
	hook := config.WebHook{Command: "", Name: "test-empty"}
	RunDown(hook, 8080)
}

func TestRunDown_Timeout(t *testing.T) {
	// Override downHookTimeout to a very short value so the test doesn't take 30 s.
	orig := downHookTimeout
	downHookTimeout = 200 * time.Millisecond
	defer func() { downHookTimeout = orig }()

	hook := config.WebHook{Command: "sleep 5", Name: "test-timeout"}

	start := time.Now()
	RunDown(hook, 8080) // should return once the 200 ms timeout fires
	elapsed := time.Since(start)

	// Should complete well within 1 s (timeout is 200 ms + process cleanup overhead).
	if elapsed > 1*time.Second {
		t.Errorf("RunDown with timed-out command took too long: %v (want < 1s)", elapsed)
	}
}

func TestRunDown_SignalTerminated(t *testing.T) {
	// A command killed by context timeout manifests as exit_code == -1 (signal kill).
	// Verify RunDown handles this quietly (no panic, no error return).
	orig := downHookTimeout
	downHookTimeout = 100 * time.Millisecond
	defer func() { downHookTimeout = orig }()

	hook := config.WebHook{Command: "sleep 10", Name: "test-signal-term"}
	// Should not panic — signal termination via context is expected during restarts.
	RunDown(hook, 8080)
}

// TestJitter verifies that the jitter helper keeps the result within ±20% of the base
// duration across a large sample, as documented.
func TestJitter(t *testing.T) {
	base := 100 * time.Millisecond
	min := time.Duration(float64(base) * 0.8)
	max := time.Duration(float64(base) * 1.2)

	for i := 0; i < 200; i++ {
		result := jitter(base)
		if result < min || result > max {
			t.Errorf("jitter(%v) = %v, want in [%v, %v]", base, result, min, max)
		}
	}
}

// resetPackageThrottle clears the shared process-wide throttle between tests
// so timing/counter state from earlier tests does not leak into these ones.
func resetPackageThrottle() {
	packageThrottle = newHookFailureThrottle()
}

// TestStartUp_TransientFlagOnFailure verifies that when the hook emits output
// matching a known transient pattern (cloudflared loopback DNS refusal), the
// callback receives HookFailure.Transient=true so the broadcast pipeline can
// downgrade the toast (mitto-y6i AC#1).
func TestStartUp_TransientFlagOnFailure(t *testing.T) {
	resetPackageThrottle()
	// Ensure the throttle does NOT suppress this single failure so onFailure
	// definitely fires (default threshold=2 → first fire is suppressed).
	origT := hookFailureThreshold
	hookFailureThreshold = 0
	defer func() { hookFailureThreshold = origT }()

	// The hook prints a transient DNS refusal line to stderr then exits non-zero.
	transientLine := `lookup cfd-features.cloudflare.com on 127.0.0.53:53: read udp 127.0.0.1:44567->127.0.0.53:53: connection refused`
	cmd := "echo '" + transientLine + "' 1>&2; exit 1"
	hook := config.WebHook{Command: cmd, Name: "cf-tunnel-up"}

	var mu sync.Mutex
	var got HookFailure
	var called bool
	hp := StartUp(hook, 8080, WithOnFailure(func(f HookFailure) {
		mu.Lock()
		defer mu.Unlock()
		called = true
		got = f
	}))
	if hp == nil {
		t.Fatal("StartUp returned nil for valid transient-error command")
	}
	// Wait for the goroutine to run cmd.Wait() and invoke the callback.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Fatal("onFailure was not invoked for transient-output failure")
	}
	if !got.Transient {
		t.Errorf("HookFailure.Transient=false, want true (classifier missed transient DNS pattern in %q)", got.Output)
	}
	if got.ExitCode != 1 {
		t.Errorf("ExitCode=%d, want 1", got.ExitCode)
	}
}

// TestStartUp_TransientThrottled verifies the sliding-window throttle: with
// the default threshold, the first N transient failures per hook name are
// suppressed (callback NOT invoked) — the (N+1)-th broadcasts (mitto-y6i AC#2).
func TestStartUp_TransientThrottled(t *testing.T) {
	resetPackageThrottle()
	origW, origT := hookFailureWindow, hookFailureThreshold
	hookFailureWindow = 5 * time.Minute
	hookFailureThreshold = 2
	defer func() { hookFailureWindow, hookFailureThreshold = origW, origT }()

	transientLine := `lookup x.example on 127.0.0.53:53: read udp 127.0.0.1:1->127.0.0.53:53: connection refused`
	cmd := "echo '" + transientLine + "' 1>&2; exit 1"
	hook := config.WebHook{Command: cmd, Name: "cf-tunnel-throttled"}

	var mu sync.Mutex
	callCount := 0
	onFailure := WithOnFailure(func(f HookFailure) {
		mu.Lock()
		defer mu.Unlock()
		if !f.Transient {
			t.Errorf("expected Transient=true on any invocation, got false")
		}
		callCount++
	})

	// Fire the transient-output hook 3 times sequentially and wait for each to complete.
	for i := 0; i < 3; i++ {
		hp := StartUp(hook, 8080, onFailure)
		if hp == nil {
			t.Fatalf("iteration %d: StartUp returned nil", i)
		}
		// Give the goroutine time to Wait() and run the failure branch.
		time.Sleep(250 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	// With threshold=2, the first 2 transient failures are suppressed; the 3rd
	// broadcasts. Callback should be invoked exactly once.
	if callCount != 1 {
		t.Errorf("callback invoked %d times, want exactly 1 (threshold=2, 3 fires → first 2 suppressed)", callCount)
	}
}

// TestRunDownWithOptions_TransientCallback verifies that RunDownWithOptions
// classifies output and passes Transient=true through to the callback, and
// that the shared throttle path applies (mitto-y6i AC#2, AC#3).
func TestRunDownWithOptions_TransientCallback(t *testing.T) {
	resetPackageThrottle()
	// Disable throttling so the single failure definitely surfaces.
	origT := hookFailureThreshold
	hookFailureThreshold = 0
	defer func() { hookFailureThreshold = origT }()

	transientLine := `the DNS query failed error=lookup features.argotunnel.com on 127.0.0.53:53: server misbehaving`
	cmd := "echo '" + transientLine + "' 1>&2; exit 1"
	hook := config.WebHook{Command: cmd, Name: "cf-tunnel-down"}

	var got HookFailure
	var called bool
	RunDownWithOptions(hook, 8080, func(f HookFailure) {
		called = true
		got = f
	})

	if !called {
		t.Fatal("RunDownWithOptions did not invoke onFailure for transient-output failure")
	}
	if !got.Transient {
		t.Errorf("HookFailure.Transient=false, want true (classifier missed pattern in %q)", got.Output)
	}
	if got.Name != "cf-tunnel-down" {
		t.Errorf("HookFailure.Name=%q, want cf-tunnel-down", got.Name)
	}
	if got.ExitCode != 1 {
		t.Errorf("HookFailure.ExitCode=%d, want 1", got.ExitCode)
	}
}

// TestRunDownWithOptions_RealErrorNotThrottled verifies that a real
// (non-transient) failure always surfaces the callback even if the throttle
// would otherwise suppress it.
func TestRunDownWithOptions_RealErrorNotThrottled(t *testing.T) {
	resetPackageThrottle()
	// Even with a very aggressive throttle, real failures must broadcast.
	origT := hookFailureThreshold
	hookFailureThreshold = 100
	defer func() { hookFailureThreshold = origT }()

	hook := config.WebHook{Command: "echo 'boom: fatal error' 1>&2; exit 2", Name: "real-down-fail"}

	var got HookFailure
	var called bool
	RunDownWithOptions(hook, 8080, func(f HookFailure) {
		called = true
		got = f
	})

	if !called {
		t.Fatal("real (non-transient) down-hook failure must not be throttled")
	}
	if got.Transient {
		t.Errorf("real failure classified transient (regex over-match?): output=%q", got.Output)
	}
	if got.ExitCode != 2 {
		t.Errorf("ExitCode=%d, want 2", got.ExitCode)
	}
}

// TestRunDownWithOptions_NilCallback verifies backward compatibility: passing
// a nil callback (as RunDown does) must not panic.
func TestRunDownWithOptions_NilCallback(t *testing.T) {
	resetPackageThrottle()
	hook := config.WebHook{Command: "exit 1", Name: "no-cb"}
	// Must not panic when onFailure is nil.
	RunDownWithOptions(hook, 8080, nil)
}

// TestRunDownWithOptions_SuccessNoCallback verifies that a successful (exit 0)
// down hook never invokes the failure callback.
func TestRunDownWithOptions_SuccessNoCallback(t *testing.T) {
	resetPackageThrottle()
	hook := config.WebHook{Command: "exit 0", Name: "ok"}
	called := false
	RunDownWithOptions(hook, 8080, func(HookFailure) { called = true })
	if called {
		t.Error("onFailure invoked for successful down hook (exit 0)")
	}
}

// TestRunDownWithOptions_TimeoutNoCallback verifies that a timed-out down hook
// does NOT invoke the failure callback (mirrors StartUp's signal-kill behavior).
func TestRunDownWithOptions_TimeoutNoCallback(t *testing.T) {
	resetPackageThrottle()
	orig := downHookTimeout
	downHookTimeout = 100 * time.Millisecond
	defer func() { downHookTimeout = orig }()

	hook := config.WebHook{Command: "sleep 5", Name: "timeout-nocb"}
	called := false
	RunDownWithOptions(hook, 8080, func(HookFailure) { called = true })
	if called {
		t.Error("onFailure invoked for timed-out down hook (should be silent on timeout)")
	}
}
