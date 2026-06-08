// Mitto Web Interface - BeadsView Component
// Displays a Beads (bd) issue list and detail view for a workspace.

const { html, useState, useEffect, useCallback, useMemo, useRef, Fragment } = window.preact;

import { apiUrl, authFetch, secureFetch, getBeadsFilters, setBeadsFilters } from "../utils/index.js";
import { getBasename } from "../lib.js";
import { PlusIcon, CloseIcon, SpinnerIcon, TrashIcon, RefreshIcon, BroomIcon, ChevronUpIcon, CheckIcon, MenuIcon, ArrowDownIcon, ArrowUpIcon, SyncIcon, SettingsIcon, ExpandIcon, CollapseIcon, MoonIcon, SunIcon, LayersIcon, getPromptIconOrDefault } from "./Icons.js";
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
  deferred: "bg-cyan-800 text-cyan-100",
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

function commentBody(text) {
  const m = renderMarkdown(text);
  if (m) return html`<div class="markdown-content text-mitto-text text-sm max-w-none" dangerouslySetInnerHTML=${{ __html: m }} />`;
  return html`<pre class="whitespace-pre-wrap break-words text-sm text-mitto-text">${text || ""}</pre>`;
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
 * BeadsDetailPanel is a fixed right-side overlay that serves two modes:
 *
 *  - View mode (an `issue` is provided): shows the read-only properties of a
 *    single issue, populated directly from the already-loaded list row so it
 *    opens instantly without an extra network request. Subtasks are computed
 *    from the full issue list via the parent field.
 *  - Create mode (`isCreating` is true): shows editable fields for a new issue
 *    plus a "Save" footer that POSTs to /api/beads/create.
 *
 * The panel uses two stacked layers so it matches the conversation
 * SessionPanel's dimming while still respecting the beads view bounds:
 *  - A `fixed inset-0` dimming backdrop covering the WHOLE window (like
 *    SessionPanel), so the conversations sidebar is dimmed too. It is hidden in
 *    fullscreen, where the panel fills the whole beads view area.
 *  - A transparent `absolute inset-0` layer scoped to the beads view that holds
 *    the panel on the right. Keeping the panel scoped means `expand` fills only
 *    the beads view area and the panel never covers the sidebar; the backdrop's
 *    dim shows through the transparent layer on the panel's left.
 * Clicking anywhere outside the panel closes it.
 */
function BeadsDetailPanel({ issue, allIssues, isCreating, workingDir, onClose, onCreated, onUpdated, showToast, onFetchPrompts, onRunPrompt, onDelete, onToggleStatus, onToggleDefer, statusBusy, onSelectIssue }) {
  const isOpen = isCreating || !!issue;
  const [isClosing, setIsClosing] = useState(false);
  const [shouldRender, setShouldRender] = useState(isOpen);
  // When true (desktop only), the panel expands to fill the beads view area
  // (hiding the issue list behind it) so a single issue's details are easier to
  // read. On mobile the panel is always full-width, so this has no effect there
  // and the expand toggle is hidden.
  const [fullscreen, setFullscreen] = useState(false);
  // Phone detection drives the panel width. We deliberately use the user agent
  // (not a viewport-width breakpoint like Tailwind's `md:`): the native macOS
  // app runs in a WKWebView that reports a Macintosh UA but can have a narrow
  // window, and must still get the desktop layout (a doubled fixed-width panel
  // with a dimming backdrop), not the full-width phone layout. A viewport-based
  // rule would misclassify that narrow window as mobile and drop the backdrop.
  const isMobile = useMemo(() => {
    if (typeof navigator === "undefined") return false;
    const ua = navigator.userAgent || "";
    return /iPhone|iPad|iPod|Android|webOS|BlackBerry|IEMobile|Opera Mini/i.test(ua);
  }, []);
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

  // View-mode inline priority editing. Clicking the priority badge opens a small
  // dropdown of the available priorities; selecting one saves via
  // /api/beads/update. `savingPriority` gates the in-flight request.
  const [editingPriority, setEditingPriority] = useState(false);
  const [savingPriority, setSavingPriority] = useState(false);
  const priorityRef = useRef(null);

  // View-mode inline assignee editing. Clicking the rendered assignee switches
  // it to a text input; blur saves via /api/beads/update when the text changed
  // (an empty value clears the assignee). `assigneeDraft` holds the in-progress
  // text; `savingAssignee` gates the in-flight request. `assigneeCancelRef` lets
  // Escape blur without saving.
  const [editingAssignee, setEditingAssignee] = useState(false);
  const [assigneeDraft, setAssigneeDraft] = useState("");
  const [savingAssignee, setSavingAssignee] = useState(false);
  const assigneeRef = useRef(null);
  const assigneeCancelRef = useRef(false);

  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/beads/show when an issue is opened. `depsBusy` gates add/remove
  // requests; `newDepType`/`newDepId` back the "add dependency" row.
  const [deps, setDeps] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");
  const [comments, setComments] = useState([]);
  const [notes, setNotes] = useState("");

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

  // Close the priority dropdown on outside click while it is open.
  useEffect(() => {
    if (!editingPriority) return undefined;
    const onDocClick = (e) => {
      if (priorityRef.current && !priorityRef.current.contains(e.target)) {
        setEditingPriority(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [editingPriority]);

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
        setFullscreen(false);
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
    setEditingPriority(false);
    setSavingPriority(false);
    setEditingAssignee(false);
    setSavingAssignee(false);
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

  // Focus the assignee input (cursor at end) when entering edit mode.
  useEffect(() => {
    if (editingAssignee && assigneeRef.current) {
      const el = assigneeRef.current;
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    }
  }, [editingAssignee]);

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

  // Persist a newly selected priority via /api/beads/update, then refresh the
  // list via onUpdated so the panel re-renders with the saved value. Selecting
  // the current priority just closes the dropdown without a request.
  const handleSetPriority = useCallback(async (next) => {
    setEditingPriority(false);
    const current = (data && typeof data.priority === "number") ? data.priority : null;
    if (next === current) return;
    setSavingPriority(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/update"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: data.id, priority: next }),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to update priority" });
      } else {
        showToast && showToast({ style: "success", title: "Priority updated" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to update priority" });
    } finally {
      setSavingPriority(false);
    }
  }, [data && data.id, data && data.priority, workingDir, showToast, onUpdated]);

  // Enter inline assignee-edit mode, seeding the draft from the current assignee.
  const startEditAssignee = useCallback(() => {
    if (savingAssignee) return;
    assigneeCancelRef.current = false;
    setAssigneeDraft((data && data.assignee) || "");
    setEditingAssignee(true);
  }, [data && data.assignee, savingAssignee]);

  // Persist the edited assignee on blur. Saves only when the (trimmed) text
  // changed; otherwise just leaves edit mode. An empty value clears the
  // assignee. Escape sets assigneeCancelRef so the blur it triggers discards the
  // draft. Uses /api/beads/update and refreshes the list via onUpdated so the
  // panel re-renders with the saved value.
  const handleAssigneeBlur = useCallback(async () => {
    if (assigneeCancelRef.current) {
      assigneeCancelRef.current = false;
      setEditingAssignee(false);
      return;
    }
    const original = (data && data.assignee) || "";
    const next = assigneeDraft.trim();
    if (next === original) {
      setEditingAssignee(false);
      return;
    }
    setSavingAssignee(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/update"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: data.id, assignee: next }),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to update assignee" });
      } else {
        showToast && showToast({ style: "success", title: next === "" ? "Assignee cleared" : "Assignee updated" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to update assignee" });
    } finally {
      setSavingAssignee(false);
      setEditingAssignee(false);
    }
  }, [data && data.id, data && data.assignee, assigneeDraft, workingDir, showToast, onUpdated]);

  // Enter saves (via blur); Escape discards the draft (via blur).
  const handleAssigneeKeyDown = useCallback((e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      e.target.blur();
    } else if (e.key === "Escape") {
      e.preventDefault();
      assigneeCancelRef.current = true;
      e.target.blur();
    }
  }, []);

  // Load the issue's full dependency edges, notes, and comments. The list row
  // only carries counts, so the actual data comes from /api/beads/show.
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
        setComments([]);
        setNotes("");
      } else {
        const issueObj = Array.isArray(respData) ? respData[0] : respData;
        setDeps((issueObj && issueObj.dependencies) || []);
        setComments((issueObj && issueObj.comments) || []);
        setNotes((issueObj && issueObj.notes) || "");
      }
    } catch (_err) {
      setDeps([]);
      setComments([]);
      setNotes("");
    } finally {
      setDepsLoading(false);
    }
  }, [workingDir, data && data.id]);

  // Fetch dependencies, notes, and comments whenever a (non-create) issue is opened or switched.
  useEffect(() => {
    setDeps([]);
    setComments([]);
    setNotes("");
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
    <${Fragment}>
      <!-- Full-window dimming backdrop (like SessionPanel) so the conversations
           sidebar is dimmed too. fixed escapes the beads view's overflow clip
           and covers the whole window; z-50 matches SessionPanel. Hidden in
           fullscreen, where the panel fills the whole beads view area. -->
      ${!fullscreen && html`
        <div
          class="fixed inset-0 z-50 bg-black/50 properties-backdrop ${isClosing ? "closing" : ""}"
          onClick=${handleClose}
        />
      `}
      <!-- Panel layer scoped to the beads view area (absolute inset-0 within the
           relative BeadsView root). It is transparent and pointer-events-none so
           the backdrop's dim shows through on the panel's left and clicks on the
           empty area fall through to the backdrop; only the panel is interactive
           (pointer-events-auto). z-[60] keeps the panel above the z-50 backdrop.
           Scoping the panel here means expand fills only the beads view area and
           the panel never covers the sidebar.
             Phone: panel is always full-width.
             Desktop normal: a doubled fixed width (40rem), capped at 85% of the
               beads view so the dim always shows on the panel's left and the
               panel never exceeds the beads view width.
             Desktop expanded: panel fills the whole beads view area. -->
      <div class="absolute inset-0 z-[60] flex justify-end pointer-events-none">
        <div class="${(isMobile || fullscreen) ? "w-full" : "w-[40rem] max-w-[85%]"} bg-mitto-sidebar flex-shrink-0 shadow-2xl h-full flex flex-col border-l border-slate-700 properties-panel pointer-events-auto ${isClosing ? "closing" : ""}">
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
          onClick=${() => setFullscreen(f => !f)}
          class="${isMobile ? "hidden" : "block"} p-1 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text flex-shrink-0"
          title=${fullscreen ? "Exit fullscreen" : "Fullscreen"}
        >
          ${fullscreen
            ? html`<${CollapseIcon} className="w-5 h-5" />`
            : html`<${ExpandIcon} className="w-5 h-5" />`}
        </button>
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
              <div class="relative" ref=${priorityRef}>
                <button
                  type="button"
                  onClick=${() => !savingPriority && setEditingPriority(o => !o)}
                  disabled=${savingPriority}
                  class="cursor-pointer hover:opacity-80 transition-opacity disabled:opacity-50 disabled:cursor-default"
                  title="Click to change priority"
                >
                  ${priorityBadge(data.priority)}
                </button>
                ${savingPriority && html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin inline-block ml-1 text-mitto-text-secondary align-middle" />`}
                ${editingPriority && html`
                  <div class="absolute left-0 top-full mt-1 z-10 bg-slate-800 border border-slate-600 rounded-lg shadow-xl py-1 min-w-[140px]">
                    ${Object.entries(PRIORITY_LABELS).map(([n, label]) => {
                      const num = Number(n);
                      const isCurrent = num === (typeof data.priority === "number" ? data.priority : 3);
                      return html`
                        <button
                          key=${n}
                          type="button"
                          onClick=${() => handleSetPriority(num)}
                          class="w-full flex items-center gap-2 px-3 py-2 text-left text-sm text-gray-200 hover:bg-slate-700 transition-colors"
                        >
                          ${priorityBadge(num)}
                          <span class="flex-1">${label}</span>
                          ${isCurrent && html`<${CheckIcon} className="w-3.5 h-3.5 text-gray-400" />`}
                        </button>
                      `;
                    })}
                  </div>
                `}
              </div>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <div>
                <div class="text-xs text-mitto-text-secondary mb-0.5">Assignee</div>
                ${editingAssignee
                  ? html`
                    <input
                      ref=${assigneeRef}
                      type="text"
                      class=${inputClass}
                      placeholder="Assignee (empty to clear)"
                      value=${assigneeDraft}
                      onInput=${e => setAssigneeDraft(e.target.value)}
                      onBlur=${handleAssigneeBlur}
                      onKeyDown=${handleAssigneeKeyDown}
                      disabled=${savingAssignee}
                    />
                  `
                  : html`
                    <div
                      class="text-sm text-mitto-text break-words cursor-text hover:text-mitto-text-secondary transition-colors flex items-center gap-2"
                      onClick=${startEditAssignee}
                      title="Click to edit"
                    >
                      ${savingAssignee && html`<${SpinnerIcon} className="w-3.5 h-3.5 animate-spin text-mitto-text-secondary flex-shrink-0" />`}
                      ${data.assignee
                        ? html`<span>${data.assignee}</span>`
                        : html`<span class="text-mitto-text-secondary italic">Unassigned. Click to set.</span>`}
                    </div>
                  `}
              </div>
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
                          ? html`<div class="markdown-content text-mitto-text text-sm max-w-none" dangerouslySetInnerHTML=${{ __html: md }} />`
                          : html`<pre class="whitespace-pre-wrap break-words text-sm text-mitto-text">${data.description}</pre>`)
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
                    <li key=${c.id}>
                      <button
                        type="button"
                        onClick=${() => onSelectIssue && onSelectIssue(c)}
                        class="w-full flex items-center gap-2 text-sm text-mitto-text text-left rounded px-1 py-0.5 hover:bg-mitto-input-box transition-colors"
                        title="Open ${c.id}"
                      >
                        ${statusBadge(c.status)}
                        <span class="font-mono text-mitto-text-secondary text-xs">${c.id}</span>
                        <span class="truncate">${c.title}</span>
                      </button>
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
                        <button
                          type="button"
                          onClick=${() => onSelectIssue && onSelectIssue((allIssues || []).find(i => i.id === d.id) || d)}
                          class="font-mono text-xs text-blue-400 hover:text-blue-300 hover:underline flex-1 min-w-0 truncate text-left"
                          title=${"Open " + d.id}
                        >${d.id}</button>
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

            <div>
              <div class="text-xs font-semibold text-mitto-text-secondary uppercase tracking-wide mb-1">Notes</div>
              ${depsLoading
                ? html`
                  <div class="flex items-center gap-2 text-xs text-mitto-text-secondary">
                    <${SpinnerIcon} className="w-3 h-3 animate-spin" /> Loading…
                  </div>
                `
                : (notes && notes.trim()
                    ? html`<div class="border border-mitto-border rounded p-2 bg-mitto-input-box">${commentBody(notes)}</div>`
                    : html`<div class="text-xs text-mitto-text-secondary italic">No notes.</div>`)
              }
            </div>

            <div>
              <div class="text-xs font-semibold text-mitto-text-secondary uppercase tracking-wide mb-1">Comments${comments.length ? ` (${comments.length})` : ""}</div>
              ${depsLoading
                ? html`
                  <div class="flex items-center gap-2 text-xs text-mitto-text-secondary">
                    <${SpinnerIcon} className="w-3 h-3 animate-spin" /> Loading…
                  </div>
                `
                : (comments.length === 0
                    ? html`<div class="text-xs text-mitto-text-secondary italic">No comments.</div>`
                    : html`
                      <ul class="space-y-2">
                        ${[...comments].sort((a, b) => new Date(a.created_at) - new Date(b.created_at)).map(cm => html`
                          <li key=${cm.id} class="border border-mitto-border rounded p-2 bg-mitto-input-box">
                            <div class="flex items-center justify-between gap-2 mb-1">
                              <span class="text-xs font-medium text-mitto-text">${cm.author || "Unknown"}</span>
                              <span class="text-xs text-mitto-text-secondary" title=${cm.created_at}>${cm.created_at ? new Date(cm.created_at).toLocaleString() : ""}</span>
                            </div>
                            ${commentBody(cm.text)}
                          </li>
                        `)}
                      </ul>
                    `)
              }
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
                  <div class="px-3 py-2 text-sm text-mitto-text-secondary">No task prompts</div>
                `}
                ${!promptsLoading && prompts.map(p => {
                  const PromptIcon = getPromptIconOrDefault(p.icon);
                  return html`
                  <button
                    key=${p.name}
                    type="button"
                    onClick=${() => { setShowPrompts(false); onRunPrompt && onRunPrompt(p, data); }}
                    title=${p.description || p.name}
                    class="w-full text-left px-3 py-2 text-sm text-mitto-text hover:bg-mitto-input-box transition-colors flex items-center gap-2"
                  >
                    <${PromptIcon} className="w-4 h-4 flex-shrink-0 opacity-70" />
                    <span class="truncate flex-1">${p.name}</span>
                  </button>
                `;
                })}
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
              onClick=${() => onToggleDefer && onToggleDefer(data)}
              disabled=${statusBusy}
              class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text disabled:opacity-40 disabled:cursor-not-allowed"
              title=${data.status === "deferred" ? "Undefer issue" : "Defer issue"}
            >
              ${data.status === "deferred"
                ? html`<${SunIcon} className="w-4 h-4" />`
                : html`<${MoonIcon} className="w-4 h-4" />`}
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
      </div>
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
export function BeadsView({ workingDir, showToast, onFetchBeadsPrompts, onRunBeadsPrompt, onFetchBeadsListPrompts, onRunBeadsListPrompt, onShowSidebar, onOpenConfig, issueSessionMap = {}, onOpenConversation, initialSelectedIssueId, initialSelectNonce = 0 }) {
  const [issues, setIssues] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  const [selectedIssue, setSelectedIssue] = useState(null);
  const [isCreating, setIsCreating] = useState(false);

  // Filter state is initialized from localStorage so that the user's applied
  // criteria are restored when they navigate away from the Beads view and
  // return within the same session. Changes are persisted via the effect below.
  const [statusFilter, setStatusFilter] = useState(() => getBeadsFilters().status);
  const [typeFilter, setTypeFilter] = useState(() => getBeadsFilters().type);
  const [search, setSearch] = useState(() => getBeadsFilters().search);

  // Persist filter criteria whenever they change.
  useEffect(() => {
    setBeadsFilters({ status: statusFilter, type: typeFilter, search });
  }, [statusFilter, typeFilter, search]);

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

  // List-level "Prompts" dropdown state (footer toolbar). These are the
  // `menus: beadsList` prompts that operate on the whole issue list rather than
  // a single issue. Loaded lazily the first time the dropdown is opened.
  const [showListPrompts, setShowListPrompts] = useState(false);
  const [listPrompts, setListPrompts] = useState([]);
  const [listPromptsLoading, setListPromptsLoading] = useState(false);
  const listPromptsRef = useRef(null);

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

  // Auto-select an issue when the view is opened focused on one (e.g. via the
  // conversation properties panel's linked-issue link). We apply once per nonce
  // — so re-opening the same issue re-selects it — and wait until the list has
  // loaded so the row data is available.
  const appliedSelectNonceRef = useRef(0);
  useEffect(() => {
    if (!initialSelectedIssueId) return;
    if (initialSelectNonce === appliedSelectNonceRef.current) return;
    if (!issues || issues.length === 0) return;
    const match = issues.find((i) => i.id === initialSelectedIssueId);
    if (match) {
      setIsCreating(false);
      setSelectedIssue(match);
      appliedSelectNonceRef.current = initialSelectNonce;
    }
  }, [initialSelectedIssueId, initialSelectNonce, issues]);

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

  // Defer or undefer a single issue ("on ice" for later) depending on its
  // current status, then refresh. Shares the /api/beads/status endpoint, which
  // also handles the defer/undefer verbs.
  const handleToggleDefer = useCallback(async (issue) => {
    if (!issue) return;
    const action = issue.status === "deferred" ? "undefer" : "defer";
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
        showToast && showToast({ style: "success", title: action === "defer" ? `Deferred ${issue.id}` : `Undeferred ${issue.id}` });
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

  // Close the list-level prompts dropdown on outside click while it is open.
  useEffect(() => {
    if (!showListPrompts) return undefined;
    const onDocClick = (e) => {
      if (listPromptsRef.current && !listPromptsRef.current.contains(e.target)) {
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
          .then((list) => setListPrompts(list || []))
          .finally(() => setListPromptsLoading(false));
      }
      return next;
    });
  }, [onFetchBeadsListPrompts, workingDir]);

  // Run a list-level prompt in a new conversation (no per-issue context).
  const handleRunListPrompt = useCallback((prompt) => {
    setShowListPrompts(false);
    onRunBeadsListPrompt && onRunBeadsListPrompt(prompt);
  }, [onRunBeadsListPrompt]);

  // Build the per-issue context menu: a Close/Reopen toggle and a Delete action,
  // plus a "Prompts" submenu listing every `menus: beadsIssues` prompt — all
  // wired to the same handlers used by the detail panel footer.
  const promptSubmenuItems = (menuPrompts || [])
    .filter((p) => p && p.name)
    .map((p) => {
      const PromptIcon = getPromptIconOrDefault(p.icon);
      return {
        label: p.name,
        icon: html`<${PromptIcon} className="w-4 h-4" />`,
        onClick: () => handleRunPrompt(p, contextMenu && contextMenu.issue),
      };
    });

  const ctxIssue = contextMenu && contextMenu.issue;
  const ctxIsClosed = ctxIssue && ctxIssue.status === "closed";
  const ctxIsDeferred = ctxIssue && ctxIssue.status === "deferred";

  // "Depends On" / "Blocks" submenus list every other issue. Picking one creates
  // a "blocks" edge in the chosen direction via handleAddDependencyEdge.
  const otherIssues = (issues || []).filter((i) => ctxIssue && i.id !== ctxIssue.id);
  const issueSubmenu = (direction) =>
    otherIssues.map((i) => ({
      label: `${i.id} · ${i.title}`,
      onClick: () => handleAddDependencyEdge(ctxIssue, i, direction),
    }));

  const contextMenuItems = [
    ...(promptSubmenuItems.length > 0
      ? [{ label: "Task", icon: html`<${PlusIcon} />`, submenu: promptSubmenuItems }]
      : []),
    ...(otherIssues.length > 0
      ? [
          { label: "Depends On", icon: html`<${ArrowDownIcon} />`, submenu: issueSubmenu("depends-on") },
          { label: "Blocks", icon: html`<${ArrowUpIcon} />`, submenu: issueSubmenu("blocks") },
        ]
      : []),
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

  return html`
    <div class="relative flex h-full overflow-hidden">
    <div class="flex flex-col flex-1 min-w-0 overflow-hidden">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border flex-shrink-0">
        <button
          onClick=${() => onShowSidebar && onShowSidebar()}
          class="md:hidden p-2 -ml-2 rounded-lg hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text flex-shrink-0"
          title="Show conversations"
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <span class="font-semibold text-lg flex-1">Tasks — ${workspaceLabel}</span>
      </div>

      <div class="beads-toolbar flex items-center gap-2 px-4 border-b border-mitto-border flex-shrink-0">
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
          <div class="space-y-2 p-2">
            ${filtered.map(issue => {
              // If a conversation is linked to this issue, render the ID as a
              // link that opens that conversation. stopPropagation keeps the
              // card's own click (which opens the detail panel) from firing.
              const linkedSessionId = issueSessionMap[issue.id];
              const isSelected = selectedIssue && selectedIssue.id === issue.id;
              // Treat an issue as an epic when it is typed as one or has at
              // least one child issue, and give it a purple tint + left accent
              // so it reads as a distinct container row. A selected card always
              // wins on background/border.
              //
              // The hovered (non-selected) row gets a translucent tint of Mitto's
              // brand red (bg-red-600/40 — the same red used for delete buttons and
              // swipe-to-delete). It stays translucent on purpose: the priority/
              // status/type badges that are themselves red or orange (Critical,
              // High, blocked, bug) are solid opaque pills, so they retain strong
              // contrast against the tint and never blend into the row.
              const childCount = childCountById[issue.id] || 0;
              const isEpic = issue.issue_type === "epic" || childCount > 0;
              const bgTone = isSelected
                ? "bg-slate-700/30"
                : isEpic
                  ? "bg-purple-500/5 hover:bg-red-600/40"
                  : "bg-slate-700/20 hover:bg-red-600/40";
              const borderTone = isSelected
                ? "border-blue-500/60"
                : isEpic
                  ? "border-slate-600/50 border-l-4 border-l-purple-500"
                  : "border-slate-600/50";
              return html`
              <div
                key=${issue.id}
                data-has-context-menu
                class="p-3 rounded-lg border cursor-pointer select-none transition-all ${bgTone} ${borderTone}"
                onClick=${() => selectIssue(issue)}
                onContextMenu=${(e) => handleRowContextMenu(e, issue)}
              >
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-mono text-xs max-w-[10rem] truncate" title=${issue.id}>
                    ${linkedSessionId && onOpenConversation
                      ? html`<a
                          href="#"
                          class="text-blue-400 hover:text-blue-300 hover:underline"
                          onClick=${(e) => { e.preventDefault(); e.stopPropagation(); onOpenConversation(linkedSessionId); }}
                        >${issue.id}</a>`
                      : html`<span class="text-mitto-text-secondary">${issue.id}</span>`}
                  </span>
                  ${typeBadge(issue.issue_type)}
                  ${statusBadge(issue.status)}
                  ${priorityBadge(issue.priority)}
                  ${childCount > 0 ? html`
                    <span
                      class="inline-flex items-center gap-1 text-xs text-purple-300"
                      title="${childCount} child issue${childCount === 1 ? "" : "s"}"
                    >
                      <${LayersIcon} className="w-3.5 h-3.5" />
                      ${childCount}
                    </span>
                  ` : null}
                </div>
                <div class="text-sm text-mitto-text mt-1 break-words">${issue.title}</div>
              </div>
            `;
            })}
          </div>
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
        <div class="relative" ref=${listPromptsRef}>
          <button
            type="button"
            onClick=${toggleListPrompts}
            class="p-1.5 rounded hover:bg-mitto-input-box transition-colors text-mitto-text-secondary hover:text-mitto-text"
            title="Run a prompt over the issue list in a new conversation"
          >
            <${ChevronUpIcon} className="w-4 h-4" />
          </button>
          ${showListPrompts && html`
            <div class="absolute bottom-full left-0 mb-2 w-64 max-h-72 overflow-y-auto bg-mitto-sidebar border border-mitto-border rounded-lg shadow-lg z-10 py-1">
              ${listPromptsLoading && html`
                <div class="flex items-center gap-2 px-3 py-2 text-sm text-mitto-text-secondary">
                  <${SpinnerIcon} className="w-4 h-4 animate-spin" /> Loading…
                </div>
              `}
              ${!listPromptsLoading && listPrompts.length === 0 && html`
                <div class="px-3 py-2 text-sm text-mitto-text-secondary">No task prompts</div>
              `}
              ${!listPromptsLoading && listPrompts.map(p => {
                const PromptIcon = getPromptIconOrDefault(p.icon);
                return html`
                <button
                  key=${p.name}
                  type="button"
                  onClick=${() => handleRunListPrompt(p)}
                  title=${p.description || p.name}
                  class="w-full text-left px-3 py-2 text-sm text-mitto-text hover:bg-mitto-input-box transition-colors flex items-center gap-2"
                >
                  <${PromptIcon} className="w-4 h-4 flex-shrink-0 opacity-70" />
                  <span class="truncate flex-1">${p.name}</span>
                </button>
              `;
              })}
            </div>
          `}
        </div>
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
            title="Tasks configuration"
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
      onToggleDefer=${handleToggleDefer}
      statusBusy=${statusBusy}
      onSelectIssue=${selectIssue}
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

