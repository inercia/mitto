// Mitto Web Interface - Global Dashboard view (mitto-aqo).
// Stats-header increment (mitto-aqo.4). The lists/carousel body lands in .5;
// the #dashboard-lists placeholder below the stats row is intentionally empty
// for now so .5 can drop content in without restructuring the container.
const { html, useState, useEffect, useMemo, useRef } = window.preact;

import { authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/endpoints.js";

const REFRESH_INTERVAL_MS = 15_000;

/**
 * Dashboard component - global cross-workspace overview.
 * @param {Object[]} allSessions - Flat list of all sessions; used for client-side
 *   prompting / loop counts so the header updates the instant a prompt starts
 *   or a loop toggles, without waiting for the next 15s poll.
 * @param {Function} showToast - Toast dispatcher; called on fetch error.
 */
export function Dashboard({ allSessions = [], showToast }) {
  const [data, setData] = useState(null); // null = first load in-flight
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    let cancelled = false;

    async function fetchOnce() {
      try {
        const res = await authFetch(endpoints.misc.dashboard());
        if (cancelled || !mountedRef.current) return;
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const json = await res.json();
        if (cancelled || !mountedRef.current) return;
        setData(json);
      } catch (err) {
        if (cancelled || !mountedRef.current) return;
        if (showToast) {
          showToast({
            style: "error",
            title: "Dashboard refresh failed",
            message: err && err.message ? err.message : String(err),
          });
        }
      }
    }

    fetchOnce();
    const id = setInterval(fetchOnce, REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      mountedRef.current = false;
      clearInterval(id);
    };
  }, [showToast]);

  // Client-side derived counts. `loop_configured` marks any session that has a
  // loop attached (matches the sidebar categorization); `loop_enabled` tells
  // whether it is currently running vs paused.
  const { prompting, loopsActive, loopsStopped } = useMemo(() => {
    let p = 0,
      la = 0,
      ls = 0;
    for (const s of allSessions) {
      if (!s) continue;
      if (s.is_prompting) p += 1;
      if (s.loop_configured || s.loop_enabled) {
        if (s.loop_enabled) la += 1;
        else ls += 1;
      }
    }
    return { prompting: p, loopsActive: la, loopsStopped: ls };
  }, [allSessions]);

  const stats = data && data.stats ? data.stats : null;
  const issuesInProgress = stats ? stats.issues_in_progress : null;
  // Fallback to server values when the client hasn't populated allSessions yet.
  const promptingCount =
    allSessions.length > 0
      ? prompting
      : stats
      ? stats.conversations_prompting
      : null;
  const loopsActiveCount =
    allSessions.length > 0
      ? loopsActive
      : stats
      ? stats.loops_active
      : null;
  const loopsStoppedCount =
    allSessions.length > 0
      ? loopsStopped
      : stats
      ? stats.loops_stopped
      : null;

  const isFirstLoad = data === null;
  const spinner = html`<span
    class="loading loading-spinner loading-md text-mitto-text-muted"
  ></span>`;

  return html`
    <div
      class="flex-1 flex flex-col min-w-0 overflow-y-auto bg-mitto-bg p-6 gap-6"
    >
      <div
        class="stats stats-vertical sm:stats-horizontal shadow bg-mitto-surface-2 w-full"
      >
        <div class="stat">
          <div class="stat-title text-mitto-text-muted">Issues in progress</div>
          <div class="stat-value text-mitto-text-strong">
            ${isFirstLoad ? spinner : issuesInProgress ?? "—"}
          </div>
          <div class="stat-desc text-mitto-text-muted">
            across all workspaces
          </div>
        </div>
        <div class="stat">
          <div class="stat-title text-mitto-text-muted">
            Conversations prompting
          </div>
          <div class="stat-value text-mitto-text-strong">
            ${promptingCount ?? (isFirstLoad ? spinner : "—")}
          </div>
          <div class="stat-desc text-mitto-text-muted">
            agents currently replying
          </div>
        </div>
        <div class="stat">
          <div class="stat-title text-mitto-text-muted">
            Loops active / stopped
          </div>
          <div class="stat-value text-mitto-text-strong">
            ${isFirstLoad && allSessions.length === 0
              ? spinner
              : html`${loopsActiveCount ?? 0}
                  <span class="text-mitto-text-muted">/</span>
                  ${loopsStoppedCount ?? 0}`}
          </div>
          <div class="stat-desc text-mitto-text-muted">
            loop-enabled sessions
          </div>
        </div>
      </div>

      <!-- Placeholder for the carousel + lists (mitto-aqo.5). Left as an empty
           container so .5 can drop its content in without restructuring. -->
      <div id="dashboard-lists" class="flex-1"></div>
    </div>
  `;
}
