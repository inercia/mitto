/**
 * Sessions REST resource module (mitto-7gta.7).
 *
 * `createSessionsResource(config)` curries the SDK's `request()` primitive
 * (`core/transport.js`) with the resolved client `config`. Deliberately does
 * NOT build URLs through `core/endpoints.js`'s registry: those builders
 * return already baseUrl+apiPrefix-prefixed URLs, and `request()`/`buildUrl()`
 * would prefix a relative path a second time, double-applying `apiPrefix`.
 * Instead this module owns its own raw, relative path templates (mirroring
 * `internal/web/routes.go`) and lets `request()` do the one-and-only prefixing.
 * `enc` matches `core/endpoints.js`'s own `encodeURIComponent` alias so path
 * segments are escaped identically across both modules.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.sessions`.
 *
 * Scope (per the mitto-7gta.7 plan comment): queue/loop sub-resources are
 * mitto-7gta.8; files/dashboard/misc are mitto-7gta.12. Images are in scope
 * here per this bead's description.
 */
import { request } from "../core/transport.js";

const enc = encodeURIComponent;

/**
 * @param {object} config - resolved config (see core/config.js)
 * @returns {object} the sessions resource
 */
export function createSessionsResource(config) {
  const call = (method, path, opts = {}) => request(config, { method, path, ...opts });

  return {
    list: (opts) => call("GET", "/api/sessions", opts),
    running: (opts) => call("GET", "/api/sessions/running", opts),
    get: (id, opts) => call("GET", `/api/sessions/${enc(id)}`, opts),
    /** @param {object} [body] - {name?, working_dir?, acp_server?, beads_issue?,
     *   origin_prompt_name?, initial_prompt_name?, arguments?} */
    create: (body, opts) => call("POST", "/api/sessions", { body, ...opts }),
    /** @param {object} patch - {name?, description?, pinned?, archived?,
     *   beads_issue?, background_color?} */
    update: (id, patch, opts) =>
      call("PATCH", `/api/sessions/${enc(id)}`, { body: patch, ...opts }),
    remove: (id, opts) => call("DELETE", `/api/sessions/${enc(id)}`, opts),
    /** @param {object} [params] - {limit?, before?, order?: "asc"|"desc"} */
    events: (id, params, opts) =>
      call("GET", `/api/sessions/${enc(id)}/events`, { query: params, ...opts }),
    changes: (id, opts) => call("GET", `/api/sessions/${enc(id)}/changes`, opts),

    getSettings: (id, opts) => call("GET", `/api/sessions/${enc(id)}/settings`, opts),
    /** @param {object} settings - map of setting name -> bool, merged server-side */
    updateSettings: (id, settings, opts) =>
      call("PATCH", `/api/sessions/${enc(id)}/settings`, {
        body: { settings },
        ...opts,
      }),

    flush: (id, opts) => call("POST", `/api/sessions/${enc(id)}/flush`, opts),
    /** @param {number} [keepLast] - defaults server-side (session.DefaultPruneKeepLast) */
    prune: (id, keepLast, opts) =>
      call("POST", `/api/sessions/${enc(id)}/prune`, {
        body: { keep_last: keepLast },
        ...opts,
      }),

    getCallback: (id, opts) => call("GET", `/api/sessions/${enc(id)}/callback`, opts),
    createCallback: (id, opts) => call("POST", `/api/sessions/${enc(id)}/callback`, opts),
    revokeCallback: (id, opts) => call("DELETE", `/api/sessions/${enc(id)}/callback`, opts),

    getUserData: (id, opts) => call("GET", `/api/sessions/${enc(id)}/user-data`, opts),
    /** @param {object} body - {attributes: session.UserDataAttribute[]} */
    setUserData: (id, body, opts) =>
      call("PUT", `/api/sessions/${enc(id)}/user-data`, { body, ...opts }),

    promptArgCache: (id, promptName, opts) =>
      call("GET", `/api/sessions/${enc(id)}/prompt-arg-cache`, {
        query: { prompt: promptName },
        ...opts,
      }),

    acknowledgeUIPrompt: (id, requestId, opts) =>
      call("POST", `/api/sessions/${enc(id)}/ui-prompt/acknowledge`, {
        body: { request_id: requestId },
        ...opts,
      }),

    images: {
      list: (id, opts) => call("GET", `/api/sessions/${enc(id)}/images`, opts),
      /** @param {FormData} form - must contain an "image" file field; the
       *  runtime sets the multipart Content-Type/boundary (see transport.js
       *  `isPassthroughBody`). */
      upload: (id, form, opts) =>
        call("POST", `/api/sessions/${enc(id)}/images`, { body: form, ...opts }),
      /** @param {string[]} paths - absolute file paths (native app only; the
       *  server restricts this endpoint to localhost connections). */
      uploadFromPath: (id, paths, opts) =>
        call("POST", `/api/sessions/${enc(id)}/images/from-path`, {
          body: { paths },
          ...opts,
        }),
      /** Returns the browser-usable URL for an image (e.g. <img src>); does
       *  not fetch bytes. Use `fetchImage()` to retrieve the raw Response. */
      url: (id, imageId) => `/api/sessions/${enc(id)}/images/${enc(imageId)}`,
      /** @returns {Promise<Response>} the raw, undecoded image response. */
      fetchImage: (id, imageId, opts) =>
        call("GET", `/api/sessions/${enc(id)}/images/${enc(imageId)}`, {
          raw: true,
          ...opts,
        }),
      remove: (id, imageId, opts) =>
        call("DELETE", `/api/sessions/${enc(id)}/images/${enc(imageId)}`, opts),
    },
  };
}
