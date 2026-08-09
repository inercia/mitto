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
 *
 * queue/loop (mitto-7gta.8): mirror internal/web/handlers/queue.go and
 * session_loop*.go 1:1. Request bodies are passed through verbatim (no
 * client-side field whitelisting, no legacy-key rewriting, no pre-validation
 * of server-enforced invariants like queue-full or the multi-trigger
 * `triggers` list) — the server is the single source of truth and any
 * rejection surfaces as `MittoApiError`.
 */
import { buildUrl, request } from "../core/transport.js";

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
       *  not fetch bytes. Use `fetchImage()` to retrieve the raw Response.
       *  Unlike the other methods it never reaches `request()`, so it applies
       *  `buildUrl()` itself to pick up `baseUrl`/`apiPrefix`. */
      url: (id, imageId) =>
        buildUrl(config, `/api/sessions/${enc(id)}/images/${enc(imageId)}`),
      /** @returns {Promise<Response>} the raw, undecoded image response. */
      fetchImage: (id, imageId, opts) =>
        call("GET", `/api/sessions/${enc(id)}/images/${enc(imageId)}`, {
          raw: true,
          ...opts,
        }),
      remove: (id, imageId, opts) =>
        call("DELETE", `/api/sessions/${enc(id)}/images/${enc(imageId)}`, opts),
    },

    queue: {
      /** @returns {Promise<{messages: object[], count: number}>} */
      list: (id, opts) => call("GET", `/api/sessions/${enc(id)}/queue`, opts),
      /** @param {object} body - {message?, image_ids?, file_ids?,
       *   scheduled_time?, arguments?, prompt_name?}. `message` or
       *   `prompt_name` is required (server rejects both empty). */
      add: (id, body, opts) => call("POST", `/api/sessions/${enc(id)}/queue`, { body, ...opts }),
      /** Sugar over `add()` for enqueuing a named workspace prompt.
       *  @param {string} promptName
       *  @param {object} [args] - values for the prompt's .Args placeholders
       *  @param {object} [extra] - additional QueueAddRequest fields
       *   (image_ids, file_ids, scheduled_time) merged into the body */
      addNamed: (id, promptName, args, extra, opts) =>
        call("POST", `/api/sessions/${enc(id)}/queue`, {
          body: { prompt_name: promptName, arguments: args, ...extra },
          ...opts,
        }),
      get: (id, msgId, opts) =>
        call("GET", `/api/sessions/${enc(id)}/queue/${enc(msgId)}`, opts),
      remove: (id, msgId, opts) =>
        call("DELETE", `/api/sessions/${enc(id)}/queue/${enc(msgId)}`, opts),
      /** Clears the entire queue (DELETE with no message id). */
      clear: (id, opts) => call("DELETE", `/api/sessions/${enc(id)}/queue`, opts),
      /** @param {"up"|"down"} direction */
      move: (id, msgId, direction, opts) =>
        call("POST", `/api/sessions/${enc(id)}/queue/${enc(msgId)}/move`, {
          body: { direction },
          ...opts,
        }),
      /** Queue behavior ({enabled, delay_seconds, max_size,
       *  auto_generate_titles}) is global/workspace-scoped, NOT per-session
       *  (docs/devel/message-queue.md §Configuration Scope) — there is no
       *  per-session queue-config route. Reads it from the `conversations`
       *  section of GET /api/config; `id` is accepted for call-site symmetry
       *  with the rest of this resource but is otherwise unused. Returns
       *  `undefined` when the server config has no queue section (not
       *  synthesized client-side). */
      config: async (id, opts) => (await call("GET", "/api/config", opts))?.conversations?.queue,
    },

    loop: {
      get: (id, opts) => call("GET", `/api/sessions/${enc(id)}/loop`, opts),
      /** @param {object} body - LoopPromptRequest: {prompt, prompt_name?,
       *   frequency, enabled, fresh_context?, max_iterations?, triggers?,
       *   child_events?, delay_seconds?, max_duration_seconds?, arguments?,
       *   condition?, condition_preset?, cooldown_seconds?,
       *   coalesce_during_busy?, run_on_start?, settle_window_seconds?}.
       *   Full replace (PUT). The legacy scalar "trigger" key is not
       *   accepted — use "triggers" (mitto-r6j.5). */
      set: (id, body, opts) => call("PUT", `/api/sessions/${enc(id)}/loop`, { body, ...opts }),
      /** @param {object} patch - LoopPromptPatchRequest, same fields as
       *   `set()` but all optional; a field is only changed when present
       *   (partial update, PATCH). `reset_counters: true` zeroes the
       *   iteration/elapsed-time counters. */
      update: (id, patch, opts) =>
        call("PATCH", `/api/sessions/${enc(id)}/loop`, { body: patch, ...opts }),
      /** Detaches the loop (preserving its settings for `restore()`).
       *  Resolves to `null` (204 No Content). */
      detach: (id, opts) => call("DELETE", `/api/sessions/${enc(id)}/loop`, opts),
      /** Restores the most recently detached loop settings. 409s
       *  (-> MittoApiError) if a loop is already configured. */
      restore: (id, opts) => call("POST", `/api/sessions/${enc(id)}/loop/restore`, opts),
      /** Triggers an immediate run, bypassing the schedule.
       *  @param {boolean} [resetTimer] - when omitted, no body is sent and
       *   the server defaults to true (reset the countdown). */
      runNow: (id, resetTimer, opts) =>
        call("POST", `/api/sessions/${enc(id)}/loop/run-now`, {
          body: resetTimer === undefined ? undefined : { reset_timer: resetTimer },
          ...opts,
        }),
      /** Read-only: a LoopPrompt draft pre-filled from the most recent named
       *  prompt's `loop:` frontmatter. Never writes session state. */
      suggestFromRecent: (id, opts) =>
        call("GET", `/api/sessions/${enc(id)}/loop/suggest-from-recent`, opts),
      acknowledgeStoppedReason: (id, opts) =>
        call("POST", `/api/sessions/${enc(id)}/loop/acknowledge-stopped-reason`, opts),
      /** Sugar over `update()` — toggles `enabled` without touching other
       *  fields (the only two ways the backend pauses/resumes a loop). */
      enable: (id, opts) =>
        call("PATCH", `/api/sessions/${enc(id)}/loop`, { body: { enabled: true }, ...opts }),
      disable: (id, opts) =>
        call("PATCH", `/api/sessions/${enc(id)}/loop`, { body: { enabled: false }, ...opts }),
    },
  };
}
