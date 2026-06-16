package processors

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/config"
)

const (
	userRequestOpenTag  = "<user_request>\n"
	userRequestCloseTag = "\n</user_request>"
)

// wrapUserRequest wraps the user's original message in an explicit delimiter so
// that processor-injected prepend/append text (e.g. session-context, reminders)
// cannot cause the agent to misclassify the real request as boilerplate setup
// context. Whitespace-only messages are returned unchanged.
func wrapUserRequest(message string) string {
	if strings.TrimSpace(message) == "" {
		return message
	}
	return userRequestOpenTag + message + userRequestCloseTag
}

// pendingPromptDispatch holds a prompt-mode processor ready for dispatch.
type pendingPromptDispatch struct {
	name    string
	prompt  string
	timeout time.Duration
}

// RerunReason describes why a processor was re-triggered.
type RerunReason string

const (
	RerunReasonTime   RerunReason = "time_elapsed"
	RerunReasonMsgs   RerunReason = "message_count"
	RerunReasonTokens RerunReason = "token_count"
)

// ProcessorResult contains the result of applying processors to a message.
type ProcessorResult struct {
	// Message is the transformed message text.
	Message string
	// Attachments contains any file attachments from processors.
	Attachments []Attachment
	// AppliedNames contains the names of processors that were applied.
	// Not serialized to JSON — only used in-memory for stats tracking.
	AppliedNames []string `json:"-"`
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

	logger.Info("processor pipeline starting",
		"total_processors", len(procs),
		"is_first_message", input.IsFirstMessage,
		"acp_server", input.ACPServer,
		"session_id", input.SessionID,
	)

	executor := NewExecutor(processorsDir, logger)
	result := &ProcessorResult{Message: input.Message}
	if input.IsFirstMessage {
		result.Message = wrapUserRequest(input.Message)
	}
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
				"on", proc.When.On,
				"match", proc.When.Match,
				"priority", proc.GetPriority(),
			)
			continue
		}

		applied++
		result.AppliedNames = append(result.AppliedNames, proc.Name)
		logger.Info("applying processor",
			"name", proc.Name,
			"on", proc.When.On,
			"match", proc.When.Match,
			"mode", map[bool]string{true: "text", false: "command"}[proc.IsTextMode()],
			"mutate", proc.GetMutate(),
			"priority", proc.GetPriority(),
		)

		// Text-mode: directly prepend or append the static text (no external command).
		if proc.IsTextMode() {
			switch proc.GetMutate() {
			case config.ProcessorMutatePrepend:
				result.Message = proc.Text + result.Message
			case config.ProcessorMutateAppend:
				result.Message += proc.Text
			}
			logger.Info("text-mode processor applied",
				"name", proc.Name,
				"mutate", proc.GetMutate(),
			)
			continue
		}

		// Prompt-mode: fire-and-forget dispatch via PromptFunc.
		// ApplyProcessors has no access to a PromptFunc — callers should use Manager.Apply
		// which routes prompt-mode processors through applyWithRerun where a PromptFunc
		// is available.
		if proc.IsPromptMode() {
			logger.Warn("prompt-mode processor skipped: use Manager.Apply for prompt-mode processors",
				"name", proc.Name,
			)
			continue
		}

		// Command-mode: create per-iteration input with current message state.
		procInput := &ProcessorInput{
			Message:             result.Message,
			IsFirstMessage:      input.IsFirstMessage,
			SessionID:           input.SessionID,
			WorkingDir:          input.WorkingDir,
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

		logger.Info("processor applied",
			"name", proc.Name,
			"output_type", proc.GetOutput(),
		)
	}

	logger.Info("processor pipeline complete",
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

	// promptFunc is an optional callback for executing prompt-mode processors.
	// Set by the web layer via SetPromptFunc to bridge to auxiliary ACP sessions.
	promptFunc PromptFunc

	// rerunState tracks per-processor run state for rerun logic.
	// Keyed by processor name. Only populated for processors with rerun config.
	// In-memory only — not persisted across restarts (isFirstPrompt=true on resume
	// handles restart case).
	rerunState map[string]*processorRunState

	// Stats tracking — updated after each Apply call.
	statsMu          sync.Mutex
	totalActivations int       // Total number of pipeline invocations (Apply calls) across session lifetime
	lastActivationAt time.Time // When the pipeline was last invoked (zero if never)
	lastAppliedNames []string  // Names of processors applied on the most recent activation

	// stateStore persists agentResponseCount and per-processor cadence state across
	// session restarts. Defaults to FileStateStore (writes processor_state.json in
	// the session directory). Injected as MemoryStateStore in unit tests.
	stateStore StateStore

	// clock returns the current time. Defaults to time.Now; overridden in tests
	// to make time-based cadence deterministic.
	clock func() time.Time
}

// processorRunState tracks when a processor last ran, for rerun scheduling.
type processorRunState struct {
	lastRunTime   time.Time
	messagesSince int
	tokensSince   int
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
		stateStore:    &FileStateStore{},
		clock:         time.Now,
	}
}

// SetStateStore replaces the state store used for persistence.
// Primarily used in unit tests to inject a MemoryStateStore.
func (m *Manager) SetStateStore(s StateStore) {
	m.stateStore = s
}

// SetClock replaces the clock function used for cadence time checks.
// Primarily used in unit tests to make time-based cadence deterministic.
func (m *Manager) SetClock(fn func() time.Time) {
	m.clock = fn
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
			When:     WhenConfig{On: PhaseUserPrompt, Match: Match(p.When.Match)},
			Mutate:   p.Mutate,
			Text:     p.Text,
			Priority: priority,
			Source:   ProcessorSourceConfig,
		}
		m.processors = append(m.processors, proc)
	}
	// Re-sort by priority (stable to preserve relative order within same priority).
	sort.SliceStable(m.processors, func(i, j int) bool {
		return m.processors[i].GetPriority() < m.processors[j].GetPriority()
	})
}

// SetPromptFunc sets the callback used to dispatch prompt-mode processors.
// The callback is injected by the web layer to bridge processor execution to
// workspace-scoped auxiliary ACP sessions (fire-and-forget).
func (m *Manager) SetPromptFunc(fn PromptFunc) {
	m.promptFunc = fn
}

// SetStats seeds the activation counters from persisted values.
// This is used when resuming a session to restore the cumulative count.
func (m *Manager) SetStats(activations int, lastAt time.Time) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.totalActivations = activations
	m.lastActivationAt = lastAt
}

// CloneWithTextProcessors returns a shallow copy of the Manager with the given
// text-mode processors merged in. The original Manager is not modified, making
// this safe to call concurrently on a shared instance.
func (m *Manager) CloneWithTextProcessors(procs []config.MessageProcessor, priority int) *Manager {
	m.statsMu.Lock()
	activations := m.totalActivations
	lastAt := m.lastActivationAt
	m.statsMu.Unlock()

	clone := &Manager{
		processorsDir:    m.processorsDir,
		logger:           m.logger,
		processors:       make([]*Processor, len(m.processors)),
		rerunState:       make(map[string]*processorRunState),
		promptFunc:       m.promptFunc,
		totalActivations: activations,
		lastActivationAt: lastAt,
		stateStore:       m.stateStore,
		clock:            m.clock,
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

	m.statsMu.Lock()
	activations := m.totalActivations
	lastAt := m.lastActivationAt
	m.statsMu.Unlock()

	clone := &Manager{
		processorsDir:    m.processorsDir,
		logger:           logger,
		processors:       make([]*Processor, len(m.processors)),
		rerunState:       make(map[string]*processorRunState),
		promptFunc:       m.promptFunc,
		totalActivations: activations,
		lastActivationAt: lastAt,
		stateStore:       m.stateStore,
		clock:            m.clock,
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
			// Stamp workspace source for all dir-loaded processors
			if p.Source == "" {
				p.Source = ProcessorSourceWorkspace
			}
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

// CloneWithEnabledOverrides returns a shallow copy of the Manager with per-processor
// enabled state overridden by the workspace .mittorc processors section.
// Each override has a Name and an Enabled pointer; if Enabled is non-nil, the
// processor's Enabled field is set to that value. The original Manager is not modified.
func (m *Manager) CloneWithEnabledOverrides(overrides []config.ProcessorOverride) *Manager {
	if len(overrides) == 0 {
		return m
	}

	// Build override map: name → enabled value
	overrideMap := make(map[string]bool, len(overrides))
	for _, o := range overrides {
		if o.Enabled != nil {
			overrideMap[o.Name] = *o.Enabled
		}
	}

	m.statsMu.Lock()
	activations := m.totalActivations
	lastAt := m.lastActivationAt
	m.statsMu.Unlock()

	clone := &Manager{
		processorsDir:    m.processorsDir,
		logger:           m.logger,
		processors:       make([]*Processor, len(m.processors)),
		rerunState:       make(map[string]*processorRunState),
		promptFunc:       m.promptFunc,
		totalActivations: activations,
		lastActivationAt: lastAt,
		stateStore:       m.stateStore,
		clock:            m.clock,
	}

	// Deep-copy processor pointers so we can modify Enabled without affecting the original.
	for i, p := range m.processors {
		if enabled, ok := overrideMap[p.Name]; ok {
			// Make a shallow copy of the processor struct so we can change Enabled.
			cp := *p
			cp.Enabled = &enabled
			clone.processors[i] = &cp
		} else {
			clone.processors[i] = p
		}
	}

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
	// Stamp source: global processors come from MITTO_DIR/processors/
	for _, p := range m.processors {
		if p.Source == "" {
			p.Source = ProcessorSourceGlobal
		}
	}
	return nil
}

// Processors returns the loaded processors.
func (m *Manager) Processors() []*Processor {
	return m.processors
}

// Apply applies all applicable processors to a message.
// Handles rerun logic for "when.sent: first" processors: if a processor has a when.rerun config,
// it tracks when the processor last ran and re-fires it when a threshold is reached.
// Returns the processor result containing the transformed message and any attachments.
func (m *Manager) Apply(ctx context.Context, input *ProcessorInput) (*ProcessorResult, error) {
	// Pre-pass: check rerun eligibility for when.sent:first processors.
	// We temporarily override isFirstMessage for processors that are due for re-run.
	rerunOverrides := m.checkRerunEligibility(input)

	// Save and patch isFirstMessage if needed
	origIsFirst := input.IsFirstMessage
	defer func() { input.IsFirstMessage = origIsFirst }()

	// Route to applyWithRerun if there are rerun overrides or prompt-mode processors.
	// Prompt-mode processors require Manager state (promptFunc) not available in ApplyProcessors.
	if len(rerunOverrides) > 0 || m.hasPromptModeProcessors() {
		// We apply the processors one at a time to handle per-processor overrides.
		return m.applyWithRerun(ctx, input, origIsFirst, rerunOverrides)
	}

	result, err := ApplyProcessors(ctx, m.processors, input, m.processorsDir, m.logger)

	// Track pipeline activation
	m.statsMu.Lock()
	m.totalActivations++
	m.lastActivationAt = time.Now()
	if result != nil {
		m.lastAppliedNames = result.AppliedNames
	}
	m.statsMu.Unlock()

	// Post-pass: update rerun state for all processors
	m.updateRerunState(input.IsFirstMessage)

	return result, err
}

// hasPromptModeProcessors returns true if any loaded processor is a prompt-mode processor.
// Used to determine whether Manager.Apply must route through applyWithRerun.
func (m *Manager) hasPromptModeProcessors() bool {
	for _, p := range m.processors {
		if p.IsPromptMode() {
			return true
		}
	}
	return false
}

// checkRerunEligibility checks which "when.on: userPrompt, match: first" processors with when.rerun config
// are due for re-run. Returns a map of processor names to the reason they should be re-triggered.
func (m *Manager) checkRerunEligibility(input *ProcessorInput) map[string]RerunReason {
	if input.IsFirstMessage {
		return nil // First message — all "match: first" processors will fire naturally
	}

	overrides := make(map[string]RerunReason)
	now := time.Now()

	for _, proc := range m.processors {
		if proc.When.On != PhaseUserPrompt || proc.When.Match != MatchFirst || proc.When.Rerun == nil || proc.Name == "" {
			continue
		}

		state, exists := m.rerunState[proc.Name]
		if !exists {
			continue // Never ran yet — will be handled by isFirstMessage
		}

		rerun := proc.When.Rerun
		// Check time threshold
		if rerun.GetAfterDuration() > 0 && now.Sub(state.lastRunTime) >= rerun.GetAfterDuration() {
			m.logger.Info("processor rerun triggered by time",
				"name", proc.Name,
				"elapsed", now.Sub(state.lastRunTime).String(),
				"threshold", rerun.AfterTime,
			)
			overrides[proc.Name] = RerunReasonTime
			continue
		}

		// Check message count threshold
		if rerun.AfterSentMsgs > 0 && state.messagesSince >= rerun.AfterSentMsgs {
			m.logger.Info("processor rerun triggered by message count",
				"name", proc.Name,
				"messages_since", state.messagesSince,
				"threshold", rerun.AfterSentMsgs,
			)
			overrides[proc.Name] = RerunReasonMsgs
			continue
		}

		// Check token count threshold
		if rerun.AfterTokens > 0 && state.tokensSince >= rerun.AfterTokens {
			m.logger.Info("processor rerun triggered by token count",
				"name", proc.Name,
				"tokens_since", state.tokensSince,
				"threshold", rerun.AfterTokens,
			)
			overrides[proc.Name] = RerunReasonTokens
		}
	}

	return overrides
}

// applyWithRerun applies processors individually, overriding isFirstMessage for
// processors that are due for re-run.
func (m *Manager) applyWithRerun(ctx context.Context, input *ProcessorInput, origIsFirst bool, rerunOverrides map[string]RerunReason) (*ProcessorResult, error) {
	result := &ProcessorResult{Message: input.Message}
	if origIsFirst || len(rerunOverrides) > 0 {
		result.Message = wrapUserRequest(input.Message)
	}

	m.logger.Info("processor pipeline starting (with rerun)",
		"total_processors", len(m.processors),
		"is_first_message", origIsFirst,
		"rerun_count", len(rerunOverrides),
	)

	executor := NewExecutor(m.processorsDir, m.logger)
	applied := 0
	skipped := 0
	var appliedNames []string

	// Collect prompt-mode processors for batched dispatch after the loop.
	var pendingPrompts []pendingPromptDispatch

	for _, proc := range m.processors {
		// Determine effective isFirstMessage for this processor
		effectiveIsFirst := origIsFirst
		if _, isRerun := rerunOverrides[proc.Name]; isRerun {
			effectiveIsFirst = true
		}

		input.IsFirstMessage = effectiveIsFirst
		shouldApply, skipReason := proc.ShouldApply(effectiveIsFirst, input)
		if !shouldApply {
			skipped++
			m.logger.Debug("processor skipped",
				"name", proc.Name,
				"reason", string(skipReason),
				"on", proc.When.On,
				"match", proc.When.Match,
				"priority", proc.GetPriority(),
			)
			continue
		}

		applied++
		appliedNames = append(appliedNames, proc.Name)
		rerunReason, isRerun := rerunOverrides[proc.Name]
		m.logger.Info("applying processor",
			"name", proc.Name,
			"on", proc.When.On,
			"match", proc.When.Match,
			"mode", map[bool]string{true: "text", false: "command"}[proc.IsTextMode()],
			"mutate", proc.GetMutate(),
			"priority", proc.GetPriority(),
			"is_rerun", isRerun,
			"rerun_reason", string(rerunReason),
		)

		// Text-mode: directly prepend or append the static text (no external command).
		if proc.IsTextMode() {
			text := SubstituteVariables(proc.Text, input)
			switch proc.GetMutate() {
			case config.ProcessorMutatePrepend:
				result.Message = text + result.Message
			case config.ProcessorMutateAppend:
				result.Message += text
			}
			input.Message = result.Message
		} else if proc.IsPromptMode() {
			// Prompt-mode: collect for batched dispatch after loop.
			if m.promptFunc == nil {
				m.logger.Warn("prompt-mode processor skipped: no PromptFunc configured",
					"name", proc.Name,
				)
				continue
			}

			// Build the prompt with variable substitution.
			assembledPrompt := SubstituteVariables(proc.Prompt, input)
			procTimeout := proc.GetTimeout().Duration()

			// Collect for batched dispatch.
			pendingPrompts = append(pendingPrompts, pendingPromptDispatch{
				name:    proc.Name,
				prompt:  assembledPrompt,
				timeout: procTimeout,
			})

			// Update rerun tracking for prompt-mode processors.
			if m.rerunState == nil {
				m.rerunState = make(map[string]*processorRunState)
			}
			if _, ok := m.rerunState[proc.Name]; !ok {
				m.rerunState[proc.Name] = &processorRunState{}
			}
			m.rerunState[proc.Name].lastRunTime = time.Now()
			m.rerunState[proc.Name].messagesSince = 0
			m.rerunState[proc.Name].tokensSince = 0

			m.logger.Info("prompt-mode processor collected for dispatch",
				"name", proc.Name,
				"prompt_len", len(assembledPrompt),
			)
		} else {
			// Command-mode: execute external command
			procInput := &ProcessorInput{
				Message:             result.Message,
				IsFirstMessage:      input.IsFirstMessage,
				SessionID:           input.SessionID,
				WorkingDir:          input.WorkingDir,
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
			if len(output.Attachments) > 0 {
				result.Attachments = append(result.Attachments, output.Attachments...)
			}
			input.Message = result.Message
		}

		// Record run for rerun tracking
		if proc.Name != "" && proc.When.Rerun != nil {
			m.rerunState[proc.Name] = &processorRunState{
				lastRunTime:   time.Now(),
				messagesSince: 0,
				tokensSince:   0,
			}
		}
	}

	// Dispatch collected prompt-mode processors.
	if len(pendingPrompts) > 0 {
		m.dispatchPromptBatch(input.WorkspaceUUID, pendingPrompts)
	}

	// Increment message counters for all rerun-tracked processors that didn't fire
	m.updateRerunState(origIsFirst)

	// Track pipeline activation
	m.statsMu.Lock()
	m.totalActivations++
	m.lastActivationAt = time.Now()
	m.lastAppliedNames = appliedNames
	m.statsMu.Unlock()

	m.logger.Info("processor pipeline complete (with rerun)",
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
		if proc.When.On != PhaseUserPrompt || proc.When.Match != MatchFirst || proc.When.Rerun == nil || proc.Name == "" {
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

// AccumulateTokenUsage adds the given token count to all rerun-tracked processors.
// Called after each prompt completes with the turn's total token count
// (actual from ACP Usage or estimated from character count).
func (m *Manager) AccumulateTokenUsage(totalTokens int) {
	if totalTokens <= 0 {
		return
	}
	for _, proc := range m.processors {
		if proc.When.On != PhaseUserPrompt || proc.When.Match != MatchFirst || proc.When.Rerun == nil || proc.Name == "" {
			continue
		}
		state, exists := m.rerunState[proc.Name]
		if !exists {
			continue
		}
		state.tokensSince += totalTokens
	}
}

// ApplyAfter runs all after-phase processors (agentResponded and agentIdle) against
// the completed agent turn. It applies stop-reason, origin, match, and cadence filters
// in declaration order, executes each processor (command or prompt mode), and accumulates
// side-effects. agentIdle processors are additionally gated on input.SessionIdle, so they
// fire only once the queue has drained — their cadence counters still accumulate across a
// burst of queued turns.
//
// Persistence: the session's AgentResponseCount and per-processor cadence state are
// loaded from input.SessionDir at the start and saved atomically after all processors
// have run. If input.SessionDir is empty (tests, store-less sessions), state is held
// only in the injected StateStore (MemoryStateStore in tests).
//
// Returns an ApplyAfterResult — never returns an error; individual processor failures
// are collected non-fatally in ApplyAfterResult.Errors.
func (m *Manager) ApplyAfter(ctx context.Context, input AfterProcessorInput) ApplyAfterResult {
	// --- Load persisted state ---
	store := m.stateStore
	if store == nil {
		store = &FileStateStore{}
	}
	state, err := store.Load(input.SessionDir)
	if err != nil {
		m.logger.Warn("after-phase: failed to load processor state, using zero-value",
			"error", err, "session_dir", input.SessionDir)
		state = &ProcessorStateData{Processors: make(map[string]*ProcessorCadenceState)}
	}

	isFirstAgentResponse := state.AgentResponseCount == 0
	now := m.clock()

	// Determine cumulative token count for this turn.
	var turnTokens int64
	if input.TokenUsage != nil {
		turnTokens = input.TokenUsage.Total
	}

	var result ApplyAfterResult
	applied := 0
	skipped := 0

	// Collect prompt-mode processors for batched dispatch after the loop.
	var pendingPrompts []pendingPromptDispatch

	m.logger.Info("after-phase processor pipeline starting",
		"total_processors", len(m.processors),
		"is_first_agent_response", isFirstAgentResponse,
		"agent_response_count", state.AgentResponseCount,
		"stop_reason", input.StopReason,
		"origin", input.Origin,
	)

	for _, proc := range m.processors {
		// Phase filter: only after-phase processors fire here (agentResponded and
		// agentIdle). agentIdle processors are additionally gated on SessionIdle below.
		if proc.When.On != PhaseAgentResponded && proc.When.On != PhaseAgentIdle {
			continue
		}

		// Enabled check
		if !proc.IsEnabled() {
			skipped++
			m.logger.Debug("after-phase processor skipped",
				"name", proc.Name, "reason", "disabled")
			continue
		}

		// StopReason filter
		if len(proc.When.StopReasons) > 0 {
			matched := false
			for _, sr := range proc.When.StopReasons {
				if sr == input.StopReason {
					matched = true
					break
				}
			}
			if !matched {
				skipped++
				m.logger.Debug("after-phase processor skipped",
					"name", proc.Name, "reason", "stopReason_mismatch",
					"stop_reason", input.StopReason, "allowed", proc.When.StopReasons)
				continue
			}
		}

		// Origin filter (excludeOrigins)
		if len(proc.When.ExcludeOrigins) > 0 {
			excluded := false
			for _, o := range proc.When.ExcludeOrigins {
				if o == input.Origin {
					excluded = true
					break
				}
			}
			if excluded {
				skipped++
				m.logger.Debug("after-phase processor skipped",
					"name", proc.Name, "reason", "origin_excluded",
					"origin", input.Origin)
				continue
			}
		}

		// Match filter (uses persisted AgentResponseCount for correctness across restarts)
		switch proc.When.Match {
		case MatchFirst:
			if !isFirstAgentResponse {
				skipped++
				m.logger.Debug("after-phase processor skipped",
					"name", proc.Name, "reason", "match=first_not_first_response")
				continue
			}
		case MatchAll:
			// always passes match filter (cadence may still gate it)
		case MatchAllExceptFirst:
			if isFirstAgentResponse {
				skipped++
				m.logger.Debug("after-phase processor skipped",
					"name", proc.Name, "reason", "match=allExceptFirst_is_first_response")
				continue
			}
		default:
			skipped++
			m.logger.Warn("after-phase processor skipped: unknown match value",
				"name", proc.Name, "match", proc.When.Match)
			continue
		}

		// Cadence filter — gates processors with when.cadence configured.
		// All specified thresholds must be met simultaneously (AND logic).
		// Pre-increment semantics: TurnsSinceLastFire is incremented BEFORE the gate
		// check so that everyNTurns:N means "fire every N agent responses that pass
		// all other filters (stop reason, origin, match)".
		if proc.When.Cadence != nil && proc.Name != "" {
			cadenceState := state.Processors[proc.Name]
			if cadenceState == nil {
				cadenceState = &ProcessorCadenceState{}
				state.Processors[proc.Name] = cadenceState
			}
			c := proc.When.Cadence

			// Pre-increment counters for this turn.
			cadenceState.TurnsSinceLastFire++
			cadenceState.TokensSinceLastFire += turnTokens

			// Check all thresholds (AND logic).
			gatePassed := true

			if c.EveryNTurns > 0 && cadenceState.TurnsSinceLastFire < c.EveryNTurns {
				gatePassed = false
				m.logger.Debug("after-phase processor cadence: turns threshold not met",
					"name", proc.Name,
					"turns_since_last_fire", cadenceState.TurnsSinceLastFire,
					"required", c.EveryNTurns)
			}
			if gatePassed && c.EveryNTokens > 0 && cadenceState.TokensSinceLastFire < c.EveryNTokens {
				gatePassed = false
				m.logger.Debug("after-phase processor cadence: tokens threshold not met",
					"name", proc.Name,
					"tokens_since_last_fire", cadenceState.TokensSinceLastFire,
					"required", c.EveryNTokens)
			}
			if gatePassed && c.AfterInterval != "" {
				interval := c.GetAfterIntervalDuration()
				if interval > 0 && !cadenceState.LastFiredAt.IsZero() {
					elapsed := now.Sub(cadenceState.LastFiredAt)
					if elapsed < interval {
						gatePassed = false
						m.logger.Debug("after-phase processor cadence: interval threshold not met",
							"name", proc.Name,
							"elapsed", elapsed,
							"required", interval)
					}
				}
			}

			if !gatePassed {
				skipped++
				continue
			}
		}

		// agentIdle gate: only fire once the agent has drained its queue and gone idle.
		// This is checked AFTER the cadence pre-increment above so that a burst of queued
		// turns still accumulates toward the cadence threshold; the processor then fires
		// once, at the idle breakpoint, with the full exchange counted. Cadence counters
		// are intentionally NOT reset here — they persist until the processor actually fires.
		if proc.When.On == PhaseAgentIdle && !input.SessionIdle {
			skipped++
			m.logger.Debug("after-phase processor skipped",
				"name", proc.Name, "reason", "agentIdle_session_busy")
			continue
		}

		applied++
		m.logger.Info("applying after-phase processor",
			"name", proc.Name,
			"match", proc.When.Match,
			"output", proc.GetOutput(),
			"mode", map[bool]string{true: "prompt", false: "command"}[proc.IsPromptMode()],
		)

		if proc.IsPromptMode() {
			// Prompt-mode: collect for batched fire-and-forget dispatch.
			// The output: field is ignored for prompt-mode — these are dispatched to
			// an auxiliary session and are not parsed as stdout.
			if m.promptFunc == nil {
				m.logger.Warn("after-phase prompt-mode processor skipped: no PromptFunc configured",
					"name", proc.Name,
				)
				skipped++
				applied-- // undo the applied++ above
				continue
			}

			assembledPrompt := substituteAfterVariables(proc.Prompt, input)
			procTimeout := proc.GetTimeout().Duration()
			pendingPrompts = append(pendingPrompts, pendingPromptDispatch{
				name:    proc.Name,
				prompt:  assembledPrompt,
				timeout: procTimeout,
			})

			// Reset cadence counters after successful collection.
			if proc.When.Cadence != nil && proc.Name != "" {
				cs := state.Processors[proc.Name]
				if cs == nil {
					cs = &ProcessorCadenceState{}
					state.Processors[proc.Name] = cs
				}
				cs.TurnsSinceLastFire = 0
				cs.TokensSinceLastFire = 0
				cs.LastFiredAt = now
			}

			m.logger.Info("after-phase prompt-mode processor collected for dispatch",
				"name", proc.Name,
				"prompt_len", len(assembledPrompt),
			)
			continue
		}

		// Command mode (text mode is forbidden for agentResponded by the loader).
		stdout, execErr := executeAfterCommand(ctx, proc, m.processorsDir, input, m.logger)

		if execErr != nil {
			m.logger.Warn("after-phase processor execution failed",
				"name", proc.Name, "error", execErr)
			result.Errors = append(result.Errors, ProcessorError{
				ProcessorName: proc.Name,
				Error:         execErr.Error(),
			})
			continue
		}

		// After a successful execution, reset cadence counters for this processor.
		if proc.When.Cadence != nil && proc.Name != "" {
			cs := state.Processors[proc.Name]
			if cs == nil {
				cs = &ProcessorCadenceState{}
				state.Processors[proc.Name] = cs
			}
			cs.TurnsSinceLastFire = 0
			cs.TokensSinceLastFire = 0
			cs.LastFiredAt = now
		}

		// Parse output according to output type.
		outputType := proc.GetOutput()
		if outputType == OutputTransform || outputType == OutputPrepend || outputType == OutputAppend {
			// These are forbidden for agentResponded by the loader; treat as discard.
			outputType = OutputDiscard
		}

		switch outputType {
		case OutputDiscard:
			// Side-effects only; stdout is intentionally discarded.

		case OutputNotify:
			notifs, parseErr := parseNotifyOutput(stdout)
			if parseErr != nil {
				m.logger.Warn("after-phase processor notify parse failed",
					"name", proc.Name, "error", parseErr)
				result.Errors = append(result.Errors, ProcessorError{
					ProcessorName: proc.Name,
					Error:         parseErr.Error(),
				})
				continue
			}
			result.Notifications = append(result.Notifications, notifs...)

		case OutputActionButtons:
			buttons, parseErr := parseActionButtonsOutput(stdout)
			if parseErr != nil {
				m.logger.Warn("after-phase processor actionButtons parse failed",
					"name", proc.Name, "error", parseErr)
				result.Errors = append(result.Errors, ProcessorError{
					ProcessorName: proc.Name,
					Error:         parseErr.Error(),
				})
				continue
			}
			result.ActionButtons = append(result.ActionButtons, buttons...)

		case OutputUserData:
			patch, parseErr := parseUserDataOutput(stdout)
			if parseErr != nil {
				m.logger.Warn("after-phase processor userData parse failed",
					"name", proc.Name, "error", parseErr)
				result.Errors = append(result.Errors, ProcessorError{
					ProcessorName: proc.Name,
					Error:         parseErr.Error(),
				})
				continue
			}
			if len(patch) > 0 {
				if result.UserDataPatch == nil {
					result.UserDataPatch = make(map[string]string)
				}
				for k, v := range patch {
					result.UserDataPatch[k] = v
				}
			}
		}

		m.logger.Info("after-phase processor applied",
			"name", proc.Name, "output_type", outputType)
	}

	// Dispatch collected prompt-mode processors (fire-and-forget).
	if len(pendingPrompts) > 0 {
		m.dispatchPromptBatch(input.WorkspaceUUID, pendingPrompts)
	}

	// --- Update and save persisted state ---
	// Increment global response count so next call knows it's not the first response.
	state.AgentResponseCount++

	if saveErr := store.Save(input.SessionDir, state); saveErr != nil {
		m.logger.Warn("after-phase: failed to save processor state",
			"error", saveErr, "session_dir", input.SessionDir)
	}

	m.logger.Info("after-phase processor pipeline complete",
		"total", len(m.processors),
		"applied", applied,
		"skipped", skipped,
		"agent_response_count", state.AgentResponseCount,
		"notifications", len(result.Notifications),
		"action_buttons", len(result.ActionButtons),
		"user_data_keys", len(result.UserDataPatch),
		"errors", len(result.Errors),
	)

	return result
}

// EstimateTokens estimates the number of tokens in a text string.
// Uses a rough heuristic of ~4 characters per token, which is a reasonable
// average for English text and code. Used as fallback when the ACP server
// doesn't report actual token usage.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + 3) / 4 // Round up
}

// dispatchPromptBatch dispatches prompt-mode processors as fire-and-forget.
// If there is a single processor, it dispatches directly with the processor name.
// If there are multiple processors, it combines their prompts into a single
// request and dispatches to a shared "batch" auxiliary session.
func (m *Manager) dispatchPromptBatch(workspaceUUID string, prompts []pendingPromptDispatch) {
	if len(prompts) == 0 {
		return
	}

	if len(prompts) == 1 {
		// Single processor — dispatch directly.
		p := prompts[0]
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), p.timeout)
			defer cancel()
			if err := m.promptFunc(bgCtx, workspaceUUID, p.name, p.prompt); err != nil {
				if m.logger != nil {
					m.logger.Error("prompt-mode processor dispatch failed",
						"name", p.name,
						"error", err,
					)
				}
			}
		}()
		m.logger.Info("prompt-mode processor dispatched (single)",
			"name", prompts[0].name,
			"prompt_len", len(prompts[0].prompt),
		)
		return
	}

	// Multiple processors — combine into a single prompt.
	var sb strings.Builder
	sb.WriteString("We would like to fulfill the following requirements:\n\n")
	maxTimeout := time.Duration(0)
	var names []string
	for i, p := range prompts {
		fmt.Fprintf(&sb, "## Requirement %d: %s\n\n", i+1, p.name)
		sb.WriteString(p.prompt)
		sb.WriteString("\n\n")
		if p.timeout > maxTimeout {
			maxTimeout = p.timeout
		}
		names = append(names, p.name)
	}

	combinedName := strings.Join(names, "+")
	combinedPrompt := sb.String()

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), maxTimeout)
		defer cancel()
		if err := m.promptFunc(bgCtx, workspaceUUID, combinedName, combinedPrompt); err != nil {
			if m.logger != nil {
				m.logger.Error("batched prompt-mode processor dispatch failed",
					"names", combinedName,
					"error", err,
				)
			}
		}
	}()

	m.logger.Info("prompt-mode processors dispatched (batched)",
		"names", combinedName,
		"count", len(prompts),
		"combined_prompt_len", len(combinedPrompt),
	)
}

// ProcessorsDir returns the processors directory path.
func (m *Manager) ProcessorsDir() string {
	return m.processorsDir
}

// ProcessorCount returns the number of loaded processors.
func (m *Manager) ProcessorCount() int {
	return len(m.processors)
}

// TotalActivations returns the total number of pipeline invocations since the manager was created.
func (m *Manager) TotalActivations() int {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return m.totalActivations
}

// LastActivationAt returns when the processor pipeline was last invoked.
// Returns a zero time.Time if the pipeline has never been invoked.
func (m *Manager) LastActivationAt() time.Time {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return m.lastActivationAt
}

// LastAppliedNames returns the names of processors that were applied during the
// most recent pipeline activation. Returns nil if the pipeline has never been invoked.
func (m *Manager) LastAppliedNames() []string {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	if m.lastAppliedNames == nil {
		return nil
	}
	result := make([]string, len(m.lastAppliedNames))
	copy(result, m.lastAppliedNames)
	return result
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
