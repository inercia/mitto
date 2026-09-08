package mcpserver

import (
	"fmt"

	"github.com/inercia/mitto/internal/backendcompat"
	"github.com/inercia/mitto/internal/config"
)

// resolveAgentOrACPServerName resolves the effective ACP server name given
// both the legacy 'acp_server' argument and the optional 'agent' alias
// (mitto-lrt.13), mirroring the CLI's --acp/--agent precedence
// (internal/cmd/agent_selection.go): if only one is set, it wins; if both
// are set, they must resolve to the same configured server, otherwise this
// returns an actionable conflict error. 'agent' additionally accepts a
// case-insensitive alias of a configured server name
// (backendcompat.ResolveProviderAlias semantics); 'acp_server' remains an
// exact match, as before. Callers must ensure at least one of acpServer,
// agent is non-empty before calling.
func resolveAgentOrACPServerName(cfg *config.Config, acpServer, agent string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("server configuration not available")
	}
	switch {
	case acpServer == "" && agent == "":
		return "", fmt.Errorf("resolveAgentOrACPServerName: both acp_server and agent are empty")
	case agent == "":
		return acpServer, nil
	case acpServer == "":
		resolved, err := backendcompat.ResolveProviderAlias(agent, cfg.ServerNames())
		if err != nil {
			return "", fmt.Errorf("resolving agent %q: %w", agent, err)
		}
		return resolved, nil
	default:
		resolvedAgent, err := backendcompat.ResolveProviderAlias(agent, cfg.ServerNames())
		if err != nil {
			return "", fmt.Errorf("resolving agent %q: %w", agent, err)
		}
		if resolvedAgent != acpServer {
			return "", fmt.Errorf(
				"'acp_server' (%q) and 'agent' (%q) resolve to different servers; specify only one",
				acpServer, agent)
		}
		return acpServer, nil
	}
}
