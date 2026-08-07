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
	return fileutil.WriteJSONAtomic(path, &entries, 0644)
}
