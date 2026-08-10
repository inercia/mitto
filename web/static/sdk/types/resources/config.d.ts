/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createConfigResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/config.
     * @param {object} [params] - {acp_server?, session_id?} — session_id
     *   further filters merged prompts using enabledWhen CEL expressions
     *   evaluated with that session's context.
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    get: (params?: object, opts?: object) => Promise<any>;
    /**
     * POST /api/config — saves the full/partial server configuration.
     * Rejected with 403 when the server's config is read-only (loaded from
     * a --config file). Body fields pass through verbatim; the server is
     * the single source of truth for validation.
     * @param {object} body - see ConfigSaveRequest in config_save.go
     *   (web?, ui?, conversations?, session?, permissions?, mcp?, models?,
     *   server_renames?)
     */
    save: (body: object, opts: any) => Promise<any>;
    /** GET /api/advanced-flags — per-session advanced feature flag registry. */
    advancedFlags: (opts: any) => Promise<any>;
    /** GET /api/external-status — external (Cloudflare Access / password) access status. */
    externalStatus: (opts: any) => Promise<any>;
    /** GET /api/supported-runners — sandbox runner support matrix for this host. */
    supportedRunners: (opts: any) => Promise<any>;
    /** GET /api/runner-defaults — default sandbox runner settings. */
    runnerDefaults: (opts: any) => Promise<any>;
};
