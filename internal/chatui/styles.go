package chatui

import (
	lipgloss "charm.land/lipgloss/v2"
)

// styles holds the lipgloss styles used across the transcript, status line
// and permission modal. Constructed once in model.go's newModel and passed
// by pointer so every sub-component shares the same palette.
type styles struct {
	userStyle    lipgloss.Style
	thoughtStyle lipgloss.Style
	toolStyle    lipgloss.Style
	errorStyle   lipgloss.Style
	statusBar    lipgloss.Style
	statusOK     lipgloss.Style
	statusBad    lipgloss.Style
	modalBorder  lipgloss.Style
}

func newStyles() *styles {
	return &styles{
		userStyle:    lipgloss.NewStyle().Bold(true),
		thoughtStyle: lipgloss.NewStyle().Faint(true).Italic(true),
		toolStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		errorStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
		statusBar:    lipgloss.NewStyle().Reverse(true).Padding(0, 1),
		statusOK:     lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		statusBad:    lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		modalBorder:  lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2),
	}
}

// renderTool renders a tool-call/tool-update item as a single dimmed,
// bracketed line: "[tool] <title>: <status>", or "[tool] <title>" when
// status is empty (e.g. file_read/file_write items, which have no status).
func (s *styles) renderTool(title, status string) string {
	if status == "" {
		return s.toolStyle.Render("[tool] " + title)
	}
	return s.toolStyle.Render("[tool] " + title + ": " + status)
}
