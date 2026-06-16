// Mitto Web Interface - BeadsView Component
// Displays a Beads (bd) issue list and detail view for a workspace.

const { html, useState, useEffect, useCallback, useMemo, useRef, Fragment } = window.preact;

import { apiUrl, authFetch, secureFetch, getBeadsFilters, setBeadsFilters, getBeadsGrouping, setBeadsGrouping } from "../utils/index.js";
import { getBasename } from "../lib.js";
import { PlusIcon, CloseIcon, TrashIcon, RefreshIcon, BroomIcon, ChevronUpIcon, CheckIcon, CircleIcon, HourglassIcon, MenuIcon, ArrowDownIcon, ArrowUpIcon, SyncIcon, SettingsIcon, ExpandIcon, CollapseIcon, MoonIcon, SunIcon, LayersIcon, EllipsisIcon, getPromptIconOrDefault } from "./Icons.js";
import { CodeEditorField } from "./CodeEditorField.js";
import { ContextMenu } from "./ContextMenu.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { Drawer } from "./Drawer.js";
import { usePullToRefresh } from "../hooks/usePullToRefresh.js";
import { useSwipeToAction } from "../hooks/index.js";

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

const TYPE_COLORS = {
  epic: "bg-purple-700 text-purple-100",
  feature: "bg-blue-700 text-blue-100 beads-type-feature",
  bug: "bg-red-700 text-red-100",
  task: "bg-mitto-surface-4 text-mitto-text-strong",
  chore: "bg-mitto-surface-4 text-mitto-text-strong",
};

function badge(text, colorClass) {
  return html`<span class="badge badge-sm font-medium px-2.5 py-0.5 ${colorClass}">${text}</span>`;
}

function priorityBadge(p) {
  const n = typeof p === "number" ? p : 3;
  return badge(PRIORITY_LABELS[n] ?? String(p), PRIORITY_COLORS[n] ?? PRIORITY_COLORS[3]);
}

export function statusBadge(s) {
  const label = (s || "open").replace(/_/g, " ");
  return badge(label, STATUS_COLORS[s] ?? "bg-mitto-surface-4 text-mitto-text-strong");
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
  return html`<pre class="whitespace-pre-wrap wrap-break-word text-sm text-mitto-text">${text || ""}</pre>`;
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
export function BeadsDetailPanel({ issue, allIssues, isCreating, workingDir, onClose, onCreated, onUpdated, showToast, onFetchPrompts, onRunPrompt, onDelete, onToggleStatus, onToggleDefer, statusBusy, onSelectIssue, createParentId }) {
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

  // Magic-wand "Improve description" state. Mirrors ChatInput's improve-prompt
  // flow but targets the create-form description. `improvingDesc` gates the
  // in-flight request and drives the spinner.
  const [improvingDesc, setImprovingDesc] = useState(false);

  // View-mode "Prompts" dropup state. Prompts are loaded lazily the first time
  // the dropup is opened (per panel mount) via onFetchPrompts.
  const [showPrompts, setShowPrompts] = useState(false);
  const [prompts, setPrompts] = useState([]);
  const [promptsLoading, setPromptsLoading] = useState(false);
  const promptsRef = useRef(null);

  // View-mode inline description editing. Clicking the rendered description
  // switches it to a CodeMirror editor; blur saves via /api/beads/update when
  // the text changed. `descDraft` holds the in-progress text (kept in sync with
  // the editor via onChange so the magic-wand disabled check stays accurate);
  // `savingDesc` gates the in-flight request.
  const [editingDesc, setEditingDesc] = useState(false);
  const [descDraft, setDescDraft] = useState("");
  const [savingDesc, setSavingDesc] = useState(false);
  // Min height (px) applied to the editor so it does not shrink relative to
  // the rendered description it replaces. Measured from descViewRef on entry.
  const [descMinHeight, setDescMinHeight] = useState(0);
  const detailEditorApiRef = useRef(null);
  const descViewRef = useRef(null);
  // Imperative handle for the create-form's description CodeMirror editor.
  const createEditorApiRef = useRef(null);

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

  // View-mode inline notes editing (mirrors description: clicking the rendered
  // notes switches to a textarea; blur saves via /api/beads/update when the
  // text changed). `notesDraft` holds the in-progress text; `savingNotes` gates
  // the in-flight request.
  const [editingNotes, setEditingNotes] = useState(false);
  const [notesDraft, setNotesDraft] = useState("");
  const [savingNotes, setSavingNotes] = useState(false);
  const [notesMinHeight, setNotesMinHeight] = useState(0);
  const notesRef = useRef(null);
  const notesViewRef = useRef(null);

  // View-mode "add comment": a "+" button at the bottom of the comments list
  // reveals a textarea with the same save-on-blur behaviour as notes. An empty
  // draft on blur just closes the editor without a request; otherwise the
  // comment is posted via /api/beads/comment and the list is refreshed.
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
    if (!description.trim()) return;
    setSubmitting(true);
    try {
      const body = { working_dir: workingDir, type, priority, description: description.trim() };
      if (title.trim()) body.title = title.trim();
      if (createParentId) body.parent = createParentId;
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
  }, [workingDir, title, type, priority, description, createParentId, showToast, onCreated, onClose]);

  // AI-enhance a description text field via the same auxiliary endpoint the chat
  // input's magic wand uses (/api/aux/improve-prompt). Works on any
  // text/setText pair so it serves both the create-form description and the
  // view-mode inline edit draft. Replaces the text with the improved version on
  // success; surfaces errors as a toast. No-op when empty or already running.
  const improveDescriptionText = useCallback(async (text, setText) => {
    if (improvingDesc || !text || !text.trim()) return;
    setImprovingDesc(true);
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 65000); // 65s timeout
    try {
      const response = await secureFetch(apiUrl("/api/aux/improve-prompt"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          prompt: text,
          workspace_uuid:
            (typeof window !== "undefined" && window.mittoCurrentWorkspaceUUID) ||
            (typeof sessionStorage !== "undefined" && sessionStorage.getItem("mittoCurrentWorkspaceUUID")) ||
            "",
        }),
        signal: controller.signal,
      });
      clearTimeout(timeoutId);
      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(errorText || "Failed to improve description");
      }
      const respData = await response.json();
      if (respData.improved_prompt) {
        setText(respData.improved_prompt);
      }
    } catch (err) {
      clearTimeout(timeoutId);
      const msg = err.name === "AbortError"
        ? "Request timed out. Please try again."
        : (err.message || "Failed to improve description");
      showToast && showToast({ style: "error", title: msg });
    } finally {
      setImprovingDesc(false);
    }
  }, [improvingDesc, showToast]);

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
    setEditingNotes(false);
    setSavingNotes(false);
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
  // `text` is the current editor value passed directly from CodeEditorField's
  // onBlur callback (not read from descDraft state).
  const handleDescBlur = useCallback(async (text) => {
    // While an AI "improve" request is in flight, ignore blur so a stray click
    // elsewhere can't save the pre-improved draft and drop the incoming result.
    // The user stays in edit mode; once the improvement lands they can blur to
    // save normally. (Clicking the wand itself never blurs — its onMouseDown
    // preventDefault keeps the CodeMirror editor focused.)
    if (improvingDesc) return;
    const original = (data && data.description) || "";
    const next = text;
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
  }, [data && data.id, data && data.description, workingDir, showToast, onUpdated, improvingDesc]);

  // Enter inline notes-edit mode, seeding the draft from the current notes.
  // Capture the rendered area's height first so the textarea opens at least as
  // tall as the content it replaces.
  const startEditNotes = useCallback(() => {
    if (savingNotes) return;
    if (notesViewRef.current) setNotesMinHeight(notesViewRef.current.offsetHeight);
    setNotesDraft(notes || "");
    setEditingNotes(true);
  }, [notes, savingNotes]);

  // Persist the edited notes on blur. Saves only when the text changed;
  // otherwise just leaves edit mode. Uses /api/beads/update with the notes
  // field and updates local state so the panel re-renders with the saved value.
  const handleNotesBlur = useCallback(async () => {
    const original = notes || "";
    const next = notesDraft;
    if (next === original) {
      setEditingNotes(false);
      return;
    }
    setSavingNotes(true);
    try {
      const res = await secureFetch(apiUrl("/api/beads/update"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: data.id, notes: next }),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to update notes" });
      } else {
        setNotes(next);
        showToast && showToast({ style: "success", title: "Notes updated" });
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to update notes" });
    } finally {
      setSavingNotes(false);
      setEditingNotes(false);
    }
  }, [notes, notesDraft, data && data.id, workingDir, showToast, onUpdated]);

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
      const res = await secureFetch(apiUrl("/api/beads/comment"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id: data.id, text }),
      });
      const respData = await readBeadsResponse(res);
      if (!res.ok || respData.error) {
        showToast && showToast({ style: "error", title: respData.error || "Failed to add comment" });
      } else {
        setCommentDraft("");
        showToast && showToast({ style: "success", title: "Comment added" });
        await fetchDeps();
        onUpdated && onUpdated();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to add comment" });
    } finally {
      setSavingComment(false);
      setAddingComment(false);
    }
  }, [commentDraft, data && data.id, workingDir, showToast, fetchDeps, onUpdated]);

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
  const renderDescToolbar = ({ text, setText, disabled }) => html`
    <div class="flex items-center gap-1 mb-1">
      <button
        type="button"
        onClick=${() => improveDescriptionText(text, setText)}
        onMouseDown=${(e) => e.preventDefault()}
        disabled=${disabled || improvingDesc || !text || !text.trim()}
        class="chat-input-action ${improvingDesc ? "loading" : ""}"
        title="Improve description with AI"
      >
        ${improvingDesc
          ? html`<span class="loading loading-spinner w-4 h-4"></span>`
          : html`
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" />
            </svg>
          `}
      </button>
    </div>
  `;

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
      <!-- Scoped daisyUI drawer confined to the beads view area (drawer-scoped =
           absolute inset-0 within the relative BeadsView root; see styles.css).
           Its drawer-overlay is transparent so the full-window backdrop above
           dims through on the panel's left and outside clicks close the panel;
           z-60 keeps the panel above the z-50 backdrop. Scoping means expand
           fills only the beads view area and the panel never covers the sidebar.
             Phone: panel is always full-width.
             Desktop normal: a doubled fixed width (40rem), capped at 85% of the
               beads view so the dim always shows on the panel's left and the
               panel never exceeds the beads view width.
             Desktop expanded: panel fills the whole beads view area. -->
      <${Drawer}
        scoped
        side="end"
        isClosing=${isClosing}
        onClose=${handleClose}
        zClass="z-60"
        widthClass=${(isMobile || fullscreen) ? "w-full" : "w-[40rem] max-w-[85%]"}
        panelClass="bg-mitto-sidebar shrink-0 h-full flex flex-col border-l border-mitto-border-1"
      >
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border shrink-0">
        <div class="flex-1 min-w-0">
          ${creating
            ? html`<h2 class="font-semibold text-base text-mitto-text">New Issue</h2>
                ${createParentId ? html`<div class="font-mono text-xs text-mitto-text-secondary">in ${createParentId}</div>` : null}`
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
                    class="font-semibold text-base text-mitto-text wrap-break-word cursor-text rounded px-1 -mx-1 hover:bg-mitto-input-box transition-colors"
                    onClick=${startEditTitle}
                    title="Click to edit"
                  >${data.title}</h2>
                `}
            `}
        </div>
        <button
          onClick=${() => setFullscreen(f => !f)}
          class="btn btn-ghost btn-square btn-sm shrink-0 ${isMobile ? "hidden" : ""}"
          title=${fullscreen ? "Exit fullscreen" : "Fullscreen"}
        >
          ${fullscreen
            ? html`<${CollapseIcon} className="w-5 h-5" />`
            : html`<${ExpandIcon} className="w-5 h-5" />`}
        </button>
        <button
          onClick=${handleClose}
          class="btn btn-ghost btn-square btn-sm shrink-0"
          title="Close"
        >
          <${CloseIcon} className="w-5 h-5" />
        </button>
      </div>

      <div class="flex-1 overflow-y-auto p-4 space-y-4">
        ${creating
          ? html`
            <fieldset class="fieldset">
              <legend class="fieldset-legend">Issue</legend>

              <div>
                <label class=${labelClass} for="new-issue-title">Title</label>
                <input
                  id="new-issue-title"
                  type="text"
                  class=${inputClass}
                  placeholder="Issue title (optional — auto-generated from description)"
                  value=${title}
                  onInput=${e => setTitle(e.target.value)}
                  disabled=${submitting}
                  autoFocus
                />
              </div>

              <div class="flex gap-3 mt-3">
                <div class="flex-1">
                  <label class=${labelClass} for="new-issue-type">Type</label>
                  <select
                    id="new-issue-type"
                    class=${selectClass}
                    value=${type}
                    onInput=${e => setType(e.target.value)}
                    disabled=${submitting}
                  >
                    ${ISSUE_TYPES.map(t => html`<option value=${t}>${t}</option>`)}
                  </select>
                </div>
                <div class="flex-1">
                  <label class=${labelClass} for="new-issue-priority">Priority</label>
                  <select
                    id="new-issue-priority"
                    class=${selectClass}
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

              <div class="mt-3">
                <label class=${labelClass} for="new-issue-desc">Description <span class="text-red-400">*</span></label>
                ${renderDescToolbar({
                  text: description,
                  setText: (v) => { setDescription(v); createEditorApiRef.current?.setValue(v); },
                  disabled: submitting,
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
                />
              </div>
            </fieldset>
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
                  class="btn btn-ghost btn-xs"
                  title="Click to change priority"
                >
                  ${priorityBadge(data.priority)}
                </button>
                ${savingPriority && html`<span class="loading loading-spinner w-3.5 h-3.5 inline-block ml-1 text-mitto-text-secondary align-middle"></span>`}
                ${editingPriority && html`
                  <ul class="menu absolute left-0 top-full mt-1 z-10 bg-base-200 rounded-box shadow-xl min-w-[140px]">
                    ${Object.entries(PRIORITY_LABELS).map(([n, label]) => {
                      const num = Number(n);
                      const isCurrent = num === (typeof data.priority === "number" ? data.priority : 3);
                      return html`
                        <li key=${n}>
                          <button
                            type="button"
                            onClick=${() => handleSetPriority(num)}
                          >
                            ${priorityBadge(num)}
                            <span class="flex-1">${label}</span>
                            ${isCurrent && html`<${CheckIcon} className="w-3.5 h-3.5 opacity-70" />`}
                          </button>
                        </li>
                      `;
                    })}
                  </ul>
                `}
              </div>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <div>
                <label class=${labelClass}>Assignee</label>
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
                      class="text-sm text-mitto-text wrap-break-word cursor-text hover:text-mitto-text-300 transition-colors flex items-center gap-2"
                      onClick=${startEditAssignee}
                      title="Click to edit"
                    >
                      ${savingAssignee && html`<span class="loading loading-spinner w-3.5 h-3.5 text-mitto-text-secondary shrink-0"></span>`}
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
              <label class=${labelClass}>Description</label>
              ${renderDescToolbar(
                editingDesc
                  ? {
                      text: descDraft,
                      setText: (v) => { setDescDraft(v); detailEditorApiRef.current?.setValue(v); },
                      disabled: savingDesc,
                    }
                  : { text: "", setText: () => {}, disabled: true }
              )}
              ${editingDesc
                ? html`
                  <${CodeEditorField}
                    value=${descDraft}
                    onChange=${(v) => setDescDraft(v)}
                    onBlur=${handleDescBlur}
                    disabled=${savingDesc}
                    darkMode=${false}
                    lineNumbers=${false}
                    lineWrapping=${true}
                    highlightActiveLine=${false}
                    className="input-font-target"
                    minHeight=${descMinHeight || 0}
                    autoFocus=${true}
                    editorApiRef=${detailEditorApiRef}
                  />
                `
                : html`
                  <div
                    ref=${descViewRef}
                    class="border border-mitto-border rounded p-3 bg-mitto-input-box cursor-text hover:border-mitto-text-secondary transition-colors relative"
                    onClick=${startEditDesc}
                    title="Click to edit"
                  >
                    ${savingDesc && html`<span class="loading loading-spinner w-4 h-4 absolute top-2 right-2 text-mitto-text-secondary"></span>`}
                    ${data.description
                      ? (md
                          ? html`<div class="markdown-content text-mitto-text text-sm max-w-none" dangerouslySetInnerHTML=${{ __html: md }} />`
                          : html`<pre class="whitespace-pre-wrap wrap-break-word text-sm text-mitto-text">${data.description}</pre>`)
                      : html`<span class="text-sm text-mitto-text-secondary italic">No description. Click to add one.</span>`
                    }
                  </div>
                `}
            </div>

            ${subtasks.length > 0 && html`
              <fieldset class="fieldset">
                <legend class="fieldset-legend">Subtasks (${subtasks.length})</legend>
                <ul class="space-y-1">
                  ${subtasks.map(c => html`
                    <li key=${c.id}>
                      <button
                        type="button"
                        onClick=${() => onSelectIssue && onSelectIssue(c)}
                        class="btn btn-ghost btn-xs w-full justify-start"
                        title="Open ${c.id}"
                      >
                        ${statusBadge(c.status)}
                        <span class="font-mono text-mitto-text-secondary text-xs">${c.id}</span>
                        <span class="truncate">${c.title}</span>
                      </button>
                    </li>
                  `)}
                </ul>
              </fieldset>
            `}

            <fieldset class="fieldset">
              <legend class="fieldset-legend">Dependencies</legend>

              <datalist id="beads-dep-options">
                ${(allIssues || [])
                  .filter(i => i.id !== data.id && !deps.some(d => d.id === i.id))
                  .map(i => html`<option key=${i.id} value=${i.id}>${i.title}</option>`)}
              </datalist>

              ${depsLoading
                ? html`
                  <div class="flex items-center gap-2 text-xs text-mitto-text-secondary">
                    <span class="loading loading-spinner w-3 h-3"></span> Loading…
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
                          class="select select-xs"
                          value=${d.dependency_type || "blocks"}
                          disabled=${depsBusy}
                          onInput=${e => { if (e.target.value !== (d.dependency_type || "blocks")) changeDepType(d.id, e.target.value); }}
                        >
                          ${DEP_TYPES.map(t => html`<option value=${t}>${t}</option>`)}
                        </select>
                        <button
                          type="button"
                          onClick=${() => onSelectIssue && onSelectIssue((allIssues || []).find(i => i.id === d.id) || d)}
                          class="font-mono text-xs text-mitto-accent-400 hover:text-mitto-accent-300 hover:underline flex-1 min-w-0 truncate text-left"
                          title=${"Open " + d.id}
                        >${d.id}</button>
                        <button
                          type="button"
                          onClick=${() => { if (depsBusy) return; mutateDep("remove", d.id); }}
                          aria-disabled=${depsBusy ? "true" : "false"}
                          class="btn btn-ghost btn-square btn-xs shrink-0 group ${depsBusy ? "opacity-40 pointer-events-none" : ""}"
                          title="Remove dependency"
                        >
                          <${CloseIcon} className="w-3.5 h-3.5 group-hover:text-red-400" />
                        </button>
                      </div>
                    `)}

                    <div class="flex items-center gap-1.5 pt-1">
                      <select
                        class="select select-xs"
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
                        class="input input-xs flex-1 min-w-0"
                      />
                      <button
                        type="button"
                        onClick=${() => { if (depsBusy || !newDepId.trim()) return; handleAddDep(); }}
                        aria-disabled=${depsBusy || !newDepId.trim() ? "true" : "false"}
                        class="btn btn-ghost btn-square btn-xs shrink-0 ${depsBusy || !newDepId.trim() ? "opacity-40 pointer-events-none" : ""}"
                        title="Add dependency"
                      >
                        ${depsBusy
                          ? html`<span class="loading loading-spinner w-3.5 h-3.5"></span>`
                          : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                      </button>
                    </div>
                  </div>
                `}
            </fieldset>

            <fieldset class="fieldset">
              <legend class="fieldset-legend">Comments${comments.length ? ` (${comments.length})` : ""}</legend>
              ${depsLoading
                ? html`
                  <div class="flex items-center gap-2 text-xs text-mitto-text-secondary">
                    <span class="loading loading-spinner w-3 h-3"></span> Loading…
                  </div>
                `
                : html`
                  <${Fragment}>
                    ${comments.length === 0
                      ? html`<div class="text-xs text-mitto-text-secondary italic">No comments.</div>`
                      : html`
                        <ul class="space-y-2">
                          ${[...comments].sort((a, b) => new Date(a.created_at) - new Date(b.created_at)).map(cm => html`
                            <li key=${cm.id} class="border-l-2 border-l-mitto-accent-500/70 bg-mitto-accent-500/10 rounded-r p-2 pl-3">
                              <div class="flex items-center justify-between gap-2 mb-1">
                                <span class="text-xs font-medium text-mitto-text">${cm.author || "Unknown"}</span>
                                <span class="text-xs text-mitto-text-secondary" title=${cm.created_at}>${cm.created_at ? new Date(cm.created_at).toLocaleString() : ""}</span>
                              </div>
                              ${commentBody(cm.text)}
                            </li>
                          `)}
                        </ul>
                      `}
                    ${addingComment
                      ? html`
                        <textarea
                          ref=${commentRef}
                          class="${textareaClass} resize-y mt-2"
                          rows="3"
                          placeholder="Add a comment…"
                          value=${commentDraft}
                          onInput=${e => setCommentDraft(e.target.value)}
                          onBlur=${handleCommentBlur}
                          disabled=${savingComment}
                        ></textarea>
                      `
                      : html`
                        <button
                          type="button"
                          onClick=${startAddComment}
                          disabled=${savingComment}
                          class="btn btn-ghost btn-xs mt-2"
                          title="Add comment"
                        >
                          ${savingComment
                            ? html`<span class="loading loading-spinner w-3.5 h-3.5"></span>`
                            : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                          <span>Add comment</span>
                        </button>
                      `}
                  </${Fragment}>
                `
              }
            </fieldset>

            <fieldset class="fieldset">
              <legend class="fieldset-legend">Notes</legend>
              ${depsLoading
                ? html`
                  <div class="flex items-center gap-2 text-xs text-mitto-text-secondary">
                    <span class="loading loading-spinner w-3 h-3"></span> Loading…
                  </div>
                `
                : editingNotes
                  ? html`
                    <textarea
                      ref=${notesRef}
                      class="${textareaClass} resize-y"
                      rows="4"
                      style=${notesMinHeight ? `min-height:${notesMinHeight}px` : null}
                      placeholder="Add notes…"
                      value=${notesDraft}
                      onInput=${e => setNotesDraft(e.target.value)}
                      onBlur=${handleNotesBlur}
                      disabled=${savingNotes}
                    ></textarea>
                  `
                  : html`
                    <div
                      ref=${notesViewRef}
                      class="border-l-2 border-l-amber-500/70 bg-amber-500/10 rounded-r p-2 pl-3 cursor-text hover:border-l-amber-500 transition-colors relative"
                      onClick=${startEditNotes}
                      title="Click to edit"
                    >
                      ${savingNotes && html`<span class="loading loading-spinner w-4 h-4 absolute top-2 right-2 text-mitto-text-secondary"></span>`}
                      ${notes && notes.trim()
                        ? commentBody(notes)
                        : html`<span class="text-sm text-mitto-text-secondary italic">No notes. Click to add.</span>`}
                    </div>
                  `}
            </fieldset>
          `}
      </div>

      ${creating && html`
        <div class="flex justify-end gap-3 p-3 border-t border-mitto-border shrink-0">
          <button
            type="button"
            onClick=${handleClose}
            disabled=${submitting}
            class="btn btn-ghost btn-sm"
          >
            Close
          </button>
          <button
            type="button"
            onClick=${handleSave}
            disabled=${!description.trim() || submitting}
            class="btn btn-primary btn-sm"
          >
            ${submitting && html`<span class="loading loading-spinner w-4 h-4"></span>`}
            Save
          </button>
        </div>
      `}

      ${!creating && data && html`
        <div class="flex items-center gap-1 p-4 border-t border-mitto-border shrink-0 relative">
          <div class="relative" ref=${promptsRef}>
            <button
              type="button"
              onClick=${togglePrompts}
              class="btn btn-ghost btn-square btn-sm"
              title="Run a prompt for this issue in a new conversation"
            >
              <${ChevronUpIcon} className="w-4 h-4" />
            </button>
            ${showPrompts && html`
              <ul class="menu absolute bottom-full left-0 mb-2 w-64 max-h-72 overflow-y-auto flex-nowrap bg-base-200 rounded-box shadow-xl z-10">
                ${promptsLoading && html`
                  <li class="px-3 py-2 flex items-center gap-2">
                    <span class="loading loading-spinner w-4 h-4"></span> Loading…
                  </li>
                `}
                ${!promptsLoading && prompts.length === 0 && html`
                  <li class="px-3 py-2 opacity-60">No task prompts</li>
                `}
                ${!promptsLoading && prompts.map(p => {
                  const PromptIcon = getPromptIconOrDefault(p.icon);
                  return html`
                  <li key=${p.name}>
                    <button
                      type="button"
                      onClick=${() => { setShowPrompts(false); onRunPrompt && onRunPrompt(p, data); }}
                      title=${p.description || p.name}
                    >
                      <span class="w-4 h-4 shrink-0"><${PromptIcon} className="w-4 h-4" /></span>
                      <span class="truncate flex-1">${p.name}</span>
                    </button>
                  </li>
                `;
                })}
              </ul>
            `}
          </div>

          <div class="flex items-center gap-1 ml-auto">
            <button
              type="button"
              onClick=${() => { if (statusBusy) return; onToggleStatus && onToggleStatus(data); }}
              aria-disabled=${statusBusy ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm ${statusBusy ? "opacity-40 pointer-events-none" : ""}"
              title=${data.status === "closed" ? "Reopen issue" : "Close issue"}
            >
              ${data.status === "closed"
                ? html`<${RefreshIcon} className="w-4 h-4" />`
                : html`<${CheckIcon} className="w-4 h-4" />`}
            </button>
            <button
              type="button"
              onClick=${() => { if (statusBusy) return; onToggleDefer && onToggleDefer(data); }}
              aria-disabled=${statusBusy ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm ${statusBusy ? "opacity-40 pointer-events-none" : ""}"
              title=${data.status === "deferred" ? "Undefer issue" : "Defer issue"}
            >
              ${data.status === "deferred"
                ? html`<${SunIcon} className="w-4 h-4" />`
                : html`<${MoonIcon} className="w-4 h-4" />`}
            </button>
            <button
              type="button"
              onClick=${() => onDelete && onDelete(data)}
              class="btn btn-ghost btn-square btn-sm group"
              title="Delete issue"
            >
              <${TrashIcon} className="w-4 h-4 group-hover:text-red-400" />
            </button>
          </div>
        </div>
      `}
      <//>
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
 * @param {function} onReturnToConversation - Called when the detail panel that
 *        was opened by following a conversation's linked-issue link is closed;
 *        returns the user to that conversation and re-opens its properties panel.
 */

// Swipeable wrapper for a single beads issue row. Mirrors the conversation
// list's swipe-to-action: swipe left to close an open issue (green/check) or
// to delete an already-closed issue (red/trash).
function BeadsIssueRow({ issue, bgTone, borderTone, onSelect, onContextMenu, onClose, onDelete, children }) {
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
    <div class="beads-item-container relative overflow-hidden" ...${containerProps}>
      <!-- Swipe action background (revealed when swiping left) -->
      <div
        class="absolute inset-0 ${isSwipeToDelete ? "bg-red-600" : "bg-green-700"} flex items-center justify-end pr-6 transition-opacity"
        style="opacity: ${isRevealed || absOffset > 20 ? 1 : 0}"
      >
        <button
          onClick=${(e) => { e.preventDefault(); e.stopPropagation(); triggerAction(); }}
          class="p-3 rounded-full ${isSwipeToDelete ? "bg-red-700 hover:bg-red-800" : "bg-green-900"} transition-colors"
          title=${isSwipeToDelete ? "Delete" : "Close"}
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
        class="list-row cursor-pointer select-none ${bgTone} ${borderTone} ${isSwiping ? "" : "transition-all duration-200"}"
        style="transform: translateX(${swipeOffset}px);"
      >
        ${children}
      </div>
    </div>
  `;
}

export function BeadsView({ workingDir, showToast, onFetchBeadsPrompts, onRunBeadsPrompt, onFetchBeadsListPrompts, onRunBeadsListPrompt, onShowSidebar, onOpenConfig, issueSessionMap = {}, issueStreamingSet = new Set(), onOpenConversation, onReturnToConversation, initialSelectedIssueId, initialSelectNonce = 0, initialCreateNonce = 0, initialRefreshNonce = 0, initialCleanupNonce = 0 }) {
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
  const [statusToggles, setStatusToggles] = useState(() => ({ ...beadsStatusToggles }));

  // Toggle a single status on/off. The new state is also written back to the
  // module-level store so it persists across remounts within the session.
  const toggleStatus = useCallback((key) => {
    setStatusToggles(prev => {
      const next = { ...prev, [key]: !prev[key] };
      beadsStatusToggles = next;
      return next;
    });
  }, []);

  // Persist type and search filters whenever they change.
  useEffect(() => {
    setBeadsFilters({ type: typeFilter, search });
  }, [typeFilter, search]);

  // Grouping toggle (persisted) and per-epic expand/collapse state (persisted).
  // Status toggles are deliberately in-memory only; these are separate.
  const [grouping, setGrouping] = useState(() => getBeadsGrouping().enabled);
  // Epics are expanded by default; we persist only the IDs the user collapses.
  const [collapsedEpics, setCollapsedEpics] = useState(() => new Set(getBeadsGrouping().collapsedEpics));

  // Write-through: persist grouping state whenever it changes.
  useEffect(() => {
    setBeadsGrouping({ enabled: grouping, collapsedEpics: [...collapsedEpics] });
  }, [grouping, collapsedEpics]);

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
  // When deleting an epic, what to do with its descendant issues:
  // "none" (leave unchanged), "close" (close open descendants), or
  // "delete" (permanently delete all descendants).
  const [childAction, setChildAction] = useState("none");
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
  // Ref for the issues scroll container — used by usePullToRefresh.
  const scrollContainerRef = useRef(null);

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

  // Pull-to-refresh: disabled while the detail panel or create drawer is open.
  const pullToRefreshDisabled = !!(selectedIssue || isCreating);
  const { pullDistance, refreshing } = usePullToRefresh(scrollContainerRef, fetchList, {
    enabled: !pullToRefreshDisabled,
    threshold: 70,
    resistance: 0.5,
  });

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

  // Tracks whether the currently-open detail panel was opened by navigating
  // directly from a conversation (the properties panel's linked-issue link).
  // When such a panel is closed, BeadsView asks the parent to return to that
  // conversation rather than leaving the user on the beads list. The flag is
  // cleared as soon as the user engages with the beads view in its own right
  // (selecting a different issue or opening the create panel).
  const openedFromConversationRef = useRef(false);

  // The list rows already carry all rich fields (description, parent, dates,
  // assignee, owner), so the detail panel is populated directly from the row —
  // no extra /show request needed. Clicking the open row again toggles it shut.
  const selectIssue = useCallback((issue) => {
    setIsCreating(false);
    setSelectedIssue(prev => {
      const willClose = prev && prev.id === issue.id;
      // Navigating to a different issue means the user is now browsing beads,
      // so a later close should stay here instead of returning to a conversation.
      if (!willClose) openedFromConversationRef.current = false;
      return willClose ? null : issue;
    });
  }, []);

  // Auto-select an issue when the view is opened focused on one (e.g. via the
  // conversation properties panel's linked-issue link). Applied once per nonce
  // so re-opening the same issue re-selects it. To avoid making the user wait
  // for the full issue list before the detail panel appears, we open the panel
  // as soon as we have the issue: instantly from the already-loaded list when
  // present, otherwise via a single `/api/beads/show` fetch for just that issue
  // (the list keeps loading in the background to back the view and the
  // dependency picker). The nonce is consumed synchronously so the effect does
  // not re-fire — and re-fetch — on every background list refresh.
  const appliedSelectNonceRef = useRef(0);
  useEffect(() => {
    if (!initialSelectedIssueId || !workingDir) return;
    if (initialSelectNonce === appliedSelectNonceRef.current) return;
    appliedSelectNonceRef.current = initialSelectNonce;

    // Fast path: the list is already loaded and carries this issue's row.
    const existing = issues.find((i) => i.id === initialSelectedIssueId);
    if (existing) {
      setIsCreating(false);
      setSelectedIssue(existing);
      openedFromConversationRef.current = true;
      return undefined;
    }

    // Cold open: fetch just this one issue so the panel opens immediately.
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch(
          apiUrl("/api/beads/show") + "?working_dir=" + encodeURIComponent(workingDir) + "&id=" + encodeURIComponent(initialSelectedIssueId),
        );
        const respData = await readBeadsResponse(res);
        if (cancelled || !res.ok || respData.error) return;
        const issueObj = Array.isArray(respData) ? respData[0] : respData;
        if (issueObj && issueObj.id) {
          setIsCreating(false);
          setSelectedIssue(issueObj);
          openedFromConversationRef.current = true;
        }
      } catch (_err) {
        // Ignore: the background list load may still surface the issue's row.
      }
    })();
    return () => { cancelled = true; };
  }, [initialSelectedIssueId, initialSelectNonce, workingDir, issues]);

  // Open the side panel in "create" mode for a brand-new issue.
  const openCreate = useCallback(() => {
    openedFromConversationRef.current = false;
    setCreateParent(null);
    setSelectedIssue(null);
    setIsCreating(true);
  }, []);

  // Open the create panel pre-seeded to create a child of the given epic.
  const openCreateInEpic = useCallback((epicId) => {
    openedFromConversationRef.current = false;
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

  // Close the side panel, whether it is in view or create mode. If the panel was
  // opened by following a conversation's linked-issue link, return to that
  // conversation instead of leaving the user on the beads list.
  const closePanel = useCallback(() => {
    const returnToConversation = openedFromConversationRef.current;
    openedFromConversationRef.current = false;
    setSelectedIssue(null);
    setIsCreating(false);
    setCreateParent(null);
    if (returnToConversation && onReturnToConversation) onReturnToConversation();
  }, [onReturnToConversation]);

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

  // Open the per-issue context menu anchored to the row's "..." button (rather
  // than at the cursor), then load the beadsIssues prompts like the right-click path.
  const handleRowMenuButton = useCallback(
    (e, issue) => {
      e.preventDefault();
      e.stopPropagation();
      const rect = e.currentTarget.getBoundingClientRect();
      setContextMenu({ x: rect.left, y: rect.bottom, issue });
      if (onFetchBeadsPrompts) {
        onFetchBeadsPrompts(workingDir).then((prompts) =>
          setMenuPrompts(prompts || []),
        );
      }
    },
    [onFetchBeadsPrompts, workingDir],
  );

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
      // Hide an issue only when its status maps to a toggle that is currently
      // off. Statuses without a toggle (e.g. blocked, deferred) are unaffected.
      if (statusToggles[issue.status] === false) return false;
      if (typeFilter !== "all" && issue.issue_type !== typeFilter) return false;
      if (search) {
        const q = search.toLowerCase();
        if (!(issue.id?.toLowerCase().includes(q) ||
              issue.title?.toLowerCase().includes(q) ||
              issue.owner?.toLowerCase().includes(q))) return false;
      }
      return true;
    });
  }, [issues, statusToggles, typeFilter, search]);

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

  // Grouped render model — only computed when the grouping toggle is on.
  // Produces a sorted top-level array of { type: "epic"|"orphan", ... } items.
  // Epics that survived the filter are shown with their filtered children;
  // epics that were filtered out but have surviving children are kept as ghost
  // header rows (context row). Two indent levels only: all grandchild+ issues
  // are attributed to their nearest TOP-LEVEL epic ancestor.
  const groupedItems = useMemo(() => {
    if (!grouping) return null;

    const issueById = new Map(issues.map(i => [i.id, i]));

    // Epics from the full list: typed as "epic" or has at least one child.
    const epicSet = new Set();
    for (const i of issues) {
      if (i.issue_type === "epic" || (childCountById[i.id] || 0) > 0) epicSet.add(i.id);
    }

    // Walk up the parent chain and return the ID of the topmost epic ancestor,
    // or null if the issue itself is a top-level epic or has no epic ancestor.
    // Guards against cycles with a seen set (mirrors deleteTargetDescendants).
    function topLevelEpicOf(issue) {
      const seen = new Set([issue.id]);
      let cur = issue;
      let result = null;
      while (cur.parent) {
        if (seen.has(cur.parent)) break;
        seen.add(cur.parent);
        const parent = issueById.get(cur.parent);
        if (!parent) break;
        cur = parent;
        if (epicSet.has(cur.id)) result = cur.id;
      }
      return result;
    }

    // Assign each filtered issue to a top-level epic group or orphan.
    // epicGroups: epicId -> { epic: issue|null, children: issue[] }
    const epicGroups = new Map();
    const epicOrderIds = [];
    const orphans = [];

    for (const issue of filtered) {
      const ancestorId = topLevelEpicOf(issue);
      if (epicSet.has(issue.id) && ancestorId === null) {
        // Top-level epic
        if (!epicGroups.has(issue.id)) {
          epicGroups.set(issue.id, { epic: issue, children: [] });
          epicOrderIds.push(issue.id);
        } else {
          epicGroups.get(issue.id).epic = issue;
        }
      } else if (ancestorId !== null) {
        // Belongs to a top-level epic (direct child, sub-epic, grandchild, …)
        if (!epicGroups.has(ancestorId)) {
          // Ghost header: epic filtered out but a child survived
          epicGroups.set(ancestorId, { epic: issueById.get(ancestorId) || null, children: [] });
          epicOrderIds.push(ancestorId);
        }
        epicGroups.get(ancestorId).children.push(issue);
      } else {
        orphans.push(issue);
      }
    }

    function cmpIssue(a, b) {
      const pa = typeof a.priority === "number" ? a.priority : 3;
      const pb = typeof b.priority === "number" ? b.priority : 3;
      if (pa !== pb) return pa - pb;
      return (a.id || "").localeCompare(b.id || "");
    }

    for (const [, group] of epicGroups) group.children.sort(cmpIssue);

    // Top-level: epics and orphans sorted together by priority then id.
    const topLevel = [];
    for (const id of epicOrderIds) topLevel.push({ type: "epic", group: epicGroups.get(id) });
    for (const issue of orphans) topLevel.push({ type: "orphan", issue });
    topLevel.sort((a, b) => {
      const ia = a.type === "epic" ? (a.group.epic || { priority: 3, id: "" }) : a.issue;
      const ib = b.type === "epic" ? (b.group.epic || { priority: 3, id: "" }) : b.issue;
      return cmpIssue(ia, ib);
    });
    return topLevel;
  }, [filtered, issues, childCountById, grouping]);

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
    () => deleteTargetDescendants.filter(d => d.issue.status !== "closed"),
    [deleteTargetDescendants],
  );

  // Reset the child-handling choice whenever the delete target changes, so it
  // never carries over from a previous deletion.
  useEffect(() => { setChildAction("none"); }, [deleteTarget]);

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
            const cres = await secureFetch(apiUrl("/api/beads/status"), {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ working_dir: workingDir, id: child.id, action: "close" }),
            });
            const cdata = await readBeadsResponse(cres);
            if (!cres.ok || cdata.error) closeFailed++;
            else closedCount++;
          } catch (err) {
            closeFailed++;
          }
        }
      } else if (childAction === "delete") {
        // Delete deepest-first so a parent is never removed before its children.
        const ordered = [...deleteTargetDescendants].sort((a, b) => b.depth - a.depth);
        for (const { issue: child } of ordered) {
          try {
            const cres = await secureFetch(apiUrl("/api/beads/delete"), {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ working_dir: workingDir, id: child.id }),
            });
            const cdata = await readBeadsResponse(cres);
            if (!cres.ok || cdata.error) childDeleteFailed++;
            else childDeletedCount++;
          } catch (err) {
            childDeleteFailed++;
          }
        }
      }

      const res = await secureFetch(apiUrl("/api/beads/delete"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, id }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        showToast && showToast({ style: "error", title: data.error || "Failed to delete issue" });
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
          showToast && showToast({
            style: "warning",
            title: `${title} (${failedTotal} child issue${failedTotal === 1 ? "" : "s"} failed to ${verb})`,
          });
        } else {
          showToast && showToast({ style: "success", title });
        }
        fetchList();
      }
    } catch (err) {
      showToast && showToast({ style: "error", title: err.message || "Failed to delete issue" });
    } finally {
      setDeletingIssue(false);
      setDeleteTarget(null);
    }
  }, [deleteTarget, childAction, deleteTargetOpenDescendants, deleteTargetDescendants, workingDir, showToast, fetchList]);

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

  // Shared row renderer for both flat and grouped render paths.
  // Treat an issue as an epic when it is typed as one or has at least one
  // child issue, giving it a purple left accent. Selected card always wins
  // on background/border. The hovered (non-selected) row gets Mitto's solid
  // brand red — the same red used for the active session item and delete
  // buttons; priority/status/type badge pills are opaque so they stay
  // readable on the red background.
  function renderIssueRow(issue) {
    const linkedSessionId = issueSessionMap[issue.id];
    const isStreamingIssue = issueStreamingSet.has(issue.id);
    const isSelected = selectedIssue && selectedIssue.id === issue.id;
    const childCount = childCountById[issue.id] || 0;
    const isEpic = issue.issue_type === "epic" || childCount > 0;
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
      <div class="list-col-grow flex flex-col gap-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          ${isStreamingIssue
            ? html`<span class="shrink-0 text-mitto-accent">
                <span
                  class="loading loading-ring loading-xs"
                  title="A linked conversation is responding..."
                ></span>
              </span>`
            : null}
          <span class="font-mono text-xs max-w-40 truncate" title=${issue.id}>
            ${linkedSessionId && onOpenConversation
              ? html`<a
                  href="#"
                  class="text-mitto-accent-400 hover:text-mitto-accent-300 hover:underline"
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
        <div class="text-sm text-mitto-text wrap-break-word">${issue.title}</div>
      </div>
      <div class="flex items-center gap-1 shrink-0 self-center">
        ${isEpic
          ? html`<button
              type="button"
              onClick=${(e) => { e.preventDefault(); e.stopPropagation(); openCreateInEpic(issue.id); }}
              class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong"
              title="New issue in epic"
              aria-label="New issue in epic"
              data-testid="beads-issue-add-child"
            >
              <${PlusIcon} className="w-3.5 h-3.5" />
            </button>`
          : null}
        <button
          type="button"
          onClick=${(e) => handleRowMenuButton(e, issue)}
          class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 text-mitto-text-muted hover:text-mitto-text-strong"
          title="More actions"
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

  return html`
    <div class="relative flex h-full overflow-hidden">
    <div class="flex flex-col flex-1 min-w-0 overflow-hidden">
      <div class="flex items-center gap-2 p-4 border-b border-mitto-border shrink-0">
        <button
          onClick=${() => onShowSidebar && onShowSidebar()}
          class="btn btn-ghost btn-square btn-sm md:hidden shrink-0"
          title="Show conversations"
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <span class="font-semibold text-lg flex-1">Tasks — ${workspaceLabel}</span>
      </div>

      <div class="beads-toolbar flex items-center gap-2 px-4 border-b border-mitto-border shrink-0">
        <div class="join shrink-0" role="group" aria-label="Filter by status">
          ${BEADS_STATUS_TOGGLES.map(t => html`
            <button
              type="button"
              onClick=${() => toggleStatus(t.key)}
              aria-pressed=${statusToggles[t.key] ? "true" : "false"}
              aria-label=${statusToggles[t.key] ? `Hide ${t.label} issues` : `Show ${t.label} issues`}
              title=${statusToggles[t.key] ? `Hide ${t.label} issues` : `Show ${t.label} issues`}
              class="btn btn-xs btn-square join-item ${statusToggles[t.key] ? "btn-active" : "btn-ghost opacity-50"}"
            >
              <${t.Icon} className="w-3.5 h-3.5" />
            </button>
          `)}
        </div>
        <div class="join shrink-0" role="group" aria-label="View mode">
          <button
            type="button"
            onClick=${() => setGrouping(g => !g)}
            aria-pressed=${grouping ? "true" : "false"}
            title=${grouping ? "Switch to flat list" : "Group issues by epic"}
            class="btn btn-xs join-item ${grouping ? "btn-active" : "btn-ghost"}"
          >
            <${LayersIcon} className="w-3.5 h-3.5" />
          </button>
        </div>
        <select
          class="select select-xs shrink-0 w-28"
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
          class="input input-xs flex-1 min-w-0"
        />
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
            transition: pullDistance === 0 ? "height 0.2s ease, opacity 0.2s ease" : "none",
            flexShrink: 0,
          }}
        >
          <span class="loading loading-spinner w-5 h-5 text-mitto-text-secondary"></span>
        </div>`}
        ${!loading && error && html`
          <div class="flex items-center justify-center h-24 text-red-400 text-sm px-4">${error}</div>
        `}
        ${!loading && !error && filtered.length === 0 && html`
          <div class="flex flex-col items-center justify-center gap-1 h-32 text-center px-4">
            <div class="text-mitto-text-secondary text-sm">No issues found</div>
            <div class="text-mitto-text-muted text-xs">Create a new issue by pressing the "+" button below.</div>
          </div>
        `}
        ${!error && filtered.length > 0 && html`
          <div class="list p-2">
            ${grouping && groupedItems
              ? groupedItems.map(item => {
                  if (item.type === "orphan") return renderIssueRow(item.issue);
                  // Epic group: render a <details> with the epic as the
                  // clickable <summary> header and its children indented below.
                  const { group } = item;
                  const epicIssue = group.epic;
                  const epicId = epicIssue ? epicIssue.id : null;
                  const isOpen = epicId ? !collapsedEpics.has(epicId) : true;
                  return html`
                    <details
                      key=${epicId || ("ghost-" + (group.children[0] && group.children[0].id))}
                      class="beads-epic-group"
                      open=${isOpen}
                      onToggle=${(e) => {
                        if (!epicId) return;
                        const open = e.currentTarget.open;
                        setCollapsedEpics(prev => {
                          const next = new Set(prev);
                          if (open) next.delete(epicId);
                          else next.add(epicId);
                          return next;
                        });
                      }}
                    >
                      <summary class="beads-epic-summary">
                        ${epicIssue
                          ? renderIssueRow(epicIssue)
                          : html`<div class="list-row opacity-60 border border-dashed border-mitto-border">
                              <div class="list-col-grow text-xs text-mitto-text-muted italic">Epic (not in current filter)</div>
                            </div>`}
                      </summary>
                      <div class="pl-8">
                        ${group.children.map(child => renderIssueRow(child))}
                      </div>
                    </details>
                  `;
                })
              : filtered.map(issue => renderIssueRow(issue))
            }
          </div>
        `}
      </div>

      <div class="flex items-center gap-1 p-4 border-t border-mitto-border shrink-0">
        <button
          onClick=${openCreate}
          class="btn btn-ghost btn-square btn-sm"
          title="New issue"
        >
          <${PlusIcon} className="w-4 h-4" />
        </button>
        <div class="relative" ref=${listPromptsRef}>
          <button
            type="button"
            onClick=${toggleListPrompts}
            class="btn btn-ghost btn-square btn-sm"
            title="Run a prompt over the issue list in a new conversation"
          >
            <${ChevronUpIcon} className="w-4 h-4" />
          </button>
          ${showListPrompts && html`
            <ul class="menu absolute bottom-full left-0 mb-2 w-64 max-h-72 overflow-y-auto flex-nowrap bg-base-200 rounded-box shadow-xl z-10">
              ${listPromptsLoading && html`
                <li class="px-3 py-2 flex items-center gap-2">
                  <span class="loading loading-spinner w-4 h-4"></span> Loading…
                </li>
              `}
              ${!listPromptsLoading && listPrompts.length === 0 && html`
                <li class="px-3 py-2 opacity-60">No task prompts</li>
              `}
              ${!listPromptsLoading && listPrompts.map(p => {
                const PromptIcon = getPromptIconOrDefault(p.icon);
                return html`
                <li key=${p.name}>
                  <button
                    type="button"
                    onClick=${() => handleRunListPrompt(p)}
                    title=${p.description || p.name}
                  >
                    <span class="w-4 h-4 shrink-0"><${PromptIcon} className="w-4 h-4" /></span>
                    <span class="truncate flex-1">${p.name}</span>
                  </button>
                </li>
              `;
              })}
            </ul>
          `}
        </div>
        <button
          onClick=${fetchList}
          class="btn btn-ghost btn-square btn-sm"
          title="Refresh"
        >
          <${RefreshIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${() => { if (closedCount === 0 || cleaningUp) return; setShowCleanupConfirm(true); }}
          aria-disabled=${closedCount === 0 || cleaningUp ? "true" : "false"}
          class="btn btn-ghost btn-square btn-sm group ${closedCount === 0 || cleaningUp ? "opacity-40 pointer-events-none" : ""}"
          title=${closedCount === 0 ? "No closed issues to clean up" : `Clean up ${closedCount} closed issue${closedCount === 1 ? "" : "s"}`}
        >
          <${BroomIcon} className="w-4 h-4 group-hover:text-red-400" />
        </button>

        ${upstream && upstream !== "none" && html`
          <div class="flex items-center gap-1 pl-2 ml-1 border-l border-mitto-border">
            <button
              onClick=${() => { if (syncAction) return; handleSync("pull"); }}
              aria-disabled=${syncAction ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm ${syncAction ? "opacity-40 pointer-events-none" : ""}"
              title=${`Pull from ${UPSTREAM_LABELS[upstream] || upstream}`}
            >
              ${syncAction === "pull"
                ? html`<span class="loading loading-spinner w-4 h-4"></span>`
                : html`<${ArrowDownIcon} className="w-4 h-4" />`}
            </button>
            <button
              onClick=${() => { if (syncAction) return; handleSync("push"); }}
              aria-disabled=${syncAction ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm ${syncAction ? "opacity-40 pointer-events-none" : ""}"
              title=${`Push to ${UPSTREAM_LABELS[upstream] || upstream}`}
            >
              ${syncAction === "push"
                ? html`<span class="loading loading-spinner w-4 h-4"></span>`
                : html`<${ArrowUpIcon} className="w-4 h-4" />`}
            </button>
            <button
              onClick=${() => { if (syncAction) return; handleSync("sync"); }}
              aria-disabled=${syncAction ? "true" : "false"}
              class="btn btn-ghost btn-square btn-sm ${syncAction ? "opacity-40 pointer-events-none" : ""}"
              title=${`Sync with ${UPSTREAM_LABELS[upstream] || upstream} (pull then push)`}
            >
              ${syncAction === "sync"
                ? html`<span class="loading loading-spinner w-4 h-4"></span>`
                : html`<${SyncIcon} className="w-4 h-4" />`}
            </button>
          </div>
        `}

        <span class="text-xs text-mitto-text-secondary ml-auto">${filtered.length} issue${filtered.length === 1 ? "" : "s"}</span>

        ${onOpenConfig && html`
          <button
            onClick=${() => onOpenConfig()}
            class="btn btn-ghost btn-square btn-sm ml-2"
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
      createParentId=${createParent}
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
      title=${deleteTargetDescendants.length > 0 ? "Delete epic" : "Delete issue"}
      message=${deleteTarget ? `This will permanently delete ${deleteTarget.id} — "${deleteTarget.title}". This cannot be undone.` : ""}
      confirmLabel="Delete"
      cancelLabel="Cancel"
      confirmVariant="danger"
      isLoading=${deletingIssue}
      onConfirm=${confirmDeleteIssue}
      onCancel=${() => setDeleteTarget(null)}
    >
      ${deleteTargetDescendants.length > 0 && html`
        <div class="mt-3 space-y-2">
          <p class="text-sm text-mitto-text-secondary">
            This epic has ${deleteTargetDescendants.length} descendant issue${deleteTargetDescendants.length === 1 ? "" : "s"}. What should happen to ${deleteTargetDescendants.length === 1 ? "it" : "them"}?
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
            <span class="text-sm text-mitto-text-secondary">Leave child issues unchanged</span>
          </label>
          ${deleteTargetOpenDescendants.length > 0 && html`
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
                Close the ${deleteTargetOpenDescendants.length} open child issue${deleteTargetOpenDescendants.length === 1 ? "" : "s"}
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
              Delete all ${deleteTargetDescendants.length} child issue${deleteTargetDescendants.length === 1 ? "" : "s"} (permanent)
            </span>
          </label>
        </div>
      `}
    </${ConfirmDialog}>
  `;
}

