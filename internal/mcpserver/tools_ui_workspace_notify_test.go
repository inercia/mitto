package mcpserver

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// broadcastWorkspaceUINotifyCall captures one BroadcastWorkspaceUINotify
// invocation for assertion in the workspace-notify tests (mitto-6bn).
type broadcastWorkspaceUINotifyCall struct {
	workspaceUUID string
	workspaceName string
	workingDir    string
	req           UINotifyRequest
}

// mockSessionManagerForWorkspaceNotify is a minimal SessionManager mock that
// supports workspace lookup and records BroadcastWorkspaceUINotify calls so
// the handler's success/error paths can be verified in isolation.
type mockSessionManagerForWorkspaceNotify struct {
	mockSessionManager
	mu         sync.Mutex
	workspaces map[string]*config.WorkspaceSettings
	broadcasts []broadcastWorkspaceUINotifyCall
}

func (m *mockSessionManagerForWorkspaceNotify) GetWorkspaceByUUID(uuid string) *config.WorkspaceSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workspaces[uuid]
}

func (m *mockSessionManagerForWorkspaceNotify) BroadcastWorkspaceUINotify(workspaceUUID, workspaceName, workingDir string, req UINotifyRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcasts = append(m.broadcasts, broadcastWorkspaceUINotifyCall{
		workspaceUUID: workspaceUUID,
		workspaceName: workspaceName,
		workingDir:    workingDir,
		req:           req,
	})
}

func (m *mockSessionManagerForWorkspaceNotify) recorded() []broadcastWorkspaceUINotifyCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]broadcastWorkspaceUINotifyCall, len(m.broadcasts))
	copy(out, m.broadcasts)
	return out
}

// newServerForWorkspaceNotify builds a server with a workspace-aware session
// manager mock plus, optionally, a registered session that carries the
// can_prompt_user flag. When registerSession is false the server has no
// registered session — the aux-session case exercised by mitto-6bn.
func newServerForWorkspaceNotify(t *testing.T, workspaces map[string]*config.WorkspaceSettings, registerSession bool) (*Server, *mockSessionManagerForWorkspaceNotify, string) {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := &mockSessionManagerForWorkspaceNotify{workspaces: workspaces}
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	sessionID := session.GenerateSessionID()
	if registerSession {
		meta := session.Metadata{
			SessionID:  sessionID,
			Name:       "Test Session",
			ACPServer:  "test-server",
			WorkingDir: "/test/dir",
			AdvancedSettings: map[string]bool{
				session.FlagCanPromptUser: true,
			},
		}
		if err := store.Create(meta); err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		if err := srv.RegisterSession(sessionID, &mockUIPrompter{}, logger); err != nil {
			t.Fatalf("RegisterSession failed: %v", err)
		}
	}

	return srv, sm, sessionID
}

// TestHandleWorkspaceUINotify_Success verifies the happy path: valid workspace
// UUID + registered caller session → broadcast fires with the expected fields.
func TestHandleWorkspaceUINotify_Success(t *testing.T) {
	ws := &config.WorkspaceSettings{
		UUID:       "ws-uuid-1",
		Name:       "My Workspace",
		WorkingDir: "/tmp/foo",
	}
	srv, sm, sessionID := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"ws-uuid-1": ws}, true)

	_, out, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID:        sessionID,
		WorkspaceUUID: "ws-uuid-1",
		Title:         "Hello",
		Message:       "world",
		Style:         "success",
		Sound:         true,
		Native:        true,
		Sticky:        true,
	})
	if err != nil {
		t.Fatalf("handleWorkspaceUINotify: %v", err)
	}
	if !out.Success {
		t.Errorf("Success=false, want true")
	}

	calls := sm.recorded()
	if len(calls) != 1 {
		t.Fatalf("broadcast count=%d, want 1", len(calls))
	}
	c := calls[0]
	if c.workspaceUUID != "ws-uuid-1" || c.workspaceName != "My Workspace" || c.workingDir != "/tmp/foo" {
		t.Errorf("broadcast workspace fields wrong: %+v", c)
	}
	if c.req.Title != "Hello" || c.req.Message != "world" || c.req.Style != "success" {
		t.Errorf("broadcast payload wrong: %+v", c.req)
	}
	if !c.req.Sound || !c.req.Native || !c.req.Sticky {
		t.Errorf("broadcast flags wrong: %+v", c.req)
	}
}

// TestHandleWorkspaceUINotify_UnregisteredCaller covers the auxiliary-session
// case: the caller supplies a self_id that has no registered session — the
// tool must still succeed because the workspace_uuid is the safety boundary
// (mitto-6bn rationale).
func TestHandleWorkspaceUINotify_UnregisteredCaller(t *testing.T) {
	ws := &config.WorkspaceSettings{UUID: "ws-aux", Name: "Aux", WorkingDir: "/tmp/aux"}
	srv, sm, _ := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"ws-aux": ws}, false)

	_, out, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID:        "not-a-registered-session",
		WorkspaceUUID: "ws-aux",
		Title:         "close-phase done",
	})
	if err != nil {
		t.Fatalf("handleWorkspaceUINotify: %v", err)
	}
	if !out.Success {
		t.Errorf("Success=false, want true")
	}
	if len(sm.recorded()) != 1 {
		t.Errorf("expected 1 broadcast, got %d", len(sm.recorded()))
	}
}

func TestHandleWorkspaceUINotify_MissingSelfID(t *testing.T) {
	srv, sm, _ := newServerForWorkspaceNotify(t, nil, false)
	_, _, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		WorkspaceUUID: "any", Title: "t",
	})
	if err == nil || !strings.Contains(err.Error(), "self_id") {
		t.Fatalf("expected self_id error, got %v", err)
	}
	if len(sm.recorded()) != 0 {
		t.Errorf("expected no broadcast on error, got %d", len(sm.recorded()))
	}
}

func TestHandleWorkspaceUINotify_MissingWorkspaceUUID(t *testing.T) {
	srv, sm, sid := newServerForWorkspaceNotify(t, nil, true)
	_, _, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sid, Title: "t",
	})
	if err == nil || !strings.Contains(err.Error(), "workspace_uuid") {
		t.Fatalf("expected workspace_uuid error, got %v", err)
	}
	if len(sm.recorded()) != 0 {
		t.Errorf("expected no broadcast on error, got %d", len(sm.recorded()))
	}
}

func TestHandleWorkspaceUINotify_UnknownWorkspace(t *testing.T) {
	srv, sm, sid := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"known": {UUID: "known"}}, true)
	_, _, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sid, WorkspaceUUID: "does-not-exist", Title: "t",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Fatalf("expected unknown workspace error, got %v", err)
	}
	if len(sm.recorded()) != 0 {
		t.Errorf("expected no broadcast on error, got %d", len(sm.recorded()))
	}
}

func TestHandleWorkspaceUINotify_MissingTitle(t *testing.T) {
	ws := &config.WorkspaceSettings{UUID: "w", Name: "w", WorkingDir: "/x"}
	srv, sm, sid := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"w": ws}, true)
	_, _, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sid, WorkspaceUUID: "w",
	})
	if err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title error, got %v", err)
	}
	if len(sm.recorded()) != 0 {
		t.Errorf("expected no broadcast on error, got %d", len(sm.recorded()))
	}
}

func TestHandleWorkspaceUINotify_InvalidStyle(t *testing.T) {
	ws := &config.WorkspaceSettings{UUID: "w", Name: "w", WorkingDir: "/x"}
	srv, sm, sid := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"w": ws}, true)
	_, _, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sid, WorkspaceUUID: "w", Title: "t", Style: "bogus",
	})
	if err == nil || !strings.Contains(err.Error(), "style must be one of") {
		t.Fatalf("expected style error, got %v", err)
	}
	if len(sm.recorded()) != 0 {
		t.Errorf("expected no broadcast on error, got %d", len(sm.recorded()))
	}
}

func TestHandleWorkspaceUINotify_DefaultStyle(t *testing.T) {
	ws := &config.WorkspaceSettings{UUID: "w", Name: "w", WorkingDir: "/x"}
	srv, sm, sid := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"w": ws}, true)
	_, out, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sid, WorkspaceUUID: "w", Title: "t",
	})
	if err != nil || !out.Success {
		t.Fatalf("handleWorkspaceUINotify: err=%v success=%v", err, out.Success)
	}
	calls := sm.recorded()
	if len(calls) != 1 {
		t.Fatalf("broadcast count=%d, want 1", len(calls))
	}
	if calls[0].req.Style != "info" {
		t.Errorf("expected default style=info, got %q", calls[0].req.Style)
	}
}

func TestHandleWorkspaceUINotify_TruncatesTitleAndMessage(t *testing.T) {
	ws := &config.WorkspaceSettings{UUID: "w", Name: "w", WorkingDir: "/x"}
	srv, sm, sid := newServerForWorkspaceNotify(t,
		map[string]*config.WorkspaceSettings{"w": ws}, true)

	longTitle := strings.Repeat("A", 250)
	longMessage := strings.Repeat("B", 1500)

	_, _, err := srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sid, WorkspaceUUID: "w", Title: longTitle, Message: longMessage,
	})
	if err != nil {
		t.Fatalf("handleWorkspaceUINotify: %v", err)
	}
	calls := sm.recorded()
	if len(calls) != 1 {
		t.Fatalf("broadcast count=%d", len(calls))
	}
	// Titles are truncated to 200 runes; last rune replaced with U+2026.
	titleRunes := []rune(calls[0].req.Title)
	if len(titleRunes) != 200 {
		t.Errorf("truncated title rune-length=%d, want 200", len(titleRunes))
	}
	if titleRunes[len(titleRunes)-1] != '…' {
		t.Errorf("truncated title should end in ellipsis, got %q", string(titleRunes[len(titleRunes)-1]))
	}
	msgRunes := []rune(calls[0].req.Message)
	if len(msgRunes) != 1000 {
		t.Errorf("truncated message rune-length=%d, want 1000", len(msgRunes))
	}
	if msgRunes[len(msgRunes)-1] != '…' {
		t.Errorf("truncated message should end in ellipsis")
	}
}

// TestHandleWorkspaceUINotify_PermissionDenied verifies the permission gate
// fires when the caller *is* a registered session but lacks CanPromptUser.
func TestHandleWorkspaceUINotify_PermissionDenied(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ws := &config.WorkspaceSettings{UUID: "w", Name: "w", WorkingDir: "/x"}
	sm := &mockSessionManagerForWorkspaceNotify{workspaces: map[string]*config.WorkspaceSettings{"w": ws}}
	srv, err := NewServer(Config{Port: 0}, Dependencies{Store: store, SessionManager: sm})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	sessionID := session.GenerateSessionID()
	meta := session.Metadata{
		SessionID: sessionID, Name: "s", ACPServer: "t", WorkingDir: "/x",
		AdvancedSettings: map[string]bool{session.FlagCanPromptUser: false},
	}
	if err := store.Create(meta); err != nil {
		t.Fatalf("Create: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := srv.RegisterSession(sessionID, &mockUIPrompter{}, logger); err != nil {
		t.Fatalf("RegisterSession: %v", err)
	}

	_, _, err = srv.handleWorkspaceUINotify(context.Background(), nil, WorkspaceUINotifyInput{
		SelfID: sessionID, WorkspaceUUID: "w", Title: "t",
	})
	if err == nil || !strings.Contains(err.Error(), "Can prompt user") {
		t.Fatalf("expected permission error, got %v", err)
	}
	if len(sm.recorded()) != 0 {
		t.Errorf("expected no broadcast on permission denial, got %d", len(sm.recorded()))
	}
}
