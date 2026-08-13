package chatui

import (
	"fmt"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inercia/mitto/internal/termmd"
)

func TestStatusLine_ConnectionAndActivityStatesHaveRedundantCues(t *testing.T) {
	tests := []struct {
		name       string
		connected  bool
		disconnect string
		working    bool
		want       []string
		dontWant   string
	}{
		{name: "connecting idle", want: []string{"○ connecting…"}, dontWant: "working"},
		{name: "connecting working", working: true, want: []string{"○ connecting…", "◆ working"}},
		{name: "connected idle", connected: true, want: []string{"● connected"}, dontWant: "working"},
		{name: "connected working", connected: true, working: true, want: []string{"● connected", "◆ working"}},
		{name: "disconnected idle", disconnect: "stream ended", want: []string{"× disconnected: stream ended"}, dontWant: "working"},
		{name: "disconnected working", disconnect: "stream ended", working: true, want: []string{"× disconnected: stream ended", "◆ working"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			styles := newStyles()
			styles.apply(termmd.ModePlain, termmd.ThemeDark)
			status := newStatusLine(styles, "Conversation")
			status.SetWidth(100)
			if tt.connected {
				status.SetConnected("Auggie")
			}
			if tt.disconnect != "" {
				status.SetDisconnected(tt.disconnect)
			}
			status.SetInFlight(tt.working)

			got := status.Render()
			assertNoANSI(t, tt.name, got)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("Render() = %q, want cue %q", got, want)
				}
			}
			if tt.dontWant != "" && strings.Contains(got, tt.dontWant) {
				t.Errorf("Render() = %q, must not contain %q", got, tt.dontWant)
			}
		})
	}
}

func TestStatusLine_StyledConnectionAndWorkingUseDistinctRoles(t *testing.T) {
	styles := newStyles()
	status := newStatusLine(styles, "Conversation")
	status.SetWidth(80)
	status.SetConnected("Auggie")
	status.SetInFlight(true)

	got := status.Render()
	for _, want := range []string{
		styles.successStyle.Render("● connected"),
		styles.accentStyle.Render("◆ working"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render() missing semantic fragment %q in %q", want, got)
		}
	}
	if ansi.Strip(got) == got {
		t.Fatal("styled status line must emit semantic ANSI styling")
	}
}

func TestStatusLine_LongContentFitsEveryWidth(t *testing.T) {
	for _, plain := range []bool{false, true} {
		for _, working := range []bool{false, true} {
			for _, width := range []int{1, 2, 3, 4, 5, 8, 16, 24, 40, 80} {
				name := fmt.Sprintf("plain=%t/working=%t/width=%d", plain, working, width)
				t.Run(name, func(t *testing.T) {
					styles := newStyles()
					if plain {
						styles.apply(termmd.ModePlain, termmd.ThemeDark)
					}
					status := newStatusLine(styles, "A very long conversation title that must be truncated")
					status.SetConnected("Auggie with a long server name")
					status.SetDisconnected("the websocket stream ended with a detailed reason")
					status.SetInFlight(working)
					status.SetWidth(width)

					got := status.Render()
					if strings.Contains(got, "\n") {
						t.Errorf("width %d rendered multiple rows: %q", width, got)
					}
					if measured := lipgloss.Width(got); measured > width {
						t.Errorf("width %d rendered %d cells: %q", width, measured, got)
					}
					if plain {
						assertNoANSI(t, name, got)
					}
					if width == 5 && working {
						stripped := ansi.Strip(got)
						if !strings.Contains(stripped, "×") || !strings.Contains(stripped, "◆") {
							t.Errorf("compact width lost state symbols: %q", stripped)
						}
					}
				})
			}
		}
	}
}
