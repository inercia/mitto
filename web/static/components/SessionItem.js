// Mitto Web Interface - Session Item Component
const { html, Fragment, useState, useMemo, useCallback } = window.preact;

import { FILTER_TAB } from "../utils/index.js";
import { useSwipeToAction } from "../hooks/index.js";
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
  ArchiveFilledIcon,
  EditIcon,
  getPromptIconOrDefault,
  BalloonIcon,
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
}) {
  const [contextMenu, setContextMenu] = useState(null);
  // menus:conversation prompts evaluated for THIS conversation. Loaded lazily
  // when the context menu opens (enabledWhen depends on this conversation's own
  // context, not the active session). Cached between opens; refreshed each open.
  const [menuPrompts, setMenuPrompts] = useState([]);

  // Check if session is archived
  const isArchived = session.archived || false;

  // Check if periodic is enabled for this session
  const isPeriodicEnabled = session.periodic_enabled || false;

  // Leading category icon for the unified-tree row:
  //   regular  -> balloon (muted)
  //   periodic -> clock (accent)
  //   archived -> archive (muted)
  // Spawned/child rows keep their ↳ marker + child-origin glyph instead.
  let CategoryIcon = BalloonIcon;
  let categoryIconClass = "text-mitto-text-muted";
  if (isArchived) {
    CategoryIcon = ArchiveIcon;
    categoryIconClass = "text-mitto-text-muted";
  } else if (isPeriodicEnabled) {
    CategoryIcon = ClockIcon;
    categoryIconClass = "text-mitto-accent";
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

  const handleRename = (e) => {
    if (e) e.stopPropagation();
    onRename(session);
  };

  const handleDelete = (e) => {
    if (e) e.stopPropagation();
    onDelete(session);
  };

  const handleArchive = (e) => {
    if (e) e.stopPropagation();
    onArchive(session, !isArchived);
  };

  // Open the per-item context menu at a viewport position. Used by both
  // right-click (handleContextMenu) and the explicit three-dot button
  // (handleMenuButtonClick). Evaluates menus:conversation prompts against THIS
  // conversation's context so the submenu reflects the clicked conversation
  // (e.g. "Report to parent" only for children), not the active session.
  const openContextMenuAt = (x, y) => {
    setContextMenu({ x, y });
    if (onFetchConversationPrompts) {
      onFetchConversationPrompts(session, workingDir).then((prompts) => {
        setMenuPrompts(prompts || []);
      });
    }
  };

  const handleContextMenu = (e) => {
    e.preventDefault();
    e.stopPropagation();
    openContextMenuAt(e.clientX, e.clientY);
  };

  // Explicit three-dot button: anchor the menu at the button's bottom-left.
  // ContextMenu is viewport-aware and will flip/shift to stay on-screen.
  const handleMenuButtonClick = (e) => {
    e.preventDefault();
    e.stopPropagation();
    const rect = e.currentTarget.getBoundingClientRect();
    openContextMenuAt(rect.left, rect.bottom);
  };

  const closeContextMenu = () => {
    setContextMenu(null);
  };

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

  // On the active (selected) row the background is the red accent, so the
  // default muted text and the accent-colored streaming dot blend into it.
  // Switch the trailing controls (badge, "..." menu, chevron) and the streaming
  // dot to the accent foreground for contrast when the row is active.
  const trailingControlClass = isActive
    ? "text-mitto-accent-fg hover:text-mitto-accent-fg"
    : "text-mitto-text-muted hover:text-mitto-text-strong";

  // Build group submenus from prompts flagged with menus:conversation.
  // Prompts are grouped by their `group` attribute; ungrouped prompts fall
  // under "Other". Each group becomes a submenu listing its prompts.
  const promptGroupItems = [];
  if (
    onSendPromptToConversation &&
    menuPrompts &&
    menuPrompts.length > 0
  ) {
    const groups = new Map();
    for (const p of menuPrompts) {
      if (!p || !p.name) continue;
      const groupName = (p.group && p.group.trim()) || "Other";
      if (!groups.has(groupName)) groups.set(groupName, []);
      groups.get(groupName).push(p);
    }
    for (const [groupName, prompts] of groups) {
      promptGroupItems.push({
        label: groupName,
        icon: html`<${LightningIcon} />`,
        submenu: prompts
          .slice()
          .sort((a, b) => a.name.localeCompare(b.name))
          .map((p) => {
            const PromptIcon = getPromptIconOrDefault(p.icon);
            return {
              label: p.name,
              icon: html`<${PromptIcon} className="w-4 h-4" />`,
              onClick: () => onSendPromptToConversation(session, p),
            };
          }),
      });
    }
  }

  const contextMenuItems = [
    // Prompt group submenus (menus:conversation prompts), e.g. "Workflow"
    ...promptGroupItems,
    {
      label: "Properties",
      icon: html`<${EditIcon} />`,
      onClick: () => handleRename(),
    },
    // "Make periodic" — only for non-periodic, non-spawned, non-archived sessions
    ...(!isPeriodicEnabled && !isSpawned && !isArchived
      ? [
          {
            label: "Make periodic",
            icon: html`<${ClockIcon} />`,
            onClick: () => onMakePeriodic && onMakePeriodic(session),
          },
        ]
      : []),
    // "Make non-periodic" — inverse: only for periodic, non-spawned sessions
    ...(isPeriodicEnabled && !isSpawned
      ? [
          {
            label: "Make non-periodic",
            icon: html`<${BalloonIcon} />`,
            onClick: () => onMakeNonPeriodic && onMakeNonPeriodic(session),
          },
        ]
      : []),
    // Hide archive option for child (spawned) sessions
    ...(isSpawned
      ? []
      : [
          {
            label: !canArchive
              ? archiveBlockedReason
              : isArchived
                ? "Unarchive"
                : "Archive",
            icon: isArchived
              ? html`<${ArchiveFilledIcon} />`
              : html`<${ArchiveIcon} />`,
            onClick: canArchive ? () => handleArchive() : undefined,
            disabled: !canArchive,
          },
        ]),
    {
      label: "Delete",
      icon: html`<${TrashIcon} />`,
      onClick: () => handleDelete(),
      danger: true,
    },
  ];

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
            class="p-3 rounded-full ${isSwipeToDelete
              ? "bg-red-700 hover:bg-red-800"
              : "bg-amber-700 hover:bg-amber-800"} transition-colors"
            title=${isSwipeToDelete ? "Delete" : "Archive"}
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
          class="px-2.5 py-1 cursor-pointer relative overflow-hidden ${isActive
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
                            class="text-mitto-text-muted text-sm leading-none shrink-0"
                            title="Spawned from another conversation"
                            >↳</span
                          >
                        `
                      : null
                  }
                  ${!isSpawned
                    ? html`
                        <span class="shrink-0 ${isActive
                          ? "text-mitto-accent-fg"
                          : categoryIconClass}">
                          <${CategoryIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                  <span
                    class="text-sm truncate ${isArchived
                      ? "text-mitto-text-300"
                      : ""}"
                    >${displayName}</span
                  >
                  ${session.child_origin === "auto"
                    ? html`
                        <span class="shrink-0 text-amber-400" title="Auto-created child">
                          <${LightningIcon} className="w-4 h-4" />
                        </span>
                      `
                    : session.child_origin === "mcp"
                      ? html`
                          <span class="shrink-0 text-mitto-accent" title="Created by agent">
                            <${RobotIcon} className="w-4 h-4" />
                          </span>
                        `
                      : session.child_origin === "human"
                        ? html`
                            <span class="shrink-0 text-mitto-success" title="Manually created child">
                              <${PersonIcon} className="w-4 h-4" />
                            </span>
                          `
                        : null}
                  ${session.isWaitingForChildren
                    ? html`
                        <span class="shrink-0 text-mitto-warning animate-pulse" title="Waiting for child conversations">
                          <${HourglassIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                  ${session.isWaitingForUserInput
                    ? html`
                        <span class="shrink-0 text-purple-400 animate-pulse" title="Waiting for user input">
                          <${QuestionMarkIcon} className="w-4 h-4" />
                        </span>
                      `
                    : null}
                </div>
              </div>
              ${isStreaming || hasChildStreaming
                ? html`
                    <span
                      class="w-2 h-2 rounded-full shrink-0 ${isActive
                        ? "bg-mitto-accent-fg"
                        : "bg-mitto-accent-400"} ${hasChildStreaming
                        ? "child-streaming-indicator"
                        : "streaming-indicator"}"
                      title=${hasChildStreaming
                        ? "Child conversation responding..."
                        : "Receiving response..."}
                    ></span>
                  `
                : isActiveSession
                  ? html`
                      <span
                        class="w-2 h-2 bg-green-400 rounded-full shrink-0"
                        title="Active"
                      ></span>
                    `
                  : !isArchived
                    ? html`
                        <span
                          class="w-2 h-2 bg-amber-400 rounded-full shrink-0"
                          title="Not connected"
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
                  hideAcpServer=${badgeHideAcpServer}
                  className="shrink-0"
                />
              `}
              ${!isSpawned &&
              hasChildren &&
              childCount > 0 &&
              html`
                <!-- Children count — a plain count-number badge. The "v"
                     chevron toggle is rendered after the "..." menu so the
                     trailing controls read <num> ... v, matching folder
                     groups (<num> + ... v). -->
                <span
                  class="badge badge-sm badge-ghost shrink-0 tabular-nums ${isActive
                    ? "text-mitto-accent-fg"
                    : ""}"
                  title="${childCount} child conversation${childCount === 1
                    ? ""
                    : "s"}"
                  >${childCount}</span
                >
              `}
              <button
                type="button"
                onClick=${handleMenuButtonClick}
                class="btn btn-ghost btn-circle btn-xs sidebar-group-action shrink-0 ${trailingControlClass}"
                title="More actions"
                aria-label="More actions"
                data-testid="session-item-menu"
              >
                <${EllipsisIcon} className="w-3.5 h-3.5" />
              </button>
            </div>
          </div>
        </div>
      </div>
    <//>
  `;
}
