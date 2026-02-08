//go:build integration

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
