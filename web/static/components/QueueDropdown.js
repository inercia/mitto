// Mitto Web Interface - Queue Dropdown Component
// Displays and manages queued messages waiting to be sent to the agent

const { useState, useEffect, useRef, useCallback, html } = window.preact;

import {
  TrashIcon,
  ChevronUpIcon,
  ChevronDownIcon,
  GripIcon,
} from "./Icons.js";
import { useResizeHandle } from "../hooks/useResizeHandle.js";
import {
  getQueueDropdownHeight,
  setQueueDropdownHeight,
  getQueueHeightConstraints,
} from "../utils/storage.js";

/**
 * Truncate text to a maximum length with ellipsis
 * @param {string} text - Text to truncate
 * @param {number} maxLength - Maximum length (default: 50)
 * @returns {string} Truncated text
 */
function truncateText(text, maxLength = 50) {
  if (!text) return "";
  if (text.length <= maxLength) return text;
  return text.substring(0, maxLength).trim() + "…";
}

/**
 * Format a scheduled time as a relative time string
 * @param {string} scheduledTime - ISO 8601 timestamp
 * @returns {string} Relative time string (e.g., "in 5 min", "in 2h")
 */
function formatRelativeTime(scheduledTime) {
  if (!scheduledTime) return "";
  const now = Date.now();
  const scheduled = new Date(scheduledTime).getTime();
  const diffMs = scheduled - now;

  if (diffMs <= 0) return "due now";

  const diffSeconds = Math.floor(diffMs / 1000);
  if (diffSeconds < 60) return `in ${diffSeconds}s`;

  const diffMinutes = Math.floor(diffSeconds / 60);
  if (diffMinutes < 60) return `in ${diffMinutes} min`;

  const diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) {
    const remainingMinutes = diffMinutes % 60;
    return remainingMinutes > 0
      ? `in ${diffHours}h ${remainingMinutes}m`
      : `in ${diffHours}h`;
  }

  const diffDays = Math.floor(diffHours / 24);
  return `in ${diffDays}d`;
}

/**
 * QueueDropdown component - displays queued messages with delete and move functionality
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the dropdown is visible
 * @param {Function} props.onClose - Callback to close the dropdown
 * @param {Array} props.messages - Array of queued messages { id, message, title, queued_at }
 * @param {Function} props.onDelete - Callback to delete a message (messageId) => void
 * @param {Function} props.onMove - Callback to move a message (messageId, direction) => void
 * @param {boolean} props.isDeleting - Whether a delete operation is in progress
 * @param {boolean} props.isMoving - Whether a move operation is in progress
 * @param {number} props.queueLength - Current number of messages in queue
 * @param {number} props.maxSize - Maximum queue size from config
 */
export function QueueDropdown({
  isOpen,
  onClose,
  messages = [],
  onDelete,
  onMove,
  isDeleting = false,
  isMoving = false,
  queueLength = 0,
  maxSize = 10,
}) {
  const dropdownRef = useRef(null);
  const inactivityTimerRef = useRef(null);

  // Tick counter to force re-render for relative time updates
  const [, setTick] = useState(0);

  // Update relative times for scheduled messages every 30 seconds
  useEffect(() => {
    const hasScheduled = messages.some((m) => m.scheduled_time);
    if (!hasScheduled || !isOpen) return;

    const interval = setInterval(() => {
      setTick((t) => t + 1);
    }, 30000);

    return () => clearInterval(interval);
  }, [messages, isOpen]);

  // Get height constraints for resize
  const heightConstraints = getQueueHeightConstraints();

  // Use resize handle hook for drag-to-resize functionality
  const { height, isDragging, handleProps } = useResizeHandle({
    initialHeight: getQueueDropdownHeight(),
    minHeight: heightConstraints.min,
    maxHeight: heightConstraints.max,
    onDragStart: () => {
      // Pause inactivity timer while dragging
      if (inactivityTimerRef.current) {
        clearTimeout(inactivityTimerRef.current);
        inactivityTimerRef.current = null;
      }
    },
    onDragEnd: (finalHeight) => {
      // Persist the height when drag ends
      setQueueDropdownHeight(finalHeight);
    },
  });

  // Compute classes for animation - positioned as floating overlay above the input
  // Shadow only on top (negative Y offset) to cast over conversation area, not over input
  // When open: use resizable height, when closed: collapse to 0
  const dropdownClasses = `queue-dropdown flex flex-col absolute bottom-full left-0 right-0 w-full bg-mitto-surface-3/95 backdrop-blur-sm border-t border-l border-r border-mitto-border-2 rounded-t-lg overflow-hidden z-20 ${
    isDragging ? "" : "transition-all duration-300 ease-out"
  } ${isOpen ? "opacity-100" : "opacity-0 pointer-events-none border-0"}`;

  // Use explicit height when open, 0 when closed
  const dropdownStyle = isOpen
    ? `height: ${height}px; box-shadow: 0 -8px 16px rgba(0, 0, 0, 0.3);`
    : "height: 0px;";

  // Reset inactivity timer on any interaction
  const resetInactivityTimer = useCallback(() => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current);
    }
    if (isOpen) {
      inactivityTimerRef.current = setTimeout(() => {
        onClose();
      }, 5000); // 5 seconds of inactivity (increased for add-to-queue workflow)
    }
  }, [isOpen, onClose]);

  // Start inactivity timer when dropdown opens
  useEffect(() => {
    if (isOpen) {
      resetInactivityTimer();
    }
    return () => {
      if (inactivityTimerRef.current) {
        clearTimeout(inactivityTimerRef.current);
      }
    };
  }, [isOpen, resetInactivityTimer]);

  // Close dropdown when clicking outside
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target)) {
        // Also check if the click was on the queue toggle button (to avoid immediate close when opening)
        const queueButton = event.target.closest("[data-queue-toggle]");
        if (!queueButton) {
          onClose();
        }
      }
    };

    // Add listener with a small delay to avoid catching the opening click
    const timeoutId = setTimeout(() => {
      document.addEventListener("click", handleClickOutside);
    }, 10);

    return () => {
      clearTimeout(timeoutId);
      document.removeEventListener("click", handleClickOutside);
    };
  }, [isOpen, onClose]);

  // Handle mouse enter/leave for inactivity timer
  const handleMouseEnter = useCallback(() => {
    if (inactivityTimerRef.current) {
      clearTimeout(inactivityTimerRef.current);
      inactivityTimerRef.current = null;
    }
  }, []);

  const handleMouseLeave = useCallback(() => {
    resetInactivityTimer();
  }, [resetInactivityTimer]);

  // Handle delete click
  const handleDelete = useCallback(
    (e, messageId) => {
      e.stopPropagation();
      if (onDelete && !isDeleting) {
        onDelete(messageId);
      }
    },
    [onDelete, isDeleting],
  );

  // Handle move up click
  const handleMoveUp = useCallback(
    (e, messageId) => {
      e.stopPropagation();
      if (onMove && !isMoving) {
        onMove(messageId, "up");
      }
    },
    [onMove, isMoving],
  );

  // Handle move down click
  const handleMoveDown = useCallback(
    (e, messageId) => {
      e.stopPropagation();
      if (onMove && !isMoving) {
        onMove(messageId, "down");
      }
    },
    [onMove, isMoving],
  );

  // Render the content wrapper - always rendered for animation, visibility controlled by height
  return html`
    <div
      ref=${dropdownRef}
      class=${dropdownClasses}
      data-is-open=${String(isOpen)}
      data-testid="queue-dropdown"
      style="transform-origin: bottom; ${dropdownStyle}"
      onMouseEnter=${handleMouseEnter}
      onMouseLeave=${handleMouseLeave}
    >
      <!-- Resize Handle at top edge -->
      <div
        class="queue-resize-handle flex items-center justify-center py-1 cursor-ns-resize hover:bg-mitto-surface-4/50 transition-colors select-none touch-none ${isDragging
          ? "bg-mitto-surface-4/50"
          : ""}"
        ...${handleProps}
        title="Drag to resize"
      >
        <${GripIcon} className="w-6 h-1.5 text-mitto-text-muted" />
      </div>

      <div
        class="queue-dropdown-header px-3 py-2 border-b border-mitto-border-1 flex items-center justify-between"
      >
        <span
          class="text-xs font-medium text-mitto-text-muted uppercase tracking-wide"
        >
          Queued Messages (${messages.length}/${maxSize})
        </span>
      </div>
      ${messages.length > 0
        ? html`
            <ul
              class="queue-dropdown-list menu menu-sm w-full p-0 gap-0 flex-nowrap flex-1 min-h-0 overflow-y-auto"
            >
              ${messages.map(
                (msg, index) => html`
                  <li
                    key=${msg.id}
                    class="queue-dropdown-item border-b border-mitto-border-1/50 last:border-b-0"
                    data-testid="queue-item"
                    data-queue-item-index=${index}
                  >
                    <div
                      class="flex items-center gap-2 px-3 py-2 rounded-none hover:bg-mitto-surface-3/50 transition-colors group"
                    >
                      <span
                        class="queue-item-number text-xs text-mitto-text-muted font-mono w-4 shrink-0"
                      >
                        ${index + 1}
                      </span>
                      <span
                        class="queue-item-text flex-1 text-sm text-mitto-text truncate"
                        title=${msg.prompt_name || msg.message}
                      >
                        ${msg.prompt_name ||
                        msg.title ||
                        truncateText(msg.message)}
                      </span>
                      ${msg.scheduled_time
                        ? html`
                            <span
                              class="text-xs text-amber-400 shrink-0 font-mono"
                              title=${new Date(
                                msg.scheduled_time,
                              ).toLocaleString()}
                            >
                              ⏰ ${formatRelativeTime(msg.scheduled_time)}
                            </span>
                          `
                        : null}
                      <div
                        class="queue-item-actions flex items-center gap-0.5 transition-opacity shrink-0"
                      >
                        <button
                          type="button"
                          onClick=${(e) => handleMoveUp(e, msg.id)}
                          aria-disabled=${isMoving || index === 0
                            ? "true"
                            : "false"}
                          class="queue-item-move-up btn btn-ghost btn-square btn-xs text-mitto-text-muted hover:text-mitto-text-strong tooltip tooltip-bottom ${isMoving ||
                          index === 0
                            ? "opacity-40 pointer-events-none"
                            : ""}"
                          data-tip=${index === 0 ? "Already at top" : "Move up"}
                          aria-label=${index === 0
                            ? "Already at top"
                            : "Move up"}
                        >
                          <${ChevronUpIcon} className="w-3.5 h-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick=${(e) => handleMoveDown(e, msg.id)}
                          aria-disabled=${isMoving ||
                          index === messages.length - 1
                            ? "true"
                            : "false"}
                          class="queue-item-move-down btn btn-ghost btn-square btn-xs text-mitto-text-muted hover:text-mitto-text-strong tooltip tooltip-bottom ${isMoving ||
                          index === messages.length - 1
                            ? "opacity-40 pointer-events-none"
                            : ""}"
                          data-tip=${index === messages.length - 1
                            ? "Already at bottom"
                            : "Move down"}
                          aria-label=${index === messages.length - 1
                            ? "Already at bottom"
                            : "Move down"}
                        >
                          <${ChevronDownIcon} className="w-3.5 h-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick=${(e) => handleDelete(e, msg.id)}
                          aria-disabled=${isDeleting ? "true" : "false"}
                          class="queue-item-delete btn btn-ghost btn-square btn-xs text-mitto-text-muted hover:bg-red-600/80 hover:text-mitto-text-strong tooltip tooltip-bottom ${isDeleting
                            ? "opacity-40 pointer-events-none"
                            : ""}"
                          data-tip="Remove from queue"
                          aria-label="Remove from queue"
                        >
                          <${TrashIcon} className="w-3.5 h-3.5" />
                        </button>
                      </div>
                    </div>
                  </li>
                `,
              )}
            </ul>
          `
        : html`
            <div
              class="queue-dropdown-empty px-3 py-4 text-center text-sm text-mitto-text-muted"
            >
              No messages in queue
            </div>
          `}
    </div>
  `;
}
