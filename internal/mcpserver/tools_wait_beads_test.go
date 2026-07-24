// Tests for the "beads_issues_reached_state" branch of mitto_conversation_wait.
// Verifies: fast path (predicate already satisfied), slow path (predicate
// satisfied after a subsequent Statuses call), timeout, the "any" match
// strategy, and input validation errors.
package mcpserver

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/beads"
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
