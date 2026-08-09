//go:build integration

// Package inprocess contains in-process integration tests for Mitto.
package inprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/beads"
	"github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/pkg/api"
)

// fakeOnTasksBeadsClient is a minimal beads.Client fake that lets the test
// control the raw `bd list --json` output returned to the onTasks runner, per
// working directory. Only List is meaningful for onTasks; every other method
// is a no-op stub required to satisfy the beads.Client interface.
type fakeOnTasksBeadsClient struct {
	mu  sync.Mutex
	raw map[string][]byte
}

func newFakeOnTasksBeadsClient() *fakeOnTasksBeadsClient {
	return &fakeOnTasksBeadsClient{raw: map[string][]byte{}}
}

func (c *fakeOnTasksBeadsClient) setRaw(dir string, raw []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw[dir] = raw
}

func (c *fakeOnTasksBeadsClient) List(_ context.Context, dir string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if raw, ok := c.raw[dir]; ok {
		return raw, nil
	}
	return []byte(`[]`), nil
}

func (c *fakeOnTasksBeadsClient) Ready(context.Context, string) ([]byte, error) {
	return []byte(`[]`), nil
}
func (c *fakeOnTasksBeadsClient) Status(context.Context, string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeOnTasksBeadsClient) Statuses(context.Context, string, []string) (map[string]string, error) {
	return nil, nil
}
func (c *fakeOnTasksBeadsClient) Show(context.Context, string, string) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeOnTasksBeadsClient) Create(context.Context, string, beads.CreateParams) ([]byte, error) {
	return []byte(`{}`), nil
}
func (c *fakeOnTasksBeadsClient) Delete(context.Context, string, string) error { return nil }
func (c *fakeOnTasksBeadsClient) ListClosedIDs(context.Context, string) ([]string, error) {
	return nil, nil
}
func (c *fakeOnTasksBeadsClient) DeleteIDs(context.Context, string, []string) error       { return nil }
func (c *fakeOnTasksBeadsClient) SetStatus(context.Context, string, string, string) error { return nil }
func (c *fakeOnTasksBeadsClient) Update(context.Context, string, beads.UpdateParams) error {
	return nil
}
func (c *fakeOnTasksBeadsClient) Comment(context.Context, string, string, string) error { return nil }
func (c *fakeOnTasksBeadsClient) Dep(context.Context, string, beads.DepParams) error    { return nil }
func (c *fakeOnTasksBeadsClient) Label(context.Context, string, beads.LabelParams) error {
	return nil
}
func (c *fakeOnTasksBeadsClient) ListAllLabels(context.Context, string) ([]byte, error) {
	return []byte(`[]`), nil
}
func (c *fakeOnTasksBeadsClient) ConfigShow(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (c *fakeOnTasksBeadsClient) ConfigSet(context.Context, string, string, string) error { return nil }
func (c *fakeOnTasksBeadsClient) ConfigUnset(context.Context, string, string) error       { return nil }
func (c *fakeOnTasksBeadsClient) EnsureInitialized(context.Context, string) error         { return nil }
func (c *fakeOnTasksBeadsClient) Sync(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (c *fakeOnTasksBeadsClient) Bootstrap(context.Context, string) ([]byte, error) {
	return nil, nil
}
func (c *fakeOnTasksBeadsClient) MigrateRemote(context.Context, string) ([]byte, error) {
	return nil, nil
}

// onTasksIssue builds a single raw beads-list row understood by
// config.ParseTasksSnapshot (see internal/config/tasks_condition.go).
func onTasksIssue(id, issueType, status string, priority int, labels []string, updatedAt string) map[string]any {
	return map[string]any{
		"id": id, "issue_type": issueType, "status": status,
		"priority": priority, "labels": labels, "updated_at": updatedAt,
	}
}

func marshalOnTasksIssues(t *testing.T, rows ...map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal issues: %v", err)
	}
	return raw
}

func onTasksChangeEvent(dir string) watcher.BeadsChangeEvent {
	return watcher.BeadsChangeEvent{WorkingDirs: []string{dir}}
}

// onTasksIssuesJSONEqual reports whether a and b decode to the same list of
// issue rows, ignoring whitespace/formatting differences (the persisted
// baseline is pretty-printed; test fixtures are compact).
func onTasksIssuesJSONEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var da, db []map[string]any
	if err := json.Unmarshal(a, &da); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &db); err != nil {
		return false
	}
	if len(da) != len(db) {
		return false
	}
	na, _ := json.Marshal(da)
	nb, _ := json.Marshal(db)
	return bytes.Equal(na, nb)
}

// createOnTasksSession creates a session rooted at workingDir (created if
// missing) with an enabled onTasks loop prompt gated by condition.
// Additional SetLoopRequest fields (MaxIterations, CooldownSeconds, ...)
// can be set via opts.
func createOnTasksSession(t *testing.T, ts *TestServer, workingDir, name, condition string, opts ...func(*client.SetLoopRequest)) *client.SessionInfo {
	t.Helper()
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", workingDir, err)
	}
	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: name, WorkingDir: workingDir})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	req := client.SetLoopRequest{Prompt: "iterate", Trigger: "onTasks", Condition: condition, Enabled: true}
	for _, opt := range opts {
		opt(&req)
	}
	cfg, err := ts.Client.SetLoop(sess.SessionID, req)
	if err != nil {
		t.Fatalf("SetLoop failed: %v", err)
	}
	if cfg.Trigger != "onTasks" {
		t.Fatalf("expected trigger=onTasks, got %q", cfg.Trigger)
	}
	if !cfg.Enabled {
		t.Fatalf("expected enabled=true after SetLoop, got false")
	}
	return sess
}

func getOnTasksLoop(t *testing.T, ts *TestServer, sessionID string) *client.LoopConfig {
	t.Helper()
	got, err := ts.Client.GetLoop(sessionID)
	if err != nil {
		t.Fatalf("GetLoop(%s) error = %v", sessionID, err)
	}
	return got
}

func assertOnTasksIterationCount(t *testing.T, ts *TestServer, sessionID string, want int) {
	t.Helper()
	if got := getOnTasksLoop(t, ts, sessionID).IterationCount; got != want {
		t.Fatalf("iteration_count = %d, want %d", got, want)
	}
}

func waitOnTasksIterationCount(t *testing.T, ts *TestServer, sessionID string, want int) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		got, err := ts.Client.GetLoop(sessionID)
		return err == nil && got.IterationCount == want
	}, fmt.Sprintf("iteration_count to reach %d for session %s", want, sessionID))
}

func waitOnTasksSessionIdle(t *testing.T, ts *TestServer, sessionID string) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		bs := ts.Server.GetSessionManager().GetSession(sessionID)
		return bs != nil && !bs.IsPrompting()
	}, "session "+sessionID+" to go idle")
}

// TestLoopOnTasksE2E verifies the onTasks loop trigger end-to-end
// against the mock ACP server: CEL-gated firing, the 3-layer loop-prevention
// system (busy guard, quiescence rebase, cooldown floor), and
// MaxIterations/MaxDuration auto-stop.
//
// The `.beads/` filesystem watcher itself is out of scope here (unit-tested
// separately in internal/config); this test drives the same entry point the
// watcher uses — LoopRunner.OnBeadsChanged — directly, with a fake
// beads.Client standing in for `bd list`.
func TestLoopOnTasksE2E(t *testing.T) {
	ts := SetupTestServer(t)
	runner := ts.Server.LoopRunner()

	fake := newFakeOnTasksBeadsClient()
	runner.SetBeadsClient(fake)
	// Keep the global cooldown floor at 0 so per-session CooldownSeconds (or its
	// absence) fully controls timing in each subtest; use a short quiescence
	// window so the busy-guard/rebase subtest doesn't need to wait 30s.
	runner.SetMinLoopTasksCooldownSeconds(0)
	runner.SetTasksQuiescenceWindow(400 * time.Millisecond)

	// -------------------------------------------------------------------------
	// Subtest 1: empty condition fires on ANY material beads change, but the
	// very first OnBeadsChanged call for a session only captures the baseline
	// (no spurious first run).
	// -------------------------------------------------------------------------
	t.Run("empty_condition_fires_on_change_not_on_initial", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-empty")
		sess := createOnTasksSession(t, ts, dir, "ontasks-empty", "")
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-e-1", "task", "open", 2, nil, "2026-07-01T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
	})

	// -------------------------------------------------------------------------
	// Subtest 2: canonical CEL example — open bug count increased.
	// -------------------------------------------------------------------------
	t.Run("condition_open_bug_count_increased", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-bugcount")
		sess := createOnTasksSession(t, ts, dir, "ontasks-bugcount",
			`Tasks.OpenByType["bug"] > Prev.OpenByType["bug"]`)
		defer ts.Client.DeleteSession(sess.SessionID)

		// The baseline must already contain an open "bug" so the CEL map index
		// `OpenByType["bug"]` doesn't hit a missing key (native CEL maps error,
		// not default-to-zero, on a missing key — see
		// TestTasksEvaluator_FailClosed in internal/config/tasks_condition_test.go).
		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-bug-0", "bug", "open", 1, nil, "2026-07-01T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		// A second open bug: OpenByType["bug"] 1 -> 2, condition true, should fire.
		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-bug-0", "bug", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-bug-1", "bug", "open", 1, nil, "2026-07-01T00:00:01Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, sess.SessionID)

		// Adding a non-bug issue does not change OpenByType["bug"]; condition
		// false, must NOT fire.
		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-bug-0", "bug", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-bug-1", "bug", "open", 1, nil, "2026-07-01T00:00:01Z"),
			onTasksIssue("mitto-task-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		time.Sleep(300 * time.Millisecond)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 1)
	})

	// -------------------------------------------------------------------------
	// Subtest 3: canonical CEL example — a label was added to a touched issue.
	// -------------------------------------------------------------------------
	t.Run("condition_label_created_or_updated", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-label")
		sess := createOnTasksSession(t, ts, dir, "ontasks-label",
			`Changes.Touched.exists(i, "PR opened" in i.labels)`)
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		// New issue without the label: condition false, must NOT fire.
		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-pr-1", "task", "open", 2, []string{"other"}, "2026-07-01T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		time.Sleep(300 * time.Millisecond)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		// The same issue gains the "PR opened" label: condition true, should fire.
		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-pr-1", "task", "open", 2, []string{"other", "PR opened"}, "2026-07-02T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
	})

	// -------------------------------------------------------------------------
	// Subtest 4: Layer 1 (busy guard) defers an event that arrives while the
	// conversation is still processing a prior fire; Layer 2 (quiescence
	// rebase) then absorbs that "self-edit" into the baseline once idle, so it
	// is never evaluated as a delta and never causes a spurious extra fire.
	// -------------------------------------------------------------------------
	t.Run("busy_guard_defers_and_quiescence_rebase_absorbs_self_edit", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-busyguard")
		sess := createOnTasksSession(t, ts, dir, "ontasks-busyguard", "")
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		v1 := marshalOnTasksIssues(t, onTasksIssue("mitto-bg-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"))
		fake.setRaw(dir, v1)
		runner.OnBeadsChanged(onTasksChangeEvent(dir)) // fire 1 kicked off (async)

		// Simulate a self-edit landing WHILE the run is still busy: TriggerNow
		// sets isPrompting synchronously before returning, so calling
		// OnBeadsChanged again right away (before waiting for fire 1 to
		// complete) reliably lands inside the busy window.
		v2 := marshalOnTasksIssues(t,
			onTasksIssue("mitto-bg-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-bg-2", "task", "open", 1, nil, "2026-07-01T00:00:01Z"))
		fake.setRaw(dir, v2)
		runner.OnBeadsChanged(onTasksChangeEvent(dir)) // Layer 1: should defer (busy), not fire again.

		// Fire 1 completing (and ONLY fire 1) confirms the busy guard held —
		// had v2 also fired, iteration_count would reach 2 instead.
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, sess.SessionID)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 1)

		// After idle + the quiescence window, the baseline rebases to v2,
		// absorbing the self-edit without having fired for it. Compare
		// semantically (decoded), not byte-for-byte: the persisted baseline is
		// pretty-printed by fileutil.WriteJSONAtomic, unlike the compact v2.
		waitFor(t, 5*time.Second, func() bool {
			bl, err := conversation.NewTasksBaselineStore(ts.Store.SessionDir(sess.SessionID)).Get()
			return err == nil && onTasksIssuesJSONEqual(t, []byte(bl.RawSnapshot), v2)
		}, "baseline to rebase to v2 after idle+quiescence")

		// Re-delivering v2 again (no real change relative to the now-rebased
		// baseline) must NOT fire.
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		time.Sleep(300 * time.Millisecond)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 1)

		// A genuinely new change on top of the rebased baseline fires again.
		v3 := marshalOnTasksIssues(t,
			onTasksIssue("mitto-bg-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-bg-2", "task", "open", 1, nil, "2026-07-01T00:00:01Z"),
			onTasksIssue("mitto-bg-3", "task", "open", 1, nil, "2026-07-01T00:00:02Z"))
		fake.setRaw(dir, v3)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 2)
	})

	// -------------------------------------------------------------------------
	// Subtest 4b: the busy-guard extends to the whole subtree — an event
	// arriving while a DELEGATED CHILD is busy (but the outer itself is idle)
	// must still defer, and the baseline must still rebase once the child
	// (and therefore the whole subtree) goes idle.
	// -------------------------------------------------------------------------
	t.Run("busy_guard_defers_when_child_in_subtree_is_busy", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-subtree")
		sess := createOnTasksSession(t, ts, dir, "ontasks-subtree", "")
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		// Fire once with v1 and let the outer go fully idle again.
		v1 := marshalOnTasksIssues(t,
			onTasksIssue("mitto-st-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"))
		fake.setRaw(dir, v1)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, sess.SessionID)

		// Register a delegated child of the outer directly in the store so
		// FindAllChildrenRecursive picks it up, and inject a fake prompting
		// BackgroundSession under the same ID so isTasksSubtreeBusy reads it
		// as busy without spinning up a real ACP child.
		childID := session.GenerateSessionID()
		if err := ts.Store.Create(session.Metadata{
			SessionID:       childID,
			ACPServer:       "test-server",
			WorkingDir:      dir,
			Name:            "Busy Subtree Child",
			ParentSessionID: sess.SessionID,
		}); err != nil {
			t.Fatalf("Create child metadata failed: %v", err)
		}
		sm := ts.Server.GetSessionManager()
		sm.AddSessionForTest(conversation.NewMinimalBackgroundSessionPrompting(childID, true))
		// The fake child bypasses normal lifecycle, so remove it from the
		// SessionManager BEFORE the outer DeleteSession defer runs the
		// cascade-delete path (defers execute LIFO).
		defer sm.RemoveSession(childID)

		// v2 arrives while the CHILD is busy (outer itself is idle). Because
		// the subtree is busy, the runner must defer and arm a rebase timer
		// instead of firing a second iteration.
		v2 := marshalOnTasksIssues(t,
			onTasksIssue("mitto-st-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-st-2", "task", "open", 1, nil, "2026-07-01T00:00:01Z"))
		fake.setRaw(dir, v2)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		time.Sleep(300 * time.Millisecond)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 1)

		// Clear the child's busy flag by reinjecting a not-prompting fake
		// under the same session ID (AddSessionForTest keys by session ID and
		// overwrites in place).
		sm.AddSessionForTest(conversation.NewMinimalBackgroundSessionPrompting(childID, false))

		// Once the subtree is fully idle, the armed timer rebases the
		// baseline to v2 — the child-side "self-edit" is absorbed without
		// ever having fired for it.
		waitFor(t, 5*time.Second, func() bool {
			bl, err := conversation.NewTasksBaselineStore(ts.Store.SessionDir(sess.SessionID)).Get()
			return err == nil && onTasksIssuesJSONEqual(t, []byte(bl.RawSnapshot), v2)
		}, "baseline to rebase to v2 after subtree idle+quiescence")

		// Re-delivering v2 (matches the rebased baseline) must NOT fire.
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		time.Sleep(300 * time.Millisecond)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 1)

		// A genuinely new change on top of the rebased baseline fires again.
		v3 := marshalOnTasksIssues(t,
			onTasksIssue("mitto-st-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-st-2", "task", "open", 1, nil, "2026-07-01T00:00:01Z"),
			onTasksIssue("mitto-st-3", "task", "open", 1, nil, "2026-07-01T00:00:02Z"))
		fake.setRaw(dir, v3)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 2)
	})

	// -------------------------------------------------------------------------
	// Subtest 5: Layer 0 (per-conversation cooldown floor) blocks a rapid
	// re-fire within the configured window, then allows it once elapsed.
	// -------------------------------------------------------------------------
	t.Run("cooldown_floor_blocks_rapid_refire", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-cooldown")
		sess := createOnTasksSession(t, ts, dir, "ontasks-cooldown", "",
			func(r *client.SetLoopRequest) { r.CooldownSeconds = 2 })
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		v1 := marshalOnTasksIssues(t, onTasksIssue("mitto-cd-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"))
		fake.setRaw(dir, v1)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, sess.SessionID)

		// A further material change within the 2s cooldown must NOT fire.
		v2 := marshalOnTasksIssues(t,
			onTasksIssue("mitto-cd-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-cd-2", "task", "open", 1, nil, "2026-07-01T00:00:01Z"))
		fake.setRaw(dir, v2)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		time.Sleep(500 * time.Millisecond)
		assertOnTasksIterationCount(t, ts, sess.SessionID, 1)

		// Once the cooldown has elapsed, re-evaluating the same pending change fires.
		time.Sleep(2 * time.Second)
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 2)
	})

	// -------------------------------------------------------------------------
	// Subtest 6: MaxIterations auto-stop, mirroring the onCompletion trigger's
	// hard backstop (Layer 0).
	// -------------------------------------------------------------------------
	t.Run("max_iterations_auto_stop", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-maxiter")
		sess := createOnTasksSession(t, ts, dir, "ontasks-maxiter", "",
			func(r *client.SetLoopRequest) { r.MaxIterations = 1 })
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-mi-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))

		waitFor(t, 10*time.Second, func() bool {
			return !getOnTasksLoop(t, ts, sess.SessionID).Enabled
		}, "onTasks loop to auto-stop after max_iterations")

		got := getOnTasksLoop(t, ts, sess.SessionID)
		if got.IterationCount != 1 {
			t.Errorf("iteration_count = %d, want 1", got.IterationCount)
		}
		if got.StoppedReason != "maxIterations" {
			t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, "maxIterations")
		}
	})

	// -------------------------------------------------------------------------
	// Subtest 8: MaxDurationSeconds auto-stop — the wall-clock cap is checked
	// at the next firing and, if exceeded, disables WITHOUT delivering.
	// -------------------------------------------------------------------------
	t.Run("max_duration_auto_stop", func(t *testing.T) {
		dir := filepath.Join(ts.TempDir, "workspace", "ontasks-maxdur")
		sess := createOnTasksSession(t, ts, dir, "ontasks-maxdur", "",
			func(r *client.SetLoopRequest) { r.MaxDurationSeconds = 1 })
		defer ts.Client.DeleteSession(sess.SessionID)

		fake.setRaw(dir, marshalOnTasksIssues(t))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		assertOnTasksIterationCount(t, ts, sess.SessionID, 0)

		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-md-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))
		waitOnTasksIterationCount(t, ts, sess.SessionID, 1)
		waitOnTasksSessionIdle(t, ts, sess.SessionID)

		// Let the 1s max-duration cap elapse since FirstRunAt was recorded.
		time.Sleep(1200 * time.Millisecond)

		fake.setRaw(dir, marshalOnTasksIssues(t,
			onTasksIssue("mitto-md-1", "task", "open", 1, nil, "2026-07-01T00:00:00Z"),
			onTasksIssue("mitto-md-2", "task", "open", 1, nil, "2026-07-01T00:00:01Z")))
		runner.OnBeadsChanged(onTasksChangeEvent(dir))

		waitFor(t, 10*time.Second, func() bool {
			return !getOnTasksLoop(t, ts, sess.SessionID).Enabled
		}, "onTasks loop to auto-stop after max_duration")

		got := getOnTasksLoop(t, ts, sess.SessionID)
		if got.IterationCount != 1 {
			t.Errorf("iteration_count = %d, want 1 (no second delivery)", got.IterationCount)
		}
		if got.StoppedReason != "maxDuration" {
			t.Errorf("StoppedReason = %q, want %q", got.StoppedReason, "maxDuration")
		}
	})
}
