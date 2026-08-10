/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createIssuesResource(config: import("../core/config.js").ResolvedConfig): {
    /** GET /api/issues?working_dir=... — list all issues. */
    list: (params: any, opts: any) => Promise<any>;
    /** GET /api/issues/stats?working_dir=... — aggregate counts by state. */
    stats: (params: any, opts: any) => Promise<any>;
    /** GET /api/issues/labels?working_dir=... — every label in use. */
    labelsAll: (params: any, opts: any) => Promise<any>;
    /** GET /api/issues/config?working_dir=... — folder-level beads config. */
    config: (params: any, opts: any) => Promise<any>;
    /** PUT /api/issues/config?working_dir=... @param {object} body - {key, value} */
    setConfig: (params: any, body: object, opts: any) => Promise<any>;
    /** DELETE /api/issues/config?working_dir=...&key=... */
    deleteConfig: (params: any, opts: any) => Promise<any>;
    /** GET /api/issues/upstream?working_dir=... — upstream sync integration config. */
    upstream: (params: any, opts: any) => Promise<any>;
    /** PUT /api/issues/upstream?working_dir=...
     * @param {object} body - {upstream, pull_prompt?, push_prompt?, sync_prompt?,
     *   pull_prompt_args?, push_prompt_args?, sync_prompt_args?} */
    setUpstream: (params: any, body: object, opts: any) => Promise<any>;
    /** GET /api/issues/{id}?working_dir=... — full issue incl. comments/dependencies. */
    show: (id: any, params: any, opts: any) => Promise<any>;
    /** POST /api/issues?working_dir=...
     * @param {object} body - {title, type?, priority?, description?, parent?,
     *   assignee?, notes?, dependencies?} */
    create: (params: any, body: object, opts: any) => Promise<any>;
    /** PATCH /api/issues/{id}?working_dir=...
     * @param {object} patch - {description?, title?, type?, priority?, assignee?, notes?} */
    update: (id: any, params: any, patch: object, opts: any) => Promise<any>;
    /** DELETE /api/issues/{id}?working_dir=... */
    remove: (id: any, params: any, opts: any) => Promise<any>;
    /** POST /api/issues/{id}/status?working_dir=... @param {object} body - {action} */
    status: (id: any, params: any, body: object, opts: any) => Promise<any>;
    /** POST /api/issues/{id}/comments?working_dir=... @param {object} body - {text} */
    comment: (id: any, params: any, body: object, opts: any) => Promise<any>;
    /** POST /api/issues/{id}/dependencies?working_dir=...
     * @param {object} body - {depends_on, type?, action} */
    dependency: (id: any, params: any, body: object, opts: any) => Promise<any>;
    /** POST /api/issues/{id}/labels?working_dir=... @param {object} body - {label, action} */
    label: (id: any, params: any, body: object, opts: any) => Promise<any>;
    /** POST /api/issues/cleanup?working_dir=... — starts an async batch
     * delete of closed issues; returns {started, total, already_running?}. */
    cleanup: (params: any, opts: any) => Promise<any>;
    /** POST /api/issues/sync?working_dir=...
     * @param {object} body - {action: "pull"|"push"|"sync"|"status"} */
    sync: (params: any, body: object, opts: any) => Promise<any>;
    /** POST /api/beads/migrate. `working_dir` is part of the JSON body here
     * (not a query param, unlike every other method on this resource).
     * @param {object} body - {working_dir, mode: "migrate"|"adopt"} */
    migrate: (body: object, opts: any) => Promise<any>;
};
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
export function withIssueCaches(issues: object, hooks?: {
    isGone?: (workingDir: string, id: string) => boolean;
    markGone?: (workingDir: string, id: string) => void;
    onListed?: (workingDir: string, issues: object[]) => void;
    shouldPreload?: (workingDir: string, id: string) => boolean;
}): object;
