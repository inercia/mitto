// Mitto Web Interface - BeadsView Component
// Displays a Beads (bd) issue list and detail view for a workspace.

const { html, useState, useEffect, useCallback, useMemo, useRef } = window.preact;

import { apiUrl, authFetch, secureFetch } from "../utils/index.js";
import { getBasename } from "../lib.js";
import { PlusIcon, CloseIcon, SpinnerIcon, TrashIcon, RefreshIcon, BroomIcon, ChevronUpIcon, CheckIcon, MenuIcon, ArrowDownIcon, ArrowUpIcon, SyncIcon, SettingsIcon } from "./Icons.js";
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

// Display labels for the folder's configured upstream task system.
const UPSTREAM_LABELS = { jira: "Jira", github: "GitHub", gitlab: "GitLab", linear: "Linear" };

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
function BeadsDetailPanel({ issue, allIssues, isCreating, workingDir, onClose, onCreated, onUpdated, showToast, onFetchPrompts, onRunPrompt, onDelete, onToggleStatus, statusBusy }) {
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
  const panelRef = useRef(null);

  // View-mode inline description editing. Clicking the rendered description
  // switches it to a textarea; blur saves via /api/beads/update when the text
  // changed. `descDraft` holds the in-progress text; `savingDesc` gates the
  // in-flight request.
  const [editingDesc, setEditingDesc] = useState(false);
  const [descDraft, setDescDraft] = useState("");
  const [savingDesc, setSavingDesc] = useState(false);
  // Min height (px) applied to the edit textarea so it does not shrink relative
  // to the rendered description it replaces. Measured from descViewRef on entry.
  const [descMinHeight, setDescMinHeight] = useState(0);
  const descRef = useRef(null);
  const descViewRef = useRef(null);

  // View-mode inline title editing. Clicking the rendered title in the header
  // switches it to a text input; blur saves via /api/beads/update when the text
  // changed. `titleDraft` holds the in-progress text; `savingTitle` gates the
  // in-flight request. `titleCancelRef` lets Escape blur without saving.
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const [savingTitle, setSavingTitle] = useState(false);
  const titleRef = useRef(null);
  const titleCancelRef = useRef(false);

  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/beads/show when an issue is opened. `depsBusy` gates add/remove
  // requests; `newDepType`/`newDepId` back the "add dependency" row.
  const [deps, setDeps] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");

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

  // Close the whole panel when clicking outside of it. Clicks on an issue row
  // are ignored (rows manage their own open/toggle), as are clicks inside a
  // floating menu or dialog layered above (z-50), so those flows still work.
  useEffect(() => {
    if (!isOpen) return undefined;
    const onDocPointer = (e) => {
      if (e.button !== 0) return;
      if (panelRef.current && panelRef.current.contains(e.target)) return;
      if (e.target.closest &&
          (e.target.closest("[data-has-context-menu]") || e.target.closest(".z-50"))) {
        return;
      }
      onClose();
    };
    const tid = setTimeout(() => document.addEventListener("mousedown", onDocPointer), 10);
    return () => {
      clearTimeout(tid);
      document.removeEventListener("mousedown", onDocPointer);
    };
  }, [isOpen, onClose]);

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

  // Leave description-edit mode whenever the viewed issue changes so a new issue
  // never opens showing the previous one's draft.
  useEffect(() => {
    setEditingDesc(false);
    setSavingDesc(false);
    setEditingTitle(false);
    setSavingTitle(false);
  }, [data && data.id]);

  // Focus the textarea (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingDesc && descRef.current) {
      const el = descRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingDesc]);

  // Focus the title input (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingTitle && titleRef.current) {
      const el = titleRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingTitle]);

  // Enter inline edit mode, seeding the draft from the current description.
  // Capture the rendered area's height first so the textarea opens at least as
  // tall as the content it replaces (it can still grow via min-height/rows).
  const startEditDesc = useCallback(() => {
    if (savingDesc) return;
    if (descViewRef.current) setDescMinHeight(descViewRef.current.offsetHeight);
    setDescDraft((data && data.description) || "");
    setEditingDesc(true);
  }, [data && data.description, savingDesc]);

  // Persist the edited description on blur. Saves only when the text changed;
  // otherwise just leaves edit mode. Uses /api/beads/update and refreshes the
  // list via onUpdated so the panel re-renders with the saved value.
  const handleDescBlur = useCallback(async () => {
    const original = (data && data.description) || "";
    const next = descDraft;
    if (next === original) {
      setEditingDesc(false);
      return;
    }
    setSavingDesc(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/update"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: data.id, description: next }),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to update description" });
      } else {
        showToast && showToast({ style: "success", title: "Description updated" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to update description" });
    } finally {
      setSavingDesc(false);
      setEditingDesc(false);
    }
  }, [data && data.id, data && data.description, descDraft, workingDir, showToast, onUpdated]);

  // Enter inline title-edit mode, seeding the draft from the current title.
  const startEditTitle = useCallback(() => {
    if (savingTitle) return;
    titleCancelRef.current = false;
    setTitleDraft((data && data.title) || "");
    setEditingTitle(true);
  }, [data && data.title, savingTitle]);

  // Persist the edited title on blur. Saves only when the (non-empty) text
  // changed; otherwise just leaves edit mode. Escape sets titleCancelRef so the
  // blur it triggers discards the draft. Uses /api/beads/update and refreshes
  // the list via onUpdated so the panel re-renders with the saved value.
  const handleTitleBlur = useCallback(async () => {
    if (titleCancelRef.current) {
      titleCancelRef.current = false;
      setEditingTitle(false);
      return;
    }
    const original = (data && data.title) || "";
    const next = titleDraft.trim();
    if (next === "" || next === original) {
      setEditingTitle(false);
      return;
    }
    setSavingTitle(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/update"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: data.id, title: next }),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to update title" });
      } else {
        showToast && showToast({ style: "success", title: "Title updated" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to update title" });
    } finally {
      setSavingTitle(false);
      setEditingTitle(false);
    }
  }, [data && data.id, data && data.title, titleDraft, workingDir, showToast, onUpdated]);

  // Enter saves (via blur); Escape discards the draft (via blur).
  const handleTitleKeyDown = useCallback((e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      titleCancelRef.current = true;
      e.target.blur();
    }
  }, []);

  // Load the issue's full dependency edges. The list row only carries a count,
  // so the actual edges come from /api/beads/show (its `dependencies` array).
  const fetchDeps = useCallback(async () => {
    if (!workingDir || !data || !data.id) return;
    setDepsLoading(true);
    try {
      const res = await authFetch(
        apiUrl("/api/beads/show") + "?working_dir=" + encodeURIComponent(workingDir) + "&id=" + encodeURIComponent(data.id),
      );
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        setDeps([]);
      } else {
        const issueObj = Array.isArray(respData) ? respData[0] : respData;
        setDeps((issueObj && issueObj.dependencies) || []);
      }
    } catch (_err) {
      setDeps([]);
    } finally {
      setDepsLoading(false);
    }
  }, [workingDir, data && data.id]);

  // Fetch dependencies whenever a (non-create) issue is opened or switched.
  useEffect(() => {
    setDeps([]);
    setNewDepId("");
    setNewDepType("blocks");
    if (isOpen && !creating && data && data.id) {
      fetchDeps();
    }
  }, [isOpen, creating, data && data.id]);

  // Add or remove a dependency edge via /api/beads/dep, then refresh both the
  // dependency list and the parent issue list (so counts stay current).
  const mutateDep = useCallback(async (action, dependsOn, depType) => {
    if (!data || !data.id || !dependsOn) return;
    setDepsBusy(true);
    try {
      const body = { working_dir: workingDir, id: data.id, depends_on: dependsOn, action };
      if (action === "add") body.type = depType || "blocks";
      const res = await secureFetch(apiUrl("/api/beads/dep"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || `Failed to ${action} dependency` });
        return false;
      }
      showToast && showToast({ style: "success", title: action === "add" ? `Added dependency on ${dependsOn}` : `Removed dependency on ${dependsOn}` });
      await fetchDeps();
      onUpdated && onUpdated();
      return true;
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || `Failed to ${action} dependency` });
      return false;
    } finally {
      setDepsBusy(false);
    }
  }, [data && data.id, workingDir, showToast, fetchDeps, onUpdated]);

  const handleAddDep = useCallback(async () => {
    const target = newDepId.trim();
    if (!target || depsBusy) return;
    const ok = await mutateDep("add", target, newDepType);
    if (ok) setNewDepId("");
  }, [newDepId, newDepType, depsBusy, mutateDep]);

  // Change the kind of an existing edge. bd has no in-place type update, so this
  // removes the edge and re-adds it with the new type. A single combined toast
  // and refresh is issued at the end.
  const changeDepType = useCallback(async (dependsOn, nextType) => {
    if (!data || !data.id || !dependsOn || depsBusy) return;
    setDepsBusy(true);
    try {
      const post = (body) => secureFetch(apiUrl("/api/beads/dep"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      let res = await post({ working_dir: workingDir, id: data.id, depends_on: dependsOn, action: "remove" });
      let respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to change dependency type" });
        return;
      }
      res = await post({ working_dir: workingDir, id: data.id, depends_on: dependsOn, type: nextType, action: "add" });
      respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to change dependency type" });
      } else {
        showToast && showToast({ style: "success", title: `Changed ${dependsOn} to ${nextType}` });
      }
      await fetchDeps();
      onUpdated && onUpdated();
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to change dependency type" });
    } finally {
      setDepsBusy(false);
    }
  }, [data && data.id, workingDir, depsBusy, showToast, fetchDeps, onUpdated]);

  if (!shouldRender) return null;
  if (!creating && !data) return null;

  const inputClass = "w-full px-3 py-2 bg-mitto-input-box border border-mitto-border rounded text-sm text-mitto-text focus:outline-none focus:ring-1 focus:ring-blue-500 placeholder-mitto-text-secondary";
  const labelClass = "block text-xs font-medium text-mitto-text-secondary mb-1";

  return html`
    <div ref=${panelRef} class="w-80 flex-shrink-0 bg-mitto-sidebar border-l border-mitto-border h-full flex flex-col properties-panel ${isClosing ? "closing" : ""}">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border flex-shrink-0">
        <div class="flex-1 min-w-0">
          ${creating
            ? html`<h2 class="font-semibold text-base text-mitto-text">New Issue</h2>`
            : html`
              <div class="font-mono text-xs text-mitto-text-secondary">${data.id}</div>
              ${editingTitle
                ? html`
                  <input
                    ref=${titleRef}
                    type="text"
                    class="${inputClass} font-semibold text-base"
                    value=${titleDraft}
                    onInput=${e => setTitleDraft(e.target.value)}
                    onBlur=${handleTitleBlur}
                    onKeyDown=${handleTitleKeyDown}
                    disabled=${savingTitle}
                  />
                `
                : html`
                  <h2
                    class="font-semibold text-base text-mitto-text break-words cursor-text rounded px-1 -mx-1 hover:bg-mitto-input-box transition-colors"
                    onClick=${startEditTitle}
                    title="Click to edit"
                  >${data.title}</h2>
                `}
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

            <div>
              <div class="text-xs text-mitto-text-secondary mb-1">Description</div>
              ${editingDesc
                ? html`
                  <textarea
                    ref=${descRef}
                    class="${inputClass} resize-y"
                    rows="6"
                    style=${descMinHeight ? `min-height:${descMinHeight}px` : null}
                    placeholder="Add a description…"
                    value=${descDraft}
                    onInput=${e => setDescDraft(e.target.value)}
                    onBlur=${handleDescBlur}
                    disabled=${savingDesc}
                  ></textarea>
                `
                : html`
                  <div
                    ref=${descViewRef}
                    class="border border-mitto-border rounded p-3 bg-mitto-input-box cursor-text hover:border-mitto-text-secondary transition-colors relative"
                    onClick=${startEditDesc}
                    title="Click to edit"
                  >
                    ${savingDesc && html`<${SpinnerIcon} className="w-4 h-4 animate-spin absolute top-2 right-2 text-mitto-text-secondary" />`}
                    ${data.description
                      ? (md
                          ? html`<div class="text-mitto-text text-sm max-w-none" dangerouslySetInnerHTML=${{ __html: md }} />`
                          : html`<pre class="whitespace-pre-wrap text-sm text-mitto-text">${data.description}</pre>`)
                      : html`<span class="text-sm text-mitto-text-secondary italic">No description. Click to add one.</span>`
                    }
                  </div>
                `}
            </div>

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

            <div>
              <div class="text-xs font-semibold text-mitto-text-secondary uppercase tracking-wide mb-1">Dependencies</div>

              <datalist id="beads-dep-options">
                ${(allIssues || [])
                  .filter(i => i.id !== data.id && !deps.some(d => d.id === i.id))
                  .map(i => html`<option key=${i.id} value=${i.id}>${i.title}</option>`)}
              </datalist>

              ${depsLoading
                ? html`
                  <div class="flex items-center gap-2 text-xs text-mitto-text-secondary">
                    <${SpinnerIcon} className="w-3 h-3 animate-spin" /> Loading…
                  </div>
                `
                : html`
                  <div class="space-y-1">
                    ${deps.length === 0 && html`
                      <div class="text-xs text-mitto-text-secondary italic">No dependencies.</div>
                    `}
                    ${deps.map(d => html`
                      <div key=${d.id} class="flex items-center gap-1.5">
                        <select
                          class="bg-mitto-input-box border border-mitto-border rounded px-1.5 py-1 text-xs text-mitto-text"
                          value=${d.dependency_type || "blocks"}
                          disabled=${depsBusy}
                          onInput=${e => { if (e.target.value !== (d.dependency_type || "blocks")) changeDepType(d.id, e.target.value); }}
                        >
                          ${DEP_TYPES.map(t => html`<option value=${t}>${t}</option>`)}
                        </select>
                        <span class="font-mono text-xs text-mitto-text flex-1 min-w-0 truncate" title=${d.title || d.id}>${d.id}</span>
                        <button
                          type="button"
                          onClick=${() => mutateDep("remove", d.id)}
                          disabled=${depsBusy}
                          class="p-1 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-red-400 disabled:opacity-40 disabled:cursor-not-allowed flex-shrink-0"
                          title="Remove dependency"
                        >
                          <${CloseIcon} className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    `)}

                    <div class="flex items-center gap-1.5 pt-1">
                      <select
                        class="bg-mitto-input-box border border-mitto-border rounded px-1.5 py-1 text-xs text-mitto-text"
                        value=${newDepType}
                        disabled=${depsBusy}
                        onInput=${e => setNewDepType(e.target.value)}
                      >
                        ${DEP_TYPES.map(t => html`<option value=${t}>${t}</option>`)}
                      </select>
                      <input
                        type="text"
                        list="beads-dep-options"
                        placeholder="issue id…"
                        value=${newDepId}
                        disabled=${depsBusy}
                        onInput=${e => setNewDepId(e.target.value)}
                        onKeyDown=${e => { if (e.key === "Enter") { e.preventDefault(); handleAddDep(); } }}
                        class="bg-mitto-input-box border border-mitto-border rounded px-2 py-1 text-xs text-mitto-text flex-1 min-w-0 placeholder-mitto-text-secondary"
                      />
                      <button
                        type="button"
                        onClick=${handleAddDep}
                        disabled=${depsBusy || !newDepId.trim()}
                        class="p-1 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text disabled:opacity-40 disabled:cursor-not-allowed flex-shrink-0"
                        title="Add dependency"
                      >
                        ${depsBusy
                          ? html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin" />`
                          : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                      </button>
                    </div>
                  </div>
                `}
            </div>
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
 *        `menus` list includes `beadsIssues`; populates the per-issue context menu.
 * @param {function} onRunBeadsPrompt - (prompt, issue) => starts a new
 *        conversation seeded with the prompt text and the issue's context.
 * @param {function} onShowSidebar - Opens the conversations sidebar (mobile);
 *        used by the header hamburger button to return to the conversation list.
 */
export function BeadsView({ workingDir, showToast, onFetchBeadsPrompts, onRunBeadsPrompt, onShowSidebar, onOpenConfig }) {
  const [issues, setIssues] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [selectedIssue, setSelectedIssue] = useState(null);
  const [isCreating, setIsCreating] = useState(false);

  const [statusFilter, setStatusFilter] = useState("all");
  const [typeFilter, setTypeFilter] = useState("all");
  const [search, setSearch] = useState("");

  // Per-issue right-click context menu. `contextMenu` holds the click position
  // and the issue it targets; `menuPrompts` are the `menus: beadsIssues` prompts shown
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

  // Folder upstream task system ("none"|"jira"|"github"|"gitlab"|"linear") and the
  // in-flight sync action ("pull"|"push"|"sync"|null), used to drive the
  // upstream sync buttons in the footer.
  const [upstream, setUpstream] = useState("none");
  const [syncAction, setSyncAction] = useState(null);

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

  // Fetch the folder's configured upstream so the sync buttons can be shown.
  useEffect(() => {
    if (!workingDir) {
      setUpstream("none");
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch(apiUrl("/api/beads/upstream") + "?working_dir=" + encodeURIComponent(workingDir));
        const data = await readBeadsResponse(res);
        if (!cancelled) setUpstream((data && data.upstream) || "none");
      } catch (_err) {
        if (!cancelled) setUpstream("none");
      }
    })();
    return () => { cancelled = true; };
  }, [workingDir]);

  // Trigger an upstream sync action (pull/push/sync) via POST /api/beads/sync.
  // The backend reads the integration from folders.json; we only send the action.
  const handleSync = useCallback(async (action) => {
    if (!workingDir || syncAction) return;
    setSyncAction(action);
    try {
      const res = await secureFetch(apiUrl("/api/beads/sync"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, action }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast && showToast({ style: "error", title: data.error || `Failed to ${action}`, message: data.stderr });
      } else {
        const verb = action === "pull" ? "Pulled" : action === "push" ? "Pushed" : "Synced";
        showToast && showToast({ style: "success", title: `${verb} with ${UPSTREAM_LABELS[upstream] || upstream}` });
        fetchList();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || `Failed to ${action}` });
    } finally {
      setSyncAction(null);
    }
  }, [workingDir, syncAction, upstream, showToast, fetchList]);

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

  // Open the per-issue context menu at the cursor and load the `menus: beadsIssues`
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

  // Create a "blocks" dependency edge from the context menu. `direction` picks
  // the argument order (the edge kind is always "blocks"):
  //   "depends-on" → issue depends on other      (bd dep add <issue> <other>)
  //   "blocks"     → issue blocks other          (bd dep add <other> <issue>)
  // since "A depends on B" is the same edge as "B is blocked by A".
  const handleAddDependencyEdge = useCallback(async (issue, other, direction) => {
    if (!issue || !other) return;
    const id = direction === "blocks" ? other.id : issue.id;
    const dependsOn = direction === "blocks" ? issue.id : other.id;
    try {
      const res = await secureFetch(apiUrl("/api/beads/dep"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id, depends_on: dependsOn, type: "blocks", action: "add" }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast && showToast({ style: "error", title: data.error || "Failed to add dependency", message: data.stderr });
      } else {
        showToast && showToast({
          style: "success",
          title: direction === "blocks" ? `${issue.id} now blocks ${other.id}` : `${issue.id} now depends on ${other.id}`,
        });
        fetchList();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to add dependency" });
    }
  }, [workingDir, showToast, fetchList]);

  // Run a beads prompt for a specific issue: delegates to the parent, which
  // creates a new conversation seeded with the prompt text and issue context.
  const handleRunPrompt = useCallback((prompt, issue) => {
    closeContextMenu();
    onRunBeadsPrompt && onRunBeadsPrompt(prompt, issue);
  }, [onRunBeadsPrompt, closeContextMenu]);

  // Build the per-issue context menu: a Close/Reopen toggle and a Delete action,
  // plus a "Prompts" submenu listing every `menus: beadsIssues` prompt — all
  // wired to the same handlers used by the detail panel footer.
  const promptSubmenuItems = (menuPrompts || [])
    .filter((p) => p && p.name)
    .map((p) => ({
      label: p.name,
      onClick: () => handleRunPrompt(p, contextMenu && contextMenu.issue),
    }));

  const ctxIssue = contextMenu && contextMenu.issue;
  const ctxIsClosed = ctxIssue && ctxIssue.status === "closed";

  // "Depends On" / "Blocks" submenus list every other issue. Picking one creates
  // a "blocks" edge in the chosen direction via handleAddDependencyEdge.
  const otherIssues = (issues || []).filter((i) => ctxIssue && i.id !== ctxIssue.id);
  const issueSubmenu = (direction) =>
    otherIssues.map((i) => ({
      label: `${i.id} · ${i.title}`,
      onClick: () => handleAddDependencyEdge(ctxIssue, i, direction),
    }));

  const contextMenuItems = [
    {
      label: ctxIsClosed ? "Reopen" : "Close",
      icon: ctxIsClosed ? html`<${RefreshIcon} />` : html`<${CheckIcon} />`,
      onClick: () => ctxIssue && handleToggleStatus(ctxIssue),
      disabled: statusBusy,
    },
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => contextMenu && setDeleteTarget(contextMenu.issue),
      danger: true,
    },
    ...(promptSubmenuItems.length > 0
      ? [{ label: "Prompts", submenu: promptSubmenuItems }]
      : []),
    ...(otherIssues.length > 0
      ? [
          { label: "Depends On", submenu: issueSubmenu("depends-on") },
          { label: "Blocks", submenu: issueSubmenu("blocks") },
        ]
      : []),
  ];

  return html`
    <div class="flex h-full overflow-hidden">
    <div class="flex flex-col flex-1 min-w-0 overflow-hidden">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border flex-shrink-0">
        <button
          onClick=${() => onShowSidebar && onShowSidebar()}
          class="md:hidden p-2 -ml-2 rounded-lg hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text flex-shrink-0"
          title="Show conversations"
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <span class="font-semibold text-lg flex-1">Beads — ${workspaceLabel}</span>
      </div>

      <div class="flex items-center gap-2 px-4 py-1.5 border-b border-mitto-border flex-shrink-0 flex-wrap">
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

      <div class="flex-1 overflow-y-auto overflow-x-auto beads-table-scroll">
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
          <table class="min-w-full text-sm text-left border-collapse">
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
                  <td class="px-3 py-2 text-mitto-text whitespace-nowrap">${issue.title}</td>
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

        ${upstream && upstream !== "none" && html`
          <div class="flex items-center gap-1 pl-2 ml-1 border-l border-mitto-border">
            <button
              onClick=${() => handleSync("pull")}
              disabled=${!!syncAction}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text disabled:opacity-40 disabled:cursor-not-allowed"
              title=${`Pull from ${UPSTREAM_LABELS[upstream] || upstream}`}
            >
              ${syncAction === "pull"
                ? html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`
                : html`<${ArrowDownIcon} className="w-4 h-4" />`}
            </button>
            <button
              onClick=${() => handleSync("push")}
              disabled=${!!syncAction}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text disabled:opacity-40 disabled:cursor-not-allowed"
              title=${`Push to ${UPSTREAM_LABELS[upstream] || upstream}`}
            >
              ${syncAction === "push"
                ? html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`
                : html`<${ArrowUpIcon} className="w-4 h-4" />`}
            </button>
            <button
              onClick=${() => handleSync("sync")}
              disabled=${!!syncAction}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text disabled:opacity-40 disabled:cursor-not-allowed"
              title=${`Sync with ${UPSTREAM_LABELS[upstream] || upstream} (pull then push)`}
            >
              ${syncAction === "sync"
                ? html`<${SpinnerIcon} className="w-4 h-4 animate-spin" />`
                : html`<${SyncIcon} className="w-4 h-4" />`}
            </button>
          </div>
        `}

        <span class="text-xs text-mitto-text-secondary ml-auto">${filtered.length} issue${filtered.length === 1 ? "" : "s"}</span>

        ${onOpenConfig && html`
          <button
            onClick=${() => onOpenConfig()}
            class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text ml-2"
            title="Beads configuration"
          >
            <${SettingsIcon} className="w-4 h-4" />
          </button>
        `}
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

