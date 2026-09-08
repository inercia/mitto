package cmd

import (
	"fmt"

	"github.com/inercia/mitto/internal/backendcompat"
	"github.com/inercia/mitto/internal/config"
)

// resolveAgentSelector resolves a user-provided "agent"/"backend" selector
// string against the configured ACP servers (mitto-lrt.13). It reuses
// backendcompat.ResolveProviderAlias's semantics: an exact match on a
// configured server name wins outright; otherwise a case-insensitive match
// is attempted, and an ambiguous canonical set (two server names folding to
// the same lowercase form) is rejected via agentbackend.ErrAmbiguousAlias
// rather than silently picked. This is a thin, additive convenience layer
// over cfg.GetServer — it does not change how --acp/acp_server resolve.
func resolveAgentSelector(cfg *config.Config, selector string) (*config.ACPServer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	if selector == "" {
		return nil, fmt.Errorf("agent selector must not be empty")
	}
	resolved, err := backendcompat.ResolveProviderAlias(selector, cfg.ServerNames())
	if err != nil {
		return nil, fmt.Errorf("resolving agent %q: %w", selector, err)
	}
	return cfg.GetServer(resolved)
}

// resolveServerSelector picks the effective ACP server given both the
// legacy --acp value and the new --agent alias (mitto-lrt.13). Precedence:
// if neither is set, returns (nil, nil) so the caller falls back to its own
// default; if only one is set, it wins; if both are set, they must resolve
// to the same configured server, otherwise this returns an actionable
// conflict error rather than silently preferring either side.
func resolveServerSelector(cfg *config.Config, acpValue, agentValue string) (*config.ACPServer, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration not loaded")
	}
	switch {
	case acpValue == "" && agentValue == "":
		return nil, nil
	case agentValue == "":
		return cfg.GetServer(acpValue)
	case acpValue == "":
		return resolveAgentSelector(cfg, agentValue)
	default:
		acpSrv, err := cfg.GetServer(acpValue)
		if err != nil {
			return nil, err
		}
		agentSrv, err := resolveAgentSelector(cfg, agentValue)
		if err != nil {
			return nil, err
		}
		if acpSrv.Name != agentSrv.Name {
			return nil, fmt.Errorf(
				"--acp %q and --agent %q resolve to different servers; specify only one",
				acpValue, agentValue)
		}
		return acpSrv, nil
	}
}
