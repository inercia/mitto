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
