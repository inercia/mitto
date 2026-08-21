package termmd

import "os"
import "testing"

// TestRealStdoutIsTerminal_MatchesRawCheck exercises the production
// implementation behind stdoutIsTerminalOverride directly (bypassing the
// test seam), recomputing the same os.ModeCharDevice check independently so
// the assertion is correct regardless of whether the test binary's stdout
// happens to be a TTY in a given CI/dev environment.
func TestRealStdoutIsTerminal_MatchesRawCheck(t *testing.T) {
	got := realStdoutIsTerminal()
	info, err := os.Stdout.Stat()
	want := err == nil && info.Mode()&os.ModeCharDevice != 0
	if got != want {
		t.Errorf("realStdoutIsTerminal() = %v, want %v (raw os.Stdout.Stat check)", got, want)
	}
}

// TestTerminalWidth_TTYCallsGetSize exercises TerminalWidth's TTY branch
// (term.GetSize on os.Stdout.Fd()), which TestTerminalWidth_FallbackWhenNotATTY
// deliberately never reaches. It asserts only that the result is positive
// rather than a specific value, since the test binary's actual stdout may or
// may not be a real terminal depending on how the suite is invoked — GetSize
// either fails (falling back to 55) or succeeds (returning the terminal's
// real width); both are valid outcomes and this keeps the test deterministic
// across environments while still covering the call.
func TestTerminalWidth_TTYCallsGetSize(t *testing.T) {
	restore := stdoutIsTerminalOverride
	stdoutIsTerminalOverride = func() bool { return true }
	t.Cleanup(func() { stdoutIsTerminalOverride = restore })

	if got := TerminalWidth(55); got <= 0 {
		t.Errorf("TerminalWidth(55) = %d, want a positive width", got)
	}
}

// TestResolveMode_TruthTable pins ResolveMode's precedence: --no-color flag,
// then $NO_COLOR, then TTY-ness, matching docs/devel/cli-conversation.md §7
// ("--no-color/NO_COLOR select glamour's notty style").
func TestResolveMode_TruthTable(t *testing.T) {
	cases := []struct {
		name        string
		noColorFlag bool
		noColorEnv  string
		isTerminal  bool
		want        Mode
	}{
		{"flag set wins even on a TTY", true, "", true, ModePlain},
		{"env set wins even on a TTY", false, "1", true, ModePlain},
		{"env set to non-empty junk still counts", false, "0", true, ModePlain},
		{"not a TTY, no flag, no env", false, "", false, ModePlain},
		{"TTY, no flag, no env", false, "", true, ModeStyled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tc.noColorEnv)
			restore := stdoutIsTerminalOverride
			stdoutIsTerminalOverride = func() bool { return tc.isTerminal }
			t.Cleanup(func() { stdoutIsTerminalOverride = restore })

			got := ResolveMode(tc.noColorFlag)
			if got != tc.want {
				t.Errorf("ResolveMode(%v) = %v, want %v", tc.noColorFlag, got, tc.want)
			}
		})
	}
}

// TestResolveTheme_TruthTable pins ResolveTheme's precedence (mitto-u7k3):
// explicit --style flag, then $GLAMOUR_STYLE (only exact "dark"/"light"),
// then terminal background detection (both stdin and stdout TTYs), then the
// ThemeDark fallback — matching ResolveTheme's doc comment and mirroring
// TestResolveMode_TruthTable's shape.
func TestResolveTheme_TruthTable(t *testing.T) {
	cases := []struct {
		name         string
		explicit     string
		glamourStyle string
		stdinTTY     bool
		stdoutTTY    bool
		hasDark      bool
		want         Theme
	}{
		{"explicit dark wins over everything", "dark", "light", true, true, false, ThemeDark},
		{"explicit light wins over everything", "light", "dark", true, true, true, ThemeLight},
		{"explicit auto falls through to env", "auto", "light", false, false, false, ThemeLight},
		{"explicit empty falls through to env", "", "dark", false, false, false, ThemeDark},
		{"env dark wins over detection", "", "dark", true, true, false, ThemeDark},
		{"env light wins over detection", "", "light", true, true, true, ThemeLight},
		{"env junk value is ignored, falls to detection", "", "dracula", true, true, true, ThemeDark},
		{"env style path is ignored, falls to detection", "", "/path/to/style.json", true, true, false, ThemeLight},
		{"detection: dark background", "", "", true, true, true, ThemeDark},
		{"detection: light background", "", "", true, true, false, ThemeLight},
		{"stdout not a TTY skips detection, falls to dark", "", "", true, false, false, ThemeDark},
		{"stdin not a TTY skips detection, falls to dark", "", "", false, true, false, ThemeDark},
		{"neither a TTY, no flag, no env", "", "", false, false, false, ThemeDark},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GLAMOUR_STYLE", tc.glamourStyle)

			restoreStdin := stdinIsTerminalOverride
			stdinIsTerminalOverride = func() bool { return tc.stdinTTY }
			t.Cleanup(func() { stdinIsTerminalOverride = restoreStdin })

			restoreStdout := stdoutIsTerminalOverride
			stdoutIsTerminalOverride = func() bool { return tc.stdoutTTY }
			t.Cleanup(func() { stdoutIsTerminalOverride = restoreStdout })

			restoreDark := hasDarkBackgroundOverride
			hasDarkBackgroundOverride = func() bool { return tc.hasDark }
			t.Cleanup(func() { hasDarkBackgroundOverride = restoreDark })

			got := ResolveTheme(tc.explicit)
			if got != tc.want {
				t.Errorf("ResolveTheme(%q) = %v, want %v", tc.explicit, got, tc.want)
			}
		})
	}
}

// TestTerminalWidth_FallbackWhenNotATTY pins TerminalWidth's fallback path:
// when stdout is not a TTY, the caller-supplied fallback (or defaultWidth if
// <= 0) is returned unconditionally, without touching term.GetSize.
func TestTerminalWidth_FallbackWhenNotATTY(t *testing.T) {
	restore := stdoutIsTerminalOverride
	stdoutIsTerminalOverride = func() bool { return false }
	t.Cleanup(func() { stdoutIsTerminalOverride = restore })

	if got := TerminalWidth(120); got != 120 {
		t.Errorf("TerminalWidth(120) = %d, want 120", got)
	}
	if got := TerminalWidth(0); got != defaultWidth {
		t.Errorf("TerminalWidth(0) = %d, want defaultWidth=%d", got, defaultWidth)
	}
	if got := TerminalWidth(-5); got != defaultWidth {
		t.Errorf("TerminalWidth(-5) = %d, want defaultWidth=%d", got, defaultWidth)
	}
}
