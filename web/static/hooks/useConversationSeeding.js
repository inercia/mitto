// Mitto Web Interface - Conversation Seeding Hook
// Shared helper to seed a conversation with a named prompt via prompt_name,
// or to create a new loop conversation driven by a named prompt.

import { secureFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";
import { endpoints } from "../utils/index.js";

/**
 * Parse a duration string or number into seconds.
 * - number → that many seconds (clamped to >= 0)
 * - string matching "NNu" where u is s/m/h/d (case-insensitive) → converted to seconds
 * - otherwise (undefined, null, unrecognised string) → 0
 *
 * @param {string|number|undefined|null} input
 * @returns {number}
 */
export function parseDurationToSeconds(input) {
  if (typeof input === "number") return Math.max(0, Math.floor(input));
  if (typeof input !== "string") return 0;
  const m = input.trim().match(/^(\d+)\s*([smhd])$/i);
  if (!m) return 0;
  const v = parseInt(m[1], 10);
  switch (m[2].toLowerCase()) {
    case "s":
      return v;
    case "m":
      return v * 60;
    case "h":
      return v * 3600;
    case "d":
      return v * 86400;
    default:
      return 0;
  }
}

/**
 * Decide which loop action to take based on the target session's state.
 *
 * Returns one of:
 *   "new-loop"  — no session (or no session_id): create a NEW loop conversation.
 *   "one-shot"  — session is already a loop, or it is a child: send once, do NOT modify config.
 *   "make-loop" — regular running conversation: configure it as a loop now.
 *
 * @param {Object|null|undefined} session - The target session object (from session list / info).
 * @returns {"new-loop" | "one-shot" | "make-loop"}
 */
export function decideLoopAction(session) {
  if (!session || !session.session_id) return "new-loop";
  if (session.loop_enabled || session.loop_configured) return "one-shot";
  if (session.parent_session_id) return "one-shot";
  return "make-loop";
}

/**
 * Make an existing regular conversation immediately a loop using a prompt's
 * declared defaults, then fire the first run.
 *
 * Steps:
 *   1. PUT /api/sessions/{id}/loop  — configure prompt_name + frequency + max_iterations
 *   2. POST /api/sessions/{id}/loop/run-now  — fire first run (reset_timer: true)
 *
 * @param {string} sessionId
 * @param {{ name: string, loop?: { value?: number, unit?: string, at?: string, maxIterations?: number } }} prompt
 * @param {{ arguments?: Object, fetchImpl?: Function }} [opts]
 * @returns {Promise<{ success: boolean, error?: string }>}
 */
export async function makeLoopNow(
  sessionId,
  prompt,
  { arguments: args, fetchImpl } = {},
) {
  if (!sessionId || !prompt?.name) {
    return { success: false, error: "invalid_request" };
  }

  const p = prompt?.loop || {};
  const value = p.value || 1;
  const unit = p.unit || "hours";
  const frequency = { value, unit };
  if (unit === "days" && p.at) {
    frequency.at = p.at;
  }

  const maxIterations =
    typeof p.maxIterations === "number" && p.maxIterations > 0
      ? p.maxIterations
      : 0;

  // New trigger/delay/maxDuration fields from prompt loop defaults.
  const trigger = p.trigger || "schedule";
  const delaySeconds = p.delay ?? 0;
  const maxDurationSeconds = parseDurationToSeconds(p.maxDuration);
  // onTasks CEL condition, from the prompt's loop frontmatter default.
  // conditionPreset is intentionally NOT threaded here (mitto-pei).
  const condition = p.condition ?? "";

  // Frontmatter-driven booleans forwarded verbatim (mitto-le4.3). Only sent
  // when the source is a real boolean — undefined/null are omitted so we do
  // not serialize accidental `null`s. Precedence has a single source here
  // (prompt.loop), unlike configureLoopSchedule which also honours a dialog
  // override.
  const freshContext = p.freshContext;
  const runOnStart = p.runOnStart;
  const coalesceDuringBusy = p.coalesceDuringBusy;

  const fetch_ = fetchImpl || secureFetch;

  // Step 1: configure loop
  try {
    const putResp = await fetch_(endpoints.sessions.loop(sessionId), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt_name: prompt.name,
        frequency,
        enabled: true,
        max_iterations: maxIterations,
        trigger,
        delay_seconds: delaySeconds,
        max_duration_seconds: maxDurationSeconds,
        ...(trigger === "onTasks" ? { condition } : {}),
        ...(typeof freshContext === "boolean"
          ? { fresh_context: freshContext }
          : {}),
        ...(typeof runOnStart === "boolean"
          ? { run_on_start: runOnStart }
          : {}),
        ...(typeof coalesceDuringBusy === "boolean"
          ? { coalesce_during_busy: coalesceDuringBusy }
          : {}),
        ...(args && typeof args === "object" && Object.keys(args).length > 0
          ? { arguments: args }
          : {}),
      }),
    });
    if (!putResp.ok) {
      let errData = {};
      try {
        errData = await putResp.json();
      } catch (_) {}
      return {
        success: false,
        error: errData.error || "loop_setup_failed",
      };
    }
  } catch (err) {
    console.error("makeLoopNow PUT error:", err);
    return { success: false, error: "loop_setup_failed" };
  }

  // Step 2: fire first run.
  // NOTE: by this point the PUT above has already persisted the loop config
  // (the conversation IS a loop). The run-now POST is best-effort: a 409
  // (Conflict / session busy) means a run is already in flight — e.g. enabling a
  // schedule-based config immediately fired its first run — so the loop is set
  // and running. Treat 409 as success rather than surfacing a misleading
  // "failed to configure loop" error to the user.
  try {
    const runResp = await fetch_(endpoints.sessions.loopRunNow(sessionId), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ reset_timer: true }),
    });
    if (!runResp.ok) {
      if (runResp.status === 409) {
        // Already running — config is set, a run is in flight. Not a failure.
        return { success: true };
      }
      let errData = {};
      try {
        errData = await runResp.json();
      } catch (_) {}
      return { success: false, error: errData.error || "run_now_failed" };
    }
  } catch (err) {
    console.error("makeLoopNow run-now error:", err);
    return { success: false, error: "run_now_failed" };
  }

  return { success: true };
}

/**
 * Build the POST body for seeding a conversation queue with a named prompt.
 * Never includes `message` or the full prompt body.
 * @param {{ name: string }} prompt
 * @param {{ arguments?: Object }} [opts]
 * @returns {Object}
 */
export function buildSeedQueueBody(prompt, { arguments: args } = {}) {
  const body = { prompt_name: prompt.name };
  if (args && typeof args === "object" && Object.keys(args).length > 0) {
    body.arguments = args;
  }
  return body;
}

/**
 * POST a named prompt to a session's queue.
 * @param {string} sessionId
 * @param {{ name: string }} prompt
 * @param {{ arguments?: Object, fetchImpl?: Function }} [opts]
 * @returns {Promise<{ success: boolean, messageId?: string, error?: string }>}
 */
export async function seedConversationWithPrompt(
  sessionId,
  prompt,
  { arguments: args, fetchImpl } = {},
) {
  if (!sessionId || !prompt?.name) {
    return { success: false, error: "invalid_request" };
  }

  const fetch_ = fetchImpl || secureFetch;
  const body = buildSeedQueueBody(prompt, { arguments: args });

  try {
    const resp = await fetch_(endpoints.sessions.queue(sessionId), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    let data = {};
    try {
      data = await resp.json();
    } catch (_) {}

    if (resp.ok || resp.status === 201) {
      return { success: true, messageId: data.id };
    }
    return {
      success: false,
      error: data.error?.code || data.error || "request_failed",
    };
  } catch (err) {
    console.error("seedConversationWithPrompt error:", err);
    return { success: false, error: "request_failed" };
  }
}

/**
 * Configure a loop schedule on a newly-created session via PUT.
 * Includes max_iterations when loop.maxIterations is a positive number,
 * or falls back to prompt?.loop?.maxIterations. Sends 0 (unlimited) otherwise.
 * @param {string} sessionId
 * @param {{ name: string, loop?: { maxIterations?: number } }} prompt
 * @param {{ value: number, unit: string, at?: string, maxIterations?: number }} loop
 * @param {{ arguments?: Object, fetchImpl?: Function }} [opts]
 * @returns {Promise<{ success: boolean, error?: string }>}
 */
export async function configureLoopSchedule(
  sessionId,
  prompt,
  loop,
  { arguments: args, fetchImpl } = {},
) {
  const { value, unit, at } = loop;
  const frequency = { value, unit };
  // Only include 'at' for daily schedules (matches backend Frequency.Validate() rules)
  if (unit === "days" && at) {
    frequency.at = at;
  }

  // Resolve max_iterations: from the dialog's returned value, then from prompt defaults.
  // A positive number is sent as-is; 0 means unlimited.
  let maxIterations = 0;
  if (typeof loop.maxIterations === "number" && loop.maxIterations > 0) {
    maxIterations = loop.maxIterations;
  } else if (
    typeof prompt?.loop?.maxIterations === "number" &&
    prompt.loop.maxIterations > 0
  ) {
    maxIterations = prompt.loop.maxIterations;
  }

  // New trigger/delay/maxDuration fields: from dialog result, then prompt defaults.
  const trigger = loop.trigger || prompt?.loop?.trigger || "schedule";
  const delaySeconds = loop.delaySeconds ?? prompt?.loop?.delay ?? 0;
  const maxDurationSeconds =
    loop.maxDurationSeconds ??
    parseDurationToSeconds(prompt?.loop?.maxDuration);
  // onTasks CEL condition: from the dialog result, then the prompt's loop
  // frontmatter default. conditionPreset is intentionally NOT threaded
  // here (mitto-pei).
  const condition = loop.condition ?? prompt?.loop?.condition ?? "";

  // Frontmatter-driven booleans forwarded verbatim (mitto-le4.3). Precedence:
  // dialog result → prompt.loop default → omit from body. `??` correctly keeps
  // an explicit `false` from the dialog rather than falling through to the
  // prompt default. Only real booleans reach the wire — undefined/null are
  // omitted so we do not serialize accidental `null`s.
  const freshContext = loop.freshContext ?? prompt?.loop?.freshContext;
  const runOnStart = loop.runOnStart ?? prompt?.loop?.runOnStart;
  const coalesceDuringBusy =
    loop.coalesceDuringBusy ?? prompt?.loop?.coalesceDuringBusy;

  const fetch_ = fetchImpl || secureFetch;
  try {
    const resp = await fetch_(endpoints.sessions.loop(sessionId), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt_name: prompt?.name,
        frequency,
        enabled: true,
        max_iterations: maxIterations,
        trigger,
        delay_seconds: delaySeconds,
        max_duration_seconds: maxDurationSeconds,
        ...(trigger === "onTasks" ? { condition } : {}),
        ...(typeof freshContext === "boolean"
          ? { fresh_context: freshContext }
          : {}),
        ...(typeof runOnStart === "boolean"
          ? { run_on_start: runOnStart }
          : {}),
        ...(typeof coalesceDuringBusy === "boolean"
          ? { coalesce_during_busy: coalesceDuringBusy }
          : {}),
        ...(args && typeof args === "object" && Object.keys(args).length > 0
          ? { arguments: args }
          : {}),
      }),
    });

    if (resp.ok) {
      return { success: true };
    }
    let errData = {};
    try {
      errData = await resp.json();
    } catch (_) {}
    return { success: false, error: errData.error || "loop_setup_failed" };
  } catch (err) {
    console.error("configureLoopSchedule error:", err);
    return { success: false, error: "loop_setup_failed" };
  }
}

/**
 * Hook providing two entry points for conversation seeding.
 * @param {{ newSession: Function }} deps
 */
export function useConversationSeeding({ newSession }) {
  const { useCallback } = window.preact;
  const seedExisting = useCallback(
    (sessionId, prompt, opts) =>
      seedConversationWithPrompt(sessionId, prompt, opts),
    [],
  );

  const startConversationWithPrompt = useCallback(
    /**
     * Create a new conversation seeded with a named prompt (one-time queue),
     * or create a new loop conversation driven by the named prompt.
     *
     * When `loop` is absent (or falsy): behave exactly as before — the
     * session is created with `initialPromptName` so the queue delivers the
     * prompt as a one-time message.
     *
     * When `loop` is present: the session is created WITHOUT a queue seed,
     * then `PUT /api/sessions/{id}/loop` configures the named prompt on the
     * loop schedule. `at` (if provided) must already be in UTC HH:MM.
     *
     * originPromptName is set on the session opts from prompt.name so the
     * backend can later detect duplicate singleton-prompt conversations.
     *
     * @param {{ workingDir, acpServer, name, beadsIssue, prompt, arguments, loop, fetchImpl }} opts
     * @returns {Promise<{ sessionId: string, reused?: boolean } | { error: string }>}
     */
    async ({
      workingDir,
      acpServer,
      name,
      beadsIssue,
      prompt,
      arguments: args,
      loop,
      fetchImpl,
    }) => {
      // Build the newSession call — skip the queue seed when loop is present.
      const sessionOpts = {
        workingDir,
        acpServer,
        name,
        beadsIssue,
        originPromptName: prompt?.name,
      };
      if (!loop) {
        // One-time path: pass the named prompt so the queue delivers it once.
        sessionOpts.initialPromptName = prompt?.name;
        sessionOpts.arguments = args;
      }

      const result = await newSession(sessionOpts);
      if (!result?.sessionId) {
        return { error: result?.error || "session_creation_failed" };
      }

      if (loop) {
        // Loop path: configure the schedule via PUT after creation.
        const putResult = await configureLoopSchedule(
          result.sessionId,
          prompt,
          loop,
          { arguments: args, fetchImpl },
        );
        if (!putResult.success) {
          // Session was created but loop config failed — surface the error.
          return { error: putResult.error };
        }
      }

      return { sessionId: result.sessionId, reused: result.reused === true };
    },
    [newSession],
  );

  return {
    seedConversationWithPrompt: seedExisting,
    startConversationWithPrompt,
  };
}
