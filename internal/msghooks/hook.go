package msghooks

import (
	"path/filepath"
	"strings"

	"github.com/inercia/mitto/internal/config"
)

// IsEnabled returns true if the hook is enabled.
func (h *Hook) IsEnabled() bool {
	if h.Enabled == nil {
		return true // Default to enabled
	}
	return *h.Enabled
}

// GetTimeout returns the hook's timeout, using the default if not set.
func (h *Hook) GetTimeout() Duration {
	if h.Timeout == 0 {
		return Duration(DefaultTimeout)
	}
	return h.Timeout
}

// GetPriority returns the hook's priority, using the default if not set.
func (h *Hook) GetPriority() int {
	if h.Priority == 0 {
		return DefaultPriority
	}
	return h.Priority
}

// GetInput returns the hook's input type, using the default if not set.
func (h *Hook) GetInput() InputType {
	if h.Input == "" {
		return DefaultInput
	}
	return h.Input
}

// GetOutput returns the hook's output type, using the default if not set.
func (h *Hook) GetOutput() OutputType {
	if h.Output == "" {
		return DefaultOutput
	}
	return h.Output
}

// GetWorkingDir returns the hook's working directory type, using the default if not set.
func (h *Hook) GetWorkingDir() WorkingDirType {
	if h.WorkingDir == "" {
		return DefaultWorkingDir
	}
	return h.WorkingDir
}

// GetOnError returns the hook's error handling, using the default if not set.
func (h *Hook) GetOnError() ErrorHandling {
	if h.OnError == "" {
		return DefaultErrorHandle
	}
	return h.OnError
}

// GetPosition returns the hook's position, using prepend as default.
func (h *Hook) GetPosition() config.ProcessorPosition {
	if h.Position == "" {
		return config.ProcessorPositionPrepend
	}
	return h.Position
}

// ShouldApply returns true if the hook should apply given the message context.
func (h *Hook) ShouldApply(isFirstMessage bool, workingDir string) bool {
	if !h.IsEnabled() {
		return false
	}

	// Check workspace filter
	if len(h.Workspaces) > 0 {
		matched := false
		for _, ws := range h.Workspaces {
			if ws == workingDir || strings.HasPrefix(workingDir, ws+string(filepath.Separator)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check when condition
	switch h.When {
	case config.ProcessorWhenFirst:
		return isFirstMessage
	case config.ProcessorWhenAll:
		return true
	case config.ProcessorWhenAllExceptFirst:
		return !isFirstMessage
	default:
		return false
	}
}

// ResolveCommand resolves the command path.
// If the command starts with "./" or "../", it's resolved relative to the hook's directory.
// Otherwise, it's returned as-is (absolute path or PATH lookup).
func (h *Hook) ResolveCommand() string {
	if strings.HasPrefix(h.Command, "./") || strings.HasPrefix(h.Command, "../") {
		return filepath.Join(h.HookDir, h.Command)
	}
	return h.Command
}
