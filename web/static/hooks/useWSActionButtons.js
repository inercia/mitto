// =============================================================================
// Mitto Web Interface — WebSocket ActionButtons sub-hook
// Extracted from useWebSocket.js (mitto-90f.5). Groups the derived
// `actionButtons` array for the active session (stable-by-reference across
// streaming updates) and the debug-log effect that fires when the button set
// changes. Reads sessions[activeSessionId]?.actionButtons directly to preserve
// the existing "stable reference across streaming" behavior (setSessions()
// spreads copy the actionButtons array by reference).
// =============================================================================

const { useMemo, useEffect } = window.preact;

export function useWSActionButtons(sessions, activeSessionId) {
  // Extract action buttons reference — stable across streaming updates.
  // During streaming, setSessions() spreads the session object which copies
  // actionButtons by reference, so this stays the same array instance until
  // buttons are actually set or cleared.
  const sessionActionButtons = sessions[activeSessionId]?.actionButtons;

  // Get action buttons for active session
  const actionButtons = useMemo(() => {
    if (!activeSessionId || !sessionActionButtons) {
      return [];
    }
    return sessionActionButtons;
  }, [sessionActionButtons, activeSessionId]);

  // Log when action buttons actually change (not inside useMemo)
  useEffect(() => {
    if (actionButtons.length > 0) {
      console.log("[ActionButtons] Buttons updated:", {
        sessionId: activeSessionId,
        buttonCount: actionButtons.length,
        buttons: actionButtons.map((b) => b.label),
      });
    }
  }, [actionButtons, activeSessionId]);

  return { actionButtons };
}
