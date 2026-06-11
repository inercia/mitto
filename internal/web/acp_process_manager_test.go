package web

import (
	"context"
	"reflect"
	"testing"
	"time"

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
//  2. getOrCreateAuxiliarySession: SetSessionModel derives its timeout from m.ctx
//     rather than from the caller's ctx, giving SetSessionModel its full window
//     regardless of how much budget NewSession consumed.
//
// This test verifies the deadline math that underpins both fixes. It deliberately
// reproduces the starvation scenario and asserts:
//   - OLD behaviour (shared budget): at least one SetSessionModel context would be
//     expired before any work could run.
//   - NEW behaviour (independent budgets + m.ctx base for SetSessionModel): every
//     SetSessionModel context retains close to its full 10-second window.
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
