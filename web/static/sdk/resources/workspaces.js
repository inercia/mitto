/**
 * Workspaces REST resource module (mitto-7gta.9).
 *
 * `createWorkspacesResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go` / `internal/web/handlers/workspace*.go`,
 * never built through `core/endpoints.js`.
 *
 * Scope note (mitto-7gta.9 plan, DECISION 1 and 2): the bead description
 * lists "update" and "restart" among the workspace verbs, but there is no
 * PUT route on `/api/workspaces` (`HandleWorkspaces` only switches on
 * GET/POST/DELETE — workspace edits go through `/api/config`, already
 * covered by `resources/config.js`), so no `update()` method exists here.
 * The ACP restart route IS workspace-scoped
 * (`POST /api/workspaces/{uuid}/restart-acp`), so it lives here as
 * `restartAcp()` rather than on `resources/acp-servers.js`.
 *
 * `remove(uuid)` sends the uuid as a QUERY param (matching
 * `handleRemoveWorkspace`'s `r.URL.Query().Get("uuid")`), not a path
 * segment. The server's legacy `working_dir` fallback is not exposed here.
 *
 * `/api/supported-runners` stays on `resources/config.js` (already
 * covered) and is not duplicated here.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.workspaces`.
 */
import { request } from "../core/transport.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the workspaces resource
 */
export function createWorkspacesResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/workspaces.
     * @param {object} [params] - {working_dir?} — when present, scopes the
     *   returned `acp_servers` list to servers configured for that folder.
     * @returns {Promise<{workspaces: object[], acp_servers: object[]}>}
     */
    list: (params, opts) => call("GET", "/api/workspaces", { query: params, ...opts }),

    /**
     * POST /api/workspaces.
     * @param {object} body - {acp_server, working_dir, name?, color?, code?}
     */
    create: (body, opts) => call("POST", "/api/workspaces", { body, ...opts }),

    /**
     * DELETE /api/workspaces?uuid=... — 409s (-> MittoApiError) when
     * conversations still use the workspace.
     * @param {string} uuid
     */
    remove: (uuid, opts) => call("DELETE", "/api/workspaces", { query: { uuid }, ...opts }),

    /** GET /api/workspaces/{uuid}/metadata — {description?, url?, group?}. */
    getMetadata: (uuid, opts) => call("GET", `/api/workspaces/${enc(uuid)}/metadata`, opts),

    /**
     * PUT /api/workspaces/{uuid}/metadata.
     * @param {object} body - {description?, url?, group?}
     */
    setMetadata: (uuid, body, opts) =>
      call("PUT", `/api/workspaces/${enc(uuid)}/metadata`, { body, ...opts }),

    /** GET /api/workspaces/{uuid}/user-data-schema. */
    getUserDataSchema: (uuid, opts) =>
      call("GET", `/api/workspaces/${enc(uuid)}/user-data-schema`, opts),

    /** PUT /api/workspaces/{uuid}/user-data-schema. */
    setUserDataSchema: (uuid, body, opts) =>
      call("PUT", `/api/workspaces/${enc(uuid)}/user-data-schema`, { body, ...opts }),

    /**
     * GET /api/workspaces/{uuid}/effective-runner-config — the resolved
     * runner config from global + agent levels (no workspace overrides).
     */
    getEffectiveRunnerConfig: (uuid, opts) =>
      call("GET", `/api/workspaces/${enc(uuid)}/effective-runner-config`, opts),

    /**
     * GET /api/workspaces/{uuid}/acp-status.
     * @returns {Promise<{alive: boolean}>}
     */
    getAcpStatus: (uuid, opts) => call("GET", `/api/workspaces/${enc(uuid)}/acp-status`, opts),

    /** POST /api/workspaces/{uuid}/restart-acp — restarts the shared ACP
     *  process so MCP changes take effect. */
    restartAcp: (uuid, opts) => call("POST", `/api/workspaces/${enc(uuid)}/restart-acp`, opts),

    /**
     * PUT /api/workspaces/{uuid}/folder-group — sets (or clears with an
     * empty string) the folder-level organizational group label shared by
     * all workspaces in the same working directory.
     * @param {string} uuid
     * @param {string} group
     */
    setFolderGroup: (uuid, group, opts) =>
      call("PUT", `/api/workspaces/${enc(uuid)}/folder-group`, { body: { group }, ...opts }),

    /**
     * GET /api/workspaces/{uuid}/mcp-tools?acp_server=... — MCP tools
     * available for the workspace's ACP server type.
     * @param {string} uuid
     * @param {string} acpServer - required by the server (400 without it)
     */
    listMcpTools: (uuid, acpServer, opts) =>
      call("GET", `/api/workspaces/${enc(uuid)}/mcp-tools`, {
        query: { acp_server: acpServer },
        ...opts,
      }),

    /**
     * POST /api/workspaces/{uuid}/mcp-tools/install.
     * @param {object} body - {acp_server, scope?, definition: {mcpServers}}
     */
    installMcpTool: (uuid, body, opts) =>
      call("POST", `/api/workspaces/${enc(uuid)}/mcp-tools/install`, { body, ...opts }),

    /**
     * POST /api/workspaces/{uuid}/mcp-tools/remove.
     * @param {object} body - {acp_server, scope?, name}
     */
    removeMcpTool: (uuid, body, opts) =>
      call("POST", `/api/workspaces/${enc(uuid)}/mcp-tools/remove`, { body, ...opts }),
  };
}
