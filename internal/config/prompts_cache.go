package config

import (
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
)

// PromptsCache provides cached access to global prompts with on-demand reload.
// The cache is invalidated when the prompts directory modification time changes.
type PromptsCache struct {
	mu sync.RWMutex

	// prompts is the cached list of parsed prompt files
	prompts []*PromptFile

	// webPrompts is the cached list of WebPrompt (for API responses)
	webPrompts []WebPrompt

	// loadedAt is when the cache was last loaded
	loadedAt time.Time

	// dirModTime is the modification time of the prompts directory when loaded
	dirModTime time.Time

	// promptsDir is the cached path to the prompts directory
	promptsDir string
}

// NewPromptsCache creates a new prompts cache.
func NewPromptsCache() *PromptsCache {
	return &PromptsCache{}
}

// Get returns the cached prompts, reloading if the directory has changed.
// This is called when the prompts dropdown is opened.
func (c *PromptsCache) Get() ([]*PromptFile, error) {
	c.mu.RLock()
	promptsDir := c.promptsDir
	cachedModTime := c.dirModTime
	prompts := c.prompts
	c.mu.RUnlock()

	// Get prompts directory path if not cached
	if promptsDir == "" {
		var err error
		promptsDir, err = appdir.PromptsDir()
		if err != nil {
			return nil, err
		}
	}

	// Check if directory has been modified
	currentModTime := GetPromptsDirModTime(promptsDir)

	// If mod time matches and we have cached data, return it
	if !cachedModTime.IsZero() && cachedModTime.Equal(currentModTime) && prompts != nil {
		return prompts, nil
	}

	// Reload prompts
	return c.reload(promptsDir, currentModTime)
}

// reload loads prompts from disk and updates the cache.
func (c *PromptsCache) reload(promptsDir string, modTime time.Time) ([]*PromptFile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.dirModTime.Equal(modTime) && c.prompts != nil {
		return c.prompts, nil
	}

	prompts, err := LoadPromptsFromDir(promptsDir)
	if err != nil {
		return nil, err
	}

	c.promptsDir = promptsDir
	c.prompts = prompts
	c.webPrompts = PromptsToWebPrompts(prompts)
	c.loadedAt = time.Now()
	c.dirModTime = modTime

	return prompts, nil
}

// GetWebPrompts returns the cached prompts as WebPrompt slice.
// This is the format used by the API.
func (c *PromptsCache) GetWebPrompts() ([]WebPrompt, error) {
	// Ensure cache is fresh
	_, err := c.Get()
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.webPrompts, nil
}

// ForceReload clears the cache and reloads from disk.
func (c *PromptsCache) ForceReload() ([]*PromptFile, error) {
	c.mu.Lock()
	c.dirModTime = time.Time{} // Reset mod time to force reload
	c.mu.Unlock()

	return c.Get()
}

// Count returns the number of cached prompts.
func (c *PromptsCache) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.prompts)
}

// LoadedAt returns when the cache was last loaded.
func (c *PromptsCache) LoadedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loadedAt
}
