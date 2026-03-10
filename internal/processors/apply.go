package processors

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/inercia/mitto/internal/config"
)

// ProcessorResult contains the result of applying processors to a message.
type ProcessorResult struct {
	// Message is the transformed message text.
	Message string
	// Attachments contains any file attachments from processors.
	Attachments []Attachment
}

// ApplyProcessors applies all applicable processors to a message.
// Processors are applied in priority order (lower priority first).
// Returns the transformed message, attachments, and any error.
func ApplyProcessors(ctx context.Context, procs []*Processor, input *ProcessorInput, processorsDir string, logger *slog.Logger) (*ProcessorResult, error) {
	if len(procs) == 0 {
		return &ProcessorResult{Message: input.Message}, nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	executor := NewExecutor(processorsDir, logger)
	result := &ProcessorResult{Message: input.Message}

	for _, proc := range procs {
		// Check if processor should apply
		if !proc.ShouldApply(input.IsFirstMessage, input.WorkingDir) {
			continue
		}

		logger.Debug("applying processor",
			"name", proc.Name,
			"when", proc.When,
			"mode", map[bool]string{true: "text", false: "command"}[proc.IsTextMode()],
			"position", proc.GetPosition(),
		)

		// Text-mode: directly prepend or append the static text (no external command).
		if proc.IsTextMode() {
			switch proc.GetPosition() {
			case config.ProcessorPositionPrepend:
				result.Message = proc.Text + result.Message
			case config.ProcessorPositionAppend:
				result.Message = result.Message + proc.Text
			}
			logger.Debug("text-mode processor applied",
				"name", proc.Name,
				"position", proc.GetPosition(),
			)
			continue
		}

		// Command-mode: create per-iteration input with current message state.
		procInput := &ProcessorInput{
			Message:             result.Message,
			IsFirstMessage:      input.IsFirstMessage,
			SessionID:           input.SessionID,
			WorkingDir:          input.WorkingDir,
			History:             input.History,
			ParentSessionID:     input.ParentSessionID,
			SessionName:         input.SessionName,
			ACPServer:           input.ACPServer,
			WorkspaceUUID:       input.WorkspaceUUID,
			AvailableACPServers: input.AvailableACPServers,
		}

		// Execute processor
		output, err := executor.Execute(ctx, proc, procInput)
		if err != nil {
			logger.Warn("processor execution failed",
				"name", proc.Name,
				"error", err,
			)

			// Handle error based on processor configuration
			if proc.GetOnError() == ErrorFail {
				return nil, fmt.Errorf("processor %q failed: %w", proc.Name, err)
			}
			// ErrorSkip: continue with next processor
			continue
		}

		// Check for error in output
		if output.Error != "" {
			logger.Warn("processor returned error",
				"name", proc.Name,
				"error", output.Error,
			)

			if proc.GetOnError() == ErrorFail {
				return nil, fmt.Errorf("processor %q returned error: %s", proc.Name, output.Error)
			}
			// Use fallback message if provided, otherwise continue
			if output.Message != "" {
				result.Message = output.Message
			}
			continue
		}

		// Apply output based on output type
		switch proc.GetOutput() {
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
				result.Message += output.Text
			}
		case OutputDiscard:
			// Do nothing with output
		}

		// Collect attachments from all processors
		if len(output.Attachments) > 0 {
			result.Attachments = append(result.Attachments, output.Attachments...)
			logger.Debug("processor added attachments",
				"name", proc.Name,
				"count", len(output.Attachments),
			)
		}

		logger.Debug("processor applied",
			"name", proc.Name,
			"output_type", proc.GetOutput(),
		)
	}

	return result, nil
}

// Manager provides a high-level interface for loading and applying processors.
type Manager struct {
	processorsDir string
	processors    []*Processor
	logger        *slog.Logger
}

// NewManager creates a new processor manager.
func NewManager(processorsDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		processorsDir: processorsDir,
		logger:        logger,
	}
}

// AddTextProcessors converts config.MessageProcessor entries into unified Processor
// entries and merges them into the manager's sorted processor list.
//
// The priority parameter controls where text-mode processors sort relative to
// command-mode processors. Pass 0 to run text-mode processors before all
// command-mode processors (which default to priority 100).
//
// Declaration order is preserved for processors with the same effective priority.
//
// NOTE: This method mutates the receiver. If the Manager is shared across
// goroutines, use CloneWithTextProcessors instead to avoid data races.
func (m *Manager) AddTextProcessors(procs []config.MessageProcessor, priority int) {
	for i, p := range procs {
		proc := &Processor{
			Name:     fmt.Sprintf("text-processor-%d", i),
			When:     p.When,
			Position: p.Position,
			Text:     p.Text,
			Priority: priority,
		}
		m.processors = append(m.processors, proc)
	}
	// Re-sort by priority (stable to preserve relative order within same priority).
	sort.SliceStable(m.processors, func(i, j int) bool {
		return m.processors[i].GetPriority() < m.processors[j].GetPriority()
	})
}

// CloneWithTextProcessors returns a shallow copy of the Manager with the given
// text-mode processors merged in. The original Manager is not modified, making
// this safe to call concurrently on a shared instance.
func (m *Manager) CloneWithTextProcessors(procs []config.MessageProcessor, priority int) *Manager {
	clone := &Manager{
		processorsDir: m.processorsDir,
		logger:        m.logger,
		processors:    make([]*Processor, len(m.processors)),
	}
	copy(clone.processors, m.processors)
	clone.AddTextProcessors(procs, priority)
	return clone
}

// Load loads all processors from the processors directory.
func (m *Manager) Load() error {
	loader := NewLoader(m.processorsDir, m.logger)
	procs, err := loader.Load()
	if err != nil {
		return err
	}
	m.processors = procs
	return nil
}

// Processors returns the loaded processors.
func (m *Manager) Processors() []*Processor {
	return m.processors
}

// Apply applies all applicable processors to a message.
// Returns the processor result containing the transformed message and any attachments.
func (m *Manager) Apply(ctx context.Context, input *ProcessorInput) (*ProcessorResult, error) {
	return ApplyProcessors(ctx, m.processors, input, m.processorsDir, m.logger)
}

// ProcessorsDir returns the processors directory path.
func (m *Manager) ProcessorsDir() string {
	return m.processorsDir
}

// ToACPAttachments converts processor attachments to a format suitable for ACP.
// It reads file contents for path-based attachments and returns base64-encoded data.
func (r *ProcessorResult) ToACPAttachments(workingDir string) ([]AttachmentData, error) {
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
