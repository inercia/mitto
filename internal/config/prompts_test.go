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
		Description:     "Test description",
		Group:           "Testing",
		EnabledWhenACP:  "auggie, claude-code",
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
	if wp.Description != "Test description" {
		t.Errorf("WebPrompt.Description = %q, want %q", wp.Description, "Test description")
	}
	if wp.Group != "Testing" {
		t.Errorf("WebPrompt.Group = %q, want %q", wp.Group, "Testing")
	}
	// File-based prompts should have Source=PromptSourceFile
	if wp.Source != PromptSourceFile {
		t.Errorf("WebPrompt.Source = %q, want %q", wp.Source, PromptSourceFile)
	}
	// ACPs field should be included for client-side filtering
	if wp.EnabledWhenACP != "auggie, claude-code" {
		t.Errorf("WebPrompt.EnabledWhenACP = %q, want %q", wp.EnabledWhenACP, "auggie, claude-code")
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
enabledWhenACP: "claude-code"
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
	if prompt.EnabledWhenACP != "claude-code" {
		t.Errorf("ACPs = %q, want %q", prompt.EnabledWhenACP, "claude-code")
	}
}

func TestParsePromptFile_WithMultipleACPs(t *testing.T) {
	data := []byte(`---
name: "Multi ACP Prompt"
enabledWhenACP: "auggie, claude-code, custom-acp"
---

This prompt works with multiple ACPs.
`)

	prompt, err := ParsePromptFile("multi-acp.md", data, time.Now())
	if err != nil {
		t.Fatalf("ParsePromptFile failed: %v", err)
	}

	if prompt.EnabledWhenACP != "auggie, claude-code, custom-acp" {
		t.Errorf("ACPs = %q, want %q", prompt.EnabledWhenACP, "auggie, claude-code, custom-acp")
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

func TestIsAllowedForACP_EmptyACPs(t *testing.T) {
	prompt := &PromptFile{Name: "Test", EnabledWhenACP: ""}

	// Empty ACPs means allowed for all
	if !prompt.IsAllowedForACP("auggie") {
		t.Error("IsAllowedForACP('auggie') = false, want true for empty ACPs")
	}
	if !prompt.IsAllowedForACP("claude-code") {
		t.Error("IsAllowedForACP('claude-code') = false, want true for empty ACPs")
	}
	if !prompt.IsAllowedForACP("") {
		t.Error("IsAllowedForACP('') = false, want true for empty ACPs")
	}
}

func TestIsAllowedForACP_SingleACP(t *testing.T) {
	prompt := &PromptFile{Name: "Test", EnabledWhenACP: "claude-code"}

	if !prompt.IsAllowedForACP("claude-code") {
		t.Error("IsAllowedForACP('claude-code') = false, want true")
	}
	if prompt.IsAllowedForACP("auggie") {
		t.Error("IsAllowedForACP('auggie') = true, want false")
	}
	// Empty ACP server should allow all prompts
	if !prompt.IsAllowedForACP("") {
		t.Error("IsAllowedForACP('') = false, want true for empty server")
	}
}

func TestIsAllowedForACP_MultipleACPs(t *testing.T) {
	prompt := &PromptFile{Name: "Test", EnabledWhenACP: "auggie, claude-code, custom-acp"}

	if !prompt.IsAllowedForACP("auggie") {
		t.Error("IsAllowedForACP('auggie') = false, want true")
	}
	if !prompt.IsAllowedForACP("claude-code") {
		t.Error("IsAllowedForACP('claude-code') = false, want true")
	}
	if !prompt.IsAllowedForACP("custom-acp") {
		t.Error("IsAllowedForACP('custom-acp') = false, want true")
	}
	if prompt.IsAllowedForACP("other-acp") {
		t.Error("IsAllowedForACP('other-acp') = true, want false")
	}
}

func TestIsAllowedForACP_CaseInsensitive(t *testing.T) {
	prompt := &PromptFile{Name: "Test", EnabledWhenACP: "Claude-Code"}

	if !prompt.IsAllowedForACP("claude-code") {
		t.Error("IsAllowedForACP('claude-code') = false, want true (case insensitive)")
	}
	if !prompt.IsAllowedForACP("CLAUDE-CODE") {
		t.Error("IsAllowedForACP('CLAUDE-CODE') = false, want true (case insensitive)")
	}
}

func TestFilterPromptsByACP(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "All ACPs", EnabledWhenACP: ""},
		{Name: "Claude Only", EnabledWhenACP: "claude-code"},
		{Name: "Auggie Only", EnabledWhenACP: "auggie"},
		{Name: "Both", EnabledWhenACP: "claude-code, auggie"},
	}

	// Filter for auggie
	filtered := FilterPromptsByACP(prompts, "auggie")
	if len(filtered) != 3 {
		t.Errorf("FilterPromptsByACP(auggie) returned %d prompts, want 3", len(filtered))
	}

	// Check that "Claude Only" is not in the filtered list
	for _, p := range filtered {
		if p.Name == "Claude Only" {
			t.Error("FilterPromptsByACP(auggie) should not include 'Claude Only'")
		}
	}

	// Filter for claude-code
	filtered = FilterPromptsByACP(prompts, "claude-code")
	if len(filtered) != 3 {
		t.Errorf("FilterPromptsByACP(claude-code) returned %d prompts, want 3", len(filtered))
	}

	// Check that "Auggie Only" is not in the filtered list
	for _, p := range filtered {
		if p.Name == "Auggie Only" {
			t.Error("FilterPromptsByACP(claude-code) should not include 'Auggie Only'")
		}
	}

	// Empty ACP server should return all prompts
	filtered = FilterPromptsByACP(prompts, "")
	if len(filtered) != 4 {
		t.Errorf("FilterPromptsByACP('') returned %d prompts, want 4", len(filtered))
	}

	// Empty prompts should return empty
	filtered = FilterPromptsByACP([]*PromptFile{}, "auggie")
	if len(filtered) != 0 {
		t.Errorf("FilterPromptsByACP on empty slice returned %d prompts, want 0", len(filtered))
	}

	// Nil prompts should return nil
	filtered = FilterPromptsByACP(nil, "auggie")
	if filtered != nil {
		t.Errorf("FilterPromptsByACP(nil) = %v, want nil", filtered)
	}
}

func TestIsSpecificToACP(t *testing.T) {
	tests := []struct {
		name      string
		acps      string
		acpServer string
		want      bool
	}{
		{"empty ACPs is not specific", "", "auggie", false},
		{"empty ACP server", "auggie", "", false},
		{"exact match", "auggie", "auggie", true},
		{"case insensitive match", "Auggie", "auggie", true},
		{"no match", "claude-code", "auggie", false},
		{"multiple ACPs with match", "claude-code, auggie", "auggie", true},
		{"multiple ACPs without match", "claude-code, other", "auggie", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PromptFile{EnabledWhenACP: tt.acps}
			got := p.IsSpecificToACP(tt.acpServer)
			if got != tt.want {
				t.Errorf("IsSpecificToACP(%q) = %v, want %v", tt.acpServer, got, tt.want)
			}
		})
	}
}

func TestMatchToolPattern(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		toolName string
		want     bool
	}{
		{"exact match", "exact_tool", "exact_tool", true},
		{"exact no match", "exact_tool", "other_tool", false},
		{"wildcard suffix match", "jira_*", "jira_search", true},
		{"wildcard suffix match 2", "jira_*", "jira_get_issue", true},
		{"wildcard suffix no match", "jira_*", "slack_post_message", false},
		{"wildcard prefix match", "*_search", "jira_search", true},
		{"wildcard prefix no match", "*_search", "jira_get_issue", false},
		{"wildcard middle match", "jira_*_search", "jira_advanced_search", true},
		{"wildcard middle no match", "jira_*_search", "jira_get_issue", false},
		{"case insensitive pattern", "JIRA_*", "jira_search", true},
		{"case insensitive tool", "jira_*", "JIRA_SEARCH", true},
		{"empty pattern", "", "jira_search", false},
		{"empty tool", "jira_*", "", false},
		{"both empty", "", "", false},
		{"star only matches all", "*", "anything", true},
		{"star only matches empty-ish", "*", "a", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchToolPattern(tt.pattern, tt.toolName)
			if got != tt.want {
				t.Errorf("MatchToolPattern(%q, %q) = %v, want %v", tt.pattern, tt.toolName, got, tt.want)
			}
		})
	}
}

func TestCollectRequiredToolPatterns(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "P1", EnabledWhenMCP: "jira_*,slack_*"},
		{Name: "P2", EnabledWhenMCP: "jira_*,github_*"},
		{Name: "P3", EnabledWhenMCP: ""},
		{Name: "P4", EnabledWhenMCP: "slack_*"},
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
		{Name: "P1", EnabledWhenMCP: ""},
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

func TestAreEnabledWhenMCPSatisfied(t *testing.T) {
	satisfied := map[string]bool{
		"jira_*":  true,
		"slack_*": true,
	}

	tests := []struct {
		name          string
		requiredTools string
		satisfiedMap  map[string]bool
		want          bool
	}{
		{"empty required tools", "", satisfied, true},
		{"all satisfied", "jira_*,slack_*", satisfied, true},
		{"some not satisfied", "jira_*,github_*", satisfied, false},
		{"none satisfied", "github_*", satisfied, false},
		{"empty patterns map", "jira_*", map[string]bool{}, false},
		{"whitespace in patterns", " jira_* , slack_* ", satisfied, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AreEnabledWhenMCPSatisfied(tt.requiredTools, tt.satisfiedMap)
			if got != tt.want {
				t.Errorf("AreEnabledWhenMCPSatisfied(%q, %v) = %v, want %v", tt.requiredTools, tt.satisfiedMap, got, tt.want)
			}
		})
	}
}

func TestParsePromptFile_WithEnabledWhenMCP(t *testing.T) {
	data := []byte(`---
name: "Jira Prompt"
enabledWhenMCP: "jira_*,slack_*"
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
	if prompt.EnabledWhenMCP != "jira_*,slack_*" {
		t.Errorf("EnabledWhenMCP = %q, want %q", prompt.EnabledWhenMCP, "jira_*,slack_*")
	}
}

func TestToWebPrompt_IncludesEnabledWhenMCP(t *testing.T) {
	prompt := &PromptFile{
		Name:           "Test",
		Content:        "Content here",
		EnabledWhenACP: "auggie",
		EnabledWhenMCP: "jira_*,slack_*",
	}

	wp := prompt.ToWebPrompt()

	if wp.EnabledWhenMCP != "jira_*,slack_*" {
		t.Errorf("WebPrompt.EnabledWhenMCP = %q, want %q", wp.EnabledWhenMCP, "jira_*,slack_*")
	}
	if wp.Source != PromptSourceFile {
		t.Errorf("WebPrompt.Source = %q, want %q", wp.Source, PromptSourceFile)
	}
}

func TestFilterPromptsSpecificToACP(t *testing.T) {
	prompts := []*PromptFile{
		{Name: "All ACPs", EnabledWhenACP: ""},
		{Name: "Claude Only", EnabledWhenACP: "claude-code"},
		{Name: "Auggie Only", EnabledWhenACP: "auggie"},
		{Name: "Both", EnabledWhenACP: "claude-code, auggie"},
	}

	// Filter for auggie - should only get prompts with explicit acps: field
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
