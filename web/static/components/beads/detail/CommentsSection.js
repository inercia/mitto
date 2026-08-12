// CommentsSection: comments list + add-comment textarea/button rendered inside
// BeadsDetailPanel's view mode. Pure prop-drilled renderer; extracted from
// BeadsView.js in mitto-90f.3 PR-8. Keeps the surrounding <fieldset> so the
// section container stays consistent with the other panel fieldsets and the
// "Comments (N)" legend count travels with the block.

const { html, Fragment } = window.preact;

import { commentBody } from "../CommentBody.js";
import { PlusIcon } from "../../Icons.js";
import { textareaClass } from "./Fields.js";

export function CommentsSection({
  comments,
  depsLoading,
  addingComment,
  commentDraft,
  setCommentDraft,
  savingComment,
  commentRef,
  handleCommentBlur,
  startAddComment,
  workingDir,
}) {
  return html`<fieldset class="fieldset min-w-0">
    <legend class="fieldset-legend">
      Comments${comments.length ? ` (${comments.length})` : ""}
    </legend>
    ${depsLoading
      ? html`
          <div
            class="flex items-center gap-2 text-xs text-mitto-text-secondary"
          >
            <span class="loading loading-spinner w-3 h-3"></span>
            Loading…
          </div>
        `
      : html`
    <${Fragment}>
      ${
        comments.length === 0
          ? html`<div
              class="text-xs text-mitto-text-secondary italic"
            >
              No comments.
            </div>`
          : html`
              <ul class="space-y-2">
                ${[...comments]
                  .sort(
                    (a, b) =>
                      new Date(a.created_at) -
                      new Date(b.created_at),
                  )
                  .map(
                    (cm) => html`
                      <li
                        key=${cm.id}
                        class="border-l-2 border-l-blue-500/70 bg-blue-500/10 rounded-r p-2 pl-3"
                      >
                        <div
                          class="flex items-center justify-between gap-2 mb-1"
                        >
                          <span
                            class="text-xs font-medium text-mitto-text"
                            >${cm.author || "Unknown"}</span
                          >
                          <span
                            class="text-xs text-mitto-text-secondary"
                            title=${cm.created_at}
                            >${cm.created_at
                              ? new Date(
                                  cm.created_at,
                                ).toLocaleString()
                              : ""}</span
                          >
                        </div>
                        ${commentBody(cm.text, workingDir)}
                      </li>
                    `,
                  )}
              </ul>
            `
      }
      ${
        addingComment
          ? html`
              <textarea
                ref=${commentRef}
                class="${textareaClass} text-sm resize-y mt-2"
                rows="3"
                placeholder="Add a comment…"
                value=${commentDraft}
                onInput=${(e) => setCommentDraft(e.target.value)}
                onBlur=${handleCommentBlur}
                disabled=${savingComment}
              ></textarea>
            `
          : html`
              <button
                type="button"
                onClick=${startAddComment}
                disabled=${savingComment}
                class="btn btn-ghost btn-xs mt-2 inline-flex tooltip tooltip-bottom"
                data-tip="Add comment"
              >
                ${savingComment
                  ? html`<span
                      class="loading loading-spinner w-3.5 h-3.5"
                    ></span>`
                  : html`<${PlusIcon} className="w-3.5 h-3.5" />`}
                <span>Add comment</span>
              </button>
            `
      }
    </${Fragment}>
  `}
  </fieldset>`;
}
