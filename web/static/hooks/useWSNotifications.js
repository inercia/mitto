// =============================================================================
// Mitto Web Interface — WebSocket Notifications sub-hook
// Extracted from useWebSocket.js (mitto-90f.5). Bridges background/toast
// notification events (loop-started, background UI prompt, background UI
// prompt timeout, background completion) to the module-level
// stores/notificationsStore.js (mitto-sus.11) instead of App-owned
// useState, so setting one of these no longer re-renders the whole App
// subtree -- see stores/notificationsStore.js and its subscriber,
// hooks/useBackgroundNotifications.js. The setter/clear function names are
// unchanged so useWebSocket.js's existing call sites need no edits.
// =============================================================================

import {
  setBackgroundCompletion,
  setLoopStarted,
  setBackgroundUIPrompt,
  setBackgroundUIPromptTimeout,
} from "../stores/notificationsStore.js";

// Clearing happens in hooks/useBackgroundNotifications.js, which imports the
// clear*/subscribe* functions directly from the store -- only the setters
// (called from useWebSocket.js's WS message handlers) are needed here.
export function useWSNotifications() {
  return {
    setBackgroundCompletion,
    setLoopStarted,
    setBackgroundUIPrompt,
    setBackgroundUIPromptTimeout,
  };
}
