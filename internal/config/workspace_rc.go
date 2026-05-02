// Package config handles configuration loading and management for Mitto.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkspaceRCFileName is the name of the workspace-specific config file.
const WorkspaceRCFileName = ".mittorc"

// WorkspaceMetadata contains optional descriptive metadata for a workspace.
// This is loaded from the workspace .mittorc file and is read-only in the UI.
type WorkspaceMetadata struct {
	// Description is a free-text description of the workspace/project.
	Description string `json:"description,omitempty"`
	// URL is an optional URL associated with the workspace (e.g., repository URL).
	URL string `json:"url,omitempty"`
	// Group is an optional grouping label for organizing workspaces.
	Group string `json:"group,omitempty"`
	// UserDataSchema defines the allowed user data fields for conversations in this workspace.
	// If nil or empty, no custom user data attributes are allowed and any provided attributes will be rejected.
	UserDataSchema *UserDataSchema `json:"user_data_schema,omitempty"`
}

// WorkspaceRC represents workspace-specific configuration loaded from .mittorc.
// Supports prompts, prompts_dirs, conversations, restricted_runners, and metadata sections; other sections are ignored.
type WorkspaceRC struct {
	// Prompts is the list of workspace-specific prompts.
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// PromptsDirs is a list of additional directories to search for prompt files.
	// Paths can be absolute or relative (resolved against the workspace directory).
	// These directories are searched in addition to global prompts directories.
	PromptsDirs []string `json:"prompts_dirs,omitempty"`
	// ProcessorsDirs is a list of additional directories to search for processor YAML files.
	// Paths can be absolute or relative (resolved against the workspace directory).
	// These directories are searched in addition to the default .mitto/processors/ directory.
	ProcessorsDirs []string `json:"processors_dirs,omitempty"`
	// ProcessorOverrides is the list of processor enabled/disabled overrides for this workspace.
	// Each entry has a name and an enabled flag. Used when a processor YAML file cannot be
	// edited in-place (e.g. global or builtin processor). Mirrors the prompts pattern.
	ProcessorOverrides []ProcessorOverride `json:"processor_overrides,omitempty"`
	// Conversations contains workspace-specific conversation processing configuration.
	Conversations *ConversationsConfig `json:"conversations,omitempty"`
	// RestrictedRunners contains per-runner-type overrides for this workspace.
	// Key is the runner type (e.g., "exec", "sandbox-exec", "firejail", "docker").
	// When a workspace uses a runner of type X, it applies the config for type X.
	// This allows workspace-specific restrictions based on runner type.
	RestrictedRunners map[string]*WorkspaceRunnerConfig `json:"restricted_runners,omitempty"`
	// Metadata contains optional descriptive metadata for the workspace.
	Metadata *WorkspaceMetadata `json:"metadata,omitempty"`
	// LoadedAt is the time when this config was loaded.
	LoadedAt time.Time `json:"-"`
	// FileModTime is the modification time of the .mittorc file when loaded.
	// Used to detect file changes efficiently.
	FileModTime time.Time `json:"-"`
}

// ProcessorOverride represents a per-processor enabled/disabled override in .mittorc.
// Mirrors the prompts pattern: entries with just {name, enabled} override the
// processor's default enabled state from its YAML file.
type ProcessorOverride struct {
	Name    string `json:"name" yaml:"name"`
	Enabled *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

// GetRunnerConfigForType returns the runner config for a specific runner type.
// Returns nil if no config exists for the runner type.
func (rc *WorkspaceRC) GetRunnerConfigForType(runnerType string) *WorkspaceRunnerConfig {
	if rc == nil || rc.RestrictedRunners == nil {
		return nil
	}

	return rc.RestrictedRunners[runnerType]
}

// rawWorkspaceRC is used for YAML unmarshaling of workspace .mittorc files.
// It uses a permissive structure to ignore unsupported sections.
type rawWorkspaceRC struct {
	// Prompts section
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
		Description     string `yaml:"description"`
		Group           string `yaml:"group"`
		EnabledWhenACP  string `yaml:"enabledWhenACP"`
		EnabledWhenMCP  string `yaml:"enabledWhenMCP"`
		Enabled         *bool  `yaml:"enabled"`
		EnabledWhen     string `yaml:"enabledWhen"`
	} `yaml:"prompts"`
	// PromptsDirs is a list of additional directories to search for prompt files
	PromptsDirs []string `yaml:"prompts_dirs"`
	// ProcessorsDirs is a list of additional directories to search for processor files
	ProcessorsDirs []string `yaml:"processors_dirs"`
	// DisabledProcessors is the legacy list of processor names disabled for this workspace.
	// Kept for backward compatibility — new code uses ProcessorOverrides instead.
	DisabledProcessors []string `yaml:"disabled_processors"`
	// ProcessorOverrides is the list of processor enabled/disabled overrides.
	// Mirrors the prompts pattern: [{name: "xxx", enabled: true/false}].
	ProcessorOverrides []struct {
		Name    string `yaml:"name"`
		Enabled *bool  `yaml:"enabled"`
	} `yaml:"processors"`
	// Conversations section for message processing and user data schema
	Conversations *struct {
		Processing *struct {
			Override   bool `yaml:"override"`
			Processors []struct {
				When     string `yaml:"when"`
				Position string `yaml:"position"`
				Text     string `yaml:"text"`
			} `yaml:"processors"`
		} `yaml:"processing"`
		// UserData defines the schema for custom user data attributes (legacy location)
		UserData []struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
			Type        string `yaml:"type"`
		} `yaml:"user_data"`
	} `yaml:"conversations"`
	// RestrictedRunners section for per-agent runner overrides
	RestrictedRunners map[string]*WorkspaceRunnerConfig `yaml:"restricted_runners"`
	// Metadata section for workspace description, URL, and user data schema
	Metadata *struct {
		Description string `yaml:"description"`
		URL         string `yaml:"url"`
		Group       string `yaml:"group"`
		UserData    []struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
			Type        string `yaml:"type"`
		} `yaml:"user_data"`
	} `yaml:"metadata"`
}

// FindWorkspaceRCPath checks the standard workspace config file locations in order
// and returns the first one that exists. The search order is:
//  1. $workspaceDir/.mittorc
//  2. $workspaceDir/.mitto/mittorc
//  3. $workspaceDir/.mitto/mitto.yaml
//
// Returns ("", nil, nil) if none of the paths exist.
// Returns (path, fileInfo, nil) for the first path found.
// Returns ("", nil, err) on unexpected stat errors.
func FindWorkspaceRCPath(workspaceDir string) (string, os.FileInfo, error) {
	candidates := []string{
		filepath.Join(workspaceDir, ".mittorc"),
		filepath.Join(workspaceDir, ".mitto", "mittorc"),
		filepath.Join(workspaceDir, ".mitto", "mitto.yaml"),
	}
	for _, p := range candidates {
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return p, info, nil
	}
	return "", nil, nil
}

// LoadWorkspaceRC loads the workspace configuration file. It searches for the
// configuration file in the following locations (in order):
//   - $workspaceDir/.mittorc
//   - $workspaceDir/.mitto/mittorc
//   - $workspaceDir/.mitto/mitto.yaml
//
// Returns nil if no config file exists or the file is empty.
// Returns an error only if the file exists but cannot be parsed.
func LoadWorkspaceRC(workspaceDir string) (*WorkspaceRC, error) {
	if workspaceDir == "" {
		return nil, nil
	}

	rcPath, info, err := FindWorkspaceRCPath(workspaceDir)
	if err != nil {
		return nil, err
	}
	if rcPath == "" {
		return nil, nil
	}

	// Skip empty files
	if info.Size() == 0 {
		return nil, nil
	}

	data, err := os.ReadFile(rcPath)
	if err != nil {
		return nil, err
	}

	rc, err := parseWorkspaceRC(data)
	if err != nil {
		return nil, err
	}
	if rc != nil {
		rc.FileModTime = info.ModTime()
	}
	return rc, nil
}

// GetWorkspaceRCModTime returns the modification time of the workspace config file.
// It searches the same locations as LoadWorkspaceRC (.mittorc, .mitto/mittorc,
// .mitto/mitto.yaml) and returns the mod time of the first file found.
// Returns zero time if no config file exists.
func GetWorkspaceRCModTime(workspaceDir string) time.Time {
	if workspaceDir == "" {
		return time.Time{}
	}
	_, info, err := FindWorkspaceRCPath(workspaceDir)
	if err != nil || info == nil {
		return time.Time{}
	}
	return info.ModTime()
}

// SaveWorkspaceMetadata saves workspace metadata (description, URL) to the workspace .mittorc file.
// It finds the existing .mittorc file using the standard search order, or creates a new .mittorc
// file in the workspace root if none exists. Only the metadata.description and metadata.url
// fields are updated; other sections (prompts, conversations, metadata.user_data, etc.) are preserved.
func SaveWorkspaceMetadata(workspaceDir, description, url, group string) error {
	if workspaceDir == "" {
		return fmt.Errorf("workspace directory is required")
	}

	// Find existing .mittorc file path, or use default
	rcPath, _, err := FindWorkspaceRCPath(workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to check workspace config: %w", err)
	}
	if rcPath == "" {
		// No existing file — create .mittorc in workspace root
		rcPath = filepath.Join(workspaceDir, WorkspaceRCFileName)
	}

	// Read existing file content (may not exist yet)
	var content map[string]interface{}
	data, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read workspace config: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &content); err != nil {
			return fmt.Errorf("failed to parse workspace config: %w", err)
		}
	}
	if content == nil {
		content = make(map[string]interface{})
	}

	// Get or create metadata section
	var metadata map[string]interface{}
	if m, ok := content["metadata"]; ok {
		if mMap, ok := m.(map[string]interface{}); ok {
			metadata = mMap
		}
	}
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	// Update only description and url, leave everything else (user_data, etc.) intact
	if description != "" {
		metadata["description"] = description
	} else {
		delete(metadata, "description")
	}
	if url != "" {
		metadata["url"] = url
	} else {
		delete(metadata, "url")
	}
	if group != "" {
		metadata["group"] = group
	} else {
		delete(metadata, "group")
	}

	// If metadata has no display fields, but preserve user_data if present
	if len(metadata) == 0 {
		delete(content, "metadata")
	} else {
		content["metadata"] = metadata
	}

	// Marshal and write back
	out, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to marshal workspace config: %w", err)
	}

	// Ensure parent directory exists (for .mitto/mittorc case)
	if err := os.MkdirAll(filepath.Dir(rcPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(rcPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write workspace config: %w", err)
	}

	return nil
}

// SaveWorkspaceRCPromptEnabled updates the enabled state of a prompt in the workspace .mittorc file.
// When disabling (enabled=false): adds or updates the entry with {name: promptName, enabled: false}.
// When re-enabling (enabled=true): removes the entry with that name from the prompts array
// (since nil/missing means enabled by default). If the prompts array becomes empty, removes the key.
// Only updates the prompts section; all other sections are preserved.
func SaveWorkspaceRCPromptEnabled(workspaceDir, promptName string, enabled bool) error {
	if workspaceDir == "" {
		return fmt.Errorf("workspace directory is required")
	}
	if promptName == "" {
		return fmt.Errorf("prompt name is required")
	}

	// Find existing .mittorc file path, or use default
	rcPath, _, err := FindWorkspaceRCPath(workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to check workspace config: %w", err)
	}
	if rcPath == "" {
		rcPath = filepath.Join(workspaceDir, WorkspaceRCFileName)
	}

	// Read existing file content (may not exist yet)
	var content map[string]interface{}
	data, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read workspace config: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &content); err != nil {
			return fmt.Errorf("failed to parse workspace config: %w", err)
		}
	}
	if content == nil {
		content = make(map[string]interface{})
	}

	// Get or create prompts section as []interface{}
	var prompts []interface{}
	if p, ok := content["prompts"]; ok {
		if pSlice, ok := p.([]interface{}); ok {
			prompts = pSlice
		}
	}

	if !enabled {
		// Disabling: find existing entry or append a new one
		found := false
		for i, entry := range prompts {
			if m, ok := entry.(map[string]interface{}); ok {
				if n, ok := m["name"]; ok && n == promptName {
					m["enabled"] = false
					prompts[i] = m
					found = true
					break
				}
			}
		}
		if !found {
			prompts = append(prompts, map[string]interface{}{
				"name":    promptName,
				"enabled": false,
			})
		}
		content["prompts"] = prompts
	} else {
		// Re-enabling: remove the entry with that name
		var filtered []interface{}
		for _, entry := range prompts {
			if m, ok := entry.(map[string]interface{}); ok {
				if n, ok := m["name"]; ok && n == promptName {
					continue // skip this entry
				}
			}
			filtered = append(filtered, entry)
		}
		if len(filtered) == 0 {
			delete(content, "prompts")
		} else {
			content["prompts"] = filtered
		}
	}

	// Marshal and write back
	out, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to marshal workspace config: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(rcPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(rcPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write workspace config: %w", err)
	}

	return nil
}

// SaveWorkspaceRCProcessorEnabled updates the enabled state of a processor in the workspace .mittorc file.
// Mirrors the prompts pattern: uses a "processors:" section with [{name, enabled}] entries.
// When changing state: adds or updates the entry with {name: processorName, enabled: value}.
// When restoring default (enabled=true for a normally-enabled processor): removes the entry.
// If the processors array becomes empty, removes the key.
// Also cleans up any legacy "disabled_processors" entries for backward compatibility.
// Only updates the processors section; all other sections are preserved.
func SaveWorkspaceRCProcessorEnabled(workspaceDir, processorName string, enabled bool) error {
	if workspaceDir == "" {
		return fmt.Errorf("workspace directory is required")
	}
	if processorName == "" {
		return fmt.Errorf("processor name is required")
	}

	// Find existing .mittorc file path, or use default
	rcPath, _, err := FindWorkspaceRCPath(workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to check workspace config: %w", err)
	}
	if rcPath == "" {
		rcPath = filepath.Join(workspaceDir, WorkspaceRCFileName)
	}

	// Read existing file content (may not exist yet)
	var content map[string]interface{}
	data, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read workspace config: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &content); err != nil {
			return fmt.Errorf("failed to parse workspace config: %w", err)
		}
	}
	if content == nil {
		content = make(map[string]interface{})
	}

	// Get or create processors section as []interface{} (mirrors prompts pattern)
	var processors []interface{}
	if p, ok := content["processors"]; ok {
		if pSlice, ok := p.([]interface{}); ok {
			processors = pSlice
		}
	}

	// Find existing entry or add/update
	found := false
	for i, entry := range processors {
		if m, ok := entry.(map[string]interface{}); ok {
			if n, ok := m["name"]; ok && n == processorName {
				m["enabled"] = enabled
				processors[i] = m
				found = true
				break
			}
		}
	}
	if !found {
		processors = append(processors, map[string]interface{}{
			"name":    processorName,
			"enabled": enabled,
		})
	}

	if len(processors) == 0 {
		delete(content, "processors")
	} else {
		content["processors"] = processors
	}

	// Clean up legacy disabled_processors: remove this name if present
	if v, ok := content["disabled_processors"]; ok {
		if list, ok := v.([]interface{}); ok {
			var filtered []interface{}
			for _, entry := range list {
				if s, ok := entry.(string); ok && s == processorName {
					continue
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(content, "disabled_processors")
			} else {
				content["disabled_processors"] = filtered
			}
		}
	}
	// Clean up legacy enabled_processors if present
	if v, ok := content["enabled_processors"]; ok {
		if list, ok := v.([]interface{}); ok {
			var filtered []interface{}
			for _, entry := range list {
				if s, ok := entry.(string); ok && s == processorName {
					continue
				}
				filtered = append(filtered, entry)
			}
			if len(filtered) == 0 {
				delete(content, "enabled_processors")
			} else {
				content["enabled_processors"] = filtered
			}
		}
	}

	// Marshal and write back
	out, err := yaml.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to marshal workspace config: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(rcPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(rcPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write workspace config: %w", err)
	}

	return nil
}

// parseWorkspaceRC parses the YAML data from a workspace .mittorc file.
func parseWorkspaceRC(data []byte) (*WorkspaceRC, error) {
	var raw rawWorkspaceRC
	if err := yaml.Unmarshal(data, &raw); err != nil {
		// Return nil for parse errors with invalid YAML
		// This allows graceful degradation when the file is malformed
		return nil, err
	}

	rc := &WorkspaceRC{
		LoadedAt: time.Now(),
	}

	// Copy prompts
	for _, p := range raw.Prompts {
		if p.Name == "" {
			continue
		}
		// Allow empty prompt text only when disabled (used to suppress same-named prompts)
		isDisabled := p.Enabled != nil && !*p.Enabled
		if p.Prompt == "" && !isDisabled {
			continue
		}
		wp := WebPrompt{
			Name:            p.Name,
			Prompt:          p.Prompt,
			BackgroundColor: p.BackgroundColor,
			Description:     p.Description,
			Group:           p.Group,
			EnabledWhenACP:  p.EnabledWhenACP,
			EnabledWhenMCP:  p.EnabledWhenMCP,
			EnabledWhen:     p.EnabledWhen,
			Enabled:         p.Enabled,
		}
		// Translate shorthand fields to enabledWhen CEL expression for backward compatibility.
		if wp.EnabledWhenACP != "" || wp.EnabledWhenMCP != "" {
			wp.EnabledWhen = TranslateShorthandToEnabledWhen(wp.EnabledWhenACP, wp.EnabledWhenMCP, wp.EnabledWhen)
		}
		rc.Prompts = append(rc.Prompts, wp)
	}

	// Copy prompts directories
	rc.PromptsDirs = raw.PromptsDirs

	// Copy processors directories
	rc.ProcessorsDirs = raw.ProcessorsDirs

	// Copy processor overrides from the new "processors:" section.
	for _, p := range raw.ProcessorOverrides {
		if p.Name == "" {
			continue
		}
		rc.ProcessorOverrides = append(rc.ProcessorOverrides, ProcessorOverride{
			Name:    p.Name,
			Enabled: p.Enabled,
		})
	}
	// Backward compatibility: migrate legacy "disabled_processors:" entries.
	// Convert each disabled name into a ProcessorOverride with enabled=false,
	// but only if not already overridden by the new format.
	if len(raw.DisabledProcessors) > 0 {
		overridden := make(map[string]bool)
		for _, o := range rc.ProcessorOverrides {
			overridden[o.Name] = true
		}
		for _, name := range raw.DisabledProcessors {
			if !overridden[name] {
				f := false
				rc.ProcessorOverrides = append(rc.ProcessorOverrides, ProcessorOverride{
					Name:    name,
					Enabled: &f,
				})
			}
		}
	}

	// Copy conversations config
	if raw.Conversations != nil && raw.Conversations.Processing != nil {
		processors := make([]MessageProcessor, 0, len(raw.Conversations.Processing.Processors))
		for _, p := range raw.Conversations.Processing.Processors {
			processors = append(processors, MessageProcessor{
				When:     ProcessorWhen(p.When),
				Position: ProcessorPosition(p.Position),
				Text:     p.Text,
			})
		}
		if len(processors) > 0 || raw.Conversations.Processing.Override {
			rc.Conversations = &ConversationsConfig{
				Processing: &ConversationProcessing{
					Override:   raw.Conversations.Processing.Override,
					Processors: processors,
				},
			}
		}
	}

	// Copy per-agent restricted runner configs
	rc.RestrictedRunners = raw.RestrictedRunners

	// Copy metadata (description, URL, user data schema)
	if raw.Metadata != nil {
		rc.Metadata = &WorkspaceMetadata{}

		if raw.Metadata.Description != "" {
			rc.Metadata.Description = raw.Metadata.Description
		}
		if raw.Metadata.URL != "" {
			rc.Metadata.URL = raw.Metadata.URL
		}
		if raw.Metadata.Group != "" {
			rc.Metadata.Group = raw.Metadata.Group
		}

		// Copy user data schema from metadata.user_data
		if len(raw.Metadata.UserData) > 0 {
			fields := make([]UserDataSchemaField, 0, len(raw.Metadata.UserData))
			for _, f := range raw.Metadata.UserData {
				if f.Name != "" {
					fields = append(fields, UserDataSchemaField{
						Name:        f.Name,
						Description: f.Description,
						Type:        UserDataAttributeType(f.Type),
					})
				}
			}
			if len(fields) > 0 {
				rc.Metadata.UserDataSchema = &UserDataSchema{
					Fields: fields,
				}
			}
		}

		// If metadata has no meaningful content, set to nil
		if rc.Metadata.Description == "" && rc.Metadata.URL == "" && rc.Metadata.Group == "" && rc.Metadata.UserDataSchema == nil {
			rc.Metadata = nil
		}
	}

	// Backward compatibility: check old location (conversations.user_data)
	if raw.Conversations != nil && len(raw.Conversations.UserData) > 0 {
		// Only use old location if new location doesn't have user_data
		if rc.Metadata == nil || rc.Metadata.UserDataSchema == nil {
			fields := make([]UserDataSchemaField, 0, len(raw.Conversations.UserData))
			for _, f := range raw.Conversations.UserData {
				if f.Name != "" {
					fields = append(fields, UserDataSchemaField{
						Name:        f.Name,
						Description: f.Description,
						Type:        UserDataAttributeType(f.Type),
					})
				}
			}
			if len(fields) > 0 {
				if rc.Metadata == nil {
					rc.Metadata = &WorkspaceMetadata{}
				}
				rc.Metadata.UserDataSchema = &UserDataSchema{
					Fields: fields,
				}
			}
		}
	}

	return rc, nil
}

// WorkspaceRCCache provides a thread-safe cache for workspace .mittorc files
// with periodic reload support.
type WorkspaceRCCache struct {
	mu          sync.RWMutex
	cache       map[string]*WorkspaceRC // keyed by workspace directory
	reloadAfter time.Duration           // how long to cache before reloading
}

// NewWorkspaceRCCache creates a new cache with the specified reload interval.
// If reloadAfter is 0, defaults to 30 seconds.
func NewWorkspaceRCCache(reloadAfter time.Duration) *WorkspaceRCCache {
	if reloadAfter == 0 {
		reloadAfter = 30 * time.Second
	}
	return &WorkspaceRCCache{
		cache:       make(map[string]*WorkspaceRC),
		reloadAfter: reloadAfter,
	}
}

// Get returns the cached workspace RC or loads it if not cached or stale.
// Returns nil if no .mittorc exists for the workspace.
// The cache is invalidated if the file's modification time has changed.
func (c *WorkspaceRCCache) Get(workspaceDir string) (*WorkspaceRC, error) {
	if workspaceDir == "" {
		return nil, nil
	}

	c.mu.RLock()
	cached, exists := c.cache[workspaceDir]
	c.mu.RUnlock()

	// Check if cache is fresh (cached can be nil if we previously marked it as "no file")
	if exists && cached != nil && time.Since(cached.LoadedAt) < c.reloadAfter {
		// Within reload interval, check if file has actually changed
		currentModTime := GetWorkspaceRCModTime(workspaceDir)

		// Handle file deletion: if file is gone and we have cached data, clear it
		if currentModTime.IsZero() {
			c.mu.Lock()
			c.cache[workspaceDir] = nil // Mark as "no file"
			c.mu.Unlock()
			return nil, nil
		}

		// If mod time matches, use cached value
		if cached.FileModTime.Equal(currentModTime) {
			return cached, nil
		}
		// File has changed, fall through to reload
	}

	// Handle case where we cached nil (no file) - check if file appeared
	if exists && cached == nil {
		currentModTime := GetWorkspaceRCModTime(workspaceDir)
		if currentModTime.IsZero() {
			// File still doesn't exist
			return nil, nil
		}
		// File now exists, fall through to load
	}

	// Load or reload
	rc, err := LoadWorkspaceRC(workspaceDir)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[workspaceDir] = rc
	c.mu.Unlock()

	return rc, nil
}

// GetLastModified returns the file modification time of the .mittorc file.
// Always checks the current file state to handle file deletion correctly.
// Returns zero time if the file doesn't exist.
func (c *WorkspaceRCCache) GetLastModified(workspaceDir string) time.Time {
	if workspaceDir == "" {
		return time.Time{}
	}

	// Always check the current file state to handle file deletion
	currentModTime := GetWorkspaceRCModTime(workspaceDir)

	// If file is deleted, ensure cache is invalidated
	if currentModTime.IsZero() {
		c.mu.Lock()
		if cached, exists := c.cache[workspaceDir]; exists && cached != nil {
			c.cache[workspaceDir] = nil // Mark as "no file"
		}
		c.mu.Unlock()
		return time.Time{}
	}

	return currentModTime
}

// Invalidate removes the cached entry for a workspace directory.
func (c *WorkspaceRCCache) Invalidate(workspaceDir string) {
	c.mu.Lock()
	delete(c.cache, workspaceDir)
	c.mu.Unlock()
}

// Clear removes all cached entries.
func (c *WorkspaceRCCache) Clear() {
	c.mu.Lock()
	c.cache = make(map[string]*WorkspaceRC)
	c.mu.Unlock()
}
