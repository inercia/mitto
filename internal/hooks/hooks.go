// Package hooks provides lifecycle hook execution for the Mitto web server.
// Hooks are shell commands that run at specific points in the server lifecycle,
// such as startup (up) and shutdown (down).
package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/logging"
)

// Process manages a running hook command and its lifecycle.
// It is safe for concurrent use.
type Process struct {
	name string
	cmd  *exec.Cmd
	mu   sync.Mutex
	done bool
}

// StartUp starts the web.hooks.up command asynchronously and returns
// a Process that can be used to stop it during shutdown.
// It replaces ${PORT} in the command with the actual port number.
// Returns nil if the command fails to start.
func StartUp(hook config.WebHook, port int) *Process {
	if hook.Command == "" {
		return nil
	}

	logger := logging.Hook()

	// Replace ${PORT} placeholder with actual port
	command := strings.ReplaceAll(hook.Command, "${PORT}", strconv.Itoa(port))

	// Log the hook execution
	hookName := hook.Name
	if hookName == "" {
		hookName = "up"
	}
	fmt.Printf("🔗 Running hook: %s\n", hookName)
	logger.Info("Starting up hook",
		"name", hookName,
		"command", command,
		"port", port,
	)

	// Create the command with a new process group so we can kill all children
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Set process group ID so we can kill the entire process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	hp := &Process{
		name: hook.Name,
		cmd:  cmd,
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		fmt.Printf("⚠️  Hook start error: %v\n", err)
		logger.Error("Failed to start up hook",
			"name", hookName,
			"error", err,
		)
		return nil
	}

	logger.Debug("Up hook started",
		"name", hookName,
		"pid", cmd.Process.Pid,
	)

	// Wait for the command in a goroutine to handle short-lived commands
	go func() {
		err := cmd.Wait()
		hp.mu.Lock()
		hp.done = true
		hp.mu.Unlock()

		// Get exit code for logging
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}

		if err != nil {
			// Check if it was killed by a signal (normal during shutdown)
			if exitCode == -1 {
				// Killed by signal, don't log as error
				logger.Debug("Up hook killed by signal",
					"name", hookName,
				)
				return
			}
			fmt.Printf("⚠️  Hook '%s' exited with code %d: %v\n", hookName, exitCode, err)
			logger.Error("Up hook exited with error",
				"name", hookName,
				"exit_code", exitCode,
				"error", err,
			)
		} else {
			fmt.Printf("🔗 Hook '%s' completed (exit code 0)\n", hookName)
			logger.Info("Up hook completed successfully",
				"name", hookName,
				"exit_code", 0,
			)
		}
	}()

	return hp
}

// Stop terminates the hook process if it's still running.
// It sends SIGTERM to the process group to ensure all child processes are also terminated.
// It is safe to call Stop on a nil Process.
func (hp *Process) Stop() {
	if hp == nil {
		return
	}

	logger := logging.Hook()

	hp.mu.Lock()
	defer hp.mu.Unlock()

	if hp.done {
		return
	}

	if hp.cmd.Process == nil {
		return
	}

	hookName := hp.name
	if hookName == "" {
		hookName = "up"
	}

	// Kill the entire process group (negative PID)
	pgid, err := syscall.Getpgid(hp.cmd.Process.Pid)
	if err == nil {
		// Send SIGTERM to the process group
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			// If SIGTERM fails, try SIGKILL
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	} else {
		// Fallback: kill just the process
		_ = hp.cmd.Process.Kill()
	}

	fmt.Printf("🔗 Stopped hook: %s\n", hookName)
	logger.Info("Stopped up hook",
		"name", hookName,
	)

	hp.done = true
}

// RunDown runs the web.hooks.down command synchronously.
// It waits for the command to complete before returning.
// It replaces ${PORT} in the command with the actual port number.
// Does nothing if the hook command is empty.
func RunDown(hook config.WebHook, port int) {
	if hook.Command == "" {
		return
	}

	logger := logging.Hook()

	// Replace ${PORT} placeholder with actual port
	command := strings.ReplaceAll(hook.Command, "${PORT}", strconv.Itoa(port))

	// Log the hook execution
	hookName := hook.Name
	if hookName == "" {
		hookName = "down"
	}
	fmt.Printf("🔗 Running down hook: %s\n", hookName)
	logger.Info("Starting down hook",
		"name", hookName,
		"command", command,
		"port", port,
	)

	// Create and run the command synchronously
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Get exit code for logging
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		fmt.Printf("⚠️  Down hook '%s' exited with code %d: %v\n", hookName, exitCode, err)
		logger.Error("Down hook failed",
			"name", hookName,
			"exit_code", exitCode,
			"error", err,
		)
	} else {
		fmt.Printf("🔗 Down hook '%s' completed (exit code 0)\n", hookName)
		logger.Info("Down hook completed",
			"name", hookName,
			"exit_code", 0,
		)
	}
}
