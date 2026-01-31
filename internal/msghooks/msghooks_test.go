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
