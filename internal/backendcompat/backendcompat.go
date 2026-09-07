// Package backendcompat bridges the legacy, ACP-specific persisted shapes
// (internal/config.ACPServer, internal/session.Metadata) to the
// protocol-neutral value types in internal/agentbackend, without requiring
// internal/agentbackend itself to know about config or session — the
// dependency inversion keeps internal/agentbackend a pure leaf package (see
// internal/agentbackend/imports_test.go) while all legacy knowledge lives
// here.
//
// This package is purely additive: it does not replace, hook into, or alter
// any existing load/save path. Legacy persisted shapes (settings.json,
// workspaces.json, session metadata) remain byte-identical; a neutral view
// is only ever produced when a caller explicitly asks for one (see
// docs/devel/agent-backend-architecture.md, mitto-lrt.5).
package backendcompat

import (
	"fmt"

	"github.com/inercia/mitto/internal/agentbackend"
	"github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/session"
)

// BackendIDForACP is the fixed BackendID used for every legacy ACP server
// connection bridged by this package. Every config.ACPServer entry speaks
// ACP today, so they all share one backend identity; a future backend kind
// would get its own BackendID rather than reusing this one.
const BackendIDForACP agentbackend.BackendID = "acp"

// BackendConnectionFromACPServer derives a neutral BackendConnection from a
// legacy config.ACPServer. The result never carries a secret value: Env is
// copied as-is (as legacy behavior already allows it to reference secrets
// indirectly via the process environment), and CredentialRef is left empty
// since ACP servers are always locally-spawned processes today, never a
// remote endpoint.
func BackendConnectionFromACPServer(srv config.ACPServer) (agentbackend.BackendConnection, error) {
	if srv.Name == "" {
		return agentbackend.BackendConnection{}, fmt.Errorf("backendcompat: ACPServer.Name must not be empty")
	}
	if srv.Command == "" {
		return agentbackend.BackendConnection{}, fmt.Errorf("backendcompat: ACPServer %q has no Command", srv.Name)
	}
	conn := agentbackend.BackendConnection{
		Backend:  BackendIDForACP,
		Protocol: agentbackend.ProtocolACP,
		Command:  srv.Command,
		Cwd:      srv.Cwd,
		Env:      srv.Env,
	}
	if err := conn.Validate(); err != nil {
		return agentbackend.BackendConnection{}, err
	}
	return conn, nil
}

// AgentRefFromACPServerName derives a neutral AgentRef from a legacy ACP
// server name. The server name becomes the ProviderID: today one
// config.ACPServer entry hosts exactly one provider, so the two identifier
// spaces coincide, but keeping them as separate typed fields lets a future
// backend host more than one provider without a breaking change.
func AgentRefFromACPServerName(serverName string) (agentbackend.AgentRef, error) {
	ref := agentbackend.AgentRef{
		Backend:  BackendIDForACP,
		Provider: agentbackend.ProviderID(serverName),
	}
	if err := ref.Validate(); err != nil {
		return agentbackend.AgentRef{}, err
	}
	return ref, nil
}

// AgentRefFromStableID derives a neutral AgentRef from an
// agents.AgentDefinition.StableID() value (mitto-lrt.9). Unlike
// AgentRefFromACPServerName, the input here is the agent's stable identity
// (e.g. its ACPId), not the configured ACP server's display name — the two
// may differ (e.g. server name "Auggie (Opus)" vs. agent StableID "auggie").
// This package intentionally does not import internal/agents (a plain
// string keeps the dependency one-directional); callers pass
// def.StableID() directly.
func AgentRefFromStableID(stableID string) (agentbackend.AgentRef, error) {
	ref := agentbackend.AgentRef{
		Backend:  BackendIDForACP,
		Provider: agentbackend.ProviderID(stableID),
	}
	if err := ref.Validate(); err != nil {
		return agentbackend.AgentRef{}, err
	}
	return ref, nil
}

// SessionRefFromMetadata derives a neutral SessionRef from legacy session
// metadata. It never collapses session.Metadata.SessionID and
// session.Metadata.ACPSessionID into a single identifier: SessionID becomes
// SessionRef.ConversationID and ACPSessionID becomes
// SessionRef.ProviderSession, matching the persisted fields exactly (see
// ADR agent-backend-architecture.md §4). meta.ACPServer becomes the
// Provider; it is not validated for existence against current config here
// (see ResolveProviderAlias for that).
func SessionRefFromMetadata(meta session.Metadata) agentbackend.SessionRef {
	return agentbackend.SessionRef{
		ConversationID:  meta.SessionID,
		Provider:        agentbackend.ProviderID(meta.ACPServer),
		ProviderSession: agentbackend.ProviderSessionID(meta.ACPSessionID),
	}
}
