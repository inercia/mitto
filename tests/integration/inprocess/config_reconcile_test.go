//go:build integration

package inprocess

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

func setupConfigReconcileServer(t *testing.T) (*TestServer, string) {
	t.Helper()
	orderFile := filepath.Join(t.TempDir(), "rpc-order.log")
	t.Setenv("MOCK_RPC_ORDER_FILE", orderFile)
	ts := SetupTestServer(t, func(c *web.Config) {
		disable := false
		c.MittoConfig.Conversations = &config.ConversationsConfig{
			Queue: &config.QueueConfig{AutoGenerateTitles: &disable},
		}
		c.MittoConfig.Models = []config.ModelProfile{
			{Name: "Baseline Sonnet", Criteria: &config.ACPServerConstraint{MatchMode: "exact", Pattern: "claude-sonnet-4-6"}},
			{Name: "Temporary Haiku", Criteria: &config.ACPServerConstraint{MatchMode: "exact", Pattern: "claude-haiku-4-5"}},
		}
	})
	return ts, orderFile
}

func connectConfigAdjustmentEvents(t *testing.T, ts *TestServer) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.HTTPServer.URL, "http") + "/mitto/api/events"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		if response != nil {
			t.Fatalf("connect global events (HTTP %d): %v", response.StatusCode, err)
		}
		t.Fatalf("connect global events: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var connected struct {
		Type string `json:"type"`
	}
	if err := conn.ReadJSON(&connected); err != nil {
		t.Fatalf("read initial global event: %v", err)
	}
	if connected.Type != "connected" {
		t.Fatalf("initial global event type = %q, want connected", connected.Type)
	}
	return conn
}

func waitConfigAdjustment(t *testing.T, conn *websocket.Conn, sessionID, configID, reason, requested, applied string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set global event deadline: %v", err)
		}
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatalf("read global event waiting for %s/%s: %v", reason, configID, err)
		}
		if event.Type != conversation.WSMsgTypeConfigOptionChanged {
			continue
		}
		var data struct {
			SessionID   string                          `json:"session_id"`
			Adjustments []conversation.ConfigAdjustment `json:"adjustments"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("decode config_option_changed: %v", err)
		}
		if data.SessionID != sessionID {
			continue
		}
		for _, adjustment := range data.Adjustments {
			if adjustment.ConfigID == configID &&
				adjustment.Reason == reason &&
				adjustment.RequestedValue == requested &&
				adjustment.AppliedValue == applied {
				return
			}
		}
	}
	t.Fatalf("no %s adjustment for %s=%q -> %q", reason, configID, requested, applied)
}

func waitConfigSnapshotWithoutEffort(t *testing.T, conn *websocket.Conn, sessionID, modelID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set global event deadline: %v", err)
		}
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatalf("read global event waiting for model snapshot %s: %v", modelID, err)
		}
		if event.Type != conversation.WSMsgTypeConfigOptionChanged {
			continue
		}
		var data struct {
			SessionID     string `json:"session_id"`
			ConfigOptions []struct {
				ID           string `json:"id"`
				Category     string `json:"category"`
				CurrentValue string `json:"current_value"`
			} `json:"config_options"`
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("decode config_option_changed: %v", err)
		}
		if data.SessionID != sessionID {
			continue
		}
		gotModel := ""
		hasEffort := false
		for _, option := range data.ConfigOptions {
			if option.Category == conversation.ConfigOptionCategoryModel {
				gotModel = option.CurrentValue
			}
			if option.Category == "thought_level" {
				hasEffort = true
			}
		}
		if gotModel == modelID && !hasEffort {
			return
		}
	}
	t.Fatalf("no full snapshot for model %s without an effort option", modelID)
}

func waitModel(t *testing.T, e *effortSession, model string) {
	t.Helper()
	waitFor(t, 10*time.Second, func() bool {
		models := e.bs.AgentModels()
		return models != nil && string(models.CurrentModelId) == model
	}, "agent model "+model)
}

func TestConfigReconcile_RemovedOptionDropsDeferredValueBeforeQueuedPrompt(t *testing.T) {
	ts, orderFile := setupConfigReconcileServer(t)
	events := connectConfigAdjustmentEvents(t, ts)
	e := newEffortSession(t, ts, orderFile, "reconcile-removed-pending")
	effortID := e.effort().ID

	if err := e.ws.SendPrompt("Simulate a slow response"); err != nil {
		t.Fatalf("SendPrompt slow: %v", err)
	}
	waitFor(t, 10*time.Second, e.bs.IsPrompting, "slow turn prompting")
	e.set(t, string(conversation.ModelConfigId), "claude-haiku-4-5")
	e.set(t, effortID, "deep")
	if !e.bs.IsPrompting() {
		t.Fatal("slow turn ended before deferred config could be staged")
	}
	if _, err := ts.Client.AddToQueue(e.id, "QUEUED_FOLLOWUP reconciliation"); err != nil {
		t.Fatalf("AddToQueue: %v", err)
	}
	waitFor(t, 30*time.Second, func() bool { return e.completed() >= 3 }, "slow and queued turns")
	waitModel(t, e, "claude-haiku-4-5")
	waitFor(t, 10*time.Second, func() bool { return e.effort() == nil }, "effort option removed")
	waitConfigAdjustment(t, events, e.id, effortID, conversation.ConfigAdjustmentOptionRemoved, "deep", "")

	lines := readRPCOrder(t, orderFile)
	modelSet := indexOfLine(lines, "set_model\tclaude-haiku-4-5", 0)
	queuedPrompt := indexOfLine(lines, "prompt\tQUEUED_FOLLOWUP reconciliation", 0)
	if modelSet < 0 || queuedPrompt < 0 || modelSet > queuedPrompt {
		t.Fatalf("want model transition before queued prompt; lines=%q", lines)
	}
	for _, line := range lines[modelSet+1 : queuedPrompt] {
		if line == effortSetLine(effortID, "deep") {
			t.Fatalf("unsupported effort setter reached agent before queued prompt: %q", lines)
		}
	}
}

func TestConfigReconcile_SupportedTransitionsValidatePreferencesAndNotify(t *testing.T) {
	ts, orderFile := setupConfigReconcileServer(t)
	events := connectConfigAdjustmentEvents(t, ts)
	e := newEffortSession(t, ts, orderFile, "reconcile-supported")
	effortID := e.effort().ID

	e.set(t, effortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "deep" }, "explicit deep selection")
	e.set(t, string(conversation.ModelConfigId), "claude-opus-4-6")
	waitModel(t, e, "claude-opus-4-6")
	waitFor(t, 10*time.Second, func() bool {
		option := e.effort()
		return option != nil && option.CurrentValue == "deep" && hasValue(e.effortValues(), "deep")
	}, "valid explicit preference retained")

	e.set(t, string(conversation.ModelConfigId), "claude-sonnet-4-6")
	waitModel(t, e, "claude-sonnet-4-6")
	e.set(t, effortID, "shallow")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "shallow" }, "explicit shallow selection")

	beforeInvalidTransition := len(readRPCOrder(t, orderFile))
	e.set(t, string(conversation.ModelConfigId), "claude-opus-4-6")
	waitModel(t, e, "claude-opus-4-6")
	waitFor(t, 10*time.Second, func() bool {
		option := e.effort()
		return option != nil && option.CurrentValue == "standard"
	}, "invalid preference replaced by agent current value")
	waitConfigAdjustment(t, events, e.id, effortID, conversation.ConfigAdjustmentValueNotAdvertised, "shallow", "standard")

	lines := readRPCOrder(t, orderFile)
	for _, line := range lines[beforeInvalidTransition:] {
		if line == effortSetLine(effortID, "shallow") {
			t.Fatalf("invalid inherited effort was sent after model change: lines=%q", lines)
		}
	}
}

func TestConfigReconcile_TemporaryOverrideAndReverseTransitionRestoreOnlyValidBaseline(t *testing.T) {
	ts, orderFile := setupConfigReconcileServer(t)
	e := newEffortSession(t, ts, orderFile, "reconcile-temporary")
	effortID := e.effort().ID
	e.set(t, effortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "deep" }, "baseline effort deep")

	runPromptWithMeta(t, e, "temporary haiku override", conversation.PromptMeta{
		PreferredModels: []config.PromptPreferredModel{{ModelName: "Temporary Haiku"}},
	})
	waitFor(t, 10*time.Second, func() bool {
		models := e.bs.AgentModels()
		option := e.effort()
		return models != nil && string(models.CurrentModelId) == "claude-sonnet-4-6" &&
			option != nil && option.CurrentValue == "deep"
	}, "valid baseline restored after temporary override")
	if e.bs.GetBaselineModel() != "claude-sonnet-4-6" {
		t.Fatalf("temporary model override changed baseline to %q", e.bs.GetBaselineModel())
	}

	e.set(t, effortID, "shallow")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "shallow" }, "baseline effort shallow")
	beforeInvalidTransition := len(readRPCOrder(t, orderFile))
	e.set(t, string(conversation.ModelConfigId), "claude-opus-4-6")
	waitModel(t, e, "claude-opus-4-6")
	e.set(t, string(conversation.ModelConfigId), "claude-sonnet-4-6")
	waitModel(t, e, "claude-sonnet-4-6")
	waitFor(t, 10*time.Second, func() bool {
		option := e.effort()
		return option != nil && option.CurrentValue == "standard"
	}, "invalid baseline preference not restored")
	lines := readRPCOrder(t, orderFile)
	for _, line := range lines[beforeInvalidTransition:] {
		if line == effortSetLine(effortID, "shallow") {
			t.Fatalf("invalid baseline effort was sent after being rejected: %q", lines)
		}
	}
}

func TestConfigReconcile_RapidEmptyAndDuplicateSnapshotsDoNotRestoreStaleEffort(t *testing.T) {
	t.Setenv("MOCK_CONFIG_UPDATE_EMPTY_FIRST", "1")
	t.Setenv("MOCK_CONFIG_UPDATE_DELAY_MS", "100")
	t.Setenv("MOCK_CONFIG_UPDATE_DUPLICATES", "2")
	ts, orderFile := setupConfigReconcileServer(t)
	events := connectConfigAdjustmentEvents(t, ts)
	e := newEffortSession(t, ts, orderFile, "reconcile-late-snapshot")
	effortID := e.effort().ID
	e.set(t, effortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "deep" }, "effort deep")

	e.set(t, string(conversation.ModelConfigId), "claude-haiku-4-5")
	waitConfigAdjustment(t, events, e.id, effortID, conversation.ConfigAdjustmentOptionRemoved, "deep", "")
	if got := indexOfLine(readRPCOrder(t, orderFile), effortSetLine(effortID, "deep"), 0); got < 0 {
		t.Fatalf("expected only the initial supported-model selection; RPC order=%q", readRPCOrder(t, orderFile))
	}

	e.set(t, string(conversation.ModelConfigId), "claude-sonnet-4-6")
	waitModel(t, e, "claude-sonnet-4-6")
	waitFor(t, 10*time.Second, func() bool {
		option := e.effort()
		return option != nil && option.CurrentValue == "standard"
	}, "effort option restored from final model snapshot")
	time.Sleep(200 * time.Millisecond)
	e.prompt(t, "PROMPT_AFTER_EMPTY_CONFIG_SNAPSHOT")
	if option := e.effort(); option == nil || option.CurrentValue != "standard" {
		t.Fatalf("late snapshots changed final supported-model state: %+v", option)
	}
	for _, line := range readRPCOrder(t, orderFile) {
		if strings.HasPrefix(line, "set_config_option\t"+effortID+"=") &&
			!strings.HasSuffix(line, "=deep") {
			t.Fatalf("unexpected effort setter around empty/late/duplicate snapshots: %q", line)
		}
	}
}

func TestConfigReconcile_MissingSnapshotTimesOutWithoutSendingStaleEffort(t *testing.T) {
	t.Setenv("MOCK_CONFIG_UPDATE_DELAY_MS", "6500")
	t.Setenv("MOCK_CONFIG_UPDATE_DUPLICATES", "2")
	ts, orderFile := setupConfigReconcileServer(t)
	events := connectConfigAdjustmentEvents(t, ts)
	e := newEffortSession(t, ts, orderFile, "reconcile-missing-snapshot")
	effortID := e.effort().ID
	e.set(t, effortID, "deep")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "deep" }, "effort deep")

	e.set(t, string(conversation.ModelConfigId), "claude-haiku-4-5")
	waitConfigAdjustment(t, events, e.id, effortID, conversation.ConfigAdjustmentSnapshotUnconfirmed, "deep", "deep")
	e.prompt(t, "PROMPT_BEFORE_LATE_CONFIG_SNAPSHOT")
	waitConfigSnapshotWithoutEffort(t, events, e.id, "claude-haiku-4-5")
	for _, line := range readRPCOrder(t, orderFile) {
		if strings.HasPrefix(line, "set_config_option\t"+effortID+"=") &&
			!strings.HasSuffix(line, "=deep") {
			t.Fatalf("stale effort setter reached the agent: %q", line)
		}
	}
}

func TestConfigReconcile_AlternateOpaqueOptionVocabulary(t *testing.T) {
	t.Setenv("MOCK_EFFORT_VARIANT", "copilot")
	ts, orderFile := setupConfigReconcileServer(t)
	events := connectConfigAdjustmentEvents(t, ts)
	e := newEffortSession(t, ts, orderFile, "reconcile-alternate-vocabulary")
	option := e.effort()
	if option == nil || option.ID != "reasoning_effort" {
		t.Fatalf("alternate effort option = %+v, want reasoning_effort", option)
	}

	e.set(t, option.ID, "xhigh")
	waitFor(t, 10*time.Second, func() bool { return e.effort().CurrentValue == "xhigh" }, "alternate xhigh selection")
	beforeInvalidTransition := len(readRPCOrder(t, orderFile))
	e.set(t, string(conversation.ModelConfigId), "claude-opus-4-6")
	waitModel(t, e, "claude-opus-4-6")
	waitFor(t, 10*time.Second, func() bool {
		current := e.effort()
		return current != nil && current.CurrentValue == "medium"
	}, "alternate agent current value")
	waitConfigAdjustment(t, events, e.id, "reasoning_effort", conversation.ConfigAdjustmentValueNotAdvertised, "xhigh", "medium")
	for _, line := range readRPCOrder(t, orderFile)[beforeInvalidTransition:] {
		if line == "set_config_option\treasoning_effort=xhigh" {
			t.Fatalf("alternate invalid value was re-sent: %q", line)
		}
	}
}

func TestConfigReconcile_LegacyAgentWithoutConfigOptionsStillPrompts(t *testing.T) {
	t.Setenv("MOCK_NO_CONFIG_OPTIONS", "1")
	ts, orderFile := setupConfigReconcileServer(t)
	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: "legacy-no-config-options"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })
	completed := make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	ws, err := ts.Client.Connect(ctx, sess.SessionID, api.SessionCallbacks{
		OnPromptComplete: func(int) { completed <- struct{}{} },
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	if err := ws.SendPrompt("legacy agent without config options"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}
	select {
	case <-completed:
	case <-time.After(30 * time.Second):
		t.Fatal("legacy agent prompt did not complete")
	}
	bs := ts.Server.GetSessionManager().GetSession(sess.SessionID)
	if bs == nil {
		t.Fatal("legacy agent background session is missing")
	}
	for _, option := range bs.ConfigOptions() {
		if option.Category == "thought_level" {
			t.Fatalf("legacy agent unexpectedly advertised effort option: %+v", option)
		}
	}
	if indexOfLine(readRPCOrder(t, orderFile), "prompt\t", 0) < 0 {
		t.Fatalf("prompt did not reach legacy agent: %q", readRPCOrder(t, orderFile))
	}
}

func effortSetLine(configID, value string) string {
	return "set_config_option\t" + configID + "=" + value
}

func (e *effortSession) effortConfigID() string {
	if option := e.effort(); option != nil {
		return option.ID
	}
	return mockEffortID
}

func runPromptWithMeta(t *testing.T, e *effortSession, text string, meta conversation.PromptMeta) {
	t.Helper()
	completed := make(chan error, 1)
	meta.OnComplete = func(err error) { completed <- err }
	if err := e.bs.PromptWithMeta(text, meta); err != nil {
		t.Fatalf("PromptWithMeta: %v", err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("prompt failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("prompt %q did not complete", text)
	}
}
