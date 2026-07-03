package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/inercia/mitto/internal/fileutil"
)

// tasksBaselineFileName is the per-session file (alongside periodic.json) that
// persists the raw beads snapshot the onTasks trigger last considered "current"
// for that conversation — i.e. its diff baseline.
const tasksBaselineFileName = "tasks_baseline.json"

// ErrTasksBaselineNotFound is returned when no onTasks baseline has been
// captured yet for a session.
var ErrTasksBaselineNotFound = errors.New("tasks baseline not found")

// TasksBaseline is the persisted onTasks diff baseline for a single
// conversation. RawSnapshot holds the raw JSON rows returned by
// `bd list --json --all -n 0` (the same shape internal/config.ParseTasksSnapshot
// consumes) at the time the baseline was captured.
//
// The baseline intentionally stores raw JSON rather than a parsed
// *config.TasksSnapshot: TasksSnapshot's byID index (used by config.DiffTasks)
// is unexported, so persisting the parsed struct directly would silently lose
// the index across a restart. Re-parsing via config.ParseTasksSnapshot on load
// always rebuilds the index correctly.
type TasksBaseline struct {
	// CapturedAt is when this baseline snapshot was captured.
	CapturedAt time.Time `json:"captured_at"`
	// RawSnapshot is the raw `bd list` JSON output at capture time.
	RawSnapshot json.RawMessage `json:"raw_snapshot"`
}

// TasksBaselineStore manages the onTasks diff baseline file for a single
// session directory. Unlike PeriodicStore, it carries no in-memory mutex —
// each instance is short-lived (created fresh per call) and writes go through
// fileutil.WriteJSONAtomic, which is safe for concurrent writers at the
// filesystem level (rename-based atomic replace).
type TasksBaselineStore struct {
	sessionDir string
}

// NewTasksBaselineStore creates a TasksBaselineStore for the given session directory.
func NewTasksBaselineStore(sessionDir string) *TasksBaselineStore {
	return &TasksBaselineStore{sessionDir: sessionDir}
}

// path returns the path to the tasks_baseline.json file.
func (bs *TasksBaselineStore) path() string {
	return filepath.Join(bs.sessionDir, tasksBaselineFileName)
}

// Get retrieves the current onTasks baseline. Returns ErrTasksBaselineNotFound
// if no baseline has been captured yet.
func (bs *TasksBaselineStore) Get() (*TasksBaseline, error) {
	var b TasksBaseline
	if err := fileutil.ReadJSON(bs.path(), &b); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrTasksBaselineNotFound
		}
		return nil, fmt.Errorf("failed to read tasks baseline file: %w", err)
	}
	return &b, nil
}

// Set captures raw as the new baseline, stamped with the current time.
func (bs *TasksBaselineStore) Set(raw []byte) error {
	b := TasksBaseline{
		CapturedAt:  time.Now().UTC(),
		RawSnapshot: json.RawMessage(raw),
	}
	if err := fileutil.WriteJSONAtomic(bs.path(), &b, 0644); err != nil {
		return fmt.Errorf("failed to write tasks baseline file: %w", err)
	}
	return nil
}
