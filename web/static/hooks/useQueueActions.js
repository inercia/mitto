// web/static/hooks/useQueueActions.js
// Manages the message-queue dropdown for the App: open/close/toggle, add/delete/move
// queued messages, the badge-pulse animation, auto-close timer after adding, and the
// auto-hide effects (when a dialog opens, the sidebar expands, or a queue_updated
// event arrives). Queue data operations (fetch/add/delete/move) are provided by the
// WebSocket layer and passed in; all queue UI state and bookkeeping live here.
const { useState, useRef, useEffect, useCallback } = window.preact;

/**
 * Message-queue dropdown actions/state hook.
 *
 * @param {Object} deps
 * @param {string|null} deps.activeSessionId - Focused conversation id.
 * @param {Function} deps.showToast - Toast dispatcher.
 * @param {Function} deps.updateDraft - Clears/sets a session's draft text.
 * @param {Function} deps.fetchQueueMessages - Refreshes queue contents.
 * @param {Function} deps.addToQueue - Enqueues a message (text/images/files/promptName).
 * @param {Function} deps.deleteQueueMessage - Removes a queued message by id.
 * @param {Function} deps.moveQueueMessage - Reorders a queued message.
 * @param {boolean} deps.settingsDialogOpen - Whether the settings dialog is open.
 * @param {boolean} deps.workspacesDialogOpen - Whether the workspaces dialog is open.
 * @param {boolean} deps.showSidebar - Whether the sidebar is expanded.
 */
export function useQueueActions({
  activeSessionId,
  showToast,
  updateDraft,
  fetchQueueMessages,
  addToQueue,
  deleteQueueMessage,
  moveQueueMessage,
  settingsDialogOpen,
  workspacesDialogOpen,
  showSidebar,
}) {
  const [showQueueDropdown, setShowQueueDropdown] = useState(false);
  const [isDeletingQueueMessage, setIsDeletingQueueMessage] = useState(false);
  const [isMovingQueueMessage, setIsMovingQueueMessage] = useState(false);
  const [isAddingToQueue, setIsAddingToQueue] = useState(false);
  const [queueBadgePulse, setQueueBadgePulse] = useState(false);

  // Ref to track queue panel auto-close timer after adding
  const queuePanelAutoCloseTimerRef = useRef(null);

  // Queue dropdown handlers
  const handleToggleQueueDropdown = useCallback(() => {
    // Cancel any auto-close timer when user manually toggles
    if (queuePanelAutoCloseTimerRef.current) {
      clearTimeout(queuePanelAutoCloseTimerRef.current);
      queuePanelAutoCloseTimerRef.current = null;
    }
    if (!showQueueDropdown) {
      // Opening - fetch latest queue messages
      fetchQueueMessages();
    }
    setShowQueueDropdown((prev) => !prev);
  }, [showQueueDropdown, fetchQueueMessages]);

  const handleCloseQueueDropdown = useCallback(() => {
    // Cancel any auto-close timer when closing
    if (queuePanelAutoCloseTimerRef.current) {
      clearTimeout(queuePanelAutoCloseTimerRef.current);
      queuePanelAutoCloseTimerRef.current = null;
    }
    setShowQueueDropdown(false);
  }, []);

  const handleDeleteQueueMessage = useCallback(
    async (messageId) => {
      setIsDeletingQueueMessage(true);
      try {
        await deleteQueueMessage(messageId);
      } finally {
        setIsDeletingQueueMessage(false);
      }
    },
    [deleteQueueMessage],
  );

  const handleMoveQueueMessage = useCallback(
    async (messageId, direction) => {
      setIsMovingQueueMessage(true);
      try {
        await moveQueueMessage(messageId, direction);
      } finally {
        setIsMovingQueueMessage(false);
      }
    },
    [moveQueueMessage],
  );

  // Handle adding message to queue (with optional images, files, and opts)
  // Called from ChatInput with message text, images, files, and optional opts (e.g. { promptName })
  const handleAddToQueue = useCallback(
    async (message, images = [], files = [], opts = {}) => {
      // Allow queueing if there's text OR images OR files OR a named prompt
      const hasContent =
        message?.trim() ||
        images.length > 0 ||
        files.length > 0 ||
        opts?.promptName;
      if (!hasContent || isAddingToQueue) return { success: false };

      setIsAddingToQueue(true);
      try {
        // Extract image and file IDs from the objects
        const imageIds = images.map((img) => img.id).filter(Boolean);
        const fileIds = files.map((f) => f.id).filter(Boolean);
        const result = await addToQueue(message, imageIds, fileIds, opts);
        if (result.success) {
          // Clear the draft after successful addition
          // Note: Images are cleared by ChatInput on success
          updateDraft(activeSessionId, "");

          // Show queue toast feedback
          showToast({
            style: "info",
            title: "Message queued",
            duration: 2000,
            dismissable: false,
          });

          // Trigger badge pulse animation
          setQueueBadgePulse(true);
          setTimeout(() => setQueueBadgePulse(false), 600);

          // Open queue panel briefly to show the new message
          fetchQueueMessages();
          setShowQueueDropdown(true);

          // Clear any existing auto-close timer
          if (queuePanelAutoCloseTimerRef.current) {
            clearTimeout(queuePanelAutoCloseTimerRef.current);
          }

          // Auto-close the queue panel after 1.5 seconds
          queuePanelAutoCloseTimerRef.current = setTimeout(() => {
            setShowQueueDropdown(false);
            queuePanelAutoCloseTimerRef.current = null;
          }, 1500);

          return { success: true };
        }
        return { success: false, error: result.error };
      } finally {
        setIsAddingToQueue(false);
      }
    },
    [
      isAddingToQueue,
      addToQueue,
      updateDraft,
      activeSessionId,
      fetchQueueMessages,
      showToast,
    ],
  );

  // Auto-hide queue dropdown when certain events occur
  useEffect(() => {
    if (!showQueueDropdown) return;

    // Close when settings or workspaces dialog opens
    if (settingsDialogOpen || workspacesDialogOpen) {
      setShowQueueDropdown(false);
    }
  }, [showQueueDropdown, settingsDialogOpen, workspacesDialogOpen]);

  // Close queue dropdown when sidebar expands (on mobile)
  useEffect(() => {
    if (showQueueDropdown && showSidebar) {
      setShowQueueDropdown(false);
    }
  }, [showQueueDropdown, showSidebar]);

  // Listen for queue updates from WebSocket to refresh the dropdown
  useEffect(() => {
    const handleQueueUpdate = () => {
      if (showQueueDropdown) {
        fetchQueueMessages();
      }
    };
    window.addEventListener("mitto:queue_updated", handleQueueUpdate);
    return () => {
      window.removeEventListener("mitto:queue_updated", handleQueueUpdate);
    };
  }, [showQueueDropdown, fetchQueueMessages]);

  return {
    showQueueDropdown,
    isDeletingQueueMessage,
    isMovingQueueMessage,
    isAddingToQueue,
    queueBadgePulse,
    handleToggleQueueDropdown,
    handleCloseQueueDropdown,
    handleDeleteQueueMessage,
    handleMoveQueueMessage,
    handleAddToQueue,
  };
}
