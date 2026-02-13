package runner

import (
	"context"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// isFirejailAvailable checks if firejail is installed and available in PATH.
func isFirejailAvailable() bool {
	_, err := exec.LookPath("firejail")
	return err == nil
}

// TestRunnerWithPipes_ExecRunner tests the exec runner with RunWithPipes.
func TestRunnerWithPipes_ExecRunner(t *testing.T) {
	// Create an exec runner (no restrictions)
	r, err := NewRunner(nil, nil, nil, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	if r.Type() != "exec" {
		t.Errorf("Expected runner type 'exec', got '%s'", r.Type())
	}

	if r.IsRestricted() {
		t.Error("Exec runner should not be restricted")
	}

	// Test RunWithPipes with a simple echo command
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "cat", nil, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}

	// Write to stdin
	testInput := "Hello from restricted runner!\n"
	if _, err := io.WriteString(stdin, testInput); err != nil {
		t.Fatalf("Failed to write to stdin: %v", err)
	}
	stdin.Close()

	// Read from stdout
	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("Failed to read from stdout: %v", err)
	}

	// Read from stderr (should be empty)
	stderrOutput, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatalf("Failed to read from stderr: %v", err)
	}

	// Wait for process to complete
	if err := wait(); err != nil {
		t.Fatalf("wait() failed: %v", err)
	}

	// Verify output
	if string(output) != testInput {
		t.Errorf("Expected output '%s', got '%s'", testInput, string(output))
	}

	if len(stderrOutput) > 0 {
		t.Errorf("Expected empty stderr, got: %s", string(stderrOutput))
	}
}

// TestRunnerWithPipes_WithRestrictions tests creating a runner with restrictions.
func TestRunnerWithPipes_WithRestrictions(t *testing.T) {
	// Create a runner with no restrictions (exec runner)
	r, err := NewRunner(nil, nil, nil, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	// For exec runner, restrictions are not enforced (it's just direct execution)
	// But the runner should still be created successfully
	if r.Type() != "exec" {
		t.Errorf("Expected runner type 'exec', got '%s'", r.Type())
	}

	// Test that RunWithPipes still works
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdin, stdout, _, wait, err := r.RunWithPipes(ctx, "echo", []string{"test"}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}

	stdin.Close()

	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("Failed to read from stdout: %v", err)
	}

	if err := wait(); err != nil {
		t.Fatalf("wait() failed: %v", err)
	}

	if !strings.Contains(string(output), "test") {
		t.Errorf("Expected output to contain 'test', got '%s'", string(output))
	}
}

// TestRunnerWithPipes_ContextCancellation tests that context cancellation kills the process.
func TestRunnerWithPipes_ContextCancellation(t *testing.T) {
	r, err := NewRunner(nil, nil, nil, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Start a long-running process
	stdin, _, _, wait, err := r.RunWithPipes(ctx, "sleep", []string{"60"}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}
	stdin.Close()

	// Cancel the context immediately
	cancel()

	// Wait should return an error (process was killed)
	err = wait()
	if err == nil {
		t.Error("Expected wait() to return an error after context cancellation")
	}
}

// TestRunnerFallback_PlatformDetection tests that runners fallback correctly on unsupported platforms
func TestRunnerFallback_PlatformDetection(t *testing.T) {
	tests := []struct {
		name           string
		runnerType     string
		shouldFallback bool
		expectedType   string
	}{
		{
			name:           "exec always works",
			runnerType:     "exec",
			shouldFallback: false,
			expectedType:   "exec",
		},
		{
			name:           "sandbox-exec on macOS",
			runnerType:     "sandbox-exec",
			shouldFallback: runtime.GOOS != "darwin",
			expectedType: func() string {
				if runtime.GOOS == "darwin" {
					return "sandbox-exec"
				}
				return "exec"
			}(),
		},
		{
			name:       "firejail on Linux",
			runnerType: "firejail",
			// Firejail should fallback if not on Linux OR if firejail is not installed
			shouldFallback: runtime.GOOS != "linux" || !isFirejailAvailable(),
			expectedType: func() string {
				if runtime.GOOS == "linux" && isFirejailAvailable() {
					return "firejail"
				}
				return "exec"
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowNetworking := true
			// Start with exec config that specifies the desired runner type
			runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
				"exec": {
					Type: tt.runnerType,
					Restrictions: &config.RunnerRestrictions{
						AllowNetworking: &allowNetworking,
					},
				},
			}

			r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
			if err != nil {
				t.Fatalf("NewRunner failed: %v", err)
			}

			if r == nil {
				t.Fatal("NewRunner returned nil runner")
			}

			actualType := r.Type()
			if actualType != tt.expectedType {
				t.Errorf("Expected runner type %q, got %q", tt.expectedType, actualType)
			}

			// Check fallback info
			if tt.shouldFallback {
				if r.FallbackInfo == nil {
					t.Error("Expected fallback info but got nil")
				} else {
					if r.FallbackInfo.RequestedType != tt.runnerType {
						t.Errorf("Expected requested type %q, got %q", tt.runnerType, r.FallbackInfo.RequestedType)
					}
					if r.FallbackInfo.FallbackType != "exec" {
						t.Errorf("Expected fallback type 'exec', got %q", r.FallbackInfo.FallbackType)
					}
					if r.FallbackInfo.Reason == "" {
						t.Error("Expected fallback reason but got empty string")
					}
					t.Logf("Fallback reason: %s", r.FallbackInfo.Reason)
				}
			} else {
				if r.FallbackInfo != nil {
					t.Errorf("Expected no fallback info but got: %+v", r.FallbackInfo)
				}
			}
		})
	}
}

// TestRunnerFallback_IsRestricted tests that fallback runners report correct restriction status
func TestRunnerFallback_IsRestricted(t *testing.T) {
	allowNetworking := true

	// Test that exec runner (fallback) is not restricted
	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "exec",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking: &allowNetworking,
			},
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	if r.IsRestricted() {
		t.Error("exec runner should not be restricted")
	}

	// Test that unsupported runner falls back to exec (not restricted)
	unsupportedType := "firejail"
	if runtime.GOOS == "linux" {
		unsupportedType = "sandbox-exec"
	}

	runnerConfigs = map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: unsupportedType,
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking: &allowNetworking,
			},
		},
	}

	r, err = NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	if r.Type() == "exec" {
		// Fallback occurred
		if r.IsRestricted() {
			t.Error("Fallback exec runner should not be restricted")
		}
		if r.FallbackInfo == nil {
			t.Error("Expected fallback info for unsupported runner")
		}
	}
}
