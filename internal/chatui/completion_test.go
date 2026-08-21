package chatui

import (
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inercia/mitto/internal/termmd"
)

func TestCompletionMenu_Filter_MatchesNamesAndAliases(t *testing.T) {
	m := newCompletionMenu(newStyles())

	m.Filter("/h")
	if !m.Open() {
		t.Fatal("/h should match /help (name) and /h (alias)")
	}
	if got := len(m.matches); got != 1 {
		t.Fatalf("len(matches) = %d, want 1 (/help, matched via its /h alias, deduped)", got)
	}
	if m.matches[0].name != "/help" {
		t.Errorf("matches[0].name = %q, want /help", m.matches[0].name)
	}
}

func TestCompletionMenu_Filter_NoMatchCloses(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/nonexistent")
	if m.Open() {
		t.Error("no matches should leave the menu closed")
	}
}

func TestCompletionMenu_Filter_SingleMatch(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/ca")
	if got := len(m.matches); got != 1 {
		// Only /cancel starts with "/ca" among the builtin set.
		t.Fatalf("len(matches) = %d, want 1 (/cancel)", got)
	}
	if _, ok := m.SingleMatch(); !ok {
		t.Error("a single match should be reported by SingleMatch")
	}
}

func TestCompletionMenu_Filter_MultiplePrefixMatches(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/c") // both /cancel and /clear start with "/c"
	if got := len(m.matches); got != 2 {
		t.Fatalf("len(matches) = %d, want 2 (/cancel, /clear)", got)
	}
	if _, ok := m.SingleMatch(); ok {
		t.Error("SingleMatch should be false with two matches")
	}
}

func TestCompletionMenu_SingleMatch_FalseWhenMultiple(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/") // matches every command
	if _, ok := m.SingleMatch(); ok {
		t.Error("SingleMatch should be false when multiple commands match")
	}
}

func TestCompletionMenu_NextPrev_CycleWithWraparound(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/") // every command matches; len >= 2
	n := len(m.matches)
	if n < 2 {
		t.Fatalf("test requires >=2 matches, got %d", n)
	}

	for i := 0; i < n; i++ {
		m.Next()
	}
	if m.selected != 0 {
		t.Errorf("after cycling Next() exactly len(matches) times, selected = %d, want 0 (wrapped)", m.selected)
	}

	m.Prev()
	if m.selected != n-1 {
		t.Errorf("Prev() from 0 should wrap to the last index, selected = %d, want %d", m.selected, n-1)
	}
}

func TestCompletionMenu_Accept_ReturnsSelectedName(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/ca")
	name, ok := m.Accept()
	if !ok || name != "/cancel" {
		t.Errorf("Accept() = (%q, %v), want (/cancel, true)", name, ok)
	}
}

func TestCompletionMenu_Render_ListsMatchesWithSelectionHighlighted(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/c") // both /cancel and /clear start with "/c"

	out := m.Render()

	if !strings.Contains(out, "/cancel") || !strings.Contains(out, "/clear") {
		t.Fatalf("Render() should list every match, got:\n%s", out)
	}
	if !strings.Contains(out, "Cancel the current operation") {
		t.Errorf("Render() should include each match's description, got:\n%s", out)
	}
	lines := strings.Split(out, "\n")
	if got := len(lines); got != 2 {
		t.Fatalf("Render() should emit one line per match, got %d lines:\n%s", got, out)
	}

	m.Next() // select /clear
	out2 := m.Render()
	if out == out2 {
		t.Error("Render() should change when the selection moves (highlight follows selected)")
	}
}

func TestCompletionMenu_Render_PlainSelectionMarkerFollowsSelection(t *testing.T) {
	styles := newStyles()
	styles.apply(termmd.ModePlain, termmd.ThemeDark)
	m := newCompletionMenu(styles)
	m.SetWidth(80)
	m.Filter("/c")

	lines := strings.Split(m.Render(), "\n")
	if !strings.HasPrefix(lines[0], "> /cancel") || !strings.HasPrefix(lines[1], "  /clear") {
		t.Fatalf("plain selection marker should identify the first row, got %q", lines)
	}
	m.Next()
	lines = strings.Split(m.Render(), "\n")
	if !strings.HasPrefix(lines[0], "  /cancel") || !strings.HasPrefix(lines[1], "> /clear") {
		t.Fatalf("plain selection marker should follow Next, got %q", lines)
	}
	for _, line := range lines {
		assertNoANSI(t, "plain completion row", line)
	}
}

func TestCompletionMenu_Render_UsesCommandDescriptionHierarchy(t *testing.T) {
	styles := newStyles()
	m := newCompletionMenu(styles)
	m.Filter("/c")
	m.Next() // Leave /cancel unselected so its command and description use hierarchy styles.

	out := m.Render()
	if !strings.Contains(out, styles.completionCommandStyle.Render("/cancel")) {
		t.Errorf("completion output missing semantic command treatment:\n%s", out)
	}
	wantDescription := styles.completionDescriptionStyle.Render("  Cancel the current operation")
	if !strings.Contains(out, wantDescription) {
		t.Errorf("completion output missing semantic description treatment:\n%s", out)
	}
}

func TestCompletionMenu_Render_ClampsEveryRowToTerminalWidth(t *testing.T) {
	for _, width := range []int{1, 2, 8, 12} {
		m := newCompletionMenu(newStyles())
		m.SetWidth(width)
		m.Filter("/c")
		for _, line := range strings.Split(m.Render(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width %d completion row measured %d cells: %q", width, got, ansi.Strip(line))
			}
		}
	}
}

func TestCompletionMenu_Close_ClearsState(t *testing.T) {
	m := newCompletionMenu(newStyles())
	m.Filter("/ca")
	m.Close()
	if m.Open() {
		t.Error("Close should leave the menu closed")
	}
	if _, ok := m.Accept(); ok {
		t.Error("Accept after Close should report no match")
	}
}
