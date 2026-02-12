// Mitto Web Interface - Agent Plan Panel Component
// Displays the agent's current execution plan with task status

const { useEffect, useRef, useCallback, useState, html } = window.preact;

import { ChevronDownIcon, ChevronUpIcon, GripIcon } from "./Icons.js";
import { useResizeHandle } from "../hooks/useResizeHandle.js";
import {
  getAgentPlanHeight,
  setAgentPlanHeight,
  getAgentPlanHeightConstraints,
} from "../utils/storage.js";

/**
 * Get status icon and color for a plan entry
 * @param {string} status - Status: pending, in_progress, completed
 * @returns {{ icon: string, colorClass: string }}
 */
function getStatusDisplay(status) {
  switch (status) {
    case "completed":
      return { icon: "✓", colorClass: "text-green-400" };
    case "in_progress":
      return { icon: "●", colorClass: "text-blue-400 animate-pulse" };
    case "pending":
    default:
      return { icon: "○", colorClass: "text-gray-500" };
  }
}

/**
 * Get priority badge for a plan entry
 * @param {string} priority - Priority: high, medium, low
 * @returns {string} CSS classes for the badge
 */
function getPriorityBadge(priority) {
  switch (priority) {
    case "high":
      return "bg-red-500/20 text-red-400 border-red-500/30";
    case "medium":
      return "bg-yellow-500/20 text-yellow-400 border-yellow-500/30";
    case "low":
    default:
      return "bg-gray-500/20 text-gray-400 border-gray-500/30";
  }
}

/**
 * AgentPlanPanel component - displays the agent's execution plan
 * @param {Object} props
 * @param {boolean} props.isOpen - Whether the panel is visible
 * @param {Function} props.onClose - Callback to close the panel
 * @param {Function} props.onToggle - Callback to toggle the panel
 * @param {Array} props.entries - Array of plan entries { content, priority, status }
 * @param {boolean} props.userPinned - Whether user manually opened the panel
 */
export function AgentPlanPanel({
  isOpen,
  onClose,
  onToggle,
  entries = [],
  userPinned = false,
}) {
  const panelRef = useRef(null);
  const autoCollapseTimerRef = useRef(null);

  // Get height constraints for resize
  const heightConstraints = getAgentPlanHeightConstraints();

  // Use resize handle hook for drag-to-resize functionality
  const { height, isDragging, handleProps } = useResizeHandle({
    initialHeight: getAgentPlanHeight(),
    minHeight: heightConstraints.min,
    maxHeight: heightConstraints.max,
    direction: "down", // Resize downward from top
    onDragStart: () => {
      // Pause auto-collapse timer while dragging
      if (autoCollapseTimerRef.current) {
        clearTimeout(autoCollapseTimerRef.current);
        autoCollapseTimerRef.current = null;
      }
    },
    onDragEnd: (finalHeight) => {
      // Persist the height when drag ends
      setAgentPlanHeight(finalHeight);
    },
  });

  // Auto-collapse after 4 seconds if not user-pinned
  useEffect(() => {
    if (isOpen && !userPinned) {
      autoCollapseTimerRef.current = setTimeout(() => {
        onClose();
      }, 4000);
    }
    return () => {
      if (autoCollapseTimerRef.current) {
        clearTimeout(autoCollapseTimerRef.current);
      }
    };
  }, [isOpen, userPinned, onClose]);

  // Close panel when clicking outside
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event) => {
      if (panelRef.current && !panelRef.current.contains(event.target)) {
        // Also check if the click was on the plan indicator button (to avoid immediate close when opening)
        const planIndicator = event.target.closest(".agent-plan-indicator");
        if (!planIndicator) {
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

  // Pause auto-collapse on mouse enter
  const handleMouseEnter = useCallback(() => {
    if (autoCollapseTimerRef.current) {
      clearTimeout(autoCollapseTimerRef.current);
      autoCollapseTimerRef.current = null;
    }
  }, []);

  // Resume auto-collapse on mouse leave (if not pinned)
  const handleMouseLeave = useCallback(() => {
    if (isOpen && !userPinned) {
      autoCollapseTimerRef.current = setTimeout(() => {
        onClose();
      }, 2000);
    }
  }, [isOpen, userPinned, onClose]);

  // Count tasks by status
  const completedCount = entries.filter((e) => e.status === "completed").length;
  const inProgressCount = entries.filter(
    (e) => e.status === "in_progress",
  ).length;
  const totalCount = entries.length;

  // Panel classes - use same color scheme as QueueDropdown (bg-slate-700/95)
  const panelClasses = `agent-plan-panel absolute top-0 left-0 right-0 w-full bg-slate-700/95 backdrop-blur-sm border-b border-l border-r border-slate-600 rounded-b-lg overflow-hidden z-20 ${
    isDragging ? "" : "transition-all duration-300 ease-out"
  } ${isOpen ? "opacity-100" : "opacity-0 pointer-events-none border-0"}`;

  const panelStyle = isOpen
    ? `height: ${height}px; box-shadow: 0 8px 16px rgba(0, 0, 0, 0.3);`
    : "height: 0px;";

  // Calculate list height (total - header - resize handle)
  const listMaxHeight = Math.max(50, height - 56);

  return html`
    <div
      ref=${panelRef}
      class=${panelClasses}
      style="transform-origin: top; ${panelStyle}"
      onMouseEnter=${handleMouseEnter}
      onMouseLeave=${handleMouseLeave}
    >
      <!-- Header -->
      <div
        class="agent-plan-header px-3 py-2 border-b border-slate-700 flex items-center justify-between cursor-pointer hover:bg-slate-600/50"
        onClick=${onToggle}
      >
        <div class="flex items-center gap-2">
          <span class="text-xs font-medium text-gray-400 uppercase tracking-wide">
            Agent Plan
          </span>
          ${totalCount > 0 && html`
            <span class="text-xs text-gray-500">
              (${completedCount}/${totalCount} complete${inProgressCount > 0 ? `, ${inProgressCount} in progress` : ""})
            </span>
          `}
        </div>
        <${ChevronUpIcon} className="w-4 h-4 text-gray-400" />
      </div>

      <!-- Task List -->
      ${entries.length > 0
        ? html`
            <div
              class="agent-plan-list overflow-y-auto"
              style="max-height: ${listMaxHeight}px;"
            >
              ${entries.map(
                (entry, index) => {
                  const statusDisplay = getStatusDisplay(entry.status);
                  return html`
                    <div
                      key=${index}
                      class="agent-plan-item flex items-start gap-2 px-3 py-2 hover:bg-slate-700/50 transition-colors border-b border-slate-700/50 last:border-b-0"
                    >
                      <span class="flex-shrink-0 mt-0.5 ${statusDisplay.colorClass}">
                        ${statusDisplay.icon}
                      </span>
                      <span class="flex-1 text-sm text-gray-200">
                        ${entry.content}
                      </span>
                      ${entry.priority && entry.priority !== "medium" && html`
                        <span class="flex-shrink-0 text-xs px-1.5 py-0.5 rounded border ${getPriorityBadge(entry.priority)}">
                          ${entry.priority}
                        </span>
                      `}
                    </div>
                  `;
                },
              )}
            </div>
          `
        : html`
            <div class="agent-plan-empty px-3 py-4 text-center text-sm text-gray-500">
              No plan available
            </div>
          `}

      <!-- Resize Handle at bottom edge -->
      <div
        class="agent-plan-resize-handle flex items-center justify-center py-1 cursor-ns-resize hover:bg-slate-600/50 transition-colors select-none touch-none ${isDragging ? "bg-slate-600/50" : ""}"
        ...${handleProps}
        title="Drag to resize"
      >
        <${GripIcon} className="w-6 h-1.5 text-gray-500" />
      </div>
    </div>
  `;
}

/**
 * AgentPlanIndicator - minimal indicator shown when panel is collapsed
 * @param {Object} props
 * @param {Function} props.onClick - Callback when clicked
 * @param {Array} props.entries - Array of plan entries
 * @param {boolean} props.hasNewUpdate - Whether there's a new update to show
 */
export function AgentPlanIndicator({ onClick, entries = [], hasNewUpdate = false }) {
  if (entries.length === 0) return null;

  const completedCount = entries.filter((e) => e.status === "completed").length;
  const inProgressCount = entries.filter((e) => e.status === "in_progress").length;
  const totalCount = entries.length;

  return html`
    <button
      type="button"
      onClick=${onClick}
      class="agent-plan-indicator flex items-center gap-1.5 px-2 py-1 rounded-full bg-slate-700/80 hover:bg-slate-600/80 transition-colors text-xs ${hasNewUpdate ? "ring-2 ring-blue-400/50" : ""}"
      title="View agent plan"
    >
      ${inProgressCount > 0
        ? html`<span class="text-blue-400 animate-pulse">●</span>`
        : html`<span class="text-gray-400">○</span>`}
      <span class="text-gray-300">${completedCount}/${totalCount}</span>
      <${ChevronDownIcon} className="w-3 h-3 text-gray-400" />
    </button>
  `;
}

