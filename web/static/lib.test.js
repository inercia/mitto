/**
 * Unit tests for Mitto Web Interface library functions
 */

import {
    ROLE_USER,
    ROLE_AGENT,
    ROLE_THOUGHT,
    ROLE_TOOL,
    ROLE_ERROR,
    ROLE_SYSTEM,
    MAX_MESSAGES,
    computeAllSessions,
    convertEventsToMessages,
    safeJsonParse,
    createSessionState,
    addMessageToSessionState,
    updateLastMessageInSession,
    removeSessionFromState,
    limitMessages
} from './lib.js';

// =============================================================================
// computeAllSessions Tests
// =============================================================================

describe('computeAllSessions', () => {
    test('returns empty array when both inputs are empty', () => {
        const result = computeAllSessions([], []);
        expect(result).toEqual([]);
    });

    test('returns active sessions when stored is empty', () => {
        const active = [
            { session_id: '1', created_at: '2024-01-01T10:00:00Z' }
        ];
        const result = computeAllSessions(active, []);
        expect(result).toHaveLength(1);
        expect(result[0].session_id).toBe('1');
    });

    test('returns stored sessions when active is empty', () => {
        const stored = [
            { session_id: '1', created_at: '2024-01-01T10:00:00Z' }
        ];
        const result = computeAllSessions([], stored);
        expect(result).toHaveLength(1);
        expect(result[0].session_id).toBe('1');
    });

    test('removes duplicates preferring active sessions', () => {
        const active = [
            { session_id: '1', name: 'Active Version', created_at: '2024-01-01T10:00:00Z' }
        ];
        const stored = [
            { session_id: '1', name: 'Stored Version', created_at: '2024-01-01T10:00:00Z' },
            { session_id: '2', name: 'Only Stored', created_at: '2024-01-01T09:00:00Z' }
        ];
        const result = computeAllSessions(active, stored);
        expect(result).toHaveLength(2);
        // Active version should be present, not stored
        expect(result.find(s => s.session_id === '1').name).toBe('Active Version');
    });

    test('sorts by last_user_message_at first', () => {
        const sessions = [
            { session_id: '1', last_user_message_at: '2024-01-01T08:00:00Z', created_at: '2024-01-01T01:00:00Z' },
            { session_id: '2', last_user_message_at: '2024-01-01T12:00:00Z', created_at: '2024-01-01T02:00:00Z' },
            { session_id: '3', last_user_message_at: '2024-01-01T10:00:00Z', created_at: '2024-01-01T03:00:00Z' }
        ];
        const result = computeAllSessions(sessions, []);
        expect(result.map(s => s.session_id)).toEqual(['2', '3', '1']);
    });

    test('falls back to updated_at when last_user_message_at is missing', () => {
        const sessions = [
            { session_id: '1', updated_at: '2024-01-01T08:00:00Z', created_at: '2024-01-01T01:00:00Z' },
            { session_id: '2', updated_at: '2024-01-01T12:00:00Z', created_at: '2024-01-01T02:00:00Z' }
        ];
        const result = computeAllSessions(sessions, []);
        expect(result.map(s => s.session_id)).toEqual(['2', '1']);
    });

    test('falls back to created_at when both timestamps are missing', () => {
        const sessions = [
            { session_id: '1', created_at: '2024-01-01T08:00:00Z' },
            { session_id: '2', created_at: '2024-01-01T12:00:00Z' }
        ];
        const result = computeAllSessions(sessions, []);
        expect(result.map(s => s.session_id)).toEqual(['2', '1']);
    });
});

// =============================================================================
// convertEventsToMessages Tests
// =============================================================================

describe('convertEventsToMessages', () => {
    test('returns empty array for empty events', () => {
        const result = convertEventsToMessages([]);
        expect(result).toEqual([]);
    });

    test('converts user_prompt event', () => {
        const events = [{
            type: 'user_prompt',
            data: { message: 'Hello' },
            timestamp: '2024-01-01T10:00:00Z'
        }];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(1);
        expect(result[0].role).toBe(ROLE_USER);
        expect(result[0].text).toBe('Hello');
    });

    test('converts agent_message event', () => {
        const events = [{
            type: 'agent_message',
            data: { html: '<p>Response</p>' },
            timestamp: '2024-01-01T10:00:00Z'
        }];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(1);
        expect(result[0].role).toBe(ROLE_AGENT);
        expect(result[0].html).toBe('<p>Response</p>');
        expect(result[0].complete).toBe(true);
    });

    test('converts agent_thought event', () => {
        const events = [{
            type: 'agent_thought',
            data: { text: 'Thinking...' },
            timestamp: '2024-01-01T10:00:00Z'
        }];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(1);
        expect(result[0].role).toBe(ROLE_THOUGHT);
        expect(result[0].text).toBe('Thinking...');
    });

    test('converts tool_call event', () => {
        const events = [{
            type: 'tool_call',
            data: { id: 'tool-1', title: 'Read File', status: 'running' },
            timestamp: '2024-01-01T10:00:00Z'
        }];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(1);
        expect(result[0].role).toBe(ROLE_TOOL);
        expect(result[0].id).toBe('tool-1');
        expect(result[0].title).toBe('Read File');
    });

    test('converts error event', () => {
        const events = [{
            type: 'error',
            data: { message: 'Something went wrong' },
            timestamp: '2024-01-01T10:00:00Z'
        }];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(1);
        expect(result[0].role).toBe(ROLE_ERROR);
        expect(result[0].text).toBe('Something went wrong');
    });

    test('ignores unknown event types', () => {
        const events = [{
            type: 'unknown_type',
            data: { foo: 'bar' },
            timestamp: '2024-01-01T10:00:00Z'
        }];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(0);
    });

    test('converts multiple events in order', () => {
        const events = [
            { type: 'user_prompt', data: { message: 'Hi' }, timestamp: '2024-01-01T10:00:00Z' },
            { type: 'agent_message', data: { html: 'Hello!' }, timestamp: '2024-01-01T10:00:01Z' },
            { type: 'user_prompt', data: { message: 'Bye' }, timestamp: '2024-01-01T10:00:02Z' }
        ];
        const result = convertEventsToMessages(events);
        expect(result).toHaveLength(3);
        expect(result[0].role).toBe(ROLE_USER);
        expect(result[1].role).toBe(ROLE_AGENT);
        expect(result[2].role).toBe(ROLE_USER);
    });
});

// =============================================================================
// safeJsonParse Tests
// =============================================================================

describe('safeJsonParse', () => {
    test('parses valid JSON', () => {
        const result = safeJsonParse('{"type": "test", "value": 123}');
        expect(result.error).toBeNull();
        expect(result.data).toEqual({ type: 'test', value: 123 });
    });

    test('returns error for invalid JSON', () => {
        const result = safeJsonParse('not valid json');
        expect(result.data).toBeNull();
        expect(result.error).toBeInstanceOf(Error);
    });

    test('parses arrays', () => {
        const result = safeJsonParse('[1, 2, 3]');
        expect(result.error).toBeNull();
        expect(result.data).toEqual([1, 2, 3]);
    });

    test('parses primitives', () => {
        expect(safeJsonParse('"hello"').data).toBe('hello');
        expect(safeJsonParse('42').data).toBe(42);
        expect(safeJsonParse('true').data).toBe(true);
        expect(safeJsonParse('null').data).toBeNull();
    });
});

// =============================================================================
// createSessionState Tests
// =============================================================================

describe('createSessionState', () => {
    test('creates session with defaults', () => {
        const result = createSessionState('session-123');
        expect(result.messages).toEqual([]);
        expect(result.info.session_id).toBe('session-123');
        expect(result.info.name).toBe('New conversation');
        expect(result.info.status).toBe('active');
    });

    test('creates session with custom options', () => {
        const result = createSessionState('session-456', {
            name: 'My Session',
            acpServer: 'auggie',
            status: 'completed'
        });
        expect(result.info.name).toBe('My Session');
        expect(result.info.acp_server).toBe('auggie');
        expect(result.info.status).toBe('completed');
    });

    test('creates session with initial messages', () => {
        const messages = [{ role: ROLE_SYSTEM, text: 'Welcome' }];
        const result = createSessionState('session-789', { messages });
        expect(result.messages).toHaveLength(1);
        expect(result.messages[0].text).toBe('Welcome');
    });
});

// =============================================================================
// addMessageToSessionState Tests
// =============================================================================

describe('addMessageToSessionState', () => {
    test('adds message to existing session', () => {
        const session = { messages: [{ role: ROLE_USER, text: 'Hi' }], info: {} };
        const newMessage = { role: ROLE_AGENT, html: 'Hello!' };
        const result = addMessageToSessionState(session, newMessage);

        expect(result.messages).toHaveLength(2);
        expect(result.messages[1]).toEqual(newMessage);
        // Original should be unchanged (immutability)
        expect(session.messages).toHaveLength(1);
    });

    test('creates session if null', () => {
        const newMessage = { role: ROLE_USER, text: 'First message' };
        const result = addMessageToSessionState(null, newMessage);

        expect(result.messages).toHaveLength(1);
        expect(result.messages[0]).toEqual(newMessage);
    });

    test('creates session if undefined', () => {
        const newMessage = { role: ROLE_USER, text: 'First message' };
        const result = addMessageToSessionState(undefined, newMessage);

        expect(result.messages).toHaveLength(1);
    });

    test('limits messages to MAX_MESSAGES when exceeding limit', () => {
        // Create a session with MAX_MESSAGES - 1 messages
        const existingMessages = Array.from({ length: MAX_MESSAGES - 1 }, (_, i) => ({
            role: ROLE_USER,
            text: `Message ${i}`
        }));
        const session = { messages: existingMessages, info: {} };

        // Add 2 more messages (should trigger limit)
        let result = addMessageToSessionState(session, { role: ROLE_USER, text: 'New 1' });
        expect(result.messages).toHaveLength(MAX_MESSAGES);

        result = addMessageToSessionState(result, { role: ROLE_USER, text: 'New 2' });
        expect(result.messages).toHaveLength(MAX_MESSAGES);

        // First message should have been removed
        expect(result.messages[0].text).toBe('Message 1');
        expect(result.messages[result.messages.length - 1].text).toBe('New 2');
    });
});

// =============================================================================
// limitMessages Tests
// =============================================================================

describe('limitMessages', () => {
    test('returns array unchanged when under limit', () => {
        const arr = [1, 2, 3];
        const result = limitMessages(arr, 10);
        expect(result).toBe(arr); // Should be same reference
    });

    test('returns array unchanged when at limit', () => {
        const arr = [1, 2, 3, 4, 5];
        const result = limitMessages(arr, 5);
        expect(result).toBe(arr);
    });

    test('returns last N items when over limit', () => {
        const arr = [1, 2, 3, 4, 5, 6, 7];
        const result = limitMessages(arr, 5);
        expect(result).toEqual([3, 4, 5, 6, 7]);
    });

    test('handles null input', () => {
        const result = limitMessages(null, 5);
        expect(result).toBeNull();
    });

    test('handles undefined input', () => {
        const result = limitMessages(undefined, 5);
        expect(result).toBeUndefined();
    });

    test('handles empty array', () => {
        const result = limitMessages([], 5);
        expect(result).toEqual([]);
    });

    test('uses MAX_MESSAGES as default limit', () => {
        const arr = Array.from({ length: MAX_MESSAGES + 10 }, (_, i) => i);
        const result = limitMessages(arr);
        expect(result).toHaveLength(MAX_MESSAGES);
        expect(result[0]).toBe(10); // First 10 should be trimmed
    });
});

// =============================================================================
// updateLastMessageInSession Tests
// =============================================================================

describe('updateLastMessageInSession', () => {
    test('updates last message', () => {
        const session = {
            messages: [
                { role: ROLE_AGENT, html: 'Hello', complete: false },
            ],
            info: {}
        };
        const result = updateLastMessageInSession(session, msg => ({ ...msg, complete: true }));

        expect(result.messages[0].complete).toBe(true);
        // Original should be unchanged
        expect(session.messages[0].complete).toBe(false);
    });

    test('returns session unchanged if no messages', () => {
        const session = { messages: [], info: {} };
        const result = updateLastMessageInSession(session, msg => ({ ...msg, complete: true }));

        expect(result).toBe(session);
    });

    test('returns null/undefined session unchanged', () => {
        expect(updateLastMessageInSession(null, msg => msg)).toBeNull();
        expect(updateLastMessageInSession(undefined, msg => msg)).toBeUndefined();
    });

    test('only updates last message, not others', () => {
        const session = {
            messages: [
                { role: ROLE_AGENT, html: 'First', complete: false },
                { role: ROLE_AGENT, html: 'Second', complete: false },
            ],
            info: {}
        };
        const result = updateLastMessageInSession(session, msg => ({ ...msg, complete: true }));

        expect(result.messages[0].complete).toBe(false);
        expect(result.messages[1].complete).toBe(true);
    });
});

// =============================================================================
// removeSessionFromState Tests
// =============================================================================

describe('removeSessionFromState', () => {
    test('removes session from state', () => {
        const sessions = {
            'session-1': { messages: [], info: {} },
            'session-2': { messages: [], info: {} }
        };
        const result = removeSessionFromState(sessions, 'session-1', 'session-2');

        expect(result.newSessions).not.toHaveProperty('session-1');
        expect(result.newSessions).toHaveProperty('session-2');
        expect(result.nextActiveSessionId).toBe('session-2');
        expect(result.needsNewSession).toBe(false);
    });

    test('switches to another session when active is removed', () => {
        const sessions = {
            'session-1': { messages: [], info: {} },
            'session-2': { messages: [], info: {} }
        };
        const result = removeSessionFromState(sessions, 'session-1', 'session-1');

        expect(result.nextActiveSessionId).toBe('session-2');
        expect(result.needsNewSession).toBe(false);
    });

    test('signals need for new session when last session is removed', () => {
        const sessions = {
            'session-1': { messages: [], info: {} }
        };
        const result = removeSessionFromState(sessions, 'session-1', 'session-1');

        expect(result.newSessions).toEqual({});
        expect(result.nextActiveSessionId).toBeNull();
        expect(result.needsNewSession).toBe(true);
    });

    test('keeps active session when non-active is removed', () => {
        const sessions = {
            'session-1': { messages: [], info: {} },
            'session-2': { messages: [], info: {} }
        };
        const result = removeSessionFromState(sessions, 'session-2', 'session-1');

        expect(result.nextActiveSessionId).toBe('session-1');
        expect(result.needsNewSession).toBe(false);
    });
});

