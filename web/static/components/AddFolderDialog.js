// Mitto Web Interface - Add Folder Dialog
//
// Presents two ways to bring a folder into the sidebar:
//  1. Pin an existing configured workspace that is currently hidden
//     (no active/stored session AND folders.json `pinned !== true`). Calls
//     onPinExisting(workingDir); parent handles PUT /api/folders/pin and toast.
//  2. Create a net-new workspace — delegates to WorkspacesDialog via
//     onCreateNew(); parent closes this dialog and opens WorkspacesDialog.
//
// Reuses the shared Modal component (ESC / backdrop / focus trap handled).

const { html, Fragment, useState, useRef, useEffect } = window.preact;

import { Modal } from "./Modal.js";
import { Portal } from "./ContextMenu.js";
import { tildifyPath } from "../utils/paths.js";

// Pure helper — pick the display label for a hidden workspace entry. Prefers
// the friendly `name` from folders.json (merged onto workspace records via
// ApplyFolderDefaults) and falls back to a tildified `working_dir` for the
// display-only fallback (the raw `working_dir` remains the underlying key).
// Exported for testing.
export function pickAddFolderLabel(ws) {
  if (!ws) return "";
  return (
    (ws.name && String(ws.name).trim()) || tildifyPath(ws.working_dir) || ""
  );
}

export function AddFolderDialog({
  isOpen,
  onClose,
  hiddenWorkspaces = [],
  onPinExisting,
  onCreateNew,
}) {
  const hasHidden =
    Array.isArray(hiddenWorkspaces) && hiddenWorkspaces.length > 0;
  // Anchor position for the "more folders" menu. Rendered through a body-level
  // Portal so it escapes the modal's overflow-hidden / overflow-y-auto clip
  // boundaries and truly floats over the modal, with our own compact sizing
  // (narrower + shorter than ContextMenu's defaults).
  const [moreMenu, _setMoreMenu] = useState(null);
  const moreBtnRef = useRef(null);
  const menuRef = useRef(null);
  // Ref mirror of moreMenu so the mount-once listeners below see fresh state
  // without needing to re-attach on every open/close (which was racy with
  // synthesized Escape presses from Playwright and, in principle, fast users).
  // Updated EAGERLY (in the same call path as setState) so document listeners
  // fired on the very next event tick always observe the latest value —
  // effect-based sync would run asynchronously after commit and can miss a
  // synthesized Escape that follows the opening click immediately.
  const moreMenuRef = useRef(null);
  const setMoreMenu = (v) => {
    moreMenuRef.current = v;
    _setMoreMenu(v);
  };

  // Close the more-menu on Escape or outside click. Registered ONCE on mount
  // and no-op when the dropdown is closed — this avoids the effect-attach race
  // that previously required a setTimeout guard for the opening click.
  useEffect(() => {
    const onKey = (e) => {
      if (!moreMenuRef.current) return;
      if (e.key === "Escape") {
        e.stopPropagation();
        setMoreMenu(null);
      }
    };
    const onDown = (e) => {
      if (!moreMenuRef.current) return;
      // Ignore clicks on the toggle button itself — it owns opening the menu
      // and would otherwise be treated as an outside click on the same tick.
      if (moreBtnRef.current && moreBtnRef.current.contains(e.target)) return;
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        setMoreMenu(null);
      }
    };
    // Capture so we run before the Modal's own Escape handler (window-level).
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("mousedown", onDown);
    };
  }, []); // eslint-disable-line

  const handlePickExisting = async (workingDir) => {
    if (!workingDir || !onPinExisting) return;
    try {
      await onPinExisting(workingDir);
    } finally {
      onClose && onClose();
    }
  };

  // Compute a viewport-aware menu box: prefer opening below the button, but
  // flip above when there's substantially more room up-top. Clamp maxHeight so
  // the menu never spills past the window edge — required because the Portal
  // renders at document root, outside any modal scroll container.
  const computeMoreMenuBox = () => {
    const btn = moreBtnRef.current;
    if (!btn) return null;
    const rect = btn.getBoundingClientRect();
    const gap = 4;
    const margin = 8; // keep the menu off the very edge of the viewport
    const vh = window.innerHeight || document.documentElement.clientHeight;
    const spaceBelow = Math.max(0, vh - rect.bottom - gap - margin);
    const spaceAbove = Math.max(0, rect.top - gap - margin);
    const openUp = spaceBelow < 200 && spaceAbove > spaceBelow;
    const maxHeight = Math.max(120, Math.min(384, openUp ? spaceAbove : spaceBelow));
    const top = openUp ? Math.max(margin, rect.top - gap - maxHeight) : rect.bottom + gap;
    return { left: rect.left, top, width: rect.width, maxHeight };
  };

  const openMoreMenu = () => {
    const box = computeMoreMenuBox();
    if (box) setMoreMenu(box);
  };

  // Recompute on resize / scroll while the menu is open so it stays inside the
  // viewport when the window changes size or the modal body scrolls.
  useEffect(() => {
    if (!moreMenu) return;
    const recompute = () => {
      const box = computeMoreMenuBox();
      if (box) setMoreMenu(box);
    };
    window.addEventListener("resize", recompute);
    window.addEventListener("scroll", recompute, true);
    return () => {
      window.removeEventListener("resize", recompute);
      window.removeEventListener("scroll", recompute, true);
    };
  }, [moreMenu !== null]); // eslint-disable-line

  const footer = html`
    <button
      type="button"
      class="btn btn-ghost"
      onClick=${onClose}
      data-testid="add-folder-cancel-btn"
    >
      Cancel
    </button>
  `;

  const topEntries = hasHidden ? hiddenWorkspaces.slice(0, 3) : [];
  // Dropdown entries are sorted alphabetically by working_dir so the "long
  // tail" is easy to scan — the top-3 block above stays in the upstream
  // (MRU-preserving) order handed to the component by app.js.
  const moreEntries = hasHidden
    ? hiddenWorkspaces.slice(3).slice().sort((a, b) => {
        const av = String(a.working_dir || "").toLowerCase();
        const bv = String(b.working_dir || "").toLowerCase();
        return av < bv ? -1 : av > bv ? 1 : 0;
      })
    : [];

  const renderItem = (ws) => html`
    <li key=${ws.working_dir}>
      <button
        type="button"
        class="flex flex-col items-start gap-0.5 w-full text-left"
        onClick=${() => handlePickExisting(ws.working_dir)}
        data-testid=${`add-folder-pick-${ws.working_dir}`}
      >
        <span class="font-semibold truncate w-full"
          >${pickAddFolderLabel(ws)}</span
        >
        <span class="text-xs text-mitto-text-muted truncate w-full"
          >${tildifyPath(ws.working_dir)}</span
        >
      </button>
    </li>
  `;

  return html`
    <${Fragment}>
      <${Modal}
        isOpen=${isOpen}
        onClose=${onClose}
        title="Add folder to sidebar"
        testid="add-folder-dialog"
        closeTestid="add-folder-close-btn"
        backdropTestid="add-folder-backdrop"
        footer=${footer}
      >
        ${
          hasHidden
            ? html`
                <p class="text-sm text-mitto-text-muted mb-3">
                  Pick a configured workspace to show it in the sidebar:
                </p>
                <ul
                  class="menu bg-mitto-surface-2 rounded-box p-2"
                  data-testid="add-folder-hidden-list"
                >
                  ${topEntries.map(renderItem)}
                </ul>
                ${moreEntries.length > 0 &&
                html`
                  <button
                    type="button"
                    ref=${moreBtnRef}
                    class="btn btn-outline btn-sm w-full mt-2 justify-between"
                    onClick=${openMoreMenu}
                    data-testid="add-folder-more-toggle"
                  >
                    Show ${moreEntries.length} more folders…
                  </button>
                `}
              `
            : html`
                <p class="text-sm italic text-mitto-text-muted">
                  All configured folders are already visible in the sidebar.
                </p>
              `
        }
        <div class="divider my-2 text-xs">or</div>
        <button
          type="button"
          class="btn btn-outline w-full"
          onClick=${() => onCreateNew && onCreateNew()}
          data-testid="add-folder-create-new-btn"
        >
          Create a new workspace…
        </button>
      </${Modal}>
      ${
        moreMenu &&
        html`
          <${Portal}>
            <ul
              ref=${menuRef}
              class="menu menu-sm bg-base-200 rounded-box shadow-xl fixed overflow-y-auto p-1"
              style="left: ${moreMenu.left}px; top: ${moreMenu.top}px; width: ${moreMenu.width}px; max-height: ${moreMenu.maxHeight}px; z-index: 9999; flex-flow: column nowrap;"
              data-testid="add-folder-more-list"
            >
              ${moreEntries.map(
                (ws) => html`
                  <li key=${ws.working_dir}>
                    <button
                      type="button"
                      class="w-full text-left truncate py-1"
                      onClick=${() => {
                        setMoreMenu(null);
                        handlePickExisting(ws.working_dir);
                      }}
                      data-testid=${`add-folder-pick-${ws.working_dir}`}
                      title=${tildifyPath(ws.working_dir)}
                    >
                      <span class="truncate">${tildifyPath(ws.working_dir)}</span>
                    </button>
                  </li>
                `,
              )}
            </ul>
          <//>
        `
      }
    </${Fragment}>
  `;
}
