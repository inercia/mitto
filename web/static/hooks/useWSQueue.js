// =============================================================================
// Mitto Web Interface — WebSocket Queue sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Owns the REST callbacks (fetch/delete/add/move); the queue data itself
// (messages/length/config) lives in stores/queueStore.js (mitto-sus.11) so
// consumers subscribe directly instead of receiving it prop-drilled through
// useWebSocket -> App. Takes activeSessionId as a parameter so it stays
// reactive to the parent's active-session state.
// =============================================================================

const { useEffect, useCallback, useRef } = window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus } from "../utils/sdkErrors.js";
import { setMessages, setLength } from "../stores/queueStore.js";

export function useWSQueue(activeSessionId, options = {}) {
  const { onSessionMissing } = options;
  const selectionRef = useRef({ id: activeSessionId, generation: 0 });
  if (selectionRef.current.id !== activeSessionId) {
    selectionRef.current = {
      id: activeSessionId,
      generation: selectionRef.current.generation + 1,
    };
  }
  const terminalIdsRef = useRef(new Set());
  const activeReadRef = useRef(null);

  const invalidateQueueSession = useCallback((sessionId) => {
    if (!sessionId) return;
    terminalIdsRef.current.add(sessionId);
    const activeRead = activeReadRef.current;
    if (activeRead?.id === sessionId) {
      activeReadRef.current = null;
      activeRead.controller.abort();
    }
    setMessages(sessionId, []);
    setLength(sessionId, 0);
  }, []);

  // Fetch queue messages for the active session
  const fetchQueueMessages = useCallback(async () => {
    const sessionId = activeSessionId;
    const selection = selectionRef.current;
    if (
      !sessionId ||
      selection.id !== sessionId ||
      terminalIdsRef.current.has(sessionId)
    ) return;

    if (activeReadRef.current) {
      activeReadRef.current.controller.abort();
      activeReadRef.current = null;
    }
    const controller = new AbortController();
    const request = {
      id: sessionId,
      generation: selection.generation,
      controller,
    };
    activeReadRef.current = request;
    const isCurrent = () =>
      activeReadRef.current === request &&
      selectionRef.current.id === sessionId &&
      selectionRef.current.generation === request.generation &&
      !terminalIdsRef.current.has(sessionId) &&
      !controller.signal.aborted;

    try {
      const data = await getSdkClient().sessions.queue.list(sessionId, {
        signal: controller.signal,
      });
      if (!isCurrent()) return;
      setMessages(sessionId, data.messages || []);
      setLength(sessionId, data.count || 0);
    } catch (err) {
      if (!isCurrent()) return;
      if (errorStatus(err) === 404) {
        invalidateQueueSession(sessionId);
        onSessionMissing?.(sessionId);
        return;
      }
      console.error("Failed to fetch queue messages:", err);
    } finally {
      if (activeReadRef.current === request) activeReadRef.current = null;
    }
  }, [activeSessionId, invalidateQueueSession, onSessionMissing]);

  // Fetch queue messages when active session changes. No explicit clear is
  // needed for a switched-away session: hooks/useQueue.js's readers return
  // []/0/default for a falsy sessionId regardless of what the store holds.
  useEffect(() => {
    if (activeSessionId) {
      fetchQueueMessages();
    }
  }, [activeSessionId, fetchQueueMessages]);

  useEffect(() => {
    const selection = selectionRef.current;
    return () => {
      if (activeReadRef.current?.generation === selection.generation) {
        activeReadRef.current.controller.abort();
        activeReadRef.current = null;
      }
    };
  }, [activeSessionId]);

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
        const selection = selectionRef.current;
        const data = await getSdkClient().sessions.queue.move(
          activeSessionId,
          messageId,
          direction,
        );
        if (
          selectionRef.current.id === activeSessionId &&
          selectionRef.current.generation === selection.generation &&
          !terminalIdsRef.current.has(activeSessionId)
        ) {
          setMessages(activeSessionId, data.messages || []);
          setLength(activeSessionId, data.count || 0);
        }
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
    invalidateQueueSession,
    deleteQueueMessage,
    addToQueue,
    moveQueueMessage,
  };
}
