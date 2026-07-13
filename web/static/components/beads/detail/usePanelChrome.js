// Panel chrome sub-hook, extracted from useBeadsDetailPanel (mitto-90f.7
// PR-15). Owns the panel's shell/chrome behavior:
//
//   - Panel open/close lifecycle: isClosing state, shouldRender gate, fade-out
//     animation timing (150ms) tied to isOpen transitions. On close the effect
//     also clears fullscreen so the next open starts docked.
//   - Fullscreen toggle state (initialFullscreen prop seeds it).
//   - Outside-click detection: document-level mousedown listener that closes
//     the panel when the click lands outside .drawer-dock and outside any
//     modal, while a kebab context menu is not open. Cleanup removes the
//     listener verbatim.
//   - Confirm-close dialog state (confirmDiscard) and the discard flow used by
//     handleClose's dirty check + handleDiscardAndClose.
//   - Kebab/panel context menu state (panelMenu / panelMenuItems) plus the
//     prompt list load in openPanelMenu (calls onFetchPrompts, seeds prompts).
//   - Header action toolbar (headerToolbarItems) mixing built-in actions
//     (Prompts / Close/Reopen / Defer/Undefer / Delete / Fullscreen / Close X)
//     with per-folder beadsIssue shortcut buttons.
//   - Per-folder beadsIssue shortcut loading (loadIssueShortcuts) with
//     stale-fetch cancellation and refresh on mitto:*_shortcuts_updated events.
//   - isMobile UA detection (used elsewhere to drop the desktop backdrop).
//
// Boundary notes:
//   * `viewDirty` and `savingView` come from the composer's view-mode cluster.
//     handleClose consults them (dirty check + save-in-flight defer) and the
//     deferred-close effect fires when savingView clears. The composer must
//     call usePanelChrome AFTER viewDirty is computed — hook order in the
//     composer is preserved by a single late call site.
//   * `data` / `creating` come from the composer (which owns lastIssueRef /
//     lastCreatingRef so they stay sticky during the fade-out).
//   * doClose wraps onClose behind the 150ms fade so the animation completes
//     before the parent unmounts the panel.
//   * The unified `chrome:` bundle shape consumed by BeadsDetailPanelBody
//     (headerToolbarItems / panelMenu / setPanelMenu / panelMenuItems /
//     confirmDiscard / setConfirmDiscard) is re-exposed by the composer from
//     this hook's flat return.

const { html, useState, useEffect, useCallback, useMemo, useRef } =
  window.preact;

import { authFetch, endpoints } from "../../../utils/index.js";
import { buildPromptGroupMenuItems } from "../../ContextMenu.js";
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

export function usePanelChrome({
  isOpen,
  data,
  creating,
  viewDirty,
  savingView,
  initialFullscreen,
  workingDir,
  statusBusy,
  onClose,
  onDelete,
  onToggleStatus,
  onToggleDefer,
  onRunPrompt,
  onFetchPrompts,
}) {
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

  // Prompts loaded for the detail-panel kebab menu.
  const [prompts, setPrompts] = useState([]);
  // ContextMenu anchor for the detail-panel kebab; null = closed.
  const [panelMenu, setPanelMenu] = useState(null);
  // When true, show the "Discard changes?" confirm dialog before closing.
  const [confirmDiscard, setConfirmDiscard] = useState(false);

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

  return {
    shouldRender,
    isClosing,
    fullscreen,
    setFullscreen,
    isMobile,
    panelMenu,
    setPanelMenu,
    panelMenuItems,
    headerToolbarItems,
    confirmDiscard,
    setConfirmDiscard,
    handleClose,
    handleDiscardAndClose,
  };
}
