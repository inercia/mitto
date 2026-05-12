package processors

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

const processorStateFileName = "processor_state.json"

// ProcessorCadenceState tracks cumulative counts for cadence throttling of one processor.
// Counters are incremented every AgentResponded turn; reset whenever the processor fires.
type ProcessorCadenceState struct {
	// TurnsSinceLastFire is the number of AgentResponded turns since this processor last fired.
	// Reset to 0 each time the processor fires.
	TurnsSinceLastFire int `json:"turns_since_last_fire"`
	// TokensSinceLastFire is the cumulative token count since this processor last fired.
	// Reset to 0 each time the processor fires.
	TokensSinceLastFire int64 `json:"tokens_since_last_fire"`
	// LastFiredAt is when this processor last fired (zero if it has never fired).
	LastFiredAt time.Time `json:"last_fired_at,omitempty"`
}

// ProcessorStateData is the JSON-serializable state for all processors in a session.
// Stored in <session_dir>/processor_state.json.
type ProcessorStateData struct {
	// AgentResponseCount is the total number of ApplyAfter calls for this session.
	// Used for match:first semantics: the processor runs only when count == 0.
	AgentResponseCount int `json:"agent_response_count"`
	// Processors maps processor name → its cadence state.
	// Processors without cadence config do not appear here.
	Processors map[string]*ProcessorCadenceState `json:"processors,omitempty"`
}

// StateStore is the interface for loading and saving processor state across session restarts.
type StateStore interface {
	// Load reads the persisted state for the given session directory.
	// Returns a non-nil zero-value state if the file does not exist yet.
	Load(sessionDir string) (*ProcessorStateData, error)
	// Save atomically persists the state to the given session directory.
	Save(sessionDir string, state *ProcessorStateData) error
}

// FileStateStore persists processor state in <session_dir>/processor_state.json.
// It uses atomic writes to prevent corruption on crash.
type FileStateStore struct{}

func (s *FileStateStore) Load(sessionDir string) (*ProcessorStateData, error) {
	state := &ProcessorStateData{
		Processors: make(map[string]*ProcessorCadenceState),
	}
	path := filepath.Join(sessionDir, processorStateFileName)
	err := fileutil.ReadJSON(path, state)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil // first run — return zero-value state
		}
		return nil, err
	}
	if state.Processors == nil {
		state.Processors = make(map[string]*ProcessorCadenceState)
	}
	return state, nil
}

func (s *FileStateStore) Save(sessionDir string, state *ProcessorStateData) error {
	if sessionDir == "" {
		return nil // no-op if session dir is not configured (e.g., tests without persistence)
	}
	path := filepath.Join(sessionDir, processorStateFileName)
	return fileutil.WriteJSONAtomic(path, state, 0644)
}

// MemoryStateStore is a non-persistent in-memory state store for testing.
// It stores state keyed by sessionDir string.
type MemoryStateStore struct {
	mu    sync.Mutex
	store map[string]*ProcessorStateData
}

// NewMemoryStateStore creates a new empty MemoryStateStore.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{store: make(map[string]*ProcessorStateData)}
}

func (s *MemoryStateStore) Load(sessionDir string) (*ProcessorStateData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.store[sessionDir]
	if !ok {
		return &ProcessorStateData{Processors: make(map[string]*ProcessorCadenceState)}, nil
	}
	// Return a deep copy to avoid races.
	return copyState(state), nil
}

func (s *MemoryStateStore) Save(sessionDir string, state *ProcessorStateData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[sessionDir] = copyState(state)
	return nil
}

// copyState returns a deep copy of a ProcessorStateData.
func copyState(src *ProcessorStateData) *ProcessorStateData {
	if src == nil {
		return &ProcessorStateData{Processors: make(map[string]*ProcessorCadenceState)}
	}
	dst := &ProcessorStateData{
		AgentResponseCount: src.AgentResponseCount,
		Processors:         make(map[string]*ProcessorCadenceState, len(src.Processors)),
	}
	for k, v := range src.Processors {
		cp := *v
		dst.Processors[k] = &cp
	}
	return dst
}
