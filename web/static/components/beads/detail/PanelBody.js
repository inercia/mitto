// Mechanical extraction of BeadsDetailPanel's ~500-LOC JSX return from
// BeadsView.js (mitto-90f.3 PR-10). All hooks, effects, refs, and handlers
// remain in the parent BeadsDetailPanel; this component is a pure passthrough
// renderer for the drawer body.
//
// Related closure captures are grouped into small bundle objects
// (create/view/deps/labels/comments/handlers/chrome) so the prop signature
// stays under the 40-prop cap. Bundles are spread straight into sub-components
// at their use-sites; this file does not unpack them beyond what the child
// components require.
//
// Architectural collapse of the ~90 captures into a single useBeadsDetailPanel
// hook is deferred to mitto-90f.7.

const { html, Fragment } = window.preact;

import { Drawer } from "../../Drawer.js";
import { Toolbar } from "../../Toolbar.js";
import { ContextMenu } from "../../ContextMenu.js";
import { ConfirmDialog } from "../../ConfirmDialog.js";
import { CollapseIcon, ExpandIcon, CopyIcon, PlusIcon, CloseIcon } from "../../Icons.js";
import { copyToClipboard } from "../../../lib.js";
import { statusBadge } from "../Badges.js";
import { labelValue } from "../DetailPanelHelpers.js";
import { PhaseTimeline } from "./PhaseTimeline.js";
import {
  TitleField,
  TypeField,
  PriorityField,
  AssigneeField,
  NotesField,
  DescriptionField,
  DependenciesCreateField,
  DependenciesViewField,
  inputClass,
  labelClass,
} from "./Fields.js";
import { CommentsSection } from "./CommentsSection.js";
import { SubtasksList, DetailActionBar } from "./Sections.js";

export function BeadsDetailPanelBody({
  // Panel chrome / mode flags
  isClosing,
  isMobile,
  fullscreen,
  setFullscreen,
  creating,
  data,
  // mitto-zbfq: loading-mode props. When isLoading is true (and creating/data
  // are absent), a minimal loading/error body is rendered inside the SAME
  // Drawer used for the loaded state so the drawer element itself is not
  // unmounted/remounted across the null→loaded transition.
  isLoading,
  loadingIssueId,
  loadError,
  onRetry,
  createParentId,
  submitting,
  viewDirty,
  savingView,
  // Shared editor state (used by DescriptionField in both create and view)
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
  // Data / navigation
  allIssues,
  subtasks,
  onSelectIssue,
  showToast,
  // Bundles
  create,
  view,
  deps,
  labels,
  comments,
  handlers,
  chrome,
  // In-viewer navigation history (mitto-qluh.2). Only wired when the panel
  // is rendered inside BeadsIssueView; the main BeadsView call site leaves
  // onGoBack/onGoForward undefined so the bottom nav bar is skipped.
  canGoBack,
  canGoForward,
  onGoBack,
  onGoForward,
}) {
  // Loading skeleton path (mitto-zbfq): render the same Drawer shell as the
  // loaded body so opening a beads issue from a conversation link shows a
  // single stable slide-in; only the inner content swaps in-place when the
  // fetch resolves. Kept minimal — no toolbar, no header actions, no
  // action bar — because they all depend on `data`.
  if (isLoading) {
    return html`
      <${Drawer}
        dock
        side="end"
        isClosing=${isClosing}
        onClose=${handlers.handleClose}
        zClass="z-60"
        rootStyle=${
          fullscreen
            ? "--dock-w:100%;--dock-maxw:100%"
            : isMobile
              ? "--dock-w:100%;--dock-maxw:100%"
              : "--dock-w:40rem;--dock-maxw:85%"
        }
        widthClass="w-full"
        panelClass="bg-mitto-sidebar shrink-0 h-full flex flex-col border-l border-mitto-border-1"
      >
        <div class="p-4 border-b border-mitto-border shrink-0">
          <div class="flex items-center gap-2">
            <div class="flex-1 min-w-0">
              <h2
                class="font-mono text-sm text-mitto-text truncate"
                title=${loadingIssueId}
              >
                ${loadingIssueId}
              </h2>
            </div>
            <button
              onClick=${handlers.handleClose}
              class="btn btn-ghost btn-square btn-sm shrink-0 inline-flex tooltip tooltip-bottom"
              data-tip="Close"
              aria-label="Close"
            >
              <${CloseIcon} className="w-5 h-5" />
            </button>
          </div>
        </div>
        <div class="flex-1 overflow-y-auto p-4">
          ${
            loadError
              ? html`
                  <div
                    role="alert"
                    class="alert alert-error alert-soft text-sm"
                  >
                    <span>${loadError}</span>
                    ${
                      onRetry
                        ? html`<button
                            class="btn btn-ghost btn-xs"
                            onClick=${onRetry}
                          >
                            Retry
                          </button>`
                        : null
                    }
                  </div>
                `
              : html`
                  <div
                    class="h-full flex flex-col items-center justify-center gap-2 text-mitto-text-500"
                    data-testid="beads-issue-loading"
                  >
                    <span
                      class="loading loading-spinner w-5 h-5 text-mitto-border-3"
                    ></span>
                    <p class="text-sm">Loading issue…</p>
                  </div>
                `
          }
        </div>
      <//>
    `;
  }

  return html`
    <${Fragment}>
      <${Drawer}
        dock
        side="end"
        isClosing=${isClosing}
        onClose=${handlers.handleClose}
        zClass="z-60"
        rootStyle=${
          fullscreen
            ? "--dock-w:100%;--dock-maxw:100%"
            : isMobile
              ? "--dock-w:100%;--dock-maxw:100%"
              : "--dock-w:40rem;--dock-maxw:85%"
        }
        widthClass="w-full"
        panelClass="bg-mitto-sidebar shrink-0 h-full flex flex-col border-l border-mitto-border-1"
      >
      <div class="p-4 border-b border-mitto-border shrink-0">
        <div class="flex items-center gap-2">
          <div class="flex-1 min-w-0">
            ${
              creating
                ? html`<${Fragment}>
                  <${TitleField}
                    mode="create"
                    title=${create.title}
                    setTitle=${create.setTitle}
                    submitting=${submitting}
                  />
                  ${createParentId ? html`<div class="font-mono text-xs text-mitto-text-secondary">in ${createParentId}</div>` : null}
                </${Fragment}>`
                : html`
                    <h2
                      class="font-semibold text-base text-mitto-text truncate"
                      title=${view.viewDraft.title || data.title || data.id}
                    >
                      ${view.viewDraft.title || data.title || data.id}
                    </h2>
                  `
            }
          </div>
          ${
            creating
              ? html`
                  <button
                    onClick=${() => setFullscreen((f) => !f)}
                    class="btn btn-ghost btn-square btn-sm shrink-0 inline-flex tooltip tooltip-bottom"
                    data-tip=${fullscreen ? "Exit fullscreen" : "Fullscreen"}
                    aria-label=${fullscreen ? "Exit fullscreen" : "Fullscreen"}
                  >
                    ${fullscreen
                      ? html`<${CollapseIcon} className="w-5 h-5" />`
                      : html`<${ExpandIcon} className="w-5 h-5" />`}
                  </button>
                `
              : null
          }
        </div>
        ${
          !creating && data
            ? html`
                <div class="mt-6">
                  <${Toolbar}
                    variant="block"
                    surface="bg-mitto-surface-3"
                    ariaLabel="Issue actions"
                    testId="beads-issue-toolbar"
                    items=${chrome.headerToolbarItems}
                  />
                </div>
              `
            : null
        }
      </div>

      <div class="flex-1 overflow-y-auto p-4 space-y-4">
        ${
          creating
            ? html`
            <${Fragment}>
              <div class="flex flex-wrap gap-2 items-center">
                <span class="${labelClass} shrink-0">Type</span>
                <${TypeField}
                  mode="create"
                  type=${create.type}
                  setType=${create.setType}
                  submitting=${submitting}
                />
                <span class="${labelClass} shrink-0">Priority</span>
                <${PriorityField}
                  mode="create"
                  priority=${create.priority}
                  setPriority=${create.setPriority}
                  submitting=${submitting}
                />
              </div>

              <div class="grid grid-cols-2 gap-3">
                ${
                  createParentId
                    ? html`
                        <div>
                          <label class=${labelClass} for="new-issue-parent"
                            >Parent</label
                          >
                          <input
                            id="new-issue-parent"
                            type="text"
                            class="${inputClass} font-mono"
                            value=${createParentId}
                            readonly
                            aria-readonly="true"
                            title="This issue will be created as a child of ${createParentId}"
                            data-testid="beads-create-parent"
                          />
                        </div>
                      `
                    : null
                }
                <div>
                  <label class=${labelClass} for="new-issue-assignee">Assignee</label>
                  <${AssigneeField}
                    mode="create"
                    createAssignee=${create.createAssignee}
                    setCreateAssignee=${create.setCreateAssignee}
                    submitting=${submitting}
                  />
                </div>
              </div>

              <${DescriptionField}
                mode="create"
                description=${description}
                setDescription=${setDescription}
                submitting=${submitting}
                createEditorApiRef=${createEditorApiRef}
                editingDesc=${view.editingDesc}
                setEditingDesc=${view.setEditingDesc}
                viewDraft=${view.viewDraft}
                setViewDraft=${view.setViewDraft}
                savingView=${savingView}
                detailEditorApiRef=${detailEditorApiRef}
                descMinHeight=${descMinHeight}
                descViewRef=${descViewRef}
                md=${md}
                startEditDesc=${handlers.startEditDesc}
                workingDir=${workingDir}
                improvingDesc=${improvingDesc}
                improveDescriptionText=${improveDescriptionText}
              />

              <fieldset class="fieldset min-w-0">
                <legend class="fieldset-legend">Dependencies</legend>
                <${DependenciesCreateField}
                  allIssues=${allIssues}
                  submitting=${submitting}
                  createDeps=${create.createDeps}
                  setCreateDeps=${create.setCreateDeps}
                  removeCreateDep=${create.removeCreateDep}
                  createNewDepType=${create.createNewDepType}
                  setCreateNewDepType=${create.setCreateNewDepType}
                  createNewDepId=${create.createNewDepId}
                  setCreateNewDepId=${create.setCreateNewDepId}
                  addCreateDep=${create.addCreateDep}
                />
              </fieldset>

              <fieldset class="fieldset min-w-0">
                <legend class="fieldset-legend">Notes</legend>
                <${NotesField}
                  mode="create"
                  createNotes=${create.createNotes}
                  setCreateNotes=${create.setCreateNotes}
                  submitting=${submitting}
                />
              </fieldset>
            </${Fragment}>
          `
            : html`
                <div class="flex flex-wrap gap-2 items-center">
                  <${TypeField}
                    mode="view"
                    viewDraft=${view.viewDraft}
                    setViewDraft=${view.setViewDraft}
                    editingType=${view.editingType}
                    setEditingType=${view.setEditingType}
                    typeRef=${view.typeRef}
                  /> ${statusBadge(data.status)}
                  <${PriorityField}
                    mode="view"
                    viewDraft=${view.viewDraft}
                    setViewDraft=${view.setViewDraft}
                  />
                </div>

                <${PhaseTimeline}
                  issueType=${data.issue_type}
                  labels=${labels.labels}
                  status=${data.status}
                />

                <div class="grid grid-cols-2 gap-3">
                  <div>
                    <label class=${labelClass}>ID</label>
                    <div class="flex items-center gap-1">
                      <span class="font-mono text-sm text-mitto-text"
                        >${data.id}</span
                      >
                      <button
                        type="button"
                        onClick=${async () => {
                          const ok = await copyToClipboard(data.id);
                          showToast &&
                            showToast(
                              ok
                                ? {
                                    style: "success",
                                    title: `Copied ${data.id}`,
                                  }
                                : {
                                    style: "error",
                                    title: "Failed to copy issue ID",
                                  },
                            );
                        }}
                        class="btn btn-ghost btn-xs btn-square inline-flex tooltip tooltip-bottom"
                        data-tip="Copy issue ID ${data.id}"
                        aria-label="Copy issue ID ${data.id}"
                      >
                        <${CopyIcon} className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </div>
                  <div>
                    <label class=${labelClass}>Assignee</label>
                    <${AssigneeField}
                      mode="view"
                      viewDraft=${view.viewDraft}
                      setViewDraft=${view.setViewDraft}
                      editingAssignee=${view.editingAssignee}
                      setEditingAssignee=${view.setEditingAssignee}
                      assigneeRef=${view.assigneeRef}
                      savingView=${savingView}
                      handleAssigneeKeyDown=${handlers.handleAssigneeKeyDown}
                      startEditAssignee=${handlers.startEditAssignee}
                    />
                  </div>
                  ${labelValue("Owner", data.owner)}
                  ${labelValue(
                    "Created",
                    data.created_at &&
                      new Date(data.created_at).toLocaleDateString(),
                  )}
                  ${labelValue(
                    "Updated",
                    data.updated_at &&
                      new Date(data.updated_at).toLocaleDateString(),
                  )}
                  ${data.parent &&
                  labelValue(
                    "Parent",
                    html`
                      <button
                        type="button"
                        onClick=${() =>
                          onSelectIssue &&
                          onSelectIssue(
                            (allIssues || []).find(
                              (i) => i.id === data.parent,
                            ) || { id: data.parent },
                          )}
                        class="font-mono text-mitto-accent-400 hover:text-mitto-accent-300 hover:underline text-left tooltip tooltip-bottom"
                        data-tip=${"Open " + data.parent}
                      >
                        ${data.parent}
                      </button>
                    `,
                  )}
                </div>

                <div>
                  <div class="text-xs text-mitto-text-secondary mb-1">
                    Labels
                  </div>
                  <datalist id="beads-label-options">
                    ${labels.allLabels
                      .filter((l) => !labels.labels.includes(l))
                      .map((l) => html`<option key=${l} value=${l}></option>`)}
                  </datalist>
                  <div class="flex flex-wrap gap-2 items-center">
                    ${labels.labels.length === 0 &&
                    !labels.addingLabel &&
                    html`<span class="text-xs text-mitto-text-secondary italic"
                      >No labels.</span
                    >`}
                    ${labels.labels.map(
                      (l) => html`
                        <span
                          key=${l}
                          class="badge badge-sm font-medium bg-mitto-surface-4 text-mitto-text-strong"
                        >
                          ${l}
                          <button
                            type="button"
                            onClick=${() => {
                              if (labels.labelsBusy) return;
                              labels.mutateLabel("remove", l);
                            }}
                            aria-disabled=${labels.labelsBusy ? "true" : "false"}
                            class="inline-flex items-center opacity-60 hover:opacity-100 hover:text-red-400 cursor-pointer tooltip tooltip-bottom ${labels.labelsBusy
                              ? "opacity-40 pointer-events-none"
                              : ""}"
                            data-tip=${'Remove label "' + l + '"'}
                            aria-label=${'Remove label "' + l + '"'}
                          >
                            <${CloseIcon} className="w-3 h-3" />
                          </button>
                        </span>
                      `,
                    )}
                    ${labels.addingLabel
                      ? html`
                          <div class="join w-52 max-w-full">
                            <input
                              ref=${labels.labelInputRef}
                              type="text"
                              list="beads-label-options"
                              placeholder="add label…"
                              value=${labels.newLabel}
                              disabled=${labels.labelsBusy}
                              onInput=${(e) => labels.setNewLabel(e.target.value)}
                              onKeyDown=${(e) => {
                                if (e.key === "Enter") {
                                  e.preventDefault();
                                  labels.handleAddLabel();
                                } else if (e.key === "Escape") {
                                  e.preventDefault();
                                  labels.setNewLabel("");
                                  labels.setAddingLabel(false);
                                }
                              }}
                              onBlur=${() => {
                                if (!labels.newLabel.trim()) labels.setAddingLabel(false);
                              }}
                              class="input input-xs flex-1 min-w-0 join-item"
                            />
                            <button
                              type="button"
                              onMouseDown=${(e) => e.preventDefault()}
                              onClick=${() => {
                                if (labels.labelsBusy || !labels.newLabel.trim()) return;
                                labels.handleAddLabel();
                              }}
                              aria-disabled=${labels.labelsBusy || !labels.newLabel.trim()
                                ? "true"
                                : "false"}
                              class="btn btn-ghost btn-square btn-xs shrink-0 join-item inline-flex tooltip tooltip-bottom ${labels.labelsBusy ||
                              !labels.newLabel.trim()
                                ? "opacity-40 pointer-events-none"
                                : ""}"
                              data-tip="Add label"
                              aria-label="Add label"
                            >
                              ${labels.labelsBusy
                                ? html`<span
                                    class="loading loading-spinner w-3 h-3"
                                  ></span>`
                                : html`<${PlusIcon} className="w-3 h-3" />`}
                            </button>
                          </div>
                        `
                      : html`
                          <button
                            type="button"
                            onClick=${() => labels.setAddingLabel(true)}
                            class="btn btn-ghost btn-square btn-xs inline-flex tooltip tooltip-bottom"
                            data-tip="Add label"
                            aria-label="Add label"
                          >
                            <${PlusIcon} className="w-3 h-3" />
                          </button>
                        `}
                  </div>
                </div>
                <${TitleField}
                  mode="view"
                  viewDraft=${view.viewDraft}
                  setViewDraft=${view.setViewDraft}
                  editingTitle=${view.editingTitle}
                  setEditingTitle=${view.setEditingTitle}
                  titleRef=${view.titleRef}
                  savingView=${savingView}
                  handleTitleKeyDown=${handlers.handleTitleKeyDown}
                  startEditTitle=${handlers.startEditTitle}
                />
                <${DescriptionField}
                  mode="view"
                  description=${description}
                  setDescription=${setDescription}
                  submitting=${submitting}
                  createEditorApiRef=${createEditorApiRef}
                  editingDesc=${view.editingDesc}
                  setEditingDesc=${view.setEditingDesc}
                  viewDraft=${view.viewDraft}
                  setViewDraft=${view.setViewDraft}
                  savingView=${savingView}
                  detailEditorApiRef=${detailEditorApiRef}
                  descMinHeight=${descMinHeight}
                  descViewRef=${descViewRef}
                  md=${md}
                  startEditDesc=${handlers.startEditDesc}
                  workingDir=${workingDir}
                  improvingDesc=${improvingDesc}
                  improveDescriptionText=${improveDescriptionText}
                />
                <${SubtasksList}
                  subtasks=${subtasks}
                  onSelectIssue=${onSelectIssue}
                />

                <fieldset class="fieldset min-w-0">
                  <legend class="fieldset-legend">Dependencies</legend>
                  <${DependenciesViewField}
                    allIssues=${allIssues}
                    data=${data}
                    deps=${deps.deps}
                    depsLoading=${deps.depsLoading}
                    depsBusy=${deps.depsBusy}
                    changeDepType=${deps.changeDepType}
                    mutateDep=${deps.mutateDep}
                    onSelectIssue=${onSelectIssue}
                    newDepType=${deps.newDepType}
                    setNewDepType=${deps.setNewDepType}
                    newDepId=${deps.newDepId}
                    setNewDepId=${deps.setNewDepId}
                    handleAddDep=${deps.handleAddDep}
                  />
                </fieldset>

                <${CommentsSection}
                  comments=${comments.comments}
                  depsLoading=${deps.depsLoading}
                  addingComment=${comments.addingComment}
                  commentDraft=${comments.commentDraft}
                  setCommentDraft=${comments.setCommentDraft}
                  savingComment=${comments.savingComment}
                  commentRef=${comments.commentRef}
                  handleCommentBlur=${comments.handleCommentBlur}
                  startAddComment=${comments.startAddComment}
                  workingDir=${workingDir}
                />

                <fieldset class="fieldset min-w-0">
                  <legend class="fieldset-legend">Notes</legend>
                  <${NotesField}
                    mode="view"
                    depsLoading=${deps.depsLoading}
                    viewDraft=${view.viewDraft}
                    setViewDraft=${view.setViewDraft}
                    editingNotes=${view.editingNotes}
                    setEditingNotes=${view.setEditingNotes}
                    notesRef=${view.notesRef}
                    notesViewRef=${view.notesViewRef}
                    notesMinHeight=${view.notesMinHeight}
                    savingView=${savingView}
                    startEditNotes=${handlers.startEditNotes}
                    workingDir=${workingDir}
                  />
                </fieldset>
              `
        }
      </div>

      <${DetailActionBar}
        creating=${creating}
        data=${data}
        handleClose=${handlers.handleClose}
        submitting=${submitting}
        handleSave=${handlers.handleSave}
        handleViewSave=${handlers.handleViewSave}
        description=${description}
        viewDirty=${viewDirty}
        savingView=${savingView}
        canGoBack=${canGoBack}
        canGoForward=${canGoForward}
        onGoBack=${onGoBack}
        onGoForward=${onGoForward}
      />
      <//>
      ${
        chrome.panelMenu &&
        html`
          <${ContextMenu}
            x=${chrome.panelMenu.x}
            y=${chrome.panelMenu.y}
            items=${chrome.panelMenuItems}
            onClose=${() => chrome.setPanelMenu(null)}
          />
        `
      }
      <${ConfirmDialog}
        isOpen=${chrome.confirmDiscard}
        title="Discard changes?"
        message="You have unsaved changes. Discard them and close?"
        confirmLabel="Discard"
        cancelLabel="Keep editing"
        confirmVariant="danger"
        onConfirm=${handlers.handleDiscardAndClose}
        onCancel=${() => chrome.setConfirmDiscard(false)}
      />
    </${Fragment}>
  `;
}
