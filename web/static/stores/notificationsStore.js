// Mitto Web Interface - Subscribable notifications store (mitto-sus.11)
//
// Generalizes the mitto-sus.6/mitto-sus.7 module-level-store pattern (see
// utils/draftStore.js, stores/sessionsStore.js) to the toast stack and the
// four ephemeral background-notification signals that used to live as
// `useState` on `App` (via `useWebSocket`'s `useWSNotifications` sub-hook).
// Before this module, showing OR dismissing a toast -- and every
// background-session completion / loop-start / UI-prompt event -- set state
// on `App`, re-rendering the whole subtree for a purely transient,
// non-visual bridging signal (the toast is the only visible effect).
//
// `showToast`/`dismissToast` are now stable module-level functions (like
// `utils/draftStore.js`'s `setDraft`) instead of being sourced from a
// per-render hook, so passing them into the ~25 `useCallback` dep arrays
// across app.js no longer matters -- their identity never changes.
// `ToastContainer` is the only component that needs the live toast list,
// and it self-subscribes via `hooks/useToast.js`'s `useToasts()`. The four
// background-notification slots are consumed by
// `hooks/useBackgroundNotifications.js`, which subscribes directly and
// never holds React/Preact state for them, so setting one no longer
// touches App's render tree at all.

/** @typedef {"toasts"|"backgroundCompletion"|"loopStarted"|"backgroundUIPrompt"|"backgroundUIPromptTimeout"} NotificationSlice */

let toastIdCounter = 0;
const MAX_TOASTS = 5;

// Default durations by severity (milliseconds) -- unchanged from the
// pre-mitto-sus.11 per-hook table in hooks/useToast.js.
const DURATION_BY_STYLE = {
  info: 5000,
  success: 5000,
  warning: 10000,
  error: 10000,
};

const state = {
  toasts: [],
  backgroundCompletion: null,
  loopStarted: null,
  backgroundUIPrompt: null,
  backgroundUIPromptTimeout: null,
};

/** @type {Record<NotificationSlice, Set<(value: any) => void>>} */
const listeners = {
  toasts: new Set(),
  backgroundCompletion: new Set(),
  loopStarted: new Set(),
  backgroundUIPrompt: new Set(),
  backgroundUIPromptTimeout: new Set(),
};

/** @type {Record<number, ReturnType<typeof setTimeout>>} */
const toastTimers = {};

function notify(slice) {
  for (const callback of listeners[slice]) {
    try {
      callback(state[slice]);
    } catch {
      // A misbehaving listener must not break the store or other listeners.
    }
  }
}

function subscribeSlice(slice) {
  return function subscribe(callback) {
    listeners[slice].add(callback);
    return () => listeners[slice].delete(callback);
  };
}

// ---------------------------------------------------------------------------
// Toasts
// ---------------------------------------------------------------------------

/** Returns the current toast stack. */
export function getToasts() {
  return state.toasts;
}

/** Subscribes to the toast stack. Returns an unsubscribe function. */
export const subscribeToasts = subscribeSlice("toasts");

/**
 * Shows a toast. A stable module-level function -- safe to pass into any
 * `useCallback` dep array without ever forcing that callback to recreate.
 *
 * @param {Object} opts
 * @param {"info"|"success"|"warning"|"error"} [opts.style="info"]
 * @param {string} opts.title - Required: main text.
 * @param {string} [opts.message=""] - Optional detail text below title.
 * @param {number|null} [opts.duration=null] - Override auto-duration (ms).
 *   null = use the severity default.
 * @param {Function|null} [opts.onClick=null] - Optional click handler.
 * @param {boolean} [opts.dismissable=true] - Show close button.
 * @param {boolean} [opts.sticky=false] - Never auto-dismiss (overrides duration).
 * @returns {number} The new toast's id (for manual dismissal).
 */
export function showToast({
  style = "info",
  title,
  message = "",
  duration = null,
  onClick = null,
  dismissable = true,
  sticky = false,
} = {}) {
  const id = ++toastIdCounter;
  const toast = { id, style, title, message, onClick, dismissable };

  const next = [...state.toasts, toast];
  // Evict oldest if over max.
  if (next.length > MAX_TOASTS) {
    const evicted = next.shift();
    if (toastTimers[evicted.id]) {
      clearTimeout(toastTimers[evicted.id]);
      delete toastTimers[evicted.id];
    }
  }
  state.toasts = next;
  notify("toasts");

  // Auto-dismiss unless sticky. Error toasts never auto-dismiss so users
  // cannot miss critical messages; they stay until manually closed.
  if (!sticky && style !== "error") {
    const ms = duration ?? DURATION_BY_STYLE[style] ?? 5000;
    toastTimers[id] = setTimeout(() => {
      delete toastTimers[id];
      dismissToast(id);
    }, ms);
  }

  return id;
}

/** Dismisses a toast by id. No-op if it was already dismissed. */
export function dismissToast(id) {
  if (toastTimers[id]) {
    clearTimeout(toastTimers[id]);
    delete toastTimers[id];
  }
  const next = state.toasts.filter((t) => t.id !== id);
  if (next.length === state.toasts.length) return;
  state.toasts = next;
  notify("toasts");
}

// ---------------------------------------------------------------------------
// Background-notification signals
// ---------------------------------------------------------------------------
// Each is a singleton "pending event" slot: useWebSocket's WS message
// handlers (via useWSNotifications.js) call setX() when a background
// session fires the corresponding event; hooks/useBackgroundNotifications.js
// subscribes directly and bridges it to a toast (+ optional native
// notification), then calls clearX() -- same lifecycle as the previous
// App-level useState-per-slot + useEffect pattern, minus the App re-render.

function defineSlot(slice) {
  return {
    get: () => state[slice],
    set: (value) => {
      state[slice] = value;
      notify(slice);
    },
    subscribe: subscribeSlice(slice),
    clear: () => {
      state[slice] = null;
      notify(slice);
    },
  };
}

const backgroundCompletionSlot = defineSlot("backgroundCompletion");
const loopStartedSlot = defineSlot("loopStarted");
const backgroundUIPromptSlot = defineSlot("backgroundUIPrompt");
const backgroundUIPromptTimeoutSlot = defineSlot("backgroundUIPromptTimeout");

export const getBackgroundCompletion = backgroundCompletionSlot.get;
export const setBackgroundCompletion = backgroundCompletionSlot.set;
export const subscribeBackgroundCompletion =
  backgroundCompletionSlot.subscribe;
export const clearBackgroundCompletion = backgroundCompletionSlot.clear;

export const getLoopStarted = loopStartedSlot.get;
export const setLoopStarted = loopStartedSlot.set;
export const subscribeLoopStarted = loopStartedSlot.subscribe;
export const clearLoopStarted = loopStartedSlot.clear;

export const getBackgroundUIPrompt = backgroundUIPromptSlot.get;
export const setBackgroundUIPrompt = backgroundUIPromptSlot.set;
export const subscribeBackgroundUIPrompt = backgroundUIPromptSlot.subscribe;
export const clearBackgroundUIPrompt = backgroundUIPromptSlot.clear;

export const getBackgroundUIPromptTimeout = backgroundUIPromptTimeoutSlot.get;
export const setBackgroundUIPromptTimeout = backgroundUIPromptTimeoutSlot.set;
export const subscribeBackgroundUIPromptTimeout =
  backgroundUIPromptTimeoutSlot.subscribe;
export const clearBackgroundUIPromptTimeout =
  backgroundUIPromptTimeoutSlot.clear;

/** Test-only: reset the shared store between test cases. */
export function _resetNotificationsStoreForTests() {
  for (const id of Object.keys(toastTimers)) {
    clearTimeout(toastTimers[id]);
    delete toastTimers[id];
  }
  state.toasts = [];
  state.backgroundCompletion = null;
  state.loopStarted = null;
  state.backgroundUIPrompt = null;
  state.backgroundUIPromptTimeout = null;
  for (const slice of Object.keys(listeners)) {
    listeners[slice].clear();
  }
  toastIdCounter = 0;
}
