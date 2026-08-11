package chatui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestExecuteSlashCommand_Help_AppendsSystemItem(t *testing.T) {
	m := newTestModel(t, true)
	handled, _ := m.executeSlashCommand("/help")
	if !handled {
		t.Fatal("/help should be handled")
	}
	if got := len(m.transcript.items); got != 1 || m.transcript.items[0].kind != itemSystem {
		t.Fatalf("/help should append a system item, items = %+v", m.transcript.items)
	}
}

func TestExecuteSlashCommand_Quit_ReturnsQuitCmd(t *testing.T) {
	m := newTestModel(t, true)
	handled, cmd := m.executeSlashCommand("/quit")
	if !handled {
		t.Fatal("/quit should be handled")
	}
	if !m.quitting {
		t.Error("/quit should set quitting = true")
	}
	if cmd == nil {
		t.Fatal("/quit should return a tea.Quit Cmd")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("cmd() = %T, want tea.QuitMsg", cmd())
	}
}

func TestExecuteSlashCommand_QuitAliases(t *testing.T) {
	for _, alias := range []string{"/exit", "/q"} {
		m := newTestModel(t, true)
		handled, _ := m.executeSlashCommand(alias)
		if !handled || !m.quitting {
			t.Errorf("%s should be treated as /quit", alias)
		}
	}
}

func TestExecuteSlashCommand_Cancel_IssuesCancelCmd(t *testing.T) {
	m := newTestModel(t, true)
	handled, cmd := m.executeSlashCommand("/cancel")
	if !handled {
		t.Fatal("/cancel should be handled")
	}
	if cmd == nil {
		t.Error("/cancel should issue a Cmd (Session.Cancel)")
	}
}

func TestExecuteSlashCommand_Clear_EmptiesTranscript(t *testing.T) {
	m := newTestModel(t, true)
	m.transcript.AppendUser("hello")
	m.transcript.AppendError("boom")

	handled, _ := m.executeSlashCommand("/clear")
	if !handled {
		t.Fatal("/clear should be handled")
	}
	if got := len(m.transcript.items); got != 0 {
		t.Errorf("/clear should empty the transcript, len(items) = %d", got)
	}
}

func TestExecuteSlashCommand_Unknown_AppendsErrorAndDoesNotSend(t *testing.T) {
	m := newTestModel(t, true)
	handled, cmd := m.executeSlashCommand("/bogus")
	if !handled {
		t.Fatal("an unrecognized slash command must still be handled (consumed) locally")
	}
	if cmd != nil {
		t.Error("an unknown command must not issue a send Cmd")
	}
	if got := len(m.transcript.items); got != 1 || m.transcript.items[0].kind != itemError {
		t.Fatalf("unknown command should append an error item, items = %+v", m.transcript.items)
	}
}

// --- integration through handleKey's enter path -----------------------------

func TestHandleKey_Enter_SlashCommandNeverSent(t *testing.T) {
	m := newTestModel(t, true)
	m.input.SetValue("/help")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	// /help produces no send Cmd, only the (possibly nil) save-history Cmd —
	// crucially, no itemUser (which would mean it round-tripped as a prompt).
	for _, it := range m.transcript.items {
		if it.kind == itemUser {
			t.Errorf("a slash command must never be appended as a user prompt, items = %+v", m.transcript.items)
		}
	}
	_ = cmd
}

func TestHandleKey_Enter_RecordsInHistory(t *testing.T) {
	m := newTestModel(t, true)
	m.input.SetValue("hello agent")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if _, ok := m.history.Prev(""); !ok {
		t.Fatal("submitting a line should record it in input history")
	}
}
