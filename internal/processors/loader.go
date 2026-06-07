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
//
// For backward compatibility, if the processors directory does not exist,
// the loader also checks for a legacy "hooks" directory in the same parent
// and loads processors from there instead.
func (l *Loader) Load() ([]*Processor, error) {
	if l.processorsDir == "" {
		return nil, nil
	}

	loadDir := l.processorsDir

	// Check if directory exists; fall back to legacy "hooks" directory
	info, err := os.Stat(loadDir)
	switch {
	case err == nil:
		// Directory exists, use it
	case os.IsNotExist(err):
		// Try legacy "hooks" directory in the same parent
		legacyDir := filepath.Join(filepath.Dir(loadDir), "hooks")
		if legacyInfo, legacyErr := os.Stat(legacyDir); legacyErr == nil && legacyInfo.IsDir() {
			l.logger.Info("Loading processors from legacy 'hooks' directory; consider migrating to 'processors'",
				"legacy_path", legacyDir,
				"new_path", loadDir,
			)
			loadDir = legacyDir
			info = legacyInfo
		} else {
			l.logger.Debug("processors directory does not exist", "path", loadDir)
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("failed to stat processors directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("processors path is not a directory: %s", loadDir)
	}

	var procs []*Processor

	// Walk the processors directory (or legacy hooks directory)
	err = filepath.WalkDir(loadDir, func(path string, d os.DirEntry, err error) error {
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

// LoadFile loads and validates a single processor from a YAML file.
// Exported for use in tests that need to validate individual processor definitions.
func (l *Loader) LoadFile(path string) (*Processor, error) {
	return l.loadProcessorFile(path)
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
	// A processor must have either a command (command-mode), text (text-mode), or prompt (prompt-mode).
	if proc.Command == "" && proc.Text == "" && proc.Prompt == "" {
		return nil, fmt.Errorf("processor must specify either 'command', 'text', or 'prompt'")
	}
	// Prompt-mode: Command and Text must be empty when Prompt is set.
	if proc.Prompt != "" && (proc.Command != "" || proc.Text != "") {
		return nil, fmt.Errorf("processor with 'prompt' must not specify 'command' or 'text'")
	}

	// Validate outputFormat
	if proc.OutputFormat != "" && proc.OutputFormat != OutputFormatRaw && proc.OutputFormat != OutputFormatJSON {
		return nil, fmt.Errorf("processor 'outputFormat' has invalid value %q; must be 'raw' or 'json'", proc.OutputFormat)
	}
	if proc.OutputFormat != "" && proc.Command == "" {
		return nil, fmt.Errorf("processor 'outputFormat' is only valid for command-mode processors")
	}

	// Validate when.on
	if proc.When.On == "" {
		return nil, fmt.Errorf("processor 'when.on' is required (must be 'userPrompt', 'agentResponded', or 'agentIdle')")
	}
	if proc.When.On != PhaseUserPrompt && proc.When.On != PhaseAgentResponded && proc.When.On != PhaseAgentIdle {
		return nil, fmt.Errorf("processor 'when.on' has invalid value %q; must be 'userPrompt', 'agentResponded', or 'agentIdle'", proc.When.On)
	}

	// Validate when.match
	if proc.When.Match == "" {
		return nil, fmt.Errorf("processor 'when.match' is required (must be 'first', 'all', or 'allExceptFirst')")
	}
	switch proc.When.Match {
	case MatchFirst, MatchAll, MatchAllExceptFirst:
		// valid
	case "all-except-first":
		return nil, fmt.Errorf("processor 'when.match' value %q is no longer accepted; use 'allExceptFirst' (camelCase) — kebab-case is no longer accepted", proc.When.Match)
	default:
		return nil, fmt.Errorf("processor 'when.match' has invalid value %q; must be 'first', 'all', or 'allExceptFirst'", proc.When.Match)
	}

	// Phase-specific validation
	switch proc.When.On {
	case PhaseAgentResponded, PhaseAgentIdle:
		// agentResponded and agentIdle share identical execution rules; only the
		// firing point differs (agentIdle additionally waits for the queue to drain).
		// Text mode forbidden for after-phase processors
		if proc.Text != "" {
			return nil, fmt.Errorf("processor 'text' is not allowed for 'when.on: %s' (use command or prompt mode)", proc.When.On)
		}
		// mutate forbidden for after-phase processors
		if proc.Mutate != "" {
			return nil, fmt.Errorf("processor 'mutate' is not allowed for 'when.on: %s'", proc.When.On)
		}
		// rerun forbidden for after-phase processors
		if proc.When.Rerun != nil {
			return nil, fmt.Errorf("processor 'when.rerun' is not allowed for 'when.on: %s'", proc.When.On)
		}
		// output transform/prepend/append forbidden for after-phase processors
		if proc.Output == OutputTransform || proc.Output == OutputPrepend || proc.Output == OutputAppend {
			return nil, fmt.Errorf("processor 'output: %s' is not allowed for 'when.on: %s'; use 'discard', 'notify', 'actionButtons', or 'userData'", proc.Output, proc.When.On)
		}
		// Default stopReasons to ["end_turn"] if not specified.
		// These match the ACP SDK StopReason constants (snake_case).
		if len(proc.When.StopReasons) == 0 {
			proc.When.StopReasons = []string{"end_turn"}
		}
		// cadence validation (rules 12-15)
		if proc.When.Cadence != nil {
			c := proc.When.Cadence
			// Rule 12: cadence + match:first is disallowed (first-response semantics are implicit)
			if proc.When.Match == MatchFirst {
				return nil, fmt.Errorf("processor 'when.cadence' is not allowed with 'when.match: first' (first-response semantics are already built-in)")
			}
			// Rule 13: at least one threshold must be specified
			if c.EveryNTurns == 0 && c.EveryNTokens == 0 && c.AfterInterval == "" {
				return nil, fmt.Errorf("processor 'when.cadence' must specify at least one of: everyNTurns, everyNTokens, afterInterval")
			}
			// Rule 14: EveryNTurns must be ≥ 1 when specified
			if c.EveryNTurns < 0 {
				return nil, fmt.Errorf("processor 'when.cadence.everyNTurns' must be ≥ 1, got %d", c.EveryNTurns)
			}
			// Rule 15: EveryNTokens must be ≥ 1 when specified
			if c.EveryNTokens < 0 {
				return nil, fmt.Errorf("processor 'when.cadence.everyNTokens' must be ≥ 1, got %d", c.EveryNTokens)
			}
			// Rule 16: AfterInterval must be a valid Go duration string
			if c.AfterInterval != "" && c.GetAfterIntervalDuration() == 0 {
				return nil, fmt.Errorf("processor 'when.cadence.afterInterval' has invalid duration %q (use Go duration syntax, e.g. '5m', '1h')", c.AfterInterval)
			}
		}

	case PhaseUserPrompt:
		// stopReasons and excludeOrigins are only for agentResponded
		if len(proc.When.StopReasons) > 0 {
			return nil, fmt.Errorf("processor 'when.stopReasons' is only valid for 'when.on: agentResponded'")
		}
		if len(proc.When.ExcludeOrigins) > 0 {
			return nil, fmt.Errorf("processor 'when.excludeOrigins' is only valid for 'when.on: agentResponded'")
		}
		// rerun only valid with match:first
		if proc.When.Rerun != nil {
			if proc.When.Match != MatchFirst {
				return nil, fmt.Errorf("'when.rerun' is only supported with 'when.match: first', got 'when.match: %s'", proc.When.Match)
			}
			if err := proc.When.Rerun.Validate(); err != nil {
				return nil, fmt.Errorf("invalid rerun config: %w", err)
			}
		}
		// Text mode requires mutate
		if proc.IsTextMode() && proc.Mutate == "" {
			return nil, fmt.Errorf("text-mode processor requires 'mutate: prepend' or 'mutate: append'")
		}
	}

	// Set internal fields
	proc.FilePath = path
	proc.HookDir = filepath.Dir(path)

	return &proc, nil
}
