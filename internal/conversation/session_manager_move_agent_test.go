package conversation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// newMoveAgentTestManager builds a SessionManager wired with a store and two
// workspaces (agent-a, agent-b) registered for the same workingDir, plus a
// matching mittoConfig with both ACP servers configured. Callers get the
// store back to create/inspect persisted sessions directly.
func newMoveAgentTestManager(t *testing.T, workingDir string) (*SessionManager, *session.Store) {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := NewSessionManagerWithOptions(SessionManagerOptions{
		Workspaces: []config.WorkspaceSettings{
			{UUID: "ws-a", ACPServer: "agent-a", WorkingDir: workingDir},
			{UUID: "ws-b", ACPServer: "agent-b", WorkingDir: workingDir},
		},
		AutoApprove: true,
	})
	sm.SetStore(store)
	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo test"},
			{Name: "agent-b", Command: "echo test"},
		},
	})
	return sm, store
}

func TestSessionManager_MoveSessionToAgent_SessionNotFound(t *testing.T) {
	sm, _ := newMoveAgentTestManager(t, "/tmp/move-agent")

	_, err := sm.MoveSessionToAgent("does-not-exist", "agent-b", MoveAgentOptions{})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("MoveSessionToAgent() error = %v, want session.ErrSessionNotFound", err)
	}
}

func TestSessionManager_MoveSessionToAgent_Archived(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir, Archived: true,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := sm.MoveSessionToAgent("s1", "agent-b", MoveAgentOptions{})
	if !errors.Is(err, ErrMoveAgentArchived) {
		t.Fatalf("MoveSessionToAgent() error = %v, want ErrMoveAgentArchived", err)
	}
}

func TestSessionManager_MoveSessionToAgent_SameAgent(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := sm.MoveSessionToAgent("s1", "agent-a", MoveAgentOptions{})
	if !errors.Is(err, ErrMoveAgentSameAgent) {
		t.Fatalf("MoveSessionToAgent() error = %v, want ErrMoveAgentSameAgent", err)
	}
}

func TestSessionManager_MoveSessionToAgent_UnknownTargetAgent(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := sm.MoveSessionToAgent("s1", "no-such-agent", MoveAgentOptions{})
	if !errors.Is(err, ErrMoveAgentUnknownTarget) {
		t.Fatalf("MoveSessionToAgent() error = %v, want ErrMoveAgentUnknownTarget", err)
	}
}

func TestSessionManager_MoveSessionToAgent_NoWorkspaceForTarget(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	// agent-c is configured globally but has no workspace registered for
	// workingDir, so the move must be rejected.
	sm.SetMittoConfig(&config.Config{
		ACPServers: []config.ACPServer{
			{Name: "agent-a", Command: "echo test"},
			{Name: "agent-b", Command: "echo test"},
			{Name: "agent-c", Command: "echo test"},
		},
	})

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, err := sm.MoveSessionToAgent("s1", "agent-c", MoveAgentOptions{})
	if !errors.Is(err, ErrMoveAgentNoWorkspace) {
		t.Fatalf("MoveSessionToAgent() error = %v, want ErrMoveAgentNoWorkspace", err)
	}
}

func TestSessionManager_MoveSessionToAgent_BusyPrompting(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockBS := NewTestBackgroundSessionPromptingWithCtx("s1", true, ctx, cancel)
	sm.mu.Lock()
	sm.sessions["s1"] = mockBS
	sm.mu.Unlock()

	_, err := sm.MoveSessionToAgent("s1", "agent-b", MoveAgentOptions{})
	if !errors.Is(err, ErrMoveAgentBusy) {
		t.Fatalf("MoveSessionToAgent() error = %v, want ErrMoveAgentBusy", err)
	}

	// Preflight must not have mutated anything.
	m, err := store.GetMetadata("s1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if m.ACPServer != "agent-a" {
		t.Errorf("ACPServer = %q after rejected busy move, want unchanged %q", m.ACPServer, "agent-a")
	}
}

func TestSessionManager_MoveSessionToAgent_BusyWaitingForChildren(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	sm.waitingForChildrenMu.Lock()
	sm.waitingForChildren["s1"] = true
	sm.waitingForChildrenMu.Unlock()

	_, err := sm.MoveSessionToAgent("s1", "agent-b", MoveAgentOptions{})
	if !errors.Is(err, ErrMoveAgentBusy) {
		t.Fatalf("MoveSessionToAgent() error = %v, want ErrMoveAgentBusy", err)
	}
}

// TestSessionManager_MoveSessionToAgent_RewritesMetadataAndRecordsEvent covers
// the happy path: ACPServer is rewritten, agent-specific fields are cleared,
// the previous baseline model is captured, and a session_change (kind
// "agent") event is recorded. Resuming on "agent-b" (command "echo test", not
// a real ACP server) is expected to fail fast; that failure must surface as
// ResumeError without failing the overall move.
func TestSessionManager_MoveSessionToAgent_RewritesMetadataAndRecordsEvent(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID:            "s1",
		ACPServer:            "agent-a",
		WorkingDir:           workingDir,
		Name:                 "Session One",
		ACPSessionID:         "acp-old-session-id",
		CurrentModeID:        "code",
		BaselineModel:        "gpt-old",
		ACPStartFailureCount: 2,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	result, err := sm.MoveSessionToAgent("s1", "agent-b", MoveAgentOptions{CloseTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("MoveSessionToAgent failed: %v", err)
	}

	if result.PreviousAgent != "agent-a" {
		t.Errorf("PreviousAgent = %q, want %q", result.PreviousAgent, "agent-a")
	}
	if result.PreviousBaselineModel != "gpt-old" {
		t.Errorf("PreviousBaselineModel = %q, want %q", result.PreviousBaselineModel, "gpt-old")
	}
	if len(result.Moved) != 1 || result.Moved[0] != "s1" {
		t.Errorf("Moved = %v, want [s1]", result.Moved)
	}
	// The move stands regardless of whether resume against the fake "echo
	// test" agent succeeds; we only assert the metadata/event side effects.

	m, err := store.GetMetadata("s1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if m.ACPServer != "agent-b" {
		t.Errorf("ACPServer = %q, want %q", m.ACPServer, "agent-b")
	}
	if m.ACPSessionID != "" {
		t.Errorf("ACPSessionID = %q, want empty", m.ACPSessionID)
	}
	if m.CurrentModeID != "" {
		t.Errorf("CurrentModeID = %q, want empty", m.CurrentModeID)
	}
	if m.BaselineModel != "" {
		t.Errorf("BaselineModel = %q, want empty", m.BaselineModel)
	}
	if m.PendingModelMappingFrom != "gpt-old" {
		t.Errorf("PendingModelMappingFrom = %q, want %q", m.PendingModelMappingFrom, "gpt-old")
	}
	if m.PendingAgentHandoffFrom != "agent-a" {
		t.Errorf("PendingAgentHandoffFrom = %q, want %q", m.PendingAgentHandoffFrom, "agent-a")
	}
	// ACPStartFailureCount is cleared by the rewrite itself, but the
	// subsequent ResumeSessionBackground attempt against the fake "echo
	// test" agent is expected to fail and legitimately re-increment it via
	// the normal resume-failure bookkeeping (session_manager.go) — that is
	// independent, correct behavior, not something MoveSessionToAgent must
	// suppress. TestSessionManager_MoveSessionToAgent_ClearsACPStartFailureCount
	// below pins the rewrite itself in isolation (no resume attempt).

	events, err := store.ReadEvents("s1")
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	var found *session.SessionChangeData
	for _, ev := range events {
		if ev.Type != session.EventTypeSessionChange {
			continue
		}
		decoded, err := session.DecodeEventData(ev)
		if err != nil {
			t.Fatalf("DecodeEventData failed: %v", err)
		}
		if data, ok := decoded.(session.SessionChangeData); ok && data.Kind == "agent" {
			found = &data
			break
		}
	}
	if found == nil {
		t.Fatal("expected a session_change event with kind \"agent\"")
	}
	if found.Value != "agent-b" || found.PreviousValue != "agent-a" {
		t.Errorf("session_change data = %+v, want Value=agent-b PreviousValue=agent-a", found)
	}
}

// TestSessionManager_MoveSessionToAgent_ClearsACPStartFailureCount pins the
// metadata rewrite step (moveAgentStopAndRebind) in isolation, without
// triggering a real ResumeSessionBackground attempt afterwards — so unlike
// the full happy-path test above, ACPStartFailureCount is asserted to be
// exactly 0 immediately after the rewrite.
func TestSessionManager_MoveSessionToAgent_ClearsACPStartFailureCount(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
		ACPStartFailureCount: 3,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	var result MoveAgentResult
	if err := sm.moveAgentStopAndRebind("s1", "agent-a", "agent-b", 200*time.Millisecond, &result); err != nil {
		t.Fatalf("moveAgentStopAndRebind failed: %v", err)
	}

	m, err := store.GetMetadata("s1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if m.ACPStartFailureCount != 0 {
		t.Errorf("ACPStartFailureCount = %d, want 0", m.ACPStartFailureCount)
	}
	if m.ACPServer != "agent-b" {
		t.Errorf("ACPServer = %q, want %q", m.ACPServer, "agent-b")
	}
}

// TestSessionManager_MoveSessionToAgent_UnreachableOldAgentDoesNotHang verifies
// that a permanently-prompting (unreachable/hung) old-agent BackgroundSession
// does not block the stop-and-rebind step beyond its close timeout:
// CloseSessionGracefully times out and the fallback CloseSession force-closes
// it. Exercised directly against moveAgentStopAndRebind (the step that owns
// this bound) rather than through the public MoveSessionToAgent entry point,
// since the latter's preflight independently and intentionally rejects any
// currently-streaming conversation outright (see
// TestSessionManager_MoveSessionToAgent_BusyPrompting) — the two are
// different lines of defense: preflight fails fast for the common case,
// while this bound protects the (possibly racing, or directly-invoked)
// stop-and-rebind step itself from ever hanging indefinitely.
func TestSessionManager_MoveSessionToAgent_UnreachableOldAgentDoesNotHang(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// A prompting mock session whose prompt never completes, simulating an
	// unreachable/hung old agent.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockBS := NewTestBackgroundSessionPromptingWithCtx("s1", true, ctx, cancel)
	sm.mu.Lock()
	sm.sessions["s1"] = mockBS
	sm.mu.Unlock()

	closeTimeout := 100 * time.Millisecond
	start := time.Now()
	var result MoveAgentResult
	err := sm.moveAgentStopAndRebind("s1", "agent-a", "agent-b", closeTimeout, &result)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("moveAgentStopAndRebind failed: %v", err)
	}
	// Bounded: should complete close to closeTimeout, never hang indefinitely.
	if elapsed > 2*time.Second {
		t.Errorf("moveAgentStopAndRebind took %v, expected bounded by close timeout %v", elapsed, closeTimeout)
	}

	// The mock session must have been force-closed (removed) despite never
	// completing its prompt on its own.
	if sm.GetSession("s1") != nil {
		t.Error("old BackgroundSession should have been force-closed and removed")
	}

	m, err := store.GetMetadata("s1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if m.ACPServer != "agent-b" {
		t.Errorf("ACPServer = %q, want %q", m.ACPServer, "agent-b")
	}
}

// TestSessionManager_MoveSessionToAgent_IncludeChildren covers the
// IncludeChildren option: an idle child bound to the old agent is moved; an
// archived child, a child bound to a different agent, and a busy child are
// all skipped and reported with a reason.
func TestSessionManager_MoveSessionToAgent_IncludeChildren(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "parent", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create(parent) failed: %v", err)
	}
	if err := store.Create(session.Metadata{
		SessionID: "child-eligible", ACPServer: "agent-a", WorkingDir: workingDir,
		ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("Create(child-eligible) failed: %v", err)
	}
	if err := store.Create(session.Metadata{
		SessionID: "child-archived", ACPServer: "agent-a", WorkingDir: workingDir,
		ParentSessionID: "parent", Archived: true,
	}); err != nil {
		t.Fatalf("Create(child-archived) failed: %v", err)
	}
	if err := store.Create(session.Metadata{
		SessionID: "child-other-agent", ACPServer: "agent-b", WorkingDir: workingDir,
		ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("Create(child-other-agent) failed: %v", err)
	}
	if err := store.Create(session.Metadata{
		SessionID: "child-busy", ACPServer: "agent-a", WorkingDir: workingDir,
		ParentSessionID: "parent",
	}); err != nil {
		t.Fatalf("Create(child-busy) failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	busyBS := NewTestBackgroundSessionPromptingWithCtx("child-busy", true, ctx, cancel)
	sm.mu.Lock()
	sm.sessions["child-busy"] = busyBS
	sm.mu.Unlock()

	result, err := sm.MoveSessionToAgent("parent", "agent-b", MoveAgentOptions{
		IncludeChildren: true,
		CloseTimeout:    200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("MoveSessionToAgent failed: %v", err)
	}

	wantMoved := map[string]bool{"parent": true, "child-eligible": true}
	if len(result.Moved) != len(wantMoved) {
		t.Fatalf("Moved = %v, want exactly %v", result.Moved, wantMoved)
	}
	for _, id := range result.Moved {
		if !wantMoved[id] {
			t.Errorf("unexpected session in Moved: %q", id)
		}
	}

	skippedReasons := make(map[string]string, len(result.Skipped))
	for _, sk := range result.Skipped {
		skippedReasons[sk.ID] = sk.Reason
	}
	if _, ok := skippedReasons["child-archived"]; !ok {
		t.Error("expected child-archived to be in Skipped")
	}
	if _, ok := skippedReasons["child-other-agent"]; !ok {
		t.Error("expected child-other-agent to be in Skipped")
	}
	if _, ok := skippedReasons["child-busy"]; !ok {
		t.Error("expected child-busy to be in Skipped")
	}

	// child-eligible was actually rebound.
	m, err := store.GetMetadata("child-eligible")
	if err != nil {
		t.Fatalf("GetMetadata(child-eligible) failed: %v", err)
	}
	if m.ACPServer != "agent-b" {
		t.Errorf("child-eligible ACPServer = %q, want %q", m.ACPServer, "agent-b")
	}
	if m.PendingAgentHandoffFrom != "agent-a" {
		t.Errorf("child-eligible PendingAgentHandoffFrom = %q, want %q (IncludeChildren must set it too)", m.PendingAgentHandoffFrom, "agent-a")
	}

	// The skipped ones were left untouched.
	for id, wantServer := range map[string]string{
		"child-archived":    "agent-a",
		"child-other-agent": "agent-b",
		"child-busy":        "agent-a",
	} {
		m, err := store.GetMetadata(id)
		if err != nil {
			t.Fatalf("GetMetadata(%s) failed: %v", id, err)
		}
		if m.ACPServer != wantServer {
			t.Errorf("%s ACPServer = %q, want unchanged %q", id, m.ACPServer, wantServer)
		}
	}
}

// TestSessionManager_MoveSessionToAgent_LoopJSONUntouched verifies that an
// existing loop.json configuration survives a move byte-for-byte (aside from
// LoopStore.Set's own bookkeeping, which we don't invoke at all — the move
// must not touch the loop sidecar).
func TestSessionManager_MoveSessionToAgent_LoopJSONUntouched(t *testing.T) {
	workingDir := "/tmp/move-agent"
	sm, store := newMoveAgentTestManager(t, workingDir)

	if err := store.Create(session.Metadata{
		SessionID: "s1", ACPServer: "agent-a", WorkingDir: workingDir,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	loopStore := store.Loop("s1")
	if err := loopStore.Set(&session.LoopPrompt{
		Prompt:    "keep going",
		Frequency: session.Frequency{Value: 1, Unit: session.FrequencyHours},
		Enabled:   true,
	}); err != nil {
		t.Fatalf("loopStore.Set failed: %v", err)
	}
	before, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get (before) failed: %v", err)
	}

	if _, err := sm.MoveSessionToAgent("s1", "agent-b", MoveAgentOptions{CloseTimeout: 200 * time.Millisecond}); err != nil {
		t.Fatalf("MoveSessionToAgent failed: %v", err)
	}

	after, err := loopStore.Get()
	if err != nil {
		t.Fatalf("loopStore.Get (after) failed: %v", err)
	}
	if before.Prompt != after.Prompt || before.Enabled != after.Enabled ||
		before.Frequency != after.Frequency || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Errorf("loop.json changed across move: before=%+v after=%+v", before, after)
	}
}
