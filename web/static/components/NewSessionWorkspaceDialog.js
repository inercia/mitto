// Mitto Web Interface - New Session Workspace Dialog Component
const { html, useState, useEffect, useMemo, useRef, useCallback } = window.preact;

import { getBasename } from "../lib.js";
import { WorkspaceBadge } from "./WorkspaceBadge.js";
import { Modal } from "./Modal.js";

// =============================================================================
// New Session Workspace Selection Dialog
// =============================================================================

// Threshold for showing filter UI and max items with keyboard shortcuts
// When workspace count exceeds this, filter input is shown and only first N items get number keys
const WORKSPACE_FILTER_THRESHOLD = 5;

// Helper to get parent directory from a path
function getParentDir(path) {
  if (!path) return "";
  const normalized = path.replace(/\\/g, "/").replace(/\/$/, "");
  const lastSlash = normalized.lastIndexOf("/");
  return lastSlash > 0 ? normalized.substring(0, lastSlash) : "/";
}

// Helper to get/set folder expansion state from localStorage
function getFolderExpansionState(folderId) {
  try {
    const state = localStorage.getItem(`workspace-folder-${folderId}`);
    return state === null ? true : state === "true"; // Default to expanded
  } catch (e) {
    return true;
  }
}

function setFolderExpansionState(folderId, expanded) {
  try {
    localStorage.setItem(`workspace-folder-${folderId}`, String(expanded));
  } catch (e) {
    // Ignore localStorage errors
  }
}

export function NewSessionWorkspaceDialog({ isOpen, workspaces, onSelect, onCancel }) {
  const [filterText, setFilterText] = useState("");
  const [expandedFolders, setExpandedFolders] = useState({});
  const filterInputRef = useRef(null);

  // Show filter only when there are more than WORKSPACE_FILTER_THRESHOLD workspaces
  const showFilter = workspaces.length > WORKSPACE_FILTER_THRESHOLD;

  // Group workspaces by working_dir (matching conversations list behavior)
  const groupedWorkspaces = useMemo(() => {
    const groups = new Map();

    workspaces.forEach((ws) => {
      const workingDir = ws.working_dir || "Unknown";

      if (!groups.has(workingDir)) {
        groups.set(workingDir, []);
      }
      groups.get(workingDir).push(ws);
    });

    // Sort workspaces within each group by ACP server name
    groups.forEach((wsArray) => {
      wsArray.sort((a, b) => {
        return (a.acp_server || "").localeCompare(b.acp_server || "");
      });
    });

    // Convert to array and sort by workspace folder name (basename)
    return Array.from(groups.entries())
      .sort(([dirA], [dirB]) => {
        const nameA = dirA ? getBasename(dirA) : "Unknown";
        const nameB = dirB ? getBasename(dirB) : "Unknown";
        return nameA.localeCompare(nameB);
      })
      .map(([workingDir, wsArray]) => ({
        workingDir,
        label: workingDir ? getBasename(workingDir) : "Unknown",
        workspaces: wsArray,
      }));
  }, [workspaces]);

  // Initialize expanded state from localStorage when dialog opens
  useEffect(() => {
    if (isOpen) {
      const initialExpanded = {};
      groupedWorkspaces.forEach(({ workingDir }) => {
        initialExpanded[workingDir] = getFolderExpansionState(workingDir);
      });
      setExpandedFolders(initialExpanded);
    }
  }, [isOpen, groupedWorkspaces]);

  // Filter workspaces based on filter text (match against name, path, and ACP server)
  const filteredGroups = useMemo(() => {
    if (!filterText.trim()) return groupedWorkspaces;

    const lowerFilter = filterText.toLowerCase();

    return groupedWorkspaces
      .map(({ workingDir, label, workspaces: wsArray }) => {
        const filtered = wsArray.filter((ws) => {
          const displayName = ws.name || getBasename(ws.working_dir);
          const matchName = displayName.toLowerCase().includes(lowerFilter);
          const matchPath = ws.working_dir.toLowerCase().includes(lowerFilter);
          const matchServer = (ws.acp_server || "")
            .toLowerCase()
            .includes(lowerFilter);
          return matchName || matchPath || matchServer;
        });

        return { workingDir, label, workspaces: filtered };
      })
      .filter(({ workspaces: wsArray }) => wsArray.length > 0);
  }, [groupedWorkspaces, filterText]);

  // Flatten filtered groups for keyboard shortcuts
  const flatFilteredWorkspaces = useMemo(() => {
    return filteredGroups.flatMap(({ workspaces: wsArray }) => wsArray);
  }, [filteredGroups]);

  // Reset filter when dialog opens
  useEffect(() => {
    if (isOpen) {
      setFilterText("");
    }
  }, [isOpen]);

  // Auto-focus filter input when dialog opens (only if filter is shown)
  useEffect(() => {
    if (isOpen && showFilter && filterInputRef.current) {
      // Focus immediately and also after a delay to win against competing focus events
      filterInputRef.current?.focus();
      // Additional delayed focus to handle cases where other handlers steal focus
      const timerId = setTimeout(() => {
        filterInputRef.current?.focus();
      }, 100);
      return () => clearTimeout(timerId);
    }
  }, [isOpen, showFilter]);

  // Handle keyboard shortcuts (1-5) to select workspaces
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e) => {
      const key = e.key;

      // Number keys 1-N for quick selection (N = WORKSPACE_FILTER_THRESHOLD)
      // Only trigger if filter is empty (so typing numbers goes to filter when there's text)
      // Check both React state and DOM value to handle race conditions with state updates
      const maxShortcut = String(WORKSPACE_FILTER_THRESHOLD);
      const filterInputHasValue = filterInputRef.current?.value?.length > 0;
      const filterIsEmpty = !filterText && !filterInputHasValue;

      if (key >= "1" && key <= maxShortcut && filterIsEmpty) {
        const index = parseInt(key, 10) - 1;
        if (index < flatFilteredWorkspaces.length) {
          e.preventDefault();
          onSelect(flatFilteredWorkspaces[index]);
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, flatFilteredWorkspaces, filterText, onSelect, onCancel]);

  // Toggle folder expansion
  const toggleFolder = useCallback((workingDir) => {
    setExpandedFolders((prev) => {
      const newExpanded = !prev[workingDir];
      setFolderExpansionState(workingDir, newExpanded);
      return { ...prev, [workingDir]: newExpanded };
    });
  }, []);

  if (!isOpen) return null;

  // Help text varies based on whether filter is shown
  const helpText = showFilter
    ? `Type to filter, or press 1-${WORKSPACE_FILTER_THRESHOLD} to select.`
    : "Click on a workspace or press its number to select it.";

  // Track global index for keyboard shortcuts
  let globalIndex = 0;

  const footer = html`
    <button type="button" onClick=${onCancel} class="btn btn-sm btn-ghost">Cancel</button>
  `;

  return html`
    <${Modal}
      isOpen=${isOpen}
      onClose=${onCancel}
      title="Select Workspace"
      footer=${footer}
    >
      <p class="text-mitto-text-muted text-sm mb-4">${helpText}</p>

      ${showFilter &&
      html`
        <div class="mb-4">
          <input
            ref=${filterInputRef}
            type="text"
            value=${filterText}
            onInput=${(e) => setFilterText(e.target.value)}
            onKeyDown=${(e) => {
              // Intercept number keys 1-9 to select workspaces quickly
              const num = parseInt(e.key, 10);
              if (
                num >= 1 &&
                num <=
                  Math.min(
                    WORKSPACE_FILTER_THRESHOLD,
                    flatFilteredWorkspaces.length,
                  )
              ) {
                e.preventDefault();
                const workspace = flatFilteredWorkspaces[num - 1];
                if (workspace) {
                  onSelect(workspace);
                }
              }
            }}
            placeholder="Filter workspaces..."
            autofocus
            autocomplete="off"
            class="w-full px-3 py-2 bg-slate-700/50 border border-mitto-border-2 rounded-lg text-sm focus:outline-none focus:border-mitto-accent-500 placeholder-gray-500"
          />
        </div>
      `}

      <div class="space-y-2">
        ${filteredGroups.length === 0
          ? html`
              <div class="text-center py-4 text-mitto-text-muted">
                No workspaces match your filter.
              </div>
            `
          : filteredGroups.map(
              ({ workingDir, label, workspaces: wsArray }) => {
                // Auto-expand folders when filtering is active
                const isExpanded = filterText.trim()
                  ? true
                  : expandedFolders[workingDir] !== false;
                const showGroupHeader = filteredGroups.length > 1;

                return html`
                  <div key=${workingDir} class="space-y-1">
                    ${showGroupHeader &&
                    html`
                      <button
                        onClick=${() => toggleFolder(workingDir)}
                        class="w-full px-2 py-1 text-left text-xs text-mitto-text-muted hover:text-mitto-text-secondary hover:bg-slate-700/30 rounded transition-colors flex items-center gap-2"
                      >
                        <span class="font-mono"
                          >${isExpanded ? "▼" : "▶"}</span
                        >
                        <span class="truncate" title=${workingDir}>
                          ${label}
                        </span>
                        <span class="text-mitto-text-muted">(${wsArray.length})</span>
                      </button>
                    `}
                    ${isExpanded &&
                    wsArray.map((ws) => {
                      const currentIndex = globalIndex++;
                      return html`
                        <button
                          key=${ws.working_dir + "|" + ws.acp_server}
                          onClick=${() => onSelect(ws)}
                          class="w-full p-3 text-left rounded-lg bg-slate-700/50 hover:bg-mitto-surface-hover transition-colors flex items-center gap-3 ${showGroupHeader
                            ? "ml-4"
                            : ""}"
                        >
                          <div
                            class="w-8 h-8 shrink-0 ${currentIndex <
                            WORKSPACE_FILTER_THRESHOLD
                              ? "flex items-center justify-center rounded-lg bg-mitto-surface-4 text-mitto-text-secondary font-mono text-sm"
                              : ""}"
                          >
                            ${currentIndex < WORKSPACE_FILTER_THRESHOLD
                              ? currentIndex + 1
                              : ""}
                          </div>
                          <${WorkspaceBadge}
                            path=${ws.working_dir}
                            customColor=${ws.color}
                            customCode=${ws.code}
                            size="lg"
                          />
                          <div class="flex-1 min-w-0">
                            ${(!showGroupHeader ||
                              (ws.name && ws.name !== label)) &&
                            html`
                              <div class="text-sm font-medium">
                                ${ws.name || getBasename(ws.working_dir)}
                              </div>
                            `}
                            ${ws.acp_server &&
                            html`
                              <div
                                class="${showGroupHeader &&
                                (!ws.name || ws.name === label)
                                  ? "text-sm font-medium"
                                  : "text-xs text-mitto-accent"}"
                              >
                                ${ws.acp_server}
                              </div>
                            `}
                            ${!showGroupHeader &&
                            html`
                              <div class="text-xs text-mitto-text-muted truncate">
                                ${ws.working_dir}
                              </div>
                            `}
                          </div>
                        </button>
                      `;
                    })}
                  </div>
                `;
              },
            )}
      </div>
    </${Modal}>
  `;
}
