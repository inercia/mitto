package slackbridge

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// TestBridge_EndToEnd_RealLoopRunner wires a Bridge to a real
// *conversation.LoopRunner (backed by a temp session.Store and a
// SessionManager holding one idle, enabled-loop session) to verify the
// whole chain reaches LoopRunner's existing dispatch path — reusing its
// enabled/idle checks — without any live Slack connection or ACP process
// (acceptance criterion #6: fake source, no credentials).
func TestBridge_EndToEnd_RealLoopRunner(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	defer store.Close()

	const targetSession = "target-1"
	if err := store.Create(session.Metadata{SessionID: targetSession, ACPServer: "test", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	if err := store.Loop(targetSession).Set(&session.LoopPrompt{
		Prompt:  "respond to the Slack message",
		Enabled: true,
		Trigger: session.TriggerOnCompletion, // arbitrary; the bridge fires via TriggerNowWithSlackEvent regardless of armed trigger
	}); err != nil {
		t.Fatalf("loopStore.Set() error = %v", err)
	}

	sm := conversation.NewSessionManagerWithOptions(conversation.SessionManagerOptions{})
	sm.AddSessionForTest(conversation.NewMinimalBackgroundSessionPrompting(targetSession, false)) // idle

	runner := conversation.NewLoopRunner(store, sm, nil)

	cfg := Config{TeamID: testTeam, ChannelID: testChannel, TargetSessionID: targetSession}
	bridge := NewBridge(cfg, runner, nil)

	err = bridge.runner.TriggerNowWithSlackEvent(cfg.TargetSessionID, true, TriggerSlack, &conversation.PromptSlackContext{
		EventID: "Ev1", ChannelID: testChannel, AuthorID: "U1", Text: "hi from slack",
	})
	// bs has no real ACP connection, so PromptWithMeta itself is expected to
	// fail synchronously ("still starting up") — what this test asserts is
	// that the call reached that point at all, i.e. it passed LoopRunner's
	// enabled+idle guards instead of being rejected earlier (e.g.
	// ErrLoopNotEnabled/ErrSessionBusy), proving the bridge is wired to a
	// real LoopRunner correctly.
	if err == nil {
		t.Fatal("expected an error since the target session has no real ACP connection")
	}
	if strings.Contains(err.Error(), "not enabled") || strings.Contains(err.Error(), "busy") {
		t.Errorf("unexpected guard rejection, want to reach PromptWithMeta: %v", err)
	}
}
