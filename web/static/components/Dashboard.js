// Mitto Web Interface - Global Dashboard view (mitto-aqo).
// Stats header (mitto-aqo.4) + horizontal carousel of all four lists
// (mitto-aqo.5) + click handlers routing to conversation focus / beads issue
// viewer (mitto-aqo.6). No pagination — every list is always visible; the
// carousel uses the shared .mitto-carousel pattern (see styles.css) so panels
// scroll horizontally when they overflow, matching the StatsCharts strip
// above.
const { html, useState, useEffect, useMemo, useRef, useCallback } =
  window.preact;

import { getSdkClient } from "../utils/sdkClient.js";
import { errorMessage } from "../utils/sdkErrors.js";
import { getBasename } from "../lib.js";
import { FolderIcon, MenuIcon } from "./Icons.js";
import { StatsCharts } from "./dashboard/StatsCharts.js";

const REFRESH_INTERVAL_MS = 15_000;
const MAX_LIST_ITEMS = 5;

// Mirror BeadsView priority vocabulary so pills look identical across views.
// bd priorities: 0 = Critical, 1 = High, 2 = Medium, 3 = Low.
const PRIORITY_LABELS = { 0: "Critical", 1: "High", 2: "Medium", 3: "Low" };
const PRIORITY_COLORS = {
  0: "badge-error",
  1: "badge-warning",
  2: "badge-info",
  3: "badge-ghost",
};

function priorityPill(p) {
  const n = typeof p === "number" ? p : 3;
  const label = PRIORITY_LABELS[n] ?? String(p);
  const color = PRIORITY_COLORS[n] ?? PRIORITY_COLORS[3];
  return html`<span class="badge badge-xs font-medium ${color}"
    >${label}</span
  >`;
}

// Ordered list definitions. `id` is kept for stable keys; pagination is now
// index-driven rather than anchor-driven.
const SLIDES = [
  { id: "dash-slide-prompting", label: "Recent conversations" },
  { id: "dash-slide-in-progress", label: "In-progress tasks" },
  { id: "dash-slide-ready", label: "Ready tasks" },
  { id: "dash-slide-recent", label: "Recently modified" },
];

// Build the "Recent conversations" panel list: X currently-prompting sessions
// first (isStreaming), then Y most-recently-active non-prompting sessions
// sorted by updated_at desc, with X + Y capped at `max`. Both groups are
// sorted individually by updated_at desc (ISO 8601 lexicographic). Null /
// undefined entries and sessions without a session_id are skipped so the
// panel never renders half-baked rows.
//
// Auto-created children (`child_origin === "auto"`, e.g. the "Coder"
// sub-agents spawned via auto_children config) are excluded from BOTH groups
// so the panel focuses on user-visible top-level conversations; MCP- and
// human-spawned children are still included (they are intentional user
// actions). Exported (see bottom of file) so the unit test suite can exercise
// the mixing logic without booting Preact.
function selectRecentConversations(allSessions, max) {
  const list = Array.isArray(allSessions) ? allSessions : [];
  const isEligible = (s) => s && s.child_origin !== "auto";
  const byUpdatedDesc = (a, b) => {
    const au = a.updated_at || "";
    const bu = b.updated_at || "";
    if (au === bu) return 0;
    return au < bu ? 1 : -1;
  };
  const prompting = list
    .filter((s) => isEligible(s) && s.isStreaming)
    .slice()
    .sort(byUpdatedDesc);
  if (prompting.length >= max) return prompting.slice(0, max);
  const remaining = max - prompting.length;
  const recent = list
    .filter((s) => isEligible(s) && !s.isStreaming)
    .slice()
    .sort(byUpdatedDesc)
    .slice(0, remaining);
  return prompting.concat(recent);
}

/**
 * Dashboard component - global cross-workspace overview.
 * @param {Object[]} allSessions - Flat list of all sessions; used for client-side
 *   prompting / loop counts so the header updates the instant a prompt starts
 *   or a loop toggles, without waiting for the next 15s poll.
 * @param {Function} showToast - Toast dispatcher; called on fetch error.
 * @param {Function} onFocusConversation - `(sessionId) => void`. Fired when a
 *   recent-conversation row is activated (click or Enter/Space).
 * @param {Function} onOpenTask - `(issueId, workingDir) => void`. Fired when a
 *   task row (in-progress / ready / epic) is activated. Callers bind the
 *   originating session id themselves.
 * @param {Function} onShowSidebar - Opens the conversations sidebar (mobile);
 *   used by the header hamburger button to return to the conversation list.
 */
export function Dashboard({
  allSessions = [],
  showToast,
  onFocusConversation,
  onOpenTask,
  onShowSidebar,
}) {
  const [data, setData] = useState(null); // null = first load in-flight
  const mountedRef = useRef(true);

  // Lifted out of the interval effect so the WS-driven refresh below can reuse
  // it. mountedRef gates late resolutions after unmount; the fetch itself has
  // no per-call cancellation token because a stale response just no-ops via
  // mountedRef — the state it would set is idempotent (setData(json)).
  const fetchDashboard = useCallback(async () => {
    try {
      const json = await getSdkClient().dashboard.summary();
      if (!mountedRef.current) return;
      setData(json);
    } catch (err) {
      if (!mountedRef.current) return;
      if (showToast) {
        showToast({
          style: "error",
          title: "Dashboard refresh failed",
          message: errorMessage(err, String(err)),
        });
      }
    }
  }, [showToast]);

  useEffect(() => {
    mountedRef.current = true;
    fetchDashboard();
    const id = setInterval(fetchDashboard, REFRESH_INTERVAL_MS);
    return () => {
      mountedRef.current = false;
      clearInterval(id);
    };
  }, [fetchDashboard]);

  // Push refresh: the backend fsnotify watcher broadcasts mitto:beads_changed
  // whenever any watched .beads/ directory mutates (bd CLI, another agent, git
  // pull, bd dolt pull). The Dashboard aggregates across ALL workspaces, so —
  // unlike BeadsView which filters by working_dir — every event is potentially
  // relevant and we refetch unconditionally. The 15s poll above is kept as a
  // safety net for the WS-disconnected case. mitto-523.
  useEffect(() => {
    const handler = () => fetchDashboard();
    window.addEventListener("mitto:beads_changed", handler);
    return () => window.removeEventListener("mitto:beads_changed", handler);
  }, [fetchDashboard]);

  // Client-side derived counts. Sessions in `allSessions` carry the prompting
  // flag as `isStreaming` (set from the WebSocket `is_prompting` field —
  // see computeAllSessions in lib.js); `is_prompting` itself is not present
  // on the client model. `loop_configured` marks any session that has a loop
  // attached (matches the sidebar categorization); `loop_enabled` tells
  // whether it is currently running vs paused.
  const { prompting, loopsActive, loopsStopped } = useMemo(() => {
    let p = 0,
      la = 0,
      ls = 0;
    for (const s of allSessions) {
      if (!s) continue;
      if (s.isStreaming) p += 1;
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
    allSessions.length > 0 ? loopsActive : stats ? stats.loops_active : null;
  const loopsStoppedCount =
    allSessions.length > 0 ? loopsStopped : stats ? stats.loops_stopped : null;

  const isFirstLoad = data === null;
  const spinner = html`<span
    class="loading loading-spinner loading-md text-mitto-text-muted"
  ></span>`;

  // Top-MAX_LIST_ITEMS conversations for the "Recent conversations" panel:
  // currently-prompting sessions first (X), then the most-recently-active
  // non-prompting ones (Y), with X + Y <= MAX_LIST_ITEMS. Within each group
  // sort by `updated_at` desc; ISO 8601 strings sort correctly lexicographically.
  // `isStreaming` is the client-side field populated from the WebSocket
  // `is_prompting` flag (see computeAllSessions in lib.js). If X already meets
  // or exceeds MAX_LIST_ITEMS, only prompting rows are shown (Y = 0).
  const recentConversationsList = useMemo(() => {
    return selectRecentConversations(allSessions, MAX_LIST_ITEMS);
  }, [allSessions]);

  // Server-side lists are already capped at 5 by /api/dashboard, but slice
  // defensively in case the contract ever changes.
  const lists = (data && data.lists) || {};
  const inProgressList = (lists.in_progress || []).slice(0, MAX_LIST_ITEMS);
  const readyList = (lists.ready || []).slice(0, MAX_LIST_ITEMS);
  const recentList = (lists.recently_modified || []).slice(0, MAX_LIST_ITEMS);

  return html`
    <div
      class="flex-1 flex flex-col min-w-0 overflow-hidden bg-mitto-bg p-6 gap-6"
    >
      <!-- Mobile header: hamburger + title. Mirrors the conversation and
           beads-view headers so the left side panel is reachable from the
           dashboard on phones. The hamburger is hidden on md+ where the
           sidebar is always visible; the title stays on all breakpoints.
           Kept OUTSIDE the scrollable content wrapper below (alongside the
           stats row) so the title stays pinned at the top as the user scrolls
           through the charts and lists. -->
      <div class="flex items-center gap-2 shrink-0">
        <button
          onClick=${() => onShowSidebar && onShowSidebar()}
          class="btn btn-ghost btn-square btn-sm md:hidden shrink-0 inline-flex tooltip tooltip-bottom"
          data-tip="Show conversations"
          aria-label="Show conversations"
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <span class="font-semibold text-2xl flex-1">Dashboard</span>
      </div>
      <!-- Stats row uses a plain 3-column CSS grid (not daisyUI's .stats
           component) because .stats-horizontal is absent from the precompiled
           tailwind.css and the default .stats layout renders empty on narrow
           viewports — see tailwind-precompiled-jit-class-gotcha. The inline
           grid-template-columns mirrors the lists grid below (which uses the
           same technique for the same reason).
           Kept OUTSIDE the scrollable content wrapper below (alongside the
           title) so the top-of-page counters stay pinned as the user scrolls
           through the charts and lists. -->
      <div
        class="grid gap-2 w-full rounded-lg shadow bg-mitto-surface-2 p-4 shrink-0"
        style="grid-template-columns: repeat(3, minmax(0, 1fr));"
      >
        <div class="flex flex-col gap-1 min-w-0">
          <div class="text-xs text-mitto-text-muted truncate">
            Issues in progress
          </div>
          <div class="text-2xl font-bold text-mitto-text-strong">
            ${isFirstLoad ? spinner : (issuesInProgress ?? "—")}
          </div>
          <div class="text-xs text-mitto-text-muted truncate">
            across all workspaces
          </div>
        </div>
        <div class="flex flex-col gap-1 min-w-0">
          <div class="text-xs text-mitto-text-muted truncate">
            Conversations prompting
          </div>
          <div class="text-2xl font-bold text-mitto-text-strong">
            ${promptingCount ?? (isFirstLoad ? spinner : "—")}
          </div>
          <div class="text-xs text-mitto-text-muted truncate">
            agents currently replying
          </div>
        </div>
        <div class="flex flex-col gap-1 min-w-0">
          <div class="text-xs text-mitto-text-muted truncate">
            Loops active / stopped
          </div>
          <div class="text-2xl font-bold text-mitto-text-strong">
            ${isFirstLoad && allSessions.length === 0
              ? spinner
              : html`${loopsActiveCount ?? 0}
                  <span class="text-mitto-text-muted">/</span>
                  ${loopsStoppedCount ?? 0}`}
          </div>
          <div class="text-xs text-mitto-text-muted truncate">
            loop-enabled sessions
          </div>
        </div>
      </div>

      <!-- Scrollable content wrapper: only the charts and lists scroll; the
           title and stats row above stay fixed. flex-1 + min-h-0 lets it own
           the remaining vertical space inside the flex column; its own gap-6
           reproduces the spacing the outer gap-6 provided when the charts and
           lists were siblings of the header. pb-6 gives the last list panel
           breathing room so its bottom row is never flush against the
           viewport edge (and, together with shrink-0 on each carousel below,
           keeps the outer container scrollable when the total content is
           taller than the viewport instead of silently squeezing the strips
           and hiding their last rows). -->
      <div class="flex-1 min-h-0 overflow-y-auto flex flex-col gap-6 pb-6">
        <!-- Timeseries charts (mitto-a86b.8): tokens, tool calls, prompts vs
           agent turns. Rendered between the stats row and the lists grid so
           the dashboard's vertical rhythm goes overview → activity → lists. -->
        <${StatsCharts} showToast=${showToast} />

        <!-- Horizontal carousel of all four lists (mitto-aqo.5). Every list is
           always visible; panels scroll horizontally when the viewport is too
           narrow to show them all side-by-side. Uses the shared
           .mitto-carousel pattern (see styles.css) so the scrollbar fades in
           only on hover / focus / active scrolling, matching the StatsCharts
           strip above. Each panel pads up to MAX_LIST_ITEMS invisible rows
           so every visible list shares the same height regardless of
           content. -->
        ${(() => {
          // Loading gate (mitto-eml): the three bd-driven panels are populated
          // only from /api/dashboard, so `isFirstLoad` is the right signal.
          // "Recent conversations" is client-derived from `allSessions` and can
          // already be populated before /api/dashboard resolves, so its gate
          // additionally requires `allSessions.length === 0` — same combined
          // gate the loops-stats block above uses.
          const rendered = [
            renderConversationRows(
              recentConversationsList,
              onFocusConversation,
              isFirstLoad && allSessions.length === 0,
            ),
            renderTaskRows(inProgressList, onOpenTask, isFirstLoad),
            renderTaskRows(readyList, onOpenTask, isFirstLoad),
            renderTaskRows(recentList, onOpenTask, isFirstLoad),
          ];
          const panels = SLIDES.map((slide, i) => ({
            slide,
            rows: rendered[i] || [],
          }));
          return html`
            <div class="mitto-carousel shrink-0 gap-4 w-full">
              ${panels.map((p) =>
                renderListPanel(p.slide, p.rows, MAX_LIST_ITEMS),
              )}
            </div>
          `;
        })()}
      </div>
    </div>
  `;
}

// A single list panel: label above a daisyUI list. `rows` is the real rows
// (unpadded); `padTo` is the row count to pad up to with invisible spacers so
// every list occupies the same vertical space regardless of content. Callers
// pass MAX_LIST_ITEMS so all panels in the carousel share the same height
// regardless of content. width:min(...) mirrors the chart-card sizing so
// the two strips visually align: panels keep a readable width and the
// parent .mitto-carousel handles horizontal overflow.
function renderListPanel(slide, rows, padTo) {
  const realRows = Array.isArray(rows) ? rows : [];
  const target = Math.max(padTo || 0, realRows.length);
  const spacers = [];
  for (let i = 0; i < target - realRows.length; i++) {
    spacers.push(spacerRow(`__spacer-${slide.id}-${i}`));
  }
  return html`
    <div
      id=${slide.id}
      class="flex flex-col gap-2"
      style="width: min(360px, 100%); flex: 0 0 auto;"
    >
      <div class="text-sm font-semibold text-mitto-text-strong">
        ${slide.label}
      </div>
      <ul class="list bg-mitto-surface-2 rounded-box shadow-md w-full">
        ${realRows}${spacers}
      </ul>
    </div>
  `;
}

// daisyUI's `.list .list-row` ships with `gap:1rem; padding:1rem`, which is
// too airy for the dashboard's dense side-by-side panels. Override to a
// compact rhythm; applied inline (rather than via a Tailwind class) because
// the precompiled tailwind.css does not include arbitrary padding utilities
// like `p-[.5rem]`. Kept as a shared const so every row helper (task,
// conversation, empty, spacer) tightens together.
const COMPACT_ROW_STYLE = "gap: 0.5rem; padding: 0.5rem 0.75rem;";

// Invisible spacer row matching the real two-line row shape (badge line +
// title line) so panels on the same page share height exactly. Every real
// row in the dashboard (task or conversation) is two lines tall; a one-line
// spacer would make padded panels visibly shorter than fully-populated ones.
// `aria-hidden` + `visibility:hidden` keeps it out of the a11y tree and off
// the tab order while still contributing height to the flow.
function spacerRow(key) {
  return html`
    <li
      class="list-row"
      style="visibility: hidden; ${COMPACT_ROW_STYLE}"
      aria-hidden="true"
      key=${key}
    >
      <div class="list-col-grow min-w-0 flex flex-col gap-1">
        <div class="text-xs">${"\u00A0"}</div>
        <div class="truncate text-sm">${"\u00A0"}</div>
      </div>
    </li>
  `;
}

// Empty-state row: keeps the same two-line shape as spacer/real rows so it
// lines up cleanly with any spacer rows the panel renderer may add below it
// to bottom-align with a sibling on the same page.
// NB: htm renders text content literally, so an HTML entity like &nbsp; would
// show as the raw string "&nbsp;". Use the actual U+00A0 non-breaking space
// character instead to reserve the first line's height.
function emptyRow() {
  return html`
    <li class="list-row" style="${COMPACT_ROW_STYLE}" key="__empty">
      <div class="list-col-grow min-w-0 flex flex-col gap-1">
        <div class="text-xs">${"\u00A0"}</div>
        <div class="text-center text-sm text-mitto-text-muted">No items</div>
      </div>
    </li>
  `;
}

// Loading-state row: same two-line shape as emptyRow so the panel does not
// jump height when the first /api/dashboard fetch resolves and swaps this
// row out. Rendered instead of the "No items" copy while data === null, so
// the user does not misread a still-in-flight fetch as "server confirmed
// nothing here" (mitto-eml). Mirrors the daisyUI spinner already used for
// the stats-row placeholders above (Dashboard.js loopsActive/stopped cell).
function loadingRow() {
  return html`
    <li class="list-row" style="${COMPACT_ROW_STYLE}" key="__loading">
      <div class="list-col-grow min-w-0 flex flex-col gap-1">
        <div class="text-xs">${"\u00A0"}</div>
        <div
          class="flex items-center justify-center gap-2 text-sm text-mitto-text-muted"
        >
          <span
            class="loading loading-spinner loading-xs text-mitto-text-muted"
            aria-hidden="true"
          ></span>
          <span>Loading…</span>
        </div>
      </div>
    </li>
  `;
}

function agentBadge(acp) {
  if (!acp) return null;
  return html`<span
    class="badge badge-xs bg-mitto-surface-4 text-mitto-text-strong"
    >${acp}</span
  >`;
}

function workspaceBadge(workingDir) {
  if (!workingDir) return null;
  const base = getBasename(workingDir) || workingDir;
  // Folder-icon-prefixed chip on a distinct surface with accent-colored text
  // so the workspace/folder reads as the row's contextual anchor and is easy
  // to scan-separate from the muted bd id next to it. Uses only precompiled
  // utilities — `bg-mitto-accent-*/text-mitto-accent-100/700` tints are not
  // in the shipped tailwind.css bundle (JIT trap).
  return html`<span
    class="inline-flex items-center gap-1 badge badge-xs bg-mitto-surface-4 text-mitto-accent border-0 min-w-0"
    title="${workingDir}"
  >
    <${FolderIcon} className="w-3 h-3 shrink-0" />
    <span class="truncate">${base}</span>
  </span>`;
}

// Interactive class suffix applied to a populated row. Kept out of the row
// builders so the empty-state placeholder and rows missing an identifier stay
// visually inert (no hover/pointer) without diverging from the row template.
const ROW_INTERACTIVE_CLASSES =
  "cursor-pointer hover:bg-mitto-surface-3 focus-visible:bg-mitto-surface-3 focus-visible:outline-none";

// Keyboard activation helper: Enter and Space match native button semantics.
function activateOnKey(fn) {
  return (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      fn();
    }
  };
}

// Two-line row (mirrors renderTaskRows shape for visual parity):
//   Line 1: workspace basename + agent badge
//   Line 2: title (grows/truncates full width)
// Interactive when `onClick` is provided AND the session has a `session_id`.
// `isLoading` gates the empty-vs-loading branch (mitto-eml): callers pass true
// while the first /api/dashboard fetch is in flight so the panel shows a
// spinner instead of the misleading "No items" copy.
function renderConversationRows(sessions, onClick, isLoading) {
  // Loading takes precedence over empty so the panel does not lie about the
  // server state while the first fetch is still in flight.
  if ((!sessions || sessions.length === 0) && isLoading) return [loadingRow()];
  // Empty list → one visible "No items" row. Bottom-alignment padding across
  // sibling panels is now handled by renderListPanel(padTo) so this helper
  // returns only the real content and never over-pads a lone panel.
  if (!sessions || sessions.length === 0) return [emptyRow()];
  const rows = sessions.map((s) => {
    if (!s) return null;
    // Match the canonical sidebar priority (SessionItem.js): user-set `name`
    // first, then the auto-generated `description`, then a static fallback.
    // The raw session_id is deliberately NOT shown \u2014 it is opaque to users.
    const title = s.name || s.description || "Untitled";
    const sid = s.session_id;
    const clickable = !!(onClick && sid);
    const activate = clickable ? () => onClick(sid) : undefined;
    // Mirror the sidebar's prompting indicator (SessionItem.js: showLoadingRing):
    // a daisyUI accent-colored loading-ring shown next to the title while the
    // agent is actively responding. Purely visual; row remains fully clickable.
    const streaming = !!s.isStreaming;
    return html`
      <li
        class="list-row ${clickable ? ROW_INTERACTIVE_CLASSES : ""}"
        style="${COMPACT_ROW_STYLE}"
        key=${sid || title}
        role=${clickable ? "button" : undefined}
        tabindex=${clickable ? "0" : undefined}
        onClick=${activate}
        onKeyDown=${clickable ? activateOnKey(activate) : undefined}
      >
        <div class="list-col-grow min-w-0 flex flex-col gap-1">
          <div class="flex items-center gap-2 min-w-0">
            <span class="min-w-0 truncate">
              ${workspaceBadge(s.working_dir)}
            </span>
            ${agentBadge(s.acp_server)}
          </div>
          <div class="flex items-center gap-2 min-w-0">
            ${streaming
              ? html`<span
                  class="loading loading-ring loading-xs shrink-0 text-mitto-accent"
                  title="Receiving response..."
                  aria-label="Receiving response..."
                ></span>`
              : null}
            <div
              class="truncate text-sm text-mitto-text-strong"
              title="${title}"
            >
              ${title}
            </div>
          </div>
        </div>
      </li>
    `;
  });
  return rows;
}

// Two-line row:
//   Line 1: workspace basename + bd id (monospace) + priority pill
//   Line 2: title (grows/truncates full width)
// Interactive only when the item has both an `id` and a `working_dir` (both
// are required to open the correct workspace's beads viewer).
// `isLoading` gates the empty-vs-loading branch (mitto-eml): callers pass true
// while the first /api/dashboard fetch is in flight so the panel shows a
// spinner instead of the misleading "No items" copy.
function renderTaskRows(items, onClick, isLoading) {
  // Loading takes precedence over empty so the panel does not lie about the
  // server state while the first fetch is still in flight.
  if ((!items || items.length === 0) && isLoading) return [loadingRow()];
  // See renderConversationRows: real rows only, bottom-alignment is handled
  // page-scoped by renderListPanel(padTo).
  if (!items || items.length === 0) return [emptyRow()];
  const rows = items.map((it) => {
    if (!it) return null;
    const id = it.id || "";
    const title = it.title || "(untitled)";
    const wd = it.working_dir || "";
    const clickable = !!(onClick && id && wd);
    const activate = clickable ? () => onClick(id, wd) : undefined;
    return html`
      <li
        class="list-row ${clickable ? ROW_INTERACTIVE_CLASSES : ""}"
        style="${COMPACT_ROW_STYLE}"
        key=${id || title}
        role=${clickable ? "button" : undefined}
        tabindex=${clickable ? "0" : undefined}
        onClick=${activate}
        onKeyDown=${clickable ? activateOnKey(activate) : undefined}
      >
        <div class="list-col-grow min-w-0 flex flex-col gap-1">
          <div class="flex items-center gap-2 min-w-0">
            <span class="min-w-0 truncate">
              ${workspaceBadge(it.working_dir)}
            </span>
            <span class="text-xs font-mono text-mitto-text-muted shrink-0">
              ${id}
            </span>
            ${priorityPill(it.priority)}
          </div>
          <div class="truncate text-sm text-mitto-text-strong" title="${title}">
            ${title}
          </div>
        </div>
      </li>
    `;
  });
  return rows;
}
