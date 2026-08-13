package chatui

import (
	lipgloss "charm.land/lipgloss/v2"
)

// statusLine is the imperative struct rendering the single-line footer:
// conversation title, ACP server, connection state, and an in-flight
// indicator. Plain data + a Render method, per crush's pattern.
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
	left := s.styles.accentStyle.Render(s.title)
	if s.acpServer != "" {
		left += s.styles.mutedStyle.Render(" · " + s.acpServer)
	}

	var right string
	switch {
	case s.disconnect != "":
		right = s.styles.errorStyle.Render("disconnected: " + s.disconnect)
	case s.connected:
		right = s.styles.successStyle.Render("connected")
	default:
		right = s.styles.warningStyle.Render("connecting…")
	}
	if s.inFlight {
		right += " " + s.styles.accentStyle.Render("●")
	}

	gap := s.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	line := left + lipgloss.NewStyle().Width(gap).Render("") + right
	return s.styles.statusBar.Width(s.width).Render(line)
}
