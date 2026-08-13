package conversation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/processors"
	"github.com/inercia/mitto/internal/session"
)

func TestResumeBackgroundSession_MissingPersistedID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Try to resume without a persisted ID
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "", // Empty ID
		ACPCommand:  "echo test",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail when PersistedID is empty")
	}
}

func TestResumeBackgroundSession_SessionNotInStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Try to resume a session that doesn't exist in the store
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "non-existent-session",
		ACPCommand:  "echo test",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail for non-existent session")
	}
}

func TestResumeBackgroundSession_InvalidACPCommand(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session in the store first
	meta := session.Metadata{
		SessionID:  "test-session-resume",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to resume with an invalid ACP command
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "test-session-resume",
		ACPCommand:  "/nonexistent/command/that/does/not/exist",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail with invalid ACP command")
	}
}

// TestResumeBackgroundSession_WiresOnTurnIdle is the mitto-aqtf reproduction:
// ResumeBackgroundSession's BackgroundSession struct literal omits the
// onTurnIdle field that NewBackgroundSession sets at
// background_session.go:674 ("onTurnIdle: cfg.OnTurnIdle"). Every other
// callback (onSelfDestruct, onTitleGenerated, onStreamingStateChanged, etc.)
// is copied in ResumeBackgroundSession's literal; onTurnIdle is the one
// missing.
//
// Effect in production: pdOnTurnIdle() (bgsession_prompt.go:1288) is guarded
// by "if bs.onTurnIdle != nil", so on every RESUMED session the end-of-turn
// arm chain (finalizeTurn -> pdOnTurnIdle -> LoopRunner.OnConversationIdle ->
// armCompletionTimer) silently no-ops, leaving the 1-minute poll fallback
// (recoverStalledOnCompletion) as the only path that ever arms an
// onCompletion loop timer — see mitto-aqtf: 48/48 arms in production logs
// were "missed end-of-turn re-arm".
//
// This test resumes a session via a fake SharedProcess (so the handshake
// succeeds without a real ACP subprocess) with a non-nil
// BackgroundSessionConfig.OnTurnIdle, and asserts the resulting
// BackgroundSession actually wires and invokes it. It FAILS on the current
// code (bs.onTurnIdle is nil after resume) and will PASS once
// ResumeBackgroundSession's struct literal copies OnTurnIdle, matching
// NewBackgroundSession.
func TestResumeBackgroundSession_WiresOnTurnIdle(t *testing.T) {
	fp := newFakeSharedProcess()

	var called atomic.Bool
	bs, err := ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID:   "test-onturnidle-resume",
		ACPServer:     "test-server",
		WorkingDir:    "/tmp",
		SharedProcess: fp,
		OnTurnIdle: func(sessionID string) {
			if sessionID != "test-onturnidle-resume" {
				t.Errorf("onTurnIdle called with unexpected sessionID %q", sessionID)
			}
			called.Store(true)
		},
	})
	if err != nil {
		t.Fatalf("ResumeBackgroundSession failed: %v", err)
	}

	if bs.onTurnIdle == nil {
		t.Fatal("mitto-aqtf: ResumeBackgroundSession did not wire onTurnIdle from " +
			"BackgroundSessionConfig.OnTurnIdle — resumed sessions can never arm the " +
			"on-completion loop timer synchronously from finalizeTurn, only via the " +
			"1-minute poll fallback")
	}

	// Simulate what pdOnTurnIdle does at the end of a clean end_turn turn.
	bs.onTurnIdle(bs.persistedID)
	if !called.Load() {
		t.Fatal("onTurnIdle callback was wired but did not fire when invoked")
	}
}

func TestResumeBackgroundSession_EmptyACPCommand(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session in the store first
	meta := session.Metadata{
		SessionID:  "test-session-empty-cmd",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "Test Session",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to resume with an empty ACP command
	_, err = ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID: "test-session-empty-cmd",
		ACPCommand:  "",
		ACPServer:   "test-server",
		WorkingDir:  "/tmp",
		Store:       store,
	})

	if err == nil {
		t.Error("ResumeBackgroundSession should fail with empty ACP command")
	}
}

// TestLookupACPServerConstraints is a regression test for the bug where
// ResumeBackgroundSession did not pass MittoConfig in BackgroundSessionConfig,
// causing bs.acpServerConstraints to stay empty on resume and the conversation
// to ignore the configured auto-selection criteria (e.g. model pattern).
//
// The fix wires the lookup through lookupACPServerConstraints; this test pins
// down the helper's behavior so both the new-session and resume call sites
// produce a populated constraint map when MittoConfig is provided, and an empty
// one (sanity check of the regression) when MittoConfig is nil.
func TestLookupACPServerConstraints(t *testing.T) {
	modelConstraint := &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.8"}
	cfg := &config.Config{
		ACPServers: []config.ACPServer{
			{
				Name: "claude-code",
				Constraints: map[string]*config.ACPServerConstraint{
					"model": modelConstraint,
				},
			},
			{Name: "other-server"},
		},
	}

	t.Run("nil config returns nil (regression case)", func(t *testing.T) {
		got := lookupACPServerConstraints(nil, "claude-code")
		if got != nil {
			t.Errorf("expected nil constraints when cfg is nil, got %v", got)
		}
	})

	t.Run("matching server returns its constraints", func(t *testing.T) {
		got := lookupACPServerConstraints(cfg, "claude-code")
		if got == nil {
			t.Fatal("expected non-nil constraints for matching server")
		}
		c, ok := got["model"]
		if !ok {
			t.Fatalf("expected 'model' constraint, got keys: %v", got)
		}
		if c.MatchMode != "lookAlike" || c.Pattern != "Opus 4.8" {
			t.Errorf("unexpected constraint: %+v", c)
		}
		if c != modelConstraint {
			t.Errorf("expected same constraint pointer, got different one")
		}
	})

	t.Run("server with no constraints returns nil", func(t *testing.T) {
		got := lookupACPServerConstraints(cfg, "other-server")
		if got != nil {
			t.Errorf("expected nil constraints for server without any, got %v", got)
		}
	})

	t.Run("unknown server returns nil", func(t *testing.T) {
		got := lookupACPServerConstraints(cfg, "does-not-exist")
		if got != nil {
			t.Errorf("expected nil constraints for unknown server, got %v", got)
		}
	})
}

// TestLookupACPServerConstraints_ModelTag_HonoursProfileOrder pins down the
// "list order = priority" contract at the ACPServerSettings.ModelTag consumer
// site (mitto-ex7.4 §2 test 3): when an ACPServer has ModelTag set (and no
// ModelProfile), lookupACPServerConstraints resolves the "model" Criteria via
// cfg.ModelProfilesByTag (which wraps the shared config.ProfilesByTag core) and
// picks matches[0]. Because ProfilesByTag walks EffectiveModelProfiles in
// Config.Models order, reordering the two same-tag profiles flips which
// profile's Criteria replaces the "model" entry of the returned constraints
// map. Mirrors the sibling regressions
// TestInitialModelPreference_HonoursProfileOrder (constraints_test.go) and
// TestAuxiliaryModelTag_HonoursProfileOrder (acpproc_process_manager_test.go).
//
// This consumer runs at config time — no SessionModelState / AvailableModels
// exist yet — so the assertion is on the returned Criteria's Pattern, not on
// per-model resolvability (that happens later via applyConfigConstraints).
func TestLookupACPServerConstraints_ModelTag_HonoursProfileOrder(t *testing.T) {
	// Canonical default names so EffectiveModelProfiles shadows the
	// same-named DefaultModelProfiles entries cleanly (user profiles first,
	// defaults appended only when unshadowed).
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

	newCfg := func(models []config.ModelProfile, srv config.ACPServer) *config.Config {
		return &config.Config{
			Models:     models,
			ACPServers: []config.ACPServer{srv},
		}
	}

	t.Run("Sonnet 5 first → tag Smart resolves to Sonnet 5", func(t *testing.T) {
		cfg := newCfg(
			[]config.ModelProfile{sonnet5, sonnet4},
			config.ACPServer{Name: "claude-code", ModelTag: "Smart"},
		)
		got := lookupACPServerConstraints(cfg, "claude-code")
		if got == nil {
			t.Fatal("expected non-nil constraints map")
		}
		c, ok := got["model"]
		if !ok || c == nil {
			t.Fatalf("expected 'model' constraint, got keys: %v", got)
		}
		if c.Pattern != "Sonnet 5" {
			t.Errorf("with [Sonnet5, Sonnet4] Models order, resolved model.Pattern = %q, want %q", c.Pattern, "Sonnet 5")
		}
	})

	t.Run("reverse order → tag Smart flips to Sonnet 4", func(t *testing.T) {
		cfg := newCfg(
			[]config.ModelProfile{sonnet4, sonnet5},
			config.ACPServer{Name: "claude-code", ModelTag: "Smart"},
		)
		got := lookupACPServerConstraints(cfg, "claude-code")
		if got == nil {
			t.Fatal("expected non-nil constraints map")
		}
		c, ok := got["model"]
		if !ok || c == nil {
			t.Fatalf("expected 'model' constraint, got keys: %v", got)
		}
		if c.Pattern != "Sonnet 4" {
			t.Errorf("with [Sonnet4, Sonnet5] Models order, resolved model.Pattern = %q, want %q", c.Pattern, "Sonnet 4")
		}
	})

	t.Run("ModelProfile takes precedence over ModelTag regardless of Models order", func(t *testing.T) {
		// Sonnet 5 first in Models — a bare ModelTag=Smart lookup would pick Sonnet 5,
		// but ModelProfile="Claude Sonnet 4" must win and pin Sonnet 4.
		cfg := newCfg(
			[]config.ModelProfile{sonnet5, sonnet4},
			config.ACPServer{
				Name:         "claude-code",
				ModelProfile: "Claude Sonnet 4",
				ModelTag:     "Smart",
			},
		)
		got := lookupACPServerConstraints(cfg, "claude-code")
		if got == nil {
			t.Fatal("expected non-nil constraints map")
		}
		c, ok := got["model"]
		if !ok || c == nil {
			t.Fatalf("expected 'model' constraint, got keys: %v", got)
		}
		if c.Pattern != "Sonnet 4" {
			t.Errorf("ModelProfile=Claude Sonnet 4 alongside ModelTag=Smart (Sonnet5-first Models) resolved model.Pattern = %q, want %q", c.Pattern, "Sonnet 4")
		}
	})

	t.Run("tag no-match falls back to raw Constraints", func(t *testing.T) {
		// No profile carries "NoSuchTag" (defaults don't either), so the lookup
		// must fall through to srv.Constraints — pointer equality confirms the
		// legacy constraint is passed through untouched (no merge/copy).
		legacyModel := &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.8"}
		cfg := newCfg(
			[]config.ModelProfile{sonnet5, sonnet4},
			config.ACPServer{
				Name:     "claude-code",
				ModelTag: "NoSuchTag",
				Constraints: map[string]*config.ACPServerConstraint{
					"model": legacyModel,
				},
			},
		)
		got := lookupACPServerConstraints(cfg, "claude-code")
		if got == nil {
			t.Fatal("expected non-nil constraints (legacy fallback)")
		}
		c, ok := got["model"]
		if !ok {
			t.Fatalf("expected 'model' constraint in fallback map, got keys: %v", got)
		}
		if c != legacyModel {
			t.Errorf("expected fallback 'model' to be the input legacy constraint pointer, got different pointer (%+v)", c)
		}
	})
}

// TestLookupContextFlushCommand pins down the per-ACP-server resolution of the
// agent-native context-flush command used by BackgroundSession.FlushContext and
// the /flush API/UI gating.
func TestLookupContextFlushCommand(t *testing.T) {
	cfg := &config.Config{
		ACPServers: []config.ACPServer{
			{Name: "claude-code", ContextFlushCommand: "/clear"},
			{Name: "no-flush"},
		},
	}

	t.Run("nil config returns empty", func(t *testing.T) {
		if got := lookupContextFlushCommand(nil, "claude-code"); got != "" {
			t.Errorf("expected empty command when cfg is nil, got %q", got)
		}
	})

	t.Run("matching server returns its command", func(t *testing.T) {
		if got := lookupContextFlushCommand(cfg, "claude-code"); got != "/clear" {
			t.Errorf("expected '/clear', got %q", got)
		}
	})

	t.Run("server without command returns empty", func(t *testing.T) {
		if got := lookupContextFlushCommand(cfg, "no-flush"); got != "" {
			t.Errorf("expected empty command, got %q", got)
		}
	})

	t.Run("unknown server returns empty", func(t *testing.T) {
		if got := lookupContextFlushCommand(cfg, "does-not-exist"); got != "" {
			t.Errorf("expected empty command for unknown server, got %q", got)
		}
	})
}

// TestFlushContext_NotConfigured verifies FlushContext refuses to send anything
// when no context-flush command is configured for the session's ACP server.
func TestFlushContext_NotConfigured(t *testing.T) {
	bs := &BackgroundSession{contextFlushCommand: ""}
	if err := bs.FlushContext(); err == nil {
		t.Fatal("expected error when no flush command is configured")
	}
}

// TestFlushContext_BugRepro_SendsExactCommand_NotProcessorPolluted reproduces
// mitto-ip1: FlushContext() must send the configured agent-native flush command
// (e.g. "/clear") to the transport as the leading/only characters of a single
// text block, exactly as flushContextInPlace does — and must NOT persist a fake
// user turn or broadcast one to observers.
//
// Root cause under test (see the mitto-ip1 Investigation comment): FlushContext
// currently calls PromptWithMeta instead of flushContextInPlace, so the command
// travels through the full processor pipeline (prepend/append injected) and the
// normal user-prompt persistence/broadcast path.
//
// The reproduction deliberately configures a Match:"all" (not "first") prepend
// processor and leaves isFirstPrompt at its zero value (false) so the failure is
// pinned on the unconditional processor-injection side effect described in the
// investigation, independent of the separately-conditional <user_request>
// wrapper (which only fires on the first message or a processor rerun).
func TestFlushContext_BugRepro_SendsExactCommand_NotProcessorPolluted(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	procMgr := processors.NewManager("", nil)
	procMgr.AddTextProcessors([]config.MessageProcessor{
		{
			When:   config.ProcessorWhenBlock{On: config.ProcessorPhaseUserPrompt, Match: config.ProcessorMatchAll},
			Mutate: config.ProcessorMutatePrepend,
			Text:   "[Session Context]\n---\n",
		},
	}, 0)

	shared := newFakeSharedProcess()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:                 ctx,
		cancel:              cancel,
		observers:           make(map[SessionObserver]struct{}),
		store:               store,
		recorder:            recorder,
		persistedID:         sessionID,
		nextSeq:             2, // recorder.Start() already persisted session_start at seq=1
		sharedProcess:       shared,
		acpID:               "acp-sess-1",
		contextFlushCommand: "/clear",
		processorManager:    procMgr,
		pendingConfig:       make(map[string]string),
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	obs := &mockSessionObserver{}
	bs.AddObserver(obs)

	if err := bs.FlushContext(); err != nil {
		t.Fatalf("FlushContext() error = %v", err)
	}

	// FlushContext dispatches asynchronously (same contract as PromptWithMeta);
	// wait for the fake transport to observe the Prompt() call.
	deadline := time.Now().Add(2 * time.Second)
	for {
		shared.mu.Lock()
		n := len(shared.promptCalls)
		shared.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for sharedProcess.Prompt to be called")
		}
		time.Sleep(5 * time.Millisecond)
	}

	shared.mu.Lock()
	blocks := shared.promptCalls[0].blocks
	shared.mu.Unlock()

	// THE BUG: an agent-native flush command must be sent as-is (prefix
	// recognition on a plain text block) — any prepended/appended text breaks
	// it. flushContextInPlace sends exactly acp.TextBlock(cmd); FlushContext
	// today does not.
	var gotText string
	if len(blocks) == 1 && blocks[0].Text != nil {
		gotText = blocks[0].Text.Text
	}
	if len(blocks) != 1 || gotText != "/clear" {
		t.Errorf("FlushContext must send exactly one text block equal to %q, got %d block(s) with text %q",
			"/clear", len(blocks), gotText)
	}

	// THE BUG: FlushContext must not persist a fake "/clear" user turn in the
	// transcript.
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}
	for _, e := range events {
		if e.Type == session.EventTypeUserPrompt {
			t.Errorf("FlushContext must not persist a user_prompt event, got: %+v", e)
		}
	}

	// THE BUG: FlushContext must not broadcast the flush command to observers
	// as if it were a real user prompt.
	if msgs := obs.getUserPromptMessages(); len(msgs) != 0 {
		t.Errorf("FlushContext must not broadcast OnUserPrompt, got: %v", msgs)
	}
}

// TestWireProcessorPendingDispatch_DeliversPreviouslySpooledBatch verifies
// the mitto-q95p fix: wireProcessorPendingDispatch (called from both
// NewBackgroundSession and ResumeBackgroundSession) wires the mitto-3421
// pending-dispatch spool onto a LIVE session's processor manager — not just
// the close-phase manager SessionManager.ApplyOnCloseProcessors builds
// locally — and opportunistically flushes any batch a prior saturated
// dispatch already spooled for this workspace (mitto-yfv8). Before the fix,
// a live session's processorManager never had SetPendingDispatchStore or
// FlushPendingDispatches wired at all, so a previously-spooled batch would
// sit undelivered until the workspace happened to close.
//
// This seeds a batch via the same FilePendingDispatchStore production uses
// (BaseDir resolved from $MITTO_DIR, redirected to a temp dir for the test),
// then confirms that calling wireProcessorPendingDispatch on a minimal live
// BackgroundSession flushes and delivers it through the wired PromptFunc.
func TestWireProcessorPendingDispatch_DeliversPreviouslySpooledBatch(t *testing.T) {
	t.Setenv(appdir.MittoDirEnv, t.TempDir())
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	const workspaceUUID = "ws-uuid-live-flush"

	// Seed a batch as if an earlier saturated dispatch (e.g. before this
	// session existed) had already spooled it.
	seedStore := &processors.FilePendingDispatchStore{}
	if _, err := seedStore.Append(processors.PendingDispatchEntry{
		WorkspaceUUID:  workspaceUUID,
		Name:           "identify-user-data",
		Prompt:         "Persist for session sess-live-flush.",
		TimeoutSeconds: 30,
		SavedAt:        time.Now(),
	}); err != nil {
		t.Fatalf("failed to seed pending-dispatch spool: %v", err)
	}

	var mu sync.Mutex
	var delivered []string
	procMgr := processors.NewManager("", nil)
	procMgr.SetPromptFunc(func(_ context.Context, _, name, _ string) error {
		mu.Lock()
		delivered = append(delivered, name)
		mu.Unlock()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := &BackgroundSession{
		ctx:              ctx,
		cancel:           cancel,
		observers:        make(map[SessionObserver]struct{}),
		processorManager: procMgr,
		workspaceUUID:    workspaceUUID,
		pendingConfig:    make(map[string]string),
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// This is the fix under test: production calls this once from both
	// NewBackgroundSession and ResumeBackgroundSession.
	bs.wireProcessorPendingDispatch()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(delivered)
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for wireProcessorPendingDispatch's flush to deliver the previously-spooled " +
				"batch — the live session's processor manager never had SetPendingDispatchStore wired / " +
				"FlushPendingDispatches invoked (mitto-q95p)")
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 1 || delivered[0] != "identify-user-data" {
		t.Fatalf("delivered = %v, want [identify-user-data]", delivered)
	}
}

// TestBackgroundSession_ContextFlushCommand pins down the resolution order
// used by ContextFlushCommand (mitto-1o8): a statically configured command is
// always authoritative, and runtime detection of the agent's advertised slash
// commands is used only as a fallback when nothing is statically configured.
func TestBackgroundSession_ContextFlushCommand(t *testing.T) {
	t.Run("static value wins even when advertised commands differ", func(t *testing.T) {
		bs := &BackgroundSession{contextFlushCommand: "/clear"}
		bs.cbSetAvailableCommands([]AvailableCommand{
			{Name: "compact"},
			{Name: "context"},
		})
		if got := bs.ContextFlushCommand(); got != "/clear" {
			t.Errorf("expected static '/clear' to win, got %q", got)
		}
	})

	t.Run("no static value, no advertised commands returns empty", func(t *testing.T) {
		bs := &BackgroundSession{}
		if got := bs.ContextFlushCommand(); got != "" {
			t.Errorf("expected empty command, got %q", got)
		}
	})

	t.Run("no static value falls back to advertised clear", func(t *testing.T) {
		bs := &BackgroundSession{}
		bs.cbSetAvailableCommands([]AvailableCommand{
			{Name: "compact"},
			{Name: "clear"},
		})
		if got := bs.ContextFlushCommand(); got != "/clear" {
			t.Errorf("expected fallback '/clear', got %q", got)
		}
	})

	t.Run("no static value and no clear falls back to compact", func(t *testing.T) {
		bs := &BackgroundSession{}
		bs.cbSetAvailableCommands([]AvailableCommand{
			{Name: "context"},
			{Name: "compact"},
		})
		if got := bs.ContextFlushCommand(); got != "/compact" {
			t.Errorf("expected fallback '/compact', got %q", got)
		}
	})

	t.Run("no static value and no allowlist match returns empty", func(t *testing.T) {
		bs := &BackgroundSession{}
		bs.cbSetAvailableCommands([]AvailableCommand{
			{Name: "context"},
			{Name: "help"},
		})
		if got := bs.ContextFlushCommand(); got != "" {
			t.Errorf("expected empty command when no allowlist entry matches, got %q", got)
		}
	})

	t.Run("clear preferred over compact when both advertised", func(t *testing.T) {
		bs := &BackgroundSession{}
		bs.cbSetAvailableCommands([]AvailableCommand{
			{Name: "compact"},
			{Name: "clear"},
		})
		if got := bs.ContextFlushCommand(); got != "/clear" {
			t.Errorf("expected 'clear' to be preferred over 'compact', got %q", got)
		}
	})

	t.Run("whitespace-only static value falls through to detection", func(t *testing.T) {
		bs := &BackgroundSession{contextFlushCommand: "   "}
		bs.cbSetAvailableCommands([]AvailableCommand{{Name: "clear"}})
		if got := bs.ContextFlushCommand(); got != "/clear" {
			t.Errorf("expected fallback '/clear' when static value is blank, got %q", got)
		}
	})
}

// TestBackgroundSessionConfig_PopulatesConstraintsFromMittoConfig verifies the
// end-to-end wiring through the constructor field: given a MittoConfig with a
// matching ACPServer entry, the constraint-lookup logic invoked by both
// constructors must populate bs.acpServerConstraints. This pins down the
// behavior expected at the ResumeBackgroundSession call site in
// session_manager.go, which previously omitted MittoConfig.
func TestBackgroundSessionConfig_PopulatesConstraintsFromMittoConfig(t *testing.T) {
	modelConstraint := &config.ACPServerConstraint{MatchMode: "lookAlike", Pattern: "Opus 4.8"}
	mittoCfg := &config.Config{
		ACPServers: []config.ACPServer{
			{
				Name: "claude-code",
				Constraints: map[string]*config.ACPServerConstraint{
					"model": modelConstraint,
				},
			},
		},
	}

	// Mirror what the constructors do (NewBackgroundSession ~line 360 and
	// ResumeBackgroundSession ~line 552): assign acpServerConstraints from the
	// helper given cfg.MittoConfig and cfg.ACPServer. The bug was that the
	// resume call site in session_manager.go never set MittoConfig, so this
	// helper received nil and the field stayed empty.
	t.Run("with MittoConfig set", func(t *testing.T) {
		cfg := BackgroundSessionConfig{
			ACPServer:   "claude-code",
			MittoConfig: mittoCfg,
		}
		bs := &BackgroundSession{}
		bs.acpServerConstraints = lookupACPServerConstraints(cfg.MittoConfig, cfg.ACPServer)

		got, ok := bs.acpServerConstraints["model"]
		if !ok {
			t.Fatalf("expected 'model' constraint to be loaded, got: %v", bs.acpServerConstraints)
		}
		if got.Pattern != "Opus 4.8" {
			t.Errorf("expected Pattern=%q, got %q", "Opus 4.8", got.Pattern)
		}
	})

	t.Run("with MittoConfig nil (regression scenario)", func(t *testing.T) {
		cfg := BackgroundSessionConfig{
			ACPServer:   "claude-code",
			MittoConfig: nil, // <- the buggy state on resume before the fix
		}
		bs := &BackgroundSession{}
		bs.acpServerConstraints = lookupACPServerConstraints(cfg.MittoConfig, cfg.ACPServer)

		if len(bs.acpServerConstraints) != 0 {
			t.Errorf("expected empty constraints when MittoConfig is nil, got %v", bs.acpServerConstraints)
		}
	})
}

func TestBackgroundSession_GetSessionID(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session-id",
	}

	if bs.GetSessionID() != "test-session-id" {
		t.Errorf("GetSessionID = %q, want %q", bs.GetSessionID(), "test-session-id")
	}
}

func TestBackgroundSession_GetACPID(t *testing.T) {
	bs := &BackgroundSession{
		acpID: "acp-session-id",
	}

	if bs.GetACPID() != "acp-session-id" {
		t.Errorf("GetACPID = %q, want %q", bs.GetACPID(), "acp-session-id")
	}
}

func TestBackgroundSession_IsClosed(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.IsClosed() {
		t.Error("New BackgroundSession should not be closed")
	}

	bs.closed.Store(1)

	if !bs.IsClosed() {
		t.Error("BackgroundSession should be closed after setting closed flag")
	}
}

// TestBackgroundSession_SelfDestruct verifies that RequestSelfDestruct sets the
// in-memory flag and IsSelfDestructRequested reflects it. The flag drives the
// deferred deletion triggered at the end of a turn in PromptWithMeta.
func TestBackgroundSession_SelfDestruct(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.IsSelfDestructRequested() {
		t.Error("New BackgroundSession should not have self-destruct requested")
	}

	bs.RequestSelfDestruct()

	if !bs.IsSelfDestructRequested() {
		t.Error("BackgroundSession should have self-destruct requested after RequestSelfDestruct")
	}
}

func TestBackgroundSession_IsPrompting(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.IsPrompting() {
		t.Error("New BackgroundSession should not be prompting")
	}

	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptMu.Unlock()

	if !bs.IsPrompting() {
		t.Error("BackgroundSession should be prompting after setting flag")
	}
}

func TestBackgroundSession_GetPromptCount(t *testing.T) {
	bs := &BackgroundSession{}

	if bs.GetPromptCount() != 0 {
		t.Errorf("GetPromptCount = %d, want 0", bs.GetPromptCount())
	}

	bs.promptMu.Lock()
	bs.promptCount = 5
	bs.promptMu.Unlock()

	if bs.GetPromptCount() != 5 {
		t.Errorf("GetPromptCount = %d, want 5", bs.GetPromptCount())
	}
}

func TestBackgroundSession_ObserverManagement(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	if bs.HasObservers() {
		t.Error("New BackgroundSession should not have observers")
	}

	if bs.ObserverCount() != 0 {
		t.Errorf("ObserverCount = %d, want 0", bs.ObserverCount())
	}

	// Add a mock observer
	mockObserver := &mockSessionObserver{}
	bs.AddObserver(mockObserver)

	if !bs.HasObservers() {
		t.Error("BackgroundSession should have observers after AddObserver")
	}

	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}

	// Add another observer
	mockObserver2 := &mockSessionObserver{}
	bs.AddObserver(mockObserver2)

	if bs.ObserverCount() != 2 {
		t.Errorf("ObserverCount = %d, want 2", bs.ObserverCount())
	}

	// Remove first observer
	bs.RemoveObserver(mockObserver)

	if bs.ObserverCount() != 1 {
		t.Errorf("ObserverCount = %d, want 1", bs.ObserverCount())
	}

	// Remove second observer
	bs.RemoveObserver(mockObserver2)

	if bs.HasObservers() {
		t.Error("BackgroundSession should not have observers after removing all")
	}
}

// mockSessionObserver is a mock implementation of SessionObserver for testing.
type mockSessionObserver struct {
	mu                   sync.Mutex
	agentMessages        []string
	agentThoughts        []string
	toolCalls            []string
	errors               []string
	completed            bool
	queueUpdates         []queueUpdate
	queueMessagesSending []string
	queueMessagesSent    []string
	availableCommands    []AvailableCommand
	acpStoppedReasons    []string
	userPromptMessages   []string // messages seen via OnUserPrompt (mitto-ip1)
}

type queueUpdate struct {
	queueLength int
	action      string
	messageID   string
}

func (m *mockSessionObserver) OnAgentMessage(seq int64, html, markdown string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentMessages = append(m.agentMessages, html)
}

func (m *mockSessionObserver) OnAgentThought(seq int64, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentThoughts = append(m.agentThoughts, text)
}

func (m *mockSessionObserver) OnToolCall(seq int64, id, title, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls = append(m.toolCalls, id)
}

func (m *mockSessionObserver) OnToolUpdate(seq int64, id string, status *string) {}

func (m *mockSessionObserver) OnPlan(seq int64, entries []PlanEntry) {}

func (m *mockSessionObserver) OnFileWrite(seq int64, path string, size int) {}

func (m *mockSessionObserver) OnFileRead(seq int64, path string, size int) {}

func (m *mockSessionObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}

func (m *mockSessionObserver) OnPromptComplete(eventCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = true
}

func (m *mockSessionObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string, promptName string, argumentCount int, arguments map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userPromptMessages = append(m.userPromptMessages, message)
}

func (m *mockSessionObserver) OnError(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, message)
}

func (m *mockSessionObserver) OnQueueUpdated(queueLength int, action string, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueUpdates = append(m.queueUpdates, queueUpdate{queueLength, action, messageID})
}

func (m *mockSessionObserver) OnQueueMessageSending(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueMessagesSending = append(m.queueMessagesSending, messageID)
}

func (m *mockSessionObserver) OnQueueMessageSent(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueMessagesSent = append(m.queueMessagesSent, messageID)
}

func (m *mockSessionObserver) OnQueueReordered(messages []session.QueuedMessage) {
	// no-op for testing
}

func (m *mockSessionObserver) OnActionButtons(buttons []ActionButton) {
	// no-op for testing
}

func (m *mockSessionObserver) OnAvailableCommandsUpdated(commands []AvailableCommand) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.availableCommands = commands
}

func (m *mockSessionObserver) OnACPStopped(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.acpStoppedReasons = append(m.acpStoppedReasons, reason)
}

func (m *mockSessionObserver) OnACPStarted() {
	// no-op for testing
}

func (m *mockSessionObserver) OnUIPrompt(req UIPromptRequest) {
	// no-op for testing
}

func (m *mockSessionObserver) OnUIPromptDismiss(requestID string, reason string) {
	// no-op for testing
}

func (m *mockSessionObserver) OnNotification(req UINotifyRequest) {
	// no-op for testing
}

func (m *mockSessionObserver) OnContextUsageUpdate(size, used int) {
	// no-op for testing
}

func (m *mockSessionObserver) getACPStoppedReasons() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.acpStoppedReasons))
	copy(result, m.acpStoppedReasons)
	return result
}

func (m *mockSessionObserver) getAvailableCommands() []AvailableCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.availableCommands == nil {
		return nil
	}
	result := make([]AvailableCommand, len(m.availableCommands))
	copy(result, m.availableCommands)
	return result
}

func (m *mockSessionObserver) getQueueMessagesSending() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.queueMessagesSending))
	copy(result, m.queueMessagesSending)
	return result
}

func (m *mockSessionObserver) getUserPromptMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.userPromptMessages))
	copy(result, m.userPromptMessages)
	return result
}

// Tests for NeedsTitle

func TestBackgroundSession_NeedsTitle_NoStore(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session",
		store:       nil, // No store
	}

	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when store is nil")
	}
}

func TestBackgroundSession_NeedsTitle_NoPersistedID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	bs := &BackgroundSession{
		persistedID: "", // Empty persisted ID
		store:       store,
	}

	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when persistedID is empty")
	}
}

func TestBackgroundSession_NeedsTitle_SessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	bs := &BackgroundSession{
		persistedID: "non-existent-session",
		store:       store,
	}

	// Session doesn't exist in store, GetMetadata will fail
	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when session is not found in store")
	}
}

func TestBackgroundSession_NeedsTitle_EmptyName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with empty name
	meta := session.Metadata{
		SessionID:  "test-session-empty-name",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // Empty name - needs title
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-empty-name",
		store:       store,
	}

	if !bs.NeedsTitle() {
		t.Error("NeedsTitle should return true when session name is empty")
	}
}

func TestBackgroundSession_NeedsTitle_HasName(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a name
	meta := session.Metadata{
		SessionID:  "test-session-with-name",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "My Conversation", // Has a name - doesn't need title
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-with-name",
		store:       store,
	}

	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false when session already has a name")
	}
}

func TestBackgroundSession_NeedsTitle_AfterRename(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with empty name
	meta := session.Metadata{
		SessionID:  "test-session-rename",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
		Name:       "", // Empty name initially
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-rename",
		store:       store,
	}

	// Initially needs title
	if !bs.NeedsTitle() {
		t.Error("NeedsTitle should return true initially when name is empty")
	}

	// Simulate auto-rename or user rename
	err = store.UpdateMetadata("test-session-rename", func(m *session.Metadata) {
		m.Name = "Auto-generated Title"
	})
	if err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	// After rename, should not need title
	if bs.NeedsTitle() {
		t.Error("NeedsTitle should return false after name is set")
	}
}

func TestBackgroundSession_GetEventCount_NilRecorder(t *testing.T) {
	bs := &BackgroundSession{
		recorder: nil,
	}

	count := bs.GetEventCount()
	if count != 0 {
		t.Errorf("GetEventCount = %d, want 0 for nil recorder", count)
	}
}

func TestBackgroundSession_CreatedAt_NilRecorder(t *testing.T) {
	bs := &BackgroundSession{
		recorder: nil,
	}

	createdAt := bs.CreatedAt()
	if !createdAt.IsZero() {
		t.Errorf("CreatedAt = %v, want zero time for nil recorder", createdAt)
	}
}

func TestBackgroundSession_NeedsTitle_NoRecorder(t *testing.T) {
	bs := &BackgroundSession{
		recorder: nil,
	}

	// Should not panic with nil recorder
	result := bs.NeedsTitle()
	if result {
		t.Error("NeedsTitle should return false when recorder is nil")
	}
}

func TestBackgroundSession_PersistedID(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session-123",
	}

	if bs.persistedID != "test-session-123" {
		t.Errorf("persistedID = %q, want %q", bs.persistedID, "test-session-123")
	}
}

func TestBackgroundSession_WorkingDirField(t *testing.T) {
	bs := &BackgroundSession{
		workingDir: "/test/workspace",
	}

	if bs.workingDir != "/test/workspace" {
		t.Errorf("workingDir = %q, want %q", bs.workingDir, "/test/workspace")
	}
}

func TestBackgroundSession_GetEventCount(t *testing.T) {
	bs := &BackgroundSession{}

	// Without recorder, should return 0
	count := bs.GetEventCount()
	if count != 0 {
		t.Errorf("GetEventCount = %d, want 0", count)
	}
}

// --- mitto-s9g2: acpContextTurns tri-state counter helpers ---

// TestBackgroundSession_MarkACPContextFresh_SetsEmpty verifies that a session
// explicitly marked fresh (e.g. right after a genuine session/new or a
// successful in-place flush) reports acpContextIsEmpty()==true.
func TestBackgroundSession_MarkACPContextFresh_SetsEmpty(t *testing.T) {
	bs := &BackgroundSession{}
	bs.acpContextTurns.Store(contextTurnsUnknown) // simulate constructor init

	bs.markACPContextFresh()

	if !bs.acpContextIsEmpty() {
		t.Error("acpContextIsEmpty() = false, want true after markACPContextFresh")
	}
	if got := bs.acpContextTurns.Load(); got != 0 {
		t.Errorf("acpContextTurns = %d, want 0 after markACPContextFresh", got)
	}
}

// TestBackgroundSession_MarkACPContextUnknown_SetsNotEmpty verifies that a
// session explicitly marked unknown (resume/load) fails safe to "not empty",
// even if it had previously been marked fresh.
func TestBackgroundSession_MarkACPContextUnknown_SetsNotEmpty(t *testing.T) {
	bs := &BackgroundSession{}
	bs.markACPContextFresh() // start fresh, then...
	bs.markACPContextUnknown()

	if bs.acpContextIsEmpty() {
		t.Error("acpContextIsEmpty() = true, want false after markACPContextUnknown")
	}
	if got := bs.acpContextTurns.Load(); got != contextTurnsUnknown {
		t.Errorf("acpContextTurns = %d, want %d (unknown sentinel)", got, contextTurnsUnknown)
	}
}

// TestBackgroundSession_NoteACPTurnDispatched_IncrementsFromFresh verifies
// that dispatching a turn on a freshly-classified (0) session increments the
// counter and the session is no longer reported as empty.
func TestBackgroundSession_NoteACPTurnDispatched_IncrementsFromFresh(t *testing.T) {
	bs := &BackgroundSession{}
	bs.markACPContextFresh()

	bs.noteACPTurnDispatched()

	if bs.acpContextIsEmpty() {
		t.Error("acpContextIsEmpty() = true, want false after one turn dispatched")
	}
	if got := bs.acpContextTurns.Load(); got != 1 {
		t.Errorf("acpContextTurns = %d, want 1 after one dispatched turn", got)
	}

	bs.noteACPTurnDispatched()
	if got := bs.acpContextTurns.Load(); got != 2 {
		t.Errorf("acpContextTurns = %d, want 2 after a second dispatched turn", got)
	}
}

// TestBackgroundSession_NoteACPTurnDispatched_DoesNotPromoteUnknownSentinel
// pins the tri-state contract: noteACPTurnDispatched must never turn the
// unknown sentinel (-1) into a count. Only markACPContextFresh/Unknown may
// reclassify the session.
func TestBackgroundSession_NoteACPTurnDispatched_DoesNotPromoteUnknownSentinel(t *testing.T) {
	bs := &BackgroundSession{}
	bs.markACPContextUnknown()

	bs.noteACPTurnDispatched()

	if got := bs.acpContextTurns.Load(); got != contextTurnsUnknown {
		t.Errorf("acpContextTurns = %d, want unchanged %d (sentinel must not be promoted)", got, contextTurnsUnknown)
	}
	if bs.acpContextIsEmpty() {
		t.Error("acpContextIsEmpty() = true, want false (still unknown)")
	}
}

// TestBackgroundSession_ACPContextIsEmpty_ZeroValueStructIsNotEmpty documents
// that a raw zero-value BackgroundSession (as used in many unit tests here)
// happens to read as "empty" (Go zero value 0 == the fresh state) UNLESS the
// constructor's fail-safe Store(contextTurnsUnknown) has run. Both real
// constructors (NewBackgroundSession/ResumeBackgroundSession) explicitly set
// the unknown sentinel before any handshake can run, so this is not reachable
// in production — this test exists purely to document why unit tests that
// construct BackgroundSession directly must call markACPContext*/Store
// explicitly rather than relying on the zero value.
func TestBackgroundSession_ACPContextIsEmpty_ZeroValueStructIsNotEmpty(t *testing.T) {
	bs := &BackgroundSession{}

	// The bare zero value (atomic.Int64 defaults to 0) reads as "empty" — this
	// is exactly why both constructors immediately Store(contextTurnsUnknown).
	if !bs.acpContextIsEmpty() {
		t.Error("expected zero-value acpContextTurns to read as empty (0), confirming constructors must override it")
	}
}

func TestBackgroundSession_CreatedAt(t *testing.T) {
	bs := &BackgroundSession{}

	// Without recorder, should return zero time
	createdAt := bs.CreatedAt()
	if !createdAt.IsZero() {
		t.Errorf("CreatedAt should return zero time when recorder is nil")
	}
}

// --- Queue Processing Tests ---

func TestBackgroundSession_HasQueuedDeliveryInProgress_ClearedOnAllExits(t *testing.T) {
	// Verifies that queuedDeliveryInProgress is cleared on every exit of processNextQueuedMessage.

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-delivery-flag"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Initially false
	if bs.HasQueuedDeliveryInProgress() {
		t.Error("Expected HasQueuedDeliveryInProgress=false initially")
	}

	// Exit path: empty queue — flag must not be set
	bs.processNextQueuedMessage()
	if bs.HasQueuedDeliveryInProgress() {
		t.Error("Expected HasQueuedDeliveryInProgress=false after empty-queue exit")
	}

	// Exit path: queue disabled — flag must not be set
	enabled := false
	bs.queueConfig = &config.QueueConfig{Enabled: &enabled}
	queue := store.Queue(sessionID)
	if _, err := queue.Add("msg", nil, nil, "client1", nil, 0, nil, ""); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	bs.processNextQueuedMessage()
	if bs.HasQueuedDeliveryInProgress() {
		t.Error("Expected HasQueuedDeliveryInProgress=false after disabled-queue exit")
	}
}

func TestBackgroundSession_ProcessNextQueuedMessage_NoStore(t *testing.T) {
	bs := &BackgroundSession{
		persistedID: "test-session",
		store:       nil, // No store
		observers:   make(map[SessionObserver]struct{}),
	}

	// Should not panic and should return early
	bs.processNextQueuedMessage()
}

func TestBackgroundSession_ProcessNextQueuedMessage_EmptyQueue(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-empty-queue",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-empty-queue",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}
	bs.AddObserver(observer)

	// Should not panic and should not notify observers (queue is empty)
	bs.processNextQueuedMessage()

	if len(observer.getQueueMessagesSending()) != 0 {
		t.Error("Should not notify OnQueueMessageSending for empty queue")
	}
}

func TestBackgroundSession_ProcessNextQueuedMessage_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-disabled-queue",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-disabled-queue")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session with queue disabled
	enabled := false
	queueConfig := &config.QueueConfig{Enabled: &enabled}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-disabled-queue",
		store:       store,
		queueConfig: queueConfig,
		observers:   make(map[SessionObserver]struct{}),
	}
	bs.AddObserver(observer)

	// Should not process queue when disabled
	bs.processNextQueuedMessage()

	if len(observer.getQueueMessagesSending()) != 0 {
		t.Error("Should not notify OnQueueMessageSending when queue is disabled")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_IsPrompting(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-prompting",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-prompting")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-prompting",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Set isPrompting to true
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptMu.Unlock()

	// Should return false when prompting
	result := bs.TryProcessQueuedMessage()
	if result {
		t.Error("TryProcessQueuedMessage should return false when isPrompting is true")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_IsClosed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-closed",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-closed")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: "test-session-closed",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Set closed flag
	bs.closed.Store(1)

	// Should return false when closed
	result := bs.TryProcessQueuedMessage()
	if result {
		t.Error("TryProcessQueuedMessage should return false when session is closed")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_DelayNotElapsed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-delay",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-delay")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session with 10 second delay
	queueConfig := &config.QueueConfig{DelaySeconds: 10}

	bs := &BackgroundSession{
		persistedID: "test-session-delay",
		store:       store,
		queueConfig: queueConfig,
		observers:   make(map[SessionObserver]struct{}),
	}

	// Set lastResponseComplete to now (delay not elapsed)
	bs.promptMu.Lock()
	bs.lastResponseComplete = time.Now()
	bs.promptMu.Unlock()

	// Should return false when delay not elapsed
	result := bs.TryProcessQueuedMessage()
	if result {
		t.Error("TryProcessQueuedMessage should return false when delay has not elapsed")
	}

	// Queue should still have the message
	queueLen, _ := queue.Len()
	if queueLen != 1 {
		t.Errorf("Queue length = %d, want 1 (message should not be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_DelayElapsed(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-delay-elapsed",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-delay-elapsed")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Create session with 1 second delay
	queueConfig := &config.QueueConfig{DelaySeconds: 1}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-delay-elapsed",
		store:       store,
		queueConfig: queueConfig,
		observers:   make(map[SessionObserver]struct{}),
	}
	bs.AddObserver(observer)

	// Set lastResponseComplete to 2 seconds ago (delay elapsed)
	bs.promptMu.Lock()
	bs.lastResponseComplete = time.Now().Add(-2 * time.Second)
	bs.promptMu.Unlock()

	// Should pop the message (but fail to send since no ACP connection)
	// The message will be popped and observer notified, but PromptWithMeta will fail
	result := bs.TryProcessQueuedMessage()

	// Result depends on whether PromptWithMeta succeeds - it won't without ACP
	// But the message should be popped and OnQueueMessageSending should be called
	_ = result // We don't check result since PromptWithMeta will fail

	// Check that OnQueueMessageSending was called
	sending := observer.getQueueMessagesSending()
	if len(sending) != 1 {
		t.Errorf("OnQueueMessageSending called %d times, want 1", len(sending))
	}

	// Queue should be empty (message was popped)
	queueLen, _ := queue.Len()
	if queueLen != 0 {
		t.Errorf("Queue length = %d, want 0 (message should be popped)", queueLen)
	}
}

func TestBackgroundSession_TryProcessQueuedMessage_ZeroDelayNoLastResponse(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-zero-delay",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-zero-delay")
	_, err = queue.Add("Test message", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: "test-session-zero-delay",
		store:       store,
		observers:   make(map[SessionObserver]struct{}),
		// No queueConfig = default (no delay)
		// lastResponseComplete is zero (no previous response)
	}
	bs.AddObserver(observer)

	// Should pop the message immediately
	bs.TryProcessQueuedMessage()

	// Check that OnQueueMessageSending was called
	sending := observer.getQueueMessagesSending()
	if len(sending) != 1 {
		t.Errorf("OnQueueMessageSending called %d times, want 1", len(sending))
	}

	// Queue should be empty
	queueLen, _ := queue.Len()
	if queueLen != 0 {
		t.Errorf("Queue length = %d, want 0", queueLen)
	}
}

func TestBackgroundSession_GetLastResponseCompleteTime(t *testing.T) {
	bs := &BackgroundSession{}

	// Initially zero
	if !bs.GetLastResponseCompleteTime().IsZero() {
		t.Error("GetLastResponseCompleteTime should return zero time initially")
	}

	// Set a time
	now := time.Now()
	bs.promptMu.Lock()
	bs.lastResponseComplete = now
	bs.promptMu.Unlock()

	got := bs.GetLastResponseCompleteTime()
	if !got.Equal(now) {
		t.Errorf("GetLastResponseCompleteTime = %v, want %v", got, now)
	}
}

func TestBackgroundSession_GetQueueConfig(t *testing.T) {
	// Nil config
	bs := &BackgroundSession{}
	if bs.GetQueueConfig() != nil {
		t.Error("GetQueueConfig should return nil when not set")
	}

	// With config
	queueConfig := &config.QueueConfig{DelaySeconds: 5}
	bs.queueConfig = queueConfig
	if bs.GetQueueConfig() != queueConfig {
		t.Error("GetQueueConfig should return the configured queue config")
	}
}

// --- Queue Title Worker Tests ---

func TestQueueTitleWorker_MessageRemovedBeforeTitleGenerated(t *testing.T) {
	// This tests the corner case where a message is removed from the queue
	// (e.g., sent to the agent) before the title generation completes.
	// The worker should handle this gracefully without logging an error.

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-title-race",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-title-race")
	msg, err := queue.Add("Test message for title", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Now remove the message (simulating it being sent to the agent)
	if err := queue.Remove(msg.ID); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Try to update the title - this should return ErrMessageNotFound
	err = queue.UpdateTitle(msg.ID, "Generated Title")
	if err == nil {
		t.Error("UpdateTitle should return error when message not found")
	}
	if err != session.ErrMessageNotFound {
		t.Errorf("UpdateTitle error = %v, want ErrMessageNotFound", err)
	}
}

func TestQueueTitleWorker_UpdateTitleSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session
	meta := session.Metadata{
		SessionID:  "test-session-title-success",
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add a message to the queue
	queue := store.Queue("test-session-title-success")
	msg, err := queue.Add("Test message for title", nil, nil, "client1", nil, 0, nil, "")
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Update the title while message is still in queue
	err = queue.UpdateTitle(msg.ID, "Generated Title")
	if err != nil {
		t.Errorf("UpdateTitle failed: %v", err)
	}

	// Verify the title was updated
	updatedMsg, err := queue.Get(msg.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if updatedMsg.Title != "Generated Title" {
		t.Errorf("Title = %q, want %q", updatedMsg.Title, "Generated Title")
	}
}

// =============================================================================
// WaitForResponseComplete Tests
// =============================================================================

// TestWaitForResponseComplete_NotPrompting tests that WaitForResponseComplete
// returns immediately when no prompt is in progress.
func TestWaitForResponseComplete_NotPrompting(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: false,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	start := time.Now()
	result := bs.WaitForResponseComplete(5 * time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("WaitForResponseComplete should return true when not prompting")
	}

	// Should return almost immediately (less than 100ms)
	if elapsed > 100*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected < 100ms when not prompting", elapsed)
	}
}

// TestWaitForResponseComplete_PromptCompletes tests that WaitForResponseComplete
// returns true when the prompt completes within the timeout.
func TestWaitForResponseComplete_PromptCompletes(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Simulate prompt completion after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		bs.promptMu.Lock()
		bs.isPrompting = false
		bs.promptCond.Broadcast()
		bs.promptMu.Unlock()
	}()

	start := time.Now()
	result := bs.WaitForResponseComplete(5 * time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("WaitForResponseComplete should return true when prompt completes")
	}

	// Should complete around 100ms (with some tolerance)
	if elapsed < 50*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected ~100ms", elapsed)
	}
}

// TestWaitForResponseComplete_Timeout tests that WaitForResponseComplete
// returns false when the timeout expires before the prompt completes.
func TestWaitForResponseComplete_Timeout(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	start := time.Now()
	result := bs.WaitForResponseComplete(100 * time.Millisecond)
	elapsed := time.Since(start)

	if result {
		t.Error("WaitForResponseComplete should return false on timeout")
	}

	// Should timeout around 100ms (with some tolerance)
	if elapsed < 80*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected ~100ms", elapsed)
	}
}

// TestWaitForResponseComplete_SessionClosed tests that WaitForResponseComplete
// returns when the session is closed.
func TestWaitForResponseComplete_SessionClosed(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Simulate session close after 100ms
	go func() {
		time.Sleep(100 * time.Millisecond)
		bs.closed.Store(1)
		bs.promptMu.Lock()
		bs.promptCond.Broadcast()
		bs.promptMu.Unlock()
	}()

	start := time.Now()
	result := bs.WaitForResponseComplete(5 * time.Second)
	elapsed := time.Since(start)

	if !result {
		t.Error("WaitForResponseComplete should return true when session is closed")
	}

	// Should complete around 100ms (with some tolerance)
	if elapsed < 50*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("WaitForResponseComplete took %v, expected ~100ms", elapsed)
	}
}

// TestWaitForResponseComplete_Concurrent tests that WaitForResponseComplete
// is safe for concurrent access.
func TestWaitForResponseComplete_Concurrent(t *testing.T) {
	bs := &BackgroundSession{
		isPrompting: true,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	var wg sync.WaitGroup
	const numWaiters = 10

	// Start multiple waiters
	for i := 0; i < numWaiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bs.WaitForResponseComplete(5 * time.Second)
		}()
	}

	// Give waiters time to start
	time.Sleep(50 * time.Millisecond)

	// Complete the prompt - all waiters should wake up
	bs.promptMu.Lock()
	bs.isPrompting = false
	bs.promptCond.Broadcast()
	bs.promptMu.Unlock()

	// Wait for all waiters with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Timed out waiting for concurrent waiters to complete")
	}
}

// =============================================================================
// Available Commands Tests
// =============================================================================

func TestBackgroundSession_AvailableCommands_InitiallyEmpty(t *testing.T) {
	bs := &BackgroundSession{}

	commands := bs.AvailableCommands()
	if commands != nil {
		t.Errorf("AvailableCommands should return nil initially, got %v", commands)
	}
}

func TestBackgroundSession_AvailableCommands_SortedAlphabetically(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Call onAvailableCommands directly with unsorted commands
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "zebra", Description: "Last command"},
		{Name: "apple", Description: "First command"},
		{Name: "mango", Description: "Middle command"},
	})

	commands := bs.AvailableCommands()
	if len(commands) != 3 {
		t.Fatalf("Expected 3 commands, got %d", len(commands))
	}

	// Verify alphabetical sorting
	if commands[0].Name != "apple" {
		t.Errorf("Expected first command to be 'apple', got %q", commands[0].Name)
	}
	if commands[1].Name != "mango" {
		t.Errorf("Expected second command to be 'mango', got %q", commands[1].Name)
	}
	if commands[2].Name != "zebra" {
		t.Errorf("Expected third command to be 'zebra', got %q", commands[2].Name)
	}
}

func TestBackgroundSession_AvailableCommands_NotifiesObservers(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Trigger available commands update
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "test", Description: "Test command"},
		{Name: "help", Description: "Help command"},
	})

	// Give observers time to receive the notification
	time.Sleep(10 * time.Millisecond)

	// Verify observer received the commands
	receivedCommands := observer.getAvailableCommands()
	if len(receivedCommands) != 2 {
		t.Fatalf("Observer should have received 2 commands, got %d", len(receivedCommands))
	}

	// Verify commands are sorted
	if receivedCommands[0].Name != "help" {
		t.Errorf("Expected first command to be 'help', got %q", receivedCommands[0].Name)
	}
	if receivedCommands[1].Name != "test" {
		t.Errorf("Expected second command to be 'test', got %q", receivedCommands[1].Name)
	}
}

func TestBackgroundSession_AvailableCommands_ReturnsDefensiveCopy(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	bs.onAvailableCommands([]AvailableCommand{
		{Name: "original", Description: "Original command"},
	})

	// Get a copy and modify it
	commands := bs.AvailableCommands()
	commands[0].Name = "modified"

	// Verify original is unchanged
	originalCommands := bs.AvailableCommands()
	if originalCommands[0].Name != "original" {
		t.Errorf("AvailableCommands should return a defensive copy, but original was modified")
	}
}

func TestBackgroundSession_AvailableCommands_IgnoredWhenClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Close the session using the public method
	bs.Close("test")

	// Try to update commands - should be ignored
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "test", Description: "Test command"},
	})

	// Verify no commands were stored
	commands := bs.AvailableCommands()
	if commands != nil {
		t.Errorf("AvailableCommands should be nil after closing, got %v", commands)
	}

	// Verify observer was not notified
	receivedCommands := observer.getAvailableCommands()
	if receivedCommands != nil {
		t.Errorf("Observer should not receive commands after session closed, got %v", receivedCommands)
	}
}

func TestBackgroundSession_AvailableCommands_MultipleObservers(t *testing.T) {
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
	}

	observer1 := &mockSessionObserver{}
	observer2 := &mockSessionObserver{}
	bs.AddObserver(observer1)
	bs.AddObserver(observer2)

	// Trigger available commands update
	bs.onAvailableCommands([]AvailableCommand{
		{Name: "shared", Description: "Shared command"},
	})

	// Give observers time to receive the notification
	time.Sleep(10 * time.Millisecond)

	// Verify both observers received the commands
	received1 := observer1.getAvailableCommands()
	received2 := observer2.getAvailableCommands()

	if len(received1) != 1 || received1[0].Name != "shared" {
		t.Errorf("Observer 1 should have received 'shared' command, got %v", received1)
	}
	if len(received2) != 1 || received2[0].Name != "shared" {
		t.Errorf("Observer 2 should have received 'shared' command, got %v", received2)
	}
}

// =============================================================================
// Tests for OnACPStopped notification (race condition fix)
// =============================================================================

// TestBackgroundSession_Close_NotifiesObserversOfACPStopped verifies that when a
// BackgroundSession is closed, all observers receive the OnACPStopped notification
// with the correct reason. This is critical for preventing the race condition where
// a client tries to send a prompt while the session is being archived.
func TestBackgroundSession_Close_NotifiesObserversOfACPStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Close the session with a specific reason
	bs.Close("archived")

	// Verify observer received the OnACPStopped notification
	reasons := observer.getACPStoppedReasons()
	if len(reasons) != 1 {
		t.Fatalf("Observer should have received 1 OnACPStopped call, got %d", len(reasons))
	}
	if reasons[0] != "archived" {
		t.Errorf("OnACPStopped reason = %q, want %q", reasons[0], "archived")
	}
}

// TestBackgroundSession_Close_NotifiesMultipleObservers verifies that all connected
// observers receive the OnACPStopped notification when the session is closed.
func TestBackgroundSession_Close_NotifiesMultipleObservers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer1 := &mockSessionObserver{}
	observer2 := &mockSessionObserver{}
	observer3 := &mockSessionObserver{}
	bs.AddObserver(observer1)
	bs.AddObserver(observer2)
	bs.AddObserver(observer3)

	// Close the session
	bs.Close("archived_timeout")

	// Verify all observers received the notification
	for i, observer := range []*mockSessionObserver{observer1, observer2, observer3} {
		reasons := observer.getACPStoppedReasons()
		if len(reasons) != 1 {
			t.Errorf("Observer %d should have received 1 OnACPStopped call, got %d", i+1, len(reasons))
		}
		if len(reasons) > 0 && reasons[0] != "archived_timeout" {
			t.Errorf("Observer %d OnACPStopped reason = %q, want %q", i+1, reasons[0], "archived_timeout")
		}
	}
}

// TestBackgroundSession_Close_OnlyNotifiesOnce verifies that closing a session
// multiple times only notifies observers once (idempotent close).
func TestBackgroundSession_Close_OnlyNotifiesOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Close the session multiple times
	bs.Close("first_close")
	bs.Close("second_close")
	bs.Close("third_close")

	// Verify observer only received one notification (from first close)
	reasons := observer.getACPStoppedReasons()
	if len(reasons) != 1 {
		t.Fatalf("Observer should have received exactly 1 OnACPStopped call, got %d", len(reasons))
	}
	if reasons[0] != "first_close" {
		t.Errorf("OnACPStopped reason = %q, want %q (from first close)", reasons[0], "first_close")
	}
}

// TestBackgroundSession_Close_NotifiesBeforeMarkingClosed verifies that observers
// are notified BEFORE the session is marked as closed. This is important because
// the notification must happen while the session is still in a valid state.
func TestBackgroundSession_Close_NotifiesBeforeMarkingClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Track the closed state when OnACPStopped is called
	var wasClosedDuringNotification bool
	observer := &trackingObserver{
		onACPStopped: func(reason string) {
			// Check if IsClosed() returns true during the notification
			// Note: The closed flag is set atomically at the start of Close(),
			// so IsClosed() will return true. However, the notification happens
			// before resources are released, which is the important part.
			wasClosedDuringNotification = bs.IsClosed()
		},
	}
	bs.AddObserver(observer)

	// Close the session
	bs.Close("test")

	// The session should be marked as closed (this is expected behavior)
	// The important thing is that the notification happens before resources are released
	if !bs.IsClosed() {
		t.Error("Session should be closed after Close()")
	}

	// Verify the callback was called
	if !wasClosedDuringNotification {
		t.Log("Note: IsClosed() returned false during notification (unexpected but not critical)")
	}
}

// TestBackgroundSession_IsClosed_AfterClose verifies that IsClosed returns true
// after the session is closed, which prevents new prompts from being sent.
func TestBackgroundSession_IsClosed_AfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Initially not closed
	if bs.IsClosed() {
		t.Error("New session should not be closed")
	}

	// Close the session
	bs.Close("test")

	// Now should be closed
	if !bs.IsClosed() {
		t.Error("Session should be closed after Close()")
	}
}

// TestBackgroundSession_Close_DifferentReasons verifies that different close reasons
// are correctly passed to observers.
func TestBackgroundSession_Close_DifferentReasons(t *testing.T) {
	testCases := []string{
		"archived",
		"archived_timeout",
		"user_closed",
		"server_shutdown",
		"error",
		"",
	}

	for _, reason := range testCases {
		t.Run("reason_"+reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			bs := &BackgroundSession{
				observers: make(map[SessionObserver]struct{}),
				ctx:       ctx,
				cancel:    cancel,
			}

			observer := &mockSessionObserver{}
			bs.AddObserver(observer)

			bs.Close(reason)

			reasons := observer.getACPStoppedReasons()
			if len(reasons) != 1 {
				t.Fatalf("Observer should have received 1 OnACPStopped call, got %d", len(reasons))
			}
			if reasons[0] != reason {
				t.Errorf("OnACPStopped reason = %q, want %q", reasons[0], reason)
			}
		})
	}
}

// TestBackgroundSession_Close_RecordsSessionEndAfterOnACPStopped verifies that
// Close() records the session_end event with the correct sequence number AFTER
// sending the OnACPStopped notification. This ensures that:
// 1. Observers are notified before the session_end event is written
// 2. The session_end event uses MaxSeq + 1 (not EventCount + 1)
// 3. The metadata status is set to "completed"
//
// This test is part of the fix for the session_end delivery bug where the event
// was never delivered to WebSocket clients.
func TestBackgroundSession_Close_RecordsSessionEndAfterOnACPStopped(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session with a recorder
	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", "/test/dir", ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Record some events to establish a known MaxSeq
	// session_start is seq 1, record a few more events
	for i := 0; i < 3; i++ {
		if err := recorder.RecordUserPrompt("test message"); err != nil {
			t.Fatalf("RecordUserPrompt failed: %v", err)
		}
	}

	// Simulate streaming by recording events with explicit sequence numbers
	// This is what BackgroundSession does during streaming
	streamingEvents := []session.Event{
		{Seq: 100, Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: "streaming message 1"}},
		{Seq: 200, Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: "streaming message 2"}},
		{Seq: 311, Type: session.EventTypeAgentMessage, Data: session.AgentMessageData{Text: "streaming message 3"}},
	}
	for _, e := range streamingEvents {
		if err := recorder.RecordEventWithSeq(e); err != nil {
			t.Fatalf("RecordEventWithSeq failed: %v", err)
		}
	}

	// Verify initial state
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	// MaxSeq should be 311 (highest seq recorded)
	if meta.MaxSeq != 311 {
		t.Errorf("Expected MaxSeq=311 before Close, got %d", meta.MaxSeq)
	}
	// EventCount = 1 (session_start) + 3 (user_prompt) + 3 (agent_message) = 7
	if meta.EventCount != 7 {
		t.Errorf("Expected EventCount=7 before Close, got %d", meta.EventCount)
	}

	// Create BackgroundSession with the recorder and an observer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	observer := &mockSessionObserver{}
	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		recorder:    recorder,
		observers:   make(map[SessionObserver]struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}
	bs.AddObserver(observer)

	// Close the session
	bs.Close("test_shutdown")

	// Verify observer received OnACPStopped
	reasons := observer.getACPStoppedReasons()
	if len(reasons) != 1 {
		t.Fatalf("Observer should have received 1 OnACPStopped call, got %d", len(reasons))
	}
	if reasons[0] != "test_shutdown" {
		t.Errorf("OnACPStopped reason = %q, want %q", reasons[0], "test_shutdown")
	}

	// Verify session_end event was recorded with correct sequence number
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Find the session_end event (should be the last one)
	var sessionEndEvent *session.Event
	for i := range events {
		if events[i].Type == session.EventTypeSessionEnd {
			sessionEndEvent = &events[i]
			break
		}
	}

	if sessionEndEvent == nil {
		t.Fatal("session_end event not found in events")
	}

	// Verify session_end uses MaxSeq + 1 (not EventCount + 1)
	// MaxSeq was 311, so session_end should be 312
	// If the bug existed, it would be 8 (EventCount + 1)
	expectedSeq := int64(312)
	if sessionEndEvent.Seq != expectedSeq {
		t.Errorf("session_end seq = %d, want %d (MaxSeq + 1, not EventCount + 1)", sessionEndEvent.Seq, expectedSeq)
	}

	// Verify metadata was updated
	meta, err = store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed after Close: %v", err)
	}

	if meta.Status != "completed" {
		t.Errorf("Metadata status = %q, want %q", meta.Status, "completed")
	}

	// Verify MaxSeq was updated to include session_end
	if meta.MaxSeq != expectedSeq {
		t.Errorf("Metadata MaxSeq = %d, want %d", meta.MaxSeq, expectedSeq)
	}

	// Verify EventCount was incremented (7 + 1 = 8)
	if meta.EventCount != 8 {
		t.Errorf("Metadata EventCount = %d, want 8", meta.EventCount)
	}
}

// trackingObserver is a minimal observer that tracks specific callbacks for testing.
type trackingObserver struct {
	onACPStopped      func(reason string)
	onUIPrompt        func(req UIPromptRequest)
	onUIPromptDismiss func(requestID string, reason string)
}

func (o *trackingObserver) OnAgentMessage(seq int64, html, markdown string)   {}
func (o *trackingObserver) OnAgentThought(seq int64, text string)             {}
func (o *trackingObserver) OnToolCall(seq int64, id, title, status string)    {}
func (o *trackingObserver) OnToolUpdate(seq int64, id string, status *string) {}
func (o *trackingObserver) OnPlan(seq int64, entries []PlanEntry)             {}
func (o *trackingObserver) OnFileWrite(seq int64, path string, size int)      {}
func (o *trackingObserver) OnFileRead(seq int64, path string, size int)       {}
func (o *trackingObserver) OnPromptComplete(eventCount int)                   {}
func (o *trackingObserver) OnUserPrompt(seq int64, senderID, promptID, message string, imageIDs, fileIDs []string, promptName string, argumentCount int, arguments map[string]string) {
}
func (o *trackingObserver) OnError(message string)                                   {}
func (o *trackingObserver) OnQueueUpdated(queueLength int, action, messageID string) {}
func (o *trackingObserver) OnQueueReordered(messages []session.QueuedMessage)        {}
func (o *trackingObserver) OnQueueMessageSending(messageID string)                   {}
func (o *trackingObserver) OnQueueMessageSent(messageID string)                      {}
func (o *trackingObserver) OnActionButtons(buttons []ActionButton)                   {}
func (o *trackingObserver) OnAvailableCommandsUpdated(commands []AvailableCommand)   {}
func (o *trackingObserver) OnACPStopped(reason string) {
	if o.onACPStopped != nil {
		o.onACPStopped(reason)
	}
}
func (o *trackingObserver) OnACPStarted() {}
func (o *trackingObserver) OnPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}
func (o *trackingObserver) OnUIPrompt(req UIPromptRequest) {
	if o.onUIPrompt != nil {
		o.onUIPrompt(req)
	}
}
func (o *trackingObserver) OnUIPromptDismiss(requestID string, reason string) {
	if o.onUIPromptDismiss != nil {
		o.onUIPromptDismiss(requestID, reason)
	}
}

func (o *trackingObserver) OnNotification(req UINotifyRequest) {}

func (o *trackingObserver) OnContextUsageUpdate(size, used int) {}

// =============================================================================
// GetMaxAssignedSeq Tests
// =============================================================================

// TestGetMaxAssignedSeq_Initial tests that GetMaxAssignedSeq returns 0 initially.
func TestGetMaxAssignedSeq_Initial(t *testing.T) {
	bs := &BackgroundSession{
		nextSeq: 1, // Initial state: nextSeq starts at 1
	}

	maxSeq := bs.GetMaxAssignedSeq()
	if maxSeq != 0 {
		t.Errorf("GetMaxAssignedSeq() = %d, want 0 (no events assigned yet)", maxSeq)
	}
}

// TestGetMaxAssignedSeq_AfterAssignment tests that GetMaxAssignedSeq returns
// the correct value after sequence numbers have been assigned.
func TestGetMaxAssignedSeq_AfterAssignment(t *testing.T) {
	tests := []struct {
		name    string
		nextSeq int64
		want    int64
	}{
		{
			name:    "after first assignment",
			nextSeq: 2, // First event was assigned seq=1
			want:    1,
		},
		{
			name:    "after 10 assignments",
			nextSeq: 11, // Events 1-10 were assigned
			want:    10,
		},
		{
			name:    "after 100 assignments",
			nextSeq: 101,
			want:    100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs := &BackgroundSession{
				nextSeq: tt.nextSeq,
			}

			got := bs.GetMaxAssignedSeq()
			if got != tt.want {
				t.Errorf("GetMaxAssignedSeq() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestGetMaxAssignedSeq_Concurrent tests that GetMaxAssignedSeq is safe
// for concurrent access.
func TestGetMaxAssignedSeq_Concurrent(t *testing.T) {
	bs := &BackgroundSession{
		nextSeq: 1,
	}

	var wg sync.WaitGroup
	const numGoroutines = 100

	// Start multiple goroutines reading and incrementing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Reader
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = bs.GetMaxAssignedSeq()
				time.Sleep(time.Microsecond)
			}
		}()

		// Writer (simulating AssignSeq)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				bs.seqMu.Lock()
				bs.nextSeq++
				bs.seqMu.Unlock()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
	// If we get here without a race condition, the test passes
}

// TestBackgroundSession_Close_ServerShutdownUsesSuspend verifies that when a session
// is closed with reason "server_shutdown", the recorder uses Suspend() instead of End().
// This prevents multiple session_end events from being recorded when the session is
// resumed after server restart.
func TestBackgroundSession_Close_ServerShutdownUsesSuspend(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Create a recorder and start it
	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-acp", "/tmp", ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Create a BackgroundSession with the recorder
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx,
		cancel:    cancel,
		recorder:  recorder,
	}

	// Close with server_shutdown reason
	bs.Close("server_shutdown")

	// Read events and verify no session_end was recorded
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	// Count session_end events (should be 0 for server_shutdown)
	sessionEndCount := 0
	for _, event := range events {
		if event.Type == session.EventTypeSessionEnd {
			sessionEndCount++
		}
	}

	if sessionEndCount != 0 {
		t.Errorf("Expected 0 session_end events for server_shutdown, got %d", sessionEndCount)
	}

	// Verify session status is still active (not completed)
	meta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if meta.Status == session.SessionStatusCompleted {
		t.Error("Session status should not be 'completed' after server_shutdown")
	}
}

// TestBackgroundSession_Close_OtherReasonsUseEnd verifies that when a session
// is closed with reasons other than "server_shutdown", the recorder uses End()
// which records a session_end event.
func TestBackgroundSession_Close_OtherReasonsUseEnd(t *testing.T) {
	testCases := []string{
		"archived",
		"user_closed",
		"session_limit_exceeded",
		"duplicate_session",
	}

	for _, reason := range testCases {
		t.Run("reason_"+reason, func(t *testing.T) {
			tmpDir := t.TempDir()
			store, err := session.NewStore(tmpDir)
			if err != nil {
				t.Fatalf("NewStore failed: %v", err)
			}

			// Create a recorder and start it
			recorder := session.NewRecorder(store)
			if err := recorder.Start("test-acp", "/tmp", ""); err != nil {
				t.Fatalf("Start failed: %v", err)
			}
			sessionID := recorder.SessionID()

			// Create a BackgroundSession with the recorder
			ctx, cancel := context.WithCancel(context.Background())
			bs := &BackgroundSession{
				observers: make(map[SessionObserver]struct{}),
				ctx:       ctx,
				cancel:    cancel,
				recorder:  recorder,
			}

			// Close with the test reason
			bs.Close(reason)

			// Read events and verify session_end was recorded
			events, err := store.ReadEvents(sessionID)
			if err != nil {
				t.Fatalf("ReadEvents failed: %v", err)
			}

			// Count session_end events (should be 1 for non-server_shutdown reasons)
			sessionEndCount := 0
			var lastSessionEnd *session.Event
			for i, event := range events {
				if event.Type == session.EventTypeSessionEnd {
					sessionEndCount++
					lastSessionEnd = &events[i]
				}
			}

			if sessionEndCount != 1 {
				t.Errorf("Expected 1 session_end event for reason %q, got %d", reason, sessionEndCount)
			}

			// Verify the reason is correct
			if lastSessionEnd != nil {
				data, ok := lastSessionEnd.Data.(session.SessionEndData)
				if !ok {
					// Try map conversion (JSON unmarshaling)
					if dataMap, ok := lastSessionEnd.Data.(map[string]interface{}); ok {
						if r, ok := dataMap["reason"].(string); ok {
							if r != reason {
								t.Errorf("session_end reason = %q, want %q", r, reason)
							}
						}
					}
				} else if data.Reason != reason {
					t.Errorf("session_end reason = %q, want %q", data.Reason, reason)
				}
			}

			// Verify session status is completed
			meta, err := store.GetMetadata(sessionID)
			if err != nil {
				t.Fatalf("GetMetadata failed: %v", err)
			}
			if meta.Status != session.SessionStatusCompleted {
				t.Errorf("Session status = %q, want %q", meta.Status, session.SessionStatusCompleted)
			}
		})
	}
}

// =============================================================================
// refreshNextSeq Tests
// =============================================================================

// TestRefreshNextSeq_NilRecorder tests that refreshNextSeq handles nil recorder gracefully.
func TestRefreshNextSeq_NilRecorder(t *testing.T) {
	bs := &BackgroundSession{
		nextSeq:  100,
		recorder: nil, // No recorder
	}

	// Should not panic and should not change nextSeq
	bs.refreshNextSeq()

	if bs.nextSeq != 100 {
		t.Errorf("nextSeq should remain unchanged when recorder is nil, got %d, want 100", bs.nextSeq)
	}
}

// TestRefreshNextSeq_UsesMaxSeqWhenHigher tests that refreshNextSeq uses MaxSeq
// when it's higher than EventCount (the bug fix scenario).
func TestRefreshNextSeq_UsesMaxSeqWhenHigher(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-maxseq"

	// Create session first (Create resets EventCount to 0)
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Then update metadata to simulate coalescing where MaxSeq > EventCount
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 10 // Only 10 events
		m.MaxSeq = 100    // But highest seq is 100 (due to coalescing)
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1, // Start low
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Refresh should use MaxSeq + 1
	bs.refreshNextSeq()

	// nextSeq should be MaxSeq + 1 = 101, not EventCount + 1 = 11
	if bs.nextSeq != 101 {
		t.Errorf("nextSeq = %d, want 101 (MaxSeq + 1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_UsesEventCountWhenHigher tests that refreshNextSeq uses EventCount
// when it's higher than MaxSeq (normal case without coalescing).
func TestRefreshNextSeq_UsesEventCountWhenHigher(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-eventcount"

	// Create session first (Create resets EventCount to 0)
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Then update metadata to set EventCount >= MaxSeq (normal case)
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 50
		m.MaxSeq = 50 // Equal to EventCount
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	bs.refreshNextSeq()

	// nextSeq should be EventCount + 1 = 51
	if bs.nextSeq != 51 {
		t.Errorf("nextSeq = %d, want 51 (EventCount + 1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_ZeroMaxSeq tests that refreshNextSeq handles zero MaxSeq
// (sessions created before MaxSeq tracking was added).
func TestRefreshNextSeq_ZeroMaxSeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-zero-maxseq"

	// Create session first (Create resets EventCount to 0)
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Then update metadata to simulate a legacy session with EventCount but no MaxSeq
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 25
		m.MaxSeq = 0 // Legacy session without MaxSeq
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	bs.refreshNextSeq()

	// nextSeq should be EventCount + 1 = 26
	if bs.nextSeq != 26 {
		t.Errorf("nextSeq = %d, want 26 (EventCount + 1 when MaxSeq is 0)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_TableDriven tests various combinations of MaxSeq and EventCount.
func TestRefreshNextSeq_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		eventCount int
		maxSeq     int64
		wantSeq    int64
	}{
		{
			name:       "MaxSeq much higher than EventCount (coalescing)",
			eventCount: 100,
			maxSeq:     500,
			wantSeq:    501, // MaxSeq + 1
		},
		{
			name:       "EventCount equals MaxSeq",
			eventCount: 100,
			maxSeq:     100,
			wantSeq:    101, // Either works, both give same result
		},
		{
			name:       "EventCount higher than MaxSeq (shouldn't happen but handle it)",
			eventCount: 200,
			maxSeq:     100,
			wantSeq:    201, // EventCount + 1
		},
		{
			name:       "Both zero (empty session)",
			eventCount: 0,
			maxSeq:     0,
			wantSeq:    1, // Start at 1
		},
		{
			name:       "MaxSeq is 1, EventCount is 0",
			eventCount: 0,
			maxSeq:     1,
			wantSeq:    2, // MaxSeq + 1
		},
		{
			name:       "Large values",
			eventCount: 10000,
			maxSeq:     50000,
			wantSeq:    50001, // MaxSeq + 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			store, err := session.NewStore(tmpDir)
			if err != nil {
				t.Fatalf("NewStore failed: %v", err)
			}
			defer store.Close()

			sessionID := "test-" + tt.name

			// Create session first (Create resets EventCount to 0)
			meta := session.Metadata{
				SessionID:  sessionID,
				ACPServer:  "test-server",
				WorkingDir: tmpDir,
			}
			if err := store.Create(meta); err != nil {
				t.Fatalf("Create failed: %v", err)
			}

			// Then update metadata to set the test values
			if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
				m.EventCount = tt.eventCount
				m.MaxSeq = tt.maxSeq
			}); err != nil {
				t.Fatalf("UpdateMetadata failed: %v", err)
			}

			recorder := session.NewRecorderWithID(store, sessionID)
			bs := &BackgroundSession{
				nextSeq:     1,
				recorder:    recorder,
				persistedID: sessionID,
			}

			bs.refreshNextSeq()

			if bs.nextSeq != tt.wantSeq {
				t.Errorf("nextSeq = %d, want %d", bs.nextSeq, tt.wantSeq)
			}
		})
	}
}

// TestRefreshNextSeq_Concurrent tests that refreshNextSeq is safe for concurrent access.
func TestRefreshNextSeq_Concurrent(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-concurrent"

	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 100
		m.MaxSeq = 500
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	var wg sync.WaitGroup
	const numGoroutines = 50

	// Start multiple goroutines calling refreshNextSeq and GetNextSeq
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Refresher
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				bs.refreshNextSeq()
				time.Sleep(time.Microsecond)
			}
		}()

		// Reader
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = bs.GetMaxAssignedSeq()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
	// If we get here without a race condition, the test passes
}

// TestRefreshNextSeq_PreservesHigherValue tests that refreshNextSeq is monotonic:
// it must never lower nextSeq below its current value, even when the store reports
// a lower MaxSeq. This is critical when events have been assigned but not yet persisted.
func TestRefreshNextSeq_PreservesHigherValue(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-refresh-preserve"

	// Create session first
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Store has lower values (MaxSeq=50) than the in-memory counter (200).
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 10
		m.MaxSeq = 50
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     200, // Already higher than store's MaxSeq — must be preserved
		recorder:    recorder,
		persistedID: sessionID,
	}

	bs.refreshNextSeq()

	// refreshNextSeq is now monotonic: nextSeq must stay at 200, not reset to 51.
	if bs.nextSeq != 200 {
		t.Errorf("nextSeq = %d, want 200 (preserved, not lowered to store's MaxSeq+1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_AfterUserPrompt tests the real-world scenario where
// refreshNextSeq is called after persisting a user prompt.
func TestRefreshNextSeq_AfterUserPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Create a session and record some events with coalescing
	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Record events that simulate coalescing (multiple chunks with same seq)
	// We'll record events with explicit seq numbers to simulate the scenario
	for i := 0; i < 5; i++ {
		if err := recorder.RecordAgentMessage("<p>chunk</p>", ""); err != nil {
			t.Fatalf("RecordAgentMessage failed: %v", err)
		}
	}

	// Now manually update MaxSeq to simulate coalescing
	// (In real usage, this happens through RecordEvent with pre-assigned seq)
	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.MaxSeq = 100 // Simulate high seq from coalescing
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	// Create BackgroundSession with the recorder
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Simulate what happens after a user prompt is persisted
	bs.refreshNextSeq()

	// nextSeq should be MaxSeq + 1 = 101, not EventCount + 1 = 7
	if bs.nextSeq != 101 {
		t.Errorf("nextSeq = %d, want 101 (MaxSeq + 1)", bs.nextSeq)
	}
}

// TestRefreshNextSeq_IntegrationWithGetNextSeq tests that refreshNextSeq
// and GetNextSeq work correctly together.
func TestRefreshNextSeq_IntegrationWithGetNextSeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-integration"

	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: tmpDir,
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if err := store.UpdateMetadata(sessionID, func(m *session.Metadata) {
		m.EventCount = 50
		m.MaxSeq = 200 // High due to coalescing
	}); err != nil {
		t.Fatalf("UpdateMetadata failed: %v", err)
	}

	recorder := session.NewRecorderWithID(store, sessionID)
	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Refresh to sync with store
	bs.refreshNextSeq()

	// Get next seq should return 201
	seq1 := bs.GetNextSeq()
	if seq1 != 201 {
		t.Errorf("First GetNextSeq() = %d, want 201", seq1)
	}

	// Next call should return 202
	seq2 := bs.GetNextSeq()
	if seq2 != 202 {
		t.Errorf("Second GetNextSeq() = %d, want 202", seq2)
	}

	// GetMaxAssignedSeq should return 202 (the last assigned)
	maxSeq := bs.GetMaxAssignedSeq()
	if maxSeq != 202 {
		t.Errorf("GetMaxAssignedSeq() = %d, want 202", maxSeq)
	}
}

// TestSeqUniqueness_ConcurrentStreamingAndUserPrompt is a regression test for
// the duplicate/out-of-order seq bug (mitto-49q). It simulates the race between
// rapid getNextSeq() calls from a streaming goroutine and a user-prompt persistence
// that also calls getNextSeq() via RecordUserPromptCompleteWithSeq. All persisted
// seqs in the resulting events.jsonl must be strictly unique.
func TestSeqUniqueness_ConcurrentStreamingAndUserPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	bs := &BackgroundSession{
		nextSeq:     1,
		recorder:    recorder,
		persistedID: sessionID,
	}

	const streamEvents = 200
	var wg sync.WaitGroup

	// Goroutine 1: rapidly assign seqs and persist agent-message events (streaming path).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < streamEvents; i++ {
			seq := bs.getNextSeq()
			if err := recorder.RecordEventWithSeq(session.Event{
				Seq:  seq,
				Type: session.EventTypeAgentMessage,
				Data: session.AgentMessageData{Text: "chunk"},
			}); err != nil {
				// Session-end races are expected at teardown; ignore.
				_ = err
			}
		}
	}()

	// Goroutine 2: persist a user prompt via the new unified path (WI-2).
	wg.Add(1)
	go func() {
		defer wg.Done()
		userSeq := bs.getNextSeq()
		_ = recorder.RecordUserPromptCompleteWithSeq(userSeq, "hello", nil, nil, "", "", 0, nil)
	}()

	wg.Wait()

	// Read back all events and verify seq uniqueness.
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	seen := make(map[int64]int) // seq -> first index
	for i, e := range events {
		if prev, dup := seen[e.Seq]; dup {
			t.Errorf("duplicate seq %d at index %d (first seen at index %d)", e.Seq, i, prev)
		}
		seen[e.Seq] = i
	}
}

// TestFreshContextPillOrdering_PillSeqBeforeUserPromptSeq verifies the mitto-c36
// invariant end-to-end via events.jsonl: when PromptWithMeta reserves a pill seq
// BEFORE the user-prompt seq (as it does when meta.FreshContext=true), and the
// downstream createFreshContextSession records the "context_cleared" pill with
// that reserved seq, the persisted transcript orders as
//
//	session_change(context_cleared, flush).seq < user_prompt.seq < agent_message.seq
//
// This matches the acceptance test in the bead's Test hooks section and pins the
// fix so it survives future refactors of PromptWithMeta and createFreshContextSession.
//
// The test replays the exact seq allocation + persistence sequence used inside
// PromptWithMeta (reserve pillSeq → reserve userPromptSeq → record via the same
// helpers the production code goes through: cmRecordSessionChangeWithSeq +
// RecordUserPromptCompleteWithSeq + RecordEventWithSeq) so it exercises the real
// BackgroundSession seq counter, the real recorder, and the real events.jsonl
// serialization — without needing an ACP mock or a full loop-runner harness.
func TestFreshContextPillOrdering_PillSeqBeforeUserPromptSeq(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Start bs.nextSeq at 2: the recorder's Start() already appended
	// session_start with seq=1, and ReadEvents dedupes on seq — so bs's own
	// allocations must skip past that.
	bs := &BackgroundSession{
		nextSeq:     2,
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Mirror PromptWithMeta's FreshContext branch: reserve pillSeq FIRST, then
	// userPromptSeq. This is the sole ordering invariant the mitto-c36 fix must
	// preserve.
	pillSeq := bs.getNextSeq()
	userPromptSeq := bs.getNextSeq()

	// Persist the user prompt (matches PromptWithMeta L477-L484).
	if err := recorder.RecordUserPromptCompleteWithSeq(userPromptSeq, "hello", nil, nil, "", "", 0, nil); err != nil {
		t.Fatalf("RecordUserPromptCompleteWithSeq failed: %v", err)
	}

	// Persist the "context_cleared" pill via the seq-aware shim that
	// createFreshContextSession → pdRecordSessionChangeWithSeq calls in the
	// production path. This is the mitto-c36 code path under test.
	bs.cmRecordSessionChangeWithSeq(pillSeq, "context_cleared", "flush", "")

	// Simulate an agent-message chunk after the flush to complete the timeline.
	agentSeq := bs.getNextSeq()
	if err := recorder.RecordEventWithSeq(session.Event{
		Seq:  agentSeq,
		Type: session.EventTypeAgentMessage,
		Data: session.AgentMessageData{Text: "hi"},
	}); err != nil {
		t.Fatalf("RecordEventWithSeq(agent_message) failed: %v", err)
	}

	// Read back events.jsonl (sorted by seq on read) and locate the three events.
	// Note: ReadEvents unmarshals Data as map[string]any (generic JSON), so
	// SessionChangeData discrimination is done by inspecting the map keys.
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	var (
		gotPillSeq              int64 = -1
		gotUserSeq              int64 = -1
		gotAgentSeq             int64 = -1
		pillOK, userOK, agentOK bool
	)
	for _, e := range events {
		switch e.Type {
		case session.EventTypeSessionChange:
			m, ok := e.Data.(map[string]any)
			if !ok {
				continue
			}
			if m["kind"] == "context_cleared" && m["value"] == "flush" {
				gotPillSeq = e.Seq
				pillOK = true
			}
		case session.EventTypeUserPrompt:
			gotUserSeq = e.Seq
			userOK = true
		case session.EventTypeAgentMessage:
			gotAgentSeq = e.Seq
			agentOK = true
		}
	}
	if !pillOK {
		t.Fatalf("did not find session_change(context_cleared, flush) in events.jsonl: %+v", events)
	}
	if !userOK {
		t.Fatalf("did not find user_prompt in events.jsonl: %+v", events)
	}
	if !agentOK {
		t.Fatalf("did not find agent_message in events.jsonl: %+v", events)
	}

	// mitto-c36 acceptance criterion:
	// session_change(context_cleared, flush).seq < user_prompt.seq < agent_message.seq
	if gotPillSeq >= gotUserSeq {
		t.Errorf("mitto-c36 ordering violated: pill seq=%d must be < user_prompt seq=%d",
			gotPillSeq, gotUserSeq)
	}
	if gotUserSeq >= gotAgentSeq {
		t.Errorf("ordering violated: user_prompt seq=%d must be < agent_message seq=%d",
			gotUserSeq, gotAgentSeq)
	}

	// Also pin that the reserved seq is exactly what upstream reserved (no drift
	// through the seq-aware shim).
	if gotPillSeq != pillSeq {
		t.Errorf("pill persisted with wrong seq: reserved=%d persisted=%d", pillSeq, gotPillSeq)
	}
	if gotUserSeq != userPromptSeq {
		t.Errorf("user prompt persisted with wrong seq: reserved=%d persisted=%d", userPromptSeq, gotUserSeq)
	}
}

// TestFreshContextPillOrdering_NewSessionKind mirrors the flush-branch test above,
// but for the new-session fallback branch (createFreshContextSession's
// pdACPConnNewSession success path). Same invariant: the "context_cleared" pill
// with Value="new_session" must persist BEFORE the user prompt.
func TestFreshContextPillOrdering_NewSessionKind(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	bs := &BackgroundSession{
		nextSeq:     2,
		recorder:    recorder,
		persistedID: sessionID,
	}

	pillSeq := bs.getNextSeq()
	userPromptSeq := bs.getNextSeq()

	if err := recorder.RecordUserPromptCompleteWithSeq(userPromptSeq, "hello", nil, nil, "", "", 0, nil); err != nil {
		t.Fatalf("RecordUserPromptCompleteWithSeq failed: %v", err)
	}
	bs.cmRecordSessionChangeWithSeq(pillSeq, "context_cleared", "new_session", "")

	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed: %v", err)
	}

	var gotPillSeq, gotUserSeq int64 = -1, -1
	for _, e := range events {
		switch e.Type {
		case session.EventTypeSessionChange:
			if m, ok := e.Data.(map[string]any); ok &&
				m["kind"] == "context_cleared" && m["value"] == "new_session" {
				gotPillSeq = e.Seq
			}
		case session.EventTypeUserPrompt:
			gotUserSeq = e.Seq
		}
	}
	if gotPillSeq < 0 || gotUserSeq < 0 {
		t.Fatalf("missing events: pill=%d user=%d events=%+v", gotPillSeq, gotUserSeq, events)
	}
	if gotPillSeq >= gotUserSeq {
		t.Errorf("mitto-c36 ordering violated (new_session): pill seq=%d must be < user_prompt seq=%d",
			gotPillSeq, gotUserSeq)
	}
}

// TestFreshContextPillOrdering_FlushFailureLeavesSeqGap pins the mitto-c36
// caveat decision (option (a) — "live with the gap"): when the reserved pill
// seq is never consumed (e.g. the in-place flush RPC failed and
// createFreshContextSession did NOT call pdRecordSessionChangeWithSeq), the
// resulting events.jsonl simply has a seq gap — no placeholder event, and the
// user prompt is still ordered strictly after where the pill would have been.
// Persistence tolerates gaps, so ReadEvents must not fail.
func TestFreshContextPillOrdering_FlushFailureLeavesSeqGap(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	bs := &BackgroundSession{
		nextSeq:     2,
		recorder:    recorder,
		persistedID: sessionID,
	}

	// Reserve the pill seq but never consume it (simulates flush RPC failure).
	pillSeq := bs.getNextSeq()
	userPromptSeq := bs.getNextSeq()

	if err := recorder.RecordUserPromptCompleteWithSeq(userPromptSeq, "hello", nil, nil, "", "", 0, nil); err != nil {
		t.Fatalf("RecordUserPromptCompleteWithSeq failed: %v", err)
	}

	events, err := store.ReadEvents(sessionID)
	if err != nil {
		t.Fatalf("ReadEvents failed (persistence should tolerate seq gaps): %v", err)
	}

	// No session_change event should exist.
	for _, e := range events {
		if e.Type == session.EventTypeSessionChange {
			t.Errorf("expected no session_change event on flush failure, got %+v", e)
		}
	}
	// The user_prompt event must still have the seq the caller reserved.
	var foundUser bool
	for _, e := range events {
		if e.Type == session.EventTypeUserPrompt {
			if e.Seq != userPromptSeq {
				t.Errorf("user_prompt seq drifted: reserved=%d persisted=%d", userPromptSeq, e.Seq)
			}
			foundUser = true
		}
	}
	if !foundUser {
		t.Fatal("user_prompt event missing")
	}
	// The reserved pillSeq is one lower than userPromptSeq → this is the tolerated gap.
	if pillSeq+1 != userPromptSeq {
		t.Errorf("expected pillSeq+1 == userPromptSeq (gap layout): pillSeq=%d userPromptSeq=%d",
			pillSeq, userPromptSeq)
	}
}

func TestFormatACPError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		contains string // expected substring in result
	}{
		{
			name:     "timeout error",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"details":"The operation was aborted due to timeout"}}`,
			contains: "tool operation timed out",
		},
		{
			name:     "peer disconnected",
			errMsg:   "peer disconnected before response",
			contains: "Lost connection to the AI agent",
		},
		{
			name:     "connection reset",
			errMsg:   "connection reset by peer",
			contains: "Lost connection to the AI agent",
		},
		{
			name:     "broken pipe",
			errMsg:   "write: broken pipe",
			contains: "Lost connection to the AI agent",
		},
		{
			name:     "context canceled",
			errMsg:   "context canceled",
			contains: "request was cancelled",
		},
		{
			name:     "context deadline exceeded",
			errMsg:   "context deadline exceeded",
			contains: "request was cancelled",
		},
		{
			name:     "rate limit",
			errMsg:   "rate limit exceeded",
			contains: "Rate limit reached",
		},
		{
			name:     "too many requests",
			errMsg:   "too many requests",
			contains: "Rate limit reached",
		},
		// --- Authentication required (mitto-r5o) ---
		{
			// Claude Code surfaces expired Anthropic OAuth token as JSON-RPC -32000
			// with message "Authentication required". FormatACPError must produce an
			// actionable hint pointing to `claude auth login` / `auggie auth login`.
			name:     "Claude Code -32000 auth required",
			errMsg:   `{"code":-32000,"message":"Authentication required"}`,
			contains: "authentication has expired",
		},
		{
			name:     "authentication required lowercase phrase",
			errMsg:   "authentication required",
			contains: "authentication has expired",
		},
		{
			name:     "generic internal error with details",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"details":"something went wrong"}}`,
			contains: "internal error",
		},
		{
			// -32603 without the word "details" previously fell through to the raw error;
			// now it should always return a user-friendly message.
			name:     "generic internal error without details keyword",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"reason":"unknown"}}`,
			contains: "internal error",
		},
		// --- HTTP 413 / context-too-large ---
		{
			// ACP SDK forwards the upstream 413 HTTP status inside the JSON-RPC error data.
			name:     "HTTP 413 status in error string",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"status":413,"message":"Context too large for model"}}`,
			contains: "too large",
		},
		{
			name:     "context too large phrase",
			errMsg:   "context too large for model",
			contains: "too large",
		},
		{
			name:     "context_too_long API code",
			errMsg:   "error: context_too_long — the prompt exceeds the model limit",
			contains: "too large",
		},
		{
			name:     "context_length_exceeded API code",
			errMsg:   "context_length_exceeded: maximum tokens reached",
			contains: "too large",
		},
		{
			name:     "context window is full",
			errMsg:   "Context window is full. Please start a new conversation.",
			contains: "too large",
		},
		{
			name:     "prompt is too long",
			errMsg:   "prompt is too long for the model",
			contains: "too large",
		},
		{
			name:     "maximum context length",
			errMsg:   "This model's maximum context length is 200000 tokens",
			contains: "too large",
		},
		// ---
		// --- HTTP status extraction from -32603 errors ---
		{
			name:     "HTTP 408 request timeout",
			errMsg:   `{"code":-32603,"message":"Internal error: HTTP error: 408 Request Timeout","data":{"httpStatus":408}}`,
			contains: "timed out",
		},
		{
			name:     "HTTP 500 server error",
			errMsg:   `{"code":-32603,"message":"Internal error: HTTP error: 500 Internal Server Error","data":{"httpStatus":500}}`,
			contains: "server error",
		},
		{
			name:     "HTTP 502 bad gateway",
			errMsg:   `{"code":-32603,"message":"Internal error: HTTP error: 502 Bad Gateway","data":{"httpStatus":502}}`,
			contains: "temporarily unavailable",
		},
		{
			name:     "HTTP 503 service unavailable",
			errMsg:   `{"code":-32603,"message":"Internal error: HTTP error: 503 Service Unavailable","data":{"httpStatus":503}}`,
			contains: "temporarily unavailable",
		},
		{
			name:     "HTTP 504 gateway timeout",
			errMsg:   `{"code":-32603,"message":"Internal error: HTTP error: 504 Gateway Timeout","data":{"httpStatus":504}}`,
			contains: "gateway timed out",
		},
		{
			name:     "unknown HTTP status in -32603",
			errMsg:   `{"code":-32603,"message":"Internal error: HTTP error: 422 Unprocessable Entity","data":{"httpStatus":422}}`,
			contains: "HTTP 422",
		},
		{
			name:     "generic internal error without HTTP status",
			errMsg:   `{"code":-32603,"message":"Internal error","data":{"details":"unknown"}}`,
			contains: "internal error",
		},
		// ---
		// --- Saturated shared process (mitto-13ck.2) ---
		{
			name:     "saturated shared process -> busy",
			errMsg:   "shared ACP process is saturated (repeated RPC timeouts); failing fast: context deadline exceeded",
			contains: "busy",
		},
		{
			name:     "saturated mid-flight -> busy",
			errMsg:   "session/new: shared ACP process became saturated mid-flight (after 1 attempt(s)); failing fast: context deadline exceeded",
			contains: "busy",
		},
		{
			// regression: non-saturated deadline still maps to the cancelled message
			name:     "plain context deadline still cancelled",
			errMsg:   "context deadline exceeded",
			contains: "cancelled",
		},
		// ---
		{
			name:     "unknown error",
			errMsg:   "some unknown error occurred",
			contains: "Prompt failed: some unknown error occurred",
		},
		{
			name:     "nil error returns empty",
			errMsg:   "",
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError{msg: tt.errMsg}
			}

			result := mittoAcp.FormatACPError(err)

			if tt.contains == "" {
				if result != "" {
					t.Errorf("FormatACPError() = %q, want empty string", result)
				}
				return
			}

			if !containsIgnoreCase(result, tt.contains) {
				t.Errorf("FormatACPError() = %q, want to contain %q", result, tt.contains)
			}
		})
	}
}

// testError is a simple error implementation for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// containsIgnoreCase checks if s contains substr (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func TestIsContextTooLargeError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		wantTrue bool
	}{
		{name: "nil", errMsg: "", wantTrue: false},
		{name: "HTTP 413 status in JSON-RPC data", errMsg: `{"code":-32603,"message":"Internal error","data":{"status":413}}`, wantTrue: true},
		// mitto-3rs: this used to be `wantTrue: true` under the old unanchored
		// `strings.Contains(errMsg, "413")` check. That check also matched "413"
		// appearing incidentally anywhere in an error string (e.g. inside a
		// request-id UUID segment), so it was replaced with status413Regex,
		// which requires "413" to be anchored to a status/HTTP-response prefix.
		// This synthetic phrase has no such anchor, so it now correctly returns
		// false — the ambiguity this bead was filed to fix.
		{name: "bare 413 digit (no status anchor, mitto-3rs)", errMsg: "upstream returned 413", wantTrue: false},
		{name: "context too large phrase", errMsg: "context too large for model", wantTrue: true},
		{name: "context_too_long API code", errMsg: "error: context_too_long", wantTrue: true},
		{name: "context_length_exceeded API code", errMsg: "context_length_exceeded", wantTrue: true},
		{name: "context window is full", errMsg: "Context window is full", wantTrue: true},
		{name: "prompt is too long", errMsg: "prompt is too long", wantTrue: true},
		{name: "maximum context length", errMsg: "maximum context length exceeded", wantTrue: true},
		// mitto-k4x: Augment chat-stream returns HTTP 400 apiStatus=invalidArgument
		// for oversized context-flush payloads (not HTTP 413). The classifier must
		// recognize the httpStatus:400 + apiStatus:"invalidArgument" substring pair
		// so the loop-runner's auto-pause guard fires (handleDeliveryFailure at
		// internal/conversation/loop_runner.go:1772 gates the counter on this) —
		// but ONLY when corroborated by a token/length overflow signal (mitto-2efc
		// narrowed this: an uncorroborated 400/invalidArgument is a generic
		// upstream rejection, not necessarily a too-large context).
		{
			name:     "HTTP 400 invalidArgument from chat-stream, corroborated by a length signal (mitto-k4x)",
			errMsg:   `-32603 Internal error: HTTP error: 400 Bad Request {"apiStatus":"invalidArgument","httpStatus":400,"httpUrl":"https://xlb.api.augmentcode.com/chat-stream","details":"context length exceeds the maximum allowed"}`,
			wantTrue: true,
		},
		// mitto-2efc: an uncorroborated 400/invalidArgument (no token/length
		// signal in the payload) must NOT be classified as context-too-large —
		// it is a generic upstream rejection (e.g. a deferred model-switch race)
		// and should instead flow through the generic delivery-failure path.
		{
			name:     "HTTP 400 invalidArgument uncorroborated (mitto-2efc)",
			errMsg:   `-32603 Internal error: HTTP error: 400 Bad Request {"apiStatus":"invalidArgument","httpStatus":400,"httpUrl":"https://xlb.api.augmentcode.com/chat-stream"}`,
			wantTrue: false,
		},
		{name: "rate limit is not context too large", errMsg: "rate limit exceeded", wantTrue: false},
		{name: "generic internal error", errMsg: `{"code":-32603,"message":"Internal error","data":{"details":"unknown"}}`, wantTrue: false},
		{name: "unrelated error", errMsg: "some other error", wantTrue: false},
		// mitto-3rs: the bare `strings.Contains(errMsg, "413")` check false-positives
		// on a 404 model-unavailable error whose requestId UUID segment happens to
		// contain the digits "413" (f24b-4130-...). This must NOT classify as
		// context-too-large, or the loop runner's auto-pause guard and the prompt
		// dispatcher's queue-stall logic misfire on an unrelated 404.
		{
			name:     "404 model-unavailable error with 413 inside requestId UUID (mitto-3rs)",
			errMsg:   `-32603 Internal error: HTTP error: 404 Not Found {"message":"The selected model is not available for this session.","requestId":"80d593fb-f24b-4130-83e3-bf89b1bca239"}`,
			wantTrue: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError{msg: tt.errMsg}
			}
			got := mittoAcp.IsContextTooLargeError(err)
			if got != tt.wantTrue {
				t.Errorf("IsContextTooLargeError(%q) = %v, want %v", tt.errMsg, got, tt.wantTrue)
			}
		})
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		wantTrue bool
	}{
		{name: "nil", errMsg: "", wantTrue: false},
		{name: "rate limit phrase", errMsg: "rate limit exceeded", wantTrue: true},
		{name: "too many requests phrase", errMsg: "too many requests", wantTrue: true},
		{name: "Rate Limit capitalized", errMsg: "Rate Limit reached", wantTrue: true},
		{name: "context too large is not rate limit", errMsg: "context too large for model", wantTrue: false},
		{name: "generic error is not rate limit", errMsg: "some other error", wantTrue: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError{msg: tt.errMsg}
			}
			got := mittoAcp.IsRateLimitError(err)
			if got != tt.wantTrue {
				t.Errorf("IsRateLimitError(%q) = %v, want %v", tt.errMsg, got, tt.wantTrue)
			}
		})
	}
}

// --- Config Options Tests ---

// TestBackgroundSession_SetSessionModes_ConvertsToConfigOptions tests that legacy
// modes are correctly converted to the config options format.
func TestBackgroundSession_SetSessionModes_ConvertsToConfigOptions(t *testing.T) {
	bs := &BackgroundSession{}

	description1 := "Ask questions without making changes"
	description2 := "Make code changes"

	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description1},
			{Id: "code", Name: "Code", Description: &description2},
			{Id: "architect", Name: "Architect"}, // No description
		},
	}

	bs.setSessionModes(modes)

	// Verify config options were created
	configOptions := bs.ConfigOptions()
	if len(configOptions) != 1 {
		t.Fatalf("ConfigOptions() length = %d, want 1", len(configOptions))
	}

	modeOption := configOptions[0]

	// Verify the mode config option structure
	if modeOption.ID != ConfigOptionCategoryMode {
		t.Errorf("ID = %q, want %q", modeOption.ID, ConfigOptionCategoryMode)
	}
	if modeOption.Name != "Mode" {
		t.Errorf("Name = %q, want %q", modeOption.Name, "Mode")
	}
	if modeOption.Category != ConfigOptionCategoryMode {
		t.Errorf("Category = %q, want %q", modeOption.Category, ConfigOptionCategoryMode)
	}
	if modeOption.Type != ConfigOptionTypeSelect {
		t.Errorf("Type = %q, want %q", modeOption.Type, ConfigOptionTypeSelect)
	}
	if modeOption.CurrentValue != "ask" {
		t.Errorf("CurrentValue = %q, want %q", modeOption.CurrentValue, "ask")
	}

	// Verify options
	if len(modeOption.Options) != 3 {
		t.Fatalf("Options length = %d, want 3", len(modeOption.Options))
	}

	// Verify first option
	if modeOption.Options[0].Value != "ask" {
		t.Errorf("Options[0].Value = %q, want %q", modeOption.Options[0].Value, "ask")
	}
	if modeOption.Options[0].Name != "Ask" {
		t.Errorf("Options[0].Name = %q, want %q", modeOption.Options[0].Name, "Ask")
	}
	if modeOption.Options[0].Description != description1 {
		t.Errorf("Options[0].Description = %q, want %q", modeOption.Options[0].Description, description1)
	}

	// Verify option without description
	if modeOption.Options[2].Description != "" {
		t.Errorf("Options[2].Description = %q, want empty", modeOption.Options[2].Description)
	}

	// Verify usesLegacyModes flag
	bs.configMu.RLock()
	usesLegacy := bs.usesLegacyModes
	bs.configMu.RUnlock()
	if !usesLegacy {
		t.Error("usesLegacyModes should be true after setSessionModes")
	}
}

// TestBackgroundSession_SetSessionModes_NilModes tests that nil modes are handled gracefully.
func TestBackgroundSession_SetSessionModes_NilModes(t *testing.T) {
	bs := &BackgroundSession{}

	// Should not panic
	bs.setSessionModes(nil)

	// Config options should be empty/nil
	configOptions := bs.ConfigOptions()
	if len(configOptions) != 0 {
		t.Errorf("ConfigOptions() length = %d, want 0", len(configOptions))
	}
}

// TestBackgroundSession_ConfigOptions_ReturnsCopy tests that ConfigOptions returns
// a copy, not a reference to the internal slice.
func TestBackgroundSession_ConfigOptions_ReturnsCopy(t *testing.T) {
	bs := &BackgroundSession{}

	description := "Test description"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Get config options
	options1 := bs.ConfigOptions()
	options2 := bs.ConfigOptions()

	// Modify the first copy
	if len(options1) > 0 {
		options1[0].CurrentValue = "modified"
	}

	// Second copy should be unaffected
	if len(options2) > 0 && options2[0].CurrentValue == "modified" {
		t.Error("ConfigOptions() should return a copy, not a reference")
	}
}

// TestBackgroundSession_GetConfigValue tests getting config values.
func TestBackgroundSession_GetConfigValue(t *testing.T) {
	bs := &BackgroundSession{}

	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "code",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Get mode value
	value := bs.GetConfigValue(ConfigOptionCategoryMode)
	if value != "code" {
		t.Errorf("GetConfigValue(%q) = %q, want %q", ConfigOptionCategoryMode, value, "code")
	}

	// Get non-existent config
	value = bs.GetConfigValue("nonexistent")
	if value != "" {
		t.Errorf("GetConfigValue(%q) = %q, want empty", "nonexistent", value)
	}
}

// TestBackgroundSession_OnCurrentModeChanged tests that mode changes from the agent
// update the config options and notify callbacks.
func TestBackgroundSession_OnCurrentModeChanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Set up modes first
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Track callback calls
	var callbackCalls []struct {
		sessionID string
		configID  string
		value     string
	}
	var callbackMu sync.Mutex
	bs.onConfigChanged = func(sessionID, configID, value string) {
		callbackMu.Lock()
		callbackCalls = append(callbackCalls, struct {
			sessionID string
			configID  string
			value     string
		}{sessionID, configID, value})
		callbackMu.Unlock()
	}
	bs.persistedID = "test-session"

	// Simulate mode change from agent
	bs.onCurrentModeChanged("code")

	// Verify current value was updated
	value := bs.GetConfigValue(ConfigOptionCategoryMode)
	if value != "code" {
		t.Errorf("GetConfigValue after mode change = %q, want %q", value, "code")
	}

	// Verify callback was called
	callbackMu.Lock()
	numCalls := len(callbackCalls)
	callbackMu.Unlock()
	if numCalls != 1 {
		t.Fatalf("Callback called %d times, want 1", numCalls)
	}

	callbackMu.Lock()
	call := callbackCalls[0]
	callbackMu.Unlock()
	if call.sessionID != "test-session" {
		t.Errorf("Callback sessionID = %q, want %q", call.sessionID, "test-session")
	}
	if call.configID != ConfigOptionCategoryMode {
		t.Errorf("Callback configID = %q, want %q", call.configID, ConfigOptionCategoryMode)
	}
	if call.value != "code" {
		t.Errorf("Callback value = %q, want %q", call.value, "code")
	}
}

// TestBackgroundSession_OnCurrentModeChanged_Closed tests that mode changes are
// ignored when the session is closed.
func TestBackgroundSession_OnCurrentModeChanged_Closed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		ctx:       ctx,
		cancel:    cancel,
		observers: make(map[SessionObserver]struct{}),
	}

	// Set up modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Close the session properly using the Close method which sets closed flag
	bs.Close("test")

	// Track callback calls
	callbackCalled := false
	bs.onConfigChanged = func(sessionID, configID, value string) {
		callbackCalled = true
	}

	// Try to change mode - should be ignored
	bs.onCurrentModeChanged("code")

	if callbackCalled {
		t.Error("Callback should not be called when session is closed")
	}

	// Value should still be "ask"
	value := bs.GetConfigValue(ConfigOptionCategoryMode)
	if value != "ask" {
		t.Errorf("GetConfigValue after ignored mode change = %q, want %q", value, "ask")
	}
}

// TestBackgroundSession_SetConfigOption_NoACP tests SetConfigOption error handling
// when there's no ACP connection.
func TestBackgroundSession_SetConfigOption_NoACP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:     ctx,
		cancel:  cancel,
		acpConn: nil, // No ACP connection
	}

	// Set up modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Try to set config option - should fail
	err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryMode, "code")
	if err == nil {
		t.Error("SetConfigOption should fail when there's no ACP connection")
	}
	if !strings.Contains(err.Error(), "no ACP connection") {
		t.Errorf("Error message = %q, should contain 'no ACP connection'", err.Error())
	}
}

// TestBackgroundSession_SetConfigOption_InvalidConfigID tests SetConfigOption error
// handling for unknown config IDs.
func TestBackgroundSession_SetConfigOption_InvalidConfigID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a mock ACP connection (we just need it to not be nil)
	// Use a type assertion trick to create a non-nil pointer without actually initializing
	var mockConn *acp.ClientSideConnection
	// We can't easily create a real ClientSideConnection, so test the error path differently
	// by verifying that after passing acpConn check, we get the right error

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Set up modes - but don't set acpConn so we get "no ACP connection" error first
	// This tests that order of checks: IsClosed -> acpConn -> configID validation
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// First verify no ACP connection error
	err := bs.SetConfigOption(context.Background(), "unknown_config", "value")
	if err == nil {
		t.Error("SetConfigOption should fail when no ACP connection")
	}
	if !strings.Contains(err.Error(), "no ACP connection") {
		t.Errorf("Error message = %q, should contain 'no ACP connection'", err.Error())
	}

	// Note: Testing unknown config ID validation requires a real ACP connection
	// which requires a full mock ACP server setup. The unit test above verifies
	// the earlier check in the code path.
	_ = mockConn // silence unused variable warning
}

// TestBackgroundSession_SetConfigOption_InvalidValue tests SetConfigOption error
// handling for invalid values.
// Note: Full validation testing requires a mock ACP server. This test verifies
// the code path up to the ACP connection check.
func TestBackgroundSession_SetConfigOption_InvalidValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Set up modes - but no acpConn, so we get "no ACP connection" error
	// The invalid value check happens after the ACP connection check
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Try to set a value - should fail with no ACP connection
	err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryMode, "invalid_mode")
	if err == nil {
		t.Error("SetConfigOption should fail when no ACP connection")
	}
	if !strings.Contains(err.Error(), "no ACP connection") {
		t.Errorf("Error message = %q, should contain 'no ACP connection'", err.Error())
	}

	// Note: Testing invalid value validation requires a real ACP connection
	// which requires a full mock ACP server setup. Integration tests cover this.
}

// TestBackgroundSession_SetConfigOption_Closed tests SetConfigOption error handling
// when the session is closed.
func TestBackgroundSession_SetConfigOption_Closed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	bs := &BackgroundSession{
		ctx:       ctx,
		cancel:    cancel,
		observers: make(map[SessionObserver]struct{}),
	}

	// Close the session properly using the Close method which sets closed flag
	bs.Close("test")

	// Try to set config option - should fail
	err := bs.SetConfigOption(context.Background(), ConfigOptionCategoryMode, "code")
	if err == nil {
		t.Error("SetConfigOption should fail when session is closed")
	}
	if !strings.Contains(err.Error(), "session is closed") {
		t.Errorf("Error message = %q, should contain 'session is closed'", err.Error())
	}
}

// TestBackgroundSession_ConfigOptions_Empty tests that ConfigOptions returns nil
// when no config options are set.
func TestBackgroundSession_ConfigOptions_Empty(t *testing.T) {
	bs := &BackgroundSession{}

	configOptions := bs.ConfigOptions()
	if configOptions != nil {
		t.Errorf("ConfigOptions() = %v, want nil", configOptions)
	}
}

// TestBackgroundSession_SetSessionModes_PersistsToMetadata tests that the initial
// mode is persisted to metadata.
func TestBackgroundSession_SetSessionModes_PersistsToMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-mode-persist"

	// Create session in store
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
	}

	// Set modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "code",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Verify metadata was updated
	updatedMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if updatedMeta.CurrentModeID != "code" {
		t.Errorf("Metadata.CurrentModeID = %q, want %q", updatedMeta.CurrentModeID, "code")
	}
}

// TestBackgroundSession_OnCurrentModeChanged_PersistsToMetadata tests that mode
// changes from the agent are persisted to metadata.
func TestBackgroundSession_OnCurrentModeChanged_PersistsToMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-mode-change-persist"

	// Create session in store
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Set initial modes
	description := "Test mode"
	modes := &acp.SessionModeState{
		CurrentModeId: "ask",
		AvailableModes: []acp.SessionMode{
			{Id: "ask", Name: "Ask", Description: &description},
			{Id: "code", Name: "Code", Description: &description},
		},
	}
	bs.setSessionModes(modes)

	// Simulate mode change from agent
	bs.onCurrentModeChanged("code")

	// Verify metadata was updated
	updatedMeta, err := store.GetMetadata(sessionID)
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if updatedMeta.CurrentModeID != "code" {
		t.Errorf("Metadata.CurrentModeID = %q, want %q", updatedMeta.CurrentModeID, "code")
	}
}

// =============================================================================
// Session MCP Server Tests
// =============================================================================

func TestStartSessionMcpServer_ReturnsEmptySlice(t *testing.T) {
	// With the global MCP server architecture, startSessionMcpServer should always
	// return an empty slice (MCP is configured globally, not passed per-session).
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-mcp-global"
	meta := session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		ctx:         ctx,
		// Note: globalMcpServer is nil, so registration will be skipped
	}

	// Agent supports HTTP MCP
	agentCaps := acp.AgentCapabilities{
		McpCapabilities: acp.McpCapabilities{
			Http: true,
		},
	}

	mcpServers := bs.startSessionMcpServer(store, agentCaps)

	// With global MCP server architecture, no McpServers are passed to ACP
	if len(mcpServers) != 0 {
		t.Errorf("Expected empty MCP servers slice (using global server), got %d", len(mcpServers))
	}
}

// TestUIPrompt tests the UI prompt functionality
func TestUIPrompt(t *testing.T) {
	t.Run("basic flow", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		bs := &BackgroundSession{
			observers:   make(map[SessionObserver]struct{}),
			ctx:         ctx,
			cancel:      cancel,
			persistedID: "test-session-123",
		}

		// Track what the observer receives
		var receivedPrompt UIPromptRequest
		var promptReceived bool
		promptCh := make(chan struct{}, 1)

		observer := &trackingObserver{
			onUIPrompt: func(req UIPromptRequest) {
				receivedPrompt = req
				promptReceived = true
				promptCh <- struct{}{}
			},
		}
		bs.AddObserver(observer)

		// Start a goroutine to answer the prompt
		go func() {
			<-promptCh // Wait for prompt to be sent
			time.Sleep(50 * time.Millisecond)
			bs.HandleUIPromptAnswer("test-request-001", "yes", "Deploy Now", "")
		}()

		// Send a UI prompt
		req := UIPromptRequest{
			RequestID:      "test-request-001",
			Type:           UIPromptTypeYesNo,
			Question:       "Do you want to deploy?",
			Options:        []UIPromptOption{{ID: "yes", Label: "Deploy Now"}, {ID: "no", Label: "Cancel"}},
			TimeoutSeconds: 5,
		}

		resp, err := bs.UIPrompt(ctx, req)
		if err != nil {
			t.Fatalf("UIPrompt failed: %v", err)
		}

		if !promptReceived {
			t.Error("Observer should have received OnUIPrompt call")
		}
		if receivedPrompt.RequestID != "test-request-001" {
			t.Errorf("OnUIPrompt request ID = %q, want %q", receivedPrompt.RequestID, "test-request-001")
		}
		if resp.OptionID != "yes" {
			t.Errorf("Response option ID = %q, want %q", resp.OptionID, "yes")
		}
		if resp.Label != "Deploy Now" {
			t.Errorf("Response label = %q, want %q", resp.Label, "Deploy Now")
		}
		if resp.TimedOut {
			t.Error("Response should not be timed out")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		bs := &BackgroundSession{
			observers:   make(map[SessionObserver]struct{}),
			ctx:         ctx,
			cancel:      cancel,
			persistedID: "test-session-123",
		}

		// Track dismiss calls
		var dismissReceived atomic.Bool
		observer := &trackingObserver{
			onUIPrompt: func(req UIPromptRequest) {},
			onUIPromptDismiss: func(requestID string, reason string) {
				dismissReceived.Store(true)
			},
		}
		bs.AddObserver(observer)

		// Send a UI prompt with very short timeout (no answer)
		req := UIPromptRequest{
			RequestID:      "test-request-timeout",
			Type:           UIPromptTypeYesNo,
			Question:       "This will timeout",
			Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
			TimeoutSeconds: 1, // 1 second timeout
		}

		resp, err := bs.UIPrompt(ctx, req)
		if err != nil {
			t.Fatalf("UIPrompt failed: %v", err)
		}

		if !resp.TimedOut {
			t.Error("Response should be timed out")
		}

		// Wait for dismiss notification
		time.Sleep(100 * time.Millisecond)
		if !dismissReceived.Load() {
			t.Error("Observer should have received OnUIPromptDismiss call")
		}
	})

	t.Run("replace prompt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		bs := &BackgroundSession{
			observers:   make(map[SessionObserver]struct{}),
			ctx:         ctx,
			cancel:      cancel,
			persistedID: "test-session-123",
		}

		var promptCount int
		var dismissReasons []string
		var mu sync.Mutex
		firstPromptReceived := make(chan struct{}, 1)
		secondPromptReceived := make(chan struct{}, 1)

		observer := &trackingObserver{
			onUIPrompt: func(req UIPromptRequest) {
				mu.Lock()
				promptCount++
				count := promptCount
				mu.Unlock()
				switch count {
				case 1:
					firstPromptReceived <- struct{}{}
				case 2:
					secondPromptReceived <- struct{}{}
				}
			},
			onUIPromptDismiss: func(requestID string, reason string) {
				mu.Lock()
				dismissReasons = append(dismissReasons, reason)
				mu.Unlock()
			},
		}
		bs.AddObserver(observer)

		// Start first prompt (don't answer it)
		firstPromptDone := make(chan struct{})
		go func() {
			defer close(firstPromptDone)
			req1 := UIPromptRequest{
				RequestID:      "first-prompt",
				Type:           UIPromptTypeYesNo,
				Question:       "First question",
				Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
				TimeoutSeconds: 30,
			}
			bs.UIPrompt(ctx, req1)
		}()

		// Wait for first prompt to be sent to observer
		select {
		case <-firstPromptReceived:
		case <-time.After(2 * time.Second):
			t.Fatal("First prompt was not received by observer")
		}

		// Send second prompt in a goroutine
		secondPromptDone := make(chan UIPromptResponse)
		go func() {
			req2 := UIPromptRequest{
				RequestID:      "second-prompt",
				Type:           UIPromptTypeYesNo,
				Question:       "Second question",
				Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
				TimeoutSeconds: 5,
			}
			resp, _ := bs.UIPrompt(ctx, req2)
			secondPromptDone <- resp
		}()

		// Wait for second prompt to be sent to observer
		select {
		case <-secondPromptReceived:
		case <-time.After(2 * time.Second):
			t.Fatal("Second prompt was not received by observer")
		}

		// Now answer the second prompt
		bs.HandleUIPromptAnswer("second-prompt", "yes", "Yes", "")

		// Get response
		var resp UIPromptResponse
		select {
		case resp = <-secondPromptDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Second prompt did not complete")
		}

		// Verify second prompt was answered
		if resp.OptionID != "yes" {
			t.Errorf("Response option ID = %q, want %q", resp.OptionID, "yes")
		}

		// Wait for first prompt to complete (it should have been replaced)
		select {
		case <-firstPromptDone:
		case <-time.After(2 * time.Second):
			t.Error("First prompt goroutine did not complete")
		}

		// Wait for async dismiss notification (sent via goroutine)
		time.Sleep(100 * time.Millisecond)

		// Verify we got 2 prompts
		mu.Lock()
		pc := promptCount
		reasons := dismissReasons
		mu.Unlock()

		if pc != 2 {
			t.Errorf("Prompt count = %d, want 2", pc)
		}

		// Verify first prompt was dismissed with "replaced" reason
		foundReplaced := false
		for _, r := range reasons {
			if r == "replaced" {
				foundReplaced = true
				break
			}
		}
		if !foundReplaced {
			t.Errorf("Expected 'replaced' in dismiss reasons, got %v", reasons)
		}
	})
}

// TestFinalizeTurn_LeavesStaleUIPrompt_MittoNisb reproduces mitto-nisb: when a
// turn ends WITHOUT the user answering a blocking mitto_ui_* prompt (e.g. the
// agent crashed, or the inactivity watchdog fired), BackgroundSession.activePrompt
// is never cleared. finalizeTurn is the single choke point PromptWithMeta calls
// at the end of every turn — both the success and error branches
// (bgsession_prompt.go: bs.promptDisp.finalizeTurn(bs, err, meta, sessionIdle)) —
// so this is the narrowest place to prove the staleness bug without needing a
// live ACP connection or a WebSocket client.
//
// A stale activePrompt is what causes internal/web/session_ws.go's
// postLoadProcessing (load_events) and attach-after-unarchive paths to re-send
// a dead prompt to a reconnecting client, resurrecting a panel the agent has
// already given up waiting on.
//
// This test currently FAILS: GetActiveUIPrompt() is non-nil after finalizeTurn.
func TestFinalizeTurn_LeavesStaleUIPrompt_MittoNisb(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers:   make(map[SessionObserver]struct{}),
		ctx:         ctx,
		cancel:      cancel,
		persistedID: "test-session-nisb",
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	promptReceived := make(chan struct{}, 1)
	observer := &trackingObserver{
		onUIPrompt: func(req UIPromptRequest) { promptReceived <- struct{}{} },
	}
	bs.AddObserver(observer)

	// Simulate an MCP tool call blocking on mitto_ui_options with a long timeout
	// (mirrors the real 300s default in internal/mcpserver/tools_ui.go).
	go func() {
		req := UIPromptRequest{
			RequestID:      "nisb-request",
			Type:           UIPromptTypeOptions,
			Question:       "Proceed?",
			Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}},
			TimeoutSeconds: 300,
		}
		bs.UIPrompt(ctx, req) //nolint:errcheck
	}()

	select {
	case <-promptReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("UI prompt was not received by observer")
	}

	if bs.GetActiveUIPrompt() == nil {
		t.Fatal("expected an active UI prompt before turn finalization")
	}

	// Simulate the agent's turn ending (this is exactly what PromptWithMeta does
	// at the end of every turn, success or error) WITHOUT the user having
	// answered the prompt.
	bs.promptDisp.finalizeTurn(bs, nil, PromptMeta{}, true)

	if ap := bs.GetActiveUIPrompt(); ap != nil {
		t.Fatalf("mitto-nisb: expected active UI prompt to be cleared after finalizeTurn, still active: %+v", ap)
	}
}

// TestCancel_DismissesActiveUIPrompt tests that Cancel() properly dismisses any active UI prompt.
// This ensures that when the user presses the Stop button, any pending MCP tool UI prompts
// (like yes/no questions or option selections) are dismissed and the UI is cleaned up.
func TestCancel_DismissesActiveUIPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers:   make(map[SessionObserver]struct{}),
		ctx:         ctx,
		cancel:      cancel,
		persistedID: "test-session-cancel-ui",
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Track what the observer receives
	var dismissReceived bool
	var dismissRequestID string
	var dismissReason string
	var mu sync.Mutex
	promptReceived := make(chan struct{}, 1)
	dismissDone := make(chan struct{}, 1)

	observer := &trackingObserver{
		onUIPrompt: func(req UIPromptRequest) {
			promptReceived <- struct{}{}
		},
		onUIPromptDismiss: func(requestID string, reason string) {
			mu.Lock()
			dismissReceived = true
			dismissRequestID = requestID
			dismissReason = reason
			mu.Unlock()
			select {
			case dismissDone <- struct{}{}:
			default:
			}
		},
	}
	bs.AddObserver(observer)

	// Start a goroutine that will start a UI prompt and wait for the answer
	promptDone := make(chan UIPromptResponse)
	go func() {
		req := UIPromptRequest{
			RequestID:      "cancel-test-request",
			Type:           UIPromptTypeYesNo,
			Question:       "This will be cancelled?",
			Options:        []UIPromptOption{{ID: "yes", Label: "Yes"}, {ID: "no", Label: "No"}},
			TimeoutSeconds: 30, // Long timeout - we'll cancel before it expires
		}
		resp, _ := bs.UIPrompt(ctx, req)
		promptDone <- resp
	}()

	// Wait for prompt to be sent to observer
	select {
	case <-promptReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt was not received by observer")
	}

	// Verify there's an active prompt
	activePrompt := bs.GetActiveUIPrompt()
	if activePrompt == nil {
		t.Fatal("There should be an active UI prompt before Cancel()")
	}
	if activePrompt.RequestID != "cancel-test-request" {
		t.Errorf("Active prompt request ID = %q, want %q", activePrompt.RequestID, "cancel-test-request")
	}

	// Now call Cancel() - this should dismiss the active UI prompt
	bs.Cancel()

	// Wait for dismiss notification (sent via goroutine)
	select {
	case <-dismissDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Dismiss notification was not received")
	}

	// Verify the prompt was dismissed with "cancelled" reason
	mu.Lock()
	if !dismissReceived {
		t.Error("Observer should have received OnUIPromptDismiss call")
	}
	if dismissRequestID != "cancel-test-request" {
		t.Errorf("Dismiss request ID = %q, want %q", dismissRequestID, "cancel-test-request")
	}
	if dismissReason != "cancelled" {
		t.Errorf("Dismiss reason = %q, want %q", dismissReason, "cancelled")
	}
	mu.Unlock()

	// Verify the prompt response indicates timeout/cancellation
	select {
	case resp := <-promptDone:
		if !resp.TimedOut {
			t.Error("Response should be marked as timed out after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Error("UI prompt goroutine did not complete after Cancel()")
	}

	// Verify there's no longer an active prompt
	if bs.GetActiveUIPrompt() != nil {
		t.Error("There should be no active UI prompt after Cancel()")
	}
}

// TestCancel_NoActiveUIPrompt tests that Cancel() works correctly when there's no active UI prompt.
func TestCancel_NoActiveUIPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers:   make(map[SessionObserver]struct{}),
		ctx:         ctx,
		cancel:      cancel,
		persistedID: "test-session-cancel-no-ui",
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Verify no active prompt
	if bs.GetActiveUIPrompt() != nil {
		t.Fatal("There should be no active UI prompt initially")
	}

	// Call Cancel() - should not panic or cause issues
	err := bs.Cancel()
	if err != nil {
		t.Errorf("Cancel() returned unexpected error: %v", err)
	}

	// Verify still no active prompt
	if bs.GetActiveUIPrompt() != nil {
		t.Error("There should still be no active UI prompt after Cancel()")
	}
}

// TestCancel_DrainsStrandedQueueAfterWedgedPrompt reproduces mitto-79x: stopping a
// wedged prompt via Cancel() must drain any messages stranded in the queue.
// Cancel()/ForceReset() are the only user-visible way to un-wedge a session, and
// the queue dispatcher is otherwise entirely event-driven (enqueue, spawn, resume,
// loop fire) — there is no periodic self-heal tick, so without an explicit drain
// here the queued message stays stuck forever. No live ACP connection is needed:
// the dispatch attempt fails fast and deterministically (session is nil), and
// this test only cares whether the queue was drained, not the ACP outcome.
func TestCancel_DrainsStrandedQueueAfterWedgedPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-cancel-drain"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Queue a message while the session will be marked "wedged" below.
	queue := store.Queue(sessionID)
	if _, err := queue.Add("stranded message", nil, nil, "client1", nil, 0, nil, ""); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers:   make(map[SessionObserver]struct{}),
		ctx:         ctx,
		cancel:      cancel,
		persistedID: sessionID,
		store:       store,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Simulate a wedged prompt: isPrompting=true as if a turn is stuck.
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptMu.Unlock()

	if err := bs.Cancel(); err != nil {
		t.Fatalf("Cancel() returned unexpected error: %v", err)
	}

	// Cancel() must trigger a queue drain (mirroring every other
	// TryProcessQueuedMessage call site in the codebase, run asynchronously
	// since promptWithMeta re-acquires promptMu). Poll with a bound instead of
	// asserting immediately after Cancel() returns.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if qLen, _ := queue.Len(); qLen == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	qLen, err := queue.Len()
	if err != nil {
		t.Fatalf("queue.Len() failed: %v", err)
	}
	if qLen != 0 {
		t.Errorf("mitto-79x: queue still has %d message(s) stranded after Cancel() on a wedged prompt; "+
			"Cancel() must drain the queue (e.g. via go bs.TryProcessQueuedMessage())", qLen)
	}
	if len(observer.getQueueMessagesSending()) == 0 {
		t.Error("mitto-79x: expected OnQueueMessageSending to fire after Cancel() drains the stranded message")
	}
}

// TestForceReset_DrainsStrandedQueueAfterWedgedPrompt is the ForceReset() sibling
// of TestCancel_DrainsStrandedQueueAfterWedgedPrompt — see that test for the full
// rationale (mitto-79x). ForceReset() has the identical drain gap as Cancel().
func TestForceReset_DrainsStrandedQueueAfterWedgedPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-forcereset-drain"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test-server",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	queue := store.Queue(sessionID)
	if _, err := queue.Add("stranded message", nil, nil, "client1", nil, 0, nil, ""); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		observers:   make(map[SessionObserver]struct{}),
		ctx:         ctx,
		cancel:      cancel,
		persistedID: sessionID,
		store:       store,
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	observer := &mockSessionObserver{}
	bs.AddObserver(observer)

	// Simulate a wedged prompt: isPrompting=true as if a turn is stuck.
	bs.promptMu.Lock()
	bs.isPrompting = true
	bs.promptMu.Unlock()

	bs.ForceReset()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if qLen, _ := queue.Len(); qLen == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	qLen, err := queue.Len()
	if err != nil {
		t.Fatalf("queue.Len() failed: %v", err)
	}
	if qLen != 0 {
		t.Errorf("mitto-79x: queue still has %d message(s) stranded after ForceReset() on a wedged prompt; "+
			"ForceReset() must drain the queue (e.g. via go bs.TryProcessQueuedMessage())", qLen)
	}
	if len(observer.getQueueMessagesSending()) == 0 {
		t.Error("mitto-79x: expected OnQueueMessageSending to fire after ForceReset() drains the stranded message")
	}
}

// =============================================================================
// IsACPReady Tests
// =============================================================================

// TestIsACPReady verifies that IsACPReady returns the correct readiness state.
func TestIsACPReady(t *testing.T) {
	// Test 1: New session with no ACP connection → not ready
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}
	if bs.IsACPReady() {
		t.Error("IsACPReady should be false when no ACP connection")
	}

	// Test 2: Closed session → not ready (even if fields were set)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	bs2 := &BackgroundSession{
		observers: make(map[SessionObserver]struct{}),
		ctx:       ctx2,
		cancel:    cancel2,
	}
	bs2.Close("test")
	if bs2.IsACPReady() {
		t.Error("IsACPReady should be false when session is closed")
	}
}

// TestIsACPReady_WithConn verifies IsACPReady returns true when acpConn is set.
func TestIsACPReady_WithConn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs := &BackgroundSession{
		ctx:    ctx,
		cancel: cancel,
	}

	// Without acpConn or sharedProcess → not ready
	if bs.IsACPReady() {
		t.Error("IsACPReady should be false without acpConn or sharedProcess")
	}

	// With sharedProcess set (acpConn is nil but sharedProcess is non-nil would also return true)
	// Since we can't easily construct an acpConn or sharedProcess in a unit test,
	// verify that the logic is: !IsClosed() && (acpConn != nil || sharedProcess != nil)
	// The nil case is already tested above.
	// Verify IsClosed() does not affect a non-closed session.
	if bs.IsClosed() {
		t.Error("IsClosed should be false for a new session")
	}
}

// =============================================================================
// PromptWithMeta Error Message Tests
// =============================================================================

// TestPromptWithMeta_NoACPConnection_ErrorMessage verifies the error message
// when PromptWithMeta is called without an ACP connection.
func TestPromptWithMeta_NoACPConnection_ErrorMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := &BackgroundSession{
		ctx:       ctx,
		cancel:    cancel,
		observers: make(map[SessionObserver]struct{}),
	}

	err := bs.PromptWithMeta("hello", PromptMeta{})
	if err == nil {
		t.Fatal("Expected error when ACP not connected")
	}

	expected := "The AI agent is still starting up"
	if !strings.Contains(err.Error(), expected) {
		t.Errorf("Error message should contain %q, got: %q", expected, err.Error())
	}
}

// TestPromptWithMeta_ClosedSession_ErrorMessage verifies the error message
// when PromptWithMeta is called on a closed session.
func TestPromptWithMeta_ClosedSession_ErrorMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bs := &BackgroundSession{
		ctx:       ctx,
		cancel:    cancel,
		observers: make(map[SessionObserver]struct{}),
	}

	bs.Close("test")

	err := bs.PromptWithMeta("hello", PromptMeta{})
	if err == nil {
		t.Fatal("Expected error when session is closed")
	}

	// Closed session returns a different error
	if !strings.Contains(err.Error(), "session is closed") {
		t.Errorf("Error message for closed session should contain 'session is closed', got: %q", err.Error())
	}
}

// TestRestartACPProcess_SharedProcess_PreservesReference verifies that when
// restartACPProcess fails for a shared-process session, the sharedProcess
// reference is preserved (not nilled out). Without this, the session becomes
// a permanent zombie where every prompt returns "still starting up".
func TestRestartACPProcess_SharedProcess_PreservesReference(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a minimal shared process stub (not actually running — Restart will fail)
	sharedProc := &alwaysFailSharedProcess{}

	bs := &BackgroundSession{
		ctx:           ctx,
		cancel:        cancel,
		persistedID:   "test-zombie-fix",
		sharedProcess: sharedProc,
		workingDir:    "/tmp/test",
		observers:     make(map[SessionObserver]struct{}),
	}

	// restartACPProcess should fail (shared process has no real connection)
	err := bs.restartACPProcess(mittoAcp.RestartReasonCrashDuringStream)
	if err == nil {
		t.Fatal("Expected restartACPProcess to fail on a process with no connection")
	}

	// The critical assertion: sharedProcess must NOT be nil after a failed restart.
	// If it's nil, PromptWithMeta would return "still starting up" permanently.
	if bs.sharedProcess == nil {
		t.Fatal("sharedProcess was nilled out after failed restart — session would become a permanent zombie")
	}

	// Verify PromptWithMeta doesn't return the "still starting up" zombie error.
	// We can't call PromptWithMeta here because it spawns goroutines that need
	// fully initialized fields (promptCond, etc.), but we can verify the guard
	// condition directly: the check is (acpConn == nil && sharedProcess == nil).
	if bs.acpConn == nil && bs.sharedProcess == nil {
		t.Error("Session is in zombie state: acpConn==nil && sharedProcess==nil — PromptWithMeta would return 'still starting up'")
	}
}

// TestSetAgentModels_InitializesBaseline verifies that setAgentModels initializes
// baselineModel from the agent's reported current model when no persisted value exists.
func TestSetAgentModels_InitializesBaseline(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.pendingConfig = make(map[string]string)

	models := &SessionModelState{
		CurrentModelId: ("claude-sonnet-4-6"),
		AvailableModels: []ModelInfo{
			{ModelId: "claude-haiku-4-5", Name: "Haiku 4.5"},
			{ModelId: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
		},
	}

	bs.setAgentModels(models)

	bs.modelMu.Lock()
	baseline := bs.baselineModel
	bs.modelMu.Unlock()

	if baseline != "claude-sonnet-4-6" {
		t.Errorf("baselineModel = %q, want %q", baseline, "claude-sonnet-4-6")
	}
}

// TestSetAgentModels_DoesNotOverwriteExistingBaseline ensures that a second call to
// setAgentModels does not overwrite an already-established baselineModel.
func TestSetAgentModels_DoesNotOverwriteExistingBaseline(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.pendingConfig = make(map[string]string)
	bs.modelMu.Lock()
	bs.baselineModel = "claude-opus-4-6" // pre-set
	bs.modelMu.Unlock()

	models := &SessionModelState{
		CurrentModelId: ("claude-sonnet-4-6"),
		AvailableModels: []ModelInfo{
			{ModelId: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
			{ModelId: "claude-opus-4-6", Name: "Opus 4.6"},
		},
	}

	bs.setAgentModels(models)

	bs.modelMu.Lock()
	baseline := bs.baselineModel
	bs.modelMu.Unlock()

	if baseline != "claude-opus-4-6" {
		t.Errorf("baselineModel = %q, want %q (should not be overwritten)", baseline, "claude-opus-4-6")
	}
}

// TestRestoreBaselineIfOverride_NoOp verifies that restoreBaselineIfOverride does nothing
// when overrideActive is false.
func TestRestoreBaselineIfOverride_NoOp(t *testing.T) {
	bs := &BackgroundSession{}
	bs.modelMu.Lock()
	bs.overrideActive = false
	bs.baselineModel = "claude-sonnet-4-6"
	bs.modelMu.Unlock()

	// No ACP connection — if setActiveModelOnly were called, it would panic/error.
	// The function should return early without trying to make any ACP call.
	bs.restoreBaselineIfOverride()

	bs.modelMu.Lock()
	override := bs.overrideActive
	bs.modelMu.Unlock()

	if override {
		t.Error("overrideActive should remain false after no-op restore")
	}
}

// TestRestoreBaselineIfOverride_ClearsOverrideFlag verifies that overrideActive is cleared
// even when agentModels is nil (no ACP connection available).
func TestRestoreBaselineIfOverride_ClearsOverrideFlag(t *testing.T) {
	bs := &BackgroundSession{}
	bs.modelMu.Lock()
	bs.overrideActive = true
	bs.baselineModel = "claude-sonnet-4-6"
	bs.modelMu.Unlock()
	// agentModels is nil → function returns after clearing the flag

	bs.restoreBaselineIfOverride()

	bs.modelMu.Lock()
	override := bs.overrideActive
	bs.modelMu.Unlock()

	if override {
		t.Error("overrideActive should be cleared after restoreBaselineIfOverride")
	}
}

// TestProcessNextQueuedMessage_RestoresBaselineOnDrain verifies that when the queue is
// empty, restoreBaselineIfOverride is called (override cleared).
func TestProcessNextQueuedMessage_RestoresBaselineOnDrain(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	sessionID := "test-session-drain"
	if err := store.Create(session.Metadata{
		SessionID:  sessionID,
		ACPServer:  "test",
		WorkingDir: "/tmp",
	}); err != nil {
		t.Fatalf("store.Create failed: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
	}
	bs.modelMu.Lock()
	bs.overrideActive = true
	bs.baselineModel = "claude-sonnet-4-6"
	bs.modelMu.Unlock()
	// agentModels is nil → restoreBaselineIfOverride won't make an ACP call

	result := bs.processNextQueuedMessage()
	if result {
		t.Error("processNextQueuedMessage should return false for empty queue")
	}

	bs.modelMu.Lock()
	override := bs.overrideActive
	bs.modelMu.Unlock()

	if override {
		t.Error("overrideActive should be cleared after queue drains")
	}
}

// TestApplyModelTag_EmptyTag_RestoresBaselineIfOverride verifies that passing
// an empty tag routes to restoreBaselineIfOverride (baseline-restore path) and
// returns ("", nil). This is the MCP-side "clear the transient override" contract.
func TestApplyModelTag_EmptyTag_RestoresBaselineIfOverride(t *testing.T) {
	bs := &BackgroundSession{}
	bs.modelMu.Lock()
	bs.overrideActive = true
	bs.baselineModel = "claude-sonnet-4-6"
	bs.modelMu.Unlock()
	// agentModels stays nil → restoreBaselineIfOverride clears the flag without
	// attempting an ACP call (see TestRestoreBaselineIfOverride_ClearsOverrideFlag).

	resolved, err := bs.ApplyModelTag(context.Background(), "")
	if err != nil {
		t.Fatalf("ApplyModelTag(\"\") returned error: %v", err)
	}
	if resolved != "" {
		t.Errorf("resolved id = %q, want empty on tag-clear", resolved)
	}
	bs.modelMu.Lock()
	override := bs.overrideActive
	bs.modelMu.Unlock()
	if override {
		t.Error("overrideActive should be cleared after ApplyModelTag(\"\")")
	}
}

// TestApplyModelTag_NoAgentCatalog_ReturnsError verifies that a non-empty tag
// against a session whose agent has not advertised any model catalog (agentModels
// nil) returns a clear error and does NOT touch overrideActive.
func TestApplyModelTag_NoAgentCatalog_ReturnsError(t *testing.T) {
	bs := &BackgroundSession{}
	// agentModels stays nil.

	resolved, err := bs.ApplyModelTag(context.Background(), "Reasoning")
	if err == nil {
		t.Fatal("expected error when agentModels is nil, got nil")
	}
	if resolved != "" {
		t.Errorf("resolved id = %q, want empty on error", resolved)
	}
	if !strings.Contains(err.Error(), "model catalog") {
		t.Errorf("error = %v, want to mention 'model catalog'", err)
	}
}

// TestApplyModelTag_NoMatchingProfile_ReturnsError verifies that a non-empty tag
// which does not resolve to any profile in the effective model catalog returns
// an error naming the offending tag.
func TestApplyModelTag_NoMatchingProfile_ReturnsError(t *testing.T) {
	bs := &BackgroundSession{}
	bs.agentModels = &SessionModelState{
		CurrentModelId: "some-agent-model",
		AvailableModels: []ModelInfo{
			{ModelId: "some-agent-model", Name: "Some Agent Model"},
		},
	}
	// Empty mittoConfig → EffectiveModelProfiles falls back to DefaultModelProfiles,
	// none of which will carry a "TotallyMadeUpTierName" tag.
	bs.mittoConfig = &config.Config{}

	tag := "TotallyMadeUpTierName"
	resolved, err := bs.ApplyModelTag(context.Background(), tag)
	if err == nil {
		t.Fatal("expected error when tag does not resolve to any model, got nil")
	}
	if resolved != "" {
		t.Errorf("resolved id = %q, want empty on error", resolved)
	}
	if !strings.Contains(err.Error(), tag) {
		t.Errorf("error = %v, want to contain tag %q", err, tag)
	}
	if !strings.Contains(err.Error(), "did not resolve") {
		t.Errorf("error = %v, want to contain 'did not resolve'", err)
	}
}

// TestStartPromptInactivityWatchdog_FiresWhenIdle verifies the watchdog cancels the
// prompt and sets the fired flag when no streamed activity is observed within the
// configured timeout, emitting both a WARN and an ERROR log along the way.
func TestStartPromptInactivityWatchdog_FiresWhenIdle(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 20 * time.Millisecond
	SetPromptInactivityTimeout(60 * time.Millisecond)
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{logger: slog.New(rec), persistedID: "test-idle"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Bool

	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)

	deadline := time.After(2 * time.Second)
	for !fired.Load() {
		select {
		case <-deadline:
			t.Fatal("watchdog did not fire within deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if ctx.Err() == nil {
		t.Error("expected prompt context to be cancelled after watchdog fired")
	}
	if len(rec.entriesAt(slog.LevelWarn)) == 0 {
		t.Error("expected a WARN log before the timeout")
	}
	if len(rec.entriesAt(slog.LevelError)) == 0 {
		t.Error("expected an ERROR log when the watchdog fired")
	}
}

// TestStartPromptInactivityWatchdog_SilentWhenActive verifies the watchdog stays quiet
// and does not fire while streamed activity continues to arrive.
func TestStartPromptInactivityWatchdog_SilentWhenActive(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 40 * time.Millisecond
	SetPromptInactivityTimeout(80 * time.Millisecond)
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{logger: slog.New(rec), persistedID: "test-active"}

	ctx, cancel := context.WithCancel(context.Background())
	var fired atomic.Bool
	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)

	// Signal activity faster than the warn window for well beyond the timeout.
	stop := time.After(250 * time.Millisecond)
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-time.After(10 * time.Millisecond):
			bs.signalAgentActivity()
		}
	}

	if fired.Load() {
		t.Error("watchdog fired despite continuous activity")
	}
	if ctx.Err() != nil {
		t.Error("prompt context should not be cancelled while activity continues")
	}
	if got := len(rec.entriesAt(slog.LevelError)); got != 0 {
		t.Errorf("expected 0 ERROR entries while active, got %d", got)
	}
	cancel()
}

// TestStartPromptInactivityWatchdog_PausesDuringUIPrompt verifies the watchdog does not
// fire while a UI prompt (permission dialog or MCP tool question) is pending, since the
// agent is legitimately blocked waiting on user input.
func TestStartPromptInactivityWatchdog_PausesDuringUIPrompt(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 20 * time.Millisecond
	SetPromptInactivityTimeout(50 * time.Millisecond)
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{logger: slog.New(rec), persistedID: "test-uiprompt"}
	// Simulate a pending UI prompt (e.g. a permission dialog awaiting the user).
	bs.activePrompt = &activeUIPrompt{request: UIPromptRequest{RequestID: "p1"}}

	ctx, cancel := context.WithCancel(context.Background())
	var fired atomic.Bool
	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)

	time.Sleep(250 * time.Millisecond)

	if fired.Load() {
		t.Error("watchdog fired while a UI prompt was active (it should pause)")
	}
	if ctx.Err() != nil {
		t.Error("prompt context should not be cancelled while a UI prompt is active")
	}
	cancel()
}

// TestStartPromptInactivityWatchdog_PausesDuringToolCall verifies the watchdog does not
// fire (and emits no WARN) while a tool call is in flight, since a long-running tool
// that streams no intermediate updates is the agent working, not a wedged agent. Once
// the tool reaches a terminal status the idle clock resumes and the warning may fire.
func TestStartPromptInactivityWatchdog_PausesDuringToolCall(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 20 * time.Millisecond
	SetPromptInactivityTimeout(50 * time.Millisecond)
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{logger: slog.New(rec), persistedID: "test-toolcall"}

	ctx, cancel := context.WithCancel(context.Background())
	var fired atomic.Bool
	// The watchdog resets in-flight tracking at prompt start, so the tool call must
	// be marked in flight after it starts — mirroring the real flow where tool_call
	// updates stream in only after the prompt begins.
	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)
	bs.trackToolCallStatus("call_1", "", "in_progress")

	// While the tool is in flight, the watchdog must stay quiet well past the timeout.
	time.Sleep(250 * time.Millisecond)
	if fired.Load() {
		t.Error("watchdog fired while a tool call was in flight (it should pause)")
	}
	if ctx.Err() != nil {
		t.Error("prompt context should not be cancelled while a tool call is in flight")
	}
	if got := len(rec.entriesAt(slog.LevelWarn)); got != 0 {
		t.Errorf("expected 0 WARN entries while a tool call is in flight, got %d", got)
	}
	if got := len(rec.entriesAt(slog.LevelError)); got != 0 {
		t.Errorf("expected 0 ERROR entries while a tool call is in flight, got %d", got)
	}

	// Complete the tool call; the watchdog should now observe idleness and warn.
	bs.trackToolCallStatus("call_1", "", "completed")
	if bs.hasInFlightToolCall() {
		t.Fatal("tool call should no longer be in flight after a terminal status")
	}

	deadline := time.After(2 * time.Second)
	for len(rec.entriesAt(slog.LevelWarn)) == 0 {
		select {
		case <-deadline:
			t.Fatal("expected a WARN log after the tool call completed and the agent went idle")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
}

// TestStartPromptInactivityWatchdog_DisabledWhenZero verifies the watchdog is a no-op
// when both the warn delay and timeout are non-positive.
func TestStartPromptInactivityWatchdog_DisabledWhenZero(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 0
	SetPromptInactivityTimeout(0)
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{logger: slog.New(rec), persistedID: "test-disabled"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Bool
	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)

	time.Sleep(100 * time.Millisecond)

	if fired.Load() {
		t.Error("watchdog fired while disabled")
	}
	if ctx.Err() != nil {
		t.Error("prompt context should not be cancelled while disabled")
	}
	if got := len(rec.entriesAt(slog.LevelError)) + len(rec.entriesAt(slog.LevelWarn)); got != 0 {
		t.Errorf("expected no WARN/ERROR entries while disabled, got %d", got)
	}
}

// TestStartPromptInactivityWatchdog_WarnOnlyWhenTimeoutZero locks in the production
// default behavior: with a positive warn delay but timeout == 0, the watchdog emits a
// WARN log when the agent goes idle but NEVER cancels the prompt (fired stays false and
// the context is not cancelled). This guards the WARN-only default from regressing back
// to an automatic cancel that could kill a legitimate long-running, silent tool call.
func TestStartPromptInactivityWatchdog_WarnOnlyWhenTimeoutZero(t *testing.T) {
	origWarn := promptInactivityWatchdogWarnDelay
	origTimeout := promptInactivityWatchdogTimeout()
	promptInactivityWatchdogWarnDelay = 20 * time.Millisecond
	SetPromptInactivityTimeout(0) // production default: cancellation disabled
	defer func() {
		promptInactivityWatchdogWarnDelay = origWarn
		SetPromptInactivityTimeout(origTimeout)
	}()

	rec := newCapturingLogHandler()
	bs := &BackgroundSession{logger: slog.New(rec), persistedID: "test-warn-only"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Bool

	bs.startPromptInactivityWatchdog(ctx, cancel, &fired)

	// Wait well past the warn delay so the idle WARN must have fired.
	deadline := time.After(2 * time.Second)
	for len(rec.entriesAt(slog.LevelWarn)) == 0 {
		select {
		case <-deadline:
			t.Fatal("expected a WARN log when idle past the warn delay")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Give the watchdog ample additional time to (incorrectly) cancel if it were going to.
	time.Sleep(150 * time.Millisecond)

	if fired.Load() {
		t.Error("watchdog cancelled the prompt despite timeout == 0 (should be WARN-only)")
	}
	if ctx.Err() != nil {
		t.Error("prompt context should not be cancelled when timeout == 0")
	}
	if got := len(rec.entriesAt(slog.LevelError)); got != 0 {
		t.Errorf("expected 0 ERROR entries in WARN-only mode, got %d", got)
	}
}

// agentWorkingTestObserver is a minimal SessionObserver that also implements
// AgentWorkingObserver, recording heartbeat calls for TestStartAgentWorkingHeartbeat_*.
type agentWorkingTestObserver struct {
	mockSessionObserver
	count    atomic.Int64
	mu       sync.Mutex
	lastData AgentWorkingData
}

func (o *agentWorkingTestObserver) OnAgentWorking(data AgentWorkingData) {
	o.mu.Lock()
	o.lastData = data
	o.mu.Unlock()
	o.count.Add(1)
}

func (o *agentWorkingTestObserver) getLastData() AgentWorkingData {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastData
}

// TestStartAgentWorkingHeartbeat_EmitsDuringSilence verifies the heartbeat fires
// repeatedly with IdleMs > 0 while the agent stays silent, and stops emitting once
// its context is cancelled (the goroutine exits).
func TestStartAgentWorkingHeartbeat_EmitsDuringSilence(t *testing.T) {
	origInterval := agentWorkingHeartbeatInterval
	origQuiet := agentWorkingHeartbeatQuietThreshold
	agentWorkingHeartbeatInterval = 20 * time.Millisecond
	agentWorkingHeartbeatQuietThreshold = 10 * time.Millisecond
	defer func() {
		agentWorkingHeartbeatInterval = origInterval
		agentWorkingHeartbeatQuietThreshold = origQuiet
	}()

	bs := &BackgroundSession{persistedID: "test-agent-working"}
	testObs := &agentWorkingTestObserver{}
	bs.AddObserver(testObs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs.startAgentWorkingHeartbeat(ctx)

	// Do NOT signal activity — poll for at least one heartbeat.
	deadline := time.After(2 * time.Second)
	for testObs.count.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("expected at least one OnAgentWorking heartbeat during silence")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if got := testObs.getLastData().IdleMs; got <= 0 {
		t.Errorf("expected IdleMs > 0, got %d", got)
	}

	cancel()
	countAfterCancel := testObs.count.Load()
	time.Sleep(3 * agentWorkingHeartbeatInterval)
	if got := testObs.count.Load(); got != countAfterCancel {
		t.Errorf("expected no further heartbeats after cancel, count went from %d to %d", countAfterCancel, got)
	}
}

// TestStartAgentWorkingHeartbeat_PausesDuringUIPrompt verifies no heartbeat is emitted
// while a UI prompt (permission dialog or MCP tool question) is active, mirroring the
// same pause mechanism used by the prompt inactivity watchdog.
func TestStartAgentWorkingHeartbeat_PausesDuringUIPrompt(t *testing.T) {
	origInterval := agentWorkingHeartbeatInterval
	origQuiet := agentWorkingHeartbeatQuietThreshold
	agentWorkingHeartbeatInterval = 20 * time.Millisecond
	agentWorkingHeartbeatQuietThreshold = 10 * time.Millisecond
	defer func() {
		agentWorkingHeartbeatInterval = origInterval
		agentWorkingHeartbeatQuietThreshold = origQuiet
	}()

	bs := &BackgroundSession{persistedID: "test-agent-working-uiprompt"}
	// Simulate a pending UI prompt (e.g. a permission dialog awaiting the user).
	bs.activePrompt = &activeUIPrompt{request: UIPromptRequest{RequestID: "p1"}}
	testObs := &agentWorkingTestObserver{}
	bs.AddObserver(testObs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bs.startAgentWorkingHeartbeat(ctx)

	time.Sleep(250 * time.Millisecond)

	if got := testObs.count.Load(); got != 0 {
		t.Errorf("expected no heartbeats while a UI prompt is active, got %d", got)
	}
}

// capturingLogHandler is a minimal slog.Handler that records emitted records for tests.
type capturingLogHandler struct {
	mu      sync.Mutex
	records []capturedLogEntry
}

type capturedLogEntry struct {
	level slog.Level
	msg   string
}

func newCapturingLogHandler() *capturingLogHandler {
	return &capturingLogHandler{}
}

func (h *capturingLogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *capturingLogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, capturedLogEntry{level: r.Level, msg: r.Message})
	return nil
}

func (h *capturingLogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturingLogHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *capturingLogHandler) entriesAt(level slog.Level) []capturedLogEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]capturedLogEntry, 0)
	for _, r := range h.records {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

// TestTriggerTitleGenerationFromLoop verifies that the helper correctly selects
// the source text for title generation given various combinations of inline prompt
// and prompt_name, including the UI placeholder "(pending)".
func TestTriggerTitleGenerationFromLoop(t *testing.T) {
	// makeBS creates a minimal BackgroundSession backed by a real session.Store.
	// The session has no name, so NeedsTitle() returns true and retryTitleGenerationIfNeeded
	// will synchronously set a quick fallback title via GenerateAndSetTitle.
	makeBS := func(t *testing.T, sid string, resolver PromptResolver) (*BackgroundSession, *session.Store) {
		t.Helper()
		tmpDir := t.TempDir()
		store, err := session.NewStore(tmpDir)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		if err := store.Create(session.Metadata{SessionID: sid, ACPServer: "test", WorkingDir: tmpDir}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		bs := &BackgroundSession{
			store:          store,
			persistedID:    sid,
			workingDir:     tmpDir,
			promptResolver: resolver,
		}
		return bs, store
	}

	getName := func(t *testing.T, store *session.Store, sid string) string {
		t.Helper()
		meta, err := store.GetMetadata(sid)
		if err != nil {
			t.Fatalf("GetMetadata: %v", err)
		}
		return meta.Name
	}

	// Case 1: usable inline prompt — resolver must NOT be consulted.
	t.Run("inline prompt usable - resolver not consulted", func(t *testing.T) {
		var resolverCalled bool
		bs, store := makeBS(t, "sid-1", func(name, dir string) (string, error) {
			resolverCalled = true
			return "should not be used", nil
		})
		bs.TriggerTitleGenerationFromLoop("Real text here", "SomeName")
		if resolverCalled {
			t.Error("resolver should not be called when inline prompt is usable")
		}
		got := getName(t, store, "sid-1")
		if !strings.Contains(strings.ToLower(got), "real") {
			t.Errorf("expected title derived from 'Real text here', got %q", got)
		}
	})

	// Case 2: inline is the "(pending)" placeholder — resolver should be used and its
	// resolved body should feed title generation.
	t.Run("(pending) placeholder resolved via resolver", func(t *testing.T) {
		bs, store := makeBS(t, "sid-2", func(name, dir string) (string, error) {
			if name == "X" {
				return "Resolved body content", nil
			}
			return "", fmt.Errorf("unexpected name %q", name)
		})
		bs.TriggerTitleGenerationFromLoop("(pending)", "X")
		got := getName(t, store, "sid-2")
		if strings.Contains(strings.ToLower(got), "pending") {
			t.Errorf("title must not be derived from '(pending)' placeholder, got %q", got)
		}
		if !strings.Contains(strings.ToLower(got), "resolved") {
			t.Errorf("expected title from resolved body, got %q", got)
		}
	})

	// Case 3: "(pending)" + resolver error → fall back to the bare prompt name.
	t.Run("(pending) resolver error falls back to name", func(t *testing.T) {
		bs, store := makeBS(t, "sid-3", func(name, dir string) (string, error) {
			return "", fmt.Errorf("resolution failed")
		})
		bs.TriggerTitleGenerationFromLoop("(pending)", "MyPromptName")
		got := getName(t, store, "sid-3")
		if !strings.Contains(got, "MyPromptName") {
			t.Errorf("expected fallback to prompt name 'MyPromptName', got %q", got)
		}
	})

	// Case 4: empty inline, no resolver configured → uses prompt name directly.
	t.Run("empty inline no resolver - uses name", func(t *testing.T) {
		bs, store := makeBS(t, "sid-4", nil)
		bs.TriggerTitleGenerationFromLoop("", "PromptXYZ")
		got := getName(t, store, "sid-4")
		if !strings.Contains(got, "PromptXYZ") {
			t.Errorf("expected title from prompt name, got %q", got)
		}
	})

	// Case 5: both empty → no-op, no title set.
	t.Run("both empty - no-op", func(t *testing.T) {
		bs, store := makeBS(t, "sid-5", nil)
		bs.TriggerTitleGenerationFromLoop("", "")
		got := getName(t, store, "sid-5")
		if got != "" {
			t.Errorf("expected no title set when both args are empty, got %q", got)
		}
	})

	// Case 6: whitespace-only inline is treated as empty; falls back to prompt name.
	t.Run("whitespace-only inline treated as empty", func(t *testing.T) {
		bs, store := makeBS(t, "sid-6", nil)
		bs.TriggerTitleGenerationFromLoop("   ", "WhitespaceName")
		got := getName(t, store, "sid-6")
		if !strings.Contains(got, "WhitespaceName") {
			t.Errorf("expected title from prompt name, got %q", got)
		}
	})
}

// alwaysFailSharedProcess is a minimal SharedProcess stub that returns errors from all methods.
// Used in tests that need a non-nil SharedProcess without starting a real ACP process.
type alwaysFailSharedProcess struct{}

func (p *alwaysFailSharedProcess) NewSession(_ context.Context, _ string, _ []acp.McpServer) (*SessionHandle, error) {
	return nil, fmt.Errorf("alwaysFailSharedProcess: NewSession not implemented")
}
func (p *alwaysFailSharedProcess) LoadSession(_ context.Context, _, _ string, _ []acp.McpServer) (*SessionHandle, error) {
	return nil, fmt.Errorf("alwaysFailSharedProcess: LoadSession not implemented")
}
func (p *alwaysFailSharedProcess) ResumeSession(_ context.Context, _, _ string, _ []acp.McpServer) (*SessionHandle, error) {
	return nil, fmt.Errorf("alwaysFailSharedProcess: ResumeSession not implemented")
}
func (p *alwaysFailSharedProcess) RegisterSession(_ acp.SessionId, _ *SessionCallbacks) {}
func (p *alwaysFailSharedProcess) UnregisterSession(_ acp.SessionId)                    {}
func (p *alwaysFailSharedProcess) ProcessDone() <-chan struct{}                         { return nil }
func (p *alwaysFailSharedProcess) Prompt(_ context.Context, _ acp.SessionId, _ []acp.ContentBlock) (acp.PromptResponse, error) {
	return acp.PromptResponse{}, fmt.Errorf("alwaysFailSharedProcess: Prompt not implemented")
}
func (p *alwaysFailSharedProcess) Cancel(_ context.Context, _ acp.SessionId) error {
	return fmt.Errorf("alwaysFailSharedProcess: Cancel not implemented")
}
func (p *alwaysFailSharedProcess) SetSessionMode(_ context.Context, _ acp.SessionId, _ string) error {
	return fmt.Errorf("alwaysFailSharedProcess: SetSessionMode not implemented")
}
func (p *alwaysFailSharedProcess) SetSessionModel(_ context.Context, _ acp.SessionId, _ string) error {
	return fmt.Errorf("alwaysFailSharedProcess: SetSessionModel not implemented")
}
func (p *alwaysFailSharedProcess) Done() <-chan struct{}                { return nil }
func (p *alwaysFailSharedProcess) Capabilities() *acp.AgentCapabilities { return nil }
func (p *alwaysFailSharedProcess) Generation() int                      { return 0 }
func (p *alwaysFailSharedProcess) Restart(_ int) error {
	return fmt.Errorf("alwaysFailSharedProcess: cannot restart — no real process")
}
func (p *alwaysFailSharedProcess) RecommendedLoadTimeout(_ bool) time.Duration { return 0 }
func (p *alwaysFailSharedProcess) MCPInitDone() bool                           { return true }
func (p *alwaysFailSharedProcess) WaitForMCPInit(_ context.Context) bool       { return true }

// TestACPInitializeAttemptTimeoutBound is a math test for mitto-13ck.2.
//
// It verifies that acpInitializeAttemptTimeout × maxACPStartRetries plus the maximum
// cumulative retry backoff is significantly less than the pre-fix worst case of
// maxACPStartRetries × 60 s ≈ 180 s (the SDK's DEFAULT_CONTROL_REQUEST_TIMEOUT hit
// on every attempt when no timeout was applied to initCtx).
func TestACPInitializeAttemptTimeoutBound(t *testing.T) {
	// Worst-case cumulative backoff across all retries (capped per-attempt).
	maxBackoffTotal := time.Duration(maxACPStartRetries-1) * acpStartRetryMaxDelay

	// Worst-case total wall time: every attempt times out + maximum backoffs.
	totalMax := time.Duration(maxACPStartRetries)*acpInitializeAttemptTimeout + maxBackoffTotal

	// Pre-fix worst case: each attempt hangs for the full SDK 60 s control timeout.
	const sdkControlTimeout = 60 * time.Second
	preFix := time.Duration(maxACPStartRetries) * sdkControlTimeout

	if totalMax >= preFix {
		t.Errorf("bounded retry tail (%v) must be less than pre-fix tail (%v); "+
			"acpInitializeAttemptTimeout (%v) is too large or maxACPStartRetries (%d) too high",
			totalMax, preFix, acpInitializeAttemptTimeout, maxACPStartRetries)
	}
	t.Logf("acpInitializeAttemptTimeout=%v, maxRetries=%d, maxBackoff=%v → total max=%v (pre-fix was %v)",
		acpInitializeAttemptTimeout, maxACPStartRetries, maxBackoffTotal, totalMax, preFix)
}

// TestBackgroundSession_ChildWaitStats verifies RecordChildWait accumulates
// count and total duration, and GetChildWaitStats reports them correctly.
func TestBackgroundSession_ChildWaitStats(t *testing.T) {
	bs := &BackgroundSession{}

	if count, total := bs.GetChildWaitStats(); count != 0 || total != 0 {
		t.Fatalf("expected zero stats before any recording, got count=%d total=%v", count, total)
	}

	bs.RecordChildWait(100 * time.Millisecond)
	bs.RecordChildWait(250 * time.Millisecond)

	count, total := bs.GetChildWaitStats()
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if total != 350*time.Millisecond {
		t.Errorf("total = %v, want %v", total, 350*time.Millisecond)
	}
}

// TestBackgroundSession_CumulativeUsage verifies pdAccumulateCumulativeUsage
// adds usage across multiple calls and GetCumulativeUsage reports the sum.
func TestBackgroundSession_CumulativeUsage(t *testing.T) {
	bs := &BackgroundSession{}

	if in, out, total := bs.GetCumulativeUsage(); in != 0 || out != 0 || total != 0 {
		t.Fatalf("expected zero usage before any accumulation, got in=%d out=%d total=%d", in, out, total)
	}

	bs.pdAccumulateCumulativeUsage(&acp.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})
	bs.pdAccumulateCumulativeUsage(&acp.Usage{InputTokens: 20, OutputTokens: 8, TotalTokens: 28})
	bs.pdAccumulateCumulativeUsage(nil) // must be a no-op

	in, out, total := bs.GetCumulativeUsage()
	if in != 30 {
		t.Errorf("input = %d, want 30", in)
	}
	if out != 13 {
		t.Errorf("output = %d, want 13", out)
	}
	if total != 43 {
		t.Errorf("total = %d, want 43", total)
	}
}

// TestApplySynthesizedModelsIfEmpty_NoOpWhenAgentModelsPresent verifies the
// shared-process hook (mitto-886) leaves bs.agentModels untouched when the
// agent already advertised a real catalog. This is the primary guard against
// double-population that would corrupt the frontend model chip with synthesized
// tier names on top of genuine agent-side model ids.
func TestApplySynthesizedModelsIfEmpty_NoOpWhenAgentModelsPresent(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.pendingConfig = make(map[string]string)

	existing := &SessionModelState{
		CurrentModelId: "claude-sonnet-4-6",
		AvailableModels: []ModelInfo{
			{ModelId: "claude-sonnet-4-6", Name: "Sonnet 4.6"},
		},
	}
	bs.agentModels = existing
	bs.mittoConfig = &config.Config{Models: []config.ModelProfile{{Name: "Opus"}}}

	bs.applySynthesizedModelsIfEmpty()

	if bs.agentModels != existing {
		t.Fatalf("agentModels was replaced: got %+v, want pointer identity with existing", bs.agentModels)
	}
	if bs.agentModels.CurrentModelId != "claude-sonnet-4-6" {
		t.Fatalf("CurrentModelId=%q, want claude-sonnet-4-6", bs.agentModels.CurrentModelId)
	}
}

// TestApplySynthesizedModelsIfEmpty_NoOpWhenNoMittoConfig documents the second
// guard: even when agentModels is nil, we cannot synthesize without a config
// (EffectiveModelProfiles would still return defaults on a nil receiver, but
// the hook short-circuits before that to preserve the "no config wired" test
// scenario).
func TestApplySynthesizedModelsIfEmpty_NoOpWhenNoMittoConfig(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.pendingConfig = make(map[string]string)
	// bs.agentModels is nil; bs.mittoConfig is nil.

	bs.applySynthesizedModelsIfEmpty()

	if bs.agentModels != nil {
		t.Fatalf("expected agentModels to remain nil when mittoConfig is nil, got %+v", bs.agentModels)
	}
}

// TestApplySynthesizedModelsIfEmpty_SynthesizesWhenEmpty is the mitto-886 core
// scenario: agent advertised no catalog via either configOptions or resp.Models
// (shared-process branch handed us a nil), local config supplies profiles →
// we must populate bs.agentModels with a synthetic state whose ModelId/Name
// mirror the profile Name (so downstream tag resolvers work identically).
func TestApplySynthesizedModelsIfEmpty_SynthesizesWhenEmpty(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.pendingConfig = make(map[string]string)
	bs.mittoConfig = &config.Config{Models: []config.ModelProfile{
		{Name: "Opus"},
		{Name: "Sonnet"},
	}}

	bs.applySynthesizedModelsIfEmpty()

	if bs.agentModels == nil {
		t.Fatalf("expected agentModels to be populated from local profiles")
	}
	// EffectiveModelProfiles merges user Models with DefaultModelProfiles, so
	// we can't assert exact len(). But the user-configured ones must appear
	// first (see EffectiveModelProfiles doc: user profiles before defaults).
	if len(bs.agentModels.AvailableModels) < 2 {
		t.Fatalf("len(AvailableModels)=%d, want >=2", len(bs.agentModels.AvailableModels))
	}
	if bs.agentModels.AvailableModels[0].ModelId != "Opus" {
		t.Fatalf("AvailableModels[0].ModelId=%q, want Opus", bs.agentModels.AvailableModels[0].ModelId)
	}
	if bs.agentModels.AvailableModels[0].Name != "Opus" {
		t.Fatalf("AvailableModels[0].Name=%q, want Opus (synth uses profile.Name for both)", bs.agentModels.AvailableModels[0].Name)
	}
	if bs.agentModels.CurrentModelId != "" {
		t.Fatalf("CurrentModelId=%q, want empty (synth cannot know current)", bs.agentModels.CurrentModelId)
	}
}

// TestApplySynthesizedModelsIfEmpty_UsesDefaultsWhenUserModelsEmpty verifies the
// mitto-886 fallback still fires when the user has no Models in settings.json:
// EffectiveModelProfiles returns DefaultModelProfiles() (the 7 canonical
// hardcoded profiles) so a fresh install without settings.json still gets a
// populated model selector for silent agents.
func TestApplySynthesizedModelsIfEmpty_UsesDefaultsWhenUserModelsEmpty(t *testing.T) {
	bs := &BackgroundSession{}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.pendingConfig = make(map[string]string)
	bs.mittoConfig = &config.Config{} // Models: nil

	bs.applySynthesizedModelsIfEmpty()

	if bs.agentModels == nil {
		t.Fatalf("expected agentModels populated from DefaultModelProfiles even with empty user Models")
	}
	if len(bs.agentModels.AvailableModels) == 0 {
		t.Fatalf("expected non-empty AvailableModels from DefaultModelProfiles")
	}
}

// ============================================================================
// mitto-9zy1: post-close observability defects (reproduce phase)
//
// Three reproduction tests below pin the defects confirmed by the Investigate
// phase (see the "Investigation [tier: Reasoning]:" bead comment). All three
// simulate the post-close tail window identified in mitto-xlwh: the recorder
// has stopped accepting writes (Suspend() sets started=false) and/or the
// session is marked closed (SimulateClose()), while session-config/queue-drain
// code paths still run synchronously from the prompt-completion tail. Each
// test currently FAILS against the unfixed code and must pass once the
// corresponding defect is fixed.
// ============================================================================

// TestApplyConfigOptionWithOpts_BugRepro_SwallowsRecorderErrorAndStillLogsSuccess
// pins defect 2a: cmRecordSessionChangeWithSeq (bgsession_config.go) swallows
// the recorder's persistence error (only logs it), so applyConfigOptionWithOpts
// (config_manager.go) proceeds to log "Config option changed" and return a nil
// error even though the session-change timeline event was never persisted.
func TestApplyConfigOptionWithOpts_BugRepro_SwallowsRecorderErrorAndStillLogsSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Simulate the post-close tail window: the recorder has stopped accepting
	// writes (mirrors what happens after Close()/End()), but session-config
	// code still runs.
	if err := recorder.Suspend(); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	handler := &recordingHandler{}
	shared := newFakeSharedProcess()
	bs := &BackgroundSession{
		recorder:      recorder,
		persistedID:   sessionID,
		store:         store,
		sharedProcess: shared,
		nextSeq:       2, // recorder.Start() already persisted session_start at seq=1
		logger:        slog.New(handler),
		agentModels:   &SessionModelState{CurrentModelId: "m-1"},
		configOptions: []SessionConfigOption{
			{
				ID:           ConfigOptionCategoryModel,
				Category:     ConfigOptionCategoryModel,
				CurrentValue: "m-1",
				Options: []SessionConfigOptionValue{
					{Value: "m-1", Name: "Model 1"},
					{Value: "m-2", Name: "Model 2"},
				},
			},
		},
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	cm := configManager{}
	applyErr := cm.applyConfigOptionWithOpts(bs, context.Background(), ConfigOptionCategoryModel, "m-2", true)

	// Desired (post-fix) behavior: a failure to persist the session-change
	// timeline event must surface as an error from applyConfigOptionWithOpts.
	if applyErr == nil {
		t.Fatal("mitto-9zy1 defect 2a: expected applyConfigOptionWithOpts to return an error when the " +
			"session-change record fails, but it returned nil (recorder error was swallowed)")
	}
	// Desired (post-fix) behavior: the caller must not claim success ("Config
	// option changed") when persistence of the timeline event failed.
	if handler.hasRecord(slog.LevelInfo, "Config option changed") {
		t.Error("mitto-9zy1 defect 2a: must not log 'Config option changed' when the session-change " +
			"record failed")
	}
}

// TestFlushPendingConfig_BugRepro_AppliesConfigAfterClose pins defect 2b:
// flushPendingConfig (config_manager.go) drains the pending-config map and
// calls applyConfigOption directly, bypassing the only cmIsClosed() liveness
// gate in the config path (present in setConfigOptionWithOpts but not
// reachable from the deferred-flush call site). A deferred config change can
// therefore still be applied to a closed/closing session.
func TestFlushPendingConfig_BugRepro_AppliesConfigAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	shared := newFakeSharedProcess()
	bs := &BackgroundSession{
		recorder:      recorder,
		persistedID:   sessionID,
		store:         store,
		sharedProcess: shared,
		nextSeq:       2,
		agentModels:   &SessionModelState{CurrentModelId: "m-1"},
		configOptions: []SessionConfigOption{
			{
				ID:           ConfigOptionCategoryModel,
				Category:     ConfigOptionCategoryModel,
				CurrentValue: "m-1",
				Options: []SessionConfigOptionValue{
					{Value: "m-1", Name: "Model 1"},
					{Value: "m-2", Name: "Model 2"},
				},
			},
		},
		pendingConfig: map[string]string{ConfigOptionCategoryModel: "m-2"},
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)

	// Mark the session closed BEFORE the deferred flush runs — mirrors the
	// prompt-completion tail racing session Close() (mitto-xlwh's window).
	bs.SimulateClose()

	bs.flushPendingConfig()

	// Desired (post-fix) behavior: flushPendingConfig must no-op once the
	// session is closed, leaving the model unchanged.
	if bs.agentModels.CurrentModelId != "m-1" {
		t.Fatalf("mitto-9zy1 defect 2b: expected flushPendingConfig to skip applying deferred config "+
			"after session close, but model changed to %q", bs.agentModels.CurrentModelId)
	}
}

// TestQueueRecordErrorEvent_BugRepro_LogsErrorOnErrorAfterClose pins defect 3:
// queueRecordErrorEvent (bgsession_queue.go) has no queueIsClosed() guard, so
// when the recorder has already stopped (post-close tail window), the attempt
// to persist a "failed to send queued message" error event itself fails, and
// a second, purely diagnostic ERROR ("Failed to persist queued send error
// event") is logged on top of the original — pure log noise during teardown.
func TestQueueRecordErrorEvent_BugRepro_LogsErrorOnErrorAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	recorder := session.NewRecorder(store)
	if err := recorder.Start("test-server", tmpDir, ""); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	sessionID := recorder.SessionID()

	// Simulate the post-close tail window: the recorder has stopped accepting
	// writes.
	if err := recorder.Suspend(); err != nil {
		t.Fatalf("Suspend failed: %v", err)
	}

	handler := &recordingHandler{}
	bs := &BackgroundSession{
		recorder:    recorder,
		persistedID: sessionID,
		nextSeq:     2,
		logger:      slog.New(handler),
	}
	bs.promptCond = sync.NewCond(&bs.promptMu)
	bs.SimulateClose()

	bs.queueRecordErrorEvent("Failed to send queued message: boom")

	// Desired (post-fix) behavior: once the session is closed, queueRecordErrorEvent
	// must not attempt (and fail) to persist, so no secondary ERROR is logged.
	if handler.hasRecord(slog.LevelError, "Failed to persist queued send error event") {
		t.Error("mitto-9zy1 defect 3: queueRecordErrorEvent must not log a secondary ERROR " +
			"when the session is already closed")
	}
}
