package chatui

import (
	"image/color"

	textarea "charm.land/bubbles/v2/textarea"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/inercia/mitto/internal/termmd"
)

// PresentationOptions is the shared style/no-color configuration used by the
// conversation picker and the chat model.
type PresentationOptions struct {
	NoColor bool
	Style   string
}

func resolvePresentation(opts PresentationOptions) (termmd.Mode, termmd.Theme, bool) {
	if opts.NoColor {
		return termmd.ModePlain, termmd.ThemeDark, false
	}
	switch opts.Style {
	case "dark":
		return termmd.ModeStyled, termmd.ThemeDark, false
	case "light":
		return termmd.ModeStyled, termmd.ThemeLight, false
	default:
		return termmd.ModeStyled, termmd.ThemeDark, true
	}
}

type semanticPalette struct {
	accent, selected, selectedText color.Color
	muted, success, warning, err   color.Color
	border, user, agent, thought   color.Color
	tool, system                   color.Color
}

// styles holds every semantic Lipgloss style used by the chat and picker.
// Components retain this pointer while apply mutates its fields when auto
// background detection resolves, so no component can retain a stale palette.
type styles struct {
	palette semanticPalette

	accentStyle                lipgloss.Style
	selectedStyle              lipgloss.Style
	selectedDescriptionStyle   lipgloss.Style
	mutedStyle                 lipgloss.Style
	successStyle               lipgloss.Style
	warningStyle               lipgloss.Style
	errorStyle                 lipgloss.Style
	borderStyle                lipgloss.Style
	userStyle                  lipgloss.Style
	agentStyle                 lipgloss.Style
	thoughtStyle               lipgloss.Style
	toolStyle                  lipgloss.Style
	systemStyle                lipgloss.Style
	completionCommandStyle     lipgloss.Style
	completionDescriptionStyle lipgloss.Style
	permissionTitleStyle       lipgloss.Style
	permissionApproveStyle     lipgloss.Style
	permissionDenyStyle        lipgloss.Style
	statusBar                  lipgloss.Style
	composerBorder             lipgloss.Style
	modalBorder                lipgloss.Style
}

func newStyles() *styles {
	s := &styles{}
	s.apply(termmd.ModeStyled, termmd.ThemeDark)
	return s
}

func (s *styles) apply(mode termmd.Mode, theme termmd.Theme) {
	s.palette = semanticPaletteFor(mode, theme)
	base := lipgloss.NewStyle()
	if mode == termmd.ModePlain {
		s.accentStyle = base
		s.selectedStyle = base
		s.selectedDescriptionStyle = base
		s.mutedStyle = base
		s.successStyle = base
		s.warningStyle = base
		s.errorStyle = base
		s.borderStyle = base
		s.userStyle = base
		s.agentStyle = base
		s.thoughtStyle = base
		s.toolStyle = base
		s.systemStyle = base
		s.completionCommandStyle = base
		s.completionDescriptionStyle = base
		s.permissionTitleStyle = base
		s.permissionApproveStyle = base
		s.permissionDenyStyle = base
		s.statusBar = base.Padding(0, 1)
		s.composerBorder = base.Border(lipgloss.RoundedBorder())
		s.modalBorder = base.Border(lipgloss.RoundedBorder())
		return
	}

	p := s.palette
	s.accentStyle = base.Foreground(p.accent)
	s.selectedStyle = base.Foreground(p.selectedText).Background(p.selected).Bold(true)
	s.selectedDescriptionStyle = base.Foreground(p.selectedText).Background(p.selected)
	s.mutedStyle = base.Foreground(p.muted)
	s.successStyle = base.Foreground(p.success)
	s.warningStyle = base.Foreground(p.warning)
	s.errorStyle = base.Foreground(p.err).Bold(true)
	s.borderStyle = base.Foreground(p.border)
	s.userStyle = base.Foreground(p.user).Bold(true)
	s.agentStyle = base.Foreground(p.agent)
	s.thoughtStyle = base.Foreground(p.thought).Italic(true)
	s.toolStyle = base.Foreground(p.tool)
	s.systemStyle = base.Foreground(p.system)
	s.completionCommandStyle = base.Foreground(p.accent).Bold(true)
	s.completionDescriptionStyle = base.Foreground(p.muted)
	s.permissionTitleStyle = base.Foreground(p.warning).Bold(true)
	s.permissionApproveStyle = base.Foreground(p.success).Bold(true)
	s.permissionDenyStyle = base.Foreground(p.err).Bold(true)
	s.statusBar = base.Foreground(p.muted).Padding(0, 1)
	s.composerBorder = base.Border(lipgloss.RoundedBorder()).BorderForeground(p.accent)
	s.modalBorder = base.Border(lipgloss.RoundedBorder()).BorderForeground(p.warning)
}

// surfaceContentWidth returns the usable width inside a one-cell border. At
// tiny widths the border is omitted, so the terminal's full width stays usable.
func surfaceContentWidth(totalWidth int) int {
	if totalWidth > 2 {
		return totalWidth - 2
	}
	return max(1, totalWidth)
}

// renderSurface applies a one-cell border without allowing negative widths.
// Terminals narrower than the border fall back to the content itself.
func renderSurface(style lipgloss.Style, content string, totalWidth int) string {
	if totalWidth < 3 {
		return content
	}
	return style.Width(surfaceContentWidth(totalWidth)).Render(content)
}

func semanticPaletteFor(mode termmd.Mode, theme termmd.Theme) semanticPalette {
	if mode == termmd.ModePlain {
		return semanticPalette{}
	}
	if theme == termmd.ThemeLight {
		return semanticPalette{
			accent: lipgloss.Color("#0369A1"), selected: lipgloss.Color("#DCEFFC"), selectedText: lipgloss.Color("#0C4A6E"),
			muted: lipgloss.Color("#64748B"), success: lipgloss.Color("#15803D"), warning: lipgloss.Color("#A16207"), err: lipgloss.Color("#B91C1C"),
			border: lipgloss.Color("#94A3B8"), user: lipgloss.Color("#6D28D9"), agent: lipgloss.Color("#1E293B"), thought: lipgloss.Color("#64748B"),
			tool: lipgloss.Color("#0369A1"), system: lipgloss.Color("#A16207"),
		}
	}
	return semanticPalette{
		accent: lipgloss.Color("#7DD3FC"), selected: lipgloss.Color("#1E3A5F"), selectedText: lipgloss.Color("#E0F2FE"),
		muted: lipgloss.Color("#94A3B8"), success: lipgloss.Color("#4ADE80"), warning: lipgloss.Color("#FBBF24"), err: lipgloss.Color("#F87171"),
		border: lipgloss.Color("#475569"), user: lipgloss.Color("#C4B5FD"), agent: lipgloss.Color("#E2E8F0"), thought: lipgloss.Color("#94A3B8"),
		tool: lipgloss.Color("#7DD3FC"), system: lipgloss.Color("#FBBF24"),
	}
}

func (s *styles) textareaStyles(mode termmd.Mode, theme termmd.Theme) textarea.Styles {
	result := textarea.DefaultStyles(theme == termmd.ThemeDark)
	if mode == termmd.ModePlain {
		result.Focused = textarea.StyleState{}
		result.Blurred = textarea.StyleState{}
		result.Cursor.Color = lipgloss.NoColor{}
		return result
	}

	result.Focused.Text = s.agentStyle
	result.Focused.Placeholder = s.mutedStyle
	result.Focused.Prompt = s.accentStyle
	result.Focused.LineNumber = s.mutedStyle
	result.Focused.CursorLineNumber = s.accentStyle
	result.Focused.EndOfBuffer = s.mutedStyle
	result.Blurred.Text = s.agentStyle
	result.Blurred.Placeholder = s.mutedStyle
	result.Blurred.Prompt = s.mutedStyle
	result.Blurred.LineNumber = s.mutedStyle
	result.Blurred.CursorLineNumber = s.mutedStyle
	result.Blurred.EndOfBuffer = s.mutedStyle
	result.Cursor.Color = s.palette.accent
	return result
}

// renderTranscriptBlock styles only the semantic label, leaving the rendered
// Markdown body untouched so Glamour's syntax colors remain authoritative.
func (s *styles) renderTranscriptBlock(label, body string, labelStyle lipgloss.Style) string {
	return labelStyle.Render(label) + "\n" + body
}

// renderTranscriptLine renders non-Markdown events as one semantic line.
func (s *styles) renderTranscriptLine(label, body string, itemStyle lipgloss.Style) string {
	return itemStyle.Render(label + " " + body)
}

// renderTool renders a tool-call/tool-update item as a labeled line. Status is
// omitted for file_read/file_write items, which do not carry one.
func (s *styles) renderTool(title, status string) string {
	if status == "" {
		return s.renderTranscriptLine("[tool]", title, s.toolStyle)
	}
	return s.renderTranscriptLine("[tool]", title+": "+status, s.toolStyle)
}
