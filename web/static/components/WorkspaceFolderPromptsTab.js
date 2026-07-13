// Mitto Web Interface — Folder Editor: Prompts tab
//
// Renders the workspace prompts management tab body of the folder editor
// (add/edit/enable/disable per-workspace prompts). Extracted verbatim from
// WorkspaceFolderEditor.js (mitto-90f.4 Increment C). Behavior-preserving;
// all state/handlers still live in the shell (WorkspacesDialog.js) and are
// drilled through as props.
const { html } = window.preact;

import { SpinnerIcon, PlusIcon, EditIcon, TrashIcon } from "./Icons.js";
import { Tooltip } from "./Tooltip.js";

export function WorkspaceFolderPromptsTab({
  folderPrompts,
  promptsLoading,
  showAddPrompt,
  setShowAddPrompt,
  editingPromptIndex,
  setEditingPromptIndex,
  editPromptName,
  setEditPromptName,
  editPromptText,
  setEditPromptText,
  editPromptColor,
  setEditPromptColor,
  editPromptGroup,
  setEditPromptGroup,
  newPromptName,
  setNewPromptName,
  newPromptText,
  setNewPromptText,
  newPromptColor,
  setNewPromptColor,
  newPromptGroup,
  setNewPromptGroup,
  promptSaving,
  saveWorkspacePrompt,
  deleteWorkspacePrompt,
  togglePromptEnabled,
}) {
  return html`
    <div class="space-y-4">
      <div class="flex items-center justify-between">
        <p class="text-sm text-mitto-text-muted">
          Manage prompts for this workspace. Built-in prompts are read-only but
          can be disabled.
        </p>
        <button
          onClick=${() => setShowAddPrompt(!showAddPrompt)}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${showAddPrompt
            ? "btn-active"
            : ""}"
          data-tip="Add Prompt"
          aria-label="Add Prompt"
        >
          <${PlusIcon} className="w-5 h-5" />
        </button>
      </div>

      ${showAddPrompt &&
      html`
        <fieldset class="fieldset pt-2">
          <legend class="fieldset-legend">New Prompt</legend>
          <label class="label" for="new-prompt-name">Button Label</label>
          <input
            id="new-prompt-name"
            type="text"
            value=${newPromptName}
            onInput=${(e) => setNewPromptName(e.target.value)}
            placeholder="e.g., Continue"
            class="input input-sm w-full"
          />
          <label class="label" for="new-prompt-text">Prompt Text</label>
          <textarea
            id="new-prompt-text"
            value=${newPromptText}
            onInput=${(e) => setNewPromptText(e.target.value)}
            placeholder="e.g., Please continue with the current task."
            rows="8"
            class="textarea textarea-sm w-full resize-y"
          />
          <label class="label" for="new-prompt-group">Group (optional)</label>
          <input
            id="new-prompt-group"
            type="text"
            value=${newPromptGroup}
            onInput=${(e) => setNewPromptGroup(e.target.value)}
            placeholder="e.g., Tasks, Code Quality"
            class="input input-sm w-full"
          />
          <label class="label">Background Color (optional)</label>
          <div class="flex items-center gap-2">
            <input
              type="color"
              value=${newPromptColor || "#334155"}
              onInput=${(e) => setNewPromptColor(e.target.value)}
              class="w-10 h-10 rounded cursor-pointer border border-mitto-border-2"
            />
            <input
              type="text"
              value=${newPromptColor}
              onInput=${(e) => setNewPromptColor(e.target.value)}
              placeholder="#E8F5E9"
              class="input input-sm flex-1 font-mono"
            />
          </div>
          <div class="flex justify-end gap-2 mt-2">
            <button
              onClick=${() => {
                setShowAddPrompt(false);
                setNewPromptName("");
                setNewPromptText("");
                setNewPromptColor("");
                setNewPromptGroup("");
              }}
              class="btn btn-ghost btn-sm"
            >
              Cancel
            </button>
            <button
              onClick=${async () => {
                await saveWorkspacePrompt({
                  name: newPromptName.trim(),
                  prompt: newPromptText.trim(),
                  backgroundColor: newPromptColor || undefined,
                  group: newPromptGroup.trim() || undefined,
                  enabled: true,
                });
                setShowAddPrompt(false);
                setNewPromptName("");
                setNewPromptText("");
                setNewPromptColor("");
                setNewPromptGroup("");
              }}
              disabled=${!newPromptName.trim() ||
              !newPromptText.trim() ||
              promptSaving}
              class="btn btn-primary btn-sm"
            >
              ${promptSaving ? "Saving..." : "Add Prompt"}
            </button>
          </div>
        </fieldset>
      `}
      ${promptsLoading
        ? html`<div class="flex items-center justify-center p-4">
            <${SpinnerIcon} className="w-5 h-5 animate-spin" />
          </div>`
        : html`
            <ul class="list">
              ${folderPrompts.length === 0
                ? html`<li class="list-row">
                    <div class="p-4 text-center text-mitto-text-muted text-sm">
                      No prompts found. Click + to add a workspace prompt.
                    </div>
                  </li>`
                : [...folderPrompts]
                    .sort((a, b) => (a.name || "").localeCompare(b.name || ""))
                    .map((prompt, idx) => {
                      const isBuiltin =
                        prompt.source === "builtin" || prompt.source === "file";
                      const isEnabled = prompt.enabled !== false;
                      return html`
                        <li key=${prompt.name} class="list-row p-0">
                          <div
                            class="list-col-grow collapse ${editingPromptIndex ===
                            idx
                              ? "collapse-open"
                              : "collapse-close"} bg-mitto-surface-3/20 rounded-sm border transition-all ${isEnabled
                              ? "border-mitto-border-2/50"
                              : "border-mitto-border-2/30 opacity-60"} w-full"
                          >
                            <div
                              class="collapse-title flex items-center gap-3 p-3 min-h-0"
                            >
                              <${Tooltip}
                                tip=${isEnabled
                                  ? "Disable this prompt"
                                  : "Enable this prompt"}
                                placement="right"
                                className="shrink-0"
                              >
                                <input
                                  type="checkbox"
                                  checked=${isEnabled}
                                  onChange=${() => togglePromptEnabled(prompt)}
                                  onClick=${(e) => e.stopPropagation()}
                                  class="checkbox checkbox-sm"
                                  aria-label=${isEnabled
                                    ? "Disable this prompt"
                                    : "Enable this prompt"}
                                />
                              <//>
                              ${prompt.backgroundColor &&
                              html`
                                <div
                                  class="w-5 h-5 rounded-sm shrink-0 border border-mitto-border-2"
                                  style="background-color: ${prompt.backgroundColor}"
                                />
                              `}
                              <div class="flex-1 min-w-0">
                                <div class="flex items-center gap-2">
                                  <span
                                    class="text-sm font-medium ${isEnabled
                                      ? "text-mitto-accent"
                                      : "text-mitto-text-muted"}"
                                    >${prompt.name}</span
                                  >
                                  <span
                                    class="badge badge-sm ${isBuiltin
                                      ? "bg-mitto-accent-500/20 text-mitto-accent"
                                      : "bg-green-500/20 text-mitto-success"}"
                                  >
                                    ${isBuiltin ? "built-in" : "workspace"}
                                  </span>
                                </div>
                                ${prompt.description &&
                                html`<p
                                  class="text-xs text-mitto-text-muted mt-0.5 truncate"
                                >
                                  ${prompt.description}
                                </p>`}
                                ${!prompt.description &&
                                prompt.prompt &&
                                html`<p
                                  class="text-xs text-mitto-text-muted mt-0.5 truncate"
                                >
                                  ${prompt.prompt.slice(0, 80)}${prompt.prompt
                                    .length > 80
                                    ? "..."
                                    : ""}
                                </p>`}
                              </div>
                              <div
                                class="flex items-center gap-1 shrink-0"
                                onClick=${(e) => e.stopPropagation()}
                              >
                                <button
                                  onClick=${() => {
                                    if (editingPromptIndex === idx) {
                                      setEditingPromptIndex(null);
                                    } else {
                                      setEditPromptName(prompt.name || "");
                                      setEditPromptText(prompt.prompt || "");
                                      setEditPromptColor(
                                        prompt.backgroundColor || "",
                                      );
                                      setEditPromptGroup(prompt.group || "");
                                      setEditingPromptIndex(idx);
                                    }
                                  }}
                                  class="btn btn-ghost btn-square btn-xs tooltip tooltip-bottom"
                                  data-tip=${isBuiltin ? "View" : "Edit"}
                                  aria-label=${isBuiltin ? "View" : "Edit"}
                                >
                                  <${EditIcon}
                                    className="w-4 h-4 text-mitto-text-muted"
                                  />
                                </button>
                                ${!isBuiltin &&
                                html`
                                  <button
                                    onClick=${() =>
                                      deleteWorkspacePrompt(prompt.name)}
                                    class="btn btn-ghost btn-square btn-xs tooltip tooltip-bottom"
                                    data-tip="Delete"
                                    aria-label="Delete"
                                  >
                                    <${TrashIcon}
                                      className="w-4 h-4 text-mitto-text-muted hover:text-mitto-danger"
                                    />
                                  </button>
                                `}
                              </div>
                            </div>
                            <div class="collapse-content px-3 pb-3">
                              <fieldset class="fieldset pt-2">
                                <legend class="fieldset-legend">
                                  ${isBuiltin ? "View Prompt" : "Edit Prompt"}
                                </legend>
                                <label
                                  class="label"
                                  for=${"edit-prompt-name-" + idx}
                                  >Button Label</label
                                >
                                <input
                                  id=${"edit-prompt-name-" + idx}
                                  type="text"
                                  value=${isBuiltin
                                    ? prompt.name
                                    : editPromptName}
                                  onInput=${(e) =>
                                    !isBuiltin &&
                                    setEditPromptName(e.target.value)}
                                  disabled=${isBuiltin}
                                  class="input input-sm w-full ${isBuiltin
                                    ? "opacity-60 cursor-not-allowed"
                                    : ""}"
                                />
                                <label
                                  class="label"
                                  for=${"edit-prompt-text-" + idx}
                                  >Prompt Text</label
                                >
                                <textarea
                                  id=${"edit-prompt-text-" + idx}
                                  rows="8"
                                  value=${isBuiltin
                                    ? prompt.prompt
                                    : editPromptText}
                                  onInput=${(e) =>
                                    !isBuiltin &&
                                    setEditPromptText(e.target.value)}
                                  disabled=${isBuiltin}
                                  class="textarea textarea-sm w-full resize-y ${isBuiltin
                                    ? "opacity-60 cursor-not-allowed"
                                    : ""}"
                                />
                                <label
                                  class="label"
                                  for=${"edit-prompt-group-" + idx}
                                  >Group (optional)</label
                                >
                                <input
                                  id=${"edit-prompt-group-" + idx}
                                  type="text"
                                  value=${isBuiltin
                                    ? prompt.group || ""
                                    : editPromptGroup}
                                  onInput=${(e) =>
                                    !isBuiltin &&
                                    setEditPromptGroup(e.target.value)}
                                  disabled=${isBuiltin}
                                  placeholder="e.g., Tasks, Code Quality"
                                  class="input input-sm w-full ${isBuiltin
                                    ? "opacity-60 cursor-not-allowed"
                                    : ""}"
                                />
                                ${!isBuiltin &&
                                html`
                                  <label class="label"
                                    >Background Color (optional)</label
                                  >
                                  <div class="flex items-center gap-2">
                                    <input
                                      type="color"
                                      value=${editPromptColor || "#334155"}
                                      onInput=${(e) =>
                                        setEditPromptColor(e.target.value)}
                                      class="w-8 h-8 rounded cursor-pointer border border-mitto-border-2"
                                    />
                                    <input
                                      type="text"
                                      value=${editPromptColor}
                                      onInput=${(e) =>
                                        setEditPromptColor(e.target.value)}
                                      placeholder="#E8F5E9"
                                      class="input input-sm flex-1 font-mono"
                                    />
                                  </div>
                                `}
                                <div class="flex justify-end gap-2 mt-2">
                                  <button
                                    onClick=${() => setEditingPromptIndex(null)}
                                    class="btn btn-ghost btn-sm"
                                  >
                                    ${isBuiltin ? "Close" : "Cancel"}
                                  </button>
                                  ${!isBuiltin &&
                                  html`
                                    <button
                                      onClick=${async () => {
                                        await saveWorkspacePrompt({
                                          name: editPromptName.trim(),
                                          prompt: editPromptText.trim(),
                                          backgroundColor:
                                            editPromptColor || undefined,
                                          group:
                                            editPromptGroup.trim() || undefined,
                                          enabled: prompt.enabled !== false,
                                        });
                                        setEditingPromptIndex(null);
                                      }}
                                      disabled=${!editPromptName.trim() ||
                                      !editPromptText.trim() ||
                                      promptSaving}
                                      class="btn btn-primary btn-sm"
                                    >
                                      ${promptSaving ? "Saving..." : "Save"}
                                    </button>
                                  `}
                                </div>
                              </fieldset>
                            </div>
                          </div>
                        </li>
                      `;
                    })}
            </ul>
          `}
    </div>
  `;
}
