/**
 * Workspace prompts REST resource module (mitto-7gta.10).
 *
 * `createPromptsResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7 precedent (`resources/sessions.js`): raw relative paths
 * mirroring `internal/web/routes.go`, never built through
 * `core/endpoints.js` (whose builders are already baseUrl+apiPrefix-prefixed
 * — `request()` would double-prefix them).
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.prompts`.
 *
 * Caching (utils/promptsCache.js's TTL + in-flight + If-Modified-Since
 * behavior) is NOT baked in here — it is available as an optional,
 * injectable decorator via `cache/ttl-cache.js` (see that module and
 * `sdk/index.js`'s `createTtlCache` export).
 */
import { request } from "../core/transport.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the prompts resource
 */
export function createPromptsResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/workspace-prompts.
     * @param {object} [params] - working_dir, session_id, enabled_context,
     *   item_kind, item_id, item_status, item_type, item_priority,
     *   item_labels, include_global.
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    list: (params, opts) => call("GET", "/api/workspace-prompts", { query: params, ...opts }),

    /**
     * POST /api/workspace-prompts — create or update a workspace prompt.
     * @param {object} body - {name, prompt, description?, group?,
     *   background_color?, enabled?, working_dir}
     */
    create: (body, opts) => call("POST", "/api/workspace-prompts", { body, ...opts }),

    /**
     * DELETE /api/workspace-prompts?name=...&working_dir=... — the server
     * expects identifying fields as query params, not a path segment or body
     * (mirrors HandleWorkspacePromptsDELETE).
     * @param {object} params - {name, working_dir}
     */
    remove: (params, opts) => call("DELETE", "/api/workspace-prompts", { query: params, ...opts }),

    /**
     * PATCH /api/workspace-prompts/{name}?working_dir=... — toggle a
     * prompt's enabled state. `working_dir` is a query param (not a body
     * field); the body carries only `enabled`.
     * @param {string} name
     * @param {string} workingDir
     * @param {boolean} enabled
     */
    setEnabled: (name, workingDir, enabled, opts) =>
      call("PATCH", `/api/workspace-prompts/${enc(name)}`, {
        query: { working_dir: workingDir },
        body: { enabled },
        ...opts,
      }),

    /**
     * GET /api/workspace-prompts/remembered-args — per-argument "remember
     * last value" for prompt dialogs (mitto-x8v, mitto-47y.6.2). sessionId is
     * optional; when provided the server merges conversation-scoped values
     * on top of folder-scoped values.
     * @param {object} params - {working_dir, prompt, session_id?}
     */
    rememberedArgs: (params, opts) =>
      call("GET", "/api/workspace-prompts/remembered-args", { query: params, ...opts }),
  };
}
