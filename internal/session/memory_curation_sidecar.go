package session

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// MemoryCurationSidecarVersion is the current memory-curation.json schema version.
const MemoryCurationSidecarVersion = 1

// MemoryCurationState is the workspace-scoped memory-curation.json ledger
// (mitto-1kl) that gates curate-memories-on-close dispatch by interval
// and/or change-count threshold instead of firing on every conversation
// close. One file per workspace UUID under appdir.MemoryCurationStateDir() —
// deliberately NOT a per-session sidecar like CloseRouterState: global
// memory curation is a workspace-wide concern independent of any single
// conversation's lifecycle, so keying it by session would recreate the very
// "runs on every close" problem this ledger exists to fix.
type MemoryCurationState struct {
	// Version is the schema version (MemoryCurationSidecarVersion).
	Version int `json:"version"`
	// LastRunAt is when the last maintenance run completed. Zero means no
	// run has ever completed for this workspace — the interval gate passes
	// immediately (backward-compatible default for fresh/upgraded installs).
	LastRunAt time.Time `json:"last_run_at,omitempty"`
	// LastRunMemoryCount is the bd memory-store key count observed at the
	// end of the last completed run. Compared against the current count to
	// decide the change-threshold gate.
	LastRunMemoryCount int `json:"last_run_memory_count,omitempty"`
	// InFlightRunID is non-empty while a maintenance run has been dispatched
	// but not yet completed — the mechanism used to coalesce concurrent
	// closes into at most one dispatch. Cleared on completion; a marker
	// older than the caller's lease window is treated as a crashed run and
	// overwritten by a fresh dispatch.
	InFlightRunID string `json:"in_flight_run_id,omitempty"`
	// InFlightStartedAt is when the in-flight run was dispatched, used to
	// detect a stale/crashed marker.
	InFlightStartedAt time.Time `json:"in_flight_started_at,omitempty"`
}

var memoryCurationLocksMu sync.Mutex
var memoryCurationLocks = make(map[string]*sync.Mutex)

// memoryCurationLockFor returns the process-wide mutex shared by every
// caller targeting path, mirroring pendingDispatchLockFor
// (internal/processors/pending_dispatch.go): Read/Write below may be called
// from independently-constructed *processors.Manager instances, so an
// instance-local mutex cannot protect the shared file.
func memoryCurationLockFor(path string) *sync.Mutex {
	key := filepath.Clean(path)
	memoryCurationLocksMu.Lock()
	defer memoryCurationLocksMu.Unlock()
	if mu := memoryCurationLocks[key]; mu != nil {
		return mu
	}
	mu := &sync.Mutex{}
	memoryCurationLocks[key] = mu
	return mu
}

func memoryCurationStatePath(baseDir, workspaceUUID string) (string, error) {
	if baseDir == "" {
		dir, err := appdir.MemoryCurationStateDir()
		if err != nil {
			return "", err
		}
		baseDir = dir
	}
	return filepath.Join(baseDir, workspaceUUID+".json"), nil
}

// ReadMemoryCurationState reads the memory-curation.json ledger for
// workspaceUUID. baseDir overrides the resolved directory (empty uses
// appdir.MemoryCurationStateDir(); tests pass a t.TempDir()). A missing file
// is the expected first-run state (zero-value + current schema version), not
// an error. An empty workspaceUUID returns the zero-value state without
// touching disk.
func ReadMemoryCurationState(baseDir, workspaceUUID string) (MemoryCurationState, error) {
	state := MemoryCurationState{Version: MemoryCurationSidecarVersion}
	if workspaceUUID == "" {
		return state, nil
	}
	path, err := memoryCurationStatePath(baseDir, workspaceUUID)
	if err != nil {
		return MemoryCurationState{}, err
	}
	mu := memoryCurationLockFor(path)
	mu.Lock()
	defer mu.Unlock()
	if err := fileutil.ReadJSON(path, &state); err != nil {
		if os.IsNotExist(err) {
			return MemoryCurationState{Version: MemoryCurationSidecarVersion}, nil
		}
		return MemoryCurationState{}, err
	}
	if state.Version == 0 {
		state.Version = MemoryCurationSidecarVersion
	}
	return state, nil
}

// WriteMemoryCurationState atomically persists state as the
// memory-curation.json ledger for workspaceUUID. baseDir overrides the
// resolved directory as in ReadMemoryCurationState. A no-op when
// workspaceUUID is empty.
func WriteMemoryCurationState(baseDir, workspaceUUID string, state MemoryCurationState) error {
	if workspaceUUID == "" {
		return nil
	}
	if state.Version == 0 {
		state.Version = MemoryCurationSidecarVersion
	}
	path, err := memoryCurationStatePath(baseDir, workspaceUUID)
	if err != nil {
		return err
	}
	mu := memoryCurationLockFor(path)
	mu.Lock()
	defer mu.Unlock()
	return fileutil.WriteJSONAtomic(path, &state, 0600)
}
