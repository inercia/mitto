package termmd

import "testing"

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
