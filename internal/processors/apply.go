package processors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/config"
)

const (
	userRequestOpenTag  = "<user_request>\n"
	userRequestCloseTag = "\n</user_request>"
)

// archiveReasonParentDeleted mirrors the bare string literal used by
// internal/web/handlers/session_delete.go (HandleDeleteSession) and
// acp_server_delete.go when cascading a delete/close down to a session's
// descendants. It is not one of the session.ArchiveReason constants — see
// mitto-ce3b — and is used here purely to gate duplicate prompt-mode
// close-phase dispatch during a cascade delete.
const archiveReasonParentDeleted = "parent_deleted"

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

const (
	systemNotesOpenTag  = "\n<mitto_system_notes>\n"
	systemNotesCloseTag = "\n</mitto_system_notes>"
)

// wrapSystemNotes wraps the appended processor instruction region (standing
// reminders) in an explicitly labeled block so the agent treats it as system
// guidance rather than additional user tasks. Whitespace-only input is returned
// unchanged.
func wrapSystemNotes(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return systemNotesOpenTag + text + systemNotesCloseTag
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

// ProcessorRun captures a single processor invocation for the conversation
// Stats tab (mitto-fm89). Manager records one of these per processor per
// pipeline pass and forwards it (via RunRecorder) to a session.EventTypeProcessorRun
// event, so exact run/error/skip counts and p50/p95 durations can be computed by
// scanning events.jsonl — a summed stats-DB counter cannot represent a percentile.
type ProcessorRun struct {
	// Name is the processor's Name.
	Name string
	// Phase identifies which pipeline ran the processor: "before" (userPrompt),
	// "after" (agentResponded/agentIdle), or "close" (conversationClosed).
	Phase string
	// Outcome is "ok", "error", or "skipped".
	Outcome string
	// Duration is the wall-clock execution time. Zero for skipped runs and for
	// text-mode/prompt-mode processors (no external command is executed).
	Duration time.Duration
	// Error is the short failure message when Outcome == "error".
	Error string
}

// RunRecorder receives one ProcessorRun per processor invocation. Called
// synchronously from the Apply*/ApplyOnClose pipelines, so implementations
// must not block — the wiring in internal/conversation appends a
// session.Event and returns immediately. nil is a valid no-op value.
type RunRecorder func(ProcessorRun)

// recordRun forwards run to m's RunRecorder if one is configured. Safe to
// call with a nil Manager or an unnamed processor (no-ops in both cases).
func (m *Manager) recordRun(run ProcessorRun) {
	if m == nil || m.runRecorder == nil || run.Name == "" {
		return
	}
	m.runRecorder(run)
}

// ApplyProcessors applies all applicable processors to a message.
// Processors are applied in priority order (lower priority first).
// recorder, if non-nil, receives one ProcessorRun per evaluated processor
// (phase "before"). Returns the transformed message, attachments, and any error.
func ApplyProcessors(ctx context.Context, procs []*Processor, input *ProcessorInput, processorsDir string, logger *slog.Logger, recorder RunRecorder) (*ProcessorResult, error) {
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

	// record forwards a ProcessorRun to recorder (mitto-fm89 Stats tab). No-op
	// when recorder is nil (default, unless Manager.Apply wired one).
	record := func(name, outcome string, dur time.Duration, errMsg string) {
		if recorder == nil {
			return
		}
		recorder(ProcessorRun{Name: name, Phase: "before", Outcome: outcome, Duration: dur, Error: errMsg})
	}

	// appendBuf accumulates all append contributions so they can be wrapped once
	// in <mitto_system_notes> on first-message assemblies.
	var appendBuf strings.Builder

	for _, proc := range procs {
		// Check if processor should apply
		shouldApply, skipReason := proc.ShouldApply(input.IsFirstMessage, input)
		if !shouldApply {
			skipped++
			record(proc.Name, "skipped", 0, "")
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
			// Render Go-template {{ }} accessors against the session context. Unlike
			// applyWithRerun, this path leaves @mitto: variables untouched (they are
			// substituted downstream on the whole assembled message); templates have no
			// downstream pass, so they must be rendered per-body here. Guarded by
			// HasTemplateSyntax so non-template bodies skip the context build.
			text := proc.Text
			if config.HasTemplateSyntax(text) {
				tctx := BuildCELContext(input)
				funcs := config.BuildTemplateFuncMap(tctx)
				if rendered, rerr := config.RenderPromptTemplate(proc.Name, text, tctx, funcs); rerr != nil {
					logger.Warn("text-mode processor template render failed; using unrendered text", "name", proc.Name, "error", rerr)
				} else {
					text = rendered
				}
			}
			switch proc.GetMutate() {
			case config.ProcessorMutatePrepend:
				result.Message = text + result.Message
			case config.ProcessorMutateAppend:
				appendBuf.WriteString(text)
			}
			record(proc.Name, "ok", 0, "")
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
			record(proc.Name, "skipped", 0, "")
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
			TasksUpstream:       input.TasksUpstream,
		}

		// Execute processor
		execStart := time.Now()
		output, err := executor.Execute(ctx, proc, procInput)
		execDur := time.Since(execStart)
		if err != nil {
			logger.Warn("processor execution failed",
				"name", proc.Name,
				"error", err,
			)
			record(proc.Name, "error", execDur, err.Error())

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
			record(proc.Name, "error", execDur, output.Error)

			if proc.GetOnError() == ErrorFail {
				return nil, fmt.Errorf("processor %q returned error: %s", proc.Name, output.Error)
			}
			// Use fallback message if provided, otherwise continue
			if output.Message != "" {
				result.Message = output.Message
			}
			continue
		}

		record(proc.Name, "ok", execDur, "")

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
				appendBuf.WriteString(output.Text)
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

	// Flush the append buffer once after all processors. On first-message assemblies,
	// wrap the accumulated region in <mitto_system_notes> so the agent treats it as
	// standing guidance rather than new tasks.
	if appendBuf.Len() > 0 {
		if input.IsFirstMessage {
			result.Message += wrapSystemNotes(appendBuf.String())
		} else {
			result.Message += appendBuf.String()
		}
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
	// promptCompletionFunc waits for a tracked auxiliary turn to finish and
	// report its durable save count. Production uses this completion-aware seam;
	// promptFunc remains for compatibility with fire-and-forget embedders/tests.
	promptCompletionFunc PromptCompletionFunc

	// notifyFunc is an optional callback invoked when a prompt-mode dispatch
	// exhausts all retries (see dispatchWithRetry). Set by the web layer via
	// SetNotifyFunc to surface a failure toast to the user — most importantly
	// for close-phase (conversationClosed) processors, where the session is
	// already archived and no other UI channel exists (mitto-exr). nil means
	// exhausted retries are only logged, matching pre-fix behavior.
	notifyFunc NotifyFunc

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

	// loadErrors holds processor documents that failed to load or validate.
	// Retained (not silently dropped) so the web layer can surface them in the UI.
	loadErrors []ProcessorLoadError

	// stateStore persists agentResponseCount and per-processor cadence state across
	// session restarts. Defaults to FileStateStore (writes processor_state.json in
	// the session directory). Injected as MemoryStateStore in unit tests.
	stateStore StateStore

	// pendingDispatchStore persists prompt-mode batches that dispatchWithRetry
	// could not deliver within its retry budget (mitto-3421), keyed by
	// workspace rather than session — see FilePendingDispatchStore for why a
	// session-scoped location is not durable enough for this. Defaults to
	// FilePendingDispatchStore. Injected with a temp-dir BaseDir in unit tests.
	pendingDispatchStore PendingDispatchStore

	// lateDeliveryFunc is an optional callback invoked by
	// FlushPendingDispatches when it successfully delivers one or more
	// previously-spooled batches (mitto-yfv8). Set by the web layer via
	// SetLateDeliveryFunc to surface an informational toast, distinct from
	// notifyFunc's failure toast.
	lateDeliveryFunc LateDeliveryFunc

	// clock returns the current time. Defaults to time.Now; overridden in tests
	// to make time-based cadence deterministic.
	clock func() time.Time

	// runRecorder, if set, receives one ProcessorRun per processor invocation
	// across Apply/applyWithRerun/ApplyAfter/ApplyOnClose (mitto-fm89 Stats
	// tab). nil by default — SessionManager wires it via SetRunRecorder to
	// append a session.EventTypeProcessorRun event. Must be propagated by
	// every CloneWith* constructor so workspace/override clones keep emitting.
	runRecorder RunRecorder
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
		// pendingDispatchStore is intentionally nil by default (mirrors the
		// promptFunc/notifyFunc opt-in pattern): unlike stateStore (always
		// scoped to a caller-provided session directory, a no-op when empty),
		// FilePendingDispatchStore's default BaseDir resolves to the shared
		// $MITTO_DIR — defaulting it here would make any test or embedding
		// that never calls SetPendingDispatchStore silently write to the
		// real user data directory the moment dispatchWithRetry gives up.
		// Production wiring opts in explicitly — see
		// SessionManager.ApplyOnCloseProcessors.
		clock: time.Now,
	}
}

// SetStateStore replaces the state store used for persistence.
// Primarily used in unit tests to inject a MemoryStateStore.
func (m *Manager) SetStateStore(s StateStore) {
	m.stateStore = s
}

// SetPendingDispatchStore replaces the store used to persist prompt-mode
// batches that dispatchWithRetry could not deliver within its retry budget
// (mitto-3421). Primarily used in unit tests to inject a
// FilePendingDispatchStore pointed at a temp directory.
func (m *Manager) SetPendingDispatchStore(s PendingDispatchStore) {
	m.pendingDispatchStore = s
}

// SetLateDeliveryFunc sets the callback invoked when FlushPendingDispatches
// successfully delivers one or more previously-spooled batches for a
// workspace (mitto-yfv8). Injected by the web layer to surface an
// informational UI toast, distinct from SetNotifyFunc's failure toast.
func (m *Manager) SetLateDeliveryFunc(fn LateDeliveryFunc) {
	m.lateDeliveryFunc = fn
}

// SetClock replaces the clock function used for cadence time checks.
// Primarily used in unit tests to make time-based cadence deterministic.
func (m *Manager) SetClock(fn func() time.Time) {
	m.clock = fn
}

// SetRunRecorder installs the callback that receives one ProcessorRun per
// processor invocation (mitto-fm89 Stats tab). nil disables recording.
func (m *Manager) SetRunRecorder(fn RunRecorder) {
	m.runRecorder = fn
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
	m.promptCompletionFunc = nil
}

// SetPromptCompletionFunc sets the completion-aware callback used by production
// prompt-mode processors. The durable spool is acknowledged only after this
// callback reports terminal success.
func (m *Manager) SetPromptCompletionFunc(fn PromptCompletionFunc) {
	m.promptCompletionFunc = fn
	m.promptFunc = nil
}

func (m *Manager) hasPromptExecutor() bool {
	return m != nil && (m.promptCompletionFunc != nil || m.promptFunc != nil)
}

// SetNotifyFunc sets the callback invoked when a prompt-mode dispatch
// exhausts all retries (mitto-exr). The callback is injected by the web
// layer to surface a workspace-scoped UI toast (e.g. via
// SessionManager.BroadcastWorkspaceUINotify), which works even when the
// originating session has already been archived (close-phase processors).
func (m *Manager) SetNotifyFunc(fn NotifyFunc) {
	m.notifyFunc = fn
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
		processorsDir:        m.processorsDir,
		logger:               m.logger,
		processors:           make([]*Processor, len(m.processors)),
		rerunState:           make(map[string]*processorRunState),
		promptFunc:           m.promptFunc,
		promptCompletionFunc: m.promptCompletionFunc,
		notifyFunc:           m.notifyFunc,
		totalActivations:     activations,
		lastActivationAt:     lastAt,
		stateStore:           m.stateStore,
		pendingDispatchStore: m.pendingDispatchStore,
		clock:                m.clock,
		runRecorder:          m.runRecorder,
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
		processorsDir:        m.processorsDir,
		logger:               logger,
		processors:           make([]*Processor, len(m.processors)),
		rerunState:           make(map[string]*processorRunState),
		promptFunc:           m.promptFunc,
		promptCompletionFunc: m.promptCompletionFunc,
		notifyFunc:           m.notifyFunc,
		totalActivations:     activations,
		lastActivationAt:     lastAt,
		stateStore:           m.stateStore,
		pendingDispatchStore: m.pendingDispatchStore,
		clock:                m.clock,
		runRecorder:          m.runRecorder,
		loadErrors:           append([]ProcessorLoadError(nil), m.loadErrors...),
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
		// Always capture load errors; stamp them as workspace-sourced.
		dirErrs := loader.Errors()
		for i := range dirErrs {
			if dirErrs[i].Source == "" {
				dirErrs[i].Source = ProcessorSourceWorkspace
			}
		}
		clone.loadErrors = append(clone.loadErrors, dirErrs...)
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
		processorsDir:        m.processorsDir,
		logger:               m.logger,
		processors:           make([]*Processor, len(m.processors)),
		rerunState:           make(map[string]*processorRunState),
		promptFunc:           m.promptFunc,
		promptCompletionFunc: m.promptCompletionFunc,
		notifyFunc:           m.notifyFunc,
		totalActivations:     activations,
		lastActivationAt:     lastAt,
		stateStore:           m.stateStore,
		pendingDispatchStore: m.pendingDispatchStore,
		clock:                m.clock,
		runRecorder:          m.runRecorder,
		loadErrors:           m.loadErrors, // read-only; safe to share
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

// LoadErrors returns processor load/validation errors retained during loading.
func (m *Manager) LoadErrors() []ProcessorLoadError { return m.loadErrors }

// Load loads all processors from the processors directory.
func (m *Manager) Load() error {
	loader := NewLoader(m.processorsDir, m.logger)
	procs, err := loader.Load()
	// Capture load errors regardless of whether the directory-level walk succeeded.
	errs := loader.Errors()
	for i := range errs {
		if errs[i].Source == "" {
			errs[i].Source = ProcessorSourceGlobal
		}
	}
	m.loadErrors = errs
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

	result, err := ApplyProcessors(ctx, m.processors, input, m.processorsDir, m.logger, m.recordRun)

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

	// appendBuf accumulates all append contributions so they can be wrapped once
	// in <mitto_system_notes> on first-message assemblies.
	var appendBuf strings.Builder

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
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "skipped"})
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
			// First @mitto: variable substitution, then Go-template render exposing
			// the session context as .Session/.ACP/.Parent/.Children/.Workspace etc.
			// Guarded by HasTemplateSyntax so non-template bodies skip context build.
			text := SubstituteVariables(proc.Text, input)
			if config.HasTemplateSyntax(text) {
				ctx := BuildCELContext(input)
				funcs := config.BuildTemplateFuncMap(ctx)
				if rendered, rerr := config.RenderPromptTemplate(proc.Name, text, ctx, funcs); rerr != nil {
					m.logger.Warn("text-mode processor template render failed; using unrendered text", "name", proc.Name, "error", rerr)
				} else {
					text = rendered
				}
			}
			switch proc.GetMutate() {
			case config.ProcessorMutatePrepend:
				result.Message = text + result.Message
				input.Message = result.Message
			case config.ProcessorMutateAppend:
				appendBuf.WriteString(text)
			}
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "ok"})
		} else if proc.IsPromptMode() {
			// Prompt-mode: collect for batched dispatch after loop.
			if !m.hasPromptExecutor() {
				m.logger.Warn("prompt-mode processor skipped: no PromptFunc configured",
					"name", proc.Name,
				)
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "skipped"})
				continue
			}

			// Build the prompt: first @mitto: variable substitution, then
			// Go-template render exposing resolved args as .Args.
			assembledPrompt := SubstituteVariables(proc.Prompt, input)
			resolvedArgs := ResolveProcessorArgs(proc.Parameters, input.ProcessorArgOverrides[proc.Name])
			ctx := BuildCELContext(input)
			ctx.Args = resolvedArgs
			funcs := config.BuildTemplateFuncMap(ctx)
			if rendered, rerr := config.RenderPromptTemplate(proc.Name, assembledPrompt, ctx, funcs); rerr != nil {
				m.logger.Warn("prompt-mode processor template render failed; using unrendered body", "name", proc.Name, "error", rerr)
			} else {
				assembledPrompt = rendered
			}
			// Skip dispatch when the rendered prompt is empty: a template may
			// deliberately render to nothing (e.g. no target file resolved), in
			// which case there is nothing to send to the auxiliary session.
			if strings.TrimSpace(assembledPrompt) == "" {
				m.logger.Debug("prompt-mode processor skipped: rendered prompt is empty", "name", proc.Name)
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "skipped"})
				continue
			}
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

			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "ok"})
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
				TasksUpstream:       input.TasksUpstream,
			}
			execStart := time.Now()
			output, err := executor.Execute(ctx, proc, procInput)
			execDur := time.Since(execStart)
			if err != nil {
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "error", Duration: execDur, Error: err.Error()})
				if proc.GetOnError() == ErrorFail {
					return nil, fmt.Errorf("processor %s failed: %w", proc.Name, err)
				}
				m.logger.Warn("processor failed, skipping",
					"name", proc.Name, "error", err)
				continue
			}
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "before", Outcome: "ok", Duration: execDur})
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
					appendBuf.WriteString(output.Text)
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

	// Flush the append buffer once after all processors. On first-message-style
	// assemblies, wrap the accumulated region in <mitto_system_notes> so the agent
	// treats it as standing guidance rather than new tasks.
	if appendBuf.Len() > 0 {
		if origIsFirst || len(rerunOverrides) > 0 {
			result.Message += wrapSystemNotes(appendBuf.String())
		} else {
			result.Message += appendBuf.String()
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
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
				m.logger.Debug("after-phase processor skipped",
					"name", proc.Name, "reason", "match=first_not_first_response")
				continue
			}
		case MatchAll:
			// always passes match filter (cadence may still gate it)
		case MatchAllExceptFirst:
			if isFirstAgentResponse {
				skipped++
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
				m.logger.Debug("after-phase processor skipped",
					"name", proc.Name, "reason", "match=allExceptFirst_is_first_response")
				continue
			}
		default:
			skipped++
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
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
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
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
			if !m.hasPromptExecutor() {
				m.logger.Warn("after-phase prompt-mode processor skipped: no PromptFunc configured",
					"name", proc.Name,
				)
				skipped++
				applied-- // undo the applied++ above
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
				continue
			}

			// Build the prompt: first @mitto: variable substitution, then
			// Go-template render exposing resolved args as .Args.
			assembledPrompt := substituteAfterVariables(proc.Prompt, input)
			resolvedArgs := ResolveProcessorArgs(proc.Parameters, input.ProcessorArgOverrides[proc.Name])
			tctx := &config.PromptEnabledContext{}
			tctx.Session.ID = input.SessionID
			tctx.Workspace.UUID = input.WorkspaceUUID
			tctx.Workspace.Folder = input.WorkingDir
			tctx.Args = resolvedArgs
			// PromptText resolver is not wired here; template fails-closed if used from processor-rendered prompts (mitto-85y.3).
			funcs := config.BuildTemplateFuncMap(tctx)
			if rendered, rerr := config.RenderPromptTemplate(proc.Name, assembledPrompt, tctx, funcs); rerr != nil {
				m.logger.Warn("prompt-mode processor template render failed; using unrendered body", "name", proc.Name, "error", rerr)
			} else {
				assembledPrompt = rendered
			}
			// Skip dispatch when the rendered prompt is empty: a template may
			// deliberately render to nothing (e.g. no target file resolved), in
			// which case there is nothing to send to the auxiliary session.
			if strings.TrimSpace(assembledPrompt) == "" {
				m.logger.Debug("after-phase prompt-mode processor skipped: rendered prompt is empty", "name", proc.Name)
				skipped++
				applied-- // undo the applied++ above
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "skipped"})
				continue
			}
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

			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "ok"})
			m.logger.Info("after-phase prompt-mode processor collected for dispatch",
				"name", proc.Name,
				"prompt_len", len(assembledPrompt),
			)
			continue
		}

		// Command mode (text mode is forbidden for agentResponded by the loader).
		afterExecStart := time.Now()
		stdout, execErr := executeAfterCommand(ctx, proc, m.processorsDir, input, m.logger)
		afterExecDur := time.Since(afterExecStart)

		if execErr != nil {
			m.logger.Warn("after-phase processor execution failed",
				"name", proc.Name, "error", execErr)
			result.Errors = append(result.Errors, ProcessorError{
				ProcessorName: proc.Name,
				Error:         execErr.Error(),
			})
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "error", Duration: afterExecDur, Error: execErr.Error()})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "error", Duration: afterExecDur, Error: parseErr.Error()})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "error", Duration: afterExecDur, Error: parseErr.Error()})
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
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "error", Duration: afterExecDur, Error: parseErr.Error()})
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

		m.recordRun(ProcessorRun{Name: proc.Name, Phase: "after", Outcome: "ok", Duration: afterExecDur})
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

// ApplyOnClose runs all conversationClosed processors for a session being archived.
// The pipeline is fire-and-forget from the caller's perspective: command-mode
// processors are executed synchronously within the caller's goroutine (bounded by
// each processor's timeout) and prompt-mode processors are collected and dispatched
// via the async PromptFunc. Callers should invoke this in a background goroutine so
// the archive HTTP request is not blocked.
//
// Only output:discard is allowed (enforced by the loader) — no notifications, action
// buttons or user_data patches are collected because the session is no longer active
// once it has been archived. Errors are logged but not propagated.
func (m *Manager) ApplyOnClose(ctx context.Context, input CloseProcessorInput) {
	if m == nil {
		return
	}

	applied := 0
	skipped := 0

	var pendingPrompts []pendingPromptDispatch

	m.logger.Info("close-phase processor pipeline starting",
		"total_processors", len(m.processors),
		"session_id", input.SessionID,
		"archive_reason", input.ArchiveReason,
	)

	for _, proc := range m.processors {
		// Phase filter: only conversationClosed processors fire here.
		if proc.When.On != PhaseConversationClosed {
			continue
		}

		if !proc.IsEnabled() {
			skipped++
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "skipped"})
			m.logger.Debug("close-phase processor skipped",
				"name", proc.Name, "reason", "disabled")
			continue
		}

		// EnabledWhen CEL gate — reuse the same context builder used elsewhere by
		// synthesising a minimal ProcessorInput.
		if proc.EnabledWhen != "" {
			procInput := &ProcessorInput{
				SessionID:     input.SessionID,
				WorkingDir:    input.WorkingDir,
				WorkspaceUUID: input.WorkspaceUUID,
			}
			if !evaluateEnabledWhen(proc, procInput, m.logger) {
				skipped++
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "skipped"})
				m.logger.Debug("close-phase processor skipped",
					"name", proc.Name, "reason", "enabledWhen_false")
				continue
			}
		}

		applied++
		m.logger.Info("applying close-phase processor",
			"name", proc.Name,
			"mode", map[bool]string{true: "prompt", false: "command"}[proc.IsPromptMode()],
		)

		if proc.IsPromptMode() {
			// mitto-ce3b: a cascade delete fires ApplyOnClose once for the parent
			// (ArchiveReason "deleted") and once more for every descendant
			// (ArchiveReason "parent_deleted") — see
			// internal/web/handlers/session_delete.go's HandleDeleteSession. Prompt-mode
			// close processors analyze the whole conversation tree, so the parent-level
			// run already covers the descendants; per-child runs are pure duplicate LLM
			// work. Skip prompt-mode collection for cascaded-child closes unless the
			// processor explicitly opts in (it may genuinely need per-child context).
			// Command-mode processors are unaffected — they may still want to run
			// per-session side effects (e.g. cleanup) for every closed session.
			if input.ArchiveReason == archiveReasonParentDeleted && !proc.RunOnCascadedClose {
				m.logger.Debug("close-phase prompt-mode processor skipped",
					"name", proc.Name, "reason", "cascaded_child_close")
				applied--
				skipped++
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "skipped"})
				continue
			}

			if !m.hasPromptExecutor() {
				m.logger.Warn("close-phase prompt-mode processor skipped: no PromptFunc configured",
					"name", proc.Name)
				applied--
				skipped++
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "skipped"})
				continue
			}

			assembledPrompt := substituteCloseVariables(proc.Prompt, input)
			resolvedArgs := ResolveProcessorArgs(proc.Parameters, input.ProcessorArgOverrides[proc.Name])
			tctx := &config.PromptEnabledContext{}
			tctx.Session.ID = input.SessionID
			tctx.Workspace.UUID = input.WorkspaceUUID
			tctx.Workspace.Folder = input.WorkingDir
			tctx.Args = resolvedArgs
			funcs := config.BuildTemplateFuncMap(tctx)
			if rendered, rerr := config.RenderPromptTemplate(proc.Name, assembledPrompt, tctx, funcs); rerr != nil {
				m.logger.Warn("close-phase prompt-mode processor template render failed; using unrendered body",
					"name", proc.Name, "error", rerr)
			} else {
				assembledPrompt = rendered
			}
			if strings.TrimSpace(assembledPrompt) == "" {
				m.logger.Debug("close-phase prompt-mode processor skipped: rendered prompt is empty",
					"name", proc.Name)
				applied--
				skipped++
				m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "skipped"})
				continue
			}
			pendingPrompts = append(pendingPrompts, pendingPromptDispatch{
				name:    proc.Name,
				prompt:  assembledPrompt,
				timeout: proc.GetTimeout().Duration(),
			})
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "ok"})
			m.logger.Info("close-phase prompt-mode processor collected for dispatch",
				"name", proc.Name,
				"prompt_len", len(assembledPrompt),
			)
			continue
		}

		// Command mode (text mode is forbidden by the loader).
		closeExecStart := time.Now()
		closeErr := executeCloseCommand(ctx, proc, m.processorsDir, input, m.logger)
		closeExecDur := time.Since(closeExecStart)
		if closeErr != nil {
			m.logger.Warn("close-phase processor execution failed",
				"name", proc.Name, "error", closeErr)
			m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "error", Duration: closeExecDur, Error: closeErr.Error()})
			continue
		}
		m.recordRun(ProcessorRun{Name: proc.Name, Phase: "close", Outcome: "ok", Duration: closeExecDur})
	}

	if len(pendingPrompts) > 0 {
		m.dispatchPromptBatch(input.WorkspaceUUID, pendingPrompts)
	}

	m.logger.Info("close-phase processor pipeline complete",
		"total", len(m.processors),
		"applied", applied,
		"skipped", skipped,
	)
}

// evaluateEnabledWhen evaluates a processor's EnabledWhen CEL expression against the
// given input. Returns true when the processor should apply, false when the gate
// evaluates to false. Missing/invalid expressions or evaluator failures fail-open
// (return true) so processors run even if CEL is unavailable, matching the ShouldApply
// behavior in hook.go.
func evaluateEnabledWhen(proc *Processor, input *ProcessorInput, logger *slog.Logger) bool {
	if proc.EnabledWhen == "" {
		return true
	}
	evaluator := config.GetCELEvaluator()
	if evaluator == nil {
		return true
	}
	ctx := BuildCELContext(input)
	compiled, err := evaluator.Compile(proc.EnabledWhen)
	if err != nil {
		if logger != nil {
			logger.Warn("close-phase processor invalid enabledWhen; failing open",
				"processor", proc.Name, "expression", proc.EnabledWhen, "error", err)
		}
		return true
	}
	result, err := evaluator.Evaluate(compiled, ctx)
	if err != nil {
		if logger != nil {
			logger.Warn("close-phase processor enabledWhen evaluate failed; failing open",
				"processor", proc.Name, "expression", proc.EnabledWhen, "error", err)
		}
		return true
	}
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

// dispatchPromptMaxRetries is the number of retry attempts (beyond the
// initial attempt) for a transient prompt-mode close-phase dispatch failure
// before giving up and (if configured) notifying the user. var (not const)
// so tests can shrink the retry cadence — mirrors the titleMaxRetries
// pattern in internal/conversation/title.go.
var dispatchPromptMaxRetries = 2 // 3 total attempts

// dispatchPromptRetryBaseDelay is the initial delay between retries
// (exponential backoff: base, 2*base, ...). The 15-minute workspace pin
// taken by SessionManager.ApplyOnCloseProcessors around the whole close
// pipeline (mitto-4is) exists specifically to make this retry window safe.
var dispatchPromptRetryBaseDelay = 2 * time.Second

// pendingDispatchBusyRetryInterval is the pause between flush-level retries
// when a fire-and-forget delivery leaves the shared process's only active-RPC
// slot occupied. The entry's configured timeout bounds the overall wait.
// var (not const) so tests can keep the async-slot lifecycle deterministic.
var pendingDispatchBusyRetryInterval = 100 * time.Millisecond

const pendingDispatchAckMaxAttempts = 3

var pendingDispatchAckRetryDelay = 25 * time.Millisecond

// dispatchSaturationMaxWait bounds how long dispatchWithRetry will keep
// retrying while the shared ACP process reports itself saturated, before
// giving up. This is deliberately much longer than the ~6s window covered
// by dispatchPromptMaxRetries/dispatchPromptRetryBaseDelay: the only event
// that clears saturation is GC Tier 5's saturated-idle recycle
// (internal/acpproc/acp_process_gc.go), which runs on a 30s ticker, so a 6s
// window can never span it — the fixed, failure-agnostic budget made
// saturation failures structurally unrecoverable (mitto-7q2). 120s spans
// ~4 GC ticks and sits comfortably inside the 15-minute close-pipeline
// workspace pin (SessionManager.ApplyOnCloseProcessors) that makes such a
// wait safe; that pin is not consulted by Tier 5, so waiting here cannot
// deadlock against it. var (not const) so tests can shrink the bound.
var dispatchSaturationMaxWait = 120 * time.Second

// dispatchSaturationRetryInterval is the fixed polling interval used while
// waiting out a sustained saturation window — distinct from the exponential
// backoff used for ordinary transient RPC failures, since here we are
// waiting for a periodic external event (the GC recycle tick) rather than
// hoping a flaky call succeeds sooner. var (not const) so tests can shrink
// the cadence.
var dispatchSaturationRetryInterval = 5 * time.Second

// isNonRetryableDispatchErr reports whether err indicates the workspace's
// shared ACP process is simply not running (e.g. reaped by GC between the
// caller's pre-check and this fire-and-forget dispatch, or a caller that
// skipped the pre-check entirely). Retrying cannot help — there is no
// process to route to — so this is a quiet, immediate skip rather than a
// retryable transient failure such as saturation or a busy shared process
// (mitto-6bn.1).
func isNonRetryableDispatchErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no shared process for workspace")
}

// isSaturationDispatchErr reports whether err indicates the shared ACP
// process is currently saturated (acperrors.ErrSharedProcessSaturated, as
// surfaced by any of the pre-RPC bails in getOrCreateAuxiliarySession).
// Unlike an ordinary transient RPC failure, saturation is cleared ONLY by
// the periodic GC Tier 5 recycle, so it is given its own bounded long-wait
// retry policy (dispatchSaturationMaxWait/dispatchSaturationRetryInterval)
// instead of counting against the normal fixed attempt budget (mitto-7q2).
//
// acperrors.ErrProcessBusy is excluded first via errors.Is: although it
// wraps the ErrSharedProcessSaturated umbrella (so its Error() string also
// contains "shared ACP process is saturated"), it is the PROACTIVE
// concurrency-load bail — purely transient, clearing as soon as concurrent
// RPC load drops, with no GC recycle involved. GC Tier 5 only recycles a
// process that is both saturated/gated AND has ActiveRPCs()==0, a condition
// ErrProcessBusy's cause (ActiveRPCs above threshold) fails by construction,
// so routing it into the long saturation wait means waiting for an event
// that structurally cannot happen (mitto-xhsj). Falling back to a substring
// match on the umbrella text (mirroring isNonRetryableDispatchErr) after the
// exclusion preserves saturation-shaped handling for the umbrella sentinel
// itself, the other granular sentinels (ErrProcessSaturated, ErrMCPInitGated),
// and legacy bare-string errors used by pre-existing tests (mitto-7q2).
func isSaturationDispatchErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, acperrors.ErrProcessBusy) {
		return false
	}
	return strings.Contains(err.Error(), "shared ACP process is saturated")
}

// dispatchRetryLogState bounds ordinary transient retry warnings to one per
// logical dispatch window. A pending-spool flush shares one state across its
// nested retry-loop calls so repeated backpressure remains visible at DEBUG
// without producing a new WARN for every attempt.
type dispatchRetryLogState struct {
	ordinaryRetryWarned bool
}

// dispatchWithRetry invokes m.promptFunc for the given name/prompt. Ordinary
// transient failures are retried up to dispatchPromptMaxRetries additional
// times with exponential backoff. Shared-process-saturation failures
// (isSaturationDispatchErr) are instead retried on a fixed
// dispatchSaturationRetryInterval cadence up to a bounded
// dispatchSaturationMaxWait wall-clock window, without consuming the normal
// attempt budget — see dispatchSaturationMaxWait for why saturation needs a
// much longer window than other transient errors (mitto-7q2). The
// non-retryable "no shared process for workspace" sentinel stops immediate
// retries but still flows through durable persistence for later delivery.
// If retrying is exhausted,
// the final error is logged at ERROR and, when a NotifyFunc is configured
// (see SetNotifyFunc), surfaced to the user — previously such failures were
// silently logged and the work was lost with no retry and no UI signal
// (mitto-exr). failLog lets single vs batched dispatch keep their distinct
// terminal wording.
func (m *Manager) dispatchWithRetry(workspaceUUID, name, prompt string, timeout time.Duration, skipLog, failLog string) {
	entry := PendingDispatchEntry{
		ID:             newPendingDispatchID(),
		WorkspaceUUID:  workspaceUUID,
		Name:           name,
		Prompt:         prompt,
		TimeoutSeconds: timeout.Seconds(),
		SavedAt:        time.Now(),
		Attempts:       1,
	}

	// Completion-aware dispatches are durable before the first RPC. A crash at
	// any point after this write leaves a claimed entry that a restarted process
	// can recover; only terminal success removes it.
	trackedPersisted := false
	if m.promptCompletionFunc != nil && m.pendingDispatchStore != nil && workspaceUUID != "" {
		appendResult, saveErr := m.pendingDispatchStore.AppendClaimed(entry)
		if saveErr != nil {
			if m.logger != nil {
				m.logger.Error(failLog+"; failed to persist batch before execution, dispatch aborted",
					"dispatch_id", entry.ID, "workspace_uuid", workspaceUUID, "name", name, "persist_error", saveErr)
			}
			if m.notifyFunc != nil {
				m.notifyFunc(workspaceUUID, name, fmt.Errorf("failed to persist batch before execution: %w", saveErr))
			}
			return
		}
		entry = appendResult.Entry
		trackedPersisted = true
		m.logPendingDispatchDrops(workspaceUUID, appendResult.Dropped)
	}

	completion, totalAttempts, waited, lastErr := m.runDispatchRetryLoopTracked(
		workspaceUUID, name, entry.ID, prompt, timeout, skipLog, &dispatchRetryLogState{})
	if lastErr == nil {
		if trackedPersisted {
			if !m.acknowledgeCompletedDispatch(entry) {
				return
			}
		}
		if m.logger != nil && m.promptCompletionFunc != nil {
			m.logger.Info("prompt-mode processor completed",
				"dispatch_id", entry.ID, "workspace_uuid", workspaceUUID, "name", name,
				"attempts", totalAttempts, "waited", waited,
				"save_count", completion.SaveCount, "save_count_known", completion.SaveCountKnown)
		}
		return
	}

	// This is the first time this batch has ever been spooled (as opposed
	// to a re-failure during FlushPendingDispatches, mitto-yfv8), so its
	// PendingDispatchEntry.Attempts starts at 1 regardless of how many RPC
	// attempts (totalAttempts, used only for logging here) it took.
	spoolAttempts := 1

	// mitto-3421: previously the combined prompt was simply dropped here — for
	// close-phase (conversationClosed) processors the originating session's
	// events are already gone, so silent discard on give-up was permanent
	// data loss. Persist the undelivered batch to a workspace-scoped spool
	// (independent of any single session directory, which may already be
	// removed from disk — see FilePendingDispatchStore) so it survives and
	// can be retried later, converting the loss into a delay.
	persisted := false
	entry.Attempts = spoolAttempts
	entry.SavedAt = time.Now()
	if lastErr != nil {
		entry.LastError = lastErr.Error()
	}
	if trackedPersisted {
		dropped, saveErr := m.pendingDispatchStore.Requeue(workspaceUUID, []PendingDispatchEntry{entry})
		if saveErr != nil {
			if m.logger != nil {
				m.logger.Error(failLog+"; failed to release durable claim for retry",
					"dispatch_id", entry.ID, "workspace_uuid", workspaceUUID, "name", name, "persist_error", saveErr)
			}
		} else {
			persisted = true
			m.logPendingDispatchDrops(workspaceUUID, dropped)
		}
	} else if m.pendingDispatchStore != nil && workspaceUUID != "" {
		appendResult, saveErr := m.pendingDispatchStore.Append(entry)
		if saveErr != nil {
			if m.logger != nil {
				m.logger.Error(failLog+"; failed to persist undelivered batch, work is lost",
					"dispatch_id", entry.ID,
					"workspace_uuid", workspaceUUID,
					"name", name,
					"attempts", totalAttempts,
					"waited", waited,
					"error", lastErr,
					"persist_error", saveErr,
				)
			}
		} else {
			persisted = true
			entry = appendResult.Entry
			m.logPendingDispatchDrops(workspaceUUID, appendResult.Dropped)
		}
	}

	if m.logger != nil {
		if persisted {
			m.logger.Error(failLog+"; batch persisted for later retry",
				"dispatch_id", entry.ID,
				"workspace_uuid", workspaceUUID,
				"name", name,
				"attempts", totalAttempts,
				"waited", waited,
				"error", lastErr,
			)
		} else if m.pendingDispatchStore == nil || workspaceUUID == "" {
			m.logger.Error(failLog+"; batch not persisted, work is lost",
				"name", name,
				"attempts", totalAttempts,
				"waited", waited,
				"error", lastErr,
			)
		}
	}
	if m.notifyFunc != nil {
		notifyErr := lastErr
		if persisted {
			notifyErr = fmt.Errorf("delivery deferred; batch persisted for later retry: %w", lastErr)
		}
		m.notifyFunc(workspaceUUID, name, notifyErr)
	}
}

func (m *Manager) logPendingDispatchDrops(workspaceUUID string, dropped []PendingDispatchEntry) {
	if m.logger == nil {
		return
	}
	for _, entry := range dropped {
		m.logger.Error("pending-dispatch spool: dropping oldest entry at capacity",
			"dispatch_id", entry.ID, "workspace_uuid", workspaceUUID,
			"name", entry.Name, "max_entries", pendingDispatchMaxEntries)
	}
}

func (m *Manager) acknowledgeCompletedDispatch(entry PendingDispatchEntry) bool {
	var ackErr error
	for attempt := 1; attempt <= pendingDispatchAckMaxAttempts; attempt++ {
		ackErr = m.pendingDispatchStore.Acknowledge(entry.WorkspaceUUID, []string{entry.ID})
		if ackErr == nil {
			return true
		}
		if attempt < pendingDispatchAckMaxAttempts {
			time.Sleep(pendingDispatchAckRetryDelay * time.Duration(attempt))
		}
	}
	if m.logger != nil {
		m.logger.Error("prompt-mode processor completed but durable acknowledgement failed; claim retained for restart recovery",
			"dispatch_id", entry.ID, "workspace_uuid", entry.WorkspaceUUID,
			"name", entry.Name, "attempts", pendingDispatchAckMaxAttempts, "error", ackErr)
	}
	if m.notifyFunc != nil {
		m.notifyFunc(entry.WorkspaceUUID, entry.Name,
			fmt.Errorf("processor completed but durable acknowledgement failed; retry retained for restart recovery: %w", ackErr))
	}
	return false
}

// runDispatchRetryLoop performs the actual retry/backoff loop against
// m.promptFunc: ordinary transient failures are retried up to
// dispatchPromptMaxRetries additional times with exponential backoff, and
// shared-process-saturation failures are retried on a fixed
// dispatchSaturationRetryInterval cadence up to dispatchSaturationMaxWait
// without consuming the normal attempt budget (mitto-7q2). Returns (attempts,
// waited, nil) on success, or (attempts, waited, the final error) once
// retrying is exhausted or the non-retryable "no shared process for
// workspace" sentinel is seen. The caller decides whether to persist that
// terminal result; initial dispatches spool it while flushes requeue it.
// attempts is the number of RPC attempts made in this call and waited is the
// total wall-clock time spent in this call (including sleeps), both for
// caller logging.
//
// Saturation-wait logging (mitto-nnte): only the FIRST saturation
// observation for a given call is logged at WARN (it carries max_wait and
// the computed deadline, enough context to triage on its own); every
// subsequent poll while still waiting for the same GC recycle is logged at
// DEBUG instead, since it is a near-duplicate of the initial WARN and a
// sustained saturation window previously produced up to
// dispatchSaturationMaxWait/dispatchSaturationRetryInterval (~24) WARNs per
// occurrence. The message text is unchanged across both levels.
func (m *Manager) runDispatchRetryLoop(workspaceUUID, name, prompt string, timeout time.Duration, skipLog string) (int, time.Duration, error) {
	_, attempts, waited, err := m.runDispatchRetryLoopTracked(
		workspaceUUID, name, newPendingDispatchID(), prompt, timeout, skipLog, &dispatchRetryLogState{})
	return attempts, waited, err
}

func (m *Manager) runDispatchRetryLoopTracked(workspaceUUID, name, dispatchID, prompt string, timeout time.Duration, skipLog string, logState *dispatchRetryLogState) (PromptCompletion, int, time.Duration, error) {
	start := time.Now()
	var completion PromptCompletion
	var lastErr error
	var saturationDeadline time.Time // zero until the first saturation error is observed
	normalRetries := 0               // count of non-saturation failures, bounded by dispatchPromptMaxRetries
	totalAttempts := 0

	for {
		if totalAttempts > 0 {
			if isSaturationDispatchErr(lastErr) {
				time.Sleep(dispatchSaturationRetryInterval)
			} else {
				delay := dispatchPromptRetryBaseDelay * time.Duration(uint(1)<<uint(normalRetries-1))
				time.Sleep(delay)
			}
		}

		bgCtx, cancel := context.WithTimeout(context.Background(), timeout)
		if m.promptCompletionFunc != nil {
			completion, lastErr = m.promptCompletionFunc(bgCtx, workspaceUUID, name, dispatchID, prompt)
		} else {
			lastErr = m.promptFunc(bgCtx, workspaceUUID, name, prompt)
		}
		cancel()
		totalAttempts++

		if lastErr == nil {
			return completion, totalAttempts, time.Since(start), nil
		}

		if isNonRetryableDispatchErr(lastErr) {
			if m.logger != nil {
				m.logger.Info("prompt-mode processor dispatch unavailable; deferring durable delivery",
					"name", name, "error", lastErr)
			}
			return completion, totalAttempts, time.Since(start), lastErr
		}

		if isSaturationDispatchErr(lastErr) {
			firstSaturationObservation := saturationDeadline.IsZero()
			if firstSaturationObservation {
				saturationDeadline = time.Now().Add(dispatchSaturationMaxWait)
			}
			if time.Now().Before(saturationDeadline) {
				if m.logger != nil {
					if firstSaturationObservation {
						m.logger.Warn("prompt-mode processor dispatch attempt failed; shared process saturated, waiting for GC recycle",
							"name", name,
							"attempt", totalAttempts,
							"max_wait", dispatchSaturationMaxWait,
							"deadline", saturationDeadline,
							"error", lastErr,
						)
					} else {
						m.logger.Debug("prompt-mode processor dispatch attempt failed; shared process saturated, waiting for GC recycle",
							"name", name,
							"attempt", totalAttempts,
							"error", lastErr,
						)
					}
				}
				continue
			}
			// Bounded saturation wait exhausted; give up below.
			break
		}

		normalRetries++
		if normalRetries > dispatchPromptMaxRetries {
			break
		}
		if m.logger != nil {
			logArgs := []any{
				"name", name,
				"attempt", totalAttempts,
				"max_attempts", dispatchPromptMaxRetries + 1,
				"error", lastErr,
			}
			if !logState.ordinaryRetryWarned {
				logState.ordinaryRetryWarned = true
				m.logger.Warn("prompt-mode processor dispatch attempt failed; will retry", logArgs...)
			} else {
				m.logger.Debug("prompt-mode processor dispatch attempt failed; will retry", logArgs...)
			}
		}
	}

	return completion, totalAttempts, time.Since(start), lastErr
}

// FlushPendingDispatches loads any prompt-mode batches previously spooled
// for workspaceUUID (because dispatchWithRetry exhausted its saturation
// retry budget — mitto-3421) and retries them now that the workspace is
// believed dispatchable, converting the earlier delay into delivery
// (mitto-yfv8).
//
// Best-effort and side-effect free when not wired: a no-op if there is no
// pendingDispatchStore, no promptFunc, or an empty workspaceUUID. Entries
// are retried sequentially (not concurrently) so a flush of several stale
// batches cannot itself re-saturate the shared process — the same condition
// that produced the spool in the first place. Claim atomically drains the
// current spool; entries appended afterward land in a new spool, and Requeue
// merges failures back without overwriting those additions (mitto-gfr1).
// A fire-and-forget delivery may leave the shared process busy while its prompt
// runs; the next entry waits and retries within its configured timeout so one
// flush opportunity can drain several entries sequentially (mitto-e3ut.1).
// Entries that fail again are written back with an incremented Attempts, and
// any entry whose Attempts exceeds pendingDispatchMaxAttempts is dropped instead
// of blocking the rest of the spool. When one or more entries are delivered,
// lateDeliveryFunc (if set) is invoked once with all delivered names.
func (m *Manager) FlushPendingDispatches(ctx context.Context, workspaceUUID string) {
	if m == nil || m.pendingDispatchStore == nil || !m.hasPromptExecutor() || workspaceUUID == "" {
		return
	}

	claim, err := m.pendingDispatchStore.Claim(workspaceUUID)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("pending-dispatch flush: failed to claim spool",
				"workspace_uuid", workspaceUUID, "error", err)
		}
		return
	}
	if m.logger != nil {
		for _, expired := range claim.Expired {
			m.logger.Error("pending-dispatch flush: dropping expired entry",
				"dispatch_id", expired.ID,
				"workspace_uuid", workspaceUUID,
				"name", expired.Name,
				"saved_at", expired.SavedAt,
				"max_age", pendingDispatchMaxAge,
			)
		}
	}
	entries := claim.Entries
	if len(entries) == 0 {
		return
	}

	var delivered []string
	var requeue []PendingDispatchEntry
	retryLogState := &dispatchRetryLogState{}

flushEntries:
	for i, entry := range entries {
		if ctx.Err() != nil {
			// Out of time — requeue this and all remaining entries unchanged
			// (does not count as a further attempt).
			requeue = append(requeue, entries[i:]...)
			break
		}

		if entry.Attempts >= pendingDispatchMaxAttempts {
			if m.logger != nil {
				m.logger.Error("pending-dispatch flush: dropping entry after exceeding max attempts",
					"dispatch_id", entry.ID,
					"workspace_uuid", workspaceUUID,
					"name", entry.Name,
					"attempts", entry.Attempts,
				)
			}
			if ackErr := m.pendingDispatchStore.Acknowledge(workspaceUUID, []string{entry.ID}); ackErr != nil && m.logger != nil {
				m.logger.Error("pending-dispatch flush: failed to acknowledge dropped entry",
					"dispatch_id", entry.ID, "workspace_uuid", workspaceUUID, "error", ackErr)
			}
			continue
		}

		timeout := time.Duration(entry.TimeoutSeconds * float64(time.Second))
		if timeout <= 0 {
			timeout = DefaultTimeout
		}

		var completion PromptCompletion
		var lastErr error
		var busyDeadline time.Time
		for {
			if !busyDeadline.IsZero() && !time.Now().Before(busyDeadline) {
				requeue = append(requeue, entries[i:]...)
				break flushEntries
			}

			completion, _, _, lastErr = m.runDispatchRetryLoopTracked(
				workspaceUUID, entry.Name, entry.ID, entry.Prompt, timeout,
				"pending-dispatch flush skipped: shared ACP process not available", retryLogState)
			if !errors.Is(lastErr, acperrors.ErrProcessBusy) {
				break
			}

			if busyDeadline.IsZero() {
				busyDeadline = time.Now().Add(timeout)
				if m.logger != nil {
					m.logger.Info("pending-dispatch flush: shared process busy; waiting to continue sequential drain",
						"dispatch_id", entry.ID,
						"workspace_uuid", workspaceUUID,
						"name", entry.Name,
						"deadline", busyDeadline,
					)
				}
			}

			wait := pendingDispatchBusyRetryInterval
			if remaining := time.Until(busyDeadline); wait > remaining {
				wait = remaining
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				requeue = append(requeue, entries[i:]...)
				break flushEntries
			case <-timer.C:
			}
		}
		if lastErr == nil {
			if !m.acknowledgeCompletedDispatch(entry) {
				continue
			}
			delivered = append(delivered, entry.Name)
			if m.logger != nil {
				m.logger.Info("pending-dispatch flush: delivered spooled batch",
					"dispatch_id", entry.ID,
					"workspace_uuid", workspaceUUID,
					"name", entry.Name,
					"prior_attempts", entry.Attempts,
					"save_count", completion.SaveCount,
					"save_count_known", completion.SaveCountKnown,
				)
			}
			continue
		}

		if isNonRetryableDispatchErr(lastErr) {
			// Workspace stopped being dispatchable mid-flush; requeue this
			// and all remaining entries unchanged (does not count as a
			// further attempt) and stop — later entries would fail the
			// same way.
			requeue = append(requeue, entries[i:]...)
			break
		}

		entry.Attempts++
		entry.LastError = lastErr.Error()
		entry.SavedAt = time.Now()
		if entry.Attempts >= pendingDispatchMaxAttempts {
			if m.logger != nil {
				m.logger.Error("pending-dispatch flush: entry failed again and exceeded max attempts; dropping",
					"dispatch_id", entry.ID,
					"workspace_uuid", workspaceUUID,
					"name", entry.Name,
					"attempts", entry.Attempts,
					"error", lastErr,
				)
			}
			if m.notifyFunc != nil {
				m.notifyFunc(workspaceUUID, entry.Name, lastErr)
			}
			if ackErr := m.pendingDispatchStore.Acknowledge(workspaceUUID, []string{entry.ID}); ackErr != nil && m.logger != nil {
				m.logger.Error("pending-dispatch flush: failed to acknowledge terminally failed entry",
					"dispatch_id", entry.ID, "workspace_uuid", workspaceUUID, "error", ackErr)
			}
			continue
		}
		requeue = append(requeue, entry)
	}

	if len(requeue) > 0 {
		dropped, requeueErr := m.pendingDispatchStore.Requeue(workspaceUUID, requeue)
		if requeueErr != nil {
			if m.logger != nil {
				IDs := make([]string, 0, len(requeue))
				for _, entry := range requeue {
					IDs = append(IDs, entry.ID)
				}
				m.logger.Error("pending-dispatch flush: failed to write back unresolved entries, work may be lost",
					"workspace_uuid", workspaceUUID, "dispatch_ids", IDs, "error", requeueErr, "count", len(requeue))
			}
		} else if m.logger != nil {
			for _, entry := range requeue {
				m.logger.Info("pending-dispatch flush: requeued unresolved entry",
					"dispatch_id", entry.ID,
					"workspace_uuid", workspaceUUID,
					"name", entry.Name,
					"attempts", entry.Attempts,
				)
			}
			for _, entry := range dropped {
				m.logger.Error("pending-dispatch spool: dropping oldest entry at capacity",
					"dispatch_id", entry.ID,
					"workspace_uuid", workspaceUUID,
					"name", entry.Name,
					"max_entries", pendingDispatchMaxEntries,
				)
			}
		}
	}

	if len(delivered) > 0 && m.lateDeliveryFunc != nil {
		m.lateDeliveryFunc(workspaceUUID, delivered)
	}
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
		go m.dispatchWithRetry(workspaceUUID, p.name, p.prompt, p.timeout,
			"prompt-mode processor dispatch skipped: shared ACP process not available",
			"prompt-mode processor dispatch failed",
		)
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

	go m.dispatchWithRetry(workspaceUUID, combinedName, combinedPrompt, maxTimeout,
		"batched prompt-mode processor dispatch skipped: shared ACP process not available",
		"batched prompt-mode processor dispatch failed",
	)

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
