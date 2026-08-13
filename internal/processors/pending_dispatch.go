package processors

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

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
	// ID uniquely identifies this persisted batch across persistence, retry,
	// delivery, and bounded-drop diagnostics (mitto-gfr1).
	ID string `json:"id"`
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

// PendingDispatchAppendResult reports the persisted entry (including its ID)
// and any oldest entries evicted by the bounded spool cap.
type PendingDispatchAppendResult struct {
	Entry   PendingDispatchEntry
	Dropped []PendingDispatchEntry
}

// PendingDispatchClaim is one atomic snapshot removed from the spool. Expired
// entries are returned separately so callers can emit auditable drop logs.
type PendingDispatchClaim struct {
	Entries []PendingDispatchEntry
	Expired []PendingDispatchEntry
}

// PendingDispatchStore persists undelivered prompt-mode batches keyed by
// workspace so they survive the originating session disappearing (e.g.
// archive_reason=deleted removes the session directory synchronously — see
// internal/web/handlers/session_delete.go — well before a saturated dispatch
// gives up minutes later) and can be inspected/retried once the workspace
// becomes dispatchable again.
type PendingDispatchStore interface {
	// Append adds one undelivered batch to the workspace's pending spool.
	Append(entry PendingDispatchEntry) (PendingDispatchAppendResult, error)
	// Claim atomically removes and returns the workspace's current spool.
	Claim(workspaceUUID string) (PendingDispatchClaim, error)
	// Requeue prepends unresolved claimed entries to anything appended after
	// the claim, preserving both sets without holding a lock during dispatch.
	Requeue(workspaceUUID string, entries []PendingDispatchEntry) ([]PendingDispatchEntry, error)
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
}

var pendingDispatchLocksMu sync.Mutex
var pendingDispatchLocks = make(map[string]*sync.Mutex)

// pendingDispatchLockFor returns the process-wide mutex shared by every store
// instance targeting path. FilePendingDispatchStore instances are constructed
// independently for live and close-phase managers, so an instance-local mutex
// cannot protect their shared read-modify-write transaction (mitto-gfr1).
func pendingDispatchLockFor(path string) *sync.Mutex {
	key := filepath.Clean(path)
	pendingDispatchLocksMu.Lock()
	defer pendingDispatchLocksMu.Unlock()
	if mu := pendingDispatchLocks[key]; mu != nil {
		return mu
	}
	mu := &sync.Mutex{}
	pendingDispatchLocks[key] = mu
	return mu
}

func newPendingDispatchID() string { return uuid.NewString() }

func (s *FilePendingDispatchStore) baseDir() (string, error) {
	if s.BaseDir != "" {
		return s.BaseDir, nil
	}
	return appdir.PendingProcessorDispatchDir()
}

func (s *FilePendingDispatchStore) spoolPath(workspaceUUID string) (string, error) {
	dir, err := s.baseDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve pending dispatch directory: %w", err)
	}
	return filepath.Join(dir, workspaceUUID+".json"), nil
}

// Append implements PendingDispatchStore.
func (s *FilePendingDispatchStore) Append(entry PendingDispatchEntry) (PendingDispatchAppendResult, error) {
	if entry.WorkspaceUUID == "" {
		return PendingDispatchAppendResult{}, fmt.Errorf("pending dispatch entry missing workspace UUID")
	}
	if entry.Attempts < 1 {
		entry.Attempts = 1
	}
	if entry.ID == "" {
		entry.ID = newPendingDispatchID()
	}
	path, err := s.spoolPath(entry.WorkspaceUUID)
	if err != nil {
		return PendingDispatchAppendResult{}, err
	}
	mu := pendingDispatchLockFor(path)
	mu.Lock()
	defer mu.Unlock()

	entries, _, readErr := s.readLocked(path)
	if readErr != nil {
		return PendingDispatchAppendResult{}, readErr
	}
	entries = append(entries, entry)
	entries, dropped := boundPendingDispatchEntries(entries)
	if err := s.writeLocked(path, entries); err != nil {
		return PendingDispatchAppendResult{}, err
	}
	return PendingDispatchAppendResult{Entry: entry, Dropped: dropped}, nil
}

// Load returns the currently pending entries for inspection and tests. Entries older than
// pendingDispatchMaxAge are dropped from the returned slice and the pruned
// set is written back under the same lock — otherwise a spool whose entries
// have all aged out is never returned to (and never claimed by)
// FlushPendingDispatches, so its file would linger on disk indefinitely
// despite the age cap.
func (s *FilePendingDispatchStore) Load(workspaceUUID string) ([]PendingDispatchEntry, error) {
	if workspaceUUID == "" {
		return nil, fmt.Errorf("pending dispatch load missing workspace UUID")
	}

	path, err := s.spoolPath(workspaceUUID)
	if err != nil {
		return nil, err
	}
	mu := pendingDispatchLockFor(path)
	mu.Lock()
	defer mu.Unlock()

	entries, IDsAdded, readErr := s.readLocked(path)
	if readErr != nil {
		return nil, readErr
	}
	fresh, expired := partitionPendingDispatchEntries(entries)
	// Persist the pruned set best-effort: a write failure only means the
	// stale entries are re-pruned on the next Load, never that a fresh
	// entry is lost, so the caller still gets the fresh set.
	if IDsAdded || len(expired) > 0 {
		_ = s.writeLocked(path, fresh)
	}
	return fresh, nil
}

// Claim implements PendingDispatchStore. Reading and removing happen under
// one process-wide path lock, so an Append is either included in this claim or
// lands in a new spool after it; it can never be deleted between Load and
// Replace as in the old two-operation protocol (mitto-gfr1).
func (s *FilePendingDispatchStore) Claim(workspaceUUID string) (PendingDispatchClaim, error) {
	if workspaceUUID == "" {
		return PendingDispatchClaim{}, fmt.Errorf("pending dispatch claim missing workspace UUID")
	}
	path, err := s.spoolPath(workspaceUUID)
	if err != nil {
		return PendingDispatchClaim{}, err
	}
	mu := pendingDispatchLockFor(path)
	mu.Lock()
	defer mu.Unlock()

	entries, _, readErr := s.readLocked(path)
	if readErr != nil {
		return PendingDispatchClaim{}, readErr
	}
	if len(entries) == 0 {
		return PendingDispatchClaim{}, nil
	}
	fresh, expired := partitionPendingDispatchEntries(entries)
	if err := s.writeLocked(path, nil); err != nil {
		return PendingDispatchClaim{}, fmt.Errorf("failed to remove claimed pending dispatch spool: %w", err)
	}
	return PendingDispatchClaim{Entries: fresh, Expired: expired}, nil
}

// Requeue implements PendingDispatchStore. Unresolved claimed entries are
// older, so they are prepended to entries appended after Claim. The shared
// path lock makes this a single merge transaction across store instances.
func (s *FilePendingDispatchStore) Requeue(workspaceUUID string, entries []PendingDispatchEntry) ([]PendingDispatchEntry, error) {
	if workspaceUUID == "" {
		return nil, fmt.Errorf("pending dispatch requeue missing workspace UUID")
	}
	if len(entries) == 0 {
		return nil, nil
	}
	ensurePendingDispatchIDs(entries)
	path, err := s.spoolPath(workspaceUUID)
	if err != nil {
		return nil, err
	}
	mu := pendingDispatchLockFor(path)
	mu.Lock()
	defer mu.Unlock()

	current, _, readErr := s.readLocked(path)
	if readErr != nil {
		return nil, readErr
	}
	merged := append(append(make([]PendingDispatchEntry, 0, len(entries)+len(current)), entries...), current...)
	merged, dropped := boundPendingDispatchEntries(merged)
	if err := s.writeLocked(path, merged); err != nil {
		return nil, err
	}
	return dropped, nil
}

// Replace overwrites the spool for setup and maintenance callers. An empty/nil entries removes the
// spool file entirely rather than writing an empty JSON array, keeping an
// idle workspace's spool directory clean.
func (s *FilePendingDispatchStore) Replace(workspaceUUID string, entries []PendingDispatchEntry) error {
	if workspaceUUID == "" {
		return fmt.Errorf("pending dispatch replace missing workspace UUID")
	}

	path, err := s.spoolPath(workspaceUUID)
	if err != nil {
		return err
	}
	mu := pendingDispatchLockFor(path)
	mu.Lock()
	defer mu.Unlock()
	ensurePendingDispatchIDs(entries)
	return s.writeLocked(path, entries)
}

func (s *FilePendingDispatchStore) readLocked(path string) ([]PendingDispatchEntry, bool, error) {
	var entries []PendingDispatchEntry
	if err := fileutil.ReadJSON(path, &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to read pending dispatch spool: %w", err)
	}
	return entries, ensurePendingDispatchIDs(entries), nil
}

func ensurePendingDispatchIDs(entries []PendingDispatchEntry) bool {
	changed := false
	for i := range entries {
		if entries[i].ID == "" {
			entries[i].ID = newPendingDispatchID()
			changed = true
		}
	}
	return changed
}

func partitionPendingDispatchEntries(entries []PendingDispatchEntry) (fresh, expired []PendingDispatchEntry) {
	cutoff := time.Now().Add(-pendingDispatchMaxAge)
	for _, entry := range entries {
		if entry.SavedAt.Before(cutoff) {
			expired = append(expired, entry)
		} else {
			fresh = append(fresh, entry)
		}
	}
	return fresh, expired
}

func boundPendingDispatchEntries(entries []PendingDispatchEntry) (kept, dropped []PendingDispatchEntry) {
	if len(entries) <= pendingDispatchMaxEntries {
		return entries, nil
	}
	excess := len(entries) - pendingDispatchMaxEntries
	dropped = append([]PendingDispatchEntry(nil), entries[:excess]...)
	return entries[excess:], dropped
}

// writeLocked persists entries to path, removing the file entirely when
// there is nothing left to keep. Callers must hold pendingDispatchLockFor(path).
func (s *FilePendingDispatchStore) writeLocked(path string, entries []PendingDispatchEntry) error {
	if len(entries) == 0 {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			return fmt.Errorf("failed to remove pending dispatch spool: %w", rmErr)
		}
		return nil
	}
	return fileutil.WriteJSONAtomic(path, &entries, 0644)
}
