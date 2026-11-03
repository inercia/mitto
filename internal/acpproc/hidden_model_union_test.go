package acpproc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	mittoAcp "github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/conversation"
)

// Harness shared by the combined agent+workspace hidden-model policy tests: a
// manager with a published agent snapshot, a literal shared process, and an
// auxiliary session state wired to it.
func newHiddenPolicyManager(t *testing.T, servers []config.ACPServer) (*ACPProcessManager, *config.HiddenModelPolicies) {
	t.Helper()
	var policies config.HiddenModelPolicies
	snapshot, err := policies.Prepare(servers)
	if err != nil {
		t.Fatalf("prepare agent policy: %v", err)
	}
	if !policies.Publish(snapshot) {
		t.Fatal("publish agent policy")
	}
	m := NewACPProcessManager(context.Background(), nil)
	t.Cleanup(m.Close)
	m.HiddenModelPolicies = &policies
	return m, &policies
}

func newAuxDispatchFixture(t *testing.T, m *ACPProcessManager, raw *conversation.SessionModelState) (*SharedACPProcess, *auxiliarySessionState) {
	t.Helper()
	process := &SharedACPProcess{config: SharedACPProcessConfig{ACPServer: "server", WorkingDir: "/workspace"}}
	state := &auxiliarySessionState{
		workspaceUUID:     "ws",
		configuredServer:  "server",
		sessionFolder:     "/workspace",
		process:           process,
		processGeneration: process.Generation(),
		modelID:           raw.CurrentModelId,
		rawModels:         cloneAuxiliaryModelState(raw),
	}
	return process, state
}

func workspaceSettings(rules []string) *config.WorkspaceSettings {
	return &config.WorkspaceSettings{UUID: "ws", ACPServer: "server", HiddenModels: rules}
}

// TestResolveAuxiliaryModel_UnionAgentAndWorkspaceHiding proves the union: the
// resolver excludes a model hidden by the agent level alone, one hidden by the
// workspace level alone, and only falls back to a model eligible under both.
func TestResolveAuxiliaryModel_UnionAgentAndWorkspaceHiding(t *testing.T) {
	m, _ := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server", HiddenModels: []string{"^old$"}}})
	raw := &conversation.SessionModelState{CurrentModelId: "old", AvailableModels: []conversation.ModelInfo{
		{ModelId: "old", Name: "Old"}, {ModelId: "new", Name: "New"}, {ModelId: "spare", Name: "Spare"},
	}}

	// Agent half alone: hidden old, new eligible → switch to new.
	target, shouldSet, err := m.resolveAuxiliaryModel(nil, raw, "server", "ws", "title-gen")
	if err != nil || target != "new" || !shouldSet {
		t.Fatalf("agent-only resolution = (%q, %t, %v), want (new, true, nil)", target, shouldSet, err)
	}

	// Workspace half alone (no agent rules published for this server): the
	// current model "new" is hidden by workspace rules → switch to old.
	wsOnly, err := policiesWithServerHiddenModels(m.HiddenModelPolicies, "server", []string{})
	if err != nil {
		t.Fatalf("publish agent-empty policy: %v", err)
	}
	m.HiddenModelPolicies.Publish(wsOnly)
	wsRaw := &conversation.SessionModelState{CurrentModelId: "new", AvailableModels: []conversation.ModelInfo{
		{ModelId: "new", Name: "New"}, {ModelId: "old", Name: "Old"}, {ModelId: "spare", Name: "Spare"},
	}}
	target, shouldSet, err = m.resolveAuxiliaryModel(workspaceSettings([]string{"^new$"}), wsRaw, "server", "ws", "title-gen")
	if err != nil || target != "old" || !shouldSet {
		t.Fatalf("workspace-only resolution = (%q, %t, %v), want (old, true, nil)", target, shouldSet, err)
	}

	// Union: agent hides old, workspace hides "new" (the current model) → spare
	// is the only eligible model.
	next, err := policiesWithServerHiddenModels(m.HiddenModelPolicies, "server", []string{"^old$"})
	if err != nil {
		t.Fatalf("publish agent policy: %v", err)
	}
	m.HiddenModelPolicies.Publish(next)
	target, shouldSet, err = m.resolveAuxiliaryModel(workspaceSettings([]string{"^new$"}), wsRaw, "server", "ws", "title-gen")
	if err != nil || target != "spare" || !shouldSet {
		t.Fatalf("union resolution = (%q, %t, %v), want (spare, true, nil)", target, shouldSet, err)
	}
}

// TestResolveAuxiliaryModel_WorkspaceRulesFailClosed proves an invalid
// workspace rule fails closed (all advertised models hidden) regardless of the
// agent level.
func TestResolveAuxiliaryModel_WorkspaceRulesFailClosed(t *testing.T) {
	m, _ := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server"}})
	raw := &conversation.SessionModelState{CurrentModelId: "visible", AvailableModels: []conversation.ModelInfo{
		{ModelId: "visible", Name: "Visible"},
	}}
	_, _, err := m.resolveAuxiliaryModel(workspaceSettings([]string{"("}), raw, "server", "ws", "title-gen")
	if err == nil {
		t.Fatal("invalid workspace rule unexpectedly resolved")
	}
	var unavailable *mittoAcp.AuxiliaryModelUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want AuxiliaryModelUnavailableError", err)
	}
	if unavailable.Reason != "all advertised models are hidden by policy" {
		t.Fatalf("reason = %q, want fail-closed all-hidden reason", unavailable.Reason)
	}
}

// TestResolveAuxiliaryModel_EmptyWorkspaceRulesKeepsAgentBehavior proves the
// no-rules regression: empty workspace rules behave byte-identically to no
// workspace settings at all.
func TestResolveAuxiliaryModel_EmptyWorkspaceRulesKeepsAgentBehavior(t *testing.T) {
	m, _ := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server", HiddenModels: []string{"^old$"}}})
	raw := &conversation.SessionModelState{CurrentModelId: "old", AvailableModels: []conversation.ModelInfo{
		{ModelId: "old", Name: "Old"}, {ModelId: "new", Name: "New"},
	}}
	nilTarget, nilSwitch, nilErr := m.resolveAuxiliaryModel(nil, raw, "server", "ws", "title-gen")
	emptyTarget, emptySwitch, emptyErr := m.resolveAuxiliaryModel(workspaceSettings(nil), raw, "server", "ws", "title-gen")
	if nilTarget != emptyTarget || nilSwitch != emptySwitch || !errors.Is(emptyErr, nilErr) && (nilErr == nil) != (emptyErr == nil) {
		t.Fatalf("empty rules resolution = (%q, %t, %v), want agent-only (%q, %t, %v)", emptyTarget, emptySwitch, emptyErr, nilTarget, nilSwitch, nilErr)
	}
}

// TestAdmitAuxiliaryDispatch_WorkspaceRulesBlockDispatch proves a dispatch of a
// model hidden by the workspace level alone is rejected even when the agent
// level hides nothing.
func TestAdmitAuxiliaryDispatch_WorkspaceRulesBlockDispatch(t *testing.T) {
	m, _ := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server"}})
	raw := &conversation.SessionModelState{CurrentModelId: "new", AvailableModels: []conversation.ModelInfo{
		{ModelId: "new", Name: "New"}, {ModelId: "spare", Name: "Spare"},
	}}
	process, state := newAuxDispatchFixture(t, m, raw)
	err := m.admitAuxiliaryDispatch(process, "ws", "title-gen", state, workspaceSettings([]string{"^new$"}))
	if err == nil {
		t.Fatal("dispatch of a workspace-hidden current model unexpectedly allowed")
	}
	var unavailable *mittoAcp.AuxiliaryModelUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %v, want AuxiliaryModelUnavailableError", err)
	}
}

// TestAdmitAuxiliaryDispatch_UnionReverificationExact confirms a target that
// remains eligible under the full union dispatches cleanly.
func TestAdmitAuxiliaryDispatch_UnionReverificationExact(t *testing.T) {
	m, _ := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server", HiddenModels: []string{"^old$"}}})
	raw := &conversation.SessionModelState{CurrentModelId: "new", AvailableModels: []conversation.ModelInfo{
		{ModelId: "old", Name: "Old"}, {ModelId: "new", Name: "New"},
	}}
	process, state := newAuxDispatchFixture(t, m, raw)
	if err := m.admitAuxiliaryDispatch(process, "ws", "title-gen", state, workspaceSettings([]string{"^spare$"})); err != nil {
		t.Fatalf("eligible current model unexpectedly blocked: %v", err)
	}
}

// TestAdmitSetModelPolicy_UnionAgentAndWorkspace proves process-level set_model
// admission (residual risk R1) enforces the combined policy: a model hidden by
// the agent level, one hidden by the workspace level, and one hidden by neither.
func TestAdmitSetModelPolicy_UnionAgentAndWorkspace(t *testing.T) {
	m, policies := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server", HiddenModels: []string{"^agent-hidden$"}}})
	p := &SharedACPProcess{config: SharedACPProcessConfig{
		ACPServer:            "server",
		HiddenModelPolicies:  policies,
		WorkspaceHiddenRules: func() []string { return []string{"^workspace-hidden$"} },
	}}

	// Workspace-hidden only: same error shape as the agent-only rejection.
	err := p.admitSetModelPolicy("session-a", "workspace-hidden")
	if err == nil || !strings.Contains(err.Error(), "is hidden by current policy") {
		t.Fatalf("workspace-hidden set_model error = %v, want hidden-by-policy error", err)
	}
	// Agent-hidden only.
	if err := p.admitSetModelPolicy("session-a", "agent-hidden"); err == nil || !strings.Contains(err.Error(), "is hidden by current policy") {
		t.Fatalf("agent-hidden set_model error = %v, want hidden-by-policy error", err)
	}
	// Neither level hides it.
	if err := p.admitSetModelPolicy("session-a", "visible"); err != nil {
		t.Fatalf("eligible set_model error = %v, want nil", err)
	}
	// Case-insensitive parity with the rule semantics.
	if err := p.admitSetModelPolicy("session-a", "WORKSPACE-HIDDEN"); err == nil {
		t.Fatal("case-insensitive workspace rule unexpectedly allowed")
	}
	_ = m
}

// TestAdmitSetModelPolicy_WorkspaceRulesWithoutSnapshotInfrastructure proves
// workspace rules still gate set_model when the manager wires no snapshot
// publication (defensive parity with the wired production path).
func TestAdmitSetModelPolicy_WorkspaceRulesWithoutSnapshotInfrastructure(t *testing.T) {
	p := &SharedACPProcess{config: SharedACPProcessConfig{
		ACPServer:            "server",
		WorkspaceHiddenRules: func() []string { return []string{"^workspace-hidden$"} },
	}}
	if err := p.admitSetModelPolicy("session-a", "workspace-hidden"); err == nil || !strings.Contains(err.Error(), "is hidden by current policy") {
		t.Fatalf("error = %v, want hidden-by-policy error without publication", err)
	}
	if err := p.admitSetModelPolicy("session-a", "visible"); err != nil {
		t.Fatalf("eligible set_model error = %v, want nil", err)
	}
}

// TestAdmitSetModelPolicy_WorkspaceRulesFailClosed proves invalid workspace
// rules block every set_model switch with the published error string.
func TestAdmitSetModelPolicy_WorkspaceRulesFailClosed(t *testing.T) {
	m, policies := newHiddenPolicyManager(t, []config.ACPServer{{Name: "server"}})
	p := &SharedACPProcess{config: SharedACPProcessConfig{
		ACPServer:            "server",
		HiddenModelPolicies:  policies,
		WorkspaceHiddenRules: func() []string { return []string{"("} },
	}}
	if err := p.admitSetModelPolicy("session-a", "anything"); err == nil || !strings.Contains(err.Error(), "is hidden by current policy") {
		t.Fatalf("error = %v, want fail-closed hidden-by-policy error", err)
	}
	_ = m
}

// TestAdmitSetModelPolicy_NilWorkspaceRulesKeepsAgentBehavior proves the
// nil-resolver regression: no workspace rules and nil publication keep legacy
// allow-all behavior.
func TestAdmitSetModelPolicy_NilWorkspaceRulesKeepsAgentBehavior(t *testing.T) {
	p := &SharedACPProcess{config: SharedACPProcessConfig{ACPServer: "server"}}
	if err := p.admitSetModelPolicy("session-a", "visible"); err != nil {
		t.Fatalf("nil-infrastructure set_model error = %v, want nil", err)
	}
}

// TestWorkspaceHiddenRulesResolverNilSafe proves the manager resolver is
// nil-safe on a missing provider and a missing workspace.
func TestWorkspaceHiddenRulesResolverNilSafe(t *testing.T) {
	m := NewACPProcessManager(context.Background(), nil)
	defer m.Close()
	resolver := m.workspaceHiddenRulesResolver("ws")
	if resolver == nil {
		t.Fatal("resolver is nil, want a callable")
	}
	if rules := resolver(); rules != nil {
		t.Fatalf("resolver without provider rules = %v, want nil", rules)
	}
	m.WorkspaceConfigProvider = func(uuid string) *config.WorkspaceSettings {
		if uuid != "ws" {
			return nil
		}
		return workspaceSettings([]string{"^hidden$"})
	}
	if rules := resolver(); rules == nil || len(rules) != 1 || rules[0] != "^hidden$" {
		t.Fatalf("resolver rules = %v, want workspace rules", rules)
	}
	if rules := m.workspaceHiddenRulesResolver("unknown")(); rules != nil {
		t.Fatalf("resolver for unknown workspace rules = %v, want nil", rules)
	}
}

// TestSharedProcessConfigMatchesWorkspace_IgnoresWorkspaceHiddenRules proves
// the reuse key stays independent of the function-field wiring: only a fn
// difference must not invalidate a matching process.
func TestSharedProcessConfigMatchesWorkspace_IgnoresWorkspaceHiddenRules(t *testing.T) {
	p := &SharedACPProcess{config: SharedACPProcessConfig{
		ACPServer:            "Auggie",
		ACPCommand:           "auggie --acp",
		ACPCwd:               "/cwd",
		HiddenModelPolicies:  nil,
		WorkspaceHiddenRules: func() []string { return []string{"^hidden$"} },
	}}
	if !sharedProcessConfigMatchesWorkspace(p, "Auggie", "auggie --acp", "/cwd", nil) {
		t.Error("a WorkspaceHiddenRules fn field must not affect process reuse")
	}
}

// policiesWithServerHiddenModels republishes a snapshot with the given rules
// for one server, leaving other servers intact (single-server tests only).
func policiesWithServerHiddenModels(policies *config.HiddenModelPolicies, server string, rules []string) (*config.HiddenModelSnapshot, error) {
	if policies == nil {
		return nil, fmt.Errorf("no publication owner")
	}
	return policies.Prepare([]config.ACPServer{{Name: server, HiddenModels: rules}})
}
