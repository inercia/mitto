//go:build integration

package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	client "github.com/inercia/mitto/pkg/api"
)

// notFoundSessionID is a well-formed (regex-valid) session ID that is never
// created by any test in this file, so the server's session-ID format
// validation (internal/web/handlers/session_id.go) lets the request through
// to the actual "does it exist" check, which then 404s as intended. A
// malformed ID like "does-not-exist" would 400 (invalid format) instead,
// masking the not-found path this file means to exercise.
const notFoundSessionID = "20260101-000000-deadbeef"

// withNonTTYStdin replaces the real os.Stdin (for the duration of the test)
// with a pipe whose write end is immediately closed, so reads see EOF right
// away. conversation_delete.go's stdinIsTerminal() deliberately checks the
// REAL os.Stdin (not cmd.InOrStdin()) to decide whether it's safe to prompt,
// so a test asserting the non-interactive refusal path must control the
// real file descriptor rather than cobra's injected stdin — otherwise the
// outcome depends on whether the test binary happens to inherit a TTY from
// its parent process (flaky across environments/launchers).
func withNonTTYStdin(t *testing.T) {
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

// This file exercises `mitto conversation new/list/get/delete` end-to-end
// against a real (in-process) web server + mock ACP agent, reusing the
// setupSendTestServer harness from conversation_send_integration_test.go
// (mitto-pscc.5's stated test scope: integration tests against the
// in-process test server for each verb, plus exit-code assertions for
// not-found (5) and unreachable (3)).

// resetLifecycleCmdState resets the package-level flag state for the four
// lifecycle commands plus their Changed() bits, mirroring resetSendCmdState
// so successive rootCmd.Execute calls in the same test binary don't leak
// flags across invocations.
func resetLifecycleCmdState() {
	conversationFlags = serverFlags{Output: "table", Timeout: 30 * time.Second}
	conversationNewFlags = newFlags{WaitTimeout: 10 * time.Minute}
	conversationListFlags = listFlags{}
	conversationDeleteFlags = deleteFlags{}
	conversationCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	conversationNewCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	conversationListCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	conversationGetCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	conversationDeleteCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

// runConv invokes `mitto conversation <verb> <args...>` through the real
// rootCmd, capturing stdout/stderr, and optionally feeding stdin.
func runConv(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetLifecycleCmdState()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	if stdin != "" {
		rootCmd.SetIn(strings.NewReader(stdin))
	} else {
		rootCmd.SetIn(strings.NewReader(""))
	}
	rootCmd.SetArgs(append([]string{"conversation"}, args...))
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// --- new ---------------------------------------------------------------

func TestConversationNewIntegration_CreatesSession(t *testing.T) {
	url := setupSendTestServer(t)

	stdout, _, err := runConv(t, "", "new", "--title", "my new conv",
		"--url", url, "--token", "x", "--api-prefix", "/mitto", "--output", "json")
	if err != nil {
		t.Fatalf("conversation new: %v", err)
	}
	if !strings.Contains(stdout, `"name": "my new conv"`) {
		t.Errorf("stdout = %q, want the title echoed back", stdout)
	}
	if !strings.Contains(stdout, `"session_id"`) {
		t.Errorf("stdout = %q, want a session_id field", stdout)
	}
}

func TestConversationNewIntegration_WithPromptAndWait(t *testing.T) {
	url := setupSendTestServer(t)

	stdout, _, err := runConv(t, "", "new", "--prompt", "conversation new --wait integration payload",
		"--url", url, "--token", "x", "--api-prefix", "/mitto",
		"--wait", "--wait-timeout", "20s")
	if err != nil {
		t.Fatalf("conversation new --wait: %v", err)
	}
	if !strings.Contains(stdout, "conversation new --wait integration payload") || !strings.Contains(stdout, "mock response") {
		t.Errorf("stdout = %q, want the mock agent's echoed reply", stdout)
	}
}

func TestConversationNewIntegration_MutuallyExclusiveFlagsIsUsageError(t *testing.T) {
	_, _, err := runConv(t, "", "new", "--prompt", "a", "--prompt-name", "b",
		"--url", "http://unused.invalid", "--token", "x", "--api-prefix", "/mitto")
	if got := exitCodeOf(t, err); got != exitUsage {
		t.Errorf("exit code = %d, want exitUsage (%d)", got, exitUsage)
	}
}

// --- list ----------------------------------------------------------------

func TestConversationListIntegration_ListsAndFiltersByDir(t *testing.T) {
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	stdout, _, err := runConv(t, "", "list", "--url", url, "--token", "x", "--api-prefix", "/mitto", "--output", "json")
	if err != nil {
		t.Fatalf("conversation list: %v", err)
	}
	if !strings.Contains(stdout, sessionID) {
		t.Errorf("stdout = %q, want it to contain the created session id %q", stdout, sessionID)
	}

	stdout, _, err = runConv(t, "", "list", "--dir", "/no/such/dir", "--url", url, "--token", "x", "--api-prefix", "/mitto", "--output", "json")
	if err != nil {
		t.Fatalf("conversation list --dir: %v", err)
	}
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("stdout = %q, want an empty JSON array for a non-matching --dir", stdout)
	}
}

// --- get -------------------------------------------------------------------

func TestConversationGetIntegration_ReturnsComposedDetails(t *testing.T) {
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	stdout, _, err := runConv(t, "", "get", sessionID, "--url", url, "--token", "x", "--api-prefix", "/mitto", "--output", "json")
	if err != nil {
		t.Fatalf("conversation get: %v", err)
	}
	if !strings.Contains(stdout, sessionID) {
		t.Errorf("stdout = %q, want it to contain the session id", stdout)
	}
	if strings.Contains(stdout, `"loop"`) {
		t.Errorf("stdout = %q, want the omitempty loop field absent for a session with no loop configured", stdout)
	}
	if !strings.Contains(stdout, `"queue_depth": 0`) {
		t.Errorf("stdout = %q, want queue_depth 0 for a freshly created session", stdout)
	}
}

func TestConversationGetIntegration_NotFoundExits5(t *testing.T) {
	url := setupSendTestServer(t)

	_, _, err := runConv(t, "", "get", notFoundSessionID, "--url", url, "--token", "x", "--api-prefix", "/mitto")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if got := exitCodeOf(t, err); got != exitNotFound {
		t.Errorf("exit code = %d, want exitNotFound (%d)", got, exitNotFound)
	}
}

func TestConversationGetIntegration_UnreachableExits3(t *testing.T) {
	_, _, err := runConv(t, "", "get", "some-id", "--url", "http://127.0.0.1:1", "--token", "x", "--api-prefix", "/mitto", "--timeout", "500ms")
	if err == nil {
		t.Fatal("expected an unreachable error")
	}
	if got := exitCodeOf(t, err); got != exitUnreachable {
		t.Errorf("exit code = %d, want exitUnreachable (%d)", got, exitUnreachable)
	}
}

// --- delete ------------------------------------------------------------

func TestConversationDeleteIntegration_ForceDeletes(t *testing.T) {
	url := setupSendTestServer(t)
	c := client.New(url, client.WithBearerToken("x"))
	sess, err := c.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	stdout, stderr, err := runConv(t, "", "delete", sess.SessionID, "--force",
		"--url", url, "--token", "x", "--api-prefix", "/mitto", "--output", "json")
	if err != nil {
		t.Fatalf("conversation delete --force: %v", err)
	}
	if !strings.Contains(stderr, "deleted") {
		t.Errorf("stderr = %q, want a deletion confirmation", stderr)
	}
	if !strings.Contains(stdout, `"deleted": true`) {
		t.Errorf("stdout = %q, want deleted:true", stdout)
	}

	if _, err := c.GetSession(sess.SessionID); err == nil {
		t.Error("expected GetSession to fail after deletion")
	}
}

func TestConversationDeleteIntegration_NonTTYWithoutForceExits2(t *testing.T) {
	withNonTTYStdin(t)
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	_, _, err := runConv(t, "", "delete", sessionID, "--url", url, "--token", "x", "--api-prefix", "/mitto")
	if err == nil {
		t.Fatal("expected a refusal error without --force on non-interactive stdin")
	}
	if got := exitCodeOf(t, err); got != exitUsage {
		t.Errorf("exit code = %d, want exitUsage (%d)", got, exitUsage)
	}
}

func TestConversationDeleteIntegration_NotFoundExits5(t *testing.T) {
	url := setupSendTestServer(t)

	_, _, err := runConv(t, "", "delete", notFoundSessionID, "--force", "--url", url, "--token", "x", "--api-prefix", "/mitto")
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if got := exitCodeOf(t, err); got != exitNotFound {
		t.Errorf("exit code = %d, want exitNotFound (%d)", got, exitNotFound)
	}
}
