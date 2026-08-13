package chatui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inercia/mitto/internal/termmd"
	"github.com/inercia/mitto/pkg/api"
)

func permissionRender(styles *styles, width int) string {
	p := newPermissionModal(nil, styles)
	p.SetWidth(width)
	p.Push(api.Event{Title: "Run tool", Description: "Read the project configuration"})
	return p.Render()
}

func TestPermissionModal_RenderMakesApproveAndDenyExplicit(t *testing.T) {
	styles := newStyles()
	out := permissionRender(styles, 64)
	plain := ansi.Strip(out)
	for _, text := range []string{"Permission required: Run tool", "[y] Approve", "[n] Deny"} {
		if !strings.Contains(plain, text) {
			t.Errorf("permission modal missing %q:\n%s", text, plain)
		}
	}
	if !strings.Contains(out, styles.permissionApproveStyle.Render("[y] Approve")) ||
		!strings.Contains(out, styles.permissionDenyStyle.Render("[n] Deny")) {
		t.Errorf("permission actions did not retain distinct success/error styles:\n%s", out)
	}
	if plain == out || !strings.Contains(plain, "╭") {
		t.Errorf("styled permission modal should have semantic ANSI and a warning boundary:\n%s", out)
	}
}

func TestPermissionModal_RenderPlainKeepsTextAndBoundaryWithoutANSI(t *testing.T) {
	styles := newStyles()
	styles.apply(termmd.ModePlain, termmd.ThemeDark)
	out := permissionRender(styles, 64)
	assertNoANSI(t, "plain permission modal", out)
	for _, text := range []string{"╭", "Permission required", "[y] Approve", "[n] Deny"} {
		if !strings.Contains(out, text) {
			t.Errorf("plain permission modal missing %q:\n%s", text, out)
		}
	}
}

func TestPermissionModal_RenderClampsNarrowAndTinyWidths(t *testing.T) {
	styles := newStyles()
	styles.apply(termmd.ModePlain, termmd.ThemeDark)
	for _, width := range []int{1, 2, 3, 8, 16} {
		out := permissionRender(styles, width)
		for _, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d permission row measured %d cells: %q", width, got, line)
			}
		}
	}
}
