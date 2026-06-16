// Mitto Web Interface - Conversation Seeding Hook
// Shared helper to seed a conversation with a named prompt via prompt_name,
// or to create a new periodic conversation driven by a named prompt.

import { secureFetch } from "../utils/csrf.js";
import { apiUrl } from "../utils/api.js";

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
export async function seedConversationWithPrompt(sessionId, prompt, { arguments: args, fetchImpl } = {}) {
  if (!sessionId || !prompt?.name) {
    return { success: false, error: "invalid_request" };
  }

  const fetch_ = fetchImpl || secureFetch;
  const body = buildSeedQueueBody(prompt, { arguments: args });

  try {
    const resp = await fetch_(apiUrl(`/api/sessions/${sessionId}/queue`), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    let data = {};
    try { data = await resp.json(); } catch (_) {}

    if (resp.ok || resp.status === 201) {
      return { success: true, messageId: data.id };
    }
    return { success: false, error: data.error || "request_failed" };
  } catch (err) {
    console.error("seedConversationWithPrompt error:", err);
    return { success: false, error: "request_failed" };
  }
}

/**
 * Configure a periodic schedule on a newly-created session via PUT.
 * @param {string} sessionId
 * @param {{ name: string }} prompt
 * @param {{ value: number, unit: string, at?: string }} periodic
 * @param {{ fetchImpl?: Function }} [opts]
 * @returns {Promise<{ success: boolean, error?: string }>}
 */
export async function configurePeriodicSchedule(sessionId, prompt, periodic, { fetchImpl } = {}) {
  const { value, unit, at } = periodic;
  const frequency = { value, unit };
  // Only include 'at' for daily schedules (matches backend Frequency.Validate() rules)
  if (unit === "days" && at) {
    frequency.at = at;
  }

  const fetch_ = fetchImpl || secureFetch;
  try {
    const resp = await fetch_(apiUrl(`/api/sessions/${sessionId}/periodic`), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        prompt_name: prompt?.name,
        frequency,
        enabled: true,
      }),
    });

    if (resp.ok) {
      return { success: true };
    }
    let errData = {};
    try { errData = await resp.json(); } catch (_) {}
    return { success: false, error: errData.error || "periodic_setup_failed" };
  } catch (err) {
    console.error("configurePeriodicSchedule error:", err);
    return { success: false, error: "periodic_setup_failed" };
  }
}

/**
 * Hook providing two entry points for conversation seeding.
 * @param {{ newSession: Function }} deps
 */
export function useConversationSeeding({ newSession }) {
  const { useCallback } = window.preact;
  const seedExisting = useCallback(
    (sessionId, prompt, opts) => seedConversationWithPrompt(sessionId, prompt, opts),
    [],
  );

  const startConversationWithPrompt = useCallback(
    /**
     * Create a new conversation seeded with a named prompt (one-time queue),
     * or create a new periodic conversation driven by the named prompt.
     *
     * When `periodic` is absent (or falsy): behave exactly as before — the
     * session is created with `initialPromptName` so the queue delivers the
     * prompt as a one-time message.
     *
     * When `periodic` is present: the session is created WITHOUT a queue seed,
     * then `PUT /api/sessions/{id}/periodic` configures the named prompt on the
     * periodic schedule. `at` (if provided) must already be in UTC HH:MM.
     *
     * @param {{ workingDir, acpServer, name, beadsIssue, prompt, arguments, periodic, fetchImpl }} opts
     * @returns {Promise<{ sessionId: string } | { error: string }>}
     */
    async ({ workingDir, acpServer, name, beadsIssue, prompt, arguments: args, periodic, fetchImpl }) => {
      // Build the newSession call — skip the queue seed when periodic is present.
      const sessionOpts = { workingDir, acpServer, name, beadsIssue };
      if (!periodic) {
        // One-time path: pass the named prompt so the queue delivers it once.
        sessionOpts.initialPromptName = prompt?.name;
        sessionOpts.arguments = args;
      }

      const result = await newSession(sessionOpts);
      if (!result?.sessionId) {
        return { error: result?.error || "session_creation_failed" };
      }

      if (periodic) {
        // Periodic path: configure the schedule via PUT after creation.
        const putResult = await configurePeriodicSchedule(
          result.sessionId, prompt, periodic, { fetchImpl },
        );
        if (!putResult.success) {
          // Session was created but periodic config failed — surface the error.
          return { error: putResult.error };
        }
      }

      return { sessionId: result.sessionId };
    },
    [newSession],
  );

  return {
    seedConversationWithPrompt: seedExisting,
    startConversationWithPrompt,
  };
}
