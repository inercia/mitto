package termmd

import (
	"os"

	term "github.com/charmbracelet/x/term"
)

// defaultWidth is the wrap width used by TerminalWidth's fallback when
// stdout is not a TTY (e.g. piped output, CI) and the caller supplies no
// override. Matches glamour's own default word-wrap width.
const defaultWidth = 80

// stdoutIsTerminalOverride lets tests substitute a fake TTY-ness answer
// without needing a real terminal attached to the test binary's stdout.
// Production code always leaves this at its default (real os.Stdout.Stat).
var stdoutIsTerminalOverride = realStdoutIsTerminal

// ResolveMode centralises styled/plain selection so the mitto-pscc.7 chat
// TUI's panes and one-shot commands (mitto-pscc.6/.4) cannot drift apart.
// ModePlain (glamour's notty style) is selected when noColorFlag is set, or
// $NO_COLOR is set to any non-empty value, or stdout is not a TTY; otherwise
// ModeStyled. ResolveMode never returns ModeDegraded — a caller must select
// that explicitly for HTML-only legacy events (see RenderHTMLFallback).
func ResolveMode(noColorFlag bool) Mode {
	if noColorFlag {
		return ModePlain
	}
	if os.Getenv("NO_COLOR") != "" {
		return ModePlain
	}
	if !StdoutIsTerminal() {
		return ModePlain
	}
	return ModeStyled
}

// StdoutIsTerminal reports whether os.Stdout is an interactive terminal, via
// the same raw os.ModeCharDevice check internal/cmd's stdinIsTerminal uses
// for stdin (conversation_delete.go) — deliberately not golang.org/x/term
// for this specific check, kept consistent across the CLI package.
func StdoutIsTerminal() bool {
	return stdoutIsTerminalOverride()
}

// realStdoutIsTerminal is the production implementation of
// stdoutIsTerminalOverride.
func realStdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// TerminalWidth returns the current terminal width for os.Stdout, or
// fallback when stdout is not a TTY or its size cannot be determined. A
// non-positive fallback is replaced by defaultWidth. Render itself never
// calls this — callers resolve the width once (or on tea.WindowSizeMsg, for
// .7) and pass it through Options.Width, keeping Render deterministic.
func TerminalWidth(fallback int) int {
	if fallback <= 0 {
		fallback = defaultWidth
	}
	if !StdoutIsTerminal() {
		return fallback
	}
	w, _, err := term.GetSize(os.Stdout.Fd())
	if err != nil || w <= 0 {
		return fallback
	}
	return w
}
