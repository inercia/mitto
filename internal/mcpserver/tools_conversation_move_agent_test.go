// tools_conversation_move_agent_test.go: unit tests for mitto_conversation_move_agent
// (mitto-f7yo.3). Exercises the handler wiring only — the actual
// MoveSessionToAgent business logic (busy/archived/same-agent/no-workspace
// checks, metadata rewrite, resume, children) is covered by
// internal/conversation/session_manager_move_agent_test.go; the agent/
// acp_server alias-resolution semantics themselves are covered by
// agent_selection_test.go. Here we only verify that the tool correctly
// resolves self_id/conversation_id="self", resolves the target agent via the
// same alias precedence as mitto_conversation_new, forwards to
// SessionManager.MoveSessionToAgentForMCP, maps its sentinel errors to clear
// tool errors, and broadcasts session_agent_moved on success.
package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// mockSessionManagerForMoveAgent is a SessionManager stub whose
// MoveSessionToAgentForMCP is fully caller-configurable (result/err) and
// records every call plus every BroadcastSessionAgentMoved invocation.
type mockSessionManagerForMoveAgent struct {
	moveAgentCalls []moveAgentCall
	moveResult     MoveAgentResult
	moveErr        error
	broadcasts     []moveAgentBroadcastCall
}

type moveAgentCall struct {
	sessionID   string
	targetAgent string
	opts        MoveAgentOptions
}

type moveAgentBroadcastCall struct {
	sessionID     string
	newAgent      string
	previousAgent string
}

func (m *mockSessionManagerForMoveAgent) MoveSessionToAgentForMCP(sessionID, targetAgent string, opts MoveAgentOptions) (MoveAgentResult, error) {
	m.moveAgentCalls = append(m.moveAgentCalls, moveAgentCall{sessionID: sessionID, targetAgent: targetAgent, opts: opts})
	return m.moveResult, m.moveErr
}

func (m *mockSessionManagerForMoveAgent) BroadcastSessionAgentMoved(sessionID, newAgent, previousAgent string) {
	m.broadcasts = append(m.broadcasts, moveAgentBroadcastCall{sessionID: sessionID, newAgent: newAgent, previousAgent: previousAgent})
}

// Remaining SessionManager methods — no-op stubs, mirroring mockSessionManager.
func (m *mockSessionManagerForMoveAgent) GetSession(sessionID string) BackgroundSession { return nil }
func (m *mockSessionManagerForMoveAgent) ListRunningSessions() []string                 { return nil }
func (m *mockSessionManagerForMoveAgent) CloseSessionGracefully(string, string, time.Duration) bool {
	return true
}
func (m *mockSessionManagerForMoveAgent) CloseSession(string, string) {}
func (m *mockSessionManagerForMoveAgent) ResumeSession(string, string, string) (BackgroundSession, error) {
	return nil, nil
}
func (m *mockSessionManagerForMoveAgent) GetWorkspacesForFolder(string) []config.WorkspaceSettings {
	return nil
}
func (m *mockSessionManagerForMoveAgent) BroadcastSessionCreated(string, string, string, string, string, string) {
}
func (m *mockSessionManagerForMoveAgent) BroadcastSessionArchived(string, bool, ...session.ArchiveReason) {
}
func (m *mockSessionManagerForMoveAgent) BroadcastSessionDeleted(string)            {}
func (m *mockSessionManagerForMoveAgent) BroadcastWaitingForChildren(string, bool)  {}
func (m *mockSessionManagerForMoveAgent) DeleteChildSessions(string)                {}
func (m *mockSessionManagerForMoveAgent) ApplyOnCloseProcessors(string, string)     {}
func (m *mockSessionManagerForMoveAgent) GetWorkspaces() []config.WorkspaceSettings { return nil }
func (m *mockSessionManagerForMoveAgent) GetWorkspaceByUUID(string) *config.WorkspaceSettings {
	return nil
}
func (m *mockSessionManagerForMoveAgent) BroadcastSessionRenamed(string, string)           {}
func (m *mockSessionManagerForMoveAgent) BroadcastSessionBeadsIssueUpdated(string, string) {}
func (m *mockSessionManagerForMoveAgent) BroadcastLoopUpdated(string, *session.LoopPrompt) {}
func (m *mockSessionManagerForMoveAgent) BroadcastWorkspaceUINotify(string, string, string, UINotifyRequest) {
}
func (m *mockSessionManagerForMoveAgent) GetUserDataSchema(string) *config.UserDataSchema { return nil }
func (m *mockSessionManagerForMoveAgent) GetWorkspacePrompts(string) []config.WebPrompt   { return nil }
func (m *mockSessionManagerForMoveAgent) GetWorkspacePromptsDirs(string) []string         { return nil }
func (m *mockSessionManagerForMoveAgent) GetWorkspaceRCLastModified(string) time.Time {
	return time.Time{}
}
func (m *mockSessionManagerForMoveAgent) GetWorkspace(string) *config.WorkspaceSettings { return nil }
func (m *mockSessionManagerForMoveAgent) InvalidateWorkspaceRC(string)                  {}
func (m *mockSessionManagerForMoveAgent) IsMCPInitTimeout(error) bool                   { return false }

// setupMoveAgentServer wires a Server with a session store + a fresh
// mockSessionManagerForMoveAgent, an app config with two ACP servers
// ("Auggie" and "Claude Code" — matching agent_selection_test.go's fixture so
// alias-resolution behavior is exercised identically), and a registered
// caller session. Returns the server, SM mock, and caller session id.
func setupMoveAgentServer(t *testing.T) (*Server, *mockSessionManagerForMoveAgent, string) {
	t.Helper()

	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	callerMeta := session.Metadata{
		SessionID:  session.GenerateSessionID(),
		Name:       "Caller",
		ACPServer:  "Auggie",
		WorkingDir: "/test/dir",
	}
	if err := store.Create(callerMeta); err != nil {
		t.Fatalf("Create caller: %v", err)
	}

	sm := &mockSessionManagerForMoveAgent{}
	cfg := testAgentSelectionMCPConfig()
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm, Config: cfg})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(callerMeta.SessionID, nil, logger); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	return srv, sm, callerMeta.SessionID
}

func TestConversationMoveAgent_MissingSelfID(t *testing.T) {
	srv, _, _ := setupMoveAgentServer(t)
	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		ConversationID: "self",
		Agent:          "Auggie",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "self_id is required") {
		t.Fatalf("got %+v, want self_id required error", out)
	}
}

func TestConversationMoveAgent_MissingConversationID(t *testing.T) {
	srv, _, callerID := setupMoveAgentServer(t)
	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID: callerID,
		Agent:  "Auggie",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "conversation_id is required") {
		t.Fatalf("got %+v, want conversation_id required error", out)
	}
}

func TestConversationMoveAgent_MissingTargetAgent(t *testing.T) {
	srv, _, callerID := setupMoveAgentServer(t)
	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:         callerID,
		ConversationID: "self",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "acp_server' or 'agent' is required") {
		t.Fatalf("got %+v, want target-agent-required error", out)
	}
}

// TestConversationMoveAgent_AliasResolution verifies the 'agent' parameter
// resolves through the same case-insensitive alias precedence as
// mitto_conversation_new's 'agent' param (mitto-lrt.13): the canonical
// server name ends up both in the call forwarded to
// MoveSessionToAgentForMCP and in the tool's own 'new_agent' output field.
func TestConversationMoveAgent_AliasResolution(t *testing.T) {
	srv, sm, callerID := setupMoveAgentServer(t)
	sm.moveResult = MoveAgentResult{Moved: []string{callerID}, PreviousAgent: "Auggie"}

	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:         callerID,
		ConversationID: "self",
		Agent:          "claude code", // lowercase alias of "Claude Code"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected success, got error: %s", out.Error)
	}
	if out.NewAgent != "Claude Code" {
		t.Fatalf("new_agent = %q, want canonical %q", out.NewAgent, "Claude Code")
	}
	if len(sm.moveAgentCalls) != 1 || sm.moveAgentCalls[0].targetAgent != "Claude Code" {
		t.Fatalf("MoveSessionToAgentForMCP call = %+v, want targetAgent %q", sm.moveAgentCalls, "Claude Code")
	}
	if sm.moveAgentCalls[0].sessionID != callerID {
		t.Fatalf("conversation_id=\"self\" was not resolved to the caller's real session id: got %q, want %q",
			sm.moveAgentCalls[0].sessionID, callerID)
	}
}

// TestConversationMoveAgent_AcpServerAndAgentDisagree verifies the same
// agree-if-both-given conflict error as mitto_conversation_new.
func TestConversationMoveAgent_AcpServerAndAgentDisagree(t *testing.T) {
	srv, sm, callerID := setupMoveAgentServer(t)

	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:         callerID,
		ConversationID: "self",
		ACPServer:      "Auggie",
		Agent:          "Claude Code",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success {
		t.Fatalf("expected failure for disagreeing acp_server/agent, got success: %+v", out)
	}
	if !strings.Contains(out.Error, "Auggie") || !strings.Contains(out.Error, "Claude Code") {
		t.Fatalf("error %q does not mention both conflicting values", out.Error)
	}
	if len(sm.moveAgentCalls) != 0 {
		t.Fatalf("expected no MoveSessionToAgentForMCP call on conflict, got %+v", sm.moveAgentCalls)
	}
}

// TestConversationMoveAgent_BusyRejection verifies ErrMoveAgentBusy is
// surfaced as a clear, non-fatal tool error advising the caller to retry once
// idle — never a hard tool-call error.
func TestConversationMoveAgent_BusyRejection(t *testing.T) {
	srv, sm, callerID := setupMoveAgentServer(t)
	sm.moveErr = ErrMoveAgentBusy

	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:         callerID,
		ConversationID: "self",
		Agent:          "Claude Code",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success {
		t.Fatalf("expected failure for busy conversation, got success: %+v", out)
	}
	if !strings.Contains(out.Error, "busy") || !strings.Contains(out.Error, "retry") {
		t.Fatalf("error %q does not mention busy/retry", out.Error)
	}
}

func TestConversationMoveAgent_SentinelErrorMapping(t *testing.T) {
	tests := []struct {
		name    string
		moveErr error
		wantMsg string
	}{
		{"same agent", ErrMoveAgentSameAgent, "same as the current agent"},
		{"unknown target", ErrMoveAgentUnknownTarget, "not configured"},
		{"no workspace", ErrMoveAgentNoWorkspace, "not configured for this conversation's folder"},
		{"archived", ErrMoveAgentArchived, "archived"},
		{"not found", session.ErrSessionNotFound, "conversation not found"},
		{"unexpected error", errors.New("boom"), "failed to move conversation to agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, sm, callerID := setupMoveAgentServer(t)
			sm.moveErr = tt.moveErr

			_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
				SelfID:         callerID,
				ConversationID: "self",
				Agent:          "Claude Code",
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Success {
				t.Fatalf("expected failure, got success: %+v", out)
			}
			if !strings.Contains(out.Error, tt.wantMsg) {
				t.Fatalf("error %q does not contain %q", out.Error, tt.wantMsg)
			}
		})
	}
}

// TestConversationMoveAgent_Success verifies the success result shape and
// that the session_agent_moved broadcast fires once per moved conversation.
func TestConversationMoveAgent_Success(t *testing.T) {
	srv, sm, callerID := setupMoveAgentServer(t)
	childID := "child-session-id"
	sm.moveResult = MoveAgentResult{
		Moved:                 []string{callerID, childID},
		Skipped:               []MoveAgentSkip{{ID: "skipped-1", Reason: "busy"}},
		PreviousAgent:         "Auggie",
		PreviousBaselineModel: "gpt-x",
		ResumeError:           "",
	}

	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:          callerID,
		ConversationID:  "self",
		ACPServer:       "Claude Code",
		IncludeChildren: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected success, got error: %s", out.Error)
	}
	if len(out.Moved) != 2 || out.Moved[0] != callerID || out.Moved[1] != childID {
		t.Fatalf("Moved = %+v, want [%q, %q]", out.Moved, callerID, childID)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].ID != "skipped-1" || out.Skipped[0].Reason != "busy" {
		t.Fatalf("Skipped = %+v, want one entry {skipped-1, busy}", out.Skipped)
	}
	if out.PreviousAgent != "Auggie" {
		t.Fatalf("PreviousAgent = %q, want %q", out.PreviousAgent, "Auggie")
	}
	if out.NewAgent != "Claude Code" {
		t.Fatalf("NewAgent = %q, want %q", out.NewAgent, "Claude Code")
	}
	if out.PreviousBaselineModel != "gpt-x" {
		t.Fatalf("PreviousBaselineModel = %q, want %q", out.PreviousBaselineModel, "gpt-x")
	}

	if !sm.moveAgentCalls[0].opts.IncludeChildren {
		t.Fatalf("expected include_children to be forwarded as true")
	}

	if len(sm.broadcasts) != 2 {
		t.Fatalf("expected 2 BroadcastSessionAgentMoved calls (one per moved id), got %d: %+v", len(sm.broadcasts), sm.broadcasts)
	}
	for i, wantID := range []string{callerID, childID} {
		if sm.broadcasts[i].sessionID != wantID || sm.broadcasts[i].newAgent != "Claude Code" || sm.broadcasts[i].previousAgent != "Auggie" {
			t.Fatalf("broadcast[%d] = %+v, want {sessionID: %q, newAgent: Claude Code, previousAgent: Auggie}", i, sm.broadcasts[i], wantID)
		}
	}
}

// TestConversationMoveAgent_SelfAlias verifies conversation_id="self" resolves
// to the caller's own real session id, mirroring mitto_conversation_update's
// convention.
func TestConversationMoveAgent_SelfAlias(t *testing.T) {
	srv, sm, callerID := setupMoveAgentServer(t)
	sm.moveResult = MoveAgentResult{Moved: []string{callerID}, PreviousAgent: "Auggie"}

	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:         callerID,
		ConversationID: callerID, // caller's own real ID, not the literal "self"
		Agent:          "Claude Code",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Success {
		t.Fatalf("expected success, got error: %s", out.Error)
	}
	if out.ConversationID != callerID {
		t.Fatalf("ConversationID = %q, want %q", out.ConversationID, callerID)
	}
}

func TestConversationMoveAgent_UnresolvableSelfID(t *testing.T) {
	srv, _, _ := setupMoveAgentServer(t)
	_, out, err := srv.handleConversationMoveAgent(context.Background(), nil, ConversationMoveAgentInput{
		SelfID:         "does-not-exist",
		ConversationID: "self",
		Agent:          "Claude Code",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Success || !strings.Contains(out.Error, "could not be resolved") {
		t.Fatalf("got %+v, want unresolvable self_id error", out)
	}
}
