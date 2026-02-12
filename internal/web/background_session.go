package web

import (
	"context"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/auxiliary"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversion"
	"github.com/inercia/mitto/internal/logging"
	"github.com/inercia/mitto/internal/msghooks"
	"github.com/inercia/mitto/internal/runner"
	"github.com/inercia/mitto/internal/session"
)

// defaultPersistInterval is the default interval for periodic persistence of buffered
// agent messages during streaming. This reduces data loss if the server crashes mid-stream.
const defaultPersistInterval = 5 * time.Second

// BackgroundSession manages an ACP session that runs independently of WebSocket connections.
// It continues running even when no client is connected, persisting all events to disk.
// Multiple observers can subscribe to receive real-time updates.
type BackgroundSession struct {
	// Immutable identifiers
	persistedID string // Session ID for persistence and routing
	acpID       string // ACP protocol session ID

	// ACP connection state
	acpCmd    *exec.Cmd
	acpConn   *acp.ClientSideConnection
	acpClient *WebClient
	acpWait   func() error // cleanup function from runner.RunWithPipes or cmd.Wait

	// Session persistence
	recorder    *session.Recorder
	eventBuffer *EventBuffer // Unified buffer for all streaming events
	bufferMu    sync.Mutex   // Protects eventBuffer

	// Sequence number tracking for event ordering
	// nextSeq is the next sequence number to assign to a new event.
	// It's initialized from the store's EventCount + 1 and incremented for each new event.
	// This ensures streaming events have the same seq as their persisted counterparts.
	nextSeq int64
	seqMu   sync.Mutex // Protects nextSeq

	// Session lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	closed atomic.Int32

	// Observers (multiple clients can observe this session)
	observersMu sync.RWMutex
	observers   map[SessionObserver]struct{}

	// Prompt state
	promptMu             sync.Mutex
	promptCond           *sync.Cond // Condition variable for waiting on prompt completion
	isPrompting          bool
	promptCount          int
	promptStartTime      time.Time // When the current prompt started (for stuck detection)
	lastResponseComplete time.Time // When the agent last completed a response (for queue delay)

	// Periodic persistence for crash recovery (H3 fix)
	// This timer periodically persists buffered agent messages during streaming
	// to reduce data loss if the server crashes mid-stream.
	persistTimer    *time.Timer
	persistInterval time.Duration

	// Configuration
	autoApprove bool
	logger      *slog.Logger

	// Resume state - for injecting conversation history
	isResumed       bool           // True if this session was resumed from storage
	store           *session.Store // Store reference for reading history
	historyInjected bool           // True after history has been injected

	// Conversation processing
	processors    []config.MessageProcessor // Merged processors from global and workspace config
	hookManager   *msghooks.Manager         // External command hooks for message transformation
	workingDir    string                    // Working directory for hook execution
	isFirstPrompt bool                      // True until first prompt is sent (for processor conditions)

	// Queue processing
	queueConfig *config.QueueConfig // Queue configuration (nil means use defaults)

	// Action buttons (follow-up suggestions)
	// These are AI-generated response options shown after the agent completes a response.
	// We use a two-tier cache (memory + disk) so suggestions persist across client
	// reconnections and server restarts. See docs/devel/follow-up-suggestions.md.
	actionButtonsConfig *config.ActionButtonsConfig // Configuration (nil means disabled)
	actionButtonsMu     sync.RWMutex                // Protects cachedActionButtons
	cachedActionButtons []ActionButton              // In-memory cache for fast access

	// File links configuration
	fileLinksConfig *config.FileLinksConfig // Configuration for file path linking
	apiPrefix       string                  // URL prefix for API endpoints (for HTTP file links)
	workspaceUUID   string                  // Workspace UUID for secure file links

	// Restricted runner for sandboxed execution
	runner *runner.Runner // Optional runner for restricted execution (nil = direct execution)

	// onStreamingStateChanged is called when the session's streaming state changes.
	onStreamingStateChanged func(sessionID string, isStreaming bool)

	// onPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	onPlanStateChanged func(sessionID string, entries []PlanEntry)

	// Available slash commands from the agent
	availableCommandsMu sync.RWMutex
	availableCommands   []AvailableCommand
}

// BackgroundSessionConfig holds configuration for creating a BackgroundSession.
type BackgroundSessionConfig struct {
	PersistedID  string
	ACPCommand   string
	ACPServer    string
	ACPSessionID string // ACP-assigned session ID for resumption (optional)
	WorkingDir   string
	AutoApprove  bool
	Logger       *slog.Logger
	Store        *session.Store
	SessionName  string
	Processors   []config.MessageProcessor // Merged processors for message transformation
	HookManager  *msghooks.Manager         // External command hooks for message transformation
	QueueConfig  *config.QueueConfig       // Queue processing configuration
	Runner       *runner.Runner            // Optional restricted runner for sandboxed execution

	ActionButtonsConfig *config.ActionButtonsConfig // Action buttons configuration
	FileLinksConfig     *config.FileLinksConfig     // File path linking configuration
	APIPrefix           string                      // URL prefix for API endpoints (for HTTP file links)
	WorkspaceUUID       string                      // Workspace UUID for secure file links

	// OnStreamingStateChanged is called when the session's streaming state changes.
	// It's called with true when streaming starts (user sends prompt) and false when it ends.
	OnStreamingStateChanged func(sessionID string, isStreaming bool)

	// OnPlanStateChanged is called when the agent plan state changes.
	// Used to cache plan state in SessionManager for restoration on conversation switch.
	OnPlanStateChanged func(sessionID string, entries []PlanEntry)
}

// NewBackgroundSession creates a new background session.
// The session starts the ACP process and is ready to accept prompts.
func NewBackgroundSession(cfg BackgroundSessionConfig) (*BackgroundSession, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bs := &BackgroundSession{
		ctx:                     ctx,
		cancel:                  cancel,
		autoApprove:             cfg.AutoApprove,
		logger:                  cfg.Logger,
		eventBuffer:             NewEventBuffer(),
		observers:               make(map[SessionObserver]struct{}),
		processors:              cfg.Processors,
		hookManager:             cfg.HookManager,
		workingDir:              cfg.WorkingDir,
		isFirstPrompt:           true, // New session starts with first prompt pending
		queueConfig:             cfg.QueueConfig,
		actionButtonsConfig:     cfg.ActionButtonsConfig,
		fileLinksConfig:         cfg.FileLinksConfig,
		apiPrefix:               cfg.APIPrefix,
		workspaceUUID:           cfg.WorkspaceUUID,
		runner:                  cfg.Runner,
		persistInterval:         defaultPersistInterval, // H3 fix: periodic persistence
		onStreamingStateChanged: cfg.OnStreamingStateChanged,
		onPlanStateChanged:      cfg.OnPlanStateChanged,
	}
	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Create recorder for persistence
	if cfg.Store != nil {
		bs.recorder = session.NewRecorder(cfg.Store)
		bs.persistedID = bs.recorder.SessionID()
		bs.store = cfg.Store
		if err := bs.recorder.Start(cfg.ACPServer, cfg.WorkingDir); err != nil {
			cancel()
			return nil, err
		}
		// Update session name and runner info
		runnerType := "exec"
		isRestricted := false
		if cfg.Runner != nil {
			runnerType = cfg.Runner.Type()
			isRestricted = cfg.Runner.IsRestricted()
		}
		cfg.Store.UpdateMetadata(bs.persistedID, func(meta *session.Metadata) {
			if cfg.SessionName != "" {
				meta.Name = cfg.SessionName
			}
			meta.RunnerType = runnerType
			meta.RunnerRestricted = isRestricted
		})

		// Initialize nextSeq from current event count (new session starts at 1)
		bs.nextSeq = int64(bs.recorder.EventCount()) + 1

		// Create session-scoped logger with context
		if bs.logger != nil {
			bs.logger = logging.WithSessionContext(bs.logger, bs.persistedID, cfg.WorkingDir, cfg.ACPServer)
		}
	}

	// Log runner information
	if bs.logger != nil {
		runnerType := "exec"
		isRestricted := false
		if cfg.Runner != nil {
			runnerType = cfg.Runner.Type()
			isRestricted = cfg.Runner.IsRestricted()
		}
		bs.logger.Debug("session created",
			"session_id", bs.persistedID,
			"workspace", cfg.WorkingDir,
			"acp_server", cfg.ACPServer,
			"runner_type", runnerType,
			"runner_restricted", isRestricted)
	}

	// Start ACP process (no ACP session ID for new sessions)
	if err := bs.startACPProcess(cfg.ACPCommand, cfg.WorkingDir, ""); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
		}
		return nil, err
	}

	// Store the ACP session ID in metadata for future resumption
	if cfg.Store != nil && bs.acpID != "" {
		if err := cfg.Store.UpdateMetadata(bs.persistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to store ACP session ID in metadata", "error", err)
		}
	}

	return bs, nil
}

// ResumeBackgroundSession creates a background session for an existing persisted session.
// This is used when switching to an old conversation - we create a new ACP connection
// but continue recording to the existing session.
// If the agent supports session loading and we have a stored ACP session ID, we attempt
// to resume the ACP session on the server side as well.
func ResumeBackgroundSession(config BackgroundSessionConfig) (*BackgroundSession, error) {
	if config.PersistedID == "" {
		return nil, &sessionError{"persisted session ID is required for resume"}
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create session-scoped logger with context
	sessionLogger := config.Logger
	if sessionLogger != nil {
		sessionLogger = logging.WithSessionContext(sessionLogger, config.PersistedID, config.WorkingDir, config.ACPServer)
	}

	bs := &BackgroundSession{
		persistedID:             config.PersistedID,
		ctx:                     ctx,
		cancel:                  cancel,
		autoApprove:             config.AutoApprove,
		logger:                  sessionLogger,
		eventBuffer:             NewEventBuffer(),
		observers:               make(map[SessionObserver]struct{}),
		isResumed:               true, // Mark as resumed session
		store:                   config.Store,
		processors:              config.Processors,
		hookManager:             config.HookManager,
		workingDir:              config.WorkingDir,
		isFirstPrompt:           false, // Resumed session = first prompt already sent
		queueConfig:             config.QueueConfig,
		actionButtonsConfig:     config.ActionButtonsConfig,
		fileLinksConfig:         config.FileLinksConfig,
		apiPrefix:               config.APIPrefix,
		workspaceUUID:           config.WorkspaceUUID,
		runner:                  config.Runner,
		persistInterval:         defaultPersistInterval, // H3 fix: periodic persistence
		onStreamingStateChanged: config.OnStreamingStateChanged,
		onPlanStateChanged:      config.OnPlanStateChanged,
	}
	// Initialize condition variable for prompt completion waiting
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Resume recorder for the existing session
	if config.Store != nil {
		bs.recorder = session.NewRecorderWithID(config.Store, config.PersistedID)
		if err := bs.recorder.Resume(); err != nil {
			cancel()
			return nil, &sessionError{"failed to resume session recording: " + err.Error()}
		}
		// Update the metadata to mark it as active again and store runner info
		runnerType := "exec"
		isRestricted := false
		if config.Runner != nil {
			runnerType = config.Runner.Type()
			isRestricted = config.Runner.IsRestricted()
		}
		if err := config.Store.UpdateMetadata(config.PersistedID, func(m *session.Metadata) {
			m.Status = "active"
			m.RunnerType = runnerType
			m.RunnerRestricted = isRestricted
		}); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to update session status", "error", err)
		}

		// Initialize nextSeq from current event count
		bs.nextSeq = int64(bs.recorder.EventCount()) + 1
	}

	// Log runner information
	if bs.logger != nil {
		runnerType := "exec"
		isRestricted := false
		if config.Runner != nil {
			runnerType = config.Runner.Type()
			isRestricted = config.Runner.IsRestricted()
		}
		bs.logger.Debug("session resumed",
			"session_id", config.PersistedID,
			"workspace", config.WorkingDir,
			"acp_server", config.ACPServer,
			"runner_type", runnerType,
			"runner_restricted", isRestricted)
	}

	// Start ACP process, passing the ACP session ID for potential resumption
	if err := bs.startACPProcess(config.ACPCommand, config.WorkingDir, config.ACPSessionID); err != nil {
		cancel()
		if bs.recorder != nil {
			bs.recorder.End("failed_to_start")
		}
		return nil, err
	}

	// If we created a new ACP session (different from the one we tried to resume),
	// update the metadata with the new ACP session ID
	if config.Store != nil && bs.acpID != "" && bs.acpID != config.ACPSessionID {
		if err := config.Store.UpdateMetadata(config.PersistedID, func(m *session.Metadata) {
			m.ACPSessionID = bs.acpID
		}); err != nil && bs.logger != nil {
			bs.logger.Warn("Failed to update ACP session ID in metadata", "error", err)
		}
	}

	return bs, nil
}

// GetSessionID returns the persisted session ID.
func (bs *BackgroundSession) GetSessionID() string {
	return bs.persistedID
}

// GetWorkingDir returns the working directory for this session.
func (bs *BackgroundSession) GetWorkingDir() string {
	return bs.workingDir
}

// GetACPID returns the ACP protocol session ID.
func (bs *BackgroundSession) GetACPID() string {
	return bs.acpID
}

// IsClosed returns true if the session has been closed.
func (bs *BackgroundSession) IsClosed() bool {
	return bs.closed.Load() != 0
}

// IsPrompting returns true if a prompt is currently being processed.
func (bs *BackgroundSession) IsPrompting() bool {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.isPrompting
}

// GetPromptCount returns the number of prompts sent in this session.
func (bs *BackgroundSession) GetPromptCount() int {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.promptCount
}

// GetLastResponseCompleteTime returns when the agent last completed a response.
// Returns zero time if no response has completed yet.
func (bs *BackgroundSession) GetLastResponseCompleteTime() time.Time {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()
	return bs.lastResponseComplete
}

// WaitForResponseComplete waits for the current prompt to complete, if one is in progress.
// Returns true if the prompt completed within the timeout, false if it timed out.
// If no prompt is in progress, returns immediately with true.
func (bs *BackgroundSession) WaitForResponseComplete(timeout time.Duration) bool {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()

	// If not prompting, return immediately
	if !bs.isPrompting {
		return true
	}

	// Create a timer for the timeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// Use a goroutine to wait on the condition with timeout
	done := make(chan bool, 1)
	go func() {
		bs.promptMu.Lock()
		for bs.isPrompting && bs.closed.Load() == 0 {
			bs.promptCond.Wait()
		}
		completed := !bs.isPrompting || bs.closed.Load() != 0
		bs.promptMu.Unlock()
		done <- completed
	}()

	// Release the lock while waiting (the goroutine will re-acquire it)
	bs.promptMu.Unlock()

	select {
	case completed := <-done:
		bs.promptMu.Lock() // Re-acquire to satisfy defer
		return completed
	case <-timer.C:
		bs.promptMu.Lock() // Re-acquire to satisfy defer
		return false
	}
}

// GetEventCount returns the current event count for the session.
// Returns 0 if the recorder is not available or there's an error.
func (bs *BackgroundSession) GetEventCount() int {
	if bs.recorder == nil {
		return 0
	}
	return bs.recorder.EventCount()
}

// getNextSeq returns the next sequence number and increments the counter.
// This is used to assign sequence numbers to streaming events.
func (bs *BackgroundSession) getNextSeq() int64 {
	bs.seqMu.Lock()
	defer bs.seqMu.Unlock()
	seq := bs.nextSeq
	bs.nextSeq++

	// L1: Structured logging for seq assignment
	if bs.logger != nil {
		bs.logger.Debug("seq_assigned",
			"seq", seq,
			"next_seq", bs.nextSeq,
			"session_id", bs.persistedID)
	}

	return seq
}

// GetNextSeq implements the SeqProvider interface.
// It returns the next sequence number for event ordering.
func (bs *BackgroundSession) GetNextSeq() int64 {
	return bs.getNextSeq()
}

// refreshNextSeq updates nextSeq from the current event count.
// This should be called after events are persisted outside the normal buffer flow
// (e.g., after user prompts are persisted directly).
func (bs *BackgroundSession) refreshNextSeq() {
	if bs.recorder == nil {
		return
	}
	bs.seqMu.Lock()
	defer bs.seqMu.Unlock()
	oldSeq := bs.nextSeq
	bs.nextSeq = int64(bs.recorder.EventCount()) + 1

	// L1: Log seq refresh
	if bs.logger != nil && oldSeq != bs.nextSeq {
		bs.logger.Debug("seq_refreshed",
			"old_next_seq", oldSeq,
			"new_next_seq", bs.nextSeq,
			"event_count", bs.recorder.EventCount(),
			"session_id", bs.persistedID)
	}
}

// CreatedAt returns when the session was created.
func (bs *BackgroundSession) CreatedAt() time.Time {
	if bs.recorder != nil {
		// This would need to be stored; for now return zero
		return time.Time{}
	}
	return time.Time{}
}

// GetQueueConfig returns the queue configuration for this session.
// May return nil if no queue config is set (use defaults in that case).
func (bs *BackgroundSession) GetQueueConfig() *config.QueueConfig {
	return bs.queueConfig
}

// --- Observer Management ---

// AddObserver adds an observer to receive session events.
// Multiple observers can be added to the same session.
// If the session is currently streaming, the observer will receive all buffered
// events (thoughts, messages, tool calls, file operations) in the order they occurred.
// If there are cached action buttons, they will be sent to the new observer.
func (bs *BackgroundSession) AddObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	if bs.observers == nil {
		bs.observers = make(map[SessionObserver]struct{})
	}
	bs.observers[observer] = struct{}{}
	observerCount := len(bs.observers)
	bs.observersMu.Unlock()

	if bs.logger != nil {
		bs.logger.Debug("Observer added", "observer_count", observerCount)
	}

	// If the session is currently prompting, replay all buffered events to the new observer
	// so they can catch up with what's been streamed since the last persisted event.
	bs.promptMu.Lock()
	isPrompting := bs.isPrompting
	bs.promptMu.Unlock()

	if isPrompting {
		bs.replayBufferedEventsTo(observer)
	} else {
		// If not prompting, send cached action buttons to the new observer
		// This ensures new clients see the follow-up suggestions
		bs.sendCachedActionButtonsTo(observer)
	}
}

// GetBufferedEvents returns a copy of all buffered events.
// This is used by SessionWSClient to replay events with deduplication.
func (bs *BackgroundSession) GetBufferedEvents() []BufferedEvent {
	bs.bufferMu.Lock()
	defer bs.bufferMu.Unlock()
	if bs.eventBuffer == nil {
		return nil
	}
	return bs.eventBuffer.Events() // Returns a copy
}

// GetMaxBufferedSeq returns the highest sequence number in the event buffer.
// Returns 0 if the buffer is empty or not initialized.
// This is used by keepalive to report the server's current max seq.
func (bs *BackgroundSession) GetMaxBufferedSeq() int64 {
	bs.bufferMu.Lock()
	defer bs.bufferMu.Unlock()
	if bs.eventBuffer == nil {
		return 0
	}
	// Find the maximum seq in all buffered events
	// (usually the last one, but we check all to be safe)
	var maxSeq int64
	for _, event := range bs.eventBuffer.Events() {
		if event.Seq > maxSeq {
			maxSeq = event.Seq
		}
	}
	return maxSeq
}

// replayBufferedEventsTo sends all buffered events to a single observer in order.
// This is used to catch up newly connected observers on in-progress streaming.
// DEPRECATED: Use GetBufferedEvents() with client-side deduplication instead.
func (bs *BackgroundSession) replayBufferedEventsTo(observer SessionObserver) {
	bs.bufferMu.Lock()
	var events []BufferedEvent
	if bs.eventBuffer != nil {
		events = bs.eventBuffer.Events() // Get a copy, don't flush
	}
	bs.bufferMu.Unlock()

	if len(events) == 0 {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Replaying buffered events to new observer", "event_count", len(events))
	}

	for _, event := range events {
		event.ReplayTo(observer)
	}
}

// sendCachedActionButtonsTo sends cached action buttons to a single observer.
// Called when a new client connects to ensure they see the current suggestions,
// even if they connected after the suggestions were originally generated.
// This solves the problem of users switching devices or refreshing and missing suggestions.
func (bs *BackgroundSession) sendCachedActionButtonsTo(observer SessionObserver) {
	buttons := bs.GetActionButtons()
	if len(buttons) == 0 {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Sending cached action buttons to new observer", "button_count", len(buttons))
	}

	observer.OnActionButtons(buttons)
}

// RemoveObserver removes an observer from the session.
func (bs *BackgroundSession) RemoveObserver(observer SessionObserver) {
	bs.observersMu.Lock()
	defer bs.observersMu.Unlock()
	delete(bs.observers, observer)
	if bs.logger != nil {
		bs.logger.Debug("Observer removed", "observer_count", len(bs.observers))
	}
}

// ObserverCount returns the number of attached observers.
func (bs *BackgroundSession) ObserverCount() int {
	bs.observersMu.RLock()
	defer bs.observersMu.RUnlock()
	return len(bs.observers)
}

// HasObservers returns true if any observers are attached.
func (bs *BackgroundSession) HasObservers() bool {
	return bs.ObserverCount() > 0
}

// notifyObservers calls a function on all observers.
func (bs *BackgroundSession) notifyObservers(fn func(SessionObserver)) {
	bs.observersMu.RLock()
	defer bs.observersMu.RUnlock()
	observerCount := len(bs.observers)
	if observerCount > 1 && bs.logger != nil {
		bs.logger.Debug("Notifying multiple observers", "count", observerCount)
	}
	for observer := range bs.observers {
		fn(observer)
	}
}

// Close shuts down the session and releases all resources.
func (bs *BackgroundSession) Close(reason string) {
	if !bs.closed.CompareAndSwap(0, 1) {
		return // Already closed
	}

	// Notify all observers that the ACP connection is being stopped.
	// This must happen BEFORE we cancel the context or close resources,
	// so observers can update their state and prevent further prompts.
	// This fixes a race condition where a prompt could be sent while
	// the session is being archived.
	bs.notifyObservers(func(o SessionObserver) {
		o.OnACPStopped(reason)
	})

	// Cancel context to stop any ongoing operations
	bs.cancel()

	// Stop periodic persistence timer (H3 fix)
	bs.stopPeriodicPersistence()

	// Flush and persist any buffered messages
	bs.flushAndPersistMessages()

	// Close ACP client
	if bs.acpClient != nil {
		bs.acpClient.Close()
	}

	// Kill ACP process and clean up resources
	bs.killACPProcess()

	// End recording
	if bs.recorder != nil {
		bs.recorder.End(reason)
	}
}

// Suspend suspends the session without ending it (for temporary disconnects).
func (bs *BackgroundSession) Suspend() {
	// Flush and persist any buffered messages
	bs.flushAndPersistMessages()

	// Suspend recording (keeps status as "active")
	if bs.recorder != nil {
		bs.recorder.Suspend()
	}
}

// buildPromptWithHistory prepends conversation history to the user's message.
// This is used when resuming a session to give the ACP agent context about
// the previous conversation.
func (bs *BackgroundSession) buildPromptWithHistory(message string) string {
	if bs.store == nil {
		return message
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		if bs.logger != nil {
			bs.logger.Warn("Failed to read events for history injection", "error", err)
		}
		return message
	}

	// Build conversation history (limit to last 5 turns to avoid token limits)
	history := session.BuildConversationHistory(events, 5)
	if history == "" {
		return message
	}

	if bs.logger != nil {
		bs.logger.Info("Injecting conversation history into resumed session",
			"history_length", len(history))
	}

	return history + message
}

// flushAndPersistMessages persists all buffered events in streaming order.
// Events are sorted by their streaming sequence number before persistence,
// ensuring correct interleaving even when events arrive out of order due to
// buffering (e.g., MarkdownBuffer holding agent messages while tool calls arrive).
func (bs *BackgroundSession) flushAndPersistMessages() {
	if bs.recorder == nil {
		return
	}

	// Lock buffer access and flush all events
	bs.bufferMu.Lock()
	var events []BufferedEvent
	if bs.eventBuffer != nil {
		events = bs.eventBuffer.Flush()
	}
	bs.bufferMu.Unlock()

	// Sort events by streaming sequence number before persistence.
	// This is critical because events may arrive out of order in the buffer:
	// - Agent message chunks are buffered in MarkdownBuffer until complete
	// - Tool calls are added to EventBuffer immediately when SafeFlush fails
	// - This can cause tool calls to appear before the agent message explaining them
	// Sorting ensures events are persisted (and later replayed) in the correct order.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Seq < events[j].Seq
	})

	// Persist each event in order
	for _, event := range events {
		if err := event.PersistTo(bs.recorder); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist event", "type", event.Type, "error", err)
		}
	}
}

// flushPendingCoalescedEvents flushes and persists any pending coalesced events
// (agent messages and thoughts) from the buffer. This is called before persisting
// discrete events (tool calls, file ops) to ensure correct ordering.
//
// This implements the "flush-on-type-change" pattern: when a discrete event arrives,
// we first persist any pending coalesced content, then persist the discrete event.
// This makes storage the single source of truth for all events.
func (bs *BackgroundSession) flushPendingCoalescedEvents() {
	if bs.recorder == nil {
		return
	}

	bs.bufferMu.Lock()
	var events []BufferedEvent
	if bs.eventBuffer != nil {
		events = bs.eventBuffer.Flush()
	}
	bs.bufferMu.Unlock()

	if len(events) == 0 {
		return
	}

	// Sort by seq to ensure correct order
	sort.Slice(events, func(i, j int) bool {
		return events[i].Seq < events[j].Seq
	})

	// Persist each event
	for _, event := range events {
		if err := event.PersistTo(bs.recorder); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist coalesced event", "type", event.Type, "error", err)
		}
	}
}

// persistDiscreteEvent persists a discrete event (tool call, file op, etc.) immediately.
// This first flushes any pending coalesced events to ensure correct ordering.
func (bs *BackgroundSession) persistDiscreteEvent(event BufferedEvent) {
	if bs.recorder == nil {
		return
	}

	// First flush any pending coalesced events (agent messages, thoughts)
	bs.flushPendingCoalescedEvents()

	// Then persist this discrete event
	if err := event.PersistTo(bs.recorder); err != nil && bs.logger != nil {
		bs.logger.Error("Failed to persist discrete event", "type", event.Type, "error", err)
	}
}

// startPeriodicPersistence starts a timer that periodically persists buffered
// agent messages during streaming. This reduces data loss if the server crashes
// mid-stream (H3 fix).
func (bs *BackgroundSession) startPeriodicPersistence() {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()

	// Stop any existing timer
	if bs.persistTimer != nil {
		bs.persistTimer.Stop()
	}

	if bs.persistInterval <= 0 {
		return
	}

	// Create a new timer that fires periodically
	bs.persistTimer = time.AfterFunc(bs.persistInterval, func() {
		bs.periodicPersistTick()
	})
}

// stopPeriodicPersistence stops the periodic persistence timer.
func (bs *BackgroundSession) stopPeriodicPersistence() {
	bs.promptMu.Lock()
	defer bs.promptMu.Unlock()

	if bs.persistTimer != nil {
		bs.persistTimer.Stop()
		bs.persistTimer = nil
	}
}

// periodicPersistTick is called by the periodic persistence timer.
// It persists any buffered agent messages and reschedules the timer.
func (bs *BackgroundSession) periodicPersistTick() {
	// Check if we're still prompting
	bs.promptMu.Lock()
	if !bs.isPrompting {
		bs.promptMu.Unlock()
		return
	}
	bs.promptMu.Unlock()

	// Persist any buffered coalesced events
	bs.flushPendingCoalescedEvents()

	if bs.logger != nil {
		bs.logger.Debug("Periodic persistence tick completed")
	}

	// Reschedule the timer
	bs.promptMu.Lock()
	if bs.isPrompting && bs.persistInterval > 0 {
		bs.persistTimer = time.AfterFunc(bs.persistInterval, func() {
			bs.periodicPersistTick()
		})
	}
	bs.promptMu.Unlock()
}

// killACPProcess terminates the ACP process and cleans up resources.
// It handles both direct execution (acpCmd) and runner-based execution.
func (bs *BackgroundSession) killACPProcess() {
	// Kill direct process if using one
	if bs.acpCmd != nil && bs.acpCmd.Process != nil {
		bs.acpCmd.Process.Kill()
	}

	// Call wait() to clean up resources (from runner.RunWithPipes or cmd.Wait)
	// This is safe to call even if the process is already dead
	if bs.acpWait != nil {
		bs.acpWait()
		bs.acpWait = nil // Prevent double cleanup
	}
}

// maxACPStartRetries is the maximum number of times to retry starting the ACP process
// if the initial connection fails (e.g., "peer disconnected before response").
const maxACPStartRetries = 2

// acpStartRetryDelay is the delay between ACP start retries.
const acpStartRetryDelay = 500 * time.Millisecond

// startACPProcess starts the ACP server process and initializes the connection.
// If acpSessionID is provided and the agent supports session loading, it attempts
// to resume that session. Otherwise, it creates a new session.
// This method includes retry logic for transient failures during startup.
func (bs *BackgroundSession) startACPProcess(acpCommand, workingDir, acpSessionID string) error {
	var lastErr error
	for attempt := 0; attempt < maxACPStartRetries; attempt++ {
		if attempt > 0 {
			if bs.logger != nil {
				bs.logger.Info("Retrying ACP process start",
					"attempt", attempt+1,
					"max_attempts", maxACPStartRetries,
					"last_error", lastErr)
			}
			// Wait before retry
			select {
			case <-bs.ctx.Done():
				return &sessionError{"context cancelled during retry: " + bs.ctx.Err().Error()}
			case <-time.After(acpStartRetryDelay):
			}
		}

		err := bs.doStartACPProcess(acpCommand, workingDir, acpSessionID)
		if err == nil {
			return nil
		}
		lastErr = err

		// Only retry on connection/initialization failures, not on validation errors
		if strings.Contains(err.Error(), "empty ACP command") {
			return err // Don't retry validation errors
		}

		if bs.logger != nil {
			bs.logger.Warn("ACP process start failed",
				"attempt", attempt+1,
				"error", err)
		}
	}

	return lastErr
}

// doStartACPProcess performs a single attempt to start the ACP process.
// stderrCollector collects stderr output from the ACP process for error reporting.
// It stores the last N bytes of stderr output that can be retrieved when errors occur.
type stderrCollector struct {
	mu       sync.Mutex
	buffer   []byte
	maxSize  int
	logger   *slog.Logger
	isClosed bool
}

// newStderrCollector creates a new stderr collector with the given max buffer size.
func newStderrCollector(maxSize int, logger *slog.Logger) *stderrCollector {
	return &stderrCollector{
		buffer:  make([]byte, 0, maxSize),
		maxSize: maxSize,
		logger:  logger,
	}
}

// Write implements io.Writer to collect stderr output.
func (c *stderrCollector) Write(p []byte) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed {
		return len(p), nil
	}

	// Log at debug level as it comes in
	if c.logger != nil && len(p) > 0 {
		c.logger.Debug("agent stderr", "output", string(p))
	}

	// Append to buffer, keeping only the last maxSize bytes
	c.buffer = append(c.buffer, p...)
	if len(c.buffer) > c.maxSize {
		c.buffer = c.buffer[len(c.buffer)-c.maxSize:]
	}

	return len(p), nil
}

// GetOutput returns the collected stderr output.
func (c *stderrCollector) GetOutput() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.buffer)
}

// Close marks the collector as closed and logs any remaining output at warn level if non-empty.
func (c *stderrCollector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.isClosed = true
}

// startStderrMonitor starts a goroutine that reads from stderr and writes to the collector.
func startStderrMonitor(stderr runner.ReadCloser, collector *stderrCollector) {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				collector.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		collector.Close()
	}()
}

func (bs *BackgroundSession) doStartACPProcess(acpCommand, workingDir, acpSessionID string) error {
	args := strings.Fields(acpCommand)
	if len(args) == 0 {
		return &sessionError{"empty ACP command"}
	}

	var stdin runner.WriteCloser
	var stdout runner.ReadCloser
	var stderr runner.ReadCloser
	var wait func() error
	var cmd *exec.Cmd
	var err error

	// Create stderr collector to capture output for error reporting
	// Keep last 8KB of stderr output
	stderrCollector := newStderrCollector(8192, bs.logger)

	// Use runner if configured, otherwise direct execution
	if bs.runner != nil {
		// Use restricted runner with RunWithPipes
		if bs.logger != nil {
			bs.logger.Info("starting ACP process through restricted runner",
				"runner_type", bs.runner.Type(),
				"command", acpCommand)
		}
		stdin, stdout, stderr, wait, err = bs.runner.RunWithPipes(bs.ctx, args[0], args[1:], nil)
		if err != nil {
			return &sessionError{"failed to start with runner: " + err.Error()}
		}

		// Monitor stderr in background
		startStderrMonitor(stderr, stderrCollector)

		// Store wait function for cleanup
		// We'll call it in Close() method
		bs.acpCmd = nil // No cmd when using runner
	} else {
		// Direct execution (no restrictions)
		cmd = exec.CommandContext(bs.ctx, args[0], args[1:]...)

		stdin, err = cmd.StdinPipe()
		if err != nil {
			return &sessionError{"failed to create stdin pipe: " + err.Error()}
		}
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			return &sessionError{"failed to create stdout pipe: " + err.Error()}
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return &sessionError{"failed to create stderr pipe: " + err.Error()}
		}

		if err := cmd.Start(); err != nil {
			return &sessionError{"failed to start ACP server: " + err.Error()}
		}

		// Monitor stderr in background (same as runner case)
		startStderrMonitor(stderrPipe, stderrCollector)

		bs.acpCmd = cmd

		// Create wait function for direct execution
		wait = func() error {
			return cmd.Wait()
		}
	}

	// Store wait function for cleanup
	bs.acpWait = wait

	// Create web client with callbacks that route to attached client or persist.
	// BackgroundSession implements SeqProvider, so seq is assigned at ACP receive time.
	webClientConfig := WebClientConfig{
		AutoApprove:          bs.autoApprove,
		SeqProvider:          bs, // BackgroundSession implements SeqProvider
		OnAgentMessage:       bs.onAgentMessage,
		OnAgentThought:       bs.onAgentThought,
		OnToolCall:           bs.onToolCall,
		OnToolUpdate:         bs.onToolUpdate,
		OnPlan:               bs.onPlan,
		OnFileWrite:          bs.onFileWrite,
		OnFileRead:           bs.onFileRead,
		OnPermission:         bs.onPermission,
		OnAvailableCommands:  bs.onAvailableCommands,
		OnCurrentModeChanged: bs.onCurrentModeChanged,
	}

	// Configure file linking if enabled
	// Web UI always uses HTTP links (file:// URLs are blocked by browsers for security)
	// Note: IsEnabled() and IsAllowOutsideWorkspace() are safe to call on nil receivers
	// and return sensible defaults (enabled=true, allowOutsideWorkspace=false)
	if bs.fileLinksConfig.IsEnabled() {
		webClientConfig.FileLinksConfig = &conversion.FileLinkerConfig{
			WorkingDir:            bs.workingDir,
			WorkspaceUUID:         bs.workspaceUUID, // Use UUID instead of path for security
			Enabled:               true,
			AllowOutsideWorkspace: bs.fileLinksConfig.IsAllowOutsideWorkspace(),
			UseHTTPLinks:          true,         // Web UI requires HTTP links
			APIPrefix:             bs.apiPrefix, // URL prefix for file server endpoint
		}
	}

	bs.acpClient = NewWebClient(webClientConfig)

	// Wrap stdout with a JSON line filter to discard non-JSON output
	// (e.g., ANSI escape sequences, terminal UI from crashed agents)
	filteredStdout := mittoAcp.NewJSONLineFilterReader(stdout, bs.logger)

	// Create ACP connection with filtered stdout
	bs.acpConn = acp.NewClientSideConnection(bs.acpClient, stdin, filteredStdout)
	if bs.logger != nil {
		bs.acpConn.SetLogger(bs.logger)
	}

	// Initialize and get agent capabilities
	initResp, err := bs.acpConn.Initialize(bs.ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
		},
	})
	if err != nil {
		// Give stderr goroutine a moment to capture any error output
		time.Sleep(100 * time.Millisecond)

		// Log the failure with command and stderr output
		stderrOutput := strings.TrimSpace(stderrCollector.GetOutput())
		if bs.logger != nil {
			logAttrs := []any{
				"command", acpCommand,
				"working_dir", workingDir,
				"error", err,
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			bs.logger.Warn("ACP process initialization failed", logAttrs...)
		}

		bs.killACPProcess()
		return &sessionError{"failed to initialize: " + err.Error()}
	}

	// Log agent information at DEBUG level
	bs.logAgentInfo(initResp)

	cwd := workingDir
	if cwd == "" {
		cwd = "."
	}

	// Try to load existing session if we have an ACP session ID and the agent supports it
	if acpSessionID != "" && initResp.AgentCapabilities.LoadSession {
		loadResp, err := bs.acpConn.LoadSession(bs.ctx, acp.LoadSessionRequest{
			SessionId:  acp.SessionId(acpSessionID),
			Cwd:        cwd,
			McpServers: []acp.McpServer{},
		})
		if err == nil {
			bs.acpID = acpSessionID
			if bs.logger != nil {
				bs.logger.Info("Resumed ACP session",
					"acp_session_id", acpSessionID)
				bs.logSessionModes(loadResp.Modes)
			}
			return nil
		}
		// Log the error but fall through to create a new session
		if bs.logger != nil {
			bs.logger.Warn("Failed to load ACP session, creating new session",
				"acp_session_id", acpSessionID,
				"error", err)
		}
	}

	// Create new session
	sessResp, err := bs.acpConn.NewSession(bs.ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		// Give stderr goroutine a moment to capture any error output
		time.Sleep(100 * time.Millisecond)

		// Log the failure with command and stderr output
		stderrOutput := strings.TrimSpace(stderrCollector.GetOutput())
		if bs.logger != nil {
			logAttrs := []any{
				"command", acpCommand,
				"working_dir", workingDir,
				"error", err,
			}
			if stderrOutput != "" {
				logAttrs = append(logAttrs, "stderr", stderrOutput)
			}
			bs.logger.Warn("ACP session creation failed", logAttrs...)
		}

		bs.killACPProcess()
		return &sessionError{"failed to create session: " + err.Error()}
	}

	bs.acpID = string(sessResp.SessionId)

	if bs.logger != nil {
		bs.logger.Info("Created new ACP session",
			"acp_session_id", bs.acpID)
		bs.logSessionModes(sessResp.Modes)
	}

	return nil
}

// logSessionModes logs the session modes/config options at DEBUG level.
// This helps with debugging which modes are available from the ACP server.
func (bs *BackgroundSession) logSessionModes(modes *acp.SessionModeState) {
	if bs.logger == nil || modes == nil {
		return
	}

	// Log current mode
	bs.logger.Debug("Session mode state",
		"current_mode", modes.CurrentModeId,
		"available_modes_count", len(modes.AvailableModes))

	// Log each available mode
	for _, mode := range modes.AvailableModes {
		desc := ""
		if mode.Description != nil {
			desc = *mode.Description
		}
		bs.logger.Debug("Available session mode",
			"mode_id", mode.Id,
			"mode_name", mode.Name,
			"mode_description", desc)
	}
}

// logAgentInfo logs the agent information and capabilities from the Initialize response at DEBUG level.
// This helps with debugging which agent is being used and what features it supports.
func (bs *BackgroundSession) logAgentInfo(resp acp.InitializeResponse) {
	if bs.logger == nil {
		return
	}

	// Log agent info if available
	if resp.AgentInfo != nil {
		bs.logger.Debug("Agent info",
			"agent_name", resp.AgentInfo.Name,
			"agent_version", resp.AgentInfo.Version)
	}

	// Log protocol version
	bs.logger.Debug("ACP protocol version",
		"protocol_version", resp.ProtocolVersion)

	// Log agent capabilities
	caps := resp.AgentCapabilities
	bs.logger.Debug("Agent capabilities",
		"load_session", caps.LoadSession,
		"mcp_http", caps.McpCapabilities.Http,
		"mcp_sse", caps.McpCapabilities.Sse,
		"prompt_audio", caps.PromptCapabilities.Audio,
		"prompt_embedded_context", caps.PromptCapabilities.EmbeddedContext,
		"prompt_image", caps.PromptCapabilities.Image)

	// Log authentication methods if available
	if len(resp.AuthMethods) > 0 {
		authMethods := make([]string, len(resp.AuthMethods))
		for i, auth := range resp.AuthMethods {
			authMethods[i] = auth.Name
		}
		bs.logger.Debug("Agent auth methods",
			"count", len(resp.AuthMethods),
			"methods", authMethods)
	}
}

// sessionError is a simple error type for session errors.
type sessionError struct {
	msg string
}

func (e *sessionError) Error() string {
	return e.msg
}

// NeedsTitle returns true if the session has no title yet and needs auto-title generation.
// Returns false if the session already has a title (either auto-generated or user-set).
func (bs *BackgroundSession) NeedsTitle() bool {
	if bs.store == nil || bs.persistedID == "" {
		return false
	}
	meta, err := bs.store.GetMetadata(bs.persistedID)
	if err != nil {
		return false
	}
	return meta.Name == ""
}

// PromptMeta contains optional metadata about the prompt source.
type PromptMeta struct {
	SenderID string   // Unique identifier of the sending client (for broadcast deduplication)
	PromptID string   // Client-generated prompt ID (for delivery confirmation)
	ImageIDs []string // IDs of images attached to the prompt
	FileIDs  []string // IDs of files attached to the prompt
}

// Prompt sends a message to the agent. This runs asynchronously.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) Prompt(message string) error {
	return bs.PromptWithMeta(message, PromptMeta{})
}

// PromptWithImages sends a message with optional images to the agent. This runs asynchronously.
// The imageIDs should be IDs of images previously uploaded to this session.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithImages(message string, imageIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs})
}

// PromptWithAttachments sends a message with optional images and files to the agent.
// This runs asynchronously. The IDs should be of previously uploaded images/files.
func (bs *BackgroundSession) PromptWithAttachments(message string, imageIDs, fileIDs []string) error {
	return bs.PromptWithMeta(message, PromptMeta{ImageIDs: imageIDs, FileIDs: fileIDs})
}

// PromptWithMeta sends a message with optional metadata to the agent. This runs asynchronously.
// The meta parameter contains sender information for multi-client broadcast.
// The response is streamed via callbacks to the attached client (if any) and persisted.
func (bs *BackgroundSession) PromptWithMeta(message string, meta PromptMeta) error {
	imageIDs := meta.ImageIDs
	fileIDs := meta.FileIDs
	if bs.IsClosed() {
		return &sessionError{"session is closed"}
	}
	if bs.acpConn == nil {
		return &sessionError{"no ACP connection"}
	}

	bs.promptMu.Lock()
	if bs.isPrompting {
		// Check if the current prompt is stuck (running for too long without activity)
		// If it's been more than 5 minutes, auto-recover by resetting the state
		const stuckPromptTimeout = 5 * time.Minute
		if !bs.promptStartTime.IsZero() && time.Since(bs.promptStartTime) > stuckPromptTimeout {
			if bs.logger != nil {
				bs.logger.Warn("Auto-recovering stuck prompt",
					"prompt_start_time", bs.promptStartTime,
					"elapsed", time.Since(bs.promptStartTime))
			}
			bs.isPrompting = false
			bs.lastResponseComplete = time.Now()
			// Fall through to process the new prompt
		} else {
			bs.promptMu.Unlock()
			return &sessionError{"prompt already in progress"}
		}
	}
	bs.isPrompting = true
	bs.promptStartTime = time.Now()
	bs.promptCount++

	// Check if we need to inject conversation history (first prompt of resumed session)
	shouldInjectHistory := bs.isResumed && !bs.historyInjected
	if shouldInjectHistory {
		bs.historyInjected = true
	}

	// Capture first prompt state for message processors
	isFirst := bs.isFirstPrompt
	if isFirst {
		bs.isFirstPrompt = false
	}
	bs.promptMu.Unlock()

	// Notify about streaming state change (prompt started)
	if bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, true)
	}

	// Start periodic persistence for crash recovery (H3 fix)
	bs.startPeriodicPersistence()

	// Load images and build content blocks
	var imageRefs []session.ImageRef
	var contentBlocks []acp.ContentBlock

	if len(imageIDs) > 0 && bs.store != nil {
		for _, imageID := range imageIDs {
			imagePath, err := bs.store.GetImagePath(bs.persistedID, imageID)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to get image path", "image_id", imageID, "error", err)
				}
				continue
			}

			// Determine MIME type from extension
			ext := ""
			if idx := strings.LastIndex(imageID, "."); idx >= 0 {
				ext = imageID[idx:]
			}
			mimeType := session.GetMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "image/png" // Default fallback
			}

			// Load image and create attachment
			att, err := mittoAcp.ImageAttachmentFromFile(imagePath, mimeType)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to load image", "image_id", imageID, "error", err)
				}
				continue
			}

			contentBlocks = append(contentBlocks, att.ToContentBlock())
			imageRefs = append(imageRefs, session.ImageRef{
				ID:       imageID,
				MimeType: mimeType,
			})
		}
	}

	// Load files and build content blocks
	var fileRefs []session.FileRef
	if len(fileIDs) > 0 && bs.store != nil {
		for _, fileID := range fileIDs {
			filePath, err := bs.store.GetFilePath(bs.persistedID, fileID)
			if err != nil {
				if bs.logger != nil {
					bs.logger.Warn("Failed to get file path", "file_id", fileID, "error", err)
				}
				continue
			}

			// Determine MIME type from extension
			ext := ""
			if idx := strings.LastIndex(fileID, "."); idx >= 0 {
				ext = fileID[idx:]
			}
			mimeType := session.GetFileMimeTypeFromExt(ext)
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			// Determine file category and create appropriate attachment
			category := session.GetFileCategory(mimeType)
			var att mittoAcp.Attachment
			if category == session.FileCategoryText {
				// Text files are embedded inline
				att, err = mittoAcp.TextFileAttachmentFromFile(filePath, mimeType)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Warn("Failed to load text file", "file_id", fileID, "error", err)
					}
					continue
				}
			} else {
				// Binary files are referenced by path
				att = mittoAcp.BinaryFileAttachment(filePath, mimeType)
			}

			contentBlocks = append(contentBlocks, att.ToContentBlock())
			fileRefs = append(fileRefs, session.FileRef{
				ID:       fileID,
				Name:     att.Name,
				MimeType: mimeType,
				Category: category,
			})
		}
	}

	// Clear action buttons when new activity starts
	// This ensures suggestions are tied to the latest agent response
	bs.clearActionButtons()

	// Clear cached plan state when new prompt starts
	// The existing plan becomes stale; a new plan will be generated for this prompt
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, nil)
	}

	// Persist user prompt with image/file references and prompt ID
	// User prompts are persisted immediately (not buffered), so we need to
	// refresh nextSeq after persistence to get the correct seq for the prompt
	// The prompt ID is included so clients can clear pending prompts on reconnect
	var userPromptSeq int64
	if bs.recorder != nil {
		if err := bs.recorder.RecordUserPromptComplete(message, imageRefs, fileRefs, meta.PromptID); err != nil && bs.logger != nil {
			bs.logger.Error("Failed to persist user prompt", "error", err)
		}
		// Get the seq that was assigned to the user prompt (it's the current event count)
		userPromptSeq = int64(bs.recorder.EventCount())
		// Update nextSeq for subsequent agent events
		bs.refreshNextSeq()
	}

	// Notify all observers about the user prompt (for multi-client sync)
	// This includes the message text so other connected clients can display it
	fileIDStrings := make([]string, len(fileRefs))
	for i, f := range fileRefs {
		fileIDStrings[i] = f.ID
	}
	bs.notifyObservers(func(o SessionObserver) {
		o.OnUserPrompt(userPromptSeq, meta.SenderID, meta.PromptID, message, imageIDs, fileIDStrings)
	})

	// Build the actual prompt to send to ACP
	// Apply message processors (prepend/append based on config)
	promptMessage := config.ApplyProcessors(message, bs.processors, isFirst)

	// Apply external command hooks
	var hookAttachmentBlocks []acp.ContentBlock
	if bs.hookManager != nil && len(bs.hookManager.Hooks()) > 0 {
		hookInput := &msghooks.HookInput{
			Message:        promptMessage,
			IsFirstMessage: isFirst,
			SessionID:      bs.persistedID,
			WorkingDir:     bs.workingDir,
		}
		hookResult, hookErr := bs.hookManager.Apply(bs.ctx, hookInput)
		if hookErr != nil {
			if bs.logger != nil {
				bs.logger.Error("Hook execution failed", "error", hookErr)
			}
			// Continue with original message on hook failure
		} else if hookResult != nil {
			promptMessage = hookResult.Message

			// Convert hook attachments to content blocks
			if len(hookResult.Attachments) > 0 {
				acpAttachments, err := hookResult.ToACPAttachments(bs.workingDir)
				if err != nil {
					if bs.logger != nil {
						bs.logger.Error("Failed to resolve hook attachments", "error", err)
					}
				} else {
					for _, att := range acpAttachments {
						if att.Type == "image" {
							hookAttachmentBlocks = append(hookAttachmentBlocks, acp.ImageBlock(att.Data, att.MimeType))
						}
						// Note: Non-image attachments could be handled differently in the future
					}
				}
			}
		}
	}

	if shouldInjectHistory {
		promptMessage = bs.buildPromptWithHistory(promptMessage)
	}

	// Build final content blocks: images first (from uploads and hooks), then text
	finalBlocks := make([]acp.ContentBlock, 0, len(contentBlocks)+len(hookAttachmentBlocks)+1)
	finalBlocks = append(finalBlocks, contentBlocks...)
	finalBlocks = append(finalBlocks, hookAttachmentBlocks...)
	finalBlocks = append(finalBlocks, acp.TextBlock(promptMessage))

	// Run prompt in background
	go func() {
		promptResp, err := bs.acpConn.Prompt(bs.ctx, acp.PromptRequest{
			SessionId: acp.SessionId(bs.acpID),
			Prompt:    finalBlocks,
		})

		// Mark prompt as complete BEFORE any further processing
		// This must happen before processNextQueuedMessage so the next message can be sent
		bs.promptMu.Lock()
		bs.isPrompting = false
		bs.promptStartTime = time.Time{}
		bs.lastResponseComplete = time.Now()
		bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
		bs.promptMu.Unlock()

		// Notify about streaming state change (prompt completed)
		if bs.onStreamingStateChanged != nil {
			bs.onStreamingStateChanged(bs.persistedID, false)
		}

		// Stop periodic persistence (H3 fix)
		bs.stopPeriodicPersistence()

		if bs.IsClosed() {
			return
		}

		// DEBUG: Log prompt completion sequence
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_sequence_start",
				"session_id", bs.persistedID,
				"observer_count", bs.ObserverCount(),
				"is_prompting", bs.IsPrompting())
		}

		// Flush markdown buffer
		if bs.acpClient != nil {
			if bs.logger != nil {
				bs.logger.Debug("prompt_completion_flush_markdown_start",
					"session_id", bs.persistedID)
			}
			bs.acpClient.FlushMarkdown()
			if bs.logger != nil {
				bs.logger.Debug("prompt_completion_flush_markdown_done",
					"session_id", bs.persistedID)
			}
		}

		// Get the agent message for async analysis (before flushing)
		var agentMessage string
		isEndTurn := err == nil && promptResp.StopReason == acp.StopReasonEndTurn
		if bs.actionButtonsConfig.IsEnabled() && isEndTurn {
			bs.bufferMu.Lock()
			if bs.eventBuffer != nil {
				agentMessage = bs.eventBuffer.GetAgentMessage()
			}
			bs.bufferMu.Unlock()
		}

		// Persist buffered messages
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_persist_start",
				"session_id", bs.persistedID)
		}
		bs.flushAndPersistMessages()
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_persist_done",
				"session_id", bs.persistedID)
		}

		// Notify all observers
		eventCount := bs.GetEventCount()
		observerCount := bs.ObserverCount()
		if bs.logger != nil {
			bs.logger.Debug("prompt_completion_notify_start",
				"session_id", bs.persistedID,
				"event_count", eventCount,
				"observer_count", observerCount)
		}

		if err != nil {
			if bs.logger != nil {
				bs.logger.Error("prompt_failed",
					"session_id", bs.persistedID,
					"error", err.Error(),
					"observer_count", observerCount)
			}
			bs.notifyObservers(func(o SessionObserver) {
				o.OnError("Prompt failed: " + err.Error())
			})
		} else {
			if bs.logger != nil {
				bs.logger.Debug("prompt_complete",
					"session_id", bs.persistedID,
					"event_count", eventCount,
					"observer_count", observerCount,
					"stop_reason", promptResp.StopReason)
			}
			bs.notifyObservers(func(o SessionObserver) {
				o.OnPromptComplete(eventCount)
			})

			// Process next queued message if queue processing is enabled
			bs.processNextQueuedMessage()

			// Async follow-up analysis (non-blocking)
			// This runs after prompt_complete so the user sees the response immediately
			// Note: 'message' is captured from the outer function scope (the user's prompt)
			if agentMessage != "" {
				// Skip follow-up analysis if there are queued messages that will be processed immediately
				// (no delay configured). The suggestions would be stale by the time they arrive.
				if bs.hasImmediateQueuedMessages() {
					bs.logger.Debug("follow-up analysis: skipped due to pending immediate queue messages")
				} else {
					go bs.analyzeFollowUpQuestions(message, agentMessage)
				}
			}
		}
	}()

	return nil
}

// analyzeFollowUpQuestions asynchronously analyzes an agent message for follow-up questions.
// It uses the auxiliary conversation to identify questions and sends suggested responses
// to observers via OnActionButtons. This is non-blocking and runs in a goroutine.
// userPrompt provides context about what the user asked.
func (bs *BackgroundSession) analyzeFollowUpQuestions(userPrompt, agentMessage string) {
	// Create a timeout context for the analysis
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if session is still valid before starting
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis skipped: session closed")
		return
	}

	bs.logger.Debug("follow-up analysis: starting",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage))

	// Use the auxiliary conversation to analyze the message
	suggestions, err := auxiliary.AnalyzeFollowUpQuestions(ctx, userPrompt, agentMessage)
	if err != nil {
		bs.logger.Debug("follow-up analysis failed", "error", err)
		return
	}

	if len(suggestions) == 0 {
		bs.logger.Debug("follow-up analysis: no suggestions found")
		return
	}

	// Check again if session is still valid and not prompting
	// If the user has already sent a new message, don't show stale suggestions
	if bs.IsClosed() {
		bs.logger.Debug("follow-up analysis: session closed before sending buttons")
		return
	}
	if bs.IsPrompting() {
		bs.logger.Debug("follow-up analysis: session is prompting, discarding buttons")
		return
	}

	// Convert auxiliary suggestions to ActionButton format
	buttons := make([]ActionButton, 0, len(suggestions))
	for _, s := range suggestions {
		buttons = append(buttons, ActionButton{
			Label:    s.Label,
			Response: s.Value,
		})
	}

	// Cache in memory
	bs.actionButtonsMu.Lock()
	bs.cachedActionButtons = buttons
	bs.actionButtonsMu.Unlock()

	// Persist to disk
	if bs.store != nil && bs.persistedID != "" {
		abStore := bs.store.ActionButtons(bs.persistedID)
		// Convert to session.ActionButton for storage
		sessionButtons := make([]session.ActionButton, len(buttons))
		for i, b := range buttons {
			sessionButtons[i] = session.ActionButton{
				Label:    b.Label,
				Response: b.Response,
			}
		}
		eventCount := bs.GetEventCount()
		if err := abStore.Set(sessionButtons, int64(eventCount)); err != nil {
			bs.logger.Debug("failed to persist action buttons", "error", err)
		}
	}

	bs.logger.Info("follow-up analysis: sending buttons to observers", "count", len(buttons))
	bs.notifyObservers(func(o SessionObserver) {
		o.OnActionButtons(buttons)
	})
}

// TriggerFollowUpSuggestions triggers follow-up suggestions analysis for a resumed session.
// This reads the last agent message from stored events and analyzes it asynchronously.
// It only works for sessions with message history and when follow-up suggestions are enabled.
// If cached action buttons already exist, they are loaded and no new analysis is triggered.
// This is non-blocking and runs the analysis in a goroutine.
// Returns true if the analysis was triggered or cached buttons were loaded, false if skipped.
func (bs *BackgroundSession) TriggerFollowUpSuggestions() bool {
	// Check if follow-up suggestions are enabled
	if !bs.actionButtonsConfig.IsEnabled() {
		bs.logger.Debug("follow-up suggestions: disabled in config")
		return false
	}

	// Check if session is prompting (don't interfere with active prompts)
	if bs.IsPrompting() {
		bs.logger.Debug("follow-up suggestions: session is prompting, skipping")
		return false
	}

	// Check if session is closed
	if bs.IsClosed() {
		bs.logger.Debug("follow-up suggestions: session is closed, skipping")
		return false
	}

	// Need store to read events
	if bs.store == nil {
		bs.logger.Debug("follow-up suggestions: no store, skipping")
		return false
	}

	// Check if we already have cached action buttons (from disk)
	// If so, load them into memory cache - no need to re-analyze
	cachedButtons := bs.GetActionButtons()
	if len(cachedButtons) > 0 {
		bs.logger.Debug("follow-up suggestions: using cached buttons from disk",
			"button_count", len(cachedButtons))
		return true
	}

	// Read stored events for this session
	events, err := bs.store.ReadEvents(bs.persistedID)
	if err != nil {
		bs.logger.Debug("follow-up suggestions: failed to read events", "error", err)
		return false
	}

	// Get the last user prompt and agent message from stored events
	userPrompt := session.GetLastUserPrompt(events)
	agentMessage := session.GetLastAgentMessage(events)
	if agentMessage == "" {
		bs.logger.Debug("follow-up suggestions: no agent message found in history")
		return false
	}

	bs.logger.Debug("follow-up suggestions: triggering analysis for resumed session",
		"user_prompt_length", len(userPrompt),
		"agent_message_length", len(agentMessage))

	// Run analysis asynchronously
	go bs.analyzeFollowUpQuestions(userPrompt, agentMessage)
	return true
}

// clearActionButtons clears the cached action buttons from memory and disk.
// Called when new conversation activity occurs (user sends a prompt) because
// the existing suggestions become stale—they were generated for the previous
// agent response, not the upcoming one. New suggestions will be generated
// when the agent completes its next response.
func (bs *BackgroundSession) clearActionButtons() {
	// Clear in-memory cache
	bs.actionButtonsMu.Lock()
	hadButtons := len(bs.cachedActionButtons) > 0
	bs.cachedActionButtons = nil
	bs.actionButtonsMu.Unlock()

	// Clear from disk
	if bs.store != nil && bs.persistedID != "" {
		abStore := bs.store.ActionButtons(bs.persistedID)
		if err := abStore.Clear(); err != nil && bs.logger != nil {
			bs.logger.Debug("failed to clear action buttons from disk", "error", err)
		}
	}

	// Notify observers that buttons are cleared (send empty array)
	if hadButtons {
		bs.notifyObservers(func(o SessionObserver) {
			o.OnActionButtons([]ActionButton{})
		})
	}
}

// GetActionButtons returns the current action buttons.
// Uses a two-tier lookup: memory cache first (fast), then disk (persistent).
// The disk fallback ensures suggestions survive server restarts.
// Returns nil if no suggestions are available.
func (bs *BackgroundSession) GetActionButtons() []ActionButton {
	// Check in-memory cache first
	bs.actionButtonsMu.RLock()
	if bs.cachedActionButtons != nil {
		result := make([]ActionButton, len(bs.cachedActionButtons))
		copy(result, bs.cachedActionButtons)
		bs.actionButtonsMu.RUnlock()
		return result
	}
	bs.actionButtonsMu.RUnlock()

	// Fall back to disk
	if bs.store == nil || bs.persistedID == "" {
		return nil
	}

	abStore := bs.store.ActionButtons(bs.persistedID)
	buttons, err := abStore.Get()
	if err != nil {
		if bs.logger != nil {
			bs.logger.Debug("failed to read action buttons from disk", "error", err)
		}
		return nil
	}

	// Convert session.ActionButton to web.ActionButton
	result := make([]ActionButton, len(buttons))
	for i, b := range buttons {
		result[i] = ActionButton{
			Label:    b.Label,
			Response: b.Response,
		}
	}

	// Cache in memory for future access
	if len(result) > 0 {
		bs.actionButtonsMu.Lock()
		bs.cachedActionButtons = result
		bs.actionButtonsMu.Unlock()
	}

	return result
}

// Cancel cancels the current prompt and resets the prompting state.
// This sends a cancel notification to the ACP agent and resets the isPrompting flag
// so the session can accept new prompts even if the agent doesn't respond to the cancel.
func (bs *BackgroundSession) Cancel() error {
	// Reset prompting state regardless of whether cancel succeeds
	// This ensures the session can accept new prompts even if the agent is unresponsive
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	// Stop periodic persistence (H3 fix)
	bs.stopPeriodicPersistence()

	if wasPrompting {
		// Flush any buffered content before notifying completion
		if bs.acpClient != nil {
			bs.acpClient.FlushMarkdown()
		}
		bs.flushAndPersistMessages()

		// Notify observers that the prompt was cancelled
		eventCount := bs.GetEventCount()
		bs.notifyObservers(func(o SessionObserver) {
			o.OnPromptComplete(eventCount)
		})

		if bs.logger != nil {
			bs.logger.Info("Session cancelled, prompting state reset")
		}
	}

	// Send cancel notification to ACP agent (best effort)
	if bs.acpConn == nil {
		return nil
	}
	return bs.acpConn.Cancel(bs.ctx, acp.CancelNotification{
		SessionId: acp.SessionId(bs.acpID),
	})
}

// ForceReset forcefully resets the session's prompting state.
// This is used when the agent is completely unresponsive and Cancel doesn't work.
// It resets the isPrompting flag, flushes any buffered content, and notifies observers.
// Unlike Cancel, this does NOT send a cancel notification to the agent.
func (bs *BackgroundSession) ForceReset() {
	bs.promptMu.Lock()
	wasPrompting := bs.isPrompting
	bs.isPrompting = false
	bs.promptStartTime = time.Time{}
	bs.lastResponseComplete = time.Now()
	bs.promptCond.Broadcast() // Signal any waiters that prompt is complete
	bs.promptMu.Unlock()

	// Notify about streaming state change if we were prompting
	if wasPrompting && bs.onStreamingStateChanged != nil {
		bs.onStreamingStateChanged(bs.persistedID, false)
	}

	// Stop periodic persistence (H3 fix)
	bs.stopPeriodicPersistence()

	if !wasPrompting {
		if bs.logger != nil {
			bs.logger.Debug("ForceReset called but session was not prompting")
		}
		return
	}

	// Flush any buffered content
	if bs.acpClient != nil {
		bs.acpClient.FlushMarkdown()
	}
	bs.flushAndPersistMessages()

	// Notify observers that the prompt was forcefully reset
	eventCount := bs.GetEventCount()
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPromptComplete(eventCount)
	})

	if bs.logger != nil {
		bs.logger.Warn("Session forcefully reset due to unresponsive agent")
	}
}

// --- Queue processing methods ---

// hasImmediateQueuedMessages returns true if there are queued messages that will be processed
// immediately (queue processing is enabled, queue is not empty, and no delay is configured).
// This is used to skip follow-up suggestion analysis when the suggestions would be stale
// by the time they arrive (because the next message will be sent immediately).
func (bs *BackgroundSession) hasImmediateQueuedMessages() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return false
	}

	// Check if there's a delay configured - if so, suggestions might still be useful
	if bs.queueConfig != nil && bs.queueConfig.GetDelaySeconds() > 0 {
		return false
	}

	// Check if we have a store and queue
	if bs.store == nil || bs.persistedID == "" {
		return false
	}

	// Check if queue has messages
	queue := bs.store.Queue(bs.persistedID)
	queueLen, err := queue.Len()
	if err != nil {
		return false
	}

	return queueLen > 0
}

// processNextQueuedMessage checks the queue and sends the next message if queue processing is enabled.
// This is called after a prompt completes and applies the configured delay before sending.
func (bs *BackgroundSession) processNextQueuedMessage() {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return
	}

	// Get the queue for this session
	if bs.store == nil {
		return
	}
	queue := bs.store.Queue(bs.persistedID)

	// Pop the next message from the queue
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty or error - nothing to do
		return
	}

	// Notify observers that we're sending a queued message
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	// Apply delay if configured
	if bs.queueConfig != nil && bs.queueConfig.GetDelaySeconds() > 0 {
		time.Sleep(time.Duration(bs.queueConfig.GetDelaySeconds()) * time.Second)
	}

	bs.sendQueuedMessage(queue, msg)
}

// TryProcessQueuedMessage checks if the session is idle and enough time has passed since the last
// response, then processes the next queued message. This is used for startup initialization
// and periodic queue checking. Returns true if a message was sent.
func (bs *BackgroundSession) TryProcessQueuedMessage() bool {
	// Check if queue processing is enabled
	if bs.queueConfig != nil && !bs.queueConfig.IsEnabled() {
		return false
	}

	// Check if session is currently prompting
	if bs.IsPrompting() {
		return false
	}

	// Check if session is closed
	if bs.IsClosed() {
		return false
	}

	// Get the queue for this session
	if bs.store == nil {
		return false
	}
	queue := bs.store.Queue(bs.persistedID)

	// Check if queue has messages
	queueLen, err := queue.Len()
	if err != nil || queueLen == 0 {
		return false
	}

	// Check if delay has elapsed since last response
	delaySeconds := 0
	if bs.queueConfig != nil {
		delaySeconds = bs.queueConfig.GetDelaySeconds()
	}

	if delaySeconds > 0 {
		lastResponse := bs.GetLastResponseCompleteTime()
		// If lastResponse is zero, we can proceed (no previous response means agent is idle)
		if !lastResponse.IsZero() {
			elapsed := time.Since(lastResponse)
			if elapsed < time.Duration(delaySeconds)*time.Second {
				// Not enough time has passed
				return false
			}
		}
	}

	// Pop and send the next message
	msg, err := queue.Pop()
	if err != nil {
		// Queue is empty or error
		return false
	}

	// Notify observers that we're sending a queued message
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSending(msg.ID)
	})

	bs.sendQueuedMessage(queue, msg)
	return true
}

// sendQueuedMessage sends a message that was popped from the queue.
func (bs *BackgroundSession) sendQueuedMessage(queue *session.Queue, msg session.QueuedMessage) {
	if bs.logger != nil {
		bs.logger.Info("Sending queued message", "session_id", bs.persistedID, "message_id", msg.ID, "message", msg.Message)
	}
	// Get updated queue length for notification
	queueLen, _ := queue.Len()

	// Notify observers about queue update (message removed)
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLen, "removed", msg.ID)
	})

	// Send the queued message
	meta := PromptMeta{
		SenderID: "queue",
		PromptID: msg.ID,
		ImageIDs: msg.ImageIDs,
	}
	if err := bs.PromptWithMeta(msg.Message, meta); err != nil {
		if bs.logger != nil {
			bs.logger.Error("Failed to send queued message", "error", err, "message_id", msg.ID)
		}
		bs.notifyObservers(func(o SessionObserver) {
			o.OnError("Failed to send queued message: " + err.Error())
		})
		return
	}

	// Notify observers that the message was sent
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueMessageSent(msg.ID)
	})
}

// NotifyQueueUpdated notifies all observers about a queue state change.
// This is called by the queue API handlers when the queue is modified externally.
func (bs *BackgroundSession) NotifyQueueUpdated(queueLength int, action string, messageID string) {
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueUpdated(queueLength, action, messageID)
	})
}

// NotifyQueueReordered notifies all observers about a queue reorder.
// This is called by the queue API handlers when the queue order changes.
func (bs *BackgroundSession) NotifyQueueReordered(messages []session.QueuedMessage) {
	bs.notifyObservers(func(o SessionObserver) {
		o.OnQueueReordered(messages)
	})
}

// --- Callback methods for WebClient ---

func (bs *BackgroundSession) onAgentMessage(seq int64, html string) {
	if bs.IsClosed() {
		return
	}

	htmlLen := len(html)

	// Buffer for coalescing (protected by mutex)
	// AppendAgentMessage handles coalescing messages with the same seq.
	// The seq was already assigned at ACP receive time by WebClient.
	// Note: We don't persist immediately because agent messages stream in chunks.
	// They are persisted when a different event type arrives (flush-on-type-change)
	// or when the prompt completes.
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendAgentMessage(seq, html)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	observerCount := bs.ObserverCount()

	// Enhanced logging for debugging message content issues
	if bs.logger != nil {
		if htmlLen > 1000 {
			// Large message - log with preview
			preview := html
			if len(preview) > 200 {
				preview = html[:100] + "..." + html[htmlLen-100:]
			}
			bs.logger.Debug("agent_message_to_observers_large",
				"seq", seq,
				"html_len", htmlLen,
				"observer_count", observerCount,
				"session_id", bs.persistedID,
				"preview", preview)
		} else if observerCount > 1 {
			bs.logger.Debug("Notifying multiple observers of agent message",
				"observer_count", observerCount,
				"html_len", htmlLen,
				"seq", seq)
		}
	}

	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentMessage(seq, html)
	})
}

func (bs *BackgroundSession) onAgentThought(seq int64, text string) {
	if bs.IsClosed() {
		return
	}

	// Buffer for persistence (protected by mutex)
	// AppendAgentThought handles coalescing thoughts with the same seq.
	// The seq was already assigned at ACP receive time by WebClient.
	bs.bufferMu.Lock()
	if bs.eventBuffer != nil {
		bs.eventBuffer.AppendAgentThought(seq, text)
	}
	bs.bufferMu.Unlock()

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAgentThought(seq, text)
	})
}

func (bs *BackgroundSession) onToolCall(seq int64, id, title, status string) {
	if bs.IsClosed() {
		return
	}

	// Immediate persistence: flush any pending coalesced events, then persist this tool call
	// This makes storage the single source of truth for all events.
	bs.persistDiscreteEvent(BufferedEvent{
		Type: BufferedEventToolCall,
		Seq:  seq,
		Data: &ToolCallData{ID: id, Title: title, Status: status},
	})

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolCall(seq, id, title, status)
	})
}

func (bs *BackgroundSession) onToolUpdate(seq int64, id string, status *string) {
	if bs.IsClosed() {
		return
	}

	// Immediate persistence: flush any pending coalesced events, then persist this update
	bs.persistDiscreteEvent(BufferedEvent{
		Type: BufferedEventToolCallUpdate,
		Seq:  seq,
		Data: &ToolCallUpdateData{ID: id, Status: status},
	})

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnToolUpdate(seq, id, status)
	})
}

func (bs *BackgroundSession) onPlan(seq int64, entries []PlanEntry) {
	if bs.IsClosed() {
		return
	}

	// Immediate persistence: flush any pending coalesced events, then persist this plan
	bs.persistDiscreteEvent(BufferedEvent{
		Type: BufferedEventPlan,
		Seq:  seq,
		Data: &PlanData{Entries: entries},
	})

	// Cache plan state in SessionManager for restoration on conversation switch
	if bs.onPlanStateChanged != nil {
		bs.onPlanStateChanged(bs.persistedID, entries)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnPlan(seq, entries)
	})
}

func (bs *BackgroundSession) onFileWrite(seq int64, path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Immediate persistence: flush any pending coalesced events, then persist this file write
	bs.persistDiscreteEvent(BufferedEvent{
		Type: BufferedEventFileWrite,
		Seq:  seq,
		Data: &FileOperationData{Path: path, Size: size},
	})

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileWrite(seq, path, size)
	})
}

func (bs *BackgroundSession) onFileRead(seq int64, path string, size int) {
	if bs.IsClosed() {
		return
	}

	// Immediate persistence: flush any pending coalesced events, then persist this file read
	bs.persistDiscreteEvent(BufferedEvent{
		Type: BufferedEventFileRead,
		Seq:  seq,
		Data: &FileOperationData{Path: path, Size: size},
	})

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnFileRead(seq, path, size)
	})
}

func (bs *BackgroundSession) onPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if bs.IsClosed() {
		return acp.RequestPermissionResponse{}, &sessionError{"session is closed"}
	}

	// Get title from tool call
	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}

	// Check if we have any observers
	hasObservers := bs.HasObservers()

	// If no observers, auto-approve if configured
	if !hasObservers {
		if bs.autoApprove {
			resp := mittoAcp.AutoApprovePermission(params.Options)
			// Record the permission decision
			if bs.recorder != nil && resp.Outcome.Selected != nil {
				bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "auto_approved_no_client")
			}
			return resp, nil
		}
		// No observers and no auto-approve - cancel
		return mittoAcp.CancelledPermissionResponse(), nil
	}

	// Get a snapshot of observers
	bs.observersMu.RLock()
	observers := make([]SessionObserver, 0, len(bs.observers))
	for o := range bs.observers {
		observers = append(observers, o)
	}
	bs.observersMu.RUnlock()

	// Ask first observer for permission (others just observe)
	if len(observers) > 0 {
		resp, err := observers[0].OnPermission(ctx, params)
		if err == nil {
			// Persist the permission decision
			if bs.recorder != nil {
				if resp.Outcome.Selected != nil {
					bs.recorder.RecordPermission(title, string(resp.Outcome.Selected.OptionId), "user_selected")
				} else {
					bs.recorder.RecordPermission(title, "", "cancelled")
				}
			}
			return resp, nil
		}
	}

	// No observer handled it - cancel
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}, nil
}

// onAvailableCommands handles the available slash commands update from the agent.
// It stores the commands and notifies all observers.
func (bs *BackgroundSession) onAvailableCommands(commands []AvailableCommand) {
	if bs.IsClosed() {
		return
	}

	// Store the commands (sorted alphabetically by name)
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name < commands[j].Name
	})

	bs.availableCommandsMu.Lock()
	bs.availableCommands = commands
	bs.availableCommandsMu.Unlock()

	if bs.logger != nil {
		// Build list of command names for logging
		commandNames := make([]string, len(commands))
		for i, cmd := range commands {
			commandNames[i] = "/" + cmd.Name
		}
		bs.logger.Debug("Available slash commands updated",
			"count", len(commands),
			"commands", commandNames)
	}

	// Notify all observers
	bs.notifyObservers(func(o SessionObserver) {
		o.OnAvailableCommandsUpdated(commands)
	})
}

// AvailableCommands returns the current list of available slash commands.
// The commands are sorted alphabetically by name.
func (bs *BackgroundSession) AvailableCommands() []AvailableCommand {
	bs.availableCommandsMu.RLock()
	defer bs.availableCommandsMu.RUnlock()

	// Return a copy to avoid mutation
	if bs.availableCommands == nil {
		return nil
	}
	result := make([]AvailableCommand, len(bs.availableCommands))
	copy(result, bs.availableCommands)
	return result
}

// onCurrentModeChanged handles the session mode change notification from the agent.
// This is logged at DEBUG level to help with debugging session configuration.
func (bs *BackgroundSession) onCurrentModeChanged(modeID string) {
	if bs.IsClosed() {
		return
	}

	if bs.logger != nil {
		bs.logger.Debug("Session mode changed",
			"new_mode_id", modeID)
	}
}
