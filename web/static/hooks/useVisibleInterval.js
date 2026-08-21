// Mitto Web Interface - Visibility-gated setInterval Hook
// Runs a callback on a fixed interval only while the page is visible
// (`document.visibilityState === "visible"`) and `enabled` is truthy.
// Clears the interval on hide/disable and re-arms on the next visible transition,
// firing the callback once immediately so timer-driven UI catches up on wake
// (e.g. the "Working…" chip's mm:ss shows the correct value on restore).
//
// Callers pass the callback inline; it's captured via a ref so identity changes
// on every render do not restart the interval — deps stay `[intervalMs, enabled]`.
// Cleanup removes the visibilitychange listener and clears any live interval.

const { useEffect, useRef } = window.preact;

/**
 * @param {Function} callback   - Invoked every `intervalMs` while visible + enabled;
 *                                also invoked once immediately on each arm (catch-up).
 * @param {number}   intervalMs - Tick interval in milliseconds.
 * @param {Object}   [options]
 * @param {boolean}  [options.enabled=true] - When false, the interval is not armed.
 */
export function useVisibleInterval(callback, intervalMs, options = {}) {
  const { enabled = true } = options;
  const cbRef = useRef(callback);
  useEffect(() => {
    cbRef.current = callback;
  }, [callback]);

  useEffect(() => {
    if (!enabled) return undefined;
    let interval = null;
    const arm = () => {
      if (
        typeof document === "undefined" ||
        document.visibilityState !== "visible"
      ) {
        return;
      }
      // Catch up immediately so timer-driven UI reflects wall-clock time on wake.
      try {
        cbRef.current();
      } catch (_) {
        // Callback errors must not prevent the interval from being scheduled.
      }
      interval = setInterval(() => {
        try {
          cbRef.current();
        } catch (_) {
          // Same rationale: keep ticking even if a tick throws.
        }
      }, intervalMs);
    };
    const disarm = () => {
      if (interval !== null) {
        clearInterval(interval);
        interval = null;
      }
    };
    const onVisibilityChange = () => {
      disarm();
      arm();
    };
    arm();
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", onVisibilityChange);
    }
    return () => {
      disarm();
      if (typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", onVisibilityChange);
      }
    };
  }, [intervalMs, enabled]);
}
