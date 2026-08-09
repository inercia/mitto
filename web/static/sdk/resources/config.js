/**
 * Global server configuration REST resource module (mitto-7gta.10).
 *
 * `createConfigResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go`, never built through
 * `core/endpoints.js`.
 *
 * Scope note (mitto-7gta.10 plan, DECISION 4): the bead description lists
 * "client.config / client.settings / client.global", but there is no
 * `/api/settings` route — global settings ARE `/api/config` (GET/POST,
 * config_get.go / config_save.go), and the only `/api/global/*` route is
 * shortcuts, which belongs to `client.shortcuts` (resources/shortcuts.js).
 * No separate `settings`/`global` namespaces are created here; this module
 * also carries the read-only discovery endpoints grouped alongside
 * `/api/config` in routes.go.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.config`.
 *
 * Caching (utils/configCache.js's TTL + in-flight + ETag behavior) is NOT
 * baked in here — it is available as an optional, injectable decorator via
 * `cache/ttl-cache.js` (see that module and `sdk/index.js`'s
 * `createTtlCache` export).
 */
import { request } from "../core/transport.js";

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the config resource
 */
export function createConfigResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/config.
     * @param {object} [params] - {acp_server?, session_id?} — session_id
     *   further filters merged prompts using enabledWhen CEL expressions
     *   evaluated with that session's context.
     */
    get: (params, opts) => call("GET", "/api/config", { query: params, ...opts }),

    /**
     * POST /api/config — saves the full/partial server configuration.
     * Rejected with 403 when the server's config is read-only (loaded from
     * a --config file). Body fields pass through verbatim; the server is
     * the single source of truth for validation.
     * @param {object} body - see ConfigSaveRequest in config_save.go
     *   (web?, ui?, conversations?, session?, permissions?, mcp?, models?,
     *   server_renames?)
     */
    save: (body, opts) => call("POST", "/api/config", { body, ...opts }),

    /** GET /api/advanced-flags — per-session advanced feature flag registry. */
    advancedFlags: (opts) => call("GET", "/api/advanced-flags", opts),

    /** GET /api/external-status — external (Cloudflare Access / password) access status. */
    externalStatus: (opts) => call("GET", "/api/external-status", opts),

    /** GET /api/supported-runners — sandbox runner support matrix for this host. */
    supportedRunners: (opts) => call("GET", "/api/supported-runners", opts),

    /** GET /api/runner-defaults — default sandbox runner settings. */
    runnerDefaults: (opts) => call("GET", "/api/runner-defaults", opts),
  };
}
