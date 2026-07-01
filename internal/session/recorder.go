package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/logging"
)

// MaxMetaBytes is the maximum allowed size (in bytes) of a JSON-encoded event
// metadata bag. If the bag exceeds this cap, it is dropped in its entirety
// (not truncated per-key) so that behaviour remains predictable.
// 4 KB is generous for lightweight annotations while blocking accidental
// secret storage.
const MaxMetaBytes = 4096

// RecordOption configures an Event before it is appended to the session log.
//
// SENSITIVITY POLICY: Options must NOT store secrets, credentials, full argument
// values, or full prompt text in the metadata bag. Meta is intended for
// lightweight, experimental annotations only. Well-established, high-traffic
// annotations should graduate to typed fields on the per-type *Data struct.
type RecordOption func(*Event)

// WithMeta sets a single key on the event's generic metadata bag. Repeated
// calls accumulate entries. Same sensitivity rules as RecordOption apply.
func WithMeta(key string, value any) RecordOption {
	return func(e *Event) {
		if e.Meta == nil {
			e.Meta = make(map[string]any)
		}
		e.Meta[key] = value
	}
}

// WithMetaMap merges all entries from m into the event's metadata bag.
// Same sensitivity rules as RecordOption apply.
func WithMetaMap(m map[string]any) RecordOption {
	return func(e *Event) {
		if len(m) == 0 {
			return
		}
		if e.Meta == nil {
			e.Meta = make(map[string]any)
		}
		for k, v := range m {
			e.Meta[k] = v
		}
	}
}

// validateMeta JSON-encodes the map and, if the result exceeds MaxMetaBytes,
// drops the entire map and logs a warning. Returns the (possibly nil) map.
func validateMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return meta
	}
	b, err := json.Marshal(meta)
	if err != nil || len(b) > MaxMetaBytes {
		size := len(b)
		if err != nil {
			size = -1
		}
		logging.Session().Warn("event meta exceeds size cap, dropped",
			"size", size, "cap", MaxMetaBytes)
		return nil
	}
	return meta
}

// applyOptions applies opts to event and validates the resulting meta.
func applyOptions(event Event, opts []RecordOption) Event {
	for _, o := range opts {
		o(&event)
	}
	event.Meta = validateMeta(event.Meta)
	return event
}

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
		sessionID: GenerateSessionID(),
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

// GenerateSessionID generates a unique session ID using timestamp and random bytes.
// Format: YYYYMMDD-HHMMSS-XXXXXXXX (e.g., 20260227-172217-a1b2c3d4)
// This format is:
// - Human-readable and sortable by creation time
// - Validated by IsValidSessionID regex in web/validation.go
// - Compatible with session store file operations
func GenerateSessionID() string {
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
func (r *Recorder) Start(acpServer, workingDir, workspaceUUID string) error {
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
			SessionID:     r.sessionID,
			ACPServer:     acpServer,
			WorkingDir:    workingDir,
			WorkspaceUUID: workspaceUUID,
		},
	}); err != nil {
		return err
	}

	log.Debug("session recording started",
		"session_id", r.sessionID,
		"acp_server", acpServer,
		"working_dir", workingDir,
		"workspace_uuid", workspaceUUID)
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
func (r *Recorder) RecordUserPrompt(message string, opts ...RecordOption) error {
	return r.RecordUserPromptComplete(message, nil, nil, "", "", 0, opts...)
}

// RecordUserPromptWithImages records a user prompt event with optional image references.
func (r *Recorder) RecordUserPromptWithImages(message string, images []ImageRef, opts ...RecordOption) error {
	return r.RecordUserPromptComplete(message, images, nil, "", "", 0, opts...)
}

// RecordUserPromptComplete records a user prompt event with optional image/file references, prompt ID, prompt name, and argument count.
// The promptID is a client-generated ID used for delivery confirmation on reconnect.
// The promptName is the name of the workspace prompt used (for UI rendering); empty string means no named prompt.
// The argumentCount is the number of Go-template .Args values supplied; 0 means no arguments (ad-hoc or no-arg named prompt).
func (r *Recorder) RecordUserPromptComplete(message string, images []ImageRef, files []FileRef, promptID string, promptName string, argumentCount int, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      UserPromptData{Message: message, Images: images, Files: files, PromptID: promptID, PromptName: promptName, ArgumentCount: argumentCount},
	}, opts))
}

// RecordUserPromptCompleteWithSeq records a user prompt event with a pre-assigned sequence number.
// The seq must have been obtained from getNextSeq() so that user-prompt persistence shares the
// same monotonic counter as the streaming path and avoids duplicate / out-of-order seq numbers.
func (r *Recorder) RecordUserPromptCompleteWithSeq(seq int64, message string, images []ImageRef, files []FileRef, promptID string, promptName string, argumentCount int, opts ...RecordOption) error {
	return r.RecordEventWithSeq(applyOptions(Event{
		Seq:       seq,
		Type:      EventTypeUserPrompt,
		Timestamp: time.Now(),
		Data:      UserPromptData{Message: message, Images: images, Files: files, PromptID: promptID, PromptName: promptName, ArgumentCount: argumentCount},
	}, opts))
}

// RecordAgentMessage records an agent message event.
func (r *Recorder) RecordAgentMessage(text string, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeAgentMessage,
		Timestamp: time.Now(),
		Data:      AgentMessageData{Text: text},
	}, opts))
}

// RecordAgentThought records an agent thought event.
func (r *Recorder) RecordAgentThought(text string, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeAgentThought,
		Timestamp: time.Now(),
		Data:      AgentThoughtData{Text: text},
	}, opts))
}

// RecordToolCall records a tool call event.
func (r *Recorder) RecordToolCall(toolCallID, title, status, kind string, rawInput, rawOutput any, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
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
	}, opts))
}

// RecordToolCallUpdate records a tool call update event.
func (r *Recorder) RecordToolCallUpdate(toolCallID string, status, title *string, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeToolCallUpdate,
		Timestamp: time.Now(),
		Data: ToolCallUpdateData{
			ToolCallID: toolCallID,
			Status:     status,
			Title:      title,
		},
	}, opts))
}

// RecordPlan records a plan event.
func (r *Recorder) RecordPlan(entries []PlanEntry, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypePlan,
		Timestamp: time.Now(),
		Data:      PlanData{Entries: entries},
	}, opts))
}

// RecordPermission records a permission event.
func (r *Recorder) RecordPermission(title, selectedOption, outcome string, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypePermission,
		Timestamp: time.Now(),
		Data: PermissionData{
			Title:          title,
			SelectedOption: selectedOption,
			Outcome:        outcome,
		},
	}, opts))
}

// RecordError records an error event.
func (r *Recorder) RecordError(message string, code int, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeError,
		Timestamp: time.Now(),
		Data:      ErrorData{Message: message, Code: code},
	}, opts))
}

// RecordFileRead records a file read event.
func (r *Recorder) RecordFileRead(path string, size int, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeFileRead,
		Timestamp: time.Now(),
		Data:      FileOperationData{Path: path, Size: size},
	}, opts))
}

// RecordFileWrite records a file write event.
func (r *Recorder) RecordFileWrite(path string, size int, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeFileWrite,
		Timestamp: time.Now(),
		Data:      FileOperationData{Path: path, Size: size},
	}, opts))
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

// RecordSessionChange records a user-initiated session change event.
// A struct param keeps the API future-proof as new kinds are added.
func (r *Recorder) RecordSessionChange(data SessionChangeData, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeSessionChange,
		Timestamp: time.Now(),
		Data:      data,
	}, opts))
}

// RecordSessionChangeWithSeq records a session change event with a pre-assigned
// sequence number obtained from getNextSeq(), so the event is ordered atomically
// with respect to concurrent streaming events (same pattern as RecordUserPromptCompleteWithSeq).
func (r *Recorder) RecordSessionChangeWithSeq(seq int64, data SessionChangeData, opts ...RecordOption) error {
	return r.RecordEventWithSeq(applyOptions(Event{
		Seq:       seq,
		Type:      EventTypeSessionChange,
		Timestamp: time.Now(),
		Data:      data,
	}, opts))
}

// RecordUIPromptAnswer records a user's response to a UI prompt from an MCP tool.
// This creates an audit trail of user decisions made through the UI prompt system.
func (r *Recorder) RecordUIPromptAnswer(requestID, optionID, label string, opts ...RecordOption) error {
	return r.recordEvent(applyOptions(Event{
		Type:      EventTypeUIPromptAnswer,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"request_id": requestID,
			"option_id":  optionID,
			"label":      label,
		},
	}, opts))
}
