/**
 * Shortcut buttons REST resource module (mitto-7gta.10).
 *
 * `createShortcutsResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go` /
 * `internal/web/handlers/{global,folder}_shortcuts.go`, never built through
 * `core/endpoints.js`.
 *
 * Covers both scopes:
 *  - Global shortcuts (stored in settings.json) via /api/global/shortcuts.
 *  - Folder shortcuts (stored in folders.json, per-workspace) via
 *    /api/folders/shortcuts, which require an absolute `working_dir` query
 *    param matching a known workspace.
 * At render time the UI merges global + folder shortcuts; this module only
 * exposes the raw per-scope CRUD, matching the server's own separation.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.shortcuts`.
 */
import { request } from "../core/transport.js";

/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createShortcutsResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/global/shortcuts.
     * @param {object} [params] - {include_prompts?: boolean} — pass
     *   `{ include_prompts: true }` only for the shortcuts editor UI; it
     *   also returns the merged global prompts list (~750 KB, mitto-r4t0).
     *   Read-only renderers must omit it.
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    getGlobal: (params, opts) =>
      call("GET", "/api/global/shortcuts", { query: params, ...opts }),

    /**
     * PUT /api/global/shortcuts.
     * @param {object} body - {sections: Object<string, Array>} — map of
     *   section name -> ShortcutButton[]. The server drops entries with an
     *   empty `prompt` and caps each section's length.
     */
    setGlobal: (body, opts) =>
      call("PUT", "/api/global/shortcuts", { body, ...opts }),

    /**
     * GET /api/folders/shortcuts?working_dir=...
     * @param {object} params - {working_dir} — must be an absolute path
     *   matching a known workspace.
     */
    getFolder: (params, opts) =>
      call("GET", "/api/folders/shortcuts", { query: params, ...opts }),

    /**
     * PUT /api/folders/shortcuts?working_dir=...
     * @param {string} workingDir - absolute path matching a known workspace.
     * @param {object} body - {sections: Object<string, Array>}
     */
    setFolder: (workingDir, body, opts) =>
      call("PUT", "/api/folders/shortcuts", {
        query: { working_dir: workingDir },
        body,
        ...opts,
      }),
  };
}
