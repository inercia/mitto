package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/inercia/mitto/pkg/api"
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

func TestConversationChat_OptionalArgumentValidation(t *testing.T) {
	for _, args := range [][]string{nil, {"session-1"}} {
		if err := conversationChatCmd.Args(conversationChatCmd, args); err != nil {
			t.Errorf("Args(%v) = %v, want nil", args, err)
		}
	}
	if err := conversationChatCmd.Args(conversationChatCmd, []string{"session-1", "session-2"}); err == nil {
		t.Fatal("Args(two IDs) = nil, want a validation error")
	}
	if got := conversationChatCmd.Use; got != "chat [conversation-id]" {
		t.Errorf("Use = %q, want optional conversation ID syntax", got)
	}
}

func TestResolveChatConversationID_ExplicitIDBypassesListAndPicker(t *testing.T) {
	got, selected, err := resolveChatConversationID(
		[]string{"session-explicit"},
		func() ([]api.SessionInfo, error) {
			t.Fatal("explicit-ID path must not list sessions")
			return nil, nil
		},
		func([]api.SessionInfo) (string, bool, error) {
			t.Fatal("explicit-ID path must not open the picker")
			return "", false, nil
		},
	)
	if err != nil || !selected || got != "session-explicit" {
		t.Fatalf("resolve explicit ID = (%q, %t, %v), want (session-explicit, true, nil)", got, selected, err)
	}
}

func TestResolveChatConversationID_NoIDOutcomes(t *testing.T) {
	boom := errors.New("list failed")
	tests := []struct {
		name      string
		list      func() ([]api.SessionInfo, error)
		pick      chatConversationPicker
		wantID    string
		wantPick  bool
		wantError string
	}{
		{
			name: "selection",
			list: func() ([]api.SessionInfo, error) {
				return []api.SessionInfo{{SessionID: "session-1"}}, nil
			},
			pick: func(sessions []api.SessionInfo) (string, bool, error) {
				if len(sessions) != 1 || sessions[0].SessionID != "session-1" {
					t.Fatalf("picker candidates = %+v, want session-1", sessions)
				}
				return "session-1", true, nil
			},
			wantID:   "session-1",
			wantPick: true,
		},
		{
			name: "cancel",
			list: func() ([]api.SessionInfo, error) {
				return []api.SessionInfo{{SessionID: "session-1"}}, nil
			},
			pick: func([]api.SessionInfo) (string, bool, error) { return "", false, nil },
		},
		{
			name: "empty",
			list: func() ([]api.SessionInfo, error) { return nil, nil },
			pick: func([]api.SessionInfo) (string, bool, error) {
				t.Fatal("empty candidate set must not open picker")
				return "", false, nil
			},
			wantError: "no selectable conversations",
		},
		{
			name: "list failure",
			list: func() ([]api.SessionInfo, error) { return nil, boom },
			pick: func([]api.SessionInfo) (string, bool, error) {
				t.Fatal("list failure must not open picker")
				return "", false, nil
			},
			wantError: "list failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotPick, err := resolveChatConversationID(nil, tc.list, tc.pick)
			if gotID != tc.wantID || gotPick != tc.wantPick {
				t.Errorf("resolve = (%q, %t), want (%q, %t)", gotID, gotPick, tc.wantID, tc.wantPick)
			}
			if tc.wantError == "" && err != nil {
				t.Fatalf("resolve error = %v, want nil", err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("resolve error = %v, want substring %q", err, tc.wantError)
			}
		})
	}
}

func TestSelectableChatSessions_FiltersAndSortsRecentFirst(t *testing.T) {
	sessions := []api.SessionInfo{
		{SessionID: "invalid-time", ChildOrigin: "manual", UpdatedAt: "unknown"},
		{SessionID: "archived", Archived: true, UpdatedAt: "2026-08-11T15:00:00Z"},
		{SessionID: "human-old", UpdatedAt: "2026-08-10T12:00:00Z"},
		{SessionID: "auto", ChildOrigin: "auto", UpdatedAt: "2026-08-11T16:00:00Z"},
		{SessionID: "mcp-new", ChildOrigin: "mcp", UpdatedAt: "2026-08-11T14:00:00Z"},
	}

	got := selectableChatSessions(sessions)
	want := []string{"mcp-new", "human-old", "invalid-time"}
	if len(got) != len(want) {
		t.Fatalf("selectable sessions = %+v, want IDs %v", got, want)
	}
	for i, id := range want {
		if got[i].SessionID != id {
			t.Errorf("selectable[%d] = %q, want %q", i, got[i].SessionID, id)
		}
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
