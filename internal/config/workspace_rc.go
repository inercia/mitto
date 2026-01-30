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
// Only the prompts section is currently supported; other sections are ignored.
type WorkspaceRC struct {
	// Prompts is the list of workspace-specific prompts.
	Prompts []WebPrompt `json:"prompts,omitempty"`
	// LoadedAt is the time when this config was loaded.
	LoadedAt time.Time `json:"-"`
}

// rawWorkspaceRC is used for YAML unmarshaling of workspace .mittorc files.
// It uses a permissive structure to ignore unsupported sections.
type rawWorkspaceRC struct {
	// Prompts section - the only currently supported section
	Prompts []struct {
		Name            string `yaml:"name"`
		Prompt          string `yaml:"prompt"`
		BackgroundColor string `yaml:"backgroundColor"`
	} `yaml:"prompts"`
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

	return parseWorkspaceRC(data)
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
func (c *WorkspaceRCCache) Get(workspaceDir string) (*WorkspaceRC, error) {
	if workspaceDir == "" {
		return nil, nil
	}

	c.mu.RLock()
	cached, exists := c.cache[workspaceDir]
	c.mu.RUnlock()

	// Return cached if exists and not stale
	if exists && cached != nil && time.Since(cached.LoadedAt) < c.reloadAfter {
		return cached, nil
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

