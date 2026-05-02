package processors

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

// Executor runs processors and processes their output.
type Executor struct {
	processorsDir string
	logger        *slog.Logger
}

// NewExecutor creates a new processor executor.
func NewExecutor(processorsDir string, logger *slog.Logger) *Executor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Executor{
		processorsDir: processorsDir,
		logger:        logger,
	}
}

// Execute runs a processor with the given input and returns the output.
func (e *Executor) Execute(ctx context.Context, proc *Processor, input *ProcessorInput) (*ProcessorOutput, error) {
	// Create timeout context
	timeout := proc.GetTimeout().Duration()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Resolve command path
	cmdPath := proc.ResolveCommand()

	// Create command
	cmd := exec.CommandContext(ctx, cmdPath, proc.Args...)

	// Set working directory
	switch proc.GetWorkingDir() {
	case WorkingDirSession:
		cmd.Dir = input.WorkingDir
	case WorkingDirHook:
		cmd.Dir = proc.HookDir
	}

	// Set environment
	cmd.Env = e.buildEnvironment(proc, input)

	// Prepare stdin if needed
	if proc.GetInput() != InputNone {
		inputJSON, err := e.prepareInput(proc, input)
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

	e.logger.Info("processor executed",
		"name", proc.Name,
		"duration", duration,
		"exit_code", cmd.ProcessState.ExitCode(),
		"stderr", stderr.String(),
	)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("processor timed out after %v", timeout)
		}
		return nil, fmt.Errorf("processor failed: %w (stderr: %s)", err, stderr.String())
	}

	// Parse output
	if proc.GetOutput() == OutputDiscard {
		return &ProcessorOutput{}, nil
	}

	return e.parseOutput(stdout.Bytes())
}

// buildEnvironment creates the environment variables for the processor.
func (e *Executor) buildEnvironment(proc *Processor, input *ProcessorInput) []string {
	// Start with current environment
	env := os.Environ()

	// Encode available ACP servers as JSON for the environment variable.
	availableServersJSON := "[]"
	if len(input.AvailableACPServers) > 0 {
		if data, err := json.Marshal(input.AvailableACPServers); err == nil {
			availableServersJSON = string(data)
		}
	}

	// Encode child sessions as JSON for the environment variable.
	childSessionsJSON := "[]"
	if len(input.ChildSessions) > 0 {
		if data, err := json.Marshal(input.ChildSessions); err == nil {
			childSessionsJSON = string(data)
		}
	}

	// Add Mitto-specific variables
	mittoEnv := map[string]string{
		"MITTO_SESSION_ID":            input.SessionID,
		"MITTO_WORKING_DIR":           input.WorkingDir,
		"MITTO_IS_FIRST_MESSAGE":      fmt.Sprintf("%t", input.IsFirstMessage),
		"MITTO_PROCESSORS_DIR":        e.processorsDir,
		"MITTO_PROCESSOR_FILE":        proc.FilePath,
		"MITTO_PROCESSOR_DIR":         proc.HookDir,
		"MITTO_HOOKS_DIR":             e.processorsDir, // legacy alias
		"MITTO_HOOK_FILE":             proc.FilePath,   // legacy alias
		"MITTO_HOOK_DIR":              proc.HookDir,    // legacy alias
		"MITTO_PARENT_SESSION_ID":     input.ParentSessionID,
		"MITTO_PARENT_SESSION_NAME":   input.ParentSessionName,
		"MITTO_SESSION_NAME":          input.SessionName,
		"MITTO_ACP_SERVER":            input.ACPServer,
		"MITTO_WORKSPACE_UUID":        input.WorkspaceUUID,
		"MITTO_AVAILABLE_ACP_SERVERS": availableServersJSON,
		"MITTO_CHILD_SESSIONS":        childSessionsJSON,
	}

	for k, v := range mittoEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Add processor-specific environment variables
	for k, v := range proc.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	return env
}

// prepareInput creates the JSON input for the processor.
func (e *Executor) prepareInput(proc *Processor, input *ProcessorInput) ([]byte, error) {
	// For InputMessage, we send a subset of the input
	if proc.GetInput() == InputMessage {
		msgInput := struct {
			Message             string               `json:"message"`
			IsFirstMessage      bool                 `json:"is_first_message"`
			SessionID           string               `json:"session_id"`
			WorkingDir          string               `json:"working_dir"`
			ParentSessionID     string               `json:"parent_session_id,omitempty"`
			ParentSessionName   string               `json:"parent_session_name,omitempty"`
			SessionName         string               `json:"session_name,omitempty"`
			ACPServer           string               `json:"acp_server,omitempty"`
			WorkspaceUUID       string               `json:"workspace_uuid,omitempty"`
			AvailableACPServers []AvailableACPServer `json:"available_acp_servers,omitempty"`
			ChildSessions       []ChildSession       `json:"child_sessions,omitempty"`
			IsPeriodic          bool                 `json:"is_periodic,omitempty"`
		}{
			Message:             input.Message,
			IsFirstMessage:      input.IsFirstMessage,
			SessionID:           input.SessionID,
			WorkingDir:          input.WorkingDir,
			ParentSessionID:     input.ParentSessionID,
			ParentSessionName:   input.ParentSessionName,
			SessionName:         input.SessionName,
			ACPServer:           input.ACPServer,
			WorkspaceUUID:       input.WorkspaceUUID,
			AvailableACPServers: input.AvailableACPServers,
			ChildSessions:       input.ChildSessions,
			IsPeriodic:          input.IsPeriodic,
		}
		return json.Marshal(msgInput)
	}

	// For InputConversation, send everything
	return json.Marshal(input)
}

// parseOutput parses the JSON output from the processor.
func (e *Executor) parseOutput(data []byte) (*ProcessorOutput, error) {
	if len(data) == 0 {
		return &ProcessorOutput{}, nil
	}

	var output ProcessorOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("failed to parse processor output as JSON: %w", err)
	}

	return &output, nil
}
