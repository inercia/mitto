// Mitto Web Interface - WorkspaceBadge Component
const { html } = window.preact;

import { getWorkspaceVisualInfo } from "../lib.js";

// =============================================================================
// Workspace Badge Component
// =============================================================================

/**
 * A colored badge showing a three-letter abbreviation for a workspace.
 * The color is deterministically generated from the workspace path,
 * or uses custom values if provided.
 *
 * @param {string} path - The workspace directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} customCode - Optional custom three-letter code
 * @param {string} customName - Optional custom friendly name
 * @param {string} size - Size variant: 'sm', 'md', 'lg' (default: 'md')
 * @param {boolean} showPath - Whether to show the full path below the badge
 */
export function WorkspaceBadge({
  path,
  customColor,
  customCode,
  customName,
  size = "md",
  showPath = false,
  className = "",
}) {
  if (!path) return null;

  const { abbreviation, color, displayName } = getWorkspaceVisualInfo(
    path,
    customColor,
    customCode,
    customName,
  );

  const sizeClasses = {
    sm: "w-8 h-8 text-xs",
    md: "w-10 h-10 text-sm",
    lg: "w-12 h-12 text-base",
  };

  return html`
    <div class="flex items-center gap-3 ${className}">
      <div
        class="flex items-center justify-center rounded-lg font-bold ${sizeClasses[
          size
        ] || sizeClasses.md}"
        style=${{
          backgroundColor: color.background,
          color: color.text,
        }}
        title=${path}
      >
        ${abbreviation}
      </div>
      ${showPath &&
      html`
        <div class="min-w-0 flex-1">
          <div class="font-medium text-sm">${displayName}</div>
          <div class="text-xs text-mitto-text-muted truncate" title=${path}>
            ${path}
          </div>
        </div>
      `}
    </div>
  `;
}

/**
 * A pill-shaped workspace badge for compact display.
 * Shows abbreviation and ACP server name (or workspace name if no ACP server).
 * Supports click action to execute a configured command (e.g., open folder in Finder).
 *
 * @param {string} path - The workspace directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} customCode - Optional custom three-letter code
 * @param {string} customName - Optional custom friendly name
 * @param {string} acpServer - The ACP server name (e.g., "auggie", "claude-code")
 * @param {string} className - Additional CSS classes
 * @param {boolean} clickable - Whether the badge is clickable (default: false)
 * @param {function} onBadgeClick - Optional callback when badge is clicked
 * @param {boolean} hideAbbreviation - When true, hide the 3-letter abbreviation
 * @param {boolean} hideAcpServer - When true, show only workspace name, not ACP server
 */
export function WorkspacePill({
  path,
  customColor,
  customCode,
  customName,
  acpServer,
  className = "",
  clickable = false,
  onBadgeClick,
  hideAbbreviation = false,
  hideAcpServer = false,
}) {
  if (!path) return null;

  const {
    abbreviation,
    color,
    displayName: wsDisplayName,
  } = getWorkspaceVisualInfo(path, customColor, customCode, customName);
  // Display ACP server name if available, otherwise fall back to workspace display name (unless hideAcpServer)
  const displayName = hideAcpServer
    ? wsDisplayName
    : acpServer || wsDisplayName;

  const handleClick = (e) => {
    if (!clickable) return;
    e.stopPropagation(); // Prevent triggering session selection
    if (onBadgeClick) {
      onBadgeClick(path);
    }
  };

  const cursorClass = clickable
    ? "cursor-pointer workspace-pill-clickable"
    : "";

  return html`
    <div
      class="workspace-pill badge badge-sm gap-1 px-2 font-medium ${cursorClass} ${className}"
      style=${{
        backgroundColor: color.background,
        color: color.text,
      }}
      title=${clickable ? `Click to open: ${path}` : path}
      onClick=${handleClick}
    >
      ${!hideAbbreviation &&
      html`<span class="font-bold">${abbreviation}</span>`}
      <span class="truncate max-w-[80px]">${displayName}</span>
    </div>
  `;
}
