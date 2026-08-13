package chatui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/inercia/mitto/internal/termmd"
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
	items        []sessionPickerItem
	cursor       int
	width        int
	height       int
	selectedID   string
	cancelled    bool
	mode         termmd.Mode
	theme        termmd.Theme
	styles       *styles
	requestTheme bool
}

func NewSessionPickerModel(sessions []api.SessionInfo, options ...PresentationOptions) *SessionPickerModel {
	var opts PresentationOptions
	if len(options) > 0 {
		opts = options[0]
	}
	mode, theme, requestTheme := resolvePresentation(opts)
	st := newStyles()
	st.apply(mode, theme)
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
	return &SessionPickerModel{
		items: items, width: 80, height: 24,
		mode: mode, theme: theme, styles: st, requestTheme: requestTheme,
	}
}

func (m *SessionPickerModel) Init() tea.Cmd {
	if m.requestTheme {
		return func() tea.Msg { return tea.RequestBackgroundColor() }
	}
	return nil
}

func (m *SessionPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		if msg.IsDark() {
			m.applyPresentation(termmd.ThemeDark)
		} else {
			m.applyPresentation(termmd.ThemeLight)
		}
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

func (m *SessionPickerModel) applyPresentation(theme termmd.Theme) {
	m.theme = theme
	m.styles.apply(m.mode, theme)
}

func (m *SessionPickerModel) renderItem(item sessionPickerItem) string {
	if item.session.WorkingDir == "" {
		return m.styles.agentStyle.Render(item.Title())
	}
	return m.styles.mutedStyle.Render(item.session.WorkingDir+"  —  ") +
		m.styles.agentStyle.Render(item.Title())
}

func (m *SessionPickerModel) View() tea.View {
	if m.cancelled || m.selectedID != "" {
		return tea.NewView("")
	}
	if len(m.items) == 0 {
		return tea.NewView(m.styles.warningStyle.Render("No selectable conversations.") +
			"\n\n" + m.styles.mutedStyle.Render("esc/q cancel"))
	}
	width := max(m.width, 20)
	visible := max(m.height-5, 1)
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	end := min(start+visible, len(m.items))

	var b strings.Builder
	b.WriteString(m.styles.accentStyle.Render("Choose a conversation"))
	b.WriteString("\n\n")
	for index := start; index < end; index++ {
		prefix := "  "
		if index == m.cursor {
			prefix = "> "
		}
		line := ansi.Truncate(prefix+m.renderItem(m.items[index]), width, "…")
		if index == m.cursor {
			line = ansi.Truncate(prefix+m.items[index].Line(), width, "…")
			line = m.styles.selectedStyle.Render(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	footer := fmt.Sprintf("%d/%d  ↑/↓ or j/k move · enter select · esc/q cancel", m.cursor+1, len(m.items))
	b.WriteString("\n" + m.styles.mutedStyle.Render(footer))
	return tea.NewView(b.String())
}

func (m *SessionPickerModel) SelectedSessionID() string { return m.selectedID }

func (m *SessionPickerModel) Cancelled() bool { return m.cancelled }
