// =============================================================================
// Mitto Web Interface — WebSocket Queue sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns queueLength/queueMessages/queueConfig state and REST callbacks
// (fetch/delete/add/move). Takes activeSessionId as a parameter so it stays
// reactive to the parent's active-session state.
// =============================================================================

const { useState, useEffect, useCallback } = window.preact;

import { secureFetch, authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/index.js";

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
      const response = await authFetch(
        endpoints.sessions.queue(activeSessionId),
      );
      if (response.ok) {
        const data = await response.json();
        setQueueMessages(data.messages || []);
        setQueueLength(data.count || 0);
      }
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
        const response = await secureFetch(
          endpoints.sessions.queueMsg(activeSessionId, messageId),
          { method: "DELETE" },
        );
        if (response.ok || response.status === 204) {
          // Refresh queue messages after deletion
          await fetchQueueMessages();
          return true;
        }
        console.error("Failed to delete queue message:", response.status);
        return false;
      } catch (err) {
        console.error("Failed to delete queue message:", err);
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
        const response = await secureFetch(
          endpoints.sessions.queue(activeSessionId),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          },
        );
        if (response.ok || response.status === 201) {
          // Parse response to get the message ID
          const data = await response.json().catch(() => ({}));
          // Refresh queue messages after addition
          await fetchQueueMessages();
          return { success: true, messageId: data.id };
        }
        // Handle queue full error
        if (response.status === 409) {
          const data = await response.json().catch(() => ({}));
          return {
            success: false,
            error: data.error?.code || data.error || "queue_full",
            message: data.error?.message || data.message,
          };
        }
        console.error("Failed to add to queue:", response.status);
        return { success: false, error: "request_failed" };
      } catch (err) {
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
        const response = await secureFetch(
          endpoints.sessions.queueMove(activeSessionId, messageId),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ direction }),
          },
        );
        if (response.ok) {
          // The response contains the updated queue, update local state
          const data = await response.json();
          setQueueMessages(data.messages || []);
          setQueueLength(data.count || 0);
          return true;
        }
        console.error("Failed to move queue message:", response.status);
        return false;
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
