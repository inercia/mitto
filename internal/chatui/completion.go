package chatui

import (
	"strings"
)

// completionMenu is the imperative struct backing slash-command tab
// completion, following the package's crush-style pattern (plain struct +
// methods called directly from Update, no nested tea.Model). It is only
// ever opened for a single-line input beginning with "/" (see
// Model.handleKey's tab handling).
type completionMenu struct {
	matches  []chatCommand
	selected int
	open     bool

	styles *styles
	width  int
}

func newCompletionMenu(styles *styles) *completionMenu {
	return &completionMenu{styles: styles}
}

func (c *completionMenu) SetWidth(w int) { c.width = w }

// Open reports whether the menu is currently showing matches.
func (c *completionMenu) Open() bool { return c.open }

// Close hides the menu without changing the input.
func (c *completionMenu) Close() {
	c.open = false
	c.matches = nil
	c.selected = 0
}

// Filter (re)populates matches for prefix (the full input value, expected
// to start with "/") against every command's name and aliases, opening the
// menu if at least one match is found. Matching is case-insensitive, as
// with mitto cli's completeInput.
func (c *completionMenu) Filter(prefix string) {
	prefix = strings.ToLower(prefix)
	c.matches = c.matches[:0]
	for _, cmd := range chatCommands {
		if strings.HasPrefix(cmd.name, prefix) {
			c.matches = append(c.matches, cmd)
			continue
		}
		for _, a := range cmd.aliases {
			if strings.HasPrefix(a, prefix) {
				c.matches = append(c.matches, cmd)
				break
			}
		}
	}
	c.selected = 0
	c.open = len(c.matches) > 0
}

// Next/Prev cycle the selection, wrapping around.
func (c *completionMenu) Next() {
	if len(c.matches) == 0 {
		return
	}
	c.selected = (c.selected + 1) % len(c.matches)
}

func (c *completionMenu) Prev() {
	if len(c.matches) == 0 {
		return
	}
	c.selected = (c.selected - 1 + len(c.matches)) % len(c.matches)
}

// Accept returns the currently selected match's canonical name (e.g.
// "/help"), and whether a match was available.
func (c *completionMenu) Accept() (string, bool) {
	if len(c.matches) == 0 {
		return "", false
	}
	return c.matches[c.selected].name, true
}

// SingleMatch returns the sole match's canonical name when exactly one
// command matches, used for tab's immediate-completion behavior (mirroring
// mitto cli's completeInput single-match convenience).
func (c *completionMenu) SingleMatch() (string, bool) {
	if len(c.matches) != 1 {
		return "", false
	}
	return c.matches[0].name, true
}

// Render draws the compact match list, selected entry highlighted.
func (c *completionMenu) Render() string {
	var b strings.Builder
	for i, m := range c.matches {
		line := m.name + "  " + m.description
		if i == c.selected {
			line = c.styles.statusOK.Render("> " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line)
		if i < len(c.matches)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
