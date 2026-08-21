package chatui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inercia/mitto/internal/termmd"
	"github.com/inercia/mitto/pkg/api"
)

func assertNoANSI(t *testing.T, name, output string) {
	t.Helper()
	if stripped := ansi.Strip(output); stripped != output {
		t.Errorf("%s emitted ANSI in plain mode:\n%q", name, output)
	}
}

func semanticRenders(s *styles) []string {
	return []string{
		s.accentStyle.Render("accent"), s.selectedStyle.Render("selected"),
		s.selectedDescriptionStyle.Render("selected description"),
		s.mutedStyle.Render("muted"), s.successStyle.Render("success"),
		s.warningStyle.Render("warning"), s.errorStyle.Render("error"),
		s.borderStyle.Render("border"), s.userStyle.Render("user"),
		s.agentStyle.Render("agent"), s.thoughtStyle.Render("thought"),
		s.toolStyle.Render("tool"), s.systemStyle.Render("system"),
		s.completionCommandStyle.Render("command"), s.completionDescriptionStyle.Render("description"),
		s.permissionTitleStyle.Render("permission"), s.permissionApproveStyle.Render("approve"),
		s.permissionDenyStyle.Render("deny"),
		s.statusBar.Width(24).Render("status"),
		s.composerBorder.Width(24).Render("composer"),
		s.modalBorder.Width(24).Render("modal"),
	}
}

func TestSemanticPalette_DarkLightAndPlainRenderDeterministically(t *testing.T) {
	dark, light, plain := newStyles(), newStyles(), newStyles()
	dark.apply(termmd.ModeStyled, termmd.ThemeDark)
	light.apply(termmd.ModeStyled, termmd.ThemeLight)
	plain.apply(termmd.ModePlain, termmd.ThemeDark)

	darkOut, lightOut, plainOut := semanticRenders(dark), semanticRenders(light), semanticRenders(plain)
	for i := range darkOut {
		if darkOut[i] == lightOut[i] {
			t.Errorf("semantic render %d is identical for dark and light palettes: %q", i, darkOut[i])
		}
		if ansi.Strip(darkOut[i]) == darkOut[i] || ansi.Strip(lightOut[i]) == lightOut[i] {
			t.Errorf("semantic render %d did not emit styling in both styled palettes", i)
		}
		assertNoANSI(t, "plain semantic render", plainOut[i])
	}
	if !strings.Contains(plainOut[len(plainOut)-1], "╭") {
		t.Errorf("plain modal lost rounded border geometry: %q", plainOut[len(plainOut)-1])
	}
}

func populatedPresentationModel(t *testing.T, noColor bool) *Model {
	t.Helper()
	m := NewModel(nil, Options{Title: "Palette test", NoColor: noColor, Style: "auto", ShowThoughts: true})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.transcript.AppendUser("**user**")
	m.transcript.AppendOrUpdateAgent(1, "**agent**", "")
	m.transcript.AppendThought("thought")
	m.transcript.AppendTool("tool-1", "Read file", "done")
	m.transcript.AppendError("failed")
	m.transcript.AppendSystem("system")
	m.input.SetValue("draft")
	m.status.SetConnected("Auggie")
	m.status.SetInFlight(true)
	m.completion.Filter("/c")
	m.perm.Push(api.Event{Title: "Approve tool", Description: "Read a file"})
	return m
}

func presentationSnapshot(m *Model) []string {
	return []string{
		m.transcript.View(),
		m.input.Styles().Focused.Prompt.Render("> "),
		m.composerView(),
		m.status.Render(),
		m.completion.Render(),
		m.perm.Render(),
	}
}

func TestModel_BackgroundColorMsgReappliesEveryPresentationSurface(t *testing.T) {
	m := populatedPresentationModel(t, false)
	dark := presentationSnapshot(m)
	m.Update(tea.BackgroundColorMsg{Color: color.White})
	light := presentationSnapshot(m)

	if m.transcript.theme != termmd.ThemeLight {
		t.Fatalf("transcript theme = %v, want light", m.transcript.theme)
	}
	for i := range dark {
		if dark[i] == light[i] {
			t.Errorf("presentation surface %d did not change after light background resolution", i)
		}
	}
}

func TestModel_NoColorRemovesANSIFromEveryPresentationSurface(t *testing.T) {
	m := populatedPresentationModel(t, true)
	if m.requestTheme {
		t.Fatal("plain presentation must not request terminal background color")
	}
	outputs := append(presentationSnapshot(m), m.inputView(), m.View().Content)
	for i, output := range outputs {
		assertNoANSI(t, "chat presentation surface", output)
		if i == 0 && !strings.Contains(output, "[tool] Read file: done") {
			t.Errorf("plain transcript lost tool text: %q", output)
		}
	}
}

func TestModel_ComposerViewShowsBoundaryHintAndThemeAwareFocus(t *testing.T) {
	renders := make(map[string]string)
	for _, style := range []string{"dark", "light"} {
		m := NewModel(nil, Options{Title: "t", Style: style})
		m.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
		out := m.composerView()
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "╭") || !strings.Contains(plain, "Send a message…") ||
			!strings.Contains(plain, "enter send") {
			t.Errorf("%s composer missing boundary, placeholder, or key hint:\n%s", style, plain)
		}
		if plain == out {
			t.Errorf("%s composer should use semantic focus styling", style)
		}
		renders[style] = out
	}
	if renders["dark"] == renders["light"] {
		t.Error("dark and light composer focus styles should render differently")
	}

	plainModel := NewModel(nil, Options{Title: "t", NoColor: true})
	plainModel.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
	plainOut := plainModel.composerView()
	assertNoANSI(t, "plain composer", plainOut)
	if !strings.Contains(plainOut, "╭") || !strings.Contains(plainOut, "enter send") {
		t.Errorf("plain composer must retain structural focus and text hint:\n%s", plainOut)
	}
}
