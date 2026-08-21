// Coalesces high-frequency state updates from background conversation streams.
// Active conversations stay immediate; callers can force terminal updates to
// consume queued chunks first so completion/error state remains authoritative.

export const BACKGROUND_SESSION_UPDATE_DELAY_MS = 100;

export function sessionWasStreaming(session, hadPendingContent) {
  return Boolean(session?.isStreaming || hadPendingContent);
}

export function sessionHasLoadedMessages(session, hadPendingContent) {
  return Boolean(session?.messages?.length || hadPendingContent);
}

export function createSessionUpdateScheduler({
  setSessions,
  getActiveSessionId,
  delayMs = BACKGROUND_SESSION_UPDATE_DELAY_MS,
  setTimeoutFn = setTimeout,
  clearTimeoutFn = clearTimeout,
}) {
  let pending = [];
  let timerId = null;

  const clearTimerIfIdle = () => {
    if (pending.length === 0 && timerId !== null) {
      clearTimeoutFn(timerId);
      timerId = null;
    }
  };

  const applyUpdates = (updates, finalUpdate) => {
    if (updates.length === 0 && !finalUpdate) return;
    setSessions((prev) => {
      const queuedResult = updates.reduce(
        (next, item) => item.update(next),
        prev,
      );
      return finalUpdate ? finalUpdate(queuedResult) : queuedResult;
    });
  };

  const flush = () => {
    timerId = null;
    const updates = pending;
    pending = [];
    applyUpdates(updates);
  };

  const takeSessionUpdates = (sessionId) => {
    const selected = [];
    const remaining = [];
    for (const item of pending) {
      (item.sessionId === sessionId ? selected : remaining).push(item);
    }
    pending = remaining;
    clearTimerIfIdle();
    return selected;
  };

  const schedule = (sessionId, update) => {
    if (sessionId === getActiveSessionId()) {
      applyUpdates(takeSessionUpdates(sessionId), update);
      return;
    }
    pending.push({ sessionId, update });
    if (timerId === null) timerId = setTimeoutFn(flush, delayMs);
  };

  const applyImmediate = (sessionId, update) => {
    applyUpdates(takeSessionUpdates(sessionId), update);
  };

  const flushSession = (sessionId) => {
    const updates = takeSessionUpdates(sessionId);
    applyUpdates(updates);
    return updates.length > 0;
  };

  const dispose = () => {
    if (timerId !== null) clearTimeoutFn(timerId);
    timerId = null;
    pending = [];
  };

  return { schedule, applyImmediate, flushSession, flush, dispose };
}
