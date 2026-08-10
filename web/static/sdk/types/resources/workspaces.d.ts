/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createWorkspacesResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/workspaces.
     * @param {object} [params] - {working_dir?} — when present, scopes the
     *   returned `acp_servers` list to servers configured for that folder.
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     * @returns {Promise<{workspaces: object[], acp_servers: object[]}>}
     */
    list: (params?: object, opts?: object) => Promise<{
        workspaces: object[];
        acp_servers: object[];
    }>;
    /**
     * POST /api/workspaces.
     * @param {object} body - {acp_server, working_dir, name?, color?, code?}
     */
    create: (body: object, opts: any) => Promise<any>;
    /**
     * DELETE /api/workspaces?uuid=... — 409s (-> MittoApiError) when
     * conversations still use the workspace.
     * @param {string} uuid
     */
    remove: (uuid: string, opts: any) => Promise<any>;
    /** GET /api/workspaces/{uuid}/metadata — {description?, url?, group?}. */
    getMetadata: (uuid: any, opts: any) => Promise<any>;
    /**
     * PUT /api/workspaces/{uuid}/metadata.
     * @param {object} body - {description?, url?, group?}
     */
    setMetadata: (uuid: any, body: object, opts: any) => Promise<any>;
    /** GET /api/workspaces/{uuid}/user-data-schema. */
    getUserDataSchema: (uuid: any, opts: any) => Promise<any>;
    /** PUT /api/workspaces/{uuid}/user-data-schema. */
    setUserDataSchema: (uuid: any, body: any, opts: any) => Promise<any>;
    /**
     * GET /api/workspaces/{uuid}/effective-runner-config — the resolved
     * runner config from global + agent levels (no workspace overrides).
     */
    getEffectiveRunnerConfig: (uuid: any, opts: any) => Promise<any>;
    /**
     * GET /api/workspaces/{uuid}/acp-status.
     * @returns {Promise<{alive: boolean}>}
     */
    getAcpStatus: (uuid: any, opts: any) => Promise<{
        alive: boolean;
    }>;
    /** POST /api/workspaces/{uuid}/restart-acp — restarts the shared ACP
     *  process so MCP changes take effect. */
    restartAcp: (uuid: any, opts: any) => Promise<any>;
    /**
     * PUT /api/workspaces/{uuid}/folder-group — sets (or clears with an
     * empty string) the folder-level organizational group label shared by
     * all workspaces in the same working directory.
     * @param {string} uuid
     * @param {string} group
     */
    setFolderGroup: (uuid: string, group: string, opts: any) => Promise<any>;
    /**
     * GET /api/workspaces/{uuid}/mcp-tools?acp_server=... — MCP tools
     * available for the workspace's ACP server type.
     * @param {string} uuid
     * @param {string} acpServer - required by the server (400 without it)
     */
    listMcpTools: (uuid: string, acpServer: string, opts: any) => Promise<any>;
    /**
     * POST /api/workspaces/{uuid}/mcp-tools/install.
     * @param {object} body - {acp_server, scope?, definition: {mcpServers}}
     */
    installMcpTool: (uuid: any, body: object, opts: any) => Promise<any>;
    /**
     * POST /api/workspaces/{uuid}/mcp-tools/remove.
     * @param {object} body - {acp_server, scope?, name}
     */
    removeMcpTool: (uuid: any, body: object, opts: any) => Promise<any>;
};
