package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// executeCloseCommand runs a command-mode processor in the conversationClosed phase.
// It marshals the CloseProcessorInput as JSON to stdin, captures stdout for logging
// (stdout is otherwise ignored — only output:discard is allowed for this phase), and
// returns any execution error. Timeout is taken from proc.GetTimeout().
func executeCloseCommand(ctx context.Context, proc *Processor, processorsDir string, input CloseProcessorInput, logger *slog.Logger) error {
	timeout := proc.GetTimeout().Duration()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdPath := proc.ResolveCommand()
	cmd := exec.CommandContext(ctx, cmdPath, proc.Args...)

	switch proc.GetWorkingDir() {
	case WorkingDirSession:
		if input.WorkingDir != "" {
			cmd.Dir = input.WorkingDir
		}
	case WorkingDirHook:
		cmd.Dir = proc.HookDir
	}

	cmd.Env = buildCloseEnvironment(proc, processorsDir, input)

	if proc.GetInput() != InputNone {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("failed to marshal close-phase input: %w", err)
		}
		cmd.Stdin = bytes.NewReader(data)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := runProcessorCommand(ctx, proc, cmd)
	duration := time.Since(start)

	if logger != nil {
		logAttrs := []any{
			"name", proc.Name,
			"duration", duration,
			"stderr", stderr.String(),
		}
		if cmd.ProcessState != nil {
			logAttrs = append(logAttrs, "exit_code", cmd.ProcessState.ExitCode())
		}
		logger.Info("close-phase processor executed", logAttrs...)
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("processor timed out after %v", timeout)
		}
		return fmt.Errorf("processor failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// substituteCloseVariables replaces @mitto: placeholders for the conversationClosed phase.
func substituteCloseVariables(template string, input CloseProcessorInput) string {
	if !strings.Contains(template, "@mitto:") {
		return template
	}

	inputJSON, _ := json.Marshal(input)

	replacements := map[string]string{
		"@mitto:close":          string(inputJSON),
		"@mitto:session_id":     input.SessionID,
		"@mitto:archive_reason": input.ArchiveReason,
		"@mitto:working_dir":    input.WorkingDir,
	}

	keys := make([]string, 0, len(replacements))
	for k := range replacements {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })

	result := template
	for _, placeholder := range keys {
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, replacements[placeholder])
		}
	}
	return result
}

// buildCloseEnvironment creates environment variables for a conversationClosed processor.
func buildCloseEnvironment(proc *Processor, processorsDir string, input CloseProcessorInput) []string {
	env := os.Environ()

	mittoEnv := map[string]string{
		"MITTO_SESSION_ID":     input.SessionID,
		"MITTO_WORKING_DIR":    input.WorkingDir,
		"MITTO_PROCESSORS_DIR": processorsDir,
		"MITTO_PROCESSOR_FILE": proc.FilePath,
		"MITTO_PROCESSOR_DIR":  proc.HookDir,
		"MITTO_HOOKS_DIR":      processorsDir, // legacy alias
		"MITTO_HOOK_FILE":      proc.FilePath, // legacy alias
		"MITTO_HOOK_DIR":       proc.HookDir,  // legacy alias
		"MITTO_ARCHIVE_REASON": input.ArchiveReason,
	}
	for k, v := range mittoEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range proc.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
