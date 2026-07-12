// Mitto Web Interface - Global Dashboard view (mitto-aqo).
// Stats header (mitto-aqo.4) + responsive grid of all four lists (mitto-aqo.5)
// + click handlers routing to conversation focus / beads issue viewer
// (mitto-aqo.6). No pagination — every list is always visible; column count
// scales 1/2/3/4 with viewport width so phones stack, tablets pair, and wide
// desktops show the whole set in one row.
const { html, useState, useEffect, useMemo, useRef } = window.preact;

import { authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/endpoints.js";
import { getBasename } from "../lib.js";
import { MenuIcon } from "./Icons.js";

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
  { id: "dash-slide-epics", label: "Epic tasks" },
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
  const epicsList = (lists.epics || []).slice(0, MAX_LIST_ITEMS);

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
      <!-- Stats row is always horizontal, including on phones — the vertical
           stack was pushing the lists below the fold on narrow viewports. On
           iPhone width (390px) the three cells share the row and the titles
           are allowed to wrap to two lines; the stat values (single digits or
           short ratios) stay comfortably on one line. -->
      <div
        class="stats stats-horizontal shadow bg-mitto-surface-2 w-full"
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
          renderTaskRows(epicsList, onOpenTask),
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

// Invisible spacer row matching the real .list-row shape so panels on the same
// page bottom-align. `aria-hidden` + `visibility:hidden` keeps it out of the
// a11y tree and off the tab order while still contributing height to the flow.
function spacerRow(key) {
  return html`
    <li
      class="list-row items-center gap-2"
      style="visibility: hidden;"
      aria-hidden="true"
      key=${key}
    >
      <div class="list-col-grow min-w-0">
        <div class="truncate text-sm">&nbsp;</div>
      </div>
    </li>
  `;
}

// Empty-state row: keeps the same .list-row shape so it lines up cleanly with
// any spacer rows the panel renderer may add below it to bottom-align with a
// sibling on the same page.
function emptyRow() {
  return html`
    <li class="list-row items-center gap-2" key="__empty">
      <div class="list-col-grow min-w-0">
        <div class="text-center text-mitto-text-muted">No items</div>
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
  return html`<span
    class="text-xs text-mitto-text-muted truncate"
    title="${workingDir}"
    >${base}</span
  >`;
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

// Row: title (grows/truncates) + agent badge + workspace basename. When
// `onClick` is provided AND the session has a `session_id`, the row becomes
// keyboard/mouse-activatable; otherwise it stays inert.
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
        class="list-row items-center gap-2 ${clickable
          ? ROW_INTERACTIVE_CLASSES
          : ""}"
        key=${sid || title}
        role=${clickable ? "button" : undefined}
        tabindex=${clickable ? "0" : undefined}
        onClick=${activate}
        onKeyDown=${clickable ? activateOnKey(activate) : undefined}
      >
        <div class="list-col-grow min-w-0">
          <div class="truncate text-sm text-mitto-text-strong" title="${title}">
            ${title}
          </div>
        </div>
        ${agentBadge(s.acp_server)} ${workspaceBadge(s.working_dir)}
      </li>
    `;
  });
  return rows;
}

// Row: bd id + title (grows/truncates) + priority pill + workspace basename.
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
        class="list-row items-center gap-2 ${clickable
          ? ROW_INTERACTIVE_CLASSES
          : ""}"
        key=${id || title}
        role=${clickable ? "button" : undefined}
        tabindex=${clickable ? "0" : undefined}
        onClick=${activate}
        onKeyDown=${clickable ? activateOnKey(activate) : undefined}
      >
        <div class="text-xs font-mono text-mitto-text-muted shrink-0">
          ${id}
        </div>
        <div class="list-col-grow min-w-0">
          <div class="truncate text-sm text-mitto-text-strong" title="${title}">
            ${title}
          </div>
        </div>
        ${priorityPill(it.priority)} ${workspaceBadge(it.working_dir)}
      </li>
    `;
  });
  return rows;
}
