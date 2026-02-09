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
)

// Verify Store implements SessionStore at compile time.
var _ SessionStore = (*Store)(nil)

// Store provides session persistence operations.
type Store struct {
	baseDir string
	mu      sync.RWMutex
	closed  bool
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

// sessionDir returns the directory path for a session.
func (s *Store) sessionDir(sessionID string) string {
	return filepath.Join(s.baseDir, sessionID)
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

// Queue returns a Queue instance for managing the message queue of a session.
// The returned Queue is safe for concurrent use.
func (s *Store) Queue(sessionID string) *Queue {
	return NewQueue(s.sessionDir(sessionID))
}

// ActionButtons returns an ActionButtonsStore instance for managing action buttons of a session.
// The returned ActionButtonsStore is safe for concurrent use.
func (s *Store) ActionButtons(sessionID string) *ActionButtonsStore {
	return NewActionButtonsStore(s.sessionDir(sessionID))
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

	log.Info("session created",
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

	// Assign sequence number (1-based, so first event is seq=1)
	event.Seq = int64(meta.EventCount + 1)

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
	meta.UpdatedAt = time.Now()
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
func (s *Store) readMetadata(sessionID string) (Metadata, error) {
	var meta Metadata
	if err := fileutil.ReadJSON(s.metadataPath(sessionID), &meta); err != nil {
		if os.IsNotExist(err) {
			return Metadata{}, ErrSessionNotFound
		}
		return Metadata{}, fmt.Errorf("failed to read metadata: %w", err)
	}
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
	return s.ReadEventsFrom(sessionID, 0)
}

// ReadEventsFrom reads events from a session's event log starting after the given sequence number.
// If afterSeq is 0, all events are returned.
// If afterSeq is 5, only events with seq > 5 are returned.
func (s *Store) ReadEventsFrom(sessionID string, afterSeq int64) ([]Event, error) {
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
	scanner := bufio.NewScanner(f)
	// Increase buffer size to handle large events (e.g., agent messages with code blocks)
	// Default is 64KB, increase to 10MB to handle very long lines
	const maxScannerBuffer = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event: %w", err)
		}
		// Only include events after the specified sequence number
		if event.Seq > afterSeq {
			events = append(events, event)
		}
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
	scanner := bufio.NewScanner(f)
	// Increase buffer size to handle large events (e.g., agent messages with code blocks)
	// Default is 64KB, increase to 10MB to handle very long lines
	const maxScannerBuffer = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBuffer)
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal event: %w", err)
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
func (s *Store) Delete(sessionID string) error {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrStoreClosed
	}

	sessionDir := s.sessionDir(sessionID)
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		return ErrSessionNotFound
	}

	if err := os.RemoveAll(sessionDir); err != nil {
		return err
	}

	log.Info("session deleted", "session_id", sessionID, "session_dir", sessionDir)
	return nil
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
