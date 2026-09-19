// =============================================================================
// Mitto Web Interface — WebSocket Queue sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns the REST callbacks (fetch/delete/add/move); the queue data itself
// (messages/length/config) lives in stores/queueStore.js (mitto-sus.11) so
// consumers subscribe directly instead of receiving it prop-drilled through
// useWebSocket -> App. Takes activeSessionId as a parameter so it stays
// reactive to the parent's active-session state.
// =============================================================================

const { useEffect, useCallback } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus } from "../utils/sdkErrors.js";
import { setMessages, setLength } from "../stores/queueStore.js";

export function useWSQueue(activeSessionId) {
  // Fetch queue messages for the active session
  const fetchQueueMessages = useCallback(async () => {
    if (!activeSessionId) return;
    try {
      const data = await getSdkClient().sessions.queue.list(activeSessionId);
      setMessages(activeSessionId, data.messages || []);
      setLength(activeSessionId, data.count || 0);
    } catch (err) {
      console.error("Failed to fetch queue messages:", err);
    }
  }, [activeSessionId]);

  // Fetch queue messages when active session changes. No explicit clear is
  // needed for a switched-away session: hooks/useQueue.js's readers return
  // []/0/default for a falsy sessionId regardless of what the store holds.
  useEffect(() => {
    if (activeSessionId) {
      fetchQueueMessages();
    }
  }, [activeSessionId, fetchQueueMessages]);

  // Delete a message from the queue
  const deleteQueueMessage = useCallback(
    async (messageId) => {
      if (!activeSessionId || !messageId) return false;
      try {
        await getSdkClient().sessions.queue.remove(activeSessionId, messageId);
        // Refresh queue messages after deletion
        await fetchQueueMessages();
        return true;
      } catch (err) {
        console.error(
          "Failed to delete queue message:",
          errorStatus(err) ?? err,
        );
        return false;
      }
    },
    [activeSessionId, fetchQueueMessages],
  );

  // Add a message to the queue
  const addToQueue = useCallback(
    async (message, imageIds = [], fileIds = [], opts = {}) => {
      const { promptName, arguments: promptArgs } = opts;
      if (!activeSessionId || (!message?.trim() && !promptName))
        return { success: false };
      try {
        const body = {
          message: message?.trim() || "",
          image_ids: imageIds,
          file_ids: fileIds,
        };
        if (promptName) body.prompt_name = promptName;
        // Forward per-parameter values for Go-template `.Args.*` substitution
        // (mitto-gtf). Only sent when non-empty to keep the wire clean on
        // argument-less queue-adds.
        if (promptArgs && Object.keys(promptArgs).length > 0) {
          body.arguments = promptArgs;
        }
        const data = await getSdkClient().sessions.queue.add(
          activeSessionId,
          body,
        );
        // Refresh queue messages after addition
        await fetchQueueMessages();
        return { success: true, messageId: data.id };
      } catch (err) {
        // Handle queue full error
        if (errorStatus(err) === 409) {
          return {
            success: false,
            error: err.code || "queue_full",
            message: err.message,
          };
        }
        console.error("Failed to add to queue:", err);
        return { success: false, error: "request_failed" };
      }
    },
    [activeSessionId, fetchQueueMessages],
  );

  // Move a message up or down in the queue
  const moveQueueMessage = useCallback(
    async (messageId, direction) => {
      if (!activeSessionId || !messageId) return false;
      if (direction !== "up" && direction !== "down") return false;
      try {
        // The response contains the updated queue, update local state
        const data = await getSdkClient().sessions.queue.move(
          activeSessionId,
          messageId,
          direction,
        );
        setMessages(activeSessionId, data.messages || []);
        setLength(activeSessionId, data.count || 0);
        return true;
      } catch (err) {
        console.error("Failed to move queue message:", err);
        return false;
      }
    },
    [activeSessionId],
  );

  return {
    fetchQueueMessages,
    deleteQueueMessage,
    addToQueue,
    moveQueueMessage,
  };
}
