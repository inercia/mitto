package chatui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// chatCommand describes one slash command available in the chat TUI: its
// canonical name (including the leading "/"), any aliases (also including
// the leading "/"), and a short description shown by /help and the
// completion menu.
type chatCommand struct {
	name        string
	aliases     []string
	description string
}

// chatCommands is the TUI's slash-command set. It mirrors mitto cli's
// parity set (internal/cmd/cli.go slashCommands: /help, /quit, /cancel)
// plus /clear, a TUI-only affordance with no cli counterpart (clearing an
// alt-screen transcript pane has no analogue in the scrolling terminal cli
// writes to).
var chatCommands = []chatCommand{
	{name: "/help", aliases: []string{"/h", "/?"}, description: "Show available commands"},
	{name: "/quit", aliases: []string{"/exit", "/q"}, description: "Exit the chat"},
	{name: "/cancel", description: "Cancel the current operation"},
	{name: "/clear", description: "Clear the transcript pane"},
}

// matchesCommand reports whether word (already lowercased, leading "/"
// included) is cmd's canonical name or one of its aliases.
func matchesCommand(cmd chatCommand, word string) bool {
	if cmd.name == word {
		return true
	}
	for _, a := range cmd.aliases {
		if a == word {
			return true
		}
	}
	return false
}

// executeSlashCommand runs a line starting with "/" locally and returns
// true if it was recognized (whether or not it produced a Cmd) — a
// recognized command is always consumed here and never forwarded to the
// agent. An unrecognized "/word" is reported as an error item (matching
// mitto cli's "Unknown command" behavior) rather than sent, so a typo
// cannot leak into the conversation.
func (m *Model) executeSlashCommand(line string) (handled bool, cmd tea.Cmd) {
	word := strings.ToLower(strings.Fields(line)[0])

	for _, c := range chatCommands {
		if !matchesCommand(c, word) {
			continue
		}
		switch c.name {
		case "/help":
			m.transcript.AppendSystem(renderHelp())
		case "/quit":
			m.quitting = true
			return true, tea.Quit
		case "/cancel":
			return true, m.cancelCmd()
		case "/clear":
			m.transcript.Clear()
		}
		return true, nil
	}

	m.transcript.AppendError("Unknown command: " + word + " (use /help for available commands)")
	return true, nil
}

// renderHelp builds the /help output listing every command and its
// aliases.
func renderHelp() string {
	var b strings.Builder
	b.WriteString("Available commands:\n")
	for _, c := range chatCommands {
		b.WriteString(c.name)
		for _, a := range c.aliases {
			b.WriteString(", " + a)
		}
		b.WriteString("  — " + c.description + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
