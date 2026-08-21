// Small panel sub-sections extracted from BeadsDetailPanel in mitto-90f.3 PR-9.
// Batched into one file because each block is only ~30 LOC and structurally
// distinct — a file-per-block would fragment the tree without benefit.
//
// - SubtasksList: renders the "Subtasks (N)" fieldset when a parent issue has
//   children. Returns null when there are no subtasks (gate lives inside so the
//   call-site is uniform with the other section renderers).
// - DetailActionBar: renders the Close/Save button row at the bottom of the
//   panel. Returns null when neither creating nor an existing issue is loaded.

const { html } = window.preact;

import { statusBadge } from "../Badges.js";
import { ChevronLeftIcon, ChevronRightIcon } from "../../Icons.js";

export function SubtasksList({ subtasks, onSelectIssue }) {
  if (!subtasks || subtasks.length === 0) return null;
  return html`<fieldset class="fieldset min-w-0">
    <legend class="fieldset-legend">Subtasks (${subtasks.length})</legend>
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
              <span class="font-mono text-mitto-text-secondary text-xs"
                >${c.id}</span
              >
              <span class="truncate">${c.title}</span>
            </button>
          </li>
        `,
      )}
    </ul>
  </fieldset>`;
}

export function DetailActionBar({
  creating,
  data,
  handleClose,
  submitting,
  handleSave,
  handleViewSave,
  description,
  viewDirty,
  savingView,
  canGoBack,
  canGoForward,
  onGoBack,
  onGoForward,
}) {
  if (!creating && !data) return null;
  const showNav = !creating && (onGoBack || onGoForward);
  return html`<div
    class="flex items-center justify-between gap-3 p-3 border-t border-mitto-border shrink-0"
  >
    ${showNav
      ? html`<div class="flex items-center gap-1">
          <button
            type="button"
            onClick=${onGoBack}
            disabled=${!canGoBack}
            class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top"
            data-tip="Back"
            aria-label="Back"
            data-testid="beads-nav-back"
          >
            <${ChevronLeftIcon} className="w-5 h-5" />
          </button>
          <button
            type="button"
            onClick=${onGoForward}
            disabled=${!canGoForward}
            class="btn btn-ghost btn-square btn-sm inline-flex tooltip tooltip-top"
            data-tip="Forward"
            aria-label="Forward"
            data-testid="beads-nav-forward"
          >
            <${ChevronRightIcon} className="w-5 h-5" />
          </button>
        </div>`
      : html`<div></div>`}
    <div class="flex items-center gap-3">
      <button
        type="button"
        onClick=${handleClose}
        disabled=${creating ? submitting : false}
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
  </div>`;
}
