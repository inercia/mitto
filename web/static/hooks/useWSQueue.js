// =============================================================================
// Mitto Web Interface — WebSocket Queue sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns queueLength/queueMessages/queueConfig state and REST callbacks
// (fetch/delete/add/move). Takes activeSessionId as a parameter so it stays
// reactive to the parent's active-session state.
// =============================================================================

const { useState, useEffect, useCallback } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus } from "../utils/sdkErrors.js";

export function useWSQueue(activeSessionId) {
  // Queue length for the active session
  const [queueLength, setQueueLength] = useState(0);

  // Queue messages for the active session
  // Array of { id, message, title, queued_at }
  const [queueMessages, setQueueMessages] = useState([]);

  // Queue configuration for the active session
  // { enabled: bool, max_size: int, delay_seconds: int }
  const [queueConfig, setQueueConfig] = useState({
    enabled: true,
    max_size: 10,
    delay_seconds: 0,
  });

  // Fetch queue messages for the active session
  const fetchQueueMessages = useCallback(async () => {
    if (!activeSessionId) {
      setQueueMessages([]);
      return;
    }
    try {
      const data = await getSdkClient().sessions.queue.list(activeSessionId);
      setQueueMessages(data.messages || []);
      setQueueLength(data.count || 0);
    } catch (err) {
      console.error("Failed to fetch queue messages:", err);
    }
  }, [activeSessionId]);

  // Fetch queue messages when active session changes
  useEffect(() => {
    if (activeSessionId) {
      fetchQueueMessages();
    } else {
      // Clear queue state when no session is active
      setQueueMessages([]);
      setQueueLength(0);
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
        setQueueMessages(data.messages || []);
        setQueueLength(data.count || 0);
        return true;
      } catch (err) {
        console.error("Failed to move queue message:", err);
        return false;
      }
    },
    [activeSessionId],
  );

  return {
    queueLength,
    queueMessages,
    queueConfig,
    setQueueLength,
    setQueueMessages,
    setQueueConfig,
    fetchQueueMessages,
    deleteQueueMessage,
    addToQueue,
    moveQueueMessage,
  };
}
