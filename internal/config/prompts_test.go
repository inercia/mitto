package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParsePromptFile_WithFrontMatter(t *testing.T) {
	data := []byte(`---
name: "Test Prompt"
description: "A test prompt"
backgroundColor: "#E8F5E9"
icon: "code"
tags: ["test", "example"]
---

This is the prompt content.

It can span multiple lines.
`)

	prompt, err := ParsePromptFile("test.md", data, time.Now())
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
	if prompt.Content != "This is the prompt content.\n\nIt can span multiple lines." {
		t.Errorf("Content = %q, want multiline content", prompt.Content)
	}
	if !prompt.IsEnabled() {
		t.Error("IsEnabled() = false, want true (default)")
	}
}

func TestParsePromptFile_WithoutFrontMatter(t *testing.T) {
	data := []byte(`This is just content without front-matter.

Multiple lines work too.
`)

	prompt, err := ParsePromptFile("my-prompt.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	// Name should be derived from filename
	if prompt.Name != "my-prompt" {
		t.Errorf("Name = %q, want %q", prompt.Name, "my-prompt")
	}
	if prompt.Content != "This is just content without front-matter.\n\nMultiple lines work too." {
		t.Errorf("Content = %q", prompt.Content)
	}
}

func TestParsePromptFile_DisabledPrompt(t *testing.T) {
	data := []byte(`---
name: "Disabled Prompt"
enabled: false
---

This prompt is disabled.
`)

	prompt, err := ParsePromptFile("disabled.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.IsEnabled() {
		t.Error("IsEnabled() = true, want false")
	}
}

func TestParsePromptFile_NameFromFilename(t *testing.T) {
	data := []byte(`---
description: "No name specified"
---

Content here.
`)

	prompt, err := ParsePromptFile("code-review.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Name != "code-review" {
		t.Errorf("Name = %q, want %q", prompt.Name, "code-review")
	}
}

func TestParsePromptFile_SubdirectoryPath(t *testing.T) {
	data := []byte(`---
name: "Git Commit"
---

Write a commit message.
`)

	prompt, err := ParsePromptFile("git/commit.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Path != "git/commit.md" {
		t.Errorf("Path = %q, want %q", prompt.Path, "git/commit.md")
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

func TestLoadPromptsFromDir(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()

	// Create root prompt
	rootPrompt := `---
name: "Root Prompt"
---

Root content.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "root.md"), []byte(rootPrompt), 0644); err != nil {
		t.Fatalf("Failed to write root.md: %v", err)
	}

	// Create subdirectory with prompt
	subDir := filepath.Join(tmpDir, "git")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	subPrompt := `---
name: "Git Commit"
backgroundColor: "#E8F5E9"
---

Write a commit message.
`
	if err := os.WriteFile(filepath.Join(subDir, "commit.md"), []byte(subPrompt), 0644); err != nil {
		t.Fatalf("Failed to write commit.md: %v", err)
	}

	// Create disabled prompt (should be excluded)
	disabledPrompt := `---
name: "Disabled"
enabled: false
---

This should not appear.
`
	if err := os.WriteFile(filepath.Join(tmpDir, "disabled.md"), []byte(disabledPrompt), 0644); err != nil {
		t.Fatalf("Failed to write disabled.md: %v", err)
	}

	// Create non-md file (should be ignored)
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
	data := []byte(`---
name: "Claude Only Prompt"
enabledWhen: 'acp.matchesServerType("claude-code")'
---

This prompt is only for Claude Code.
`)

	prompt, err := ParsePromptFile("claude-only.md", data, time.Now())
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
	data := []byte(`---
name: "Multi ACP Prompt"
enabledWhen: 'acp.matchesServerType(["auggie", "claude-code", "custom-acp"])'
---

This prompt works with multiple ACPs.
`)

	prompt, err := ParsePromptFile("multi-acp.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	want := `acp.matchesServerType(["auggie", "claude-code", "custom-acp"])`
	if prompt.EnabledWhen != want {
		t.Errorf("EnabledWhen = %q, want %q", prompt.EnabledWhen, want)
	}
}

func TestParsePromptFile_WithGroup(t *testing.T) {
	data := []byte(`---
name: "Test Prompt"
description: "A test prompt"
group: "Testing"
backgroundColor: "#E8F5E9"
---

This is a test prompt with a group.
`)

	prompt, err := ParsePromptFile("test.md", data, time.Now())
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
	data := []byte(`---
name: "Context Prompt"
group: "Workflow"
menus: conversation
---

This prompt appears in the conversation context menu.
`)

	prompt, err := ParsePromptFile("context.md", data, time.Now())
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
	data := []byte(`---
name: "Multi Menu Prompt"
menus: "conversation, group"
---

This prompt appears in multiple menus.
`)

	prompt, err := ParsePromptFile("multi.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.Menus != "conversation, group" {
		t.Errorf("Menus = %q, want %q", prompt.Menus, "conversation, group")
	}
}

func TestParsePromptFile_WithoutMenus(t *testing.T) {
	data := []byte(`---
name: "Plain Prompt"
---

A prompt without a menus attribute.
`)

	prompt, err := ParsePromptFile("plain.md", data, time.Now())
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
	data := []byte(`---
name: "Jira Prompt"
enabledWhen: 'tools.hasAllPatterns(["jira_*", "slack_*"])'
---

This prompt requires Jira and Slack tools.
`)

	prompt, err := ParsePromptFile("jira-prompt.md", data, time.Now())
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
