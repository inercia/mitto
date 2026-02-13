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
 * @param {Function} callback - Function to call when preferences are loaded
 * @returns {Function} Unsubscribe function
 */
export function onUIPreferencesLoaded(callback) {
  uiPreferencesLoadedCallbacks.push(callback);
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

        console.debug(
          "[Mitto] UI preferences loaded from server:",
          prefs.grouping_mode,
          "groups:",
          Object.keys(prefs.expanded_groups || {}).length,
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

// Grouping modes: 'none' | 'server' | 'folder'
const GROUPING_MODE_KEY = "mitto_conversation_grouping_mode";
const EXPANDED_GROUPS_KEY = "mitto_conversation_expanded_groups";

/**
 * Get the current conversation grouping mode from localStorage
 * @returns {'none' | 'server' | 'folder'} The current grouping mode
 */
export function getGroupingMode() {
  try {
    const value = localStorage.getItem(GROUPING_MODE_KEY);
    return value === "server" || value === "folder" ? value : "none";
  } catch (e) {
    console.warn("Failed to read grouping mode from localStorage:", e);
    return "none";
  }
}

/**
 * Save the conversation grouping mode to localStorage and server
 * @param {'none' | 'server' | 'folder'} mode - The grouping mode to save
 */
export function setGroupingMode(mode) {
  try {
    if (mode === "server" || mode === "folder") {
      localStorage.setItem(GROUPING_MODE_KEY, mode);
    } else {
      localStorage.removeItem(GROUPING_MODE_KEY);
    }
    // Also save to server for persistence across app launches
    saveUIPreferencesToServer(getCurrentUIPreferences());
  } catch (e) {
    console.warn("Failed to save grouping mode to localStorage:", e);
  }
}

/**
 * Cycle to the next grouping mode
 * @returns {'none' | 'server' | 'folder'} The new grouping mode
 */
export function cycleGroupingMode() {
  const current = getGroupingMode();
  let next;
  switch (current) {
    case "none":
      next = "server";
      break;
    case "server":
      next = "folder";
      break;
    case "folder":
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
 * @param {string} groupKey - The unique key for the group (server name or folder path)
 * @param {boolean} expanded - Whether the group is expanded
 */
export function setGroupExpanded(groupKey, expanded) {
  try {
    const groups = getExpandedGroups();
    groups[groupKey] = expanded;
    localStorage.setItem(EXPANDED_GROUPS_KEY, JSON.stringify(groups));
    // Also save to server for persistence across app launches
    saveUIPreferencesToServer(getCurrentUIPreferences());
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
