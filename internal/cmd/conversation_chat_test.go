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
