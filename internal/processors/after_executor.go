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

// executeAfterCommand runs a command-mode processor in the agentResponded phase.
// It marshals the AfterProcessorInput as JSON to stdin, captures stdout, and
// returns the raw stdout string. Timeout is taken from proc.GetTimeout().
func executeAfterCommand(ctx context.Context, proc *Processor, processorsDir string, input AfterProcessorInput, logger *slog.Logger) (string, error) {
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

	cmd.Env = buildAfterEnvironment(proc, processorsDir, input)

	if proc.GetInput() != InputNone {
		data, err := json.Marshal(input)
		if err != nil {
			return "", fmt.Errorf("failed to marshal after-phase input: %w", err)
		}
		cmd.Stdin = bytes.NewReader(data)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if logger != nil {
		logger.Info("after-phase processor executed",
			"name", proc.Name,
			"duration", duration,
			"exit_code", cmd.ProcessState.ExitCode(),
			"stderr", stderr.String(),
		)
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("processor timed out after %v", timeout)
		}
		return "", fmt.Errorf("processor failed: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}

// executeAfterPrompt handles prompt-mode processors in the agentResponded phase.
// The prompt template is rendered with after-phase variable substitution, and the
// rendered text is treated as the stdout payload (parsed per output: type).
func executeAfterPrompt(proc *Processor, input AfterProcessorInput) (string, error) {
	rendered := substituteAfterVariables(proc.Prompt, input)
	return rendered, nil
}

// substituteAfterVariables replaces @mitto: placeholders for the agentResponded phase.
func substituteAfterVariables(template string, input AfterProcessorInput) string {
	if !strings.Contains(template, "@mitto:") {
		return template
	}

	turnJSON, _ := json.Marshal(input)
	agentMessages := strings.Join(input.AgentMessages, "\n")

	replacements := map[string]string{
		"@mitto:turn":           string(turnJSON),
		"@mitto:agent_messages": agentMessages,
		"@mitto:user_prompt":    input.UserPrompt,
		"@mitto:stop_reason":    input.StopReason,
		"@mitto:origin":         input.Origin,
		"@mitto:session_id":     input.SessionID,
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

// parseNotifyOutput parses processor stdout for output: notify.
// Accepts:
//   - JSON object: {"title": "...", "message": "...", "style": "info|success|warning|error"}
//   - Plain text: first line = title, rest = message, style defaults to "info"
func parseNotifyOutput(stdout string) ([]AfterNotification, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var notif AfterNotification
	if err := json.Unmarshal([]byte(stdout), &notif); err == nil && notif.Title != "" {
		if notif.Style == "" {
			notif.Style = "info"
		}
		return []AfterNotification{notif}, nil
	}
	lines := strings.SplitN(stdout, "\n", 2)
	title := strings.TrimSpace(lines[0])
	message := ""
	if len(lines) > 1 {
		message = strings.TrimSpace(lines[1])
	}
	return []AfterNotification{{Title: title, Message: message, Style: "info"}}, nil
}

// parseActionButtonsOutput parses processor stdout for output: actionButtons.
// Accepts:
//   - JSON array:  [{"label": "...", "prompt": "..."}, ...]
//   - JSON object: {"label": "...", "prompt": "..."}  — treated as single-element array
//   - Empty/blank — returns nil (no buttons)
func parseActionButtonsOutput(stdout string) ([]AfterActionButton, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var buttons []AfterActionButton
	if err := json.Unmarshal([]byte(stdout), &buttons); err == nil {
		return buttons, nil
	}
	var button AfterActionButton
	if err := json.Unmarshal([]byte(stdout), &button); err == nil && button.Label != "" {
		return []AfterActionButton{button}, nil
	}
	return nil, fmt.Errorf("invalid actionButtons output: expected JSON array or object, got: %s", afterTruncate(stdout, 100))
}

// parseUserDataOutput parses processor stdout for output: userData.
// Expects a JSON object of string → string entries.
func parseUserDataOutput(stdout string) (map[string]string, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}
	var patch map[string]string
	if err := json.Unmarshal([]byte(stdout), &patch); err != nil {
		return nil, fmt.Errorf("invalid userData output: expected JSON object of string→string: %w", err)
	}
	return patch, nil
}

// afterTruncate truncates s to at most max bytes, appending "..." if truncated.
func afterTruncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// buildAfterEnvironment creates environment variables for an agentResponded processor.
func buildAfterEnvironment(proc *Processor, processorsDir string, input AfterProcessorInput) []string {
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
		"MITTO_ORIGIN":         input.Origin,
		"MITTO_STOP_REASON":    input.StopReason,
	}
	for k, v := range mittoEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range proc.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}
