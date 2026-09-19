// web/static/hooks/useToast.js
// Central toast notification hook for Mitto Web Interface. Backed by the
// module-level stores/notificationsStore.js (mitto-sus.11) instead of
// component state, so `showToast`/`dismissToast` are stable functions
// across renders -- passing them into a `useCallback` dep array never
// forces a recreation -- and only `ToastContainer` (via `useToasts()`)
// re-renders on a toast show/dismiss, not the caller of `useToast()`.
const { useState, useEffect } = window.preact;

import {
  showToast,
  dismissToast,
  getToasts,
  subscribeToasts,
} from "../stores/notificationsStore.js";

/**
 * Returns the stable `showToast`/`dismissToast` functions. Does not
 * subscribe to the toast list itself -- components that render the list
 * (currently only `ToastContainer`) should use `useToasts()` instead.
 * Returns { showToast, dismissToast }.
 */
export function useToast() {
  return { showToast, dismissToast };
}

/** Subscribes to the live toast stack. Used by ToastContainer only. */
export function useToasts() {
  const [toasts, setToasts] = useState(getToasts);
  useEffect(() => subscribeToasts(setToasts), []);
  return toasts;
}
