package chatui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	client "github.com/inercia/mitto/pkg/api"
)

// newTestModel builds a Model with a nil session (never dereferenced by
// these tests: they assert on state mutated synchronously inside
// Update/handleKey/handleEvent, never by invoking the tea.Cmd closures that
// call Session methods) and a fixed size, mirroring the WindowSizeMsg every
// real run gets before any key or event is processed.
func newTestModel(t *testing.T, showThoughts bool) *Model {
	t.Helper()
	m := NewModel(nil, Options{Title: "test-conversation", ShowThoughts: showThoughts})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

// --- streamed agent message coalescing ------------------------------------

func TestModel_AgentMessage_SameSeqCoalesces_NewSeqAppends(t *testing.T) {
	m := newTestModel(t, true)

	m.Update(eventMsg{event: client.Event{Kind: client.EventAgentMessage, Seq: 1, Text: "Hel"}})
	m.Update(eventMsg{event: client.Event{Kind: client.EventAgentMessage, Seq: 1, Text: "Hello world"}})
	if got := len(m.transcript.items); got != 1 {
		t.Fatalf("after two chunks on the same seq, len(items) = %d, want 1", got)
	}
	if got := m.transcript.items[0].markdown; got != "Hello world" {
		t.Errorf("items[0].markdown = %q, want the latest accumulated chunk", got)
	}

	m.Update(eventMsg{event: client.Event{Kind: client.EventAgentMessage, Seq: 2, Text: "Second"}})
	if got := len(m.transcript.items); got != 2 {
		t.Fatalf("a new seq must append, len(items) = %d, want 2", got)
	}
}

// --- tool_call -> tool_update status transition ---------------------------

func TestModel_ToolCall_ThenUpdate_MutatesInPlaceByID(t *testing.T) {
	m := newTestModel(t, true)

	m.Update(eventMsg{event: client.Event{Kind: client.EventToolCall, ID: "t1", Title: "Read file", Status: "running"}})
	m.Update(eventMsg{event: client.Event{Kind: client.EventToolUpdate, ID: "t1", Status: "done"}})

	if got := len(m.transcript.items); got != 1 {
		t.Fatalf("tool_update on a known ID must mutate in place, len(items) = %d, want 1", got)
	}
	if got := m.transcript.items[0].status; got != "done" {
		t.Errorf("items[0].status = %q, want %q", got, "done")
	}
}

func TestModel_ToolUpdate_UnknownID_SynthesizesItem(t *testing.T) {
	m := newTestModel(t, true)

	m.Update(eventMsg{event: client.Event{Kind: client.EventToolUpdate, ID: "ghost", Status: "done"}})

	if got := len(m.transcript.items); got != 1 {
		t.Fatalf("an update for an unseen ID must not be silently dropped, len(items) = %d, want 1", got)
	}
	if got := m.transcript.items[0].toolID; got != "ghost" {
		t.Errorf("synthesized item.toolID = %q, want %q", got, "ghost")
	}
}

// --- thought suppression under --no-thoughts ------------------------------

func TestModel_AgentThought_SuppressedWhenShowThoughtsFalse(t *testing.T) {
	shown := newTestModel(t, true)
	shown.Update(eventMsg{event: client.Event{Kind: client.EventAgentThought, Text: "thinking..."}})
	if got := len(shown.transcript.items); got != 1 {
		t.Errorf("ShowThoughts=true: len(items) = %d, want 1", got)
	}

	hidden := newTestModel(t, false)
	hidden.Update(eventMsg{event: client.Event{Kind: client.EventAgentThought, Text: "thinking..."}})
	if got := len(hidden.transcript.items); got != 0 {
		t.Errorf("ShowThoughts=false: len(items) = %d, want 0 (dropped at append time)", got)
	}
}

// --- permission modal open/answer/close -----------------------------------

func TestModel_PermissionModal_QueueAdvancesOnAnswerThenCloses(t *testing.T) {
	m := newTestModel(t, true)
	m.Update(eventMsg{event: client.Event{Kind: client.EventPermission, RequestID: "p1", Title: "Run rm -rf"}})
	m.Update(eventMsg{event: client.Event{Kind: client.EventPermission, RequestID: "p2", Title: "Write file"}})
	if !m.perm.Open() {
		t.Fatal("modal should be open after two pushes")
	}

	_, cmd := m.Update(tea.KeyPressMsg{Text: "y", Code: 'y'})
	if cmd == nil {
		t.Error("answering 'y' should issue a Cmd (Session.AnswerPermission)")
	}
	if !m.perm.Open() {
		t.Fatal("a second queued request must still be shown after the first is answered")
	}
	if got := m.perm.current().RequestID; got != "p2" {
		t.Errorf("current().RequestID = %q, want %q (queue advanced)", got, "p2")
	}

	m.Update(tea.KeyPressMsg{Text: "n", Code: 'n'})
	if m.perm.Open() {
		t.Error("modal should close once every queued request is answered")
	}
}

// --- streamEndMsg quits the program ---------------------------------------

func TestModel_StreamEndMsg_QuitsAndRecordsDisconnect(t *testing.T) {
	m := newTestModel(t, true)
	boom := errors.New("connection reset")

	_, cmd := m.Update(streamEndMsg{err: boom})

	if !m.quitting {
		t.Error("streamEndMsg should set quitting = true")
	}
	if !errors.Is(m.QuitErr(), boom) {
		t.Errorf("QuitErr() = %v, want %v", m.QuitErr(), boom)
	}
	if m.status.disconnect != boom.Error() {
		t.Errorf("status.disconnect = %q, want %q", m.status.disconnect, boom.Error())
	}
	if cmd == nil {
		t.Fatal("expected a tea.Quit Cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd() = %T, want tea.QuitMsg", cmd())
	}
}

// --- key routing: quit keys, esc, enter -----------------------------------

func TestModel_HandleKey_QuitKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Text: "q", Code: 'q'}, {Code: 'c', Mod: tea.ModCtrl}} {
		m := newTestModel(t, true)
		_, cmd := m.Update(key)
		if !m.quitting {
			t.Errorf("key %q should set quitting = true", key.String())
		}
		if cmd == nil || func() bool { _, ok := cmd().(tea.QuitMsg); return !ok }() {
			t.Errorf("key %q should return a tea.Quit Cmd", key.String())
		}
	}
}

func TestModel_HandleKey_EscCancelsInFlightWithoutQuitting(t *testing.T) {
	m := newTestModel(t, true)
	m.inFlight = true
	m.status.SetInFlight(true)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.quitting {
		t.Error("esc must not quit the program")
	}
	if m.inFlight {
		t.Error("esc should clear inFlight synchronously (Plan: cancel is issued as a Cmd, but UI feedback is immediate)")
	}
	if cmd == nil {
		t.Error("esc should issue a Cmd (Session.Cancel)")
	}
}

func TestModel_HandleKey_EnterSubmitsAndClearsInput(t *testing.T) {
	m := newTestModel(t, true)
	m.input.SetValue("hello agent")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := len(m.transcript.items); got != 1 || m.transcript.items[0].kind != itemUser {
		t.Fatalf("enter with non-empty input should append a user item, items = %+v", m.transcript.items)
	}
	if got := m.input.Value(); got != "" {
		t.Errorf("input.Value() = %q, want empty after submit", got)
	}
	if cmd == nil {
		t.Error("submitting should issue a Cmd (Session.SendPrompt)")
	}
}

func TestModel_HandleKey_EnterOnEmptyInputIsNoop(t *testing.T) {
	m := newTestModel(t, true)

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := len(m.transcript.items); got != 0 {
		t.Errorf("enter on empty input must not append anything, len(items) = %d", got)
	}
	if cmd != nil {
		t.Error("enter on empty input should not issue a Cmd")
	}
}

// --- user_prompt echo dedup ------------------------------------------------

func TestModel_UserPrompt_FromAnotherClientIsAppended(t *testing.T) {
	m := newTestModel(t, true)

	// A nil session means clientID() is "", so no event can be attributed to
	// us — every user_prompt must render, which is the safe fallback.
	m.Update(eventMsg{event: client.Event{Kind: client.EventUserPrompt, SenderID: "other", Message: "from the web UI"}})

	if got := len(m.transcript.items); got != 1 {
		t.Fatalf("a prompt from another client must render, len(items) = %d, want 1", got)
	}
	if got := m.transcript.items[0].markdown; got != "from the web UI" {
		t.Errorf("items[0].markdown = %q, want the other client's message", got)
	}
}

func TestModel_UserPrompt_OwnEchoIsDropped(t *testing.T) {
	m := newTestModel(t, true)
	m.clientIDFn = func() string { return "me" }

	m.input.SetValue("hello agent")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := len(m.transcript.items); got != 1 {
		t.Fatalf("submitting should optimistically append one item, len(items) = %d", got)
	}

	// The server broadcasts user_prompt back to the sender too; without the
	// is_mine dedup this would render the message a second time.
	m.Update(eventMsg{event: client.Event{Kind: client.EventUserPrompt, SenderID: "me", Message: "hello agent"}})

	if got := len(m.transcript.items); got != 1 {
		t.Errorf("our own user_prompt echo must be dropped, len(items) = %d, want 1", got)
	}
}

// --- golden-frame-lite: View() renders the layout at a fixed size ---------

func TestModel_View_RendersTitleAndConnectionState(t *testing.T) {
	m := newTestModel(t, true)

	if got := m.View().Content; !strings.Contains(got, "test-conversation") {
		t.Errorf("initial View() should show the title, got:\n%s", got)
	}

	m.Update(eventMsg{event: client.Event{Kind: client.EventConnected, ACPServer: "Auggie"}})
	if got := m.View().Content; !strings.Contains(got, "Auggie") || !strings.Contains(got, "connected") {
		t.Errorf("after EventConnected, View() should show the ACP server and connected state, got:\n%s", got)
	}
}
