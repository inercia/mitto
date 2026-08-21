package chatui

import (
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// statusLine is the imperative struct rendering the single-line footer:
// conversation title, ACP server, connection state, and agent activity.
// Plain data + a Render method, per crush's pattern.
type statusLine struct {
	title      string
	acpServer  string
	connected  bool
	disconnect string // non-empty once the stream has terminated; shown instead of "connected"
	inFlight   bool
	width      int

	styles *styles
}

func newStatusLine(styles *styles, title string) *statusLine {
	return &statusLine{styles: styles, title: title}
}

func (s *statusLine) SetWidth(w int) { s.width = w }

func (s *statusLine) SetConnected(acpServer string) {
	s.connected = true
	s.acpServer = acpServer
	s.disconnect = ""
}

func (s *statusLine) SetDisconnected(reason string) {
	s.connected = false
	s.disconnect = reason
}

func (s *statusLine) SetInFlight(v bool) { s.inFlight = v }

func (s *statusLine) Render() string {
	width := max(1, s.width)
	bar := s.styles.statusBar
	frameWidth := bar.GetHorizontalFrameSize()
	if width <= frameWidth {
		bar = bar.Padding(0)
		frameWidth = 0
	}
	contentWidth := max(1, width-frameWidth)

	left := s.styles.accentStyle.Render(s.title)
	if s.acpServer != "" {
		left += s.styles.mutedStyle.Render(" · " + s.acpServer)
	}

	// Keep enough identity visible at normal terminal widths. The state summary
	// receives the whole row at tiny widths, where its compact symbols win.
	rightBudget := contentWidth
	if contentWidth >= 24 {
		identityFloor := min(16, contentWidth/3)
		rightBudget -= identityFloor + 1
	}
	right := s.renderState(rightBudget)
	rightWidth := lipgloss.Width(right)

	leftBudget := contentWidth - rightWidth
	gap := 0
	if leftBudget > 1 && rightWidth > 0 {
		gap = 1
		leftBudget--
	}
	left = ansi.Truncate(left, max(0, leftBudget), "…")
	line := left + lipgloss.NewStyle().Width(gap).Render("") + right
	return bar.Width(width).Render(line)
}

func (s *statusLine) renderState(width int) string {
	if width <= 0 {
		return ""
	}

	var symbol, label string
	var stateStyle lipgloss.Style
	switch {
	case s.disconnect != "":
		symbol, label, stateStyle = "×", "disconnected", s.styles.errorStyle
	case s.connected:
		symbol, label, stateStyle = "●", "connected", s.styles.successStyle
	default:
		symbol, label, stateStyle = "○", "connecting…", s.styles.warningStyle
	}
	connection := stateStyle.Render(symbol + " " + label)
	activity := ""
	if s.inFlight {
		activity = s.styles.mutedStyle.Render(" · ") + s.styles.accentStyle.Render("◆ working")
	}

	base := connection + activity
	if lipgloss.Width(base) > width {
		compact := stateStyle.Render(symbol)
		if s.inFlight {
			compact += " " + s.styles.accentStyle.Render("◆")
		}
		return ansi.Truncate(compact, width, "")
	}

	if s.disconnect == "" {
		return base
	}
	reasonWidth := width - lipgloss.Width(base) - 2
	if reasonWidth <= 0 {
		return base
	}
	reason := ansi.Truncate(s.disconnect, reasonWidth, "…")
	return connection + s.styles.errorStyle.Render(": "+reason) + activity
}
