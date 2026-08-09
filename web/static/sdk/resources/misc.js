/**
 * Misc REST resource module (mitto-7gta.12).
 *
 * `createMiscResource(config, configResource)` curries the SDK's `request()`
 * primitive (`core/transport.js`) with the resolved client `config`,
 * following the mitto-7gta.7/.10 precedent: raw relative paths mirroring
 * `internal/web/routes.go`, never built through `core/endpoints.js`.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.misc`.
 *
 * The discovery endpoints listed in this bead's description (advancedFlags,
 * externalStatus, supportedRunners, runnerDefaults) already exist on
 * `resources/config.js` (`client.serverConfig`, mitto-7gta.10) — they are
 * NOT reimplemented here. `createMiscResource` takes the already-constructed
 * config resource instance and delegates to its methods directly, so
 * `client.misc.advancedFlags === client.serverConfig.advancedFlags` (same
 * function object, no duplicate implementation).
 */
import { request } from "../core/transport.js";

/**
 * @param {object} config - resolved config (see core/config.js)
 * @param {object} configResource - the resource created by
 *   `createConfigResource(config)` (see resources/config.js)
 * @returns {object} the misc resource
 */
export function createMiscResource(config, configResource) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    uiPreferences: {
      get: (opts) => call("GET", "/api/ui-preferences", opts),
      /** @param {object} prefs - see UIPreferences in ui_preferences.go */
      save: (prefs, opts) => call("PUT", "/api/ui-preferences", { body: prefs, ...opts }),
    },

    /** GET /api/csrf-token. */
    csrfToken: (opts) => call("GET", "/api/csrf-token", opts),

    /** GET /api/check-file-exists?path= — localhost-only server-side; a
     *  403 from the external listener surfaces as `MittoApiError`.
     *  @param {string} path - absolute file path
     *  @returns {Promise<{exists: boolean}>} */
    checkFileExists: (path, opts) =>
      call("GET", "/api/check-file-exists", { query: { path }, ...opts }),

    /** POST /api/save-file-to-path — localhost-only server-side.
     *  @param {string} path - absolute file path
     *  @param {string} content */
    saveFileToPath: (path, content, opts) =>
      call("POST", "/api/save-file-to-path", { body: { path, content }, ...opts }),

    /** POST /api/aux/improve-prompt. May answer 503 while the auxiliary
     *  session warms up — surfaced as `MittoApiError`, no client-side
     *  retry.
     *  @param {string} prompt
     *  @param {string} workspaceUUID
     *  @returns {Promise<{improved_prompt: string}>} */
    improvePrompt: (prompt, workspaceUUID, opts) =>
      call("POST", "/api/aux/improve-prompt", {
        body: { prompt, workspace_uuid: workspaceUUID },
        ...opts,
      }),

    // Delegated discovery endpoints (mitto-7gta.10, resources/config.js) —
    // same function objects, not reimplementations.
    advancedFlags: configResource.advancedFlags,
    externalStatus: configResource.externalStatus,
    supportedRunners: configResource.supportedRunners,
    runnerDefaults: configResource.runnerDefaults,
  };
}
