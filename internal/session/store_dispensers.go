// Dispenser methods on *Store that construct session-scoped sub-stores
// (Queue, ActionButtons, Loop, Callback). Grouped here so store.go stays
// focused on core CRUD.
package session

// Queue returns a Queue instance for managing the message queue of a session.
// The returned Queue is safe for concurrent use, including across
// independently-constructed *Queue values pointed at the same session
// directory (mitto-pr0): the mutex is shared via a process-wide registry
// keyed on the resolved session directory, see queueLockFor in queue.go.
func (s *Store) Queue(sessionID string) *Queue {
	return NewQueue(s.sessionDir(sessionID))
}

// ActionButtons returns an ActionButtonsStore instance for managing action buttons of a session.
// The returned ActionButtonsStore is safe for concurrent use.
func (s *Store) ActionButtons(sessionID string) *ActionButtonsStore {
	return NewActionButtonsStore(s.sessionDir(sessionID))
}

// Loop returns a LoopStore instance for managing the loop prompt of a session.
// The returned LoopStore is safe for concurrent use. It is wired to notify
// this Store's loop-stopped observer (see SetLoopStoppedObserver) when the
// session's loop transitions from enabled to stopped.
func (s *Store) Loop(sessionID string) *LoopStore {
	s.mu.RLock()
	obs := s.loopStoppedObserver
	s.mu.RUnlock()
	return newLoopStoreWithObserver(s.sessionDir(sessionID), sessionID, obs)
}

// Callback returns a CallbackStore instance for managing the callback token of a session.
// The returned CallbackStore is safe for concurrent use.
func (s *Store) Callback(sessionID string) *CallbackStore {
	return NewCallbackStore(s.sessionDir(sessionID))
}
