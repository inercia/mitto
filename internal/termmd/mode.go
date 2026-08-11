package termmd

import (
	"os"

	lipgloss "charm.land/lipgloss/v2"
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

// stdinIsTerminalOverride mirrors stdoutIsTerminalOverride for os.Stdin,
// used only by ResolveTheme's background-detection guard below (detecting
// the background color requires both stdin and stdout to be TTYs).
var stdinIsTerminalOverride = realStdinIsTerminal

// hasDarkBackgroundOverride lets tests substitute a fake background-color
// answer without querying a real terminal via OSC 11. Production code
// always leaves this at its default (real lipgloss.HasDarkBackground).
var hasDarkBackgroundOverride = realHasDarkBackground

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

// ResolveTheme centralises ModeStyled's dark/light palette selection
// (mitto-u7k3), so the chat TUI and one-shot commands cannot drift apart —
// mirroring ResolveMode's role for styled/plain. Precedence, highest first:
//
//  1. explicit is "dark" or "light" (from --style; any other value,
//     including "auto" or "", falls through)
//  2. $GLAMOUR_STYLE, only when it is exactly "light" or "dark" — any other
//     value (a named style like "dracula", or a style file path) is
//     ignored here: passing arbitrary glamour styles through is a distinct
//     feature this bead does not build
//  3. terminal background detection (hasDarkBackgroundOverride), when both
//     stdin and stdout are TTYs (a raw-mode OSC 11 query needs both)
//  4. ThemeDark — glamour's own fallback, and also what
//     lipgloss.HasDarkBackground itself returns on any detection error
func ResolveTheme(explicit string) Theme {
	if t, ok := parseThemeName(explicit); ok {
		return t
	}
	if t, ok := parseThemeName(os.Getenv("GLAMOUR_STYLE")); ok {
		return t
	}
	if StdinIsTerminal() && StdoutIsTerminal() {
		if hasDarkBackgroundOverride() {
			return ThemeDark
		}
		return ThemeLight
	}
	return ThemeDark
}

// parseThemeName maps "dark"/"light" (case-sensitive, matching glamour's own
// style-name convention) to a Theme. Any other value, including "auto" and
// "", reports ok=false so the caller falls through to the next precedence
// step.
func parseThemeName(name string) (Theme, bool) {
	switch name {
	case "dark":
		return ThemeDark, true
	case "light":
		return ThemeLight, true
	default:
		return ThemeDark, false
	}
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

// StdinIsTerminal reports whether os.Stdin is an interactive terminal, via
// the same raw os.ModeCharDevice check StdoutIsTerminal uses — kept in this
// package (rather than reusing internal/cmd's identical stdinIsTerminal) so
// ResolveTheme's detection guard below does not need a CLI-package import.
func StdinIsTerminal() bool {
	return stdinIsTerminalOverride()
}

// realStdinIsTerminal is the production implementation of
// stdinIsTerminalOverride.
func realStdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// realHasDarkBackground is the production implementation of
// hasDarkBackgroundOverride: it queries the terminal via OSC 11 (blocking,
// raw-mode) through lipgloss.HasDarkBackground. Callers must already know
// both stdin and stdout are TTYs (see ResolveTheme) — lipgloss itself
// returns true (its documented dark-on-error default) when either is not.
func realHasDarkBackground() bool {
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
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
