package agents

import "sync"

// ProviderReach describes how a provider is reached at runtime, independent
// of its AvailabilityState. A Local provider is backed by a known
// AgentDefinition on disk (install/status/mcp-* scripts may exist for it); a
// Remote provider is only host-advertised (e.g. by a future non-ACP
// backend) and has no local scripts (see
// docs/devel/agent-backend-architecture.md, mitto-lrt.9).
type ProviderReach string

const (
	// ReachLocal identifies a provider backed by a local AgentDefinition.
	ReachLocal ProviderReach = "local"
	// ReachRemote identifies a provider advertised only by a connected
	// backend host, with no local AgentDefinition/scripts.
	ReachRemote ProviderReach = "remote"
)

// Reach reports how this definition is reached. Every AgentDefinition loaded
// from disk (via Manager.ListAgents/GetAgent) is, by construction, backed by
// local metadata and — for whichever commands exist — local scripts, so this
// is always ReachLocal. A future remote-only provider descriptor (no
// AgentDefinition) is represented directly as a ConfiguredProvider with no
// matching StableID instead (see ComposeAvailability), never as an
// AgentDefinition.
func (a *AgentDefinition) Reach() ProviderReach { return ReachLocal }

// AvailabilityState is a four-state view of whether a provider is a usable
// runtime choice, distinguishing states that were previously collapsed into
// a single "available" boolean (mitto-lrt.9):
//
//   - Installed:  a local binary/CLI for this agent was detected (status.sh
//     Installed=true). Only ever true for ReachLocal providers.
//   - Configured: a live config entry (e.g. config.ACPServer) references
//     this provider.
//   - Connected:  a backend Connection is currently up for this provider.
//   - Available:  usable as a runtime choice right now. Fail-closed: never
//     true merely because a descriptor exists (mitto-lrt.9 DD8) — requires
//     Configured, an unsupported/disabled backend to be absent, and either
//     Connected or (ReachLocal && Installed).
type AvailabilityState struct {
	Installed  bool
	Configured bool
	Connected  bool
	Available  bool
}

// ConfiguredProvider describes one provider referenced by live
// configuration (e.g. one config.ACPServer entry), decoupled from
// internal/config so this package stays free of that import (mirroring the
// existing ConstraintSpec precedent in types.go).
type ConfiguredProvider struct {
	// Name is the configured provider's display name (e.g. ACPServer.Name).
	Name string
	// StableID optionally links this configured provider back to a known
	// AgentDefinition via AgentDefinition.StableID(). Empty when the
	// configured provider doesn't declare/match any known local definition
	// (e.g. a purely remote provider, or a removed/unknown agent type) —
	// ComposeAvailability treats that case as ReachRemote.
	StableID string
	// Disabled marks an explicitly disabled backend/provider descriptor.
	// Disabled providers can never be Available regardless of connection
	// state (mitto-lrt.9 DD8).
	Disabled bool
	// ProtocolSupported reports whether the backend/protocol this provider
	// speaks is a recognized, supported one (e.g.
	// agentbackend.ValidateProtocol == nil). A merely-descriptive,
	// unsupported backend (e.g. a disabled/experimental AHP adapter) must
	// never surface as Available just because a descriptor exists.
	// Defaults to true so existing ACP-only callers need not set it.
	ProtocolSupported bool
}

// ConnectionState is the live connection state for one configured provider,
// supplied by the caller (composed from an agentbackend.Connection.State()
// query, or from process-liveness checks for the legacy ACP path). Kept as a
// plain struct here — not agentbackend.LifecycleState — so this package
// never needs to import internal/agentbackend.
type ConnectionState struct {
	Connected bool
}

// AgentProviderView is the composed, additive availability view for one
// provider: either a known local AgentDefinition (ReachLocal) or a
// configured-but-unmatched remote descriptor (ReachRemote).
type AgentProviderView struct {
	StableID     string
	DirName      string // empty for ReachRemote (no local definition)
	Name         string // configured display name, when configured
	Reach        ProviderReach
	Availability AvailabilityState
}

// ComposeAvailability computes the four-state AvailabilityState for every
// known local AgentDefinition plus every configured provider that doesn't
// match one, from already-gathered inputs. It is a pure function: it never
// runs a script, opens a connection, or reads a file — callers gather
// `installed` via Manager.GetStatus (ONLY for ReachLocal definitions; never
// for a ConfiguredProvider with no matching StableID, per mitto-lrt.9 DD4)
// and `conns` via the live backend/process layer.
//
// installed and conns are keyed by StableID.
func ComposeAvailability(defs []*AgentDefinition, installed map[string]bool, configured []ConfiguredProvider, conns map[string]ConnectionState) []AgentProviderView {
	matchedConfigured := make(map[string]bool, len(configured))
	configuredByStableID := make(map[string][]ConfiguredProvider, len(configured))
	for _, cp := range configured {
		if cp.StableID != "" {
			configuredByStableID[cp.StableID] = append(configuredByStableID[cp.StableID], cp)
		}
	}

	views := make([]AgentProviderView, 0, len(defs)+len(configured))

	// One view per known local definition, whether or not it is currently
	// configured.
	for _, def := range defs {
		id := def.StableID()
		matches := configuredByStableID[id]

		av := AvailabilityState{Installed: installed[id]}
		if len(matches) == 0 {
			views = append(views, AgentProviderView{StableID: id, DirName: def.DirName, Reach: ReachLocal, Availability: av})
			continue
		}
		for _, cp := range matches {
			matchedConfigured[cp.Name] = true
			views = append(views, AgentProviderView{
				StableID:     id,
				DirName:      def.DirName,
				Name:         cp.Name,
				Reach:        ReachLocal,
				Availability: composeConfigured(av, cp, conns[id]),
			})
		}
	}

	// One view per configured provider that matched no local definition:
	// ReachRemote — never installed (no local script exists to check), and
	// scripts must never be invoked for it (mitto-lrt.9 DD4).
	for _, cp := range configured {
		if matchedConfigured[cp.Name] {
			continue
		}
		views = append(views, AgentProviderView{
			StableID:     cp.StableID,
			Name:         cp.Name,
			Reach:        ReachRemote,
			Availability: composeConfigured(AvailabilityState{}, cp, conns[cp.StableID]),
		})
	}

	return views
}

// composeConfigured folds one ConfiguredProvider + its ConnectionState into
// an already-Installed-populated AvailabilityState, applying the fail-closed
// Available formula (mitto-lrt.9 DD3/DD8).
func composeConfigured(base AvailabilityState, cp ConfiguredProvider, conn ConnectionState) AvailabilityState {
	av := base
	av.Configured = true
	av.Connected = conn.Connected
	usable := !cp.Disabled && cp.ProtocolSupported
	av.Available = usable && (av.Connected || av.Installed)
	return av
}

// CatalogCacheKey scopes a cached catalog (e.g. models/modes list) to the
// specific backend, provider, and capability/version generation it was
// fetched under, so a reconnect or capability refresh can invalidate exactly
// the stale entries without discarding a provider's stable selection
// identity (StableID/AgentRef), which is never stored in the cache itself
// (mitto-lrt.9 DD6).
type CatalogCacheKey struct {
	Backend  string
	Provider string
	Version  string
}

// CatalogCache is a concurrency-safe cache for provider catalogs (e.g.
// available models), scoped by CatalogCacheKey. It holds arbitrary catalog
// payloads (any); it never stores or mutates selection identity.
type CatalogCache struct {
	mu    sync.RWMutex
	items map[CatalogCacheKey]any
}

// NewCatalogCache returns an empty, ready-to-use CatalogCache.
func NewCatalogCache() *CatalogCache {
	return &CatalogCache{items: make(map[CatalogCacheKey]any)}
}

// Get returns the cached catalog for key, if present.
func (c *CatalogCache) Get(key CatalogCacheKey) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.items[key]
	return v, ok
}

// Set stores catalog for key, replacing any prior entry.
func (c *CatalogCache) Set(key CatalogCacheKey, catalog any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = catalog
}

// InvalidateProvider drops every cached entry for (backend, provider),
// regardless of Version — used on reconnect or capability refresh, when the
// prior version generation is no longer trustworthy.
func (c *CatalogCache) InvalidateProvider(backend, provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.items {
		if k.Backend == backend && k.Provider == provider {
			delete(c.items, k)
		}
	}
}
