package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePromptFile_WithFrontMatter(t *testing.T) {
	data := []byte(`name: "Test Prompt"
description: "A test prompt"
backgroundColor: "#E8F5E9"
icon: "code"
tags: ["test", "example"]
prompt: |
  This is the prompt content.

  It can span multiple lines.
`)

	prompt, err := ParsePromptFile("test.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Test Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Test Prompt")
	}
	if prompt.Description != "A test prompt" {
		t.Errorf("Description = %q, want %q", prompt.Description, "A test prompt")
	}
	if prompt.BackgroundColor != "#E8F5E9" {
		t.Errorf("BackgroundColor = %q, want %q", prompt.BackgroundColor, "#E8F5E9")
	}
	if prompt.Icon != "code" {
		t.Errorf("Icon = %q, want %q", prompt.Icon, "code")
	}
	if len(prompt.Tags) != 2 || prompt.Tags[0] != "test" || prompt.Tags[1] != "example" {
		t.Errorf("Tags = %v, want [test example]", prompt.Tags)
	}
	wantContent := "This is the prompt content.\n\nIt can span multiple lines.\n"
	if prompt.Content != wantContent {
		t.Errorf("Content = %q, want %q", prompt.Content, wantContent)
	}
	if !prompt.IsEnabled() {
		t.Error("IsEnabled() = false, want true (default)")
	}
}

func TestParsePromptFile_NameFromFilenameNoNameField(t *testing.T) {
	data := []byte(`prompt: |
  This is just content with no name.

  Multiple lines work too.
`)

	prompt, err := ParsePromptFile("my-prompt.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	// Name should be derived from filename (strips .prompt.yaml)
	if prompt.Name != "my-prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "my-prompt")
	}
	wantContent := "This is just content with no name.\n\nMultiple lines work too.\n"
	if prompt.Content != wantContent {
		t.Errorf("Content = %q, want %q", prompt.Content, wantContent)
	}
}

func TestParsePromptFile_DisabledPrompt(t *testing.T) {
	data := []byte(`name: "Disabled Prompt"
enabled: false
prompt: |
  This prompt is disabled.
`)

	prompt, err := ParsePromptFile("disabled.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.IsEnabled() {
		t.Error("IsEnabled() = true, want false")
	}
}

func TestParsePromptFile_NameFromFilename(t *testing.T) {
	data := []byte(`description: "No name specified"
prompt: |
  Content here.
`)

	prompt, err := ParsePromptFile("code-review.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "code-review" {
		t.Errorf("Name = %q, want %q", prompt.Name, "code-review")
	}
}

func TestParsePromptFile_SubdirectoryPath(t *testing.T) {
	data := []byte(`name: "Git Commit"
prompt: |
  Write a commit message.
`)

	prompt, err := ParsePromptFile("git/commit.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Path != "git/commit.prompt.yaml" {
		t.Errorf("Path = %q, want %q", prompt.Path, "git/commit.prompt.yaml")
	}
	if prompt.Name != "Git Commit" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Git Commit")
	}
}

func TestToWebPrompt(t *testing.T) {
	prompt := &PromptFile{
		Name:            "Test",
		Content:         "Content here",
		BackgroundColor: "#FF0000",
		Icon:            "beads",
		Description:     "Test description",
		Group:           "Testing",
		Menus:           "conversation",
		EnabledWhen:     `acp.matchesServerType(["auggie", "claude-code"])`,
	}

	wp := prompt.ToWebPrompt()

	if wp.Name != "Test" {
		t.Errorf("WebPrompt.Name = %q, want %q", wp.Name, "Test")
	}
	if wp.Prompt != "Content here" {
		t.Errorf("WebPrompt.Prompt = %q, want %q", wp.Prompt, "Content here")
	}
	if wp.BackgroundColor != "#FF0000" {
		t.Errorf("WebPrompt.BackgroundColor = %q, want %q", wp.BackgroundColor, "#FF0000")
	}
	if wp.Icon != "beads" {
		t.Errorf("WebPrompt.Icon = %q, want %q", wp.Icon, "beads")
	}
	if wp.Description != "Test description" {
		t.Errorf("WebPrompt.Description = %q, want %q", wp.Description, "Test description")
	}
	if wp.Group != "Testing" {
		t.Errorf("WebPrompt.Group = %q, want %q", wp.Group, "Testing")
	}
	if wp.Menus != "conversation" {
		t.Errorf("WebPrompt.Menus = %q, want %q", wp.Menus, "conversation")
	}
	// File-based prompts should have Source=PromptSourceFile
	if wp.Source != PromptSourceFile {
		t.Errorf("WebPrompt.Source = %q, want %q", wp.Source, PromptSourceFile)
	}
	// EnabledWhen CEL expression should be passed through
	wantEnabledWhen := `acp.matchesServerType(["auggie", "claude-code"])`
	if wp.EnabledWhen != wantEnabledWhen {
		t.Errorf("WebPrompt.EnabledWhen = %q, want %q", wp.EnabledWhen, wantEnabledWhen)
	}
}

func TestParsePromptFile_WithPeriodic(t *testing.T) {
	data := []byte(`name: "Daily Standup"
description: "Run daily standup"
periodic:
  value: 1
  unit: days
  at: "09:00"
prompt: |
  Run the daily standup.
`)

	prompt, err := ParsePromptFile("daily-standup.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Daily Standup" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Daily Standup")
	}
	if prompt.Periodic == nil {
		t.Fatal("Periodic = nil, want non-nil")
	}
	if prompt.Periodic.Value != 1 {
		t.Errorf("Periodic.Value = %d, want 1", prompt.Periodic.Value)
	}
	if prompt.Periodic.Unit != "days" {
		t.Errorf("Periodic.Unit = %q, want %q", prompt.Periodic.Unit, "days")
	}
	if prompt.Periodic.At != "09:00" {
		t.Errorf("Periodic.At = %q, want %q", prompt.Periodic.At, "09:00")
	}

	// Verify ToWebPrompt carries the Periodic field.
	wp := prompt.ToWebPrompt()
	if wp.Periodic == nil {
		t.Fatal("WebPrompt.Periodic = nil, want non-nil after ToWebPrompt()")
	}
	if wp.Periodic.Value != 1 {
		t.Errorf("WebPrompt.Periodic.Value = %d, want 1", wp.Periodic.Value)
	}
	if wp.Periodic.Unit != "days" {
		t.Errorf("WebPrompt.Periodic.Unit = %q, want %q", wp.Periodic.Unit, "days")
	}
	if wp.Periodic.At != "09:00" {
		t.Errorf("WebPrompt.Periodic.At = %q, want %q", wp.Periodic.At, "09:00")
	}
}

func TestParsePromptFile_WithPeriodic_NoAt(t *testing.T) {
	data := []byte(`name: "Hourly Check"
periodic:
  value: 2
  unit: hours
prompt: |
  Check every 2 hours.
`)

	prompt, err := ParsePromptFile("hourly.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Periodic == nil {
		t.Fatal("Periodic = nil, want non-nil")
	}
	if prompt.Periodic.Value != 2 {
		t.Errorf("Periodic.Value = %d, want 2", prompt.Periodic.Value)
	}
	if prompt.Periodic.Unit != "hours" {
		t.Errorf("Periodic.Unit = %q, want %q", prompt.Periodic.Unit, "hours")
	}
	if prompt.Periodic.At != "" {
		t.Errorf("Periodic.At = %q, want empty (no at for hours)", prompt.Periodic.At)
	}
}

func TestParsePromptFile_WithPeriodic_MaxIterations(t *testing.T) {
	data := []byte(`name: "Capped"
periodic:
  value: 1
  unit: hours
  maxIterations: 5
prompt: |
  do thing
`)

	prompt, err := ParsePromptFile("capped.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Periodic == nil {
		t.Fatal("Periodic = nil, want non-nil")
	}
	if prompt.Periodic.MaxIterations != 5 {
		t.Errorf("Periodic.MaxIterations = %d, want 5", prompt.Periodic.MaxIterations)
	}

	// Verify ToWebPrompt carries the MaxIterations field.
	wp := prompt.ToWebPrompt()
	if wp.Periodic == nil {
		t.Fatal("WebPrompt.Periodic = nil, want non-nil after ToWebPrompt()")
	}
	if wp.Periodic.MaxIterations != 5 {
		t.Errorf("WebPrompt.Periodic.MaxIterations = %d, want 5", wp.Periodic.MaxIterations)
	}
}

func TestParsePromptFile_NoPeriodic(t *testing.T) {
	data := []byte(`name: "One-time Prompt"
prompt: |
  Just a regular prompt.
`)

	prompt, err := ParsePromptFile("one-time.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if prompt.Periodic != nil {
		t.Errorf("Periodic = %+v, want nil for prompt without periodic field", prompt.Periodic)
	}

	wp := prompt.ToWebPrompt()
	if wp.Periodic != nil {
		t.Errorf("WebPrompt.Periodic = %+v, want nil", wp.Periodic)
	}
}

func TestMergePrompts_PreservesPeriodicField(t *testing.T) {
	periodic := &PromptPeriodic{Value: 3, Unit: "hours"}
	globalPrompts := []WebPrompt{
		{Name: "Periodic Prompt", Prompt: "do it", Periodic: periodic, Source: PromptSourceFile},
		{Name: "Regular Prompt", Prompt: "also do it", Source: PromptSourceFile},
	}

	// MergePrompts should carry the Periodic field through.
	merged := MergePrompts(globalPrompts, nil, nil)

	var found *WebPrompt
	for i := range merged {
		if merged[i].Name == "Periodic Prompt" {
			found = &merged[i]
			break
		}
	}
	if found == nil {
		t.Fatal("Periodic Prompt not found in merged result")
	}
	if found.Periodic == nil {
		t.Fatal("merged Periodic Prompt has nil Periodic field, want non-nil")
	}
	if found.Periodic.Value != 3 {
		t.Errorf("merged Periodic.Value = %d, want 3", found.Periodic.Value)
	}
	if found.Periodic.Unit != "hours" {
		t.Errorf("merged Periodic.Unit = %q, want hours", found.Periodic.Unit)
	}
}

func TestLoadPromptsFromDir(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	// Create root prompt
	rootPrompt := `name: "Root Prompt"
prompt: |
  Root content.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "root.prompt.yaml"), []byte(rootPrompt), 0644); err != nil {
		t.Fatalf("Failed to write root.prompt.yaml: %v", err)
	}

	// Create subdirectory with prompt
	subDir := filepath.Join(tmpDir, "git")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	subPrompt := `name: "Git Commit"
backgroundColor: "#E8F5E9"
prompt: |
  Write a commit message.
`
	if err := os.WriteFile(filepath.Join(subDir, "commit.prompt.yaml"), []byte(subPrompt), 0644); err != nil {
		t.Fatalf("Failed to write commit.prompt.yaml: %v", err)
	}

	// Create disabled prompt (should be excluded)
	disabledPrompt := `name: "Disabled"
enabled: false
prompt: |
  This should not appear.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "disabled.prompt.yaml"), []byte(disabledPrompt), 0644); err != nil {
		t.Fatalf("Failed to write disabled.prompt.yaml: %v", err)
	}

	// Create non-prompt.yaml file (should be ignored)
	if err := os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("Failed to write readme.txt: %v", err)
	}

	// Load prompts
	prompts, err := LoadPromptsFromDir(tmpDir)
	if err != nil {
		t.Fatalf("LoadPromptsFromDir failed: %v", err)
	}

	// Should have 3 prompts (root + git/commit + disabled)
	// LoadPromptsFromDir now includes disabled prompts so they can participate in merge
	if len(prompts) != 3 {
		t.Errorf("len(prompts) = %d, want 3", len(prompts))
	}

	// Check that we have the expected prompts
	names := make(map[string]bool)
	for _, p := range prompts {
		names[p.Name] = true
	}

	if !names["Root Prompt"] {
		t.Error("Missing 'Root Prompt'")
	}
	if !names["Git Commit"] {
		t.Error("Missing 'Git Commit'")
	}
	if !names["Disabled"] {
		t.Error("Missing 'Disabled' - disabled prompts should be included for merge")
	}

	// Verify the disabled prompt has IsEnabled() == false
	for _, p := range prompts {
		if p.Name == "Disabled" {
			if p.IsEnabled() {
				t.Error("'Disabled' prompt should have IsEnabled() == false")
			}
		}
	}
}

func TestLoadPromptsFromDir_NonExistent(t *testing.T) {
	prompts, err := LoadPromptsFromDir("/nonexistent/path")
	if err != nil {
		t.Fatalf("LoadPromptsFromDir failed: %v", err)
	}
	if prompts != nil {
		t.Errorf("prompts = %v, want nil for non-existent directory", prompts)
	}
}

func TestPromptsToWebPrompts(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "One", Content: "Content 1"},
		{Name: "Two", Content: "Content 2", BackgroundColor: "#FF0000"},
	}

	webPrompts := PromptsToWebPrompts(prompts)

	if len(webPrompts) != 2 {
		t.Fatalf("len(webPrompts) = %d, want 2", len(webPrompts))
	}
	if webPrompts[0].Name != "One" {
		t.Errorf("webPrompts[0].Name = %q, want %q", webPrompts[0].Name, "One")
	}
	if webPrompts[1].BackgroundColor != "#FF0000" {
		t.Errorf("webPrompts[1].BackgroundColor = %q, want %q", webPrompts[1].BackgroundColor, "#FF0000")
	}
}

func TestPromptsToWebPrompts_Empty(t *testing.T) {
	webPrompts := PromptsToWebPrompts(nil)
	if webPrompts != nil {
		t.Errorf("PromptsToWebPrompts(nil) = %v, want nil", webPrompts)
	}

	webPrompts = PromptsToWebPrompts([]*PromptFile{})
	if webPrompts != nil {
		t.Errorf("PromptsToWebPrompts([]) = %v, want nil", webPrompts)
	}
}

func TestParsePromptFile_WithACPs(t *testing.T) {
	data := []byte(`name: "Claude Only Prompt"
enabledWhen: 'acp.matchesServerType("claude-code")'
prompt: |
  This prompt is only for Claude Code.
`)

	prompt, err := ParsePromptFile("claude-only.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Claude Only Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Claude Only Prompt")
	}
	want := `acp.matchesServerType("claude-code")`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestParsePromptFile_WithMultipleACPs(t *testing.T) {
	data := []byte(`name: "Multi ACP Prompt"
enabledWhen: 'acp.matchesServerType(["auggie", "claude-code", "custom-acp"])'
prompt: |
  This prompt works with multiple ACPs.
`)

	prompt, err := ParsePromptFile("multi-acp.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	want := `acp.matchesServerType(["auggie", "claude-code", "custom-acp"])`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestParsePromptFile_WithGroup(t *testing.T) {
	data := []byte(`name: "Test Prompt"
description: "A test prompt"
group: "Testing"
backgroundColor: "#E8F5E9"
prompt: |
  This is a test prompt with a group.
`)

	prompt, err := ParsePromptFile("test.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Test Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Test Prompt")
	}
	if prompt.Group != "Testing" {
		t.Errorf("Group = %q, want %q", prompt.Group, "Testing")
	}
	if prompt.Description != "A test prompt" {
		t.Errorf("Description = %q, want %q", prompt.Description, "A test prompt")
	}
}

func TestParsePromptFile_WithMenus(t *testing.T) {
	data := []byte(`name: "Context Prompt"
group: "Workflow"
menus: conversation
prompt: |
  This prompt appears in the conversation context menu.
`)

	prompt, err := ParsePromptFile("context.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Context Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Context Prompt")
	}
	if prompt.Group != "Workflow" {
		t.Errorf("Group = %q, want %q", prompt.Group, "Workflow")
	}
	if prompt.Menus != "conversation" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "conversation")
	}

	// The menus field must survive conversion to WebPrompt for the API response.
	wp := prompt.ToWebPrompt()
	if wp.Menus != "conversation" {
		t.Errorf("WebPrompt.Menus = %q, want %q", wp.Menus, "conversation")
	}
}

func TestParsePromptFile_WithMultipleMenus(t *testing.T) {
	data := []byte(`name: "Multi Menu Prompt"
menus: "conversation, group"
prompt: |
  This prompt appears in multiple menus.
`)

	prompt, err := ParsePromptFile("multi.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Menus != "conversation, group" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "conversation, group")
	}
}

func TestParsePromptFile_WithoutMenus(t *testing.T) {
	data := []byte(`name: "Plain Prompt"
prompt: |
  A prompt without a menus attribute.
`)

	prompt, err := ParsePromptFile("plain.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Menus != "" {
		t.Errorf("Menus = %q, want empty", prompt.Menus)
	}
}

func TestIsSpecificToACP(t *testing.T) {
	tests := []struct {
		name        string
		enabledWhen string
		acpServer   string
		want        bool
	}{
		{"empty enabledWhen is not specific", "", "auggie", false},
		{"empty ACP server", `acp.matchesServerType("auggie")`, "", false},
		{"exact match single", `acp.matchesServerType("auggie")`, "auggie", true},
		{"case insensitive match", `acp.matchesServerType("Auggie")`, "auggie", true},
		{"no match", `acp.matchesServerType("claude-code")`, "auggie", false},
		{"multiple ACPs with match", `acp.matchesServerType(["claude-code", "auggie"])`, "auggie", true},
		{"multiple ACPs without match", `acp.matchesServerType(["claude-code", "other"])`, "auggie", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PromptFile{EnabledWhen: tt.enabledWhen}
			got := p.IsSpecificToACP(tt.acpServer)
			if got != tt.want {
				t.Errorf("IsSpecificToACP(%q) = %v, want %v", tt.acpServer, got, tt.want)
			}
		})
	}
}

func TestCollectRequiredToolPatterns(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "P1", EnabledWhen: `tools.hasAllPatterns(["jira_*", "slack_*"])`},
		{Name: "P2", EnabledWhen: `tools.hasAllPatterns(["jira_*", "github_*"])`},
		{Name: "P3", EnabledWhen: ""},
		{Name: "P4", EnabledWhen: `tools.hasPattern("slack_*")`},
	}

	patterns := CollectRequiredToolPatterns(prompts)

	// Should have jira_*, slack_*, github_* (deduplicated)
	if len(patterns) != 3 {
		t.Errorf("CollectRequiredToolPatterns returned %d patterns, want 3: %v", len(patterns), patterns)
	}

	seen := make(map[string]bool)
	for _, p := range patterns {
		seen[p] = true
	}

	for _, expected := range []string{"jira_*", "slack_*", "github_*"} {
		if !seen[expected] {
			t.Errorf("CollectRequiredToolPatterns missing pattern %q", expected)
		}
	}
}

func TestCollectRequiredToolPatterns_Empty(t *testing.T) {
	// All prompts have no required tools
	prompts := []*PromptFile{
		{Name: "P1", EnabledWhen: ""},
		{Name: "P2"},
	}
	patterns := CollectRequiredToolPatterns(prompts)
	if len(patterns) != 0 {
		t.Errorf("CollectRequiredToolPatterns with no required tools returned %v, want empty", patterns)
	}

	// Nil slice
	patterns = CollectRequiredToolPatterns(nil)
	if len(patterns) != 0 {
		t.Errorf("CollectRequiredToolPatterns(nil) returned %v, want empty", patterns)
	}
}

func TestParsePromptFile_WithEnabledWhenTools(t *testing.T) {
	data := []byte(`name: "Jira Prompt"
enabledWhen: 'tools.hasAllPatterns(["jira_*", "slack_*"])'
prompt: |
  This prompt requires Jira and Slack tools.
`)

	prompt, err := ParsePromptFile("jira-prompt.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "Jira Prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "Jira Prompt")
	}
	want := `tools.hasAllPatterns(["jira_*", "slack_*"])`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestToWebPrompt_IncludesEnabledWhen(t *testing.T) {
	prompt := &PromptFile{
		Name:        "Test",
		Content:     "Content here",
		EnabledWhen: `acp.matchesServerType("auggie") && tools.hasAllPatterns(["jira_*", "slack_*"])`,
	}

	wp := prompt.ToWebPrompt()

	want := `acp.matchesServerType("auggie") && tools.hasAllPatterns(["jira_*", "slack_*"])`
	if wp.EnabledWhen != want {
		t.Errorf("WebPrompt.EnabledWhen = %q, want %q", wp.EnabledWhen, want)
	}
	if wp.Source != PromptSourceFile {
		t.Errorf("WebPrompt.Source = %q, want %q", wp.Source, PromptSourceFile)
	}
}

func TestFilterPromptsSpecificToACP(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "All ACPs", EnabledWhen: ""},
		{Name: "Claude Only", EnabledWhen: `acp.matchesServerType("claude-code")`},
		{Name: "Auggie Only", EnabledWhen: `acp.matchesServerType("auggie")`},
		{Name: "Both", EnabledWhen: `acp.matchesServerType(["claude-code", "auggie"])`},
	}

	// Filter for auggie - should only get prompts with explicit acp filter in enabledWhen
	filtered := FilterPromptsSpecificToACP(prompts, "auggie")
	if len(filtered) != 2 {
		t.Errorf("FilterPromptsSpecificToACP(auggie) returned %d prompts, want 2", len(filtered))
	}

	// Check that "All ACPs" and "Claude Only" are not in the filtered list
	for _, p := range filtered {
		if p.Name == "All ACPs" {
			t.Error("FilterPromptsSpecificToACP(auggie) should not include 'All ACPs' (generic prompt)")
		}
		if p.Name == "Claude Only" {
			t.Error("FilterPromptsSpecificToACP(auggie) should not include 'Claude Only'")
		}
	}

	// Filter for claude-code
	filtered = FilterPromptsSpecificToACP(prompts, "claude-code")
	if len(filtered) != 2 {
		t.Errorf("FilterPromptsSpecificToACP(claude-code) returned %d prompts, want 2", len(filtered))
	}

	// Empty ACP server should return nil
	filtered = FilterPromptsSpecificToACP(prompts, "")
	if filtered != nil {
		t.Errorf("FilterPromptsSpecificToACP('') = %v, want nil", filtered)
	}

	// Empty prompts should return nil
	filtered = FilterPromptsSpecificToACP([]*PromptFile{}, "auggie")
	if filtered != nil {
		t.Errorf("FilterPromptsSpecificToACP on empty slice = %v, want nil", filtered)
	}

	// Nil prompts should return nil
	filtered = FilterPromptsSpecificToACP(nil, "auggie")
	if filtered != nil {
		t.Errorf("FilterPromptsSpecificToACP(nil) = %v, want nil", filtered)
	}
}

func TestParsePromptFile_WithPeriodic_OnCompletion(t *testing.T) {
	data := []byte(`name: "On Completion Prompt"
periodic:
  trigger: onCompletion
  delay: 10
  maxDuration: "2h"
prompt: |
  Fire after agent stops.
`)

	prompt, err := ParsePromptFile("on-completion.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Periodic == nil {
		t.Fatal("Periodic = nil, want non-nil")
	}
	if prompt.Periodic.Trigger != "onCompletion" {
		t.Errorf("Periodic.Trigger = %q, want %q", prompt.Periodic.Trigger, "onCompletion")
	}
	if prompt.Periodic.Delay != 10 {
		t.Errorf("Periodic.Delay = %d, want 10", prompt.Periodic.Delay)
	}
	if prompt.Periodic.MaxDuration != "2h" {
		t.Errorf("Periodic.MaxDuration = %q, want %q", prompt.Periodic.MaxDuration, "2h")
	}
	// value/unit absent → zero values
	if prompt.Periodic.Value != 0 {
		t.Errorf("Periodic.Value = %d, want 0 (not set)", prompt.Periodic.Value)
	}
	if prompt.Periodic.Unit != "" {
		t.Errorf("Periodic.Unit = %q, want empty (not set)", prompt.Periodic.Unit)
	}
}

func TestParsePromptFile_WithPeriodic_ScheduleNoTrigger(t *testing.T) {
	data := []byte(`name: "Schedule Prompt"
periodic:
  value: 2
  unit: hours
  maxIterations: 5
prompt: |
  Run every 2 hours.
`)

	prompt, err := ParsePromptFile("schedule.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Periodic == nil {
		t.Fatal("Periodic = nil, want non-nil")
	}
	// Trigger absent → empty string (schedule default)
	if prompt.Periodic.Trigger != "" {
		t.Errorf("Periodic.Trigger = %q, want empty (schedule default)", prompt.Periodic.Trigger)
	}
	if prompt.Periodic.Value != 2 {
		t.Errorf("Periodic.Value = %d, want 2", prompt.Periodic.Value)
	}
	if prompt.Periodic.Unit != "hours" {
		t.Errorf("Periodic.Unit = %q, want %q", prompt.Periodic.Unit, "hours")
	}
	if prompt.Periodic.MaxIterations != 5 {
		t.Errorf("Periodic.MaxIterations = %d, want 5", prompt.Periodic.MaxIterations)
	}
	if prompt.Periodic.Delay != 0 {
		t.Errorf("Periodic.Delay = %d, want 0 (not set)", prompt.Periodic.Delay)
	}
	if prompt.Periodic.MaxDuration != "" {
		t.Errorf("Periodic.MaxDuration = %q, want empty (not set)", prompt.Periodic.MaxDuration)
	}
}

func TestToWebPrompt_OnCompletion_JSONRoundTrip(t *testing.T) {
	pf := &PromptFile{
		Name:    "On Completion",
		Content: "body",
		Periodic: &PromptPeriodic{
			Trigger:     "onCompletion",
			Delay:       10,
			MaxDuration: "2h",
		},
	}

	wp := pf.ToWebPrompt()
	if wp.Periodic == nil {
		t.Fatal("WebPrompt.Periodic = nil, want non-nil")
	}

	raw, err := json.Marshal(wp)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(raw)

	if !strings.Contains(jsonStr, `"trigger":"onCompletion"`) {
		t.Errorf("JSON missing trigger field; got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"delay":10`) {
		t.Errorf("JSON missing delay field; got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"maxDuration":"2h"`) {
		t.Errorf("JSON missing maxDuration field; got: %s", jsonStr)
	}

	// Also verify via struct fields.
	if wp.Periodic.Trigger != "onCompletion" {
		t.Errorf("WebPrompt.Periodic.Trigger = %q, want %q", wp.Periodic.Trigger, "onCompletion")
	}
	if wp.Periodic.Delay != 10 {
		t.Errorf("WebPrompt.Periodic.Delay = %d, want 10", wp.Periodic.Delay)
	}
	if wp.Periodic.MaxDuration != "2h" {
		t.Errorf("WebPrompt.Periodic.MaxDuration = %q, want %q", wp.Periodic.MaxDuration, "2h")
	}
}

// ---- PromptParameter / Parameters field tests ----

func TestIsKnownPromptParameterType(t *testing.T) {
	for _, known := range KnownPromptParameterTypes {
		if !IsKnownPromptParameterType(known) {
			t.Errorf("IsKnownPromptParameterType(%q) = false, want true", known)
		}
	}
	if IsKnownPromptParameterType("unknown") {
		t.Error("IsKnownPromptParameterType(\"unknown\") = true, want false")
	}
	if IsKnownPromptParameterType("") {
		t.Error("IsKnownPromptParameterType(\"\") = true, want false")
	}
}

func TestParsePromptFile_WithParameters(t *testing.T) {
	reqTrue := true
	data := []byte(`name: "Task Prompt"
parameters:
  - name: id
    type: beadsId
    description: the task ID
    required: true
  - name: folder
    type: workspaceFolder
prompt: |
  Work on ${id} in ${folder}.
`)

	prompt, err := ParsePromptFile("task.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if len(prompt.Parameters) != 2 {
		t.Fatalf("len(Parameters) = %d, want 2", len(prompt.Parameters))
	}

	p0 := prompt.Parameters[0]
	if p0.Name != "id" {
		t.Errorf("Parameters[0].Name = %q, want %q", p0.Name, "id")
	}
	if p0.Type != "beadsId" {
		t.Errorf("Parameters[0].Type = %q, want %q", p0.Type, "beadsId")
	}
	if p0.Description != "the task ID" {
		t.Errorf("Parameters[0].Description = %q, want %q", p0.Description, "the task ID")
	}
	if p0.Required == nil || *p0.Required != reqTrue {
		t.Errorf("Parameters[0].Required = %v, want *true", p0.Required)
	}

	p1 := prompt.Parameters[1]
	if p1.Name != "folder" {
		t.Errorf("Parameters[1].Name = %q, want %q", p1.Name, "folder")
	}
	if p1.Type != "workspaceFolder" {
		t.Errorf("Parameters[1].Type = %q, want %q", p1.Type, "workspaceFolder")
	}
	if p1.Required != nil {
		t.Errorf("Parameters[1].Required = %v, want nil (absent)", p1.Required)
	}
}

func TestToWebPrompt_RoundTripsParameters(t *testing.T) {
	req := true
	pf := &PromptFile{
		Name:    "Param Prompt",
		Content: "body",
		Parameters: []PromptParameter{
			{Name: "id", Type: "beadsId", Description: "task id", Required: &req},
			{Name: "note", Type: "text"},
		},
	}

	wp := pf.ToWebPrompt()

	if len(wp.Parameters) != 2 {
		t.Fatalf("WebPrompt.Parameters len = %d, want 2", len(wp.Parameters))
	}
	if wp.Parameters[0].Name != "id" || wp.Parameters[0].Type != "beadsId" {
		t.Errorf("WebPrompt.Parameters[0] = %+v, want {id beadsId}", wp.Parameters[0])
	}
	if wp.Parameters[0].Required == nil || !*wp.Parameters[0].Required {
		t.Errorf("WebPrompt.Parameters[0].Required = %v, want *true", wp.Parameters[0].Required)
	}
	if wp.Parameters[1].Name != "note" || wp.Parameters[1].Type != "text" {
		t.Errorf("WebPrompt.Parameters[1] = %+v, want {note text}", wp.Parameters[1])
	}
}

func TestParsePromptFile_UnknownParameterType(t *testing.T) {
	data := []byte(`name: "Bad Prompt"
parameters:
  - name: foo
    type: notAType
prompt: |
  body
`)

	_, err := ParsePromptFile("bad.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for unknown parameter type, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %q, want it to mention 'unknown type'", err.Error())
	}
}

func TestParsePromptFile_EmptyParameterName(t *testing.T) {
	data := []byte(`name: "Bad Prompt"
parameters:
  - name: ""
    type: text
prompt: |
  body
`)

	_, err := ParsePromptFile("bad.prompt.yaml", data, time.Now())
	if err == nil {
		t.Fatal("ParsePromptFile should fail for empty parameter name, got nil error")
	}
	if !strings.Contains(err.Error(), "name must not be empty") {
		t.Errorf("error = %q, want it to mention 'name must not be empty'", err.Error())
	}
}

func TestParsePromptFile_NoParameters(t *testing.T) {
	data := []byte(`name: "Simple Prompt"
prompt: |
  No params here.
`)

	prompt, err := ParsePromptFile("simple.prompt.yaml", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}
	if len(prompt.Parameters) != 0 {
		t.Errorf("Parameters = %v, want empty", prompt.Parameters)
	}
}

func TestValidatePromptParameters(t *testing.T) {
	t.Run("empty name returns error containing 'name must not be empty'", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "", Type: "text"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "name must not be empty") {
			t.Errorf("error = %q, want it to contain 'name must not be empty'", err.Error())
		}
	})

	t.Run("unknown type returns error containing 'unknown type'", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "x", Type: "notAType"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "unknown type") {
			t.Errorf("error = %q, want it to contain 'unknown type'", err.Error())
		}
	})

	t.Run("childSessionId with empty menus is OK", func(t *testing.T) {
		err := ValidatePromptParameters("", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=prompts is OK", func(t *testing.T) {
		err := ValidatePromptParameters("prompts", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=conversation is OK", func(t *testing.T) {
		err := ValidatePromptParameters("conversation", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=prompts,conversation is OK", func(t *testing.T) {
		err := ValidatePromptParameters("prompts, conversation", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("childSessionId with menus=beadsList returns error mentioning childSessionId and beadsList", func(t *testing.T) {
		err := ValidatePromptParameters("beadsList", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "childSessionId") {
			t.Errorf("error = %q, want it to contain 'childSessionId'", err.Error())
		}
		if !strings.Contains(err.Error(), "beadsList") {
			t.Errorf("error = %q, want it to contain 'beadsList'", err.Error())
		}
	})

	t.Run("childSessionId with menus=conversation,beadsList returns error", func(t *testing.T) {
		err := ValidatePromptParameters("conversation, beadsList", []PromptParameter{{Name: "s", Type: "childSessionId"}})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "childSessionId") {
			t.Errorf("error = %q, want it to contain 'childSessionId'", err.Error())
		}
	})

	t.Run("non-childSessionId param with beadsList menus is OK", func(t *testing.T) {
		err := ValidatePromptParameters("beadsList", []PromptParameter{{Name: "x", Type: "text"}})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestParsePromptFile_ChildSessionId(t *testing.T) {
	tests := []struct {
		name    string
		menus   string
		wantErr bool
	}{
		{"no menus line is OK", "", false},
		{"menus=prompts is OK", "prompts", false},
		{"menus=conversation is OK", "conversation", false},
		{"menus=beadsList errors with childSessionId mention", "beadsList", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			menusLine := ""
			if tc.menus != "" {
				menusLine = "menus: " + tc.menus + "\n"
			}
			data := []byte("name: \"Test\"\n" + menusLine + "parameters:\n  - name: child\n    type: childSessionId\nprompt: |\n  body\n")
			_, err := ParsePromptFile("test.prompt.yaml", data, time.Now())
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "childSessionId") {
					t.Errorf("error = %q, want it to contain 'childSessionId'", err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestWorkspaceRC_SkipsInvalidChildSessionIdPrompt(t *testing.T) {
	yaml := `
prompts:
  - name: "Valid Prompt"
    prompt: "do something"
    menus: conversation
    parameters:
      - name: child
        type: childSessionId
  - name: "Invalid Prompt"
    prompt: "do something else"
    menus: beadsList
    parameters:
      - name: child
        type: childSessionId
`
	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}
	if len(rc.Prompts) != 1 {
		t.Errorf("Prompts count = %d, want 1 (invalid prompt should be skipped)", len(rc.Prompts))
	}
	if len(rc.Prompts) > 0 && rc.Prompts[0].Name != "Valid Prompt" {
		t.Errorf("Prompts[0].Name = %q, want %q", rc.Prompts[0].Name, "Valid Prompt")
	}
}

func TestMigrateMarkdownPromptsInDir(t *testing.T) {
	dir := t.TempDir()

	// Legacy .md prompt with YAML front-matter + markdown body.
	legacy := `---
name: "CSO: latest report"
description: "Generate a report"
backgroundColor: "#C8E6C9"
group: "CSOs"
---

# CSO Latest Report

Read the latest messages and generate a report.
`
	mdPath := filepath.Join(dir, "cso-latest.md")
	if err := os.WriteFile(mdPath, []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	// A legacy file without front-matter: whole body is the content.
	plainPath := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(plainPath, []byte("Just the body.\n"), 0644); err != nil {
		t.Fatalf("write plain file: %v", err)
	}

	migrated, err := MigrateMarkdownPromptsInDir(dir)
	if err != nil {
		t.Fatalf("MigrateMarkdownPromptsInDir failed: %v", err)
	}
	if len(migrated) != 2 {
		t.Fatalf("migrated count = %d, want 2", len(migrated))
	}

	// Original .md files are kept.
	if _, err := os.Stat(mdPath); err != nil {
		t.Errorf("legacy .md should be kept: %v", err)
	}

	// New .prompt.yaml written and parses back into the expected prompt.
	p, err := LoadPromptFile(dir, "cso-latest.prompt.yaml")
	if err != nil {
		t.Fatalf("load migrated file: %v", err)
	}
	if p.Name != "CSO: latest report" {
		t.Errorf("Name = %q, want %q", p.Name, "CSO: latest report")
	}
	if p.Group != "CSOs" {
		t.Errorf("Group = %q, want %q", p.Group, "CSOs")
	}
	if p.BackgroundColor != "#C8E6C9" {
		t.Errorf("BackgroundColor = %q, want %q", p.BackgroundColor, "#C8E6C9")
	}
	wantBody := "# CSO Latest Report\n\nRead the latest messages and generate a report."
	if p.Content != wantBody {
		t.Errorf("Content = %q, want %q", p.Content, wantBody)
	}

	// Plain file: name derived from filename, whole file is content.
	plain, err := LoadPromptFile(dir, "plain.prompt.yaml")
	if err != nil {
		t.Fatalf("load migrated plain file: %v", err)
	}
	if plain.Name != "plain" {
		t.Errorf("plain Name = %q, want %q", plain.Name, "plain")
	}
	if plain.Content != "Just the body." {
		t.Errorf("plain Content = %q, want %q", plain.Content, "Just the body.")
	}

	// Idempotency: a second run migrates nothing (targets already exist).
	again, err := MigrateMarkdownPromptsInDir(dir)
	if err != nil {
		t.Fatalf("second MigrateMarkdownPromptsInDir failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second run migrated %d, want 0", len(again))
	}

	// Non-existent directory is treated as empty (no error).
	none, err := MigrateMarkdownPromptsInDir(filepath.Join(dir, "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if none != nil {
		t.Errorf("missing dir migrated = %v, want nil", none)
	}
}

// TestMigrateMarkdownPromptsInDir_LiteralBlock verifies that a multi-line body
// containing emoji (4-byte UTF-8 runes) is written as a readable literal block
// scalar with the emoji preserved, rather than the escaped double-quoted scalar
// that yaml.v3 emits by default, and that it round-trips back to the original.
func TestMigrateMarkdownPromptsInDir_LiteralBlock(t *testing.T) {
	dir := t.TempDir()

	body := "# Title\n\nLine with emoji 🔴 and a ▶️ button.\n\n- item one\n- item two"
	legacy := "---\nname: \"Emoji Prompt\"\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "emoji.md"), []byte(legacy), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	migrated, err := MigrateMarkdownPromptsInDir(dir)
	if err != nil {
		t.Fatalf("MigrateMarkdownPromptsInDir failed: %v", err)
	}
	if len(migrated) != 1 {
		t.Fatalf("migrated count = %d, want 1", len(migrated))
	}

	raw, err := os.ReadFile(filepath.Join(dir, "emoji.prompt.yaml"))
	if err != nil {
		t.Fatalf("read migrated file: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "prompt: |-") {
		t.Errorf("expected literal block scalar, got:\n%s", text)
	}
	if strings.Contains(text, "\\U0001F534") || strings.Contains(text, `prompt: "`) {
		t.Errorf("body should not be an escaped double-quoted scalar, got:\n%s", text)
	}
	if !strings.Contains(text, "🔴") {
		t.Errorf("emoji should be preserved verbatim, got:\n%s", text)
	}

	p, err := LoadPromptFile(dir, "emoji.prompt.yaml")
	if err != nil {
		t.Fatalf("load migrated file: %v", err)
	}
	if p.Content != body {
		t.Errorf("Content round-trip = %q, want %q", p.Content, body)
	}
}
