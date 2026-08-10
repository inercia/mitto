/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createPromptsResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/workspace-prompts.
     * @param {object} [params] - working_dir, session_id, enabled_context,
     *   item_kind, item_id, item_status, item_type, item_priority,
     *   item_labels, include_global.
     * @param {object} [opts] - forwarded to request() (e.g. headers, signal)
     */
    list: (params?: object, opts?: object) => Promise<any>;
    /**
     * POST /api/workspace-prompts — create or update a workspace prompt.
     * @param {object} body - {name, prompt, description?, group?,
     *   background_color?, enabled?, working_dir}
     */
    create: (body: object, opts: any) => Promise<any>;
    /**
     * DELETE /api/workspace-prompts?name=...&working_dir=... — the server
     * expects identifying fields as query params, not a path segment or body
     * (mirrors HandleWorkspacePromptsDELETE).
     * @param {object} params - {name, working_dir}
     */
    remove: (params: object, opts: any) => Promise<any>;
    /**
     * PATCH /api/workspace-prompts/{name}?working_dir=... — toggle a
     * prompt's enabled state. `working_dir` is a query param (not a body
     * field); the body carries only `enabled`.
     * @param {string} name
     * @param {string} workingDir
     * @param {boolean} enabled
     */
    setEnabled: (name: string, workingDir: string, enabled: boolean, opts: any) => Promise<any>;
    /**
     * GET /api/workspace-prompts/remembered-args — per-argument "remember
     * last value" for prompt dialogs (mitto-x8v, mitto-47y.6.2). sessionId is
     * optional; when provided the server merges conversation-scoped values
     * on top of folder-scoped values.
     * @param {object} params - {working_dir, prompt, session_id?}
     */
    rememberedArgs: (params: object, opts: any) => Promise<any>;
};
