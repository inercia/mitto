package procstart

import (
	"os"

	mittoAcp "github.com/inercia/mitto/internal/acp"
)

// BuildACPProcessEnv constructs the environment slice for an ACP subprocess.
//
// Layering (later wins):
//
//  1. os.Environ() — inherited from the Mitto process (lowest).
//  2. agentHintEnv — Mitto-authored agent-hint vars (e.g. AGENT_MODE=1) that
//     announce agent presence to shells the ACP subprocess later spawns.
//     Intentionally the lowest Mitto-injected layer so they can be overridden
//     per-server via serverEnv (e.g. settings.json acp_servers[].env with
//     AGENT_MODE="" to disable). See acp.BuildAgentHintEnv.
//  3. serverEnv — server-specific env from settings.json (acp_servers[].env).
//  4. mittoEnv — MITTO_* vars set by Mitto (highest precedence; identity
//     vars must not be spoofed).
//
// Any nil map is treated as empty. Shared between the direct-exec and
// restricted-runner branches so both see the same env.
func BuildACPProcessEnv(serverEnv, mittoEnv, agentHintEnv map[string]string) []string {
	combined := make(map[string]string, len(serverEnv)+len(mittoEnv)+len(agentHintEnv))
	for k, v := range agentHintEnv {
		combined[k] = v
	}
	for k, v := range serverEnv {
		combined[k] = v // serverEnv overrides agent hints
	}
	for k, v := range mittoEnv {
		combined[k] = v // MITTO_* vars keep highest precedence
	}
	return mittoAcp.MergeEnv(os.Environ(), combined)
}
