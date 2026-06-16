// Mitto Web Interface - Conversation Seeding Hook
// Shared helper to seed a conversation with a named prompt via prompt_name.

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
    async ({ workingDir, acpServer, name, beadsIssue, prompt, arguments: args }) => {
      const result = await newSession({
        workingDir,
        acpServer,
        name,
        beadsIssue,
        initialPromptName: prompt?.name,
        arguments: args,
      });
      if (!result?.sessionId) {
        return { error: result?.error || "session_creation_failed" };
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
