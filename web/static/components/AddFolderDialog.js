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
  const [moreMenu, setMoreMenu] = useState(null);
  const moreBtnRef = useRef(null);
  const menuRef = useRef(null);

  // Close the more-menu on Escape or outside click.
  useEffect(() => {
    if (!moreMenu) return undefined;
    const onKey = (e) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        setMoreMenu(null);
      }
    };
    const onDown = (e) => {
      if (menuRef.current && !menuRef.current.contains(e.target)) {
        setMoreMenu(null);
      }
    };
    // Delay outside-click so the opening click on the toggle doesn't dismiss.
    const t = setTimeout(
      () => document.addEventListener("mousedown", onDown),
      10,
    );
    // Capture so we run before the Modal's own Escape handler (window-level).
    document.addEventListener("keydown", onKey, true);
    return () => {
      clearTimeout(t);
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey, true);
    };
  }, [moreMenu]);

  const handlePickExisting = async (workingDir) => {
    if (!workingDir || !onPinExisting) return;
    try {
      await onPinExisting(workingDir);
    } finally {
      onClose && onClose();
    }
  };

  const openMoreMenu = () => {
    const btn = moreBtnRef.current;
    if (!btn) return;
    const rect = btn.getBoundingClientRect();
    setMoreMenu({ left: rect.left, top: rect.bottom + 4, width: rect.width });
  };

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
  const moreEntries = hasHidden ? hiddenWorkspaces.slice(3) : [];

  const renderItem = (ws) => html`
    <li key=${ws.working_dir}>
      <button
        type="button"
        class="flex flex-col items-start gap-0.5 w-full text-left"
        onClick=${() => handlePickExisting(ws.working_dir)}
        data-testid=${`add-folder-pick-${ws.working_dir}`}
      >
        <span class="font-semibold truncate w-full"
          >${ws.name || ws.working_dir}</span
        >
        <span class="text-xs text-mitto-text-muted truncate w-full"
          >${ws.working_dir}</span
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
              class="menu menu-sm bg-base-200 rounded-box shadow-xl fixed max-h-80 overflow-y-auto flex-nowrap p-1"
              style="left: ${moreMenu.left}px; top: ${moreMenu.top}px; width: ${moreMenu.width}px; z-index: 9999;"
              data-testid="add-folder-more-list"
            >
              ${moreEntries.map(
                (ws) => html`
                  <li key=${ws.working_dir}>
                    <button
                      type="button"
                      class="truncate w-full text-left"
                      onClick=${() => {
                        setMoreMenu(null);
                        handlePickExisting(ws.working_dir);
                      }}
                      data-testid=${`add-folder-pick-${ws.working_dir}`}
                      title=${ws.working_dir}
                    >
                      <span class="truncate">${ws.name || ws.working_dir}</span>
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
