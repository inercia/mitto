package processors

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

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

	logger.Debug("processor pipeline starting",
		"total_processors", len(procs),
		"is_first_message", input.IsFirstMessage,
		"acp_server", input.ACPServer,
		"session_id", input.SessionID,
	)

	executor := NewExecutor(processorsDir, logger)
	result := &ProcessorResult{Message: input.Message}
	applied := 0
	skipped := 0

	for _, proc := range procs {
		// Check if processor should apply
		shouldApply, skipReason := proc.ShouldApply(input.IsFirstMessage, input)
		if !shouldApply {
			skipped++
			logger.Debug("processor skipped",
				"name", proc.Name,
				"reason", string(skipReason),
				"when", proc.When,
				"priority", proc.GetPriority(),
			)
			continue
		}

		applied++
		logger.Debug("applying processor",
			"name", proc.Name,
			"when", proc.When,
			"mode", map[bool]string{true: "text", false: "command"}[proc.IsTextMode()],
			"position", proc.GetPosition(),
			"priority", proc.GetPriority(),
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

	logger.Debug("processor pipeline complete",
		"total", len(procs),
		"applied", applied,
		"skipped", skipped,
		"attachments", len(result.Attachments),
		"message_length", len(result.Message),
	)

	return result, nil
}

// Manager provides a high-level interface for loading and applying processors.
type Manager struct {
	processorsDir string
	processors    []*Processor
	logger        *slog.Logger

	// rerunState tracks per-processor run state for rerun logic.
	// Keyed by processor name. Only populated for processors with rerun config.
	// In-memory only — not persisted across restarts (isFirstPrompt=true on resume
	// handles restart case).
	rerunState map[string]*processorRunState
}

// processorRunState tracks when a processor last ran, for rerun scheduling.
type processorRunState struct {
	lastRunTime   time.Time
	messagesSince int
}

// NewManager creates a new processor manager.
func NewManager(processorsDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		processorsDir: processorsDir,
		logger:        logger,
		rerunState:    make(map[string]*processorRunState),
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
		rerunState:    make(map[string]*processorRunState),
	}
	copy(clone.processors, m.processors)
	clone.AddTextProcessors(procs, priority)
	return clone
}

// CloneWithDirProcessors returns a shallow copy of the Manager with processors
// loaded from additional directories merged in. Processors from later directories
// override earlier ones with the same name. The original Manager is not modified.
// Non-existent directories are silently ignored.
func (m *Manager) CloneWithDirProcessors(dirs []string, logger *slog.Logger) *Manager {
	if len(dirs) == 0 {
		return m
	}
	if logger == nil {
		logger = m.logger
	}

	clone := &Manager{
		processorsDir: m.processorsDir,
		logger:        logger,
		processors:    make([]*Processor, len(m.processors)),
		rerunState:    make(map[string]*processorRunState),
	}
	copy(clone.processors, m.processors)

	seen := make(map[string]bool)
	for _, p := range clone.processors {
		if p.Name != "" {
			seen[p.Name] = true
		}
	}

	for _, dir := range dirs {
		loader := NewLoader(dir, logger)
		procs, err := loader.Load()
		if err != nil {
			logger.Debug("Skipping workspace processors directory", "dir", dir, "error", err)
			continue
		}
		if len(procs) == 0 {
			continue
		}

		logger.Debug("Loaded workspace processors", "dir", dir, "count", len(procs))
		for _, p := range procs {
			if p.Name != "" && seen[p.Name] {
				// Workspace processor overrides global with same name
				for i, existing := range clone.processors {
					if existing.Name == p.Name {
						logger.Debug("Workspace processor overrides global",
							"name", p.Name,
							"dir", dir,
							"overridden_file", existing.FilePath,
						)
						clone.processors[i] = p
						break
					}
				}
			} else {
				clone.processors = append(clone.processors, p)
				if p.Name != "" {
					seen[p.Name] = true
				}
			}
		}
	}

	sort.SliceStable(clone.processors, func(i, j int) bool {
		return clone.processors[i].GetPriority() < clone.processors[j].GetPriority()
	})

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
// Handles rerun logic for "when: first" processors: if a processor has a rerun config,
// it tracks when the processor last ran and re-fires it when a threshold is reached.
// Returns the processor result containing the transformed message and any attachments.
func (m *Manager) Apply(ctx context.Context, input *ProcessorInput) (*ProcessorResult, error) {
	// Pre-pass: check rerun eligibility for when:first processors.
	// We temporarily override isFirstMessage for processors that are due for re-run.
	rerunOverrides := m.checkRerunEligibility(input)

	// Save and patch isFirstMessage if needed
	origIsFirst := input.IsFirstMessage
	defer func() { input.IsFirstMessage = origIsFirst }()

	if len(rerunOverrides) > 0 {
		// We apply the processors one at a time to handle per-processor overrides.
		return m.applyWithRerun(ctx, input, origIsFirst, rerunOverrides)
	}

	result, err := ApplyProcessors(ctx, m.processors, input, m.processorsDir, m.logger)

	// Post-pass: update rerun state for all processors
	m.updateRerunState(input.IsFirstMessage)

	return result, err
}

// checkRerunEligibility checks which "when: first" processors with rerun config
// are due for re-run. Returns a set of processor names that should be re-triggered.
func (m *Manager) checkRerunEligibility(input *ProcessorInput) map[string]bool {
	if input.IsFirstMessage {
		return nil // First message — all "when: first" processors will fire naturally
	}

	overrides := make(map[string]bool)
	now := time.Now()

	for _, proc := range m.processors {
		if proc.When != config.ProcessorWhenFirst || proc.Rerun == nil || proc.Name == "" {
			continue
		}

		state, exists := m.rerunState[proc.Name]
		if !exists {
			continue // Never ran yet — will be handled by isFirstMessage
		}

		rerun := proc.Rerun
		// Check time threshold
		if rerun.GetAfterDuration() > 0 && now.Sub(state.lastRunTime) >= rerun.GetAfterDuration() {
			m.logger.Debug("processor rerun triggered by time",
				"name", proc.Name,
				"elapsed", now.Sub(state.lastRunTime).String(),
				"threshold", rerun.AfterTime,
			)
			overrides[proc.Name] = true
			continue
		}

		// Check message count threshold
		if rerun.AfterSentMsgs > 0 && state.messagesSince >= rerun.AfterSentMsgs {
			m.logger.Debug("processor rerun triggered by message count",
				"name", proc.Name,
				"messages_since", state.messagesSince,
				"threshold", rerun.AfterSentMsgs,
			)
			overrides[proc.Name] = true
		}
	}

	return overrides
}

// applyWithRerun applies processors individually, overriding isFirstMessage for
// processors that are due for re-run.
func (m *Manager) applyWithRerun(ctx context.Context, input *ProcessorInput, origIsFirst bool, rerunOverrides map[string]bool) (*ProcessorResult, error) {
	result := &ProcessorResult{Message: input.Message}

	m.logger.Debug("processor pipeline starting (with rerun)",
		"total_processors", len(m.processors),
		"is_first_message", origIsFirst,
		"rerun_count", len(rerunOverrides),
	)

	executor := NewExecutor(m.processorsDir, m.logger)
	applied := 0
	skipped := 0

	for _, proc := range m.processors {
		// Determine effective isFirstMessage for this processor
		effectiveIsFirst := origIsFirst
		if rerunOverrides[proc.Name] {
			effectiveIsFirst = true
		}

		input.IsFirstMessage = effectiveIsFirst
		shouldApply, skipReason := proc.ShouldApply(effectiveIsFirst, input)
		if !shouldApply {
			skipped++
			m.logger.Debug("processor skipped",
				"name", proc.Name,
				"reason", string(skipReason),
				"when", proc.When,
				"priority", proc.GetPriority(),
			)
			continue
		}

		applied++
		m.logger.Debug("applying processor",
			"name", proc.Name,
			"when", proc.When,
			"mode", map[bool]string{true: "text", false: "command"}[proc.IsTextMode()],
			"position", proc.GetPosition(),
			"priority", proc.GetPriority(),
			"is_rerun", rerunOverrides[proc.Name],
		)

		// Text-mode: directly prepend or append the static text (no external command).
		if proc.IsTextMode() {
			text := SubstituteVariables(proc.Text, input)
			switch proc.GetPosition() {
			case config.ProcessorPositionPrepend:
				result.Message = text + result.Message
			case config.ProcessorPositionAppend:
				result.Message = result.Message + text
			}
			input.Message = result.Message
		} else {
			// Command-mode: execute external command
			procInput := &ProcessorInput{
				Message:             result.Message,
				IsFirstMessage:      input.IsFirstMessage,
				SessionID:           input.SessionID,
				WorkingDir:          input.WorkingDir,
				History:             input.History,
				ParentSessionID:     input.ParentSessionID,
				ParentSessionName:   input.ParentSessionName,
				SessionName:         input.SessionName,
				ACPServer:           input.ACPServer,
				WorkspaceUUID:       input.WorkspaceUUID,
				AvailableACPServers: input.AvailableACPServers,
				ChildSessions:       input.ChildSessions,
			}
			output, err := executor.Execute(ctx, proc, procInput)
			if err != nil {
				if proc.GetOnError() == ErrorFail {
					return nil, fmt.Errorf("processor %s failed: %w", proc.Name, err)
				}
				m.logger.Warn("processor failed, skipping",
					"name", proc.Name, "error", err)
				continue
			}
			result.Message = output.Message
			if len(output.Attachments) > 0 {
				result.Attachments = append(result.Attachments, output.Attachments...)
			}
			input.Message = output.Message
		}

		// Record run for rerun tracking
		if proc.Name != "" && proc.Rerun != nil {
			m.rerunState[proc.Name] = &processorRunState{
				lastRunTime:   time.Now(),
				messagesSince: 0,
			}
		}
	}

	// Increment message counters for all rerun-tracked processors that didn't fire
	m.updateRerunState(origIsFirst)

	m.logger.Debug("processor pipeline complete (with rerun)",
		"total", len(m.processors),
		"applied", applied,
		"skipped", skipped,
	)

	return result, nil
}

// updateRerunState updates the rerun state after each Apply call.
// For processors that ran (isFirstMessage was true and they applied), the state
// was already reset in the apply loop. For all other rerun-tracked processors,
// increment the message counter.
func (m *Manager) updateRerunState(wasFirstMessage bool) {
	for _, proc := range m.processors {
		if proc.When != config.ProcessorWhenFirst || proc.Rerun == nil || proc.Name == "" {
			continue
		}

		state, exists := m.rerunState[proc.Name]
		if !exists {
			if wasFirstMessage {
				// First time running — initialize state
				m.rerunState[proc.Name] = &processorRunState{
					lastRunTime:   time.Now(),
					messagesSince: 0,
				}
			}
			continue
		}

		// Increment message counter (for processors that didn't fire this round)
		state.messagesSince++
	}
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
