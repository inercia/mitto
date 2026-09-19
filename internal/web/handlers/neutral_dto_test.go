// neutral_dto_test.go — unit tests for the optional, additive protocol-neutral
// "backend" descriptor (mitto-lrt.12). Covers: identity never synthesized
// when meta.ACPServer is empty; identity-only projection when no live
// BackgroundSession is attached (archived/suspended sessions); the two
// identifier spaces (ConversationID vs. ProviderSession) never collapsing;
// and the three-state capability projection (unknown/supported/unsupported)
// plus model/config-option mirroring once a live session is attached.
package handlers

import (
	"errors"
	"testing"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

func TestBuildNeutralBackendDescriptor_NilWhenACPServerEmpty(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-1", ACPServer: ""}
	if desc := BuildNeutralBackendDescriptor(meta, nil); desc != nil {
		t.Fatalf("expected nil descriptor for empty ACPServer (never synthesized), got %+v", desc)
	}
}

func TestBuildNeutralBackendDescriptor_IdentityOnly_NoLiveSession(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-1", ACPServer: "Auggie", ACPSessionID: "upstream-42"}
	desc := BuildNeutralBackendDescriptor(meta, nil)
	if desc == nil {
		t.Fatal("expected a non-nil descriptor when ACPServer is set")
	}
	if desc.AgentRef == nil || desc.AgentRef.Backend != "acp" || desc.AgentRef.Provider != "Auggie" {
		t.Errorf("AgentRef = %+v, want {Backend: acp, Provider: Auggie}", desc.AgentRef)
	}
	if desc.SessionRef == nil {
		t.Fatal("expected a non-nil SessionRef")
	}
	if desc.SessionRef.ConversationID != "conv-1" || desc.SessionRef.Provider != "Auggie" || desc.SessionRef.ProviderSession != "upstream-42" {
		t.Errorf("SessionRef = %+v, want {conv-1, Auggie, upstream-42}", desc.SessionRef)
	}
	// The two identifier spaces must never collapse.
	if desc.SessionRef.ConversationID == desc.SessionRef.ProviderSession {
		t.Fatalf("ConversationID and ProviderSession collapsed to %q", desc.SessionRef.ConversationID)
	}
	// No live session attached: capability/model/config sub-blocks stay absent.
	if desc.Capabilities != nil || desc.Model != nil || desc.ConfigOptions != nil {
		t.Errorf("expected no live-state sub-blocks without a BackgroundSession, got %+v", desc)
	}
}

func TestBuildNeutralBackendDescriptor_EmptyACPSessionIDPreserved(t *testing.T) {
	// A session that has never resumed has no ACPSessionID yet; the neutral
	// projection must reflect that as empty, never synthesizing a value.
	meta := session.Metadata{SessionID: "conv-2", ACPServer: "Auggie"}
	desc := BuildNeutralBackendDescriptor(meta, nil)
	if desc == nil || desc.SessionRef == nil {
		t.Fatal("expected a non-nil descriptor/SessionRef")
	}
	if desc.SessionRef.ProviderSession != "" {
		t.Errorf("ProviderSession = %q, want empty", desc.SessionRef.ProviderSession)
	}
}

func TestBuildNeutralBackendDescriptor_CapabilitiesBaseline(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-3", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{SessionID: "conv-3"})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if desc == nil {
		t.Fatal("expected a non-nil descriptor")
	}
	want := map[string]string{
		"files":           "supported",
		"permissions":     "supported",
		"terminals":       "unknown",
		"images":          "unsupported",
		"model_selection": "unsupported",
		"mode_selection":  "unsupported",
	}
	for feature, wantState := range want {
		if got := desc.Capabilities[feature]; got != wantState {
			t.Errorf("Capabilities[%q] = %q, want %q (full map: %+v)", feature, got, wantState, desc.Capabilities)
		}
	}
	if desc.Model != nil {
		t.Errorf("expected nil Model with no agent models, got %+v", desc.Model)
	}
	if desc.ConfigOptions != nil {
		t.Errorf("expected nil ConfigOptions with none set, got %+v", desc.ConfigOptions)
	}
}

func TestBuildNeutralBackendDescriptor_ImagesSupportedWhenAgentAdvertises(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-4", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:           "conv-4",
		AgentSupportsImages: true,
	})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if got := desc.Capabilities["images"]; got != "supported" {
		t.Errorf("Capabilities[images] = %q, want supported", got)
	}
}

func TestBuildNeutralBackendDescriptor_ModelSelectionAndModelMirror(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-5", ACPServer: "Auggie"}
	desc2 := "A larger model"
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID: "conv-5",
		AgentModels: &conversation.SessionModelState{
			CurrentModelId: "m-1",
			AvailableModels: []conversation.ModelInfo{
				{ModelId: "m-1", Name: "Model 1"},
				{ModelId: "m-2", Name: "Model 2", Description: &desc2},
			},
		},
	})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if got := desc.Capabilities["model_selection"]; got != "supported" {
		t.Errorf("Capabilities[model_selection] = %q, want supported", got)
	}
	if desc.Model == nil {
		t.Fatal("expected a non-nil Model block")
	}
	if desc.Model.CurrentID != "m-1" {
		t.Errorf("Model.CurrentID = %q, want m-1", desc.Model.CurrentID)
	}
	if len(desc.Model.Available) != 2 {
		t.Fatalf("Model.Available = %+v, want 2 entries", desc.Model.Available)
	}
	if desc.Model.Available[1].Description != "A larger model" {
		t.Errorf("Model.Available[1].Description = %q, want %q", desc.Model.Available[1].Description, "A larger model")
	}
	if desc.Model.Available[0].Description != "" {
		t.Errorf("Model.Available[0].Description = %q, want empty (nil pointer dereferenced safely)", desc.Model.Available[0].Description)
	}
}

func TestBuildNeutralBackendDescriptor_ModeSelectionAndConfigOptionsMirror(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-6", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID: "conv-6",
		ConfigOptions: []conversation.SessionConfigOption{{
			ID:           conversation.ConfigOptionCategoryMode,
			Category:     conversation.ConfigOptionCategoryMode,
			CurrentValue: "default",
			Options: []conversation.SessionConfigOptionValue{
				{Value: "default", Name: "Default"},
				{Value: "plan", Name: "Plan", Description: "Plan-only mode"},
			},
		}},
	})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if got := desc.Capabilities["mode_selection"]; got != "supported" {
		t.Errorf("Capabilities[mode_selection] = %q, want supported", got)
	}
	if len(desc.ConfigOptions) != 1 {
		t.Fatalf("ConfigOptions = %+v, want 1 entry", desc.ConfigOptions)
	}
	opt := desc.ConfigOptions[0]
	if opt.ID != "mode" || opt.Category != "mode" || opt.Current != "default" {
		t.Errorf("ConfigOptions[0] = %+v, want {ID: mode, Category: mode, Current: default}", opt)
	}
	if len(opt.Values) != 2 || opt.Values[1].Value != "plan" || opt.Values[1].Description != "Plan-only mode" {
		t.Errorf("ConfigOptions[0].Values = %+v, unexpected shape", opt.Values)
	}
}

// TestBuildNeutralBackendDescriptor_ProviderDiscovererAgrees_LegacyValueKept
// proves the mitto-mx9.5 verifyNeutralProvider parity check: when the live
// ProviderDiscoverer's result agrees with the legacy meta.ACPServer-derived
// AgentRef.Provider (the always-true case for ACP, which is
// one-provider-per-process), AgentRef.Provider still reports the legacy
// value unchanged — this is a pure observability/parity check, never a
// second source of truth.
func TestBuildNeutralBackendDescriptor_ProviderDiscovererAgrees_LegacyValueKept(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-7", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:          "conv-7",
		ProviderDiscoverer: conversation.NewTestProviderDiscoverer(agentbackend.ProviderID("Auggie")),
	})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if desc == nil || desc.AgentRef == nil {
		t.Fatal("expected a non-nil descriptor/AgentRef")
	}
	if desc.AgentRef.Provider != "Auggie" {
		t.Errorf("AgentRef.Provider = %q, want %q (legacy value, discoverer agrees)", desc.AgentRef.Provider, "Auggie")
	}
}

// TestBuildNeutralBackendDescriptor_ProviderDiscovererDisagrees_LegacyValueKept
// proves the mitto-mx9.5 fail-safe: a live discoverer disagreeing with the
// persisted identity must NEVER silently overwrite AgentRef.Provider — that
// field stays sourced from meta.ACPServer regardless of what the discoverer
// reports (mx9's "no user-visible change" invariant).
func TestBuildNeutralBackendDescriptor_ProviderDiscovererDisagrees_LegacyValueKept(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-8", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:          "conv-8",
		ProviderDiscoverer: conversation.NewTestProviderDiscoverer(agentbackend.ProviderID("SomeOtherProvider")),
	})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if desc == nil || desc.AgentRef == nil {
		t.Fatal("expected a non-nil descriptor/AgentRef")
	}
	if desc.AgentRef.Provider != "Auggie" {
		t.Errorf("AgentRef.Provider = %q, want %q (legacy value, never overwritten by a disagreeing discoverer)", desc.AgentRef.Provider, "Auggie")
	}
}

// TestBuildNeutralBackendDescriptor_ProviderDiscovererErrors_LegacyValueKept
// proves a discoverer error (or an empty result) is a silent no-op: the
// descriptor is still built successfully from the legacy value, with no
// panic and no mutation.
func TestBuildNeutralBackendDescriptor_ProviderDiscovererErrors_LegacyValueKept(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-9", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{
		SessionID:          "conv-9",
		ProviderDiscoverer: conversation.NewTestProviderDiscovererError(errors.New("discovery unavailable")),
	})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if desc == nil || desc.AgentRef == nil {
		t.Fatal("expected a non-nil descriptor/AgentRef")
	}
	if desc.AgentRef.Provider != "Auggie" {
		t.Errorf("AgentRef.Provider = %q, want %q (legacy value kept on discoverer error)", desc.AgentRef.Provider, "Auggie")
	}
}

// TestBuildNeutralBackendDescriptor_NoProviderDiscoverer_LegacyValueKept
// proves the common case today (no BackendProvider injected, e.g. every
// existing test above using a bare conversation.NewTestBackgroundSession):
// verifyNeutralProvider's discoverer lookup returns ok=false and is a no-op,
// so AgentRef.Provider is unaffected.
func TestBuildNeutralBackendDescriptor_NoProviderDiscoverer_LegacyValueKept(t *testing.T) {
	meta := session.Metadata{SessionID: "conv-10", ACPServer: "Auggie"}
	bs := conversation.NewTestBackgroundSession(conversation.BackgroundSessionTestOpts{SessionID: "conv-10"})
	desc := BuildNeutralBackendDescriptor(meta, bs)
	if desc == nil || desc.AgentRef == nil {
		t.Fatal("expected a non-nil descriptor/AgentRef")
	}
	if desc.AgentRef.Provider != "Auggie" {
		t.Errorf("AgentRef.Provider = %q, want %q (no discoverer injected)", desc.AgentRef.Provider, "Auggie")
	}
}
