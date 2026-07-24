// Tests for the "beads_issues_reached_state" branch of mitto_conversation_wait.
// Verifies: fast path (predicate already satisfied), deadline-driven slow path
// (predicate satisfied after a subsequent Statuses call), timeout, the "any"
// match strategy, input validation errors, and the fs-event slow path — real
// BeadsWatcher wake-up on a .beads/ change, correct subscribe/unsubscribe
// lifecycle (no watcher touch on fast path, no leaked subscriber on
// completion or context cancellation).
package mcpserver

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/beads"
	beadswatcher "github.com/inercia/mitto/internal/beads/watcher"
	"github.com/inercia/mitto/internal/session"
)

// fakeBeadsClient is a beads.Client stub used only by the wait-branch tests.
// Only Statuses is exercised; the rest satisfy the interface and panic if a
// regression starts relying on them.
type fakeBeadsClient struct {
	mu        sync.Mutex
	states    map[string]string
	callCount atomic.Int64
	err       error
}

func newFakeBeadsClient(initial map[string]string) *fakeBeadsClient {
	cp := make(map[string]string, len(initial))
	for k, v := range initial {
		cp[k] = v
	}
	return &fakeBeadsClient{states: cp}
}

func (f *fakeBeadsClient) set(id, status string) {
	f.mu.Lock()
	f.states[id] = status
	f.mu.Unlock()
}

func (f *fakeBeadsClient) Statuses(_ context.Context, _ string, ids []string) (map[string]string, error) {
	f.callCount.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if st, ok := f.states[id]; ok {
			out[id] = st
		}
	}
	return out, nil
}

func (f *fakeBeadsClient) List(context.Context, string) ([]byte, error)   { panic("List: not used") }
func (f *fakeBeadsClient) Ready(context.Context, string) ([]byte, error)  { panic("Ready: not used") }
func (f *fakeBeadsClient) Status(context.Context, string) ([]byte, error) { panic("Status: not used") }
func (f *fakeBeadsClient) Show(context.Context, string, string) ([]byte, error) {
	panic("Show: not used")
}
func (f *fakeBeadsClient) Create(context.Context, string, beads.CreateParams) ([]byte, error) {
	panic("Create: not used")
}
func (f *fakeBeadsClient) Delete(context.Context, string, string) error {
	panic("Delete: not used")
}
func (f *fakeBeadsClient) ListClosedIDs(context.Context, string) ([]string, error) {
	panic("ListClosedIDs: not used")
}
func (f *fakeBeadsClient) DeleteIDs(context.Context, string, []string) error {
	panic("DeleteIDs: not used")
}
func (f *fakeBeadsClient) SetStatus(context.Context, string, string, string) error {
	panic("SetStatus: not used")
}
func (f *fakeBeadsClient) Update(context.Context, string, beads.UpdateParams) error {
	panic("Update: not used")
}
func (f *fakeBeadsClient) Comment(context.Context, string, string, string) error {
	panic("Comment: not used")
}
func (f *fakeBeadsClient) Dep(context.Context, string, beads.DepParams) error {
	panic("Dep: not used")
}
func (f *fakeBeadsClient) Label(context.Context, string, beads.LabelParams) error {
	panic("Label: not used")
}
func (f *fakeBeadsClient) ListAllLabels(context.Context, string) ([]byte, error) {
	panic("ListAllLabels: not used")
}
func (f *fakeBeadsClient) ConfigShow(context.Context, string) (map[string]string, error) {
	panic("ConfigShow: not used")
}
func (f *fakeBeadsClient) ConfigSet(context.Context, string, string, string) error {
	panic("ConfigSet: not used")
}
func (f *fakeBeadsClient) ConfigUnset(context.Context, string, string) error {
	panic("ConfigUnset: not used")
}
func (f *fakeBeadsClient) EnsureInitialized(context.Context, string) error {
	panic("EnsureInitialized: not used")
}
func (f *fakeBeadsClient) Sync(context.Context, string, string, string) (string, error) {
	panic("Sync: not used")
}
func (f *fakeBeadsClient) MigrateRemote(context.Context, string) ([]byte, error) {
	panic("MigrateRemote: not used")
}
func (f *fakeBeadsClient) Bootstrap(context.Context, string) ([]byte, error) {
	panic("Bootstrap: not used")
}

var _ beads.Client = (*fakeBeadsClient)(nil)

// setupBeadsWait builds a server with a running caller session, a fake beads
// client, and no BeadsWatcher (the poll/deadline path is exercised).
func setupBeadsWait(t *testing.T, initial map[string]string) (*Server, string, *fakeBeadsClient) {
	t.Helper()
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(false)
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	fake := newFakeBeadsClient(initial)
	srv.SetBeadsClient(fake)
	return srv, callerID, fake
}

func TestConversationWait_BeadsFastPath_All_Satisfied(t *testing.T) {
	srv, callerID, fake := setupBeadsWait(t, map[string]string{
		"mitto-1": "closed",
		"mitto-2": "closed",
	})
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-1", "mitto-2"},
		BeadsTargetState: "closed",
	})
	if err != nil {
		t.Fatalf("handleConversationWait: %v", err)
	}
	if output.Error != "" {
		t.Fatalf("unexpected error: %s", output.Error)
	}
	if !output.Success || output.TimedOut {
		t.Fatalf("expected success/no-timeout, got %+v", output)
	}
	if len(output.ReachedIssues) != 2 {
		t.Fatalf("expected 2 reached, got %v", output.ReachedIssues)
	}
	if got := fake.callCount.Load(); got != 1 {
		t.Errorf("expected 1 Statuses call (fast path), got %d", got)
	}
}

func TestConversationWait_BeadsFastPath_Any_Satisfied(t *testing.T) {
	srv, callerID, _ := setupBeadsWait(t, map[string]string{
		"mitto-1": "open",
		"mitto-2": "closed",
		"mitto-3": "open",
	})
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-1", "mitto-2", "mitto-3"},
		BeadsTargetState: "closed",
		BeadsMatch:       "any",
	})
	if err != nil {
		t.Fatalf("handleConversationWait: %v", err)
	}
	if !output.Success || output.TimedOut {
		t.Fatalf("expected success/no-timeout, got %+v", output)
	}
	if len(output.ReachedIssues) != 1 || output.ReachedIssues[0] != "mitto-2" {
		t.Errorf("expected reached=[mitto-2], got %v", output.ReachedIssues)
	}
	if len(output.PendingIssues) != 2 {
		t.Errorf("expected 2 pending, got %v", output.PendingIssues)
	}
}

func TestConversationWait_BeadsSlowPath_Transition(t *testing.T) {
	srv, callerID, fake := setupBeadsWait(t, map[string]string{
		"mitto-1": "open",
	})

	// Flip status to closed shortly after the wait starts so the final
	// deadline re-evaluation observes it.
	go func() {
		time.Sleep(50 * time.Millisecond)
		fake.set("mitto-1", "closed")
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan ConversationWaitOutput, 1)
	go func() {
		_, out, _ := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
			SelfID:           callerID,
			ConversationID:   "self",
			What:             "beads_issues_reached_state",
			BeadsIssues:      []string{"mitto-1"},
			BeadsTargetState: "closed",
			TimeoutSeconds:   1, // deadline re-eval will pick up the flipped state
		})
		done <- out
	}()

	select {
	case out := <-done:
		if !out.Success {
			t.Fatalf("expected success, got %+v", out)
		}
		if len(out.ReachedIssues) != 1 || out.ReachedIssues[0] != "mitto-1" {
			t.Errorf("expected reached=[mitto-1], got %v (timed_out=%v)",
				out.ReachedIssues, out.TimedOut)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return within 3s")
	}
}

func TestConversationWait_BeadsTimeout(t *testing.T) {
	srv, callerID, _ := setupBeadsWait(t, map[string]string{
		"mitto-1": "open",
	})
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-1"},
		BeadsTargetState: "closed",
		TimeoutSeconds:   1,
	})
	if err != nil {
		t.Fatalf("handleConversationWait: %v", err)
	}
	if !output.TimedOut {
		t.Fatalf("expected timed_out=true, got %+v", output)
	}
	if len(output.PendingIssues) != 1 {
		t.Errorf("expected 1 pending on timeout, got %v", output.PendingIssues)
	}
	if got := output.CurrentStates["mitto-1"]; got != "open" {
		t.Errorf("expected current_states[mitto-1]=open, got %q", got)
	}
}

func TestConversationWait_BeadsValidation_MissingIssues(t *testing.T) {
	srv, callerID, _ := setupBeadsWait(t, map[string]string{})
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsTargetState: "closed",
	})
	if err != nil {
		t.Fatalf("handleConversationWait: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected validation error for missing beads_issues")
	}
}

func TestConversationWait_BeadsValidation_InvalidMatch(t *testing.T) {
	srv, callerID, _ := setupBeadsWait(t, map[string]string{"mitto-1": "open"})
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-1"},
		BeadsTargetState: "closed",
		BeadsMatch:       "bogus",
	})
	if err != nil {
		t.Fatalf("handleConversationWait: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected validation error for invalid beads_match")
	}
}

func TestConversationWait_BeadsNoClient(t *testing.T) {
	targetID := session.GenerateSessionID()
	mockBS := newMockBackgroundSessionForWait(false)
	srv, callerID := setupServerForWait(t, targetID, mockBS)
	ctx := context.Background()

	_, output, err := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-1"},
		BeadsTargetState: "closed",
	})
	if err != nil {
		t.Fatalf("handleConversationWait: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error when beads client is not wired")
	}
}

// setupBeadsWaitWithWatcher wires the wait-branch fake client together with a
// real BeadsWatcher rooted at a fresh temp workspace whose caller session's
// WorkingDir points at that workspace's root. The workspace's .beads/ dir is
// created empty so the watcher can add its fsnotify watch immediately (the
// parent-fallback path is exercised by its own unit tests in the watcher
// package). Returns the workspace root so tests can drive fs events by writing
// into <workspace>/.beads/.
func setupBeadsWaitWithWatcher(
	t *testing.T,
	initial map[string]string,
) (srv *Server, callerID, workspace string, fake *fakeBeadsClient, bw *beadswatcher.BeadsWatcher) {
	t.Helper()

	srv, callerID, fake = setupBeadsWait(t, initial)

	// Point the caller session at a real temp workspace so the handler
	// resolves a valid working directory whose .beads/ dir we can mutate.
	workspace = t.TempDir()
	beadsDir := filepath.Join(workspace, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := srv.store.UpdateMetadata(callerID, func(m *session.Metadata) {
		m.WorkingDir = workspace
	}); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	// Real watcher with a short debounce so the test does not sit on the
	// 750 ms production default.
	var err error
	bw, err = beadswatcher.NewBeadsWatcher(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatalf("NewBeadsWatcher: %v", err)
	}
	bw.SetDebounceDelay(20 * time.Millisecond)
	bw.SetMaxWait(100 * time.Millisecond)
	bw.Start()
	t.Cleanup(func() { _ = bw.Close() })

	srv.SetBeadsWatcher(bw)
	return srv, callerID, workspace, fake, bw
}

// touchBeadsLastTouched writes <workspace>/.beads/last-touched with the given
// bytes — the canonical fsnotify trigger used by the beads watcher.
func touchBeadsLastTouched(t *testing.T, workspace string, payload string) {
	t.Helper()
	path := filepath.Join(workspace, ".beads", "last-touched")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write last-touched: %v", err)
	}
}

// TestConversationWait_BeadsSlowPath_WatcherWake verifies the fs-event slow
// path: the handler subscribes to the BeadsWatcher, flipping the fake client's
// state and touching .beads/last-touched wakes it up, and it returns success
// well before either the deadline or the 30 s poll fallback.
func TestConversationWait_BeadsSlowPath_WatcherWake(t *testing.T) {
	srv, callerID, workspace, fake, _ := setupBeadsWaitWithWatcher(t, map[string]string{
		"mitto-1": "open",
	})

	// Flip status then trigger the watcher shortly after the wait starts.
	go func() {
		time.Sleep(75 * time.Millisecond)
		fake.set("mitto-1", "closed")
		touchBeadsLastTouched(t, workspace, "1")
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan ConversationWaitOutput, 1)
	go func() {
		_, out, _ := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
			SelfID:           callerID,
			ConversationID:   "self",
			What:             "beads_issues_reached_state",
			BeadsIssues:      []string{"mitto-1"},
			BeadsTargetState: "closed",
			// Longer than the deadline-only slow-path test: if the watcher
			// wake fires correctly the handler returns immediately; if it
			// silently misses the event we'll still exit via the deadline
			// re-evaluation but the assertion on Statuses call count below
			// will fail (fast-path + one wake-driven eval = 2, poll-only
			// deadline path would evaluate once at deadline == 1).
			TimeoutSeconds: 4,
		})
		done <- out
	}()

	select {
	case out := <-done:
		if !out.Success || out.TimedOut {
			t.Fatalf("expected success/no-timeout, got %+v", out)
		}
		if len(out.ReachedIssues) != 1 || out.ReachedIssues[0] != "mitto-1" {
			t.Errorf("expected reached=[mitto-1], got %v", out.ReachedIssues)
		}
		// Fast-path (1) + at least one wake-driven re-eval (>=1) = >=2.
		// The 30 s poll fallback cannot fire in a 4 s wait, so any count
		// beyond the fast-path evaluation proves the watcher path drove
		// the wake-up.
		if got := fake.callCount.Load(); got < 2 {
			t.Errorf("expected >=2 Statuses calls (fast-path + watcher wake), got %d", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return within 3s — watcher wake likely missed")
	}
}

// TestConversationWait_BeadsWatcher_UnsubscribesOnCompletion asserts that when
// the fast path returns immediately, no subscriber is left registered on the
// watcher (defer bw.Unsubscribe runs only when Subscribe succeeded — the fast
// path returns before Subscribe is called, so this covers the "no leak on
// fast path" case). It also covers the slow path returning after a watcher
// wake: subscription must be released before the handler returns.
func TestConversationWait_BeadsWatcher_UnsubscribesOnCompletion(t *testing.T) {
	// Case 1: fast-path never subscribes.
	srv, callerID, _, _, bw := setupBeadsWaitWithWatcher(t, map[string]string{
		"mitto-1": "closed",
	})
	before := bw.SubscriberCount()

	_, out, err := srv.handleConversationWait(context.Background(), nil, ConversationWaitInput{
		SelfID:           callerID,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-1"},
		BeadsTargetState: "closed",
	})
	if err != nil || !out.Success {
		t.Fatalf("fast-path wait: err=%v out=%+v", err, out)
	}
	if got := bw.SubscriberCount(); got != before {
		t.Errorf("fast path must not touch the watcher; subs before=%d after=%d", before, got)
	}

	// Case 2: slow path subscribes then unsubscribes when the predicate is
	// satisfied via a watcher wake.
	srv2, callerID2, workspace2, fake2, bw2 := setupBeadsWaitWithWatcher(t, map[string]string{
		"mitto-2": "open",
	})
	before2 := bw2.SubscriberCount()

	go func() {
		time.Sleep(75 * time.Millisecond)
		fake2.set("mitto-2", "closed")
		touchBeadsLastTouched(t, workspace2, "1")
	}()

	_, out2, err2 := srv2.handleConversationWait(context.Background(), nil, ConversationWaitInput{
		SelfID:           callerID2,
		ConversationID:   "self",
		What:             "beads_issues_reached_state",
		BeadsIssues:      []string{"mitto-2"},
		BeadsTargetState: "closed",
		TimeoutSeconds:   4,
	})
	if err2 != nil || !out2.Success || out2.TimedOut {
		t.Fatalf("slow-path wait: err=%v out=%+v", err2, out2)
	}
	if got := bw2.SubscriberCount(); got != before2 {
		t.Errorf("slow path leaked subscriber; subs before=%d after=%d", before2, got)
	}
}

// TestConversationWait_BeadsSlowPath_ContextCancel drives the slow path and
// then cancels the caller's context. The handler must exit cleanly with an
// error output (not a panic, not a leak) and must not report success.
func TestConversationWait_BeadsSlowPath_ContextCancel(t *testing.T) {
	srv, callerID, _, _, bw := setupBeadsWaitWithWatcher(t, map[string]string{
		"mitto-1": "open",
	})
	before := bw.SubscriberCount()

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel shortly after the handler enters its slow-path select.
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()

	done := make(chan ConversationWaitOutput, 1)
	go func() {
		_, out, _ := srv.handleConversationWait(ctx, nil, ConversationWaitInput{
			SelfID:           callerID,
			ConversationID:   "self",
			What:             "beads_issues_reached_state",
			BeadsIssues:      []string{"mitto-1"},
			BeadsTargetState: "closed",
			TimeoutSeconds:   30,
		})
		done <- out
	}()

	select {
	case out := <-done:
		if out.Success {
			t.Fatalf("expected non-success on ctx cancel, got %+v", out)
		}
		if out.Error == "" {
			t.Fatalf("expected error message on ctx cancel, got %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return within 2s after ctx cancel")
	}

	// Give the deferred Unsubscribe time to run (it fires before the return
	// but SubscriberCount reads a lock the same goroutine has just released).
	time.Sleep(20 * time.Millisecond)
	if got := bw.SubscriberCount(); got != before {
		t.Errorf("ctx-cancel leaked subscriber; subs before=%d after=%d", before, got)
	}
}
