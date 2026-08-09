/**
 * Beads issues REST resource module (mitto-7gta.11).
 *
 * `createIssuesResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`, following the
 * mitto-7gta.7/.10 precedent (`resources/sessions.js`, `resources/shortcuts.js`):
 * raw relative path templates mirroring `internal/web/routes.go`, never built
 * through `core/endpoints.js` (its builders already apply
 * `baseUrl`+`apiPrefix`; `request()`/`buildUrl()` would double-prefix).
 *
 * `working_dir` is a REQUIRED query param on every `/api/issues` route (every
 * handler 400s without it, and it must be an absolute path matching a known
 * workspace) — always pass it inside `params`. The one exception is
 * `migrate()`, whose `/api/beads/migrate` route reads `working_dir` from the
 * JSON body instead (`beads_migrate.go`'s `migrateRequest`). No client-side
 * validation of absoluteness/known-workspace is performed here — the server
 * is the single source of truth and rejections surface as `MittoApiError`.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.issues`.
 */
import { request } from "../core/transport.js";
import { MittoApiError } from "../core/errors.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the issues resource
 */
export function createIssuesResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  const resource = {
    /** GET /api/issues?working_dir=... — list all issues. */
    list: (params, opts) => call("GET", "/api/issues", { query: params, ...opts }),

    /** GET /api/issues/stats?working_dir=... — aggregate counts by state. */
    stats: (params, opts) => call("GET", "/api/issues/stats", { query: params, ...opts }),

    /** GET /api/issues/labels?working_dir=... — every label in use. */
    labelsAll: (params, opts) => call("GET", "/api/issues/labels", { query: params, ...opts }),

    /** GET /api/issues/config?working_dir=... — folder-level beads config. */
    config: (params, opts) => call("GET", "/api/issues/config", { query: params, ...opts }),
    /** PUT /api/issues/config?working_dir=... @param {object} body - {key, value} */
    setConfig: (params, body, opts) =>
      call("PUT", "/api/issues/config", { query: params, body, ...opts }),
    /** DELETE /api/issues/config?working_dir=...&key=... */
    deleteConfig: (params, opts) => call("DELETE", "/api/issues/config", { query: params, ...opts }),

    /** GET /api/issues/upstream?working_dir=... — upstream sync integration config. */
    upstream: (params, opts) => call("GET", "/api/issues/upstream", { query: params, ...opts }),
    /** PUT /api/issues/upstream?working_dir=...
     * @param {object} body - {upstream, pull_prompt?, push_prompt?, sync_prompt?,
     *   pull_prompt_args?, push_prompt_args?, sync_prompt_args?} */
    setUpstream: (params, body, opts) =>
      call("PUT", "/api/issues/upstream", { query: params, body, ...opts }),

    /** GET /api/issues/{id}?working_dir=... — full issue incl. comments/dependencies. */
    show: (id, params, opts) => call("GET", `/api/issues/${enc(id)}`, { query: params, ...opts }),

    /** POST /api/issues?working_dir=...
     * @param {object} body - {title, type?, priority?, description?, parent?,
     *   assignee?, notes?, dependencies?} */
    create: (params, body, opts) => call("POST", "/api/issues", { query: params, body, ...opts }),

    /** PATCH /api/issues/{id}?working_dir=...
     * @param {object} patch - {description?, title?, type?, priority?, assignee?, notes?} */
    update: (id, params, patch, opts) =>
      call("PATCH", `/api/issues/${enc(id)}`, { query: params, body: patch, ...opts }),

    /** DELETE /api/issues/{id}?working_dir=... */
    remove: (id, params, opts) => call("DELETE", `/api/issues/${enc(id)}`, { query: params, ...opts }),

    /** POST /api/issues/{id}/status?working_dir=... @param {object} body - {action} */
    status: (id, params, body, opts) =>
      call("POST", `/api/issues/${enc(id)}/status`, { query: params, body, ...opts }),

    /** POST /api/issues/{id}/comments?working_dir=... @param {object} body - {text} */
    comment: (id, params, body, opts) =>
      call("POST", `/api/issues/${enc(id)}/comments`, { query: params, body, ...opts }),

    /** POST /api/issues/{id}/dependencies?working_dir=...
     * @param {object} body - {depends_on, type?, action} */
    dependency: (id, params, body, opts) =>
      call("POST", `/api/issues/${enc(id)}/dependencies`, { query: params, body, ...opts }),

    /** POST /api/issues/{id}/labels?working_dir=... @param {object} body - {label, action} */
    label: (id, params, body, opts) =>
      call("POST", `/api/issues/${enc(id)}/labels`, { query: params, body, ...opts }),

    /** POST /api/issues/cleanup?working_dir=... — starts an async batch
     * delete of closed issues; returns {started, total, already_running?}. */
    cleanup: (params, opts) => call("POST", "/api/issues/cleanup", { query: params, ...opts }),

    /** POST /api/issues/sync?working_dir=...
     * @param {object} body - {action: "pull"|"push"|"sync"|"status"} */
    sync: (params, body, opts) => call("POST", "/api/issues/sync", { query: params, body, ...opts }),

    /** POST /api/beads/migrate. `working_dir` is part of the JSON body here
     * (not a query param, unlike every other method on this resource).
     * @param {object} body - {working_dir, mode: "migrate"|"adopt"} */
    migrate: (body, opts) => call("POST", "/api/beads/migrate", { body, ...opts }),
  };

  // Aliases matching the bead description's plural naming, sharing the same
  // implementation as their singular counterparts above (no second code path).
  resource.comments = resource.comment;
  resource.dependencies = resource.dependency;
  resource.labels = resource.label;

  return resource;
}

/**
 * Wraps an issues resource (from `createIssuesResource`) with optional,
 * fully dependency-injected cache side effects mirroring
 * `utils/beadsGoneCache.js`, `utils/beadsKnownIds.js` and
 * `utils/beadsPreload.js` — WITHOUT importing them, so the SDK stays
 * standalone (docs/devel/js-client-library.md §4 environment-agnostic
 * contract). Every hook is optional; omitted hooks make the corresponding
 * behavior a no-op, so `withIssueCaches(issues, {})` is a pure pass-through.
 *
 * @param {object} issues - a resource from `createIssuesResource(config)`
 * @param {object} [hooks]
 * @param {(workingDir: string, id: string) => boolean} [hooks.isGone] -
 *   return true when `id` is known to 404 in `workingDir` (skip the network
 *   call and reject immediately, mirroring `beadsGoneCache.isGone`).
 * @param {(workingDir: string, id: string) => void} [hooks.markGone] -
 *   called when `show()` observes a real 404 (mirrors `beadsGoneCache.markGone`).
 * @param {(workingDir: string, issues: object[]) => void} [hooks.onListed] -
 *   called with the raw array after a successful `list()` (mirrors
 *   `beadsKnownIds.fetchAndCacheBeadsIds`'s cache-population step; event
 *   dispatch is left to the caller/UI layer).
 * @param {(workingDir: string, id: string) => boolean} [hooks.shouldPreload] -
 *   return true when `preload()` should fetch `id` (mirrors
 *   `beadsPreload.js`'s TTL dedup; omitted = always preload).
 * @returns {object} the wrapped issues resource, plus a `preload(ids, params)` method
 */
export function withIssueCaches(issues, hooks = {}) {
  const { isGone, markGone, onListed, shouldPreload } = hooks;

  const wrapped = {
    ...issues,

    show: async (id, params, opts) => {
      const workingDir = params?.working_dir;
      if (isGone && isGone(workingDir, id)) {
        throw new MittoApiError(`Request failed with status 404`, {
          status: 404,
          code: "not_found",
        });
      }
      try {
        return await issues.show(id, params, opts);
      } catch (err) {
        if (markGone && err?.status === 404) markGone(workingDir, id);
        throw err;
      }
    },

    list: async (params, opts) => {
      const result = await issues.list(params, opts);
      if (onListed && Array.isArray(result)) onListed(params?.working_dir, result);
      return result;
    },

    /**
     * Fire-and-forget warmup of `show()` for each non-deduped id. Errors are
     * swallowed — this is a best-effort cache warmer, mirroring
     * `utils/beadsPreload.js`'s `preloadBeadsIssues`.
     * @param {string[]} ids
     * @param {object} params - {working_dir, ...}
     */
    preload: (ids, params) => {
      const workingDir = params?.working_dir;
      if (!Array.isArray(ids)) return;
      for (const id of ids) {
        if (!id) continue;
        if (isGone && isGone(workingDir, id)) continue;
        if (shouldPreload && !shouldPreload(workingDir, id)) continue;
        wrapped.show(id, params).catch(() => {});
      }
    },
  };

  return wrapped;
}
