// Mitto Web Interface - Local Storage Utilities
// Functions for persisting state in localStorage and server-side
//
// For UI preferences (grouping mode, expanded groups), we use a hybrid approach:
// - localStorage is used as a fast cache for immediate reads
// - Server-side storage is the source of truth (persists across app launches)
// - On app load, we sync from server to localStorage
// - On changes, we update both localStorage and server

import { secureFetch, authFetch } from "./csrf.js";
import { endpoints } from "./endpoints.js";

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
    migrateLegacyTabStorage();
    try {
      // Use authFetch to include credentials for cross-origin requests
      const response = await authFetch(endpoints.misc.uiPreferences());
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
        if (prefs.prompt_sort_mode) {
          localStorage.setItem(PROMPT_SORT_MODE_KEY, prefs.prompt_sort_mode);
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
      const response = await secureFetch(endpoints.misc.uiPreferences(), {
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
    prompt_sort_mode: getPromptSortMode(),
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

// Per-group (folder) last-focused conversation. Lets the sidebar restore the
// conversation that was last focused in a group when that group is reopened,
// keyed by the unified-tree folder key. Stored as a single JSON object.
const LAST_SESSION_BY_GROUP_KEY = "mitto_last_session_by_group";

/**
 * Get the last-focused session ID for a sidebar group (folder).
 * @param {string} groupKey - The unified-tree folder key
 * @returns {string|null} The remembered session ID, or null if none
 */
export function getLastSessionForGroup(groupKey) {
  try {
    const raw = localStorage.getItem(LAST_SESSION_BY_GROUP_KEY);
    if (!raw) return null;
    const map = JSON.parse(raw);
    return (map && map[groupKey]) || null;
  } catch (e) {
    return null;
  }
}

/**
 * Save the last-focused session ID for a sidebar group (folder).
 * @param {string} groupKey - The unified-tree folder key
 * @param {string|null} sessionId - The session ID to remember, or null to clear
 */
export function setLastSessionForGroup(groupKey, sessionId) {
  if (!groupKey) return;
  try {
    const raw = localStorage.getItem(LAST_SESSION_BY_GROUP_KEY);
    let map = {};
    if (raw) {
      try {
        map = JSON.parse(raw) || {};
      } catch (e) {
        map = {};
      }
    }
    if (sessionId) {
      map[groupKey] = sessionId;
    } else {
      delete map[groupKey];
    }
    localStorage.setItem(LAST_SESSION_BY_GROUP_KEY, JSON.stringify(map));
  } catch (e) {
    console.warn("Failed to save last session for group to localStorage:", e);
  }
}

// =============================================================================
// Legacy Tab Storage Migration (mitto-1er.8.4-C)
// =============================================================================

const DETAB_MIGRATION_KEY = "mitto_detab_migration_done";

/**
 * One-time idempotent migration that removes orphaned per-tab localStorage
 * keys left over from the 3-tab sidebar (removed in mitto-1er.8.4). Safe to
 * call on every startup — the guard key prevents redundant work.
 *
 * Cleans up:
 *   - mitto_conversation_filter_tab       (persisted active tab)
 *   - mitto_filter_tab_grouping           (per-tab grouping modes)
 *   - mitto_last_session_id_{tab}         (per-tab last-focused session)
 *   - Tab-scoped expanded-group entries.  OLD keys contain the control-char
 *     separator \u0001 (e.g. "conversations\u0001/path"). NEW unscoped keys
 *     (bare folder paths, "archived:<key>", "parent:<id>") never contain
 *     \u0001, so pruning by \u0001 presence is collision-free.
 */
export function migrateLegacyTabStorage() {
  try {
    if (localStorage.getItem(DETAB_MIGRATION_KEY)) return;

    // Remove orphaned top-level keys (use string literals; constants deleted)
    localStorage.removeItem("mitto_conversation_filter_tab");
    localStorage.removeItem("mitto_filter_tab_grouping");
    ["conversations", "periodic", "archived"].forEach((t) =>
      localStorage.removeItem("mitto_last_session_id_" + t),
    );

    // Strip tab-scoped entries from the expanded-groups object. Old tab-scoped
    // keys contain \u0001; new unscoped keys (folder paths, "archived:…",
    // "parent:…") never do — so this is safe and collision-free.
    try {
      const raw = localStorage.getItem("mitto_conversation_expanded_groups");
      if (raw) {
        const obj = JSON.parse(raw);
        if (obj && typeof obj === "object") {
          let changed = false;
          for (const key of Object.keys(obj)) {
            if (key.includes("\u0001")) {
              delete obj[key];
              changed = true;
            }
          }
          if (changed) {
            localStorage.setItem(
              "mitto_conversation_expanded_groups",
              JSON.stringify(obj),
            );
          }
        }
      }
    } catch (innerErr) {
      console.warn(
        "[Mitto] Failed to prune tab-scoped expanded groups:",
        innerErr,
      );
    }

    localStorage.setItem(DETAB_MIGRATION_KEY, "1");
  } catch (e) {
    console.warn("[Mitto] Failed to run legacy tab storage migration:", e);
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
// UI Prompt Panel Height Persistence (localStorage)
// =============================================================================

const UI_PROMPT_HEIGHT_KEY = "mitto_ui_prompt_panel_height";
const DEFAULT_UI_PROMPT_HEIGHT = 350;
const MIN_UI_PROMPT_HEIGHT = 150;
const MAX_UI_PROMPT_HEIGHT = 600;

/**
 * Get the user's preferred UI prompt panel height from localStorage
 * @returns {number} The height in pixels, or default if not set
 */
export function getUIPromptPanelHeight() {
  try {
    const value = localStorage.getItem(UI_PROMPT_HEIGHT_KEY);
    if (value) {
      const height = parseInt(value, 10);
      return Math.max(
        MIN_UI_PROMPT_HEIGHT,
        Math.min(MAX_UI_PROMPT_HEIGHT, height),
      );
    }
    return DEFAULT_UI_PROMPT_HEIGHT;
  } catch (e) {
    return DEFAULT_UI_PROMPT_HEIGHT;
  }
}

/**
 * Save the user's preferred UI prompt panel height to localStorage
 * @param {number} height - The height in pixels
 */
export function setUIPromptPanelHeight(height) {
  try {
    const clampedHeight = Math.max(
      MIN_UI_PROMPT_HEIGHT,
      Math.min(MAX_UI_PROMPT_HEIGHT, height),
    );
    localStorage.setItem(UI_PROMPT_HEIGHT_KEY, String(clampedHeight));
  } catch (e) {
    console.warn("Failed to save UI prompt panel height to localStorage:", e);
  }
}

/**
 * Get UI prompt panel height constraints
 * @returns {{ min: number, max: number, default: number }}
 */
export function getUIPromptPanelHeightConstraints() {
  return {
    min: MIN_UI_PROMPT_HEIGHT,
    max: MAX_UI_PROMPT_HEIGHT,
    default: DEFAULT_UI_PROMPT_HEIGHT,
  };
}

// =============================================================================
// Sidebar Width Persistence (localStorage)
// =============================================================================

const SIDEBAR_WIDTH_KEY = "mitto_sidebar_width";
const DEFAULT_SIDEBAR_WIDTH = 320; // w-80 = 320px (current default)
const MIN_SIDEBAR_WIDTH = 320; // Cannot shrink below current default
const MAX_SIDEBAR_WIDTH = 640; // 2× default

/**
 * Get the user's preferred sidebar width from localStorage
 * @returns {number} The width in pixels, or default if not set
 */
export function getSidebarWidth() {
  try {
    const value = localStorage.getItem(SIDEBAR_WIDTH_KEY);
    if (value) {
      const width = parseInt(value, 10);
      return Math.max(MIN_SIDEBAR_WIDTH, Math.min(MAX_SIDEBAR_WIDTH, width));
    }
    return DEFAULT_SIDEBAR_WIDTH;
  } catch (e) {
    console.warn("Failed to read sidebar width from localStorage:", e);
    return DEFAULT_SIDEBAR_WIDTH;
  }
}

/**
 * Save the user's preferred sidebar width to localStorage
 * @param {number} width - The width in pixels
 */
export function setSidebarWidth(width) {
  try {
    const clampedWidth = Math.max(
      MIN_SIDEBAR_WIDTH,
      Math.min(MAX_SIDEBAR_WIDTH, width),
    );
    localStorage.setItem(SIDEBAR_WIDTH_KEY, String(clampedWidth));
  } catch (e) {
    console.warn("Failed to save sidebar width to localStorage:", e);
  }
}

/**
 * Get the constraints for sidebar width
 * @returns {{min: number, max: number, default: number}}
 */
export function getSidebarWidthConstraints() {
  return {
    min: MIN_SIDEBAR_WIDTH,
    max: MAX_SIDEBAR_WIDTH,
    default: DEFAULT_SIDEBAR_WIDTH,
  };
}

// =============================================================================
// Textarea Min Height Persistence (localStorage)
// =============================================================================

const TEXTAREA_MIN_HEIGHT_KEY = "mitto_textarea_min_height";
const DEFAULT_TEXTAREA_MIN_HEIGHT = 80; // Current CSS min-height (rows="3")
const MIN_TEXTAREA_MIN_HEIGHT = 80; // Cannot shrink below current default
const MAX_TEXTAREA_MIN_HEIGHT = 400; // Allow generous resize range
const TEXTAREA_HARD_MAX_HEIGHT = 500; // Hard cap for auto-grow

/**
 * Get the user's preferred textarea min height from localStorage
 * @returns {number} The height in pixels, or default if not set
 */
export function getTextareaMinHeight() {
  try {
    const value = localStorage.getItem(TEXTAREA_MIN_HEIGHT_KEY);
    if (value) {
      const height = parseInt(value, 10);
      return Math.max(
        MIN_TEXTAREA_MIN_HEIGHT,
        Math.min(MAX_TEXTAREA_MIN_HEIGHT, height),
      );
    }
    return DEFAULT_TEXTAREA_MIN_HEIGHT;
  } catch (e) {
    console.warn("Failed to read textarea min height from localStorage:", e);
    return DEFAULT_TEXTAREA_MIN_HEIGHT;
  }
}

/**
 * Save the user's preferred textarea min height to localStorage
 * @param {number} height - The height in pixels
 */
export function setTextareaMinHeight(height) {
  try {
    const clampedHeight = Math.max(
      MIN_TEXTAREA_MIN_HEIGHT,
      Math.min(MAX_TEXTAREA_MIN_HEIGHT, height),
    );
    localStorage.setItem(TEXTAREA_MIN_HEIGHT_KEY, String(clampedHeight));
  } catch (e) {
    console.warn("Failed to save textarea min height to localStorage:", e);
  }
}

/**
 * Get the constraints for textarea min height
 * @returns {{min: number, max: number, default: number, hardMax: number}}
 */
export function getTextareaMinHeightConstraints() {
  return {
    min: MIN_TEXTAREA_MIN_HEIGHT,
    max: MAX_TEXTAREA_MIN_HEIGHT,
    default: DEFAULT_TEXTAREA_MIN_HEIGHT,
    hardMax: TEXTAREA_HARD_MAX_HEIGHT,
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
// Prompt sorting mode: "alphabetical" (default) or "color"
const PROMPT_SORT_MODE_KEY = "mitto_prompt_sort_mode";

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
 * @returns {'none' | 'server' | 'folder' | 'workspace'} The current grouping mode
 */
export function getGroupingMode() {
  try {
    const value = localStorage.getItem(GROUPING_MODE_KEY);
    if (value === "server" || value === "folder" || value === "workspace") {
      return value;
    }
    return "folder"; // Default to folder grouping
  } catch (e) {
    console.warn("Failed to read grouping mode from localStorage:", e);
    return "folder"; // Default to folder grouping
  }
}

/**
 * Save the conversation grouping mode to localStorage and server
 * @param {'none' | 'server' | 'folder' | 'workspace'} mode - The grouping mode to save
 */
export function setGroupingMode(mode) {
  try {
    if (mode === "server" || mode === "folder" || mode === "workspace") {
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
 * Cycle order: none -> server -> folder -> workspace -> none
 * @returns {'none' | 'server' | 'folder' | 'workspace'} The new grouping mode
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
// Conversation Filter Tab Enum (kept for sessionGrouping.js / SessionItem.js /
// SessionList.js which classify sessions by category)
// =============================================================================

/**
 * Filter tab values
 */
export const FILTER_TAB = {
  CONVERSATIONS: "conversations",
  PERIODIC: "periodic",
  ARCHIVED: "archived",
};

/**
 * Derive which filter tab a session belongs to from its state. Mirrors the
 * tab-filtering logic used throughout the app (archived → archived,
 * periodic_enabled → periodic, otherwise → conversations).
 *
 * NOTE: uses periodic_enabled (runs active), NOT periodic_configured (config exists).
 * A paused/draft periodic conversation (configured but not enabled) falls into
 * the CONVERSATIONS group — its editor is still visible via periodic_configured.
 * @param {Object} session - A session object (archived, periodic_enabled flags)
 * @returns {string} The filter tab for the session
 */
export function getFilterTabForSession(session) {
  if (!session) return FILTER_TAB.CONVERSATIONS;
  if (session.archived) return FILTER_TAB.ARCHIVED;
  if (session.periodic_enabled) return FILTER_TAB.PERIODIC;
  return FILTER_TAB.CONVERSATIONS;
}

// =============================================================================
// Category Filter (mitto-1er.10) — sessionStorage (browser-session scope)
// =============================================================================

const CATEGORY_FILTER_KEY = "mitto_category_filter";

/**
 * Default category filter: all categories visible.
 * Shape: { regular, periodic, archived, tasks } (all booleans).
 */
export const DEFAULT_CATEGORY_FILTER = {
  regular: true,
  periodic: true,
  archived: true,
  tasks: true,
};

/**
 * Read the category filter from sessionStorage (browser-session scope; resets
 * in a fresh browser session). Returns the all-visible default when unset or
 * invalid.
 * @returns {{regular: boolean, periodic: boolean, archived: boolean, tasks: boolean}}
 */
export function getCategoryFilter() {
  try {
    const value = sessionStorage.getItem(CATEGORY_FILTER_KEY);
    if (!value) return { ...DEFAULT_CATEGORY_FILTER };
    const parsed = JSON.parse(value);
    return {
      regular: parsed.regular !== false,
      periodic: parsed.periodic !== false,
      archived: parsed.archived !== false,
      tasks: parsed.tasks !== false,
    };
  } catch (e) {
    console.warn("Failed to read category filter from sessionStorage:", e);
    return { ...DEFAULT_CATEGORY_FILTER };
  }
}

/**
 * Persist the category filter to sessionStorage (browser-session scope).
 * @param {{regular: boolean, periodic: boolean, archived: boolean, tasks: boolean}} state
 */
export function setCategoryFilter(state) {
  try {
    const s = state || {};
    const normalized = {
      regular: s.regular !== false,
      periodic: s.periodic !== false,
      archived: s.archived !== false,
      tasks: s.tasks !== false,
    };
    sessionStorage.setItem(CATEGORY_FILTER_KEY, JSON.stringify(normalized));
  } catch (e) {
    console.warn("Failed to save category filter to sessionStorage:", e);
  }
}

// =============================================================================
// Per-Tab Grouping Persistence (localStorage)
// =============================================================================

/**
// =============================================================================
// Prompt Sorting Mode
// =============================================================================

/**
 * Get the prompt sorting mode from localStorage
 * @returns {'alphabetical' | 'color'} - The sorting mode (default: 'alphabetical')
 */
export function getPromptSortMode() {
  try {
    const value = localStorage.getItem(PROMPT_SORT_MODE_KEY);
    if (value === "color" || value === "alphabetical") {
      return value;
    }
  } catch (e) {
    console.warn("[Mitto] Failed to get prompt sort mode:", e);
  }
  return "alphabetical"; // Default
}

/**
 * Save the prompt sorting mode to localStorage and server
 * @param {'alphabetical' | 'color'} mode - The sorting mode to save
 */
export function setPromptSortMode(mode) {
  try {
    if (mode === "alphabetical" || mode === "color") {
      localStorage.setItem(PROMPT_SORT_MODE_KEY, mode);
    } else {
      localStorage.removeItem(PROMPT_SORT_MODE_KEY);
    }
    // Also save to server for persistence across app launches
    saveUIPreferencesToServer(getCurrentUIPreferences());
    // Dispatch event for components that need to react to sort mode changes
    window.dispatchEvent(
      new CustomEvent("mitto-prompt-sort-mode-changed", {
        detail: { mode },
      }),
    );
  } catch (e) {
    console.warn("[Mitto] Failed to set prompt sort mode:", e);
  }
}

// =============================================================================
// Beads View Filters
// =============================================================================
//
// Persists the Beads view filter criteria (type, search text) in localStorage
// so they are restored when the user navigates away from the Beads view and
// returns within the same session. These do not need to survive app restarts,
// so a simple localStorage-only approach is used (no server sync). The status
// filter is intentionally not persisted here — it lives only in memory.

const BEADS_FILTERS_KEY = "mitto_beads_filters";

const DEFAULT_BEADS_FILTERS = { type: "all", search: "" };

/**
 * Get the persisted Beads view filters from localStorage.
 * @returns {{type: string, search: string}} The filter state, falling back to
 *          defaults ("all"/"") for any missing field.
 */
export function getBeadsFilters() {
  try {
    const value = localStorage.getItem(BEADS_FILTERS_KEY);
    if (value) {
      const parsed = JSON.parse(value);
      return {
        type:
          typeof parsed.type === "string"
            ? parsed.type
            : DEFAULT_BEADS_FILTERS.type,
        search:
          typeof parsed.search === "string"
            ? parsed.search
            : DEFAULT_BEADS_FILTERS.search,
      };
    }
  } catch (e) {
    console.warn("Failed to read Beads filters from localStorage:", e);
  }
  return { ...DEFAULT_BEADS_FILTERS };
}

/**
 * Persist the Beads view filters to localStorage.
 * @param {{type?: string, search?: string}} filters - Filter state to save.
 */
export function setBeadsFilters(filters) {
  try {
    const toStore = {
      type: filters?.type ?? DEFAULT_BEADS_FILTERS.type,
      search: filters?.search ?? DEFAULT_BEADS_FILTERS.search,
    };
    localStorage.setItem(BEADS_FILTERS_KEY, JSON.stringify(toStore));
  } catch (e) {
    console.warn("Failed to save Beads filters to localStorage:", e);
  }
}

// ---------------------------------------------------------------------------
// Beads grouping state (toolbar toggle + per-epic expand/collapse)
// ---------------------------------------------------------------------------
// The status filter is deliberately in-memory only (beadsStatusToggles) so it
// is NOT included here. This key stores the grouping toggle and the set of
// collapsed epic IDs, both of which survive a full reload. Epics are EXPANDED
// by default; only the epics the user explicitly collapses are persisted.

const BEADS_GROUPING_KEY = "mitto_beads_grouping";
const DENSITY_KEY = "mitto_conversation_density";

const DEFAULT_BEADS_GROUPING = { enabled: true, collapsedEpics: [] };

/**
 * Get the persisted Beads grouping state from localStorage.
 * @returns {{enabled: boolean, collapsedEpics: string[]}} Grouping state,
 *          falling back to defaults for any missing or malformed fields.
 */
export function getBeadsGrouping() {
  try {
    const value = localStorage.getItem(BEADS_GROUPING_KEY);
    if (value) {
      const parsed = JSON.parse(value);
      return {
        enabled:
          typeof parsed.enabled === "boolean"
            ? parsed.enabled
            : DEFAULT_BEADS_GROUPING.enabled,
        collapsedEpics: Array.isArray(parsed.collapsedEpics)
          ? parsed.collapsedEpics.filter((x) => typeof x === "string")
          : DEFAULT_BEADS_GROUPING.collapsedEpics,
      };
    }
  } catch (e) {
    console.warn("Failed to read Beads grouping from localStorage:", e);
  }
  return { ...DEFAULT_BEADS_GROUPING, collapsedEpics: [] };
}

/**
 * Persist the Beads grouping state to localStorage.
 * @param {{enabled?: boolean, collapsedEpics?: string[]}} state - Grouping state to save.
 */
export function setBeadsGrouping(state) {
  try {
    const toStore = {
      enabled:
        typeof state?.enabled === "boolean"
          ? state.enabled
          : DEFAULT_BEADS_GROUPING.enabled,
      collapsedEpics: Array.isArray(state?.collapsedEpics)
        ? state.collapsedEpics
        : DEFAULT_BEADS_GROUPING.collapsedEpics,
    };
    localStorage.setItem(BEADS_GROUPING_KEY, JSON.stringify(toStore));
  } catch (e) {
    console.warn("Failed to save Beads grouping to localStorage:", e);
  }
}

// ---------------------------------------------------------------------------
// Beads sort preference (field + direction)
// ---------------------------------------------------------------------------
// Persists the field the Beads list is sorted by and the direction. Like the
// other Beads view preferences this is localStorage-only (no server sync) and
// survives a full reload. The default matches the most useful first view:
// newest issues first (creation date, descending).

const BEADS_SORT_KEY = "mitto_beads_sort";
const BEADS_SORT_FIELDS = ["created", "updated", "priority"];
const BEADS_SORT_DIRECTIONS = ["asc", "desc"];
const DEFAULT_BEADS_SORT = { field: "created", direction: "desc" };

/**
 * Get the persisted Beads sort preference from localStorage.
 * @returns {{field: 'created'|'updated'|'priority', direction: 'asc'|'desc'}}
 *          Sort state, falling back to defaults for any missing/invalid field.
 */
export function getBeadsSort() {
  try {
    const value = localStorage.getItem(BEADS_SORT_KEY);
    if (value) {
      const parsed = JSON.parse(value);
      return {
        field: BEADS_SORT_FIELDS.includes(parsed.field)
          ? parsed.field
          : DEFAULT_BEADS_SORT.field,
        direction: BEADS_SORT_DIRECTIONS.includes(parsed.direction)
          ? parsed.direction
          : DEFAULT_BEADS_SORT.direction,
      };
    }
  } catch (e) {
    console.warn("Failed to read Beads sort from localStorage:", e);
  }
  return { ...DEFAULT_BEADS_SORT };
}

/**
 * Persist the Beads sort preference to localStorage.
 * @param {{field?: string, direction?: string}} sort - Sort state to save.
 */
export function setBeadsSort(sort) {
  try {
    const toStore = {
      field: BEADS_SORT_FIELDS.includes(sort?.field)
        ? sort.field
        : DEFAULT_BEADS_SORT.field,
      direction: BEADS_SORT_DIRECTIONS.includes(sort?.direction)
        ? sort.direction
        : DEFAULT_BEADS_SORT.direction,
    };
    localStorage.setItem(BEADS_SORT_KEY, JSON.stringify(toStore));
  } catch (e) {
    console.warn("Failed to save Beads sort to localStorage:", e);
  }
}

/**
 * Get the current conversation density mode from localStorage.
 * @returns {'condensed' | 'comfortable'} The current density mode
 */
export function getDensity() {
  try {
    const value = localStorage.getItem(DENSITY_KEY);
    if (value === "condensed" || value === "comfortable") {
      return value;
    }
    return "condensed";
  } catch (e) {
    console.warn("Failed to read density from localStorage:", e);
    return "condensed";
  }
}

/**
 * Save the conversation density mode to localStorage.
 * @param {'condensed' | 'comfortable'} mode - The density mode to save
 */
export function setDensity(mode) {
  try {
    if (mode === "condensed" || mode === "comfortable") {
      localStorage.setItem(DENSITY_KEY, mode);
    } else {
      localStorage.removeItem(DENSITY_KEY);
    }
  } catch (e) {
    console.warn("Failed to save density to localStorage:", e);
  }
}
