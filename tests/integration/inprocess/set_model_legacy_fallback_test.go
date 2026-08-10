//go:build integration

package inprocess

import (
	"context"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/pkg/api"
)

// TestSetSessionModel_LegacyFallback_PreSchema013 is the mitto-vd5 regression:
// SharedACPProcess.SetSessionModel must fall back to session/set_config_option
// when the agent returns -32601 Method not found on the new session/set_model
// RPC. Without the fallback, pre-0.13-schema agents (older Auggie / Claude
// Code) would fail every model switch and applyConfigConstraints would drop
// out with skip_no_agent_models / set_model_failed.
//
// Verified end-to-end via the mock ACP server's MOCK_SET_MODEL_FORCE_LEGACY=1
// mode, which returns -32601 unconditionally on session/set_model, so the
// only path to a successful convergence is Mitto's legacy fallback delivering
// the model via session/set_config_option.
//
// The assertion is on AgentModels().CurrentModelId (agent-confirmed model),
// NOT on GetConfigValue("model") — the optimistic pre-apply would mask a
// broken fallback path if we asserted on the local config. Only the
// AgentModels update after a successful RPC accept proves the fallback wire
// actually reached and was accepted by the mock agent.
func TestSetSessionModel_LegacyFallback_PreSchema013(t *testing.T) {
	const expectedModelID = "claude-opus-4-6"

	ts := setupTestServerWithModelConstraintAndEnv(t, "Opus",
		map[string]string{"MOCK_SET_MODEL_FORCE_LEGACY": "1"})

	sess, err := ts.Client.CreateSession(api.CreateSessionRequest{
		Name: "set-model-legacy-fallback",
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	t.Cleanup(func() { ts.Client.DeleteSession(sess.SessionID) })

	var promptComplete bool
	callbacks := api.SessionCallbacks{
		OnPromptComplete: func(_ int) { promptComplete = true },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sess.SessionID, callbacks)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	if err := ws.LoadEvents(50, 0, 0); err != nil {
		t.Fatalf("LoadEvents failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt failed: %v", err)
	}

	// Wait for the prompt to complete (triggers deferred session/new +
	// applyConfigConstraints, which is what fires SetSessionModel and takes
	// the -32601 -> legacy fallback branch).
	waitFor(t, 30*time.Second, func() bool { return promptComplete },
		"prompt complete")

	sm := ts.Server.GetSessionManager()
	var bs *conversation.BackgroundSession
	waitFor(t, 5*time.Second, func() bool {
		bs = sm.GetSession(sess.SessionID)
		return bs != nil
	}, "session materialised")

	// The AGENT-CONFIRMED model — updated only after a successful
	// SetSessionModel RPC — must converge to the constrained value. Since
	// session/set_model is force-returning -32601 in the mock, the ONLY way
	// this can succeed is Mitto taking the legacy-fallback branch inside
	// SharedACPProcess.SetSessionModel and sending session/set_config_option.
	waitFor(t, 15*time.Second, func() bool {
		am := bs.AgentModels()
		return am != nil && string(am.CurrentModelId) == expectedModelID
	}, "agent-confirmed model converged via legacy fallback")

	am := bs.AgentModels()
	got := ""
	if am != nil {
		got = string(am.CurrentModelId)
	}
	if got != expectedModelID {
		t.Fatalf("agent-confirmed model = %q, want %q (legacy fallback did not deliver)",
			got, expectedModelID)
	}
	t.Logf("legacy-fallback path delivered model %q via session/set_config_option", got)
}
