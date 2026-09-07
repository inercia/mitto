package backendcompat

import (
	"fmt"
	"strings"

	"github.com/inercia/mitto/internal/agentbackend"
)

// ResolveProviderAlias resolves a possibly-stale ACP server name (as found
// in persisted session metadata) against the current set of canonical
// server names from live configuration, using the same precedence as
// internal/session's migration_001_normalize_acp.go: exact match first,
// then case-insensitive match. If no canonical name matches at all, alias
// is returned unchanged (the provider may have been deleted; callers decide
// how to handle that) rather than erroring.
//
// Unlike the migration (which silently picks the first case-insensitive
// match it finds), this rejects an ambiguous canonical set outright: if two
// or more canonical names fold to the same lowercase form, resolution
// cannot be unambiguous and the caller must fix the underlying duplicate
// names rather than have one silently picked.
func ResolveProviderAlias(alias string, canonical []string) (string, error) {
	if alias == "" {
		return "", fmt.Errorf("backendcompat: alias must not be empty")
	}
	for _, name := range canonical {
		if name == alias {
			return name, nil
		}
	}

	lowerToCanonical := make(map[string]string, len(canonical))
	for _, name := range canonical {
		lower := strings.ToLower(name)
		if existing, ok := lowerToCanonical[lower]; ok && existing != name {
			return "", fmt.Errorf("%w: canonical names %q and %q both fold to %q",
				agentbackend.ErrAmbiguousAlias, existing, name, lower)
		}
		lowerToCanonical[lower] = name
	}

	if match, ok := lowerToCanonical[strings.ToLower(alias)]; ok {
		return match, nil
	}
	// No canonical name matches (e.g. the provider was renamed away from
	// or deleted); leave alias unchanged rather than guessing.
	return alias, nil
}

// ReconcileProviderID reconciles a legacy ACP server name with an optional
// neutral ProviderID describing the same entity, applying documented
// precedence: if only one side is set, it wins; if both are set and agree,
// it is returned; if both are set and disagree, this returns
// agentbackend.ErrConflictingIdentity rather than silently preferring
// either side — the caller must surface the conflict rather than guess
// which one is authoritative.
func ReconcileProviderID(legacyName string, neutral agentbackend.ProviderID) (agentbackend.ProviderID, error) {
	switch {
	case legacyName == "" && neutral == "":
		return "", nil
	case legacyName == "":
		return neutral, nil
	case neutral == "":
		return agentbackend.ProviderID(legacyName), nil
	case agentbackend.ProviderID(legacyName) == neutral:
		return neutral, nil
	default:
		return "", fmt.Errorf("%w: legacy name %q vs neutral provider %q",
			agentbackend.ErrConflictingIdentity, legacyName, neutral)
	}
}
