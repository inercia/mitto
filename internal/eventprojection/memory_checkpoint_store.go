package eventprojection

import "sync"

// MemoryCheckpointStore is an in-memory CheckpointStore, useful as a
// deterministic test double and as a "no durable persistence" fallback
// (checkpoints live only as long as the process). The durable,
// session-sidecar-backed implementation lives in the sibling package
// eventprojectionsession.
//
// Safe for concurrent use.
type MemoryCheckpointStore struct {
	mu    sync.Mutex
	byKey map[SourceID]*Checkpoint
}

// NewMemoryCheckpointStore returns an empty MemoryCheckpointStore.
func NewMemoryCheckpointStore() *MemoryCheckpointStore {
	return &MemoryCheckpointStore{byKey: make(map[SourceID]*Checkpoint)}
}

// Load implements CheckpointStore.
func (s *MemoryCheckpointStore) Load(src SourceID) (*Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp, ok := s.byKey[src]
	if !ok {
		return nil, ErrCheckpointNotFound
	}
	return cloneCheckpoint(cp), nil
}

// Save implements CheckpointStore.
func (s *MemoryCheckpointStore) Save(cp *Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[cp.Source] = cloneCheckpoint(cp)
	return nil
}

// cloneCheckpoint returns a deep-enough copy so the store's internal state
// and a caller's live *Checkpoint never alias the same backing map/slice.
func cloneCheckpoint(cp *Checkpoint) *Checkpoint {
	out := &Checkpoint{
		Source:      cp.Source,
		Epoch:       cp.Epoch,
		LastCursor:  cp.LastCursor,
		IdentitySeq: make(map[string]int64, len(cp.IdentitySeq)),
	}
	if len(cp.Committed) > 0 {
		out.Committed = append([]string(nil), cp.Committed...)
	}
	for k, v := range cp.IdentitySeq {
		out.IdentitySeq[k] = v
	}
	return out
}
