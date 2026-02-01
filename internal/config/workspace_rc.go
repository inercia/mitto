// Package config handles configuration loading and management for Mitto.
package config

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkspaceRCFileName is the name of the workspace-specific config file.
const WorkspaceRCFileName = ".mittorc"

// WorkspaceRC represents workspace-specific configuration loaded from .mittorc.
// Supports prompts and conversations sections; other sections are ignored.
type WorkspaceRC struct {
	// Prompts is the list of workspace-specific prompts.
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// Conversations contains workspace-specific conversation processing configuration.
	Conversations *ConversationsConfig `json:"conversations,omitempty"`
	// LoadedAt is the time when this config was loaded.
	LoadedAt time.Time `json:"-"`
	// FileModTime is the modification time of the .mittorc file when loaded.
	// Used to detect file changes efficiently.
	FileModTime time.Time `json:"-"`
}

// rawWorkspaceRC is used for YAML unmarshaling of workspace .mittorc files.
// It uses a permissive structure to ignore unsupported sections.
type rawWorkspaceRC struct {
	// Prompts section
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
	} `yaml:"prompts"`
	// Conversations section for message processing
	Conversations *struct {
		Processing *struct {
			Override   bool `yaml:"override"`
			Processors []struct {
				When     string `yaml:"when"`
				Position string `yaml:"position"`
				Text     string `yaml:"text"`
			} `yaml:"processors"`
		} `yaml:"processing"`
	} `yaml:"conversations"`
}

// LoadWorkspaceRC loads the .mittorc file from a workspace directory.
// Returns nil if the file doesn't exist or is empty.
// Returns an error only if the file exists but cannot be parsed.
func LoadWorkspaceRC(workspaceDir string) (*WorkspaceRC, error) {
	if workspaceDir == "" {
		return nil, nil
	}

	rcPath := filepath.Join(workspaceDir, WorkspaceRCFileName)

	// Check if file exists
	info, err := os.Stat(rcPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
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

// GetWorkspaceRCModTime returns the modification time of the .mittorc file.
// Returns zero time if the file doesn't exist.
func GetWorkspaceRCModTime(workspaceDir string) time.Time {
	if workspaceDir == "" {
		return time.Time{}
	}
	rcPath := filepath.Join(workspaceDir, WorkspaceRCFileName)
	info, err := os.Stat(rcPath)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
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
		if p.Name != "" && p.Prompt != "" {
			rc.Prompts = append(rc.Prompts, WebPrompt{
				Name:            p.Name,
				Prompt:          p.Prompt,
				BackgroundColor: p.BackgroundColor,
			})
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
