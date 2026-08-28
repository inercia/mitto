import { request } from "../core/transport.js";

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
export function createTaskLabelColorsResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    /**
     * GET /api/global/task-label-colors.
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    getGlobal: (opts) => call("GET", "/api/global/task-label-colors", opts),
    /**
     * PUT /api/global/task-label-colors.
     * @param {TaskLabelColorsBody} body
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    setGlobal: (body, opts) => call("PUT", "/api/global/task-label-colors", { body, ...opts }),

    /**
     * GET /api/folders/task-label-colors?working_dir=...
     * @param {object} params - {working_dir} — must be an absolute path
     *   matching a known workspace.
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    getFolder: (params, opts) =>
      call("GET", "/api/folders/task-label-colors", { query: params, ...opts }),
    /**
     * PUT /api/folders/task-label-colors?working_dir=...
     * @param {string} workingDir - absolute path matching a known workspace.
     * @param {TaskLabelColorsBody} body
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    setFolder: (workingDir, body, opts) =>
      call("PUT", "/api/folders/task-label-colors", {
        query: { working_dir: workingDir },
        body,
        ...opts,
      }),
  };
}
