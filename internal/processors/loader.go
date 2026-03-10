package processors

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads processors from the processors directory.
type Loader struct {
	processorsDir string
	logger        *slog.Logger
}

// NewLoader creates a new processor loader for the given directory.
func NewLoader(processorsDir string, logger *slog.Logger) *Loader {
	if logger == nil {
		logger = slog.Default()
	}
	return &Loader{
		processorsDir: processorsDir,
		logger:        logger,
	}
}

// Load discovers and parses all YAML files in the processors directory.
// Returns processors sorted by priority (lower priority first).
// Files in subdirectories named "disabled" are skipped.
func (l *Loader) Load() ([]*Processor, error) {
	if l.processorsDir == "" {
		return nil, nil
	}

	// Check if directory exists
	info, err := os.Stat(l.processorsDir)
	if os.IsNotExist(err) {
		l.logger.Debug("processors directory does not exist", "path", l.processorsDir)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat processors directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("processors path is not a directory: %s", l.processorsDir)
	}

	var procs []*Processor

	// Walk the processors directory
	err = filepath.WalkDir(l.processorsDir, func(path string, d os.DirEntry, err error) error {
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

		proc, err := l.loadProcessorFile(path)
		if err != nil {
			l.logger.Warn("failed to load processor file", "path", path, "error", err)
			return nil // Continue with other files
		}

		if proc != nil {
			procs = append(procs, proc)
			l.logger.Debug("loaded processor", "name", proc.Name, "path", path)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk processors directory: %w", err)
	}

	// Sort by priority (lower first)
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].GetPriority() < procs[j].GetPriority()
	})

	l.logger.Info("loaded processors", "count", len(procs))
	return procs, nil
}

// loadProcessorFile loads a single processor from a YAML file.
func (l *Loader) loadProcessorFile(path string) (*Processor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var proc Processor
	if err := yaml.Unmarshal(data, &proc); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate required fields
	if proc.Name == "" {
		return nil, fmt.Errorf("processor name is required")
	}
	// A processor must have either a command (command-mode) or text (text-mode).
	if proc.Command == "" && proc.Text == "" {
		return nil, fmt.Errorf("processor must specify either 'command' or 'text'")
	}
	if proc.When == "" {
		return nil, fmt.Errorf("processor 'when' is required")
	}

	// Set internal fields
	proc.FilePath = path
	proc.HookDir = filepath.Dir(path)

	return &proc, nil
}

