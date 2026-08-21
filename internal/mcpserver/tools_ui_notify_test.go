package mcpserver

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/inercia/mitto/internal/session"
)

// newServerWithNotifyPrompter builds a server + registered session with the
// CanPromptUser flag set, returning the server, session ID, and the mock
// UIPrompter attached to that session so the session-scoped UINotify path
// (handleUINotify) can be tested in isolation.
func newServerWithNotifyPrompter(t *testing.T) (*Server, string, *mockUIPrompter) {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: &mockSessionManager{}})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	sessionID := session.GenerateSessionID()
	meta := session.Metadata{
		SessionID:  sessionID,
		Name:       "notify-test",
		ACPServer:  "test-server",
		WorkingDir: "/test",
		AdvancedSettings: map[string]bool{
			session.FlagCanPromptUser: true,
		},
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	prompter := &mockUIPrompter{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(sessionID, prompter, logger); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}
	return srv, sessionID, prompter
}

// TestHandleUINotify_BeadsIssueRoundTrip verifies the mitto-9yz contract on
// the session-scoped path: an optional beads_issue field on UINotifyInput
// survives end-to-end into the UINotifyRequest handed to the session's
// UIPrompter, so the frontend can render a bead-clickable toast.
func TestHandleUINotify_BeadsIssueRoundTrip(t *testing.T) {
	srv, sid, prompter := newServerWithNotifyPrompter(t)

	_, out, err := srv.handleUINotify(context.Background(), nil, UINotifyInput{
		SelfID:     sid,
		Title:      "@mitto mention addressed",
		Message:    "on mitto-abc",
		Style:      "success",
		BeadsIssue: "mitto-abc",
	})
	if err != nil || !out.Success {
		t.Fatalf("handleUINotify: err=%v success=%v", err, out.Success)
	}
	notifies := prompter.recordedNotifies()
	if len(notifies) != 1 {
		t.Fatalf("UINotify calls=%d, want 1", len(notifies))
	}
	n := notifies[0]
	if n.BeadsIssue != "mitto-abc" {
		t.Errorf("BeadsIssue not propagated: got %q, want %q", n.BeadsIssue, "mitto-abc")
	}
	if n.Title != "@mitto mention addressed" || n.Message != "on mitto-abc" || n.Style != "success" {
		t.Errorf("payload wrong: %+v", n)
	}
}

// TestHandleUINotify_BeadsIssueOmittedByDefault asserts the zero-value path:
// callers that don't set BeadsIssue produce a request whose BeadsIssue is the
// empty string (matching today's byte-identical wire behavior post-omitempty).
func TestHandleUINotify_BeadsIssueOmittedByDefault(t *testing.T) {
	srv, sid, prompter := newServerWithNotifyPrompter(t)

	_, out, err := srv.handleUINotify(context.Background(), nil, UINotifyInput{
		SelfID: sid,
		Title:  "plain",
	})
	if err != nil || !out.Success {
		t.Fatalf("handleUINotify: err=%v success=%v", err, out.Success)
	}
	notifies := prompter.recordedNotifies()
	if len(notifies) != 1 {
		t.Fatalf("UINotify calls=%d, want 1", len(notifies))
	}
	if notifies[0].BeadsIssue != "" {
		t.Errorf("BeadsIssue should default to empty, got %q", notifies[0].BeadsIssue)
	}
}
