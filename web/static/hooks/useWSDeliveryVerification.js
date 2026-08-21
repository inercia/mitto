// Mitto Web Interface - useWSDeliveryVerification hook (mitto-90f.6.3)
// Owns the send-side delivery verification cluster (C2) extracted from
// useWebSocket.js: sendPrompt with its nested attemptSend and
// verifyDeliveryAfterReconnect budget loop, cancelPrompt, forceReset,
// retryPendingPrompts, and resolvePendingSendsForSession.
//
// The composer (useWebSocket) still owns the two mutable buckets these
// helpers read/write — pendingSendsRef and lastConfirmedPromptRef — because
// handleSessionMessage (in the composer) also reads/writes them at ~10 sites
// across the connected/prompt_received/user_prompt/error branches. Both refs
// are passed in as props; the sub-hook never allocates its own.
//
// C1 (useWSConnection) transport primitives (sendToSession,
// waitForSessionConnection, isConnectionHealthy, sessionWsRefs) and C3
// (useWSMobileResilience) tunable (isMobileDevice) are also passed in as
// props. See rule 21-web-frontend-state (activeSessionIdRef closures),
// rule 22-web-frontend-websocket (sessionWsRefs read discipline), and
// rule 24-web-frontend-sync (dedup stays in the composer's
// handleSessionMessage — no receive-side logic in this hook).

const { useCallback } = window.preact;

import {
  ROLE_USER,
  ROLE_AGENT,
  ROLE_THOUGHT,
  ROLE_ERROR,
  generatePromptId,
  savePendingPrompt,
  removePendingPrompt,
  getPendingPromptsForSession,
  limitMessages,
} from "../lib.js";

/**
 * useWSDeliveryVerification
 *
 * @param {object} props
 * @param {string|null} props.activeSessionId
 * @param {Function} props.addMessageToSession
 * @param {Function} props.updateLastMessage
 * @param {Function} props.clearActionButtons
 * @param {Function} props.setSessions
 * @param {Function} props.sendToSession — (sessionId, msg) => boolean
 * @param {Function} props.waitForSessionConnection — (sessionId, timeout?) => Promise<import("../sdk/index.js").SessionStream>
 * @param {Function} props.isConnectionHealthy — (sessionId) => boolean
 * @param {{ current: Object<string, import("../sdk/index.js").SessionStream> }} props.sessionWsRefs — from C1 (mitto-7gta.30)
 * @param {boolean} props.isMobileDevice — from C3, tunes INITIAL_ACK_TIMEOUT_MS
 * @param {{ current: Object<string, { resolve, reject, timeoutId }> }} props.pendingSendsRef
 * @param {{ current: Object<string, { promptId: string, seq: number }> }} props.lastConfirmedPromptRef
 * @returns {{
 *   sendPrompt: Function,
 *   cancelPrompt: Function,
 *   forceReset: Function,
 *   retryPendingPrompts: Function,
 *   rejectOversizedPromptsForSession: Function,
 *   resolvePendingSendsForSession: Function
 * }}
 */
export function useWSDeliveryVerification({
  activeSessionId,
  addMessageToSession,
  updateLastMessage,
  clearActionButtons,
  setSessions,
  sendToSession,
  waitForSessionConnection,
  isConnectionHealthy,
  sessionWsRefs,
  isMobileDevice,
  pendingSendsRef,
  lastConfirmedPromptRef,
}) {
  // Timeout configuration for message delivery with automatic retry
  // Total budget: 10 seconds - user can wait this long for message delivery
  const TOTAL_DELIVERY_BUDGET_MS = 10000;
  // Initial ACK timeout: short to quickly detect zombie connections
  // Mobile gets slightly longer due to network variability.
  const INITIAL_ACK_TIMEOUT_MS = isMobileDevice ? 4000 : 3000;
  // Timeout for reconnection during retry
  const RECONNECT_TIMEOUT_MS = 4000;

  /**
   * Resolve all pending sends for a session as successful.
   * Called when we receive agent response, which proves the prompt was received.
   * @param {string} sessionId - The session ID
   */
  const resolvePendingSendsForSession = useCallback((sessionId) => {
    // Find all pending sends for this session and resolve them
    for (const [promptId, pending] of Object.entries(pendingSendsRef.current)) {
      // We don't track sessionId in pendingSendsRef, but we can check localStorage
      // For simplicity, resolve all pending sends when agent responds
      // (there should typically only be one pending send at a time)
      if (pending) {
        console.log(
          `Resolving pending send ${promptId} - agent response received`,
        );
        clearTimeout(pending.timeoutId);
        pending.resolve({ success: true, promptId });
        delete pendingSendsRef.current[promptId];
        removePendingPrompt(promptId);
      }
    }
  }, []);

  // Close code 1009 means the server rejected the frame before it could parse
  // prompt_id. Quarantine every pending prompt for this session so reconnect
  // recovery cannot replay the same poison frame indefinitely.
  const rejectOversizedPromptsForSession = useCallback((sessionId) => {
    const message =
      "Message is too large to send. Shorten it or attach the content as a file.";
    const pending = getPendingPromptsForSession(sessionId);
    for (const { promptId } of pending) {
      removePendingPrompt(promptId);
      const inFlight = pendingSendsRef.current[promptId];
      if (inFlight) {
        clearTimeout(inFlight.timeoutId);
        inFlight.reject(new Error(message));
        delete pendingSendsRef.current[promptId];
      }
    }
    setSessions((prev) => {
      const session = prev[sessionId];
      if (!session) return prev;
      const messages = limitMessages([
        ...session.messages,
        { role: ROLE_ERROR, text: message, timestamp: Date.now() },
      ]);
      return {
        ...prev,
        [sessionId]: { ...session, messages, isStreaming: false },
      };
    });
  }, []);

  /**
   * Send a prompt to the active session.
   * Returns a Promise that resolves on ACK or rejects on timeout/failure.
   * If WebSocket is not connected or unhealthy, automatically triggers reconnection and waits.
   * @param {string} message - The message text
   * @param {Array} images - Optional array of images
   * @param {Array} files - Optional array of files
   * @param {Object} options - Optional settings: { timeout: number, skipMessageAdd: boolean }
   * @returns {Promise<{success: boolean, promptId: string}>}
   */
  const sendPrompt = useCallback(
    async (message, images = [], files = [], options = {}) => {
      const startTime = Date.now();

      if (!activeSessionId) {
        throw new Error("No active session");
      }

      // Check if the session stream is connected and healthy
      let ws = sessionWsRefs.current[activeSessionId];
      const isHealthy = isConnectionHealthy(activeSessionId) ?? true;
      const needsReconnect = !ws || ws.state !== "open" || !isHealthy;

      if (needsReconnect) {
        console.log(
          `Connection needs reconnect before sending (ws=${!!ws}, state=${ws?.state}, healthy=${isHealthy})`,
        );
        // Force close any existing zombie connection
        if (ws) {
          delete sessionWsRefs.current[activeSessionId];
          ws.close();
        }
        // Wait for fresh connection
        ws = await waitForSessionConnection(activeSessionId);
      }

      // Clear any existing action buttons when sending a new prompt
      clearActionButtons(activeSessionId);


      // Add user message with optional images and files (unless skipped for retry)
      if (!options.skipMessageAdd) {
        const userMessage = {
          role: ROLE_USER,
          text: message,
          timestamp: Date.now(),
          promptName: options.promptName || undefined,
        };
        if (images.length > 0) {
          userMessage.images = images; // Array of { id, url, name, mimeType }
        }
        if (files.length > 0) {
          userMessage.files = files; // Array of { id, name, mimeType, size, category }
        }
        addMessageToSession(activeSessionId, userMessage);
        // Mark any previous streaming message as complete
        updateLastMessage(activeSessionId, (m) =>
          !m.complete && (m.role === ROLE_AGENT || m.role === ROLE_THOUGHT)
            ? { ...m, complete: true }
            : m,
        );
      }

      // Generate a unique prompt ID for delivery tracking
      const promptId = generatePromptId();
      const imageIds = images.map((img) => img.id);
      const fileIds = files.map((f) => f.id);

      // Save to pending queue BEFORE sending (for mobile reliability)
      savePendingPrompt(activeSessionId, promptId, message, imageIds, fileIds);

      /**
       * Helper to attempt sending and wait for ACK with timeout.
       * Returns: { success: true, promptId } on ACK, or throws on timeout/failure.
       */
      const attemptSend = (ackTimeout) => {
        return new Promise((resolve, reject) => {
          const timeoutId = setTimeout(() => {
            const pending = pendingSendsRef.current[promptId];
            if (!pending) return; // Already resolved
            delete pendingSendsRef.current[promptId];
            reject(new Error("ACK_TIMEOUT"));
          }, ackTimeout);

          // Track the pending send
          pendingSendsRef.current[promptId] = { resolve, reject, timeoutId };

          // Send prompt with prompt_id for acknowledgment
          const sent = sendToSession(activeSessionId, {
            type: "prompt",
            data: {
              message,
              prompt_name: options.promptName || undefined,
              image_ids: imageIds,
              file_ids: fileIds,
              prompt_id: promptId,
            },
          });

          if (!sent) {
            // WebSocket send failed immediately
            clearTimeout(timeoutId);
            delete pendingSendsRef.current[promptId];
            reject(new Error("Failed to send message"));
          }
        });
      };

      /**
       * Helper to force reconnect and verify if the prompt was delivered.
       * Returns: true if delivered, false if not delivered.
       * Throws on reconnection failure.
       */
      const verifyDeliveryAfterReconnect = async (reconnectTimeout) => {
        console.log(
          `Forcing reconnect to verify delivery of prompt ${promptId}`,
        );

        // Force close the potentially zombie connection
        const currentWs = sessionWsRefs.current[activeSessionId];
        if (currentWs) {
          delete sessionWsRefs.current[activeSessionId];
          currentWs.close();
        }

        // Wait for fresh connection - this will receive the connected message
        // which includes last_user_prompt_id for delivery verification
        await waitForSessionConnection(activeSessionId, reconnectTimeout);

        // Small delay to ensure the connected message handler has run
        await new Promise((r) => setTimeout(r, 100));

        // Check if our prompt was the last one delivered
        const confirmed = lastConfirmedPromptRef.current[activeSessionId];
        if (confirmed && confirmed.promptId === promptId) {
          console.log(
            `Prompt ${promptId} was confirmed delivered after reconnect`,
          );
          return true;
        }

        console.log(
          `Prompt ${promptId} was NOT delivered (last confirmed: ${confirmed?.promptId})`,
        );
        return false;
      };

      // Main delivery logic with retry
      try {
        // First attempt with short ACK timeout
        const result = await attemptSend(INITIAL_ACK_TIMEOUT_MS);
        removePendingPrompt(promptId);
        return result;
      } catch (err) {
        if (err.message !== "ACK_TIMEOUT") {
          // Non-timeout error (e.g., send failed) - don't retry
          throw err;
        }

        // ACK timeout - reconnect and verify/retry
        const elapsed = Date.now() - startTime;
        const remainingBudget = TOTAL_DELIVERY_BUDGET_MS - elapsed;

        if (remainingBudget <= 0) {
          throw new Error(
            "Message delivery timed out. Please check your connection and try again.",
          );
        }

        console.log(
          `ACK timeout after ${elapsed}ms, ${remainingBudget}ms budget remaining`,
        );

        try {
          // Reconnect and check if message was delivered
          const reconnectTimeout = Math.min(
            remainingBudget,
            RECONNECT_TIMEOUT_MS,
          );
          const wasDelivered =
            await verifyDeliveryAfterReconnect(reconnectTimeout);

          if (wasDelivered) {
            removePendingPrompt(promptId);
            return { success: true, promptId, verifiedOnReconnect: true };
          }

          // Message was NOT delivered - retry on fresh connection
          const elapsedAfterReconnect = Date.now() - startTime;
          const retryBudget = TOTAL_DELIVERY_BUDGET_MS - elapsedAfterReconnect;

          if (retryBudget <= 500) {
            // Not enough time for a meaningful retry
            throw new Error(
              "Message delivery could not be confirmed. Please try again.",
            );
          }

          console.log(`Retrying send with ${retryBudget}ms budget`);

          // Retry the send on the fresh connection
          const result = await attemptSend(retryBudget);
          removePendingPrompt(promptId);
          return { ...result, retriedOnReconnect: true };
        } catch (reconnectErr) {
          if (reconnectErr.message === "ACK_TIMEOUT") {
            throw new Error(
              "Message delivery could not be confirmed after retry. Please check your connection.",
            );
          }
          // Reconnection or retry failed
          console.error("Delivery retry failed:", reconnectErr);
          throw new Error(
            "Connection lost and could not reconnect. Please check your network and try again.",
          );
        }
      }
    },
    [
      activeSessionId,
      addMessageToSession,
      updateLastMessage,
      clearActionButtons,
    ],
  );

  const cancelPrompt = useCallback(() => {
    if (!activeSessionId) return;
    sendToSession(activeSessionId, { type: "cancel" });
    // Clear any active UI prompt when user cancels
    setSessions((prev) => {
      const session = prev[activeSessionId];
      if (!session || !session.activeUIPrompt) return prev;
      return {
        ...prev,
        [activeSessionId]: { ...session, activeUIPrompt: null },
      };
    });
  }, [activeSessionId]);

  // Force reset a stuck session (when agent is unresponsive)
  const forceReset = useCallback(() => {
    if (!activeSessionId) return;
    console.log("Force resetting session:", activeSessionId);
    sendToSession(activeSessionId, { type: "force_reset" });
  }, [activeSessionId]);

  // Retry pending prompts for a session (called on reconnect or visibility change)
  const retryPendingPrompts = useCallback((sessionId) => {
    const pending = getPendingPromptsForSession(sessionId);
    if (pending.length === 0) return;

    console.log(
      `Retrying ${pending.length} pending prompt(s) for session ${sessionId}`,
    );

    for (const { promptId, message, imageIds } of pending) {
      const sent = sendToSession(sessionId, {
        type: "prompt",
        data: { message, image_ids: imageIds || [], prompt_id: promptId },
      });
      if (sent) {
        console.log(`Retried pending prompt: ${promptId}`);
      } else {
        console.warn(
          `Failed to retry pending prompt (WebSocket not ready): ${promptId}`,
        );
        // Stop retrying if WebSocket is not ready - will retry on next reconnect
        break;
      }
    }
  }, []);

  return {
    sendPrompt,
    cancelPrompt,
    forceReset,
    retryPendingPrompts,
    rejectOversizedPromptsForSession,
    resolvePendingSendsForSession,
  };
}
