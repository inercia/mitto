package processors

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// This file covers the mitto-3od.4 acceptance-criterion test matrix (one
// test per AC row from the bead description) that is NOT already exercised
// by close_router_test.go or apply_close_phase_prompts_gate_test.go. It
// reuses those files' helpers (newCloseRouterApplyTestStore,
// waitForCloseRouterState) rather than duplicating infrastructure.

// knowledgeRouterYAMLPath is the real builtin processor file, loaded (not
// re-typed) so prompt-contract assertions pin the shipping template.
const knowledgeRouterYAMLPath = "../../config/processors/builtin/knowledge-router.yaml"

// dispatchKnowledgeRouter loads the real knowledge-router.yaml, wires it
// into a fresh Manager with mockFn as the completion callback, and runs
// ApplyOnClose against sessionID/historySnapshot in workingDir. Blocks until
// mockFn has been invoked (or the test times out).
func dispatchKnowledgeRouter(t *testing.T, store *session.Store, sessionID, workingDir, historySnapshot string, mockFn func(prompt string) (PromptCompletion, error)) string {
	t.Helper()
	loader := NewLoader(filepath.Dir(knowledgeRouterYAMLPath), nil)
	proc, err := loader.LoadFile(knowledgeRouterYAMLPath)
	if err != nil {
		t.Fatalf("LoadFile(%s): %v", knowledgeRouterYAMLPath, err)
	}

	m := NewManager("", nil)
	m.processors = []*Processor{proc}

	var mu sync.Mutex
	var capturedPrompt string
	done := make(chan struct{}, 1)
	m.SetPromptCompletionFunc(func(_ context.Context, _, _, _, prompt string) (PromptCompletion, error) {
		mu.Lock()
		capturedPrompt = prompt
		mu.Unlock()
		completion, err := mockFn(prompt)
		done <- struct{}{}
		return completion, err
	})

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:       sessionID,
		WorkspaceUUID:   "ws-" + sessionID,
		WorkingDir:      workingDir,
		SessionStore:    store,
		HistorySnapshot: historySnapshot,
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for promptCompletionFunc to be invoked")
	}

	mu.Lock()
	defer mu.Unlock()
	return capturedPrompt
}

// --- AC#1: no-op ------------------------------------------------------------------

func TestKnowledgeRouter_NoOp_EmptyFindings(t *testing.T) {
	const sessionID = "sess-ac1-noop"
	store := newCloseRouterApplyTestStore(t, sessionID)

	dispatchKnowledgeRouter(t, store, sessionID, t.TempDir(), `{"events":[]}`, func(string) (PromptCompletion, error) {
		return PromptCompletion{FinalMessage: `{"findings":[]}`}, nil
	})

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 1 && !s.Runs[0].CompletedAt.IsZero()
	})
	if len(state.Runs[0].Findings) != 0 {
		t.Fatalf("Findings = %+v, want empty for a zero-candidate snapshot", state.Runs[0].Findings)
	}
}

// --- AC#2: preference-only ----------------------------------------------------------

func TestKnowledgeRouter_PreferencesOnly_TargetsConfiguredFile(t *testing.T) {
	const sessionID = "sess-ac2-prefs"
	store := newCloseRouterApplyTestStore(t, sessionID)

	dispatchKnowledgeRouter(t, store, sessionID, t.TempDir(), `{"events":[{"text":"I like squash commits"}]}`, func(string) (PromptCompletion, error) {
		return PromptCompletion{FinalMessage: `{"findings":[{"text":"prefers squash commits","destination":"preferences","written":true,"target_path":".augment/rules/90-local.md"}]}`}, nil
	})

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 1 && !s.Runs[0].CompletedAt.IsZero()
	})
	findings := state.Runs[0].Findings
	if len(findings) != 1 {
		t.Fatalf("Findings = %+v, want exactly 1", findings)
	}
	f := findings[0]
	if f.Destination != "preferences" || !f.Written || f.TargetPath != ".augment/rules/90-local.md" {
		t.Errorf("finding = %+v, want preferences/written/target set", f)
	}
}

// --- AC#3/#4: scoped-rule cross-agent -------------------------------------------------

// TestKnowledgeRouter_ScopedRule_AuggieLayout exercises the real template's
// auto-detect branch for a workspace laid out like an Augment/Auggie project
// (.augment/rules/ present): the dispatched prompt must resolve both the
// preferences and rules destinations to that directory, and a mock "rules"
// finding must be recorded verbatim in the sidecar.
func TestKnowledgeRouter_ScopedRule_AuggieLayout(t *testing.T) {
	const sessionID = "sess-ac3-auggie"
	store := newCloseRouterApplyTestStore(t, sessionID)

	workingDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workingDir, ".augment", "rules"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	prompt := dispatchKnowledgeRouter(t, store, sessionID, workingDir, `{"events":[{"text":"naming convention X"}]}`, func(string) (PromptCompletion, error) {
		return PromptCompletion{FinalMessage: `{"findings":[{"text":"project uses naming convention X","destination":"rules","written":true,"target_path":".augment/rules/40-naming.md"}]}`}, nil
	})

	if !strings.Contains(prompt, ".augment/rules") {
		t.Errorf("dispatched prompt does not reference the auto-detected .augment/rules directory; prompt=%s", prompt)
	}
	if strings.Contains(prompt, ".cursor/rules") || strings.Contains(prompt, ".codex/rules") {
		t.Errorf("dispatched prompt leaked a non-matching agent layout; prompt=%s", prompt)
	}

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 1 && !s.Runs[0].CompletedAt.IsZero()
	})
	findings := state.Runs[0].Findings
	if len(findings) != 1 || findings[0].Destination != "rules" || findings[0].TargetPath != ".augment/rules/40-naming.md" {
		t.Fatalf("Findings = %+v, want one rules finding targeting .augment/rules/40-naming.md", findings)
	}
}

// TestKnowledgeRouter_ScopedRule_ClaudeCodeLayout documents the AC#4
// cross-agent gap: config/processors/builtin/knowledge-router.yaml's
// auto-detect block only checks .augment/rules, .cursor/rules, and
// .codex/rules — it has no branch for Claude Code's .claude/rules or
// CLAUDE.md layout, so an identical finding cannot yet be shown to route to
// a different file depending on the ACP server. Tracked by mitto-3od.5
// (filed alongside this test matrix); un-skip once that template gap is
// closed.
func TestKnowledgeRouter_ScopedRule_ClaudeCodeLayout(t *testing.T) {
	t.Skip("blocked on mitto-3od.5: knowledge-router.yaml does not yet auto-detect the Claude Code .claude/rules or CLAUDE.md layout (only .augment, .cursor, .codex are detected today)")
}

// --- AC#5: beads-memory-only (prompt-contract lint) -----------------------------------

// TestKnowledgeRouter_BeadsMemory_PromptContract pins the router's memory
// destination instructions at the source: the router's Go code has no path
// that shells out to `bd` directly (it dispatches an LLM prompt, and the LLM
// runs `bd` itself as a tool call), so the contract "never bd memories
// --json, always a targeted bd memories <keyword>, refine via bd remember
// --key on a hit" can only be verified by asserting the literals are present
// in the real prompt template.
func TestKnowledgeRouter_BeadsMemory_PromptContract(t *testing.T) {
	data, err := os.ReadFile(knowledgeRouterYAMLPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", knowledgeRouterYAMLPath, err)
	}
	body := string(data)

	mustContain := []string{
		`bd memories "<keyword>"`,
		`bd remember --key <existing-key>`,
		`never`,
		`bd memories --json`,
	}
	for _, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("knowledge-router.yaml missing expected literal %q", want)
		}
	}
}

// --- AC#6: mixed-input ---------------------------------------------------------------

func TestKnowledgeRouter_MixedInput_OnePerDestination(t *testing.T) {
	const sessionID = "sess-ac6-mixed"
	store := newCloseRouterApplyTestStore(t, sessionID)

	const mockMsg = `{"findings":[
		{"text":"prefers rebase over merge","destination":"preferences","written":true,"target_path":"p.md"},
		{"text":"all new endpoints need integration tests","destination":"rules","written":true,"target_path":"r.md"},
		{"text":"the retry backoff is capped at 30s by design","destination":"memory","written":true},
		{"text":"add pagination to the list endpoint","destination":"issue","written":true},
		{"text":"just restated the task, no new info","destination":"none","written":false}
	]}`
	dispatchKnowledgeRouter(t, store, sessionID, t.TempDir(), `{"events":[{"text":"mixed conversation"}]}`, func(string) (PromptCompletion, error) {
		return PromptCompletion{FinalMessage: mockMsg}, nil
	})

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 1 && !s.Runs[0].CompletedAt.IsZero()
	})
	findings := state.Runs[0].Findings
	if len(findings) != 5 {
		t.Fatalf("Findings = %+v, want 5 (one per destination)", findings)
	}

	wantDestinations := []string{"preferences", "rules", "memory", "issue", "none"}
	seenKeys := make(map[string]bool, 5)
	for i, f := range findings {
		if f.Destination != wantDestinations[i] {
			t.Errorf("findings[%d].Destination = %q, want %q", i, f.Destination, wantDestinations[i])
		}
		if seenKeys[f.LogicalKey] {
			t.Errorf("findings[%d].LogicalKey %q collided with an earlier finding — dedup must not collapse distinct candidates", i, f.LogicalKey)
		}
		seenKeys[f.LogicalKey] = true
	}
	if findings[4].Written {
		t.Errorf("the 'none' destination finding must be Written=false, got %+v", findings[4])
	}
}

// --- AC#7: retry idempotency -----------------------------------------------------------

// TestKnowledgeRouter_Retry_SameHash_ZeroNewOps pins the "same snapshot
// twice" half of AC#7: the second run against the identical history
// snapshot must see the prior written logical key injected into its
// dispatched prompt, and (simulating the LLM correctly honoring that state
// block) records zero NEW findings in the second run.
func TestKnowledgeRouter_Retry_SameHash_ZeroNewOps(t *testing.T) {
	const sessionID = "sess-ac7-samehash"
	store := newCloseRouterApplyTestStore(t, sessionID)

	const historySnapshot = `{"events":[{"text":"prefers dark mode"}]}`
	hash := session.CloseRouterSnapshotHash([]byte(historySnapshot))
	priorKey := session.CloseRouterLogicalKey("prefers dark mode")
	if err := session.WriteCloseRouterState(store, sessionID, session.CloseRouterState{
		Runs: []session.CloseRouterRun{{
			RunID: "run-prior", HistorySnapshotHash: hash,
			CompletedAt: time.Now().Add(-time.Hour),
			Findings: []session.CloseRouterFinding{
				{LogicalKey: priorKey, Destination: "preferences", Written: true, TargetPath: ".augment/rules/90-local.md"},
			},
		}},
	}); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	prompt := dispatchKnowledgeRouter(t, store, sessionID, t.TempDir(), historySnapshot, func(string) (PromptCompletion, error) {
		return PromptCompletion{FinalMessage: `{"findings":[]}`}, nil
	})

	if !strings.Contains(prompt, priorKey) {
		t.Errorf("second dispatch against the same snapshot hash must inject the prior logical key; prompt=%s", prompt)
	}

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 2 && !s.Runs[1].CompletedAt.IsZero()
	})
	if len(state.Runs[1].Findings) != 0 {
		t.Fatalf("second run Findings = %+v, want zero new persistence ops", state.Runs[1].Findings)
	}
}

// TestKnowledgeRouter_Retry_DifferentHash_RunsNormally pins the "history
// changed" half of AC#7: a second run whose snapshot hashes differently
// from the sidecar's recorded run must NOT inject a router-state block, and
// runs (and persists) normally.
func TestKnowledgeRouter_Retry_DifferentHash_RunsNormally(t *testing.T) {
	const sessionID = "sess-ac7-diffhash"
	store := newCloseRouterApplyTestStore(t, sessionID)

	const olderSnapshot = `{"events":[{"text":"prefers dark mode"}]}`
	olderHash := session.CloseRouterSnapshotHash([]byte(olderSnapshot))
	if err := session.WriteCloseRouterState(store, sessionID, session.CloseRouterState{
		Runs: []session.CloseRouterRun{{
			RunID: "run-prior", HistorySnapshotHash: olderHash,
			CompletedAt: time.Now().Add(-time.Hour),
			Findings: []session.CloseRouterFinding{
				{LogicalKey: session.CloseRouterLogicalKey("prefers dark mode"), Destination: "preferences", Written: true, TargetPath: "p.md"},
			},
		}},
	}); err != nil {
		t.Fatalf("WriteCloseRouterState: %v", err)
	}

	const newerSnapshot = `{"events":[{"text":"prefers dark mode"},{"text":"also prefers tabs"}]}`
	prompt := dispatchKnowledgeRouter(t, store, sessionID, t.TempDir(), newerSnapshot, func(string) (PromptCompletion, error) {
		return PromptCompletion{FinalMessage: `{"findings":[{"text":"also prefers tabs","destination":"preferences","written":true,"target_path":"p.md"}]}`}, nil
	})

	// The static template body always mentions "<mitto_close_router_state>"
	// (in its own step-4 instructions), so a bare tag-name Contains check
	// would false-positive on every run. Look for the block's actual
	// injected content instead (see buildCloseRouterStateBlock).
	if strings.Contains(prompt, "IMPORTANT: Findings already persisted by an earlier run") {
		t.Errorf("a snapshot-hash mismatch must not inject a router-state block; prompt=%s", prompt)
	}

	state := waitForCloseRouterState(t, store, sessionID, func(s session.CloseRouterState) bool {
		return len(s.Runs) == 2 && !s.Runs[1].CompletedAt.IsZero()
	})
	if len(state.Runs[1].Findings) != 1 || state.Runs[1].Findings[0].Destination != "preferences" {
		t.Fatalf("second run Findings = %+v, want one new preferences finding (full pass on changed history)", state.Runs[1].Findings)
	}
}

// --- AC#8: legacy-suppression ----------------------------------------------------------

// gatedLegacyEnabledWhenCase pins one legacy close processor's REAL
// enabledWhen (loaded from its shipping YAML) exercised through
// evaluateEnabledWhen directly with the ProcessorInput fields needed to
// satisfy every OTHER condition in its gate, isolating the knowledge-router
// suppression clause's effect from the processor's own preconditions
// (ACP server type, working-dir layout, loop state).
type gatedLegacyEnabledWhenCase struct {
	yamlPath   string
	buildInput func(workingDir string, routerEnabled bool) *ProcessorInput
	setupDir   func(t *testing.T, workingDir string)
}

func routerSnapshotFn(enabled bool) func() *config.PromptsSnapshot {
	return func() *config.PromptsSnapshot {
		if enabled {
			return &config.PromptsSnapshot{Names: []string{"knowledge-router"}, EnabledNames: []string{"knowledge-router"}}
		}
		return &config.PromptsSnapshot{Names: []string{"knowledge-router"}, EnabledNames: nil}
	}
}

func TestKnowledgeRouter_LegacySuppression_AllFourBuiltins(t *testing.T) {
	cases := map[string]gatedLegacyEnabledWhenCase{
		"memorize-preferences": {
			yamlPath: "../../config/processors/builtin/memorize-preferences.yaml",
			buildInput: func(workingDir string, routerEnabled bool) *ProcessorInput {
				return &ProcessorInput{WorkingDir: workingDir, IsLoop: false, PromptsSnapshotFn: routerSnapshotFn(routerEnabled)}
			},
		},
		"extract-memories-on-close": {
			yamlPath: "../../config/processors/builtin/extract-memories-on-close.yaml",
			setupDir: func(t *testing.T, workingDir string) {
				if err := os.MkdirAll(filepath.Join(workingDir, ".beads"), 0o755); err != nil {
					t.Fatalf("MkdirAll .beads: %v", err)
				}
			},
			buildInput: func(workingDir string, routerEnabled bool) *ProcessorInput {
				return &ProcessorInput{WorkingDir: workingDir, IsLoop: false, PromptsSnapshotFn: routerSnapshotFn(routerEnabled)}
			},
		},
		"auggie-update-rules": {
			yamlPath: "../../config/processors/builtin/auggie-update-rules.yaml",
			setupDir: func(t *testing.T, workingDir string) {
				if err := os.MkdirAll(filepath.Join(workingDir, ".augment", "rules"), 0o755); err != nil {
					t.Fatalf("MkdirAll .augment/rules: %v", err)
				}
			},
			buildInput: func(workingDir string, routerEnabled bool) *ProcessorInput {
				return &ProcessorInput{WorkingDir: workingDir, IsLoop: false, ACPServer: "augment", PromptsSnapshotFn: routerSnapshotFn(routerEnabled)}
			},
		},
		"claude-update-memory": {
			yamlPath: "../../config/processors/builtin/claude-update-memory.yaml",
			setupDir: func(t *testing.T, workingDir string) {
				if err := os.WriteFile(filepath.Join(workingDir, "CLAUDE.md"), []byte("# notes\n"), 0o644); err != nil {
					t.Fatalf("WriteFile CLAUDE.md: %v", err)
				}
			},
			buildInput: func(workingDir string, routerEnabled bool) *ProcessorInput {
				return &ProcessorInput{WorkingDir: workingDir, IsLoop: false, ACPServer: "claude-code", PromptsSnapshotFn: routerSnapshotFn(routerEnabled)}
			},
		},
	}

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not on PATH: extract-memories-on-close's CommandExists(\"bd\") gate cannot be exercised")
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			loader := NewLoader(filepath.Dir(tc.yamlPath), nil)
			proc, err := loader.LoadFile(tc.yamlPath)
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", tc.yamlPath, err)
			}

			workingDir := t.TempDir()
			if tc.setupDir != nil {
				tc.setupDir(t, workingDir)
			}

			if got := evaluateEnabledWhen(proc, tc.buildInput(workingDir, false), nil); !got {
				t.Errorf("router disabled: evaluateEnabledWhen = %v, want true (processor's own preconditions are satisfied)", got)
			}
			if got := evaluateEnabledWhen(proc, tc.buildInput(workingDir, true), nil); got {
				t.Errorf("router enabled: evaluateEnabledWhen = %v, want false (suppressed by !Prompts.IsEnabled(\"knowledge-router\"))", got)
			}
		})
	}
}

// TestKnowledgeRouter_LegacySuppression_ApplyOnCloseRecordsAllSkipped wires
// the four real legacy YAMLs into one Manager (mirroring production
// composition) and confirms ApplyOnClose's own recorded ProcessorRun
// Outcome/SkipReason for each — the AC's literal
// "ProcessorRunData.Outcome==skipped" assertion — when the router is
// enabled via PromptsSnapshotFn.
func TestKnowledgeRouter_LegacySuppression_ApplyOnCloseRecordsAllSkipped(t *testing.T) {
	loader := NewLoader("../../config/processors/builtin", nil)
	names := []string{"memorize-preferences", "extract-memories-on-close", "auggie-update-rules", "claude-update-memory"}
	var procs []*Processor
	for _, name := range names {
		proc, err := loader.LoadFile("../../config/processors/builtin/" + name + ".yaml")
		if err != nil {
			t.Fatalf("LoadFile(%s): %v", name, err)
		}
		procs = append(procs, proc)
	}

	const sessionID = "sess-ac8-suppression"
	store := newCloseRouterApplyTestStore(t, sessionID)
	m := NewManager("", nil)
	m.processors = procs

	var mu sync.Mutex
	runsByName := make(map[string]ProcessorRun, len(names))
	m.SetRunRecorder(func(run ProcessorRun) {
		mu.Lock()
		runsByName[run.Name] = run
		mu.Unlock()
	})

	m.ApplyOnClose(context.Background(), CloseProcessorInput{
		SessionID:         sessionID,
		SessionStore:      store,
		ArchiveReason:     "manual",
		PromptsSnapshotFn: routerSnapshotFn(true),
	})

	mu.Lock()
	defer mu.Unlock()
	if len(runsByName) != len(names) {
		t.Fatalf("runsByName = %+v, want one recorded run per legacy processor", runsByName)
	}
	for _, name := range names {
		run, ok := runsByName[name]
		if !ok {
			t.Errorf("no run recorded for %q", name)
			continue
		}
		if run.Outcome != "skipped" || run.SkipReason != string(SkipReasonEnabledWhen) {
			t.Errorf("%s: run = %+v, want Outcome=skipped SkipReason=%s", name, run, SkipReasonEnabledWhen)
		}
	}
}

// TestCurateMemoriesOnClose_NotGatedByKnowledgeRouter is a static check
// (deliberately not a full ApplyOnClose run, which would need a live `bd`
// store) that curate-memories-on-close.yaml — explicitly out of scope per
// mitto-3od's acceptance criteria — carries no knowledge-router suppression
// clause and so is unaffected by enabling the router.
func TestCurateMemoriesOnClose_NotGatedByKnowledgeRouter(t *testing.T) {
	const path = "../../config/processors/builtin/curate-memories-on-close.yaml"
	loader := NewLoader(filepath.Dir(path), nil)
	proc, err := loader.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(%s): %v", path, err)
	}
	if strings.Contains(proc.EnabledWhen, "knowledge-router") {
		t.Errorf("curate-memories-on-close.EnabledWhen = %q, want no knowledge-router gate (out of scope per AC)", proc.EnabledWhen)
	}
}
