//go:build integration

package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

// TestSandboxExec_Integration tests sandbox-exec runner (macOS only).
func TestSandboxExec_Integration(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is only available on macOS")
	}

	// Check if sandbox-exec is available
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not found in PATH")
	}

	// Create a sandbox-exec runner with restrictions using per-runner-type config
	allowNetworking := false
	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "sandbox-exec",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking:   &allowNetworking,
				AllowReadFolders:  []string{"/tmp"},
				AllowWriteFolders: []string{"/tmp"},
			},
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	if r.Type() != "sandbox-exec" {
		t.Errorf("Expected runner type 'sandbox-exec', got '%s'", r.Type())
	}

	if !r.IsRestricted() {
		t.Error("sandbox-exec runner should be restricted")
	}

	// Test RunWithPipes with a simple command
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "echo", []string{"hello from sandbox"}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}

	stdin.Close()

	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("Failed to read from stdout: %v", err)
	}

	stderrOutput, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatalf("Failed to read from stderr: %v", err)
	}

	if err := wait(); err != nil {
		t.Fatalf("wait() failed: %v", err)
	}

	if !strings.Contains(string(output), "hello from sandbox") {
		t.Errorf("Expected output to contain 'hello from sandbox', got '%s'", string(output))
	}

	if len(stderrOutput) > 0 {
		t.Logf("stderr output: %s", string(stderrOutput))
	}
}

// TestFirejail_Integration tests firejail runner (Linux only).
func TestFirejail_Integration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("firejail is only available on Linux")
	}

	// Check if firejail is available
	if _, err := exec.LookPath("firejail"); err != nil {
		t.Skip("firejail not found in PATH")
	}

	// Create a firejail runner with restrictions using per-runner-type config
	allowNetworking := false
	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "firejail",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking:   &allowNetworking,
				AllowReadFolders:  []string{"/tmp"},
				AllowWriteFolders: []string{"/tmp"},
			},
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	if r.Type() != "firejail" {
		t.Errorf("Expected runner type 'firejail', got '%s'", r.Type())
	}

	if !r.IsRestricted() {
		t.Error("firejail runner should be restricted")
	}

	// Test RunWithPipes with a simple command
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "echo", []string{"hello from firejail"}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}

	stdin.Close()

	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("Failed to read from stdout: %v", err)
	}

	stderrOutput, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatalf("Failed to read from stderr: %v", err)
	}

	if err := wait(); err != nil {
		t.Fatalf("wait() failed: %v", err)
	}

	if !strings.Contains(string(output), "hello from firejail") {
		t.Errorf("Expected output to contain 'hello from firejail', got '%s'", string(output))
	}

	if len(stderrOutput) > 0 {
		t.Logf("stderr output: %s", string(stderrOutput))
	}
}

// TestDocker_Integration tests docker runner.
func TestDocker_Integration(t *testing.T) {
	// Check if docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found in PATH")
	}

	// Check if docker daemon is running
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		t.Skip("docker daemon not running")
	}

	// Create a docker runner with restrictions using per-runner-type config
	allowNetworking := false
	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "docker",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking: &allowNetworking,
				Docker: &config.DockerRestrictions{
					Image: "alpine:latest",
				},
			},
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}

	if r.Type() != "docker" {
		t.Errorf("Expected runner type 'docker', got '%s'", r.Type())
	}

	if !r.IsRestricted() {
		t.Error("docker runner should be restricted")
	}

	// Test RunWithPipes with a simple command
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "echo", []string{"hello from docker"}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}

	stdin.Close()

	output, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("Failed to read from stdout: %v", err)
	}

	stderrOutput, err := io.ReadAll(stderr)
	if err != nil {
		t.Fatalf("Failed to read from stderr: %v", err)
	}

	if err := wait(); err != nil {
		t.Fatalf("wait() failed: %v", err)
	}

	if !strings.Contains(string(output), "hello from docker") {
		t.Errorf("Expected output to contain 'hello from docker', got '%s'", string(output))
	}

	if len(stderrOutput) > 0 {
		t.Logf("stderr output: %s", string(stderrOutput))
	}
}

// TestNetworkRestriction_Integration tests that network restrictions are enforced.
func TestNetworkRestriction_Integration(t *testing.T) {
	if runtime.GOOS == "darwin" {
		// Test with sandbox-exec on macOS
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found in PATH")
		}

		allowNetworking := false
		runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
			"exec": {
				Type: "sandbox-exec",
				Restrictions: &config.RunnerRestrictions{
					AllowNetworking: &allowNetworking,
				},
			},
		}

		r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
		if err != nil {
			t.Fatalf("NewRunner failed: %v", err)
		}

		// Try to ping google.com (should fail with network restrictions)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "ping", []string{"-c", "1", "8.8.8.8"}, nil)
		if err != nil {
			// Expected to fail
			t.Logf("RunWithPipes failed as expected: %v", err)
			return
		}

		stdin.Close()
		io.ReadAll(stdout)
		io.ReadAll(stderr)
		err = wait()

		// Should fail due to network restrictions
		if err == nil {
			t.Error("Expected ping to fail with network restrictions, but it succeeded")
		} else {
			t.Logf("ping failed as expected: %v", err)
		}
	} else if runtime.GOOS == "linux" {
		// Test with firejail on Linux
		if _, err := exec.LookPath("firejail"); err != nil {
			t.Skip("firejail not found in PATH")
		}

		allowNetworking := false
		runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
			"exec": {
				Type: "firejail",
				Restrictions: &config.RunnerRestrictions{
					AllowNetworking: &allowNetworking,
				},
			},
		}

		r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
		if err != nil {
			t.Fatalf("NewRunner failed: %v", err)
		}

		// Try to ping google.com (should fail with network restrictions)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "ping", []string{"-c", "1", "8.8.8.8"}, nil)
		if err != nil {
			// Expected to fail
			t.Logf("RunWithPipes failed as expected: %v", err)
			return
		}

		stdin.Close()
		io.ReadAll(stdout)
		io.ReadAll(stderr)
		err = wait()

		// Should fail due to network restrictions
		if err == nil {
			t.Error("Expected ping to fail with network restrictions, but it succeeded")
		} else {
			t.Logf("ping failed as expected: %v", err)
		}
	} else {
		t.Skip("Network restriction test not implemented for this platform")
	}
}

// TestSandboxExec_DenyReadOutsideAllowedFolders verifies that sandbox-exec
// blocks reads from paths the default sandbox profile denies (macOS only).
//
// The go-restricted-runner sandbox profile is `(allow default)` with explicit
// deny rules for `/Users/*/(Documents|Desktop|Downloads|Pictures|Movies|Music)`
// (see sandbox_profile.tpl). AllowReadFolders adds allow rules but does not
// flip the profile to deny-by-default, so we target ~/Documents where the
// profile's regex-based deny is actually in force.
func TestSandboxExec_DenyReadOutsideAllowedFolders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is only available on macOS")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not found in PATH")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	docsDir := filepath.Join(home, "Documents")
	if _, err := os.Stat(docsDir); err != nil {
		t.Skipf("~/Documents not accessible for outside-sandbox setup (%v); cannot verify sandbox read-deny", err)
	}
	targetPath := filepath.Join(docsDir, fmt.Sprintf("mitto-sandbox-read-test-%d.txt", os.Getpid()))
	if err := os.WriteFile(targetPath, []byte("secret-payload\n"), 0644); err != nil {
		t.Skipf("cannot seed file in ~/Documents (likely TCC-restricted for this test host): %v", err)
	}
	defer os.Remove(targetPath)

	allowNetworking := true
	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "sandbox-exec",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking:  &allowNetworking,
				AllowReadFolders: []string{"/tmp"},
			},
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	if r.Type() != "sandbox-exec" {
		t.Fatalf("expected sandbox-exec, got %q", r.Type())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "cat", []string{targetPath}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}
	stdin.Close()

	stdoutBytes, _ := io.ReadAll(stdout)
	stderrBytes, _ := io.ReadAll(stderr)
	waitErr := wait()

	t.Logf("target: %s", targetPath)
	t.Logf("stdout: %q", string(stdoutBytes))
	t.Logf("stderr: %q", string(stderrBytes))
	t.Logf("wait err: %v", waitErr)

	stderrLower := strings.ToLower(string(stderrBytes))
	permissionDenied := strings.Contains(stderrLower, "permission denied") ||
		strings.Contains(stderrLower, "operation not permitted") ||
		strings.Contains(stderrLower, "not permitted")
	stdoutEmpty := len(stdoutBytes) == 0

	if waitErr == nil && !(stdoutEmpty && permissionDenied) && !stdoutEmpty {
		t.Errorf("expected sandbox to deny read of %q, but got stdout=%q stderr=%q err=nil",
			targetPath, string(stdoutBytes), string(stderrBytes))
	}
}

// TestSandboxExec_DenyWriteOutsideAllowedFolders verifies that sandbox-exec
// blocks writes to paths the default sandbox profile denies (macOS only).
//
// The default profile denies file-write* on system paths (/etc, /bin,
// /usr/bin, /System, /Library, etc.). AllowWriteFolders is additive over
// `(allow default)`, so we target /etc where the profile's deny is in force.
// Note: /etc is also OS-perm-denied for regular users, so this test verifies
// the runner correctly propagates write failures through the pipe rather than
// isolating a sandbox-only enforcement window.
func TestSandboxExec_DenyWriteOutsideAllowedFolders(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is only available on macOS")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not found in PATH")
	}

	targetPath := fmt.Sprintf("/etc/mitto-sandbox-write-test-%d.txt", os.Getpid())
	os.Remove(targetPath)
	defer os.Remove(targetPath)

	allowNetworking := true
	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: "sandbox-exec",
			Restrictions: &config.RunnerRestrictions{
				AllowNetworking:   &allowNetworking,
				AllowReadFolders:  []string{"/tmp"},
				AllowWriteFolders: []string{"/tmp"},
			},
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner failed: %v", err)
	}
	if r.Type() != "sandbox-exec" {
		t.Fatalf("expected sandbox-exec, got %q", r.Type())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmdStr := fmt.Sprintf("echo hello > %s", targetPath)
	stdin, stdout, stderr, wait, err := r.RunWithPipes(ctx, "sh", []string{"-c", cmdStr}, nil)
	if err != nil {
		t.Fatalf("RunWithPipes failed: %v", err)
	}
	stdin.Close()

	stdoutBytes, _ := io.ReadAll(stdout)
	stderrBytes, _ := io.ReadAll(stderr)
	waitErr := wait()

	t.Logf("target: %s", targetPath)
	t.Logf("stdout: %q", string(stdoutBytes))
	t.Logf("stderr: %q", string(stderrBytes))
	t.Logf("wait err: %v", waitErr)

	_, statErr := os.Stat(targetPath)
	fileExists := statErr == nil

	if waitErr == nil && fileExists {
		t.Errorf("expected sandbox to deny write of %q, but file exists and wait() returned no error", targetPath)
	}
}

// TestRunner_FallbackWhenRunnerUnavailable verifies the silent-successful
// fallback path when a requested runner is unavailable on this platform.
func TestRunner_FallbackWhenRunnerUnavailable(t *testing.T) {
	var requestedType string
	switch runtime.GOOS {
	case "darwin":
		// firejail is not available on macOS.
		requestedType = "firejail"
	case "linux":
		// Skip if docker is actually usable — we need an unavailable runner.
		if _, err := exec.LookPath("docker"); err == nil {
			if err := exec.Command("docker", "info").Run(); err == nil {
				t.Skip("docker is available on this host; cannot exercise fallback path")
			}
		}
		requestedType = "docker"
	default:
		t.Skip("unsupported OS for fallback test")
	}

	runnerConfigs := map[string]*config.WorkspaceRunnerConfig{
		"exec": {
			Type: requestedType,
		},
	}

	r, err := NewRunner(nil, nil, runnerConfigs, "/tmp", nil)
	if err != nil {
		t.Fatalf("NewRunner returned error, expected silent fallback: %v", err)
	}
	if r.FallbackInfo == nil {
		t.Fatalf("expected FallbackInfo to be populated for unavailable runner %q", requestedType)
	}
	if r.FallbackInfo.RequestedType != requestedType {
		t.Errorf("FallbackInfo.RequestedType = %q, want %q", r.FallbackInfo.RequestedType, requestedType)
	}
	if r.FallbackInfo.FallbackType != "exec" {
		t.Errorf("FallbackInfo.FallbackType = %q, want %q", r.FallbackInfo.FallbackType, "exec")
	}
	if r.Type() != "exec" {
		t.Errorf("r.Type() = %q, want %q", r.Type(), "exec")
	}
	if r.IsRestricted() {
		t.Error("fallback runner should not be restricted")
	}
	t.Logf("fallback reason: %s", r.FallbackInfo.Reason)
}
