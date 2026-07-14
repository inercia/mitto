// =============================================================================
// Mitto Web Interface — WebSocket Notifications sub-hook
// Extracted from useWebSocket.js (mitto-90f.5).
// Groups background/toast notification state: loop-started, background UI
// prompt, background UI prompt timeout. Setters are exposed so ws message
// handlers in the parent hook can populate them.
// =============================================================================

const { useState, useCallback } = window.preact;

export function useWSNotifications() {
  // Track background session completions for toast notifications
  // { sessionId, sessionName, timestamp }
  const [backgroundCompletion, setBackgroundCompletion] = useState(null);

  // Track loop session starts for toast notifications
  // { sessionId, sessionName, timestamp }
  const [loopStarted, setLoopStarted] = useState(null);

  // Track background UI prompts for toast notifications
  // { sessionId, sessionName, question, timestamp }
  const [backgroundUIPrompt, setBackgroundUIPrompt] = useState(null);

  // Track background UI prompt timeouts for native OS notifications
  // Fired when a blocking prompt in a background session times out with no active viewer.
  // { sessionId, sessionName, question, timestamp }
  const [backgroundUIPromptTimeout, setBackgroundUIPromptTimeout] =
    useState(null);

  // Clear background completion notification
  const clearBackgroundCompletion = useCallback(() => {
    setBackgroundCompletion(null);
  }, []);

  // Clear loop started notification
  const clearLoopStarted = useCallback(() => {
    setLoopStarted(null);
  }, []);

  // Clear background UI prompt notification
  const clearBackgroundUIPrompt = useCallback(() => {
    setBackgroundUIPrompt(null);
  }, []);

  // Clear background UI prompt timeout notification
  const clearBackgroundUIPromptTimeout = useCallback(() => {
    setBackgroundUIPromptTimeout(null);
  }, []);

  return {
    backgroundCompletion,
    setBackgroundCompletion,
    clearBackgroundCompletion,
    loopStarted,
    setLoopStarted,
    clearLoopStarted,
    backgroundUIPrompt,
    setBackgroundUIPrompt,
    clearBackgroundUIPrompt,
    backgroundUIPromptTimeout,
    setBackgroundUIPromptTimeout,
    clearBackgroundUIPromptTimeout,
  };
}
