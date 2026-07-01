//go:build integration

package inprocess

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/client"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/web"
)

// setupDeferredConfigServer creates a test server whose mock ACP process records the
// arrival order of prompt/set_model/set_mode RPCs to a temp file (MOCK_RPC_ORDER_FILE).
// Queue title auto-generation is disabled so the auxiliary session does not emit extra
// prompt entries that would pollute the order file.
func setupDeferredConfigServer(t *testing.T) (*TestServer, string) {
	t.Helper()
	orderFile := filepath.Join(t.TempDir(), "rpc-order.log")
	t.Setenv("MOCK_RPC_ORDER_FILE", orderFile)
	ts := SetupTestServer(t, func(c *web.Config) {
		disable := false
		if c.MittoConfig != nil {
			c.MittoConfig.Conversations = &config.ConversationsConfig{
				Queue: &config.QueueConfig{AutoGenerateTitles: &disable},
			}
		}
	})
	return ts, orderFile
}

// readRPCOrder returns the non-empty lines ("<method>\t<detail>") of the order file.
func readRPCOrder(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read rpc order file: %v", err)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// assertDeferredOrder verifies from the mock RPC-order file that: the slow-turn prompt
// was recorded first; then EXACTLY ONE config RPC (method) carrying wantValue (the
// last-write-wins value); the supersededValue was NEVER sent to the agent; and finally
// the queued follow-up prompt, strictly AFTER the config RPC.
func assertDeferredOrder(t *testing.T, path, method, wantValue, supersededValue string) {
	t.Helper()
	lines := readRPCOrder(t, path)
	idxSlow, idxCfg, idxQueued, cfgCount := -1, -1, -1, 0
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, "slow response"):
			if idxSlow == -1 {
				idxSlow = i
			}
		case strings.HasPrefix(ln, "prompt\t") && strings.Contains(ln, "QUEUED_FOLLOWUP"):
			if idxQueued == -1 {
				idxQueued = i
			}
		case strings.HasPrefix(ln, method+"\t"):
			cfgCount++
			if strings.Contains(ln, supersededValue) {
				t.Fatalf("superseded %s value reached the agent: %q (last-write-wins violated); lines=%v", method, ln, lines)
			}
			if strings.Contains(ln, wantValue) && idxCfg == -1 {
				idxCfg = i
			}
		}
	}
	if idxSlow == -1 {
		t.Fatalf("slow-turn prompt not recorded; lines=%v", lines)
	}
	if idxCfg == -1 {
		t.Fatalf("expected %s=%s not recorded; lines=%v", method, wantValue, lines)
	}
	if idxQueued == -1 {
		t.Fatalf("queued follow-up prompt not recorded; lines=%v", lines)
	}
	if cfgCount != 1 {
		t.Fatalf("expected exactly one %s RPC, got %d; lines=%v", method, cfgCount, lines)
	}
	if !(idxSlow < idxCfg && idxCfg < idxQueued) {
		t.Fatalf("ordering wrong: slow=%d %s=%d queued=%d; lines=%v", idxSlow, method, idxCfg, idxQueued, lines)
	}
}

// deferAndAssertMidTurn waits for the slow turn to start, defers two changes to configID
// (supersededValue then wantValue), and asserts the optimistic local state, that the turn
// was not cancelled, and that no RPC for this method was issued mid-turn. It returns bs.
func deferAndAssertMidTurn(t *testing.T, ts *TestServer, orderFile, sessionID, configID, method, supersededValue, wantValue string) *conversation.BackgroundSession {
	t.Helper()
	sm := ts.Server.GetSessionManager()
	var bs *conversation.BackgroundSession
	waitFor(t, 10*time.Second, func() bool {
		bs = sm.GetSession(sessionID)
		return bs != nil && bs.IsPrompting()
	}, "agent prompting (slow turn)")

	cfgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := bs.SetConfigOption(cfgCtx, configID, supersededValue); err != nil {
		t.Fatalf("SetConfigOption %s=%s: %v", configID, supersededValue, err)
	}
	if err := bs.SetConfigOption(cfgCtx, configID, wantValue); err != nil {
		t.Fatalf("SetConfigOption %s=%s: %v", configID, wantValue, err)
	}

	if got := bs.GetConfigValue(configID); got != wantValue {
		t.Fatalf("optimistic %s value = %q, want %q", configID, got, wantValue)
	}
	if !bs.IsPrompting() {
		t.Fatalf("turn was ended/cancelled by a deferred %s change", configID)
	}
	for _, ln := range readRPCOrder(t, orderFile) {
		if strings.HasPrefix(ln, method+"\t") {
			t.Fatalf("%s RPC issued mid-turn (should be deferred): %q", method, ln)
		}
	}
	return bs
}

// runDeferredConfigTest drives the shared deferred-config scenario: start a slow turn,
// defer two config changes mid-turn (last-write-wins), enqueue a follow-up while still
// prompting, then verify the deferred RPC is flushed before the queued prompt and that
// the agent ends up on the last-write-wins value. confirm asserts the agent-applied value.
func runDeferredConfigTest(t *testing.T, configID, method, supersededValue, wantValue string, confirm func(t *testing.T, bs *conversation.BackgroundSession)) {
	ts, orderFile := setupDeferredConfigServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{Name: "deferred-" + configID})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	var mu sync.Mutex
	var completes int
	var errs []string
	cb := client.SessionCallbacks{
		OnPromptComplete: func(int) { mu.Lock(); completes++; mu.Unlock() },
		OnError:          func(m string) { mu.Lock(); errs = append(errs, m); mu.Unlock() },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ws, err := ts.Client.Connect(ctx, sess.SessionID, cb)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer ws.Close()
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("Simulate a slow response"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	bs := deferAndAssertMidTurn(t, ts, orderFile, sess.SessionID, configID, method, supersededValue, wantValue)

	// Enqueue a follow-up while the slow turn is still running so it is dispatched on
	// the prompting→idle transition, AFTER the deferred config is flushed.
	if !bs.IsPrompting() {
		t.Fatalf("agent stopped prompting before the follow-up could be enqueued")
	}
	if _, err := ts.Client.AddToQueue(sess.SessionID, "QUEUED_FOLLOWUP marker"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}

	// Wait for both the slow turn and the queued turn to complete.
	waitFor(t, 30*time.Second, func() bool { mu.Lock(); defer mu.Unlock(); return completes >= 2 }, "both turns complete")

	confirm(t, bs)

	mu.Lock()
	gotErrs := append([]string{}, errs...)
	mu.Unlock()
	if len(gotErrs) > 0 {
		t.Fatalf("unexpected errors during turns: %v", gotErrs)
	}

	assertDeferredOrder(t, orderFile, method, wantValue, supersededValue)
}

// TestDeferredModelConfig_FlushesBeforeQueuedPrompt verifies that a model change made
// while the agent is prompting is deferred (no mid-turn RPC, turn not cancelled),
// reflected optimistically, and flushed via set_model BEFORE the next queued prompt —
// applying only the last-write-wins value.
func TestDeferredModelConfig_FlushesBeforeQueuedPrompt(t *testing.T) {
	runDeferredConfigTest(t, "model", "set_model", "claude-opus-4-6", "claude-haiku-4-5",
		func(t *testing.T, bs *conversation.BackgroundSession) {
			waitFor(t, 10*time.Second, func() bool {
				am := bs.AgentModels()
				return am != nil && string(am.CurrentModelId) == "claude-haiku-4-5"
			}, "agent-confirmed model claude-haiku-4-5")
		})
}

// TestDeferredModeConfig_FlushesBeforeQueuedPrompt is the mode-change counterpart of
// TestDeferredModelConfig_FlushesBeforeQueuedPrompt (legacy set_mode API).
func TestDeferredModeConfig_FlushesBeforeQueuedPrompt(t *testing.T) {
	runDeferredConfigTest(t, "mode", "set_mode", "ask", "architect",
		func(t *testing.T, bs *conversation.BackgroundSession) {
			waitFor(t, 10*time.Second, func() bool {
				return bs.GetConfigValue("mode") == "architect"
			}, "agent-confirmed mode architect")
		})
}

// setupModelTagsServer builds a test server whose MittoConfig declares model profiles, so
// the Model(tag) template func / Session.HasModelTag CEL macro can resolve the current
// model's tags. The mock ACP's default current model is "Sonnet 4.6" (claude-sonnet-4-6).
func setupModelTagsServer(t *testing.T, profiles []config.ModelProfile) (*TestServer, string) {
	t.Helper()
	orderFile := filepath.Join(t.TempDir(), "rpc-order.log")
	t.Setenv("MOCK_RPC_ORDER_FILE", orderFile)
	ts := SetupTestServer(t, func(c *web.Config) {
		if c.MittoConfig != nil {
			disable := false
			c.MittoConfig.Conversations = &config.ConversationsConfig{
				Queue: &config.QueueConfig{AutoGenerateTitles: &disable},
			}
			c.MittoConfig.Models = profiles
		}
	})
	return ts, orderFile
}

// TestTemplateRender_ModelTag verifies that {{ if Model "tag" }} resolves against the
// session's CURRENT model tags (mitto-i5sr): the matching tag renders its branch while a
// non-matching tag falls through to the else branch (no error). The mock's current model is
// "Sonnet 4.6", tagged "smart" here; "expensive" is reserved for Opus, so it must NOT match.
func TestTemplateRender_ModelTag(t *testing.T) {
	ts, orderFile := setupModelTagsServer(t, []config.ModelProfile{
		{Name: "Sonnet", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Sonnet"}, Tags: []string{"smart"}},
		{Name: "Opus", Criteria: &config.ACPServerConstraint{MatchMode: "contains", Pattern: "Opus"}, Tags: []string{"expensive"}},
	})
	writeTemplatePrompt(t, ts, "tmpl-modeltag", "tmpl-modeltag",
		`MT:{{ if Model "smart" }}SMART{{ else }}NOTSMART{{ end }}/{{ if Model "expensive" }}EXP{{ else }}NOTEXP{{ end }}`)

	lines := runTemplatePromptAndWait(t, ts, orderFile, "tmpl-modeltag", nil)

	rendered := promptLineFor(lines, "MT:")
	if rendered == "" {
		t.Fatalf("expected MT: line in RPC order; got lines: %v", lines)
	}
	if !strings.Contains(rendered, "MT:SMART/NOTEXP") {
		t.Errorf("model-tag branches wrong: got %q, want it to contain %q", rendered, "MT:SMART/NOTEXP")
	}
	if strings.Contains(rendered, "{{") {
		t.Errorf("literal {{ remains in rendered prompt: %q", rendered)
	}
	t.Logf("rendered: %q", rendered)
}
