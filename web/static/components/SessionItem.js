// Mitto Web Interface - Session Item Component
const { html, Fragment, useMemo, useCallback } = window.preact;

import { FILTER_TAB } from "../utils/index.js";
import { useSwipeToAction, useConversationMenu } from "../hooks/index.js";
import { getArchiveReasonText, getGlobalWorkingDir } from "../lib.js";
import {
  PERIODIC_PROGRESS_STYLE,
  PERIODIC_PROGRESS_COLORS,
  PERIODIC_PROGRESS_URGENT_THRESHOLD,
} from "../constants.js";
import { WorkspacePill } from "./WorkspaceBadge.js";
import { ContextMenu } from "./ContextMenu.js";
import {
  LightningIcon,
  RobotIcon,
  PersonIcon,
  HourglassIcon,
  QuestionMarkIcon,
  TrashIcon,
  ArchiveIcon,
  MittoIcon,
  ClockIcon,
  EllipsisIcon,
} from "./Icons.js";

/**
 * Calculate periodic progress background style.
 * Returns a CSS background style showing elapsed time as a progress indicator.
 *
 * @param {Object} params - Parameters
 * @param {string|null} params.nextScheduledAt - ISO timestamp of next scheduled run
 * @param {Object|null} params.frequency - Frequency config { value, unit, at? }
 * @param {boolean} params.isLight - Whether light theme is active
 * @returns {string|null} CSS background style or null if not applicable
 */
export function getPeriodicProgressStyle({ nextScheduledAt, frequency, isLight }) {
  // Skip if progress indicator is disabled
  if (PERIODIC_PROGRESS_STYLE === "none" || !nextScheduledAt || !frequency) {
    return null;
  }

  const colors = PERIODIC_PROGRESS_COLORS[PERIODIC_PROGRESS_STYLE];
  if (!colors) return null;

  const themeColors = isLight ? colors.light : colors.dark;
  const now = Date.now();
  const nextTime = new Date(nextScheduledAt).getTime();

  // Calculate the interval duration in milliseconds
  let intervalMs;
  switch (frequency.unit) {
    case "minutes":
      intervalMs = frequency.value * 60 * 1000;
      break;
    case "hours":
      intervalMs = frequency.value * 60 * 60 * 1000;
      break;
    case "days":
      intervalMs = frequency.value * 24 * 60 * 60 * 1000;
      break;
    default:
      return null;
  }

  // Calculate elapsed time since last run (interval start)
  const intervalStart = nextTime - intervalMs;
  const elapsed = now - intervalStart;
  const progress = Math.max(0, Math.min(1, elapsed / intervalMs));

  // Determine if we're in "urgent" state (close to next run)
  const remaining = 1 - progress;
  const isUrgent = remaining < PERIODIC_PROGRESS_URGENT_THRESHOLD;

  // Get the appropriate color
  const elapsedColor = isUrgent
    ? themeColors.urgentElapsed
    : themeColors.elapsed;
  const remainingColor = themeColors.remaining;

  // Create the gradient - progress goes left to right
  const progressPercent = (progress * 100).toFixed(1);

  return `linear-gradient(to right, ${elapsedColor} 0%, ${elapsedColor} ${progressPercent}%, ${remainingColor} ${progressPercent}%, ${remainingColor} 100%)`;
}

export function SessionItem({
  session,
  isActive,
  onSelect,
  onRename,
  onDelete,
  onArchive,
  workspaceColor = null,
  workspaceCode = null,
  workspaceName = null,
  badgeClickEnabled = false,
  onBadgeClick,
  hasQueuedMessages = false,
  isSessionStreaming = false,
  hideBadge = false,
  badgeHideAbbreviation = false,
  badgeHideAcpServer = false,
  isLightTheme = false,
  filterTab = FILTER_TAB.CONVERSATIONS,
  groupingMode = "none", // Current grouping mode (to hide spawned indicator in hierarchical mode)
  onFetchConversationPrompts, // Async (session, workingDir) => menus:conversation prompts evaluated for THIS conversation
  onSendPromptToConversation, // Called with (session, prompt) when a context-menu prompt is clicked
  onMakePeriodic, // Called with (session) to convert a regular session to periodic
  onMakeNonPeriodic, // Called with (session) to revert a periodic session to regular
  // New props for parent-child hierarchy display
  isSpawned = false, // If true, shows "spawned" indicator (child session)
  extraLeftPadding = "", // Additional CSS class for left padding (e.g., "pl-6")
  childCount = 0, // Number of child sessions (for collapsed parents)
  hasChildStreaming = false, // If true and collapsed, shows streaming indicator for child
  isNew = false, // If true, applies blink animation for new conversations
  // Props for expand/collapse functionality (when session has children)
  hasChildren = false, // If true, shows expand/collapse chevron
  isExpanded = false, // If true, chevron points down (expanded state)
  onToggleExpand = null, // Callback when expand/collapse is clicked
  density = "condensed",
}) {
  // Check if session is archived
  const isArchived = session.archived || false;

  // Check if periodic is enabled for this session
  const isPeriodicEnabled = session.periodic_enabled || false;

  // Leading category icon for the unified-tree row:
  //   regular  -> mitto bubble (muted)
  //   periodic -> clock (muted)
  //   archived -> archive (muted)
  // Spawned/child rows keep their ↳ marker + child-origin glyph instead.
  let CategoryIcon = MittoIcon;
  let categoryIconClass = "text-mitto-text-muted";
  if (isArchived) {
    CategoryIcon = ArchiveIcon;
    categoryIconClass = "text-mitto-text-muted";
  } else if (isPeriodicEnabled) {
    CategoryIcon = ClockIcon;
    categoryIconClass = "text-mitto-text-muted";
  }

  // Calculate periodic progress background style
  const periodicProgressBg = useMemo(() => {
    if (!isPeriodicEnabled || isArchived) return null;
    return getPeriodicProgressStyle({
      nextScheduledAt: session.next_scheduled_at,
      frequency: session.periodic_frequency,
      isLight: isLightTheme,
    });
  }, [
    isPeriodicEnabled,
    isArchived,
    session.next_scheduled_at,
    session.periodic_frequency,
    isLightTheme,
  ]);

  // Archive button should be disabled if:
  // 1. There are queued messages (can't archive with pending messages)
  // 2. The session is streaming (agent is responding - archiving would block for up to 5 minutes)
  const canArchive = !hasQueuedMessages && !isSessionStreaming;

  // Get the reason why archiving is blocked (for tooltip)
  const archiveBlockedReason = hasQueuedMessages
    ? "Clear queue before archiving"
    : isSessionStreaming
      ? "Wait for response to complete"
      : null;

  // Get working_dir from session, or fall back to global map
  const workingDir =
    session.working_dir || getGlobalWorkingDir(session.session_id) || "";
  // Get acp_server from session
  const acpServer = session.acp_server || "";

  // Build tooltip with session metadata
  const buildTooltip = () => {
    const parts = [];

    // Workspace folder
    if (workingDir) {
      parts.push(`Folder: ${workingDir}`);
    }

    // ACP server
    if (acpServer) {
      parts.push(`Server: ${acpServer}`);
    }

    // Runner type
    if (session.runner_type) {
      const runnerInfo = session.runner_restricted
        ? `${session.runner_type} (restricted)`
        : `${session.runner_type} (unrestricted)`;
      parts.push(`Runner: ${runnerInfo}`);
    }

    // Message/event count
    if (session.messageCount !== undefined) {
      parts.push(`Messages: ${session.messageCount}`);
    } else if (session.event_count !== undefined) {
      parts.push(`Events: ${session.event_count}`);
    }

    // Creation time
    if (session.created_at) {
      const createdDate = new Date(session.created_at);
      parts.push(`Created: ${createdDate.toLocaleString()}`);
    }

    // Last activity time
    if (session.updated_at) {
      const updatedDate = new Date(session.updated_at);
      parts.push(`Last activity: ${updatedDate.toLocaleString()}`);
    } else if (session.last_user_message_at) {
      const lastMsgDate = new Date(session.last_user_message_at);
      parts.push(`Last message: ${lastMsgDate.toLocaleString()}`);
    }

    // Archived time (for archived sessions)
    if (isArchived && session.archived_at) {
      const archivedDate = new Date(session.archived_at);
      parts.push(`Archived: ${archivedDate.toLocaleString()}`);
    }

    // GC-suspended status (for periodic sessions paused to save resources)
    if (session.gc_suspended) {
      parts.push("Status: Suspended (saving resources)");
    }

    // Next scheduled run (for periodic sessions)
    if (isPeriodicEnabled && session.next_scheduled_at) {
      const nextDate = new Date(session.next_scheduled_at);
      const now = Date.now();
      const diff = nextDate.getTime() - now;
      if (diff > 0) {
        // Format relative time
        const hours = Math.floor(diff / (1000 * 60 * 60));
        const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));
        let relativeTime;
        if (hours > 24) {
          const days = Math.floor(hours / 24);
          relativeTime = `${days}d ${hours % 24}h`;
        } else if (hours > 0) {
          relativeTime = `${hours}h ${minutes}m`;
        } else {
          relativeTime = `${minutes}m`;
        }
        parts.push(
          `Next run: ${nextDate.toLocaleString()} (in ${relativeTime})`,
        );
      }
    }

    return parts.join("\n");
  };

  // Determine swipe action based on filter tab and session type:
  // - Archived tab: swipe to delete
  // - Child (spawned) sessions: swipe to delete (archive not applicable)
  // - Regular/Periodic tabs: swipe to archive
  const isSwipeToDelete = filterTab === FILTER_TAB.ARCHIVED || isSpawned;

  // Swipe action handler - archive or delete based on current tab
  const handleSwipeAction = useCallback(() => {
    if (isSwipeToDelete) {
      onDelete(session);
    } else {
      // Archive the session (pass true to archive)
      onArchive(session, true);
    }
  }, [isSwipeToDelete, session, onDelete, onArchive]);

  // Swipe-to-action hook (archive or delete based on tab)
  const {
    swipeOffset,
    isSwiping,
    isSwipingRef,
    isRevealed,
    containerProps,
    reset,
    triggerAction,
  } = useSwipeToAction({
    onAction: handleSwipeAction,
    threshold: 0.5,
    revealWidth: 80,
    disabled: false,
  });

  // Per-conversation actions menu (shared with the chat header). Provides the
  // context-menu state, the assembled menu items, and the right-click /
  // three-dot button handlers, all scoped to THIS conversation.
  const {
    contextMenu,
    contextMenuItems,
    closeContextMenu,
    handleContextMenu,
    handleMenuButtonClick,
  } = useConversationMenu({
    session,
    workingDir,
    isArchived,
    isPeriodicEnabled,
    isSpawned,
    canArchive,
    archiveBlockedReason,
    onRename,
    onDelete,
    onArchive,
    onMakePeriodic,
    onMakeNonPeriodic,
    onFetchConversationPrompts,
    onSendPromptToConversation,
  });

  // Handle click - only select if not swiping/revealed
  // Use ref for isSwiping to avoid stale closure issues
  const handleClick = useCallback(() => {
    if (isSwipingRef.current) return;
    if (isRevealed) {
      reset();
      return;
    }
    onSelect(session.session_id);
  }, [isSwipingRef, isRevealed, reset, onSelect, session.session_id]);

  const displayName = session.name || session.description || "Untitled";
  // Archived sessions should never show as active (they have no ACP connection)
  const isActiveSession =
    !isArchived && (session.isActive || session.status === "active");
  const isStreaming = !isArchived && (session.isStreaming || false);
  // Show a daisyUI loading ring in the leading-icon slot when this conversation's
  // own agent is responding, OR when a (collapsed) child conversation is responding.
  // It replaces the category icon on regular rows, and sits next to the ↳ marker on
  // spawned (child) rows (which otherwise have no icon). The ring replaces the
  // streaming status dot, so that dot is suppressed too.
  const showLoadingRing = isStreaming || hasChildStreaming;
  // The ring tooltip distinguishes self-streaming from a responding child.
  const ringTitle = isStreaming
    ? "Receiving response..."
    : "Child conversation responding...";

  // On the active (selected) row the background is the red accent, so the
  // default muted text and the accent-colored streaming dot blend into it.
  // Switch the trailing controls (badge, "..." menu, chevron) and the streaming
  // dot to the accent foreground for contrast when the row is active.
  const trailingControlClass = isActive
    ? "text-mitto-accent-fg hover:text-mitto-accent-fg"
    : "text-mitto-text-muted hover:text-mitto-text-strong";

  // Calculate visual feedback intensity based on swipe progress
  const absOffset = Math.abs(swipeOffset);
  const deleteProgress = Math.min(absOffset / 160, 1); // Max at 160px

  // Context menu must be rendered outside the overflow-hidden containers
  // to prevent clipping. Use a Fragment to render it as a sibling.
  return html`
    <${Fragment}>
      ${contextMenu &&
      html`
        <${ContextMenu}
          x=${contextMenu.x}
          y=${contextMenu.y}
          items=${contextMenuItems}
          onClose=${closeContextMenu}
        />
      `}
      <div
        class="session-item-container relative overflow-hidden"
        ...${containerProps}
      >
        <!-- Swipe action background (revealed when swiping left) -->
        <!-- Shows Archive (amber) for regular/periodic tabs, Delete (red) for archived tab -->
        <div
          class="absolute inset-0 ${isSwipeToDelete
            ? "bg-red-600"
            : "bg-amber-600"} flex items-center justify-end pr-6 transition-opacity"
          style="opacity: ${isRevealed || absOffset > 20 ? 1 : 0}"
        >
          <button
            onClick=${(e) => {
              e.preventDefault();
              e.stopPropagation();
              triggerAction();
            }}
            class="p-3 rounded-full tooltip tooltip-left ${isSwipeToDelete
              ? "bg-red-700 hover:bg-red-800"
              : "bg-amber-700 hover:bg-amber-800"} transition-colors"
            data-tip=${isSwipeToDelete ? "Delete" : "Archive"}
            aria-label=${isSwipeToDelete ? "Delete" : "Archive"}
          >
            ${isSwipeToDelete
              ? html`<${TrashIcon} className="w-5 h-5 text-white" />`
              : html`<${ArchiveIcon} className="w-5 h-5 text-white" />`}
          </button>
        </div>
        <!-- Swipeable content -->
        <div
          onClick=${handleClick}
          onContextMenu=${handleContextMenu}
          class="px-2.5 ${density === "comfortable" ? "py-2.5" : "py-1"} rounded-lg cursor-pointer relative overflow-hidden ${isActive
            ? "bg-mitto-accent text-mitto-accent-fg"
            : "bg-mitto-sidebar hover:bg-mitto-surface-3/50"} ${isSwiping
            ? ""
            : "transition-transform duration-200"} ${extraLeftPadding} ${isNew
            ? "session-item-new"
            : ""}"
          style="transform: translateX(${swipeOffset}px);"
          title=${buildTooltip()}
          data-session-id=${session.session_id}
          data-has-context-menu="true"
        >
          ${periodicProgressBg
            ? html`<div
                class="absolute inset-0 z-0 pointer-events-none"
                style="background: ${periodicProgressBg};"
                aria-hidden="true"
              ></div>`
            : ""}
          <div class="relative z-10">
            <!-- Top row: status indicator, title, and workspace pill -->
            <div class="flex items-center gap-2">
              <div class="flex-1 min-w-0">
                <div class="flex items-center gap-2 min-w-0">
                  ${isSpawned
                    ? html`
                          <span
                            class="text-sm leading-none shrink-0 tooltip tooltip-right ${isActive
                              ? "text-mitto-accent-fg"
                              : "text-mitto-text-muted"}"
                            data-tip="Spawned from another conversation"
                            aria-label="Spawned from another conversation"
                            >↳</span
                          >
                        `
                      : null
                  }
                  ${isSpawned && showLoadingRing
                    ? html`
                        <span class="shrink-0 ${isActive
                          ? "text-mitto-accent-fg"
                          : "text-mitto-accent"}">
                          <span
                            class="loading loading-ring loading-xs tooltip tooltip-right"
                            data-tip=${ringTitle}
                            aria-label=${ringTitle}
                          ></span>
                        </span>
                      `
                    : null}
                  ${!isSpawned
                    ? html`
                        <span class="shrink-0 ${isActive
                          ? "text-mitto-accent-fg"
                          : showLoadingRing
                            ? "text-mitto-accent"
                            : categoryIconClass}">
                          ${showLoadingRing
                            ? html`<span
                                class="loading loading-ring loading-xs tooltip tooltip-right"
                                data-tip=${ringTitle}
                                aria-label=${ringTitle}
                              ></span>`
                            : html`<${CategoryIcon} className="w-4 h-4" />`}
                        </span>
                      `
                    : null}
                  <span
                    class="text-sm truncate ${isActive
                      ? "text-mitto-accent-fg"
                      : isArchived
                        ? "text-mitto-text-300"
                        : ""}"
                    >${displayName}</span
                  >
                  ${session.child_origin === "auto"
                    ? html`
                        <span class="shrink-0 text-amber-400 tooltip tooltip-right" data-tip="Auto-created child" aria-label="Auto-created child">
                          <${LightningIcon} className="w-4 h-4" />
                        </span>
                      `
                    : session.child_origin === "mcp"
                      ? html`
                          <span class="shrink-0 text-mitto-accent tooltip tooltip-right" data-tip="Created by agent" aria-label="Created by agent">
                            <${RobotIcon} className="w-4 h-4" />
                          </span>
                        `
                      : session.child_origin === "human"
                        ? html`
                            <span class="shrink-0 text-mitto-success tooltip tooltip-right" data-tip="Manually created child" aria-label="Manually created child">
                              <${PersonIcon} className="w-4 h-4" />
                            </span>
                          `
                        : null}
                  ${session.isWaitingForChildren
                    ? html`
                        <span class="shrink-0 text-mitto-warning animate-pulse tooltip tooltip-right" data-tip="Waiting for child conversations" aria-label="Waiting for child conversations">
                          <${HourglassIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                  ${session.isWaitingForUserInput
                    ? html`
                        <span class="shrink-0 text-purple-400 animate-pulse tooltip tooltip-right" data-tip="Waiting for user input" aria-label="Waiting for user input">
                          <${QuestionMarkIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                </div>
              </div>
              ${showLoadingRing || isActiveSession
                ? null
                : !isArchived
                    ? html`
                        <span
                          class="w-2 h-2 bg-amber-400 rounded-full shrink-0 tooltip tooltip-left"
                          data-tip="Not connected"
                          aria-label="Not connected"
                        ></span>
                      `
                    : null}
              ${workingDir &&
              !hideBadge &&
              html`
                <${WorkspacePill}
                  path=${workingDir}
                  customColor=${workspaceColor}
                  customCode=${workspaceCode}
                  customName=${workspaceName}
                  acpServer=${acpServer}
                  clickable=${badgeClickEnabled}
                  onBadgeClick=${onBadgeClick}
                  hideAbbreviation=${badgeHideAbbreviation}
                  hideAcpServer=${true}
                  className="shrink-0"
                />
              `}
              ${!isSpawned &&
              hasChildren &&
              childCount > 0 &&
              html`
                <!-- Children count — a clickable count-number badge. Clicking it
                     toggles the nested child conversations (children are collapsed
                     by default and never auto-expand). -->
                <span
                  role="button"
                  tabindex="0"
                  onClick=${(e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    if (onToggleExpand) onToggleExpand();
                  }}
                  onKeyDown=${(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      e.stopPropagation();
                      if (onToggleExpand) onToggleExpand();
                    }
                  }}
                  class="badge badge-sm badge-ghost shrink-0 tabular-nums cursor-pointer tooltip tooltip-left ${isActive
                    ? "bg-mitto-accent-fg text-mitto-accent"
                    : ""}"
                  aria-expanded=${isExpanded}
                  data-tip="${isExpanded ? "Collapse" : "Expand"} ${childCount} child conversation${childCount ===
                  1
                    ? ""
                    : "s"}"
                  >${childCount}</span
                >
              `}
              <button
                type="button"
                onClick=${handleMenuButtonClick}
                class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 tooltip tooltip-left ${trailingControlClass}"
                data-tip="More actions"
                aria-label="More actions"
                data-testid="session-item-menu"
              >
                <${EllipsisIcon} className="w-3.5 h-3.5" />
              </button>
            </div>
            ${density === "comfortable" && acpServer
              ? html`<div class="text-[0.5625rem] text-mitto-text-muted italic font-normal truncate mt-0.5 pl-6">${acpServer}</div>`
              : null}
          </div>
        </div>
      </div>
    <//>
  `;
}
