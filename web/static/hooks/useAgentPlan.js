// web/static/hooks/useAgentPlan.js
// Manages the per-conversation Agent Plan panel for the App: stores plan entries
// per session, reacts to mitto:plan_update events, auto-expands the panel for the
// active conversation, erases completed plans after a delay, expires plans after a
// number of follow-up user messages, and exposes panel toggle/close plus a
// per-session cleanup used when a conversation is deleted.
const { useState, useRef, useEffect, useCallback, useMemo } = window.preact;

/**
 * Agent Plan panel state/handlers hook.
 *
 * @param {Object} deps
 * @param {string|null} deps.activeSessionId - Focused conversation id.
 * @returns {{
 *   planEntries: Array,
 *   showPlanPanel: boolean,
 *   planUserPinned: boolean,
 *   handleTogglePlanPanel: Function,
 *   handleClosePlanPanel: Function,
 *   trackUserMessageForPlanExpiration: Function,
 *   clearPlanForSession: Function,
 * }}
 */
export function useAgentPlan({ activeSessionId }) {
  // Agent Plan panel state - per-session plan entries stored as { sessionId: entries[] }
  const [planEntriesMap, setPlanEntriesMap] = useState({});
  const [showPlanPanel, setShowPlanPanel] = useState(false);
  const [planUserPinned, setPlanUserPinned] = useState(false);
  // Plan expiration tracking - per-session: { sessionId: { completedAt: timestamp, messagesAfterCompletion: number } }
  const [planExpirationMap, setPlanExpirationMap] = useState({});
  // Plan completion timer - per-session: { sessionId: timeoutId }
  const planCompletionTimersRef = useRef({});

  // Delay in milliseconds before erasing a completed plan
  const PLAN_COMPLETION_ERASE_DELAY = 5000;

  // Number of user messages after plan completion before auto-expiring (configurable between 3-4)
  const PLAN_EXPIRATION_MESSAGE_THRESHOLD = 3;

  // Computed: get plan entries for active session
  const planEntries = useMemo(() => {
    if (!activeSessionId) return [];
    return planEntriesMap[activeSessionId] || [];
  }, [planEntriesMap, activeSessionId]);

  // Helper function to compare plan entries
  const arePlanEntriesEqual = useCallback((a, b) => {
    if (!a && !b) return true;
    if (!a || !b) return false;
    if (a.length !== b.length) return false;
    // Compare each entry by content, status, and priority
    for (let i = 0; i < a.length; i++) {
      if (
        a[i].content !== b[i].content ||
        a[i].status !== b[i].status ||
        a[i].priority !== b[i].priority
      ) {
        return false;
      }
    }
    return true;
  }, []);

  // Listen for plan updates from WebSocket - store per session in the map
  // When all tasks are completed, erase the plan after a delay
  useEffect(() => {
    const handlePlanUpdate = (event) => {
      const { sessionId, entries } = event.detail;
      if (!sessionId) return;

      // Check if this is a new plan (has entries) or an update to existing
      const hasEntries = entries && entries.length > 0;

      // Get existing entries for comparison
      const existingEntries = planEntriesMap[sessionId] || [];

      // Check if the plan has actually changed
      const hasChanged = !arePlanEntriesEqual(existingEntries, entries || []);

      // If nothing changed, skip all updates
      if (!hasChanged) {
        console.log(
          `[Plan] No changes for session ${sessionId}, skipping update`,
        );
        return;
      }

      // Check if all tasks are completed
      const allCompleted =
        hasEntries && entries.every((e) => e.status === "completed");

      // Cancel any existing completion timer for this session
      if (planCompletionTimersRef.current[sessionId]) {
        clearTimeout(planCompletionTimersRef.current[sessionId]);
        delete planCompletionTimersRef.current[sessionId];
      }

      if (allCompleted) {
        // All tasks completed - update entries to show completion, then schedule erasure
        console.log(
          `[Plan] All tasks completed for session ${sessionId}, scheduling erasure in ${PLAN_COMPLETION_ERASE_DELAY}ms`,
        );

        // Update entries to show completed state
        setPlanEntriesMap((prev) => ({
          ...prev,
          [sessionId]: entries || [],
        }));

        // Remove from expiration tracking if present
        setPlanExpirationMap((prev) => {
          const { [sessionId]: _, ...rest } = prev;
          return rest;
        });

        // Schedule plan erasure after delay
        planCompletionTimersRef.current[sessionId] = setTimeout(() => {
          console.log(`[Plan] Erasing completed plan for session ${sessionId}`);
          delete planCompletionTimersRef.current[sessionId];

          // Close panel first (triggers CSS transition)
          if (sessionId === activeSessionId) {
            setShowPlanPanel(false);
            setPlanUserPinned(false);
          }

          // Wait for panel close animation (300ms transition) before removing entries
          setTimeout(() => {
            setPlanEntriesMap((prevEntries) => {
              const { [sessionId]: _, ...restEntries } = prevEntries;
              return restEntries;
            });
          }, 350); // Slightly longer than 300ms transition to ensure it completes
        }, PLAN_COMPLETION_ERASE_DELAY);

        return;
      }

      // Store plan entries for this session in the map
      setPlanEntriesMap((prev) => ({
        ...prev,
        [sessionId]: entries || [],
      }));

      // Reset expiration tracking when new/updated plan with incomplete tasks is received
      if (hasEntries) {
        setPlanExpirationMap((prev) => {
          const existing = prev[sessionId];
          if (existing) {
            console.log(
              `[Plan] New/updated plan for session ${sessionId}, resetting expiration tracking`,
            );
            const { [sessionId]: _, ...rest } = prev;
            return rest;
          }
          return prev;
        });
      }

      // Auto-expand the panel if this is the active session and not already pinned
      if (sessionId === activeSessionId && !planUserPinned && hasEntries) {
        setShowPlanPanel(true);
      }
    };
    window.addEventListener("mitto:plan_update", handlePlanUpdate);
    return () => {
      window.removeEventListener("mitto:plan_update", handlePlanUpdate);
    };
  }, [activeSessionId, planUserPinned, planEntriesMap, arePlanEntriesEqual]);

  // Reset panel state (but not entries) when switching sessions
  // The entries are preserved in planEntriesMap and will show the badge indicator
  useEffect(() => {
    setShowPlanPanel(false);
    setPlanUserPinned(false);
  }, [activeSessionId]);

  // Plan panel handlers
  const handleTogglePlanPanel = useCallback(() => {
    setShowPlanPanel((prev) => {
      if (!prev) {
        // Opening - mark as user pinned
        setPlanUserPinned(true);
      }
      return !prev;
    });
  }, []);

  const handleClosePlanPanel = useCallback(() => {
    setShowPlanPanel(false);
    setPlanUserPinned(false);
  }, []);

  // Clean up plan entries, expiration tracking, and completion timers for a session
  // (used when a conversation is deleted)
  const clearPlanForSession = useCallback((sessionId) => {
    setPlanEntriesMap((prev) => {
      const { [sessionId]: _, ...rest } = prev;
      return rest;
    });
    setPlanExpirationMap((prev) => {
      const { [sessionId]: _, ...rest } = prev;
      return rest;
    });
    if (planCompletionTimersRef.current[sessionId]) {
      clearTimeout(planCompletionTimersRef.current[sessionId]);
      delete planCompletionTimersRef.current[sessionId];
    }
  }, []);

  // Track user messages for plan expiration - called when user sends a prompt
  const trackUserMessageForPlanExpiration = useCallback(
    (sessionId) => {
      if (!sessionId) return;

      setPlanExpirationMap((prev) => {
        const existing = prev[sessionId];
        if (!existing?.completedAt) {
          // No completed plan being tracked for this session
          return prev;
        }

        const newCount = (existing.messagesAfterCompletion || 0) + 1;
        console.log(
          `[Plan Expiration] User message sent for session ${sessionId}, count: ${newCount}/${PLAN_EXPIRATION_MESSAGE_THRESHOLD}`,
        );

        if (newCount >= PLAN_EXPIRATION_MESSAGE_THRESHOLD) {
          // Threshold reached - expire the plan
          console.log(
            `[Plan Expiration] Threshold reached for session ${sessionId}, expiring plan`,
          );

          // Remove from expiration tracking
          const { [sessionId]: _, ...rest } = prev;

          // Schedule plan removal with graceful animation:
          // 1. Close panel first (triggers CSS transition)
          // 2. Wait for transition to complete (300ms)
          // 3. Then remove entries from state
          setTimeout(() => {
            // Close panel if it's showing this session's plan
            if (sessionId === activeSessionId) {
              setShowPlanPanel(false);
              setPlanUserPinned(false);
            }

            // Wait for panel close animation (300ms transition) before removing entries
            setTimeout(() => {
              setPlanEntriesMap((prevEntries) => {
                const { [sessionId]: __, ...restEntries } = prevEntries;
                return restEntries;
              });
            }, 350); // Slightly longer than 300ms transition to ensure it completes
          }, 0);

          return rest;
        }

        // Update message count
        return {
          ...prev,
          [sessionId]: {
            ...existing,
            messagesAfterCompletion: newCount,
          },
        };
      });
    },
    [activeSessionId, PLAN_EXPIRATION_MESSAGE_THRESHOLD],
  );

  return {
    planEntries,
    showPlanPanel,
    planUserPinned,
    handleTogglePlanPanel,
    handleClosePlanPanel,
    trackUserMessageForPlanExpiration,
    clearPlanForSession,
  };
}
