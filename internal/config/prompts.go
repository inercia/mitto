// Package config provides prompt file parsing for global prompts.
// Prompt files are markdown files with YAML front-matter stored in MITTO_DIR/prompts/.
package config

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PromptFile represents a parsed markdown prompt file with YAML front-matter.
// Files are stored in MITTO_DIR/prompts/ and can be organized in subdirectories.
type PromptFile struct {
	// Path is the relative path from the prompts directory (e.g., "git/commit.md")
	Path string `json:"-"`

	// Name is the display name for the prompt button.
	// If not specified in front-matter, derived from filename.
	Name string `yaml:"name" json:"name"`

	// Description is an optional description shown as tooltip in the UI.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Group is an optional group name for organizing prompts in the UI.
	// Prompts with the same group will be displayed together under a group header.
	// If empty, the prompt will appear in an "Other" section.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`

	// BackgroundColor is an optional hex color for the prompt button (e.g., "#E8F5E9").
	BackgroundColor string `yaml:"backgroundColor,omitempty" json:"backgroundColor,omitempty"`

	// Icon is an optional icon identifier for future use.
	Icon string `yaml:"icon,omitempty" json:"icon,omitempty"`

	// Tags is an optional list of categorization tags for future use.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// EnabledWhenACP is an optional comma-separated list of ACP server names this prompt applies to.
	// If empty, the prompt works with all ACP servers.
	// Example: "enabledWhenACP: auggie, claude-code" means only show this prompt for those ACP servers.
	// Legacy key "acps" is also supported for backward compatibility.
	EnabledWhenACP string `yaml:"enabledWhenACP,omitempty" json:"-"`

	// EnabledWhenMCP is an optional comma-separated list of tool name patterns required for this prompt.
	// Patterns support * as wildcard (e.g., "jira_*,slack_*").
	// If specified, the prompt is only shown when all required tool patterns are satisfied
	// (at least one matching tool exists for each pattern).
	// If empty, the prompt is always shown (no tool requirements).
	EnabledWhenMCP string `yaml:"enabledWhenMCP,omitempty" json:"-"`

	// Enabled controls whether the prompt is active. Defaults to true if not specified.
	Enabled *bool `yaml:"enabled,omitempty" json:"-"`

	// EnabledWhen is an optional CEL expression that determines when this prompt is visible.
	// If empty, the prompt is always visible.
	// If the expression evaluates to true, the prompt is visible; otherwise hidden.
	// Available context: acp.*, workspace.*, session.*, parent.*, children.*, tools.*
	// Example: "!session.isChild" hides the prompt in child conversations.
	EnabledWhen string `yaml:"enabledWhen,omitempty" json:"-"`

	// Content is the markdown body after the front-matter.
	Content string `json:"prompt"`

	// FileModTime is the file's modification time for cache invalidation.
	FileModTime time.Time `json:"-"`
}

// IsEnabled returns true if the prompt is enabled.
// A nil Enabled field is treated as true (enabled by default).
func (p *PromptFile) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

// IsSpecificToACP returns true if the prompt is specifically targeted at the given ACP server.
// Unlike IsAllowedForACP, this returns false for prompts with empty ACPs field (generic prompts).
// This is used to show ACP-specific prompts in the server settings UI.
func (p *PromptFile) IsSpecificToACP(acpServer string) bool {
	if acpServer == "" {
		return false
	}

	// Check legacy enabledWhenACP field (backward compat for user configs)
	if p.EnabledWhenACP != "" {
		for _, acp := range strings.Split(p.EnabledWhenACP, ",") {
			acp = strings.TrimSpace(acp)
			if strings.EqualFold(acp, acpServer) {
				return true
			}
		}
	}

	// Check enabledWhen CEL expression for acp.matchesServerType("serverType")
	if p.EnabledWhen != "" {
		lowerExpr := strings.ToLower(p.EnabledWhen)
		lowerServer := strings.ToLower(acpServer)
		if strings.Contains(lowerExpr, "acp.matchesserver") && strings.Contains(lowerExpr, lowerServer) {
			return true
		}
	}

	return false
}

// ToWebPrompt converts the PromptFile to a WebPrompt for API responses.
// File-based prompts are marked with Source=PromptSourceFile.
func (p *PromptFile) ToWebPrompt() WebPrompt {
	return WebPrompt{
		Name:            p.Name,
		Prompt:          p.Content,
		BackgroundColor: p.BackgroundColor,
		Description:     p.Description,
		Group:           p.Group,
		Source:          PromptSourceFile,
		EnabledWhenACP:  p.EnabledWhenACP,
		EnabledWhenMCP:  p.EnabledWhenMCP,
		EnabledWhen:     p.EnabledWhen,
		Enabled:         p.Enabled,
	}
}

// HasVisibilityCondition returns true if the prompt has a enabledWhen expression.
func (p *PromptFile) HasVisibilityCondition() bool {
	return strings.TrimSpace(p.EnabledWhen) != ""
}

// promptLegacyFields holds fields from legacy prompt YAML keys for backward compatibility.
// These field names were used before the rename to enabledWhenACP / enabledWhenMCP.
type promptLegacyFields struct {
	ACPs          string `yaml:"acps"`
	RequiredTools string `yaml:"required_tools"`
}

// migratePromptLegacyFields copies legacy field values into the current field names
// if the current fields are empty. This provides backward compatibility for existing
// prompt files that still use the old YAML keys "acps" and "required_tools".
func migratePromptLegacyFields(p *PromptFile, frontMatterData []byte) {
	var legacy promptLegacyFields
	if err := yaml.Unmarshal(frontMatterData, &legacy); err != nil {
		return
	}
	if p.EnabledWhenACP == "" && legacy.ACPs != "" {
		p.EnabledWhenACP = legacy.ACPs
	}
	if p.EnabledWhenMCP == "" && legacy.RequiredTools != "" {
		p.EnabledWhenMCP = legacy.RequiredTools
	}
}

// frontMatterDelimiter is the YAML front-matter delimiter.
const frontMatterDelimiter = "---"

// ParsePromptFile parses a markdown file with YAML front-matter.
// The file format is:
//
//	---
//	name: "My Prompt"
//	description: "Optional description"
//	backgroundColor: "#E8F5E9"
//	---
//
//	Prompt content here...
//
// If no front-matter is present, the entire file is treated as content
// and the name is derived from the filename.
func ParsePromptFile(path string, data []byte, modTime time.Time) (*PromptFile, error) {
	prompt := &PromptFile{
		Path:        path,
		FileModTime: modTime,
	}

	content := string(data)

	// Check for front-matter
	if strings.HasPrefix(strings.TrimSpace(content), frontMatterDelimiter) {
		// Find the closing delimiter
		lines := strings.Split(content, "\n")
		var frontMatterEnd int
		foundStart := false

		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == frontMatterDelimiter {
				if !foundStart {
					foundStart = true
					continue
				}
				frontMatterEnd = i
				break
			}
		}

		if frontMatterEnd > 0 {
			// Extract and parse front-matter
			frontMatter := strings.Join(lines[1:frontMatterEnd], "\n")
			frontMatterBytes := []byte(frontMatter)
			if err := yaml.Unmarshal(frontMatterBytes, prompt); err != nil {
				return nil, fmt.Errorf("failed to parse front-matter in %s: %w", path, err)
			}

			// Apply legacy field name migrations for backward compatibility.
			// Handles "acps" → EnabledWhenACP and "required_tools" → EnabledWhenMCP.
			migratePromptLegacyFields(prompt, frontMatterBytes)

			// Translate shorthand fields to enabledWhen CEL expression for backward compatibility.
			// Users may still use enabledWhenACP/enabledWhenMCP in their prompt files.
			if prompt.EnabledWhenACP != "" || prompt.EnabledWhenMCP != "" {
				prompt.EnabledWhen = TranslateShorthandToEnabledWhen(prompt.EnabledWhenACP, prompt.EnabledWhenMCP, prompt.EnabledWhen)
			}

			// Extract content after front-matter
			if frontMatterEnd+1 < len(lines) {
				prompt.Content = strings.TrimSpace(strings.Join(lines[frontMatterEnd+1:], "\n"))
			}
		} else {
			// Malformed front-matter - treat entire file as content
			prompt.Content = strings.TrimSpace(content)
		}
	} else {
		// No front-matter - entire file is content
		prompt.Content = strings.TrimSpace(content)
	}

	// Derive name from filename if not specified
	if prompt.Name == "" {
		base := filepath.Base(path)
		prompt.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	return prompt, nil
}

// LoadPromptFile loads and parses a single prompt file.
func LoadPromptFile(promptsDir, relativePath string) (*PromptFile, error) {
	fullPath := filepath.Join(promptsDir, relativePath)

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat prompt file %s: %w", relativePath, err)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read prompt file %s: %w", relativePath, err)
	}

	return ParsePromptFile(relativePath, data, info.ModTime())
}

// LoadPromptsFromDir loads all .md files from a directory recursively.
// Disabled prompts (enabled: false) are included so they can suppress same-named
// prompts from lower-priority directories during the merge phase.
// Returns an empty slice if the directory doesn't exist.
func LoadPromptsFromDir(dir string) ([]*PromptFile, error) {
	// Check if directory exists
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	var prompts []*PromptFile

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process .md files
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %s: %w", path, err)
		}

		// Load and parse the file
		prompt, err := LoadPromptFile(dir, relPath)
		if err != nil {
			// Log warning but continue with other files
			// In production, this would use a logger
			return nil
		}

		prompts = append(prompts, prompt)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk prompts directory %s: %w", dir, err)
	}

	return prompts, nil
}

// PromptsToWebPrompts converts a slice of PromptFile to WebPrompt.
func PromptsToWebPrompts(prompts []*PromptFile) []WebPrompt {
	if len(prompts) == 0 {
		return nil
	}

	result := make([]WebPrompt, 0, len(prompts))
	for _, p := range prompts {
		result = append(result, p.ToWebPrompt())
	}
	return result
}

// FilterPromptsSpecificToACP filters prompts to only include those specifically targeted
// at the given ACP server (have acps: field that includes the server name).
// Generic prompts (with empty acps: field) are excluded.
// If acpServer is empty, returns an empty slice.
func FilterPromptsSpecificToACP(prompts []*PromptFile, acpServer string) []*PromptFile {
	if acpServer == "" || len(prompts) == 0 {
		return nil
	}

	result := make([]*PromptFile, 0)
	for _, p := range prompts {
		if p.IsSpecificToACP(acpServer) {
			result = append(result, p)
		}
	}
	return result
}

// GetPromptsDirModTime returns the most recent modification time of any file
// in the prompts directory. Returns zero time if directory doesn't exist.
func GetPromptsDirModTime(dir string) time.Time {
	var latest time.Time

	// Check directory itself
	info, err := os.Stat(dir)
	if err != nil {
		return latest
	}
	latest = info.ModTime()

	// Walk all files to find the most recent modification
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})

	return latest
}

// toolPatternCallRe matches tools.has*Pattern* function calls in CEL expressions.
var toolPatternCallRe = regexp.MustCompile(`tools\.has(?:All|Any)?Patterns?\([^)]*`)

// quotedStringRe matches double-quoted string literals.
var quotedStringRe = regexp.MustCompile(`"([^"]+)"`)

// extractToolPatternsFromCEL extracts tool glob patterns from enabledWhen CEL expressions.
// Looks for tools.hasPattern("..."), tools.hasAllPatterns([...]), tools.hasAnyPattern([...]).
func extractToolPatternsFromCEL(expr string) []string {
	if expr == "" || !strings.Contains(expr, "tools.has") {
		return nil
	}
	var patterns []string
	calls := toolPatternCallRe.FindAllString(expr, -1)
	for _, call := range calls {
		matches := quotedStringRe.FindAllStringSubmatch(call, -1)
		for _, m := range matches {
			if len(m) > 1 {
				patterns = append(patterns, m[1])
			}
		}
	}
	return patterns
}

// CollectRequiredToolPatterns extracts all unique required tool patterns from a list of prompts.
// Patterns may come from the legacy enabledWhenMCP field or from enabledWhen CEL expressions.
func CollectRequiredToolPatterns(prompts []*PromptFile) []string {
	seen := make(map[string]bool)
	var patterns []string

	addPattern := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}

	for _, p := range prompts {
		// From legacy enabledWhenMCP field
		if p.EnabledWhenMCP != "" {
			for _, pattern := range strings.Split(p.EnabledWhenMCP, ",") {
				addPattern(strings.TrimSpace(pattern))
			}
		}
		// From enabledWhen CEL expression
		for _, pattern := range extractToolPatternsFromCEL(p.EnabledWhen) {
			addPattern(pattern)
		}
	}
	return patterns
}

// CollectRequiredToolPatternsFromWebPrompts extracts all unique required tool patterns from WebPrompts.
// Patterns may come from the legacy enabledWhenMCP field or from enabledWhen CEL expressions.
func CollectRequiredToolPatternsFromWebPrompts(prompts []WebPrompt) []string {
	seen := make(map[string]bool)
	var patterns []string

	addPattern := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}

	for _, p := range prompts {
		// From legacy enabledWhenMCP field
		if p.EnabledWhenMCP != "" {
			for _, pattern := range strings.Split(p.EnabledWhenMCP, ",") {
				addPattern(strings.TrimSpace(pattern))
			}
		}
		// From enabledWhen CEL expression
		for _, pattern := range extractToolPatternsFromCEL(p.EnabledWhen) {
			addPattern(pattern)
		}
	}
	return patterns
}

// TranslateShorthandToEnabledWhen merges enabledWhenACP and enabledWhenMCP
// into an enabledWhen CEL expression using convenience functions.
// If enabledWhen is already set, the shorthand conditions are ANDed with it.
// The shorthand fields are syntactic sugar for the underlying CEL functions:
//   - enabledWhenACP: "augment" → acp.matchesServerType("augment")
//   - enabledWhenACP: "augment, claude-code" → acp.matchesServerType(["augment", "claude-code"])
//   - enabledWhenMCP: "mitto_*" → tools.hasPattern("mitto_*")
//   - enabledWhenMCP: "mitto_*, jira_*" → tools.hasAllPatterns(["mitto_*", "jira_*"])
func TranslateShorthandToEnabledWhen(enabledWhenACP, enabledWhenMCP, existingEnabledWhen string) string {
	var parts []string

	if enabledWhenACP != "" {
		servers := splitAndTrimCSV(enabledWhenACP)
		if len(servers) == 1 {
			parts = append(parts, fmt.Sprintf("acp.matchesServerType(%q)", servers[0]))
		} else if len(servers) > 1 {
			quoted := make([]string, len(servers))
			for i, s := range servers {
				quoted[i] = fmt.Sprintf("%q", s)
			}
			parts = append(parts, fmt.Sprintf("acp.matchesServerType([%s])", strings.Join(quoted, ", ")))
		}
	}

	if enabledWhenMCP != "" {
		patterns := splitAndTrimCSV(enabledWhenMCP)
		if len(patterns) == 1 {
			parts = append(parts, fmt.Sprintf("tools.hasPattern(%q)", patterns[0]))
		} else if len(patterns) > 1 {
			quoted := make([]string, len(patterns))
			for i, p := range patterns {
				quoted[i] = fmt.Sprintf("%q", p)
			}
			parts = append(parts, fmt.Sprintf("tools.hasAllPatterns([%s])", strings.Join(quoted, ", ")))
		}
	}

	if existingEnabledWhen != "" {
		parts = append(parts, existingEnabledWhen)
	}

	return strings.Join(parts, " && ")
}

// splitAndTrimCSV splits a comma-separated string and trims whitespace from each element.
func splitAndTrimCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// init registers a custom YAML unmarshaler for handling the enabled field.
func init() {
	// Ensure bytes package is used (for potential future use)
	_ = bytes.Buffer{}
}

// UpdatePromptFileEnabled reads a prompt .md file, updates the enabled field in its YAML
// frontmatter, and writes it back. When enabling (enabled=true), the enabled key is deleted
// from the frontmatter (nil means default=true). When disabling (enabled=false), it is set
// to false explicitly.
func UpdatePromptFileEnabled(filePath string, enabled bool) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read prompt file %s: %w", filePath, err)
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	// Find frontmatter boundaries
	if !strings.HasPrefix(strings.TrimSpace(content), frontMatterDelimiter) {
		return fmt.Errorf("prompt file %s has no YAML frontmatter", filePath)
	}

	frontMatterEnd := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontMatterDelimiter {
			frontMatterEnd = i
			break
		}
	}
	if frontMatterEnd < 0 {
		return fmt.Errorf("prompt file %s has malformed frontmatter (no closing ---)", filePath)
	}

	// Parse frontmatter into a map to preserve unknown fields
	frontMatterText := strings.Join(lines[1:frontMatterEnd], "\n")
	var fm map[string]interface{}
	if err := yaml.Unmarshal([]byte(frontMatterText), &fm); err != nil {
		return fmt.Errorf("failed to parse frontmatter in %s: %w", filePath, err)
	}
	if fm == nil {
		fm = make(map[string]interface{})
	}

	if enabled {
		// Remove enabled key — nil means enabled by default
		delete(fm, "enabled")
	} else {
		fm["enabled"] = false
	}

	// Re-marshal frontmatter
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("failed to marshal frontmatter for %s: %w", filePath, err)
	}

	// Reconstruct file: ---\n<yaml>---\n<body>
	body := strings.Join(lines[frontMatterEnd+1:], "\n")
	newContent := "---\n" + string(fmBytes) + "---\n" + body

	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write prompt file %s: %w", filePath, err)
	}

	return nil
}

// SlugifyPromptName converts a prompt name to a filesystem-safe slug.
// e.g., "Add tests" → "add-tests"
func SlugifyPromptName(name string) string {
	slug := strings.ToLower(name)
	var result []byte
	lastHyphen := false
	for _, c := range slug {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, byte(c))
			lastHyphen = false
		} else if !lastHyphen {
			result = append(result, '-')
			lastHyphen = true
		}
	}
	return strings.Trim(string(result), "-")
}
