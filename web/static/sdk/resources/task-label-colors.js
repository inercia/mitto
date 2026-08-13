import { request } from "../core/transport.js";

/** Global ordered task-label color settings stored in settings.json. */
export function createTaskLabelColorsResource(config) {
  const call = (method, opts = {}) =>
    request(config, {
      method,
      path: "/api/global/task-label-colors",
      ...opts,
    });

  return {
    getGlobal: (opts) => call("GET", opts),
    setGlobal: (body, opts) => call("PUT", { body, ...opts }),
  };
}
