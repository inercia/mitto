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
import {
  readBeadsResponse,
  matchesSearch,
  cmpBySort,
  SORT_FIELD_OPTIONS,
  SORT_FIELD_LABELS,
  CLEANUP_PROGRESS_TOAST_INTERVAL_MS,
  UPSTREAM_LABELS,
  DEP_TYPES,
  PRIORITY_LABELS,
  ISSUE_TYPES,
  BEADS_SUPPORTS_HOVER,
  BEADS_TOOLTIP_DELAY_MS,
} from "../utils/beads.js";
// Re-export STATUS_COLORS at its original location so any external consumer
// that had `import { STATUS_COLORS } from ".../BeadsView.js"` keeps working
// after the move to utils/beads.js (mitto-90f.3 E-3).
export { STATUS_COLORS } from "../utils/beads.js";
import {
  priorityBadge,
  statusBadge,
  depStatusBadge,
  typeBadge,
} from "./beads/Badges.js";
import {
  renderMarkdown,
  handleBeadsContentClick,
  commentBody,
} from "./beads/CommentBody.js";
// Detail-panel sub-components (Fields.js, CommentsSection.js, Sections.js) are
// no longer imported here directly — they are used exclusively by
// BeadsDetailPanelBody, which is what BeadsDetailPanel now renders.
import { BeadsDetailPanelBody } from "./beads/detail/PanelBody.js";
import { useBeadsDetailPanel } from "./beads/detail/useBeadsDetailPanel.js";
// Re-export statusBadge at its original location so SessionPanel.js
// (`import { statusBadge as beadsStatusBadge } from "./BeadsView.js"`) keeps
// working after the move to beads/Badges.js (mitto-90f.3 E-4).
export { statusBadge } from "./beads/Badges.js";
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
  LinkIcon,
  ListIcon,
  LightningIcon,
  BoldIcon,
  ItalicIcon,
  StrikethroughIcon,
  InlineCodeIcon,
  CodeBlockIcon,
  NumberedListIcon,
  HeadingIcon,
  QuoteIcon,
} from "./Icons.js";
import { CodeEditorField } from "./CodeEditorField.js";
import {
  ContextMenu,
  buildPromptGroupMenuItems,
  PortalTooltip,
} from "./ContextMenu.js";
import { ConfirmDialog } from "./ConfirmDialog.js";
import { Tooltip } from "./Tooltip.js";
import { Toolbar } from "./Toolbar.js";
import { usePullToRefresh } from "../hooks/usePullToRefresh.js";
import { useSwipeToAction } from "../hooks/index.js";

// ---- helpers ----------------------------------------------------------------
//
// Pure helpers (readBeadsResponse, matchesSearch, cmpBySort, SORT_FIELD_*) and
// pure-data constants (UPSTREAM_LABELS, DEP_TYPES, PRIORITY_*, STATUS_COLORS,
// TYPE_COLORS, ISSUE_TYPES, CLEANUP_PROGRESS_TOAST_INTERVAL_MS,
// BEADS_SUPPORTS_HOVER, BEADS_TOOLTIP_DELAY_MS) live in ../utils/beads.js so
// they can be unit-tested without the window.preact bootstrap. See
// mitto-90f.3 E-1 (helpers) and E-3 (pure-data constants).

// Status filter toggle buttons shown in the Beads toolbar. Each button toggles
// the visibility of issues with the matching status. `key` is the bd status
// value; `label` is the user-facing text (used for the tooltip/aria-label of
// the icon-only button); `Icon` is the glyph rendered inside the button.
// Icon-carrying; stays in this file (utils/beads.js is framework-free).
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

// Badge sub-components (badge, priorityBadge, statusBadge, depStatusBadge,
// typeBadge) live in ./beads/Badges.js so they can be reused independently of
// this file's large surface. See mitto-90f.3 E-4.

// Markdown rendering (renderMarkdown), link-click interception
// (handleBeadsContentClick) and the shared markdown/pre wrapper (commentBody)
// live in ./beads/CommentBody.js. See mitto-90f.3 E-5.

// ---- Detail side panel ------------------------------------------------------
//
// Small helpers used by the detail panel (labelValue) live in
// ./beads/DetailPanelHelpers.js. See mitto-90f.3 E-6.

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
  const h = useBeadsDetailPanel({
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
  });

  if (!h.shouldRender) return null;
  if (!h.creating && !h.data) return null;

  return html`
    <${BeadsDetailPanelBody}
      isClosing=${h.isClosing}
      isMobile=${h.isMobile}
      fullscreen=${h.fullscreen}
      setFullscreen=${h.setFullscreen}
      creating=${h.creating}
      data=${h.data}
      createParentId=${h.createParentId}
      submitting=${h.submitting}
      viewDirty=${h.viewDirty}
      savingView=${h.savingView}
      description=${h.description}
      setDescription=${h.setDescription}
      createEditorApiRef=${h.createEditorApiRef}
      detailEditorApiRef=${h.detailEditorApiRef}
      descMinHeight=${h.descMinHeight}
      descViewRef=${h.descViewRef}
      md=${h.md}
      workingDir=${h.workingDir}
      improvingDesc=${h.improvingDesc}
      improveDescriptionText=${h.improveDescriptionText}
      allIssues=${h.allIssues}
      subtasks=${h.subtasks}
      onSelectIssue=${h.onSelectIssue}
      showToast=${h.showToast}
      create=${h.create}
      view=${h.view}
      deps=${h.deps}
      labels=${h.labels}
      comments=${h.comments}
      handlers=${h.handlers}
      chrome=${h.chrome}
    />
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

/**
 * SchemaSkewDialog — confirm-and-run modal that offers to execute the beads
 * schema migration on the user's behalf when a list load returns HTTP 409
 * `beads_schema_skew`. Reuses ConfirmDialog for chrome/spinner/footer, and
 * renders the extra body (options radios, warning, ack checkbox, inline error)
 * via the children slot.
 *
 * @param {Object} props
 * @param {boolean} props.isOpen
 * @param {string} props.dbPath          - DB path from error.details.db_path.
 * @param {string} props.hint            - Human hint from error.details.hint.
 * @param {Array}  props.options         - Parsed options[] from the gate JSON
 *   (each `{ mode, command, description }`). May be empty; when empty the
 *   dialog defaults to `mode=migrate`.
 * @param {string} props.workingDir      - Sent to the backend as working_dir.
 * @param {Function} props.onSuccess     - Called on HTTP 200 (parent refreshes).
 * @param {Function} props.onCancel      - Called when the user dismisses.
 * @param {Function} [props.showToast]   - Optional toast(text, style) helper.
 */
function SchemaSkewDialog({
  isOpen,
  dbPath,
  hint,
  options,
  workingDir,
  onSuccess,
  onCancel,
  showToast,
}) {
  const hasOptions = Array.isArray(options) && options.length > 0;
  const defaultMode = hasOptions ? options[0].mode || "migrate" : "migrate";
  const [mode, setMode] = useState(defaultMode);
  const [ackChecked, setAckChecked] = useState(false);
  const [isRunning, setIsRunning] = useState(false);
  const [errorMsg, setErrorMsg] = useState("");

  // Reset transient state whenever the dialog is (re)opened so a previous
  // inline error / stale mode / checkbox does not leak across invocations.
  useEffect(() => {
    if (isOpen) {
      setMode(defaultMode);
      setAckChecked(false);
      setIsRunning(false);
      setErrorMsg("");
    }
    // defaultMode is derived from options; safe to depend on isOpen only —
    // options is stable for the lifetime of a single skew event.
  }, [isOpen]);

  // Enable the confirm button only when the "designated migrator" ack is
  // checked for `migrate` mode. `adopt` mode is not destructive-ish and does
  // not require the ack.
  const canConfirm = mode === "adopt" || ackChecked;

  const handleConfirm = async () => {
    if (isRunning) return;
    setIsRunning(true);
    setErrorMsg("");
    try {
      const res = await authFetch(endpoints.beads.migrate(), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ working_dir: workingDir, mode }),
      });
      const data = await readBeadsResponse(res);
      if (!res.ok || data.error) {
        if (data.code === "migrate_from_ui_disabled") {
          setErrorMsg(
            "Running migrations from the UI is disabled in this instance's settings. Ask an administrator to enable `web.beads.allow_migrate_from_ui`, or run the migration from a terminal on the designated clone.",
          );
        } else {
          setErrorMsg(
            data.error || data.message || `Migration failed (HTTP ${res.status})`,
          );
        }
        setIsRunning(false);
        return;
      }
      // Success: parent handles toast + refresh + close.
      onSuccess?.();
    } catch (err) {
      setErrorMsg(err?.message || "Migration request failed");
      setIsRunning(false);
    }
  };

  const message = dbPath
    ? `Run the beads schema migration on this clone for the database at ${dbPath}?`
    : "Run the beads schema migration on this clone?";

  return html`
    <${ConfirmDialog}
      isOpen=${isOpen}
      title="Run beads migration"
      message=${message}
      confirmLabel="Yes, run migration"
      cancelLabel="No"
      confirmVariant="primary"
      isLoading=${isRunning}
      confirmDisabled=${!canConfirm}
      onConfirm=${handleConfirm}
      onCancel=${onCancel}
    >
      <div class="mt-3 space-y-3" data-testid="schema-skew-dialog-body">
        ${dbPath &&
        html`<div
          class="text-xs font-mono break-all text-mitto-text-secondary"
        >
          ${dbPath}
        </div>`}
        ${hint &&
        html`<div class="text-xs text-mitto-text-secondary">${hint}</div>`}
        ${hasOptions &&
        html`
          <div class="space-y-2">
            ${options.map(
              (opt) => html`
                <label
                  class="flex items-start gap-3 cursor-pointer select-none"
                >
                  <input
                    type="radio"
                    name="schema-skew-mode"
                    value=${opt.mode}
                    checked=${mode === opt.mode}
                    disabled=${isRunning}
                    onChange=${() => setMode(opt.mode)}
                    class="radio radio-sm mt-0.5"
                  />
                  <span class="text-sm text-mitto-text-secondary">
                    <span class="font-medium">${opt.mode}</span>
                    ${opt.description
                      ? html` — ${opt.description}`
                      : opt.command
                        ? html` — <code class="text-xs">${opt.command}</code>`
                        : null}
                  </span>
                </label>
              `,
            )}
          </div>
        `}
        <div
          class="text-xs text-amber-400 border border-amber-500 bg-amber-500/10 rounded p-2"
        >
          For remote-backed databases the migration must be run on exactly one
          designated clone. Independent migrations on separate clones fork the
          schema (upstream bug #4259).
        </div>
        ${mode === "migrate" &&
        html`
          <label class="flex items-start gap-3 cursor-pointer select-none">
            <input
              type="checkbox"
              checked=${ackChecked}
              disabled=${isRunning}
              onChange=${(e) => setAckChecked(e.target.checked)}
              class="checkbox checkbox-sm mt-0.5"
              data-testid="schema-skew-ack-checkbox"
            />
            <span class="text-sm text-mitto-text-secondary">
              I understand this is the designated migrator clone.
            </span>
          </label>
        `}
        ${errorMsg &&
        html`
          <div
            class="text-xs text-red-400 break-all"
            data-testid="schema-skew-dialog-error"
          >
            ${errorMsg}
          </div>
        `}
        ${!canConfirm &&
        !errorMsg &&
        html`
          <div class="text-xs text-mitto-text-muted">
            Check the acknowledgement above to enable
            <span class="italic">Yes, run migration</span>.
          </div>
        `}
      </div>
    </${ConfirmDialog}>
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
  // Set when the list load failed with a "beads_schema_skew" error code: the
  // beads database is behind the bd binary's schema and is remote-backed, so
  // bd refuses to auto-migrate it. Drives a distinct actionable error card
  // (see the render error region below) instead of the plain error text.
  const [schemaSkew, setSchemaSkew] = useState(null);
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
  const [showTypeMenu, setShowTypeMenu] = useState(false);
  const typeMenuRef = useRef(null);

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

  // Close the type-filter menu on outside click while it is open.
  useEffect(() => {
    if (!showTypeMenu) return undefined;
    const onDocClick = (e) => {
      if (typeMenuRef.current && !typeMenuRef.current.contains(e.target)) {
        setShowTypeMenu(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [showTypeMenu]);

  // Per-issue right-click context menu. `contextMenu` holds the click position
  // and the issue it targets; `menuPrompts` are the `menus: beadsIssues` prompts shown
  // in the "Prompts" submenu. Actions are not wired to behavior yet.
  const [contextMenu, setContextMenu] = useState(null);
  const [menuPrompts, setMenuPrompts] = useState([]);

  // Schema-skew migration confirm-and-run dialog visibility. The dialog itself
  // is a small component (SchemaSkewDialog) that reuses ConfirmDialog and drives
  // POST /api/beads/migrate on confirm; the underlying schemaSkew state (dbPath,
  // hint, options, message) is populated by fetchList when the backend returns
  // HTTP 409 code=beads_schema_skew.
  const [showMigrateDialog, setShowMigrateDialog] = useState(false);

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
  // Saved argument maps (name→string) for the configured pull/push/sync prompts.
  const [pullPromptArgs, setPullPromptArgs] = useState({});
  const [pushPromptArgs, setPushPromptArgs] = useState({});
  const [syncPromptArgs, setSyncPromptArgs] = useState({});

  // List-level "Prompts" dropdown state (footer toolbar). These are the
  // `menus: beadsList` prompts that operate on the whole issue list rather than
  // a single issue. Loaded lazily the first time the dropdown is opened.
  // ContextMenu anchor for the list-level prompts button; null = closed. The
  // menu now uses buildPromptGroupMenuItems + ContextMenu (like the detail-panel
  // kebab), so grouped submenus and per-prompt loop toggles are handled by
  // ContextMenu itself — no local open flag or loop-override state needed.
  const [listPromptsAnchor, setListPromptsAnchor] = useState(null);
  const [listPrompts, setListPrompts] = useState([]);
  const [listPromptsLoading, setListPromptsLoading] = useState(false);

  // Shortcut buttons configured for this folder's tasksList section.
  const [shortcuts, setShortcuts] = useState([]);
  // Map from prompt name → prompt object, built once shortcuts + prompts are loaded.
  const [shortcutPromptMap, setShortcutPromptMap] = useState(new Map());
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
        if (data.code === "beads_schema_skew") {
          setSchemaSkew({
            message: data.error,
            dbPath: (data.details && data.details.db_path) || "",
            hint: (data.details && data.details.hint) || "",
            options:
              (data.details && Array.isArray(data.details.options)
                ? data.details.options
                : []) || [],
          });
        } else {
          setSchemaSkew(null);
        }
        setError(data.error || data.message || "Failed to load issues");
        setIssues([]);
      } else {
        setSchemaSkew(null);
        setIssues(Array.isArray(data) ? data : []);
      }
    } catch (err) {
      setSchemaSkew(null);
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
          setPullPromptArgs((data && data.pull_prompt_args) || {});
          setPushPromptArgs((data && data.push_prompt_args) || {});
          setSyncPromptArgs((data && data.sync_prompt_args) || {});
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
        // Merge global (settings.json) + folder (folders.json) shortcuts for the
        // tasksList section. Global buttons come first; folder buttons that
        // duplicate a global prompt are dropped.
        const [folderRes, globalRes] = await Promise.all([
          authFetch(endpoints.folders.shortcuts({ working_dir: workingDir })),
          authFetch(endpoints.global.shortcuts()).catch(() => null),
        ]);
        const folderData = await folderRes.json().catch(() => ({}));
        const globalData = globalRes
          ? await globalRes.json().catch(() => ({}))
          : {};
        const globalList = globalData?.sections?.tasksList || [];
        const folderList = folderData?.sections?.tasksList || [];
        const globalNames = new Set(globalList.map((s) => s.prompt));
        const list = [
          ...globalList,
          ...folderList.filter((s) => !globalNames.has(s.prompt)),
        ];
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
    // Global shortcuts changes affect every folder, so always refresh.
    const globalHandler = () => loadShortcuts();
    window.addEventListener("mitto:folder_shortcuts_updated", handler);
    window.addEventListener("mitto:global_shortcuts_updated", globalHandler);
    return () => {
      window.removeEventListener("mitto:folder_shortcuts_updated", handler);
      window.removeEventListener(
        "mitto:global_shortcuts_updated",
        globalHandler,
      );
    };
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

  // Open the list-level prompts ContextMenu anchored to the toolbar button,
  // lazily loading the `menus: beadsList` prompts for this workspace each time
  // it is opened (grouped into submenus by buildPromptGroupMenuItems below).
  const openListPromptsMenu = useCallback(
    (e) => {
      e.preventDefault();
      e.stopPropagation();
      const rect = e.currentTarget.getBoundingClientRect();
      setListPromptsAnchor({ x: rect.left, y: rect.bottom });
      if (onFetchBeadsListPrompts && workingDir) {
        setListPromptsLoading(true);
        onFetchBeadsListPrompts(workingDir)
          .then((list) => setListPrompts(list || []))
          .finally(() => setListPromptsLoading(false));
      }
    },
    [onFetchBeadsListPrompts, workingDir],
  );

  // Run a list-level prompt in a new conversation (no per-issue context).
  const handleRunListPrompt = useCallback(
    (prompt, opts) => {
      setListPromptsAnchor(null);
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

  // ---- Top toolbar (moved from the former bottom footer) --------------------
  // The list-level actions now live in a portable Toolbar "pill" at the top of
  // the view, vertically aligned with the sidebar toolbar (both sit right below
  // their p-4 header, in a px-3 wrapper with no top padding). Order: new issue,
  // list-prompts dropdown, refresh, clean up, | upstream sync group |,
  // | shortcuts |, spacer, issue count, tasks-config. Conditional groups are
  // set off by thin dividers.
  const upstreamLabel = UPSTREAM_LABELS[upstream] || upstream;
  const busySpinner = html`<span
    class="loading loading-spinner w-4 h-4"
  ></span>`;

  const cleanupTip =
    cleaningUp && cleanupProgress && cleanupProgress.total > 0
      ? `Removing ${cleanupProgress.deleted}/${cleanupProgress.total}…`
      : closedCount === 0
        ? "No closed issues to clean up"
        : `Clean up ${closedCount} closed issue${closedCount === 1 ? "" : "s"}`;
  const cleanupAria =
    closedCount === 0
      ? "No closed issues to clean up"
      : `Clean up ${closedCount} closed issue${closedCount === 1 ? "" : "s"}`;

  // Upstream pull/push/sync buttons. Two flavours: "prompts" runs configured
  // prompts via onLaunchPrompt; a real backend (jira/github/…) calls handleSync
  // and shows an inline spinner on the in-flight action.
  const upstreamItems =
    upstream === "prompts"
      ? [
          {
            kind: "button",
            testId: "beads-pull-btn",
            icon: html`<${ArrowDownIcon} className="w-4 h-4" />`,
            tip: pullPromptName
              ? `Pull: run "${pullPromptName}"`
              : "No pull prompt configured",
            ariaLabel: pullPromptName
              ? `Pull: run "${pullPromptName}"`
              : "No pull prompt configured",
            disabled: !pullPromptName || !onLaunchPrompt,
            onClick: () =>
              pullPromptName &&
              onLaunchPrompt &&
              onLaunchPrompt("pull", pullPromptName, pullPromptArgs),
          },
          {
            kind: "button",
            testId: "beads-push-btn",
            icon: html`<${ArrowUpIcon} className="w-4 h-4" />`,
            tip: pushPromptName
              ? `Push: run "${pushPromptName}"`
              : "No push prompt configured",
            ariaLabel: pushPromptName
              ? `Push: run "${pushPromptName}"`
              : "No push prompt configured",
            disabled: !pushPromptName || !onLaunchPrompt,
            onClick: () =>
              pushPromptName &&
              onLaunchPrompt &&
              onLaunchPrompt("push", pushPromptName, pushPromptArgs),
          },
          {
            kind: "button",
            testId: "beads-sync-btn",
            icon: html`<${SyncIcon} className="w-4 h-4" />`,
            tip: syncPromptName
              ? `Sync: run "${syncPromptName}"`
              : "No sync prompt configured",
            ariaLabel: syncPromptName
              ? `Sync: run "${syncPromptName}"`
              : "No sync prompt configured",
            disabled: !syncPromptName || !onLaunchPrompt,
            onClick: () =>
              syncPromptName &&
              onLaunchPrompt &&
              onLaunchPrompt("sync", syncPromptName, syncPromptArgs),
          },
        ]
      : [
          {
            kind: "button",
            testId: "beads-pull-btn",
            icon:
              syncAction === "pull"
                ? busySpinner
                : html`<${ArrowDownIcon} className="w-4 h-4" />`,
            tip: `Pull from ${upstreamLabel}`,
            ariaLabel: `Pull from ${upstreamLabel}`,
            disabled: !!syncAction,
            onClick: () => !syncAction && handleSync("pull"),
          },
          {
            kind: "button",
            testId: "beads-push-btn",
            icon:
              syncAction === "push"
                ? busySpinner
                : html`<${ArrowUpIcon} className="w-4 h-4" />`,
            tip: `Push to ${upstreamLabel}`,
            ariaLabel: `Push to ${upstreamLabel}`,
            disabled: !!syncAction,
            onClick: () => !syncAction && handleSync("push"),
          },
          {
            kind: "button",
            testId: "beads-sync-btn",
            icon:
              syncAction === "sync"
                ? busySpinner
                : html`<${SyncIcon} className="w-4 h-4" />`,
            tip: `Sync with ${upstreamLabel} (pull then push)`,
            ariaLabel: `Sync with ${upstreamLabel} (pull then push)`,
            disabled: !!syncAction,
            onClick: () => !syncAction && handleSync("sync"),
          },
        ];

  // Per-folder shortcut buttons (tasksList section). A missing linked prompt is
  // shown greyed/disabled, mirroring the former footer behaviour.
  const shortcutItems = shortcuts.map((sc, i) => {
    const prompt = shortcutPromptMap.get(sc.prompt);
    const found = !!prompt;
    const Icon = getPromptIconOrDefault(sc.icon || (prompt && prompt.icon));
    return {
      kind: "button",
      testId: `beads-shortcut-btn-${i}`,
      // On phone-width screens only the first shortcut is shown; the rest are
      // hidden (see .mitto-shortcut-extra in styles-v2.css) to avoid overflow.
      className: i > 0 ? "mitto-shortcut-extra" : undefined,
      icon: html`<${Icon} className="w-4 h-4" />`,
      tip: found ? sc.prompt : `Prompt "${sc.prompt}" not found`,
      ariaLabel: found
        ? `Run "${sc.prompt}"`
        : `Prompt "${sc.prompt}" not found`,
      disabled: !found,
      onClick: () => found && handleRunListPrompt(prompt),
    };
  });

  // Group the beadsList prompts by their `group` into per-group submenus,
  // identical to the conversation menu and the detail-panel kebab. ContextMenu
  // renders the hover flyouts and per-prompt loop toggles from these items.
  const listPromptGroupItems = listPromptsLoading
    ? [{ label: "Loading…", disabled: true }]
    : (() => {
        const groups = buildPromptGroupMenuItems(
          listPrompts,
          handleRunListPrompt,
          html`<${PlusIcon} />`,
        );
        return groups.length === 0
          ? [{ label: "No task prompts", disabled: true }]
          : groups;
      })();

  const listToolbarItems = [
    {
      kind: "button",
      testId: "beads-new-issue-btn",
      icon: html`<${PlusIcon} className="w-4 h-4" />`,
      tip: "New issue",
      ariaLabel: "New issue",
      onClick: openCreate,
    },
    {
      kind: "button",
      testId: "beads-list-prompts-btn",
      icon: html`<${LightningIcon} className="w-4 h-4" />`,
      tip: "Run a prompt over the issue list in a new conversation",
      ariaLabel: "Run a prompt over the issue list in a new conversation",
      onClick: openListPromptsMenu,
    },
    {
      kind: "button",
      testId: "beads-refresh-btn",
      icon: html`<${RefreshIcon} className="w-4 h-4" />`,
      tip: "Refresh",
      ariaLabel: "Refresh",
      onClick: fetchList,
    },
    {
      kind: "button",
      testId: "beads-cleanup-btn",
      icon: html`<${BroomIcon} className="w-4 h-4 group-hover:text-red-400" />`,
      tip: cleanupTip,
      ariaLabel: cleanupAria,
      className: "group",
      disabled: closedCount === 0 || cleaningUp,
      onClick: () => {
        if (closedCount === 0 || cleaningUp) return;
        setShowCleanupConfirm(true);
      },
    },
    ...(upstream && upstream !== "none"
      ? [{ kind: "separator" }, ...upstreamItems]
      : []),
    ...(shortcuts.length > 0 ? [{ kind: "separator" }, ...shortcutItems] : []),
  ];

  return html`
    <div class="relative flex h-full overflow-hidden">
    <div class="flex flex-col flex-1 min-w-0 overflow-hidden">
      <div class="flex items-center gap-2 p-4 shrink-0">
        <button
          onClick=${() => onShowSidebar && onShowSidebar()}
          class="btn btn-ghost btn-square btn-sm md:hidden shrink-0 inline-flex tooltip tooltip-bottom"
          data-tip="Show conversations"
          aria-label="Show conversations"
        >
          <${MenuIcon} className="w-6 h-6" />
        </button>
        <span class="font-semibold text-2xl flex-1">Tasks — ${workspaceLabel}</span>
      </div>

      <!-- List-level actions rendered via the portable Toolbar component
           (components/Toolbar.js) as a floating "pill", vertically aligned with
           the sidebar toolbar. The prompts button opens a ContextMenu, which
           handles its own outside-click / Escape dismissal. -->
      <div
        class="px-3 pb-2 shrink-0"
        data-testid="beads-actions-toolbar"
      >
        <${Toolbar}
          variant="block"
          surface="bg-mitto-surface-3"
          ariaLabel="Task list actions"
          items=${listToolbarItems}
        />
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
        <details
          class="dropdown shrink-0"
          ref=${typeMenuRef}
          open=${showTypeMenu}
          onToggle=${(e) => {
            const open = e.currentTarget.open;
            if (open !== showTypeMenu) setShowTypeMenu(open);
          }}
        >
          <summary
            class="btn btn-xs btn-ghost gap-1 list-none w-28"
            data-testid="beads-type-filter-button"
            aria-label="Filter by type"
          >
            <span class="flex-1 truncate">
              ${typeFilter === "all" ? "All types" : typeFilter}
            </span>
            <${ChevronDownIcon} className="w-3 h-3 shrink-0 opacity-60" />
          </summary>
          <ul
            class="dropdown-content menu menu-sm bg-base-200 rounded-box shadow-xl z-10 mt-1 w-44"
            data-testid="beads-type-filter-menu"
          >
            <li class="menu-title text-xs">Type</li>
            <li>
              <button
                type="button"
                class=${typeFilter === "all" ? "menu-active" : ""}
                onClick=${() => {
                  setTypeFilter("all");
                  setShowTypeMenu(false);
                }}
              >
                <span class="w-4 h-4 shrink-0">
                  ${
                    typeFilter === "all"
                      ? html`<${CheckIcon} className="w-4 h-4" />`
                      : null
                  }
                </span>
                <span class="flex-1">All types</span>
              </button>
            </li>
            ${allTypes.map(
              (t) => html`
                <li key=${t}>
                  <button
                    type="button"
                    class=${typeFilter === t ? "menu-active" : ""}
                    onClick=${() => {
                      setTypeFilter(t);
                      setShowTypeMenu(false);
                    }}
                  >
                    <span class="w-4 h-4 shrink-0">
                      ${typeFilter === t
                        ? html`<${CheckIcon} className="w-4 h-4" />`
                        : null}
                    </span>
                    <span class="flex-1">${t}</span>
                  </button>
                </li>
              `,
            )}
          </ul>
        </details>
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
          (schemaSkew
            ? html`
                <div
                  class="flex flex-col items-center justify-center gap-2 py-8 px-4 text-center"
                >
                  <div class="text-amber-400 text-sm font-medium">
                    ${schemaSkew.message &&
                    schemaSkew.dbPath &&
                    schemaSkew.message.includes(schemaSkew.dbPath)
                      ? schemaSkew.message
                      : schemaSkew.dbPath
                        ? `The beads database at ${schemaSkew.dbPath} needs migration`
                        : "Beads schema needs migration"}
                  </div>
                  ${schemaSkew.dbPath &&
                  html`<div
                    class="text-mitto-text-secondary text-xs font-mono break-all"
                  >
                    ${schemaSkew.dbPath}
                  </div>`}
                  <div class="text-mitto-text-secondary text-xs max-w-md">
                    ${schemaSkew.hint}
                  </div>
                  <button
                    onClick=${() => setShowMigrateDialog(true)}
                    class="btn btn-primary btn-sm mt-2"
                    data-testid="beads-run-migration-btn"
                  >
                    Run migration…
                  </button>
                </div>
              `
            : html`
                <div
                  class="flex items-center justify-center h-24 text-red-400 text-sm px-4"
                >
                  ${error}
                </div>
              `)
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
                Create a new issue by pressing the "+" button above.
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

      <!-- Bottom status bar: issue count + tasks-configuration gear. The
           action buttons moved to the top Toolbar pill; this area keeps only
           the statistics and the config affordance. -->
      <div
        class="flex items-center gap-1 p-4 border-t border-mitto-border shrink-0"
      >
        <span class="text-xs text-mitto-text-secondary ml-auto"
          >${filtered.length} issue${filtered.length === 1 ? "" : "s"}</span
        >
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
    ${
      listPromptsAnchor &&
      html`
        <${ContextMenu}
          x=${listPromptsAnchor.x}
          y=${listPromptsAnchor.y}
          items=${listPromptGroupItems}
          onClose=${() => setListPromptsAnchor(null)}
        />
      `
    }

    <${SchemaSkewDialog}
      isOpen=${showMigrateDialog && !!schemaSkew}
      dbPath=${schemaSkew ? schemaSkew.dbPath : ""}
      hint=${schemaSkew ? schemaSkew.hint : ""}
      options=${schemaSkew ? schemaSkew.options : []}
      workingDir=${workingDir}
      showToast=${showToast}
      onSuccess=${() => {
        showToast && showToast("Migration completed", "success");
        setShowMigrateDialog(false);
        setSchemaSkew(null);
        fetchList();
      }}
      onCancel=${() => setShowMigrateDialog(false)}
    />

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
