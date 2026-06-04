// Mitto Web Interface - BeadsView Component
// Displays a Beads (bd) issue list and detail view for a workspace.

const { html, useState, useEffect, useCallback, useMemo, useRef } = window.preact;

import { apiUrl, authFetch, secureFetch } from "../utils/index.js";
import { getBasename } from "../lib.js";
import { PlusIcon, CloseIcon, SpinnerIcon, TrashIcon, RefreshIcon, BroomIcon, ChevronUpIcon, CheckIcon } from "./Icons.js";
import { ContextMenu } from "./ContextMenu.js";
import { ConfirmDialog } from "./ConfirmDialog.js";

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
      return JSON.parse(text);
    } catch (_e) {
      // fall through to error object below
    }
  }
  return { error: (text && text.trim()) || `Request failed (HTTP ${res.status})` };
}

const PRIORITY_LABELS = { 0: "Critical", 1: "High", 2: "Medium", 3: "Low" };
const PRIORITY_COLORS = {
  0: "bg-red-600 text-white",
  1: "bg-orange-500 text-white",
  2: "bg-yellow-500 text-black",
  3: "bg-slate-600 text-white",
};

const STATUS_COLORS = {
  open: "bg-green-700 text-green-100",
  in_progress: "bg-blue-700 text-blue-100",
  closed: "bg-slate-600 text-white",
  blocked: "bg-red-700 text-red-100",
};

const TYPE_COLORS = {
  epic: "bg-purple-700 text-purple-100",
  feature: "bg-blue-700 text-blue-100",
  bug: "bg-red-700 text-red-100",
  task: "bg-slate-600 text-white",
  chore: "bg-slate-600 text-white",
};

function badge(text, colorClass) {
  return html`<span class="px-1.5 py-0.5 rounded text-xs font-medium ${colorClass}">${text}</span>`;
}

function priorityBadge(p) {
  const n = typeof p === "number" ? p : 3;
  return badge(PRIORITY_LABELS[n] ?? String(p), PRIORITY_COLORS[n] ?? PRIORITY_COLORS[3]);
}

function statusBadge(s) {
  const label = (s || "open").replace(/_/g, " ");
  return badge(label, STATUS_COLORS[s] ?? "bg-slate-600 text-white");
}

function typeBadge(t) {
  return badge(t || "task", TYPE_COLORS[t] ?? TYPE_COLORS.task);
}

function renderMarkdown(text) {
  if (!text) return null;
  if (typeof window !== "undefined" && window.marked && window.DOMPurify) {
    const raw = window.marked.parse(text);
    return window.DOMPurify.sanitize(raw, { USE_PROFILES: { html: true } });
  }
  return null;
}

// ---- Detail side panel ------------------------------------------------------

const ISSUE_TYPES = ["task", "feature", "epic", "bug", "chore"];

function labelValue(label, value) {
  if (value === null || value === undefined || value === "") return null;
  return html`
    <div>
      <div class="text-xs text-mitto-text-secondary mb-0.5">${label}</div>
      <div class="text-sm text-mitto-text break-words">${value}</div>
    </div>
  `;
}

/**
 * BeadsDetailPanel is a right-docked panel that serves two modes:
 *
 *  - View mode (an `issue` is provided): shows the read-only properties of a
 *    single issue, populated directly from the already-loaded list row so it
 *    opens instantly without an extra network request. Subtasks are computed
 *    from the full issue list via the parent field.
 *  - Create mode (`isCreating` is true): shows editable fields for a new issue
 *    plus a "Save" footer that POSTs to /api/beads/create.
 *
 * It matches the SessionPanel slide animation and look/feel.
 */
function BeadsDetailPanel({ issue, allIssues, isCreating, workingDir, onClose, onCreated, showToast, onFetchPrompts, onRunPrompt, onDelete, onToggleStatus, statusBusy }) {
  const isOpen = isCreating || !!issue;
  const [isClosing, setIsClosing] = useState(false);
  const [shouldRender, setShouldRender] = useState(isOpen);
  const lastIssueRef = useRef(issue);
  const lastCreatingRef = useRef(isCreating);
  if (issue) lastIssueRef.current = issue;
  if (isOpen) lastCreatingRef.current = isCreating;

  // Create-mode form state.
  const [title, setTitle] = useState("");
  const [type, setType] = useState("task");
  const [priority, setPriority] = useState(2); // 2 = Medium
  const [description, setDescription] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // View-mode "Prompts" dropup state. Prompts are loaded lazily the first time
  // the dropup is opened (per panel mount) via onFetchPrompts.
  const [showPrompts, setShowPrompts] = useState(false);
  const [prompts, setPrompts] = useState([]);
  const [promptsLoading, setPromptsLoading] = useState(false);
  const promptsRef = useRef(null);

  // Reset the form whenever create mode is (re)entered.
  useEffect(() => {
    if (isCreating) {
      setTitle("");
      setType("task");
      setPriority(2);
      setDescription("");
      setSubmitting(false);
    }
  }, [isCreating]);

  // Close the prompts dropup on outside click while it is open.
  useEffect(() => {
    if (!showPrompts) return undefined;
    const onDocClick = (e) => {
      if (promptsRef.current && !promptsRef.current.contains(e.target)) {
        setShowPrompts(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [showPrompts]);

  const togglePrompts = useCallback(() => {
    setShowPrompts((open) => {
      const next = !open;
      if (next && onFetchPrompts && workingDir) {
        setPromptsLoading(true);
        onFetchPrompts(workingDir)
          .then((list) => setPrompts(list || []))
          .finally(() => setPromptsLoading(false));
      }
      return next;
    });
  }, [onFetchPrompts, workingDir]);

  useEffect(() => {
    if (isOpen) {
      setShouldRender(true);
      setIsClosing(false);
    } else if (shouldRender) {
      setIsClosing(true);
      const timer = setTimeout(() => {
        setShouldRender(false);
        setIsClosing(false);
      }, 150);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  const handleClose = useCallback(() => {
    setIsClosing(true);
    setTimeout(() => onClose(), 150);
  }, [onClose]);

  const handleSave = useCallback(async () => {
    if (!title.trim()) return;
    setSubmitting(true);
    try {
      const body = { working_dir: workingDir, title: title.trim(), type, priority };
      if (description.trim()) body.description = description.trim();
      const res = await secureFetch(apiUrl("/api/beads/create"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to create issue" });
      } else {
        showToast && showToast({ style: "success", title: "Issue created" });
        onCreated && onCreated();
        onClose && onClose();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to create issue" });
    } finally {
      setSubmitting(false);
    }
  }, [workingDir, title, type, priority, description, showToast, onCreated, onClose]);

  // While closing, keep rendering whichever mode was last open.
  const creating = isOpen ? isCreating : lastCreatingRef.current;
  const data = issue || lastIssueRef.current;
  const md = useMemo(
    () => renderMarkdown(!creating && data && data.description),
    [creating, data && data.description],
  );
  const subtasks = useMemo(
    () => (!creating && data ? allIssues.filter(i => i.parent === data.id) : []),
    [creating, allIssues, data && data.id],
  );

  if (!shouldRender) return null;
  if (!creating && !data) return null;

  const inputClass = "w-full px-3 py-2 bg-mitto-input-box border border-mitto-border rounded text-sm text-mitto-text focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-mitto-text-secondary";
  const labelClass = "block text-xs font-medium text-mitto-text-secondary mb-1";

  return html`
    <div class="w-80 flex-shrink-0 bg-mitto-sidebar border-l border-mitto-border h-full flex flex-col properties-panel ${isClosing ? "closing" : ""}">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border flex-shrink-0">
        <div class="flex-1 min-w-0">
          ${creating
            ? html`<h2 class="font-semibold text-base text-mitto-text">New Issue</h2>`
            : html`
              <div class="font-mono text-xs text-mitto-text-secondary">${data.id}</div>
              <h2 class="font-semibold text-base text-mitto-text break-words">${data.title}</h2>
            `}
        </div>
        <button
          onClick=${handleClose}
          class="p-1 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text flex-shrink-0"
          title="Close"
        >
          <${CloseIcon} className="w-5 h-5" />
        </button>
      </div>

      <div class="flex-1 overflow-y-auto p-4 space-y-4">
        ${creating
          ? html`
            <div>
              <label class=${labelClass}>Title <span class="text-red-400">*</span></label>
              <input
                type="text"
                class=${inputClass}
                placeholder="Issue title"
                value=${title}
                onInput=${e => setTitle(e.target.value)}
                disabled=${submitting}
                autoFocus
              />
            </div>

            <div class="flex gap-3">
              <div class="flex-1">
                <label class=${labelClass}>Type</label>
                <select
                  class=${inputClass}
                  value=${type}
                  onInput=${e => setType(e.target.value)}
                  disabled=${submitting}
                >
                  ${ISSUE_TYPES.map(t => html`<option value=${t}>${t}</option>`)}
                </select>
              </div>
              <div class="flex-1">
                <label class=${labelClass}>Priority</label>
                <select
                  class=${inputClass}
                  value=${priority}
                  onInput=${e => setPriority(Number(e.target.value))}
                  disabled=${submitting}
                >
                  ${Object.entries(PRIORITY_LABELS).map(([n, label]) =>
                    html`<option value=${n}>${label}</option>`
                  )}
                </select>
              </div>
            </div>

            <div>
              <label class=${labelClass}>Description</label>
              <textarea
                class="${inputClass} resize-none"
                rows="6"
                placeholder="Optional description…"
                value=${description}
                onInput=${e => setDescription(e.target.value)}
                disabled=${submitting}
              ></textarea>
            </div>
          `
          : html`
            <div class="flex flex-wrap gap-2 items-center">
              ${typeBadge(data.issue_type)}
              ${statusBadge(data.status)}
              ${priorityBadge(data.priority)}
            </div>

            <div class="grid grid-cols-2 gap-3">
              ${labelValue("Assignee", data.assignee)}
              ${labelValue("Owner", data.owner)}
              ${labelValue("Created", data.created_at && new Date(data.created_at).toLocaleDateString())}
              ${labelValue("Updated", data.updated_at && new Date(data.updated_at).toLocaleDateString())}
              ${data.parent && labelValue("Parent", html`<span class="font-mono">${data.parent}</span>`)}
            </div>

            ${data.description && html`
              <div>
                <div class="text-xs text-mitto-text-secondary mb-1">Description</div>
                <div class="border border-mitto-border rounded p-3 bg-mitto-input-box">
                  ${md
                    ? html`<div class="text-mitto-text text-sm max-w-none" dangerouslySetInnerHTML=${{ __html: md }} />`
                    : html`<pre class="whitespace-pre-wrap text-sm text-mitto-text">${data.description}</pre>`
                  }
                </div>
              </div>
            `}

            ${subtasks.length > 0 && html`
              <div>
                <div class="text-xs font-semibold text-mitto-text-secondary uppercase tracking-wide mb-1">Subtasks (${subtasks.length})</div>
                <ul class="space-y-1">
                  ${subtasks.map(c => html`
                    <li key=${c.id} class="flex items-center gap-2 text-sm text-mitto-text">
                      ${statusBadge(c.status)}
                      <span class="font-mono text-mitto-text-secondary text-xs">${c.id}</span>
                      <span class="truncate">${c.title}</span>
                    </li>
                  `)}
                </ul>
              </div>
            `}
          `}
      </div>

      ${creating && html`
        <div class="flex justify-end gap-3 p-3 border-t border-mitto-border flex-shrink-0">
          <button
            type="button"
            onClick=${handleClose}
            disabled=${submitting}
            class="px-4 py-2 text-sm hover:bg-mitto-input-box rounded-lg transition-colors text-mitto-text-secondary disabled:opacity-50"
          >
            Close
          </button>
          <button
            type="button"
            onClick=${handleSave}
            disabled=${!title.trim() || submitting}
            class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2"
          >
            ${submitting && html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`}
            Save
          </button>
        </div>
      `}

      ${!creating && data && html`
        <div class="flex items-center gap-1 p-4 border-t border-mitto-border flex-shrink-0 relative">
          <div class="relative" ref=${promptsRef}>
            <button
              type="button"
              onClick=${togglePrompts}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text"
              title="Run a prompt for this issue in a new conversation"
            >
              <${ChevronUpIcon} className="w-4 h-4" />
            </button>
            ${showPrompts && html`
              <div class="absolute bottom-full left-0 mb-2 w-64 max-h-72 overflow-y-auto bg-mitto-sidebar border border-mitto-border rounded-lg shadow-lg z-10 py-1">
                ${promptsLoading && html`
                  <div class="flex items-center gap-2 px-3 py-2 text-sm text-mitto-text-secondary">
                    <${SpinnerIcon} className="w-4 h-4 animate-spin" /> Loading…
                  </div>
                `}
                ${!promptsLoading && prompts.length === 0 && html`
                  <div class="px-3 py-2 text-sm text-mitto-text-secondary">No beads prompts</div>
                `}
                ${!promptsLoading && prompts.map(p => html`
                  <button
                    key=${p.name}
                    type="button"
                    onClick=${() => { setShowPrompts(false); onRunPrompt && onRunPrompt(p, data); }}
                    title=${p.description || p.name}
                    class="w-full text-left px-3 py-2 text-sm text-mitto-text hover:bg-mitto-input-box transition-colors"
                  >
                    ${p.name}
                  </button>
                `)}
              </div>
            `}
          </div>

          <div class="flex items-center gap-1 ml-auto">
            <button
              type="button"
              onClick=${() => onToggleStatus && onToggleStatus(data)}
              disabled=${statusBusy}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text disabled:opacity-40 disabled:cursor-not-allowed"
              title=${data.status === "closed" ? "Reopen issue" : "Close issue"}
            >
              ${data.status === "closed"
                ? html`<${RefreshIcon} className="w-4 h-4" />`
                : html`<${CheckIcon} className="w-4 h-4" />`}
            </button>
            <button
              type="button"
              onClick=${() => onDelete && onDelete(data)}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-red-400"
              title="Delete issue"
            >
              <${TrashIcon} className="w-4 h-4" />
            </button>
          </div>
        </div>
      `}
    </div>
  `;
}

// ---- Main BeadsView ---------------------------------------------------------

/**
 * BeadsView renders the Beads issue list and detail panel for a workspace.
 * @param {string} workingDir - Absolute path of the workspace directory.
 * @param {function} showToast - Toast notification helper from parent.
 * @param {function} onFetchBeadsPrompts - Async (workingDir) => prompts whose
 *        `menus` list includes `beads`; populates the per-issue context menu.
 * @param {function} onRunBeadsPrompt - (prompt, issue) => starts a new
 *        conversation seeded with the prompt text and the issue's context.
 */
export function BeadsView({ workingDir, showToast, onFetchBeadsPrompts, onRunBeadsPrompt }) {
  const [issues, setIssues] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [selectedIssue, setSelectedIssue] = useState(null);
  const [isCreating, setIsCreating] = useState(false);

  const [statusFilter, setStatusFilter] = useState("all");
  const [typeFilter, setTypeFilter] = useState("all");
  const [search, setSearch] = useState("");

  // Per-issue right-click context menu. `contextMenu` holds the click position
  // and the issue it targets; `menuPrompts` are the `menus: beads` prompts shown
  // in the "Prompts" submenu. Actions are not wired to behavior yet.
  const [contextMenu, setContextMenu] = useState(null);
  const [menuPrompts, setMenuPrompts] = useState([]);

  // "Clean up closed issues" confirmation + in-flight state.
  const [showCleanupConfirm, setShowCleanupConfirm] = useState(false);
  const [cleaningUp, setCleaningUp] = useState(false);

  // Single-issue delete confirmation target + in-flight state, and the
  // in-flight flag for the close/reopen status toggle.
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deletingIssue, setDeletingIssue] = useState(false);
  const [statusBusy, setStatusBusy] = useState(false);

  const workspaceLabel = workingDir ? getBasename(workingDir) : "Workspace";

  const fetchList = useCallback(async () => {
    if (!workingDir) return;
    setLoading(true);
    setError(null);
    try {
      const res = await authFetch(apiUrl("/api/beads/list") + "?working_dir=" + encodeURIComponent(workingDir));
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

  // The list rows already carry all rich fields (description, parent, dates,
  // assignee, owner), so the detail panel is populated directly from the row —
  // no extra /show request needed. Clicking the open row again toggles it shut.
  const selectIssue = useCallback((issue) => {
    setIsCreating(false);
    setSelectedIssue(prev => (prev && prev.id === issue.id ? null : issue));
  }, []);

  // Open the side panel in "create" mode for a brand-new issue.
  const openCreate = useCallback(() => {
    setSelectedIssue(null);
    setIsCreating(true);
  }, []);

  // Close the side panel, whether it is in view or create mode.
  const closePanel = useCallback(() => {
    setSelectedIssue(null);
    setIsCreating(false);
  }, []);

  // Open the per-issue context menu at the cursor and load the `menus: beads`
  // prompts for this workspace so the "Prompts" submenu reflects them.
  const handleRowContextMenu = useCallback(
    (e, issue) => {
      e.preventDefault();
      e.stopPropagation();
      setContextMenu({ x: e.clientX, y: e.clientY, issue });
      if (onFetchBeadsPrompts) {
        onFetchBeadsPrompts(workingDir).then((prompts) =>
          setMenuPrompts(prompts || []),
        );
      }
    },
    [onFetchBeadsPrompts, workingDir],
  );

  const closeContextMenu = useCallback(() => setContextMenu(null), []);

  // Keep the open detail panel in sync when the list refreshes: replace it with
  // the fresh row if it still exists, otherwise close the panel.
  useEffect(() => {
    setSelectedIssue(prev => {
      if (!prev) return prev;
      return issues.find(i => i.id === prev.id) || null;
    });
  }, [issues]);

  const filtered = useMemo(() => {
    return issues.filter(issue => {
      if (statusFilter !== "all" && issue.status !== statusFilter) return false;
      if (typeFilter !== "all" && issue.issue_type !== typeFilter) return false;
      if (search) {
        const q = search.toLowerCase();
        if (!(issue.id?.toLowerCase().includes(q) ||
              issue.title?.toLowerCase().includes(q) ||
              issue.owner?.toLowerCase().includes(q))) return false;
      }
      return true;
    });
  }, [issues, statusFilter, typeFilter, search]);

  const allStatuses = useMemo(() => [...new Set(issues.map(i => i.status).filter(Boolean))], [issues]);
  const allTypes = useMemo(() => [...new Set(issues.map(i => i.issue_type).filter(Boolean))], [issues]);

  const closedCount = useMemo(() => issues.filter(i => i.status === "closed").length, [issues]);

  // Permanently delete every closed issue, then refresh the list. The confirm
  // dialog gates this destructive action.
  const handleCleanup = useCallback(async () => {
    setCleaningUp(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/cleanup"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast && showToast({ style: "error", title: data.error || "Failed to clean up issues" });
      } else {
        const n = data.deleted || 0;
        showToast && showToast({
          style: "success",
          title: n === 0 ? "No closed issues to remove" : `Removed ${n} closed issue${n === 1 ? "" : "s"}`,
        });
        fetchList();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to clean up issues" });
    } finally {
      setCleaningUp(false);
      setShowCleanupConfirm(false);
    }
  }, [workingDir, showToast, fetchList]);

  // Permanently delete a single issue, then refresh the list. The confirm
  // dialog (gated on deleteTarget) calls this.
  const confirmDeleteIssue = useCallback(async () => {
    if (!deleteTarget) return;
    setDeletingIssue(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/delete"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: deleteTarget.id }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast && showToast({ style: "error", title: data.error || "Failed to delete issue" });
      } else {
        showToast && showToast({ style: "success", title: `Deleted ${deleteTarget.id}` });
        fetchList();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to delete issue" });
    } finally {
      setDeletingIssue(false);
      setDeleteTarget(null);
    }
  }, [deleteTarget, workingDir, showToast, fetchList]);

  // Close or reopen a single issue depending on its current status, then refresh.
  const handleToggleStatus = useCallback(async (issue) => {
    if (!issue) return;
    const action = issue.status === "closed" ? "reopen" : "close";
    setStatusBusy(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/status"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: issue.id, action }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast && showToast({ style: "error", title: data.error || `Failed to ${action} issue` });
      } else {
        showToast && showToast({ style: "success", title: action === "close" ? `Closed ${issue.id}` : `Reopened ${issue.id}` });
        fetchList();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || `Failed to ${action} issue` });
    } finally {
      setStatusBusy(false);
    }
  }, [workingDir, showToast, fetchList]);

  // Run a beads prompt for a specific issue: delegates to the parent, which
  // creates a new conversation seeded with the prompt text and issue context.
  const handleRunPrompt = useCallback((prompt, issue) => {
    closeContextMenu();
    onRunBeadsPrompt && onRunBeadsPrompt(prompt, issue);
  }, [onRunBeadsPrompt, closeContextMenu]);

  // Build the per-issue context menu: a Delete action plus a "Prompts" submenu
  // listing every `menus: beads` prompt, both wired to the same handlers used
  // by the detail panel footer.
  const promptSubmenuItems = (menuPrompts || [])
    .filter((p) => p && p.name)
    .map((p) => ({
      label: p.name,
      onClick: () => handleRunPrompt(p, contextMenu && contextMenu.issue),
    }));

  const contextMenuItems = [
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => contextMenu && setDeleteTarget(contextMenu.issue),
      danger: true,
    },
    ...(promptSubmenuItems.length > 0
      ? [{ label: "Prompts", submenu: promptSubmenuItems }]
      : []),
  ];

  return html`
    <div class="flex h-full overflow-hidden">
    <div class="flex flex-col flex-1 min-w-0 overflow-hidden">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border flex-shrink-0">
        <span class="font-semibold text-lg flex-1">Beads — ${workspaceLabel}</span>
      </div>

      <div class="flex items-center gap-2 px-4 py-2 border-b border-mitto-border flex-shrink-0 flex-wrap">
        <select
          class="bg-mitto-input-box border border-mitto-border rounded px-2 py-1 text-xs text-mitto-text"
          value=${statusFilter}
          onInput=${e => setStatusFilter(e.target.value)}
        >
          <option value="all">All statuses</option>
          ${allStatuses.map(s => html`<option value=${s}>${s.replace(/_/g, " ")}</option>`)}
        </select>
        <select
          class="bg-mitto-input-box border border-mitto-border rounded px-2 py-1 text-xs text-mitto-text"
          value=${typeFilter}
          onInput=${e => setTypeFilter(e.target.value)}
        >
          <option value="all">All types</option>
          ${allTypes.map(t => html`<option value=${t}>${t}</option>`)}
        </select>
        <input
          type="text"
          placeholder="Search…"
          value=${search}
          onInput=${e => setSearch(e.target.value)}
          class="bg-mitto-input-box border border-mitto-border rounded px-2 py-1 text-xs text-mitto-text flex-1 min-w-0 placeholder-mitto-text-secondary"
        />
      </div>

      <div class="flex-1 overflow-y-auto">
        ${loading && html`
          <div class="flex items-center justify-center h-24 text-mitto-text-secondary gap-2">
            <${SpinnerIcon} className="w-4 h-4 animate-spin" /> Loading issues…
          </div>
        `}
        ${!loading && error && html`
          <div class="flex items-center justify-center h-24 text-red-400 text-sm px-4">${error}</div>
        `}
        ${!loading && !error && filtered.length === 0 && html`
          <div class="flex items-center justify-center h-24 text-mitto-text-secondary text-sm">No issues found</div>
        `}
        ${!loading && !error && filtered.length > 0 && html`
          <table class="w-full text-sm text-left border-collapse">
            <thead class="sticky top-0 bg-mitto-sidebar text-xs text-mitto-text-secondary uppercase tracking-wide">
              <tr>
                <th class="px-3 py-2">ID</th>
                <th class="px-3 py-2">Type</th>
                <th class="px-3 py-2">Status</th>
                <th class="px-3 py-2">Title</th>
                <th class="px-3 py-2">Assignee</th>
                <th class="px-3 py-2">Priority</th>
              </tr>
            </thead>
            <tbody>
              ${filtered.map(issue => html`
                <tr
                  key=${issue.id}
                  data-has-context-menu
                  class="border-t border-mitto-border hover:bg-mitto-input-box cursor-pointer select-none transition-colors ${selectedIssue && selectedIssue.id === issue.id ? "bg-mitto-input-box" : ""}"
                  onClick=${() => selectIssue(issue)}
                  onContextMenu=${(e) => handleRowContextMenu(e, issue)}
                >
                  <td class="px-3 py-2 font-mono text-mitto-text-secondary text-xs whitespace-nowrap">${issue.id}</td>
                  <td class="px-3 py-2 whitespace-nowrap">${typeBadge(issue.issue_type)}</td>
                  <td class="px-3 py-2 whitespace-nowrap">${statusBadge(issue.status)}</td>
                  <td class="px-3 py-2 text-mitto-text max-w-xs truncate">${issue.title}</td>
                  <td class="px-3 py-2 text-mitto-text-secondary text-xs whitespace-nowrap">${issue.owner || ""}</td>
                  <td class="px-3 py-2 whitespace-nowrap">${priorityBadge(issue.priority)}</td>
                </tr>
              `)}
            </tbody>
          </table>
        `}
      </div>

      <div class="flex items-center gap-1 p-4 border-t border-mitto-border flex-shrink-0">
        <button
          onClick=${openCreate}
          class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text"
          title="New issue"
        >
          <${PlusIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${fetchList}
          class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text"
          title="Refresh"
        >
          <${RefreshIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${() => setShowCleanupConfirm(true)}
          disabled=${closedCount === 0 || cleaningUp}
          class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-red-400 disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:text-mitto-text-secondary disabled:hover:bg-transparent"
          title=${closedCount === 0 ? "No closed issues to clean up" : `Clean up ${closedCount} closed issue${closedCount === 1 ? "" : "s"}`}
        >
          <${BroomIcon} className="w-4 h-4" />
        </button>
        <span class="text-xs text-mitto-text-secondary ml-auto">${filtered.length} issue${filtered.length === 1 ? "" : "s"}</span>
      </div>
    </div>

    <${BeadsDetailPanel}
      issue=${selectedIssue}
      allIssues=${issues}
      isCreating=${isCreating}
      workingDir=${workingDir}
      onClose=${closePanel}
      onCreated=${fetchList}
      showToast=${showToast}
      onFetchPrompts=${onFetchBeadsPrompts}
      onRunPrompt=${handleRunPrompt}
      onDelete=${(issue) => setDeleteTarget(issue)}
      onToggleStatus=${handleToggleStatus}
      statusBusy=${statusBusy}
    />
    </div>

    ${contextMenu && html`
      <${ContextMenu}
        x=${contextMenu.x}
        y=${contextMenu.y}
        items=${contextMenuItems}
        onClose=${closeContextMenu}
      />
    `}

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
      title="Delete issue"
      message=${deleteTarget ? `This will permanently delete ${deleteTarget.id} — "${deleteTarget.title}". This cannot be undone.` : ""}
      confirmLabel="Delete"
      cancelLabel="Cancel"
      confirmVariant="danger"
      isLoading=${deletingIssue}
      onConfirm=${confirmDeleteIssue}
      onCancel=${() => setDeleteTarget(null)}
    />
  `;
}

