package chatui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inercia/mitto/pkg/api"
)

type visualMode struct {
	name    string
	style   string
	noColor bool
}

var visualModes = []visualMode{
	{name: "dark", style: "dark"},
	{name: "light", style: "light"},
	{name: "plain", noColor: true},
}

func assertVisualWidth(t *testing.T, width int, frame string) {
	t.Helper()
	for row, line := range strings.Split(frame, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Errorf("row %d measured %d cells at width %d: %q", row, got, width, ansi.Strip(line))
		}
	}
}

func assertVisualStyling(t *testing.T, mode visualMode, frame string) {
	t.Helper()
	if mode.noColor {
		assertNoANSI(t, mode.name+" visual frame", frame)
	} else if ansi.Strip(frame) == frame {
		t.Errorf("%s visual frame emitted no ANSI styling", mode.name)
	}
}

func TestVisualRegressionPickerMatrix(t *testing.T) {
	sessions := []api.SessionInfo{
		{SessionID: "alpha", Name: "Alpha", WorkingDir: "/workspace/a"},
		{SessionID: "beta", Name: "Beta", WorkingDir: "/workspace/b"},
	}
	normalFrames := make(map[string]string)

	for _, mode := range visualModes {
		for _, width := range []int{64, 20} {
			t.Run(fmt.Sprintf("%s/width=%d", mode.name, width), func(t *testing.T) {
				picker := NewSessionPickerModel(sessions, PresentationOptions{Style: mode.style, NoColor: mode.noColor})
				picker.Update(tea.WindowSizeMsg{Width: width, Height: 10})
				first := picker.View().Content
				firstSelection := ansi.Truncate("> "+picker.items[picker.cursor].Line(), width, "…")
				if width == 64 {
					normalFrames[mode.name] = first
				}
				plainFirst := ansi.Strip(first)
				if strings.Count(plainFirst, "> ") != 1 || !strings.Contains(plainFirst, firstSelection) {
					t.Errorf("first picker frame lost its single selection cue:\n%s", plainFirst)
				}
				if !strings.Contains(first, picker.styles.selectedStyle.Render(firstSelection)) {
					t.Errorf("first picker row lost semantic selected styling:\n%s", first)
				}

				picker.Update(tea.KeyPressMsg{Code: tea.KeyDown})
				second := picker.View().Content
				secondSelection := ansi.Truncate("> "+picker.items[picker.cursor].Line(), width, "…")
				plainSecond := ansi.Strip(second)
				if first == second || strings.Count(plainSecond, "> ") != 1 ||
					!strings.Contains(plainSecond, secondSelection) {
					t.Errorf("selection did not move exactly once:\n%s", plainSecond)
				}
				assertVisualWidth(t, width, first)
				assertVisualWidth(t, width, second)
				assertVisualStyling(t, mode, first)
				assertVisualStyling(t, mode, second)
			})
		}
	}

	if normalFrames["dark"] == normalFrames["light"] {
		t.Error("dark and light picker frames collapsed to identical styling")
	}
	if ansi.Strip(normalFrames["dark"]) != ansi.Strip(normalFrames["light"]) {
		t.Error("dark and light picker palettes changed semantic text or layout")
	}
}

func visualChatModel(mode visualMode, width int) *Model {
	m := NewModel(nil, Options{
		Title: "Visual matrix", Style: mode.style, NoColor: mode.noColor, ShowThoughts: true,
	})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 36})
	m.transcript.AppendUser("user body")
	m.transcript.AppendOrUpdateAgent(1, "assistant body", "")
	m.transcript.AppendThought("thought body")
	m.transcript.AppendTool("tool-1", "Read file", "done")
	m.transcript.AppendSystem("system body")
	m.transcript.AppendError("error body")
	return m
}

func TestVisualRegressionChatSurfaceMatrix(t *testing.T) {
	normalFrames := make(map[string]string)
	labels := []string{"[user]", "[assistant]", "[thought]", "[tool]", "[system]", "[error]"}

	for _, mode := range visualModes {
		for _, width := range []int{64, 24} {
			t.Run(fmt.Sprintf("%s/width=%d", mode.name, width), func(t *testing.T) {
				m := visualChatModel(mode, width)
				base := m.View().Content
				if width == 64 {
					normalFrames[mode.name] = base
				}
				plainBase := ansi.Strip(base)
				for _, label := range labels {
					if !strings.Contains(plainBase, label) {
						t.Errorf("base frame missing transcript cue %q:\n%s", label, plainBase)
					}
				}
				if !strings.Contains(plainBase, "○ connecting…") || !strings.Contains(plainBase, "enter send") {
					t.Errorf("base frame missing status or composer cue:\n%s", plainBase)
				}

				m.input.SetValue("/c")
				m.completion.Filter("/c")
				m.recalculateLayout()
				completionFirst := m.View().Content
				m.completion.Next()
				completionSecond := m.View().Content
				if completionFirst == completionSecond ||
					!strings.Contains(ansi.Strip(completionFirst), "> /cancel") ||
					!strings.Contains(ansi.Strip(completionSecond), "> /clear") {
					t.Errorf("completion selection cue did not follow selection")
				}

				m.perm.Push(api.Event{Title: "Run tool", Description: "Read configuration"})
				m.recalculateLayout()
				permission := m.View().Content
				plainPermission := ansi.Strip(permission)
				permissionCues := []string{"Permission required: Run tool", "[y] Approve", "[n] Deny"}
				if width == 24 {
					permissionCues = []string{"Permission required:", "[y] Approve", "[n]", "Deny"}
				}
				for _, cue := range permissionCues {
					if !strings.Contains(plainPermission, cue) {
						t.Errorf("permission frame missing %q:\n%s", cue, plainPermission)
					}
				}

				for _, frame := range []string{base, completionFirst, completionSecond, permission} {
					assertVisualWidth(t, width, frame)
					assertVisualStyling(t, mode, frame)
				}
			})
		}
	}

	if normalFrames["dark"] == normalFrames["light"] {
		t.Error("dark and light chat frames collapsed to identical styling")
	}
	if ansi.Strip(normalFrames["dark"]) != ansi.Strip(normalFrames["light"]) {
		t.Error("dark and light chat palettes changed semantic text or layout")
	}
}

func TestVisualRegressionFooterStateMatrix(t *testing.T) {
	for _, mode := range visualModes {
		for _, width := range []int{64, 5} {
			t.Run(fmt.Sprintf("%s/width=%d", mode.name, width), func(t *testing.T) {
				styles := newStyles()
				presentationMode, theme, _ := resolvePresentation(PresentationOptions{Style: mode.style, NoColor: mode.noColor})
				styles.apply(presentationMode, theme)
				frames := make(map[string]string)
				for _, state := range []string{"connecting", "connected", "working", "disconnected"} {
					status := newStatusLine(styles, "Visual matrix")
					status.SetWidth(width)
					switch state {
					case "connected":
						status.SetConnected("Auggie")
					case "working":
						status.SetConnected("Auggie")
						status.SetInFlight(true)
					case "disconnected":
						status.SetDisconnected("stream ended")
					}
					frames[state] = status.Render()
					assertVisualWidth(t, width, frames[state])
					assertVisualStyling(t, mode, frames[state])
				}

				seen := make(map[string]string)
				for state, frame := range frames {
					plain := ansi.Strip(frame)
					if previous, exists := seen[plain]; exists {
						t.Errorf("semantic states %s and %s collapsed to %q", previous, state, plain)
					}
					seen[plain] = state
				}
			})
		}
	}
}
