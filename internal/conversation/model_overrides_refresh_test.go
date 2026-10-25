package conversation

import (
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func overrideWorkspace(uuid, acpServer, workingDir, modelID string) config.WorkspaceSettings {
	return config.WorkspaceSettings{
		UUID:       uuid,
		ACPServer:  acpServer,
		WorkingDir: workingDir,
		ModelOverrides: config.ModelOverrides{
			{ACPServer: acpServer, Tag: "Smart", ModelID: modelID},
		},
	}
}

func overrideCatalog() *SessionModelState {
	return &SessionModelState{
		CurrentModelId: "other-model",
		AvailableModels: []ModelInfo{
			{ModelId: "old-model", Name: "Old"},
			{ModelId: "new-model", Name: "New"},
			{ModelId: "other-model", Name: "Other"},
		},
	}
}

func resolveSmart(bs *BackgroundSession, catalog *SessionModelState) string {
	return SelectHighestPriorityModel(
		[]config.PromptPreferredModel{{ModelTag: "Smart"}},
		nil,
		catalog,
		bs.modelOverrideForTag,
	)
}

// TestSetWorkspaces_RefreshesLiveSessionModelOverrides covers mitto-971: an
// already-running conversation must resolve model tags with the workspace
// overrides edited after the session was created.
func TestSetWorkspaces_RefreshesLiveSessionModelOverrides(t *testing.T) {
	bs := &BackgroundSession{
		workspaceUUID: "ws1",
		acpServer:     "OpenCode",
		workingDir:    "/repo",
	}
	bs.modelOverrides = config.ModelOverrides{
		{ACPServer: "OpenCode", Tag: "Smart", ModelID: "old-model"},
	}
	sm := &SessionManager{
		sessions:   map[string]*BackgroundSession{"s1": bs},
		wsRegistry: &WorkspaceRegistry{},
	}
	catalog := overrideCatalog()

	if got := resolveSmart(bs, catalog); got != "old-model" {
		t.Fatalf("before refresh resolved %q, want old-model", got)
	}

	sm.SetWorkspaces([]config.WorkspaceSettings{
		overrideWorkspace("ws1", "OpenCode", "/repo", "new-model"),
	})

	if got := resolveSmart(bs, catalog); got != "new-model" {
		t.Fatalf("after refresh resolved %q, want new-model", got)
	}
}

// TestRefreshModelOverrides_ScopesToACPServerAndKeepsUnknownWorkspaces verifies
// overrides stay scoped to the session's ACP server and that sessions whose
// workspace disappeared keep their previous snapshot.
func TestRefreshModelOverrides_ScopesToACPServerAndKeepsUnknownWorkspaces(t *testing.T) {
	opencode := &BackgroundSession{workspaceUUID: "ws1", acpServer: "OpenCode", workingDir: "/repo"}
	otherServer := &BackgroundSession{workspaceUUID: "ws1", acpServer: "Other", workingDir: "/repo"}
	orphan := &BackgroundSession{workspaceUUID: "gone", acpServer: "OpenCode", workingDir: "/elsewhere"}
	orphan.modelOverrides = config.ModelOverrides{
		{ACPServer: "OpenCode", Tag: "Smart", ModelID: "old-model"},
	}
	sm := &SessionManager{
		sessions: map[string]*BackgroundSession{
			"a": opencode,
			"b": otherServer,
			"c": orphan,
		},
		wsRegistry: &WorkspaceRegistry{},
	}
	catalog := overrideCatalog()

	sm.SetWorkspaces([]config.WorkspaceSettings{
		overrideWorkspace("ws1", "OpenCode", "/repo", "new-model"),
	})

	if got := resolveSmart(opencode, catalog); got != "new-model" {
		t.Fatalf("OpenCode session resolved %q, want new-model", got)
	}
	if got := resolveSmart(otherServer, catalog); got != "" {
		t.Fatalf("Other session resolved %q, want no override match", got)
	}
	if got := resolveSmart(orphan, catalog); got != "old-model" {
		t.Fatalf("orphan session resolved %q, want previous override kept", got)
	}
}
