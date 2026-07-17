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
//  2. serverEnv — server-specific env from settings.json (acp_servers[].env).
//  3. mittoEnv — MITTO_* vars set by Mitto (highest precedence).
//
// This is shared between the direct-exec and restricted-runner branches so that
// the runner branch sees the same env as the non-runner branch.
func BuildACPProcessEnv(serverEnv map[string]string, mittoEnv map[string]string) []string {
	combined := make(map[string]string, len(serverEnv)+len(mittoEnv))
	for k, v := range serverEnv {
		combined[k] = v
	}
	for k, v := range mittoEnv {
		combined[k] = v // MITTO_* vars keep highest precedence
	}
	return mittoAcp.MergeEnv(os.Environ(), combined)
}
