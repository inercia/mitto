package msghooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
)

func TestHookIsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		enabled  *bool
		expected bool
	}{
		{"nil (default)", nil, true},
		{"true", boolPtr(true), true},
		{"false", boolPtr(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Hook{Enabled: tt.enabled}
			if got := h.IsEnabled(); got != tt.expected {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHookGetters(t *testing.T) {
	// Test defaults
	h := &Hook{}
	if got := h.GetTimeout(); got != Duration(DefaultTimeout) {
		t.Errorf("GetTimeout() = %v, want %v", got, DefaultTimeout)
	}
	if got := h.GetPriority(); got != DefaultPriority {
		t.Errorf("GetPriority() = %v, want %v", got, DefaultPriority)
	}
	if got := h.GetInput(); got != DefaultInput {
		t.Errorf("GetInput() = %v, want %v", got, DefaultInput)
	}
	if got := h.GetOutput(); got != DefaultOutput {
		t.Errorf("GetOutput() = %v, want %v", got, DefaultOutput)
	}
	if got := h.GetWorkingDir(); got != DefaultWorkingDir {
		t.Errorf("GetWorkingDir() = %v, want %v", got, DefaultWorkingDir)
	}
	if got := h.GetOnError(); got != DefaultErrorHandle {
		t.Errorf("GetOnError() = %v, want %v", got, DefaultErrorHandle)
	}

	// Test custom values
	h2 := &Hook{
		Timeout:    Duration(10 * time.Second),
		Priority:   50,
		Input:      InputConversation,
		Output:     OutputAppend,
		WorkingDir: WorkingDirHook,
		OnError:    ErrorFail,
	}
	if got := h2.GetTimeout(); got != Duration(10*time.Second) {
		t.Errorf("GetTimeout() = %v, want %v", got, 10*time.Second)
	}
	if got := h2.GetPriority(); got != 50 {
		t.Errorf("GetPriority() = %v, want 50", got)
	}
}

func TestHookShouldApply(t *testing.T) {
	tests := []struct {
		name           string
		hook           *Hook
		isFirstMessage bool
		workingDir     string
		expected       bool
	}{
		{
			name:           "disabled hook",
			hook:           &Hook{Enabled: boolPtr(false), When: config.ProcessorWhenAll},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name:           "when=first, is first",
			hook:           &Hook{When: config.ProcessorWhenFirst},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name:           "when=first, not first",
			hook:           &Hook{When: config.ProcessorWhenFirst},
			isFirstMessage: false,
			expected:       false,
		},
		{
			name:           "when=all, is first",
			hook:           &Hook{When: config.ProcessorWhenAll},
			isFirstMessage: true,
			expected:       true,
		},
		{
			name:           "when=all, not first",
			hook:           &Hook{When: config.ProcessorWhenAll},
			isFirstMessage: false,
			expected:       true,
		},
		{
			name:           "when=all-except-first, is first",
			hook:           &Hook{When: config.ProcessorWhenAllExceptFirst},
			isFirstMessage: true,
			expected:       false,
		},
		{
			name:           "when=all-except-first, not first",
			hook:           &Hook{When: config.ProcessorWhenAllExceptFirst},
			isFirstMessage: false,
			expected:       true,
		},
		{
			name:           "workspace filter match",
			hook:           &Hook{When: config.ProcessorWhenAll, Workspaces: []string{"/project"}},
			isFirstMessage: true,
			workingDir:     "/project",
			expected:       true,
		},
		{
			name:           "workspace filter no match",
			hook:           &Hook{When: config.ProcessorWhenAll, Workspaces: []string{"/project"}},
			isFirstMessage: true,
			workingDir:     "/other",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.hook.ShouldApply(tt.isFirstMessage, tt.workingDir); got != tt.expected {
				t.Errorf("ShouldApply() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestResolveCommand(t *testing.T) {
	h := &Hook{
		Command: "./script.sh",
		HookDir: "/hooks",
	}
	if got := h.ResolveCommand(); got != "/hooks/script.sh" {
		t.Errorf("ResolveCommand() = %v, want /hooks/script.sh", got)
	}

	h2 := &Hook{
		Command: "/usr/bin/echo",
		HookDir: "/hooks",
	}
	if got := h2.ResolveCommand(); got != "/usr/bin/echo" {
		t.Errorf("ResolveCommand() = %v, want /usr/bin/echo", got)
	}
}

func TestLoaderLoad(t *testing.T) {
	// Create temp directory with test hooks
	tmpDir, err := os.MkdirTemp("", "hooks-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a valid hook file
	hookContent := `
name: test-hook
command: /bin/echo
when: all
`
	if err := os.WriteFile(filepath.Join(tmpDir, "test.yaml"), []byte(hookContent), 0644); err != nil {
		t.Fatalf("Failed to write hook file: %v", err)
	}

	// Create disabled directory with a hook (should be skipped)
	disabledDir := filepath.Join(tmpDir, "disabled")
	if err := os.MkdirAll(disabledDir, 0755); err != nil {
		t.Fatalf("Failed to create disabled dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(disabledDir, "skip.yaml"), []byte(hookContent), 0644); err != nil {
		t.Fatalf("Failed to write disabled hook file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	hooks, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(hooks) != 1 {
		t.Errorf("Load() returned %d hooks, want 1", len(hooks))
	}

	if hooks[0].Name != "test-hook" {
		t.Errorf("Hook name = %v, want test-hook", hooks[0].Name)
	}
}

func TestLoaderLoadEmpty(t *testing.T) {
	// Test with non-existent directory
	loader := NewLoader("/nonexistent/path", nil)
	hooks, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("Load() returned %d hooks, want 0", len(hooks))
	}
}

func TestLoaderLoadInvalidYAML(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "hooks-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create invalid YAML file
	if err := os.WriteFile(filepath.Join(tmpDir, "invalid.yaml"), []byte("invalid: yaml: content:"), 0644); err != nil {
		t.Fatalf("Failed to write invalid file: %v", err)
	}

	loader := NewLoader(tmpDir, nil)
	hooks, err := loader.Load()
	// Should not error, just skip invalid files
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("Load() returned %d hooks, want 0 (invalid should be skipped)", len(hooks))
	}
}

func TestExecutorPrepareInput(t *testing.T) {
	executor := NewExecutor("/hooks", nil)
	hook := &Hook{Input: InputMessage}
	input := &HookInput{
		Message:        "test message",
		IsFirstMessage: true,
		SessionID:      "session-123",
		WorkingDir:     "/project",
	}

	data, err := executor.prepareInput(hook, input)
	if err != nil {
		t.Fatalf("prepareInput() error = %v", err)
	}

	// Verify JSON contains expected fields
	expected := `"message":"test message"`
	if !contains(string(data), expected) {
		t.Errorf("prepareInput() = %s, want to contain %s", data, expected)
	}
}

func TestApplyHooksEmpty(t *testing.T) {
	ctx := context.Background()
	input := &HookInput{Message: "original"}

	result, err := ApplyHooks(ctx, nil, input, "", nil)
	if err != nil {
		t.Fatalf("ApplyHooks() error = %v", err)
	}
	if result.Message != "original" {
		t.Errorf("ApplyHooks() = %v, want original", result.Message)
	}
	if len(result.Attachments) != 0 {
		t.Errorf("ApplyHooks() attachments = %d, want 0", len(result.Attachments))
	}
}

func TestApplyHooksTransform(t *testing.T) {
	// Create a temp directory for the hook
	tmpDir := t.TempDir()

	// Create a simple echo script that transforms the message
	scriptPath := filepath.Join(tmpDir, "transform.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "transformed message"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "transform-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenAll,
			Output:  OutputTransform,
			Input:   InputMessage,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original message",
		IsFirstMessage: true,
		SessionID:      "test-session",
		WorkingDir:     tmpDir,
	}

	result, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyHooks() error = %v", err)
	}
	if result.Message != "transformed message" {
		t.Errorf("ApplyHooks() = %q, want %q", result.Message, "transformed message")
	}
}

func TestApplyHooksPrepend(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "prepend.sh")
	scriptContent := `#!/bin/sh
echo '{"text": "PREFIX: "}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "prepend-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenAll,
			Output:  OutputPrepend,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyHooks() error = %v", err)
	}
	expected := "PREFIX: original"
	if result.Message != expected {
		t.Errorf("ApplyHooks() = %q, want %q", result.Message, expected)
	}
}

func TestApplyHooksAppend(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "append.sh")
	scriptContent := `#!/bin/sh
echo '{"text": " :SUFFIX"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "append-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenAll,
			Output:  OutputAppend,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyHooks() error = %v", err)
	}
	expected := "original :SUFFIX"
	if result.Message != expected {
		t.Errorf("ApplyHooks() = %q, want %q", result.Message, expected)
	}
}

func TestApplyHooksDiscard(t *testing.T) {
	tmpDir := t.TempDir()

	// Script that outputs something, but it should be discarded
	scriptPath := filepath.Join(tmpDir, "discard.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "this should be ignored"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "discard-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenAll,
			Output:  OutputDiscard,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyHooks() error = %v", err)
	}
	// Message should remain unchanged
	if result.Message != "original" {
		t.Errorf("ApplyHooks() = %q, want %q", result.Message, "original")
	}
}

func TestApplyHooksSkipsNonApplicable(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "first-only.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "first message only"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "first-only-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenFirst, // Only applies to first message
			Output:  OutputTransform,
			Input:   InputNone,
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: false, // Not first message
		WorkingDir:     tmpDir,
	}

	result, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyHooks() error = %v", err)
	}
	// Hook should not apply, message unchanged
	if result.Message != "original" {
		t.Errorf("ApplyHooks() = %q, want %q", result.Message, "original")
	}
}

func TestApplyHooksErrorSkip(t *testing.T) {
	tmpDir := t.TempDir()

	// Script that fails
	scriptPath := filepath.Join(tmpDir, "fail.sh")
	scriptContent := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "failing-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenAll,
			Output:  OutputTransform,
			OnError: ErrorSkip, // Should skip on error
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err != nil {
		t.Fatalf("ApplyHooks() should not error with ErrorSkip, got: %v", err)
	}
	// Message should remain unchanged
	if result.Message != "original" {
		t.Errorf("ApplyHooks() = %q, want %q", result.Message, "original")
	}
}

func TestApplyHooksErrorFail(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fail.sh")
	scriptContent := `#!/bin/sh
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	hooks := []*Hook{
		{
			Name:    "failing-hook",
			Command: scriptPath,
			When:    config.ProcessorWhenAll,
			Output:  OutputTransform,
			OnError: ErrorFail, // Should fail on error
			HookDir: tmpDir,
		},
	}

	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	_, err := ApplyHooks(ctx, hooks, input, tmpDir, nil)
	if err == nil {
		t.Fatal("ApplyHooks() should error with ErrorFail")
	}
}

func TestManagerLoadAndApply(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a hook file
	hookContent := `
name: test-manager-hook
command: /bin/echo
args: ['{"message": "from manager"}']
when: all
output: transform
`
	if err := os.WriteFile(filepath.Join(tmpDir, "hook.yaml"), []byte(hookContent), 0644); err != nil {
		t.Fatalf("Failed to write hook file: %v", err)
	}

	manager := NewManager(tmpDir, nil)

	// Test Load
	if err := manager.Load(); err != nil {
		t.Fatalf("Manager.Load() error = %v", err)
	}

	// Test Hooks
	hooks := manager.Hooks()
	if len(hooks) != 1 {
		t.Fatalf("Manager.Hooks() = %d hooks, want 1", len(hooks))
	}
	if hooks[0].Name != "test-manager-hook" {
		t.Errorf("Hook name = %q, want %q", hooks[0].Name, "test-manager-hook")
	}

	// Test HooksDir
	if manager.HooksDir() != tmpDir {
		t.Errorf("Manager.HooksDir() = %q, want %q", manager.HooksDir(), tmpDir)
	}

	// Test Apply
	ctx := context.Background()
	input := &HookInput{
		Message:        "original",
		IsFirstMessage: true,
		WorkingDir:     tmpDir,
	}

	result, err := manager.Apply(ctx, input)
	if err != nil {
		t.Fatalf("Manager.Apply() error = %v", err)
	}
	if result.Message != "from manager" {
		t.Errorf("Manager.Apply() = %q, want %q", result.Message, "from manager")
	}
}

func TestExecutorExecute(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "exec.sh")
	scriptContent := `#!/bin/sh
echo '{"message": "executed"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	executor := NewExecutor(tmpDir, nil)
	hook := &Hook{
		Name:    "exec-test",
		Command: scriptPath,
		Output:  OutputTransform,
		Input:   InputNone,
		HookDir: tmpDir,
	}
	input := &HookInput{
		Message:    "test",
		WorkingDir: tmpDir,
	}

	ctx := context.Background()
	output, err := executor.Execute(ctx, hook, input)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.Message != "executed" {
		t.Errorf("Execute() message = %q, want %q", output.Message, "executed")
	}
}

func TestExecutorExecuteTimeout(t *testing.T) {
	tmpDir := t.TempDir()

	// Script that sleeps longer than timeout
	scriptPath := filepath.Join(tmpDir, "slow.sh")
	scriptContent := `#!/bin/sh
sleep 10
echo '{"message": "should not reach"}'
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	executor := NewExecutor(tmpDir, nil)
	hook := &Hook{
		Name:    "slow-hook",
		Command: scriptPath,
		Output:  OutputTransform,
		Timeout: Duration(100 * time.Millisecond), // Very short timeout
		HookDir: tmpDir,
	}
	input := &HookInput{
		Message:    "test",
		WorkingDir: tmpDir,
	}

	ctx := context.Background()
	_, err := executor.Execute(ctx, hook, input)
	if err == nil {
		t.Fatal("Execute() should error on timeout")
	}
	if !contains(err.Error(), "timed out") {
		t.Errorf("Execute() error = %q, want to contain 'timed out'", err.Error())
	}
}

func TestExecutorBuildEnvironment(t *testing.T) {
	executor := NewExecutor("/hooks", nil)
	hook := &Hook{
		FilePath: "/hooks/test.yaml",
		HookDir:  "/hooks",
		Environment: map[string]string{
			"CUSTOM_VAR": "custom_value",
		},
	}
	input := &HookInput{
		SessionID:      "session-123",
		WorkingDir:     "/project",
		IsFirstMessage: true,
	}

	env := executor.buildEnvironment(hook, input)

	// Check for expected environment variables
	envMap := make(map[string]string)
	for _, e := range env {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				envMap[e[:i]] = e[i+1:]
				break
			}
		}
	}

	if envMap["MITTO_SESSION_ID"] != "session-123" {
		t.Errorf("MITTO_SESSION_ID = %q, want %q", envMap["MITTO_SESSION_ID"], "session-123")
	}
	if envMap["MITTO_WORKING_DIR"] != "/project" {
		t.Errorf("MITTO_WORKING_DIR = %q, want %q", envMap["MITTO_WORKING_DIR"], "/project")
	}
	if envMap["MITTO_IS_FIRST_MESSAGE"] != "true" {
		t.Errorf("MITTO_IS_FIRST_MESSAGE = %q, want %q", envMap["MITTO_IS_FIRST_MESSAGE"], "true")
	}
	if envMap["CUSTOM_VAR"] != "custom_value" {
		t.Errorf("CUSTOM_VAR = %q, want %q", envMap["CUSTOM_VAR"], "custom_value")
	}
}

func TestExecutorParseOutput(t *testing.T) {
	executor := NewExecutor("/hooks", nil)

	tests := []struct {
		name    string
		data    []byte
		wantMsg string
		wantErr bool
	}{
		{
			name:    "empty output",
			data:    []byte{},
			wantMsg: "",
			wantErr: false,
		},
		{
			name:    "valid JSON",
			data:    []byte(`{"message": "hello"}`),
			wantMsg: "hello",
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    []byte(`not json`),
			wantMsg: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := executor.parseOutput(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && output.Message != tt.wantMsg {
				t.Errorf("parseOutput() message = %q, want %q", output.Message, tt.wantMsg)
			}
		})
	}
}

func TestHookGetPosition(t *testing.T) {
	tests := []struct {
		name     string
		position config.ProcessorPosition
		expected config.ProcessorPosition
	}{
		{"empty defaults to prepend", "", config.ProcessorPositionPrepend},
		{"prepend", config.ProcessorPositionPrepend, config.ProcessorPositionPrepend},
		{"append", config.ProcessorPositionAppend, config.ProcessorPositionAppend},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Hook{Position: tt.position}
			if got := h.GetPosition(); got != tt.expected {
				t.Errorf("GetPosition() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDurationUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"valid duration", "10s", 10 * time.Second, false},
		{"empty defaults", "", DefaultTimeout, false},
		{"invalid", "not-a-duration", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := d.UnmarshalYAML(func(v interface{}) error {
				*(v.(*string)) = tt.input
				return nil
			})
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalYAML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && d.Duration() != tt.expected {
				t.Errorf("UnmarshalYAML() = %v, want %v", d.Duration(), tt.expected)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func boolPtr(b bool) *bool {
	return &b
}
