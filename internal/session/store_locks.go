package session

import "sync"

// storeSessionLock serializes I/O for one session. refs counts both holders and
// waiters so the registry never creates two lock identities for the same ID.
type storeSessionLock struct {
	mu   sync.RWMutex
	refs int
}

// lockSessionRead holds the Store lifecycle gate and the session read lock.
// Lock order is Store.mu -> sessionLocksMu -> storeSessionLock.mu.
func (s *Store) lockSessionRead(sessionID string) (func(), error) {
	entry, err := s.retainSessionLock(sessionID)
	if err != nil {
		return nil, err
	}
	entry.mu.RLock()
	return func() {
		entry.mu.RUnlock()
		s.releaseSessionLock(sessionID, entry)
	}, nil
}

// lockSessionWrite holds the Store lifecycle gate and the session write lock.
func (s *Store) lockSessionWrite(sessionID string) (func(), error) {
	entry, err := s.retainSessionLock(sessionID)
	if err != nil {
		return nil, err
	}
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		s.releaseSessionLock(sessionID, entry)
	}, nil
}

func (s *Store) retainSessionLock(sessionID string) (*storeSessionLock, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrStoreClosed
	}

	s.sessionLocksMu.Lock()
	if s.sessionLocks == nil {
		s.sessionLocks = make(map[string]*storeSessionLock)
	}
	entry := s.sessionLocks[sessionID]
	if entry == nil {
		entry = &storeSessionLock{}
		s.sessionLocks[sessionID] = entry
	}
	entry.refs++
	s.sessionLocksMu.Unlock()
	return entry, nil
}

// releaseSessionLock runs after the session mutex has been released. Deleting
// an entry before that point could let a new caller create a second mutex while
// the old one is still held.
func (s *Store) releaseSessionLock(sessionID string, entry *storeSessionLock) {
	s.sessionLocksMu.Lock()
	entry.refs--
	if entry.refs == 0 {
		delete(s.sessionLocks, sessionID)
	}
	s.sessionLocksMu.Unlock()
	s.mu.RUnlock()
}
