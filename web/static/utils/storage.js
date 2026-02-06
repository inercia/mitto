// Mitto Web Interface - Local Storage Utilities
// Functions for persisting state in localStorage

// =============================================================================
// Sync State Persistence (localStorage)
// =============================================================================

/**
 * Get the last seen sequence number for a session from localStorage
 * @param {string} sessionId - The session ID
 * @returns {number} The last seen sequence number, or 0 if not found
 */
export function getLastSeenSeq(sessionId) {
  try {
    const key = `mitto_session_seq_${sessionId}`;
    const value = localStorage.getItem(key);
    return value ? parseInt(value, 10) : 0;
  } catch (e) {
    console.warn("Failed to read last seen seq from localStorage:", e);
    return 0;
  }
}

/**
 * Save the last seen sequence number for a session to localStorage
 * @param {string} sessionId - The session ID
 * @param {number} seq - The sequence number to save
 */
export function setLastSeenSeq(sessionId, seq) {
  try {
    const key = `mitto_session_seq_${sessionId}`;
    localStorage.setItem(key, String(seq));
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
