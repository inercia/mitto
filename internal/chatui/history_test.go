package chatui

import (
	"testing"

	"github.com/inercia/mitto/internal/termmd"
	"github.com/inercia/mitto/pkg/api"
)

// newTestTranscript builds a bare transcript (no Model/textarea overhead)
// for history.go's applySyncEvent, which only ever touches a *transcript.
func newTestTranscript(t *testing.T) *transcript {
	t.Helper()
	tr := newTranscript(newStyles(), true)
	tr.SetSize(80, 24)
	return tr
}

func TestApplySyncEvent_AgentMessage(t *testing.T) {
	tr := newTestTranscript(t)
	ev := api.SyncEvent{Seq: 1, Type: "agent_message", Data: map[string]any{
		"text": "**hi**", "html": "<b>hi</b>",
	}}
	applySyncEvent(tr, ev)

	if got := len(tr.items); got != 1 {
		t.Fatalf("len(items) = %d, want 1", got)
	}
	if got := tr.items[0]; got.kind != itemAgent || got.markdown != "**hi**" || got.html != "<b>hi</b>" {
		t.Errorf("item = %+v, want kind=itemAgent markdown=%q html=%q", got, "**hi**", "<b>hi</b>")
	}
}

func TestApplySyncEvent_AgentMessage_NoMarkdown_DegradesToHTML(t *testing.T) {
	// Pre-mitto-pscc.3 history events carry no "text" field at all — only
	// html. renderModeFor (items.go) must fall back to ModeDegraded for
	// these, which it does purely from markdown=="".
	tr := newTestTranscript(t)
	ev := api.SyncEvent{Seq: 1, Type: "agent_message", Data: map[string]any{"html": "<b>legacy</b>"}}
	applySyncEvent(tr, ev)

	it := tr.items[0]
	if it.markdown != "" || it.html != "<b>legacy</b>" {
		t.Fatalf("item = %+v, want empty markdown + the legacy html", it)
	}
	if got := renderModeFor(termmd.ModeStyled, it.markdown); got != termmd.ModeDegraded {
		t.Errorf("renderModeFor with empty markdown = %v, want ModeDegraded", got)
	}
}

func TestApplySyncEvent_ToolCallThenUpdate(t *testing.T) {
	tr := newTestTranscript(t)
	applySyncEvent(tr, api.SyncEvent{Type: "tool_call", Data: map[string]any{
		"ID": "t1", "Title": "Read file", "Status": "running",
	}})
	applySyncEvent(tr, api.SyncEvent{Type: "tool_update", Data: map[string]any{
		"ID": "t1", "Status": "done",
	}})

	if got := len(tr.items); got != 1 {
		t.Fatalf("tool_update on a known ID must mutate in place, len(items) = %d, want 1", got)
	}
	if got := tr.items[0].status; got != "done" {
		t.Errorf("status = %q, want %q", got, "done")
	}
}

func TestApplySyncEvent_UserPromptAndError(t *testing.T) {
	tr := newTestTranscript(t)
	applySyncEvent(tr, api.SyncEvent{Type: "user_prompt", Data: map[string]any{"message": "hello"}})
	applySyncEvent(tr, api.SyncEvent{Type: "error", Data: map[string]any{"message": "boom"}})

	if got := len(tr.items); got != 2 {
		t.Fatalf("len(items) = %d, want 2", got)
	}
	if tr.items[0].kind != itemUser || tr.items[0].markdown != "hello" {
		t.Errorf("items[0] = %+v, want a user item with markdown %q", tr.items[0], "hello")
	}
	if tr.items[1].kind != itemError || tr.items[1].title != "boom" {
		t.Errorf("items[1] = %+v, want an error item with title %q", tr.items[1], "boom")
	}
}

func TestApplySyncEvent_UnknownType_Ignored(t *testing.T) {
	tr := newTestTranscript(t)
	applySyncEvent(tr, api.SyncEvent{Type: "something_new", Data: map[string]any{"x": 1}})
	if got := len(tr.items); got != 0 {
		t.Errorf("an unrecognized event type must be a silent no-op, len(items) = %d, want 0", got)
	}
}
