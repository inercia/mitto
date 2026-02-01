package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
	"github.com/inercia/mitto/internal/logging"
)

const (
	// DefaultHeartbeatInterval is the default interval for heartbeat updates.
	DefaultHeartbeatInterval = 10 * time.Second

	// DefaultStaleTimeout is the default timeout after which a lock is considered stale.
	DefaultStaleTimeout = 60 * time.Second
)

var (
	// ErrSessionProcessing indicates the session is actively processing and cannot be stolen.
	ErrSessionProcessing = errors.New("session is currently processing - agent is working")

	// ErrSessionWaitingPermission indicates the session is waiting for permission.
	ErrSessionWaitingPermission = errors.New("session is waiting for permission approval")

	// ErrLockNotHeld indicates the caller doesn't hold the lock.
	ErrLockNotHeld = errors.New("lock not held by this instance")
)

// Lock represents an active session lock held by this process.
type Lock struct {
	store      *Store
	sessionID  string
	instanceID string
	info       LockInfo
	mu         sync.Mutex
	released   bool
	stopCh     chan struct{}
	doneCh     chan struct{}
}

// generateInstanceID creates a unique instance identifier.
func generateInstanceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("instance-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// getHostname returns the current hostname or "unknown" if it fails.
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

// TryAcquireLock attempts to acquire a lock on the session.
// Returns ErrSessionLocked if the session is locked by another active process.
// Returns ErrSessionProcessing if the session is locked and actively processing.
func (s *Store) TryAcquireLock(sessionID, clientType string) (*Lock, error) {
	return s.acquireLock(sessionID, clientType, false, false)
}

// ForceAcquireLock forcibly acquires a lock, but only if the session is idle or stale.
// Returns ErrSessionProcessing if the session is actively processing.
func (s *Store) ForceAcquireLock(sessionID, clientType string) (*Lock, error) {
	return s.acquireLock(sessionID, clientType, true, false)
}

// ForceInterruptLock forcibly acquires a lock even if the session is processing.
// This should only be used with explicit user confirmation.
func (s *Store) ForceInterruptLock(sessionID, clientType string) (*Lock, error) {
	return s.acquireLock(sessionID, clientType, true, true)
}

// acquireLock is the internal implementation for lock acquisition.
func (s *Store) acquireLock(sessionID, clientType string, force, interrupt bool) (*Lock, error) {
	log := logging.Session()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	// Check if session exists
	if !s.sessionExistsLocked(sessionID) {
		return nil, ErrSessionNotFound
	}

	lockPath := s.lockPath(sessionID)
	instanceID := generateInstanceID()
	currentHostname := getHostname()

	// Check existing lock
	existingLock, err := s.readLockFile(lockPath)
	if err == nil {
		// Lock file exists, check if we can acquire
		switch {
		case existingLock.IsProcessDead(currentHostname):
			// Process is dead, we can safely take over
			log.Debug("taking over lock from dead process",
				"session_id", sessionID,
				"dead_pid", existingLock.PID,
				"dead_hostname", existingLock.Hostname)
		case !force:
			// Not forcing, check if lock is stale
			if !existingLock.IsStale(DefaultStaleTimeout) {
				return nil, ErrSessionLocked
			}
			log.Debug("taking over stale lock",
				"session_id", sessionID,
				"stale_pid", existingLock.PID,
				"last_heartbeat", existingLock.Heartbeat)
		default:
			// Forcing, but check if safe to steal
			if !interrupt && !existingLock.IsSafeToSteal(DefaultStaleTimeout) {
				switch existingLock.Status {
				case LockStatusProcessing:
					return nil, ErrSessionProcessing
				case LockStatusWaitingPermission:
					return nil, ErrSessionWaitingPermission
				default:
					return nil, ErrSessionLocked
				}
			}
			log.Debug("forcing lock acquisition",
				"session_id", sessionID,
				"previous_pid", existingLock.PID,
				"previous_status", existingLock.Status,
				"interrupt", interrupt)
		}
	}

	// Create new lock
	now := time.Now()
	lockInfo := LockInfo{
		PID:          os.Getpid(),
		Hostname:     getHostname(),
		InstanceID:   instanceID,
		ClientType:   clientType,
		StartedAt:    now,
		Heartbeat:    now,
		LastActivity: now,
		Status:       LockStatusIdle,
	}

	if err := s.writeLockFile(lockPath, lockInfo); err != nil {
		return nil, fmt.Errorf("failed to write lock file: %w", err)
	}

	lock := &Lock{
		store:      s,
		sessionID:  sessionID,
		instanceID: instanceID,
		info:       lockInfo,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}

	// Register lock for cleanup on exit/crash
	registerLock(lock)

	// Start heartbeat goroutine
	go lock.heartbeatLoop()

	log.Info("session lock acquired",
		"session_id", sessionID,
		"client_type", clientType,
		"instance_id", instanceID,
		"pid", lockInfo.PID)

	return lock, nil
}

// GetLockInfo retrieves the current lock information for a session.
// Returns nil if the session is not locked.
func (s *Store) GetLockInfo(sessionID string) (*LockInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	lockPath := s.lockPath(sessionID)
	info, err := s.readLockFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Not locked
		}
		return nil, err
	}
	return &info, nil
}

// IsLocked checks if a session is currently locked.
func (s *Store) IsLocked(sessionID string) (bool, *LockInfo, error) {
	info, err := s.GetLockInfo(sessionID)
	if err != nil {
		return false, nil, err
	}
	if info == nil {
		return false, nil, nil
	}
	// Check if lock is stale
	if info.IsStale(DefaultStaleTimeout) {
		return false, info, nil // Stale lock, effectively unlocked
	}
	return true, info, nil
}

// readLockFile reads and parses a lock file.
func (s *Store) readLockFile(path string) (LockInfo, error) {
	var info LockInfo
	if err := fileutil.ReadJSON(path, &info); err != nil {
		return LockInfo{}, err
	}
	return info, nil
}

// writeLockFile writes a lock file atomically.
func (s *Store) writeLockFile(path string, info LockInfo) error {
	return fileutil.WriteJSONAtomic(path, info, 0644)
}

// sessionExistsLocked checks if a session exists (must be called with lock held).
func (s *Store) sessionExistsLocked(sessionID string) bool {
	_, err := os.Stat(s.metadataPath(sessionID))
	return err == nil
}

// --- Lock methods ---

// SessionID returns the session ID this lock is for.
func (l *Lock) SessionID() string {
	return l.sessionID
}

// InstanceID returns the unique instance ID for this lock holder.
func (l *Lock) InstanceID() string {
	return l.instanceID
}

// Info returns a copy of the current lock info.
func (l *Lock) Info() LockInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.info
}

// SetStatus updates the lock status.
func (l *Lock) SetStatus(status LockStatus, message string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.released {
		return ErrLockNotHeld
	}

	l.info.Status = status
	l.info.StatusMessage = message
	l.info.LastActivity = time.Now()

	return l.writeLockFileLocked()
}

// SetProcessing marks the session as actively processing.
func (l *Lock) SetProcessing(message string) error {
	return l.SetStatus(LockStatusProcessing, message)
}

// SetIdle marks the session as idle.
func (l *Lock) SetIdle() error {
	return l.SetStatus(LockStatusIdle, "")
}

// SetWaitingPermission marks the session as waiting for permission.
func (l *Lock) SetWaitingPermission(message string) error {
	return l.SetStatus(LockStatusWaitingPermission, message)
}

// Release releases the lock.
func (l *Lock) Release() error {
	log := logging.Session()
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.released {
		return nil // Already released
	}

	l.released = true
	close(l.stopCh)

	// Unregister from cleanup registry
	unregisterLock(l)

	// Wait for heartbeat to stop
	<-l.doneCh

	// Remove lock file
	lockPath := l.store.lockPath(l.sessionID)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove lock file: %w", err)
	}

	log.Info("session lock released", "session_id", l.sessionID, "instance_id", l.instanceID)
	return nil
}

// IsValid checks if this lock is still valid (not stolen by another process).
func (l *Lock) IsValid() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.released {
		return false
	}

	// Read current lock file and check if we still own it
	lockPath := l.store.lockPath(l.sessionID)
	currentInfo, err := l.store.readLockFile(lockPath)
	if err != nil {
		return false // Lock file gone or unreadable
	}

	return currentInfo.InstanceID == l.instanceID
}

// heartbeatLoop periodically updates the heartbeat timestamp.
func (l *Lock) heartbeatLoop() {
	defer close(l.doneCh)

	ticker := time.NewTicker(DefaultHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.mu.Lock()
			if !l.released {
				l.info.Heartbeat = time.Now()
				l.writeLockFileLocked()
			}
			l.mu.Unlock()
		}
	}
}

// writeLockFileLocked writes the lock file (must be called with l.mu held).
func (l *Lock) writeLockFileLocked() error {
	lockPath := l.store.lockPath(l.sessionID)
	return l.store.writeLockFile(lockPath, l.info)
}

// Done returns a channel that is closed when the lock is released or stolen.
func (l *Lock) Done() <-chan struct{} {
	return l.stopCh
}

// StartWatcher starts a goroutine that monitors for lock theft.
// The returned channel will receive true if the lock is stolen, then close.
func (l *Lock) StartWatcher(checkInterval time.Duration) <-chan bool {
	stolenCh := make(chan bool, 1)

	go func() {
		defer close(stolenCh)

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-l.stopCh:
				return
			case <-ticker.C:
				if !l.IsValid() {
					stolenCh <- true
					return
				}
			}
		}
	}()

	return stolenCh
}

// --- Helper types for lock status checking ---

// LockCheckResult contains the result of checking a session's lock status.
type LockCheckResult struct {
	IsLocked     bool
	IsStale      bool
	CanResume    bool      // Can resume without force
	CanForce     bool      // Can force resume (idle or stale)
	CanInterrupt bool      // Can interrupt (always true, but requires confirmation)
	LockInfo     *LockInfo // nil if not locked
	Message      string    // Human-readable status message
}

// CheckLockStatus checks the lock status of a session and returns detailed information.
func (s *Store) CheckLockStatus(sessionID string) (*LockCheckResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, ErrStoreClosed
	}

	// Check if session exists
	if !s.sessionExistsLocked(sessionID) {
		return nil, ErrSessionNotFound
	}

	result := &LockCheckResult{
		CanInterrupt: true, // Always possible with --force-interrupt
	}

	lockPath := s.lockPath(sessionID)
	info, err := s.readLockFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No lock file - session is available
			result.CanResume = true
			result.CanForce = true
			result.Message = "Session is not locked and can be resumed"
			return result, nil
		}
		return nil, fmt.Errorf("failed to read lock file: %w", err)
	}

	result.IsLocked = true
	result.LockInfo = &info
	result.IsStale = info.IsStale(DefaultStaleTimeout)

	currentHostname := getHostname()

	// Check if the owning process is dead (crashed without cleanup)
	if info.IsProcessDead(currentHostname) {
		result.CanResume = true // Can resume directly, process is dead
		result.CanForce = true
		result.IsStale = true // Treat as stale
		result.Message = fmt.Sprintf("Session lock owner (PID %d) is no longer running - safe to resume (process crashed)", info.PID)
		return result, nil
	}

	if result.IsStale {
		result.CanResume = false // Still need --force for stale locks
		result.CanForce = true
		staleDuration := time.Since(info.Heartbeat).Round(time.Second)
		result.Message = fmt.Sprintf("Session appears stale (no heartbeat for %v) - safe to force resume", staleDuration)
		return result, nil
	}

	// Lock is active
	switch info.Status {
	case LockStatusIdle:
		result.CanResume = false // Need --force even for idle
		result.CanForce = true
		result.Message = fmt.Sprintf("Session is locked by %s (PID %d) but idle - can force resume",
			info.ClientType, info.PID)
	case LockStatusProcessing:
		result.CanForce = false
		result.Message = fmt.Sprintf("Session is locked by %s (PID %d) and actively processing - cannot steal (agent is working). Use --force-interrupt to interrupt.",
			info.ClientType, info.PID)
		if info.StatusMessage != "" {
			result.Message += fmt.Sprintf(" Status: %s", info.StatusMessage)
		}
	case LockStatusWaitingPermission:
		result.CanForce = false
		result.Message = fmt.Sprintf("Session is locked by %s (PID %d) and waiting for permission - cannot steal. Use --force-interrupt to interrupt.",
			info.ClientType, info.PID)
		if info.StatusMessage != "" {
			result.Message += fmt.Sprintf(" Waiting for: %s", info.StatusMessage)
		}
	default:
		result.CanForce = false
		result.Message = fmt.Sprintf("Session is locked by %s (PID %d) with unknown status: %s",
			info.ClientType, info.PID, info.Status)
	}

	return result, nil
}
