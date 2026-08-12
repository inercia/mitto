package chatui

import (
	"fmt"
	"sort"
	"strings"

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

// Line renders the item as a single line: the working directory (folder)
// and the conversation title, clearly separated. When no working directory
// is known, only the title is shown.
func (i sessionPickerItem) Line() string {
	if i.session.WorkingDir == "" {
		return i.Title()
	}
	return i.session.WorkingDir + "  —  " + i.Title()
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
	sort.SliceStable(items, func(left, right int) bool {
		leftDir := strings.ToLower(items[left].session.WorkingDir)
		rightDir := strings.ToLower(items[right].session.WorkingDir)
		if leftDir != rightDir {
			return leftDir < rightDir
		}
		leftTitle := strings.ToLower(items[left].Title())
		rightTitle := strings.ToLower(items[right].Title())
		if leftTitle != rightTitle {
			return leftTitle < rightTitle
		}
		return items[left].session.SessionID < items[right].session.SessionID
	})
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
	visible := max(m.height-5, 1)
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
		b.WriteString(ansi.Truncate(prefix+m.items[index].Line(), width, "…"))
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n%d/%d  ↑/↓ or j/k move · enter select · esc/q cancel", m.cursor+1, len(m.items))
	return tea.NewView(b.String())
}

func (m *SessionPickerModel) SelectedSessionID() string { return m.selectedID }

func (m *SessionPickerModel) Cancelled() bool { return m.cancelled }
