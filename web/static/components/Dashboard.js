// Mitto Web Interface - Global Dashboard view (mitto-aqo).
// Stats header (mitto-aqo.4) + responsive grid of all four lists (mitto-aqo.5)
// + click handlers routing to conversation focus / beads issue viewer
// (mitto-aqo.6). No pagination — every list is always visible; column count
// scales 1/2/3/4 with viewport width so phones stack, tablets pair, and wide
// desktops show the whole set in one row.
const { html, useState, useEffect, useMemo, useRef, useCallback } =
  window.preact;

import { authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/endpoints.js";
import { getBasename } from "../lib.js";
import { FolderIcon, MenuIcon } from "./Icons.js";

const REFRESH_INTERVAL_MS = 15_000;
const MAX_LIST_ITEMS = 5;
// Responsive column count for the lists grid. Thresholds tuned so each column
// stays wide enough to read task titles + priority pill without truncating on
// common device widths — in particular iPad portrait (768px) and iPad landscape
// (1024px) both fell into a "2 narrow columns" bucket with earlier Tailwind-sm
// (640px) mirror, which the user reported as too narrow. New buckets:
//   <900px  → 1 column (iPhone, iPad portrait, narrow browser windows)
//   900-1279 → 2 columns (iPad landscape, small laptop)
//   1280-1535 → 3 columns (typical desktop)
//   ≥1536   → 4 columns (wide desktop)
// The grid template is applied via inline style because the precompiled
// tailwind.css does NOT include any responsive-prefixed grid-cols utilities
// (only base .grid-cols-1/2/3) — see tailwind-precompiled-jit-class-gotcha.
const COLUMN_BREAKPOINTS = [
  { minWidth: 1536, columns: 4 },
  { minWidth: 1280, columns: 3 },
  { minWidth: 900, columns: 2 },
];
const DEFAULT_COLUMNS = 1;

function pickColumns() {
  if (typeof window === "undefined" || !window.matchMedia) {
    return COLUMN_BREAKPOINTS[0].columns;
  }
  for (const bp of COLUMN_BREAKPOINTS) {
    if (window.matchMedia(`(min-width: ${bp.minWidth}px)`).matches) {
      return bp.columns;
    }
  }
  return DEFAULT_COLUMNS;
}

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
  return html`<span class="badge badge-xs font-medium ${color}">${label}</span>`;
}

// Ordered list definitions. `id` is kept for stable keys; pagination is now
// index-driven rather than anchor-driven.
const SLIDES = [
  { id: "dash-slide-prompting", label: "Prompting conversations" },
  { id: "dash-slide-in-progress", label: "In-progress tasks" },
  { id: "dash-slide-ready", label: "Ready tasks" },
  { id: "dash-slide-recent", label: "Recently modified" },
];

/**
 * Dashboard component - global cross-workspace overview.
 * @param {Object[]} allSessions - Flat list of all sessions; used for client-side
 *   prompting / loop counts so the header updates the instant a prompt starts
 *   or a loop toggles, without waiting for the next 15s poll.
 * @param {Function} showToast - Toast dispatcher; called on fetch error.
 * @param {Function} onFocusConversation - `(sessionId) => void`. Fired when a
 *   prompting-conversation row is activated (click or Enter/Space).
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
  // Number of grid columns for the lists row, driven by viewport width.
  // Initialised synchronously from matchMedia so the first render already
  // uses the correct column count; kept in sync via listeners below.
  const [columns, setColumns] = useState(pickColumns);
  const mountedRef = useRef(true);

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) return;
    const mqs = COLUMN_BREAKPOINTS.map((bp) =>
      window.matchMedia(`(min-width: ${bp.minWidth}px)`),
    );
    const onChange = () => setColumns(pickColumns());
    for (const mq of mqs) mq.addEventListener("change", onChange);
    return () => {
      for (const mq of mqs) mq.removeEventListener("change", onChange);
    };
  }, []);

  // Lifted out of the interval effect so the WS-driven refresh below can reuse
  // it. mountedRef gates late resolutions after unmount; the fetch itself has
  // no per-call cancellation token because a stale response just no-ops via
  // mountedRef — the state it would set is idempotent (setData(json)).
  const fetchDashboard = useCallback(async () => {
    try {
      const res = await authFetch(endpoints.misc.dashboard());
      if (!mountedRef.current) return;
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const json = await res.json();
      if (!mountedRef.current) return;
      setData(json);
    } catch (err) {
      if (!mountedRef.current) return;
      if (showToast) {
        showToast({
          style: "error",
          title: "Dashboard refresh failed",
          message: err && err.message ? err.message : String(err),
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

  // Top-5 prompting conversations, most recently updated first. Session
  // `updated_at` is an ISO 8601 string; localeCompare / lexicographic ordering
  // works for that format. Filter by `isStreaming` — the client-side field
  // populated from the WebSocket `is_prompting` flag; see the count block above.
  const promptingList = useMemo(() => {
    return (allSessions || [])
      .filter((s) => s && s.isStreaming)
      .slice()
      .sort((a, b) => {
        const au = a.updated_at || "";
        const bu = b.updated_at || "";
        if (au === bu) return 0;
        return au < bu ? 1 : -1;
      })
      .slice(0, MAX_LIST_ITEMS);
  }, [allSessions]);

  // Server-side lists are already capped at 5 by /api/dashboard, but slice
  // defensively in case the contract ever changes.
  const lists = (data && data.lists) || {};
  const inProgressList = (lists.in_progress || []).slice(0, MAX_LIST_ITEMS);
  const readyList = (lists.ready || []).slice(0, MAX_LIST_ITEMS);
  const recentList = (lists.recently_modified || []).slice(0, MAX_LIST_ITEMS);

  return html`
    <div
      class="flex-1 flex flex-col min-w-0 overflow-y-auto bg-mitto-bg p-6 gap-6"
    >
      <!-- Mobile header: hamburger + title. Mirrors the conversation and
           beads-view headers so the left side panel is reachable from the
           dashboard on phones. The hamburger is hidden on md+ where the
           sidebar is always visible; the title stays on all breakpoints. -->
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
           same technique for the same reason). -->
      <div
        class="grid gap-2 w-full rounded-lg shadow bg-mitto-surface-2 p-4"
        style="grid-template-columns: repeat(3, minmax(0, 1fr));"
      >
        <div class="flex flex-col gap-1 min-w-0">
          <div class="text-xs text-mitto-text-muted truncate">
            Issues in progress
          </div>
          <div class="text-2xl font-bold text-mitto-text-strong">
            ${isFirstLoad ? spinner : issuesInProgress ?? "—"}
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

      <!-- Responsive grid of all four lists (mitto-aqo.5). No carousel:
           every list is always visible. Column count is driven by the
           columns state (1/2/3/4) which reacts to viewport width via
           matchMedia. The grid template is applied via inline style rather
           than Tailwind's responsive prefixes because the precompiled
           tailwind.css does not include prefixed grid-cols utilities. Each
           panel pads up to MAX_LIST_ITEMS invisible rows so every list in
           the same row shares the same height regardless of content. -->
      ${(() => {
        const rendered = [
          renderConversationRows(promptingList, onFocusConversation),
          renderTaskRows(inProgressList, onOpenTask),
          renderTaskRows(readyList, onOpenTask),
          renderTaskRows(recentList, onOpenTask),
        ];
        const panels = SLIDES.map((slide, i) => ({
          slide,
          rows: rendered[i] || [],
        }));
        const gridStyle = `grid-template-columns: repeat(${columns}, minmax(0, 1fr));`;
        return html`
          <div class="grid gap-4 w-full" style=${gridStyle}>
            ${panels.map((p) =>
              renderListPanel(p.slide, p.rows, MAX_LIST_ITEMS),
            )}
          </div>
        `;
      })()}
    </div>
  `;
}

// A single list panel: label above a daisyUI list. `rows` is the real rows
// (unpadded); `padTo` is the row count to pad up to with invisible spacers so
// every list occupies the same vertical space regardless of content. Callers
// pass MAX_LIST_ITEMS so all panels in the responsive grid share the same
// height whether they are stacked on a phone or aligned in a wide-desktop row.
function renderListPanel(slide, rows, padTo) {
  const realRows = Array.isArray(rows) ? rows : [];
  const target = Math.max(padTo || 0, realRows.length);
  const spacers = [];
  for (let i = 0; i < target - realRows.length; i++) {
    spacers.push(spacerRow(`__spacer-${slide.id}-${i}`));
  }
  return html`
    <div id=${slide.id} class="flex flex-col gap-2 min-w-0">
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
        <div class="text-xs">&nbsp;</div>
        <div class="truncate text-sm">&nbsp;</div>
      </div>
    </li>
  `;
}

// Empty-state row: keeps the same two-line shape as spacer/real rows so it
// lines up cleanly with any spacer rows the panel renderer may add below it
// to bottom-align with a sibling on the same page.
function emptyRow() {
  return html`
    <li class="list-row" style="${COMPACT_ROW_STYLE}" key="__empty">
      <div class="list-col-grow min-w-0 flex flex-col gap-1">
        <div class="text-xs">&nbsp;</div>
        <div class="text-center text-sm text-mitto-text-muted">No items</div>
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
function renderConversationRows(sessions, onClick) {
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
          <div class="truncate text-sm text-mitto-text-strong" title="${title}">
            ${title}
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
function renderTaskRows(items, onClick) {
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
