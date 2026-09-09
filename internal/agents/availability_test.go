package agents

import "testing"

func localDef(dirName, acpID string) *AgentDefinition {
	return &AgentDefinition{DirName: dirName, Metadata: AgentMetadata{ACPId: acpID}}
}

// TestComposeAvailability_UnconfiguredLocalDefinition covers a known local
// definition that is not referenced by any live config entry: it must
// surface, but never as Available, even when installed (mitto-lrt.9 DD8:
// Available requires Configured).
func TestComposeAvailability_UnconfiguredLocalDefinition(t *testing.T) {
	defs := []*AgentDefinition{localDef("auggie", "auggie")}
	installed := map[string]bool{"auggie": true}

	views := ComposeAvailability(defs, installed, nil, nil)
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	v := views[0]
	if v.Reach != ReachLocal || v.StableID != "auggie" || v.DirName != "auggie" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if !v.Availability.Installed {
		t.Fatalf("Installed = false, want true")
	}
	if v.Availability.Configured || v.Availability.Connected || v.Availability.Available {
		t.Fatalf("unconfigured definition must not be Configured/Connected/Available: %+v", v.Availability)
	}
}

// TestComposeAvailability_ConfiguredAndInstalled_NoConnection covers the
// common "configured, installed, not yet connected" case: Available must be
// true via the Installed leg of the fail-closed formula.
func TestComposeAvailability_ConfiguredAndInstalled_NoConnection(t *testing.T) {
	defs := []*AgentDefinition{localDef("auggie", "auggie")}
	installed := map[string]bool{"auggie": true}
	configured := []ConfiguredProvider{{Name: "Auggie", StableID: "auggie", ProtocolSupported: true}}

	views := ComposeAvailability(defs, installed, configured, nil)
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	av := views[0].Availability
	if !av.Configured || !av.Installed || av.Connected {
		t.Fatalf("unexpected base state: %+v", av)
	}
	if !av.Available {
		t.Fatalf("Available = false, want true (Installed leg should satisfy fail-closed formula)")
	}
}

// TestComposeAvailability_ConfiguredAndConnected_NotInstalled covers the
// "connected but no local install detected" case (e.g. a remote-attach
// scenario surfaced through a local definition): Available must be true via
// the Connected leg.
func TestComposeAvailability_ConfiguredAndConnected_NotInstalled(t *testing.T) {
	defs := []*AgentDefinition{localDef("auggie", "auggie")}
	configured := []ConfiguredProvider{{Name: "Auggie", StableID: "auggie", ProtocolSupported: true}}
	conns := map[string]ConnectionState{"auggie": {Connected: true}}

	views := ComposeAvailability(defs, nil, configured, conns)
	av := views[0].Availability
	if av.Installed {
		t.Fatalf("Installed = true, want false (not in installed map)")
	}
	if !av.Connected || !av.Available {
		t.Fatalf("Connected/Available = %v/%v, want true/true", av.Connected, av.Available)
	}
}

// TestComposeAvailability_DisabledNeverAvailable pins DD8: a Disabled
// provider must never be Available even when installed and connected.
func TestComposeAvailability_DisabledNeverAvailable(t *testing.T) {
	defs := []*AgentDefinition{localDef("auggie", "auggie")}
	installed := map[string]bool{"auggie": true}
	configured := []ConfiguredProvider{{Name: "Auggie", StableID: "auggie", ProtocolSupported: true, Disabled: true}}
	conns := map[string]ConnectionState{"auggie": {Connected: true}}

	views := ComposeAvailability(defs, installed, configured, conns)
	if views[0].Availability.Available {
		t.Fatalf("Available = true for a Disabled provider, want false")
	}
}

// TestComposeAvailability_UnsupportedProtocolNeverAvailable pins DD8/DD3: an
// unsupported protocol/backend must never be Available merely because a
// descriptor exists, even when installed and connected.
func TestComposeAvailability_UnsupportedProtocolNeverAvailable(t *testing.T) {
	defs := []*AgentDefinition{localDef("auggie", "auggie")}
	installed := map[string]bool{"auggie": true}
	configured := []ConfiguredProvider{{Name: "Auggie", StableID: "auggie", ProtocolSupported: false}}
	conns := map[string]ConnectionState{"auggie": {Connected: true}}

	views := ComposeAvailability(defs, installed, configured, conns)
	if views[0].Availability.Available {
		t.Fatalf("Available = true for an unsupported protocol, want false")
	}
}

// TestComposeAvailability_RemoteUnmatchedConfigured covers a configured
// provider with no matching local StableID: it must surface as ReachRemote,
// with no DirName and Installed always false (no local script exists to
// check, and DD4 forbids probing one).
func TestComposeAvailability_RemoteUnmatchedConfigured(t *testing.T) {
	configured := []ConfiguredProvider{{Name: "RemoteThing", StableID: "", ProtocolSupported: true}}
	conns := map[string]ConnectionState{"": {Connected: true}}

	views := ComposeAvailability(nil, nil, configured, conns)
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	v := views[0]
	if v.Reach != ReachRemote || v.DirName != "" || v.Name != "RemoteThing" {
		t.Fatalf("unexpected remote view: %+v", v)
	}
	if v.Availability.Installed {
		t.Fatalf("Installed = true for a remote provider, want false (never probed, DD4)")
	}
	if !v.Availability.Available {
		t.Fatalf("Available = false, want true (Connected leg)")
	}
}

// TestComposeAvailability_MultipleConfiguredEntriesForOneDefinition covers
// two configured providers (e.g. two ACP server entries) pointing at the
// same local definition: one view per configured entry, all sharing
// StableID/DirName but with distinct Name/Availability.
func TestComposeAvailability_MultipleConfiguredEntriesForOneDefinition(t *testing.T) {
	defs := []*AgentDefinition{localDef("auggie", "auggie")}
	installed := map[string]bool{"auggie": true}
	configured := []ConfiguredProvider{
		{Name: "Auggie (Sonnet)", StableID: "auggie", ProtocolSupported: true},
		{Name: "Auggie (Opus)", StableID: "auggie", ProtocolSupported: true},
	}
	conns := map[string]ConnectionState{"auggie": {Connected: true}}

	views := ComposeAvailability(defs, installed, configured, conns)
	if len(views) != 2 {
		t.Fatalf("len(views) = %d, want 2", len(views))
	}
	names := map[string]bool{}
	for _, v := range views {
		if v.StableID != "auggie" || v.DirName != "auggie" || v.Reach != ReachLocal {
			t.Fatalf("unexpected shared-identity view: %+v", v)
		}
		if !v.Availability.Available {
			t.Fatalf("expected Available=true for %+v", v)
		}
		names[v.Name] = true
	}
	if !names["Auggie (Sonnet)"] || !names["Auggie (Opus)"] {
		t.Fatalf("missing expected configured names, got: %+v", names)
	}
}

// TestCatalogCache_GetSetInvalidateProvider covers the scoped cache
// lifecycle: Set/Get round-trip, and InvalidateProvider dropping only
// entries for the matching (Backend, Provider) pair regardless of Version,
// leaving other providers untouched (mitto-lrt.9 DD6).
func TestCatalogCache_GetSetInvalidateProvider(t *testing.T) {
	c := NewCatalogCache()

	keyV1 := CatalogCacheKey{Backend: "acp", Provider: "auggie", Version: "v1"}
	keyV2 := CatalogCacheKey{Backend: "acp", Provider: "auggie", Version: "v2"}
	otherProvider := CatalogCacheKey{Backend: "acp", Provider: "claude-code", Version: "v1"}

	c.Set(keyV1, []string{"model-a"})
	c.Set(keyV2, []string{"model-b"})
	c.Set(otherProvider, []string{"model-c"})

	if v, ok := c.Get(keyV1); !ok || v.([]string)[0] != "model-a" {
		t.Fatalf("Get(keyV1) = %v, %v; want model-a, true", v, ok)
	}

	c.InvalidateProvider("acp", "auggie")

	if _, ok := c.Get(keyV1); ok {
		t.Fatalf("Get(keyV1) still present after InvalidateProvider")
	}
	if _, ok := c.Get(keyV2); ok {
		t.Fatalf("Get(keyV2) still present after InvalidateProvider")
	}
	if v, ok := c.Get(otherProvider); !ok || v.([]string)[0] != "model-c" {
		t.Fatalf("Get(otherProvider) = %v, %v; want model-c, true (must survive unrelated invalidation)", v, ok)
	}
}

// TestCatalogCache_GetMissing confirms a lookup miss reports ok=false rather
// than a zero-value hit.
func TestCatalogCache_GetMissing(t *testing.T) {
	c := NewCatalogCache()
	if _, ok := c.Get(CatalogCacheKey{Backend: "acp", Provider: "unknown"}); ok {
		t.Fatalf("Get() on empty cache returned ok=true, want false")
	}
}
