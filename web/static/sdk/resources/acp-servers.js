/**
 * ACP server lifecycle REST resource module (mitto-7gta.9).
 *
 * `createAcpServersResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go` /
 * `internal/web/handlers/acp_server_delete.go`, never built through
 * `core/endpoints.js`.
 *
 * Scope note (mitto-7gta.9 plan, DECISION 2): the bead description lists
 * "restart" among the ACP server verbs, but the only restart route is
 * workspace-scoped (`POST /api/workspaces/{uuid}/restart-acp`) — it lives on
 * `resources/workspaces.js` as `restartAcp(uuid)` instead.
 *
 * Only two routes exist for this resource: the guided two-step deletion flow
 * (bead mitto-pgt) — `prepareDelete` returns the impact plan (active
 * conversations that block deletion, per-folder plans with reassign
 * candidates), `reassignAndDelete` executes the user's per-folder choices
 * and removes the server from settings.json.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.acpServers`.
 */
import { request } from "../core/transport.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the acpServers resource
 */
export function createAcpServersResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/acp-servers/{name}/prepare-delete — the impact plan: active
     * conversations that block deletion, and per-folder reassign candidates.
     * @param {string} name
     */
    prepareDelete: (name, opts) =>
      call("GET", `/api/acp-servers/${enc(name)}/prepare-delete`, opts),

    /**
     * POST /api/acp-servers/{name}/reassign-and-delete — applies the user's
     * per-folder choices, then removes the server from settings.json.
     * Blocked (-> MittoApiError) if any active conversation still uses the
     * server (mirrors `prepareDelete`'s active-conversation guard).
     * @param {string} name
     * @param {object} body - {folders: Object<string,string>} — maps
     *   working_dir -> replacement ACP server name, or "" / "delete" to
     *   delete conversations and the workspace config for that folder. Any
     *   folder returned by `prepareDelete` must be present here (missing
     *   entries are treated as "delete").
     */
    reassignAndDelete: (name, body, opts) =>
      call("POST", `/api/acp-servers/${enc(name)}/reassign-and-delete`, { body, ...opts }),
  };
}
