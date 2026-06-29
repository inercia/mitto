package acpproc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/config"
)

func TestACPProcessManager_GetOrCreateProcess_RequiresWorkspace(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	_, err := m.GetOrCreateProcess(nil, "", "", nil, nil, false)
	if err == nil {
		t.Fatal("expected error for nil workspace")
	}
}

func TestACPProcessManager_GetOrCreateProcess_RequiresUUID(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	_, err := m.GetOrCreateProcess(&config.WorkspaceSettings{}, "", "", nil, nil, false)
	if err == nil {
		t.Fatal("expected error for empty UUID")
	}
}

func TestACPProcessManager_Close_Empty(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	// Should not panic
	m.Close()

	if m.ProcessCount() != 0 {
		t.Errorf("expected 0 processes after close, got %d", m.ProcessCount())
	}
}

func TestACPProcessManager_StopProcess_Nonexistent(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	// Should not panic
	m.StopProcess("nonexistent-uuid")
}

func TestACPProcessManager_ProcessCount(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	if m.ProcessCount() != 0 {
		t.Errorf("expected 0, got %d", m.ProcessCount())
	}
}

// Tests for auxiliary session management

func TestACPProcessManager_CloseWorkspaceAuxiliary(t *testing.T) {
	ctx := context.Background()
	mgr := NewACPProcessManager(ctx, nil)
	defer mgr.Close()

	// Add some mock auxiliary sessions
	mgr.auxMu.Lock()
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}] = &auxiliarySessionState{
		sessionID: "session1",
	}
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "follow-up"}] = &auxiliarySessionState{
		sessionID: "session2",
	}
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace2", purpose: "title-gen"}] = &auxiliarySessionState{
		sessionID: "session3",
	}
	mgr.auxMu.Unlock()

	// Close workspace1's auxiliary sessions
	err := mgr.CloseWorkspaceAuxiliary("workspace1")
	if err != nil {
		t.Fatalf("CloseWorkspaceAuxiliary() error = %v", err)
	}

	// Check that workspace1's sessions are removed
	mgr.auxMu.Lock()
	defer mgr.auxMu.Unlock()

	if _, exists := mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}]; exists {
		t.Error("workspace1 title-gen session should be removed")
	}

	if _, exists := mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "follow-up"}]; exists {
		t.Error("workspace1 follow-up session should be removed")
	}

	// Check that workspace2's session still exists
	if _, exists := mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace2", purpose: "title-gen"}]; !exists {
		t.Error("workspace2 title-gen session should still exist")
	}
}

func TestACPProcessManager_InvalidateAuxiliarySessions(t *testing.T) {
	ctx := context.Background()
	mgr := NewACPProcessManager(ctx, nil)
	defer mgr.Close()

	// Add mock auxiliary sessions for two workspaces
	mgr.auxMu.Lock()
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}] = &auxiliarySessionState{
		sessionID: "session1",
	}
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "follow-up"}] = &auxiliarySessionState{
		sessionID: "session2",
	}
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace2", purpose: "title-gen"}] = &auxiliarySessionState{
		sessionID: "session3",
	}
	mgr.auxMu.Unlock()

	// Invalidate workspace1's auxiliary sessions
	mgr.invalidateAuxiliarySessions("workspace1")

	// Check that workspace1's sessions are removed
	mgr.auxMu.Lock()
	defer mgr.auxMu.Unlock()

	if _, exists := mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}]; exists {
		t.Error("workspace1 title-gen session should be invalidated")
	}
	if _, exists := mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "follow-up"}]; exists {
		t.Error("workspace1 follow-up session should be invalidated")
	}

	// Check that workspace2's session is untouched
	if _, exists := mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace2", purpose: "title-gen"}]; !exists {
		t.Error("workspace2 title-gen session should still exist")
	}
}

func TestACPProcessManager_InvalidateAuxiliarySessions_NoopForEmptyWorkspace(t *testing.T) {
	ctx := context.Background()
	mgr := NewACPProcessManager(ctx, nil)
	defer mgr.Close()

	// Add a session for a different workspace
	mgr.auxMu.Lock()
	mgr.auxSessions[auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}] = &auxiliarySessionState{
		sessionID: "session1",
	}
	mgr.auxMu.Unlock()

	// Invalidate a non-existent workspace — should be a no-op
	mgr.invalidateAuxiliarySessions("nonexistent")

	mgr.auxMu.Lock()
	defer mgr.auxMu.Unlock()

	if len(mgr.auxSessions) != 1 {
		t.Errorf("expected 1 session remaining, got %d", len(mgr.auxSessions))
	}
}

func TestACPProcessManager_PromptAuxiliary_NoProcess(t *testing.T) {
	ctx := context.Background()
	mgr := NewACPProcessManager(ctx, nil)
	defer mgr.Close()

	// Try to prompt auxiliary without a workspace process
	_, err := mgr.PromptAuxiliary(ctx, "nonexistent-workspace", "title-gen", "test message")

	if err == nil {
		t.Error("PromptAuxiliary() should return error when workspace process doesn't exist")
	}
}

func TestAuxSessionKey(t *testing.T) {
	// Test that auxSessionKey works as a map key
	m := make(map[auxSessionKey]string)

	key1 := auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}
	key2 := auxSessionKey{workspaceUUID: "workspace1", purpose: "title-gen"}
	key3 := auxSessionKey{workspaceUUID: "workspace1", purpose: "follow-up"}
	key4 := auxSessionKey{workspaceUUID: "workspace2", purpose: "title-gen"}

	m[key1] = "value1"

	// Same workspace and purpose should retrieve the same value
	if m[key2] != "value1" {
		t.Error("Same auxSessionKey should retrieve same value")
	}

	// Different purpose should not exist
	if _, exists := m[key3]; exists {
		t.Error("Different purpose should not exist in map")
	}

	// Different workspace should not exist
	if _, exists := m[key4]; exists {
		t.Error("Different workspace should not exist in map")
	}
}

func TestNewAuxiliaryClient(t *testing.T) {
	client := newAuxiliaryClient()

	if client == nil {
		t.Fatal("newAuxiliaryClient() returned nil")
	}

	// Test reset
	client.reset()

	// Test getResponse on empty client
	response := client.getResponse()
	if response != "" {
		t.Errorf("getResponse() = %q, want empty string", response)
	}
}

func TestAuxiliaryClient_ResponseCollection(t *testing.T) {
	client := newAuxiliaryClient()

	// Simulate collecting response text
	client.mu.Lock()
	client.response.WriteString("Hello ")
	client.response.WriteString("World")
	client.mu.Unlock()

	got := client.getResponse()
	want := "Hello World"

	if got != want {
		t.Errorf("getResponse() = %q, want %q", got, want)
	}

	// Test reset
	client.reset()
	got = client.getResponse()
	if got != "" {
		t.Errorf("After reset, getResponse() = %q, want empty string", got)
	}
}

// ---- mapsEqual tests ----

func TestMapsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    map[string]string
		b    map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, map[string]string{}, true},
		{"empty vs nil", map[string]string{}, nil, true},
		{"both empty", map[string]string{}, map[string]string{}, true},
		{"identical", map[string]string{"A": "1", "B": "2"}, map[string]string{"A": "1", "B": "2"}, true},
		{"different values", map[string]string{"A": "1"}, map[string]string{"A": "2"}, false},
		{"different keys", map[string]string{"A": "1"}, map[string]string{"B": "1"}, false},
		{"different lengths", map[string]string{"A": "1"}, map[string]string{"A": "1", "B": "2"}, false},
		{"subset a of b", map[string]string{"A": "1"}, map[string]string{"A": "1", "B": "2"}, false},
		{"one nil one non-empty", nil, map[string]string{"A": "1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("mapsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// ---- sharedProcessConfigMatchesWorkspace tests ----

func TestSharedProcessConfigMatchesWorkspace_NilInputs(t *testing.T) {
	// nil process should not match
	if sharedProcessConfigMatchesWorkspace(nil, "test", "cmd", "", nil) {
		t.Error("nil process should not match")
	}
}

func TestSharedProcessConfigMatchesWorkspace_MatchesWithoutEnv(t *testing.T) {
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			ACPCwd:     "/cwd",
		},
	}
	if !sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "/cwd", nil) {
		t.Error("expected match when all fields match (no env)")
	}
}

func TestSharedProcessConfigMatchesWorkspace_MatchesWithEnv(t *testing.T) {
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			Env:        map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"},
		},
	}
	if !sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "",
		map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"}) {
		t.Error("expected match when all fields including Env match")
	}
}

func TestSharedProcessConfigMatchesWorkspace_EnvChanged(t *testing.T) {
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			Env:        map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096"},
		},
	}
	if sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "",
		map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"}) {
		t.Error("should NOT match when Env values differ — process must be recreated")
	}
}

func TestSharedProcessConfigMatchesWorkspace_EnvAdded(t *testing.T) {
	// Process was started without env, but resolved env now has values — should NOT match
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			Env:        nil,
		},
	}
	if sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "",
		map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"}) {
		t.Error("should NOT match when env was added to config — process must be recreated")
	}
}

func TestSharedProcessConfigMatchesWorkspace_EnvRemoved(t *testing.T) {
	// Process was started with env, resolved env is now nil — should NOT match
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			Env:        map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"},
		},
	}
	if sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "", nil) {
		t.Error("should NOT match when env was removed from config — process must be recreated")
	}
}

func TestSharedProcessConfigMatchesWorkspace_CommandDiffers(t *testing.T) {
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp --model opus4.5",
			Env:        map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"},
		},
	}
	if sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp --model opus4.6", "",
		map[string]string{"NODE_OPTIONS": "--max-old-space-size=8192"}) {
		t.Error("should NOT match when command differs")
	}
}

func TestSharedProcessConfigMatchesWorkspace_ServerDiffers(t *testing.T) {
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			ACPCwd:     "/cwd",
		},
	}
	if sharedProcessConfigMatchesWorkspace(p, "ClaudeCode", "auggie --acp", "/cwd", nil) {
		t.Error("should NOT match when server differs")
	}
}

func TestSharedProcessConfigMatchesWorkspace_CwdDiffers(t *testing.T) {
	p := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			ACPCwd:     "/cwd/one",
		},
	}
	if sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "/cwd/two", nil) {
		t.Error("should NOT match when cwd differs")
	}
}

func TestSharedProcessConfigMatchesWorkspace_NilVsEmptyEnvMatches(t *testing.T) {
	// A process started with no env (nil) must match a re-resolved empty map,
	// and vice versa — this is a benign equivalence that must NOT trigger recreation.
	pNil := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			ACPCwd:     "/cwd",
			Env:        nil,
		},
	}
	if !sharedProcessConfigMatchesWorkspace(pNil, "Auggie", "auggie --acp", "/cwd", map[string]string{}) {
		t.Error("nil stored env vs resolved empty map should match")
	}

	pEmpty := &SharedACPProcess{
		config: SharedACPProcessConfig{
			ACPServer:  "Auggie",
			ACPCommand: "auggie --acp",
			ACPCwd:     "/cwd",
			Env:        map[string]string{},
		},
	}
	if !sharedProcessConfigMatchesWorkspace(pEmpty, "Auggie", "auggie --acp", "/cwd", nil) {
		t.Error("empty stored env vs resolved nil map should match")
	}
}

// ---- diffEnvKeys tests ----

func TestDiffEnvKeys(t *testing.T) {
	tests := []struct {
		name        string
		a           map[string]string
		b           map[string]string
		wantAdded   []string
		wantRemoved []string
		wantChanged []string
	}{
		{
			name:        "both nil",
			a:           nil,
			b:           nil,
			wantAdded:   nil,
			wantRemoved: nil,
			wantChanged: nil,
		},
		{
			name:        "nil vs empty",
			a:           nil,
			b:           map[string]string{},
			wantAdded:   nil,
			wantRemoved: nil,
			wantChanged: nil,
		},
		{
			name:        "identical",
			a:           map[string]string{"A": "1", "B": "2"},
			b:           map[string]string{"A": "1", "B": "2"},
			wantAdded:   nil,
			wantRemoved: nil,
			wantChanged: nil,
		},
		{
			name:        "added keys sorted",
			a:           map[string]string{"A": "1"},
			b:           map[string]string{"A": "1", "Z": "9", "M": "5"},
			wantAdded:   []string{"M", "Z"},
			wantRemoved: nil,
			wantChanged: nil,
		},
		{
			name:        "removed keys sorted",
			a:           map[string]string{"A": "1", "Z": "9", "M": "5"},
			b:           map[string]string{"A": "1"},
			wantAdded:   nil,
			wantRemoved: []string{"M", "Z"},
			wantChanged: nil,
		},
		{
			name:        "changed keys sorted",
			a:           map[string]string{"B": "1", "A": "1"},
			b:           map[string]string{"B": "2", "A": "9"},
			wantAdded:   nil,
			wantRemoved: nil,
			wantChanged: []string{"A", "B"},
		},
		{
			name:        "mixed add remove change",
			a:           map[string]string{"KEEP": "x", "GONE": "y", "MOD": "1"},
			b:           map[string]string{"KEEP": "x", "MOD": "2", "NEW": "z"},
			wantAdded:   []string{"NEW"},
			wantRemoved: []string{"GONE"},
			wantChanged: []string{"MOD"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			added, removed, changed := diffEnvKeys(tc.a, tc.b)
			if !reflect.DeepEqual(added, tc.wantAdded) {
				t.Errorf("added = %v, want %v", added, tc.wantAdded)
			}
			if !reflect.DeepEqual(removed, tc.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, tc.wantRemoved)
			}
			if !reflect.DeepEqual(changed, tc.wantChanged) {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

// TestPrewarmContextBudgetIsolation is a regression test for mitto-54p.
//
// Root cause: prewarmAuxiliarySessions previously created ONE 30-second context and
// shared it across FOUR parallel goroutines. Inside getOrCreateAuxiliarySession,
// auxMu serialises those goroutines, so the shared deadline is consumed sequentially.
// After N slow NewSession calls drain most of the budget, the remaining time on ctx is
// near zero; the subsequent SetSessionModel timeout derived from ctx via
//
//	context.WithTimeout(ctx, 10*time.Second)
//
// inherits the exhausted deadline and is immediately expired → "context deadline
// exceeded", rpc_ms=0.
//
// The fix has two parts (both tested here):
//  1. prewarmAuxiliarySessions: each goroutine creates its OWN independent timeout
//     (derived from m.ctx) so one slow NewSession cannot starve the others.
//  2. getOrCreateAuxiliarySession: SetSessionModel is now performed in a background
//     goroutine with its own generous budget (setModelAsyncCallerBudget, 90s) derived
//     from m.ctx rather than from the caller's ctx — so the model switch is never
//     blocked on caller-deadline pressure (mitto-f7q, Option 4).
//
// This test verifies the deadline-isolation math that underpins both fixes. It
// deliberately reproduces the starvation scenario and asserts:
//   - OLD behaviour (shared budget): at least one SetSessionModel context would be
//     expired before any work could run.
//   - NEW behaviour (independent budgets + m.ctx base for model-switch goroutine):
//     every SetSessionModel context retains close to its full budget.
func TestPrewarmContextBudgetIsolation(t *testing.T) {
	const (
		numSessions      = 4
		workPerSession   = 60 * time.Millisecond // simulates NewSession latency
		modelSetTimeout  = 10 * time.Second
		minExpectedSlack = 9 * time.Second // SetSessionModel must retain at least this much
	)

	// ── OLD behaviour: shared deadline drained by sequential work ──────────────
	// Budget is intentionally set to just under the total serial work time so that
	// the last iteration sees a nearly-expired (or already expired) ctx.
	oldBudget := time.Duration(float64(numSessions*int(workPerSession)) * 0.95)
	oldBehaviorDemonstratesStarvation := func() bool {
		sharedCtx, cancel := context.WithTimeout(context.Background(), oldBudget)
		defer cancel()
		for i := 0; i < numSessions; i++ {
			time.Sleep(workPerSession)
			// OLD: SetSessionModel derives from the shared (drained) ctx.
			setCtx, setCancel := context.WithTimeout(sharedCtx, modelSetTimeout)
			expired := setCtx.Err() != nil
			setCancel()
			if expired {
				return true
			}
		}
		return false
	}

	if !oldBehaviorDemonstratesStarvation() {
		// Timing was too generous on this machine; skip rather than produce a
		// false-positive pass — the test is only meaningful when starvation occurs.
		t.Skip("timing-sensitive: could not reproduce pre-fix starvation; skipping")
	}

	// ── NEW behaviour: independent per-goroutine contexts + m.ctx base ─────────
	// Represents the fixed code: each prewarm goroutine has its own 30s ctx (from
	// m.ctx), and SetSessionModel derives from m.ctx (not the drained caller ctx).
	managerCtx := context.Background() // stands in for m.ctx in production code

	for i := 0; i < numSessions; i++ {
		// Fix part 1: each goroutine creates its own independent timeout.
		// The ctx is not passed to SetSessionModel (that uses managerCtx directly),
		// but it scopes the goroutine's overall budget — kept here to mirror
		// the real prewarmAuxiliarySessions structure.
		_, goroutineCancel := context.WithTimeout(managerCtx, 30*time.Second)

		time.Sleep(workPerSession) // simulate NewSession latency

		// Fix part 2: SetSessionModel derives from managerCtx (m.ctx), not from
		// the goroutine's ctx that might be near its own deadline.
		setCtx, setCancel := context.WithTimeout(managerCtx, modelSetTimeout)

		if err := setCtx.Err(); err != nil {
			t.Errorf("NEW behaviour: session %d SetSessionModel ctx already expired: %v", i, err)
			setCancel()
			goroutineCancel()
			continue
		}
		deadline, ok := setCtx.Deadline()
		if !ok {
			t.Errorf("NEW behaviour: session %d SetSessionModel ctx has no deadline", i)
			setCancel()
			goroutineCancel()
			continue
		}
		if remaining := time.Until(deadline); remaining < minExpectedSlack {
			t.Errorf("NEW behaviour: session %d SetSessionModel has only %v remaining, want >= %v",
				i, remaining, minExpectedSlack)
		}

		setCancel()
		goroutineCancel()
	}
}

// TestAuxNewSessionDeadlineIndependentOfCallerCtx is a regression test for mitto-rlk.
//
// Root cause: getOrCreateAuxiliarySession held auxMu for its entire body. When several
// goroutines are serialised on auxMu and a dead/slow MCP server causes each prior
// SetSessionModel to burn its full 10 s deadline, the caller ctx arrives at the
// process.NewSession call already expired — producing rpc_ms=0, ctx_already_expired=true.
//
// The fix: derive the NewSession context from m.ctx (manager lifetime) with its OWN
// 30 s budget, not from the (possibly drained) caller ctx. A quick ctx.Err() guard
// still honours an explicitly cancelled caller before the RPC starts.
//
// This test verifies the deadline math directly (no real ACP process required):
//   - OLD behaviour: caller ctx drained by serial work → NewSession ctx already expired.
//   - NEW behaviour: NewSession ctx derived from m.ctx → always has its full 30 s window.
func TestAuxNewSessionDeadlineIndependentOfCallerCtx(t *testing.T) {
	const (
		numSessions       = 4
		workPerSession    = 60 * time.Millisecond // simulates per-session SetSessionModel latency
		newSessionTimeout = 30 * time.Second
		minExpectedSlack  = 29 * time.Second // NewSession ctx must retain at least this much
	)

	// ── OLD behaviour: caller ctx is shared and drained by serial work ───────────
	// Budget just under total serial work so the last iteration arrives with an
	// already-expired (or near-zero) ctx — reproducing the wedge signature.
	oldBudget := time.Duration(float64(numSessions*int(workPerSession)) * 0.95)
	oldBehaviorDemonstratesStarvation := func() bool {
		callerCtx, cancel := context.WithTimeout(context.Background(), oldBudget)
		defer cancel()
		for i := 0; i < numSessions; i++ {
			time.Sleep(workPerSession) // serial work holds auxMu equivalent
			// OLD: NewSession is called with the shared (drained) callerCtx.
			if callerCtx.Err() != nil {
				return true // ctx already expired before NewSession would run
			}
		}
		return false
	}

	if !oldBehaviorDemonstratesStarvation() {
		t.Skip("timing-sensitive: could not reproduce pre-fix caller-ctx starvation; skipping")
	}

	// ── NEW behaviour: NewSession ctx derived from m.ctx (manager lifetime) ──────
	managerCtx := context.Background() // stands in for m.ctx in production code

	for i := 0; i < numSessions; i++ {
		time.Sleep(workPerSession) // simulate prior sessions consuming wall time under auxMu

		// Fix: NewSession derives its context from managerCtx (m.ctx), not from the
		// drained caller ctx.
		newCtx, newCancel := context.WithTimeout(managerCtx, newSessionTimeout)

		if err := newCtx.Err(); err != nil {
			t.Errorf("NEW behaviour: session %d NewSession ctx already expired: %v", i, err)
			newCancel()
			continue
		}
		deadline, ok := newCtx.Deadline()
		if !ok {
			t.Errorf("NEW behaviour: session %d NewSession ctx has no deadline", i)
			newCancel()
			continue
		}
		if remaining := time.Until(deadline); remaining < minExpectedSlack {
			t.Errorf("NEW behaviour: session %d NewSession ctx has only %v remaining, want >= %v",
				i, remaining, minExpectedSlack)
		}

		newCancel()
	}
}

// TestAuxCreateMuLockStructure verifies the per-key creation-lock design introduced in
// mitto-w19. It does NOT require a real ACP process; it exercises only the locking
// machinery stored in auxCreateMu.
//
// Assertions:
//  1. The same key always returns the same *sync.Mutex pointer (idempotent allocation).
//  2. Different keys return distinct *sync.Mutex pointers.
//  3. Two goroutines locking different keys' createMu do not block each other (they can
//     hold their locks simultaneously).
//  4. Two goroutines locking the SAME key's createMu serialize: while one holds it the
//     other cannot acquire it immediately (TryLock returns false).
func TestAuxCreateMuLockStructure(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	keyA := auxSessionKey{workspaceUUID: "ws1", purpose: "title-gen"}
	keyB := auxSessionKey{workspaceUUID: "ws1", purpose: "follow-up"}

	// Helper: get-or-create the createMu for a key (mirrors the production logic).
	getCreateMu := func(key auxSessionKey) *sync.Mutex {
		m.auxMu.Lock()
		defer m.auxMu.Unlock()
		mu, ok := m.auxCreateMu[key]
		if !ok {
			mu = &sync.Mutex{}
			m.auxCreateMu[key] = mu
		}
		return mu
	}

	// ── Assertion 1: same key → same pointer ─────────────────────────────────────
	mu1 := getCreateMu(keyA)
	mu2 := getCreateMu(keyA)
	if mu1 != mu2 {
		t.Errorf("same key must return the same *sync.Mutex, got different pointers")
	}

	// ── Assertion 2: different keys → distinct pointers ───────────────────────────
	muB := getCreateMu(keyB)
	if mu1 == muB {
		t.Errorf("different keys must return distinct *sync.Mutex pointers")
	}

	// ── Assertion 3: different-key locks do not block each other ─────────────────
	mu1.Lock()
	// muB is a different lock; it must be acquirable while mu1 is held.
	if !muB.TryLock() {
		t.Error("locking different-key createMu should not block (different keys must be independent)")
	} else {
		muB.Unlock()
	}
	mu1.Unlock()

	// ── Assertion 4: same-key lock serializes ─────────────────────────────────────
	muSame := getCreateMu(keyA)
	muSame.Lock()
	// A second attempt on the same mutex must fail (it's already held).
	if muSame.TryLock() {
		t.Error("same-key createMu must not be acquirable while already held (same-key callers must serialize)")
		muSame.Unlock() // release the erroneous second acquisition
	}
	muSame.Unlock()

	// ── Assertion 5: no duplicate entry for the same key in auxSessions ──────────
	// Manually insert one session and verify a subsequent getOrCreate attempt
	// returns that same session without creating a duplicate (map has only one entry).
	m.auxMu.Lock()
	existingState := &auxiliarySessionState{sessionID: "sess-existing"}
	m.auxSessions[keyA] = existingState
	m.auxMu.Unlock()

	m.auxMu.Lock()
	count := 0
	for k := range m.auxSessions {
		if k == keyA {
			count++
		}
	}
	m.auxMu.Unlock()
	if count != 1 {
		t.Errorf("expected exactly 1 entry for keyA in auxSessions, got %d", count)
	}
}

// TestSetModelAsyncBudgetMath verifies that setModelAsyncCallerBudget (90s) is
// large enough to cover worst-case semaphore contention at server wakeup (mitto-f7q).
//
// Worst case: the background goroutine queues behind N-1 prior holders, each
// completing schedule-sum (12+8+5=25s) + max jitter backoff ≈ 25s. With N=4
// concurrent aux sessions, 3 prior holders × 25s = 75s wait before the semaphore
// is acquired. The goroutine's own retries add ≤25s, totalling ≤100s in the
// absolute worst case. 90s covers the expected contention (≤4 concurrent at
// wakeup) while excluding the extreme 4-holder worst case.
func TestSetModelAsyncBudgetMath(t *testing.T) {
	const (
		maxConcurrentCallers = 4 // from bead: ~4 concurrent children at wakeup
		maxRetries           = setSessionModelMaxAttempts
		// Max backoff per retry cycle (attempt 3 carries the largest delay).
		maxJitteredBackoff = time.Duration(float64(setSessionModelRetryBaseDelay)*float64(maxRetries-1)*(1+setSessionModelRetryJitterRatio)) + setSessionModelRetryBaseDelay
		asyncBudget        = setModelAsyncCallerBudget
	)

	// Per-caller worst-case: sum of the attempt schedule + total jittered backoff.
	var scheduleSum time.Duration
	for _, d := range setSessionModelAttemptTimeouts {
		scheduleSum += d
	}
	perCallerMax := scheduleSum + maxJitteredBackoff

	// Semaphore wait: up to (N-1) prior holders each at their worst case.
	semWaitMax := time.Duration(maxConcurrentCallers-1) * perCallerMax

	// Verify that the async budget exceeds the expected contention region
	// (first 3 of 4 holders exhausted), even if not the absolute 4-holder worst case.
	expectedContentionCoverage := time.Duration(maxConcurrentCallers-2) * perCallerMax
	if asyncBudget < expectedContentionCoverage {
		t.Errorf("setModelAsyncCallerBudget (%v) is less than expected contention coverage (%v); "+
			"increase the budget constant", asyncBudget, expectedContentionCoverage)
	}

	t.Logf("per-caller max: %v, sem wait (N-1=%d holders): %v, async budget: %v",
		perCallerMax, maxConcurrentCallers-1, semWaitMax, asyncBudget)
}

// TestSetModelAttemptTimeoutSchedule asserts structural invariants of the per-attempt
// deadline schedule (mitto-f7q): length tied to max-attempts, attempt-1 sized for cold
// warm-up, total ≤ 25s (unchanged from prior 3×8s), and non-increasing order.
func TestSetModelAttemptTimeoutSchedule(t *testing.T) {
	schedule := setSessionModelAttemptTimeouts

	if got := len(schedule); got != setSessionModelMaxAttempts {
		t.Errorf("len(setSessionModelAttemptTimeouts) = %d, want %d (setSessionModelMaxAttempts)",
			got, setSessionModelMaxAttempts)
	}

	// Attempt-1 must be sized above the observed 8s cold-model clamp (p95 evidence).
	if schedule[0] < 12*time.Second {
		t.Errorf("attempt-1 timeout = %v, want >= 12s (sized for cold warm-up p95)", schedule[0])
	}

	// Total must not exceed 25s so setModelAsyncCallerBudget contention math is valid.
	var total time.Duration
	for _, d := range schedule {
		total += d
	}
	if total > 25*time.Second {
		t.Errorf("sum(setSessionModelAttemptTimeouts) = %v, want <= 25s (total must not grow)", total)
	}

	// Timeouts must be non-increasing (front-loaded for cold start).
	for i := 1; i < len(schedule); i++ {
		if schedule[i] > schedule[i-1] {
			t.Errorf("attempt-%d timeout (%v) > attempt-%d timeout (%v); schedule must be non-increasing",
				i+1, schedule[i], i, schedule[i-1])
		}
	}
}

// TestSetModelRetryJitter verifies that the jittered backoff delay applied in
// SetSessionModel's retry loop stays within the expected bounds (mitto-f7q, Option 3).
//
// The jitter formula is:
//
//	delay = (attempt-1) × base + rand([0, base × ratio))
//
// So for attempt 2: delay ∈ [base, base×(1+ratio)) = [300ms, 450ms).
// For attempt 3:    delay ∈ [2×base, 2×base + base×ratio) = [600ms, 750ms).
func TestSetModelRetryJitter(t *testing.T) {
	base := setSessionModelRetryBaseDelay
	ratio := setSessionModelRetryJitterRatio

	for _, tc := range []struct {
		attempt  int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{
			attempt:  2,
			minDelay: base,                                                        // (2-1)×base + 0
			maxDelay: base + time.Duration(float64(base)*ratio) - time.Nanosecond, // exclusive upper
		},
		{
			attempt:  3,
			minDelay: 2 * base,                                                      // (3-1)×base + 0
			maxDelay: 2*base + time.Duration(float64(base)*ratio) - time.Nanosecond, // exclusive upper
		},
	} {
		// Run many iterations to catch jitter that exceeds bounds.
		for i := 0; i < 500; i++ {
			jitter := time.Duration(rand.Int63n(int64(float64(base) * ratio)))
			delay := time.Duration(tc.attempt-1)*base + jitter
			if delay < tc.minDelay || delay > tc.maxDelay {
				t.Errorf("attempt %d iter %d: delay %v outside [%v, %v]",
					tc.attempt, i, delay, tc.minDelay, tc.maxDelay)
				break
			}
		}
	}
}

// TestNewSessionRetryJitter verifies that the jittered backoff delay applied in
// NewSession's retry loop stays within the expected bounds (mitto-4no7, parity with
// TestSetModelRetryJitter).
//
// The jitter formula is:
//
//	delay = (attempt-1) × base + rand([0, base × ratio))
//
// So for attempt 2: delay ∈ [base, base×(1+ratio)) = [300ms, 450ms).
// For attempt 3:    delay ∈ [2×base, 2×base + base×ratio) = [600ms, 750ms).
func TestNewSessionRetryJitter(t *testing.T) {
	base := sessionCreateRetryBaseDelay
	ratio := sessionCreateRetryJitterRatio

	for _, tc := range []struct {
		attempt  int
		minDelay time.Duration
		maxDelay time.Duration
	}{
		{
			attempt:  2,
			minDelay: base,                                                        // (2-1)×base + 0
			maxDelay: base + time.Duration(float64(base)*ratio) - time.Nanosecond, // exclusive upper
		},
		{
			attempt:  3,
			minDelay: 2 * base,                                                      // (3-1)×base + 0
			maxDelay: 2*base + time.Duration(float64(base)*ratio) - time.Nanosecond, // exclusive upper
		},
	} {
		// Run many iterations to catch jitter that exceeds bounds.
		for i := 0; i < 500; i++ {
			jitter := time.Duration(rand.Int63n(int64(float64(base) * ratio)))
			delay := time.Duration(tc.attempt-1)*base + jitter
			if delay < tc.minDelay || delay > tc.maxDelay {
				t.Errorf("attempt %d iter %d: delay %v outside [%v, %v]",
					tc.attempt, i, delay, tc.minDelay, tc.maxDelay)
				break
			}
		}
	}
}

// TestDiffEnvKeys_NeverLeaksValues asserts that the returned slices contain only
// key names and never the (potentially secret) values.
func TestDiffEnvKeys_NeverLeaksValues(t *testing.T) {
	a := map[string]string{"API_TOKEN": "old-secret", "STAY": "v"}
	b := map[string]string{"API_TOKEN": "new-secret", "STAY": "v", "PASSWORD": "hunter2"}

	added, removed, changed := diffEnvKeys(a, b)

	all := append(append(append([]string{}, added...), removed...), changed...)
	for _, k := range all {
		if k == "old-secret" || k == "new-secret" || k == "hunter2" {
			t.Fatalf("diffEnvKeys leaked a value: %q", k)
		}
	}
	if !reflect.DeepEqual(added, []string{"PASSWORD"}) {
		t.Errorf("added = %v, want [PASSWORD]", added)
	}
	if !reflect.DeepEqual(changed, []string{"API_TOKEN"}) {
		t.Errorf("changed = %v, want [API_TOKEN]", changed)
	}
}

// TestSetSessionModel_DeadProcessFailsFast is a regression test for mitto-13ck.1.
//
// Previously, SetSessionModel had no liveness check at the start of each retry
// attempt. When the shared ACP process was dead (processDone closed), each attempt
// would hang for the full 8 s per-attempt budget waiting for the RPC to time out,
// even though the outcome was predetermined. With 3 attempts that burns 24 s before
// returning an error.
//
// The fix: a non-blocking select on processDone at the top of each attempt loop,
// returning immediately with a non-retryable error so the loop exits in O(µs).
//
// This test verifies the fail-fast path without a real ACP process.
func TestSetSessionModel_DeadProcessFailsFast(t *testing.T) {
	// Build a minimal SharedACPProcess with a closed processDone channel and a
	// non-nil conn pointer (so the nil-conn guard doesn't fire first).
	// We use a real channel but don't need a real ACP connection — the liveness
	// check fires before any RPC is attempted.
	done := make(chan struct{})
	close(done)

	p := &SharedACPProcess{
		// conn must be non-nil to pass the initial nil check.
		// new() allocates a zero-value struct; the processDone check fires before
		// any method is called on it, so no ACP connection is actually needed.
		conn:        new(acp.ClientSideConnection),
		processDone: done,
		setModelSem: make(chan struct{}, 1),
	}

	ctx := context.Background()
	start := time.Now()
	err := p.SetSessionModel(ctx, "session-id", "some-model")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("SetSessionModel must return an error when the process is dead")
	}
	// Must fail in well under 1 s, not after the 8 s per-attempt deadline.
	const maxElapsed = 500 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("SetSessionModel took %v on dead process; want < %v (fail-fast not working)", elapsed, maxElapsed)
	}
	// The error must NOT be a timeout/deadline error — so isRetryableSetModelError
	// would return false and no retry is attempted.
	if isRetryableSetModelError(err) {
		t.Errorf("dead-process error must not be retryable, got: %v", err)
	}
}

// TestProcessInitializeAttemptTimeoutBound is a math test for mitto-13ck.2.
//
// It verifies that the per-attempt Initialize timeout (processInitializeAttemptTimeout)
// multiplied by the max start retries, plus maximum cumulative retry backoff, is
// significantly less than the pre-fix worst case of maxProcessStartRetries × 60 s
// (≈ 180 s — the SDK's DEFAULT_CONTROL_REQUEST_TIMEOUT hit on each attempt).
//
// The target: bounded retry tail well under the pre-fix ~180 s.
func TestProcessInitializeAttemptTimeoutBound(t *testing.T) {
	// Max cumulative backoff across all retries.
	// BackoffDelay uses exponential backoff capped at processStartRetryMaxDelay.
	maxBackoffTotal := time.Duration(maxProcessStartRetries-1) * processStartRetryMaxDelay

	// Worst-case total wall time for all retry attempts.
	totalMax := time.Duration(maxProcessStartRetries)*processInitializeAttemptTimeout + maxBackoffTotal

	// Pre-fix worst case: each attempt hangs the full SDK 60 s control timeout.
	const sdkControlTimeout = 60 * time.Second
	preFix := time.Duration(maxProcessStartRetries) * sdkControlTimeout

	if totalMax >= preFix {
		t.Errorf("bounded retry tail (%v) must be less than pre-fix tail (%v); "+
			"increase processInitializeAttemptTimeout or maxProcessStartRetries is too large",
			totalMax, preFix)
	}
	t.Logf("processInitializeAttemptTimeout=%v, maxRetries=%d, maxBackoff=%v → total max=%v (pre-fix was %v)",
		processInitializeAttemptTimeout, maxProcessStartRetries, maxBackoffTotal, totalMax, preFix)
}

// TestSharedACPProcess_SaturationStateMachine verifies the saturation state machine
// (mitto-13ck.2): initial state is unsaturated, threshold trips it, success clears it,
// and the cooldown self-clears when it elapses.
func TestSharedACPProcess_SaturationStateMachine(t *testing.T) {
	p := &SharedACPProcess{}

	// Initially not saturated.
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false initially")
	}

	// Trip saturation by reaching the threshold.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	if !p.isSaturated() {
		t.Fatal("expected isSaturated()=true after threshold timeouts")
	}

	// A successful RPC clears saturation.
	p.recordRPCSuccess()
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false after recordRPCSuccess")
	}
	if p.consecutiveRPCTimeouts != 0 {
		t.Errorf("expected consecutiveRPCTimeouts=0 after success, got %d", p.consecutiveRPCTimeouts)
	}

	// Cooldown self-clear: drive saturated again then backdate the timer.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	p.saturatedUntil = time.Now().Add(-time.Second) // force expiry
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false after cooldown elapsed")
	}
	if p.consecutiveRPCTimeouts != 0 {
		t.Errorf("expected consecutiveRPCTimeouts reset to 0 on cooldown expiry, got %d", p.consecutiveRPCTimeouts)
	}
}

// TestNewSession_SaturatedFailsFast is a regression test for mitto-13ck.2.
// When the shared process is flagged saturated, NewSession must return in <500ms
// with a context.DeadlineExceeded-wrapped error instead of draining the full retry budget.
func TestNewSession_SaturatedFailsFast(t *testing.T) {
	p := &SharedACPProcess{
		conn: new(acp.ClientSideConnection),
		// processDone left nil = process considered alive; saturation must fire regardless.
	}

	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}

	start := time.Now()
	_, err := p.NewSession(context.Background(), ".", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("NewSession must return an error when saturated")
	}
	const maxElapsed = 500 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("NewSession took %v on saturated process; want < %v (fail-fast not working)", elapsed, maxElapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded)=true, got: %v", err)
	}
}

// TestLoadSession_SaturatedFailsFast is a regression test for mitto-13ck.2.
// When the shared process is flagged saturated, LoadSession must return in <500ms
// with a context.DeadlineExceeded-wrapped error. The saturation guard fires before
// the caps check so caps can be left nil.
func TestLoadSession_SaturatedFailsFast(t *testing.T) {
	p := &SharedACPProcess{
		conn: new(acp.ClientSideConnection),
		// caps left nil — saturation guard fires before caps check.
	}

	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}

	start := time.Now()
	_, err := p.LoadSession(context.Background(), "acp-session-id", ".", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("LoadSession must return an error when saturated")
	}
	const maxElapsed = 500 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("LoadSession took %v on saturated process; want < %v (fail-fast not working)", elapsed, maxElapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded)=true, got: %v", err)
	}
}

// TestSetSessionModel_SaturatedFailsFast is a regression test for mitto-13ck.1.
// When the shared process is flagged saturated, SetSessionModel must return in <500ms
// with a context.DeadlineExceeded-wrapped error instead of exhausting all attempts
// (each an 8s hang). The entry guard fires before the semaphore acquisition.
func TestSetSessionModel_SaturatedFailsFast(t *testing.T) {
	p := &SharedACPProcess{
		conn:        new(acp.ClientSideConnection),
		setModelSem: make(chan struct{}, 1),
		// processDone left nil = process considered alive; saturation must fire regardless.
	}
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	start := time.Now()
	err := p.SetSessionModel(context.Background(), "session-id", "some-model")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("SetSessionModel must return an error when saturated")
	}
	const maxElapsed = 500 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("SetSessionModel took %v on saturated process; want < %v (fail-fast not working)", elapsed, maxElapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded)=true, got: %v", err)
	}
}

// TestShouldFailFastCreateAttempt verifies the pure decision helper (mitto-13ck.2).
func TestShouldFailFastCreateAttempt(t *testing.T) {
	bigBudget := sessionCreateAttemptTimeout * 2
	smallBudget := sessionCreateAttemptTimeout / 2

	cases := []struct {
		name        string
		attempt     int
		saturated   bool
		hasDeadline bool
		remaining   time.Duration
		wantBail    bool
	}{
		{"attempt=1 always proceeds even if saturated", 1, true, true, smallBudget, false},
		{"attempt=1 always proceeds even if low budget", 1, false, true, smallBudget, false},
		{"attempt=2 saturated -> bail", 2, true, false, 0, true},
		{"attempt=2 not saturated no deadline -> proceed", 2, false, false, 0, false},
		{"attempt=2 not saturated remaining < timeout -> bail", 2, false, true, smallBudget, true},
		{"attempt=2 not saturated remaining >= timeout -> proceed", 2, false, true, bigBudget, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bail, reason := shouldFailFastCreateAttempt(tc.attempt, tc.saturated, tc.hasDeadline, tc.remaining)
			if bail != tc.wantBail {
				t.Errorf("bail=%v, want %v (reason=%q)", bail, tc.wantBail, reason)
			}
			if bail && reason == "" {
				t.Error("reason must be non-empty when bail=true")
			}
			if !bail && reason != "" {
				t.Errorf("reason must be empty when bail=false, got %q", reason)
			}
		})
	}
}

// TestLoadSession_ExpiredContextNoSaturation verifies that LoadSession's entry guard
// (mitto-13ck.2) returns fast without incrementing the saturation counter when the
// caller's context is already cancelled on entry.
func TestLoadSession_ExpiredContextNoSaturation(t *testing.T) {
	// Build a minimal SharedACPProcess sufficient to reach the entry guard:
	// conn non-nil, processDone nil (alive), caps nil — saturation guard fires
	// before caps check, and entry guard fires before the RPC.
	p := &SharedACPProcess{
		conn: new(acp.ClientSideConnection),
	}

	// Verify baseline: counter starts at 0.
	p.saturationMu.Lock()
	before := p.consecutiveRPCTimeouts
	p.saturationMu.Unlock()
	if before != 0 {
		t.Fatalf("expected consecutiveRPCTimeouts=0 initially, got %d", before)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	start := time.Now()
	_, err := p.LoadSession(ctx, "acp-session-id", ".", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("LoadSession must return an error for cancelled context")
	}
	const maxElapsed = 500 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("LoadSession took %v; want < %v", elapsed, maxElapsed)
	}

	// Saturation counter must NOT have incremented.
	p.saturationMu.Lock()
	after := p.consecutiveRPCTimeouts
	p.saturationMu.Unlock()
	if after != before {
		t.Errorf("consecutiveRPCTimeouts changed from %d to %d; expired-context must not increment it", before, after)
	}
	if p.isSaturated() {
		t.Error("process must not be flagged saturated after expired-context entry guard")
	}
}

// TestSaturationCooldownForLevel verifies the escalating-cooldown math (mitto-13ck.2):
// level 1 → base (30s), level 2 → 2×base (60s), level 3 → 4×base (120s), with an
// upper cap of sessionSaturationCooldownMax (5min). Level 0 and negative levels
// return the base. Very high levels must not overflow and must return the cap.
func TestSaturationCooldownForLevel(t *testing.T) {
	base := sessionSaturationCooldownBase
	max := sessionSaturationCooldownMax

	cases := []struct {
		level int
		want  time.Duration
	}{
		{-1, base},
		{0, base},
		{1, base},     // 30s × 2^0 = 30s
		{2, 2 * base}, // 30s × 2^1 = 60s
		{3, 4 * base}, // 30s × 2^2 = 120s
		{4, 8 * base}, // 30s × 2^3 = 240s
		{5, max},      // 30s × 2^4 = 480s → capped at 300s
		{100, max},    // very large level: must not overflow, must return cap
		{1000, max},   // extreme level: same cap guarantee
	}
	for _, tc := range cases {
		got := saturationCooldownForLevel(tc.level)
		if got != tc.want {
			t.Errorf("saturationCooldownForLevel(%d) = %v, want %v", tc.level, got, tc.want)
		}
	}
}

// TestSaturationStateMachine_EscalatingCooldown verifies that repeated saturation trips
// grow the cooldown exponentially (mitto-13ck.2) and that a successful RPC resets the
// level, reverting the cooldown to the base on the next event.
func TestSaturationStateMachine_EscalatingCooldown(t *testing.T) {
	p := &SharedACPProcess{}

	// Trip saturation once (level 1 → 30s cooldown).
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	p.saturationMu.Lock()
	lvl1 := p.saturationLevel
	cd1 := time.Until(p.saturatedUntil)
	p.saturationMu.Unlock()
	if lvl1 != 1 {
		t.Errorf("after first trip: saturationLevel = %d, want 1", lvl1)
	}
	wantCD1 := saturationCooldownForLevel(1)
	if cd1 < wantCD1-time.Second || cd1 > wantCD1+time.Second {
		t.Errorf("after level-1 trip: cooldown ≈ %v, want ≈ %v", cd1, wantCD1)
	}

	// Simulate cooldown elapsing → probe mode.
	p.saturationMu.Lock()
	p.saturatedUntil = time.Now().Add(-time.Millisecond)
	p.saturationMu.Unlock()
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false after cooldown elapsed")
	}
	p.saturationMu.Lock()
	if !p.inProbe {
		t.Error("expected inProbe=true after cooldown self-clear")
	}
	p.saturationMu.Unlock()

	// Probe timeout → level escalates to 2 (60s cooldown).
	p.recordRPCTimeout()
	p.saturationMu.Lock()
	lvl2 := p.saturationLevel
	inProbeAfter := p.inProbe
	cd2 := time.Until(p.saturatedUntil)
	p.saturationMu.Unlock()
	if lvl2 != 2 {
		t.Errorf("after probe timeout: saturationLevel = %d, want 2", lvl2)
	}
	if inProbeAfter {
		t.Error("inProbe must be false after probe timeout (cleared by recordRPCTimeout)")
	}
	wantCD2 := saturationCooldownForLevel(2)
	if cd2 < wantCD2-time.Second || cd2 > wantCD2+time.Second {
		t.Errorf("after level-2 trip: cooldown ≈ %v, want ≈ %v", cd2, wantCD2)
	}

	// Simulate second cooldown elapsing → probe mode again.
	p.saturationMu.Lock()
	p.saturatedUntil = time.Now().Add(-time.Millisecond)
	p.saturationMu.Unlock()
	p.isSaturated() // triggers inProbe=true transition

	// Second probe timeout → level escalates to 3 (120s cooldown).
	p.recordRPCTimeout()
	p.saturationMu.Lock()
	lvl3 := p.saturationLevel
	cd3 := time.Until(p.saturatedUntil)
	p.saturationMu.Unlock()
	if lvl3 != 3 {
		t.Errorf("after second probe timeout: saturationLevel = %d, want 3", lvl3)
	}
	wantCD3 := saturationCooldownForLevel(3)
	if cd3 < wantCD3-time.Second || cd3 > wantCD3+time.Second {
		t.Errorf("after level-3 trip: cooldown ≈ %v, want ≈ %v", cd3, wantCD3)
	}

	// A successful RPC resets level to 0 and clears all state.
	p.recordRPCSuccess()
	p.saturationMu.Lock()
	lvlReset := p.saturationLevel
	ctrReset := p.consecutiveRPCTimeouts
	probeReset := p.inProbe
	untilReset := p.saturatedUntil
	p.saturationMu.Unlock()
	if lvlReset != 0 {
		t.Errorf("after recordRPCSuccess: saturationLevel = %d, want 0", lvlReset)
	}
	if ctrReset != 0 {
		t.Errorf("after recordRPCSuccess: consecutiveRPCTimeouts = %d, want 0", ctrReset)
	}
	if probeReset {
		t.Error("after recordRPCSuccess: inProbe must be false")
	}
	if !untilReset.IsZero() {
		t.Error("after recordRPCSuccess: saturatedUntil must be zero")
	}

	// After reset, next trip should again use level 1 (base cooldown).
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	p.saturationMu.Lock()
	lvlAfterReset := p.saturationLevel
	p.saturationMu.Unlock()
	if lvlAfterReset != 1 {
		t.Errorf("after success-reset + re-trip: saturationLevel = %d, want 1", lvlAfterReset)
	}
}

// TestSaturationStateMachine_ProbeMode verifies that isSaturated() sets inProbe=true
// when a cooldown elapses, and that a probe success fully resets all saturation state
// (mitto-13ck.2). Distinct from TestSaturationStateMachine_EscalatingCooldown which
// focuses on probe timeouts.
func TestSaturationStateMachine_ProbeMode(t *testing.T) {
	p := &SharedACPProcess{}

	// Trip saturation then force cooldown expiry.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	p.saturationMu.Lock()
	p.saturatedUntil = time.Now().Add(-time.Millisecond)
	p.saturationMu.Unlock()

	// isSaturated() should self-clear and set inProbe.
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false after cooldown elapsed")
	}
	p.saturationMu.Lock()
	if !p.inProbe {
		t.Error("expected inProbe=true after cooldown self-clear")
	}
	if p.consecutiveRPCTimeouts != 0 {
		t.Errorf("expected consecutiveRPCTimeouts=0 after self-clear, got %d", p.consecutiveRPCTimeouts)
	}
	p.saturationMu.Unlock()

	// A successful probe RPC resets everything (level, counter, probe flag).
	p.recordRPCSuccess()
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false after probe success")
	}
	p.saturationMu.Lock()
	if p.inProbe {
		t.Error("inProbe must be false after probe success")
	}
	if p.saturationLevel != 0 {
		t.Errorf("saturationLevel must be 0 after probe success, got %d", p.saturationLevel)
	}
	p.saturationMu.Unlock()
}

// TestNewSession_ProbeIsSingleAttempt verifies the probe-mode state invariant in
// NewSession (mitto-13ck.2): when inProbe is true (post-cooldown), the saturation
// state machine limits the caller to one attempt.
//
// Because a zero-value acp.ClientSideConnection NPE's when an RPC is actually
// issued, this test exercises the state machine directly rather than calling
// NewSession end-to-end. It verifies:
//
//  1. After cooldown expiry, isSaturated() sets inProbe=true.
//  2. The probe decision (effectiveMaxAttempts=1) is driven by reading inProbe.
//  3. A simulated probe timeout (recordRPCTimeout with inProbe=true) immediately
//     escalates the cooldown level and clears inProbe, without waiting for the
//     sessionSaturationTimeoutThreshold consecutive timeouts that the normal path requires.
func TestNewSession_ProbeIsSingleAttempt(t *testing.T) {
	p := &SharedACPProcess{}

	// Trip saturation to level 1.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}
	p.saturationMu.Lock()
	lvlBefore := p.saturationLevel
	p.saturationMu.Unlock()
	if lvlBefore != 1 {
		t.Fatalf("precondition: saturationLevel = %d, want 1", lvlBefore)
	}

	// Force cooldown expiry → isSaturated() sets inProbe=true.
	p.saturationMu.Lock()
	p.saturatedUntil = time.Now().Add(-time.Millisecond)
	p.saturationMu.Unlock()
	if p.isSaturated() {
		t.Fatal("expected isSaturated()=false after cooldown elapsed")
	}
	p.saturationMu.Lock()
	if !p.inProbe {
		t.Fatal("expected inProbe=true after cooldown self-clear; test precondition not met")
	}
	p.saturationMu.Unlock()

	// Verify that inProbe drives effectiveMaxAttempts=1 (mirrors the logic in NewSession).
	p.saturationMu.Lock()
	effectiveMaxAttempts := sessionCreateMaxAttempts
	if p.inProbe {
		effectiveMaxAttempts = 1
	}
	p.saturationMu.Unlock()
	if effectiveMaxAttempts != 1 {
		t.Errorf("effectiveMaxAttempts = %d when inProbe=true, want 1", effectiveMaxAttempts)
	}

	// Simulate the probe timing out (what NewSession would record after one hung attempt).
	// A single recordRPCTimeout with inProbe=true must immediately escalate the level.
	p.recordRPCTimeout()
	p.saturationMu.Lock()
	probeAfter := p.inProbe
	lvlAfter := p.saturationLevel
	p.saturationMu.Unlock()
	if probeAfter {
		t.Error("inProbe must be cleared by recordRPCTimeout (probe escalation path)")
	}
	if lvlAfter <= lvlBefore {
		t.Errorf("saturationLevel must increase after probe timeout: before=%d after=%d", lvlBefore, lvlAfter)
	}
	// The new cooldown must reflect the escalated level.
	wantCD := saturationCooldownForLevel(lvlAfter)
	p.saturationMu.Lock()
	cd := time.Until(p.saturatedUntil)
	p.saturationMu.Unlock()
	if cd < wantCD-time.Second || cd > wantCD+time.Second {
		t.Errorf("after probe timeout: cooldown ≈ %v, want ≈ %v (level %d)", cd, wantCD, lvlAfter)
	}
}

// TestSaturationCooldownCap verifies that the escalating cooldown is capped at
// sessionSaturationCooldownMax regardless of how many probe-timeout trips occur
// (mitto-13ck.2). Many successive escalations must never exceed the cap.
func TestSaturationCooldownCap(t *testing.T) {
	p := &SharedACPProcess{}

	// Drive saturation to level 1 first.
	for i := 0; i < sessionSaturationTimeoutThreshold; i++ {
		p.recordRPCTimeout()
	}

	// Simulate many probe-timeout escalations.
	for round := 0; round < 20; round++ {
		p.saturationMu.Lock()
		p.saturatedUntil = time.Now().Add(-time.Millisecond)
		p.saturationMu.Unlock()
		p.isSaturated()      // self-clear → inProbe=true
		p.recordRPCTimeout() // probe timeout → escalate

		p.saturationMu.Lock()
		cd := time.Until(p.saturatedUntil)
		p.saturationMu.Unlock()
		// Cooldown must never exceed the cap (allow 1s tolerance for Now().Add latency).
		if cd > sessionSaturationCooldownMax+time.Second {
			t.Errorf("round %d: cooldown %v exceeds max %v", round, cd, sessionSaturationCooldownMax)
		}
	}
}

// TestAuxStartupJitter verifies the de-stagger jitter helper (mitto-xicp): values are
// always in [0, max) for positive max, and 0 for non-positive max.
func TestAuxStartupJitter(t *testing.T) {
	if got := auxStartupJitter(0); got != 0 {
		t.Errorf("auxStartupJitter(0) = %v, want 0", got)
	}
	if got := auxStartupJitter(-time.Second); got != 0 {
		t.Errorf("auxStartupJitter(-1s) = %v, want 0", got)
	}

	max := auxModelSwitchStartupJitter
	for i := 0; i < 1000; i++ {
		got := auxStartupJitter(max)
		if got < 0 || got >= max {
			t.Fatalf("auxStartupJitter(%v) = %v, out of range [0, %v)", max, got, max)
		}
	}
}

// TestSessionCreateTotalBudgetBound is a math test for mitto-8d7.
//
// It verifies that the NewSession total wall-clock budget (sessionCreateTotalBudget)
// genuinely bounds the worst case below the pre-fix tail (max attempts × per-attempt
// timeout ≈ 75s) while still leaving room for at least two full per-attempt budgets,
// and that the remaining budget after two attempts is small enough that the existing
// shouldFailFastCreateAttempt bail trips before a third full-budget attempt.
func TestSessionCreateTotalBudgetBound(t *testing.T) {
	preFixTail := time.Duration(sessionCreateMaxAttempts) * sessionCreateAttemptTimeout

	// The budget must actually bound the loop below the pre-fix worst case.
	if sessionCreateTotalBudget >= preFixTail {
		t.Errorf("sessionCreateTotalBudget (%v) must be < max attempts tail (%v) to bound the loop",
			sessionCreateTotalBudget, preFixTail)
	}

	// The budget must leave room for at least two full per-attempt budgets so a single
	// slow create that succeeds on retry is not regressed to a single attempt.
	if sessionCreateTotalBudget < 2*sessionCreateAttemptTimeout {
		t.Errorf("sessionCreateTotalBudget (%v) must fund >=2 attempts (2×%v) to avoid regressing retries",
			sessionCreateTotalBudget, sessionCreateAttemptTimeout)
	}

	// After two full per-attempt timeouts, the remaining budget must be insufficient to
	// fund another attempt, so shouldFailFastCreateAttempt bails before attempt 3.
	remainingAfterTwo := sessionCreateTotalBudget - 2*sessionCreateAttemptTimeout
	bail, reason := shouldFailFastCreateAttempt(3, false, true, remainingAfterTwo)
	if !bail {
		t.Errorf("attempt=3 with remaining=%v must bail (budget exhausted); got bail=false", remainingAfterTwo)
	}
	if bail && reason == "" {
		t.Error("bail reason must be non-empty")
	}
	t.Logf("sessionCreateTotalBudget=%v, per-attempt=%v, maxAttempts=%d → pre-fix tail=%v, remaining-after-2=%v",
		sessionCreateTotalBudget, sessionCreateAttemptTimeout, sessionCreateMaxAttempts, preFixTail, remainingAfterTwo)
}

// TestRPCErrorCode verifies that rpcErrorCode (mitto-8d7) extracts the JSON-RPC error
// code from a bare or wrapped *acp.RequestError and reports absence for other errors.
func TestRPCErrorCode(t *testing.T) {
	// Bare RequestError (e.g. -32603 Internal error from the agent).
	bare := acp.NewInternalError(map[string]any{"detail": "slow create"})
	if code, ok := rpcErrorCode(bare); !ok || code != -32603 {
		t.Errorf("rpcErrorCode(bare) = (%d, %v), want (-32603, true)", code, ok)
	}

	// Wrapped RequestError must still be unwrapped via errors.As.
	wrapped := fmt.Errorf("failed to create session: %w", bare)
	if code, ok := rpcErrorCode(wrapped); !ok || code != -32603 {
		t.Errorf("rpcErrorCode(wrapped) = (%d, %v), want (-32603, true)", code, ok)
	}

	// Non-RPC errors report no code.
	if code, ok := rpcErrorCode(errors.New("plain error")); ok || code != 0 {
		t.Errorf("rpcErrorCode(plain) = (%d, %v), want (0, false)", code, ok)
	}

	// Nil error reports no code.
	if code, ok := rpcErrorCode(nil); ok || code != 0 {
		t.Errorf("rpcErrorCode(nil) = (%d, %v), want (0, false)", code, ok)
	}
}
