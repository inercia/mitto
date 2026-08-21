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
 * Global ordered task-label color settings stored in settings.json.
 * @param {import("../core/config.js").ResolvedConfig} config
 */
export function createTaskLabelColorsResource(config: import("../core/config.js").ResolvedConfig): {
    /**
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    getGlobal: (opts?: import("../core/transport.js").RequestOptions) => Promise<TaskLabelColorsBody>;
    /**
     * @param {TaskLabelColorsBody} body
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    setGlobal: (body: TaskLabelColorsBody, opts?: import("../core/transport.js").RequestOptions) => Promise<TaskLabelColorsBody>;
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
