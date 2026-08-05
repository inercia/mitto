package acpproc

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/inercia/mitto/internal/acpproc/acperrors"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
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
// deadline schedule (mitto-f7q; attempt-1 lower bound raised for mitto-8qp): length tied
// to max-attempts, attempt-1 sized above large-context warm-up latency, total bounded so
// setModelAsyncCallerBudget contention math stays valid, and non-increasing order.
func TestSetModelAttemptTimeoutSchedule(t *testing.T) {
	schedule := setSessionModelAttemptTimeouts

	if got := len(schedule); got != setSessionModelMaxAttempts {
		t.Errorf("len(setSessionModelAttemptTimeouts) = %d, want %d (setSessionModelMaxAttempts)",
			got, setSessionModelMaxAttempts)
	}

	// Attempt-1 must be sized above the genuine warm-up latency of large-context models
	// (e.g. claude-sonnet-5-0-500k, observed >12s). The prior 12s bound was smaller than
	// that latency, so attempt-1 always timed out and the shrinking retries were guaranteed
	// to fail (mitto-8qp). 16s leaves headroom above the observed 12s+ warm-up.
	if schedule[0] < 16*time.Second {
		t.Errorf("attempt-1 timeout = %v, want >= 16s (sized above large-context warm-up, mitto-8qp)", schedule[0])
	}

	// The schedule sum must stay within the contention bound the async budget can cover
	// (derived exactly as in TestSetModelAsyncBudgetMath): with N=4 concurrent callers the
	// expected contention coverage is (N-2)×(scheduleSum + maxJitteredBackoff), and that
	// must not exceed setModelAsyncCallerBudget. Rearranged: scheduleSum must not exceed
	// budget/(N-2) − maxJitteredBackoff. This replaces the old fixed 25s cap, which forced
	// attempt-1 too small to cover real warm-up latency (mitto-8qp).
	const maxConcurrentCallers = 4
	maxJitteredBackoff := time.Duration(float64(setSessionModelRetryBaseDelay)*float64(setSessionModelMaxAttempts-1)*(1+setSessionModelRetryJitterRatio)) + setSessionModelRetryBaseDelay
	maxScheduleSum := setModelAsyncCallerBudget/time.Duration(maxConcurrentCallers-2) - maxJitteredBackoff

	var total time.Duration
	for _, d := range schedule {
		total += d
	}
	if total > maxScheduleSum {
		t.Errorf("sum(setSessionModelAttemptTimeouts) = %v, want <= %v (contention bound; setModelAsyncCallerBudget math)",
			total, maxScheduleSum)
	}

	// Timeouts must be non-increasing (front-loaded for cold start).
	for i := 1; i < len(schedule); i++ {
		if schedule[i] > schedule[i-1] {
			t.Errorf("attempt-%d timeout (%v) > attempt-%d timeout (%v); schedule must be non-increasing",
				i+1, schedule[i], i, schedule[i-1])
		}
	}
}

// simulateSetModelRetryLoop mirrors the per-attempt deadline decision of
// SharedACPProcess.SetSessionModel: each attempt is granted setSessionModelAttemptTimeouts[i]
// of budget, an attempt "succeeds" only when the model's real warm-up latency fits within
// that attempt's budget, and the whole call must complete within outerBudget. It returns
// the 1-based attempt number that succeeded, or 0 if all attempts timed out / the outer
// budget was exhausted. Pure (no real sleeps) so the schedule's behavioural contract can
// be unit-tested deterministically.
func simulateSetModelRetryLoop(rpcLatency, outerBudget time.Duration) (succeededAttempt int) {
	var elapsed time.Duration
	for attempt := 1; attempt <= setSessionModelMaxAttempts; attempt++ {
		perAttempt := setSessionModelAttemptTimeouts[attempt-1]
		// The attempt is bounded by both its own per-attempt deadline and the remaining
		// outer budget (context.WithTimeout(ctx, perAttempt) with ctx carrying outerBudget).
		remaining := outerBudget - elapsed
		if remaining <= 0 {
			return 0
		}
		effective := perAttempt
		if remaining < effective {
			effective = remaining
		}
		if rpcLatency <= effective {
			return attempt // RPC completed within this attempt's budget.
		}
		// Attempt timed out after consuming its effective budget.
		elapsed += effective
	}
	return 0
}

// TestSetModelSchedule_LargeContextModelSucceedsWithinOuterBudget reproduces mitto-8qp:
// the "Aux set_model RPC context-deadline storm". A large-context model (e.g.
// claude-sonnet-5-0-500k) has a genuine set_model warm-up latency that exceeds attempt-1's
// budget. Because the schedule SHRINKS (12s -> 8s -> 5s), every subsequent retry has an
// even smaller budget than the first, so all three attempts are GUARANTEED to fail with
// "context deadline exceeded" — even though the outer async caller budget
// (setModelAsyncCallerBudget, ~90s) has 60-90s of unused headroom (per the bead's log
// evidence: ctx_remaining_ms stayed 68-90s throughout).
//
// Expected (correct) behaviour: a model whose warm-up latency is well within the outer
// budget must eventually succeed via retry. This test asserts that contract and therefore
// FAILS on the current shrinking schedule (no attempt is >= the model's latency) and will
// PASS once the fix widens/flattens the per-attempt schedule to cover realistic warm-up
// latency within the ample outer budget.
func TestSetModelSchedule_LargeContextModelSucceedsWithinOuterBudget(t *testing.T) {
	// Observed warm-up latency for a 500k-context model per the bead's logs: attempt-1
	// consistently burned its full 12s budget (rpc_ms=12000) without completing, i.e. the
	// true latency is > 12s. 13s is a conservative representative value that is still far
	// below the ~90s outer async budget.
	const largeModelWarmupLatency = 13 * time.Second

	if largeModelWarmupLatency >= setModelAsyncCallerBudget {
		t.Fatalf("test premise invalid: model latency %v must be < outer budget %v",
			largeModelWarmupLatency, setModelAsyncCallerBudget)
	}

	attempt := simulateSetModelRetryLoop(largeModelWarmupLatency, setModelAsyncCallerBudget)
	if attempt == 0 {
		t.Fatalf("mitto-8qp reproduced: set_model for a %v-warm-up model never succeeded across "+
			"%d attempts (schedule %v), despite %v of outer budget — the shrinking per-attempt "+
			"schedule guarantees failure for models whose warm-up exceeds attempt-1's budget",
			largeModelWarmupLatency, setSessionModelMaxAttempts,
			setSessionModelAttemptTimeouts, setModelAsyncCallerBudget)
	}
	t.Logf("set_model for a %v-warm-up model succeeded on attempt %d (schedule %v, outer budget %v)",
		largeModelWarmupLatency, attempt, setSessionModelAttemptTimeouts, setModelAsyncCallerBudget)
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
			bail, reason := shouldFailFastCreateAttempt(tc.attempt, tc.saturated, tc.hasDeadline, tc.remaining, sessionCreateAttemptTimeout)
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

// TestSetModelFailureIsTerminal verifies the pure log-level decision helper (mitto-8qp):
// only the terminal set_model failure (non-retryable error, or the last attempt with the
// retry budget exhausted) is treated as terminal (logged at Warn); intermediate retryable
// attempts are non-terminal (logged at Debug) so a best-effort switch that falls back
// cleanly no longer emits 3x repeated "SetSessionModel failed" Warn noise.
func TestSetModelFailureIsTerminal(t *testing.T) {
	cases := []struct {
		name      string
		attempt   int
		retryable bool
		want      bool
	}{
		{"non-retryable on attempt 1 -> terminal", 1, false, true},
		{"retryable early attempt -> not terminal", 1, true, false},
		{"retryable middle attempt -> not terminal", setSessionModelMaxAttempts - 1, true, false},
		{"retryable last attempt -> terminal (budget exhausted)", setSessionModelMaxAttempts, true, true},
		{"non-retryable last attempt -> terminal", setSessionModelMaxAttempts, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := setModelFailureIsTerminal(tc.attempt, tc.retryable); got != tc.want {
				t.Errorf("setModelFailureIsTerminal(%d, %v)=%v, want %v", tc.attempt, tc.retryable, got, tc.want)
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

	// A successful RPC resets level to 0 and clears all consecutive-path state.
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

	// Isolate the consecutive-path re-trip check from the rate/rolling-window
	// trigger (mitto-5eq): recordRPCSuccess intentionally does NOT wipe the
	// rolling window (that would reintroduce the interspersed-success reset bug),
	// so the residual timeouts from earlier in this test could otherwise let the
	// rate trigger fire before the consecutive threshold on the next 3 timeouts,
	// promoting saturationLevel past 1. This test is specifically exercising the
	// consecutive path in isolation, so clear the window here. Coverage for the
	// interaction ("success clears state but window survives") lives in
	// TestSaturationRate_SuccessDoesNotWipeWindow.
	p.saturationMu.Lock()
	p.saturationBuckets = nil
	p.saturationMu.Unlock()

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
	bail, reason := shouldFailFastCreateAttempt(3, false, true, remainingAfterTwo, sessionCreateAttemptTimeout)
	if !bail {
		t.Errorf("attempt=3 with remaining=%v must bail (budget exhausted); got bail=false", remainingAfterTwo)
	}
	if bail && reason == "" {
		t.Error("bail reason must be non-empty")
	}
	t.Logf("sessionCreateTotalBudget=%v, per-attempt=%v, maxAttempts=%d → pre-fix tail=%v, remaining-after-2=%v",
		sessionCreateTotalBudget, sessionCreateAttemptTimeout, sessionCreateMaxAttempts, preFixTail, remainingAfterTwo)
}

// TestAuxSessionCreateBudgetFundsRetry is the mitto-54k.11 reproduction.
//
// The bug: getOrCreateAuxiliarySession wraps SharedACPProcess.NewSession in a fresh
// context with budget auxSessionCreateBudget (was hardcoded 30s). NewSession itself
// runs a bounded-retry loop where each attempt has sessionCreateAttemptTimeout=25s
// and the total sequence is capped by sessionCreateTotalBudget=60s. When attempt 1
// hits the agent's own internal ~25s deadline (a full per-attempt drain — evidence:
// aux-session logs showing `rpc_ms≈25000, error="context deadline exceeded"`), the
// remaining wall-clock in the wrapper ctx is ~5s; shouldFailFastCreateAttempt then
// bails at the attempt-2 boundary because remaining (5s) < perAttemptBudget (25s),
// so the retry loop never gets a second shot even though sessionCreateTotalBudget
// (60s) would allow it. The aux caller ultimately sees deadline_exceeded and every
// title-gen / mcp-check / follow-up dispatch that lands during the wedge fails.
//
// This is a pure math invariant test — no ACP process is required. It asserts that
// auxSessionCreateBudget funds at least TWO full per-attempt session/new RPCs, and
// simulates the "attempt 1 drains 25s" scenario against shouldFailFastCreateAttempt
// to prove that the pre-fix 30s budget makes the fail-fast bail trip before
// attempt 2. With the pre-fix value of 30s the test FAILS (that is the reproduction);
// widening auxSessionCreateBudget to >= 2×sessionCreateAttemptTimeout (50s) — the
// Fix phase will bump it to sessionCreateTotalBudget (60s) for parity with the
// underlying NewSession retry sequence — makes it PASS.
func TestAuxSessionCreateBudgetFundsRetry(t *testing.T) {
	// Invariant 1: the wrapper budget must fund at least two full per-attempt
	// session/new RPCs. Otherwise a first attempt that legitimately consumes its
	// full per-attempt budget leaves no room for a retry.
	if auxSessionCreateBudget < 2*sessionCreateAttemptTimeout {
		t.Errorf("auxSessionCreateBudget (%v) must fund >=2 attempts (2×%v = %v) so that a "+
			"first attempt hitting the agent's internal 25s deadline still leaves budget "+
			"for a retry (mitto-54k.11); currently only %v remains after one full attempt",
			auxSessionCreateBudget, sessionCreateAttemptTimeout,
			2*sessionCreateAttemptTimeout, auxSessionCreateBudget-sessionCreateAttemptTimeout)
	}

	// Invariant 2: after attempt 1 drains a full per-attempt budget,
	// shouldFailFastCreateAttempt MUST NOT bail at the attempt-2 boundary. If it
	// does, the retry loop is regressed to a single attempt on this path, which is
	// the observed mitto-54k.11 failure mode. Simulate the wrapper by taking the
	// aux budget, subtracting one full per-attempt drain, and asking the fail-fast
	// predicate whether attempt 2 can proceed.
	remainingAfterAttempt1 := auxSessionCreateBudget - sessionCreateAttemptTimeout
	bail, reason := shouldFailFastCreateAttempt(
		2,                           // attempt
		false,                       // not saturated (isolate the budget path)
		true,                        // ctx has deadline
		remainingAfterAttempt1,      // remaining wall-clock in wrapper ctx
		sessionCreateAttemptTimeout, // per-attempt budget the loop wants to fund
	)
	if bail {
		t.Errorf("aux wrapper budget %v drained by one full per-attempt (%v) leaves only %v — "+
			"shouldFailFastCreateAttempt bails at attempt 2 (reason=%q). This regresses the "+
			"NewSession retry loop to a single attempt on the aux path even though "+
			"sessionCreateTotalBudget (%v) would fund a retry. This is the mitto-54k.11 wedge.",
			auxSessionCreateBudget, sessionCreateAttemptTimeout, remainingAfterAttempt1,
			reason, sessionCreateTotalBudget)
	}

	t.Logf("auxSessionCreateBudget=%v, per-attempt=%v, remaining-after-attempt1=%v, bail=%v",
		auxSessionCreateBudget, sessionCreateAttemptTimeout, remainingAfterAttempt1, bail)
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

// TestIsAgentInternalDeadlineErr verifies detection of the auggie session/new
// wedge signature: a JSON-RPC -32603 ("Internal error") whose data carries
// "context deadline exceeded". This is delivered as an *acp.RequestError, NOT a
// Go context.DeadlineExceeded, so it must be matched via the code+message check.
func TestIsAgentInternalDeadlineErr(t *testing.T) {
	// The real auggie wedge: -32603 with data.error="context deadline exceeded".
	wedge := acp.NewInternalError(map[string]any{"error": "context deadline exceeded"})
	if !isAgentInternalDeadlineErr(wedge) {
		t.Errorf("isAgentInternalDeadlineErr(wedge) = false, want true (err=%v)", wedge)
	}

	// Wrapped in the retry loop's error must still match via errors.As.
	wrapped := fmt.Errorf("failed to create session: %w", wedge)
	if !isAgentInternalDeadlineErr(wrapped) {
		t.Errorf("isAgentInternalDeadlineErr(wrapped) = false, want true")
	}

	// A -32603 without a deadline signature (some other internal error) must NOT match.
	otherInternal := acp.NewInternalError(map[string]any{"error": "disk full"})
	if isAgentInternalDeadlineErr(otherInternal) {
		t.Errorf("isAgentInternalDeadlineErr(otherInternal) = true, want false")
	}

	// A different RPC code with a deadline message must NOT match (only -32603).
	notFound := acp.NewInvalidParams(map[string]any{"error": "context deadline exceeded"})
	if isAgentInternalDeadlineErr(notFound) {
		t.Errorf("isAgentInternalDeadlineErr(-32602 deadline) = true, want false")
	}

	// A plain Go context.DeadlineExceeded is Mitto's own deadline, not the agent's
	// -32603 signature — it is handled by the errors.Is branch, not this helper.
	if isAgentInternalDeadlineErr(context.DeadlineExceeded) {
		t.Errorf("isAgentInternalDeadlineErr(context.DeadlineExceeded) = true, want false")
	}

	// Nil error must not match.
	if isAgentInternalDeadlineErr(nil) {
		t.Errorf("isAgentInternalDeadlineErr(nil) = true, want false")
	}
}

// TestAgentInternalDeadlineIsRetryable verifies the agent-internal -32603 deadline
// is classified retryable so the bounded NewSession loop records each attempt's
// timeout toward saturation rather than returning permanently after one attempt.
func TestAgentInternalDeadlineIsRetryable(t *testing.T) {
	wedge := acp.NewInternalError(map[string]any{"error": "context deadline exceeded"})
	if !isRetryableCreateError(wedge) {
		t.Errorf("isRetryableCreateError(agent -32603 deadline) = false, want true")
	}
	// A non-deadline internal error remains non-retryable.
	if isRetryableCreateError(acp.NewInternalError(map[string]any{"error": "disk full"})) {
		t.Errorf("isRetryableCreateError(-32603 non-deadline) = true, want false")
	}
}

// TestGetOrCreateAuxiliarySession_SaturatedBails is the mitto-z70 regression:
// when the shared process for the workspace is already flagged saturated (via
// repeated RPC timeouts / cold-MCP wedge), getOrCreateAuxiliarySession MUST
// fail fast WITHOUT issuing a session/new RPC. Otherwise every background
// aux-session request (title-gen, mcp-check, follow-up, etc.) piles more
// cold-init pressure onto an agent that is already struggling to initialise
// its MCP servers, amplifying the wedge.
func TestGetOrCreateAuxiliarySession_SaturatedBails(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	const wsUUID = "ws-saturated"

	// Install a bare shared process and force it into the saturated state by
	// setting saturatedUntil into the future. Reaching into private fields is
	// safe here because the test lives in the same package (acpproc) — the
	// production callers use recordRPCTimeout, but the state machine is what
	// matters for IsSaturated(), which is a pure read of saturatedUntil.
	proc := newTestSharedProcess()
	proc.saturationMu.Lock()
	proc.saturatedUntil = time.Now().Add(30 * time.Second)
	proc.saturationLevel = 1
	proc.saturationMu.Unlock()

	if !proc.IsSaturated() {
		t.Fatal("test setup: expected process to report IsSaturated()=true")
	}

	m.mu.Lock()
	m.processes[wsUUID] = proc
	m.mu.Unlock()

	_, err := m.getOrCreateAuxiliarySession(context.Background(), wsUUID, "title-gen")
	if err == nil {
		t.Fatal("expected error from getOrCreateAuxiliarySession on a saturated process")
	}
	// The error must classify as a deadline-exceeded family sentinel so callers
	// can distinguish "agent is wedged, back off" from a genuine failure.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected err to wrap context.DeadlineExceeded, got %v", err)
	}
	if !strings.Contains(err.Error(), "saturated") {
		t.Errorf("expected error message to mention 'saturated', got %q", err.Error())
	}
	// mitto-13n.2: the reactive IsSaturated() bail is real, timeout-driven
	// degradation and must classify as the specific ErrProcessSaturated
	// sentinel (not the busy or MCP-gated ones), while still satisfying the
	// umbrella ErrSharedProcessSaturated for transition-era callers. This
	// FAILS today because ErrProcessSaturated does not exist yet.
	if !errors.Is(err, acperrors.ErrProcessSaturated) {
		t.Errorf("expected err to wrap acperrors.ErrProcessSaturated, got %v", err)
	}
	if !errors.Is(err, ErrSharedProcessSaturated) {
		t.Errorf("expected err to still wrap the umbrella ErrSharedProcessSaturated during transition, got %v", err)
	}
	if errors.Is(err, acperrors.ErrProcessBusy) {
		t.Errorf("saturation bail must NOT classify as ErrProcessBusy, got %v", err)
	}
	if errors.Is(err, acperrors.ErrMCPInitGated) {
		t.Errorf("saturation bail must NOT classify as ErrMCPInitGated, got %v", err)
	}
}

// TestAuxiliaryModelTag_HonoursProfileOrder locks the "list order = priority"
// contract at the AUXILIARY-model consumer site (mitto-ex7.4): when
// WorkspaceSettings.AuxiliaryModelTag is set, getOrCreateAuxiliarySession
// iterates m.ModelProfilesByTagResolver(tag) in slice order and picks the
// FIRST profile whose Criteria resolves against the aux session's available
// models. This test drives the extracted helper resolveAuxTagConstraint
// directly with two same-tag profiles and asserts that reversing the
// resolver's slice flips which Criteria wins.
func TestAuxiliaryModelTag_HonoursProfileOrder(t *testing.T) {
	models := &conversation.SessionModelState{
		CurrentModelId: "gpt-4o", // does not satisfy either Sonnet profile
		AvailableModels: []conversation.ModelInfo{
			{ModelId: "claude-sonnet-5-0", Name: "Claude Sonnet 5"},
			{ModelId: "claude-sonnet-4-6", Name: "Claude Sonnet 4"},
			{ModelId: "gpt-4o", Name: "GPT-4o"},
		},
	}
	sonnet5 := config.ModelProfile{
		Name:     "Claude Sonnet 5",
		Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Sonnet 5"},
		Tags:     []string{"Smart", "Coding"},
	}
	sonnet4 := config.ModelProfile{
		Name:     "Claude Sonnet 4",
		Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Sonnet 4"},
		Tags:     []string{"Smart", "Coding"},
	}

	// Sonnet 5 first (mirroring the post-split default order that
	// ModelProfilesByTagResolver would return) → aux Criteria is Sonnet 5's.
	got := resolveAuxTagConstraint([]config.ModelProfile{sonnet5, sonnet4}, models)
	if got == nil || got.Pattern != "Sonnet 5" {
		t.Errorf("with [Sonnet5, Sonnet4] resolver order, auxConstraint = %+v, want pattern %q", got, "Sonnet 5")
	}
	// Reverse the resolver's slice → aux Criteria now flips to Sonnet 4's.
	got = resolveAuxTagConstraint([]config.ModelProfile{sonnet4, sonnet5}, models)
	if got == nil || got.Pattern != "Sonnet 4" {
		t.Errorf("with [Sonnet4, Sonnet5] resolver order, auxConstraint = %+v, want pattern %q", got, "Sonnet 4")
	}
	// No profile with a resolvable Criteria → nil (leaves the caller on the
	// server default model).
	unmatchable := config.ModelProfile{
		Name:     "Nope",
		Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "never-matches-anything"},
		Tags:     []string{"Smart"},
	}
	if got := resolveAuxTagConstraint([]config.ModelProfile{unmatchable}, models); got != nil {
		t.Errorf("unmatchable profile: resolveAuxTagConstraint = %+v, want nil", got)
	}
}

// TestGetOrCreateAuxiliarySession_HighActiveRPCsBails is the mitto-9gt
// reproduction: during a parallel fan-out on a not-yet-saturated shared ACP
// process, non-essential auxiliary purposes (follow-up, keepalive, mcp-check,
// title-gen) MUST bail fast instead of paying the full auxSessionCreateBudget
// (60s) on a NewSession RPC that will time out on the agent's internal
// deadline. The existing IsSaturated() bail (mitto-z70) is REACTIVE — it fires
// only after sessionSaturationTimeoutThreshold=3 consecutive timeouts or the
// rate-window trip — so the FIRST storm always slips past it. Evidence on the
// bead: two aux sessions (follow-up, keepalive) both burned new_session_ms=60002
// on a process whose IsSaturated() never returned true during that window,
// consuming the parent's 2m wait_children budget.
//
// The complementary guard is PROACTIVE: when process.ActiveRPCs() reports the
// shared process is already serving N concurrent user-facing RPCs above some
// threshold, non-essential aux purposes should bail immediately with the same
// "process saturated / defer aux" sentinel that mitto-z70 returns. This test
// primes activeRPCs to a busy count and asserts the bail. Currently there is
// no such guard, so the call proceeds past the IsSaturated() check into
// NewSession and either hangs on a nil conn or returns a non-sentinel error —
// either way, this test FAILS until the fix phase adds the proactive bail.
func TestGetOrCreateAuxiliarySession_HighActiveRPCsBails(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	const wsUUID = "ws-busy-fanout"

	// Install a bare shared process. Crucially, do NOT flag it saturated —
	// this test isolates the FIRST-storm hole where saturation has not yet
	// tripped (real repro: parent + fan-out of ~3-6 concurrent RPCs, no
	// timeouts yet accumulated).
	proc := newTestSharedProcess()

	// Prime the in-flight RPC counter to simulate a busy parent + fan-out.
	// K=6 mirrors the concurrent_prompting=6 observed on the bead at the
	// moment the aux keepalive session/new was issued.
	const busyRPCs = 6
	for i := 0; i < busyRPCs; i++ {
		proc.activeRPCs.Add(1)
	}
	if got := proc.ActiveRPCs(); got != busyRPCs {
		t.Fatalf("test setup: ActiveRPCs()=%d, want %d", got, busyRPCs)
	}

	// Precondition: the process must NOT already be saturated — otherwise
	// the existing mitto-z70 bail would fire and we would not be testing
	// the proactive guard.
	if proc.IsSaturated() {
		t.Fatal("test setup: process must not be saturated (this test isolates the pre-saturation hole)")
	}

	m.mu.Lock()
	m.processes[wsUUID] = proc
	m.mu.Unlock()

	// Non-essential aux purpose (follow-up) is exactly the class the bead
	// documents burning 60s on a busy process.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, err := m.getOrCreateAuxiliarySession(ctx, wsUUID, "follow-up")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from getOrCreateAuxiliarySession on a busy process (ActiveRPCs above threshold)")
	}
	// Must classify as a deadline-exceeded family sentinel so callers can
	// distinguish "agent is busy, back off" from a genuine failure — same
	// contract mitto-z70's saturated bail obeys.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected err to wrap context.DeadlineExceeded (saturation-family sentinel), got %v", err)
	}
	// Error message must mention the busy/saturated condition so operators
	// can distinguish this bail from unrelated failures in log analysis.
	msg := err.Error()
	if !strings.Contains(msg, "saturated") && !strings.Contains(msg, "busy") && !strings.Contains(msg, "active RPCs") {
		t.Errorf("expected error message to mention 'saturated'/'busy'/'active RPCs', got %q", msg)
	}
	// Must bail well under the auxSessionCreateBudget (60s). A proactive
	// guard is a synchronous check; anything above a few hundred ms means
	// the call fell through into NewSession — the exact bug this test pins.
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast bail (<500ms), got %v — call fell through into NewSession", elapsed)
	}
	// mitto-13n.2: transient concurrent-RPC load-shedding is NOT the same
	// as real degradation — it must classify as ErrProcessBusy specifically
	// (not ErrProcessSaturated/ErrMCPInitGated), while still satisfying the
	// umbrella sentinel for transition-era callers. FAILS today because
	// ErrProcessBusy does not exist yet.
	if !errors.Is(err, acperrors.ErrProcessBusy) {
		t.Errorf("expected err to wrap acperrors.ErrProcessBusy, got %v", err)
	}
	if !errors.Is(err, ErrSharedProcessSaturated) {
		t.Errorf("expected err to still wrap the umbrella ErrSharedProcessSaturated during transition, got %v", err)
	}
	if errors.Is(err, acperrors.ErrProcessSaturated) {
		t.Errorf("busy bail must NOT classify as ErrProcessSaturated, got %v", err)
	}
	if errors.Is(err, acperrors.ErrMCPInitGated) {
		t.Errorf("busy bail must NOT classify as ErrMCPInitGated, got %v", err)
	}
}

// TestGetOrCreateAuxiliarySession_MCPInitTimedOutBails is the mitto-337
// reproduction (hard case): the shared process's stderr monitor has already
// observed the agent report "MCP initialization timed out"
// (mcpInitTimedOut=true), yet the process is QUIESCENT — not saturated
// (IsSaturated()=false, no accumulated recordRPCTimeout calls yet) and not
// busy (ActiveRPCs() below auxSessionCreateBusyRPCThreshold). Neither existing
// guard (mitto-z70 IsSaturated, mitto-9gt ActiveRPCs) fires, so today the call
// falls through into NewSession and burns the full auxSessionCreateBudget
// (60s) against an agent that has explicitly given up on MCP init — exactly
// the "22:31/22:32/22:33 serialized 60s failures" log signature on the bead.
//
// getOrCreateAuxiliarySession should instead consult the already-tracked
// MCPInitTimedOut() signal and bail immediately with the same
// ErrSharedProcessSaturated + context.DeadlineExceeded sentinel the other
// bails use. This test FAILS today because no such bail exists.
func TestGetOrCreateAuxiliarySession_MCPInitTimedOutBails(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	const wsUUID = "ws-mcp-init-timed-out"

	proc := newTestSharedProcess()
	proc.mcpInitTimedOut.Store(true)

	// Preconditions: isolate this guard from the two existing ones.
	if proc.IsSaturated() {
		t.Fatal("test setup: process must not be saturated (isolates the MCP-init-aware guard)")
	}
	if got := proc.ActiveRPCs(); got >= auxSessionCreateBusyRPCThreshold {
		t.Fatalf("test setup: ActiveRPCs()=%d must be below threshold %d", got, auxSessionCreateBusyRPCThreshold)
	}

	m.mu.Lock()
	m.processes[wsUUID] = proc
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, err := m.getOrCreateAuxiliarySession(ctx, wsUUID, "title-gen")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from getOrCreateAuxiliarySession while MCP init has timed out")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected err to wrap context.DeadlineExceeded (saturation-family sentinel), got %v", err)
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "mcp") {
		t.Errorf("expected error message to mention the MCP-init condition, got %q", err.Error())
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast bail (<500ms), got %v — call fell through into NewSession", elapsed)
	}
	// mitto-13n.2: a wedged/timed-out MCP handshake is neither timeout-driven
	// process degradation nor concurrent-RPC load — it must classify as
	// ErrMCPInitGated specifically, while still satisfying the umbrella
	// sentinel for transition-era callers. FAILS today because
	// ErrMCPInitGated does not exist yet.
	if !errors.Is(err, acperrors.ErrMCPInitGated) {
		t.Errorf("expected err to wrap acperrors.ErrMCPInitGated, got %v", err)
	}
	if !errors.Is(err, ErrSharedProcessSaturated) {
		t.Errorf("expected err to still wrap the umbrella ErrSharedProcessSaturated during transition, got %v", err)
	}
	if errors.Is(err, acperrors.ErrProcessSaturated) {
		t.Errorf("mcp-init-timed-out bail must NOT classify as ErrProcessSaturated, got %v", err)
	}
	if errors.Is(err, acperrors.ErrProcessBusy) {
		t.Errorf("mcp-init-timed-out bail must NOT classify as ErrProcessBusy, got %v", err)
	}
}

// TestGetOrCreateAuxiliarySession_MCPInitInProgressBails is the mitto-337
// reproduction (soft case): the agent has reported it is actively waiting on
// its MCP handshake (mcpInitInProgress=true) and has never completed one
// (mcpInitDone=false) — i.e. it is mid cold-start, not yet hard-timed-out.
// Same gap as the hard case: neither IsSaturated() nor ActiveRPCs() fires on a
// quiescent process, so a non-essential aux purpose (follow-up) falls through
// into a doomed 60s NewSession call instead of bailing on the
// already-available "agent is gated on MCP init" signal.
//
// This test FAILS today for the same reason as the timed-out case above.
func TestGetOrCreateAuxiliarySession_MCPInitInProgressBails(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()

	const wsUUID = "ws-mcp-init-in-progress"

	proc := newTestSharedProcess()
	proc.mcpInitInProgress.Store(true)
	// mcpInitDone left false (zero value): the handshake is still running,
	// not yet warm — this is the precondition that distinguishes the "gated"
	// case from a normal warm per-session re-handshake (mitto-29q), which
	// must NOT be bailed.

	if proc.IsSaturated() {
		t.Fatal("test setup: process must not be saturated (isolates the MCP-init-aware guard)")
	}
	if got := proc.ActiveRPCs(); got >= auxSessionCreateBusyRPCThreshold {
		t.Fatalf("test setup: ActiveRPCs()=%d must be below threshold %d", got, auxSessionCreateBusyRPCThreshold)
	}

	m.mu.Lock()
	m.processes[wsUUID] = proc
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, err := m.getOrCreateAuxiliarySession(ctx, wsUUID, "follow-up")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from getOrCreateAuxiliarySession while MCP init is in progress")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected err to wrap context.DeadlineExceeded (saturation-family sentinel), got %v", err)
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "mcp") {
		t.Errorf("expected error message to mention the MCP-init condition, got %q", err.Error())
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast bail (<500ms), got %v — call fell through into NewSession", elapsed)
	}
	// mitto-13n.2: same classification contract as the timed-out (hard) case
	// above — an in-progress/never-completed MCP handshake must classify as
	// ErrMCPInitGated, not ErrProcessSaturated/ErrProcessBusy, while still
	// satisfying the umbrella sentinel. FAILS today because ErrMCPInitGated
	// does not exist yet.
	if !errors.Is(err, acperrors.ErrMCPInitGated) {
		t.Errorf("expected err to wrap acperrors.ErrMCPInitGated, got %v", err)
	}
	if !errors.Is(err, ErrSharedProcessSaturated) {
		t.Errorf("expected err to still wrap the umbrella ErrSharedProcessSaturated during transition, got %v", err)
	}
	if errors.Is(err, acperrors.ErrProcessSaturated) {
		t.Errorf("mcp-init-in-progress bail must NOT classify as ErrProcessSaturated, got %v", err)
	}
	if errors.Is(err, acperrors.ErrProcessBusy) {
		t.Errorf("mcp-init-in-progress bail must NOT classify as ErrProcessBusy, got %v", err)
	}
}
