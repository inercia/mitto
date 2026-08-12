package chatui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/inercia/mitto/pkg/api"
)

func assertSessionPickerQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected tea.Quit command, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("command returned %T, want tea.QuitMsg", msg)
	}
}

func TestSessionPickerItem_TitleAndLine(t *testing.T) {
	item := sessionPickerItem{session: api.SessionInfo{
		SessionID:     "session-1",
		Status:        "idle",
		WorkspaceName: "Demo",
		WorkspaceUUID: "workspace-fallback",
		WorkingDir:    "/tmp/demo",
		UpdatedAt:     "2026-08-11T14:05:00+02:00",
	}}
	if got := item.Title(); got != "session-1" {
		t.Errorf("Title() = %q, want ID fallback", got)
	}
	if got := (sessionPickerItem{session: api.SessionInfo{Name: "Named conversation"}}).Title(); got != "Named conversation" {
		t.Errorf("named Title() = %q, want conversation name", got)
	}

	line := item.Line()
	if !strings.Contains(line, "/tmp/demo") {
		t.Errorf("Line() = %q, want working dir /tmp/demo", line)
	}
	if !strings.Contains(line, "session-1") {
		t.Errorf("Line() = %q, want title/ID fallback session-1", line)
	}
	for _, unwanted := range []string{"ID session-1", "idle", "Demo", "workspace-fallback", "2026-08-11", "updated"} {
		if strings.Contains(line, unwanted) {
			t.Errorf("Line() = %q, must not contain status/workspace/timestamp %q", line, unwanted)
		}
	}

	// No working dir: falls back to title only.
	noDir := sessionPickerItem{session: api.SessionInfo{SessionID: "no-dir"}}
	if got := noDir.Line(); got != "no-dir" {
		t.Errorf("Line() with no WorkingDir = %q, want bare title/ID %q", got, "no-dir")
	}
}

func TestNewSessionPickerModel_SortsByFolderThenTitle(t *testing.T) {
	sessions := []api.SessionInfo{
		{SessionID: "zebra", WorkingDir: "/work/Beta", Name: "Alpha"},
		{SessionID: "bravo", WorkingDir: "/work/alpha", Name: "Zulu"},
		{SessionID: "alpha", WorkingDir: "/work/alpha", Name: "alpha"},
	}
	m := NewSessionPickerModel(sessions)

	want := []string{"alpha", "bravo", "zebra"}
	for index, item := range m.items {
		if got := item.session.SessionID; got != want[index] {
			t.Errorf("items[%d].SessionID = %q, want %q", index, got, want[index])
		}
	}
	if sessions[0].SessionID != "zebra" {
		t.Errorf("NewSessionPickerModel mutated caller input: first ID = %q", sessions[0].SessionID)
	}
}

func TestSessionPickerModel_NavigationAndSelection(t *testing.T) {
	m := NewSessionPickerModel([]api.SessionInfo{{SessionID: "first"}, {SessionID: "second"}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := m.SelectedSessionID(); got != "second" {
		t.Errorf("SelectedSessionID() = %q, want second", got)
	}
	if m.Cancelled() {
		t.Error("selection must not mark picker cancelled")
	}
	assertSessionPickerQuit(t, cmd)
}

func TestSessionPickerModel_CancelKeys(t *testing.T) {
	keys := []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: 'c', Mod: tea.ModCtrl},
		{Text: "q", Code: 'q'},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			m := NewSessionPickerModel([]api.SessionInfo{{SessionID: "first"}})
			_, cmd := m.Update(key)
			if !m.Cancelled() {
				t.Errorf("key %q did not cancel picker", key.String())
			}
			if m.SelectedSessionID() != "" {
				t.Errorf("key %q selected %q while cancelling", key.String(), m.SelectedSessionID())
			}
			assertSessionPickerQuit(t, cmd)
		})
	}
}

func TestSessionPickerModel_ViewResizesAndKeepsCursorVisible(t *testing.T) {
	m := NewSessionPickerModel([]api.SessionInfo{
		{SessionID: "first", Name: "First"},
		{SessionID: "second", Name: "Second"},
		{SessionID: "third", Name: "Third"},
	})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 6}) // one visible row
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	view := m.View().Content
	if !strings.Contains(view, "> Second") || strings.Contains(view, "First") {
		t.Errorf("resized View() did not keep the cursor row visible:\n%s", view)
	}
	if !strings.Contains(view, "2/3") {
		t.Errorf("View() = %q, want position indicator 2/3", view)
	}
}

func TestSessionPickerModel_EmptyViewAndBoundedNavigation(t *testing.T) {
	empty := NewSessionPickerModel(nil)
	if got := empty.View().Content; !strings.Contains(got, "No selectable conversations") {
		t.Errorf("empty View() = %q, want clear empty state", got)
	}

	m := NewSessionPickerModel([]api.SessionInfo{{SessionID: "only"}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != 0 {
		t.Errorf("single-item navigation moved cursor to %d, want 0", m.cursor)
	}
}
