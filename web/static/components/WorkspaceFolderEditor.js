// Mitto Web Interface — Folder Editor Panel
//
// Renders the tab bar and tab bodies shown when a FOLDER (a group of
// workspaces sharing a working_dir) is selected in the Workspaces dialog.
// Behavior-preserving extraction from WorkspacesDialog.js: all state and
// handlers still live in the shell and are received as props (pragmatic
// pass-through per mitto-90f.4 plan §3.1/§4). No effect-timing changes.
const { html, Fragment } = window.preact;

import { getBasename } from "../lib.js";
import { hasNativeFolderPicker, pickFolder } from "../utils/index.js";

import {
  SpinnerIcon,
  FolderIcon,
  TrashIcon,
  PlusIcon,
  RobotIcon,
  GlobeIcon,
  ErrorIcon,
} from "./Icons.js";

import { AutoChildrenEditor } from "./SettingsDialog.js";
import { Tooltip } from "./Tooltip.js";
import { ShortcutsEditor } from "./ShortcutsEditor.js";
import { WorkspaceFolderBeadsTab } from "./WorkspaceFolderBeadsTab.js";
import { WorkspaceFolderPromptsTab } from "./WorkspaceFolderPromptsTab.js";

// Section descriptors for the folder Shortcuts tab. Section IDs match those
// persisted on the server (folders.json) and used by the render-time toolbars.
const SHORTCUT_SECTIONS = [
  {
    id: "tasksList",
    label: "Tasks list",
    desc: "Buttons shown in the Tasks list toolbar.",
  },
  {
    id: "conversations",
    label: "Conversation",
    desc: "Buttons shown in the conversation toolbar; run in the current conversation.",
  },
  {
    id: "beadsIssue",
    label: "Beads issue",
    desc: "Buttons shown on the Beads issue view.",
  },
];

export function WorkspaceFolderEditor(props) {
  return renderFolderEditor(props);
}

// The bulk of the panel is implemented as a plain function so the very large
// JSX return can be moved verbatim from the shell without a wrapper diff.
function renderFolderEditor({
  // Tab bar + shared shell state
  activeTab,
  setActiveTab,
  folderTabs,
  selectedFolder,
  setSelectedFolder,
  groupedWorkspaces,
  workspaces,
  setWorkspaces,
  newFolderKey,
  getWorkspaceKey,
  modelProfiles,
  // Folder header edit fields (grouped state/setters from useFolderGeneralEdits)
  edits,
  editSetters,
  folderGroupSuggestions,
  // Metadata tab (grouped state/setters from useFolderMetadataConfig)
  metadata,
  metadataSetters,
  // Beads tab (grouped state/handlers from useBeadsFolderConfig)
  beads,
  beadsSetters,
  beadsHandlers,
  // Prompts tab (grouped state/setters/handlers from useFolderPromptsConfig)
  prompts,
  promptsSetters,
  promptsHandlers,
  // Processors tab (grouped state/setters/handlers from useFolderProcessorsConfig)
  processors,
  processorsSetters,
  processorsHandlers,
  // Shortcuts tab (grouped state/handlers from useFolderShortcutsConfig)
  shortcuts,
  shortcutsHandlers,
  // Prompt parameter dialog opener (drilled through to WorkspaceFolderBeadsTab
  // so the Prompt Actions sliders button can collect args for parametrized
  // Pull/Push/Sync prompts).
  onOpenPromptParamDialog,
}) {
  const { editName, editCode, editColor, editGroup, editAutoChildren } = edits;
  const {
    setEditName,
    setEditCode,
    setEditColor,
    setEditGroup,
    setEditAutoChildren,
  } = editSetters;
  const folderGroup = groupedWorkspaces.find(
    (g) => g.displayName === selectedFolder,
  );
  const firstWs = folderGroup?.workspaces[0];
  if (!firstWs)
    return html`<div
      class="flex items-center justify-center h-full text-mitto-text-muted text-sm"
    >
      No workspaces in this folder
    </div>`;
  const isNewFolder = newFolderKey && getWorkspaceKey(firstWs) === newFolderKey;
  const isIncomplete =
    isNewFolder && (!firstWs.working_dir || firstWs.working_dir.trim() === "");
  const updateNewFolderPath = (path) => {
    setWorkspaces((prev) => {
      // If no other workspace already lives in this folder, this is the
      // folder's first workspace — mark it as the default for the folder.
      const isFirstForFolder = !prev.some(
        (ws) => getWorkspaceKey(ws) !== newFolderKey && ws.working_dir === path,
      );
      return prev.map((ws) =>
        getWorkspaceKey(ws) === newFolderKey
          ? {
              ...ws,
              working_dir: path,
              is_default: isFirstForFolder ? true : undefined,
            }
          : ws,
      );
    });
    // Update the selected folder name to reflect new path
    const newDisplayName = editName || getBasename(path) || "New Workspace";
    setSelectedFolder(newDisplayName);
  };
  return html`<${Fragment}>
                  <!-- Folder tab bar (daisyUI radio tabs-border) -->
                  <div role="tablist" class="tabs tabs-border px-4 shrink-0">
                    ${folderTabs.map(
                      (tab) => html`
                        <input
                          key=${tab.id}
                          type="radio"
                          name="ws-folder-tabs"
                          role="tab"
                          title=${tab.label}
                          aria-label=${tab.short}
                          data-testid=${`ws-tab-${tab.id}`}
                          checked=${activeTab === tab.id}
                          onChange=${() => setActiveTab(tab.id)}
                          class="tab ${activeTab === tab.id
                            ? "tab-active text-mitto-accent"
                            : ""}"
                        />
                      `,
                    )}
                  </div>

                  <!-- Folder tab content -->
                  <div
                    class="flex-1 overflow-y-auto p-6"
                    data-testid="ws-tab-content"
                  >
                    <!-- Folder General tab -->
                    ${
                      activeTab === "general" &&
                      html`
                        <div class="space-y-4">
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Location</legend>
                            <label class="label" for="ws-working-dir"
                              >Working Directory</label
                            >
                            <div class="ws-working-dir-slot">
                              ${isNewFolder
                                ? html`
                                    <div class="flex gap-2">
                                      <input
                                        id="ws-working-dir"
                                        type="text"
                                        value=${firstWs.working_dir}
                                        onInput=${(e) =>
                                          updateNewFolderPath(e.target.value)}
                                        placeholder="/path/to/project"
                                        class="input input-sm flex-1 ${isIncomplete
                                          ? "border-error"
                                          : ""}"
                                      />
                                      ${hasNativeFolderPicker() &&
                                      html`
                                        <button
                                          onClick=${async () => {
                                            const p = await pickFolder();
                                            if (p) updateNewFolderPath(p);
                                          }}
                                          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom"
                                          data-tip="Browse"
                                          aria-label="Browse"
                                        >
                                          <${FolderIcon} className="w-4 h-4" />
                                        </button>
                                      `}
                                    </div>
                                    ${isIncomplete &&
                                    html`<p class="label text-error">
                                      Please select a folder for this workspace.
                                    </p>`}
                                  `
                                : html`
                                    <input
                                      id="ws-working-dir"
                                      type="text"
                                      value=${firstWs.working_dir}
                                      readonly
                                      class="input input-sm w-full cursor-default"
                                    />
                                  `}
                            </div>
                            <label class="label" for="ws-display-name"
                              >Display Name</label
                            >
                            <input
                              id="ws-display-name"
                              type="text"
                              value=${editName}
                              onInput=${(e) => setEditName(e.target.value)}
                              placeholder=${getBasename(firstWs.working_dir)}
                              class="input input-sm w-full"
                            />
                          </fieldset>
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Appearance</legend>
                            <div class="flex gap-4 items-start">
                              <div class="flex-1 min-w-0">
                                <label class="label" for="ws-folder-group"
                                  >Group</label
                                >
                                <input
                                  id="ws-folder-group"
                                  type="text"
                                  list="ws-folder-group-options"
                                  value=${editGroup}
                                  onInput=${(e) => setEditGroup(e.target.value)}
                                  placeholder="e.g., development, personal..."
                                  class="input input-sm w-full"
                                />
                                <datalist id="ws-folder-group-options">
                                  ${folderGroupSuggestions.map(
                                    (g) => html`<option value=${g}></option>`,
                                  )}
                                </datalist>
                                <p class="text-xs text-mitto-text-muted mt-1">
                                  Organize folders into groups. Existing groups
                                  are suggested as you type.
                                </p>
                              </div>
                              <div class="flex-1 min-w-0">
                                <label class="label" for="ws-badge-code"
                                  >Badge Code</label
                                >
                                <input
                                  id="ws-badge-code"
                                  type="text"
                                  value=${editCode}
                                  onInput=${(e) =>
                                    setEditCode(
                                      e.target.value.toUpperCase().slice(0, 3),
                                    )}
                                  placeholder="Auto (3 max)"
                                  maxlength="3"
                                  class="input input-sm w-full font-mono uppercase"
                                />
                              </div>
                              <div class="shrink-0">
                                <label class="label" for="ws-badge-color"
                                  >Badge Color</label
                                >
                                <div class="flex items-center gap-2">
                                  <input
                                    id="ws-badge-color"
                                    type="color"
                                    value=${editColor}
                                    onInput=${(e) =>
                                      setEditColor(e.target.value)}
                                    class="rounded cursor-pointer border border-mitto-border"
                                    style="width: 38px; height: 38px"
                                  />
                                  <span
                                    class="text-xs text-mitto-text-muted font-mono"
                                    >${editColor}</span
                                  >
                                </div>
                              </div>
                            </div>
                          </fieldset>
                        </div>
                      `
                    }

                    <!-- Folder Metadata tab -->
                    ${
                      activeTab === "metadata" &&
                      html`
                        <div class="space-y-4">
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">Metadata</legend>
                            <label class="label" for="ws-meta-description"
                              >Description</label
                            >
                            <textarea
                              id="ws-meta-description"
                              value=${metadata.editMetaDescription}
                              onInput=${(e) =>
                                metadataSetters.setEditMetaDescription(
                                  e.target.value,
                                )}
                              placeholder="A description of this workspace/project..."
                              rows="3"
                              class="textarea textarea-sm w-full resize-vertical"
                            />
                            <label class="label" for="ws-meta-url">URL</label>
                            <input
                              id="ws-meta-url"
                              type="url"
                              value=${metadata.editMetaUrl}
                              onInput=${(e) =>
                                metadataSetters.setEditMetaUrl(e.target.value)}
                              placeholder="https://github.com/..."
                              class="input input-sm w-full"
                            />
                            <label class="label" for="ws-meta-group"
                              >Group</label
                            >
                            <input
                              id="ws-meta-group"
                              type="text"
                              value=${metadata.editMetaGroup}
                              onInput=${(e) =>
                                metadataSetters.setEditMetaGroup(
                                  e.target.value,
                                )}
                              placeholder="e.g., CGW, Infrastructure, Frontend..."
                              class="input input-sm w-full"
                            />
                          </fieldset>

                          <!-- User Data Schema Editor -->
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">
                              User Data Schema
                            </legend>
                            <div class="flex items-center justify-between mb-2">
                              <p class="label">
                                Define custom data attributes for conversations
                                in this workspace.
                              </p>
                              <button
                                onClick=${() =>
                                  metadataSetters.setEditUserDataFields(
                                    (prev) => [
                                      ...prev,
                                      {
                                        name: "",
                                        type: "string",
                                        description: "",
                                      },
                                    ],
                                  )}
                                class="btn btn-ghost btn-xs gap-1 tooltip tooltip-bottom"
                                data-tip="Add Field"
                              >
                                <${PlusIcon} className="w-3.5 h-3.5" />
                                Add Field
                              </button>
                            </div>
                            ${metadata.editUserDataFields.length === 0 &&
                            html`
                              <p
                                class="text-xs text-mitto-text-muted italic py-2"
                              >
                                No fields defined. Click "Add Field" to create
                                one.
                              </p>
                            `}
                            ${metadata.editUserDataFields.length > 0 &&
                            html`
                              <ul class="list">
                                ${metadata.editUserDataFields.map(
                                  (field, i) => html`
                                    <li
                                      key=${i}
                                      class="list-row items-start gap-2"
                                    >
                                      <div class="flex-1 min-w-0">
                                        <label
                                          class="label"
                                          for=${"ws-udf-name-" + i}
                                          >Name</label
                                        >
                                        <input
                                          id=${"ws-udf-name-" + i}
                                          type="text"
                                          value=${field.name}
                                          onInput=${(e) =>
                                            metadataSetters.setEditUserDataFields(
                                              (prev) =>
                                                prev.map((f, idx) =>
                                                  idx === i
                                                    ? {
                                                        ...f,
                                                        name: e.target.value,
                                                      }
                                                    : f,
                                                ),
                                            )}
                                          placeholder="e.g., JIRA Ticket"
                                          class="input input-sm w-full"
                                          style="height: 28px; box-sizing: border-box"
                                        />
                                      </div>
                                      <div class="w-24 shrink-0">
                                        <label
                                          class="label"
                                          for=${"ws-udf-type-" + i}
                                          >Type</label
                                        >
                                        <select
                                          id=${"ws-udf-type-" + i}
                                          value=${field.type}
                                          onChange=${(e) =>
                                            metadataSetters.setEditUserDataFields(
                                              (prev) =>
                                                prev.map((f, idx) =>
                                                  idx === i
                                                    ? {
                                                        ...f,
                                                        type: e.target.value,
                                                      }
                                                    : f,
                                                ),
                                            )}
                                          class="select select-sm w-full"
                                          style="height: 28px; box-sizing: border-box"
                                        >
                                          <option value="string">string</option>
                                          <option value="url">url</option>
                                        </select>
                                      </div>
                                      <div class="flex-1 min-w-0">
                                        <label
                                          class="label"
                                          for=${"ws-udf-desc-" + i}
                                          >Description</label
                                        >
                                        <input
                                          id=${"ws-udf-desc-" + i}
                                          type="text"
                                          value=${field.description}
                                          onInput=${(e) =>
                                            metadataSetters.setEditUserDataFields(
                                              (prev) =>
                                                prev.map((f, idx) =>
                                                  idx === i
                                                    ? {
                                                        ...f,
                                                        description:
                                                          e.target.value,
                                                      }
                                                    : f,
                                                ),
                                            )}
                                          placeholder="Optional description..."
                                          class="input input-sm w-full"
                                          style="height: 28px; box-sizing: border-box"
                                        />
                                      </div>
                                      <div class="shrink-0 pt-4">
                                        <button
                                          onClick=${() =>
                                            metadataSetters.setEditUserDataFields(
                                              (prev) =>
                                                prev.filter(
                                                  (_, idx) => idx !== i,
                                                ),
                                            )}
                                          class="btn btn-ghost btn-square btn-xs tooltip tooltip-bottom"
                                          data-tip="Remove field"
                                          aria-label="Remove field"
                                        >
                                          <${TrashIcon}
                                            className="w-3.5 h-3.5"
                                          />
                                        </button>
                                      </div>
                                    </li>
                                  `,
                                )}
                              </ul>
                            `}
                          </fieldset>
                        </div>
                      `
                    }

                    <!-- Folder Beads tab -->
                    ${
                      activeTab === "beads" &&
                      html`<${WorkspaceFolderBeadsTab}
                        beads=${beads}
                        beadsSetters=${beadsSetters}
                        beadsHandlers=${beadsHandlers}
                        onOpenPromptParamDialog=${onOpenPromptParamDialog}
                      />`
                    }

                    <!-- Folder Prompts tab -->
                    ${
                      activeTab === "prompts" &&
                      html`<${WorkspaceFolderPromptsTab}
                        prompts=${prompts}
                        promptsSetters=${promptsSetters}
                        promptsHandlers=${promptsHandlers}
                      />`
                    }

                    <!-- Folder Processors tab -->
                    ${
                      activeTab === "processors" &&
                      html`
                        <div class="space-y-4">
                          <p class="text-sm text-mitto-text-muted">
                            Manage processors for this workspace. Global
                            processors can be disabled per workspace.
                          </p>

                          ${processors.processorsLoading
                            ? html`<div
                                class="flex items-center justify-center p-4"
                              >
                                <${SpinnerIcon}
                                  className="w-5 h-5 animate-spin"
                                />
                              </div>`
                            : html`
                                <div class="space-y-2">
                                  ${processors.folderProcessors.length === 0
                                    ? html`<div
                                        class="p-4 text-center text-mitto-text-muted text-sm"
                                      >
                                        No processors found for this workspace.
                                      </div>`
                                    : processors.folderProcessors.map(
                                        (proc) => {
                                          const hasError = !!proc.error;
                                          const isWorkspace =
                                            proc.source === "workspace";
                                          const isEnabled =
                                            proc.enabled !== false;
                                          const isPromptMode =
                                            proc.mode === "prompt";
                                          const sourceLabel = isWorkspace
                                            ? "workspace"
                                            : proc.source === "builtin"
                                              ? "built-in"
                                              : "global";
                                          const sourceBadgeClass = isWorkspace
                                            ? "bg-green-500/20 text-mitto-success"
                                            : proc.source === "builtin"
                                              ? "bg-mitto-accent-500/20 text-mitto-accent"
                                              : "bg-orange-500/20 text-orange-400";
                                          const borderClass = hasError
                                            ? "border-error/40"
                                            : isPromptMode
                                              ? "border-purple-500/30"
                                              : isEnabled
                                                ? "border-mitto-border-2/50"
                                                : "border-mitto-border-2/30 opacity-60";
                                          const isExpanded =
                                            processors.expandedProcessor ===
                                            proc.name;
                                          return html`
                                            <div
                                              key=${proc.name}
                                              class="collapse collapse-plus ${isExpanded
                                                ? "collapse-open"
                                                : "collapse-close"} bg-mitto-surface-3/20 rounded-sm border transition-all ${borderClass} ${!isEnabled &&
                                              !isPromptMode &&
                                              !hasError
                                                ? "opacity-60"
                                                : ""}"
                                            >
                                              <div
                                                class="collapse-title flex items-center gap-3 p-3 min-h-0 pr-12"
                                                onClick=${() =>
                                                  processorsSetters.setExpandedProcessor(
                                                    isExpanded
                                                      ? null
                                                      : proc.name,
                                                  )}
                                              >
                                                <${Tooltip}
                                                  tip=${hasError
                                                    ? "Invalid processor — cannot enable/disable"
                                                    : isEnabled
                                                      ? "Disable this processor"
                                                      : "Enable this processor"}
                                                  placement="right"
                                                  className="shrink-0"
                                                >
                                                  <input
                                                    type="checkbox"
                                                    checked=${isEnabled}
                                                    disabled=${hasError}
                                                    onChange=${() => {
                                                      if (!hasError)
                                                        processorsHandlers.toggleProcessorEnabled(
                                                          proc,
                                                        );
                                                    }}
                                                    onClick=${(e) =>
                                                      e.stopPropagation()}
                                                    class="checkbox checkbox-sm"
                                                    aria-label=${hasError
                                                      ? "Invalid processor"
                                                      : isEnabled
                                                        ? "Disable this processor"
                                                        : "Enable this processor"}
                                                  />
                                                <//>
                                                <div class="flex-1 min-w-0">
                                                  <div
                                                    class="flex items-center gap-2"
                                                  >
                                                    ${isPromptMode &&
                                                    html`<${RobotIcon}
                                                      className="w-4 h-4 text-purple-400 shrink-0"
                                                    />`}
                                                    <span
                                                      class="text-sm font-medium font-mono ${hasError ||
                                                      !isEnabled
                                                        ? "text-mitto-text-muted"
                                                        : "text-mitto-accent"}"
                                                      >${proc.name}</span
                                                    >
                                                    ${proc.source === "global"
                                                      ? html`<${GlobeIcon}
                                                          className="w-3.5 h-3.5 text-orange-400 shrink-0"
                                                          title="Global processor"
                                                        />`
                                                      : html`<span
                                                          class="badge badge-sm ${sourceBadgeClass}"
                                                          >${sourceLabel}</span
                                                        >`}
                                                    ${hasError &&
                                                    html`
                                                      <${Tooltip}
                                                        tip=${proc.error}
                                                        placement="right"
                                                        className="shrink-0"
                                                      >
                                                        <span
                                                          class="badge badge-sm badge-error gap-1"
                                                        >
                                                          <${ErrorIcon}
                                                            className="w-3 h-3"
                                                          />
                                                          error
                                                        </span>
                                                      <//>
                                                    `}
                                                    ${proc.on &&
                                                    html`<span
                                                      class="text-xs text-mitto-text-muted"
                                                      >${proc.on}${proc.match
                                                        ? `:${proc.match}`
                                                        : ""}</span
                                                    >`}
                                                  </div>
                                                  ${proc.description &&
                                                  html`<p
                                                    class="text-xs text-mitto-text-muted mt-0.5 truncate"
                                                  >
                                                    ${proc.description}
                                                  </p>`}
                                                </div>
                                              </div>
                                              <div
                                                class="collapse-content px-3 pb-3"
                                              >
                                                <div class="space-y-2 text-sm">
                                                  ${proc.description &&
                                                  html`
                                                    <div>
                                                      <span
                                                        class="text-xs text-mitto-text-muted block mb-0.5"
                                                        >Description</span
                                                      >
                                                      <p
                                                        class="text-mitto-text"
                                                      >
                                                        ${proc.description}
                                                      </p>
                                                    </div>
                                                  `}
                                                  ${proc.on &&
                                                  html`
                                                    <div>
                                                      <span
                                                        class="text-xs text-mitto-text-muted block mb-0.5"
                                                        >Trigger</span
                                                      >
                                                      <p
                                                        class="font-mono text-xs"
                                                      >
                                                        ${proc.on}${proc.match
                                                          ? `: ${proc.match}`
                                                          : ""}
                                                      </p>
                                                    </div>
                                                  `}
                                                  ${proc.mode &&
                                                  html`
                                                    <div>
                                                      <span
                                                        class="text-xs text-mitto-text-muted block mb-0.5"
                                                        >Mode</span
                                                      >
                                                      <p
                                                        class="font-mono text-xs"
                                                      >
                                                        ${proc.mode}
                                                      </p>
                                                    </div>
                                                  `}
                                                  ${proc.source &&
                                                  html`
                                                    <div>
                                                      <span
                                                        class="text-xs text-mitto-text-muted block mb-0.5"
                                                        >Source</span
                                                      >
                                                      <p
                                                        class="font-mono text-xs"
                                                      >
                                                        ${proc.source}
                                                      </p>
                                                    </div>
                                                  `}
                                                  ${proc.parameters?.length &&
                                                  html`
                                                    <div>
                                                      <span
                                                        class="text-xs text-mitto-text-muted block mb-0.5"
                                                        >Arguments</span
                                                      >
                                                      <div
                                                        class="space-y-2 mt-1"
                                                      >
                                                        ${proc.parameters.map(
                                                          (p) => {
                                                            const currentValue =
                                                              (processors
                                                                .processorArgEdits[
                                                                proc.name
                                                              ] || {})[
                                                                p.name
                                                              ] !== undefined
                                                                ? (processors
                                                                    .processorArgEdits[
                                                                    proc.name
                                                                  ] || {})[
                                                                    p.name
                                                                  ]
                                                                : p.value;
                                                            return html`
                                                              <div
                                                                key=${p.name}
                                                              >
                                                                <div
                                                                  class="text-xs text-mitto-text-muted font-mono mb-0.5"
                                                                >
                                                                  ${p.name}
                                                                  ${p.description &&
                                                                  html`<span
                                                                    class="font-sans font-normal opacity-70"
                                                                  >
                                                                    —
                                                                    ${p.description}</span
                                                                  >`}
                                                                </div>
                                                                ${p.type ===
                                                                "boolean"
                                                                  ? html`<input
                                                                      type="checkbox"
                                                                      checked=${currentValue ===
                                                                      "true"}
                                                                      onChange=${(
                                                                        e,
                                                                      ) =>
                                                                        processorsSetters.setProcessorArgEdits(
                                                                          (
                                                                            prev,
                                                                          ) => ({
                                                                            ...prev,
                                                                            [proc.name]:
                                                                              {
                                                                                ...(prev[
                                                                                  proc
                                                                                    .name
                                                                                ] ||
                                                                                  {}),
                                                                                [p.name]:
                                                                                  e
                                                                                    .target
                                                                                    .checked
                                                                                    ? "true"
                                                                                    : "false",
                                                                              },
                                                                          }),
                                                                        )}
                                                                      class="checkbox checkbox-sm"
                                                                    />`
                                                                  : html`<input
                                                                      type="text"
                                                                      value=${currentValue}
                                                                      onInput=${(
                                                                        e,
                                                                      ) =>
                                                                        processorsSetters.setProcessorArgEdits(
                                                                          (
                                                                            prev,
                                                                          ) => ({
                                                                            ...prev,
                                                                            [proc.name]:
                                                                              {
                                                                                ...(prev[
                                                                                  proc
                                                                                    .name
                                                                                ] ||
                                                                                  {}),
                                                                                [p.name]:
                                                                                  e
                                                                                    .target
                                                                                    .value,
                                                                              },
                                                                          }),
                                                                        )}
                                                                      class="input input-sm w-full"
                                                                    />`}
                                                              </div>
                                                            `;
                                                          },
                                                        )}
                                                      </div>
                                                      ${(
                                                        proc.parameters || []
                                                      ).some((p) => {
                                                        const edited =
                                                          (processors
                                                            .processorArgEdits[
                                                            proc.name
                                                          ] || {})[p.name];
                                                        return (
                                                          edited !==
                                                            undefined &&
                                                          edited !== p.value
                                                        );
                                                      }) &&
                                                      html`
                                                        <button
                                                          onClick=${() =>
                                                            processorsHandlers.saveProcessorArguments(
                                                              proc,
                                                            )}
                                                          class="btn btn-primary btn-sm mt-2"
                                                        >
                                                          Save
                                                        </button>
                                                      `}
                                                    </div>
                                                  `}
                                                </div>
                                              </div>
                                            </div>
                                          `;
                                        },
                                      )}
                                </div>
                              `}
                        </div>
                      `
                    }

                    <!-- Folder Shortcuts tab -->
                    ${
                      activeTab === "shortcuts" &&
                      html`
                        <div class="space-y-4">
                          <${ShortcutsEditor}
                            sections=${SHORTCUT_SECTIONS}
                            shortcutsSections=${shortcuts.shortcutsSections}
                            sectionPrompts=${shortcuts.sectionPrompts}
                            loading=${shortcuts.shortcutsLoading}
                            error=${shortcuts.shortcutsError}
                            redundantPromptNames=${shortcuts.shortcutRedundantPromptNames}
                            onAdd=${shortcutsHandlers.addShortcutRow}
                            onUpdate=${shortcutsHandlers.updateShortcutRow}
                            onRemove=${shortcutsHandlers.removeShortcutRow}
                            onMove=${shortcutsHandlers.moveShortcutRow}
                          />
                        </div>
                      `
                    }

                    <!-- Folder Children tab -->
                    ${
                      activeTab === "children" &&
                      html`
                        <div class="space-y-5">
                          <p class="text-sm text-mitto-text-muted">
                            Configure automatic child conversations for this
                            folder.
                          </p>
                          <${AutoChildrenEditor}
                            children=${editAutoChildren}
                            workspaces=${workspaces}
                            currentWorkspaceUUID=${firstWs?.uuid}
                            onChange=${setEditAutoChildren}
                            getBasename=${getBasename}
                            modelProfiles=${modelProfiles}
                          />
                        </div>
                      `
                    }
                  </div>
  </${Fragment}>`;
}
