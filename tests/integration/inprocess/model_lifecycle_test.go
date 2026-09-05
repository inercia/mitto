//go:build integration

package inprocess

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/web"
	"github.com/inercia/mitto/pkg/api"
)

// Drive the real manager/transport against the mock ACP. The two conversations
// share one process, but never share a current model or a prompt override.
func TestConversationModelLifecycle(t *testing.T) {
	const initial, manual = "claude-opus-4-6", "claude-haiku-4-5"
	orderFile := filepath.Join(t.TempDir(), "rpc-order.log")
	t.Setenv("MOCK_RPC_ORDER_FILE", orderFile)
	ts := SetupTestServer(t, func(c *web.Config) {
		c.Workspaces[0].InitialModelProfile = "Claude Opus"
		// A legacy server default must only seed a new conversation, never
		// override an explicit initial selection or a later manual choice.
		c.MittoConfig.ACPServers[0].Constraints = map[string]*config.ACPServerConstraint{
			"model": {Pattern: "Sonnet", MatchMode: "contains"},
		}
	})
	sm := ts.Server.GetSessionManager()
	runPrompt := func(bs *conversation.BackgroundSession, meta conversation.PromptMeta) {
		t.Helper()
		done := make(chan error, 1)
		meta.OnComplete = func(err error) { done <- err }
		if err := bs.PromptWithMeta("hello", meta); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("turn did not finish")
		}
	}
	create := func(name string) (string, *conversation.BackgroundSession) {
		t.Helper()
		sess, err := ts.Client.CreateSession(api.CreateSessionRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		var bs *conversation.BackgroundSession
		waitFor(t, 15*time.Second, func() bool {
			bs = sm.GetSession(sess.SessionID)
			return bs != nil
		}, "conversation created")
		// session/new is deliberately deferred until the first prompt.
		runPrompt(bs, conversation.PromptMeta{})
		waitFor(t, 15*time.Second, func() bool {
			bs = sm.GetSession(sess.SessionID)
			return bs != nil && bs.AgentModels() != nil && bs.AgentModels().CurrentModelId == initial
		}, "initial model applied through ACP")
		return sess.SessionID, bs
	}
	assertModel := func(id string, bs *conversation.BackgroundSession, want string) {
		t.Helper()
		if bs.GetBaselineModel() != want || bs.GetConfigValue("model") != want || bs.AgentModels().CurrentModelId != want {
			t.Fatalf("%s: baseline=%q UI=%q agent=%q, want %q", id,
				bs.GetBaselineModel(), bs.GetConfigValue("model"), bs.AgentModels().CurrentModelId, want)
		}
		meta, err := ts.Store.GetMetadata(id)
		if err != nil || meta.BaselineModel != want {
			t.Fatalf("%s: persisted baseline=%q, want %q (error %v)", id, meta.BaselineModel, want, err)
		}
	}

	idA, a := create("model-A")
	idB, b := create("model-B")
	if err := a.SetConfigOption(context.Background(), "model", manual); err != nil {
		t.Fatal(err)
	}
	assertModel(idA, a, manual)
	assertModel(idB, b, initial)

	runPrompt(a, conversation.PromptMeta{
		PreferredModels: []config.PromptPreferredModel{{ModelName: "Claude Sonnet 4"}},
	})
	assertModel(idA, a, manual)
	assertModel(idB, b, initial)
	runPrompt(b, conversation.PromptMeta{})

	// Simulate suspension and recreation of A while B remains on its own model.
	meta, err := ts.Store.GetMetadata(idA)
	if err != nil {
		t.Fatal(err)
	}
	sm.CloseSession(idA, "model lifecycle test")
	a, err = sm.ResumeSession(idA, meta.Name, meta.WorkingDir)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool {
		return a.AgentModels() != nil && a.AgentModels().CurrentModelId == manual
	}, "conversation model restored after resume")
	assertModel(idA, a, manual)
	assertModel(idB, b, initial)
	runPrompt(a, conversation.PromptMeta{})
	// Verify agent-side state at each prompt, not just Mitto's local snapshot.
	for acpID, want := range map[string][]string{
		a.GetACPID(): {initial, "claude-sonnet-4-6", manual},
		b.GetACPID(): {initial, initial},
	} {
		var got []string
		for _, line := range readRPCOrder(t, orderFile) {
			if model, ok := strings.CutPrefix(line, "prompt_model\t"+acpID+"\t"); ok {
				got = append(got, model)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ACP session %s ran with models %v, want %v", acpID, got, want)
		}
	}
}
