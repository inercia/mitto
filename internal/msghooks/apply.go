package msghooks

import (
	"context"
	"fmt"
	"log/slog"
)

// HookResult contains the result of applying hooks to a message.
type HookResult struct {
	// Message is the transformed message text.
	Message string
	// Attachments contains any file attachments from hooks.
	Attachments []Attachment
}

// ApplyHooks applies all applicable hooks to a message.
// Hooks are applied in priority order (lower priority first).
// Returns the transformed message, attachments, and any error.
func ApplyHooks(ctx context.Context, hooks []*Hook, input *HookInput, hooksDir string, logger *slog.Logger) (*HookResult, error) {
	if len(hooks) == 0 {
		return &HookResult{Message: input.Message}, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	executor := NewExecutor(hooksDir, logger)
	result := &HookResult{Message: input.Message}

	for _, hook := range hooks {
		// Check if hook should apply
		if !hook.ShouldApply(input.IsFirstMessage, input.WorkingDir) {
			continue
		}

		logger.Debug("applying hook",
			"name", hook.Name,
			"when", hook.When,
			"position", hook.GetPosition(),
		)

		// Create input for this hook with current message
		hookInput := &HookInput{
			Message:        result.Message,
			IsFirstMessage: input.IsFirstMessage,
			SessionID:      input.SessionID,
			WorkingDir:     input.WorkingDir,
			History:        input.History,
		}

		// Execute hook
		output, err := executor.Execute(ctx, hook, hookInput)
		if err != nil {
			logger.Warn("hook execution failed",
				"name", hook.Name,
				"error", err,
			)

			// Handle error based on hook configuration
			if hook.GetOnError() == ErrorFail {
				return nil, fmt.Errorf("hook %q failed: %w", hook.Name, err)
			}
			// ErrorSkip: continue with next hook
			continue
		}

		// Check for error in output
		if output.Error != "" {
			logger.Warn("hook returned error",
				"name", hook.Name,
				"error", output.Error,
			)

			if hook.GetOnError() == ErrorFail {
				return nil, fmt.Errorf("hook %q returned error: %s", hook.Name, output.Error)
			}
			// Use fallback message if provided, otherwise continue
			if output.Message != "" {
				result.Message = output.Message
			}
			continue
		}

		// Apply output based on output type
		switch hook.GetOutput() {
		case OutputTransform:
			if output.Message != "" {
				result.Message = output.Message
			}
		case OutputPrepend:
			if output.Text != "" {
				result.Message = output.Text + result.Message
			}
		case OutputAppend:
			if output.Text != "" {
				result.Message = result.Message + output.Text
			}
		case OutputDiscard:
			// Do nothing with output
		}

		// Collect attachments from all hooks
		if len(output.Attachments) > 0 {
			result.Attachments = append(result.Attachments, output.Attachments...)
			logger.Debug("hook added attachments",
				"name", hook.Name,
				"count", len(output.Attachments),
			)
		}

		logger.Debug("hook applied",
			"name", hook.Name,
			"output_type", hook.GetOutput(),
		)
	}

	return result, nil
}

// Manager provides a high-level interface for loading and applying hooks.
type Manager struct {
	hooksDir string
	hooks    []*Hook
	logger   *slog.Logger
}

// NewManager creates a new hook manager.
func NewManager(hooksDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		hooksDir: hooksDir,
		logger:   logger,
	}
}

// Load loads all hooks from the hooks directory.
func (m *Manager) Load() error {
	loader := NewLoader(m.hooksDir, m.logger)
	hooks, err := loader.Load()
	if err != nil {
		return err
	}
	m.hooks = hooks
	return nil
}

// Hooks returns the loaded hooks.
func (m *Manager) Hooks() []*Hook {
	return m.hooks
}

// Apply applies all applicable hooks to a message.
// Returns the hook result containing the transformed message and any attachments.
func (m *Manager) Apply(ctx context.Context, input *HookInput) (*HookResult, error) {
	return ApplyHooks(ctx, m.hooks, input, m.hooksDir, m.logger)
}

// HooksDir returns the hooks directory path.
func (m *Manager) HooksDir() string {
	return m.hooksDir
}

// ToACPAttachments converts hook attachments to a format suitable for ACP.
// It reads file contents for path-based attachments and returns base64-encoded data.
func (r *HookResult) ToACPAttachments(workingDir string) ([]AttachmentData, error) {
	if len(r.Attachments) == 0 {
		return nil, nil
	}

	result := make([]AttachmentData, 0, len(r.Attachments))
	for _, att := range r.Attachments {
		data, err := att.ResolveData(workingDir)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve attachment %q: %w", att.Name, err)
		}
		result = append(result, data)
	}
	return result, nil
}

// AttachmentData contains resolved attachment data ready for ACP.
type AttachmentData struct {
	Type     string // "image", "text", "file"
	Data     string // base64-encoded content
	MimeType string
	Name     string
}
