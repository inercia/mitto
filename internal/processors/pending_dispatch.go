package processors

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// pendingDispatchMaxAttempts caps how many times a single spooled entry may
// be re-appended after a failed retry (mitto-yfv8). Sequential flushing
// (FlushPendingDispatches) means a poison entry that fails every time still
// costs at most its own dispatchWithRetry budget per flush before this cap
// drops it, instead of blocking other entries indefinitely.
const pendingDispatchMaxAttempts = 3

// pendingDispatchMaxAge bounds how old a spooled entry may be before it is
// discarded at load time (mitto-yfv8). A close-phase batch (e.g. memory
// extraction) that is now this stale is no longer worth re-running.
const pendingDispatchMaxAge = 24 * time.Hour

// pendingDispatchMaxEntries caps the number of entries retained per
// workspace spool (mitto-yfv8). Append drops the oldest entries first once
// the cap is exceeded, bounding spool growth independent of the age cap.
const pendingDispatchMaxEntries = 32

// PendingDispatchEntry captures one undelivered prompt-mode processor batch
// that could not be dispatched within dispatchWithRetry's retry budget.
// Persisted so the work is retried later instead of permanently lost
// (mitto-3421).
type PendingDispatchEntry struct {
	// WorkspaceUUID is the workspace the batch was destined for.
	WorkspaceUUID string `json:"workspace_uuid"`
	// Name is the processor name, or "+"-joined combined name for a batched
	// dispatch (see dispatchPromptBatch).
	Name string `json:"name"`
	// Prompt is the fully-assembled prompt text that was never delivered.
	Prompt string `json:"prompt"`
	// TimeoutSeconds is the per-dispatch timeout that was in effect.
	TimeoutSeconds float64 `json:"timeout_seconds"`
	// SavedAt is when dispatchWithRetry gave up and persisted this entry.
	SavedAt time.Time `json:"saved_at"`
	// LastError is the final dispatch error's message, for diagnostics.
	LastError string `json:"last_error,omitempty"`
	// Attempts counts how many times this entry has been spooled (i.e. how
	// many flush-and-retry cycles have failed for it), starting at 1 when
	// first persisted. FlushPendingDispatches drops an entry once this
	// exceeds pendingDispatchMaxAttempts (mitto-yfv8).
	Attempts int `json:"attempts,omitempty"`
}

// PendingDispatchStore persists undelivered prompt-mode batches keyed by
// workspace so they survive the originating session disappearing (e.g.
// archive_reason=deleted removes the session directory synchronously — see
// internal/web/handlers/session_delete.go — well before a saturated dispatch
// gives up minutes later) and can be inspected/retried once the workspace
// becomes dispatchable again.
type PendingDispatchStore interface {
	// Append adds one undelivered batch to the workspace's pending spool.
	Append(entry PendingDispatchEntry) error
	// Load returns the workspace's currently spooled entries, oldest first.
	// Entries older than pendingDispatchMaxAge are dropped and never
	// returned; the pruned set is persisted so an all-stale spool is
	// reclaimed rather than lingering forever. Returns a nil/empty slice
	// (not an error) when there is no spool file for the workspace.
	Load(workspaceUUID string) ([]PendingDispatchEntry, error)
	// Replace atomically overwrites the workspace's spool with entries. An
	// empty/nil entries removes the spool file entirely. Used by
	// FlushPendingDispatches to claim the spool before retrying it, and to
	// write back only the entries that failed again.
	Replace(workspaceUUID string, entries []PendingDispatchEntry) error
}

// FilePendingDispatchStore persists entries under
// <BaseDir>/<workspaceUUID>.json, one file per workspace holding a JSON
// array of pending entries (mirrors the appdir.RememberedArgsDir /
// internal/rememberedargs per-workspace-file pattern). BaseDir defaults to
// appdir.PendingProcessorDispatchDir() when empty; tests override it with a
// t.TempDir() so they never touch the real Mitto data directory.
//
// Deliberately independent of any single session's SessionDir: a session
// being closed with archive_reason=deleted has its on-disk directory removed
// synchronously by the HTTP handler (internal/web/handlers/session_delete.go
// calls store.Delete right after firing the close-phase pipeline), while
// dispatchWithRetry's saturation give-up can take up to
// dispatchSaturationMaxWait (minutes) to fire — so a file written into
// SessionDir at give-up time would already be deleted (or fail to write at
// all, since the parent directory is gone) for exactly the archive reason
// this bug report is about.
type FilePendingDispatchStore struct {
	// BaseDir overrides the resolved directory. Empty uses
	// appdir.PendingProcessorDispatchDir().
	BaseDir string

	mu sync.Mutex
}

func (s *FilePendingDispatchStore) baseDir() (string, error) {
	if s.BaseDir != "" {
		return s.BaseDir, nil
	}
	return appdir.PendingProcessorDispatchDir()
}

// Append implements PendingDispatchStore.
func (s *FilePendingDispatchStore) Append(entry PendingDispatchEntry) error {
	if entry.WorkspaceUUID == "" {
		return fmt.Errorf("pending dispatch entry missing workspace UUID")
	}
	if entry.Attempts < 1 {
		entry.Attempts = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.baseDir()
	if err != nil {
		return fmt.Errorf("failed to resolve pending dispatch directory: %w", err)
	}
	path := filepath.Join(dir, entry.WorkspaceUUID+".json")

	var entries []PendingDispatchEntry
	if readErr := fileutil.ReadJSON(path, &entries); readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("failed to read existing pending dispatch spool: %w", readErr)
	}
	entries = append(entries, entry)
	// Bound spool growth: drop the oldest entries first once over the cap
	// (mitto-yfv8), independent of the age cap applied at Load time.
	if len(entries) > pendingDispatchMaxEntries {
		entries = entries[len(entries)-pendingDispatchMaxEntries:]
	}
	return fileutil.WriteJSONAtomic(path, &entries, 0644)
}

// Load implements PendingDispatchStore. Entries older than
// pendingDispatchMaxAge are dropped from the returned slice and the pruned
// set is written back under the same lock — otherwise a spool whose entries
// have all aged out is never returned to (and never claimed by)
// FlushPendingDispatches, so its file would linger on disk indefinitely
// despite the age cap.
func (s *FilePendingDispatchStore) Load(workspaceUUID string) ([]PendingDispatchEntry, error) {
	if workspaceUUID == "" {
		return nil, fmt.Errorf("pending dispatch load missing workspace UUID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.baseDir()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve pending dispatch directory: %w", err)
	}
	path := filepath.Join(dir, workspaceUUID+".json")

	var entries []PendingDispatchEntry
	if readErr := fileutil.ReadJSON(path, &entries); readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read pending dispatch spool: %w", readErr)
	}

	cutoff := time.Now().Add(-pendingDispatchMaxAge)
	total := len(entries)
	fresh := entries[:0]
	for _, e := range entries {
		if e.SavedAt.Before(cutoff) {
			continue
		}
		fresh = append(fresh, e)
	}
	// Persist the pruned set best-effort: a write failure only means the
	// stale entries are re-pruned on the next Load, never that a fresh
	// entry is lost, so the caller still gets the fresh set.
	if len(fresh) != total {
		_ = s.writeLocked(path, fresh)
	}
	return fresh, nil
}

// Replace implements PendingDispatchStore. An empty/nil entries removes the
// spool file entirely rather than writing an empty JSON array, keeping an
// idle workspace's spool directory clean.
func (s *FilePendingDispatchStore) Replace(workspaceUUID string, entries []PendingDispatchEntry) error {
	if workspaceUUID == "" {
		return fmt.Errorf("pending dispatch replace missing workspace UUID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir, err := s.baseDir()
	if err != nil {
		return fmt.Errorf("failed to resolve pending dispatch directory: %w", err)
	}
	path := filepath.Join(dir, workspaceUUID+".json")

	return s.writeLocked(path, entries)
}

// writeLocked persists entries to path, removing the file entirely when
// there is nothing left to keep. Callers must hold s.mu.
func (s *FilePendingDispatchStore) writeLocked(path string, entries []PendingDispatchEntry) error {
	if len(entries) == 0 {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("failed to remove pending dispatch spool: %w", rmErr)
		}
		return nil
	}
	return fileutil.WriteJSONAtomic(path, &entries, 0644)
}
