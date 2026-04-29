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

func TestParseWorkspaceRC_DisabledPromptNoContent(t *testing.T) {
	yaml := `
prompts:
  - name: "Add tests"
    enabled: false
  - name: "Active Prompt"
    prompt: "Do something"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if len(rc.Prompts) != 2 {
		t.Fatalf("len(rc.Prompts) = %d, want 2", len(rc.Prompts))
	}

	// Disabled prompt should be included even without prompt text
	disabled := rc.Prompts[0]
	if disabled.Name != "Add tests" {
		t.Errorf("disabled prompt name = %q, want %q", disabled.Name, "Add tests")
	}
	if disabled.Enabled == nil || *disabled.Enabled {
		t.Error("disabled prompt should have Enabled = false")
	}
	if disabled.Prompt != "" {
		t.Errorf("disabled prompt should have empty Prompt, got %q", disabled.Prompt)
	}

	// Active prompt should be included normally
	active := rc.Prompts[1]
	if active.Name != "Active Prompt" {
		t.Errorf("active prompt name = %q, want %q", active.Name, "Active Prompt")
	}
	if active.Enabled != nil {
		t.Error("active prompt should have nil Enabled (defaults to true)")
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

func TestParseWorkspaceRC_ProcessorsDirs(t *testing.T) {
	yaml := `
processors_dirs:
  - ".processors"
  - "team/shared-processors"
prompts:
  - name: "Test"
    text: "test prompt"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC error: %v", err)
	}
	if len(rc.ProcessorsDirs) != 2 {
		t.Fatalf("ProcessorsDirs count = %d, want 2", len(rc.ProcessorsDirs))
	}
	if rc.ProcessorsDirs[0] != ".processors" {
		t.Errorf("ProcessorsDirs[0] = %q, want %q", rc.ProcessorsDirs[0], ".processors")
	}
	if rc.ProcessorsDirs[1] != "team/shared-processors" {
		t.Errorf("ProcessorsDirs[1] = %q, want %q", rc.ProcessorsDirs[1], "team/shared-processors")
	}
}

func TestParseWorkspaceRC_EmptyProcessorsDirs(t *testing.T) {
	yaml := `
processors_dirs: []
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC error: %v", err)
	}
	if len(rc.ProcessorsDirs) != 0 {
		t.Errorf("ProcessorsDirs count = %d, want 0", len(rc.ProcessorsDirs))
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

func TestParseWorkspaceRC_UserDataSchema(t *testing.T) {
	yaml := `
metadata:
  user_data:
    - name: "JIRA ticket"
      description: "The JIRA ticket for tracking"
      type: url
    - name: "Description"
      type: string
    - name: "Notes"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	if rc.Metadata == nil {
		t.Fatal("Metadata is nil")
	}

	if rc.Metadata.UserDataSchema == nil {
		t.Fatal("Metadata.UserDataSchema is nil")
	}

	if len(rc.Metadata.UserDataSchema.Fields) != 3 {
		t.Fatalf("Metadata.UserDataSchema.Fields count = %d, want 3", len(rc.Metadata.UserDataSchema.Fields))
	}

	// Check first field (JIRA ticket with url type and description)
	if rc.Metadata.UserDataSchema.Fields[0].Name != "JIRA ticket" {
		t.Errorf("Field 0 name = %q, want %q", rc.Metadata.UserDataSchema.Fields[0].Name, "JIRA ticket")
	}
	if rc.Metadata.UserDataSchema.Fields[0].Type != UserDataTypeURL {
		t.Errorf("Field 0 type = %q, want %q", rc.Metadata.UserDataSchema.Fields[0].Type, UserDataTypeURL)
	}
	if rc.Metadata.UserDataSchema.Fields[0].Description != "The JIRA ticket for tracking" {
		t.Errorf("Field 0 description = %q, want %q", rc.Metadata.UserDataSchema.Fields[0].Description, "The JIRA ticket for tracking")
	}

	// Check second field (Description with string type, no description)
	if rc.Metadata.UserDataSchema.Fields[1].Name != "Description" {
		t.Errorf("Field 1 name = %q, want %q", rc.Metadata.UserDataSchema.Fields[1].Name, "Description")
	}
	if rc.Metadata.UserDataSchema.Fields[1].Type != UserDataTypeString {
		t.Errorf("Field 1 type = %q, want %q", rc.Metadata.UserDataSchema.Fields[1].Type, UserDataTypeString)
	}
	// Field without description should be empty
	if rc.Metadata.UserDataSchema.Fields[1].Description != "" {
		t.Errorf("Field 1 description = %q, want empty", rc.Metadata.UserDataSchema.Fields[1].Description)
	}

	// Check third field (Notes with empty type - defaults to string)
	if rc.Metadata.UserDataSchema.Fields[2].Name != "Notes" {
		t.Errorf("Field 2 name = %q, want %q", rc.Metadata.UserDataSchema.Fields[2].Name, "Notes")
	}
	// Empty type is stored as-is; DefaultType() handles the conversion at validation time
	if rc.Metadata.UserDataSchema.Fields[2].Type != "" {
		t.Errorf("Field 2 type = %q, want empty string", rc.Metadata.UserDataSchema.Fields[2].Type)
	}
}

func TestParseWorkspaceRC_Metadata(t *testing.T) {
	yaml := `
metadata:
  description: |-
    Mitto is a multi-agent coordination system.
    It supports multiple workspaces.
  url: https://github.com/inercia/mitto/
  group: MyGroup
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}
	if rc.Metadata == nil {
		t.Fatal("Metadata is nil, want non-nil")
	}
	if rc.Metadata.Description != "Mitto is a multi-agent coordination system.\nIt supports multiple workspaces." {
		t.Errorf("Description = %q, want multiline description", rc.Metadata.Description)
	}
	if rc.Metadata.URL != "https://github.com/inercia/mitto/" {
		t.Errorf("URL = %q, want %q", rc.Metadata.URL, "https://github.com/inercia/mitto/")
	}
	if rc.Metadata.Group != "MyGroup" {
		t.Errorf("Group = %q, want %q", rc.Metadata.Group, "MyGroup")
	}
}

func TestParseWorkspaceRC_MetadataPartial(t *testing.T) {
	yaml := `
metadata:
  description: "Just a description"
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}
	if rc.Metadata == nil {
		t.Fatal("Metadata is nil, want non-nil")
	}
	if rc.Metadata.Description != "Just a description" {
		t.Errorf("Description = %q, want %q", rc.Metadata.Description, "Just a description")
	}
	if rc.Metadata.URL != "" {
		t.Errorf("URL = %q, want empty", rc.Metadata.URL)
	}
}

func TestParseWorkspaceRC_NoMetadata(t *testing.T) {
	yaml := `
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
	if rc.Metadata != nil {
		t.Errorf("Metadata = %v, want nil", rc.Metadata)
	}
}

func TestParseWorkspaceRC_UserDataSchemaWithProcessors(t *testing.T) {
	yaml := `
conversations:
  processing:
    processors:
      - when: first
        position: before
        text: "System context"
metadata:
  user_data:
    - name: "Priority"
      type: string
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}

	// Check processing config is present
	if rc.Conversations == nil || rc.Conversations.Processing == nil {
		t.Fatal("Conversations.Processing is nil")
	}
	if len(rc.Conversations.Processing.Processors) != 1 {
		t.Fatalf("Processors count = %d, want 1", len(rc.Conversations.Processing.Processors))
	}

	// Check user data schema is present in metadata
	if rc.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	if rc.Metadata.UserDataSchema == nil {
		t.Fatal("Metadata.UserDataSchema is nil")
	}
	if len(rc.Metadata.UserDataSchema.Fields) != 1 {
		t.Fatalf("Metadata.UserDataSchema.Fields count = %d, want 1", len(rc.Metadata.UserDataSchema.Fields))
	}
	if rc.Metadata.UserDataSchema.Fields[0].Name != "Priority" {
		t.Errorf("Field 0 name = %q, want %q", rc.Metadata.UserDataSchema.Fields[0].Name, "Priority")
	}
}

func TestParseWorkspaceRC_UserDataSchemaLegacy(t *testing.T) {
	yaml := `
conversations:
  user_data:
    - name: "JIRA ticket"
      type: url
    - name: "Description"
      type: string
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}
	if rc.Metadata == nil {
		t.Fatal("Metadata is nil, want non-nil (from legacy user_data)")
	}
	if rc.Metadata.UserDataSchema == nil {
		t.Fatal("Metadata.UserDataSchema is nil")
	}
	if len(rc.Metadata.UserDataSchema.Fields) != 2 {
		t.Fatalf("Fields count = %d, want 2", len(rc.Metadata.UserDataSchema.Fields))
	}
	if rc.Metadata.UserDataSchema.Fields[0].Name != "JIRA ticket" {
		t.Errorf("Field 0 name = %q, want %q", rc.Metadata.UserDataSchema.Fields[0].Name, "JIRA ticket")
	}
}

func TestLoadWorkspaceRC_DotMittoDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mittoDir := filepath.Join(tmpDir, ".mitto")
	if err := os.MkdirAll(mittoDir, 0755); err != nil {
		t.Fatalf("Failed to create .mitto dir: %v", err)
	}

	rcContent := `
prompts:
  - name: "DotMitto Prompt"
    prompt: "Loaded from .mitto/mittorc"
`
	rcPath := filepath.Join(mittoDir, "mittorc")
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to write .mitto/mittorc: %v", err)
	}

	rc, err := LoadWorkspaceRC(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("LoadWorkspaceRC returned nil, want config from .mitto/mittorc")
	}
	if len(rc.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(rc.Prompts))
	}
	if rc.Prompts[0].Name != "DotMitto Prompt" {
		t.Errorf("prompt name = %q, want %q", rc.Prompts[0].Name, "DotMitto Prompt")
	}
}

func TestLoadWorkspaceRC_DotMittoDirYaml(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mittoDir := filepath.Join(tmpDir, ".mitto")
	if err := os.MkdirAll(mittoDir, 0755); err != nil {
		t.Fatalf("Failed to create .mitto dir: %v", err)
	}

	rcContent := `
prompts:
  - name: "YAML Prompt"
    prompt: "Loaded from .mitto/mitto.yaml"
`
	rcPath := filepath.Join(mittoDir, "mitto.yaml")
	if err := os.WriteFile(rcPath, []byte(rcContent), 0644); err != nil {
		t.Fatalf("Failed to write .mitto/mitto.yaml: %v", err)
	}

	rc, err := LoadWorkspaceRC(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("LoadWorkspaceRC returned nil, want config from .mitto/mitto.yaml")
	}
	if len(rc.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(rc.Prompts))
	}
	if rc.Prompts[0].Name != "YAML Prompt" {
		t.Errorf("prompt name = %q, want %q", rc.Prompts[0].Name, "YAML Prompt")
	}
}

func TestLoadWorkspaceRC_PriorityOrder(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mittoDir := filepath.Join(tmpDir, ".mitto")
	if err := os.MkdirAll(mittoDir, 0755); err != nil {
		t.Fatalf("Failed to create .mitto dir: %v", err)
	}

	// Create both .mittorc (higher priority) and .mitto/mitto.yaml (lower priority)
	mittoRCContent := `
prompts:
  - name: "DotMittoRC Prompt"
    prompt: "From .mittorc"
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".mittorc"), []byte(mittoRCContent), 0644); err != nil {
		t.Fatalf("Failed to write .mittorc: %v", err)
	}

	yamlContent := `
prompts:
  - name: "YAML Prompt"
    prompt: "From .mitto/mitto.yaml"
`
	if err := os.WriteFile(filepath.Join(mittoDir, "mitto.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write .mitto/mitto.yaml: %v", err)
	}

	rc, err := LoadWorkspaceRC(tmpDir)
	if err != nil {
		t.Fatalf("LoadWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("LoadWorkspaceRC returned nil")
	}
	if len(rc.Prompts) != 1 {
		t.Fatalf("Prompts count = %d, want 1", len(rc.Prompts))
	}
	// .mittorc must win
	if rc.Prompts[0].Name != "DotMittoRC Prompt" {
		t.Errorf("prompt name = %q, want %q (expected .mittorc to win)", rc.Prompts[0].Name, "DotMittoRC Prompt")
	}
}

func TestGetWorkspaceRCModTime_DotMittoDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mitto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mittoDir := filepath.Join(tmpDir, ".mitto")
	if err := os.MkdirAll(mittoDir, 0755); err != nil {
		t.Fatalf("Failed to create .mitto dir: %v", err)
	}

	rcPath := filepath.Join(mittoDir, "mittorc")
	if err := os.WriteFile(rcPath, []byte("prompts: []\n"), 0644); err != nil {
		t.Fatalf("Failed to write .mitto/mittorc: %v", err)
	}

	fi, statErr := os.Stat(rcPath)
	if statErr != nil {
		t.Fatalf("os.Stat failed: %v", statErr)
	}

	modTime := GetWorkspaceRCModTime(tmpDir)
	if modTime.IsZero() {
		t.Error("GetWorkspaceRCModTime returned zero time, want non-zero")
	}
	if !modTime.Equal(fi.ModTime()) {
		t.Errorf("modTime = %v, want %v", modTime, fi.ModTime())
	}
}

func TestParseWorkspaceRC_UserDataSchemaNewOverridesLegacy(t *testing.T) {
	yaml := `
metadata:
  user_data:
    - name: "New Field"
      type: string
conversations:
  user_data:
    - name: "Old Field"
      type: url
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if rc == nil {
		t.Fatal("parseWorkspaceRC returned nil")
	}
	if rc.Metadata == nil || rc.Metadata.UserDataSchema == nil {
		t.Fatal("Metadata.UserDataSchema is nil")
	}
	// New location should win
	if len(rc.Metadata.UserDataSchema.Fields) != 1 {
		t.Fatalf("Fields count = %d, want 1", len(rc.Metadata.UserDataSchema.Fields))
	}
	if rc.Metadata.UserDataSchema.Fields[0].Name != "New Field" {
		t.Errorf("Field 0 name = %q, want %q", rc.Metadata.UserDataSchema.Fields[0].Name, "New Field")
	}
}
