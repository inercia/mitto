/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createShortcutsResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/global/shortcuts.
     * @param {object} [params] - {include_prompts?: boolean} — pass
     *   `{ include_prompts: true }` only for the shortcuts editor UI; it
     *   also returns the merged global prompts list (~750 KB, mitto-r4t0).
     *   Read-only renderers must omit it.
     * @param {import("../core/transport.js").RequestOptions} [opts] -
     *   forwarded to request() (e.g. headers, signal)
     */
    getGlobal: (params?: object, opts?: import("../core/transport.js").RequestOptions) => Promise<any>;
    /**
     * PUT /api/global/shortcuts.
     * @param {object} body - {sections: Object<string, Array>} — map of
     *   section name -> ShortcutButton[]. The server drops entries with an
     *   empty `prompt` and caps each section's length.
     */
    setGlobal: (body: object, opts: any) => Promise<any>;
    /**
     * GET /api/folders/shortcuts?working_dir=...
     * @param {object} params - {working_dir} — must be an absolute path
     *   matching a known workspace.
     */
    getFolder: (params: object, opts: any) => Promise<any>;
    /**
     * PUT /api/folders/shortcuts?working_dir=...
     * @param {string} workingDir - absolute path matching a known workspace.
     * @param {object} body - {sections: Object<string, Array>}
     */
    setFolder: (workingDir: string, body: object, opts: any) => Promise<any>;
};
