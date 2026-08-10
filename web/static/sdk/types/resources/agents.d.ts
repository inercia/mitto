/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createAgentsResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/agents/types — the list of available agent definitions
     * (both builtin and user-created).
     * @returns {Promise<{agent_types: string[]}>}
     */
    types: (opts: any) => Promise<{
        agent_types: string[];
    }>;
    /**
     * POST /api/agents/scan — runs status.sh for every known agent
     * definition and returns their detection results.
     * @returns {Promise<object[]>} AgentScanResult[]
     */
    scan: (opts: any) => Promise<object[]>;
    /**
     * POST /api/agents/confirm — saves the selected agents as ACP server
     * entries in settings.json. Rejected with 403 when the server's config
     * is read-only (loaded from a --config file).
     * @param {object[]} agentsList - AgentConfirmEntry[]: {name, command,
     *   type?, dir_name?}
     */
    confirm: (agentsList: object[], opts: any) => Promise<any>;
};
