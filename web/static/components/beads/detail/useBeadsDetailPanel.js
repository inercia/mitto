// Custom hook housing all state / refs / memos / callbacks / effects of the
// former BeadsDetailPanel body (mitto-90f.7 PR-11). Extracted verbatim as a
// mechanical code motion from web/static/components/BeadsView.js — no renames,
// no restructuring, no dead-code removal. Every one of the original 107 hooks
// (44 useState + 15 useRef + 7 useMemo + 25 useCallback + 16 useEffect)
// remains in its original declaration order so React's hook-order invariant is
// preserved.
//
// The hook returns a single bag whose shape mirrors the 31 top-level props
// (7 bundles + 24 flat) BeadsDetailPanelBody consumes (see PR-10). The caller
// (BeadsDetailPanel in BeadsView.js) collapses to a ~15-LOC glue that unpacks
// this bag straight into <BeadsDetailPanelBody .../>.
//
// Effect 1124 (issue-switch reset touching deps/labels/comments/notes) stays
// inline here — it is the one cross-cluster effect the scoping pass flagged;
// splitting it belongs to a later sub-split PR (mitto-90f.7+).

const { html, useState, useEffect, useCallback, useMemo, useRef } =
  window.preact;

import { authFetch, secureFetch, endpoints } from "../../../utils/index.js";
import { readBeadsResponse } from "../../../utils/beads.js";
import { renderMarkdown } from "../CommentBody.js";
import { buildPromptGroupMenuItems } from "../../ContextMenu.js";
import { useIssueLabels } from "./useIssueLabels.js";
import {
  PlusIcon,
  CheckIcon,
  RefreshIcon,
  MoonIcon,
  SunIcon,
  TrashIcon,
  ExpandIcon,
  CollapseIcon,
  CloseIcon,
  LightningIcon,
  getPromptIconOrDefault,
} from "../../Icons.js";

export function useBeadsDetailPanel({
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
  // Shortcut buttons configured for this folder's beadsIssue section (mirrors
  // the list toolbar's tasksList shortcuts, but keyed to the open issue).
  const [issueShortcuts, setIssueShortcuts] = useState([]);
  // Map from prompt name → prompt object, resolved from the beadsIssues menu.
  const [issueShortcutPromptMap, setIssueShortcutPromptMap] = useState(
    new Map(),
  );
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
  // After a successful Save, holds the just-persisted field values so the dirty
  // check clears immediately — without waiting for the async onUpdated() refresh
  // to flow updated `data` back down. Reset to null when a different issue opens
  // (the seed effect below). When set, it takes precedence over viewOriginal.
  const [savedBaseline, setSavedBaseline] = useState(null);

  // View-mode dependencies. The list rows only carry a dependency_count, so the
  // full edges (id + title + status + dependency_type) are fetched from
  // /api/issues/{id} when an issue is opened. `depsBusy` gates add/remove
  // requests; `newDepType`/`newDepId` back the "add dependency" row.
  const [deps, setDeps] = useState([]);
  const [depsLoading, setDepsLoading] = useState(false);
  const [depsBusy, setDepsBusy] = useState(false);
  const [newDepType, setNewDepType] = useState("blocks");
  const [newDepId, setNewDepId] = useState("");
  // Labels editing state + handlers now live in useIssueLabels (mitto-90f.7
  // PR-12). fetchDepsRef bridges the labels->deps callback tangle: fetchDeps
  // is defined later in this hook, so we hand the sub-hook a ref that we
  // populate below (see `fetchDepsRef.current = fetchDeps`). The composer
  // continues to read labels state via `labels.*` (see fetchDeps refresh and
  // the issue-switch reset effect below).
  const fetchDepsRef = useRef(null);
  const labels = useIssueLabels({
    data,
    workingDir,
    showToast,
    fetchDepsRef,
    onUpdated,
    isOpen,
    creating,
  });
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
    // A successful save records its persisted values in savedBaseline; compare
    // against those so the panel is no longer "dirty" the instant Save resolves.
    const base = savedBaseline || viewOriginal;
    const t = viewDraft.title.trim();
    return (
      (t !== "" && t !== base.title) ||
      viewDraft.type !== base.type ||
      viewDraft.priority !== base.priority ||
      viewDraft.description !== base.description ||
      viewDraft.assignee.trim() !== base.assignee ||
      viewDraft.notes !== base.notes
    );
  }, [creating, viewDraft, viewOriginal, savedBaseline]);

  // handleClose and handleDiscardAndClose are defined here (after creating and
  // viewDirty) because their dep arrays reference both computed values.
  const doClose = useCallback(() => {
    setIsClosing(true);
    setTimeout(() => onClose(), 150);
  }, [onClose]);

  // Set when a close is requested while a save is still in flight. The close is
  // deferred until the save settles (resolved by the effect below) so a
  // Save→Close race no longer surfaces a spurious "Discard changes?" prompt.
  const pendingCloseRef = useRef(false);

  const handleClose = useCallback(() => {
    // A save is still running: remember that the user wants to close and let the
    // in-flight save finish first. The deferred close resolves in the effect
    // below once savingView clears.
    if (!creating && savingView) {
      pendingCloseRef.current = true;
      return;
    }
    if (!creating && viewDirty) {
      setConfirmDiscard(true);
      return;
    }
    doClose();
  }, [creating, viewDirty, savingView, doClose]);

  // Resolve a close that was deferred while a save was in flight. A successful
  // save clears viewDirty (savedBaseline now matches the draft) so the panel
  // closes silently; a failed save leaves the draft dirty, so we fall through to
  // the discard guard rather than silently losing the user's edits.
  useEffect(() => {
    if (savingView || !pendingCloseRef.current) return;
    pendingCloseRef.current = false;
    if (!creating && viewDirty) {
      setConfirmDiscard(true);
      return;
    }
    doClose();
  }, [savingView, creating, viewDirty, doClose]);

  const handleDiscardAndClose = useCallback(() => {
    setConfirmDiscard(false);
    doClose();
  }, [doClose]);

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

  // Load per-folder beadsIssue-section shortcut buttons for the detail toolbar.
  // Mirrors BeadsView's tasksList loader: fetch section entries, then resolve
  // each entry's prompt name against the beadsIssues menu via onFetchPrompts.
  // `isStale` lets the mount effect below cancel a stale in-flight fetch when
  // the folder changes mid-request.
  const loadIssueShortcuts = useCallback(
    async (isStale) => {
      if (!workingDir) {
        setIssueShortcuts([]);
        setIssueShortcutPromptMap(new Map());
        return;
      }
      try {
        // Merge global + folder shortcuts for the beadsIssue section. Global
        // buttons come first; folder buttons duplicating a global prompt drop out.
        const [folderRes, globalRes] = await Promise.all([
          authFetch(endpoints.folders.shortcuts({ working_dir: workingDir })),
          authFetch(endpoints.global.shortcuts()).catch(() => null),
        ]);
        const cfg = await folderRes.json().catch(() => ({}));
        const globalData = globalRes
          ? await globalRes.json().catch(() => ({}))
          : {};
        const globalList = globalData?.sections?.beadsIssue || [];
        const folderList = cfg?.sections?.beadsIssue || [];
        const globalNames = new Set(globalList.map((s) => s.prompt));
        const list = [
          ...globalList,
          ...folderList.filter((s) => !globalNames.has(s.prompt)),
        ];
        if (isStale && isStale()) return;
        setIssueShortcuts(list);
        if (list.length > 0 && onFetchPrompts) {
          const prompts = await onFetchPrompts(workingDir);
          if (isStale && isStale()) return;
          const map = new Map((prompts || []).map((p) => [p.name, p]));
          setIssueShortcutPromptMap(map);
        } else {
          setIssueShortcutPromptMap(new Map());
        }
      } catch (_err) {
        if (isStale && isStale()) return;
        setIssueShortcuts([]);
        setIssueShortcutPromptMap(new Map());
      }
    },
    [workingDir, onFetchPrompts],
  );

  // Initial load (and reload on folder switch), with stale-fetch cancellation.
  useEffect(() => {
    let cancelled = false;
    loadIssueShortcuts(() => cancelled);
    return () => {
      cancelled = true;
    };
  }, [loadIssueShortcuts]);

  // Refresh shortcut buttons immediately when the Workspaces dialog saves new
  // shortcuts for this folder, so no page reload is needed.
  useEffect(() => {
    const handler = (e) => {
      const dir = e?.detail?.working_dir;
      if (!dir || dir === workingDir) loadIssueShortcuts();
    };
    // Global shortcuts changes affect every folder, so always refresh.
    const globalHandler = () => loadIssueShortcuts();
    window.addEventListener("mitto:folder_shortcuts_updated", handler);
    window.addEventListener("mitto:global_shortcuts_updated", globalHandler);
    return () => {
      window.removeEventListener("mitto:folder_shortcuts_updated", handler);
      window.removeEventListener(
        "mitto:global_shortcuts_updated",
        globalHandler,
      );
    };
  }, [loadIssueShortcuts, workingDir]);

  // The panel context menu is now prompts-only: the former Close/Defer/Delete
  // menu items are surfaced as direct buttons in the header Toolbar
  // (headerToolbarItems below). The toolbar's "Run a prompt" button opens it.
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
    if (promptGroupItems.length === 0) {
      return [{ label: "No prompts available", disabled: true }];
    }
    return promptGroupItems;
  }, [data, prompts, onRunPrompt]);

  // Header action toolbar (view mode). Replaces the former "…" overflow menu and
  // standalone fullscreen button: a prompts trigger, Close/Reopen, Defer/Undefer,
  // a destructive Delete set apart by a separator, then the per-folder shortcut
  // buttons (separated), a spacer, then fullscreen at the right edge.
  const headerToolbarItems = useMemo(() => {
    if (!data) return [];
    // Per-folder shortcut buttons (beadsIssue section). A missing linked prompt
    // is shown greyed/disabled, mirroring the list toolbar's tasksList shortcuts.
    const issueShortcutItems = issueShortcuts.map((sc, i) => {
      const prompt = issueShortcutPromptMap.get(sc.prompt);
      const found = !!prompt;
      const Icon = getPromptIconOrDefault(sc.icon || (prompt && prompt.icon));
      return {
        kind: "button",
        testId: `beads-issue-shortcut-btn-${i}`,
        // On phone-width screens only the first shortcut is shown; the rest are
        // hidden (see .mitto-shortcut-extra in styles-v2.css) to avoid overflow.
        className: i > 0 ? "mitto-shortcut-extra" : undefined,
        icon: html`<${Icon} className="w-4 h-4" />`,
        tip: found ? sc.prompt : `Prompt "${sc.prompt}" not found`,
        ariaLabel: found
          ? `Run "${sc.prompt}"`
          : `Prompt "${sc.prompt}" not found`,
        disabled: !found,
        onClick: () => found && onRunPrompt && onRunPrompt(prompt, data),
      };
    });
    return [
      {
        kind: "button",
        testId: "beads-panel-prompts",
        icon: html`<${LightningIcon} className="w-4 h-4" />`,
        tip: "Run a prompt",
        ariaLabel: "Run a prompt",
        onClick: openPanelMenu,
      },
      { kind: "separator" },
      {
        kind: "button",
        testId: "beads-panel-status",
        icon:
          data.status === "closed"
            ? html`<${RefreshIcon} className="w-4 h-4" />`
            : html`<${CheckIcon} className="w-4 h-4" />`,
        tip: data.status === "closed" ? "Reopen" : "Close",
        ariaLabel: data.status === "closed" ? "Reopen" : "Close",
        disabled: statusBusy,
        onClick: () => onToggleStatus && onToggleStatus(data),
      },
      {
        kind: "button",
        testId: "beads-panel-defer",
        icon:
          data.status === "deferred"
            ? html`<${SunIcon} className="w-4 h-4" />`
            : html`<${MoonIcon} className="w-4 h-4" />`,
        tip: data.status === "deferred" ? "Undefer" : "Defer",
        ariaLabel: data.status === "deferred" ? "Undefer" : "Defer",
        disabled: statusBusy,
        onClick: () => onToggleDefer && onToggleDefer(data),
      },
      { kind: "separator" },
      {
        kind: "button",
        testId: "beads-panel-delete",
        icon: html`<${TrashIcon} className="w-4 h-4" />`,
        tip: "Delete",
        ariaLabel: "Delete",
        danger: true,
        onClick: () => onDelete && onDelete(data),
      },
      ...(issueShortcuts.length > 0
        ? [{ kind: "separator" }, ...issueShortcutItems]
        : []),
      { kind: "spacer" },
      {
        kind: "button",
        testId: "beads-panel-fullscreen",
        icon: fullscreen
          ? html`<${CollapseIcon} className="w-4 h-4" />`
          : html`<${ExpandIcon} className="w-4 h-4" />`,
        tip: fullscreen ? "Exit fullscreen" : "Fullscreen",
        ariaLabel: fullscreen ? "Exit fullscreen" : "Fullscreen",
        onClick: () => setFullscreen((f) => !f),
      },
      {
        kind: "button",
        testId: "beads-panel-close",
        icon: html`<${CloseIcon} className="w-4 h-4" />`,
        tip: "Close",
        ariaLabel: "Close",
        onClick: () => handleClose(),
      },
    ];
  }, [
    data,
    statusBusy,
    fullscreen,
    openPanelMenu,
    onToggleStatus,
    onToggleDefer,
    onDelete,
    onRunPrompt,
    issueShortcuts,
    issueShortcutPromptMap,
    handleClose,
  ]);

  // Seed non-notes fields whenever a different issue opens (notes come from
  // fetchDeps below, which calls setViewDraft when seedDraftNotes is true).
  useEffect(() => {
    if (creating || !data || !data.id) return;
    setSavedBaseline(null);
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
        // Record what we just persisted so viewDirty clears immediately (the
        // normalized values mirror how the dirty check reads the draft), instead
        // of staying dirty until the async onUpdated() refresh re-seeds `data`.
        setSavedBaseline({
          title: viewDraft.title.trim(),
          type: viewDraft.type,
          priority: viewDraft.priority,
          description: viewDraft.description,
          assignee: viewDraft.assignee.trim(),
          notes: viewDraft.notes,
        });
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
          labels.setLabels([]);
          setComments([]);
          setNotes("");
          if (seedDraftNotes) setViewDraft((prev) => ({ ...prev, notes: "" }));
        } else {
          const issueObj = Array.isArray(respData) ? respData[0] : respData;
          setDeps((issueObj && issueObj.dependencies) || []);
          labels.setLabels((issueObj && issueObj.labels) || []);
          setComments((issueObj && issueObj.comments) || []);
          const fetchedNotes = (issueObj && issueObj.notes) || "";
          setNotes(fetchedNotes);
          if (seedDraftNotes)
            setViewDraft((prev) => ({ ...prev, notes: fetchedNotes }));
        }
      } catch (_err) {
        setDeps([]);
        labels.setLabels([]);
        setComments([]);
        setNotes("");
        if (seedDraftNotes) setViewDraft((prev) => ({ ...prev, notes: "" }));
      } finally {
        setDepsLoading(false);
      }
    },
    [workingDir, data && data.id, labels.setLabels],
  );
  // Wire the fetchDepsRef forward-reference bridge used by useIssueLabels so
  // mutateLabel can trigger a full issue refresh after add/remove.
  fetchDepsRef.current = fetchDeps;

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
    labels.setLabels([]);
    setComments([]);
    setNotes("");
    setNewDepId("");
    setNewDepType("blocks");
    labels.setNewLabel("");
    labels.setAddingLabel(false);
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

  // NOTE: the two early-return-null gates that historically lived here
  // (`if (!shouldRender) return null;` and `if (!creating && !data) return null;`)
  // were rendering guards for the JSX return that used to follow. Now that
  // the hook returns a data bag instead of JSX, the guards move to the caller
  // (BeadsDetailPanel in BeadsView.js), which checks h.shouldRender /
  // h.creating / h.data on the returned bag before rendering.

  return {
    // Early-return gates (caller uses these before rendering)
    shouldRender,
    creating,
    data,
    // Flat props (24)
    isClosing,
    isMobile,
    fullscreen,
    setFullscreen,
    createParentId,
    submitting,
    viewDirty,
    savingView,
    description,
    setDescription,
    createEditorApiRef,
    detailEditorApiRef,
    descMinHeight,
    descViewRef,
    md,
    workingDir,
    improvingDesc,
    improveDescriptionText,
    allIssues,
    subtasks,
    onSelectIssue,
    showToast,
    // Bundles (7)
    create: {
      title,
      setTitle,
      type,
      setType,
      priority,
      setPriority,
      createAssignee,
      setCreateAssignee,
      createDeps,
      setCreateDeps,
      removeCreateDep,
      createNewDepType,
      setCreateNewDepType,
      createNewDepId,
      setCreateNewDepId,
      addCreateDep,
      createNotes,
      setCreateNotes,
    },
    view: {
      viewDraft,
      setViewDraft,
      editingType,
      setEditingType,
      typeRef,
      editingAssignee,
      setEditingAssignee,
      assigneeRef,
      editingTitle,
      setEditingTitle,
      titleRef,
      editingDesc,
      setEditingDesc,
      editingNotes,
      setEditingNotes,
      notesRef,
      notesViewRef,
      notesMinHeight,
    },
    deps: {
      deps,
      depsLoading,
      depsBusy,
      changeDepType,
      mutateDep,
      newDepType,
      setNewDepType,
      newDepId,
      setNewDepId,
      handleAddDep,
    },
    labels,
    comments: {
      comments,
      addingComment,
      commentDraft,
      setCommentDraft,
      savingComment,
      commentRef,
      handleCommentBlur,
      startAddComment,
    },
    handlers: {
      handleClose,
      handleSave,
      handleViewSave,
      handleDiscardAndClose,
      handleTitleKeyDown,
      handleAssigneeKeyDown,
      startEditTitle,
      startEditAssignee,
      startEditDesc,
      startEditNotes,
    },
    chrome: {
      headerToolbarItems,
      panelMenu,
      setPanelMenu,
      panelMenuItems,
      confirmDiscard,
      setConfirmDiscard,
    },
  };
}
