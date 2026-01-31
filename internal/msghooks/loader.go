package msghooks

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads hooks from the hooks directory.
type Loader struct {
	hooksDir string
	logger   *slog.Logger
}

// NewLoader creates a new hook loader for the given directory.
func NewLoader(hooksDir string, logger *slog.Logger) *Loader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Loader{
		hooksDir: hooksDir,
		logger:   logger,
	}
}

// Load discovers and parses all YAML files in the hooks directory.
// Returns hooks sorted by priority (lower priority first).
// Files in subdirectories named "disabled" are skipped.
func (l *Loader) Load() ([]*Hook, error) {
	if l.hooksDir == "" {
		return nil, nil
	}

	// Check if directory exists
	info, err := os.Stat(l.hooksDir)
	if os.IsNotExist(err) {
		l.logger.Debug("hooks directory does not exist", "path", l.hooksDir)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat hooks directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("hooks path is not a directory: %s", l.hooksDir)
	}

	var hooks []*Hook

	// Walk the hooks directory
	err = filepath.WalkDir(l.hooksDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			l.logger.Warn("error accessing path", "path", path, "error", err)
			return nil // Continue walking
		}

		// Skip directories
		if d.IsDir() {
			// Skip "disabled" directories
			if d.Name() == "disabled" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process YAML files
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		hook, err := l.loadHookFile(path)
		if err != nil {
			l.logger.Warn("failed to load hook file", "path", path, "error", err)
			return nil // Continue with other files
		}

		if hook != nil {
			hooks = append(hooks, hook)
			l.logger.Debug("loaded hook", "name", hook.Name, "path", path)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk hooks directory: %w", err)
	}

	// Sort by priority (lower first)
	sort.Slice(hooks, func(i, j int) bool {
		return hooks[i].GetPriority() < hooks[j].GetPriority()
	})

	l.logger.Info("loaded hooks", "count", len(hooks))
	return hooks, nil
}

// loadHookFile loads a single hook from a YAML file.
func (l *Loader) loadHookFile(path string) (*Hook, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var hook Hook
	if err := yaml.Unmarshal(data, &hook); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate required fields
	if hook.Name == "" {
		return nil, fmt.Errorf("hook name is required")
	}
	if hook.Command == "" {
		return nil, fmt.Errorf("hook command is required")
	}
	if hook.When == "" {
		return nil, fmt.Errorf("hook 'when' is required")
	}

	// Set internal fields
	hook.FilePath = path
	hook.HookDir = filepath.Dir(path)

	return &hook, nil
}

// LoadFromDir is a convenience function to load hooks from a directory.
func LoadFromDir(hooksDir string, logger *slog.Logger) ([]*Hook, error) {
	loader := NewLoader(hooksDir, logger)
	return loader.Load()
}
