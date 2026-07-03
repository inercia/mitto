// Mitto Web Interface - Pull-to-Refresh Hook
// Handles drag-down gesture on a scrollable container for touch devices.

const { useState, useEffect, useRef } = window.preact;

/**
 * Hook that attaches pull-to-refresh gesture handling to a scrollable element.
 *
 * @param {Object} ref - React ref for the scroll container element.
 * @param {Function} onRefresh - Async callback invoked when the threshold is exceeded.
 * @param {Object} options - Configuration options.
 * @param {boolean} [options.enabled=true] - Disable the gesture (e.g. when a panel is open).
 * @param {number} [options.threshold=70] - Pull distance (px) required to trigger a refresh.
 * @param {number} [options.resistance=0.5] - Resistance factor applied to pull distance.
 * @returns {{ pullDistance: number, refreshing: boolean }}
 */
export function usePullToRefresh(ref, onRefresh, options = {}) {
  const { enabled = true, threshold = 70, resistance = 0.5 } = options;

  const [pullDistance, setPullDistance] = useState(0);
  const [refreshing, setRefreshing] = useState(false);

  // Refs to track touch state without triggering re-renders.
  const startYRef = useRef(0);
  const armedRef = useRef(false);
  const refreshingRef = useRef(false);

  // Keep refreshingRef in sync with state so handlers can read it without
  // capturing a stale closure value.
  useEffect(() => {
    refreshingRef.current = refreshing;
  }, [refreshing]);

  useEffect(() => {
    const el = ref.current;
    if (!el || !enabled) return;

    const handleTouchStart = (e) => {
      // Only arm the gesture if the element is scrolled to the very top.
      // iOS momentum can briefly produce negative scrollTop — treat <= 0 as top.
      if (el.scrollTop <= 0 && e.touches[0]) {
        startYRef.current = e.touches[0].clientY;
        armedRef.current = true;
      } else {
        armedRef.current = false;
      }
    };

    // Non-passive: must call preventDefault() to suppress the browser's own
    // pull-to-reload on Chrome/Android and the rubber-band chain on iOS.
    const handleTouchMove = (e) => {
      if (!armedRef.current || refreshingRef.current) return;
      if (!e.touches[0]) return;

      const deltaY = e.touches[0].clientY - startYRef.current;

      // Re-check scrollTop: user may have scrolled down since touchstart.
      if (el.scrollTop > 0 || deltaY <= 0) {
        armedRef.current = false;
        setPullDistance(0);
        return;
      }

      // Suppress native scroll/reload while we handle the gesture.
      e.preventDefault();

      setPullDistance(Math.round(deltaY * resistance));
    };

    const handleTouchEnd = () => {
      if (!armedRef.current) return;
      armedRef.current = false;

      // Read the latest pullDistance via a state-update function to avoid
      // stale closure.
      setPullDistance((current) => {
        if (current >= threshold && !refreshingRef.current) {
          // Show the spinner and invoke the callback.
          setRefreshing(true);
          refreshingRef.current = true;
          Promise.resolve(onRefresh?.()).finally(() => {
            setRefreshing(false);
            refreshingRef.current = false;
          });
        }
        // Snap back to 0 (animate via CSS transition on the indicator).
        return 0;
      });
    };

    const handleTouchCancel = () => {
      armedRef.current = false;
      setPullDistance(0);
    };

    el.addEventListener("touchstart", handleTouchStart, { passive: true });
    el.addEventListener("touchmove", handleTouchMove, { passive: false });
    el.addEventListener("touchend", handleTouchEnd, { passive: true });
    el.addEventListener("touchcancel", handleTouchCancel, { passive: true });

    return () => {
      el.removeEventListener("touchstart", handleTouchStart);
      el.removeEventListener("touchmove", handleTouchMove);
      el.removeEventListener("touchend", handleTouchEnd);
      el.removeEventListener("touchcancel", handleTouchCancel);
    };
  }, [ref, onRefresh, enabled, threshold, resistance]);

  return { pullDistance, refreshing };
}
