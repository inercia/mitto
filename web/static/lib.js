// Mitto Web Interface - Shared Library Functions
// This file contains pure functions and utilities that can be tested independently

// Maximum number of messages to keep in browser memory per session.
// This prevents memory issues in very long sessions while keeping enough context.
export const MAX_MESSAGES = 100;

// Message roles
export const ROLE_USER = 'user';
export const ROLE_AGENT = 'agent';
export const ROLE_THOUGHT = 'thought';
export const ROLE_TOOL = 'tool';
export const ROLE_ERROR = 'error';
export const ROLE_SYSTEM = 'system';

/**
 * Combines active and stored sessions, avoiding duplicates, and sorts by last user message time.
 * @param {Array} activeSessions - Currently active sessions in memory
 * @param {Array} storedSessions - Sessions loaded from storage
 * @returns {Array} Combined and sorted sessions
 */
export function computeAllSessions(activeSessions, storedSessions) {
    const activeIds = new Set(activeSessions.map(s => s.session_id));
    const filteredStored = storedSessions.filter(s => !activeIds.has(s.session_id));
    const combined = [...activeSessions, ...filteredStored];
    // Sort by last user message time (most recent first), falling back to updated_at, then created_at
    combined.sort((a, b) => {
        const aTime = a.last_user_message_at || a.updated_at || a.created_at;
        const bTime = b.last_user_message_at || b.updated_at || b.created_at;
        return new Date(bTime) - new Date(aTime);
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

