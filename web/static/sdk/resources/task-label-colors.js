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
 * Global ordered task-label color settings stored in settings.json.
 * @param {import("../core/config.js").ResolvedConfig} config
 */
export function createTaskLabelColorsResource(config) {
  const call = (method, opts = {}) =>
    request(config, {
      method,
      path: "/api/global/task-label-colors",
      ...opts,
    });

  return {
    /**
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    getGlobal: (opts) => call("GET", opts),
    /**
     * @param {TaskLabelColorsBody} body
     * @param {import("../core/transport.js").RequestOptions} [opts]
     * @returns {Promise<TaskLabelColorsBody>}
     */
    setGlobal: (body, opts) => call("PUT", { body, ...opts }),
  };
}
