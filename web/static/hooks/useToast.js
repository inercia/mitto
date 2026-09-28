// web/static/hooks/useToast.js
// Central toast notification hook for Mitto Web Interface. Backed by the
// module-level stores/notificationsStore.js (mitto-sus.11) instead of
// component state, so `showToast`/`dismissToast` are stable functions
// across renders -- passing them into a `useCallback` dep array never
// forces a recreation -- and only `ToastContainer` (via `useToasts()`)
// re-renders on a toast show/dismiss, not the caller of `useToast()`.
//
// `useState`/`useEffect` are read from window.preact lazily inside
// useToasts() (not destructured at module top-level) so that under Bun's
// shared-process test runner an unrelated test file that transiently
// imports this module (e.g. via MoveAgentDialog.js) with a throwaway
// window.preact stub can't permanently freeze this hook onto stale
// mocks -- matches the convention documented in useBeadsIntegration.js.

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
  const { useState, useEffect } = window.preact;
  const [toasts, setToasts] = useState(getToasts);
  useEffect(() => subscribeToasts(setToasts), []);
  return toasts;
}

