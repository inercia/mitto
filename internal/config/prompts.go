// Package config provides prompt file parsing for global prompts.
// Prompt files are YAML files stored in MITTO_DIR/prompts/.
package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PromptPeriodic declares that selecting this prompt should start a periodic
// (recurring) conversation instead of a one-time one. Presence implies opt-in;
// the fields provide sensible defaults for the schedule dialog.
//
// Example frontmatter (schedule-based):
//
//	periodic:
//	  value: 1
//	  unit: hours          # minutes | hours | days
//	  at: "09:00"          # optional, only for days (UTC)
//	  maxIterations: 10    # optional; 0/absent = unlimited scheduled runs
//
// Example frontmatter (on-completion trigger):
//
//	periodic:
//	  trigger: onCompletion  # fire after the agent stops responding
//	  delay: 30              # seconds to wait after agent stops (clamped to floor at consumption)
//	  maxIterations: 20      # optional safety cap
//	  maxDuration: "4h"      # optional wall-clock cap; 0/absent = unlimited
type PromptPeriodic struct {
	// Value is the number of time units between runs (min 1). Used for trigger: schedule (default).
	Value int `yaml:"value" json:"value"`
	// Unit is the time unit: "minutes", "hours", or "days". Used for trigger: schedule (default).
	Unit string `yaml:"unit" json:"unit"`
	// At is the time of day in HH:MM format (UTC). Only meaningful for the "days" unit.
	At string `yaml:"at,omitempty" json:"at,omitempty"`
	// MaxIterations caps the number of scheduled runs when the conversation is made periodic (0 / absent = unlimited).
	MaxIterations int `yaml:"maxIterations,omitempty" json:"maxIterations,omitempty"`
	// Trigger selects how the periodic run fires: "" or "schedule" (default, frequency-based)
	// vs "onCompletion" (fire after the agent stops responding + Delay seconds).
	Trigger string `yaml:"trigger,omitempty" json:"trigger,omitempty"`
	// Delay is the number of seconds to wait after the agent stops responding before the
	// next run. Only meaningful for trigger: onCompletion. Clamped to a global minimum
	// (default 5s) at the consumption boundary.
	Delay int `yaml:"delay,omitempty" json:"delay,omitempty"`
	// MaxDuration is an optional wall-clock cap (e.g. "2h", "30m"); 0/absent = unlimited.
	// Parsed to seconds at the consumption boundary.
	MaxDuration string `yaml:"maxDuration,omitempty" json:"maxDuration,omitempty"`
}

// PromptParameterCache configures value caching for a single prompt parameter.
// When present, a successfully collected argument value may be reused within the
// same conversation without re-prompting the user.
//
// Example YAML:
//
//	cache:
//	  destination: memory   # only "memory" is valid in v1
//	  ttl: 1h               # optional Go duration; absent => cached for conversation lifetime
type PromptParameterCache struct {
	// Destination is the cache backend. Only "memory" is valid in v1; future versions
	// may introduce additional backends (e.g. "disk"). The value is validated at parse
	// time against KnownPromptCacheDestinations.
	Destination string `yaml:"destination" json:"destination"`
	// TTL is an optional Go duration string (e.g. "1h", "30m") that limits how long
	// the cached value is valid. When absent or empty, the value is cached for the
	// entire conversation lifetime (no expiry).
	TTL string `yaml:"ttl,omitempty" json:"ttl,omitempty"`
}

// PromptParameter declares a single named, typed parameter that the prompt body
// references via Go-template {{ .Args.NAME }} or {{ Arg "NAME" "default" }} syntax.
type PromptParameter struct {
	// Name is the placeholder name used in the prompt body (e.g. "id" for {{ .Args.id }}).
	Name string `yaml:"name" json:"name"`
	// Type is one of the known parameter types (see KnownPromptParameterTypes).
	Type string `yaml:"type" json:"type"`
	// Description is an optional human-readable hint shown in the UI / MCP schema.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Required, when explicitly set to true, signals that the parameter must be
	// supplied before the prompt is dispatched. Defaults to unset (caller decides).
	// Declarative defaults are handled by the Arg helper in the template body, not here.
	Required *bool `yaml:"required,omitempty" json:"required,omitempty"`
	// Default is the default value substituted when the parameter is not explicitly
	// supplied. Required for processor parameters (mandatory); optional for prompt-file
	// parameters (the Arg helper in the template body also provides per-site defaults).
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// Cache, when non-nil, enables per-conversation value caching for this parameter.
	// The collected argument value is stored so the UI can skip re-asking within the
	// same conversation. See PromptParameterCache for the configuration schema.
	Cache *PromptParameterCache `yaml:"cache,omitempty" json:"cache,omitempty"`
}

// PromptFile represents a parsed YAML prompt file.
// Files are stored in MITTO_DIR/prompts/ and can be organized in subdirectories.
type PromptFile struct {
	// Path is the relative path from the prompts directory (e.g., "git/commit.prompt.yaml")
	Path string `yaml:"-" json:"-"`

	// Name is the display name for the prompt button.
	// If not specified in front-matter, derived from filename.
	Name string `yaml:"name" json:"name"`

	// Description is an optional description shown as tooltip in the UI.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Group is an optional group name for organizing prompts in the UI.
	// Prompts with the same group will be displayed together under a group header.
	// If empty, the prompt will appear in an "Other" section.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`

	// Menus is a comma-separated list of UI menus this prompt should appear in
	// (beyond the default ChatInput dropup). For example, "conversation" makes the
	// prompt available in the per-conversation context menu. Multiple values may be
	// combined, e.g. "conversation,group".
	Menus string `yaml:"menus,omitempty" json:"menus,omitempty"`

	// BackgroundColor is an optional hex color for the prompt button (e.g., "#E8F5E9").
	BackgroundColor string `yaml:"backgroundColor,omitempty" json:"backgroundColor,omitempty"`

	// Icon is an optional icon identifier for future use.
	Icon string `yaml:"icon,omitempty" json:"icon,omitempty"`

	// Tags is an optional list of categorization tags for future use.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// Enabled controls whether the prompt is active. Defaults to true if not specified.
	Enabled *bool `yaml:"enabled,omitempty" json:"-"`

	// EnabledWhen is an optional CEL expression that determines when this prompt is visible.
	// If empty, the prompt is always visible.
	// If the expression evaluates to true, the prompt is visible; otherwise hidden.
	// Available context: acp.*, workspace.*, session.*, parent.*, children.*, tools.*
	// Example: "!session.isChild" hides the prompt in child conversations.
	EnabledWhen string `yaml:"enabledWhen,omitempty" json:"-"`

	// Periodic, if set, declares that selecting this prompt in a menu creates a
	// periodic (recurring) conversation instead of a one-time seed.
	// Presence implies opt-in; the fields provide default values for the schedule
	// dialog. The "at" field is in HH:MM UTC and is only valid for the "days" unit.
	Periodic *PromptPeriodic `yaml:"periodic,omitempty" json:"periodic,omitempty"`

	// PreferredModels is an ordered list of case-insensitive glob patterns matched against
	// available model IDs and display names. The first match wins. Empty/absent means use
	// the session's baseline model.
	PreferredModels []string `yaml:"preferredModels,omitempty" json:"preferredModels,omitempty"`

	// Parameters declares the named, typed inputs this prompt expects.
	// Each entry must have a non-empty name and a recognised type (see KnownPromptParameterTypes).
	// Callers substitute values via Go-template .Args.NAME or Arg helper in Content.
	Parameters []PromptParameter `yaml:"parameters,omitempty" json:"parameters,omitempty"`

	// Content is the prompt body text, stored under the "prompt" key in the YAML file.
	Content string `yaml:"prompt" json:"prompt"`

	// FileModTime is the file's modification time for cache invalidation.
	FileModTime time.Time `yaml:"-" json:"-"`
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

	// Check enabledWhen CEL expression for ACP.MatchesServerType("serverType").
	// We lowercase both sides for a case-insensitive prefix match: "acp.matchesserver"
	// is a deliberate prefix of the lowercased canonical form "acp.matchesservertype",
	// which still matches correctly while tolerating minor capitalisation variations.
	if p.EnabledWhen != "" {
		lowerExpr := strings.ToLower(p.EnabledWhen)
		lowerServer := strings.ToLower(acpServer)
		if strings.Contains(lowerExpr, "acp.matchesservertype") && strings.Contains(lowerExpr, lowerServer) {
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
		Icon:            p.Icon,
		Description:     p.Description,
		Group:           p.Group,
		Menus:           p.Menus,
		Source:          PromptSourceFile,
		EnabledWhen:     p.EnabledWhen,
		Enabled:         p.Enabled,
		Periodic:        p.Periodic,
		PreferredModels: p.PreferredModels,
		Parameters:      p.Parameters,
	}
}

// HasVisibilityCondition returns true if the prompt has a enabledWhen expression.
func (p *PromptFile) HasVisibilityCondition() bool {
	return strings.TrimSpace(p.EnabledWhen) != ""
}

// ParsePromptFile parses a YAML prompt file.
// The file format is a single YAML document with all fields as top-level keys:
//
//	name: "My Prompt"
//	description: "Optional description"
//	backgroundColor: "#E8F5E9"
//	prompt: |
//	  Prompt content here...
//
// The name is derived from the filename if not specified in the file.
func ParsePromptFile(path string, data []byte, modTime time.Time) (*PromptFile, error) {
	prompt := &PromptFile{
		Path:        path,
		FileModTime: modTime,
	}

	if err := yaml.Unmarshal(data, prompt); err != nil {
		return nil, fmt.Errorf("failed to parse prompt file %s: %w", path, err)
	}

	// Derive name from filename if not specified
	if prompt.Name == "" {
		base := filepath.Base(path)
		// Strip .prompt.yaml extension specifically, then fall back to last ext
		name := strings.TrimSuffix(base, ".prompt.yaml")
		if name == base {
			name = strings.TrimSuffix(base, filepath.Ext(base))
		}
		prompt.Name = name
	}

	// Validate parameters block.
	if err := ValidatePromptParameters(prompt.Menus, prompt.Parameters); err != nil {
		return nil, fmt.Errorf("prompt file %s: %w", path, err)
	}

	// Validate Go-template syntax + cond/when CEL literals (mitto-m7sb.6).
	// Fast-path no-op for bodies without "{{". Fail-fast on invalid templates.
	if err := PrecompileTemplateConds(prompt.Name, prompt.Content); err != nil {
		return nil, fmt.Errorf("prompt file %s: %w", path, err)
	}

	// Warn (non-fatal) when the body still uses deprecated @mitto: tokens (mitto-m7sb.9).
	WarnDeprecatedMittoVars(prompt.Name, prompt.Content)

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

// LoadPromptsFromDir loads all .prompt.yaml files from a directory recursively.
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

		// Only process .prompt.yaml files
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".prompt.yaml") {
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

// toolPatternCallRe matches Tools.Has*Pattern* function calls in CEL expressions.
var toolPatternCallRe = regexp.MustCompile(`Tools\.Has(?:All|Any)?Patterns?\([^)]*`)

// quotedStringRe matches double-quoted string literals.
var quotedStringRe = regexp.MustCompile(`"([^"]+)"`)

// extractToolPatternsFromCEL extracts tool glob patterns from enabledWhen CEL expressions.
// Looks for Tools.HasPattern("..."), Tools.HasAllPatterns([...]), Tools.HasAnyPattern([...]).
func extractToolPatternsFromCEL(expr string) []string {
	if expr == "" || !strings.Contains(expr, "Tools.Has") {
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
// Patterns come from enabledWhen CEL expressions (Tools.HasPattern, Tools.HasAllPatterns, etc.).
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
		// From enabledWhen CEL expression
		for _, pattern := range extractToolPatternsFromCEL(p.EnabledWhen) {
			addPattern(pattern)
		}
	}
	return patterns
}

// CollectRequiredToolPatternsFromWebPrompts extracts all unique required tool patterns from WebPrompts.
// Patterns come from enabledWhen CEL expressions (Tools.HasPattern, Tools.HasAllPatterns, etc.).
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
		// From enabledWhen CEL expression
		for _, pattern := range extractToolPatternsFromCEL(p.EnabledWhen) {
			addPattern(pattern)
		}
	}
	return patterns
}

// UpdatePromptFileEnabled reads a .prompt.yaml file, updates the enabled field,
// and writes it back. When enabling (enabled=true), the enabled key is removed
// (nil means default=true). When disabling (enabled=false), it is set explicitly.
func UpdatePromptFileEnabled(filePath string, enabled bool) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read prompt file %s: %w", filePath, err)
	}

	var prompt PromptFile
	if err := yaml.Unmarshal(data, &prompt); err != nil {
		return fmt.Errorf("failed to parse prompt file %s: %w", filePath, err)
	}

	if enabled {
		prompt.Enabled = nil // nil means enabled by default
	} else {
		f := false
		prompt.Enabled = &f
	}

	out, err := yaml.Marshal(&prompt)
	if err != nil {
		return fmt.Errorf("failed to marshal prompt file %s: %w", filePath, err)
	}

	if err := os.WriteFile(filePath, out, 0644); err != nil {
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
