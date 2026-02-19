package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/logging"
)

// Recorder records events to a session store.
type Recorder struct {
	store       *Store
	sessionID   string
	mu          sync.Mutex
	started     bool
	pruneConfig *PruneConfig
}

// NewRecorder creates a new session recorder.
func NewRecorder(store *Store) *Recorder {
	return &Recorder{
		store:     store,
		sessionID: generateSessionID(),
	}
}

// SetPruneConfig sets the pruning configuration for the recorder.
// When set, pruning is automatically performed after recording events
// to keep the session within the configured limits.
func (r *Recorder) SetPruneConfig(config *PruneConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneConfig = config
}

// generateSessionID generates a unique session ID using timestamp and random bytes.
func generateSessionID() string {
	timestamp := time.Now().Format("20060102-150405")
	randomBytes := make([]byte, 4)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to just timestamp if random fails
		return timestamp
	}
	return fmt.Sprintf("%s-%s", timestamp, hex.EncodeToString(randomBytes))
}

// NewRecorderWithID creates a new session recorder with a specific session ID.
func NewRecorderWithID(store *Store, sessionID string) *Recorder {
	return &Recorder{
		store:     store,
		sessionID: sessionID,
	}
}

// SessionID returns the session ID.
func (r *Recorder) SessionID() string {
	return r.sessionID
}

// Start starts a new recording session.
func (r *Recorder) Start(acpServer, workingDir string) error {
	log := logging.Session()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("session already started")
	}

	meta := Metadata{
		SessionID:  r.sessionID,
		ACPServer:  acpServer,
		WorkingDir: workingDir,
	}

	if err := r.store.Create(meta); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	r.started = true

	// Record session start event (call store directly to avoid deadlock)
	if err := r.store.AppendEvent(r.sessionID, Event{
		Type:      EventTypeSessionStart,
		Timestamp: time.Now(),
		Data: SessionStartData{
			SessionID:  r.sessionID,
			ACPServer:  acpServer,
			WorkingDir: workingDir,
		},
	}); err != nil {
		return err
	}

	log.Debug("session recording started",
		"session_id", r.sessionID,
		"acp_server", acpServer,
		"working_dir", workingDir)
	return nil
}

// Resume resumes recording to an existing session.
// This is used when switching back to a previously created session.
// If the session was previously completed (has a session_end event),
// its status is updated back to active.
func (r *Recorder) Resume() error {
	log := logging.Session()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return fmt.Errorf("session already started")
	}

	// Verify the session exists and get its metadata
	meta, err := r.store.GetMetadata(r.sessionID)
	if err != nil {
		if err == ErrSessionNotFound {
			return fmt.Errorf("session %s does not exist", r.sessionID)
		}
		return fmt.Errorf("failed to get session metadata: %w", err)
	}

	// If the session was previously completed, update status back to active
	if meta.Status == SessionStatusCompleted {
		if err := r.store.UpdateMetadata(r.sessionID, func(m *Metadata) {
			m.Status = SessionStatusActive
		}); err != nil {
			return fmt.Errorf("failed to update session status: %w", err)
		}
		log.Debug("session status updated from completed to active", "session_id", r.sessionID)
	}

	r.started = true
	log.Debug("session recording resumed", "session_id", r.sessionID)
	return nil
}

// RecordUserPrompt records a user prompt event.
func (r *Recorder) RecordUserPrompt(message string) error {
	return r.RecordUserPromptComplete(message, nil, nil, "")
}

// RecordUserPromptWithImages records a user prompt event with optional image references.
func (r *Recorder) RecordUserPromptWithImages(message string, images []ImageRef) error {
	return r.RecordUserPromptComplete(message, images, nil, "")
}

// RecordUserPromptFull records a user prompt event with optional image references and prompt ID.
// The promptID is a client-generated ID used for delivery confirmation on reconnect.
// Deprecated: Use RecordUserPromptComplete for new code that needs file support.
func (r *Recorder) RecordUserPromptFull(message string, images []ImageRef, promptID string) error {
	return r.RecordUserPromptComplete(message, images, nil, promptID)
}

// RecordUserPromptComplete records a user prompt event with optional image/file references and prompt ID.
// The promptID is a client-generated ID used for delivery confirmation on reconnect.
func (r *Recorder) RecordUserPromptComplete(message string, images []ImageRef, files []FileRef, promptID string) error {
	return r.recordEvent(Event{
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      UserPromptData{Message: message, Images: images, Files: files, PromptID: promptID},
	})
}

// RecordAgentMessage records an agent message event.
func (r *Recorder) RecordAgentMessage(text string) error {
	return r.recordEvent(Event{
		Type:      EventTypeAgentMessage,
		Timestamp: time.Now(),
		Data:      AgentMessageData{Text: text},
	})
}

// RecordAgentThought records an agent thought event.
func (r *Recorder) RecordAgentThought(text string) error {
	return r.recordEvent(Event{
		Type:      EventTypeAgentThought,
		Timestamp: time.Now(),
		Data:      AgentThoughtData{Text: text},
	})
}

// RecordToolCall records a tool call event.
func (r *Recorder) RecordToolCall(toolCallID, title, status, kind string, rawInput, rawOutput any) error {
	return r.recordEvent(Event{
		Type:      EventTypeToolCall,
		Timestamp: time.Now(),
		Data: ToolCallData{
			ToolCallID: toolCallID,
			Title:      title,
			Status:     status,
			Kind:       kind,
			RawInput:   rawInput,
			RawOutput:  rawOutput,
		},
	})
}

// RecordToolCallUpdate records a tool call update event.
func (r *Recorder) RecordToolCallUpdate(toolCallID string, status, title *string) error {
	return r.recordEvent(Event{
		Type:      EventTypeToolCallUpdate,
		Timestamp: time.Now(),
		Data: ToolCallUpdateData{
			ToolCallID: toolCallID,
			Status:     status,
			Title:      title,
		},
	})
}

// RecordPlan records a plan event.
func (r *Recorder) RecordPlan(entries []PlanEntry) error {
	return r.recordEvent(Event{
		Type:      EventTypePlan,
		Timestamp: time.Now(),
		Data:      PlanData{Entries: entries},
	})
}

// RecordPermission records a permission event.
func (r *Recorder) RecordPermission(title, selectedOption, outcome string) error {
	return r.recordEvent(Event{
		Type:      EventTypePermission,
		Timestamp: time.Now(),
		Data: PermissionData{
			Title:          title,
			SelectedOption: selectedOption,
			Outcome:        outcome,
		},
	})
}

// RecordError records an error event.
func (r *Recorder) RecordError(message string, code int) error {
	return r.recordEvent(Event{
		Type:      EventTypeError,
		Timestamp: time.Now(),
		Data:      ErrorData{Message: message, Code: code},
	})
}

// RecordFileRead records a file read event.
func (r *Recorder) RecordFileRead(path string, size int) error {
	return r.recordEvent(Event{
		Type:      EventTypeFileRead,
		Timestamp: time.Now(),
		Data:      FileOperationData{Path: path, Size: size},
	})
}

// RecordFileWrite records a file write event.
func (r *Recorder) RecordFileWrite(path string, size int) error {
	return r.recordEvent(Event{
		Type:      EventTypeFileWrite,
		Timestamp: time.Now(),
		Data:      FileOperationData{Path: path, Size: size},
	})
}

// Suspend suspends the recording session but keeps it active for later resumption.
// This is used when the connection is temporarily closed (e.g., browser refresh).
// The session remains "active" so it can be resumed without creating a new session.
func (r *Recorder) Suspend() error {
	log := logging.Session()
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	// Mark as not started so further operations are blocked,
	// but don't record a session end event or change the status.
	// The session remains "active" in the metadata.
	r.started = false
	log.Debug("session recording suspended", "session_id", r.sessionID)
	return nil
}

// End ends the recording session with the given end data.
// The SessionEndData struct can include additional context like signal, event count, etc.
func (r *Recorder) End(data SessionEndData) error {
	log := logging.Session()
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	// Mark as ended first to prevent duplicate session_end events
	// if End is called multiple times (e.g., during shutdown)
	r.started = false

	// Get the correct sequence number for session_end.
	// Due to message coalescing during streaming, MaxSeq can be much higher than EventCount.
	// We must use MaxSeq + 1 to ensure session_end appears after all streamed events.
	maxSeq := r.MaxSeq()
	eventCount := int64(r.EventCount())
	var nextSeq int64
	if maxSeq > eventCount {
		nextSeq = maxSeq + 1
	} else {
		nextSeq = eventCount + 1
	}

	// Record session end event with the correct sequence number
	if err := r.store.RecordEvent(r.sessionID, Event{
		Seq:       nextSeq,
		Type:      EventTypeSessionEnd,
		Timestamp: time.Now(),
		Data:      data,
	}); err != nil {
		return err
	}

	// Update metadata status
	if err := r.store.UpdateMetadata(r.sessionID, func(meta *Metadata) {
		meta.Status = SessionStatusCompleted
	}); err != nil {
		return err
	}

	log.Debug("session recording ended",
		"session_id", r.sessionID,
		"reason", data.Reason,
		"signal", data.Signal,
		"event_count", data.EventCount,
		"was_prompting", data.WasPrompting,
		"acp_connected", data.ACPConnected)
	return nil
}

// recordEvent records an event to the store.
func (r *Recorder) recordEvent(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return fmt.Errorf("session not started")
	}

	if err := r.store.AppendEvent(r.sessionID, event); err != nil {
		return err
	}

	// Prune if configured and limits are exceeded
	if r.pruneConfig != nil && r.pruneConfig.IsEnabled() {
		// Pruning is best-effort; log errors but don't fail the recording
		if _, err := r.store.PruneIfNeeded(r.sessionID, r.pruneConfig); err != nil {
			log := logging.Session()
			log.Warn("failed to prune session after recording event",
				"session_id", r.sessionID,
				"error", err)
		}
	}

	return nil
}

// RecordEventWithSeq records an event with a pre-assigned sequence number.
// This is used for immediate persistence where seq is assigned at streaming time.
// Unlike recordEvent, this method uses Store.RecordEvent which preserves the seq.
func (r *Recorder) RecordEventWithSeq(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return fmt.Errorf("session not started")
	}

	if err := r.store.RecordEvent(r.sessionID, event); err != nil {
		return err
	}

	// Prune if configured and limits are exceeded
	if r.pruneConfig != nil && r.pruneConfig.IsEnabled() {
		// Pruning is best-effort; log errors but don't fail the recording
		if _, err := r.store.PruneIfNeeded(r.sessionID, r.pruneConfig); err != nil {
			log := logging.Session()
			log.Warn("failed to prune session after recording event",
				"session_id", r.sessionID,
				"error", err)
		}
	}

	return nil
}

// IsStarted returns whether the session has been started.
func (r *Recorder) IsStarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started
}

// EventCount returns the current event count for the session.
// Returns 0 if the session doesn't exist or there's an error.
func (r *Recorder) EventCount() int {
	if r.store == nil {
		return 0
	}
	meta, err := r.store.GetMetadata(r.sessionID)
	if err != nil {
		return 0
	}
	return meta.EventCount
}

// MaxSeq returns the highest sequence number persisted for the session.
// Returns 0 if the session doesn't exist, there's an error, or MaxSeq is not set.
// This is used to initialize nextSeq when resuming a session.
func (r *Recorder) MaxSeq() int64 {
	if r.store == nil {
		return 0
	}
	meta, err := r.store.GetMetadata(r.sessionID)
	if err != nil {
		return 0
	}
	return meta.MaxSeq
}

// RecordUIPromptAnswer records a user's response to a UI prompt from an MCP tool.
// This creates an audit trail of user decisions made through the UI prompt system.
func (r *Recorder) RecordUIPromptAnswer(requestID, optionID, label string) error {
	return r.recordEvent(Event{
		Type:      EventTypeUIPromptAnswer,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"request_id": requestID,
			"option_id":  optionID,
			"label":      label,
		},
	})
}
