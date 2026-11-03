package conversation

import (
	"strings"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

// publishAgentHiddenRules publishes agent-level rules for the "OpenCode" server
// into a runtime owner shared with the session.
func publishAgentHiddenRules(t *testing.T, rules []string) *config.HiddenModelPolicies {
	t.Helper()
	policies := &config.HiddenModelPolicies{}
	snapshot, err := policies.Prepare([]config.ACPServer{{Name: "OpenCode", HiddenModels: rules}})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !policies.Publish(snapshot) {
		t.Fatal("publish rejected")
	}
	return policies
}

func workspaceHiddenSession(policies *config.HiddenModelPolicies, workspaceRules []string) *BackgroundSession {
	bs := &BackgroundSession{
		workspaceUUID:       "ws1",
		acpServer:           "OpenCode",
		workingDir:          "/repo",
		hiddenModelPolicies: policies,
	}
	bs.setWorkspaceHiddenRules(workspaceRules)
	return bs
}

func TestWorkspaceHiddenModels_UnionOfAgentAndWorkspace(t *testing.T) {
	bs := workspaceHiddenSession(publishAgentHiddenRules(t, []string{"^agent-model$"}), []string{"terra"})

	p, err := bs.hiddenModelPolicy()
	if err != nil {
		t.Fatalf("hiddenModelPolicy: %v", err)
	}
	if !p.Hides("agent-model", "") {
		t.Error("agent-only match must be hidden")
	}
	if !p.Hides("x-terra", "") {
		t.Error("workspace-only match must be hidden")
	}
	if p.Hides("plain", "Plain") {
		t.Error("model matched by neither level must stay selectable")
	}
}

func TestWorkspaceHiddenModels_EmptyWorkspaceIsNoOp(t *testing.T) {
	agentOnly := workspaceHiddenSession(publishAgentHiddenRules(t, []string{"^agent-model$"}), nil)
	p, err := agentOnly.hiddenModelPolicy()
	if err != nil || p == nil || !p.Hides("agent-model", "") || p.Hides("other", "") {
		t.Fatalf("empty workspace rules must equal agent-only policy: %v, %v", p, err)
	}

	none := workspaceHiddenSession(publishAgentHiddenRules(t, nil), []string{"  "})
	if p, err := none.hiddenModelPolicy(); p != nil || err != nil {
		t.Fatalf("no rules at either level: got %v, %v; want nil, nil", p, err)
	}
}

func TestWorkspaceHiddenModels_FailClosedWorkspaceLevel(t *testing.T) {
	bs := workspaceHiddenSession(publishAgentHiddenRules(t, []string{"^agent-model$"}), []string{"("})

	p, err := bs.hiddenModelPolicy()
	if err == nil || p != nil {
		t.Fatalf("invalid workspace regex must fail closed: %v, %v", p, err)
	}
	if !strings.Contains(err.Error(), "workspace") || !strings.Contains(err.Error(), "ws1") {
		t.Errorf("error must name the workspace level and workspace: %v", err)
	}
	if _, _, rerr := bs.readHiddenModelPolicy(); rerr == nil {
		t.Error("read path must also fail closed")
	}
}

func TestWorkspaceHiddenModels_FailClosedAgentAndWorkspaceIndependently(t *testing.T) {
	badAgentCfg := &config.Config{ACPServers: []config.ACPServer{
		{Name: "OpenCode", Command: "c", HiddenModels: []string{"("}},
	}}

	agentBad := &BackgroundSession{workspaceUUID: "ws1", acpServer: "OpenCode", workingDir: "/repo", mittoConfig: badAgentCfg}
	agentBad.setWorkspaceHiddenRules([]string{"ok"})
	if _, err := agentBad.hiddenModelPolicy(); err == nil || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("agent-level compile failure must fail closed naming agent: %v", err)
	}

	bothBad := &BackgroundSession{workspaceUUID: "ws1", acpServer: "OpenCode", workingDir: "/repo", mittoConfig: badAgentCfg}
	bothBad.setWorkspaceHiddenRules([]string{"("})
	err := func() error { _, e := bothBad.hiddenModelPolicy(); return e }()
	if err == nil || !strings.Contains(err.Error(), "agent") || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("both levels bad must report both: %v", err)
	}
}

func TestWorkspaceHiddenModels_RefreshPushesAndExplicitEmptyClears(t *testing.T) {
	bs := workspaceHiddenSession(nil, nil)
	sm := &SessionManager{
		sessions:   map[string]*BackgroundSession{"s1": bs},
		wsRegistry: &WorkspaceRegistry{},
	}

	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws1", ACPServer: "OpenCode", WorkingDir: "/repo", HiddenModels: []string{"^hidden$"}},
	})
	if p, err := bs.hiddenModelPolicy(); err != nil || p == nil || !p.Hides("hidden", "") {
		t.Fatalf("edited workspace rules must apply without restart: %v, %v", p, err)
	}

	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws1", ACPServer: "OpenCode", WorkingDir: "/repo", HiddenModels: []string{}},
	})
	if p, err := bs.hiddenModelPolicy(); err != nil || p != nil {
		t.Fatalf("explicit empty rules must clear the filter: %v, %v", p, err)
	}
}

func TestWorkspaceHiddenModels_DisappearedWorkspaceKeepsLastRules(t *testing.T) {
	orphan := &BackgroundSession{workspaceUUID: "gone", acpServer: "OpenCode", workingDir: "/elsewhere"}
	orphan.setWorkspaceHiddenRules([]string{"^kept$"})
	sm := &SessionManager{
		sessions:   map[string]*BackgroundSession{"c": orphan},
		wsRegistry: &WorkspaceRegistry{},
	}

	sm.SetWorkspaces([]config.WorkspaceSettings{
		{UUID: "ws1", ACPServer: "OpenCode", WorkingDir: "/repo", HiddenModels: []string{"^other$"}},
	})

	if p, err := orphan.hiddenModelPolicy(); err != nil || p == nil || !p.Hides("kept", "") {
		t.Fatalf("orphan session must keep its last workspace rules: %v, %v", p, err)
	}
	if p, _ := orphan.hiddenModelPolicy(); p.Hides("other", "") {
		t.Fatal("rules of another workspace must not leak into orphan session")
	}
}
