/**
 * @typedef {Object} TaskLabelColorEntry
 * @property {string} label
 * @property {string} color - Six-digit hexadecimal color (`#rrggbb`).
 */
/**
 * @typedef {Object} TaskLabelColorsBody
 * @property {TaskLabelColorEntry[]} entries - Ordered; first matching label wins.
 */
/**
 * Global ordered task-label color settings (settings.json) and folder-level
 * overrides (folders.json, per-workspace, mitto-m5f.2), mirroring
 * resources/shortcuts.js's global/folder split.
 * @param {import("../core/config.js").ResolvedConfig} config
 */
export function createTaskLabelColorsResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * GET /api/global/task-label-colors.
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    getGlobal: (opts?: import("../core/transport.js").RequestOptions) => Promise<TaskLabelColorsBody>;
    /**
     * PUT /api/global/task-label-colors.
     * @param {TaskLabelColorsBody} body
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    setGlobal: (body: TaskLabelColorsBody, opts?: import("../core/transport.js").RequestOptions) => Promise<TaskLabelColorsBody>;
    /**
     * GET /api/folders/task-label-colors?working_dir=...
     * @param {object} params - {working_dir} — must be an absolute path
     *   matching a known workspace.
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    getFolder: (params: object, opts?: import("../core/transport.js").RequestOptions) => Promise<TaskLabelColorsBody>;
    /**
     * PUT /api/folders/task-label-colors?working_dir=...
     * @param {string} workingDir - absolute path matching a known workspace.
     * @param {TaskLabelColorsBody} body
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    setFolder: (workingDir: string, body: TaskLabelColorsBody, opts?: import("../core/transport.js").RequestOptions) => Promise<TaskLabelColorsBody>;
};
export type TaskLabelColorEntry = {
    label: string;
    /**
     * - Six-digit hexadecimal color (`#rrggbb`).
     */
    color: string;
};
export type TaskLabelColorsBody = {
    /**
     * - Ordered; first matching label wins.
     */
    entries: TaskLabelColorEntry[];
};
