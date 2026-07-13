// =============================================================================
// Mitto Web Interface — WebSocket SessionSelectors sub-hook
// Extracted from useWebSocket.js (mitto-90f.5). Groups the pure `activeSession`-
// derived useMemo selectors: messages, sessionInfo, isStreaming, agentWorking,
// isRunning, hasMoreMessages, isLoadingMore, hasReachedLimit. All selectors
// depend only on `activeSession`; no other coupling.
// =============================================================================

const { useMemo } = window.preact;

import { MAX_MESSAGES } from "../lib.js";

export function useWSSessionSelectors(activeSession) {
  // Get current session's messages
  const messages = useMemo(() => {
    return activeSession?.messages || [];
  }, [activeSession]);

  // Get current session info (enhanced with message count)
  const sessionInfo = useMemo(() => {
    if (!activeSession) return null;
    const info = activeSession.info || {};
    // Include message count from the messages array
    return {
      ...info,
      messageCount: activeSession.messages?.length || 0,
    };
  }, [activeSession]);

  // Get streaming state for active session
  const isStreaming = useMemo(() => {
    return activeSession?.isStreaming || false;
  }, [activeSession]);

  // Get "agent is still working" heartbeat state for active session (transient,
  // set by the agent_working WS message, cleared on prompt_complete).
  const agentWorking = useMemo(() => {
    return activeSession?.agentWorking || null;
  }, [activeSession]);

  // Check if the ACP agent is running for the active session.
  // When false, the session exists but the agent process hasn't started yet
  // (e.g., during resume). Prompts should be blocked until acp_started arrives.
  const isRunning = useMemo(() => {
    return activeSession?.isRunning ?? false;
  }, [activeSession]);

  // Check if active session has more messages to load
  const hasMoreMessages = useMemo(() => {
    return activeSession?.hasMoreMessages || false;
  }, [activeSession]);

  // Check if active session is currently loading more messages
  const isLoadingMore = useMemo(() => {
    return activeSession?.isLoadingMore || false;
  }, [activeSession]);

  // Check if active session has reached the message limit
  // When true, we've loaded MAX_MESSAGES and can't load more to protect memory
  const hasReachedLimit = useMemo(() => {
    const messageCount = activeSession?.messages?.length || 0;
    return messageCount >= MAX_MESSAGES;
  }, [activeSession]);

  return {
    messages,
    sessionInfo,
    isStreaming,
    agentWorking,
    isRunning,
    hasMoreMessages,
    isLoadingMore,
    hasReachedLimit,
  };
}
