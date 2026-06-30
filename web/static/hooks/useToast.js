// web/static/hooks/useToast.js
// Central toast notification manager for Mitto Web Interface.
const { useState, useCallback, useRef, useEffect } = window.preact;

let toastIdCounter = 0;

// Default durations by severity (milliseconds)
const DURATION_BY_STYLE = {
  info: 5000,
  success: 5000,
  warning: 10000,
  error: 10000,
};

/**
 * Central toast notification manager.
 * Returns { showToast, dismissToast, toasts }.
 *
 * @param {Object} options
 * @param {number} options.maxToasts - Maximum number of toasts shown simultaneously (default: 5)
 */
export function useToast({ maxToasts = 5 } = {}) {
  const [toasts, setToasts] = useState([]);
  const timersRef = useRef({});

  // Cleanup all pending timers on unmount
  useEffect(() => {
    return () => {
      const timers = timersRef.current;
      Object.keys(timers).forEach((id) => clearTimeout(timers[id]));
      timersRef.current = {};
    };
  }, []);

  const dismissToast = useCallback((id) => {
    // Clear timer if any
    if (timersRef.current[id]) {
      clearTimeout(timersRef.current[id]);
      delete timersRef.current[id];
    }
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const showToast = useCallback(
    ({
      style = "info", // "info" | "success" | "warning" | "error"
      title, // Required: main text
      message = "", // Optional: detail text below title
      duration = null, // Override auto-duration (ms). null = use severity default
      onClick = null, // Optional click handler (e.g., switch session)
      dismissable = true, // Show close button
      sticky = false, // Never auto-dismiss (overrides duration)
    }) => {
      const id = ++toastIdCounter;
      const toast = { id, style, title, message, onClick, dismissable };

      setToasts((prev) => {
        const next = [...prev, toast];
        // Evict oldest if over max
        if (next.length > maxToasts) {
          const evicted = next.shift();
          if (timersRef.current[evicted.id]) {
            clearTimeout(timersRef.current[evicted.id]);
            delete timersRef.current[evicted.id];
          }
        }
        return next;
      });

      // Auto-dismiss unless sticky
      if (!sticky) {
        const ms = duration ?? DURATION_BY_STYLE[style] ?? 5000;
        timersRef.current[id] = setTimeout(() => {
          delete timersRef.current[id];
          setToasts((prev) => prev.filter((t) => t.id !== id));
        }, ms);
      }

      return id;
    },
    [maxToasts],
  );

  return { showToast, dismissToast, toasts };
}
