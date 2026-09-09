package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// TestHandleListSessions_WorkspaceIdentity covers the three-step derivation
// added for mitto-pscc.5.1 (session_list.go's HandleListSessions,
// SessionListResponse.WorkspaceUUID/WorkspaceName):
//
//  1. a live (currently-loaded) session resolves its workspace UUID directly
//     from the BackgroundSession via SessionManager.GetWorkspaceUUIDForSession;
//  2. a not-currently-loaded session falls back to a (WorkingDir, ACPServer)
//     registry lookup via GetWorkspaceByDirAndACP;
//  3. a session matching neither leaves both fields empty (and, being
//     `omitempty`, absent from the JSON) rather than fabricating a default.
func TestHandleListSessions_WorkspaceIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	const (
		liveSessionID     = "20260131-120000-live00001"
		fallbackSessionID = "20260131-120000-fallback01"
		unresolvedID      = "20260131-120000-unresolvd1"
		liveWorkspaceUUID = "ws-live-uuid"
		fallbackWSUUID    = "ws-fallback-uuid"
		fallbackWSName    = "Fallback Workspace"
		fallbackDir       = "/tmp/fallback-dir"
		liveDir           = "/tmp/live-dir"
		unresolvedDir     = "/tmp/no-such-workspace-dir"
		acpServer         = "test-server"
	)

	for _, m := range []session.Metadata{
		{SessionID: liveSessionID, ACPServer: acpServer, WorkingDir: liveDir},
		{SessionID: fallbackSessionID, ACPServer: acpServer, WorkingDir: fallbackDir},
		{SessionID: unresolvedID, ACPServer: acpServer, WorkingDir: unresolvedDir},
	} {
		if err := store.Create(m); err != nil {
			t.Fatalf("Create(%s) failed: %v", m.SessionID, err)
		}
	}

	sm := conversation.NewSessionManager("", acpServer, false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		// Registered under a different dir than the live session uses, so the
		// live case only succeeds if it goes through GetWorkspaceUUIDForSession
		// (step a) rather than the dir+ACP fallback (step b).
		{UUID: liveWorkspaceUUID, WorkingDir: "/tmp/registry-dir-for-live", ACPServer: acpServer},
		{UUID: fallbackWSUUID, Name: fallbackWSName, WorkingDir: fallbackDir, ACPServer: acpServer},
	})
	// Live session: BackgroundSession itself carries the workspace UUID.
	sm.AddSessionForTest(conversation.NewMinimalBackgroundSession(liveSessionID, liveDir, liveWorkspaceUUID))
	// fallbackSessionID and unresolvedID are deliberately NOT added as live
	// sessions, so GetWorkspaceUUIDForSession returns "" for both and the
	// handler must fall back to the dir+ACP registry lookup.

	h := New(Deps{Store: store, SessionManager: sm})

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	h.HandleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var got []SessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	byID := make(map[string]SessionListResponse, len(got))
	for _, r := range got {
		byID[r.SessionID] = r
	}
	if len(byID) != 3 {
		t.Fatalf("expected 3 sessions in response, got %d", len(byID))
	}

	live := byID[liveSessionID]
	if live.WorkspaceUUID != liveWorkspaceUUID {
		t.Errorf("live session WorkspaceUUID = %q, want %q", live.WorkspaceUUID, liveWorkspaceUUID)
	}

	fallback := byID[fallbackSessionID]
	if fallback.WorkspaceUUID != fallbackWSUUID {
		t.Errorf("fallback session WorkspaceUUID = %q, want %q", fallback.WorkspaceUUID, fallbackWSUUID)
	}
	if fallback.WorkspaceName != fallbackWSName {
		t.Errorf("fallback session WorkspaceName = %q, want %q", fallback.WorkspaceName, fallbackWSName)
	}

	unresolved := byID[unresolvedID]
	if unresolved.WorkspaceUUID != "" {
		t.Errorf("unresolved session WorkspaceUUID = %q, want empty", unresolved.WorkspaceUUID)
	}
	if unresolved.WorkspaceName != "" {
		t.Errorf("unresolved session WorkspaceName = %q, want empty", unresolved.WorkspaceName)
	}

	// mitto-lrt.12: the optional additive "backend" descriptor rides inside
	// SessionListResponse whenever a SessionManager is wired (identity is
	// computable from meta.ACPServer for all three fixtures here).
	for id, r := range byID {
		if r.Backend == nil {
			t.Errorf("session %s: expected a non-nil Backend descriptor, got nil", id)
			continue
		}
		if r.Backend.AgentRef == nil || r.Backend.AgentRef.Provider != acpServer {
			t.Errorf("session %s: Backend.AgentRef = %+v, want Provider=%q", id, r.Backend.AgentRef, acpServer)
		}
	}

	// omitempty: the raw JSON for the unresolved session must not carry the
	// workspace keys at all, not just empty strings — this is what lets old
	// SDK/CLI clients ignore the fields entirely when unset.
	var raw []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("Failed to unmarshal raw response: %v", err)
	}
	for _, r := range raw {
		if r["session_id"] != unresolvedID {
			continue
		}
		if _, ok := r["workspace_uuid"]; ok {
			t.Errorf("unresolved session JSON unexpectedly has workspace_uuid key: %v", r["workspace_uuid"])
		}
		if _, ok := r["workspace_name"]; ok {
			t.Errorf("unresolved session JSON unexpectedly has workspace_name key: %v", r["workspace_name"])
		}
	}
}

// TestHandleListSessions_NoSessionManager_BackendAbsent pins legacy behavior
// (mitto-lrt.12): without a SessionManager wired (e.g. a minimal Handlers as
// used by other unit tests), the "backend" computation is skipped entirely
// and the JSON key stays absent — same as before this feature existed.
func TestHandleListSessions_NoSessionManager_BackendAbsent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if err := store.Create(session.Metadata{SessionID: "no-sm-session", ACPServer: "Auggie", WorkingDir: "/tmp"}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	h := New(Deps{Store: store}) // no SessionManager

	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()
	h.HandleListSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 session, got %d", len(raw))
	}
	if _, ok := raw[0]["backend"]; ok {
		t.Errorf("expected no \"backend\" key without a SessionManager, got %v", raw[0]["backend"])
	}
}
