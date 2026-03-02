// Mitto Web Interface - Local Storage Utilities
// Functions for persisting state in localStorage and server-side
//
// For UI preferences (grouping mode, expanded groups), we use a hybrid approach:
// - localStorage is used as a fast cache for immediate reads
// - Server-side storage is the source of truth (persists across app launches)
// - On app load, we sync from server to localStorage
// - On changes, we update both localStorage and server

import { apiUrl } from "./api.js";
import { secureFetch, authFetch } from "./csrf.js";

// =============================================================================
// UI Preferences Server Sync
// =============================================================================

// In-memory cache of UI preferences (populated from server on init)
let uiPreferencesCache = null;
let uiPreferencesSyncPromise = null;
let uiPreferencesLoadedCallbacks = [];

/**
 * Register a callback to be called when UI preferences are loaded from server.
 * This is useful for components that need to re-read their state after server sync.
 *
 * If preferences have already been loaded, the callback is invoked immediately
 * to handle the case where a component subscribes after the initial load completes.
 *
 * @param {Function} callback - Function to call when preferences are loaded
 * @returns {Function} Unsubscribe function
 */
export function onUIPreferencesLoaded(callback) {
  uiPreferencesLoadedCallbacks.push(callback);

  // If preferences are already loaded, call the callback immediately
  // This handles the race condition where initUIPreferences() completes
  // before a component has a chance to subscribe
  if (uiPreferencesCache !== null) {
    try {
      callback(uiPreferencesCache);
    } catch (e) {
      console.warn("[Mitto] Error in UI preferences callback:", e);
    }
  }

  return () => {
    uiPreferencesLoadedCallbacks = uiPreferencesLoadedCallbacks.filter(
      (cb) => cb !== callback,
    );
  };
}

/**
 * Initialize UI preferences by loading from server.
 * This should be called once on app startup.
 * @returns {Promise<void>}
 */
export async function initUIPreferences() {
  if (uiPreferencesSyncPromise) {
    return uiPreferencesSyncPromise;
  }

  uiPreferencesSyncPromise = (async () => {
    try {
      // Use authFetch to include credentials for cross-origin requests
      const response = await authFetch(apiUrl("/api/ui-preferences"));
      if (response.ok) {
        const prefs = await response.json();
        uiPreferencesCache = prefs;

        // Sync to localStorage for fast reads
        if (prefs.grouping_mode) {
          localStorage.setItem(GROUPING_MODE_KEY, prefs.grouping_mode);
        }
        if (prefs.expanded_groups) {
          localStorage.setItem(
            EXPANDED_GROUPS_KEY,
            JSON.stringify(prefs.expanded_groups),
          );
        }
        if (prefs.filter_tab_grouping) {
          localStorage.setItem(
            FILTER_TAB_GROUPING_KEY,
            JSON.stringify(prefs.filter_tab_grouping),
          );
        }

        console.debug(
          "[Mitto] UI preferences loaded from server:",
          prefs.grouping_mode,
          "groups:",
          Object.keys(prefs.expanded_groups || {}).length,
          "tab groupings:",
          Object.keys(prefs.filter_tab_grouping || {}).length,
        );

        // Notify listeners that preferences have been loaded
        uiPreferencesLoadedCallbacks.forEach((cb) => {
          try {
            cb(prefs);
          } catch (e) {
            console.warn("[Mitto] Error in UI preferences callback:", e);
          }
        });
      }
    } catch (e) {
      console.warn("[Mitto] Failed to load UI preferences from server:", e);
    }
  })();

  return uiPreferencesSyncPromise;
}

/**
 * Save UI preferences to server (debounced).
 * @param {Object} prefs - The preferences to save
 */
let saveTimeout = null;
function saveUIPreferencesToServer(prefs) {
  // Debounce saves to avoid too many requests
  if (saveTimeout) {
    clearTimeout(saveTimeout);
  }

  saveTimeout = setTimeout(async () => {
    try {
      // Use secureFetch to include CSRF token and credentials for cross-origin requests
      const response = await secureFetch(apiUrl("/api/ui-preferences"), {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(prefs),
      });
      if (!response.ok) {
        console.warn(
          "[Mitto] Failed to save UI preferences to server:",
          response.status,
        );
      }
    } catch (e) {
      console.warn("[Mitto] Failed to save UI preferences to server:", e);
    }
  }, 500); // 500ms debounce
}

/**
 * Get current UI preferences for saving to server.
 * @returns {Object}
 */
function getCurrentUIPreferences() {
  return {
    grouping_mode: getGroupingMode(),
    expanded_groups: getExpandedGroups(),
    filter_tab_grouping: getAllFilterTabGroupings(),
  };
}

// =============================================================================
// Sync State Persistence (localStorage)
// =============================================================================

/**
 * Get the last seen sequence number for a session from localStorage.
 * Used for tracking sync state and reconnection.
 * @param {string} sessionId - The session ID
 * @returns {number} The last seen sequence number, or 0 if not found
 */
export function getLastSeenSeq(sessionId) {
  try {
    const value = localStorage.getItem(`mitto_last_seen_seq_${sessionId}`);
    if (value) {
      const seq = parseInt(value, 10);
      return isNaN(seq) ? 0 : seq;
    }
    return 0;
  } catch (e) {
    console.warn("Failed to read last seen seq from localStorage:", e);
    return 0;
  }
}

/**
 * Save the last seen sequence number for a session to localStorage.
 * Used for tracking sync state and reconnection.
 * @param {string} sessionId - The session ID
 * @param {number} seq - The sequence number to save
 */
export function setLastSeenSeq(sessionId, seq) {
  try {
    if (seq > 0) {
      localStorage.setItem(`mitto_last_seen_seq_${sessionId}`, String(seq));
    } else {
      localStorage.removeItem(`mitto_last_seen_seq_${sessionId}`);
    }
  } catch (e) {
    console.warn("Failed to save last seen seq to localStorage:", e);
  }
}

/**
 * Get the last active session ID from localStorage
 * @returns {string|null} The last active session ID, or null if not found
 */
export function getLastActiveSessionId() {
  try {
    return localStorage.getItem("mitto_last_session_id") || null;
  } catch (e) {
    return null;
  }
}

/**
 * Save the last active session ID to localStorage
 * @param {string|null} sessionId - The session ID to save, or null to clear
 */
export function setLastActiveSessionId(sessionId) {
  try {
    if (sessionId) {
      localStorage.setItem("mitto_last_session_id", sessionId);
    } else {
      localStorage.removeItem("mitto_last_session_id");
    }
  } catch (e) {
    console.warn("Failed to save last session ID to localStorage:", e);
  }
}

// =============================================================================
// Queue Dropdown Height Persistence (localStorage)
// =============================================================================

const QUEUE_HEIGHT_KEY = "mitto_queue_dropdown_height";
const DEFAULT_QUEUE_HEIGHT = 256; // Default max-h-64 equivalent in pixels
const MIN_QUEUE_HEIGHT = 100;
const MAX_QUEUE_HEIGHT = 500;

/**
 * Get the user's preferred queue dropdown height from localStorage
 * @returns {number} The height in pixels, or default if not set
 */
export function getQueueDropdownHeight() {
  try {
    const value = localStorage.getItem(QUEUE_HEIGHT_KEY);
    if (value) {
      const height = parseInt(value, 10);
      // Clamp to valid range
      return Math.max(MIN_QUEUE_HEIGHT, Math.min(MAX_QUEUE_HEIGHT, height));
    }
    return DEFAULT_QUEUE_HEIGHT;
  } catch (e) {
    console.warn("Failed to read queue dropdown height from localStorage:", e);
    return DEFAULT_QUEUE_HEIGHT;
  }
}

/**
 * Save the user's preferred queue dropdown height to localStorage
 * @param {number} height - The height in pixels
 */
export function setQueueDropdownHeight(height) {
  try {
    // Clamp to valid range
    const clampedHeight = Math.max(
      MIN_QUEUE_HEIGHT,
      Math.min(MAX_QUEUE_HEIGHT, height),
    );
    localStorage.setItem(QUEUE_HEIGHT_KEY, String(clampedHeight));
  } catch (e) {
    console.warn("Failed to save queue dropdown height to localStorage:", e);
  }
}

/**
 * Get the constraints for queue dropdown height
 * @returns {{min: number, max: number, default: number}}
 */
export function getQueueHeightConstraints() {
  return {
    min: MIN_QUEUE_HEIGHT,
    max: MAX_QUEUE_HEIGHT,
    default: DEFAULT_QUEUE_HEIGHT,
  };
}

// =============================================================================
// Agent Plan Panel Height Persistence (localStorage)
// =============================================================================

const AGENT_PLAN_HEIGHT_KEY = "mitto_agent_plan_height";
const DEFAULT_AGENT_PLAN_HEIGHT = 200;
const MIN_AGENT_PLAN_HEIGHT = 100;
const MAX_AGENT_PLAN_HEIGHT = 400;

/**
 * Get the user's preferred agent plan panel height from localStorage
 * @returns {number} The height in pixels, or default if not set
 */
export function getAgentPlanHeight() {
  try {
    const value = localStorage.getItem(AGENT_PLAN_HEIGHT_KEY);
    if (value) {
      const height = parseInt(value, 10);
      return Math.max(
        MIN_AGENT_PLAN_HEIGHT,
        Math.min(MAX_AGENT_PLAN_HEIGHT, height),
      );
    }
    return DEFAULT_AGENT_PLAN_HEIGHT;
  } catch (e) {
    console.warn("Failed to read agent plan height from localStorage:", e);
    return DEFAULT_AGENT_PLAN_HEIGHT;
  }
}

/**
 * Save the user's preferred agent plan panel height to localStorage
 * @param {number} height - The height in pixels
 */
export function setAgentPlanHeight(height) {
  try {
    const clampedHeight = Math.max(
      MIN_AGENT_PLAN_HEIGHT,
      Math.min(MAX_AGENT_PLAN_HEIGHT, height),
    );
    localStorage.setItem(AGENT_PLAN_HEIGHT_KEY, String(clampedHeight));
  } catch (e) {
    console.warn("Failed to save agent plan height to localStorage:", e);
  }
}

/**
 * Get the constraints for agent plan panel height
 * @returns {{min: number, max: number, default: number}}
 */
export function getAgentPlanHeightConstraints() {
  return {
    min: MIN_AGENT_PLAN_HEIGHT,
    max: MAX_AGENT_PLAN_HEIGHT,
    default: DEFAULT_AGENT_PLAN_HEIGHT,
  };
}

// =============================================================================
// Conversation Grouping Persistence (localStorage)
// =============================================================================

// Grouping modes: 'none' | 'server' | 'workspace'
// Note: 'workspace' groups by directory + ACP server combination
// (allows multiple workspaces to share the same folder with different agents)
const GROUPING_MODE_KEY = "mitto_conversation_grouping_mode";
const EXPANDED_GROUPS_KEY = "mitto_conversation_expanded_groups";
// Per-tab grouping (each filter tab can have its own grouping mode)
const FILTER_TAB_GROUPING_KEY = "mitto_filter_tab_grouping";

// Accordion mode: when enabled, only one group can be expanded at a time
// This is configured via settings (ui.web.single_expanded_group)
let singleExpandedGroupMode = false;

/**
 * Set whether accordion mode is enabled (single expanded group).
 * This should be called when config is loaded.
 * @param {boolean} enabled - Whether accordion mode is enabled
 */
export function setSingleExpandedGroupMode(enabled) {
  singleExpandedGroupMode = enabled;
}

/**
 * Get whether accordion mode is enabled (single expanded group).
 * @returns {boolean} Whether accordion mode is enabled
 */
export function getSingleExpandedGroupMode() {
  return singleExpandedGroupMode;
}

/**
 * Get the current conversation grouping mode from localStorage
 * @returns {'none' | 'server' | 'workspace'} The current grouping mode
 */
export function getGroupingMode() {
  try {
    const value = localStorage.getItem(GROUPING_MODE_KEY);
    // Support legacy 'folder' mode by migrating to 'workspace'
    if (value === "folder") {
      localStorage.setItem(GROUPING_MODE_KEY, "workspace");
      return "workspace";
    }
    return value === "server" || value === "workspace" ? value : "none";
  } catch (e) {
    console.warn("Failed to read grouping mode from localStorage:", e);
    return "none";
  }
}

/**
 * Save the conversation grouping mode to localStorage and server
 * @param {'none' | 'server' | 'workspace'} mode - The grouping mode to save
 */
export function setGroupingMode(mode) {
  try {
    if (mode === "server" || mode === "workspace") {
      localStorage.setItem(GROUPING_MODE_KEY, mode);
    } else {
      localStorage.removeItem(GROUPING_MODE_KEY);
    }
    // Also save to server for persistence across app launches
    saveUIPreferencesToServer(getCurrentUIPreferences());
    // Dispatch event for components that need to react to grouping mode changes
    // (e.g., App component for navigableSessions filtering in "visible_groups" cycling mode)
    window.dispatchEvent(
      new CustomEvent("mitto-grouping-mode-changed", {
        detail: { mode },
      }),
    );
  } catch (e) {
    console.warn("Failed to save grouping mode to localStorage:", e);
  }
}

/**
 * Cycle to the next grouping mode
 * @returns {'none' | 'server' | 'workspace'} The new grouping mode
 */
export function cycleGroupingMode() {
  const current = getGroupingMode();
  let next;
  switch (current) {
    case "none":
      next = "server";
      break;
    case "server":
      next = "workspace";
      break;
    case "workspace":
      next = "none";
      break;
    default:
      next = "none";
  }
  setGroupingMode(next);
  return next;
}

/**
 * Get the expanded/collapsed state of groups from localStorage
 * @returns {Object} Map of group keys to boolean (true = expanded)
 */
export function getExpandedGroups() {
  try {
    const value = localStorage.getItem(EXPANDED_GROUPS_KEY);
    if (value) {
      return JSON.parse(value);
    }
    return {};
  } catch (e) {
    console.warn("Failed to read expanded groups from localStorage:", e);
    return {};
  }
}

/**
 * Save the expanded/collapsed state of a group to localStorage and server
 * @param {string} groupKey - The unique key for the group (server name or workspace key)
 * @param {boolean} expanded - Whether the group is expanded
 */
export function setGroupExpanded(groupKey, expanded) {
  try {
    const groups = getExpandedGroups();
    groups[groupKey] = expanded;
    localStorage.setItem(EXPANDED_GROUPS_KEY, JSON.stringify(groups));
    // Also save to server for persistence across app launches
    saveUIPreferencesToServer(getCurrentUIPreferences());
    // Dispatch event for components that need to react to expanded groups changes
    // (e.g., App component for navigableSessions filtering in "visible_groups" cycling mode)
    window.dispatchEvent(
      new CustomEvent("mitto-expanded-groups-changed", {
        detail: { groupKey, expanded },
      }),
    );
  } catch (e) {
    console.warn("Failed to save expanded group state to localStorage:", e);
  }
}

/**
 * Check if a group is expanded
 * - Most groups default to expanded (true) if not yet tracked
 * - The "__archived__" group defaults to collapsed (false)
 * @param {string} groupKey - The unique key for the group
 * @returns {boolean} Whether the group is expanded
 */
export function isGroupExpanded(groupKey) {
  const groups = getExpandedGroups();
  // Check if explicitly set
  if (groupKey in groups) {
    return groups[groupKey];
  }
  // Default: archived section is collapsed, all others are expanded
  if (groupKey === "__archived__") {
    return false;
  }
  return true;
}

// =============================================================================
// Conversation Filter Tab Persistence (localStorage)
// =============================================================================

// Filter tabs: 'conversations' | 'periodic' | 'archived'
const FILTER_TAB_KEY = "mitto_conversation_filter_tab";

/**
 * Filter tab values
 */
export const FILTER_TAB = {
  CONVERSATIONS: "conversations",
  PERIODIC: "periodic",
  ARCHIVED: "archived",
};

/**
 * Get the current conversation filter tab from localStorage
 * @returns {'conversations' | 'periodic' | 'archived'} The current filter tab
 */
export function getFilterTab() {
  try {
    const value = localStorage.getItem(FILTER_TAB_KEY);
    if (
      value === FILTER_TAB.CONVERSATIONS ||
      value === FILTER_TAB.PERIODIC ||
      value === FILTER_TAB.ARCHIVED
    ) {
      return value;
    }
    return FILTER_TAB.CONVERSATIONS; // Default
  } catch (e) {
    console.warn("Failed to read filter tab from localStorage:", e);
    return FILTER_TAB.CONVERSATIONS;
  }
}

/**
 * Save the conversation filter tab to localStorage
 * @param {'conversations' | 'periodic' | 'archived'} tab - The filter tab to save
 */
export function setFilterTab(tab) {
  try {
    if (
      tab === FILTER_TAB.CONVERSATIONS ||
      tab === FILTER_TAB.PERIODIC ||
      tab === FILTER_TAB.ARCHIVED
    ) {
      localStorage.setItem(FILTER_TAB_KEY, tab);
    } else {
      localStorage.removeItem(FILTER_TAB_KEY);
    }
    // Dispatch event for components that need to react to filter tab changes
    // (e.g., App component for navigableSessions filtering)
    window.dispatchEvent(
      new CustomEvent("mitto-filter-tab-changed", {
        detail: { tab },
      }),
    );
  } catch (e) {
    console.warn("Failed to save filter tab to localStorage:", e);
  }
}

// =============================================================================
// Per-Tab Grouping Persistence (localStorage)
// =============================================================================

/**
 * Default grouping modes for each filter tab:
 * - Conversations: group by workspace
 * - Periodic: no grouping (flat list)
 * - Archived: group by workspace
 */
const DEFAULT_TAB_GROUPING = {
  [FILTER_TAB.CONVERSATIONS]: "workspace",
  [FILTER_TAB.PERIODIC]: "none",
  [FILTER_TAB.ARCHIVED]: "workspace",
};

/**
 * Get the grouping mode for a specific filter tab from localStorage
 * @param {string} tabId - The filter tab ID (conversations, periodic, archived)
 * @returns {'none' | 'server' | 'workspace'} The grouping mode for that tab
 */
export function getFilterTabGrouping(tabId) {
  try {
    const value = localStorage.getItem(FILTER_TAB_GROUPING_KEY);
    if (value) {
      const tabGroupings = JSON.parse(value);
      const mode = tabGroupings[tabId];
      if (mode === "none" || mode === "server" || mode === "workspace") {
        return mode;
      }
    }
    // Return default for this tab
    return DEFAULT_TAB_GROUPING[tabId] || "none";
  } catch (e) {
    console.warn("Failed to read filter tab grouping from localStorage:", e);
    return DEFAULT_TAB_GROUPING[tabId] || "none";
  }
}

/**
 * Save the grouping mode for a specific filter tab to localStorage and server
 * @param {string} tabId - The filter tab ID (conversations, periodic, archived)
 * @param {'none' | 'server' | 'workspace'} mode - The grouping mode to save
 */
export function setFilterTabGrouping(tabId, mode) {
  try {
    // Get current tab groupings
    let tabGroupings = {};
    const value = localStorage.getItem(FILTER_TAB_GROUPING_KEY);
    if (value) {
      tabGroupings = JSON.parse(value);
    }

    // Update the grouping for this tab
    if (mode === "none" || mode === "server" || mode === "workspace") {
      tabGroupings[tabId] = mode;
    } else {
      // Use default for invalid modes
      tabGroupings[tabId] = DEFAULT_TAB_GROUPING[tabId] || "none";
    }

    localStorage.setItem(FILTER_TAB_GROUPING_KEY, JSON.stringify(tabGroupings));

    // Also save to server for persistence across app launches
    saveUIPreferencesToServer(getCurrentUIPreferences());

    // Dispatch event for components that need to react to grouping mode changes
    window.dispatchEvent(
      new CustomEvent("mitto-grouping-mode-changed", {
        detail: { mode, tabId },
      }),
    );
  } catch (e) {
    console.warn("Failed to save filter tab grouping to localStorage:", e);
  }
}

// Valid filter tabs for validation
const VALID_FILTER_TABS = new Set([
  FILTER_TAB.CONVERSATIONS,
  FILTER_TAB.PERIODIC,
  FILTER_TAB.ARCHIVED,
]);

// Valid grouping modes for validation
const VALID_GROUPING_MODES = new Set(["none", "server", "workspace"]);

/**
 * Get all filter tab groupings from localStorage.
 * Filters to only known tabs and valid grouping modes to prevent
 * invalid data from being sent to the server API.
 * @returns {Object} Map of tabId to grouping mode
 */
export function getAllFilterTabGroupings() {
  try {
    const value = localStorage.getItem(FILTER_TAB_GROUPING_KEY);
    if (value) {
      const parsed = JSON.parse(value);
      // Filter to only valid tabs and modes
      const validated = {};
      for (const [tabId, mode] of Object.entries(parsed)) {
        if (VALID_FILTER_TABS.has(tabId) && VALID_GROUPING_MODES.has(mode)) {
          validated[tabId] = mode;
        }
      }
      return validated;
    }
    return {};
  } catch (e) {
    console.warn("Failed to read filter tab groupings from localStorage:", e);
    return {};
  }
}

/**
 * Cycle to the next grouping mode for a specific filter tab
 * @param {string} tabId - The filter tab ID
 * @returns {'none' | 'server' | 'workspace'} The new grouping mode
 */
export function cycleFilterTabGrouping(tabId) {
  const current = getFilterTabGrouping(tabId);
  let next;
  switch (current) {
    case "none":
      next = "server";
      break;
    case "server":
      next = "workspace";
      break;
    case "workspace":
      next = "none";
      break;
    default:
      next = "none";
  }
  setFilterTabGrouping(tabId, next);
  return next;
}
