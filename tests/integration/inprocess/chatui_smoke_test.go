//go:build integration

package inprocess

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/inercia/mitto/internal/chatui"
	client "github.com/inercia/mitto/pkg/api"
)

// modelSender adapts *chatui.Model to chatui.RunPump's programSender
// interface by calling Update synchronously for every message, mirroring
// what a real tea.Program's event loop does — just without a terminal.
// Guarded by mu since RunPump's goroutine and the test's assertions both
// touch the model.
type modelSender struct {
	mu    sync.Mutex
	model *chatui.Model
}

func (s *modelSender) Send(msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model.Update(msg)
}

func (s *modelSender) view() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model.View().Content
}

// TestChatUI_SmokeAgainstRealServer is Layer 4 of the mitto-pscc.12 test
// strategy: one thin end-to-end path exercising chatui.Model + chatui.RunPump
// against a real Mitto server (SetupTestServer) with the mock ACP agent
// behind it — connect, send a prompt, receive a streamed answer via the real
// production pump, and confirm it renders. Layers 1-3 already cover the
// Update()/render/pump logic in isolation; this only pins that the real
// wiring (Session.Connect, EventsChan, RunPump, Model.Update, View) works
// end to end. Deliberately not a PTY test (Plan decision: the chat TUI needs
// an alt-screen terminal CI does not have) — it drives the Model directly,
// the same way internal/cmd/conversation_chat.go wires RunPump into
// tea.NewProgram, minus the terminal program itself.
func TestChatUI_SmokeAgainstRealServer(t *testing.T) {
	ts := SetupTestServer(t)

	sess, err := ts.Client.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = ts.Client.DeleteSession(sess.SessionID) })

	model := chatui.NewModel(nil, chatui.Options{Title: "smoke-test", ShowThoughts: true})
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	sender := &modelSender{model: model}

	loaded := make(chan struct{})
	var loadedOnce sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ws, err := ts.Client.Connect(ctx, sess.SessionID, client.SessionCallbacks{
		OnEventsLoaded: func(events []client.SyncEvent, hasMore bool, isPrompting bool) {
			model.SeedHistory(events)
			loadedOnce.Do(func() { close(loaded) })
		},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	model.SetSession(ws)

	evCh, errCh, err := ws.EventsChan(ctx)
	if err != nil {
		t.Fatalf("EventsChan: %v", err)
	}

	if err := ws.LoadEvents(20, 0, 0); err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OnEventsLoaded")
	}

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		chatui.RunPump(ctx, evCh, errCh, sender)
	}()

	// SendPrompt directly (not via handleKey): Layer 1 already covers the
	// enter/submit key-routing logic in isolation, so this smoke test's job
	// is the Session -> pump -> Model wiring, not input handling.
	if err := ws.SendPrompt("hello"); err != nil {
		t.Fatalf("SendPrompt: %v", err)
	}

	waitFor(t, 15*time.Second, func() bool {
		return strings.Contains(sender.view(), "mock ACP agent")
	}, "agent reply rendered in the transcript")

	cancel()
	select {
	case <-pumpDone:
	case <-time.After(2 * time.Second):
		t.Fatal("RunPump did not exit after context cancellation")
	}
}
