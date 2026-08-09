/**
 * Agent discovery REST resource module (mitto-7gta.9).
 *
 * `createAgentsResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go` /
 * `internal/web/handlers/{agent_discovery,config_metadata}.go`, never built
 * through `core/endpoints.js`.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.agents`.
 */
import { request } from "../core/transport.js";

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the agents resource
 */
export function createAgentsResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/agents/types — the list of available agent definitions
     * (both builtin and user-created).
     * @returns {Promise<{agent_types: string[]}>}
     */
    types: (opts) => call("GET", "/api/agents/types", opts),

    /**
     * POST /api/agents/scan — runs status.sh for every known agent
     * definition and returns their detection results.
     * @returns {Promise<object[]>} AgentScanResult[]
     */
    scan: (opts) => call("POST", "/api/agents/scan", opts),

    /**
     * POST /api/agents/confirm — saves the selected agents as ACP server
     * entries in settings.json. Rejected with 403 when the server's config
     * is read-only (loaded from a --config file).
     * @param {object[]} agentsList - AgentConfirmEntry[]: {name, command,
     *   type?, dir_name?}
     */
    confirm: (agentsList, opts) =>
      call("POST", "/api/agents/confirm", { body: { agents: agentsList }, ...opts }),
  };
}
