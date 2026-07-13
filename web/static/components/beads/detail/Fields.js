// Leaf field sub-components extracted from BeadsDetailPanel in
// components/BeadsView.js (mitto-90f.3 PR-7b). These are prop-drilled: every
// value the field reads or the setter it calls is passed as an explicit prop —
// no closure capture on the parent panel's scope. Behaviour, markup and class
// names are preserved byte-for-byte from the original in-panel definitions so
// this move is behaviorally invisible.
//
// This module depends on the frontend runtime (window.preact + htm) so it
// lives under components/, not utils/.

const { html } = window.preact;

import { typeBadge, priorityBadge } from "../Badges.js";
import {
  CheckIcon,
  BoldIcon,
  ItalicIcon,
  StrikethroughIcon,
  InlineCodeIcon,
  CodeBlockIcon,
  LinkIcon,
  ListIcon,
  NumberedListIcon,
  HeadingIcon,
  QuoteIcon,
} from "../../Icons.js";
import { CodeEditorField } from "../../CodeEditorField.js";
import { commentBody, handleBeadsContentClick } from "../CommentBody.js";
import { ISSUE_TYPES, PRIORITY_LABELS } from "../../../utils/beads.js";

// daisyUI's .input/.select/.textarea set their corner radius via the logical
// longhand border-start-start-radius:var(--radius-field), which a Tailwind
// `rounded-*` shorthand utility does NOT override. Some themes set
// --radius-field as high as 2rem, turning these edit fields into pills. Pin
// --radius-field so edit-mode fields keep the same subtle 0.25rem corners as
// the panel's description/notes boxes, regardless of theme.
export const inputClass = "input input-sm w-full [--radius-field:0.25rem]";
export const selectClass = "select select-sm w-full [--radius-field:0.25rem]";
export const textareaClass =
  "textarea textarea-sm w-full [--radius-field:0.25rem]";
// Block label with a small gap so it doesn't sit flush against its field.
export const labelClass = "label block mb-1";

export function TitleField({
  mode,
  title,
  setTitle,
  submitting,
  viewDraft,
  setViewDraft,
  editingTitle,
  setEditingTitle,
  titleRef,
  savingView,
  handleTitleKeyDown,
  startEditTitle,
}) {
  if (mode === "create") {
    return html` <input
      id="new-issue-title"
      type="text"
      class=${inputClass}
      placeholder="Issue title (optional — auto-generated from description)"
      value=${title}
      onInput=${(e) => setTitle(e.target.value)}
      disabled=${submitting}
    />`;
  }
  return editingTitle
    ? html` <input
        ref=${titleRef}
        type="text"
        class="${inputClass} font-semibold text-base"
        value=${viewDraft.title}
        onInput=${(e) =>
          setViewDraft((p) => ({ ...p, title: e.target.value }))}
        onBlur=${() => setEditingTitle(false)}
        onKeyDown=${handleTitleKeyDown}
        disabled=${savingView}
      />`
    : html` <h2
        class="font-semibold text-base text-mitto-text wrap-break-word cursor-text rounded px-1 -mx-1 hover:bg-mitto-input-box transition-colors block tooltip tooltip-bottom"
        onClick=${startEditTitle}
        data-tip="Click to edit"
      >
        ${viewDraft.title}
      </h2>`;
}

export function TypeField({
  mode,
  type,
  setType,
  submitting,
  viewDraft,
  setViewDraft,
  editingType,
  setEditingType,
  typeRef,
}) {
  return mode === "create"
    ? html` <select
        id="new-issue-type"
        class=${selectClass}
        value=${type}
        onInput=${(e) => setType(e.target.value)}
        disabled=${submitting}
      >
        ${ISSUE_TYPES.map((t) => html`<option value=${t}>${t}</option>`)}
      </select>`
    : html` <div class="relative" ref=${typeRef}>
        <button
          type="button"
          onClick=${() => setEditingType((o) => !o)}
          class="btn btn-ghost btn-xs inline-flex tooltip tooltip-bottom"
          data-tip="Click to change type"
        >
          ${typeBadge(viewDraft.type)}
        </button>
        ${editingType &&
        html`
          <ul
            class="menu absolute left-0 top-full mt-1 z-10 bg-base-200 rounded-box shadow-xl min-w-[140px]"
          >
            ${ISSUE_TYPES.map((t) => {
              const isCurrent = t === viewDraft.type;
              return html`
                <li key=${t}>
                  <button
                    type="button"
                    onClick=${() => {
                      setViewDraft((p) => ({ ...p, type: t }));
                      setEditingType(false);
                    }}
                  >
                    ${typeBadge(t)}
                    <span class="flex-1">${t}</span>
                    ${isCurrent &&
                    html`<${CheckIcon} className="w-3.5 h-3.5 opacity-70" />`}
                  </button>
                </li>
              `;
            })}
          </ul>
        `}
      </div>`;
}

export function PriorityField({
  mode,
  priority,
  setPriority,
  submitting,
  viewDraft,
  setViewDraft,
}) {
  return mode === "create"
    ? html` <select
        id="new-issue-priority"
        class=${selectClass}
        value=${priority}
        onInput=${(e) => setPriority(Number(e.target.value))}
        disabled=${submitting}
      >
        ${Object.entries(PRIORITY_LABELS).map(
          ([n, label]) => html`<option value=${n}>${label}</option>`,
        )}
      </select>`
    : html` <div class="dropdown">
        <div
          tabindex="0"
          role="button"
          class="btn btn-ghost btn-xs inline-flex tooltip tooltip-bottom"
          data-tip="Click to change priority"
        >
          ${priorityBadge(viewDraft.priority)}
        </div>
        <ul
          tabindex="0"
          class="dropdown-content menu mt-1 z-10 bg-base-200 rounded-box shadow-xl min-w-[140px]"
        >
          ${Object.entries(PRIORITY_LABELS).map(([n, label]) => {
            const num = Number(n);
            const isCurrent = num === viewDraft.priority;
            return html`
              <li key=${n}>
                <button
                  type="button"
                  onClick=${(ev) => {
                    setViewDraft((p) => ({ ...p, priority: num }));
                    ev.currentTarget.blur();
                    if (document.activeElement) document.activeElement.blur();
                  }}
                >
                  ${priorityBadge(num)}
                  <span class="flex-1">${label}</span>
                  ${isCurrent &&
                  html`<${CheckIcon} className="w-3.5 h-3.5 opacity-70" />`}
                </button>
              </li>
            `;
          })}
        </ul>
      </div>`;
}

export function AssigneeField({
  mode,
  createAssignee,
  setCreateAssignee,
  submitting,
  viewDraft,
  setViewDraft,
  editingAssignee,
  setEditingAssignee,
  assigneeRef,
  savingView,
  handleAssigneeKeyDown,
  startEditAssignee,
}) {
  if (mode === "create") {
    return html` <input
      id="new-issue-assignee"
      type="text"
      class=${inputClass}
      placeholder="Assignee"
      value=${createAssignee}
      disabled=${submitting}
      onInput=${(e) => setCreateAssignee(e.target.value)}
    />`;
  }
  return editingAssignee
    ? html` <input
        ref=${assigneeRef}
        type="text"
        class=${inputClass}
        placeholder="Assignee (empty to clear)"
        value=${viewDraft.assignee}
        onInput=${(e) =>
          setViewDraft((p) => ({ ...p, assignee: e.target.value }))}
        onBlur=${() => setEditingAssignee(false)}
        onKeyDown=${handleAssigneeKeyDown}
        disabled=${savingView}
      />`
    : html` <div
        class="text-sm text-mitto-text wrap-break-word cursor-text hover:text-mitto-text-300 transition-colors flex items-center gap-2 tooltip tooltip-bottom"
        onClick=${startEditAssignee}
        data-tip="Click to edit"
      >
        ${viewDraft.assignee
          ? html`<span>${viewDraft.assignee}</span>`
          : html`<span class="text-mitto-text-secondary italic"
              >Unassigned. Click to set.</span
            >`}
      </div>`;
}

export function NotesField({
  mode,
  createNotes,
  setCreateNotes,
  submitting,
  depsLoading,
  viewDraft,
  setViewDraft,
  editingNotes,
  setEditingNotes,
  notesRef,
  notesViewRef,
  notesMinHeight,
  savingView,
  startEditNotes,
  workingDir,
}) {
  if (mode === "create") {
    return html` <textarea
      id="new-issue-notes"
      class="${textareaClass} resize-y min-h-[80px]"
      placeholder="Optional notes"
      disabled=${submitting}
      onInput=${(e) => setCreateNotes(e.target.value)}
      value=${createNotes}
    ></textarea>`;
  }
  if (depsLoading) {
    return html`<div
      class="flex items-center gap-2 text-xs text-mitto-text-secondary"
    >
      <span class="loading loading-spinner w-3 h-3"></span> Loading…
    </div>`;
  }
  return editingNotes
    ? html` <textarea
        ref=${notesRef}
        class="${textareaClass} resize-y"
        rows="4"
        style=${notesMinHeight ? `min-height:${notesMinHeight}px` : null}
        placeholder="Add notes…"
        value=${viewDraft.notes}
        onInput=${(e) =>
          setViewDraft((p) => ({ ...p, notes: e.target.value }))}
        onBlur=${() => setEditingNotes(false)}
        disabled=${savingView}
      ></textarea>`
    : html` <div
        ref=${notesViewRef}
        class="card border-l-2 border-l-amber-500/70 bg-amber-500/10 rounded-r p-2 pl-3 cursor-text hover:border-l-amber-500 transition-colors relative block tooltip tooltip-bottom"
        onClick=${startEditNotes}
        data-tip="Click to edit"
      >
        ${viewDraft.notes && viewDraft.notes.trim()
          ? commentBody(viewDraft.notes, workingDir)
          : html`<span class="text-sm text-mitto-text-secondary italic"
              >No notes. Click to add.</span
            >`}
      </div>`;
}

// DescriptionField is self-contained (includes label + wrapper) to avoid
// Fragment-induced CodeMirror remount cycles. The markdown toolbar
// (formerly `renderDescToolbar` in the parent) is inlined here since it has
// no other caller.
export function DescriptionField({
  mode,
  description,
  setDescription,
  submitting,
  createEditorApiRef,
  editingDesc,
  setEditingDesc,
  viewDraft,
  setViewDraft,
  savingView,
  detailEditorApiRef,
  descMinHeight,
  descViewRef,
  md,
  startEditDesc,
  workingDir,
  improvingDesc,
  improveDescriptionText,
}) {
  const renderToolbar = ({ text, setText, disabled, editorApiRef }) => html`
    <div class="flex flex-wrap items-center gap-1 mb-1">
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Bold"
        aria-label="Bold"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("**", "**", "bold text")}
      >
        <${BoldIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Italic"
        aria-label="Italic"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("*", "*", "italic")}
      >
        <${ItalicIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Strikethrough"
        aria-label="Strikethrough"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("~~", "~~", "strikethrough")}
      >
        <${StrikethroughIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Inline code"
        aria-label="Inline code"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection("\`", "\`", "code")}
      >
        <${InlineCodeIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Code block"
        aria-label="Code block"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() =>
          editorApiRef?.current?.wrapSelection(
            "\n\`\`\`\n",
            "\n\`\`\`\n",
            "code",
          )}
      >
        <${CodeBlockIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Link"
        aria-label="Link"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.insertLink("text", "url")}
      >
        <${LinkIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Bulleted list"
        aria-label="Bulleted list"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines("- ")}
      >
        <${ListIcon} className="w-4 h-4" />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Numbered list"
        aria-label="Numbered list"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines((i) => `${i + 1}. `)}
      >
        <${NumberedListIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Heading"
        aria-label="Heading"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines("## ")}
      >
        <${HeadingIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action tooltip tooltip-bottom"
        disabled=${disabled}
        data-tip="Quote"
        aria-label="Quote"
        onMouseDown=${(e) => e.preventDefault()}
        onClick=${() => editorApiRef?.current?.prefixLines("> ")}
      >
        <${QuoteIcon} />
      </button>
      <button
        type="button"
        class="chat-input-action ${improvingDesc
          ? "improving"
          : ""} ml-auto tooltip tooltip-bottom"
        onClick=${() => improveDescriptionText(text, setText)}
        onMouseDown=${(e) => e.preventDefault()}
        disabled=${disabled || improvingDesc || !text || !text.trim()}
        data-tip="Improve description with AI"
        aria-label="Improve description with AI"
      >
        ${improvingDesc
          ? html`<span class="loading loading-spinner w-4 h-4"></span>`
          : html`
              <svg
                class="w-4 h-4"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z"
                />
              </svg>
            `}
      </button>
    </div>
  `;

  if (mode === "create") {
    return html` <div>
      <label class=${labelClass} for="new-issue-desc"
        >Description <span class="text-red-400">*</span></label
      >
      ${renderToolbar({
        text: description,
        setText: (v) => {
          setDescription(v);
          createEditorApiRef.current?.setValue(v);
        },
        disabled: submitting,
        editorApiRef: createEditorApiRef,
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
        autoFocus=${true}
      />
    </div>`;
  }
  return html` <div>
    <label class=${labelClass}>Description</label>
    ${renderToolbar(
      editingDesc
        ? {
            text: viewDraft.description,
            setText: (v) => {
              setViewDraft((p) => ({ ...p, description: v }));
              detailEditorApiRef.current?.setValue(v);
            },
            disabled: savingView,
            editorApiRef: detailEditorApiRef,
          }
        : { text: "", setText: () => {}, disabled: true },
    )}
    ${editingDesc
      ? html` <${CodeEditorField}
          value=${viewDraft.description}
          onChange=${(v) => setViewDraft((p) => ({ ...p, description: v }))}
          onBlur=${() => setEditingDesc(false)}
          disabled=${savingView}
          darkMode=${false}
          lineNumbers=${false}
          lineWrapping=${true}
          highlightActiveLine=${false}
          className="input-font-target"
          minHeight=${descMinHeight || 0}
          autoFocus=${true}
          editorApiRef=${detailEditorApiRef}
        />`
      : html` <div
          ref=${descViewRef}
          class="card border border-mitto-border rounded p-3 bg-mitto-input-box cursor-text hover:border-mitto-text-secondary transition-colors relative block tooltip tooltip-bottom"
          onClick=${startEditDesc}
          data-tip="Click to edit"
        >
          ${viewDraft.description
            ? md
              ? html`<div
                  class="markdown-content text-mitto-text text-sm max-w-none"
                  onClick=${(e) => handleBeadsContentClick(e, workingDir)}
                  dangerouslySetInnerHTML=${{ __html: md }}
                />`
              : html`<pre
                  class="whitespace-pre-wrap wrap-break-word text-sm text-mitto-text"
                >
${viewDraft.description}</pre
                >`
            : html`<span class="text-sm text-mitto-text-secondary italic"
                >No description. Click to add one.</span
              >`}
        </div>`}
  </div>`;
}
