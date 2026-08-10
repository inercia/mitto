package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/logging"
)

const (
	eventsFileName   = "events.jsonl"
	metadataFileName = "metadata.json"
	lockFileName     = ".lock"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionLocked   = errors.New("session is locked by another process")
	ErrStoreClosed     = errors.New("store is closed")
	// ErrSessionNoArchive is the canonical rejection reported by every archive
	// entry point when the target conversation's Metadata.NoArchive flag is set
	// (mitto-yvel.3), so REST and MCP surface the same wording. Deletion is
	// unaffected by this flag and must never check it.
	ErrSessionNoArchive = errors.New("conversation is marked non-archivable and cannot be archived; delete it instead")
)

// Verify Store implements SessionStore at compile time.
var _ SessionStore = (*Store)(nil)

// Store provides session persistence operations.
type Store struct {
	baseDir string
	mu      sync.RWMutex
	closed  bool

	// deleteObserver, when set, is invoked once per session removed by Delete
	// (the target session itself plus any cascade-deleted descendants), after
	// s.mu has been released. Guarded by s.mu; see SetDeleteObserver.
	deleteObserver func(sessionID, parentSessionID string)

	// loopStoppedObserver, when set, is invoked once a session's loop
	// transitions from enabled to stopped via LoopStore.MarkStopped. Guarded
	// by s.mu; see SetLoopStoppedObserver. Threaded into each *LoopStore
	// returned by Loop(sessionID) (store_dispensers.go).
	loopStoppedObserver func(sessionID string, reason StoppedReason)
}

// SetDeleteObserver registers a callback invoked once per session removed by
// Delete (target plus any cascade-deleted descendants). The callback receives
// the deleted session's ID and its immediate parent's ID (empty string if it
// had none). It is invoked after the store's internal lock has been released,
// so the callback may safely call back into other Store methods (e.g.
// GetMetadata, Exists) without deadlocking. Pass nil to clear the observer.
//
// Only Delete notifies: retention-driven removal of archived sessions
// (CleanupArchivedSessions) deliberately does not, since it reclaims disk for
// sessions already archived rather than acting on a user-visible deletion.
func (s *Store) SetDeleteObserver(fn func(sessionID, parentSessionID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteObserver = fn
}

// SetLoopStoppedObserver registers a callback invoked once a session's loop
// transitions from enabled to stopped (LoopStore.MarkStopped), across every
// stop path (auto-stop on max iterations/duration, resume/delivery/context
// failures, MCP loop_enabled:false, REST pause, archive). The callback
// receives the stopped session's own ID and the StoppedReason; it does NOT
// receive a parent session ID (unlike SetDeleteObserver) because, unlike a
// deleted session, the stopped session's own metadata still exists at
// notification time, so a caller that needs the parent can resolve it via
// GetMetadata(sessionID).ParentSessionID. Invoked after the write and after
// the LoopStore's internal lock has been released, so the callback may
// safely call back into the store. Pass nil to clear the observer.
func (s *Store) SetLoopStoppedObserver(fn func(sessionID string, reason StoppedReason)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loopStoppedObserver = fn
}

// NewStore creates a new session store with the given base directory.
func NewStore(baseDir string) (*Store, error) {
	log := logging.Session()
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}
	log.Debug("session store initialized", "base_dir", baseDir)
	return &Store{baseDir: baseDir}, nil
}

// RunMigrations runs any pending data migrations on the session store.
// The context parameter is optional and provides external information to migrations
// (e.g., ACP server name mappings for the normalize migration).
// This should be called after NewStore and before the store is used.
func (s *Store) RunMigrations(ctx *MigrationContext) error {
	return RunMigrations(s.baseDir, ctx)
}

// BaseDir returns the base directory of the store.
func (s *Store) BaseDir() string {
	return s.baseDir
}

// sessionDir returns the directory path for a session.
func (s *Store) sessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, sessionID)
}

// SessionDir returns the directory path for a session (exported).
func (s *Store) SessionDir(sessionID string) string {
	return s.sessionDir(sessionID)
}

// eventsPath returns the events file path for a session.
func (s *Store) eventsPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), eventsFileName)
}

// metadataPath returns the metadata file path for a session.
func (s *Store) metadataPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), metadataFileName)
}

// lockPath returns the lock file path for a session.
func (s *Store) lockPath(sessionID string) string {
	return filepath.Join(s.sessionDir(sessionID), lockFileName)
}

// Create creates a new session with the given metadata.
func (s *Store) Create(meta Metadata) error {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	sessionDir := s.sessionDir(meta.SessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// Create empty events file
	eventsFile, err := os.Create(s.eventsPath(meta.SessionID))
	if err != nil {
		return fmt.Errorf("failed to create events file: %w", err)
	}
	eventsFile.Close()

	// Write metadata
	meta.CreatedAt = time.Now()
	meta.UpdatedAt = meta.CreatedAt
	meta.EventCount = 0
	meta.Status = SessionStatusActive

	if err := s.writeMetadata(meta); err != nil {
		return err
	}

	log.Debug("session created",
		"session_id", meta.SessionID,
		"acp_server", meta.ACPServer,
		"working_dir", meta.WorkingDir,
		"session_dir", sessionDir)
	return nil
}

// AppendEvent appends an event to the session's event log.
// The event's Seq field is automatically assigned based on the current event count.
func (s *Store) AppendEvent(sessionID string, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	// Read metadata first to get current event count for sequence number
	meta, err := s.readMetadata(sessionID)
	if err != nil {
		return err
	}

	eventsPath := s.eventsPath(sessionID)
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("failed to open events file: %w", err)
	}
	defer f.Close()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Assign sequence number. Use max(EventCount, MaxSeq)+1 so that after
	// pruning (which reduces EventCount but preserves MaxSeq), new events
	// don't collide with existing seqs already written to the file.
	nextSeq := int64(meta.EventCount) + 1
	if meta.MaxSeq+1 > nextSeq {
		nextSeq = meta.MaxSeq + 1
	}
	event.Seq = nextSeq

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	// L1: Structured logging for event persistence
	log := logging.Session()
	log.Debug("event_persisted",
		"session_id", sessionID,
		"seq", event.Seq,
		"event_type", event.Type,
		"event_count", meta.EventCount+1)

	// Update metadata
	meta.EventCount++
	if event.Seq > meta.MaxSeq {
		meta.MaxSeq = event.Seq
	}
	meta.UpdatedAt = time.Now()
	// Track last user message time for sorting conversations
	if event.Type == EventTypeUserPrompt {
		meta.LastUserMessageAt = event.Timestamp
	}
	return s.writeMetadata(meta)
}

// RecordEvent persists an event with its pre-assigned sequence number.
// Unlike AppendEvent, this method does NOT reassign the seq field.
// The event.Seq must be > 0 (assigned by the caller).
// This is used for immediate persistence where seq is assigned at streaming time.
func (s *Store) RecordEvent(sessionID string, event Event) error {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	// Validate seq is pre-assigned
	if event.Seq <= 0 {
		return fmt.Errorf("event.Seq must be > 0, got %d", event.Seq)
	}

	// Read metadata to validate seq ordering
	meta, err := s.readMetadata(sessionID)
	if err != nil {
		return err
	}

	// Validate monotonic ordering: event.Seq should be max(EventCount, MaxSeq) + 1.
	// After pruning, EventCount can be less than MaxSeq, so we use whichever is higher.
	// Log at DEBUG level as a safety check — mismatches indicate a bug.
	expectedSeq := int64(meta.EventCount) + 1
	if meta.MaxSeq+1 > expectedSeq {
		expectedSeq = meta.MaxSeq + 1
	}
	if event.Seq != expectedSeq {
		log.Debug("seq_mismatch_on_persist",
			"session_id", sessionID,
			"event_seq", event.Seq,
			"expected_seq", expectedSeq,
			"event_type", event.Type)
		// Continue anyway - the event has the seq assigned at streaming time
	}

	eventsPath := s.eventsPath(sessionID)
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("failed to open events file: %w", err)
	}
	defer f.Close()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Note: We do NOT reassign event.Seq - it's already set by the caller

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	log.Debug("event_recorded",
		"session_id", sessionID,
		"seq", event.Seq,
		"event_type", event.Type,
		"event_count", meta.EventCount+1)

	// Update metadata
	meta.EventCount++
	meta.UpdatedAt = time.Now()
	// Track the highest seq seen (for immediate persistence)
	if event.Seq > meta.MaxSeq {
		meta.MaxSeq = event.Seq
	}
	// Track last user message time for sorting conversations
	if event.Type == EventTypeUserPrompt {
		meta.LastUserMessageAt = event.Timestamp
	}
	return s.writeMetadata(meta)
}

// GetMetadata retrieves the metadata for a session.
func (s *Store) GetMetadata(sessionID string) (Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return Metadata{}, ErrStoreClosed
	}

	return s.readMetadata(sessionID)
}

// readMetadata reads metadata from disk (must be called with lock held).
// Automatically migrates ChildOrigin for backward compatibility with old metadata files.
func (s *Store) readMetadata(sessionID string) (Metadata, error) {
	var meta Metadata
	if err := fileutil.ReadJSON(s.metadataPath(sessionID), &meta); err != nil {
		if os.IsNotExist(err) {
			return Metadata{}, ErrSessionNotFound
		}
		return Metadata{}, fmt.Errorf("failed to read metadata: %w", err)
	}
	// Migrate old metadata that has IsAutoChild/ParentSessionID but no ChildOrigin
	meta.MigrateChildOrigin()
	return meta, nil
}

// writeMetadata writes metadata to disk (must be called with lock held).
func (s *Store) writeMetadata(meta Metadata) error {
	if err := fileutil.WriteJSON(s.metadataPath(meta.SessionID), meta, 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}
	return nil
}

// UpdateMetadata updates the metadata for a session.
func (s *Store) UpdateMetadata(sessionID string, updateFn func(*Metadata)) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	meta, err := s.readMetadata(sessionID)
	if err != nil {
		return err
	}

	updateFn(&meta)
	meta.UpdatedAt = time.Now()
	return s.writeMetadata(meta)
}

// ReadEvents reads all events from a session's event log.
func (s *Store) ReadEvents(sessionID string) ([]Event, error) {
	return s.ReadEventsFrom(sessionID, 0, 0)
}

// ReadEventsFrom reads events from a session's event log starting after the given sequence number.
// If afterSeq is 0, all events are returned.
// If afterSeq is 5, only events with seq > 5 are returned.
func (s *Store) ReadEventsFrom(sessionID string, afterSeq int64, limit int) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	f, err := os.Open(s.eventsPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to open events file: %w", err)
	}
	defer f.Close()

	var events []Event
	log := logging.Session()
	scanner := bufio.NewScanner(f)
	// Increase buffer size to handle large events (e.g., agent messages with code blocks)
	// Default is 64KB, increase to 10MB to handle very long lines
	const maxScannerBuffer = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)
	seenSeqs := make(map[int64]struct{})
	lineNum := 0
	dupCount := 0
	for scanner.Scan() {
		lineNum++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			// Skip corrupt lines (e.g. a torn write) so a single bad line does
			// not make the whole conversation unreadable. Don't log content (user data).
			log.Warn("skipping corrupt event line", "session_id", sessionID, "line", lineNum, "bytes", len(scanner.Bytes()), "error", err)
			continue
		}
		// Defensive dedup: keep only the first occurrence of each seq.
		// Duplicate seqs can appear in corrupted files written during concurrent
		// AppendEvent / RecordEvent races before the fix.
		if _, seen := seenSeqs[event.Seq]; seen {
			dupCount++
			continue
		}
		seenSeqs[event.Seq] = struct{}{}
		// Only include events after the specified sequence number
		if event.Seq > afterSeq {
			events = append(events, event)
			// Stop early if we've reached the limit (0 = unlimited for backward compat)
			if limit > 0 && len(events) >= limit {
				break
			}
		}
	}

	if dupCount > 0 {
		log.Debug("deduped duplicate seq events on read",
			"session_id", sessionID,
			"dropped", dupCount)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read events: %w", err)
	}

	return events, nil
}

// ReadEventsLast reads the last N events from a session's event log.
// If beforeSeq > 0, only events with seq < beforeSeq are considered.
// Returns events in chronological order (oldest first).
func (s *Store) ReadEventsLast(sessionID string, limit int, beforeSeq int64) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	f, err := os.Open(s.eventsPath(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to open events file: %w", err)
	}
	defer f.Close()

	// Read all matching events first (we need to know total count to get last N)
	var allEvents []Event
	log := logging.Session()
	scanner := bufio.NewScanner(f)
	// Increase buffer size to handle large events (e.g., agent messages with code blocks)
	// Default is 64KB, increase to 10MB to handle very long lines
	const maxScannerBuffer = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			// Skip corrupt lines (e.g. a torn write) so a single bad line does
			// not make the whole conversation unreadable. Don't log content (user data).
			log.Warn("skipping corrupt event line", "session_id", sessionID, "line", lineNum, "bytes", len(scanner.Bytes()), "error", err)
			continue
		}
		// If beforeSeq is specified, only include events before it
		if beforeSeq > 0 && event.Seq >= beforeSeq {
			continue
		}
		allEvents = append(allEvents, event)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read events: %w", err)
	}

	// Return last N events
	if limit > 0 && len(allEvents) > limit {
		return allEvents[len(allEvents)-limit:], nil
	}
	return allEvents, nil
}

// ReadEventsLastReverse reads the last N events in reverse chronological order (newest first).
// If beforeSeq > 0, only events with seq < beforeSeq are considered.
// This is optimized for UIs that render newest messages first.
func (s *Store) ReadEventsLastReverse(sessionID string, limit int, beforeSeq int64) ([]Event, error) {
	// Get events in chronological order first
	events, err := s.ReadEventsLast(sessionID, limit, beforeSeq)
	if err != nil {
		return nil, err
	}

	// Reverse the slice to get newest first
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	return events, nil
}

// List returns metadata for all sessions.
func (s *Store) List() ([]Metadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var sessions []Metadata
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.readMetadata(entry.Name())
		if err != nil {
			// Skip sessions with invalid metadata
			continue
		}
		sessions = append(sessions, meta)
	}

	return sessions, nil
}

// Delete removes a session and all its data from local storage.
//
// Note: This only deletes the local session data (events, metadata).
// If the session was associated with an ACP server session (via ACPSessionID
// in metadata), that server-side session is NOT deleted. The ACP protocol
// does not provide a session deletion mechanism - server-side sessions are
// managed by the ACP server itself (typically cleaned up on server restart
// or via server-specific expiration policies).
//
// If the session being deleted is a parent session (has child sessions),
// auto-children (IsAutoChild=true) are cascade deleted while MCP-children
// (IsAutoChild=false) are orphaned (ParentSessionID cleared).
func (s *Store) Delete(sessionID string) error {
	deleted, observer, err := s.deleteLocked(sessionID)
	if err != nil {
		return err
	}

	// Notify the delete observer, if any, only after the lock has been
	// released so an observer that calls back into the Store cannot
	// deadlock. Order matches removal order: cascade descendants first,
	// then the target session itself.
	if observer != nil {
		for _, d := range deleted {
			observer(d.ID, d.ParentID)
		}
	}

	return nil
}

// deleteLocked performs the actual deletion under s.mu and returns the list
// of sessions removed (cascade descendants followed by the target session),
// along with a snapshot of the delete observer captured under the lock. It
// does not invoke the observer itself - that is the caller's (Delete's)
// responsibility, done after the lock is released.
func (s *Store) deleteLocked(sessionID string) ([]deletedSession, func(string, string), error) {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, nil, ErrStoreClosed
	}

	sessionDir := s.sessionDir(sessionID)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		return nil, nil, ErrSessionNotFound
	}

	// Capture the target session's own parent before removing its metadata,
	// so it can be reported to the delete observer below.
	var parentID string
	if meta, err := s.readMetadata(sessionID); err == nil {
		parentID = meta.ParentSessionID
	}

	// Before deleting, find and clean up any child sessions that reference this parent.
	// Auto-children are cascade deleted; MCP-children are orphaned.
	deleted, err := s.handleChildSessionsOnParentDelete(sessionID, nil)
	if err != nil {
		log.Error("failed to handle child sessions on parent delete", "error", err, "session_id", sessionID)
		// Continue with deletion even if cleanup fails - we don't want to block deletion
	}

	if err := os.RemoveAll(sessionDir); err != nil {
		return nil, nil, err
	}

	// Release the shared per-directory queue lock (mitto-pr0), if any, so the
	// process-wide registry does not retain an entry for a deleted session.
	releaseQueueLock(sessionDir)

	deleted = append(deleted, deletedSession{ID: sessionID, ParentID: parentID})

	log.Debug("session deleted", "session_id", sessionID, "session_dir", sessionDir)
	return deleted, s.deleteObserver, nil
}

// Exists checks if a session exists.
func (s *Store) Exists(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false
	}

	_, err := os.Stat(s.metadataPath(sessionID))
	return err == nil
}

// CountSessions returns the number of stored sessions.
// M3: This is used by the health check endpoint.
func (s *Store) CountSessions() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return 0, ErrStoreClosed
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			// Check if it has a metadata file (valid session)
			metaPath := filepath.Join(s.baseDir, entry.Name(), "metadata.json")
			if _, err := os.Stat(metaPath); err == nil {
				count++
			}
		}
	}
	return count, nil
}

// Close closes the store.
func (s *Store) Close() error {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	log.Debug("session store closed", "base_dir", s.baseDir)
	return nil
}
