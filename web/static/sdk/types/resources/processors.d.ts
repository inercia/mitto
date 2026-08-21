/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createProcessorsResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/workspaces/{uuid}/processors — the merged (global +
     * workspace) processor list for a workspace.
     * @param {string} uuid
     */
    list: (uuid: string, opts: any) => Promise<any>;
    /**
     * PATCH /api/workspaces/{uuid}/processors/{name} — toggle a processor's
     * enabled state.
     * @param {string} uuid
     * @param {string} name
     * @param {boolean} enabled
     */
    setEnabled: (uuid: string, name: string, enabled: boolean, opts: any) => Promise<any>;
    /**
     * PUT /api/workspaces/{uuid}/processors/{name}/arguments — persist
     * prompt-mode processor argument overrides. Empty values clear an
     * override; non-empty values set it. Only known parameter keys (as
     * declared by the processor) are accepted server-side.
     * @param {string} uuid
     * @param {string} name
     * @param {Object<string,string>} argumentsMap
     */
    setArguments: (uuid: string, name: string, argumentsMap: {
        [x: string]: string;
    }, opts: any) => Promise<any>;
};
