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
        console.warn('Failed to read last seen seq from localStorage:', e);
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
        console.warn('Failed to save last seen seq to localStorage:', e);
    }
}

/**
 * Get the last active session ID from localStorage
 * @returns {string|null} The last active session ID, or null if not found
 */
export function getLastActiveSessionId() {
    try {
        return localStorage.getItem('mitto_last_session_id') || null;
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
            localStorage.setItem('mitto_last_session_id', sessionId);
        } else {
            localStorage.removeItem('mitto_last_session_id');
        }
    } catch (e) {
        console.warn('Failed to save last session ID to localStorage:', e);
    }
}

