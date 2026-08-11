package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// withChatNonTTYStdin replaces the real os.Stdin with a pipe whose write end
// is immediately closed, so reads see EOF right away. Named distinctly from
// conversation_lifecycle_integration_test.go's withNonTTYStdin (an
// integration-tagged file) so both compile together without a symbol clash
// when the suite runs with `-tags integration`.
//
// conversation_chat.go's stdinIsTerminal() (conversation_delete.go) checks
// the REAL os.Stdin, not cobra's injected stdin, so this must swap the real
// file descriptor rather than rely on rootCmd.SetIn.
func withChatNonTTYStdin(t *testing.T) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe write end: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
	})
}

// TestConversationChat_NonTTYStdin_ExitsUsageError covers the Plan's
// "requires a TTY" precondition (docs/devel/cli-conversation.md, this
// bead's description): a non-interactive stdin must exit 2 (usage) pointing
// at "conversation send --wait", before any dialing to a server — so this
// test needs no --url/--token and no running server. It only needs stdin
// itself to be non-a-TTY: the check is `!stdinIsTerminal() ||
// !termmd.StdoutIsTerminal()` (OR), and go test's own stdout is not a TTY
// in virtually every CI/dev invocation, but this test does not rely on that
// — forcing stdin alone is sufficient to trip the check regardless of the
// test binary's stdout.
func TestConversationChat_NonTTYStdin_ExitsUsageError(t *testing.T) {
	withChatNonTTYStdin(t)

	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"conversation", "chat", "20260101-000000-deadbeef"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a non-interactive stdin")
	}
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	if ec.ExitCode() != exitUsage {
		t.Errorf("ExitCode() = %d, want exitUsage (%d)", ec.ExitCode(), exitUsage)
	}
	if !strings.Contains(err.Error(), "conversation send --wait") {
		t.Errorf("error should point at the non-interactive alternative: %v", err)
	}
}

// TestConversationChat_Flags pins the documented flags/defaults (Plan:
// --history N replays recent events, --no-thoughts hides thinking events)
// so the CLI contract does not silently drift.
func TestConversationChat_Flags(t *testing.T) {
	cases := map[string]string{
		"history":     "20",
		"no-thoughts": "false",
	}
	for name, want := range cases {
		f := conversationChatCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("flag --%s not registered", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("--%s default = %q, want %q", name, f.DefValue, want)
		}
	}
}

// TestConversationChat_StylePersistentFlag pins --style as a persistent flag
// on the "conversation" group (mitto-u7k3), defaulting to "auto" — so every
// subcommand, not just chat, sees the same palette selector.
func TestConversationChat_StylePersistentFlag(t *testing.T) {
	f := conversationCmd.PersistentFlags().Lookup("style")
	if f == nil {
		t.Fatal("persistent flag --style not registered on conversationCmd")
	}
	if f.DefValue != "auto" {
		t.Errorf("--style default = %q, want %q", f.DefValue, "auto")
	}
	if conversationChatCmd.Flags().Lookup("style") == nil {
		t.Error("--style should be visible to the chat subcommand via inheritance")
	}
}

// TestResolveChatStyle pins the flag > $GLAMOUR_STYLE > "auto" precedence
// that resolveChatStyle applies before the TUI starts (mitto-u7k3). Only the
// exact values "dark"/"light" are honored at either level; anything else
// (including "auto", "", a named glamour style, or a style file path) falls
// through, ultimately to "auto" so chatui.Model does its own
// tea.RequestBackgroundColor detection.
func TestResolveChatStyle(t *testing.T) {
	cases := []struct {
		name  string
		flag  string
		env   string
		unset bool
		want  string
	}{
		{name: "flag dark wins over env light", flag: "dark", env: "light", want: "dark"},
		{name: "flag light wins over env dark", flag: "light", env: "dark", want: "light"},
		{name: "flag auto falls through to env dark", flag: "auto", env: "dark", want: "dark"},
		{name: "flag auto falls through to env light", flag: "auto", env: "light", want: "light"},
		{name: "empty flag falls through to env", flag: "", env: "light", want: "light"},
		{name: "unknown flag falls through to env", flag: "dracula", env: "dark", want: "dark"},
		{name: "flag auto, no env", flag: "auto", unset: true, want: "auto"},
		{name: "flag auto, unrelated env style", flag: "auto", env: "dracula", want: "auto"},
		{name: "flag auto, env style path", flag: "auto", env: "/tmp/custom.json", want: "auto"},
		{name: "flag auto, empty env", flag: "auto", env: "", want: "auto"},
		{name: "case-sensitive: Dark is not dark", flag: "Dark", unset: true, want: "auto"},
		{name: "case-sensitive env: LIGHT is not light", flag: "auto", env: "LIGHT", want: "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unset {
				t.Setenv("GLAMOUR_STYLE", "")
				if err := os.Unsetenv("GLAMOUR_STYLE"); err != nil {
					t.Fatalf("unsetting GLAMOUR_STYLE: %v", err)
				}
			} else {
				t.Setenv("GLAMOUR_STYLE", tc.env)
			}
			if got := resolveChatStyle(tc.flag); got != tc.want {
				t.Errorf("resolveChatStyle(%q) with GLAMOUR_STYLE=%q = %q, want %q",
					tc.flag, tc.env, got, tc.want)
			}
		})
	}
}
