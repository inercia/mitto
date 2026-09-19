package handlers

// Optional, additive protocol-neutral descriptor embedded in existing REST
// session responses and WebSocket "connected"/"acp_started" snapshots
// (mitto-lrt.12). Behavior-preserving: this block is computed only when the
// identifying data is actually available (never synthesized) and rides
// alongside the existing acp_server/acp_session_id/acp_ready fields, which
// keep their current meaning unchanged. See docs/devel/agent-backend-architecture.md.

import (
	"context"
	"log/slog"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/backendcompat"
	"github.com/inercia/mitto/internal/conversation"
	"github.com/inercia/mitto/internal/session"
)

// NeutralAgentRef is the JSON-shaped mirror of agentbackend.AgentRef.
type NeutralAgentRef struct {
	Backend  string `json:"backend"`
	Provider string `json:"provider"`
}

// NeutralSessionRef is the JSON-shaped mirror of agentbackend.SessionRef.
// ConversationID and ProviderSession are kept distinct fields, exactly as
// agentbackend.SessionRef does not collapse the two identifier spaces.
type NeutralSessionRef struct {
	ConversationID  string `json:"conversation_id"`
	Provider        string `json:"provider"`
	ProviderSession string `json:"provider_session,omitempty"`
}

// NeutralModelOption is the JSON-shaped mirror of agentbackend.ModelDescriptor.
type NeutralModelOption struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// NeutralModelState is the JSON-shaped mirror of agentbackend.ModelState.
type NeutralModelState struct {
	CurrentID string               `json:"current_id,omitempty"`
	Available []NeutralModelOption `json:"available,omitempty"`
}

// NeutralConfigOptionValue is the JSON-shaped mirror of agentbackend.ConfigOptionValue.
type NeutralConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// NeutralConfigOption is the JSON-shaped mirror of agentbackend.ConfigOption.
type NeutralConfigOption struct {
	ID       string                     `json:"id"`
	Category string                     `json:"category,omitempty"`
	Current  string                     `json:"current,omitempty"`
	Values   []NeutralConfigOptionValue `json:"values,omitempty"`
}

// NeutralBackendDescriptor is the optional "backend" block. Every field is
// omitempty: a legacy consumer that ignores unknown JSON keys sees no change
// at all, and a neutral-aware consumer only sees the sub-blocks that were
// actually computable for this session.
type NeutralBackendDescriptor struct {
	AgentRef      *NeutralAgentRef      `json:"agent_ref,omitempty"`
	SessionRef    *NeutralSessionRef    `json:"session_ref,omitempty"`
	Capabilities  map[string]string     `json:"capabilities,omitempty"`
	Model         *NeutralModelState    `json:"model,omitempty"`
	ConfigOptions []NeutralConfigOption `json:"config_options,omitempty"`
}

// BuildNeutralBackendDescriptor projects legacy session.Metadata (identity)
// plus, when available, live *conversation.BackgroundSession state
// (capabilities/model/config-options) into the additive neutral descriptor.
// bs may be nil (e.g. archived/suspended session) — identity is still
// computed from persisted metadata, but the live-state sub-blocks are
// omitted. Returns nil when identity cannot be computed (meta.ACPServer
// empty), since the block must never be synthesized.
func BuildNeutralBackendDescriptor(meta session.Metadata, bs *conversation.BackgroundSession) *NeutralBackendDescriptor {
	if meta.ACPServer == "" {
		return nil
	}
	agentRef, err := backendcompat.AgentRefFromACPServerName(meta.ACPServer)
	if err != nil {
		return nil
	}
	sessRef := backendcompat.SessionRefFromMetadata(meta)

	desc := &NeutralBackendDescriptor{
		AgentRef: &NeutralAgentRef{
			Backend:  string(agentRef.Backend),
			Provider: string(agentRef.Provider),
		},
		SessionRef: &NeutralSessionRef{
			ConversationID:  sessRef.ConversationID,
			Provider:        string(sessRef.Provider),
			ProviderSession: string(sessRef.ProviderSession),
		},
	}

	if bs == nil {
		return desc
	}

	desc.Capabilities = neutralCapabilities(bs)

	if models := bs.AgentModels(); models != nil {
		available := make([]NeutralModelOption, 0, len(models.AvailableModels))
		for _, m := range models.AvailableModels {
			modelDesc := ""
			if m.Description != nil {
				modelDesc = *m.Description
			}
			available = append(available, NeutralModelOption{ID: m.ModelId, Name: m.Name, Description: modelDesc})
		}
		desc.Model = &NeutralModelState{CurrentID: models.CurrentModelId, Available: available}
	}

	if cfgOpts := bs.ConfigOptions(); len(cfgOpts) > 0 {
		out := make([]NeutralConfigOption, 0, len(cfgOpts))
		for _, o := range cfgOpts {
			values := make([]NeutralConfigOptionValue, 0, len(o.Options))
			for _, v := range o.Options {
				values = append(values, NeutralConfigOptionValue{Value: v.Value, Name: v.Name, Description: v.Description})
			}
			out = append(out, NeutralConfigOption{ID: o.ID, Category: o.Category, Current: o.CurrentValue, Values: values})
		}
		desc.ConfigOptions = out
	}

	verifyNeutralProvider(bs, desc)

	return desc
}

// verifyNeutralProvider consults bs's live ProviderDiscoverer (mitto-mx9.5),
// when one is available, and cross-checks its result against
// desc.AgentRef.Provider (derived from the persisted meta.ACPServer field).
// This is the first production call site for agentbackend.ProviderDiscovery
// (via the conversation.ProviderDiscoverer seam) outside of tests/fakes.
//
// It never mutates desc: AgentRef.Provider stays sourced from persisted
// identity, since a live probe disagreeing must not silently rewrite an
// identity field derived from on-disk metadata (see the mismatch branch
// below). Today the two values are always equal — ACP is one-provider-
// per-process, so the discoverer reports exactly the same provider the
// legacy path already derived from meta.ACPServer — so this is purely an
// observability/parity check; it becomes meaningful once a non-ACP backend
// can expose more than one provider through the same lease.
func verifyNeutralProvider(bs *conversation.BackgroundSession, desc *NeutralBackendDescriptor) {
	if bs == nil || desc == nil || desc.AgentRef == nil {
		return
	}
	discoverer, ok := bs.NeutralProviderDiscoverer()
	if !ok {
		return
	}
	providers, err := discoverer.Providers(context.Background())
	if err != nil || len(providers) == 0 {
		return
	}
	if string(providers[0]) != desc.AgentRef.Provider {
		slog.Debug("neutral_dto: provider discoverer disagrees with legacy identity; keeping legacy value",
			"legacy_provider", desc.AgentRef.Provider,
			"discovered_provider", providers[0])
	}
}

// neutralCapabilities projects the subset of agentbackend.Feature states
// directly knowable from BackgroundSession's already-public getters. This
// mirrors the "constant fact about this host" reasoning of
// internal/acpbackend's sessionCapabilities (Files/Permissions are always
// supported because Mitto's ACP handshake always advertises Fs read/write
// client capabilities and always wires an auto-approving permission
// handler), without requiring the unexported acpbackend type or a raw
// acp.AgentCapabilities value that BackgroundSession does not expose.
// Unknown/unmodeled features report agentbackend.CapabilityUnknown rather
// than guessing, per the Capabilities contract.
func neutralCapabilities(bs *conversation.BackgroundSession) map[string]string {
	states := map[agentbackend.Feature]agentbackend.CapabilityState{
		agentbackend.FeatureFiles:          agentbackend.CapabilitySupported,
		agentbackend.FeaturePermissions:    agentbackend.CapabilitySupported,
		agentbackend.FeatureTerminals:      agentbackend.CapabilityUnknown,
		agentbackend.FeatureImages:         agentbackend.CapabilityUnsupported,
		agentbackend.FeatureModelSelection: agentbackend.CapabilityUnsupported,
		agentbackend.FeatureModeSelection:  agentbackend.CapabilityUnsupported,
	}

	if bs.AgentSupportsImages() {
		states[agentbackend.FeatureImages] = agentbackend.CapabilitySupported
	}

	if models := bs.AgentModels(); models != nil && len(models.AvailableModels) > 0 {
		states[agentbackend.FeatureModelSelection] = agentbackend.CapabilitySupported
	}

	for _, opt := range bs.ConfigOptions() {
		if opt.Category == conversation.ConfigOptionCategoryMode {
			states[agentbackend.FeatureModeSelection] = agentbackend.CapabilitySupported
			break
		}
	}

	out := make(map[string]string, len(states))
	for feature, state := range states {
		out[string(feature)] = state.String()
	}
	return out
}
