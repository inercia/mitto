/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createAcpServersResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/acp-servers/{name}/prepare-delete — the impact plan: active
     * conversations that block deletion, and per-folder reassign candidates.
     * @param {string} name
     */
    prepareDelete: (name: string, opts: any) => Promise<any>;
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
    reassignAndDelete: (name: string, body: object, opts: any) => Promise<any>;
};
