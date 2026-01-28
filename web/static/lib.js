// Mitto Web Interface - Shared Library Functions
// This file contains pure functions and utilities that can be tested independently

// Maximum number of messages to keep in browser memory per session.
// This prevents memory issues in very long sessions while keeping enough context.
export const MAX_MESSAGES = 100;

// Number of events to load initially when switching to a session.
// This provides a faster initial load while allowing users to load more history.
export const INITIAL_EVENTS_LIMIT = 50;

// Message roles
export const ROLE_USER = 'user';
export const ROLE_AGENT = 'agent';
export const ROLE_THOUGHT = 'thought';
export const ROLE_TOOL = 'tool';
export const ROLE_ERROR = 'error';
export const ROLE_SYSTEM = 'system';

/**
 * Combines active and stored sessions, avoiding duplicates, and sorts by creation time (most recent first).
 * @param {Array} activeSessions - Currently active sessions in memory
 * @param {Array} storedSessions - Sessions loaded from storage
 * @returns {Array} Combined and sorted sessions
 */
// Global map to store working_dir values from API responses
// This is used as a fallback when React state updates haven't propagated yet
const globalWorkingDirMap = new Map();

// Function to update the global working_dir map
export function updateGlobalWorkingDir(sessionId, workingDir) {
    if (sessionId && workingDir) {
        globalWorkingDirMap.set(sessionId, workingDir);
    }
}

// Function to get working_dir from the global map
export function getGlobalWorkingDir(sessionId) {
    return globalWorkingDirMap.get(sessionId) || '';
}

export function computeAllSessions(activeSessions, storedSessions) {
    // Update global map from storedSessions
    storedSessions.forEach(s => {
        if (s.session_id && s.working_dir) {
            globalWorkingDirMap.set(s.session_id, s.working_dir);
        }
    });

    // Create a map of stored sessions for quick lookup
    const storedMap = new Map(storedSessions.map(s => [s.session_id, s]));

    // Merge working_dir from storedSessions (or global map) into activeSessions
    const mergedActive = activeSessions.map(s => {
        const stored = storedMap.get(s.session_id);
        const globalWd = globalWorkingDirMap.get(s.session_id);
        // Get working_dir from: stored session, global map, or existing value
        const workingDir = stored?.working_dir || globalWd || s.working_dir || '';
        if (workingDir && workingDir !== s.working_dir) {
            return { ...s, working_dir: workingDir };
        }
        return s;
    });

    const activeIds = new Set(mergedActive.map(s => s.session_id));
    const filteredStored = storedSessions.filter(s => !activeIds.has(s.session_id));
    const combined = [...mergedActive, ...filteredStored];
    // Sort by creation time (most recent first)
    combined.sort((a, b) => {
        return new Date(b.created_at) - new Date(a.created_at);
    });
    return combined;
}

/**
 * Helper function to convert stored events to messages for display.
 * @param {Array} events - Array of stored session events
 * @returns {Array} Array of message objects for rendering
 */
export function convertEventsToMessages(events) {
    const messages = [];
    for (const event of events) {
        const seq = event.seq || 0; // Include sequence number for tracking
        switch (event.type) {
            case 'user_prompt':
                messages.push({
                    role: ROLE_USER,
                    text: event.data?.message || event.data?.text || '',
                    timestamp: new Date(event.timestamp).getTime(),
                    seq
                });
                break;
            case 'agent_message':
                messages.push({
                    role: ROLE_AGENT,
                    html: event.data?.html || event.data?.text || '',
                    complete: true,
                    timestamp: new Date(event.timestamp).getTime(),
                    seq
                });
                break;
            case 'agent_thought':
                messages.push({
                    role: ROLE_THOUGHT,
                    text: event.data?.text || '',
                    complete: true,
                    timestamp: new Date(event.timestamp).getTime(),
                    seq
                });
                break;
            case 'tool_call':
                messages.push({
                    role: ROLE_TOOL,
                    id: event.data?.tool_call_id || event.data?.id,
                    title: event.data?.title,
                    status: event.data?.status || 'completed',
                    timestamp: new Date(event.timestamp).getTime(),
                    seq
                });
                break;
            case 'error':
                messages.push({
                    role: ROLE_ERROR,
                    text: event.data?.message || '',
                    timestamp: new Date(event.timestamp).getTime(),
                    seq
                });
                break;
        }
    }
    return messages;
}

/**
 * Safely parse JSON with error handling.
 * @param {string} jsonString - The JSON string to parse
 * @returns {{ data: any, error: Error|null }} Parsed data or error
 */
export function safeJsonParse(jsonString) {
    try {
        return { data: JSON.parse(jsonString), error: null };
    } catch (error) {
        return { data: null, error };
    }
}

/**
 * Create a new session state object.
 * @param {string} sessionId - The session ID
 * @param {Object} options - Session options
 * @returns {Object} New session state
 */
export function createSessionState(sessionId, options = {}) {
    const { name, acpServer, createdAt, messages = [], status = 'active' } = options;
    return {
        messages,
        info: {
            session_id: sessionId,
            name: name || 'New conversation',
            acp_server: acpServer || '',
            created_at: createdAt || new Date().toISOString(),
            status
        }
    };
}

/**
 * Limit an array to the last N items.
 * @param {Array} arr - Array to limit
 * @param {number} maxItems - Maximum number of items to keep (default: MAX_MESSAGES)
 * @returns {Array} Array with at most maxItems elements (the last ones)
 */
export function limitMessages(arr, maxItems = MAX_MESSAGES) {
    if (!arr || arr.length <= maxItems) {
        return arr;
    }
    return arr.slice(-maxItems);
}

/**
 * Add a message to a session's message list immutably.
 * Automatically limits messages to MAX_MESSAGES to prevent memory issues.
 * @param {Object} session - Current session state
 * @param {Object} message - Message to add
 * @returns {Object} New session state with message added
 */
export function addMessageToSessionState(session, message) {
    if (!session) {
        session = { messages: [], info: {} };
    }
    const newMessages = limitMessages([...session.messages, message]);
    return {
        ...session,
        messages: newMessages
    };
}

/**
 * Update the last message in a session immutably.
 * @param {Object} session - Current session state
 * @param {Function} updater - Function to update the last message
 * @returns {Object} New session state with updated last message
 */
export function updateLastMessageInSession(session, updater) {
    if (!session || session.messages.length === 0) {
        return session;
    }
    const messages = [...session.messages];
    const lastIdx = messages.length - 1;
    messages[lastIdx] = updater(messages[lastIdx]);
    return { ...session, messages };
}

/**
 * Remove a session from sessions state and determine next active session.
 * @param {Object} sessions - Current sessions state { sessionId: sessionData }
 * @param {string} sessionIdToRemove - Session ID to remove
 * @param {string} currentActiveSessionId - Currently active session ID
 * @returns {{ newSessions: Object, nextActiveSessionId: string|null, needsNewSession: boolean }}
 */
export function removeSessionFromState(sessions, sessionIdToRemove, currentActiveSessionId) {
    const { [sessionIdToRemove]: removed, ...rest } = sessions;
    
    let nextActiveSessionId = currentActiveSessionId;
    let needsNewSession = false;
    
    if (sessionIdToRemove === currentActiveSessionId) {
        const remainingIds = Object.keys(rest);
        if (remainingIds.length > 0) {
            nextActiveSessionId = remainingIds[0];
        } else {
            nextActiveSessionId = null;
            needsNewSession = true;
        }
    }
    
    return { newSessions: rest, nextActiveSessionId, needsNewSession };
}

// =============================================================================
// Workspace Visual Identification
// =============================================================================

/**
 * Extracts the basename from a directory path.
 * @param {string} path - Full directory path
 * @returns {string} The basename (last component) of the path
 */
export function getBasename(path) {
    if (!path) return '';
    // Handle both Unix and Windows paths
    const parts = path.replace(/\\/g, '/').split('/').filter(p => p);
    return parts[parts.length - 1] || '';
}

/**
 * Generates a three-letter abbreviation from a directory basename.
 * Algorithm:
 * 1. If name has hyphens/underscores, take first letter of each word (up to 3)
 * 2. If name is camelCase, take first letter of each word (up to 3)
 * 3. Otherwise, take first 3 consonants, or first 3 characters
 *
 * @param {string} path - Full directory path or basename
 * @returns {string} Three-letter uppercase abbreviation
 */
export function getWorkspaceAbbreviation(path) {
    const basename = getBasename(path);
    if (!basename) return '???';

    // Split by common separators (hyphen, underscore, space)
    const separatorParts = basename.split(/[-_\s]+/).filter(p => p);

    if (separatorParts.length >= 2) {
        // Take first letter of each part (up to 3)
        const abbr = separatorParts
            .slice(0, 3)
            .map(p => p[0])
            .join('')
            .toUpperCase();
        // Pad with last part's letters if needed
        if (abbr.length < 3 && separatorParts.length > 0) {
            const lastPart = separatorParts[separatorParts.length - 1];
            return (abbr + lastPart.slice(1, 4 - abbr.length)).toUpperCase().slice(0, 3);
        }
        return abbr.slice(0, 3);
    }

    // Check for camelCase
    const camelParts = basename.split(/(?=[A-Z])/).filter(p => p);
    if (camelParts.length >= 2) {
        const abbr = camelParts
            .slice(0, 3)
            .map(p => p[0])
            .join('')
            .toUpperCase();
        if (abbr.length < 3 && camelParts.length > 0) {
            const lastPart = camelParts[camelParts.length - 1];
            return (abbr + lastPart.slice(1, 4 - abbr.length)).toUpperCase().slice(0, 3);
        }
        return abbr.slice(0, 3);
    }

    // Single word: take first 3 consonants, or first 3 characters
    const consonants = basename.replace(/[aeiouAEIOU]/g, '');
    if (consonants.length >= 3) {
        return consonants.slice(0, 3).toUpperCase();
    }

    // Fall back to first 3 characters
    return basename.slice(0, 3).toUpperCase();
}

/**
 * Simple hash function for strings.
 * @param {string} str - String to hash
 * @returns {number} Hash value (positive integer)
 */
function hashString(str) {
    let hash = 0;
    for (let i = 0; i < str.length; i++) {
        const char = str.charCodeAt(i);
        hash = ((hash << 5) - hash) + char;
        hash = hash & hash; // Convert to 32-bit integer
    }
    return Math.abs(hash);
}

/**
 * Converts a hex color to RGB components.
 * @param {string} hex - Hex color (e.g., "#ff5500" or "ff5500")
 * @returns {object|null} { r, g, b } or null if invalid
 */
export function hexToRgb(hex) {
    if (!hex) return null;
    const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
    if (!result) return null;
    return {
        r: parseInt(result[1], 16),
        g: parseInt(result[2], 16),
        b: parseInt(result[3], 16)
    };
}

/**
 * Calculates relative luminance of a color for accessibility.
 * @param {number} r - Red (0-255)
 * @param {number} g - Green (0-255)
 * @param {number} b - Blue (0-255)
 * @returns {number} Relative luminance (0-1)
 */
export function getLuminance(r, g, b) {
    const [rs, gs, bs] = [r, g, b].map(c => {
        c = c / 255;
        return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
    });
    return 0.2126 * rs + 0.7152 * gs + 0.0722 * bs;
}

/**
 * Generates color info from a hex color string.
 * @param {string} hexColor - Hex color (e.g., "#ff5500")
 * @returns {object|null} Color object with { background, text, border } or null if invalid
 */
export function getColorFromHex(hexColor) {
    const rgb = hexToRgb(hexColor);
    if (!rgb) return null;

    const luminance = getLuminance(rgb.r, rgb.g, rgb.b);
    // Use white text for dark backgrounds (luminance < 0.4), dark text otherwise
    const text = luminance < 0.4 ? 'white' : 'rgb(30, 30, 30)';

    // Border is a darker version of the background
    const darkenFactor = 0.7;
    const borderR = Math.round(rgb.r * darkenFactor);
    const borderG = Math.round(rgb.g * darkenFactor);
    const borderB = Math.round(rgb.b * darkenFactor);
    const border = `rgb(${borderR}, ${borderG}, ${borderB})`;

    return {
        background: hexColor,
        text,
        border
    };
}

/**
 * Converts HSL values to a hex color string.
 * @param {number} h - Hue (0-360)
 * @param {number} s - Saturation (0-100)
 * @param {number} l - Lightness (0-100)
 * @returns {string} Hex color (e.g., "#ff5500")
 */
export function hslToHex(h, s, l) {
    s /= 100;
    l /= 100;
    const a = s * Math.min(l, 1 - l);
    const f = n => {
        const k = (n + h / 30) % 12;
        const color = l - a * Math.max(Math.min(k - 3, 9 - k, 1), -1);
        return Math.round(255 * color).toString(16).padStart(2, '0');
    };
    return `#${f(0)}${f(8)}${f(4)}`;
}

/**
 * Generates a deterministic color for a workspace based on its path.
 * Uses HSL color space for better control over saturation and lightness.
 *
 * @param {string} path - Full directory path
 * @returns {object} Color object with { hue, background, backgroundHex, text, border }
 */
export function getWorkspaceColor(path) {
    const basename = getBasename(path);
    if (!basename) {
        return {
            hue: 0,
            background: 'rgb(100, 100, 100)',
            backgroundHex: '#646464',
            text: 'white',
            border: 'rgb(120, 120, 120)'
        };
    }

    // Generate hue from hash (0-360)
    const hash = hashString(basename);
    const hue = hash % 360;

    // Use fixed saturation and lightness for consistent appearance
    // Saturation: 65% for vibrant but not overwhelming colors
    // Lightness: 45% for good contrast with white text
    const saturation = 65;
    const lightness = 45;

    // Generate the background color
    const background = `hsl(${hue}, ${saturation}%, ${lightness}%)`;
    const backgroundHex = hslToHex(hue, saturation, lightness);

    // For text, use white for dark backgrounds (lightness < 55%)
    // and dark for light backgrounds
    const text = lightness < 55 ? 'white' : 'rgb(30, 30, 30)';

    // Border is slightly darker version
    const border = `hsl(${hue}, ${saturation}%, ${Math.max(lightness - 10, 20)}%)`;

    return { hue, background, backgroundHex, text, border };
}

/**
 * Gets complete workspace visual info (abbreviation and color).
 * @param {string} path - Full directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @returns {object} { abbreviation, color: { background, text, border }, basename }
 */
export function getWorkspaceVisualInfo(path, customColor = null) {
    // If a custom color is provided and valid, use it
    const color = customColor ? getColorFromHex(customColor) : null;

    return {
        abbreviation: getWorkspaceAbbreviation(path),
        color: color || getWorkspaceColor(path),
        basename: getBasename(path)
    };
}

// Credential validation constants
export const MIN_USERNAME_LENGTH = 3;
export const MAX_USERNAME_LENGTH = 64;
export const MIN_PASSWORD_LENGTH = 8;
export const MAX_PASSWORD_LENGTH = 128;

// Common weak passwords that should be rejected
const COMMON_WEAK_PASSWORDS = new Set([
    'password', 'password1', 'password12', '12345678', '123456789',
    'qwerty123', 'admin123', 'letmein', 'welcome', 'monkey123',
    'dragon123', 'master123', 'changeme'
]);

/**
 * Validates a username for external access authentication.
 * @param {string} username - Username to validate
 * @returns {string} Error message if invalid, empty string if valid
 */
export function validateUsername(username) {
    const trimmed = (username || '').trim();

    if (!trimmed) {
        return 'Username is required';
    }

    if (trimmed.length < MIN_USERNAME_LENGTH) {
        return 'Username must be at least 3 characters';
    }

    if (trimmed.length > MAX_USERNAME_LENGTH) {
        return 'Username must be at most 64 characters';
    }

    // Username should start with a letter or number
    if (!/^[a-zA-Z0-9]/.test(trimmed)) {
        return 'Username must start with a letter or number';
    }

    // Check for valid characters (alphanumeric, underscore, hyphen, dot)
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(trimmed)) {
        return 'Username can only contain letters, numbers, underscore, hyphen, and dot';
    }

    return '';
}

/**
 * Validates a password for external access authentication.
 * @param {string} password - Password to validate
 * @returns {string} Error message if invalid, empty string if valid
 */
export function validatePassword(password) {
    if (!password) {
        return 'Password is required';
    }

    if (password.length < MIN_PASSWORD_LENGTH) {
        return 'Password must be at least 8 characters';
    }

    if (password.length > MAX_PASSWORD_LENGTH) {
        return 'Password must be at most 128 characters';
    }

    // Check against common weak passwords (case-insensitive)
    if (COMMON_WEAK_PASSWORDS.has(password.toLowerCase())) {
        return 'Password is too common. Please choose a stronger password';
    }

    // Check for minimum complexity: at least one letter and one number or special char
    const hasLetter = /[a-zA-Z]/.test(password);
    const hasNonLetter = /[^a-zA-Z\s]/.test(password);

    if (!hasLetter || !hasNonLetter) {
        return 'Password must contain at least one letter and one number or special character';
    }

    return '';
}

/**
 * Validates both username and password.
 * @param {string} username - Username to validate
 * @param {string} password - Password to validate
 * @returns {string} First error message found, or empty string if both valid
 */
export function validateCredentials(username, password) {
    const usernameError = validateUsername(username);
    if (usernameError) return usernameError;
    return validatePassword(password);
}
