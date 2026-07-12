// Mitto Web Interface - Global Dashboard view (mitto-aqo).
// Stats header (mitto-aqo.4) + 4-slide carousel of lists (mitto-aqo.5).
// List rows are inert in this increment; click handlers land in mitto-aqo.6.
const { html, useState, useEffect, useMemo, useRef } = window.preact;

import { authFetch } from "../utils/csrf.js";
import { endpoints } from "../utils/endpoints.js";
import { getBasename } from "../lib.js";

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
  return html`<span class="badge badge-xs font-medium ${color}">${label}</span>`;
}

// Slides are anchor-navigated; keep the ids stable for the indicator links.
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

  // Top-5 prompting conversations, most recently updated first. Session
  // `updated_at` is an ISO 8601 string; localeCompare / lexicographic ordering
  // works for that format.
  const promptingList = useMemo(() => {
    return (allSessions || [])
      .filter((s) => s && s.is_prompting)
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

      <!-- Carousel with 4 lists (mitto-aqo.5). Rows are inert div elements;
           click handlers wire in mitto-aqo.6. Anchor-navigated: prev/next and
           indicator buttons point at #dash-slide-* ids on each carousel-item.
           Empty slides render a "No items" placeholder so the slide index and
           navigation stay consistent regardless of data. -->
      <div class="flex flex-col gap-2">
        <div class="carousel w-full rounded-box">
          ${renderSlide(SLIDES[0], renderConversationRows(promptingList))}
          ${renderSlide(SLIDES[1], renderTaskRows(inProgressList))}
          ${renderSlide(SLIDES[2], renderTaskRows(readyList))}
          ${renderSlide(SLIDES[3], renderTaskRows(epicsList))}
        </div>
        <div class="flex justify-center gap-2 py-1">
          ${SLIDES.map(
            (s, i) => html`
              <a
                href="#${s.id}"
                class="btn btn-xs btn-ghost text-mitto-text-muted"
                aria-label="Show ${s.label}"
                title="${s.label}"
                >${i + 1}</a
              >
            `,
          )}
        </div>
      </div>
    </div>
  `;
}

// A single carousel slide: label + prev/next controls above a daisyUI list.
// `rows` is the pre-rendered list-row markup (or a "No items" placeholder).
function renderSlide(slide, rows) {
  const idx = SLIDES.findIndex((s) => s.id === slide.id);
  const prev = SLIDES[(idx - 1 + SLIDES.length) % SLIDES.length];
  const next = SLIDES[(idx + 1) % SLIDES.length];
  return html`
    <div id=${slide.id} class="carousel-item w-full flex-col gap-2 px-1">
      <div class="flex items-center justify-between w-full">
        <div class="text-sm font-semibold text-mitto-text-strong">
          ${slide.label}
        </div>
        <div class="flex gap-1">
          <a
            href="#${prev.id}"
            class="btn btn-xs btn-ghost text-mitto-text-muted"
            aria-label="Previous slide"
            title="${prev.label}"
            >❮</a
          >
          <a
            href="#${next.id}"
            class="btn btn-xs btn-ghost text-mitto-text-muted"
            aria-label="Next slide"
            title="${next.label}"
            >❯</a
          >
        </div>
      </div>
      <ul class="list bg-mitto-surface-2 rounded-box shadow-md w-full">
        ${rows}
      </ul>
    </div>
  `;
}

function emptyPlaceholder() {
  return html`
    <li>
      <div class="text-center text-mitto-text-muted p-6">No items</div>
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

// Row: title (grows/truncates) + agent badge + workspace basename.
function renderConversationRows(sessions) {
  if (!sessions || sessions.length === 0) return emptyPlaceholder();
  return sessions.map((s) => {
    if (!s) return null;
    const title = s.title || s.session_id || "(untitled)";
    return html`
      <li class="list-row items-center gap-2" key=${s.session_id || title}>
        <div class="list-col-grow min-w-0">
          <div class="truncate text-sm text-mitto-text-strong" title="${title}">
            ${title}
          </div>
        </div>
        ${agentBadge(s.acp_server)} ${workspaceBadge(s.working_dir)}
      </li>
    `;
  });
}

// Row: bd id + title (grows/truncates) + priority pill + workspace basename.
function renderTaskRows(items) {
  if (!items || items.length === 0) return emptyPlaceholder();
  return items.map((it) => {
    if (!it) return null;
    const id = it.id || "";
    const title = it.title || "(untitled)";
    return html`
      <li class="list-row items-center gap-2" key=${id || title}>
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
}
