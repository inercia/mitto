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
import { promptParameters } from "../utils/prompts.js";

import {
  SpinnerIcon,
  FolderIcon,
  TrashIcon,
  EditIcon,
  PlusIcon,
  RobotIcon,
  GlobeIcon,
  ErrorIcon,
  SlidersIcon,
} from "./Icons.js";

import { AutoChildrenEditor } from "./SettingsDialog.js";
import { Tooltip } from "./Tooltip.js";
import { ShortcutsEditor } from "./ShortcutsEditor.js";

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

// Descriptors used by the Beads Config tab to render the label + config-key
// help table for each supported upstream task system. Kept in sync with the
// same-named constant in WorkspacesDialog.js so shell-side handlers can
// continue to reference it without depending on this file.
const BEADS_UPSTREAM_HELP = {
  github: {
    label: "GitHub",
    rows: [
      { key: "github.token", desc: "Personal access token" },
      { key: "github.owner", desc: "Repository owner" },
      { key: "github.repo", desc: "Repository name" },
      { key: "github.repository", desc: 'Combined "owner/repo" format' },
      { key: "github.url", desc: "Custom API URL (GitHub Enterprise)" },
    ],
  },
  jira: {
    label: "Jira",
    rows: [
      {
        key: "jira.url",
        desc: 'Base URL, e.g. "https://company.atlassian.net"',
      },
      { key: "jira.project", desc: 'Project key, e.g. "PROJ"' },
      {
        key: "jira.projects",
        desc: 'Multiple projects, comma-separated, e.g. "PROJ1,PROJ2"',
      },
      { key: "jira.api_token", desc: "API token" },
      { key: "jira.username", desc: "Account email (Jira Cloud)" },
      {
        key: "jira.push_prefix",
        desc: 'Only push matching issues, e.g. "hippo" or "proj1,proj2"',
      },
    ],
  },
  gitlab: {
    label: "GitLab",
    rows: [
      { key: "gitlab.url", desc: "GitLab instance URL" },
      { key: "gitlab.token", desc: "Personal access token" },
      { key: "gitlab.project_id", desc: "Project ID or path" },
      { key: "gitlab.group_id", desc: "Group ID for group-level sync" },
      {
        key: "gitlab.default_project_id",
        desc: "Project for creating issues in group mode",
      },
    ],
  },
  linear: {
    label: "Linear",
    rows: [
      { key: "linear.api_key", desc: "API key (for individual developers)" },
      { key: "linear.team_id", desc: "Team ID (UUID)" },
      {
        key: "linear.team_ids",
        desc: "Multiple team IDs, comma-separated UUIDs",
      },
      { key: "linear.project_id", desc: "Optional: sync only this project" },
      { key: "linear.id_mode", desc: 'ID generation: "hash" (default)' },
      { key: "linear.hash_length", desc: "Hash length 3-8 (default: 6)" },
    ],
  },
};

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
  // Folder header edit fields (owned by shell, still driven by handleSave)
  editName,
  setEditName,
  editCode,
  setEditCode,
  editColor,
  setEditColor,
  editGroup,
  setEditGroup,
  editAutoChildren,
  setEditAutoChildren,
  folderGroupSuggestions,
  // Metadata tab
  editMetaDescription,
  setEditMetaDescription,
  editMetaUrl,
  setEditMetaUrl,
  editMetaGroup,
  setEditMetaGroup,
  editUserDataFields,
  setEditUserDataFields,
  // Beads tab
  beadsConfig,
  beadsConfigLoading,
  beadsConfigError,
  beadsConfigSaving,
  newBeadsKey,
  setNewBeadsKey,
  newBeadsValue,
  setNewBeadsValue,
  beadsUpstream,
  beadsUpstreamSaving,
  beadsUpstreamPrompts,
  beadsUpstreamPromptsLoading,
  beadsPullPrompt,
  beadsPushPrompt,
  beadsSyncPrompt,
  beadsPullPromptArgs,
  beadsPushPromptArgs,
  beadsSyncPromptArgs,
  saveBeadsUpstream,
  saveBeadsPromptName,
  saveBeadsPromptArgs,
  setBeadsConfigKey,
  unsetBeadsConfigKey,
  // Prompts tab
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
  // Processors tab
  folderProcessors,
  processorsLoading,
  expandedProcessor,
  setExpandedProcessor,
  processorArgEdits,
  setProcessorArgEdits,
  toggleProcessorEnabled,
  saveProcessorArguments,
  // Shortcuts tab
  shortcutsSections,
  sectionPrompts,
  shortcutsLoading,
  shortcutsError,
  shortcutRedundantPromptNames,
  addShortcutRow,
  updateShortcutRow,
  removeShortcutRow,
  moveShortcutRow,
}) {
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
                              value=${editMetaDescription}
                              onInput=${(e) =>
                                setEditMetaDescription(e.target.value)}
                              placeholder="A description of this workspace/project..."
                              rows="3"
                              class="textarea textarea-sm w-full resize-vertical"
                            />
                            <label class="label" for="ws-meta-url">URL</label>
                            <input
                              id="ws-meta-url"
                              type="url"
                              value=${editMetaUrl}
                              onInput=${(e) => setEditMetaUrl(e.target.value)}
                              placeholder="https://github.com/..."
                              class="input input-sm w-full"
                            />
                            <label class="label" for="ws-meta-group"
                              >Group</label
                            >
                            <input
                              id="ws-meta-group"
                              type="text"
                              value=${editMetaGroup}
                              onInput=${(e) => setEditMetaGroup(e.target.value)}
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
                                  setEditUserDataFields((prev) => [
                                    ...prev,
                                    {
                                      name: "",
                                      type: "string",
                                      description: "",
                                    },
                                  ])}
                                class="btn btn-ghost btn-xs gap-1 tooltip tooltip-bottom"
                                data-tip="Add Field"
                              >
                                <${PlusIcon} className="w-3.5 h-3.5" />
                                Add Field
                              </button>
                            </div>
                            ${editUserDataFields.length === 0 &&
                            html`
                              <p
                                class="text-xs text-mitto-text-muted italic py-2"
                              >
                                No fields defined. Click "Add Field" to create
                                one.
                              </p>
                            `}
                            ${editUserDataFields.length > 0 &&
                            html`
                              <ul class="list">
                                ${editUserDataFields.map(
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
                                            setEditUserDataFields((prev) =>
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
                                            setEditUserDataFields((prev) =>
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
                                            setEditUserDataFields((prev) =>
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
                                            setEditUserDataFields((prev) =>
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
                      html`
                        <div class="space-y-4">
                          <p class="text-sm text-mitto-text-muted">
                            Mitto uses${" "}
                            <a
                              href="https://github.com/steveyegge/beads"
                              onClick=${(e) => {
                                e.preventDefault();
                                openExternalURL(
                                  "https://github.com/steveyegge/beads",
                                );
                              }}
                              class="text-mitto-accent hover:text-mitto-accent-300 underline cursor-pointer"
                              >beads</a
                            >${" "}(the <code>bd</code> tool) for managing
                            tasks.
                          </p>
                          <!-- Upstream task system selector (persisted in folders.json) -->
                          <fieldset class="fieldset pt-2">
                            <legend class="fieldset-legend">
                              Upstream Tasks
                            </legend>
                            <p class="text-xs text-mitto-text-muted">
                              Select the external task system beads syncs with.
                              When set, Pull/Push/Sync actions appear in the
                              Tasks view for this folder.
                            </p>
                            <select
                              value=${beadsUpstream}
                              onInput=${(e) =>
                                saveBeadsUpstream(e.target.value)}
                              disabled=${beadsUpstreamSaving}
                              class="select select-sm w-full max-w-md disabled:opacity-50"
                            >
                              <option value="none">None</option>
                              <option value="jira">Jira</option>
                              <option value="github">GitHub</option>
                              <option value="gitlab">GitLab</option>
                              <option value="linear">Linear</option>
                              <option value="prompts">Prompts</option>
                            </select>
                          </fieldset>

                          ${beadsUpstream !== "none" &&
                          BEADS_UPSTREAM_HELP[beadsUpstream] &&
                          html`
                            <div
                              class="p-3 bg-mitto-input-box border border-mitto-border rounded-md"
                            >
                              <p class="text-xs text-mitto-text-muted mb-2">
                                Recommended
                                ${BEADS_UPSTREAM_HELP[beadsUpstream].label}
                                keys${" "} (click a key to fill the add-key
                                field below):
                              </p>
                              <div class="space-y-1">
                                ${BEADS_UPSTREAM_HELP[beadsUpstream].rows.map(
                                  (row) => html`
                                    <div
                                      key=${row.key}
                                      class="flex items-baseline gap-2 text-xs"
                                    >
                                      <button
                                        type="button"
                                        onClick=${() => setNewBeadsKey(row.key)}
                                        class="font-mono text-mitto-accent hover:text-mitto-accent-300 hover:underline whitespace-nowrap tooltip tooltip-bottom"
                                        data-tip="Use this key in the add-key field below"
                                      >
                                        ${row.key}
                                      </button>
                                      <span class="text-mitto-text-muted"
                                        >— ${row.desc}</span
                                      >
                                    </div>
                                  `,
                                )}
                              </div>
                            </div>
                          `}
                          ${beadsUpstream === "prompts" &&
                          html`
                            <fieldset class="fieldset pt-2">
                              <legend class="fieldset-legend">
                                Prompt Actions
                              </legend>
                              <p class="label">
                                Choose an enabled prompt for each button. Use
                                the sliders button to configure arguments for
                                parametrized prompts.
                              </p>
                              ${beadsUpstreamPromptsLoading
                                ? html`<div
                                    class="flex items-center gap-2 text-sm text-mitto-text-muted"
                                  >
                                    <${SpinnerIcon}
                                      className="w-4 h-4 animate-spin"
                                    />
                                    Loading prompts…
                                  </div>`
                                : html`
                                    <div class="space-y-2 pt-1">
                                      ${[
                                        {
                                          label: "Pull",
                                          field: "pull_prompt",
                                          value: beadsPullPrompt,
                                          args: beadsPullPromptArgs,
                                        },
                                        {
                                          label: "Push",
                                          field: "push_prompt",
                                          value: beadsPushPrompt,
                                          args: beadsPushPromptArgs,
                                        },
                                        {
                                          label: "Sync",
                                          field: "sync_prompt",
                                          value: beadsSyncPrompt,
                                          args: beadsSyncPromptArgs,
                                        },
                                      ].map(({ label, field, value, args }) => {
                                        const selectedPrompt = value
                                          ? beadsUpstreamPrompts.find(
                                              (p) => p.name === value,
                                            )
                                          : null;
                                        const params = selectedPrompt
                                          ? promptParameters(selectedPrompt)
                                          : [];
                                        const canEditArgs =
                                          !!value && params.length > 0;
                                        const argsDisabled =
                                          !canEditArgs || beadsUpstreamSaving;
                                        return html`
                                          <div
                                            key=${field}
                                            class="flex items-center gap-2 max-w-md"
                                          >
                                            <span
                                              class="text-xs text-mitto-text-secondary"
                                              style="min-width: 2.5rem"
                                              >${label}</span
                                            >
                                            <select
                                              value=${beadsUpstreamPrompts.some(
                                                (p) => p.name === value,
                                              )
                                                ? value
                                                : ""}
                                              onInput=${(e) =>
                                                saveBeadsPromptName(
                                                  field,
                                                  e.target.value,
                                                )}
                                              disabled=${beadsUpstreamSaving}
                                              class="select select-sm flex-1 disabled:opacity-50"
                                            >
                                              <option value="">— none —</option>
                                              ${beadsUpstreamPrompts.map(
                                                (p) => html`
                                                  <option
                                                    key=${p.name}
                                                    value=${p.name}
                                                  >
                                                    ${p.name}
                                                  </option>
                                                `,
                                              )}
                                            </select>
                                            <button
                                              type="button"
                                              onClick=${() => {
                                                if (
                                                  !canEditArgs ||
                                                  !onOpenPromptParamDialog ||
                                                  !selectedPrompt
                                                )
                                                  return;
                                                onOpenPromptParamDialog(
                                                  selectedPrompt,
                                                  params,
                                                  async (userArgs) => {
                                                    await saveBeadsPromptArgs(
                                                      field,
                                                      userArgs,
                                                    );
                                                  },
                                                  { initialValues: args || {} },
                                                );
                                              }}
                                              disabled=${argsDisabled}
                                              class="shrink-0 p-1.5 rounded border border-mitto-border dark:border-mitto-border-2 bg-white dark:bg-mitto-surface-2 transition-colors ${argsDisabled
                                                ? "opacity-50 cursor-not-allowed"
                                                : "cursor-pointer hover:bg-mitto-surface-hover dark:hover:bg-mitto-surface-3"}"
                                              aria-label=${`Set ${label.toLowerCase()} prompt arguments`}
                                              data-testid=${`beads-${field}-args-btn`}
                                            >
                                              <${SlidersIcon}
                                                className="w-4 h-4 text-mitto-text-secondary"
                                              />
                                            </button>
                                          </div>
                                        `;
                                      })}
                                    </div>
                                  `}
                            </fieldset>
                          `}

                          <div class="pt-2 border-t border-mitto-border"></div>

                          <p class="text-xs text-mitto-text-muted">
                            Integration settings stored in this folder's beads
                            database via${" "}
                            <span class="font-mono text-mitto-text-muted"
                              >bd config</span
                            >. Use namespaced keys such as${" "}
                            <span class="font-mono text-mitto-text-muted"
                              >jira.url</span
                            >,${" "}
                            <span class="font-mono text-mitto-text-muted"
                              >github.repo</span
                            >, or${" "}
                            <span class="font-mono text-mitto-text-muted"
                              >${"custom.<key>"}</span
                            >.
                          </p>

                          ${beadsConfigError &&
                          html`
                            <div
                              role="alert"
                              class="alert alert-warning alert-soft text-xs"
                            >
                              ${beadsConfigError}
                            </div>
                          `}
                          ${beadsConfigLoading
                            ? html`<div
                                class="flex items-center gap-2 text-sm text-mitto-text-muted"
                              >
                                <${SpinnerIcon}
                                  className="w-4 h-4 animate-spin"
                                />
                                Loading…
                              </div>`
                            : beadsConfig &&
                              html`
                                ${(() => {
                                  const editable = Object.entries(
                                    beadsConfig,
                                  ).filter(([k]) => k.includes("."));
                                  const system = Object.entries(
                                    beadsConfig,
                                  ).filter(([k]) => !k.includes("."));
                                  return html`
                                    <div class="space-y-2">
                                      ${editable.length === 0
                                        ? html`<p
                                            class="text-xs text-mitto-text-muted italic"
                                          >
                                            No integration keys set yet.
                                          </p>`
                                        : editable.map(
                                            ([k, v]) => html`
                                              <div
                                                key=${k}
                                                class="flex gap-2 items-center"
                                              >
                                                <input
                                                  type="text"
                                                  value=${k}
                                                  readonly
                                                  class="input input-sm font-mono cursor-default"
                                                  style="width: 38%; height: 38px; box-sizing: border-box"
                                                />
                                                <input
                                                  key=${k + ":" + v}
                                                  type="text"
                                                  defaultValue=${v}
                                                  disabled=${beadsConfigSaving}
                                                  onBlur=${(e) => {
                                                    if (e.target.value !== v)
                                                      setBeadsConfigKey(
                                                        k,
                                                        e.target.value,
                                                      );
                                                  }}
                                                  class="input input-sm flex-1 font-mono"
                                                  style="height: 38px; box-sizing: border-box"
                                                />
                                                <button
                                                  onClick=${() => {
                                                    if (beadsConfigSaving)
                                                      return;
                                                    unsetBeadsConfigKey(k);
                                                  }}
                                                  aria-disabled=${beadsConfigSaving
                                                    ? "true"
                                                    : "false"}
                                                  class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${beadsConfigSaving
                                                    ? "opacity-40 pointer-events-none"
                                                    : ""}"
                                                  data-tip="Delete this key"
                                                  aria-label="Delete this key"
                                                  style="height: 38px; box-sizing: border-box"
                                                >
                                                  <${TrashIcon}
                                                    className="w-4 h-4"
                                                  />
                                                </button>
                                              </div>
                                            `,
                                          )}

                                      <!-- Add a new key -->
                                      <div class="flex gap-2 items-center">
                                        <input
                                          type="text"
                                          value=${newBeadsKey}
                                          onInput=${(e) =>
                                            setNewBeadsKey(e.target.value)}
                                          placeholder="jira.url"
                                          class="input input-sm font-mono"
                                          style="width: 38%; height: 38px; box-sizing: border-box"
                                        />
                                        <input
                                          type="text"
                                          value=${newBeadsValue}
                                          onInput=${(e) =>
                                            setNewBeadsValue(e.target.value)}
                                          placeholder="value"
                                          class="input input-sm flex-1 font-mono"
                                          style="height: 38px; box-sizing: border-box"
                                        />
                                        <button
                                          onClick=${async () => {
                                            const key = newBeadsKey.trim();
                                            if (!key) return;
                                            if (beadsConfigSaving) return;
                                            await setBeadsConfigKey(
                                              key,
                                              newBeadsValue,
                                            );
                                            setNewBeadsKey("");
                                            setNewBeadsValue("");
                                          }}
                                          aria-disabled=${beadsConfigSaving ||
                                          !newBeadsKey.trim()
                                            ? "true"
                                            : "false"}
                                          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${beadsConfigSaving ||
                                          !newBeadsKey.trim()
                                            ? "opacity-40 pointer-events-none"
                                            : ""}"
                                          data-tip="Add key"
                                          aria-label="Add key"
                                          style="height: 38px; box-sizing: border-box"
                                        >
                                          <${PlusIcon} className="w-4 h-4" />
                                        </button>
                                      </div>
                                    </div>

                                    ${system.length > 0 &&
                                    html`
                                      <fieldset class="fieldset pt-2 mt-4">
                                        <legend class="fieldset-legend">
                                          System
                                        </legend>
                                        <p class="label">
                                          Operational beads settings (read-only
                                          here; edit via the bd CLI).
                                        </p>
                                        <div class="space-y-1">
                                          ${system.map(
                                            ([k, v]) => html`
                                              <div
                                                key=${k}
                                                class="flex gap-2 text-xs font-mono text-mitto-text-muted"
                                              >
                                                <span
                                                  class="truncate"
                                                  style="width: 38%"
                                                  >${k}</span
                                                >
                                                <span class="flex-1 truncate"
                                                  >${String(v)}</span
                                                >
                                              </div>
                                            `,
                                          )}
                                        </div>
                                      </fieldset>
                                    `}
                                  `;
                                })()}
                              `}
                        </div>
                      `
                    }

                    <!-- Folder Prompts tab -->
                    ${
                      activeTab === "prompts" &&
                      html`
                        <div class="space-y-4">
                          <div class="flex items-center justify-between">
                            <p class="text-sm text-mitto-text-muted">
                              Manage prompts for this workspace. Built-in
                              prompts are read-only but can be disabled.
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
                              <legend class="fieldset-legend">
                                New Prompt
                              </legend>
                              <label class="label" for="new-prompt-name"
                                >Button Label</label
                              >
                              <input
                                id="new-prompt-name"
                                type="text"
                                value=${newPromptName}
                                onInput=${(e) =>
                                  setNewPromptName(e.target.value)}
                                placeholder="e.g., Continue"
                                class="input input-sm w-full"
                              />
                              <label class="label" for="new-prompt-text"
                                >Prompt Text</label
                              >
                              <textarea
                                id="new-prompt-text"
                                value=${newPromptText}
                                onInput=${(e) =>
                                  setNewPromptText(e.target.value)}
                                placeholder="e.g., Please continue with the current task."
                                rows="8"
                                class="textarea textarea-sm w-full resize-y"
                              />
                              <label class="label" for="new-prompt-group"
                                >Group (optional)</label
                              >
                              <input
                                id="new-prompt-group"
                                type="text"
                                value=${newPromptGroup}
                                onInput=${(e) =>
                                  setNewPromptGroup(e.target.value)}
                                placeholder="e.g., Tasks, Code Quality"
                                class="input input-sm w-full"
                              />
                              <label class="label"
                                >Background Color (optional)</label
                              >
                              <div class="flex items-center gap-2">
                                <input
                                  type="color"
                                  value=${newPromptColor || "#334155"}
                                  onInput=${(e) =>
                                    setNewPromptColor(e.target.value)}
                                  class="w-10 h-10 rounded cursor-pointer border border-mitto-border-2"
                                />
                                <input
                                  type="text"
                                  value=${newPromptColor}
                                  onInput=${(e) =>
                                    setNewPromptColor(e.target.value)}
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
                                      backgroundColor:
                                        newPromptColor || undefined,
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
                            ? html`<div
                                class="flex items-center justify-center p-4"
                              >
                                <${SpinnerIcon}
                                  className="w-5 h-5 animate-spin"
                                />
                              </div>`
                            : html`
                                <ul class="list">
                                  ${folderPrompts.length === 0
                                    ? html`<li class="list-row">
                                        <div
                                          class="p-4 text-center text-mitto-text-muted text-sm"
                                        >
                                          No prompts found. Click + to add a
                                          workspace prompt.
                                        </div>
                                      </li>`
                                    : [...folderPrompts]
                                        .sort((a, b) =>
                                          (a.name || "").localeCompare(
                                            b.name || "",
                                          ),
                                        )
                                        .map((prompt, idx) => {
                                          const isBuiltin =
                                            prompt.source === "builtin" ||
                                            prompt.source === "file";
                                          const isEnabled =
                                            prompt.enabled !== false;
                                          return html`
                                            <li
                                              key=${prompt.name}
                                              class="list-row p-0"
                                            >
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
                                                      onChange=${() =>
                                                        togglePromptEnabled(
                                                          prompt,
                                                        )}
                                                      onClick=${(e) =>
                                                        e.stopPropagation()}
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
                                                    <div
                                                      class="flex items-center gap-2"
                                                    >
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
                                                        ${isBuiltin
                                                          ? "built-in"
                                                          : "workspace"}
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
                                                      ${prompt.prompt.slice(
                                                        0,
                                                        80,
                                                      )}${prompt.prompt.length >
                                                      80
                                                        ? "..."
                                                        : ""}
                                                    </p>`}
                                                  </div>
                                                  <div
                                                    class="flex items-center gap-1 shrink-0"
                                                    onClick=${(e) =>
                                                      e.stopPropagation()}
                                                  >
                                                    <button
                                                      onClick=${() => {
                                                        if (
                                                          editingPromptIndex ===
                                                          idx
                                                        ) {
                                                          setEditingPromptIndex(
                                                            null,
                                                          );
                                                        } else {
                                                          setEditPromptName(
                                                            prompt.name || "",
                                                          );
                                                          setEditPromptText(
                                                            prompt.prompt || "",
                                                          );
                                                          setEditPromptColor(
                                                            prompt.backgroundColor ||
                                                              "",
                                                          );
                                                          setEditPromptGroup(
                                                            prompt.group || "",
                                                          );
                                                          setEditingPromptIndex(
                                                            idx,
                                                          );
                                                        }
                                                      }}
                                                      class="btn btn-ghost btn-square btn-xs tooltip tooltip-bottom"
                                                      data-tip=${isBuiltin
                                                        ? "View"
                                                        : "Edit"}
                                                      aria-label=${isBuiltin
                                                        ? "View"
                                                        : "Edit"}
                                                    >
                                                      <${EditIcon}
                                                        className="w-4 h-4 text-mitto-text-muted"
                                                      />
                                                    </button>
                                                    ${!isBuiltin &&
                                                    html`
                                                      <button
                                                        onClick=${() =>
                                                          deleteWorkspacePrompt(
                                                            prompt.name,
                                                          )}
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
                                                <div
                                                  class="collapse-content px-3 pb-3"
                                                >
                                                  <fieldset
                                                    class="fieldset pt-2"
                                                  >
                                                    <legend
                                                      class="fieldset-legend"
                                                    >
                                                      ${isBuiltin
                                                        ? "View Prompt"
                                                        : "Edit Prompt"}
                                                    </legend>
                                                    <label
                                                      class="label"
                                                      for=${"edit-prompt-name-" +
                                                      idx}
                                                      >Button Label</label
                                                    >
                                                    <input
                                                      id=${"edit-prompt-name-" +
                                                      idx}
                                                      type="text"
                                                      value=${isBuiltin
                                                        ? prompt.name
                                                        : editPromptName}
                                                      onInput=${(e) =>
                                                        !isBuiltin &&
                                                        setEditPromptName(
                                                          e.target.value,
                                                        )}
                                                      disabled=${isBuiltin}
                                                      class="input input-sm w-full ${isBuiltin
                                                        ? "opacity-60 cursor-not-allowed"
                                                        : ""}"
                                                    />
                                                    <label
                                                      class="label"
                                                      for=${"edit-prompt-text-" +
                                                      idx}
                                                      >Prompt Text</label
                                                    >
                                                    <textarea
                                                      id=${"edit-prompt-text-" +
                                                      idx}
                                                      rows="8"
                                                      value=${isBuiltin
                                                        ? prompt.prompt
                                                        : editPromptText}
                                                      onInput=${(e) =>
                                                        !isBuiltin &&
                                                        setEditPromptText(
                                                          e.target.value,
                                                        )}
                                                      disabled=${isBuiltin}
                                                      class="textarea textarea-sm w-full resize-y ${isBuiltin
                                                        ? "opacity-60 cursor-not-allowed"
                                                        : ""}"
                                                    />
                                                    <label
                                                      class="label"
                                                      for=${"edit-prompt-group-" +
                                                      idx}
                                                      >Group (optional)</label
                                                    >
                                                    <input
                                                      id=${"edit-prompt-group-" +
                                                      idx}
                                                      type="text"
                                                      value=${isBuiltin
                                                        ? prompt.group || ""
                                                        : editPromptGroup}
                                                      onInput=${(e) =>
                                                        !isBuiltin &&
                                                        setEditPromptGroup(
                                                          e.target.value,
                                                        )}
                                                      disabled=${isBuiltin}
                                                      placeholder="e.g., Tasks, Code Quality"
                                                      class="input input-sm w-full ${isBuiltin
                                                        ? "opacity-60 cursor-not-allowed"
                                                        : ""}"
                                                    />
                                                    ${!isBuiltin &&
                                                    html`
                                                      <label class="label"
                                                        >Background Color
                                                        (optional)</label
                                                      >
                                                      <div
                                                        class="flex items-center gap-2"
                                                      >
                                                        <input
                                                          type="color"
                                                          value=${editPromptColor ||
                                                          "#334155"}
                                                          onInput=${(e) =>
                                                            setEditPromptColor(
                                                              e.target.value,
                                                            )}
                                                          class="w-8 h-8 rounded cursor-pointer border border-mitto-border-2"
                                                        />
                                                        <input
                                                          type="text"
                                                          value=${editPromptColor}
                                                          onInput=${(e) =>
                                                            setEditPromptColor(
                                                              e.target.value,
                                                            )}
                                                          placeholder="#E8F5E9"
                                                          class="input input-sm flex-1 font-mono"
                                                        />
                                                      </div>
                                                    `}
                                                    <div
                                                      class="flex justify-end gap-2 mt-2"
                                                    >
                                                      <button
                                                        onClick=${() =>
                                                          setEditingPromptIndex(
                                                            null,
                                                          )}
                                                        class="btn btn-ghost btn-sm"
                                                      >
                                                        ${isBuiltin
                                                          ? "Close"
                                                          : "Cancel"}
                                                      </button>
                                                      ${!isBuiltin &&
                                                      html`
                                                        <button
                                                          onClick=${async () => {
                                                            await saveWorkspacePrompt(
                                                              {
                                                                name: editPromptName.trim(),
                                                                prompt:
                                                                  editPromptText.trim(),
                                                                backgroundColor:
                                                                  editPromptColor ||
                                                                  undefined,
                                                                group:
                                                                  editPromptGroup.trim() ||
                                                                  undefined,
                                                                enabled:
                                                                  prompt.enabled !==
                                                                  false,
                                                              },
                                                            );
                                                            setEditingPromptIndex(
                                                              null,
                                                            );
                                                          }}
                                                          disabled=${!editPromptName.trim() ||
                                                          !editPromptText.trim() ||
                                                          promptSaving}
                                                          class="btn btn-primary btn-sm"
                                                        >
                                                          ${promptSaving
                                                            ? "Saving..."
                                                            : "Save"}
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
                      `
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

                          ${processorsLoading
                            ? html`<div
                                class="flex items-center justify-center p-4"
                              >
                                <${SpinnerIcon}
                                  className="w-5 h-5 animate-spin"
                                />
                              </div>`
                            : html`
                                <div class="space-y-2">
                                  ${folderProcessors.length === 0
                                    ? html`<div
                                        class="p-4 text-center text-mitto-text-muted text-sm"
                                      >
                                        No processors found for this workspace.
                                      </div>`
                                    : folderProcessors.map((proc) => {
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
                                          expandedProcessor === proc.name;
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
                                                setExpandedProcessor(
                                                  isExpanded ? null : proc.name,
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
                                                      toggleProcessorEnabled(
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
                                                    <p class="text-mitto-text">
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
                                                    <div class="space-y-2 mt-1">
                                                      ${proc.parameters.map(
                                                        (p) => {
                                                          const currentValue =
                                                            (processorArgEdits[
                                                              proc.name
                                                            ] || {})[p.name] !==
                                                            undefined
                                                              ? (processorArgEdits[
                                                                  proc.name
                                                                ] || {})[p.name]
                                                              : p.value;
                                                          return html`
                                                            <div key=${p.name}>
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
                                                                      setProcessorArgEdits(
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
                                                                      setProcessorArgEdits(
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
                                                        (processorArgEdits[
                                                          proc.name
                                                        ] || {})[p.name];
                                                      return (
                                                        edited !== undefined &&
                                                        edited !== p.value
                                                      );
                                                    }) &&
                                                    html`
                                                      <button
                                                        onClick=${() =>
                                                          saveProcessorArguments(
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
                                      })}
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
                            shortcutsSections=${shortcutsSections}
                            sectionPrompts=${sectionPrompts}
                            loading=${shortcutsLoading}
                            error=${shortcutsError}
                            redundantPromptNames=${shortcutRedundantPromptNames}
                            onAdd=${addShortcutRow}
                            onUpdate=${updateShortcutRow}
                            onRemove=${removeShortcutRow}
                            onMove=${moveShortcutRow}
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
