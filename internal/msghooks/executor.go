package msghooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// Executor runs hooks and processes their output.
type Executor struct {
	hooksDir string
	logger   *slog.Logger
}

// NewExecutor creates a new hook executor.
func NewExecutor(hooksDir string, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{
		hooksDir: hooksDir,
		logger:   logger,
	}
}

// Execute runs a hook with the given input and returns the output.
func (e *Executor) Execute(ctx context.Context, hook *Hook, input *HookInput) (*HookOutput, error) {
	// Create timeout context
	timeout := hook.GetTimeout().Duration()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Resolve command path
	cmdPath := hook.ResolveCommand()

	// Create command
	cmd := exec.CommandContext(ctx, cmdPath, hook.Args...)

	// Set working directory
	switch hook.GetWorkingDir() {
	case WorkingDirSession:
		cmd.Dir = input.WorkingDir
	case WorkingDirHook:
		cmd.Dir = hook.HookDir
	}

	// Set environment
	cmd.Env = e.buildEnvironment(hook, input)

	// Prepare stdin if needed
	if hook.GetInput() != InputNone {
		inputJSON, err := e.prepareInput(hook, input)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare input: %w", err)
		}
		cmd.Stdin = bytes.NewReader(inputJSON)
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	e.logger.Debug("hook executed",
		"name", hook.Name,
		"duration", duration,
		"exit_code", cmd.ProcessState.ExitCode(),
		"stderr", stderr.String(),
	)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("hook timed out after %v", timeout)
		}
		return nil, fmt.Errorf("hook failed: %w (stderr: %s)", err, stderr.String())
	}

	// Parse output
	if hook.GetOutput() == OutputDiscard {
		return &HookOutput{}, nil
	}

	return e.parseOutput(stdout.Bytes())
}

// buildEnvironment creates the environment variables for the hook.
func (e *Executor) buildEnvironment(hook *Hook, input *HookInput) []string {
	// Start with current environment
	env := os.Environ()

	// Add Mitto-specific variables
	mittoEnv := map[string]string{
		"MITTO_SESSION_ID":       input.SessionID,
		"MITTO_WORKING_DIR":      input.WorkingDir,
		"MITTO_IS_FIRST_MESSAGE": fmt.Sprintf("%t", input.IsFirstMessage),
		"MITTO_HOOKS_DIR":        e.hooksDir,
		"MITTO_HOOK_FILE":        hook.FilePath,
		"MITTO_HOOK_DIR":         hook.HookDir,
	}

	for k, v := range mittoEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Add hook-specific environment variables
	for k, v := range hook.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// prepareInput creates the JSON input for the hook.
func (e *Executor) prepareInput(hook *Hook, input *HookInput) ([]byte, error) {
	// For InputMessage, we send a subset of the input
	if hook.GetInput() == InputMessage {
		msgInput := struct {
			Message        string `json:"message"`
			IsFirstMessage bool   `json:"is_first_message"`
			SessionID      string `json:"session_id"`
			WorkingDir     string `json:"working_dir"`
		}{
			Message:        input.Message,
			IsFirstMessage: input.IsFirstMessage,
			SessionID:      input.SessionID,
			WorkingDir:     input.WorkingDir,
		}
		return json.Marshal(msgInput)
	}

	// For InputConversation, send everything
	return json.Marshal(input)
}

// parseOutput parses the JSON output from the hook.
func (e *Executor) parseOutput(data []byte) (*HookOutput, error) {
	if len(data) == 0 {
		return &HookOutput{}, nil
	}

	var output HookOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse hook output as JSON: %w", err)
	}

	return &output, nil
}
