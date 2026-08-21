// Mitto Web Interface — Workspaces Dialog · Left Panel
//
// Renders the workspace/folder list (scroll pane) and its bottom toolbar
// (Add Folder / Delete / Duplicate / Add Server / Collapse / Expand).
// Behavior-preserving extraction from WorkspacesDialog.js: all state and
// handlers still live in the shell and are received as props (pragmatic
// pass-through per mitto-90f.4 plan). Pure render function — no hooks.
const { html } = window.preact;

import {
  SpinnerIcon,
  FolderIcon,
  TrashIcon,
  DuplicateIcon,
  ChevronRightIcon,
  ChevronDownIcon,
  ExpandIcon,
  CollapseIcon,
  ServerIcon,
} from "./Icons.js";

import { WorkspaceBadge } from "./WorkspaceBadge.js";

export function WorkspacesLeftPanel({
  scrollContainerRef,
  loading,
  workspaces,
  groupedWorkspaces,
  expandedFolders,
  selectedFolder,
  selectedWorkspaceKey,
  acpServers,
  leftPanelWidth,
  isNewFolderIncomplete,
  folderCanAddServer,
  setSelectedFolder,
  setSelectedWorkspaceKey,
  guardNewFolder,
  toggleFolder,
  expandFolder,
  getWorkspaceKey,
  addWorkspace,
  removeWorkspace,
  duplicateWorkspace,
  addServerToFolder,
  collapseAllFolders,
  expandAllFolders,
}) {
  return html`
    <div class="shrink-0 flex flex-col" style="width: ${leftPanelWidth}px">
      <div
        ref=${scrollContainerRef}
        class="flex-1 overflow-y-auto p-3 space-y-0.5"
      >
        ${loading
          ? html`<div class="flex items-center justify-center py-8">
              <${SpinnerIcon} className="w-6 h-6 text-mitto-accent" />
            </div>`
          : workspaces.length === 0
            ? html`<div
                class="text-center py-8 text-mitto-text-muted text-sm px-2"
              >
                <${FolderIcon} className="w-8 h-8 mx-auto mb-2 opacity-40" />
                <p>No workspaces.</p>
                <p class="text-xs mt-1">
                  Click the folder icon below to add one.
                </p>
              </div>`
            : groupedWorkspaces.map(({ displayName, workspaces: wsGroup }) => {
                const isFolderSelected =
                  selectedFolder === displayName && !selectedWorkspaceKey;
                const isExpanded = expandedFolders[displayName] !== false;
                return html`
                  <div key=${displayName} class="mb-0.5">
                    <!-- Folder header -->
                    <div
                      data-folder-name=${displayName}
                      class="group flex items-center gap-2 px-3 py-1 rounded-sm cursor-pointer transition-colors ${isFolderSelected
                        ? "bg-mitto-accent-500/10"
                        : "hover:bg-base-200/40"}"
                      onClick=${() =>
                        guardNewFolder(() => {
                          if (isFolderSelected) {
                            toggleFolder(displayName);
                          } else {
                            setSelectedFolder(displayName);
                            setSelectedWorkspaceKey(null);
                            expandFolder(displayName);
                          }
                        })}
                    >
                      <span
                        class="shrink-0 flex items-center cursor-pointer"
                        role="button"
                        aria-label=${isExpanded
                          ? "Collapse folder"
                          : "Expand folder"}
                        onClick=${(e) => {
                          e.stopPropagation();
                          toggleFolder(displayName);
                        }}
                      >
                        ${isExpanded
                          ? html`<${ChevronDownIcon}
                              className="w-3.5 h-3.5 text-mitto-text-muted"
                            />`
                          : html`<${ChevronRightIcon}
                              className="w-3.5 h-3.5 text-mitto-text-muted"
                            />`}
                      </span>
                      <${FolderIcon}
                        className="w-4 h-4 text-mitto-text-muted shrink-0"
                      />
                      <span
                        class="text-sm font-medium truncate flex-1"
                        title=${wsGroup[0]?.working_dir || "No folder selected"}
                        >${displayName}</span
                      >
                      <span class="text-xs text-mitto-text-muted"
                        >${wsGroup.length}</span
                      >
                    </div>
                    <!-- Workspace children -->
                    ${isExpanded
                      ? html`
                          <div
                            class="ml-4 pl-3 border-l border-mitto-border mt-0.5"
                          >
                            ${wsGroup.map((ws) => {
                              const key = getWorkspaceKey(ws);
                              const isSelected = key === selectedWorkspaceKey;
                              return html`
                                <div
                                  key=${key}
                                  class="group flex items-center gap-2 px-3 py-1 cursor-pointer transition-colors ${isSelected
                                    ? "bg-mitto-accent-500/20"
                                    : "hover:bg-base-200/40"}"
                                  onClick=${() =>
                                    guardNewFolder(() => {
                                      setSelectedWorkspaceKey(key);
                                      setSelectedFolder(null);
                                    })}
                                >
                                  <${WorkspaceBadge}
                                    path=${ws.working_dir}
                                    customColor=${ws.color}
                                    customCode=${ws.code}
                                    customName=${ws.name}
                                    size="sm"
                                  />
                                  <span class="text-sm truncate flex-1"
                                    >${ws.acp_server}</span
                                  >
                                </div>
                              `;
                            })}
                          </div>
                        `
                      : ""}
                  </div>
                `;
              })}
      </div>

      <!-- Toolbar: Add Folder / Delete / Duplicate / Add Server -->
      <div
        class="flex items-center justify-end gap-1 px-3 py-2 border-t border-mitto-border"
      >
        <button
          onClick=${addWorkspace}
          aria-disabled=${acpServers.length === 0 || isNewFolderIncomplete
            ? "true"
            : "false"}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${acpServers.length ===
            0 || isNewFolderIncomplete
            ? "opacity-40 pointer-events-none"
            : ""}"
          data-tip="Add folder"
          aria-label="Add folder"
        >
          <${FolderIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${() =>
            selectedWorkspaceKey && removeWorkspace(selectedWorkspaceKey)}
          aria-disabled=${!selectedWorkspaceKey ||
          selectedFolder ||
          workspaces.length <= 1
            ? "true"
            : "false"}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${!selectedWorkspaceKey ||
          selectedFolder ||
          workspaces.length <= 1
            ? "opacity-40 pointer-events-none"
            : ""}"
          data-tip="Delete selected ACP server"
          aria-label="Delete selected ACP server"
        >
          <${TrashIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${() =>
            selectedWorkspaceKey && duplicateWorkspace(selectedWorkspaceKey)}
          aria-disabled=${!selectedWorkspaceKey ? "true" : "false"}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${!selectedWorkspaceKey
            ? "opacity-40 pointer-events-none"
            : ""}"
          data-tip="Duplicate selected workspace"
          aria-label="Duplicate selected workspace"
        >
          <${DuplicateIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${addServerToFolder}
          aria-disabled=${!selectedFolder || !folderCanAddServer
            ? "true"
            : "false"}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${!selectedFolder ||
          !folderCanAddServer
            ? "opacity-40 pointer-events-none"
            : ""}"
          data-tip="Add ACP server to folder"
          aria-label="Add ACP server to folder"
        >
          <${ServerIcon} className="w-4 h-4" />
        </button>
        <div
          class="h-5 border-l border-mitto-border mx-1"
          aria-hidden="true"
        ></div>
        <button
          onClick=${collapseAllFolders}
          aria-disabled=${groupedWorkspaces.length === 0 ? "true" : "false"}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${groupedWorkspaces.length ===
          0
            ? "opacity-40 pointer-events-none"
            : ""}"
          data-tip="Collapse all"
          aria-label="Collapse all folders"
        >
          <${CollapseIcon} className="w-4 h-4" />
        </button>
        <button
          onClick=${expandAllFolders}
          aria-disabled=${groupedWorkspaces.length === 0 ? "true" : "false"}
          class="btn btn-ghost btn-square btn-sm tooltip tooltip-bottom ${groupedWorkspaces.length ===
          0
            ? "opacity-40 pointer-events-none"
            : ""}"
          data-tip="Expand all"
          aria-label="Expand all folders"
        >
          <${ExpandIcon} className="w-4 h-4" />
        </button>
      </div>
    </div>
  `;
}
