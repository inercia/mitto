/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 */
export function createSessionsResource(config: import("../core/config.js").ResolvedConfig): {
    list: (opts: any) => Promise<any>;
    running: (opts: any) => Promise<any>;
    get: (id: any, opts: any) => Promise<any>;
    /** @param {object} [body] - {name?, working_dir?, acp_server?, beads_issue?,
     *   origin_prompt_name?, initial_prompt_name?, arguments?}
     *  @param {import("../core/transport.js").RequestOptions} [opts] -
     *   forwarded to request() (e.g. headers, signal) */
    create: (body?: object, opts?: import("../core/transport.js").RequestOptions) => Promise<any>;
    /** @param {object} patch - {name?, description?, pinned?, archived?,
     *   beads_issue?, background_color?} */
    update: (id: any, patch: object, opts: any) => Promise<any>;
    remove: (id: any, opts: any) => Promise<any>;
    /** @param {object} [params] - {limit?, before?, order?: "asc"|"desc"}
     *  @param {import("../core/transport.js").RequestOptions} [opts] -
     *   forwarded to request() (e.g. headers, signal) */
    events: (id: any, params?: object, opts?: import("../core/transport.js").RequestOptions) => Promise<any>;
    changes: (id: any, opts: any) => Promise<any>;
    getSettings: (id: any, opts: any) => Promise<any>;
    /** @param {object} settings - map of setting name -> bool, merged server-side */
    updateSettings: (id: any, settings: object, opts: any) => Promise<any>;
    flush: (id: any, opts: any) => Promise<any>;
    /** @param {number} [keepLast] - defaults server-side (session.DefaultPruneKeepLast)
     *  @param {import("../core/transport.js").RequestOptions} [opts] -
     *   forwarded to request() (e.g. headers, signal) */
    prune: (id: any, keepLast?: number, opts?: import("../core/transport.js").RequestOptions) => Promise<any>;
    getCallback: (id: any, opts: any) => Promise<any>;
    createCallback: (id: any, opts: any) => Promise<any>;
    revokeCallback: (id: any, opts: any) => Promise<any>;
    getUserData: (id: any, opts: any) => Promise<any>;
    /** @param {object} body - {attributes: session.UserDataAttribute[]} */
    setUserData: (id: any, body: object, opts: any) => Promise<any>;
    promptArgCache: (id: any, promptName: any, opts: any) => Promise<any>;
    acknowledgeUIPrompt: (id: any, requestId: any, opts: any) => Promise<any>;
    images: {
        list: (id: any, opts: any) => Promise<any>;
        /** @param {FormData} form - must contain an "image" file field; the
         *  runtime sets the multipart Content-Type/boundary (see transport.js
         *  `isPassthroughBody`). */
        upload: (id: any, form: FormData, opts: any) => Promise<any>;
        /** @param {string[]} paths - absolute file paths (native app only; the
         *  server restricts this endpoint to localhost connections). */
        uploadFromPath: (id: any, paths: string[], opts: any) => Promise<any>;
        /** Returns the browser-usable URL for an image (e.g. <img src>); does
         *  not fetch bytes. Use `fetchImage()` to retrieve the raw Response.
         *  Unlike the other methods it never reaches `request()`, so it applies
         *  `buildUrl()` itself to pick up `baseUrl`/`apiPrefix`. */
        url: (id: any, imageId: any) => string;
        /** @returns {Promise<Response>} the raw, undecoded image response. */
        fetchImage: (id: any, imageId: any, opts: any) => Promise<Response>;
        remove: (id: any, imageId: any, opts: any) => Promise<any>;
    };
    queue: {
        /** @returns {Promise<{messages: object[], count: number}>} */
        list: (id: any, opts: any) => Promise<{
            messages: object[];
            count: number;
        }>;
        /** @param {object} body - {message?, image_ids?, file_ids?,
         *   scheduled_time?, arguments?, prompt_name?}. `message` or
         *   `prompt_name` is required (server rejects both empty). */
        add: (id: any, body: object, opts: any) => Promise<any>;
        /** Sugar over `add()` for enqueuing a named workspace prompt.
         *  @param {string} promptName
         *  @param {object} [args] - values for the prompt's .Args placeholders
         *  @param {object} [extra] - additional QueueAddRequest fields
         *   (image_ids, file_ids, scheduled_time) merged into the body
         *  @param {import("../core/transport.js").RequestOptions} [opts] -
         *   forwarded to request() (e.g. headers, signal) */
        addNamed: (id: any, promptName: string, args?: object, extra?: object, opts?: import("../core/transport.js").RequestOptions) => Promise<any>;
        get: (id: any, msgId: any, opts: any) => Promise<any>;
        remove: (id: any, msgId: any, opts: any) => Promise<any>;
        /** Clears the entire queue (DELETE with no message id). */
        clear: (id: any, opts: any) => Promise<any>;
        /** @param {"up"|"down"} direction */
        move: (id: any, msgId: any, direction: "up" | "down", opts: any) => Promise<any>;
        /** Queue behavior ({enabled, delay_seconds, max_size,
         *  auto_generate_titles}) is global/workspace-scoped, NOT per-session
         *  (docs/devel/message-queue.md §Configuration Scope) — there is no
         *  per-session queue-config route. Reads it from the `conversations`
         *  section of GET /api/config; `id` is accepted for call-site symmetry
         *  with the rest of this resource but is otherwise unused. Returns
         *  `undefined` when the server config has no queue section (not
         *  synthesized client-side). */
        config: (id: any, opts: any) => Promise<any>;
    };
    loop: {
        get: (id: any, opts: any) => Promise<any>;
        /** @param {object} body - LoopPromptRequest: {prompt, prompt_name?,
         *   frequency, enabled, fresh_context?, max_iterations?, triggers?,
         *   child_events?, delay_seconds?, max_duration_seconds?, arguments?,
         *   condition?, condition_preset?, cooldown_seconds?,
         *   coalesce_during_busy?, run_on_start?, settle_window_seconds?,
         *   loop_apply_prompt_defaults?}.
         *   Full replace (PUT). The legacy scalar "trigger" key is not
         *   accepted — use "triggers" (mitto-r6j.5). */
        set: (id: any, body: object, opts: any) => Promise<any>;
        /** @param {object} patch - LoopPromptPatchRequest: the same fields as
         *   `set()` (minus `loop_apply_prompt_defaults`) but all optional; a
         *   field is only changed when present (partial update, PATCH).
         *   `triggers` and `child_events`, when present, REPLACE the stored
         *   lists wholesale. `reset_counters: true` zeroes the
         *   iteration/elapsed-time counters. */
        update: (id: any, patch: object, opts: any) => Promise<any>;
        /** Detaches the loop (preserving its settings for `restore()`).
         *  Resolves to `null` (204 No Content). */
        detach: (id: any, opts: any) => Promise<any>;
        /** Restores the most recently detached loop settings. 409s
         *  (-> MittoApiError) if a loop is already configured. */
        restore: (id: any, opts: any) => Promise<any>;
        /** Triggers an immediate run, bypassing the schedule.
         *  @param {boolean} [resetTimer] - when omitted, no body is sent and
         *   the server defaults to true (reset the countdown).
         *  @param {import("../core/transport.js").RequestOptions} [opts] -
         *   forwarded to request() (e.g. headers, signal) */
        runNow: (id: any, resetTimer?: boolean, opts?: import("../core/transport.js").RequestOptions) => Promise<any>;
        /** Read-only: a LoopPrompt draft pre-filled from the most recent named
         *  prompt's `loop:` frontmatter. Never writes session state. */
        suggestFromRecent: (id: any, opts: any) => Promise<any>;
        acknowledgeStoppedReason: (id: any, opts: any) => Promise<any>;
        /** Sugar over `update()` — toggles `enabled` without touching other
         *  fields (the only two ways the backend pauses/resumes a loop). */
        enable: (id: any, opts: any) => Promise<any>;
        disable: (id: any, opts: any) => Promise<any>;
    };
};
