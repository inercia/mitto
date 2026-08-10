//go:build integration

package cmd

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/web"
	client "github.com/inercia/mitto/pkg/api"
)

// This file exercises `mitto conversation send [--wait]` end-to-end against a
// real (in-process) web server + mock ACP agent, mirroring the harness in
// tests/integration/inprocess/setup_test.go. It lives in package cmd (rather
// than tests/integration/inprocess) so it can drive the actual
// runConversationSend/waitForQueuedMessage/queueGate code via rootCmd.Execute,
// per mitto-pscc.6's Plan comment (integration cases: idle dispatch, busy
// correlation, --wait completion, --wait-timeout).

// findRepoRootForCmdTest walks up from the working directory to the nearest
// go.mod, the same heuristic tests/integration/cli/cli_test.go uses. Needed
// because this file's candidate scenario-dir lookup below must be absolute:
// the mock ACP server's own relative-path fallback
// (tests/fixtures/responses, ../fixtures/responses, ../../fixtures/responses)
// is resolved against ITS OWN cwd, which only happens to land on the repo's
// tests/fixtures/responses for packages nested exactly like
// tests/integration/inprocess — not for internal/cmd.
func findRepoRootForCmdTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod)")
		}
		dir = parent
	}
}

// findMockACPServerForCmdTest locates tests/mocks/acp-server/mock-acp-server
// under the repo root.
func findMockACPServerForCmdTest(t *testing.T, repoRoot string) string {
	t.Helper()
	p := filepath.Join(repoRoot, "tests", "mocks", "acp-server", "mock-acp-server")
	if _, err := os.Stat(p); err != nil {
		t.Skip("mock-acp-server not found. Run 'make build-mock-acp' first")
	}
	return p
}

// setupSendTestServer starts an in-process Mitto web server backed by the
// mock ACP server and returns its base URL. Cleaned up via t.Cleanup.
func setupSendTestServer(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmpDir)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)

	repoRoot := findRepoRootForCmdTest(t)
	mockACPBinary := findMockACPServerForCmdTest(t, repoRoot)
	scenariosDir := filepath.Join(repoRoot, "tests", "fixtures", "responses")

	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll workspace: %v", err)
	}

	// Pass --scenarios as an absolute path: the mock server's own relative
	// fallback candidates are resolved against its process cwd (the
	// session's working directory here), not this test binary's cwd, so
	// fixtures like "Simulate a slow response" would otherwise silently fall
	// back to the generic canned reply.
	mockACPCmd := mockACPBinary + ` --scenarios "` + scenariosDir + `"`

	mittoConfig := &config.Config{
		ACPServers: []config.ACPServer{{Name: "mock-acp", Command: mockACPCmd}},
	}

	webConfig := web.Config{
		Workspaces: []config.WorkspaceSettings{
			{ACPServer: "mock-acp", WorkingDir: workspaceDir},
		},
		ACPCommand:              mockACPCmd,
		ACPServer:               "mock-acp",
		DefaultWorkingDir:       workspaceDir,
		AutoApprove:             true,
		FromCLI:                 true,
		MittoConfig:             mittoConfig,
		DisableAuxiliaryPrewarm: true,
	}

	srv, err := web.NewServer(webConfig)
	if err != nil {
		t.Fatalf("web.NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	httpServer := httptest.NewServer(srv.Handler())
	t.Cleanup(httpServer.Close)

	return httpServer.URL
}

// resetSendCmdState resets the package-level flag state that
// runConversationSend reads, plus the Changed() bit on every flag the
// conversation/send command trees register, so successive rootCmd.Execute
// calls in the same test binary don't leak flags across invocations (cobra
// does not do this automatically for pointer-bound package vars).
func resetSendCmdState() {
	conversationFlags = serverFlags{Output: "table", Timeout: 30 * time.Second}
	conversationSendFlags = sendFlags{WaitTimeout: 10 * time.Minute}
	conversationCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	conversationSendCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

// runSend invokes `mitto conversation send <args...>` through the real
// rootCmd (so PersistentPreRunE's conversation/auth config-load skip and all
// of runConversationSend's flag-resolution logic run exactly as in
// production), capturing stdout/stderr.
func runSend(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetSendCmdState()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(append([]string{"conversation", "send"}, args...))
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func createTestSession(t *testing.T, serverURL string) string {
	t.Helper()
	c := client.New(serverURL)
	sess, err := c.CreateSession(client.CreateSessionRequest{})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	t.Cleanup(func() { _ = c.DeleteSession(sess.SessionID) })
	return sess.SessionID
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	var ec *exitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected *exitCodeError, got %T: %v", err, err)
	}
	return ec.ExitCode()
}

// TestConversationSendIntegration_NoWait_EnqueuesAndReturns covers the
// idle-session no-wait path: the command must succeed immediately and print
// the queued message (id, queued_at, message).
func TestConversationSendIntegration_NoWait_EnqueuesAndReturns(t *testing.T) {
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	stdout, _, err := runSend(t, sessionID, "hello", "--url", url, "--token", "x", "--api-prefix", "/mitto", "--output", "json")
	if err != nil {
		t.Fatalf("conversation send: %v", err)
	}
	if !strings.Contains(stdout, `"message": "hello"`) {
		t.Errorf("stdout = %q, want the queued message echoed back", stdout)
	}
	if !strings.Contains(stdout, `"id": "q-`) {
		t.Errorf("stdout = %q, want a queue message id", stdout)
	}
}

// TestConversationSendIntegration_Wait_ReturnsOnCompletion is the core
// regression test for the LoadEvents/AddObserver fix: without it this
// command would hang until --wait-timeout on every idle-session turn (see
// the "Testing" bead comment on mitto-pscc.6 for the root cause).
func TestConversationSendIntegration_Wait_ReturnsOnCompletion(t *testing.T) {
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	// Avoid any fixture trigger pattern (tests/fixtures/responses/*.json) so
	// the mock server's generic default reply ("I received your message: X")
	// is what comes back, deterministically.
	stdout, _, err := runSend(t, sessionID, "conversation send integration test payload",
		"--url", url, "--token", "x", "--api-prefix", "/mitto",
		"--wait", "--wait-timeout", "20s")
	if err != nil {
		t.Fatalf("conversation send --wait: %v", err)
	}
	if !strings.Contains(stdout, "conversation send integration test payload") || !strings.Contains(stdout, "mock response") {
		t.Errorf("stdout = %q, want the mock agent's echoed reply", stdout)
	}
}

// TestConversationSendIntegration_Wait_BusyCorrelatesRightTurn asserts
// decision 2 from the Plan comment: on a session already streaming a decoy
// turn, --wait must report OUR message's completion, not the decoy's.
func TestConversationSendIntegration_Wait_BusyCorrelatesRightTurn(t *testing.T) {
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	// Kick off a slow decoy turn (~4s of streaming) without waiting.
	if _, _, err := runSend(t, sessionID, "Simulate a slow response", "--url", url, "--token", "x", "--api-prefix", "/mitto"); err != nil {
		t.Fatalf("decoy send: %v", err)
	}

	// While busy, enqueue+wait for a second, distinct message.
	stdout, _, err := runSend(t, sessionID, "Second message",
		"--url", url, "--token", "x", "--api-prefix", "/mitto",
		"--wait", "--wait-timeout", "30s")
	if err != nil {
		t.Fatalf("conversation send --wait (busy session): %v", err)
	}
	if !strings.Contains(stdout, "Second message") {
		t.Errorf("stdout = %q, want it to report OUR message's reply, not the decoy's", stdout)
	}
	if strings.Contains(stdout, "Starting a slow response") {
		t.Errorf("stdout = %q, leaked the decoy turn's content", stdout)
	}
}

// TestConversationSendIntegration_Wait_TimesOutWithoutCancelling asserts
// decision 3: a --wait-timeout expiry surfaces as exit 6 while the queued
// turn keeps running server-side (the CLI process never calls Session.Cancel).
func TestConversationSendIntegration_Wait_TimesOutWithoutCancelling(t *testing.T) {
	url := setupSendTestServer(t)
	sessionID := createTestSession(t, url)

	_, _, err := runSend(t, sessionID, "Simulate a slow response",
		"--url", url, "--token", "x", "--api-prefix", "/mitto",
		"--wait", "--wait-timeout", "300ms")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if got := exitCodeOf(t, err); got != exitWaitTimeout {
		t.Errorf("exit code = %d, want exitWaitTimeout (%d)", got, exitWaitTimeout)
	}

	// The agent must still be running (not cancelled by the timed-out
	// client): a follow-up message queues behind the still-streaming decoy
	// turn and, once it eventually finishes, is dispatched and completes
	// normally — proving the decoy was never interrupted.
	stdout, _, werr := runSend(t, sessionID, "after the decoy finishes",
		"--url", url, "--token", "x", "--api-prefix", "/mitto",
		"--wait", "--wait-timeout", "20s")
	if werr != nil {
		t.Fatalf("follow-up conversation send --wait: %v", werr)
	}
	if !strings.Contains(stdout, "after the decoy finishes") {
		t.Errorf("stdout = %q, want the follow-up message's own reply once the decoy completes", stdout)
	}
}
