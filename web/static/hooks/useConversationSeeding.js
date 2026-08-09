// Mitto Web Interface - Conversation Seeding Hook
// Shared helper to seed a conversation with a named prompt via prompt_name,
// or to create a new loop conversation driven by a named prompt.

import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus } from "../utils/sdkErrors.js";
import { endpoints } from "../utils/index.js";

/**
 * Normalize a prompt.loop.trigger field into an array of trigger names
 * (mitto-r6j). The prompt frontmatter schema stores `trigger:` as a list
 * (e.g. ["schedule", "onCompletion"]), but legacy prompts on disk / MCP
 * responses may still surface the pre-r6j scalar string. Anything not
 * recognisable falls back to ["schedule"] to preserve pre-r6j behaviour.
 *
 * @param {unknown} raw
 * @returns {string[]}
 */
export function normalizePromptTriggers(raw) {
  if (Array.isArray(raw)) {
    const cleaned = raw.filter((t) => typeof t === "string" && t.length > 0);
    return cleaned.length > 0 ? cleaned : ["schedule"];
  }
  if (typeof raw === "string" && raw.length > 0) return [raw];
  return ["schedule"];
}

/**
 * Extract the loop attributes from a prompt.loop block that MAY use the new
 * nested-per-trigger schema (mitto-r6j: `schedule: { value, unit, at }`,
 * `onCompletion: { delay }`, `onTasks: { condition, conditionPreset,
 * coalesceDuringBusy, settleWindow, cooldown }`). Returns a flat view with
 * safe defaults so callers do not have to reach into optional nested blocks.
 *
 * @param {Object|null|undefined} loopBlock
 * @returns {{ triggers: string[], value: number, unit: string, at: string,
 *   delay: number, condition: string, conditionPreset: string,
 *   coalesceDuringBusy: boolean|undefined, maxIterations: number,
 *   maxDuration: string, freshContext: boolean|undefined,
 *   runOnStart: boolean|undefined }}
 */
export function readPromptLoopDefaults(loopBlock) {
  const p = loopBlock || {};
  const sched = p.schedule || {};
  const oc = p.onCompletion || {};
  const ot = p.onTasks || {};
  return {
    triggers: normalizePromptTriggers(p.trigger),
    value: sched.value ?? 1,
    unit: sched.unit ?? "hours",
    at: sched.at ?? "",
    delay: oc.delay ?? 0,
    condition: ot.condition ?? "",
    conditionPreset: ot.conditionPreset ?? "",
    coalesceDuringBusy: ot.coalesceDuringBusy,
    maxIterations: p.maxIterations ?? 0,
    maxDuration: p.maxDuration ?? "",
    freshContext: p.freshContext,
    runOnStart: p.runOnStart,
  };
}

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
 *   1. PUT /api/sessions/{id}/loop  — configure prompt_name + frequency +
 *      triggers[] + max_iterations
 *   2. POST /api/sessions/{id}/loop/run-now  — fire first run (reset_timer: true)
 *
 * prompt.loop follows the mitto-r6j nested-per-trigger schema:
 *   {
 *     trigger: string[],  // armed triggers (schedule|onCompletion|onTasks)
 *     schedule?: { value, unit, at? },
 *     onCompletion?: { delay },
 *     onTasks?: { condition, conditionPreset, coalesceDuringBusy },
 *     maxIterations?, maxDuration?, freshContext?, runOnStart?,
 *   }
 *
 * @param {string} sessionId
 * @param {{ name: string, loop?: Object }} prompt
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

  // mitto-r6j: prompt.loop defaults now live under nested per-trigger blocks
  // (loop.schedule.*, loop.onCompletion.*, loop.onTasks.*) and loop.trigger is
  // a list of armed trigger names. readPromptLoopDefaults returns a flat view
  // with safe fallbacks so we can construct the REST body without reaching
  // into optional nested objects at every use.
  const p = readPromptLoopDefaults(prompt?.loop);
  const frequency = { value: p.value, unit: p.unit };
  if (p.unit === "days" && p.at) {
    frequency.at = p.at;
  }

  const maxIterations =
    typeof p.maxIterations === "number" && p.maxIterations > 0
      ? p.maxIterations
      : 0;

  const triggers = p.triggers;
  const delaySeconds = p.delay;
  const maxDurationSeconds = parseDurationToSeconds(p.maxDuration);
  // onTasks CEL condition, from the prompt's loop frontmatter default.
  // conditionPreset is intentionally NOT threaded here (mitto-pei).
  const condition = p.condition;

  // Frontmatter-driven booleans forwarded verbatim (mitto-le4.3). Only sent
  // when the source is a real boolean — undefined/null are omitted so we do
  // not serialize accidental `null`s. Precedence has a single source here
  // (prompt.loop), unlike configureLoopSchedule which also honours a dialog
  // override.
  const { freshContext, runOnStart, coalesceDuringBusy } = p;

  // Step 1: configure loop. Send `triggers` (canonical list, mitto-r6j) as
  // the primary field. condition is sent when onTasks is one of the armed
  // triggers.
  const armsOnTasks = triggers.includes("onTasks");
  const loopBody = {
    prompt_name: prompt.name,
    frequency,
    enabled: true,
    max_iterations: maxIterations,
    triggers,
    delay_seconds: delaySeconds,
    max_duration_seconds: maxDurationSeconds,
    ...(armsOnTasks ? { condition } : {}),
    ...(typeof freshContext === "boolean"
      ? { fresh_context: freshContext }
      : {}),
    ...(typeof runOnStart === "boolean" ? { run_on_start: runOnStart } : {}),
    ...(typeof coalesceDuringBusy === "boolean"
      ? { coalesce_during_busy: coalesceDuringBusy }
      : {}),
    ...(args && typeof args === "object" && Object.keys(args).length > 0
      ? { arguments: args }
      : {}),
  };

  if (fetchImpl) {
    try {
      const putResp = await fetchImpl(endpoints.sessions.loop(sessionId), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(loopBody),
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
  } else {
    try {
      await getSdkClient().sessions.loop.set(sessionId, loopBody);
    } catch (err) {
      console.error("makeLoopNow PUT error:", err);
      if (errorStatus(err) === undefined) {
        return { success: false, error: "loop_setup_failed" };
      }
      return { success: false, error: err.body?.error || "loop_setup_failed" };
    }
  }

  // Step 2: fire first run.
  // NOTE: by this point the PUT above has already persisted the loop config
  // (the conversation IS a loop). The run-now POST is best-effort: a 409
  // (Conflict / session busy) means a run is already in flight — e.g. enabling a
  // schedule-based config immediately fired its first run — so the loop is set
  // and running. Treat 409 as success rather than surfacing a misleading
  // "failed to configure loop" error to the user.
  if (fetchImpl) {
    try {
      const runResp = await fetchImpl(
        endpoints.sessions.loopRunNow(sessionId),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ reset_timer: true }),
        },
      );
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
  } else {
    try {
      await getSdkClient().sessions.loop.runNow(sessionId, true);
    } catch (err) {
      if (errorStatus(err) === 409) {
        // Already running — config is set, a run is in flight. Not a failure.
        return { success: true };
      }
      console.error("makeLoopNow run-now error:", err);
      if (errorStatus(err) === undefined) {
        return { success: false, error: "run_now_failed" };
      }
      return { success: false, error: err.body?.error || "run_now_failed" };
    }
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

  const body = buildSeedQueueBody(prompt, { arguments: args });

  if (fetchImpl) {
    try {
      const resp = await fetchImpl(endpoints.sessions.queue(sessionId), {
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

  try {
    const data = await getSdkClient().sessions.queue.add(sessionId, body);
    return { success: true, messageId: data?.id };
  } catch (err) {
    console.error("seedConversationWithPrompt error:", err);
    if (errorStatus(err) === undefined) {
      return { success: false, error: "request_failed" };
    }
    return { success: false, error: err.code || "request_failed" };
  }
}

/**
 * Configure a loop schedule on a newly-created session via PUT.
 *
 * The `loop` param carries the dialog's confirm result (mitto-r6j):
 *   - triggers: string[] — armed triggers (required, non-empty)
 *   - value, unit, at?: schedule frequency (used when "schedule" is armed)
 *   - maxIterations?: run cap (falls back to prompt default)
 *   - delaySeconds?: onCompletion delay (falls back to prompt default)
 *   - maxDurationSeconds?: wall-clock cap (falls back to prompt default)
 *   - condition?: onTasks CEL (falls back to prompt default)
 *   - freshContext?, runOnStart?, coalesceDuringBusy?: boolean overrides
 *
 * Prompt-level defaults are read from the new nested-per-trigger schema via
 * readPromptLoopDefaults, so callers that pass an incomplete `loop` (e.g.
 * only picked the triggers) still get a fully-formed REST payload.
 *
 * @param {string} sessionId
 * @param {{ name?: string, loop?: Object }} prompt
 * @param {Object} loop
 * @param {{ arguments?: Object, fetchImpl?: Function }} [opts]
 * @returns {Promise<{ success: boolean, error?: string }>}
 */
export async function configureLoopSchedule(
  sessionId,
  prompt,
  loop,
  { arguments: args, fetchImpl } = {},
) {
  const promptDefaults = readPromptLoopDefaults(prompt?.loop);
  const value = loop.value ?? promptDefaults.value;
  const unit = loop.unit ?? promptDefaults.unit;
  const at = loop.at ?? promptDefaults.at;
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
  } else if (promptDefaults.maxIterations > 0) {
    maxIterations = promptDefaults.maxIterations;
  }

  // triggers list: from dialog result, then prompt defaults. Also accept a
  // scalar `trigger` for backward-compat with the pre-r6j dialog shape.
  let triggers;
  if (Array.isArray(loop.triggers) && loop.triggers.length > 0) {
    triggers = loop.triggers;
  } else if (typeof loop.trigger === "string" && loop.trigger.length > 0) {
    triggers = [loop.trigger];
  } else {
    triggers = promptDefaults.triggers;
  }

  const delaySeconds = loop.delaySeconds ?? promptDefaults.delay;
  const maxDurationSeconds =
    loop.maxDurationSeconds ??
    parseDurationToSeconds(prompt?.loop?.maxDuration);
  // onTasks CEL condition: from the dialog result, then the prompt's loop
  // frontmatter default. conditionPreset is intentionally NOT threaded
  // here (mitto-pei).
  const condition = loop.condition ?? promptDefaults.condition;

  // Frontmatter-driven booleans forwarded verbatim (mitto-le4.3). Precedence:
  // dialog result → prompt.loop default → omit from body. `??` correctly keeps
  // an explicit `false` from the dialog rather than falling through to the
  // prompt default. Only real booleans reach the wire — undefined/null are
  // omitted so we do not serialize accidental `null`s.
  const freshContext = loop.freshContext ?? promptDefaults.freshContext;
  const runOnStart = loop.runOnStart ?? promptDefaults.runOnStart;
  const coalesceDuringBusy =
    loop.coalesceDuringBusy ?? promptDefaults.coalesceDuringBusy;

  const armsOnTasks = triggers.includes("onTasks");
  const loopBody = {
    prompt_name: prompt?.name,
    frequency,
    enabled: true,
    max_iterations: maxIterations,
    triggers,
    delay_seconds: delaySeconds,
    max_duration_seconds: maxDurationSeconds,
    ...(armsOnTasks ? { condition } : {}),
    ...(typeof freshContext === "boolean"
      ? { fresh_context: freshContext }
      : {}),
    ...(typeof runOnStart === "boolean" ? { run_on_start: runOnStart } : {}),
    ...(typeof coalesceDuringBusy === "boolean"
      ? { coalesce_during_busy: coalesceDuringBusy }
      : {}),
    ...(args && typeof args === "object" && Object.keys(args).length > 0
      ? { arguments: args }
      : {}),
  };

  if (fetchImpl) {
    try {
      const resp = await fetchImpl(endpoints.sessions.loop(sessionId), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(loopBody),
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

  try {
    await getSdkClient().sessions.loop.set(sessionId, loopBody);
    return { success: true };
  } catch (err) {
    console.error("configureLoopSchedule error:", err);
    if (errorStatus(err) === undefined) {
      return { success: false, error: "loop_setup_failed" };
    }
    return { success: false, error: err.body?.error || "loop_setup_failed" };
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
