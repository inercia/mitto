package conversation

import (
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// Tests for mitto-b3qe: auto-children select their model by capability tag,
// resolved lazily against the child's own agent (see
// resolveAutoChildInitialModelPreference and cbInitBaselineModelIfEmpty).

func TestResolveAutoChildInitialModelPreference(t *testing.T) {
	tests := []struct {
		name     string
		child    config.AutoChild
		targetWS *config.WorkspaceSettings
		cfg      *config.Config
		want     []config.PromptPreferredModel
	}{
		{
			name:     "child ModelTag wins over everything else",
			child:    config.AutoChild{Title: "Coder", ModelTag: "Coding"},
			targetWS: &config.WorkspaceSettings{ACPServer: "auggie", InitialModelProfile: "Claude Opus"},
			cfg: &config.Config{ACPServers: []config.ACPServer{
				{Name: "auggie", InitialModelTag: "Smart"},
			}},
			want: []config.PromptPreferredModel{{ModelTag: "Coding"}},
		},
		{
			name:     "no child tag: falls back to target workspace initial-model preference",
			child:    config.AutoChild{Title: "Coder"},
			targetWS: &config.WorkspaceSettings{ACPServer: "auggie", InitialModelTag: "Smart"},
			cfg: &config.Config{ACPServers: []config.ACPServer{
				{Name: "auggie", InitialModelTag: "Cheap"},
			}},
			want: []config.PromptPreferredModel{{ModelTag: "Smart"}},
		},
		{
			name:     "no child tag, no workspace preference: falls back to target ACP server preference",
			child:    config.AutoChild{Title: "Coder"},
			targetWS: &config.WorkspaceSettings{ACPServer: "auggie"},
			cfg: &config.Config{ACPServers: []config.ACPServer{
				{Name: "auggie", InitialModelTag: "Cheap"},
			}},
			want: []config.PromptPreferredModel{{ModelTag: "Cheap"}},
		},
		{
			name:     "nothing configured anywhere: nil (agent default)",
			child:    config.AutoChild{Title: "Coder"},
			targetWS: &config.WorkspaceSettings{ACPServer: "auggie"},
			cfg:      &config.Config{ACPServers: []config.ACPServer{{Name: "auggie"}}},
			want:     nil,
		},
		{
			name:     "target ACP server not found in config: nil, no panic",
			child:    config.AutoChild{Title: "Coder"},
			targetWS: &config.WorkspaceSettings{ACPServer: "missing-server"},
			cfg:      &config.Config{},
			want:     nil,
		},
		{
			name:     "nil cfg: nil, no panic",
			child:    config.AutoChild{Title: "Coder"},
			targetWS: &config.WorkspaceSettings{ACPServer: "auggie"},
			cfg:      nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAutoChildInitialModelPreference(tt.child, tt.targetWS, tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveAutoChildInitialModelPreference() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestResumeBackgroundSession_SetsInitialModelPreference is a regression test
// for mitto-b3qe: the resume constructor previously never set
// bs.initialModelPreference, so auto-children (and workspace/ACP-server
// initial_model_tag/initial_model_profile) were silently ignored on resume.
func TestResumeBackgroundSession_SetsInitialModelPreference(t *testing.T) {
	fp := newFakeSharedProcess()
	pref := []config.PromptPreferredModel{{ModelTag: "Coding"}}

	bs, err := ResumeBackgroundSession(BackgroundSessionConfig{
		PersistedID:            "test-initial-model-pref-resume",
		ACPServer:              "test-server",
		WorkingDir:             "/tmp",
		SharedProcess:          fp,
		InitialModelPreference: pref,
	})
	if err != nil {
		t.Fatalf("ResumeBackgroundSession failed: %v", err)
	}
	if !reflect.DeepEqual(bs.initialModelPreference, pref) {
		t.Errorf("bs.initialModelPreference = %+v, want %+v", bs.initialModelPreference, pref)
	}
}

// TestCreateAutoChildren_ModelTagAndWorkspaceFallback_IntegratesResolution is an
// integration-level check that createAutoChildren wires
// resolveAutoChildInitialModelPreference correctly end-to-end (target-workspace
// resolution, GetWorkspaceByUUID lookup) for both a child with its own ModelTag
// and one that must fall back to the target workspace's InitialModelTag. The
// ACP resume itself still fails in unit tests (no real ACP server), but
// store.Create happens first so the persisted metadata proves the pipeline ran
// without panicking on either branch.
func TestCreateAutoChildren_ModelTagAndWorkspaceFallback_IntegratesResolution(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sm := NewSessionManager("", "test-server", false, nil)
	sm.SetStore(store)

	targetWS := config.WorkspaceSettings{
		UUID:            "ws-target",
		ACPServer:       "target-server",
		WorkingDir:      "/work-auto-children-tags",
		InitialModelTag: "Smart", // fallback preference when a child has no ModelTag of its own
	}
	parentWS := config.WorkspaceSettings{
		UUID:       "ws-parent-tags",
		ACPServer:  "test-server",
		WorkingDir: "/work-auto-children-tags",
		AutoChildren: []config.AutoChild{
			{Title: "Coder", ModelTag: "Coding"},                  // own tag wins
			{Title: "Reviewer", TargetWorkspaceUUID: "ws-target"}, // falls back to targetWS.InitialModelTag
		},
	}
	sm.SetWorkspaces([]config.WorkspaceSettings{parentWS, targetWS})

	parentID := session.GenerateSessionID()
	parentBS := &BackgroundSession{persistedID: parentID, workingDir: parentWS.WorkingDir}
	if err := store.Create(session.Metadata{
		SessionID: parentID, Status: "active", ACPServer: parentWS.ACPServer, WorkingDir: parentWS.WorkingDir,
	}); err != nil {
		t.Fatalf("store.Create(parent): %v", err)
	}

	sm.createAutoChildren(parentBS, &parentWS)

	metas, err := store.List()
	if err != nil {
		t.Fatalf("store.List: %v", err)
	}
	var children []session.Metadata
	for _, m := range metas {
		if m.ParentSessionID == parentID {
			children = append(children, m)
		}
	}
	if got, want := len(children), 2; got != want {
		t.Fatalf("auto-child count = %d, want %d (metas=%+v)", got, want, metas)
	}
	// The second child (no own tag) must resolve its ACP server from the
	// target workspace, proving GetWorkspaceByUUID + the fallback chain ran.
	foundReviewer := false
	for _, c := range children {
		if c.Name == "Reviewer" {
			foundReviewer = true
			if c.ACPServer != targetWS.ACPServer {
				t.Errorf("Reviewer child ACPServer = %q, want %q (from target workspace)", c.ACPServer, targetWS.ACPServer)
			}
		}
	}
	if !foundReviewer {
		t.Fatalf("Reviewer child not found among persisted children: %+v", children)
	}
}

// TestCbInitBaselineModelIfEmpty_PersistedBaselineWinsOverInitialModelPreference
// pins down that a persisted BaselineModel always wins over re-resolving
// InitialModelPreference on a later resume (mitto-b3qe requirement: an
// auto-child's tag is resolved once, at creation time, not on every resume).
func TestCbInitBaselineModelIfEmpty_PersistedBaselineWinsOverInitialModelPreference(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	sessionID := "test-persisted-baseline-wins"
	if err := store.Create(session.Metadata{SessionID: sessionID, BaselineModel: "persisted-model"}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	bs := &BackgroundSession{
		persistedID: sessionID,
		store:       store,
		mittoConfig: &config.Config{},
		// A non-empty preference must be ignored entirely once a persisted
		// baseline exists — this is what auto-children rely on to avoid
		// re-resolving (and possibly flapping) the tag on every resume.
		initialModelPreference: []config.PromptPreferredModel{{ModelTag: "Coding"}},
	}
	bs.agentModels = &SessionModelState{
		CurrentModelId: "agent-default",
		AvailableModels: []ModelInfo{
			{ModelId: "agent-default"},
			{ModelId: "some-coding-model", Name: "Claude Sonnet 5"},
		},
	}
	bs.cbInitBaselineModelIfEmpty("agent-default")

	if got := bs.baselineModel; got != "persisted-model" {
		t.Errorf("baselineModel = %q, want %q (persisted baseline must win over InitialModelPreference)", got, "persisted-model")
	}
}
