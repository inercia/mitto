// Mitto Web Interface - Queue Dropdown Component
// Displays and manages queued messages waiting to be sent to the agent

const { useEffect, useRef, useCallback, html } = window.preact;

import {
  TrashIcon,
  ChevronUpIcon,
  ChevronDownIcon,
} from "./Icons.js";

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

  // Compute classes for animation - positioned as floating overlay above the input
  // Shadow only on top (negative Y offset) to cast over conversation area, not over input
  const dropdownClasses = `queue-dropdown absolute bottom-full left-0 right-0 w-full bg-slate-700/95 backdrop-blur-sm border-t border-l border-r border-slate-600 rounded-t-lg overflow-hidden transition-all duration-300 ease-out z-20 ${
    isOpen ? "max-h-64 opacity-100" : "max-h-0 opacity-0 pointer-events-none border-0"
  }`;
  const dropdownStyle = isOpen ? "box-shadow: 0 -8px 16px rgba(0, 0, 0, 0.3);" : "";

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
      style="transform-origin: bottom; ${dropdownStyle}"
      onMouseEnter=${handleMouseEnter}
      onMouseLeave=${handleMouseLeave}
    >
      <div
        class="queue-dropdown-header px-3 py-2 border-b border-slate-700 flex items-center justify-between"
      >
        <span class="text-xs font-medium text-gray-400 uppercase tracking-wide">
          Queued Messages (${queueLength}/${maxSize})
        </span>
      </div>
      ${messages.length > 0
        ? html`
            <div class="queue-dropdown-list max-h-48 overflow-y-auto">
              ${messages.map(
                (msg, index) => html`
                  <div
                    key=${msg.id}
                    class="queue-dropdown-item flex items-center gap-2 px-3 py-2 hover:bg-slate-700/50 transition-colors border-b border-slate-700/50 last:border-b-0 group"
                  >
                    <span
                      class="queue-item-number text-xs text-gray-500 font-mono w-4 flex-shrink-0"
                    >
                      ${index + 1}
                    </span>
                    <span
                      class="queue-item-text flex-1 text-sm text-gray-200 truncate"
                      title=${msg.message}
                    >
                      ${msg.title || truncateText(msg.message)}
                    </span>
                    <div
                      class="queue-item-actions flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity flex-shrink-0"
                    >
                      <button
                        type="button"
                        onClick=${(e) => handleMoveUp(e, msg.id)}
                        disabled=${isMoving || index === 0}
                        class="queue-item-move-up p-1 rounded hover:bg-slate-600 text-gray-400 hover:text-white transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:bg-transparent disabled:hover:text-gray-400"
                        title=${index === 0 ? "Already at top" : "Move up"}
                      >
                        <${ChevronUpIcon} className="w-3.5 h-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick=${(e) => handleMoveDown(e, msg.id)}
                        disabled=${isMoving || index === messages.length - 1}
                        class="queue-item-move-down p-1 rounded hover:bg-slate-600 text-gray-400 hover:text-white transition-colors disabled:opacity-30 disabled:cursor-not-allowed disabled:hover:bg-transparent disabled:hover:text-gray-400"
                        title=${index === messages.length - 1
                          ? "Already at bottom"
                          : "Move down"}
                      >
                        <${ChevronDownIcon} className="w-3.5 h-3.5" />
                      </button>
                      <button
                        type="button"
                        onClick=${(e) => handleDelete(e, msg.id)}
                        disabled=${isDeleting}
                        class="queue-item-delete p-1 rounded hover:bg-red-600/80 text-gray-400 hover:text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                        title="Remove from queue"
                      >
                        <${TrashIcon} className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </div>
                `,
              )}
            </div>
          `
        : html`
            <div
              class="queue-dropdown-empty px-3 py-4 text-center text-sm text-gray-500"
            >
              No messages in queue
            </div>
          `}
    </div>
  `;
}
