package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// newACPDelHandlers builds a Handlers facade with the deps the ACP-server
// delete flow needs (store, session manager, config, workspace sync no-op).
// The caller wires MITTO_DIR to a temp dir via t.Setenv beforehand when tests
// exercise settings persistence.
func newACPDelHandlers(t *testing.T, sm *conversation.SessionManager, cfg *config.Config, store *session.Store) *Handlers {
	t.Helper()
	return New(Deps{
		SessionManager:           sm,
		MittoConfig:              cfg,
		Store:                    store,
		SyncConfigWorkspaces:     func() {},
		BroadcastSessionArchived: func(string, bool, ...session.ArchiveReason) {},
		BroadcastSessionDeleted:  func(string) {},
		BroadcastACPStopped:      func(string, string) {},
	})
}

func newACPDelStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestHandleACPServerPrepareDelete_UnknownServer(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	cfg := &config.Config{ACPServers: []config.ACPServer{{Name: "keep"}}}
	h := newACPDelHandlers(t, sm, cfg, newACPDelStore(t))

	req := httptest.NewRequest(http.MethodGet, "/api/acp-servers/missing/prepare-delete", nil)
	req.SetPathValue("name", "missing")
	w := httptest.NewRecorder()
	h.HandleACPServerPrepareDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func TestHandleACPServerPrepareDelete_FoldersAndCandidates(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-a", WorkingDir: "/dir1", ACPServer: "old"},
		{UUID: "ws-b", WorkingDir: "/dir1", ACPServer: "other"},
		{UUID: "ws-c", WorkingDir: "/dir2", ACPServer: "old"},
	})
	cfg := &config.Config{ACPServers: []config.ACPServer{
		{Name: "old"}, {Name: "other"}, {Name: "third"},
	}}
	store := newACPDelStore(t)
	// Two archived conversations in /dir1 and one non-archived in /dir2.
	must(t, store.Create(session.Metadata{SessionID: "s1", ACPServer: "old", WorkingDir: "/dir1", Archived: true}))
	must(t, store.Create(session.Metadata{SessionID: "s2", ACPServer: "old", WorkingDir: "/dir1", Archived: true}))
	must(t, store.Create(session.Metadata{SessionID: "s3", ACPServer: "old", WorkingDir: "/dir2"}))
	h := newACPDelHandlers(t, sm, cfg, store)

	req := httptest.NewRequest(http.MethodGet, "/api/acp-servers/old/prepare-delete", nil)
	req.SetPathValue("name", "old")
	w := httptest.NewRecorder()
	h.HandleACPServerPrepareDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var resp ACPServerPrepareDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Server != "old" {
		t.Errorf("server = %q", resp.Server)
	}
	if resp.HasActive {
		t.Errorf("HasActive = true, want false (no active conversations)")
	}
	if len(resp.ActiveConversations) != 0 {
		t.Errorf("expected no active conversations, got %+v", resp.ActiveConversations)
	}
	if len(resp.Folders) != 2 {
		t.Fatalf("expected 2 folders, got %d: %+v", len(resp.Folders), resp.Folders)
	}
	// /dir1 first (sorted), 2 archived, 0 non-archived, candidate=other.
	f1 := resp.Folders[0]
	if f1.WorkingDir != "/dir1" || f1.ArchivedConversations != 2 || f1.NonArchivedConversations != 0 {
		t.Errorf("folder[0] mismatch: %+v", f1)
	}
	if !equalStrings(f1.ReplacementCandidates, []string{"other"}) {
		t.Errorf("folder[0] candidates = %v", f1.ReplacementCandidates)
	}
	if !equalStrings(f1.WorkspaceUUIDs, []string{"ws-a"}) {
		t.Errorf("folder[0] workspaces = %v", f1.WorkspaceUUIDs)
	}
	// /dir2 has no other workspace-registered server → no candidates.
	f2 := resp.Folders[1]
	if f2.WorkingDir != "/dir2" || f2.ArchivedConversations != 0 || f2.NonArchivedConversations != 1 {
		t.Errorf("folder[1] mismatch: %+v", f2)
	}
	if len(f2.ReplacementCandidates) != 0 {
		t.Errorf("folder[1] candidates = %v (want empty)", f2.ReplacementCandidates)
	}
}

func TestHandleACPServerReassignAndDelete_RCFileRefused(t *testing.T) {
	sm := conversation.NewSessionManager("", "", false, nil)
	cfg := &config.Config{ACPServers: []config.ACPServer{{Name: "rc", Source: config.SourceRCFile}}}
	h := newACPDelHandlers(t, sm, cfg, newACPDelStore(t))

	body := strings.NewReader(`{"folders":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/acp-servers/rc/reassign-and-delete", body)
	req.SetPathValue("name", "rc")
	w := httptest.NewRecorder()
	h.HandleACPServerReassignAndDelete(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setupSettingsFile writes a minimal settings.json containing the given ACP
// servers to a fresh MITTO_DIR temp dir, and returns nothing (t.Setenv wires
// MITTO_DIR for subsequent LoadSettings/SaveSettings calls).
func setupSettingsFile(t *testing.T, servers []config.ACPServerSettings) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv(appdir.MittoDirEnv, tmp)
	appdir.ResetCache()
	t.Cleanup(appdir.ResetCache)
	if err := config.SaveSettings(&config.Settings{ACPServers: servers}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
}

func TestHandleACPServerReassignAndDelete_ReassignFolder(t *testing.T) {
	setupSettingsFile(t, []config.ACPServerSettings{
		{Name: "old", Command: "old-cmd"},
		{Name: "new", Command: "new-cmd"},
	})

	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	// Two conversations in /dir1 on "old": one archived, one not. Both must be
	// reassigned to "new" and marked archived, and ACPSessionID cleared.
	must(t, store.Create(session.Metadata{
		SessionID: "conv-live", ACPServer: "old", WorkingDir: "/dir1",
		ACPSessionID: "acp-old-1",
	}))
	must(t, store.Create(session.Metadata{
		SessionID: "conv-archived", ACPServer: "old", WorkingDir: "/dir1",
		Archived: true, ACPSessionID: "acp-old-2", CurrentModeID: "code",
	}))

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-1", WorkingDir: "/dir1", ACPServer: "old"},
		{UUID: "ws-2", WorkingDir: "/dir1", ACPServer: "new"},
	})
	cfg := &config.Config{ACPServers: []config.ACPServer{
		{Name: "old", Command: "old-cmd"},
		{Name: "new", Command: "new-cmd"},
	}}
	h := newACPDelHandlers(t, sm, cfg, store)

	body := strings.NewReader(`{"folders":{"/dir1":"new"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/acp-servers/old/reassign-and-delete", body)
	req.SetPathValue("name", "old")
	w := httptest.NewRecorder()
	h.HandleACPServerReassignAndDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var reassignResp ACPServerReassignAndDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &reassignResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if reassignResp.ReassignedConversationCount != 2 {
		t.Errorf("ReassignedConversationCount = %d, want 2", reassignResp.ReassignedConversationCount)
	}
	if reassignResp.ReassignedWorkspaceCount != 1 {
		t.Errorf("ReassignedWorkspaceCount = %d, want 1", reassignResp.ReassignedWorkspaceCount)
	}
	if reassignResp.DeletedConversationCount != 0 {
		t.Errorf("DeletedConversationCount = %d, want 0", reassignResp.DeletedConversationCount)
	}

	// Both conversations should now point at "new", be archived, and have
	// ACPSessionID cleared.
	for _, id := range []string{"conv-live", "conv-archived"} {
		m, err := store.GetMetadata(id)
		if err != nil {
			t.Fatalf("GetMetadata(%s): %v", id, err)
		}
		if m.ACPServer != "new" {
			t.Errorf("%s ACPServer = %q, want new", id, m.ACPServer)
		}
		if m.ACPSessionID != "" {
			t.Errorf("%s ACPSessionID = %q, want cleared", id, m.ACPSessionID)
		}
		if !m.Archived {
			t.Errorf("%s Archived = false, want true", id)
		}
		if m.CurrentModeID != "" {
			t.Errorf("%s CurrentModeID = %q, want cleared", id, m.CurrentModeID)
		}
	}

	// The workspace config for /dir1+old should be gone; the folder still has
	// ws-2 (new) intact.
	found := false
	for _, ws := range sm.GetWorkspaces() {
		if ws.WorkingDir == "/dir1" && ws.ACPServer == "old" {
			t.Errorf("workspace for /dir1+old still present: %+v", ws)
		}
		if ws.UUID == "ws-2" && ws.ACPServer == "new" {
			found = true
		}
	}
	if !found {
		t.Errorf("original ws-2/new workspace lost after reassign")
	}

	// The server should have been removed from in-memory config AND settings.json.
	if _, err := cfg.GetServer("old"); err == nil {
		t.Errorf("MittoConfig still contains 'old' server")
	}
	cfgOnDisk, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if _, err := cfgOnDisk.GetServer("old"); err == nil {
		t.Errorf("settings.json still contains 'old' server")
	}
	if _, err := cfgOnDisk.GetServer("new"); err != nil {
		t.Errorf("settings.json lost 'new' server: %v", err)
	}
}

func TestHandleACPServerReassignAndDelete_NoReplacementDeletesFolder(t *testing.T) {
	setupSettingsFile(t, []config.ACPServerSettings{{Name: "solo", Command: "solo-cmd"}})

	store, err := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	must(t, store.Create(session.Metadata{SessionID: "gone-1", ACPServer: "solo", WorkingDir: "/dir1"}))
	must(t, store.Create(session.Metadata{SessionID: "gone-2", ACPServer: "solo", WorkingDir: "/dir1", Archived: true}))

	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws-only", WorkingDir: "/dir1", ACPServer: "solo"},
	})
	cfg := &config.Config{ACPServers: []config.ACPServer{{Name: "solo", Command: "solo-cmd"}}}
	h := newACPDelHandlers(t, sm, cfg, store)

	// Empty replacement means "delete conversations in this folder".
	body := strings.NewReader(`{"folders":{"/dir1":""}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/acp-servers/solo/reassign-and-delete", body)
	req.SetPathValue("name", "solo")
	w := httptest.NewRecorder()
	h.HandleACPServerReassignAndDelete(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	for _, id := range []string{"gone-1", "gone-2"} {
		if _, err := store.GetMetadata(id); err == nil {
			t.Errorf("conversation %s still present after delete", id)
		}
	}
	if len(sm.GetWorkspaces()) != 0 {
		t.Errorf("workspaces still present: %+v", sm.GetWorkspaces())
	}
	if _, err := cfg.GetServer("solo"); err == nil {
		t.Errorf("MittoConfig still contains 'solo' server")
	}
}

func TestHandleACPServerReassignAndDelete_InvalidReplacement(t *testing.T) {
	setupSettingsFile(t, []config.ACPServerSettings{{Name: "old"}, {Name: "new"}})

	store := newACPDelStore(t)
	must(t, store.Create(session.Metadata{SessionID: "c1", ACPServer: "old", WorkingDir: "/dir"}))
	sm := conversation.NewSessionManager("", "", false, nil)
	sm.SetWorkspaces([]config.WorkspaceSettings{{UUID: "w1", WorkingDir: "/dir", ACPServer: "old"}})
	cfg := &config.Config{ACPServers: []config.ACPServer{{Name: "old"}, {Name: "new"}}}
	h := newACPDelHandlers(t, sm, cfg, store)

	body := strings.NewReader(`{"folders":{"/dir":"does-not-exist"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/acp-servers/old/reassign-and-delete", body)
	req.SetPathValue("name", "old")
	w := httptest.NewRecorder()
	h.HandleACPServerReassignAndDelete(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
}
