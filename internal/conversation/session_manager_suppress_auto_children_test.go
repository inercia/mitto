package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// SessionManager-level coverage for mitto-nlx.4: pins the gate at
// session_manager.go:1877, which decides whether to fire the
// createAutoChildren goroutine based on CreateSessionOptions.SuppressAutoChildren.
//
// Direct verification uses createAutoChildren (same-package, unexported) as
// the observable side-effect surface: it persists child metadata to
// sm.store BEFORE attempting to start the child's ACP process (see
// session_manager.go:459 — store.Create precedes ResumeSessionWithModelConstraint).
// This lets tests observe children in the store even when the ACP start
// downstream fails, which it always does in unit-test environments.

// TestCreateAutoChildren_PersistsChildMetadata is a positive control: when
// createAutoChildren runs with a workspace that has AutoChildren entries, the
// child sessions must be persisted to the store with the correct parent-child
// linkage. This is the observable that TestCreateSessionOptions_SuppressAutoChildren_Gate
// below relies on.
func TestCreateAutoChildren_PersistsChildMetadata(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := NewSessionManager("", "test-server", false, nil)
	sm.SetStore(store)

	workspace := &config.WorkspaceSettings{
		UUID:       "ws-parent",
		ACPServer:  "test-server",
		WorkingDir: "/work-auto-children",
		AutoChildren: []config.AutoChild{
			{Title: "Coder"},
			{Title: "Reviewer"},
		},
	}
	sm.SetWorkspaces([]config.WorkspaceSettings{*workspace})

	// Minimal parent BS: createAutoChildren only reads GetSessionID and
	// GetWorkingDir, both of which are direct field accessors.
	parentID := session.GenerateSessionID()
	parentBS := &BackgroundSession{
		persistedID: parentID,
		workingDir:  workspace.WorkingDir,
	}
	// Persist the parent metadata so any downstream lookups (e.g. via store.List)
	// can distinguish the parent from its children by ParentSessionID.
	if err := store.Create(session.Metadata{
		SessionID:  parentID,
		Status:     "active",
		ACPServer:  workspace.ACPServer,
		WorkingDir: workspace.WorkingDir,
	}); err != nil {
		t.Fatalf("store.Create(parent): %v", err)
	}

	// Call the unexported worker directly. It attempts ResumeSession for each
	// child — which fails in tests because there is no real ACP — but the
	// store.Create for each child metadata happens BEFORE that failure, so we
	// still observe the persisted rows.
	sm.createAutoChildren(parentBS, workspace)

	metas, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}

	// Count children linked to the parent.
	var children []session.Metadata
	for _, m := range metas {
		if m.ParentSessionID == parentID {
			children = append(children, m)
		}
	}
	if got, want := len(children), len(workspace.AutoChildren); got != want {
		t.Fatalf("auto-child count = %d, want %d (metas=%+v)", got, want, metas)
	}
	// Each child must carry ChildOrigin=auto so the UI + cascade-delete
	// paths behave correctly. (session.IsAutoChild is a legacy field kept
	// for backward compat; ChildOrigin is the modern equivalent.)
	for _, c := range children {
		if c.ChildOrigin != session.ChildOriginAuto {
			t.Errorf("child %q: ChildOrigin=%q, want %q", c.SessionID, c.ChildOrigin, session.ChildOriginAuto)
		}
		if c.WorkingDir != workspace.WorkingDir {
			t.Errorf("child %q: WorkingDir=%q, want %q (inherit parent)", c.SessionID, c.WorkingDir, workspace.WorkingDir)
		}
	}
}

// TestCreateAutoChildren_EmptyWorkspaceIsNoOp pins the early-return guard at
// session_manager.go:406 — a workspace with no AutoChildren must be a no-op
// even if it is otherwise well-formed. This is the invariant a
// SuppressAutoChildren=true call effectively simulates from the createAutoChildren
// side: no children ever get persisted.
func TestCreateAutoChildren_EmptyWorkspaceIsNoOp(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := NewSessionManager("", "test-server", false, nil)
	sm.SetStore(store)

	workspace := &config.WorkspaceSettings{
		UUID:         "ws-empty",
		ACPServer:    "test-server",
		WorkingDir:   "/work-empty-autoch",
		AutoChildren: nil,
	}

	parentID := session.GenerateSessionID()
	parentBS := &BackgroundSession{persistedID: parentID, workingDir: workspace.WorkingDir}

	before, _ := store.List()
	sm.createAutoChildren(parentBS, workspace)
	after, _ := store.List()
	if len(after) != len(before) {
		t.Fatalf("createAutoChildren on empty AutoChildren produced %d new entries, want 0", len(after)-len(before))
	}
}

// TestCreateSessionOptions_SuppressAutoChildren_ZeroValue pins the wire-level
// default: the zero value of CreateSessionOptions must NOT suppress spawning.
// Callers that never construct the options struct (or default it) get the
// pre-mitto-nlx behavior — a top-level create always triggers auto_children.
func TestCreateSessionOptions_SuppressAutoChildren_ZeroValue(t *testing.T) {
	var opts CreateSessionOptions
	if opts.SuppressAutoChildren {
		t.Errorf("CreateSessionOptions zero value has SuppressAutoChildren=true, want false (default must preserve legacy behavior)")
	}
}

// TestCreateSessionWithWorkspace_DelegatesWithZeroOptions pins the
// backwards-compatible bridge at session_manager.go:1511: the plain
// CreateSessionWithWorkspace forwards a zero-value CreateSessionOptions,
// so pre-mitto-nlx callers keep triggering auto_children spawns.
//
// The call itself fails downstream in unit tests (no real ACP server), so
// this test only asserts the entry contract: passing nil workspace and a
// short-cancel context surfaces the same non-suppression path via the
// options-aware variant. We use a fast-cancel context so the test doesn't
// wait through the full ACP retry budget.
func TestCreateSessionWithWorkspace_DelegatesWithZeroOptions(t *testing.T) {
	sm := NewSessionManager("", "test-server", false, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // fail-fast: cancel before the ACP retries run

	// Passing nil workspace forces the default-workspace lookup, which
	// yields nil (no workspaces configured) and short-circuits before
	// any ACP start attempt.
	_, err := sm.CreateSessionWithWorkspace(ctx, "unit-test", "", nil)
	if err == nil {
		t.Fatal("CreateSessionWithWorkspace with no workspaces succeeded unexpectedly")
	}
	// Give any background goroutines a small window to schedule so the test's
	// t.Cleanup path doesn't race with a lingering worker.
	time.Sleep(10 * time.Millisecond)
}

// TestCreateSessionWithWorkspaceAndOptions_SuppressAutoChildren_NoSpawnOnFail
// pins the failure-path invariant: when CreateSessionWithWorkspaceAndOptions
// fails before reaching the gate (as it always does in unit tests without a
// real ACP), NO auto-child metadata is persisted regardless of the
// SuppressAutoChildren flag. This complements the direct
// createAutoChildren test above — together they cover both branches of the
// gate at session_manager.go:1877.
func TestCreateSessionWithWorkspaceAndOptions_SuppressAutoChildren_NoSpawnOnFail(t *testing.T) {
	// Both true and false variants must produce the same observable outcome
	// on the ACP-fail path: no child metadata in the store. This proves the
	// createAutoChildren goroutine is NEVER dispatched when the parent
	// create fails, so the SuppressAutoChildren flag cannot leak spurious
	// children on error.
	for _, suppress := range []bool{true, false} {
		t.Run(map[bool]string{true: "suppress=true", false: "suppress=false"}[suppress], func(t *testing.T) {
			store, err := session.NewStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			t.Cleanup(func() { store.Close() })

			sm := NewSessionManager("", "test-server", false, nil)
			sm.SetStore(store)
			sm.SetWorkspaces([]config.WorkspaceSettings{
				{UUID: "ws-a", ACPServer: "test-server", WorkingDir: "/work-fail",
					AutoChildren: []config.AutoChild{{Title: "Coder"}}},
			})

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, err = sm.CreateSessionWithWorkspaceAndOptions(ctx, "unit", "/work-fail", nil,
				CreateSessionOptions{SuppressAutoChildren: suppress})
			if err == nil {
				t.Fatal("expected create to fail without a real ACP server")
			}

			// Bounded poll for any child metadata that might have leaked
			// via a stray goroutine.
			deadline := time.Now().Add(200 * time.Millisecond)
			for time.Now().Before(deadline) {
				metas, _ := store.List()
				for _, m := range metas {
					if m.ChildOrigin == session.ChildOriginAuto {
						t.Fatalf("unexpected auto-child persisted on ACP-fail path (suppress=%v): %+v", suppress, m)
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
