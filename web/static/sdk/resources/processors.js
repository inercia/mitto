/**
 * Workspace processors REST resource module (mitto-7gta.10).
 *
 * `createProcessorsResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go` / `internal/web/handlers/workspace_processors.go`,
 * never built through `core/endpoints.js`.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.processors`.
 */
import { request } from "../core/transport.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the processors resource
 */
export function createProcessorsResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/workspaces/{uuid}/processors — the merged (global +
     * workspace) processor list for a workspace.
     * @param {string} uuid
     */
    list: (uuid, opts) =>
      call("GET", `/api/workspaces/${enc(uuid)}/processors`, opts),

    /**
     * PATCH /api/workspaces/{uuid}/processors/{name} — toggle a processor's
     * enabled state.
     * @param {string} uuid
     * @param {string} name
     * @param {boolean} enabled
     */
    setEnabled: (uuid, name, enabled, opts) =>
      call("PATCH", `/api/workspaces/${enc(uuid)}/processors/${enc(name)}`, {
        body: { enabled },
        ...opts,
      }),

    /**
     * PUT /api/workspaces/{uuid}/processors/{name}/arguments — persist
     * prompt-mode processor argument overrides. Empty values clear an
     * override; non-empty values set it. Only known parameter keys (as
     * declared by the processor) are accepted server-side.
     * @param {string} uuid
     * @param {string} name
     * @param {Object<string,string>} argumentsMap
     */
    setArguments: (uuid, name, argumentsMap, opts) =>
      call(
        "PUT",
        `/api/workspaces/${enc(uuid)}/processors/${enc(name)}/arguments`,
        { body: { arguments: argumentsMap }, ...opts },
      ),
  };
}
