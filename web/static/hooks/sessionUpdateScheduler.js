// Coalesces high-frequency state updates from background conversation streams.
// Active-session updates stay prompt (first chunk of a burst commits
// synchronously) but frame-pace under sustained load (mitto-sus.3): once a
// burst is under way, further chunks queue and commit at most once per
// animation frame instead of once per WebSocket message. Callers can force
// terminal updates to consume queued chunks first so completion/error state
// remains authoritative.

import { perfMark } from "../utils/perfMarks.js";

export const BACKGROUND_SESSION_UPDATE_DELAY_MS = 100;

const hasRAF = typeof requestAnimationFrame === "function";

/** Default frame scheduler: real rAF when available, else a ~60fps timer. */
function defaultRequestFrame(callback) {
  return hasRAF ? requestAnimationFrame(callback) : setTimeout(callback, 16);
}

/** Cancels a handle produced by defaultRequestFrame. */
function defaultCancelFrame(handle) {
  if (hasRAF) cancelAnimationFrame(handle);
  else clearTimeout(handle);
}

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
  requestFrameFn = defaultRequestFrame,
  cancelFrameFn = defaultCancelFrame,
}) {
  let pending = [];
  let timerId = null;

  // Frame-paced active-session queue (mitto-sus.3). Holds updates for
  // whichever session was active when the current coalescing window opened;
  // drained either by the animation frame, the hidden-tab fallback timer, or
  // an explicit applyImmediate/flushSession call for that same session.
  let activeQueue = [];
  let frameId = null;
  let frameFallbackId = null;

  const clearTimerIfIdle = () => {
    if (pending.length === 0 && timerId !== null) {
      clearTimeoutFn(timerId);
      timerId = null;
    }
  };

  const clearFrameTimers = () => {
    if (frameId !== null) {
      cancelFrameFn(frameId);
      frameId = null;
    }
    if (frameFallbackId !== null) {
      clearTimeoutFn(frameFallbackId);
      frameFallbackId = null;
    }
  };

  const applyUpdates = (updates, finalUpdate) => {
    if (updates.length === 0 && !finalUpdate) return;
    // mitto-sus.1: per-chunk apply-cost benchmark seam (no-op unless perf
    // instrumentation is enabled — see utils/perfMarks.js).
    perfMark("ws.chunk.applied", { count: updates.length });
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

  // Pulls any frame-queued active updates belonging to sessionId out of
  // activeQueue without touching updates queued for a different session.
  const takeActiveUpdates = (sessionId) => {
    if (activeQueue.length === 0) return [];
    const selected = [];
    const remaining = [];
    for (const item of activeQueue) {
      (item.sessionId === sessionId ? selected : remaining).push(item);
    }
    activeQueue = remaining;
    if (activeQueue.length === 0) clearFrameTimers();
    return selected;
  };

  // Drains whatever is currently frame-queued (any session) into a single
  // setSessions commit. Invoked by the animation frame or, on hidden/
  // throttled tabs where rAF stalls, by the fallback timer — whichever
  // fires first wins and this call cancels the other.
  const flushActiveFrame = () => {
    clearFrameTimers();
    if (activeQueue.length === 0) return;
    const updates = activeQueue;
    activeQueue = [];
    applyUpdates(updates);
  };

  const armActiveFrame = () => {
    if (frameId !== null || frameFallbackId !== null) return;
    frameId = requestFrameFn(flushActiveFrame);
    frameFallbackId = setTimeoutFn(flushActiveFrame, delayMs);
  };

  const schedule = (sessionId, update) => {
    if (sessionId === getActiveSessionId()) {
      if (activeQueue.length === 0 && frameId === null && frameFallbackId === null) {
        // Idle -> busy transition: commit the first chunk of a burst
        // immediately so the first visible token is never delayed, then
        // open a coalescing window for whatever else arrives before the
        // next frame.
        applyUpdates(takeSessionUpdates(sessionId), update);
        armActiveFrame();
        return;
      }
      activeQueue.push({ sessionId, update });
      return;
    }
    pending.push({ sessionId, update });
    if (timerId === null) timerId = setTimeoutFn(flush, delayMs);
  };

  const applyImmediate = (sessionId, update) => {
    const updates = [...takeActiveUpdates(sessionId), ...takeSessionUpdates(sessionId)];
    applyUpdates(updates, update);
  };

  const flushSession = (sessionId) => {
    const updates = [...takeActiveUpdates(sessionId), ...takeSessionUpdates(sessionId)];
    applyUpdates(updates);
    return updates.length > 0;
  };

  const dispose = () => {
    if (timerId !== null) clearTimeoutFn(timerId);
    timerId = null;
    pending = [];
    clearFrameTimers();
    activeQueue = [];
  };

  return { schedule, applyImmediate, flushSession, flush, dispose };
}
