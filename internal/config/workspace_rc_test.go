package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseWorkspaceRC_ValidPrompts(t *testing.T) {
	yaml := `
prompts:
  - name: "Fix Bug"
    prompt: "Please fix this bug"
    backgroundColor: "#E8F5E9"
  - name: "Add Tests"
    prompt: "Write tests for this code"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	if len(rc.Prompts) != 2 {
		t.Errorf("Prompts count = %d, want 2", len(rc.Prompts))
	}

	if rc.Prompts[0].Name != "Fix Bug" {
		t.Errorf("first prompt name = %q, want %q", rc.Prompts[0].Name, "Fix Bug")
	}

	if rc.Prompts[0].Prompt != "Please fix this bug" {
		t.Errorf("first prompt text = %q, want %q", rc.Prompts[0].Prompt, "Please fix this bug")
	}

	if rc.Prompts[0].BackgroundColor != "#E8F5E9" {
		t.Errorf("first prompt backgroundColor = %q, want %q", rc.Prompts[0].BackgroundColor, "#E8F5E9")
	}

	if rc.Prompts[1].Name != "Add Tests" {
		t.Errorf("second prompt name = %q, want %q", rc.Prompts[1].Name, "Add Tests")
	}

	if rc.Prompts[1].BackgroundColor != "" {
		t.Errorf("second prompt backgroundColor = %q, want empty", rc.Prompts[1].BackgroundColor)
	}
}

func TestParseWorkspaceRC_EmptyPrompts(t *testing.T) {
	yaml := `
prompts: []
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	if len(rc.Prompts) != 0 {
		t.Errorf("Prompts count = %d, want 0", len(rc.Prompts))
	}
}

func TestParseWorkspaceRC_IgnoresOtherSections(t *testing.T) {
	yaml := `
acp:
  - auggie:
      command: "auggie --acp"
web:
  host: "0.0.0.0"
  port: 8080
prompts:
  - name: "Review"
    prompt: "Review this code"
ui:
  confirmations:
    delete_session: false
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	// Only prompts should be extracted
	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}

	if rc.Prompts[0].Name != "Review" {
		t.Errorf("prompt name = %q, want %q", rc.Prompts[0].Name, "Review")
	}
}

func TestParseWorkspaceRC_SkipsEmptyEntries(t *testing.T) {
	yaml := `
prompts:
  - name: "Valid"
    prompt: "Valid prompt"
  - name: ""
    prompt: "Empty name"
  - name: "No prompt"
    prompt: ""
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	// Only the first prompt with non-empty name and prompt should be included
	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}

	if rc.Prompts[0].Name != "Valid" {
		t.Errorf("prompt name = %q, want %q", rc.Prompts[0].Name, "Valid")
	}
}

func TestParseWorkspaceRC_InvalidYAML(t *testing.T) {
	yaml := `{{invalid yaml`
	_, err := parseWorkspaceRC([]byte(yaml))
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestParseWorkspaceRC_PromptsDirs(t *testing.T) {
	yaml := `
prompts_dirs:
  - ".prompts"
  - "/shared/team/prompts"
  - "project-prompts"
prompts:
  - name: "Inline"
    prompt: "Inline prompt"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	if len(rc.PromptsDirs) != 3 {
		t.Errorf("PromptsDirs count = %d, want 3", len(rc.PromptsDirs))
	}

	if rc.PromptsDirs[0] != ".prompts" {
		t.Errorf("PromptsDirs[0] = %q, want %q", rc.PromptsDirs[0], ".prompts")
	}

	if rc.PromptsDirs[1] != "/shared/team/prompts" {
		t.Errorf("PromptsDirs[1] = %q, want %q", rc.PromptsDirs[1], "/shared/team/prompts")
	}

	if rc.PromptsDirs[2] != "project-prompts" {
		t.Errorf("PromptsDirs[2] = %q, want %q", rc.PromptsDirs[2], "project-prompts")
	}

	// Should also have the inline prompt
	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}
}

func TestParseWorkspaceRC_PromptsDirsOnly(t *testing.T) {
	yaml := `
prompts_dirs:
  - ".prompts"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	if len(rc.PromptsDirs) != 1 {
		t.Errorf("PromptsDirs count = %d, want 1", len(rc.PromptsDirs))
	}

	if len(rc.Prompts) != 0 {
		t.Errorf("Prompts count = %d, want 0", len(rc.Prompts))
	}
}

func TestParseWorkspaceRC_EmptyPromptsDirs(t *testing.T) {
	yaml := `
prompts_dirs: []
prompts:
  - name: "Test"
    prompt: "Test prompt"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	if len(rc.PromptsDirs) != 0 {
		t.Errorf("PromptsDirs count = %d, want 0", len(rc.PromptsDirs))
	}

	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}
}

func TestLoadWorkspaceRC_NonexistentFile(t *testing.T) {
	rc, err := LoadWorkspaceRC("/nonexistent/path/to/workspace")
	if err != nil {
		t.Errorf("LoadWorkspaceRC should return nil, nil for nonexistent file, got error: %v", err)
	}
	if rc != nil {
		t.Errorf("LoadWorkspaceRC should return nil for nonexistent file, got: %v", rc)
	}
}

func TestLoadWorkspaceRC_EmptyDir(t *testing.T) {
	rc, err := LoadWorkspaceRC("")
	if err != nil {
		t.Errorf("LoadWorkspaceRC should return nil, nil for empty dir, got error: %v", err)
	}
	if rc != nil {
		t.Errorf("LoadWorkspaceRC should return nil for empty dir, got: %v", rc)
	}
}

func TestLoadWorkspaceRC_ValidFile(t *testing.T) {
	// Create a temp directory with a .mittorc file
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rcContent := `
prompts:
  - name: "Test Prompt"
    prompt: "This is a test prompt"
`
	rcPath := filepath.Join(tmpDir, WorkspaceRCFileName)
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	rc, err := LoadWorkspaceRC(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("LoadWorkspaceRC returned nil")
	}

	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}

	if rc.Prompts[0].Name != "Test Prompt" {
		t.Errorf("prompt name = %q, want %q", rc.Prompts[0].Name, "Test Prompt")
	}
}

func TestLoadWorkspaceRC_EmptyFile(t *testing.T) {
	// Create a temp directory with an empty .mittorc file
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rcPath := filepath.Join(tmpDir, WorkspaceRCFileName)
	if err := os.WriteFile(rcPath, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to write empty .mittorc: %v", err)
	}

	rc, err := LoadWorkspaceRC(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC should return nil for empty file, got error: %v", err)
	}

	if rc != nil {
		t.Errorf("LoadWorkspaceRC should return nil for empty file, got: %v", rc)
	}
}

func TestWorkspaceRCCache_Get(t *testing.T) {
	// Create a temp directory with a .mittorc file
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rcContent := `
prompts:
  - name: "Cached Prompt"
    prompt: "This should be cached"
`
	rcPath := filepath.Join(tmpDir, WorkspaceRCFileName)
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	cache := NewWorkspaceRCCache(1 * time.Hour)

	// First get should load from file
	rc1, err := cache.Get(tmpDir)
	if err != nil {
		t.Fatalf("cache.Get failed: %v", err)
	}

	if rc1 == nil || len(rc1.Prompts) != 1 {
		t.Fatal("cache.Get should return the loaded config")
	}

	// Second get should return cached value
	rc2, err := cache.Get(tmpDir)
	if err != nil {
		t.Fatalf("cache.Get (cached) failed: %v", err)
	}

	// Should be the same pointer (cached)
	if rc1 != rc2 {
		t.Error("cache.Get should return cached value")
	}
}

func TestWorkspaceRCCache_Invalidate(t *testing.T) {
	cache := NewWorkspaceRCCache(1 * time.Hour)

	// Create a temp directory with a .mittorc file
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rcContent := `prompts: [{name: "Test", prompt: "Test prompt"}]`
	rcPath := filepath.Join(tmpDir, WorkspaceRCFileName)
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	// Load into cache
	_, err = cache.Get(tmpDir)
	if err != nil {
		t.Fatalf("cache.Get failed: %v", err)
	}

	// Invalidate
	cache.Invalidate(tmpDir)

	// Update the file
	rcContent2 := `prompts: [{name: "Updated", prompt: "Updated prompt"}]`
	if err := os.WriteFile(rcPath, []byte(rcContent2), 0644); err != nil {
		t.Fatalf("Failed to write updated .mittorc: %v", err)
	}

	// Get again should reload
	rc, err := cache.Get(tmpDir)
	if err != nil {
		t.Fatalf("cache.Get after invalidate failed: %v", err)
	}

	if rc.Prompts[0].Name != "Updated" {
		t.Errorf("prompt name = %q, want %q", rc.Prompts[0].Name, "Updated")
	}
}

func TestWorkspaceRCCache_Clear(t *testing.T) {
	cache := NewWorkspaceRCCache(1 * time.Hour)

	// Populate cache with a nil entry
	cache.mu.Lock()
	cache.cache["test-dir"] = nil
	cache.mu.Unlock()

	// Clear all entries
	cache.Clear()

	cache.mu.RLock()
	count := len(cache.cache)
	cache.mu.RUnlock()

	if count != 0 {
		t.Errorf("cache should be empty after Clear, got %d entries", count)
	}
}

func TestParseWorkspaceRC_Conversations(t *testing.T) {
	yaml := `
prompts:
  - name: "Test"
    prompt: "Test prompt"
conversations:
  processing:
    override: false
    processors:
      - when: first
        position: prepend
        text: "System context\n\n"
      - when: all
        position: append
        text: "\n\n[Be concise]"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	// Check prompts are still parsed
	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}

	// Check conversations config
	if rc.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if rc.Conversations.Processing == nil {
		t.Fatal("Conversations.Processing is nil")
	}
	if rc.Conversations.Processing.Override {
		t.Error("Override should be false")
	}
	if len(rc.Conversations.Processing.Processors) != 2 {
		t.Fatalf("Processors count = %d, want 2", len(rc.Conversations.Processing.Processors))
	}

	p0 := rc.Conversations.Processing.Processors[0]
	if p0.When != ProcessorWhenFirst {
		t.Errorf("Processor[0].When = %q, want %q", p0.When, ProcessorWhenFirst)
	}
	if p0.Position != ProcessorPositionPrepend {
		t.Errorf("Processor[0].Position = %q, want %q", p0.Position, ProcessorPositionPrepend)
	}
	if p0.Text != "System context\n\n" {
		t.Errorf("Processor[0].Text = %q, want %q", p0.Text, "System context\n\n")
	}

	p1 := rc.Conversations.Processing.Processors[1]
	if p1.When != ProcessorWhenAll {
		t.Errorf("Processor[1].When = %q, want %q", p1.When, ProcessorWhenAll)
	}
	if p1.Position != ProcessorPositionAppend {
		t.Errorf("Processor[1].Position = %q, want %q", p1.Position, ProcessorPositionAppend)
	}
}

func TestParseWorkspaceRC_ConversationsOverride(t *testing.T) {
	yaml := `
conversations:
  processing:
    override: true
    processors:
      - when: first
        position: prepend
        text: "Override only"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if !rc.Conversations.Processing.Override {
		t.Error("Override should be true")
	}
	if len(rc.Conversations.Processing.Processors) != 1 {
		t.Fatalf("Processors count = %d, want 1", len(rc.Conversations.Processing.Processors))
	}
}

func TestParseWorkspaceRC_NoConversations(t *testing.T) {
	yaml := `
prompts:
  - name: "Test"
    prompt: "Test prompt"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc.Conversations != nil {
		t.Error("Conversations should be nil when not specified")
	}
}

func TestLoadWorkspaceRC_WithConversations(t *testing.T) {
	// Create a temp directory with a .mittorc file containing conversations
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	rcContent := `
prompts:
  - name: "Test Prompt"
    prompt: "This is a test prompt"
conversations:
  processing:
    processors:
      - when: first
        position: prepend
        text: "Project context: This is a test project.\n\n"
`
	rcPath := filepath.Join(tmpDir, WorkspaceRCFileName)
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	rc, err := LoadWorkspaceRC(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("LoadWorkspaceRC returned nil")
	}

	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1", len(rc.Prompts))
	}

	if rc.Conversations == nil {
		t.Fatal("Conversations is nil")
	}
	if len(rc.Conversations.Processing.Processors) != 1 {
		t.Fatalf("Processors count = %d, want 1", len(rc.Conversations.Processing.Processors))
	}
}
