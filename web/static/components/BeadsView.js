// Mitto Web Interface - BeadsView Component
// Displays a Beads (bd) issue list and detail view for a workspace.

const { html, useState, useEffect, useCallback, useMemo, useRef, Fragment } =
  window.preact;

import {
  apiUrl,
  authFetch,
  secureFetch,
  endpoints,
  getBeadsFilters,
  setBeadsFilters,
  getBeadsGrouping,
  setBeadsGrouping,
  getBeadsSort,
  setBeadsSort,
} from "../utils/index.js";
import { getBasename, copyToClipboard } from "../lib.js";
import {
  PlusIcon,
  CloseIcon,
  TrashIcon,
  RefreshIcon,
  BroomIcon,
  ChevronUpIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  CheckIcon,
  CircleIcon,
  HourglassIcon,
  MenuIcon,
  ArrowDownIcon,
  ArrowUpIcon,
  SyncIcon,
  SettingsIcon,
  ExpandIcon,
  CollapseIcon,
  MoonIcon,
  SunIcon,
  LayersIcon,
  EllipsisIcon,
  SortIcon,
  CopyIcon,
  getPromptIconOrDefault,
  PeriodicIcon,
  LinkIcon,
  ListIcon,
  BoldIcon,
  ItalicIcon,
  StrikethroughIcon,
  InlineCodeIcon,
  CodeBlockIcon,
  NumberedListIcon,
  HeadingIcon,
  QuoteIcon,
} from "./Icons.js";
import {
  promptPeriodicMode,
  promptPeriodicIsToggleable,
  promptPeriodicDefaultOn,
} from "../utils/prompts.js";
import { CodeEditorField } from "./CodeEditorField.js";
import {
  ContextMenu,
  buildPromptGroupMenuItems,
  PortalTooltip,
} from "./ContextMenu.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { Drawer } from "./Drawer.js";
import { Tooltip } from "./Tooltip.js";
import { usePullToRefresh } from "../hooks/usePullToRefresh.js";
import { useSwipeToAction } from "../hooks/index.js";

// How often (ms) to surface a progress toast during a bulk closed-issue
// cleanup. Progress events arrive per server-side batch (25 issues each), which
// can be more frequent than is useful as toasts, so we throttle visible updates
// to this rate and keep a single live toast updated in place.
const CLEANUP_PROGRESS_TOAST_INTERVAL_MS = 3000;

// ---- helpers ----------------------------------------------------------------

// Safely read a fetch Response body that is expected to be JSON. If the body is
// not valid JSON (e.g. a plain-text error page from a 403/500), return an object
// with an `error` field instead of throwing. This prevents WebKit/Safari from
// surfacing the cryptic "The string did not match the expected pattern." error
// when res.json() is called on a non-JSON body.
async function readBeadsResponse(res) {
  const text = await res.text();
  if (text) {
    try {
      const parsed = JSON.parse(text);
      // Normalize the canonical nested error envelope {error:{code,message,details}}
      // down to the flat {error:"<message>", stderr} shape the beads consumers expect.
      // This covers both validation errors (4xx) and bd-failure errors (500, canonical envelope).
      if (parsed && typeof parsed.error === "object" && parsed.error !== null) {
        return {
          error: parsed.error.message || `Request failed (HTTP ${res.status})`,
          stderr:
            (parsed.error.details && parsed.error.details.stderr) || undefined,
        };
      }
      return parsed;
    } catch (_e) {
      // fall through to error object below
    }
  }
  return {
    error: (text && text.trim()) || `Request failed (HTTP ${res.status})`,
  };
}

// matchesSearch returns true when `issue` matches the user's search query.
// The query is whitespace-tokenized (case-insensitive) and every token must
// appear as a substring of one of the searchable fields: id, title, owner,
// or description (body). An empty / whitespace-only query matches everything.
// The exact-ID case (e.g. "mitto-3bx") is naturally covered because the full
// id substring-matches itself.
function matchesSearch(issue, search) {
  if (!search) return true;
  const tokens = search.toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return true;
  const id = (issue.id || "").toLowerCase();
  const title = (issue.title || "").toLowerCase();
  const owner = (issue.owner || "").toLowerCase();
  const description = (issue.description || "").toLowerCase();
  for (const t of tokens) {
    if (
      !(
        id.includes(t) ||
        title.includes(t) ||
        owner.includes(t) ||
        description.includes(t)
      )
    ) {
      return false;
    }
  }
  return true;
}

// Display labels for the folder's configured upstream task system.
const UPSTREAM_LABELS = {
  jira: "Jira",
  github: "GitHub",
  gitlab: "GitLab",
  linear: "Linear",
};

// Dependency edge kinds accepted by "bd dep add -t" (mirrors the backend
// allow-list in beads_api.go). "blocks" is the default/most common kind, so it
// is listed first.
const DEP_TYPES = [
  "blocks",
  "related",
  "parent-child",
  "discovered-from",
  "until",
  "caused-by",
  "validates",
  "relates-to",
  "supersedes",
  "tracks",
];

const PRIORITY_LABELS = { 0: "Critical", 1: "High", 2: "Medium", 3: "Low" };
const PRIORITY_COLORS = {
  0: "badge-error",
  1: "badge-warning",
  2: "badge-info",
  3: "badge-ghost",
};

export const STATUS_COLORS = {
  open: "bg-green-700 text-green-100",
  in_progress: "bg-blue-700 text-blue-100 beads-status-inprogress",
  closed: "bg-mitto-surface-4 text-mitto-text-strong",
  blocked: "bg-red-700 text-red-100",
  deferred: "bg-cyan-800 text-cyan-100",
};

// Status filter toggle buttons shown in the Beads toolbar. Each button toggles
// the visibility of issues with the matching status. `key` is the bd status
// value; `label` is the user-facing text (used for the tooltip/aria-label of
// the icon-only button); `Icon` is the glyph rendered inside the button.
const BEADS_STATUS_TOGGLES = [
  { key: "open", label: "open", Icon: CircleIcon },
  { key: "in_progress", label: "in-progress", Icon: HourglassIcon },
  { key: "closed", label: "closed", Icon: CheckIcon },
];

// In-memory (not persisted) status toggle state for the Beads view. Kept at
// module scope so the user's selection survives navigating away from and back
// to the Beads view within the same app session. It intentionally resets on a
// full reload / app restart to its default: open and in-progress shown, closed
// hidden.
let beadsStatusToggles = { open: true, in_progress: true, closed: false };

// Hover-only tooltips are pointless on touch devices (no hover); gate the portal
// toolbar tooltip the same way daisyUI gates its CSS tooltips so taps never
// trigger a stuck bubble.
const BEADS_SUPPORTS_HOVER =
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(hover: hover)").matches;

// Delay before a toolbar tooltip appears on hover (ms).
const BEADS_TOOLTIP_DELAY_MS = 250;

const TYPE_COLORS = {
  epic: "bg-purple-700 text-purple-100",
  feature: "bg-blue-700 text-blue-100 beads-type-feature",
  bug: "bg-red-700 text-red-100",
  task: "bg-mitto-surface-4 text-mitto-text-strong",
  chore: "bg-mitto-surface-4 text-mitto-text-strong",
};

function badge(text, colorClass) {
  return html`<span
    class="badge badge-sm font-medium px-2.5 py-0.5 ${colorClass}"
    >${text}</span
  >`;
}

function priorityBadge(p) {
  const n = typeof p === "number" ? p : 3;
  return badge(
    PRIORITY_LABELS[n] ?? String(p),
    PRIORITY_COLORS[n] ?? PRIORITY_COLORS[3],
  );
}

export function statusBadge(s) {
  const label = (s || "open").replace(/_/g, " ");
  return badge(
    label,
    STATUS_COLORS[s] ?? "bg-mitto-surface-4 text-mitto-text-strong",
  );
}

// Status badge for the (narrow) dependencies list: shows the full status label
// on normal screens and collapses to a single-letter abbreviation on small
// screens (see .beads-badge-abbr / .beads-badge-full in styles.css). The full
// label is kept in `title` for hover/accessibility.
function depStatusBadge(s) {
  const label = (s || "open").replace(/_/g, " ");
  const colorClass =
    STATUS_COLORS[s] ?? "bg-mitto-surface-4 text-mitto-text-strong";
  return html`<span
    class="badge badge-sm font-medium px-2.5 py-0.5 ${colorClass}"
    title=${label}
  >
    <span class="beads-badge-abbr">${label.charAt(0)}</span
    ><span class="beads-badge-full">${label}</span>
  </span>`;
}

function typeBadge(t) {
  return badge(t || "task", TYPE_COLORS[t] ?? TYPE_COLORS.task);
}

// Sort menu options. `field` is the persisted key; `key` is the issue property
// holding the value to compare on (priority is numeric, the dates are RFC3339
// strings).
const SORT_FIELD_OPTIONS = [
  { field: "created", label: "Creation date", key: "created_at" },
  { field: "updated", label: "Modification date", key: "updated_at" },
  { field: "priority", label: "Priority", key: "priority" },
];

const SORT_FIELD_LABELS = Object.fromEntries(
  SORT_FIELD_OPTIONS.map((o) => [o.field, o.label]),
);

// Compare two issues for the chosen sort field and direction. Priority is a
// number (0 = highest) so ascending = most important first; the dates compare
// by parsed timestamp. A stable id tiebreaker keeps ordering deterministic and
// is intentionally independent of direction.
function cmpBySort(a, b, sort) {
  const dir = sort.direction === "asc" ? 1 : -1;
  let primary = 0;
  if (sort.field === "priority") {
    const pa = typeof a.priority === "number" ? a.priority : 3;
    const pb = typeof b.priority === "number" ? b.priority : 3;
    primary = pa - pb;
  } else {
    const key = sort.field === "updated" ? "updated_at" : "created_at";
    primary =
      (Date.parse(a?.[key] || "") || 0) - (Date.parse(b?.[key] || "") || 0);
  }
  if (primary !== 0) return primary * dir;
  return (a.id || "").localeCompare(b.id || "");
}

function renderMarkdown(text) {
  if (!text) return null;
  if (typeof window !== "undefined" && window.marked && window.DOMPurify) {
    const raw = window.marked.parse(text);
    return window.DOMPurify.sanitize(raw, { USE_PROFILES: { html: true } });
  }
  return null;
}

function commentBody(text) {
  const m = renderMarkdown(text);
  if (m)
    return html`<div
      class="markdown-content text-mitto-text text-sm max-w-none"
      dangerouslySetInnerHTML=${{ __html: m }}
    />`;
  return html`<pre
    class="whitespace-pre-wrap wrap-break-word text-sm text-mitto-text"
  >
${text || ""}</pre
  >`;
}

// ---- Detail side panel ------------------------------------------------------

const ISSUE_TYPES = ["task", "feature", "epic", "bug", "chore"];

function labelValue(label, value) {
  if (value === null || value === undefined || value === "") return null;
  return html`
    <div>
      <div class="text-xs text-mitto-text-secondary mb-0.5">${label}</div>
      <div class="text-sm text-mitto-text wrap-break-word">${value}</div>
    </div>
  `;
}

/**
 * BeadsDetailPanel is a fixed right-side overlay that serves two modes:
 *
 *  - View mode (an `issue` is provided): shows the read-only properties of a
 *    single issue, populated directly from the already-loaded list row so it
 *    opens instantly without an extra network request. Subtasks are computed
 *    from the full issue list via the parent field.
 *  - Create mode (`isCreating` is true): shows editable fields for a new issue
 *    plus a "Save" footer that POSTs to /api/issues.
 *
 * The panel is a dock-mode daisyUI Drawer (drawer-dock; see styles.css) docked
 * to the right edge of the beads view area and confined to its own width — NOT a
 * full-area overlay — with no dimming backdrop. A composited full-window overlay
 * over the issue list dropped the list's GPU backing store on pointer-move and
 * blanked it (mitto-cdf), so dock mode leaves the list to the panel's left under
 * no composited layer. `expand`/fullscreen widens the panel to fill the area.
 * Clicking anywhere outside the panel (the issue list / conversation) closes it,
 * detected via a document mousedown listener rather than a backdrop element.
 */
export function BeadsDetailPanel({
  issue,
  allIssues,
  isCreating,
  workingDir,
  initialFullscreen,
  onClose,
  onCreated,
  onUpdated,
  showToast,
  onFetchPrompts,
  onRunPrompt,
  onDelete,
  onToggleStatus,
  onToggleDefer,
  statusBusy,
  onSelectIssue,
  createParentId,
}) {
  const isOpen = isCreating || !!issue;
  const [isClosing, setIsClosing] = useState(false);
  const [shouldRender, setShouldRender] = useState(isOpen);
  // When true the panel expands to fill the available area (hiding the issue
  // list behind it) so a single issue's details are easier to read. On desktop
  // that is the beads view area; on small screens — where the panel is otherwise
  // confined to a strip with a list peek beside it (mitto-cdf) — it fills the
  // viewport (the dock's 85vw cap is lifted via --dock-maxw:100% when fullscreen).
  // The expand toggle is shown on every screen size now that the small-screen
  // panel is confined rather than always full-width. The single-issue overlay
  // (BeadsIssueView) passes initialFullscreen=false so it opens as the docked
  // ~40rem side panel over the conversation; the toggle still lets the user
  // expand it to fill the area.
  const [fullscreen, setFullscreen] = useState(!!initialFullscreen);
  // Phone detection drives the panel width. We deliberately use the user agent
  // (not a viewport-width breakpoint like Tailwind's `md:`): the native macOS
  // app runs in a WKWebView that reports a Macintosh UA but can have a narrow
  // window, and must still get the desktop layout (a doubled fixed-width panel
  // with a dimming backdrop), not the full-width phone layout. A viewport-based
  // rule would misclassify that narrow window as mobile and drop the backdrop.
  const isMobile = useMemo(() => {
    if (typeof navigator === "undefined") return false;
    const ua = navigator.userAgent || "";
    return /iPhone|iPad|iPod|Android|webOS|BlackBerry|IEMobile|Opera Mini/i.test(
      ua,
    );
  }, []);
  const lastIssueRef = useRef(issue);
  const lastCreatingRef = useRef(isCreating);
  if (issue) lastIssueRef.current = issue;
  if (isOpen) lastCreatingRef.current = isCreating;

  // While closing, keep rendering whichever mode was last open.
  const creating = isOpen ? isCreating : lastCreatingRef.current;
  const data = issue || lastIssueRef.current;

  // Create-mode form state.
  const [title, setTitle] = useState("");
  const [type, setType] = useState("task");
  const [priority, setPriority] = useState(2); // 2 = Medium
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [createDeps, setCreateDeps] = useState([]);
  const [createNewDepType, setCreateNewDepType] = useState("blocks");
  const [createNewDepId, setCreateNewDepId] = useState("");
  const [createAssignee, setCreateAssignee] = useState("");
  const [createNotes, setCreateNotes] = useState("");

  // Magic-wand "Improve description" state. Mirrors ChatInput's improve-prompt
  // flow but targets the create-form description. `improvingDesc` gates the
  // in-flight request and drives the spinner.
  const [improvingDesc, setImprovingDesc] = useState(false);

  // Prompts loaded for the detail-panel kebab menu.
  const [prompts, setPrompts] = useState([]);
  // ContextMenu anchor for the detail-panel kebab; null = closed.
  const [panelMenu, setPanelMenu] = useState(null);

  // View-mode inline description editing. editingDesc switches the rendered
  // description to a CodeMirror editor. Edits accumulate in viewDraft and are
  // persisted by the unified Save button. descMinHeight keeps the editor at
  // least as tall as the content it replaces.
  const [editingDesc, setEditingDesc] = useState(false);
  const [descMinHeight, setDescMinHeight] = useState(0);
  const detailEditorApiRef = useRef(null);
  const descViewRef = useRef(null);
  // Imperative handle for the create-form's description CodeMirror editor.
  const createEditorApiRef = useRef(null);

  // View-mode inline title editing.
  const [editingTitle, setEditingTitle] = useState(false);
  const titleRef = useRef(null);
  // Snapshot of viewDraft.title captured on startEditTitle so Escape can revert.
  const titleEditStartRef = useRef("");

  // View-mode inline type editing.
  const [editingType, setEditingType] = useState(false);
  const typeRef = useRef(null);

  // View-mode inline assignee editing.
  const [editingAssignee, setEditingAssignee] = useState(false);
  const assigneeRef = useRef(null);
  // Snapshot of viewDraft.assignee captured on startEditAssignee so Escape can revert.
  const assigneeEditStartRef = useRef("");

  // Draft / dirty / save state for view mode. All six editable fields
  // accumulate into viewDraft; a single Save posts them together.
  const [viewDraft, setViewDraft] = useState({
    title: "",
    type: "task",
    priority: 2,
    description: "",
    assignee: "",
    notes: "",
  });
  const [savingView, setSavingView] = useState(false);
  // When true, show the "Discard changes?" confirm dialog before closing.
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/issues/{id} when an issue is opened. `depsBusy` gates add/remove
  // requests; `newDepType`/`newDepId` back the "add dependency" row.
  const [deps, setDeps] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");
  const [comments, setComments] = useState([]);
  const [notes, setNotes] = useState("");

  // View-mode inline notes editing.
  const [editingNotes, setEditingNotes] = useState(false);
  const [notesMinHeight, setNotesMinHeight] = useState(0);
  const notesRef = useRef(null);
  const notesViewRef = useRef(null);

  // View-mode "add comment": a "+" button at the bottom of the comments list
  // reveals a textarea with the same save-on-blur behaviour as notes. An empty
  // draft on blur just closes the editor without a request; otherwise the
  // comment is posted via /api/issues/{id}/comments and the list is refreshed.
  const [addingComment, setAddingComment] = useState(false);
  const [commentDraft, setCommentDraft] = useState("");
  const [savingComment, setSavingComment] = useState(false);
  const commentRef = useRef(null);

  // Reset the form whenever create mode is (re)entered.
  useEffect(() => {
    if (isCreating) {
      setTitle("");
      setType("task");
      setPriority(2);
      setDescription("");
      setSubmitting(false);
      setCreateDeps([]);
      setCreateNewDepType("blocks");
      setCreateNewDepId("");
      setCreateAssignee("");
      setCreateNotes("");
    }
  }, [isCreating]);

  // Close the type dropdown on outside click while it is open.
  useEffect(() => {
    if (!editingType) return undefined;
    const onDocClick = (e) => {
      if (typeRef.current && !typeRef.current.contains(e.target)) {
        setEditingType(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [editingType]);

  const openPanelMenu = useCallback(
    (e) => {
      e.preventDefault();
      e.stopPropagation();
      const rect = e.currentTarget.getBoundingClientRect();
      setPanelMenu({ x: rect.left, y: rect.bottom });
      if (onFetchPrompts && workingDir) {
        // Pass the issue so item.*-gated prompts (e.g. Start work hidden for
        // closed issues) evaluate against this issue's status (mitto-gns).
        onFetchPrompts(workingDir, data).then((list) => setPrompts(list || []));
      }
    },
    [onFetchPrompts, workingDir, data],
  );

  useEffect(() => {
    if (isOpen) {
      setShouldRender(true);
      setIsClosing(false);
    } else if (shouldRender) {
      setIsClosing(true);
      const timer = setTimeout(() => {
        setShouldRender(false);
        setIsClosing(false);
        setFullscreen(false);
      }, 150);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  const handleSave = useCallback(async () => {
    if (!description.trim()) return;
    setSubmitting(true);
    try {
      const body = { type, priority, description: description.trim() };
      if (title.trim()) body.title = title.trim();
      if (createParentId) body.parent = createParentId;
      if (createAssignee.trim()) body.assignee = createAssignee.trim();
      if (createNotes.trim()) body.notes = createNotes.trim();
      if (createDeps.length)
        body.dependencies = createDeps.map((d) => ({
          id: d.id,
          type: d.type || "blocks",
        }));
      const res = await secureFetch(
        endpoints.issues.create({ working_dir: workingDir }),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast &&
          showToast({
            style: "error",
            title: respData.error || "Failed to create issue",
          });
      } else {
        showToast && showToast({ style: "success", title: "Issue created" });
        onCreated && onCreated();
        onClose && onClose();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to create issue",
        });
    } finally {
      setSubmitting(false);
    }
  }, [
    workingDir,
    title,
    type,
    priority,
    description,
    createParentId,
    createAssignee,
    createNotes,
    createDeps,
    showToast,
    onCreated,
    onClose,
  ]);

  const addCreateDep = useCallback(() => {
    const id = createNewDepId.trim();
    if (!id) return;
    if (createDeps.some((d) => d.id === id)) return;
    setCreateDeps((prev) => [...prev, { id, type: createNewDepType }]);
    setCreateNewDepId("");
  }, [createNewDepId, createNewDepType, createDeps]);

  const removeCreateDep = useCallback((id) => {
    setCreateDeps((prev) => prev.filter((d) => d.id !== id));
  }, []);

  // AI-enhance a description text field via the same auxiliary endpoint the chat
  // input's magic wand uses (/api/aux/improve-prompt). Works on any
  // text/setText pair so it serves both the create-form description and the
  // view-mode inline edit draft. Replaces the text with the improved version on
  // success; surfaces errors as a toast. No-op when empty or already running.
  const improveDescriptionText = useCallback(
    async (text, setText) => {
      if (improvingDesc || !text || !text.trim()) return;
      setImprovingDesc(true);
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 65000); // 65s timeout
      try {
        const response = await secureFetch(endpoints.aux.improvePrompt(), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            prompt: text,
            workspace_uuid:
              (typeof window !== "undefined" &&
                window.mittoCurrentWorkspaceUUID) ||
              (typeof sessionStorage !== "undefined" &&
                sessionStorage.getItem("mittoCurrentWorkspaceUUID")) ||
              "",
          }),
          signal: controller.signal,
        });
        clearTimeout(timeoutId);
        if (!response.ok) {
          const errData = await response.json().catch(() => ({}));
          throw new Error(
            errData?.error?.message ||
              errData?.message ||
              "Failed to improve description",
          );
        }
        const respData = await response.json();
        if (respData.improved_prompt) {
          setText(respData.improved_prompt);
        }
      } catch (err) {
        clearTimeout(timeoutId);
        const msg =
          err.name === "AbortError"
            ? "Request timed out. Please try again."
            : err.message || "Failed to improve description";
        showToast && showToast({ style: "error", title: msg });
      } finally {
        setImprovingDesc(false);
      }
    },
    [improvingDesc, showToast],
  );

  // md renders the draft description so the read-only view reflects in-progress edits.
  const md = useMemo(
    () => renderMarkdown(!creating && viewDraft && viewDraft.description),
    [creating, viewDraft && viewDraft.description],
  );
  const subtasks = useMemo(
    () =>
      !creating && data ? allIssues.filter((i) => i.parent === data.id) : [],
    [creating, allIssues, data && data.id],
  );

  // The "original" values used to compute dirtiness. Notes come from async
  // fetchDeps, so they are sourced from the `notes` state rather than data.
  const viewOriginal = useMemo(
    () => ({
      title: (data && data.title) || "",
      type: (data && data.issue_type) || "task",
      priority: data && typeof data.priority === "number" ? data.priority : 2,
      description: (data && data.description) || "",
      assignee: (data && data.assignee) || "",
      notes: notes || "",
    }),
    [
      data && data.id,
      data && data.title,
      data && data.issue_type,
      data && data.priority,
      data && data.description,
      data && data.assignee,
      notes,
    ],
  );

  const viewDirty = useMemo(() => {
    if (creating) return false;
    const t = viewDraft.title.trim();
    return (
      (t !== "" && t !== viewOriginal.title) ||
      viewDraft.type !== viewOriginal.type ||
      viewDraft.priority !== viewOriginal.priority ||
      viewDraft.description !== viewOriginal.description ||
      viewDraft.assignee.trim() !== viewOriginal.assignee ||
      viewDraft.notes !== viewOriginal.notes
    );
  }, [creating, viewDraft, viewOriginal]);

  // handleClose and handleDiscardAndClose are defined here (after creating and
  // viewDirty) because their dep arrays reference both computed values.
  const handleClose = useCallback(() => {
    if (!creating && viewDirty) {
      setConfirmDiscard(true);
      return;
    }
    setIsClosing(true);
    setTimeout(() => onClose(), 150);
  }, [creating, viewDirty, onClose]);

  const handleDiscardAndClose = useCallback(() => {
    setConfirmDiscard(false);
    setIsClosing(true);
    setTimeout(() => onClose(), 150);
  }, [onClose]);

  // Close the panel when the user clicks outside of it (e.g. on the issue list
  // or conversation to its left). Dock mode (mitto-cdf) deliberately has no
  // dimming backdrop — a composited full-area overlay over the list dropped its
  // GPU backing store on pointer-move — so outside clicks are detected with a
  // document listener (no DOM overlay) instead. Clicks inside the docked panel,
  // inside any modal dialog (the confirm/discard dialog renders as a
  // viewport-covering .modal sibling), or while the kebab context menu is open
  // are ignored so those surfaces keep working; the context menu dismisses
  // itself via its own outside-click handler. handleClose routes through the
  // unsaved-changes guard, so an outside click with a dirty draft prompts to
  // discard rather than closing immediately.
  useEffect(() => {
    if (!isOpen) return undefined;
    const onDocMouseDown = (e) => {
      const t = e.target;
      if (!t || !t.closest) return;
      if (t.closest(".drawer-dock") || t.closest(".modal")) return;
      if (panelMenu) return;
      handleClose();
    };
    document.addEventListener("mousedown", onDocMouseDown);
    return () => document.removeEventListener("mousedown", onDocMouseDown);
  }, [isOpen, panelMenu, handleClose]);

  const panelMenuItems = useMemo(() => {
    if (!data) return [];
    const promptGroupItems = buildPromptGroupMenuItems(
      prompts,
      (p, opts) => {
        setPanelMenu(null);
        onRunPrompt && onRunPrompt(p, data, opts);
      },
      html`<${PlusIcon} />`,
    );
    return [
      ...promptGroupItems,
      {
        label: data.status === "closed" ? "Reopen" : "Close",
        icon:
          data.status === "closed"
            ? html`<${RefreshIcon} />`
            : html`<${CheckIcon} />`,
        onClick: () => onToggleStatus && onToggleStatus(data),
        disabled: statusBusy,
      },
      {
        label: data.status === "deferred" ? "Undefer" : "Defer",
        icon:
          data.status === "deferred"
            ? html`<${SunIcon} />`
            : html`<${MoonIcon} />`,
        onClick: () => onToggleDefer && onToggleDefer(data),
        disabled: statusBusy,
      },
      {
        label: "Delete",
        icon: html`<${TrashIcon} />`,
        onClick: () => onDelete && onDelete(data),
        danger: true,
      },
    ];
  }, [
    data,
    prompts,
    statusBusy,
    onRunPrompt,
    onToggleStatus,
    onToggleDefer,
    onDelete,
  ]);

  // Seed non-notes fields whenever a different issue opens (notes come from
  // fetchDeps below, which calls setViewDraft when seedDraftNotes is true).
  useEffect(() => {
    if (creating || !data || !data.id) return;
    setViewDraft({
      title: data.title || "",
      type: data.issue_type || "task",
      priority: typeof data.priority === "number" ? data.priority : 2,
      description: data.description || "",
      assignee: data.assignee || "",
      notes: "",
    });
  }, [creating, data && data.id]);

  // Leave all edit modes whenever the viewed issue changes.
  useEffect(() => {
    setEditingDesc(false);
    setEditingTitle(false);
    setEditingType(false);
    setEditingAssignee(false);
    setEditingNotes(false);
    setAddingComment(false);
    setSavingComment(false);
    setCommentDraft("");
  }, [data && data.id]);

  // The description CodeMirror editor auto-focuses on mount (autoFocus prop)
  // so no separate useEffect is needed here.

  // Focus the notes textarea (cursor at end) when entering notes-edit mode.
  useEffect(() => {
    if (editingNotes && notesRef.current) {
      const el = notesRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingNotes]);

  // Focus the new-comment textarea when the "add comment" editor opens.
  useEffect(() => {
    if (addingComment && commentRef.current) {
      commentRef.current.focus();
    }
  }, [addingComment]);

  // Focus the title input (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingTitle && titleRef.current) {
      const el = titleRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingTitle]);

  // Focus the assignee input (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingAssignee && assigneeRef.current) {
      const el = assigneeRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingAssignee]);

  const startEditDesc = useCallback(() => {
    if (descViewRef.current) setDescMinHeight(descViewRef.current.offsetHeight);
    setEditingDesc(true);
  }, []);

  const startEditNotes = useCallback(() => {
    if (notesViewRef.current)
      setNotesMinHeight(notesViewRef.current.offsetHeight);
    setEditingNotes(true);
  }, []);

  const startEditTitle = useCallback(() => {
    titleEditStartRef.current = viewDraft.title;
    setEditingTitle(true);
  }, [viewDraft.title]);

  const startEditAssignee = useCallback(() => {
    assigneeEditStartRef.current = viewDraft.assignee;
    setEditingAssignee(true);
  }, [viewDraft.assignee]);

  // Enter saves (via blur); Escape reverts to snapshot and blurs.
  const handleTitleKeyDown = useCallback((e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      setViewDraft((p) => ({ ...p, title: titleEditStartRef.current }));
      e.target.blur();
    }
  }, []);

  const handleAssigneeKeyDown = useCallback((e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      setViewDraft((p) => ({ ...p, assignee: assigneeEditStartRef.current }));
      e.target.blur();
    }
  }, []);

  // Unified Save: patches all dirty fields in one PATCH /api/issues/{id} call.
  const handleViewSave = useCallback(async () => {
    if (!data || !data.id || savingView) return;
    const body = {};
    const t = viewDraft.title.trim();
    if (t !== "" && t !== viewOriginal.title) body.title = t;
    if (viewDraft.type !== viewOriginal.type) body.type = viewDraft.type;
    if (viewDraft.priority !== viewOriginal.priority)
      body.priority = viewDraft.priority;
    if (viewDraft.description !== viewOriginal.description)
      body.description = viewDraft.description;
    if (viewDraft.assignee.trim() !== viewOriginal.assignee)
      body.assignee = viewDraft.assignee.trim();
    if (viewDraft.notes !== viewOriginal.notes) body.notes = viewDraft.notes;
    if (Object.keys(body).length === 0) return;
    setSavingView(true);
    try {
      const res = await secureFetch(
        endpoints.issues.update(data.id, { working_dir: workingDir }),
        {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast &&
          showToast({
            style: "error",
            title: respData.error || "Failed to save changes",
          });
      } else {
        if ("notes" in body) setNotes(viewDraft.notes);
        setEditingTitle(false);
        setEditingType(false);
        setEditingDesc(false);
        setEditingNotes(false);
        setEditingAssignee(false);
        showToast && showToast({ style: "success", title: "Changes saved" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to save changes",
        });
    } finally {
      setSavingView(false);
    }
  }, [
    viewDraft,
    viewOriginal,
    data && data.id,
    workingDir,
    savingView,
    showToast,
    onUpdated,
  ]);

  // Load the issue's full dependency edges, notes, and comments. The list row
  // only carries counts, so the actual data comes from /api/issues/{id}.
  // seedDraftNotes: when true, also seeds viewDraft.notes from the response so
  // the initial open has a correct draft baseline. Callers that refresh deps
  // after a dep add/remove or comment post must pass false to avoid clobbering
  // an in-progress notes edit.
  const fetchDeps = useCallback(
    async (seedDraftNotes = false) => {
      if (!workingDir || !data || !data.id) return;
      setDepsLoading(true);
      try {
        const res = await authFetch(
          endpoints.issues.show(data.id, { working_dir: workingDir }),
        );
        const respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          setDeps([]);
          setComments([]);
          setNotes("");
          if (seedDraftNotes) setViewDraft((prev) => ({ ...prev, notes: "" }));
        } else {
          const issueObj = Array.isArray(respData) ? respData[0] : respData;
          setDeps((issueObj && issueObj.dependencies) || []);
          setComments((issueObj && issueObj.comments) || []);
          const fetchedNotes = (issueObj && issueObj.notes) || "";
          setNotes(fetchedNotes);
          if (seedDraftNotes)
            setViewDraft((prev) => ({ ...prev, notes: fetchedNotes }));
        }
      } catch (_err) {
        setDeps([]);
        setComments([]);
        setNotes("");
        if (seedDraftNotes) setViewDraft((prev) => ({ ...prev, notes: "" }));
      } finally {
        setDepsLoading(false);
      }
    },
    [workingDir, data && data.id],
  );

  // Open the new-comment editor with an empty draft.
  const startAddComment = useCallback(() => {
    if (savingComment) return;
    setCommentDraft("");
    setAddingComment(true);
  }, [savingComment]);

  // Persist a new comment on blur. An empty (whitespace-only) draft just closes
  // the editor without a request. On success the comment list is refreshed via
  // fetchDeps and the parent list is notified via onUpdated.
  const handleCommentBlur = useCallback(async () => {
    const text = commentDraft.trim();
    if (!text) {
      setAddingComment(false);
      return;
    }
    setSavingComment(true);
    try {
      const res = await secureFetch(
        endpoints.issues.comments(data.id, { working_dir: workingDir }),
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ text }),
        },
      );
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast &&
          showToast({
            style: "error",
            title: respData.error || "Failed to add comment",
          });
      } else {
        setCommentDraft("");
        showToast && showToast({ style: "success", title: "Comment added" });
        await fetchDeps(false);
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to add comment",
        });
    } finally {
      setSavingComment(false);
      setAddingComment(false);
    }
  }, [
    commentDraft,
    data && data.id,
    workingDir,
    showToast,
    fetchDeps,
    onUpdated,
  ]);

  // Fetch dependencies, notes, and comments whenever a (non-create) issue is opened or switched.
  // seedDraftNotes=true so the initial open seeds viewDraft.notes from the response.
  useEffect(() => {
    setDeps([]);
    setComments([]);
    setNotes("");
    setNewDepId("");
    setNewDepType("blocks");
    if (isOpen && !creating && data && data.id) {
      fetchDeps(true);
    }
  }, [isOpen, creating, data && data.id]);

  // Add or remove a dependency edge via /api/issues/{id}/dependencies, then refresh both the
  // dependency list and the parent issue list (so counts stay current).
  const mutateDep = useCallback(
    async (action, dependsOn, depType) => {
      if (!data || !data.id || !dependsOn) return;
      setDepsBusy(true);
      try {
        const body = { depends_on: dependsOn, action };
        if (action === "add") body.type = depType || "blocks";
        const res = await secureFetch(
          endpoints.issues.dependencies(data.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          },
        );
        const respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || `Failed to ${action} dependency`,
            });
          return false;
        }
        showToast &&
          showToast({
            style: "success",
            title:
              action === "add"
                ? `Added dependency on ${dependsOn}`
                : `Removed dependency on ${dependsOn}`,
          });
        await fetchDeps(false);
        onUpdated && onUpdated();
        return true;
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} dependency`,
          });
        return false;
      } finally {
        setDepsBusy(false);
      }
    },
    [data && data.id, workingDir, showToast, fetchDeps, onUpdated],
  );

  const handleAddDep = useCallback(async () => {
    const target = newDepId.trim();
    if (!target || depsBusy) return;
    const ok = await mutateDep("add", target, newDepType);
    if (ok) setNewDepId("");
  }, [newDepId, newDepType, depsBusy, mutateDep]);

  // Change the kind of an existing edge. bd has no in-place type update, so this
  // removes the edge and re-adds it with the new type. A single combined toast
  // and refresh is issued at the end.
  const changeDepType = useCallback(
    async (dependsOn, nextType) => {
      if (!data || !data.id || !dependsOn || depsBusy) return;
      setDepsBusy(true);
      try {
        const post = (body) =>
          secureFetch(
            endpoints.issues.dependencies(data.id, { working_dir: workingDir }),
            {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(body),
            },
          );
        let res = await post({ depends_on: dependsOn, action: "remove" });
        let respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || "Failed to change dependency type",
            });
          return;
        }
        res = await post({
          depends_on: dependsOn,
          type: nextType,
          action: "add",
        });
        respData = await readBeadsResponse(res);
        if (!res.ok || respData.error) {
          showToast &&
            showToast({
              style: "error",
              title: respData.error || "Failed to change dependency type",
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title: `Changed ${dependsOn} to ${nextType}`,
            });
        }
        await fetchDeps(false);
        onUpdated && onUpdated();
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || "Failed to change dependency type",
          });
      } finally {
        setDepsBusy(false);
      }
    },
    [data && data.id, workingDir, depsBusy, showToast, fetchDeps, onUpdated],
  );

  if (!shouldRender) return null;
  if (!creating && !data) return null;

  // daisyUI's .input/.select/.textarea set their corner radius via the logical
  // longhand border-start-start-radius:var(--radius-field), which a Tailwind
  // `rounded-*` shorthand utility does NOT override. Some themes set
  // --radius-field as high as 2rem, turning these edit fields into pills. Pin
  // --radius-field so edit-mode fields keep the same subtle 0.25rem corners as
  // the panel's description/notes boxes, regardless of theme.
  const inputClass = "input input-sm w-full [--radius-field:0.25rem]";
  const selectClass = "select select-sm w-full [--radius-field:0.25rem]";
  const textareaClass = "textarea textarea-sm w-full [--radius-field:0.25rem]";
  // Block label with a small gap so it doesn't sit flush against its field.
  const labelClass = "label block mb-1";

  // Toolbar row rendered directly above the description field. Currently holds
  // the magic-wand "Improve description" button (same UX as the chat input's
  // improve-prompt action); the flex row is structured to take future markdown
  // formatting buttons (bold, italics, …). Always rendered — even in read-only
  // (view) mode — but the controls are disabled/greyed unless an editable target
  // is supplied: { text, setText } back the active field (create form or inline
  // edit draft) and `disabled` force-greys the row regardless (read-only view).
  const renderDescToolbar = ({ text, setText, disabled, editorApiRef }) => html`
    <div class="flex items-center gap-1 mb-1">
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Bold"
        aria-label="Bold"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("**", "**", "bold text")}
      >
        <${BoldIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Italic"
        aria-label="Italic"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("*", "*", "italic")}
      >
        <${ItalicIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Strikethrough"
        aria-label="Strikethrough"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("~~", "~~", "strikethrough")}
      >
        <${StrikethroughIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Inline code"
        aria-label="Inline code"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("\`", "\`", "code")}
      >
        <${InlineCodeIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Code block"
        aria-label="Code block"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection(
            "\n\`\`\`\n",
            "\n\`\`\`\n",
            "code",
          )}
      >
        <${CodeBlockIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Link"
        aria-label="Link"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.insertLink("text", "url")}
      >
        <${LinkIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Bulleted list"
        aria-label="Bulleted list"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines("- ")}
      >
        <${ListIcon} className="w-4 h-4" />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Numbered list"
        aria-label="Numbered list"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines((i) => `${i + 1}. `)}
      >
        <${NumberedListIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Heading"
        aria-label="Heading"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines("## ")}
      >
        <${HeadingIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Quote"
        aria-label="Quote"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines("> ")}
      >
        <${QuoteIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action ${improvingDesc
          ? "improving"
          : ""} ml-auto tooltip tooltip-bottom"
        onClick=${() => improveDescriptionText(text, setText)}
        onMouseDown=${(e) => e.preventDefault()}
        disabled=${disabled || improvingDesc || !text || !text.trim()}
        data-tip="Improve description with AI"
        aria-label="Improve description with AI"
      >
        ${improvingDesc
          ? html`<span class="loading loading-spinner w-4 h-4"></span>`
          : html`
              <svg
                class="w-4 h-4"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z"
                />
              </svg>
            `}
      </button>
    </div>
  `;

  // ---- field renderers (close over component state) -------------------------

  const TitleField = (mode) => {
    if (mode === "create") {
      return html` <input
        id="new-issue-title"
        type="text"
        class=${inputClass}
        placeholder="Issue title (optional — auto-generated from description)"
        value=${title}
        onInput=${(e) => setTitle(e.target.value)}
        disabled=${submitting}
      />`;
    }
    return editingTitle
      ? html` <input
          ref=${titleRef}
          type="text"
          class="${inputClass} font-semibold text-base"
          value=${viewDraft.title}
          onInput=${(e) =>
            setViewDraft((p) => ({ ...p, title: e.target.value }))}
          onBlur=${() => setEditingTitle(false)}
          onKeyDown=${handleTitleKeyDown}
          disabled=${savingView}
        />`
      : html` <h2
          class="font-semibold text-base text-mitto-text wrap-break-word cursor-text rounded px-1 -mx-1 hover:bg-mitto-input-box transition-colors block tooltip tooltip-bottom"
          onClick=${startEditTitle}
          data-tip="Click to edit"
        >
          ${viewDraft.title}
        </h2>`;
  };

  const TypeField = (mode) =>
    mode === "create"
      ? html` <select
          id="new-issue-type"
          class=${selectClass}
          value=${type}
          onInput=${(e) => setType(e.target.value)}
          disabled=${submitting}
        >
          ${ISSUE_TYPES.map((t) => html`<option value=${t}>${t}</option>`)}
        </select>`
      : html` <div class="relative" ref=${typeRef}>
          <button
            type="button"
            onClick=${() => setEditingType((o) => !o)}
            class="btn btn-ghost btn-xs inline-flex tooltip tooltip-bottom"
            data-tip="Click to change type"
          >
            ${typeBadge(viewDraft.type)}
          </button>
          ${editingType &&
          html`
            <ul
              class="menu absolute left-0 top-full mt-1 z-10 bg-base-200 rounded-box shadow-xl min-w-[140px]"
            >
              ${ISSUE_TYPES.map((t) => {
                const isCurrent = t === viewDraft.type;
                return html`
                  <li key=${t}>
                    <button
                      type="button"
                      onClick=${() => {
                        setViewDraft((p) => ({ ...p, type: t }));
                        setEditingType(false);
                      }}
                    >
                      ${typeBadge(t)}
                      <span class="flex-1">${t}</span>
                      ${isCurrent &&
                      html`<${CheckIcon} className="w-3.5 h-3.5 opacity-70" />`}
                    </button>
                  </li>
                `;
              })}
            </ul>
          `}
        </div>`;

  const PriorityField = (mode) =>
    mode === "create"
      ? html` <select
          id="new-issue-priority"
          class=${selectClass}
          value=${priority}
          onInput=${(e) => setPriority(Number(e.target.value))}
          disabled=${submitting}
        >
          ${Object.entries(PRIORITY_LABELS).map(
            ([n, label]) => html`<option value=${n}>${label}</option>`,
          )}
        </select>`
      : html` <div class="dropdown">
          <div
            tabindex="0"
            role="button"
            class="btn btn-ghost btn-xs inline-flex tooltip tooltip-bottom"
            data-tip="Click to change priority"
          >
            ${priorityBadge(viewDraft.priority)}
          </div>
          <ul
            tabindex="0"
            class="dropdown-content menu mt-1 z-10 bg-base-200 rounded-box shadow-xl min-w-[140px]"
          >
            ${Object.entries(PRIORITY_LABELS).map(([n, label]) => {
              const num = Number(n);
              const isCurrent = num === viewDraft.priority;
              return html`
                <li key=${n}>
                  <button
                    type="button"
                    onClick=${(ev) => {
                      setViewDraft((p) => ({ ...p, priority: num }));
                      ev.currentTarget.blur();
                      if (document.activeElement) document.activeElement.blur();
                    }}
                  >
                    ${priorityBadge(num)}
                    <span class="flex-1">${label}</span>
                    ${isCurrent &&
                    html`<${CheckIcon} className="w-3.5 h-3.5 opacity-70" />`}
                  </button>
                </li>
              `;
            })}
          </ul>
        </div>`;

  // DescriptionField is self-contained (includes label + wrapper) to avoid
  // Fragment-induced CodeMirror remount cycles.
  const DescriptionField = (mode) => {
    if (mode === "create") {
      return html` <div>
        <label class=${labelClass} for="new-issue-desc"
          >Description <span class="text-red-400">*</span></label
        >
        ${renderDescToolbar({
          text: description,
          setText: (v) => {
            setDescription(v);
            createEditorApiRef.current?.setValue(v);
          },
          disabled: submitting,
          editorApiRef: createEditorApiRef,
        })}
        <${CodeEditorField}
          value=${description}
          onChange=${(v) => setDescription(v)}
          onBlur=${(v) => setDescription(v)}
          disabled=${submitting}
          darkMode=${false}
          lineNumbers=${false}
          lineWrapping=${true}
          highlightActiveLine=${false}
          className="input-font-target"
          minHeight=${160}
          editorApiRef=${createEditorApiRef}
          autoFocus=${true}
        />
      </div>`;
    }
    return html` <div>
      <label class=${labelClass}>Description</label>
      ${renderDescToolbar(
        editingDesc
          ? {
              text: viewDraft.description,
              setText: (v) => {
                setViewDraft((p) => ({ ...p, description: v }));
                detailEditorApiRef.current?.setValue(v);
              },
              disabled: savingView,
              editorApiRef: detailEditorApiRef,
            }
          : { text: "", setText: () => {}, disabled: true },
      )}
      ${editingDesc
        ? html` <${CodeEditorField}
            value=${viewDraft.description}
            onChange=${(v) => setViewDraft((p) => ({ ...p, description: v }))}
            onBlur=${() => setEditingDesc(false)}
            disabled=${savingView}
            darkMode=${false}
            lineNumbers=${false}
            lineWrapping=${true}
            highlightActiveLine=${false}
            className="input-font-target"
            minHeight=${descMinHeight || 0}
            autoFocus=${true}
            editorApiRef=${detailEditorApiRef}
          />`
        : html` <div
            ref=${descViewRef}
            class="card border border-mitto-border rounded p-3 bg-mitto-input-box cursor-text hover:border-mitto-text-secondary transition-colors relative block tooltip tooltip-bottom"
            onClick=${startEditDesc}
            data-tip="Click to edit"
          >
            ${viewDraft.description
              ? md
                ? html`<div
                    class="markdown-content text-mitto-text text-sm max-w-none"
                    dangerouslySetInnerHTML=${{ __html: md }}
                  />`
                : html`<pre
                    class="whitespace-pre-wrap wrap-break-word text-sm text-mitto-text"
                  >
${viewDraft.description}</pre
                  >`
              : html`<span class="text-sm text-mitto-text-secondary italic"
                  >No description. Click to add one.</span
                >`}
          </div>`}
    </div>`;
  };

  const AssigneeField = (mode) => {
    if (mode === "create") {
      return html` <input
        id="new-issue-assignee"
        type="text"
        class=${inputClass}
        placeholder="Assignee"
        value=${createAssignee}
        disabled=${submitting}
        onInput=${(e) => setCreateAssignee(e.target.value)}
      />`;
    }
    return editingAssignee
      ? html` <input
          ref=${assigneeRef}
          type="text"
          class=${inputClass}
          placeholder="Assignee (empty to clear)"
          value=${viewDraft.assignee}
          onInput=${(e) =>
            setViewDraft((p) => ({ ...p, assignee: e.target.value }))}
          onBlur=${() => setEditingAssignee(false)}
          onKeyDown=${handleAssigneeKeyDown}
          disabled=${savingView}
        />`
      : html` <div
          class="text-sm text-mitto-text wrap-break-word cursor-text hover:text-mitto-text-300 transition-colors flex items-center gap-2 tooltip tooltip-bottom"
          onClick=${startEditAssignee}
          data-tip="Click to edit"
        >
          ${viewDraft.assignee
            ? html`<span>${viewDraft.assignee}</span>`
            : html`<span class="text-mitto-text-secondary italic"
                >Unassigned. Click to set.</span
              >`}
        </div>`;
  };

  const NotesField = (mode) => {
    if (mode === "create") {
      return html` <textarea
        id="new-issue-notes"
        class="${textareaClass} resize-y min-h-[80px]"
        placeholder="Optional notes"
        disabled=${submitting}
        onInput=${(e) => setCreateNotes(e.target.value)}
        value=${createNotes}
      ></textarea>`;
    }
    if (depsLoading) {
      return html`<div
        class="flex items-center gap-2 text-xs text-mitto-text-secondary"
      >
        <span class="loading loading-spinner w-3 h-3"></span> Loading…
      </div>`;
    }
    return editingNotes
      ? html` <textarea
          ref=${notesRef}
          class="${textareaClass} resize-y"
          rows="4"
          style=${notesMinHeight ? `min-height:${notesMinHeight}px` : null}
          placeholder="Add notes…"
          value=${viewDraft.notes}
          onInput=${(e) =>
            setViewDraft((p) => ({ ...p, notes: e.target.value }))}
          onBlur=${() => setEditingNotes(false)}
          disabled=${savingView}
        ></textarea>`
      : html` <div
          ref=${notesViewRef}
          class="card border-l-2 border-l-amber-500/70 bg-amber-500/10 rounded-r p-2 pl-3 cursor-text hover:border-l-amber-500 transition-colors relative block tooltip tooltip-bottom"
          onClick=${startEditNotes}
          data-tip="Click to edit"
        >
          ${viewDraft.notes && viewDraft.notes.trim()
            ? commentBody(viewDraft.notes)
            : html`<span class="text-sm text-mitto-text-secondary italic"
                >No notes. Click to add.</span
              >`}
        </div>`;
  };

  const DependenciesField = (mode) => {
    if (mode === "create") {
      return html` <datalist id="beads-create-dep-options">
          ${(allIssues || [])
            .filter((i) => !createDeps.some((d) => d.id === i.id))
            .map(
              (i) =>
                html`<option key=${i.id} value=${i.id}>${i.title}</option>`,
            )}
        </datalist>
        <ul class="list mt-1">
          ${createDeps.map(
            (d) => html`
              <li key=${d.id} class="list-row items-center px-2 py-1 gap-2">
                <select
                  class="select select-xs beads-dep-type-select shrink-0"
                  value=${d.type || "blocks"}
                  disabled=${submitting}
                  onInput=${(e) =>
                    setCreateDeps((prev) =>
                      prev.map((x) =>
                        x.id === d.id ? { ...x, type: e.target.value } : x,
                      ),
                    )}
                >
                  ${DEP_TYPES.map(
                    (t) => html`<option value=${t}>${t}</option>`,
                  )}
                </select>
                <span class="list-col-grow font-mono text-xs min-w-0 truncate"
                  >${d.id}</span
                >
                <button
                  type="button"
                  onClick=${() => removeCreateDep(d.id)}
                  disabled=${submitting}
                  class="btn btn-ghost btn-square btn-xs shrink-0 inline-flex tooltip tooltip-bottom"
                  data-tip="Remove dependency"
                  aria-label="Remove dependency"
                >
                  <${CloseIcon} className="w-3.5 h-3.5" />
                </button>
              </li>
            `,
          )}
        </ul>
        <div class="join w-full mt-1">
          <select
            class="select select-xs beads-dep-type-select join-item"
            value=${createNewDepType}
            disabled=${submitting}
            onInput=${(e) => setCreateNewDepType(e.target.value)}
          >
            ${DEP_TYPES.map((t) => html`<option value=${t}>${t}</option>`)}
          </select>
          <input
            type="text"
            list="beads-create-dep-options"
            placeholder="issue id…"
            value=${createNewDepId}
            disabled=${submitting}
            onInput=${(e) => setCreateNewDepId(e.target.value)}
            onKeyDown=${(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addCreateDep();
              }
            }}
            class="input input-xs flex-1 min-w-0 join-item"
          />
          <button
            type="button"
            onClick=${addCreateDep}
            aria-disabled=${!createNewDepId.trim() || submitting
              ? "true"
              : "false"}
            class="btn btn-ghost btn-square btn-xs shrink-0 join-item inline-flex tooltip tooltip-bottom ${!createNewDepId.trim() ||
            submitting
              ? "opacity-40 pointer-events-none"
              : ""}"
            data-tip="Add dependency"
            aria-label="Add dependency"
          >
            <${PlusIcon} className="w-3.5 h-3.5" />
          </button>
        </div>`;
    }
    return html` <datalist id="beads-dep-options">
        ${(allIssues || [])
          .filter((i) => i.id !== data.id && !deps.some((d) => d.id === i.id))
          .map(
            (i) => html`<option key=${i.id} value=${i.id}>${i.title}</option>`,
          )}
      </datalist>
      ${depsLoading
        ? html`<div
            class="flex items-center gap-2 text-xs text-mitto-text-secondary"
          >
            <span class="loading loading-spinner w-3 h-3"></span> Loading…
          </div>`
        : html`
            <div class="beads-deps-grid">
              ${deps.length === 0 &&
              html`<span
                class="beads-dep-empty text-xs text-mitto-text-secondary italic py-1"
                >No dependencies.</span
              >`}
              ${deps.map(
                (d) => html`
              <${Fragment} key=${d.id}>
                <span class="beads-dep-badge">${depStatusBadge(d.status)}</span>
                <select
                  class="select select-xs beads-dep-type-select"
                  value=${d.dependency_type || "blocks"}
                  disabled=${depsBusy}
                  onInput=${(e) => {
                    if (e.target.value !== (d.dependency_type || "blocks"))
                      changeDepType(d.id, e.target.value);
                  }}
                >
                  ${DEP_TYPES.map((t) => html`<option value=${t}>${t}</option>`)}
                </select>
                <button
                  type="button"
                  onClick=${() => onSelectIssue && onSelectIssue((allIssues || []).find((i) => i.id === d.id) || d)}
                  class="input input-xs w-full min-w-0 text-left hover:underline tooltip tooltip-bottom"
                  data-tip=${"Open " + d.id}
                >
                  <span class="font-mono text-xs text-mitto-accent-400 shrink-0">${d.id}</span>
                  <span class="truncate text-xs text-mitto-text min-w-0">${d.title}</span>
                </button>
                <button
                  type="button"
                  onClick=${() => {
                    if (depsBusy) return;
                    mutateDep("remove", d.id);
                  }}
                  aria-disabled=${depsBusy ? "true" : "false"}
                  class="btn btn-ghost btn-square btn-xs group inline-flex tooltip tooltip-bottom ${depsBusy ? "opacity-40 pointer-events-none" : ""}"
                  data-tip="Remove dependency"
                  aria-label="Remove dependency"
                >
                  <${CloseIcon} className="w-3.5 h-3.5 group-hover:text-red-400" />
                </button>
              </${Fragment}>
            `,
              )}
              <span class="beads-dep-badge"></span>
              <select
                class="select select-xs beads-dep-type-select"
                value=${newDepType}
                disabled=${depsBusy}
                onInput=${(e) => setNewDepType(e.target.value)}
              >
                ${DEP_TYPES.map((t) => html`<option value=${t}>${t}</option>`)}
              </select>
              <input
                type="text"
                list="beads-dep-options"
                placeholder="issue id…"
                value=${newDepId}
                disabled=${depsBusy}
                onInput=${(e) => setNewDepId(e.target.value)}
                onKeyDown=${(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault();
                    handleAddDep();
                  }
                }}
                class="input input-xs w-full min-w-0"
              />
              <button
                type="button"
                onClick=${() => {
                  if (depsBusy || !newDepId.trim()) return;
                  handleAddDep();
                }}
                aria-disabled=${depsBusy || !newDepId.trim() ? "true" : "false"}
                class="btn btn-ghost btn-square btn-xs inline-flex tooltip tooltip-bottom ${depsBusy ||
                !newDepId.trim()
                  ? "opacity-40 pointer-events-none"
                  : ""}"
                data-tip="Add dependency"
                aria-label="Add dependency"
              >
                ${depsBusy
                  ? html`<span
                      class="loading loading-spinner w-3.5 h-3.5"
                    ></span>`
                  : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
              </button>
            </div>
          `}`;
  };

  return html`
    <${Fragment}>
      <!-- Dock-mode daisyUI drawer docked to the right edge of the beads view
           area (drawer-dock; see styles.css). NO dimming backdrop: a full-area
           composited overlay over the beads list is exactly what dropped the
           list's GPU backing store and blanked it on pointer-move (mitto-cdf),
           so dock mode confines the panel to its own width and leaves the list
           to its left under no composited layer. z-60 keeps it above content.
             Small screens (confined): full width, but capped at 85vw by the dock
               media query so a peek of the list always remains on the left.
             Desktop normal: 40rem wide, capped at 85% of the beads view so the
               list always stays visible on the panel's left.
             Expanded (fullscreen) / standalone: fills the whole area — on desktop
               the beads view, on small screens the viewport (--dock-maxw:100%
               lifts the media-query cap). -->
      <${Drawer}
        dock
        side="end"
        isClosing=${isClosing}
        onClose=${handleClose}
        zClass="z-60"
        rootStyle=${
          fullscreen
            ? "--dock-w:100%;--dock-maxw:100%"
            : isMobile
              ? "--dock-w:100%"
              : "--dock-w:40rem;--dock-maxw:85%"
        }
        widthClass="w-full"
        panelClass="bg-mitto-sidebar shrink-0 h-full flex flex-col border-l border-mitto-border-1"
      >
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border shrink-0">
        <div class="flex-1 min-w-0">
          ${
            creating
              ? html`<${Fragment}>
                ${TitleField("create")}
                ${createParentId ? html`<div class="font-mono text-xs text-mitto-text-secondary">in ${createParentId}</div>` : null}
              </${Fragment}>`
              : html`
                  <div class="flex items-center gap-1">
                    <span class="font-mono text-xs text-mitto-text-secondary"
                      >${data.id}</span
                    >
                    <button
                      type="button"
                      onClick=${async () => {
                        const ok = await copyToClipboard(data.id);
                        showToast &&
                          showToast(
                            ok
                              ? { style: "success", title: `Copied ${data.id}` }
                              : {
                                  style: "error",
                                  title: "Failed to copy issue ID",
                                },
                          );
                      }}
                      class="btn btn-ghost btn-xs btn-square inline-flex tooltip tooltip-bottom"
                      data-tip="Copy issue ID ${data.id}"
                      aria-label="Copy issue ID ${data.id}"
                    >
                      <${CopyIcon} className="w-3.5 h-3.5" />
                    </button>
                  </div>
                  ${TitleField("view")}
                `
          }
        </div>
        ${
          !creating &&
          data &&
          html`
            <button
              type="button"
              onClick=${openPanelMenu}
              class="btn btn-ghost btn-square btn-sm shrink-0 inline-flex tooltip tooltip-bottom"
              data-tip="More actions"
              aria-label="More actions"
            >
              <${EllipsisIcon} className="w-5 h-5" />
            </button>
          `
        }
        <button
          onClick=${() => setFullscreen((f) => !f)}
          class="btn btn-ghost btn-square btn-sm shrink-0 inline-flex tooltip tooltip-bottom"
          data-tip=${fullscreen ? "Exit fullscreen" : "Fullscreen"}
          aria-label=${fullscreen ? "Exit fullscreen" : "Fullscreen"}
        >
          ${
            fullscreen
              ? html`<${CollapseIcon} className="w-5 h-5" />`
              : html`<${ExpandIcon} className="w-5 h-5" />`
          }
        </button>
      </div>

      <div class="flex-1 overflow-y-auto p-4 space-y-4">
        ${
          creating
            ? html`
            <${Fragment}>
              <div class="flex flex-wrap gap-2 items-center">
                <span class="${labelClass} shrink-0">Type</span>
                ${TypeField("create")}
                <span class="${labelClass} shrink-0">Priority</span>
                ${PriorityField("create")}
              </div>

              <div class="grid grid-cols-2 gap-3">
                ${
                  createParentId
                    ? html`
                        <div>
                          <label class=${labelClass} for="new-issue-parent"
                            >Parent</label
                          >
                          <input
                            id="new-issue-parent"
                            type="text"
                            class="${inputClass} font-mono"
                            value=${createParentId}
                            readonly
                            aria-readonly="true"
                            title="This issue will be created as a child of ${createParentId}"
                            data-testid="beads-create-parent"
                          />
                        </div>
                      `
                    : null
                }
                <div>
                  <label class=${labelClass} for="new-issue-assignee">Assignee</label>
                  ${AssigneeField("create")}
                </div>
              </div>

              ${DescriptionField("create")}

              <fieldset class="fieldset">
                <legend class="fieldset-legend">Dependencies</legend>
                ${DependenciesField("create")}
              </fieldset>

              <fieldset class="fieldset">
                <legend class="fieldset-legend">Notes</legend>
                ${NotesField("create")}
              </fieldset>
            </${Fragment}>
          `
            : html`
                <div class="flex flex-wrap gap-2 items-center">
                  ${TypeField("view")} ${statusBadge(data.status)}
                  ${PriorityField("view")}
                </div>

                <div class="grid grid-cols-2 gap-3">
                  <div>
                    <label class=${labelClass}>Assignee</label>
                    ${AssigneeField("view")}
                  </div>
                  ${labelValue("Owner", data.owner)}
                  ${labelValue(
                    "Created",
                    data.created_at &&
                      new Date(data.created_at).toLocaleDateString(),
                  )}
                  ${labelValue(
                    "Updated",
                    data.updated_at &&
                      new Date(data.updated_at).toLocaleDateString(),
                  )}
                  ${data.parent &&
                  labelValue(
                    "Parent",
                    html`
                      <button
                        type="button"
                        onClick=${() =>
                          onSelectIssue &&
                          onSelectIssue(
                            (allIssues || []).find(
                              (i) => i.id === data.parent,
                            ) || { id: data.parent },
                          )}
                        class="font-mono text-mitto-accent-400 hover:text-mitto-accent-300 hover:underline text-left tooltip tooltip-bottom"
                        data-tip=${"Open " + data.parent}
                      >
                        ${data.parent}
                      </button>
                    `,
                  )}
                </div>

                ${DescriptionField("view")}
                ${subtasks.length > 0 &&
                html`
                  <fieldset class="fieldset">
                    <legend class="fieldset-legend">
                      Subtasks (${subtasks.length})
                    </legend>
                    <ul class="space-y-1">
                      ${subtasks.map(
                        (c) => html`
                          <li key=${c.id}>
                            <button
                              type="button"
                              onClick=${() => onSelectIssue && onSelectIssue(c)}
                              class="btn btn-ghost btn-xs w-full justify-start inline-flex tooltip tooltip-bottom"
                              data-tip="Open ${c.id}"
                            >
                              ${statusBadge(c.status)}
                              <span
                                class="font-mono text-mitto-text-secondary text-xs"
                                >${c.id}</span
                              >
                              <span class="truncate">${c.title}</span>
                            </button>
                          </li>
                        `,
                      )}
                    </ul>
                  </fieldset>
                `}

                <fieldset class="fieldset">
                  <legend class="fieldset-legend">Dependencies</legend>
                  ${DependenciesField("view")}
                </fieldset>

                <fieldset class="fieldset">
                  <legend class="fieldset-legend">
                    Comments${comments.length ? ` (${comments.length})` : ""}
                  </legend>
                  ${depsLoading
                    ? html`
                        <div
                          class="flex items-center gap-2 text-xs text-mitto-text-secondary"
                        >
                          <span class="loading loading-spinner w-3 h-3"></span>
                          Loading…
                        </div>
                      `
                    : html`
                  <${Fragment}>
                    ${
                      comments.length === 0
                        ? html`<div
                            class="text-xs text-mitto-text-secondary italic"
                          >
                            No comments.
                          </div>`
                        : html`
                            <ul class="space-y-2">
                              ${[...comments]
                                .sort(
                                  (a, b) =>
                                    new Date(a.created_at) -
                                    new Date(b.created_at),
                                )
                                .map(
                                  (cm) => html`
                                    <li
                                      key=${cm.id}
                                      class="border-l-2 border-l-mitto-accent-500/70 bg-mitto-accent-500/10 rounded-r p-2 pl-3"
                                    >
                                      <div
                                        class="flex items-center justify-between gap-2 mb-1"
                                      >
                                        <span
                                          class="text-xs font-medium text-mitto-text"
                                          >${cm.author || "Unknown"}</span
                                        >
                                        <span
                                          class="text-xs text-mitto-text-secondary"
                                          title=${cm.created_at}
                                          >${cm.created_at
                                            ? new Date(
                                                cm.created_at,
                                              ).toLocaleString()
                                            : ""}</span
                                        >
                                      </div>
                                      ${commentBody(cm.text)}
                                    </li>
                                  `,
                                )}
                            </ul>
                          `
                    }
                    ${
                      addingComment
                        ? html`
                            <textarea
                              ref=${commentRef}
                              class="${textareaClass} resize-y mt-2"
                              rows="3"
                              placeholder="Add a comment…"
                              value=${commentDraft}
                              onInput=${(e) => setCommentDraft(e.target.value)}
                              onBlur=${handleCommentBlur}
                              disabled=${savingComment}
                            ></textarea>
                          `
                        : html`
                            <button
                              type="button"
                              onClick=${startAddComment}
                              disabled=${savingComment}
                              class="btn btn-ghost btn-xs mt-2 inline-flex tooltip tooltip-bottom"
                              data-tip="Add comment"
                            >
                              ${savingComment
                                ? html`<span
                                    class="loading loading-spinner w-3.5 h-3.5"
                                  ></span>`
                                : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                              <span>Add comment</span>
                            </button>
                          `
                    }
                  </${Fragment}>
                `}
                </fieldset>

                <fieldset class="fieldset">
                  <legend class="fieldset-legend">Notes</legend>
                  ${NotesField("view")}
                </fieldset>
              `
        }
      </div>

      ${
        (creating || data) &&
        html`
          <div
            class="flex justify-end gap-3 p-3 border-t border-mitto-border shrink-0"
          >
            <button
              type="button"
              onClick=${handleClose}
              disabled=${creating ? submitting : savingView}
              class="btn btn-ghost btn-sm inline-flex tooltip tooltip-top"
              data-tip="Close"
            >
              Close
            </button>
            <button
              type="button"
              onClick=${creating ? handleSave : handleViewSave}
              disabled=${creating
                ? !description.trim() || submitting
                : !viewDirty || savingView}
              class="btn btn-primary btn-sm inline-flex tooltip tooltip-top"
              data-tip="Save changes"
            >
              ${(creating ? submitting : savingView)
                ? html`<span class="loading loading-spinner w-4 h-4"></span>`
                : null}
              Save
            </button>
          </div>
        `
      }
      <//>
      ${
        panelMenu &&
        html`
          <${ContextMenu}
            x=${panelMenu.x}
            y=${panelMenu.y}
            items=${panelMenuItems}
            onClose=${() => setPanelMenu(null)}
          />
        `
      }
      <${ConfirmDialog}
        isOpen=${confirmDiscard}
        title="Discard changes?"
        message="You have unsaved changes. Discard them and close?"
        confirmLabel="Discard"
        cancelLabel="Keep editing"
        confirmVariant="danger"
        onConfirm=${handleDiscardAndClose}
        onCancel=${() => setConfirmDiscard(false)}
      />
    </${Fragment}>
  `;
}

// ---- Standalone single-issue viewer -----------------------------------------

/**
 * BeadsIssueView renders a single beads issue as a docked side panel overlaid
 * on the conversation (it returns a Fragment whose BeadsDetailPanel is a
 * dock-mode drawer, so it does not reflow the conversation behind it). Opened
 * when the user follows a conversation's "Linked beads issue" link. The issue
 * is fetched from /api/issues/{id}; clicking a dependency navigates within the
 * viewer via another show fetch. Close (X) / outside-click returns to the
 * conversation via onReturnToConversation. The expand toggle in the panel
 * header lets the user widen it to fill the area.
 */
export function BeadsIssueView({
  workingDir,
  issueId,
  selectNonce,
  showToast,
  onFetchBeadsPrompts,
  onRunBeadsPrompt,
  onReturnToConversation,
}) {
  // currentIssueId tracks in-viewer navigation (e.g. clicking a dep id).
  const [currentIssueId, setCurrentIssueId] = useState(issueId);
  const [issue, setIssue] = useState(null);
  const [statusBusy, setStatusBusy] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deletingIssue, setDeletingIssue] = useState(false);
  // Bumped to re-fetch the current issue after a status/defer/dep change.
  const [refreshNonce, setRefreshNonce] = useState(0);
  // Full issue list for the workspace, used to compute the current issue's
  // subtasks (children). /api/issues/{id} does not return children, so without
  // the list the Subtasks section would never render here even though it does
  // in the Tasks list view (which passes its already-loaded list as allIssues).
  const [listIssues, setListIssues] = useState([]);

  // Reset to the externally-requested issue when the prop changes.
  useEffect(() => {
    setCurrentIssueId(issueId);
  }, [issueId, selectNonce]);

  // Fetch the current issue from /api/issues/{id}.
  useEffect(() => {
    if (!workingDir || !currentIssueId) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch(
          endpoints.issues.show(currentIssueId, { working_dir: workingDir }),
        );
        const data = await readBeadsResponse(res);
        if (cancelled) return;
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || "Failed to load issue",
            });
        } else {
          const issueObj = Array.isArray(data) ? data[0] : data;
          setIssue(issueObj || null);
        }
      } catch (_err) {
        if (!cancelled)
          showToast &&
            showToast({ style: "error", title: "Failed to load issue" });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [workingDir, currentIssueId, refreshNonce]);

  // Fetch the full issue list so BeadsDetailPanel can derive subtasks for the
  // current issue. Re-fetched on refreshNonce so children stay current after a
  // status/defer/delete change. Non-fatal on failure: the single issue still
  // loads; only the Subtasks section is omitted.
  useEffect(() => {
    if (!workingDir) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch(
          endpoints.issues.list({ working_dir: workingDir }),
        );
        const data = await readBeadsResponse(res);
        if (cancelled) return;
        if (res.ok && !data.error && Array.isArray(data)) {
          setListIssues(data);
        }
      } catch (_err) {
        // Non-fatal: subtasks just won't render.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [workingDir, refreshNonce]);

  const refresh = useCallback(() => setRefreshNonce((n) => n + 1), []);

  // In-viewer navigation: clicking a dep id re-fetches that issue.
  const handleSelectIssue = useCallback((depObj) => {
    const id = depObj?.id;
    if (id) setCurrentIssueId(id);
  }, []);

  const handleToggleStatus = useCallback(
    async (iss) => {
      if (!iss) return;
      const action = iss.status === "closed" ? "reopen" : "close";
      setStatusBusy(true);
      try {
        const res = await secureFetch(
          endpoints.issues.status(iss.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action }),
          },
        );
        const data = await readBeadsResponse(res);
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || `Failed to ${action} issue`,
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title:
                action === "close" ? `Closed ${iss.id}` : `Reopened ${iss.id}`,
            });
          refresh();
        }
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} issue`,
          });
      } finally {
        setStatusBusy(false);
      }
    },
    [workingDir, showToast, refresh],
  );

  const handleToggleDefer = useCallback(
    async (iss) => {
      if (!iss) return;
      const action = iss.status === "deferred" ? "undefer" : "defer";
      setStatusBusy(true);
      try {
        const res = await secureFetch(
          endpoints.issues.status(iss.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action }),
          },
        );
        const data = await readBeadsResponse(res);
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || `Failed to ${action} issue`,
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title:
                action === "defer"
                  ? `Deferred ${iss.id}`
                  : `Undeferred ${iss.id}`,
            });
          refresh();
        }
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} issue`,
          });
      } finally {
        setStatusBusy(false);
      }
    },
    [workingDir, showToast, refresh],
  );

  const confirmDeleteIssue = useCallback(async () => {
    if (!deleteTarget) return;
    const id = deleteTarget.id;
    setDeletingIssue(true);
    try {
      const res = await secureFetch(
        endpoints.issues.remove(id, { working_dir: workingDir }),
        {
          method: "DELETE",
        },
      );
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast &&
          showToast({
            style: "error",
            title: data.error || "Failed to delete issue",
          });
      } else {
        showToast && showToast({ style: "success", title: `Deleted ${id}` });
        onReturnToConversation && onReturnToConversation();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to delete issue",
        });
    } finally {
      setDeletingIssue(false);
      setDeleteTarget(null);
    }
  }, [deleteTarget, workingDir, showToast, onReturnToConversation]);

  return html`
    <${Fragment}>
      <${BeadsDetailPanel}
        issue=${issue}
        allIssues=${listIssues}
        isCreating=${false}
        workingDir=${workingDir}
        initialFullscreen=${false}
        onClose=${onReturnToConversation}
        onUpdated=${refresh}
        showToast=${showToast}
        onFetchPrompts=${onFetchBeadsPrompts}
        onRunPrompt=${onRunBeadsPrompt}
        onDelete=${(iss) => setDeleteTarget(iss)}
        onToggleStatus=${handleToggleStatus}
        onToggleDefer=${handleToggleDefer}
        statusBusy=${statusBusy}
        onSelectIssue=${handleSelectIssue}
      />
      <${ConfirmDialog}
        isOpen=${!!deleteTarget}
        title="Delete issue"
        message=${deleteTarget ? `This will permanently delete ${deleteTarget.id} — "${deleteTarget.title}". This cannot be undone.` : ""}
        confirmLabel="Delete"
        cancelLabel="Cancel"
        confirmVariant="danger"
        isLoading=${deletingIssue}
        onConfirm=${confirmDeleteIssue}
        onCancel=${() => setDeleteTarget(null)}
      />
    </${Fragment}>
  `;
}

// ---- Main BeadsView ---------------------------------------------------------

/**
 * BeadsView renders the Beads issue list and detail panel for a workspace.
 * @param {string} workingDir - Absolute path of the workspace directory.
 * @param {function} showToast - Toast notification helper from parent.
 * @param {function} onFetchBeadsPrompts - Async (workingDir) => prompts whose
 *        `menus` list includes `beadsIssues`; populates the per-issue context menu.
 * @param {function} onRunBeadsPrompt - (prompt, issue) => starts a new
 *        conversation seeded with the prompt text and the issue's context.
 * @param {function} onFetchBeadsListPrompts - Async (workingDir) => prompts whose
 *        `menus` list includes `beadsList`; populates the list-level prompts
 *        dropdown in the footer toolbar.
 * @param {function} onRunBeadsListPrompt - (prompt) => starts a new conversation
 *        seeded with the prompt text alone (these prompts take no parameters).
 * @param {function} onShowSidebar - Opens the conversations sidebar (mobile);
 *        used by the header hamburger button to return to the conversation list.
 */

// Swipeable wrapper for a single beads issue row. Mirrors the conversation
// list's swipe-to-action: swipe left to close an open issue (green/check) or
// to delete an already-closed issue (red/trash).
function BeadsIssueRow({
  issue,
  bgTone,
  borderTone,
  onSelect,
  onContextMenu,
  onClose,
  onDelete,
  children,
}) {
  // Closed issues can't be closed again — swipe deletes them instead (mirrors
  // SessionItem, where the archived tab swaps archive for delete).
  const isSwipeToDelete = issue.status === "closed";

  const handleSwipeAction = useCallback(() => {
    if (isSwipeToDelete) onDelete();
    else onClose();
  }, [isSwipeToDelete, onClose, onDelete]);

  const {
    swipeOffset,
    isSwiping,
    isSwipingRef,
    isRevealed,
    containerProps,
    reset,
    triggerAction,
  } = useSwipeToAction({
    onAction: handleSwipeAction,
    threshold: 0.5,
    revealWidth: 80,
    disabled: false,
  });

  // Only select on a genuine tap (not a swipe); a revealed row resets first.
  const handleClick = useCallback(() => {
    if (isSwipingRef.current) return;
    if (isRevealed) {
      reset();
      return;
    }
    onSelect();
  }, [isSwipingRef, isRevealed, reset, onSelect]);

  const absOffset = Math.abs(swipeOffset);

  return html`
    <div
      class="beads-item-container relative overflow-hidden"
      ...${containerProps}
    >
      <!-- Swipe action background (revealed when swiping left) -->
      <div
        class="absolute inset-0 ${isSwipeToDelete
          ? "bg-red-600"
          : "bg-green-700"} flex items-center justify-end pr-6 transition-opacity"
        style="opacity: ${isRevealed || absOffset > 20 ? 1 : 0}"
      >
        <button
          onClick=${(e) => {
            e.preventDefault();
            e.stopPropagation();
            triggerAction();
          }}
          class="p-3 rounded-full ${isSwipeToDelete
            ? "bg-red-700 hover:bg-red-800"
            : "bg-green-900"} transition-colors tooltip tooltip-left"
          data-tip=${isSwipeToDelete ? "Delete" : "Close"}
          aria-label=${isSwipeToDelete ? "Delete" : "Close"}
        >
          ${isSwipeToDelete
            ? html`<${TrashIcon} className="w-5 h-5 text-white" />`
            : html`<${CheckIcon} className="w-5 h-5 text-white" />`}
        </button>
      </div>
      <!-- Swipeable content (the original list-row card) -->
      <div
        data-has-context-menu
        onClick=${handleClick}
        onContextMenu=${onContextMenu}
        class="list-row cursor-pointer select-none ${bgTone} ${borderTone} ${isSwiping
          ? ""
          : "transition-all duration-200"}"
        style="transform: translateX(${swipeOffset}px);"
      >
        ${children}
      </div>
    </div>
  `;
}

export function BeadsView({
  workingDir,
  showToast,
  dismissToast,
  onFetchBeadsPrompts,
  onRunBeadsPrompt,
  onFetchBeadsListPrompts,
  onRunBeadsListPrompt,
  onShowSidebar,
  onOpenConfig,
  issueSessionMap = {},
  issueStreamingSet = new Set(),
  onOpenConversation,
  onLaunchPrompt,
  initialCreateNonce = 0,
  initialRefreshNonce = 0,
  initialCleanupNonce = 0,
}) {
  const [issues, setIssues] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [selectedIssue, setSelectedIssue] = useState(null);
  const [isCreating, setIsCreating] = useState(false);
  // When the create panel is opened via an epic's "+" button, this holds the
  // epic's id so the new issue is created as that epic's child (parent).
  const [createParent, setCreateParent] = useState(null);

  // The type and search filters are initialized from localStorage so that the
  // user's applied criteria are restored when they navigate away from the Beads
  // view and return within the same session. Changes are persisted via the
  // effect below. The status toggles are deliberately NOT persisted to
  // localStorage — they live only in memory (see `beadsStatusToggles`).
  const [typeFilter, setTypeFilter] = useState(() => getBeadsFilters().type);
  const [search, setSearch] = useState(() => getBeadsFilters().search);

  // Status filter toggles, seeded from the in-memory module state so the
  // selection survives navigating away and back within the same session.
  const [statusToggles, setStatusToggles] = useState(() => ({
    ...beadsStatusToggles,
  }));

  // Toggle a single status on/off. The new state is also written back to the
  // module-level store so it persists across remounts within the session.
  const toggleStatus = useCallback((key) => {
    setStatusToggles((prev) => {
      const next = { ...prev, [key]: !prev[key] };
      beadsStatusToggles = next;
      return next;
    });
  }, []);

  // Toolbar tooltips can't use daisyUI's CSS tooltip: the toolbar lives inside
  // two `overflow-hidden` ancestors (panel root + column), so a centered
  // tooltip-bottom bubble on a left-edge button (e.g. the status filters) is
  // clipped at the panel edge. Render those through a body-level PortalTooltip
  // instead, anchored at the cursor and clamped to the viewport — same approach
  // as the SessionItem row tooltip. `data-tip`/`aria-label` are kept on the
  // buttons (test selectors and a11y), but the `tooltip` classes are dropped so
  // the clipped CSS bubble no longer renders.
  const [toolbarTip, setToolbarTip] = useState(null);
  const toolbarTipTimerRef = useRef(null);
  const showToolbarTip = useCallback((e, text) => {
    if (!BEADS_SUPPORTS_HOVER || !text) return;
    const x = e.clientX;
    const y = e.clientY;
    clearTimeout(toolbarTipTimerRef.current);
    toolbarTipTimerRef.current = setTimeout(
      () => setToolbarTip({ x, y, text }),
      BEADS_TOOLTIP_DELAY_MS,
    );
  }, []);
  const hideToolbarTip = useCallback(() => {
    clearTimeout(toolbarTipTimerRef.current);
    setToolbarTip(null);
  }, []);
  useEffect(() => () => clearTimeout(toolbarTipTimerRef.current), []);

  // Persist type and search filters whenever they change.
  useEffect(() => {
    setBeadsFilters({ type: typeFilter, search });
  }, [typeFilter, search]);

  // Grouping toggle (persisted) and per-epic expand/collapse state (persisted).
  // Status toggles are deliberately in-memory only; these are separate.
  const [grouping, setGrouping] = useState(() => getBeadsGrouping().enabled);
  // Epics are expanded by default; we persist only the IDs the user collapses.
  const [collapsedEpics, setCollapsedEpics] = useState(
    () => new Set(getBeadsGrouping().collapsedEpics),
  );

  // Write-through: persist grouping state whenever it changes.
  useEffect(() => {
    setBeadsGrouping({
      enabled: grouping,
      collapsedEpics: [...collapsedEpics],
    });
  }, [grouping, collapsedEpics]);

  // Sort preference (field + direction), persisted to localStorage. Defaults to
  // newest-first by creation date. `showSortMenu` drives the toolbar dropdown.
  const [sort, setSort] = useState(() => getBeadsSort());
  const [showSortMenu, setShowSortMenu] = useState(false);
  const sortMenuRef = useRef(null);

  // Write-through: persist the sort preference whenever it changes.
  useEffect(() => {
    setBeadsSort(sort);
  }, [sort]);

  // Close the sort menu on outside click while it is open.
  useEffect(() => {
    if (!showSortMenu) return undefined;
    const onDocClick = (e) => {
      if (sortMenuRef.current && !sortMenuRef.current.contains(e.target)) {
        setShowSortMenu(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [showSortMenu]);

  // Per-issue right-click context menu. `contextMenu` holds the click position
  // and the issue it targets; `menuPrompts` are the `menus: beadsIssues` prompts shown
  // in the "Prompts" submenu. Actions are not wired to behavior yet.
  const [contextMenu, setContextMenu] = useState(null);
  const [menuPrompts, setMenuPrompts] = useState([]);

  // "Clean up closed issues" confirmation + in-flight state.
  const [showCleanupConfirm, setShowCleanupConfirm] = useState(false);
  const [cleaningUp, setCleaningUp] = useState(false);
  const [cleanupProgress, setCleanupProgress] = useState(null);
  // Bookkeeping for the single "live" cleanup progress toast: the id of the
  // currently shown toast (so it can be replaced/dismissed in place) and the
  // timestamp of the last shown toast (so updates are throttled, not per-batch).
  const cleanupToastIdRef = useRef(null);
  const lastCleanupToastAtRef = useRef(0);

  // Single-issue delete confirmation target + in-flight state, and the
  // in-flight flag for the close/reopen status toggle.
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deletingIssue, setDeletingIssue] = useState(false);
  // When deleting an epic, what to do with its descendant issues:
  // "none" (leave unchanged), "close" (close open descendants), or
  // "delete" (permanently delete all descendants).
  const [childAction, setChildAction] = useState("none");
  const [statusBusy, setStatusBusy] = useState(false);

  // Folder upstream task system ("none"|"jira"|"github"|"gitlab"|"linear"|"prompts") and the
  // in-flight sync action ("pull"|"push"|"sync"|null), used to drive the
  // upstream sync buttons in the footer.
  const [upstream, setUpstream] = useState("none");
  const [syncAction, setSyncAction] = useState(null);
  // For the "prompts" upstream type: names of the configured pull/push/sync prompts.
  const [pullPromptName, setPullPromptName] = useState("");
  const [pushPromptName, setPushPromptName] = useState("");
  const [syncPromptName, setSyncPromptName] = useState("");

  // List-level "Prompts" dropdown state (footer toolbar). These are the
  // `menus: beadsList` prompts that operate on the whole issue list rather than
  // a single issue. Loaded lazily the first time the dropdown is opened.
  const [showListPrompts, setShowListPrompts] = useState(false);
  const [listPrompts, setListPrompts] = useState([]);
  const [listPromptsLoading, setListPromptsLoading] = useState(false);
  // Per-send periodic override for beadsList prompts, keyed by prompt name.
  // Reset whenever the list reloads (see effect below).
  const [listPeriodicOn, setListPeriodicOn] = useState({});

  // Shortcut buttons configured for this folder's tasksList section.
  const [shortcuts, setShortcuts] = useState([]);
  // Map from prompt name → prompt object, built once shortcuts + prompts are loaded.
  const [shortcutPromptMap, setShortcutPromptMap] = useState(new Map());
  const listPromptsRef = useRef(null);
  // Ref for the issues scroll container — used by usePullToRefresh.
  const scrollContainerRef = useRef(null);

  const workspaceLabel = workingDir ? getBasename(workingDir) : "Workspace";

  const fetchList = useCallback(async () => {
    if (!workingDir) return;
    setLoading(true);
    setError(null);
    try {
      const res = await authFetch(
        endpoints.issues.list({ working_dir: workingDir }),
      );
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        setError(data.error || data.message || "Failed to load issues");
        setIssues([]);
      } else {
        setIssues(Array.isArray(data) ? data : []);
      }
    } catch (err) {
      setError(err.message || "Failed to load issues");
    } finally {
      setLoading(false);
    }
  }, [workingDir]);

  useEffect(() => {
    fetchList();
  }, [fetchList]);

  // Pull-to-refresh: disabled while the detail panel or create drawer is open.
  const pullToRefreshDisabled = !!(selectedIssue || isCreating);
  const { pullDistance, refreshing } = usePullToRefresh(
    scrollContainerRef,
    fetchList,
    {
      enabled: !pullToRefreshDisabled,
      threshold: 70,
      resistance: 0.5,
    },
  );

  // Fetch the folder's configured upstream so the sync buttons can be shown.
  useEffect(() => {
    if (!workingDir) {
      setUpstream("none");
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch(
          endpoints.issues.upstream({ working_dir: workingDir }),
        );
        const data = await readBeadsResponse(res);
        if (!cancelled) {
          setUpstream((data && data.upstream) || "none");
          setPullPromptName((data && data.pull_prompt) || "");
          setPushPromptName((data && data.push_prompt) || "");
          setSyncPromptName((data && data.sync_prompt) || "");
        }
      } catch (_err) {
        if (!cancelled) setUpstream("none");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [workingDir]);

  // Fetch folder shortcut buttons and resolve their prompt objects eagerly so
  // buttons can dispatch immediately without a lazy-load on click. Extracted to
  // a callback so both the initial load and the "shortcuts updated" event
  // listener (below) can reuse it. `isStale` lets the workingDir effect cancel a
  // stale in-flight fetch when the folder changes mid-request.
  const loadShortcuts = useCallback(
    async (isStale) => {
      if (!workingDir) {
        setShortcuts([]);
        setShortcutPromptMap(new Map());
        return;
      }
      try {
        const res = await authFetch(
          endpoints.folders.shortcuts({ working_dir: workingDir }),
        );
        const data = await res.json().catch(() => ({}));
        const list = data?.sections?.tasksList || [];
        if (isStale && isStale()) return;
        setShortcuts(list);
        if (list.length > 0 && onFetchBeadsListPrompts) {
          const prompts = await onFetchBeadsListPrompts(workingDir);
          if (isStale && isStale()) return;
          const map = new Map((prompts || []).map((p) => [p.name, p]));
          setShortcutPromptMap(map);
        } else {
          setShortcutPromptMap(new Map());
        }
      } catch (_err) {
        if (isStale && isStale()) return;
        setShortcuts([]);
        setShortcutPromptMap(new Map());
      }
    },
    [workingDir, onFetchBeadsListPrompts],
  );

  // Initial load (and reload on folder switch), with stale-fetch cancellation.
  useEffect(() => {
    let cancelled = false;
    loadShortcuts(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [loadShortcuts]);

  // Refresh shortcut buttons immediately when the Workspaces dialog saves new
  // shortcuts for this folder, so no page reload is needed.
  useEffect(() => {
    const handler = (e) => {
      const dir = e?.detail?.working_dir;
      if (!dir || dir === workingDir) loadShortcuts();
    };
    window.addEventListener("mitto:folder_shortcuts_updated", handler);
    return () =>
      window.removeEventListener("mitto:folder_shortcuts_updated", handler);
  }, [loadShortcuts, workingDir]);

  // Auto-refresh the issue list when the backend fsnotify watcher reports
  // external changes to .beads (another agent/CLI, git pull, bd dolt pull).
  // Scope the refetch to this view's working_dir to avoid a global thundering
  // refresh across all open Tasks views.
  useEffect(() => {
    const handler = (e) => {
      const dirs = e?.detail?.working_dirs;
      if (!dirs || (Array.isArray(dirs) && dirs.includes(workingDir))) {
        fetchList();
      }
    };
    window.addEventListener("mitto:beads_changed", handler);
    return () => window.removeEventListener("mitto:beads_changed", handler);
  }, [workingDir, fetchList]);

  // Trigger an upstream sync action (pull/push/sync) via POST /api/issues/sync.
  // The backend reads the integration from folders.json; we only send the action.
  const handleSync = useCallback(
    async (action) => {
      if (!workingDir || syncAction) return;
      setSyncAction(action);
      try {
        const res = await secureFetch(
          endpoints.issues.sync({ working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action }),
          },
        );
        const data = await readBeadsResponse(res);
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || `Failed to ${action}`,
              message: data.stderr,
            });
        } else {
          const verb =
            action === "pull"
              ? "Pulled"
              : action === "push"
                ? "Pushed"
                : "Synced";
          showToast &&
            showToast({
              style: "success",
              title: `${verb} with ${UPSTREAM_LABELS[upstream] || upstream}`,
            });
          fetchList();
        }
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action}`,
          });
      } finally {
        setSyncAction(null);
      }
    },
    [workingDir, syncAction, upstream, showToast, fetchList],
  );

  // The list rows already carry all rich fields (description, parent, dates,
  // assignee, owner), so the detail panel is populated directly from the row —
  // no extra /show request needed. Clicking the open row again toggles it shut.
  const selectIssue = useCallback((issue) => {
    setIsCreating(false);
    setSelectedIssue((prev) => (prev && prev.id === issue.id ? null : issue));
  }, []);

  // Open the side panel in "create" mode for a brand-new issue.
  const openCreate = useCallback(() => {
    setCreateParent(null);
    setSelectedIssue(null);
    setIsCreating(true);
  }, []);

  // Open the create panel pre-seeded to create a child of the given epic.
  const openCreateInEpic = useCallback((epicId) => {
    setCreateParent(epicId);
    setSelectedIssue(null);
    setIsCreating(true);
  }, []);

  // Open the create panel when asked to from outside (e.g. the global "new
  // task" keyboard shortcut). We apply once per nonce so repeated presses keep
  // (re)opening it; unlike issue selection this does not depend on the list
  // having loaded.
  const appliedCreateNonceRef = useRef(0);
  useEffect(() => {
    if (!initialCreateNonce) return;
    if (initialCreateNonce === appliedCreateNonceRef.current) return;
    appliedCreateNonceRef.current = initialCreateNonce;
    openCreate();
  }, [initialCreateNonce, openCreate]);

  // Refresh the issue list when asked from outside (the sidebar Tasks menu's
  // "Refresh" action). Applied once per nonce so it re-fetches even when the
  // beads view is already showing this folder.
  const appliedRefreshNonceRef = useRef(0);
  useEffect(() => {
    if (!initialRefreshNonce) return;
    if (initialRefreshNonce === appliedRefreshNonceRef.current) return;
    appliedRefreshNonceRef.current = initialRefreshNonce;
    fetchList();
  }, [initialRefreshNonce, fetchList]);

  // Open the "clean up closed issues" confirmation when asked from outside (the
  // sidebar Tasks menu's "Cleanup closed" action). The dialog, cleanup request,
  // toast, and subsequent refresh are all owned here.
  const appliedCleanupNonceRef = useRef(0);
  useEffect(() => {
    if (!initialCleanupNonce) return;
    if (initialCleanupNonce === appliedCleanupNonceRef.current) return;
    appliedCleanupNonceRef.current = initialCleanupNonce;
    setShowCleanupConfirm(true);
  }, [initialCleanupNonce]);

  // Close the side panel, whether it is in view or create mode.
  const closePanel = useCallback(() => {
    setSelectedIssue(null);
    setIsCreating(false);
    setCreateParent(null);
  }, []);

  // Open the per-issue context menu at the cursor and load the `menus: beadsIssues`
  // prompts for this workspace so the "Prompts" submenu reflects them.
  // The issue is passed so the server can evaluate item.*-gated enabledWhen per row.
  const handleRowContextMenu = useCallback(
    (e, issue) => {
      e.preventDefault();
      e.stopPropagation();
      setContextMenu({ x: e.clientX, y: e.clientY, issue });
      if (onFetchBeadsPrompts) {
        onFetchBeadsPrompts(workingDir, issue).then((prompts) =>
          setMenuPrompts(prompts || []),
        );
      }
    },
    [onFetchBeadsPrompts, workingDir],
  );

  const closeContextMenu = useCallback(() => setContextMenu(null), []);

  // Open the per-issue context menu anchored to the row's "..." button (rather
  // than at the cursor), then load the beadsIssues prompts like the right-click path.
  // The issue is passed so the server can evaluate item.*-gated enabledWhen per row.
  const handleRowMenuButton = useCallback(
    (e, issue) => {
      e.preventDefault();
      e.stopPropagation();
      const rect = e.currentTarget.getBoundingClientRect();
      setContextMenu({ x: rect.left, y: rect.bottom, issue });
      if (onFetchBeadsPrompts) {
        onFetchBeadsPrompts(workingDir, issue).then((prompts) =>
          setMenuPrompts(prompts || []),
        );
      }
    },
    [onFetchBeadsPrompts, workingDir],
  );

  // Keep the open detail panel in sync when the list refreshes: replace it with
  // the fresh row if it still exists, otherwise close the panel.
  useEffect(() => {
    setSelectedIssue((prev) => {
      if (!prev) return prev;
      return issues.find((i) => i.id === prev.id) || null;
    });
  }, [issues]);

  const filtered = useMemo(() => {
    const out = issues.filter((issue) => {
      // Hide an issue only when its status maps to a toggle that is currently
      // off. Statuses without a toggle (e.g. blocked, deferred) are unaffected.
      if (statusToggles[issue.status] === false) return false;
      if (typeFilter !== "all" && issue.issue_type !== typeFilter) return false;
      if (!matchesSearch(issue, search)) return false;
      return true;
    });
    // Flat list ordering follows the user's sort preference. The grouped view
    // re-sorts within groupedItems, so this ordering only drives the flat path.
    out.sort((a, b) => cmpBySort(a, b, sort));
    return out;
  }, [issues, statusToggles, typeFilter, search, sort]);

  const allTypes = useMemo(
    () => [...new Set(issues.map((i) => i.issue_type).filter(Boolean))],
    [issues],
  );

  // Map of issue id -> number of issues that name it as their parent. Computed
  // from the full list (not the filtered view) so an epic's child count stays
  // accurate even when its children are filtered out of view.
  const childCountById = useMemo(() => {
    const counts = {};
    for (const i of issues) {
      if (i.parent) counts[i.parent] = (counts[i.parent] || 0) + 1;
    }
    return counts;
  }, [issues]);

  // Grouped render model — only computed when the grouping toggle is on.
  // Produces a sorted top-level array of { type: "epic"|"orphan", ... } items.
  // Epics that survived the filter are shown with their filtered children;
  // epics that were filtered out but have surviving children are kept as ghost
  // header rows (context row). Nesting is RECURSIVE: sub-epics render as their
  // own collapsible groups rather than being flattened into the top-level epic.
  //
  // Group shape: { epic: issue|null, items: Array<{type:"issue",issue}|{type:"subEpic",group}> }
  // items are sorted by the active sort preference and may interleave normal
  // issues with nested sub-epic groups at each level.
  const groupedItems = useMemo(() => {
    if (!grouping) return null;

    const issueById = new Map(issues.map((i) => [i.id, i]));

    // Epics from the full list: typed as "epic" or has at least one child.
    const epicSet = new Set();
    for (const i of issues) {
      if (i.issue_type === "epic" || (childCountById[i.id] || 0) > 0)
        epicSet.add(i.id);
    }

    // Walk up the parent chain and return the ID of the NEAREST (direct) epic
    // ancestor, or null if there is no epic ancestor. Guards against cycles.
    function directEpicParentOf(issue) {
      const seen = new Set([issue.id]);
      let cur = issue;
      while (cur.parent) {
        if (seen.has(cur.parent)) break;
        seen.add(cur.parent);
        const parent = issueById.get(cur.parent);
        if (!parent) break;
        cur = parent;
        if (epicSet.has(cur.id)) return cur.id;
      }
      return null;
    }

    // epicGroups: epicId -> { epic: issue|null, items: [] }
    // items: [{type:"issue", issue}] or [{type:"subEpic", group}]
    const epicGroups = new Map();
    const epicOrderIds = []; // insertion-order top-level epic ids
    const orphans = [];
    // Cycle guard for ensureGroup recursion (epic parent-chain cycles).
    const inProgress = new Set();

    // Create or retrieve the group for epicId; recursively ensures the group is
    // linked into the hierarchy up to the top-level (ghost-header safe).
    function ensureGroup(epicId) {
      if (epicGroups.has(epicId)) return epicGroups.get(epicId);
      if (inProgress.has(epicId)) return null; // cycle in epic hierarchy
      inProgress.add(epicId);

      const epicIssue = issueById.get(epicId) || null;
      const group = { epic: epicIssue, items: [] };
      epicGroups.set(epicId, group);

      const parentEpicId = epicIssue ? directEpicParentOf(epicIssue) : null;
      if (parentEpicId) {
        // Sub-epic: link into parent's item list.
        const parentGroup = ensureGroup(parentEpicId);
        if (parentGroup) parentGroup.items.push({ type: "subEpic", group });
      } else {
        // Top-level epic (including ghost epics with no epic ancestor).
        epicOrderIds.push(epicId);
      }

      inProgress.delete(epicId);
      return group;
    }

    for (const issue of filtered) {
      if (epicSet.has(issue.id)) {
        // This filtered issue is itself an epic — ensure its group exists and
        // update the epic reference (it may have been created as a ghost).
        const g = ensureGroup(issue.id);
        if (g) g.epic = issue;
      } else {
        // Non-epic issue: attach to its direct epic parent, or orphan.
        const parentEpicId = directEpicParentOf(issue);
        if (parentEpicId !== null) {
          const parentGroup = ensureGroup(parentEpicId);
          if (parentGroup) parentGroup.items.push({ type: "issue", issue });
        } else {
          orphans.push(issue);
        }
      }
    }

    // Sort items inside each group: normal issues and sub-epic groups are
    // interleaved and sorted together using each item's representative issue.
    for (const [, group] of epicGroups) {
      group.items.sort((a, b) => {
        const ia =
          a.type === "issue"
            ? a.issue
            : a.group.epic || { priority: 3, id: "" };
        const ib =
          b.type === "issue"
            ? b.issue
            : b.group.epic || { priority: 3, id: "" };
        return cmpBySort(ia, ib, sort);
      });
    }

    // Top-level: epics and orphans sorted together. Each row sorts by its own
    // representative issue — an epic by the epic's own attributes, an orphan by
    // its own — so the active sort field/direction applies throughout. A ghost
    // epic (filtered out but with surviving children) has no representative, so
    // it falls back to a low-priority, undated placeholder.
    const topLevel = [];
    for (const id of epicOrderIds)
      topLevel.push({ type: "epic", group: epicGroups.get(id) });
    for (const issue of orphans) topLevel.push({ type: "orphan", issue });
    topLevel.sort((a, b) => {
      const ia =
        a.type === "epic" ? a.group.epic || { priority: 3, id: "" } : a.issue;
      const ib =
        b.type === "epic" ? b.group.epic || { priority: 3, id: "" } : b.issue;
      return cmpBySort(ia, ib, sort);
    });
    return topLevel;
  }, [filtered, issues, childCountById, grouping, sort]);

  // Every descendant (children, grandchildren, ...) of the issue queued for
  // deletion, each tagged with its depth below the target. Used to offer the
  // recursive "close"/"delete children" actions when deleting an epic.
  const deleteTargetDescendants = useMemo(() => {
    if (!deleteTarget) return [];
    // Build a parent -> children index over the whole issue set.
    const byParent = new Map();
    for (const i of issues) {
      const list = byParent.get(i.parent);
      if (list) list.push(i);
      else byParent.set(i.parent, [i]);
    }
    // Walk the subtree, guarding against cycles via a seen set.
    const out = [];
    const seen = new Set([deleteTarget.id]);
    const stack = [{ id: deleteTarget.id, depth: 0 }];
    while (stack.length) {
      const { id, depth } = stack.pop();
      const kids = byParent.get(id) || [];
      for (const k of kids) {
        if (seen.has(k.id)) continue;
        seen.add(k.id);
        out.push({ issue: k, depth: depth + 1 });
        stack.push({ id: k.id, depth: depth + 1 });
      }
    }
    return out;
  }, [deleteTarget, issues]);

  // The still-open descendants — closing already-closed issues is a no-op, so
  // the "close children" option only targets these.
  const deleteTargetOpenDescendants = useMemo(
    () => deleteTargetDescendants.filter((d) => d.issue.status !== "closed"),
    [deleteTargetDescendants],
  );

  // Reset the child-handling choice whenever the delete target changes, so it
  // never carries over from a previous deletion.
  useEffect(() => {
    setChildAction("none");
  }, [deleteTarget]);

  const closedCount = useMemo(
    () => issues.filter((i) => i.status === "closed").length,
    [issues],
  );

  // Start a background bulk-delete of all closed issues. The HTTP call returns
  // immediately; progress arrives via the mitto:beads_cleanup_progress event.
  const handleCleanup = useCallback(async () => {
    setCleaningUp(true);
    setCleanupProgress(null);
    setShowCleanupConfirm(false);
    try {
      const res = await secureFetch(
        endpoints.issues.cleanup({ working_dir: workingDir }),
        {
          method: "POST",
        },
      );
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast &&
          showToast({
            style: "error",
            title: data.error || "Failed to clean up issues",
          });
        setCleaningUp(false);
        return;
      }
      if (!data.started) {
        if (data.already_running) {
          showToast &&
            showToast({ style: "info", title: "Cleanup already in progress" });
        } else {
          showToast &&
            showToast({
              style: "success",
              title: "No closed issues to remove",
            });
        }
        setCleaningUp(false);
        return;
      }
      // Background job started; progress arrives via mitto:beads_cleanup_progress.
      const total = data.total || 0;
      setCleanupProgress({ deleted: 0, total });
      // Immediate feedback that the (potentially long) operation has begun. This
      // sticky toast is then replaced in place by throttled progress updates.
      lastCleanupToastAtRef.current = Date.now();
      cleanupToastIdRef.current = showToast
        ? showToast({
            style: "info",
            title: `Removing ${total} closed issue${total === 1 ? "" : "s"}…`,
            sticky: true,
          })
        : null;
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to clean up issues",
        });
      setCleaningUp(false);
    }
  }, [workingDir, showToast]);

  useEffect(() => {
    // Dismiss the live progress toast (if any) so a terminal outcome can take
    // its place, or so a stale toast does not linger on unmount.
    const clearProgressToast = () => {
      if (cleanupToastIdRef.current != null && dismissToast) {
        dismissToast(cleanupToastIdRef.current);
      }
      cleanupToastIdRef.current = null;
    };
    const onProgress = (e) => {
      const d = (e && e.detail) || {};
      if (d.working_dir !== workingDir) return;
      if (d.error) {
        clearProgressToast();
        showToast &&
          showToast({
            style: "error",
            title: d.error || "Failed to clean up issues",
          });
        setCleaningUp(false);
        setCleanupProgress(null);
        fetchList();
        return;
      }
      const deleted = d.deleted || 0;
      const total = d.total || 0;
      setCleanupProgress({ deleted, total });
      if (d.done) {
        clearProgressToast();
        showToast &&
          showToast({
            style: "success",
            title: `Removed ${deleted} closed issue${deleted === 1 ? "" : "s"}`,
          });
        setCleaningUp(false);
        setCleanupProgress(null);
        fetchList();
        return;
      }
      // Mid-flight: refresh the single live progress toast, throttled so a long
      // run with many batches does not spam one toast per batch.
      const now = Date.now();
      if (
        showToast &&
        now - lastCleanupToastAtRef.current >=
          CLEANUP_PROGRESS_TOAST_INTERVAL_MS
      ) {
        lastCleanupToastAtRef.current = now;
        clearProgressToast();
        cleanupToastIdRef.current = showToast({
          style: "info",
          title: `Removing closed issues… ${deleted}/${total}`,
          sticky: true,
        });
      }
    };
    window.addEventListener("mitto:beads_cleanup_progress", onProgress);
    return () => {
      window.removeEventListener("mitto:beads_cleanup_progress", onProgress);
      clearProgressToast();
    };
  }, [workingDir, showToast, dismissToast, fetchList]);

  // Permanently delete a single issue, then refresh the list. The confirm
  // dialog (gated on deleteTarget) calls this.
  const confirmDeleteIssue = useCallback(async () => {
    if (!deleteTarget) return;
    const id = deleteTarget.id;
    setDeletingIssue(true);
    try {
      // Apply the chosen recursive action to the epic's descendants first
      // (best-effort). Each failure is counted so the final toast can report
      // partial success without aborting the epic delete.
      let closedCount = 0;
      let closeFailed = 0;
      let childDeletedCount = 0;
      let childDeleteFailed = 0;

      if (childAction === "close") {
        for (const { issue: child } of deleteTargetOpenDescendants) {
          try {
            const cres = await secureFetch(
              endpoints.issues.status(child.id, { working_dir: workingDir }),
              {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ action: "close" }),
              },
            );
            const cdata = await readBeadsResponse(cres);
            if (!cres.ok || cdata.error) closeFailed++;
            else closedCount++;
          } catch (err) {
            closeFailed++;
          }
        }
      } else if (childAction === "delete") {
        // Delete deepest-first so a parent is never removed before its children.
        const ordered = [...deleteTargetDescendants].sort(
          (a, b) => b.depth - a.depth,
        );
        for (const { issue: child } of ordered) {
          try {
            const cres = await secureFetch(
              endpoints.issues.remove(child.id, { working_dir: workingDir }),
              {
                method: "DELETE",
              },
            );
            const cdata = await readBeadsResponse(cres);
            if (!cres.ok || cdata.error) childDeleteFailed++;
            else childDeletedCount++;
          } catch (err) {
            childDeleteFailed++;
          }
        }
      }

      const res = await secureFetch(
        endpoints.issues.remove(id, { working_dir: workingDir }),
        {
          method: "DELETE",
        },
      );
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast &&
          showToast({
            style: "error",
            title: data.error || "Failed to delete issue",
          });
      } else {
        let title = `Deleted ${id}`;
        if (closedCount > 0) {
          title += ` and closed ${closedCount} child issue${closedCount === 1 ? "" : "s"}`;
        }
        if (childDeletedCount > 0) {
          title += ` and deleted ${childDeletedCount} child issue${childDeletedCount === 1 ? "" : "s"}`;
        }
        const failedTotal = closeFailed + childDeleteFailed;
        if (failedTotal > 0) {
          const verb = childAction === "delete" ? "delete" : "close";
          showToast &&
            showToast({
              style: "warning",
              title: `${title} (${failedTotal} child issue${failedTotal === 1 ? "" : "s"} failed to ${verb})`,
            });
        } else {
          showToast && showToast({ style: "success", title });
        }
        fetchList();
      }
    } catch (err) {
      showToast &&
        showToast({
          style: "error",
          title: err.message || "Failed to delete issue",
        });
    } finally {
      setDeletingIssue(false);
      setDeleteTarget(null);
    }
  }, [
    deleteTarget,
    childAction,
    deleteTargetOpenDescendants,
    deleteTargetDescendants,
    workingDir,
    showToast,
    fetchList,
  ]);

  // Close or reopen a single issue depending on its current status, then refresh.
  const handleToggleStatus = useCallback(
    async (issue) => {
      if (!issue) return;
      const action = issue.status === "closed" ? "reopen" : "close";
      setStatusBusy(true);
      try {
        const res = await secureFetch(
          endpoints.issues.status(issue.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action }),
          },
        );
        const data = await readBeadsResponse(res);
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || `Failed to ${action} issue`,
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title:
                action === "close"
                  ? `Closed ${issue.id}`
                  : `Reopened ${issue.id}`,
            });
          fetchList();
        }
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} issue`,
          });
      } finally {
        setStatusBusy(false);
      }
    },
    [workingDir, showToast, fetchList],
  );

  // Defer or undefer a single issue ("on ice" for later) depending on its
  // current status, then refresh. Uses /api/issues/{id}/status, which also
  // handles the defer/undefer verbs.
  const handleToggleDefer = useCallback(
    async (issue) => {
      if (!issue) return;
      const action = issue.status === "deferred" ? "undefer" : "defer";
      setStatusBusy(true);
      try {
        const res = await secureFetch(
          endpoints.issues.status(issue.id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ action }),
          },
        );
        const data = await readBeadsResponse(res);
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || `Failed to ${action} issue`,
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title:
                action === "defer"
                  ? `Deferred ${issue.id}`
                  : `Undeferred ${issue.id}`,
            });
          fetchList();
        }
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || `Failed to ${action} issue`,
          });
      } finally {
        setStatusBusy(false);
      }
    },
    [workingDir, showToast, fetchList],
  );

  // Create a "blocks" dependency edge from the context menu. `direction` picks
  // the argument order (the edge kind is always "blocks"):
  //   "depends-on" → issue depends on other      (bd dep add <issue> <other>)
  //   "blocks"     → issue blocks other          (bd dep add <other> <issue>)
  // since "A depends on B" is the same edge as "B is blocked by A".
  const handleAddDependencyEdge = useCallback(
    async (issue, other, direction) => {
      if (!issue || !other) return;
      const id = direction === "blocks" ? other.id : issue.id;
      const dependsOn = direction === "blocks" ? issue.id : other.id;
      try {
        const res = await secureFetch(
          endpoints.issues.dependencies(id, { working_dir: workingDir }),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              depends_on: dependsOn,
              type: "blocks",
              action: "add",
            }),
          },
        );
        const data = await readBeadsResponse(res);
        if (!res.ok || data.error) {
          showToast &&
            showToast({
              style: "error",
              title: data.error || "Failed to add dependency",
              message: data.stderr,
            });
        } else {
          showToast &&
            showToast({
              style: "success",
              title:
                direction === "blocks"
                  ? `${issue.id} now blocks ${other.id}`
                  : `${issue.id} now depends on ${other.id}`,
            });
          fetchList();
        }
      } catch (err) {
        showToast &&
          showToast({
            style: "error",
            title: err.message || "Failed to add dependency",
          });
      }
    },
    [workingDir, showToast, fetchList],
  );

  // Run a beads prompt for a specific issue: delegates to the parent, which
  // creates a new conversation seeded with the prompt text and issue context.
  const handleRunPrompt = useCallback(
    (prompt, issue, opts) => {
      closeContextMenu();
      onRunBeadsPrompt && onRunBeadsPrompt(prompt, issue, opts);
    },
    [onRunBeadsPrompt, closeContextMenu],
  );

  // Close the list-level prompts dropdown on outside click while it is open.
  useEffect(() => {
    if (!showListPrompts) return undefined;
    const onDocClick = (e) => {
      if (
        listPromptsRef.current &&
        !listPromptsRef.current.contains(e.target)
      ) {
        setShowListPrompts(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [showListPrompts]);

  // Toggle the list-level prompts dropdown, lazily loading the `menus: beadsList`
  // prompts for this workspace the first time it is opened.
  const toggleListPrompts = useCallback(() => {
    setShowListPrompts((open) => {
      const next = !open;
      if (next && onFetchBeadsListPrompts && workingDir) {
        setListPromptsLoading(true);
        onFetchBeadsListPrompts(workingDir)
          .then((list) => {
            const prompts = list || [];
            setListPrompts(prompts);
            // Seed per-item periodic toggle defaults from each prompt's mode/default.
            const seed = {};
            for (const p of prompts) {
              if (promptPeriodicIsToggleable(p)) {
                seed[p.name] = promptPeriodicDefaultOn(p);
              }
            }
            setListPeriodicOn(seed);
          })
          .finally(() => setListPromptsLoading(false));
      }
      return next;
    });
  }, [onFetchBeadsListPrompts, workingDir]);

  // Run a list-level prompt in a new conversation (no per-issue context).
  const handleRunListPrompt = useCallback(
    (prompt, opts) => {
      setShowListPrompts(false);
      onRunBeadsListPrompt && onRunBeadsListPrompt(prompt, undefined, opts);
    },
    [onRunBeadsListPrompt],
  );

  // Group the beadsIssues prompts by their `group` into per-group submenus,
  // identical to the conversation menu and the detail-panel kebab.
  const promptGroupItems = buildPromptGroupMenuItems(
    menuPrompts,
    (p, opts) => handleRunPrompt(p, contextMenu && contextMenu.issue, opts),
    html`<${PlusIcon} />`,
  );

  const ctxIssue = contextMenu && contextMenu.issue;
  const ctxIsClosed = ctxIssue && ctxIssue.status === "closed";
  const ctxIsDeferred = ctxIssue && ctxIssue.status === "deferred";

  // "Depends On" / "Blocks" submenus list every other open/in-progress issue.
  // Picking one creates a "blocks" edge in the chosen direction via
  // handleAddDependencyEdge. Closed/deferred issues are excluded as dependency targets.
  const otherIssues = (issues || []).filter(
    (i) =>
      ctxIssue &&
      i.id !== ctxIssue.id &&
      (i.status === "open" || i.status === "in_progress"),
  );
  const issueSubmenu = (direction) =>
    otherIssues.map((i) => ({
      label: `${i.id} · ${i.title}`,
      onClick: () => handleAddDependencyEdge(ctxIssue, i, direction),
    }));

  const contextMenuItems = [
    ...promptGroupItems,
    ...(otherIssues.length > 0
      ? [
          {
            label: "Depends On",
            icon: html`<${ArrowDownIcon} />`,
            submenu: issueSubmenu("depends-on"),
          },
          {
            label: "Blocks",
            icon: html`<${ArrowUpIcon} />`,
            submenu: issueSubmenu("blocks"),
          },
        ]
      : []),
    {
      label: "Copy ID",
      icon: html`<${CopyIcon} />`,
      onClick: async () => {
        if (!ctxIssue) return;
        const ok = await copyToClipboard(ctxIssue.id);
        showToast &&
          showToast(
            ok
              ? { style: "success", title: `Copied ${ctxIssue.id}` }
              : { style: "error", title: "Failed to copy issue ID" },
          );
      },
    },
    {
      label: ctxIsClosed ? "Reopen" : "Close",
      icon: ctxIsClosed ? html`<${RefreshIcon} />` : html`<${CheckIcon} />`,
      onClick: () => ctxIssue && handleToggleStatus(ctxIssue),
      disabled: statusBusy,
    },
    {
      label: ctxIsDeferred ? "Undefer" : "Defer",
      icon: ctxIsDeferred ? html`<${SunIcon} />` : html`<${MoonIcon} />`,
      onClick: () => ctxIssue && handleToggleDefer(ctxIssue),
      disabled: statusBusy,
    },
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => contextMenu && setDeleteTarget(contextMenu.issue),
      danger: true,
    },
  ];

  // Shared row renderer for both flat and grouped render paths.
  // Treat an issue as an epic when it is typed as one or has at least one
  // child issue, giving it a purple left accent. Selected card always wins
  // on background/border. The hovered (non-selected) row gets Mitto's solid
  // brand red — the same red used for the active session item and delete
  // buttons; priority/status/type badge pills are opaque so they stay
  // readable on the red background.
  // When `epicExpanded` is non-null the row is an epic group header in grouped
  // mode: a chevron is prepended to indicate collapse/expand state (the native
  // <details> disclosure marker is hidden via .beads-epic-summary).
  function renderIssueRow(issue, epicExpanded = null) {
    const linkedSessionId = issueSessionMap[issue.id];
    const isStreamingIssue = issueStreamingSet.has(issue.id);
    const isSelected = selectedIssue && selectedIssue.id === issue.id;
    const childCount = childCountById[issue.id] || 0;
    const isEpic = issue.issue_type === "epic" || childCount > 0;
    const showChevron = epicExpanded !== null;
    const bgTone = isSelected
      ? "bg-mitto-surface-3/30"
      : "bg-mitto-surface-3/20 hover:bg-red-600";
    // Each issue renders as a self-contained card with a delicate border,
    // matching the ACP Servers / Runners lists. The base border is applied
    // here as Tailwind utilities (not in CSS) so the two distinctive Mitto
    // state treatments — a full accent border when selected, and the purple
    // left-accent for epics — share equal specificity and override correctly.
    const borderTone = isSelected
      ? "border border-mitto-accent-500/60"
      : isEpic
        ? "border border-mitto-border border-l-4 border-l-purple-500"
        : "border border-mitto-border";
    const rowContent = html`
      ${showChevron
        ? html`<button
            type="button"
            class="shrink-0 self-center btn btn-ghost btn-circle btn-xs text-mitto-text-muted hover:text-mitto-text-strong inline-flex tooltip tooltip-right"
            data-tip=${epicExpanded ? "Collapse epic" : "Expand epic"}
            aria-label=${epicExpanded ? "Collapse epic" : "Expand epic"}
            aria-expanded=${epicExpanded ? "true" : "false"}
            data-testid="beads-epic-chevron"
            onClick=${(e) => {
              // Toggle collapse/expand only — never select the epic (open the
              // detail panel) and never let the native <summary> toggle fire.
              // stopPropagation keeps the click off the BeadsIssueRow onSelect
              // and the summary; we then drive collapsedEpics ourselves (the
              // <details> onToggle re-derives the same state idempotently).
              e.preventDefault();
              e.stopPropagation();
              setCollapsedEpics((prev) => {
                const next = new Set(prev);
                if (next.has(issue.id)) next.delete(issue.id);
                else next.add(issue.id);
                return next;
              });
            }}
          >
            ${epicExpanded
              ? html`<${ChevronDownIcon} className="w-4 h-4" />`
              : html`<${ChevronRightIcon} className="w-4 h-4" />`}
          </button>`
        : null}
      <div class="list-col-grow flex flex-col gap-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          ${isStreamingIssue
            ? html`<span
                class="shrink-0 text-mitto-accent tooltip tooltip-bottom"
                data-tip="A linked conversation is responding..."
                aria-label="A linked conversation is responding..."
              >
                <span class="loading loading-ring loading-xs"></span>
              </span>`
            : null}
          <span class="font-mono text-xs max-w-40 truncate" title=${issue.id}>
            ${linkedSessionId && onOpenConversation
              ? html`<a
                  href="#"
                  class="text-mitto-accent-400 hover:text-mitto-accent-300 hover:underline"
                  onClick=${(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    onOpenConversation(linkedSessionId);
                  }}
                  >${issue.id}</a
                >`
              : html`<span class="text-mitto-text-secondary"
                  >${issue.id}</span
                >`}
          </span>
          ${typeBadge(issue.issue_type)} ${statusBadge(issue.status)}
          ${priorityBadge(issue.priority)}
          ${childCount > 0
            ? html`
                <span
                  class="inline-flex items-center gap-1 text-xs text-purple-300 tooltip tooltip-bottom"
                  data-tip="${childCount} child issue${childCount === 1
                    ? ""
                    : "s"}"
                >
                  <${LayersIcon} className="w-3.5 h-3.5" />
                  ${childCount}
                </span>
              `
            : null}
        </div>
        <div class="text-sm text-mitto-text wrap-break-word">
          ${issue.title}
        </div>
      </div>
      <div class="flex items-center gap-1 shrink-0 self-center">
        ${isEpic
          ? html`<button
              type="button"
              onClick=${(e) => {
                e.preventDefault();
                e.stopPropagation();
                openCreateInEpic(issue.id);
              }}
              onMouseEnter=${(e) => showToolbarTip(e, "New issue in epic")}
              onMouseLeave=${hideToolbarTip}
              onMouseDown=${hideToolbarTip}
              class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong inline-flex"
              data-tip="New issue in epic"
              aria-label="New issue in epic"
              data-testid="beads-issue-add-child"
            >
              <${PlusIcon} className="w-3.5 h-3.5" />
            </button>`
          : null}
        <button
          type="button"
          onClick=${(e) => handleRowMenuButton(e, issue)}
          onMouseEnter=${(e) => showToolbarTip(e, "More actions")}
          onMouseLeave=${hideToolbarTip}
          onMouseDown=${hideToolbarTip}
          class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong inline-flex"
          data-tip="More actions"
          aria-label="More actions"
          data-testid="beads-issue-menu"
        >
          <${EllipsisIcon} className="w-3.5 h-3.5" />
        </button>
      </div>
    `;
    return html`
      <${BeadsIssueRow}
        key=${issue.id}
        issue=${issue}
        bgTone=${bgTone}
        borderTone=${borderTone}
        onSelect=${() => selectIssue(issue)}
        onContextMenu=${(e) => handleRowContextMenu(e, issue)}
        onClose=${() => handleToggleStatus(issue)}
        onDelete=${() => setDeleteTarget(issue)}
      >${rowContent}</${BeadsIssueRow}>
    `;
  }

  // Recursive renderer for a grouped epic node.
  // group: { epic: issue|null, items: Array<{type:"issue",issue}|{type:"subEpic",group}> }
  // depth: nesting depth (1 = top-level epic children, 2 = sub-epic children, …)
  // Indentation uses inline style so Tailwind JIT precompilation is not required.
  // depth=1 → padding-left:2rem (matching the original pl-8 / 2rem).
  function renderEpicGroup(group, depth) {
    const epicIssue = group.epic;
    const epicId = epicIssue ? epicIssue.id : null;
    const isOpen = epicId ? !collapsedEpics.has(epicId) : true;
    // Stable key: use epicId, or fall back to the first item's issue id for ghosts.
    const firstItem = group.items[0];
    const ghostKey = firstItem
      ? "ghost-" +
        (firstItem.type === "issue"
          ? firstItem.issue.id
          : firstItem.group.epic
            ? firstItem.group.epic.id
            : "")
      : "ghost";
    return html`
      <details
        key=${epicId || ghostKey}
        class="beads-epic-group"
        open=${isOpen}
        onToggle=${(e) => {
          if (!epicId) return;
          const open = e.currentTarget.open;
          setCollapsedEpics((prev) => {
            const next = new Set(prev);
            if (open) next.delete(epicId);
            else next.add(epicId);
            return next;
          });
        }}
      >
        <summary class="beads-epic-summary">
          ${epicIssue
            ? renderIssueRow(epicIssue, isOpen)
            : html`<div
                class="list-row opacity-60 border border-dashed border-mitto-border"
              >
                <span
                  class="shrink-0 self-center text-mitto-text-muted"
                  aria-hidden="true"
                  data-testid="beads-epic-chevron"
                >
                  ${isOpen
                    ? html`<${ChevronDownIcon} className="w-4 h-4" />`
                    : html`<${ChevronRightIcon} className="w-4 h-4" />`}
                </span>
                <div class="list-col-grow text-xs text-mitto-text-muted italic">
                  Epic (not in current filter)
                </div>
              </div>`}
        </summary>
        <div
          class="pl-8"
          style=${depth > 1 ? "padding-left: " + depth * 2 + "rem" : ""}
        >
          ${group.items.map((item) => {
            if (item.type === "issue") return renderIssueRow(item.issue);
            return renderEpicGroup(item.group, depth + 1);
          })}
        </div>
      </details>
    `;
  }

  return html`
    <div class="relative flex h-full overflow-hidden">
    <div class="flex flex-col flex-1 min-w-0 overflow-hidden">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border shrink-0">
        <button
          onClick=${() => onShowSidebar && onShowSidebar()}
          class="btn btn-ghost btn-square btn-sm md:hidden shrink-0 inline-flex tooltip tooltip-bottom"
          data-tip="Show conversations"
          aria-label="Show conversations"
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <span class="font-semibold text-lg flex-1">Tasks — ${workspaceLabel}</span>
      </div>

      <div class="beads-toolbar flex items-center gap-2 px-4 border-b border-mitto-border shrink-0">
        <div class="join shrink-0" role="group" aria-label="Filter by status">
          ${BEADS_STATUS_TOGGLES.map((t) => {
            const tip = statusToggles[t.key]
              ? `Hide ${t.label} issues`
              : `Show ${t.label} issues`;
            return html`
              <button
                type="button"
                onClick=${() => toggleStatus(t.key)}
                onMouseEnter=${(e) => showToolbarTip(e, tip)}
                onMouseLeave=${hideToolbarTip}
                onMouseDown=${hideToolbarTip}
                aria-pressed=${statusToggles[t.key] ? "true" : "false"}
                aria-label=${tip}
                data-tip=${tip}
                class="btn btn-xs btn-square join-item inline-flex ${statusToggles[
                  t.key
                ]
                  ? "btn-active"
                  : "btn-ghost opacity-50"}"
              >
                <${t.Icon} className="w-3.5 h-3.5" />
              </button>
            `;
          })}
        </div>
        <div class="join shrink-0" role="group" aria-label="View mode">
          <button
            type="button"
            onClick=${() => setGrouping((g) => !g)}
            onMouseEnter=${(e) => showToolbarTip(e, grouping ? "Switch to flat list" : "Group issues by epic")}
            onMouseLeave=${hideToolbarTip}
            onMouseDown=${hideToolbarTip}
            aria-pressed=${grouping ? "true" : "false"}
            data-tip=${grouping ? "Switch to flat list" : "Group issues by epic"}
            aria-label=${grouping ? "Switch to flat list" : "Group issues by epic"}
            class="btn btn-xs join-item inline-flex ${grouping ? "btn-active" : "btn-ghost"}"
          >
            <${LayersIcon} className="w-3.5 h-3.5" />
          </button>
        </div>
        <select
          class="select select-xs shrink-0 w-28"
          value=${typeFilter}
          onInput=${(e) => setTypeFilter(e.target.value)}
        >
          <option value="all">All types</option>
          ${allTypes.map((t) => html`<option value=${t}>${t}</option>`)}
        </select>
        <input
          type="text"
          placeholder="Search id, title, body…"
          value=${search}
          onInput=${(e) => setSearch(e.target.value)}
          class="input input-xs flex-1 min-w-0"
        />
        <div class="relative shrink-0" ref=${sortMenuRef}>
          <button
            type="button"
            onClick=${() => setShowSortMenu((o) => !o)}
            onMouseEnter=${(e) => showToolbarTip(e, `Sort by ${SORT_FIELD_LABELS[sort.field]} (${sort.direction === "asc" ? "ascending" : "descending"})`)}
            onMouseLeave=${hideToolbarTip}
            onMouseDown=${hideToolbarTip}
            aria-haspopup="true"
            aria-expanded=${showSortMenu ? "true" : "false"}
            class="btn btn-xs gap-1 inline-flex ${showSortMenu ? "btn-active" : "btn-ghost"}"
            data-tip=${`Sort by ${SORT_FIELD_LABELS[sort.field]} (${sort.direction === "asc" ? "ascending" : "descending"})`}
            aria-label=${`Sort by ${SORT_FIELD_LABELS[sort.field]} (${sort.direction === "asc" ? "ascending" : "descending"})`}
            data-testid="beads-sort-button"
          >
            <${SortIcon} className="w-3.5 h-3.5" />
            ${
              sort.direction === "asc"
                ? html`<${ArrowUpIcon} className="w-3 h-3" />`
                : html`<${ArrowDownIcon} className="w-3 h-3" />`
            }
          </button>
          ${
            showSortMenu &&
            html`
              <ul
                class="menu absolute top-full right-0 mt-2 w-52 bg-base-200 rounded-box shadow-xl z-10"
                data-testid="beads-sort-menu"
              >
                <li class="menu-title">Sort by</li>
                ${SORT_FIELD_OPTIONS.map(
                  (opt) => html`
                    <li key=${opt.field}>
                      <button
                        type="button"
                        onClick=${() =>
                          setSort((s) => ({ ...s, field: opt.field }))}
                        class=${sort.field === opt.field ? "menu-active" : ""}
                      >
                        <span class="w-4 h-4 shrink-0">
                          ${sort.field === opt.field
                            ? html`<${CheckIcon} className="w-4 h-4" />`
                            : null}
                        </span>
                        <span class="flex-1">${opt.label}</span>
                      </button>
                    </li>
                  `,
                )}
                <li class="menu-title">Direction</li>
                ${[
                  { dir: "asc", label: "Ascending", Icon: ArrowUpIcon },
                  { dir: "desc", label: "Descending", Icon: ArrowDownIcon },
                ].map(
                  (d) => html`
                    <li key=${d.dir}>
                      <button
                        type="button"
                        onClick=${() =>
                          setSort((s) => ({ ...s, direction: d.dir }))}
                        class=${sort.direction === d.dir ? "menu-active" : ""}
                      >
                        <span class="w-4 h-4 shrink-0">
                          ${sort.direction === d.dir
                            ? html`<${CheckIcon} className="w-4 h-4" />`
                            : null}
                        </span>
                        <span class="flex-1">${d.label}</span>
                        <${d.Icon} className="w-3.5 h-3.5 opacity-60" />
                      </button>
                    </li>
                  `,
                )}
              </ul>
            `
          }
        </div>
        ${
          toolbarTip &&
          html`
            <${PortalTooltip}
              x=${toolbarTip.x}
              y=${toolbarTip.y}
              text=${toolbarTip.text}
            />
          `
        }
      </div>

      <div class="flex-1 overflow-y-auto overflow-x-auto beads-table-scroll" ref=${scrollContainerRef}>
        ${html`<div
          class="pull-to-refresh-indicator"
          style=${{
            height: refreshing || loading ? "40px" : `${pullDistance}px`,
            opacity: refreshing || loading ? 1 : Math.min(1, pullDistance / 70),
            overflow: "hidden",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            transition:
              pullDistance === 0
                ? "height 0.2s ease, opacity 0.2s ease"
                : "none",
            flexShrink: 0,
          }}
        >
          <span
            class="loading loading-spinner w-5 h-5 text-mitto-text-secondary"
          ></span>
        </div>`}
        ${
          !loading &&
          error &&
          html`
            <div
              class="flex items-center justify-center h-24 text-red-400 text-sm px-4"
            >
              ${error}
            </div>
          `
        }
        ${
          !loading &&
          !error &&
          filtered.length === 0 &&
          html`
            <div
              class="flex flex-col items-center justify-center gap-1 h-32 text-center px-4"
            >
              <div class="text-mitto-text-secondary text-sm">
                No issues found
              </div>
              <div class="text-mitto-text-muted text-xs">
                Create a new issue by pressing the "+" button below.
              </div>
            </div>
          `
        }
        ${
          !error &&
          filtered.length > 0 &&
          html`
            <div class="list p-2">
              ${grouping && groupedItems
                ? groupedItems.map((item) => {
                    if (item.type === "orphan")
                      return renderIssueRow(item.issue);
                    // Epic group: render recursively via renderEpicGroup.
                    // depth=1 → 2rem padding-left (matches the original pl-8 / 2rem).
                    return renderEpicGroup(item.group, 1);
                  })
                : filtered.map((issue) => renderIssueRow(issue))}
            </div>
          `
        }
      </div>

      <div class="flex items-center gap-1 p-4 border-t border-mitto-border shrink-0">
        <button
          onClick=${openCreate}
          class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top"
          data-tip="New issue"
          aria-label="New issue"
        >
          <${PlusIcon} className="w-4 h-4" />
        </button>
        <div class="relative" ref=${listPromptsRef}>
          <button
            type="button"
            onClick=${toggleListPrompts}
            class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top"
            data-tip="Run a prompt over the issue list in a new conversation"
            aria-label="Run a prompt over the issue list in a new conversation"
          >
            <${ChevronUpIcon} className="w-4 h-4" />
          </button>
          ${
            showListPrompts &&
            html`
              <ul
                class="menu absolute bottom-full left-0 mb-2 w-64 max-h-72 overflow-y-auto flex-nowrap bg-base-200 rounded-box shadow-xl z-10"
              >
                ${listPromptsLoading &&
                html`
                  <li class="px-3 py-2 flex items-center gap-2">
                    <span class="loading loading-spinner w-4 h-4"></span>
                    Loading…
                  </li>
                `}
                ${!listPromptsLoading &&
                listPrompts.length === 0 &&
                html` <li class="px-3 py-2 opacity-60">No task prompts</li> `}
                ${!listPromptsLoading &&
                listPrompts.map((p) => {
                  const PromptIcon = getPromptIconOrDefault(p.icon);
                  return html`
                    <li key=${p.name}>
                      <button
                        type="button"
                        onClick=${() => {
                          const mode = promptPeriodicMode(p);
                          const opts =
                            mode === "optional"
                              ? {
                                  asPeriodic:
                                    listPeriodicOn[p.name] !== undefined
                                      ? listPeriodicOn[p.name]
                                      : promptPeriodicDefaultOn(p),
                                }
                              : undefined;
                          handleRunListPrompt(p, opts);
                        }}
                        title=${p.description || p.name}
                      >
                        <span class="w-4 h-4 shrink-0"
                          ><${PromptIcon} className="w-4 h-4"
                        /></span>
                        <span class="truncate flex-1">${p.name}</span>
                        ${(() => {
                          const mode = promptPeriodicMode(p);
                          if (mode === "none") return null;
                          if (mode === "optional") {
                            const on =
                              listPeriodicOn[p.name] !== undefined
                                ? listPeriodicOn[p.name]
                                : promptPeriodicDefaultOn(p);
                            return html`<input
                              type="checkbox"
                              class="toggle toggle-primary shrink-0"
                              checked=${on}
                              title="Run as periodic (recurring) conversation"
                              onClick=${(e) => e.stopPropagation()}
                              onChange=${(e) => {
                                e.stopPropagation();
                                setListPeriodicOn((m) => ({
                                  ...m,
                                  [p.name]: e.target.checked,
                                }));
                              }}
                            />`;
                          }
                          // mode === "always": locked badge (unchanged look)
                          return html`<span
                            class="shrink-0 text-success opacity-80"
                            title="Periodic prompt — always sets the conversation to recurring mode"
                            ><${PeriodicIcon} className="w-3.5 h-3.5"
                          /></span>`;
                        })()}
                      </button>
                    </li>
                  `;
                })}
              </ul>
            `
          }
        </div>
        <button
          onClick=${fetchList}
          class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top"
          data-tip="Refresh"
          aria-label="Refresh"
        >
          <${RefreshIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${() => {
            if (closedCount === 0 || cleaningUp) return;
            setShowCleanupConfirm(true);
          }}
          aria-disabled=${closedCount === 0 || cleaningUp ? "true" : "false"}
          class="btn btn-ghost btn-square btn-sm group inline-flex tooltip tooltip-top ${closedCount === 0 || cleaningUp ? "opacity-40 pointer-events-none" : ""}"
          data-tip=${cleaningUp && cleanupProgress && cleanupProgress.total > 0 ? `Removing ${cleanupProgress.deleted}/${cleanupProgress.total}…` : closedCount === 0 ? "No closed issues to clean up" : `Clean up ${closedCount} closed issue${closedCount === 1 ? "" : "s"}`}
          aria-label=${closedCount === 0 ? "No closed issues to clean up" : `Clean up ${closedCount} closed issue${closedCount === 1 ? "" : "s"}`}
        >
          <${BroomIcon} className="w-4 h-4 group-hover:text-red-400" />
        </button>

        ${
          upstream &&
          upstream !== "none" &&
          html`
            <div
              class="flex items-center gap-1 pl-2 ml-1 border-l border-mitto-border"
            >
              ${upstream === "prompts"
                ? html`
                    <button
                      onClick=${() => {
                        if (!pullPromptName || !onLaunchPrompt) return;
                        onLaunchPrompt("pull", pullPromptName);
                      }}
                      aria-disabled=${!pullPromptName || !onLaunchPrompt
                        ? "true"
                        : "false"}
                      class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${!pullPromptName ||
                      !onLaunchPrompt
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip=${pullPromptName
                        ? `Pull: run "${pullPromptName}"`
                        : "No pull prompt configured"}
                      aria-label=${pullPromptName
                        ? `Pull: run "${pullPromptName}"`
                        : "No pull prompt configured"}
                    >
                      <${ArrowDownIcon} className="w-4 h-4" />
                    </button>
                    <button
                      onClick=${() => {
                        if (!pushPromptName || !onLaunchPrompt) return;
                        onLaunchPrompt("push", pushPromptName);
                      }}
                      aria-disabled=${!pushPromptName || !onLaunchPrompt
                        ? "true"
                        : "false"}
                      class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${!pushPromptName ||
                      !onLaunchPrompt
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip=${pushPromptName
                        ? `Push: run "${pushPromptName}"`
                        : "No push prompt configured"}
                      aria-label=${pushPromptName
                        ? `Push: run "${pushPromptName}"`
                        : "No push prompt configured"}
                    >
                      <${ArrowUpIcon} className="w-4 h-4" />
                    </button>
                    <button
                      onClick=${() => {
                        if (!syncPromptName || !onLaunchPrompt) return;
                        onLaunchPrompt("sync", syncPromptName);
                      }}
                      aria-disabled=${!syncPromptName || !onLaunchPrompt
                        ? "true"
                        : "false"}
                      class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${!syncPromptName ||
                      !onLaunchPrompt
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip=${syncPromptName
                        ? `Sync: run "${syncPromptName}"`
                        : "No sync prompt configured"}
                      aria-label=${syncPromptName
                        ? `Sync: run "${syncPromptName}"`
                        : "No sync prompt configured"}
                    >
                      <${SyncIcon} className="w-4 h-4" />
                    </button>
                  `
                : html`
                    <button
                      onClick=${() => {
                        if (syncAction) return;
                        handleSync("pull");
                      }}
                      aria-disabled=${syncAction ? "true" : "false"}
                      class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${syncAction
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip=${`Pull from ${UPSTREAM_LABELS[upstream] || upstream}`}
                      aria-label=${`Pull from ${UPSTREAM_LABELS[upstream] || upstream}`}
                    >
                      ${syncAction === "pull"
                        ? html`<span
                            class="loading loading-spinner w-4 h-4"
                          ></span>`
                        : html`<${ArrowDownIcon} className="w-4 h-4" />`}
                    </button>
                    <button
                      onClick=${() => {
                        if (syncAction) return;
                        handleSync("push");
                      }}
                      aria-disabled=${syncAction ? "true" : "false"}
                      class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${syncAction
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip=${`Push to ${UPSTREAM_LABELS[upstream] || upstream}`}
                      aria-label=${`Push to ${UPSTREAM_LABELS[upstream] || upstream}`}
                    >
                      ${syncAction === "push"
                        ? html`<span
                            class="loading loading-spinner w-4 h-4"
                          ></span>`
                        : html`<${ArrowUpIcon} className="w-4 h-4" />`}
                    </button>
                    <button
                      onClick=${() => {
                        if (syncAction) return;
                        handleSync("sync");
                      }}
                      aria-disabled=${syncAction ? "true" : "false"}
                      class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${syncAction
                        ? "opacity-40 pointer-events-none"
                        : ""}"
                      data-tip=${`Sync with ${UPSTREAM_LABELS[upstream] || upstream} (pull then push)`}
                      aria-label=${`Sync with ${UPSTREAM_LABELS[upstream] || upstream} (pull then push)`}
                    >
                      ${syncAction === "sync"
                        ? html`<span
                            class="loading loading-spinner w-4 h-4"
                          ></span>`
                        : html`<${SyncIcon} className="w-4 h-4" />`}
                    </button>
                  `}
            </div>
          `
        }

        ${shortcuts.length > 0 &&
          html`
            <div
              class="flex items-center gap-1 pl-2 ml-1 border-l border-mitto-border"
            >
              ${shortcuts.map((sc, i) => {
                const prompt = shortcutPromptMap.get(sc.prompt);
                const found = !!prompt;
                // Empty shortcut icon → fall back to the linked prompt's own icon.
                const Icon = getPromptIconOrDefault(sc.icon || prompt?.icon);
                return html`
                  <button
                    key=${i}
                    type="button"
                    onClick=${() => found && handleRunListPrompt(prompt)}
                    aria-disabled=${found ? "false" : "true"}
                    class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top ${found ? "" : "opacity-40 pointer-events-none"}"
                    data-tip=${found
                      ? `Run "${sc.prompt}"`
                      : `Prompt "${sc.prompt}" not found`}
                    aria-label=${found
                      ? `Run "${sc.prompt}"`
                      : `Prompt "${sc.prompt}" not found`}
                  >
                    <span class="w-4 h-4">
                      <${Icon} className="w-4 h-4" />
                    </span>
                  </button>
                `;
              })}
            </div>
          `}

        <span class="text-xs text-mitto-text-secondary ml-auto">${filtered.length} issue${filtered.length === 1 ? "" : "s"}</span>

        ${
          onOpenConfig &&
          html`
            <button
              onClick=${() => onOpenConfig()}
              class="btn btn-ghost btn-square btn-sm ml-2 inline-flex tooltip tooltip-top"
              data-tip="Tasks configuration"
              aria-label="Tasks configuration"
            >
              <${SettingsIcon} className="w-4 h-4" />
            </button>
          `
        }
      </div>
    </div>

    <${BeadsDetailPanel}
      issue=${selectedIssue}
      allIssues=${issues}
      isCreating=${isCreating}
      workingDir=${workingDir}
      onClose=${closePanel}
      onCreated=${fetchList}
      onUpdated=${fetchList}
      showToast=${showToast}
      onFetchPrompts=${onFetchBeadsPrompts}
      onRunPrompt=${handleRunPrompt}
      onDelete=${(issue) => setDeleteTarget(issue)}
      onToggleStatus=${handleToggleStatus}
      onToggleDefer=${handleToggleDefer}
      statusBusy=${statusBusy}
      onSelectIssue=${selectIssue}
      createParentId=${createParent}
    />
    </div>

    ${
      contextMenu &&
      html`
        <${ContextMenu}
          x=${contextMenu.x}
          y=${contextMenu.y}
          items=${contextMenuItems}
          onClose=${closeContextMenu}
        />
      `
    }

    <${ConfirmDialog}
      isOpen=${showCleanupConfirm}
      title="Clean up closed issues"
      message=${`This will permanently delete ${closedCount} closed issue${closedCount === 1 ? "" : "s"}. This cannot be undone.`}
      confirmLabel="Delete"
      cancelLabel="Cancel"
      confirmVariant="danger"
      isLoading=${cleaningUp}
      onConfirm=${handleCleanup}
      onCancel=${() => setShowCleanupConfirm(false)}
    />

    <${ConfirmDialog}
      isOpen=${!!deleteTarget}
      title=${deleteTargetDescendants.length > 0 ? "Delete epic" : "Delete issue"}
      message=${deleteTarget ? `This will permanently delete ${deleteTarget.id} — "${deleteTarget.title}". This cannot be undone.` : ""}
      confirmLabel="Delete"
      cancelLabel="Cancel"
      confirmVariant="danger"
      isLoading=${deletingIssue}
      onConfirm=${confirmDeleteIssue}
      onCancel=${() => setDeleteTarget(null)}
    >
      ${
        deleteTargetDescendants.length > 0 &&
        html`
          <div class="mt-3 space-y-2">
            <p class="text-sm text-mitto-text-secondary">
              This epic has ${deleteTargetDescendants.length} descendant
              issue${deleteTargetDescendants.length === 1 ? "" : "s"}. What
              should happen to
              ${deleteTargetDescendants.length === 1 ? "it" : "them"}?
            </p>
            <label class="flex items-start gap-3 cursor-pointer select-none">
              <input
                type="radio"
                name="child-action"
                value="none"
                checked=${childAction === "none"}
                disabled=${deletingIssue}
                onChange=${() => setChildAction("none")}
                class="radio radio-sm mt-0.5"
              />
              <span class="text-sm text-mitto-text-secondary"
                >Leave child issues unchanged</span
              >
            </label>
            ${deleteTargetOpenDescendants.length > 0 &&
            html`
              <label class="flex items-start gap-3 cursor-pointer select-none">
                <input
                  type="radio"
                  name="child-action"
                  value="close"
                  checked=${childAction === "close"}
                  disabled=${deletingIssue}
                  onChange=${() => setChildAction("close")}
                  class="radio radio-sm mt-0.5"
                />
                <span class="text-sm text-mitto-text-secondary">
                  Close the ${deleteTargetOpenDescendants.length} open child
                  issue${deleteTargetOpenDescendants.length === 1 ? "" : "s"}
                </span>
              </label>
            `}
            <label class="flex items-start gap-3 cursor-pointer select-none">
              <input
                type="radio"
                name="child-action"
                value="delete"
                checked=${childAction === "delete"}
                disabled=${deletingIssue}
                onChange=${() => setChildAction("delete")}
                class="radio radio-sm radio-error mt-0.5"
              />
              <span class="text-sm text-mitto-text-secondary">
                Delete all ${deleteTargetDescendants.length} child
                issue${deleteTargetDescendants.length === 1 ? "" : "s"}
                (permanent)
              </span>
            </label>
          </div>
        `
      }
    </${ConfirmDialog}>
  `;
}
