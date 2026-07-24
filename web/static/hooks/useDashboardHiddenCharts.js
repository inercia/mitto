// web/static/hooks/useDashboardHiddenCharts.js
// Live-updating view of the `dashboard_hidden_charts` UI preference.
// Reads the current value from storage.js and re-renders on either signal:
//   1. `mitto-dashboard-hidden-charts-changed` window event dispatched by
//      setDashboardHiddenCharts (in-tab writes: Settings ▸ Dashboard).
//   2. `onUIPreferencesLoaded` — first async server → localStorage mirror on
//      cold app launch, so the Dashboard honours the last-saved visibility
//      even before the user opens Settings.
import {
  getDashboardHiddenCharts,
  onUIPreferencesLoaded,
} from "../utils/storage.js";
const { useEffect, useState } = window.preact;

/**
 * @returns {string[]} Canonical chart IDs to hide (subset of
 *   KNOWN_DASHBOARD_CHART_IDS in storage.js).
 */
export function useDashboardHiddenCharts() {
  const [hidden, setHidden] = useState(getDashboardHiddenCharts);

  useEffect(() => {
    const refresh = () => {
      const next = getDashboardHiddenCharts();
      setHidden((prev) => {
        if (
          prev.length === next.length &&
          prev.every((v, i) => v === next[i])
        ) {
          return prev;
        }
        return next;
      });
    };

    window.addEventListener("mitto-dashboard-hidden-charts-changed", refresh);
    const unsubscribeLoaded = onUIPreferencesLoaded(refresh);

    return () => {
      window.removeEventListener(
        "mitto-dashboard-hidden-charts-changed",
        refresh,
      );
      if (typeof unsubscribeLoaded === "function") unsubscribeLoaded();
    };
  }, []);

  return hidden;
}
