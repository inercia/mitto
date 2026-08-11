package chatui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/inercia/mitto/pkg/api"
)

type sessionPickerItem struct {
	session api.SessionInfo
}

func (i sessionPickerItem) Title() string {
	if i.session.Name != "" {
		return i.session.Name
	}
	return i.session.SessionID
}

func (i sessionPickerItem) Description() string {
	parts := []string{"ID " + i.session.SessionID}
	if i.session.Status != "" {
		parts = append(parts, i.session.Status)
	}
	if workspace := sessionPickerWorkspace(i.session); workspace != "" {
		parts = append(parts, "workspace "+workspace)
	}
	if i.session.WorkingDir != "" {
		parts = append(parts, i.session.WorkingDir)
	}
	parts = append(parts, formatSessionPickerUpdatedAt(i.session.UpdatedAt))
	return strings.Join(parts, " · ")
}

func sessionPickerWorkspace(session api.SessionInfo) string {
	if session.WorkspaceName != "" {
		return session.WorkspaceName
	}
	return session.WorkspaceUUID
}

func formatSessionPickerUpdatedAt(value string) string {
	updated, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "updated unknown"
	}
	return "updated " + updated.UTC().Format("2006-01-02 15:04 UTC")
}

// SessionPickerModel lets a CLI user choose a conversation before the chat
// TUI starts. It performs no I/O in Update; the command owns API calls and the
// tea.Program lifecycle.
type SessionPickerModel struct {
	items      []sessionPickerItem
	cursor     int
	width      int
	height     int
	selectedID string
	cancelled  bool
}

func NewSessionPickerModel(sessions []api.SessionInfo) *SessionPickerModel {
	items := make([]sessionPickerItem, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, sessionPickerItem{session: session})
	}
	return &SessionPickerModel{items: items, width: 80, height: 24}
}

func (m *SessionPickerModel) Init() tea.Cmd { return nil }

func (m *SessionPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
			return m, nil
		case "enter":
			if len(m.items) > 0 {
				m.selectedID = m.items[m.cursor].session.SessionID
				return m, tea.Quit
			}
		case "esc", "ctrl+c", "q":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *SessionPickerModel) View() tea.View {
	if m.cancelled || m.selectedID != "" {
		return tea.NewView("")
	}
	if len(m.items) == 0 {
		return tea.NewView("No selectable conversations.\n\nesc/q cancel")
	}
	width := max(m.width, 20)
	visible := max((m.height-5)/3, 1)
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	end := min(start+visible, len(m.items))

	var b strings.Builder
	b.WriteString("Choose a conversation\n\n")
	for index := start; index < end; index++ {
		prefix := "  "
		if index == m.cursor {
			prefix = "> "
		}
		b.WriteString(ansi.Truncate(prefix+m.items[index].Title(), width, "…"))
		b.WriteByte('\n')
		b.WriteString(ansi.Truncate("  "+m.items[index].Description(), width, "…"))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n%d/%d  ↑/↓ or j/k move · enter select · esc/q cancel", m.cursor+1, len(m.items))
	return tea.NewView(b.String())
}

func (m *SessionPickerModel) SelectedSessionID() string { return m.selectedID }

func (m *SessionPickerModel) Cancelled() bool { return m.cancelled }
