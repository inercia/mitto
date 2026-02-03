package config

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
)

// PromptsCache provides cached access to global prompts with on-demand reload.
// The cache supports multiple directories with proper priority ordering:
//  1. Default MITTO_DIR/prompts/ (always included)
//  2. Additional directories from global config (prompts_dirs in settings)
//  3. Workspace-specific directories (prompts_dirs in .mittorc)
//
// Later directories override earlier ones when prompts have the same name.
type PromptsCache struct {
	mu sync.RWMutex

	// prompts is the cached list of parsed prompt files
	prompts []*PromptFile

	// webPrompts is the cached list of WebPrompt (for API responses)
	webPrompts []WebPrompt

	// loadedAt is when the cache was last loaded
	loadedAt time.Time

	// dirModTimes tracks modification times for all directories
	dirModTimes map[string]time.Time

	// defaultDir is the default MITTO_DIR/prompts/ directory
	defaultDir string

	// additionalDirs are extra directories from global config
	additionalDirs []string

	// workspaceDirs are directories from workspace .mittorc (resolved to absolute paths)
	workspaceDirs []string

	// workspaceRoot is the workspace directory for resolving relative paths
	workspaceRoot string
}

// NewPromptsCache creates a new prompts cache.
func NewPromptsCache() *PromptsCache {
	return &PromptsCache{
		dirModTimes: make(map[string]time.Time),
	}
}

// SetAdditionalDirs sets the additional directories from global config.
// These directories are searched after the default MITTO_DIR/prompts/.
// Paths should be absolute.
func (c *PromptsCache) SetAdditionalDirs(dirs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.additionalDirs = dirs
	// Clear cache to force reload with new directories
	c.dirModTimes = make(map[string]time.Time)
}

// SetWorkspaceDirs sets the workspace-specific directories.
// Relative paths are resolved against workspaceRoot.
// These directories are searched last (highest priority).
func (c *PromptsCache) SetWorkspaceDirs(workspaceRoot string, dirs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.workspaceRoot = workspaceRoot

	// Resolve relative paths to absolute
	c.workspaceDirs = make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if filepath.IsAbs(dir) {
			c.workspaceDirs = append(c.workspaceDirs, dir)
		} else if workspaceRoot != "" {
			c.workspaceDirs = append(c.workspaceDirs, filepath.Join(workspaceRoot, dir))
		}
		// Skip relative paths if no workspace root is set
	}

	// Clear cache to force reload with new directories
	c.dirModTimes = make(map[string]time.Time)
}

// ClearWorkspaceDirs clears the workspace-specific directories.
// Called when switching workspaces or when workspace .mittorc is removed.
func (c *PromptsCache) ClearWorkspaceDirs() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workspaceDirs = nil
	c.workspaceRoot = ""
	// Clear cache to force reload
	c.dirModTimes = make(map[string]time.Time)
}

// getAllDirs returns all directories to search, in priority order.
// Returns: default dir, additional dirs, workspace dirs
func (c *PromptsCache) getAllDirs() ([]string, error) {
	// Get default directory if not cached
	if c.defaultDir == "" {
		var err error
		c.defaultDir, err = appdir.PromptsDir()
		if err != nil {
			return nil, err
		}
	}

	dirs := make([]string, 0, 1+len(c.additionalDirs)+len(c.workspaceDirs))

	// 1. Default MITTO_DIR/prompts/ (lowest priority)
	dirs = append(dirs, c.defaultDir)

	// 2. Additional directories from global config
	dirs = append(dirs, c.additionalDirs...)

	// 3. Workspace directories (highest priority)
	dirs = append(dirs, c.workspaceDirs...)

	return dirs, nil
}

// needsReload checks if any directory has been modified since last load.
func (c *PromptsCache) needsReload(dirs []string) bool {
	if c.prompts == nil {
		return true
	}

	for _, dir := range dirs {
		currentModTime := GetPromptsDirModTime(dir)
		cachedModTime, exists := c.dirModTimes[dir]

		// New directory or modified directory
		if !exists || !cachedModTime.Equal(currentModTime) {
			return true
		}
	}

	return false
}

// Get returns the cached prompts, reloading if any directory has changed.
// This is called when the prompts dropdown is opened.
func (c *PromptsCache) Get() ([]*PromptFile, error) {
	c.mu.RLock()
	dirs, err := c.getAllDirs()
	if err != nil {
		c.mu.RUnlock()
		return nil, err
	}
	needsReload := c.needsReload(dirs)
	prompts := c.prompts
	c.mu.RUnlock()

	if !needsReload && prompts != nil {
		return prompts, nil
	}

	return c.reload()
}

// reload loads prompts from all directories and updates the cache.
func (c *PromptsCache) reload() ([]*PromptFile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dirs, err := c.getAllDirs()
	if err != nil {
		return nil, err
	}

	// Double-check after acquiring write lock
	if !c.needsReload(dirs) && c.prompts != nil {
		return c.prompts, nil
	}

	// Load prompts from all directories, merging by name
	// Later directories override earlier ones
	promptsByName := make(map[string]*PromptFile)
	newModTimes := make(map[string]time.Time)

	for _, dir := range dirs {
		modTime := GetPromptsDirModTime(dir)
		newModTimes[dir] = modTime

		// Skip non-existent directories silently
		dirPrompts, err := LoadPromptsFromDir(dir)
		if err != nil {
			// Log warning but continue with other directories
			continue
		}

		// Merge prompts - later ones override earlier ones by name
		for _, p := range dirPrompts {
			promptsByName[p.Name] = p
		}
	}

	// Convert map to slice
	prompts := make([]*PromptFile, 0, len(promptsByName))
	for _, p := range promptsByName {
		prompts = append(prompts, p)
	}

	c.prompts = prompts
	c.webPrompts = PromptsToWebPrompts(prompts)
	c.loadedAt = time.Now()
	c.dirModTimes = newModTimes

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

// GetWebPromptsForACP returns the cached prompts filtered by ACP server.
// If acpServer is empty, returns all prompts (no filtering).
func (c *PromptsCache) GetWebPromptsForACP(acpServer string) ([]WebPrompt, error) {
	// Ensure cache is fresh
	prompts, err := c.Get()
	if err != nil {
		return nil, err
	}

	// If no ACP server specified, return all prompts
	if acpServer == "" {
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.webPrompts, nil
	}

	// Filter prompts by ACP server
	filtered := FilterPromptsByACP(prompts, acpServer)
	return PromptsToWebPrompts(filtered), nil
}

// GetWebPromptsSpecificToACP returns prompts that are specifically targeted at the given ACP server.
// Unlike GetWebPromptsForACP, this excludes generic prompts (with empty acps: field).
// This is used to show ACP-specific prompts in the server settings UI.
func (c *PromptsCache) GetWebPromptsSpecificToACP(acpServer string) ([]WebPrompt, error) {
	// Ensure cache is fresh
	prompts, err := c.Get()
	if err != nil {
		return nil, err
	}

	// Filter prompts specific to this ACP server
	filtered := FilterPromptsSpecificToACP(prompts, acpServer)
	return PromptsToWebPrompts(filtered), nil
}

// ForceReload clears the cache and reloads from disk.
func (c *PromptsCache) ForceReload() ([]*PromptFile, error) {
	c.mu.Lock()
	c.dirModTimes = make(map[string]time.Time) // Reset mod times to force reload
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

// GetDirectories returns the list of directories being searched.
// Useful for debugging and the CLI prompts list command.
func (c *PromptsCache) GetDirectories() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dirs, _ := c.getAllDirs()
	return dirs
}
