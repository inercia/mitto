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
    MAX_MARKDOWN_LENGTH,
    MIN_USERNAME_LENGTH,
    MAX_USERNAME_LENGTH,
    MIN_PASSWORD_LENGTH,
    MAX_PASSWORD_LENGTH,
    computeAllSessions,
    convertEventsToMessages,
    getMinSeq,
    getMaxSeq,
    safeJsonParse,
    createSessionState,
    addMessageToSessionState,
    updateLastMessageInSession,
    removeSessionFromState,
    limitMessages,
    getBasename,
    getWorkspaceAbbreviation,
    getWorkspaceColor,
    getWorkspaceVisualInfo,
    hexToRgb,
    getLuminance,
    getColorFromHex,
    hslToHex,
    validateUsername,
    validatePassword,
    validateCredentials,
    generatePromptId,
    savePendingPrompt,
    removePendingPrompt,
    getPendingPrompts,
    getPendingPromptsForSession,
    cleanupExpiredPrompts,
    hasMarkdownContent,
    renderUserMarkdown
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

    test('sorts by created_at (most recent first)', () => {
        const sessions = [
            { session_id: '1', created_at: '2024-01-01T08:00:00Z' },
            { session_id: '2', created_at: '2024-01-01T12:00:00Z' },
            { session_id: '3', created_at: '2024-01-01T10:00:00Z' }
        ];
        const result = computeAllSessions(sessions, []);
        expect(result.map(s => s.session_id)).toEqual(['2', '3', '1']);
    });

    test('ignores last_user_message_at and updated_at for sorting', () => {
        const sessions = [
            { session_id: '1', last_user_message_at: '2024-01-01T23:00:00Z', updated_at: '2024-01-01T22:00:00Z', created_at: '2024-01-01T01:00:00Z' },
            { session_id: '2', last_user_message_at: '2024-01-01T01:00:00Z', updated_at: '2024-01-01T02:00:00Z', created_at: '2024-01-01T12:00:00Z' }
        ];
        const result = computeAllSessions(sessions, []);
        // Session 2 created later, so it should be first despite older last_user_message_at
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

    test('reverses events when reverseInput option is true', () => {
        // Events in reverse order (newest first, as returned by API with order=desc)
        const events = [
            { seq: 3, type: 'user_prompt', data: { message: 'Bye' }, timestamp: '2024-01-01T10:00:02Z' },
            { seq: 2, type: 'agent_message', data: { html: 'Hello!' }, timestamp: '2024-01-01T10:00:01Z' },
            { seq: 1, type: 'user_prompt', data: { message: 'Hi' }, timestamp: '2024-01-01T10:00:00Z' }
        ];
        const result = convertEventsToMessages(events, { reverseInput: true });
        expect(result).toHaveLength(3);
        // Should be in chronological order (oldest first)
        expect(result[0].seq).toBe(1);
        expect(result[0].text).toBe('Hi');
        expect(result[1].seq).toBe(2);
        expect(result[2].seq).toBe(3);
        expect(result[2].text).toBe('Bye');
    });

    test('does not modify original array when reverseInput is true', () => {
        const events = [
            { seq: 3, type: 'user_prompt', data: { message: 'C' }, timestamp: '2024-01-01T10:00:02Z' },
            { seq: 2, type: 'user_prompt', data: { message: 'B' }, timestamp: '2024-01-01T10:00:01Z' },
            { seq: 1, type: 'user_prompt', data: { message: 'A' }, timestamp: '2024-01-01T10:00:00Z' }
        ];
        convertEventsToMessages(events, { reverseInput: true });
        // Original array should be unchanged
        expect(events[0].seq).toBe(3);
        expect(events[1].seq).toBe(2);
        expect(events[2].seq).toBe(1);
    });
});

// =============================================================================
// getMinSeq and getMaxSeq Tests
// =============================================================================

describe('getMinSeq', () => {
    test('returns minimum sequence number', () => {
        const events = [{ seq: 5 }, { seq: 2 }, { seq: 8 }, { seq: 1 }];
        expect(getMinSeq(events)).toBe(1);
    });

    test('returns 0 for empty array', () => {
        expect(getMinSeq([])).toBe(0);
    });

    test('returns 0 for null input', () => {
        expect(getMinSeq(null)).toBe(0);
    });

    test('returns 0 for undefined input', () => {
        expect(getMinSeq(undefined)).toBe(0);
    });

    test('handles events with missing seq', () => {
        const events = [{ seq: 5 }, { }, { seq: 3 }];
        expect(getMinSeq(events)).toBe(0);
    });
});

describe('getMaxSeq', () => {
    test('returns maximum sequence number', () => {
        const events = [{ seq: 5 }, { seq: 2 }, { seq: 8 }, { seq: 1 }];
        expect(getMaxSeq(events)).toBe(8);
    });

    test('returns 0 for empty array', () => {
        expect(getMaxSeq([])).toBe(0);
    });

    test('returns 0 for null input', () => {
        expect(getMaxSeq(null)).toBe(0);
    });

    test('returns 0 for undefined input', () => {
        expect(getMaxSeq(undefined)).toBe(0);
    });

    test('handles events with missing seq', () => {
        const events = [{ seq: 5 }, { }, { seq: 3 }];
        expect(getMaxSeq(events)).toBe(5);
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

// =============================================================================
// Workspace Visual Identification Tests
// =============================================================================

describe('getBasename', () => {
    test('extracts basename from Unix path', () => {
        expect(getBasename('/home/user/my-project')).toBe('my-project');
        expect(getBasename('/Users/dev/awesome-app')).toBe('awesome-app');
        expect(getBasename('/path/to/src')).toBe('src');
    });

    test('extracts basename from Windows path', () => {
        expect(getBasename('C:\\Users\\dev\\project')).toBe('project');
        expect(getBasename('D:\\work\\my-app')).toBe('my-app');
    });

    test('handles paths with trailing slashes', () => {
        expect(getBasename('/home/user/project/')).toBe('project');
    });

    test('handles single component paths', () => {
        expect(getBasename('project')).toBe('project');
        expect(getBasename('/project')).toBe('project');
    });

    test('returns empty string for empty input', () => {
        expect(getBasename('')).toBe('');
        expect(getBasename(null)).toBe('');
        expect(getBasename(undefined)).toBe('');
    });
});

describe('getWorkspaceAbbreviation', () => {
    test('generates abbreviation from hyphenated names', () => {
        // Two-word names get padded to 3 chars from last word
        expect(getWorkspaceAbbreviation('/home/user/my-project')).toBe('MPR');
        expect(getWorkspaceAbbreviation('/path/to/awesome-web-app')).toBe('AWA');
        expect(getWorkspaceAbbreviation('/path/to/a-b')).toBe('AB');
    });

    test('generates abbreviation from underscored names', () => {
        expect(getWorkspaceAbbreviation('/path/to/my_project')).toBe('MPR');
        expect(getWorkspaceAbbreviation('/path/to/awesome_web_app')).toBe('AWA');
    });

    test('generates abbreviation from camelCase names', () => {
        expect(getWorkspaceAbbreviation('/path/to/myProject')).toBe('MPR');
        expect(getWorkspaceAbbreviation('/path/to/AwesomeWebApp')).toBe('AWA');
    });

    test('generates abbreviation from single words using consonants', () => {
        expect(getWorkspaceAbbreviation('/path/to/project')).toBe('PRJ');
        expect(getWorkspaceAbbreviation('/path/to/mitto')).toBe('MTT');
    });

    test('falls back to first 3 characters for short names', () => {
        expect(getWorkspaceAbbreviation('/path/to/src')).toBe('SRC');
        expect(getWorkspaceAbbreviation('/path/to/app')).toBe('APP');
    });

    test('returns ??? for empty input', () => {
        expect(getWorkspaceAbbreviation('')).toBe('???');
        expect(getWorkspaceAbbreviation(null)).toBe('???');
    });

    test('abbreviations are uppercase', () => {
        const abbr = getWorkspaceAbbreviation('/path/to/lowercase');
        expect(abbr).toBe(abbr.toUpperCase());
    });
});

describe('getWorkspaceColor', () => {
    test('returns color object with required properties', () => {
        const color = getWorkspaceColor('/path/to/project');
        expect(color).toHaveProperty('hue');
        expect(color).toHaveProperty('background');
        expect(color).toHaveProperty('backgroundHex');
        expect(color).toHaveProperty('text');
        expect(color).toHaveProperty('border');
    });

    test('backgroundHex is a valid hex color', () => {
        const color = getWorkspaceColor('/path/to/project');
        expect(color.backgroundHex).toMatch(/^#[0-9a-f]{6}$/i);
    });

    test('generates consistent colors for same path', () => {
        const color1 = getWorkspaceColor('/path/to/project');
        const color2 = getWorkspaceColor('/path/to/project');
        expect(color1.hue).toBe(color2.hue);
        expect(color1.background).toBe(color2.background);
    });

    test('generates different colors for different paths', () => {
        const color1 = getWorkspaceColor('/path/to/project1');
        const color2 = getWorkspaceColor('/path/to/project2');
        // Different basenames should produce different hues (usually)
        // Note: There's a small chance of collision, but it's unlikely
        expect(color1.hue).not.toBe(color2.hue);
    });

    test('hue is in valid range (0-360)', () => {
        const paths = ['/a', '/b', '/c', '/project', '/my-app', '/test-123'];
        paths.forEach(path => {
            const color = getWorkspaceColor(path);
            expect(color.hue).toBeGreaterThanOrEqual(0);
            expect(color.hue).toBeLessThan(360);
        });
    });

    test('returns gray for empty path', () => {
        const color = getWorkspaceColor('');
        expect(color.background).toBe('rgb(100, 100, 100)');
    });
});

describe('getWorkspaceVisualInfo', () => {
    test('returns complete visual info object', () => {
        const info = getWorkspaceVisualInfo('/home/user/my-project');
        expect(info).toHaveProperty('abbreviation');
        expect(info).toHaveProperty('color');
        expect(info).toHaveProperty('basename');
        expect(info.abbreviation).toBe('MPR');
        expect(info.basename).toBe('my-project');
        expect(info.color).toHaveProperty('background');
    });

    test('all properties are consistent with individual functions', () => {
        const path = '/path/to/awesome-app';
        const info = getWorkspaceVisualInfo(path);
        expect(info.abbreviation).toBe(getWorkspaceAbbreviation(path));
        expect(info.basename).toBe(getBasename(path));
        expect(info.color.hue).toBe(getWorkspaceColor(path).hue);
    });

    test('uses custom color when provided', () => {
        const path = '/home/user/my-project';
        const customColor = '#ff5500';
        const info = getWorkspaceVisualInfo(path, customColor);
        expect(info.color.background).toBe(customColor);
        expect(info.abbreviation).toBe('MPR'); // abbreviation unchanged
    });

    test('ignores invalid custom color', () => {
        const path = '/home/user/my-project';
        const invalidColor = 'not-a-color';
        const info = getWorkspaceVisualInfo(path, invalidColor);
        // Should fall back to auto-generated color
        expect(info.color.hue).toBeDefined();
    });
});

// =============================================================================
// Color Helper Functions Tests
// =============================================================================

describe('hexToRgb', () => {
    test('converts valid hex colors', () => {
        expect(hexToRgb('#ff0000')).toEqual({ r: 255, g: 0, b: 0 });
        expect(hexToRgb('#00ff00')).toEqual({ r: 0, g: 255, b: 0 });
        expect(hexToRgb('#0000ff')).toEqual({ r: 0, g: 0, b: 255 });
        expect(hexToRgb('#ffffff')).toEqual({ r: 255, g: 255, b: 255 });
        expect(hexToRgb('#000000')).toEqual({ r: 0, g: 0, b: 0 });
    });

    test('handles hex without hash', () => {
        expect(hexToRgb('ff5500')).toEqual({ r: 255, g: 85, b: 0 });
    });

    test('returns null for invalid hex', () => {
        expect(hexToRgb('invalid')).toBeNull();
        expect(hexToRgb('#fff')).toBeNull(); // short form not supported
        expect(hexToRgb('')).toBeNull();
        expect(hexToRgb(null)).toBeNull();
    });
});

describe('getLuminance', () => {
    test('returns high luminance for white', () => {
        expect(getLuminance(255, 255, 255)).toBeCloseTo(1, 2);
    });

    test('returns low luminance for black', () => {
        expect(getLuminance(0, 0, 0)).toBe(0);
    });

    test('returns mid-range luminance for gray', () => {
        const lum = getLuminance(128, 128, 128);
        expect(lum).toBeGreaterThan(0.1);
        expect(lum).toBeLessThan(0.5);
    });
});

describe('getColorFromHex', () => {
    test('returns color object for valid hex', () => {
        const color = getColorFromHex('#ff5500');
        expect(color).toHaveProperty('background', '#ff5500');
        expect(color).toHaveProperty('text');
        expect(color).toHaveProperty('border');
    });

    test('returns white text for dark backgrounds', () => {
        const color = getColorFromHex('#000000');
        expect(color.text).toBe('white');
    });

    test('returns dark text for light backgrounds', () => {
        const color = getColorFromHex('#ffffff');
        expect(color.text).toBe('rgb(30, 30, 30)');
    });

    test('returns null for invalid hex', () => {
        expect(getColorFromHex('invalid')).toBeNull();
    });
});

describe('hslToHex', () => {
    test('converts primary colors', () => {
        expect(hslToHex(0, 100, 50)).toBe('#ff0000'); // red
        expect(hslToHex(120, 100, 50)).toBe('#00ff00'); // green
        expect(hslToHex(240, 100, 50)).toBe('#0000ff'); // blue
    });

    test('converts grayscale', () => {
        expect(hslToHex(0, 0, 0)).toBe('#000000'); // black
        expect(hslToHex(0, 0, 100)).toBe('#ffffff'); // white
        expect(hslToHex(0, 0, 50)).toBe('#808080'); // gray
    });
});

// =============================================================================
// Credential Validation Tests
// =============================================================================

describe('validateUsername', () => {
    test('accepts valid usernames', () => {
        expect(validateUsername('admin')).toBe('');
        expect(validateUsername('user123')).toBe('');
        expect(validateUsername('john.doe')).toBe('');
        expect(validateUsername('my-user')).toBe('');
        expect(validateUsername('my_user')).toBe('');
        expect(validateUsername('User123')).toBe('');
        expect(validateUsername('a1b')).toBe(''); // minimum length
    });

    test('rejects empty or missing username', () => {
        expect(validateUsername('')).toBe('Username is required');
        expect(validateUsername('   ')).toBe('Username is required');
        expect(validateUsername(null)).toBe('Username is required');
        expect(validateUsername(undefined)).toBe('Username is required');
    });

    test('rejects too short usernames', () => {
        expect(validateUsername('ab')).toBe('Username must be at least 3 characters');
        expect(validateUsername('a')).toBe('Username must be at least 3 characters');
    });

    test('rejects too long usernames', () => {
        const longUsername = 'a'.repeat(MAX_USERNAME_LENGTH + 1);
        expect(validateUsername(longUsername)).toBe('Username must be at most 64 characters');
    });

    test('rejects usernames not starting with letter or number', () => {
        expect(validateUsername('_user')).toBe('Username must start with a letter or number');
        expect(validateUsername('-user')).toBe('Username must start with a letter or number');
        expect(validateUsername('.user')).toBe('Username must start with a letter or number');
    });

    test('rejects usernames with invalid characters', () => {
        expect(validateUsername('user@name')).toBe('Username can only contain letters, numbers, underscore, hyphen, and dot');
        expect(validateUsername('user name')).toBe('Username can only contain letters, numbers, underscore, hyphen, and dot');
        expect(validateUsername('user!123')).toBe('Username can only contain letters, numbers, underscore, hyphen, and dot');
    });

    test('trims whitespace before validation', () => {
        expect(validateUsername('  admin  ')).toBe('');
    });
});

describe('validatePassword', () => {
    test('accepts valid passwords', () => {
        expect(validatePassword('MyP@ssw0rd')).toBe('');
        expect(validatePassword('SecurePass123')).toBe('');
        expect(validatePassword('abcd1234')).toBe('');
        expect(validatePassword('Pass!@#$%')).toBe('');
        expect(validatePassword('a1b2c3d4')).toBe(''); // minimum length with complexity
    });

    test('rejects empty or missing password', () => {
        expect(validatePassword('')).toBe('Password is required');
        expect(validatePassword(null)).toBe('Password is required');
        expect(validatePassword(undefined)).toBe('Password is required');
    });

    test('rejects too short passwords', () => {
        expect(validatePassword('abc123')).toBe('Password must be at least 8 characters');
        expect(validatePassword('Pass1')).toBe('Password must be at least 8 characters');
    });

    test('rejects too long passwords', () => {
        const longPassword = 'a1'.repeat(65); // 130 chars
        expect(validatePassword(longPassword)).toBe('Password must be at most 128 characters');
    });

    test('rejects common weak passwords', () => {
        expect(validatePassword('password')).toBe('Password is too common. Please choose a stronger password');
        expect(validatePassword('PASSWORD')).toBe('Password is too common. Please choose a stronger password');
        expect(validatePassword('12345678')).toBe('Password is too common. Please choose a stronger password');
        expect(validatePassword('qwerty123')).toBe('Password is too common. Please choose a stronger password');
        expect(validatePassword('admin123')).toBe('Password is too common. Please choose a stronger password');
        expect(validatePassword('changeme')).toBe('Password is too common. Please choose a stronger password');
    });

    test('rejects passwords without letters', () => {
        expect(validatePassword('12345678!')).toBe('Password must contain at least one letter and one number or special character');
    });

    test('rejects passwords without numbers or special characters', () => {
        expect(validatePassword('abcdefgh')).toBe('Password must contain at least one letter and one number or special character');
        expect(validatePassword('PasswordOnly')).toBe('Password must contain at least one letter and one number or special character');
    });

    test('accepts passwords with special characters instead of numbers', () => {
        expect(validatePassword('Password!')).toBe('');
        expect(validatePassword('SecurePass@#')).toBe('');
    });
});

describe('validateCredentials', () => {
    test('returns empty string when both are valid', () => {
        expect(validateCredentials('admin', 'SecurePass123')).toBe('');
    });

    test('returns username error first if username is invalid', () => {
        expect(validateCredentials('', 'SecurePass123')).toBe('Username is required');
        expect(validateCredentials('ab', 'SecurePass123')).toBe('Username must be at least 3 characters');
    });

    test('returns password error if username is valid but password is invalid', () => {
        expect(validateCredentials('admin', '')).toBe('Password is required');
        expect(validateCredentials('admin', 'short')).toBe('Password must be at least 8 characters');
        expect(validateCredentials('admin', 'password')).toBe('Password is too common. Please choose a stronger password');
    });
});

// =============================================================================
// Pending Prompts Queue Tests
// =============================================================================

// Mock localStorage for testing
const localStorageMock = (() => {
    let store = {};
    return {
        getItem: (key) => store[key] || null,
        setItem: (key, value) => { store[key] = value; },
        removeItem: (key) => { delete store[key]; },
        clear: () => { store = {}; }
    };
})();

// Replace global localStorage with mock
Object.defineProperty(global, 'localStorage', { value: localStorageMock });

describe('generatePromptId', () => {
    test('generates unique IDs', () => {
        const id1 = generatePromptId();
        const id2 = generatePromptId();
        expect(id1).not.toBe(id2);
    });

    test('generates IDs with prompt_ prefix', () => {
        const id = generatePromptId();
        expect(id.startsWith('prompt_')).toBe(true);
    });

    test('generates IDs with timestamp component', () => {
        const before = Date.now();
        const id = generatePromptId();
        const after = Date.now();

        // Extract timestamp from ID (format: prompt_<timestamp>_<random>)
        const parts = id.split('_');
        expect(parts.length).toBe(3);
        const timestamp = parseInt(parts[1], 10);
        expect(timestamp).toBeGreaterThanOrEqual(before);
        expect(timestamp).toBeLessThanOrEqual(after);
    });
});

describe('savePendingPrompt and getPendingPrompts', () => {
    beforeEach(() => {
        localStorageMock.clear();
    });

    test('saves and retrieves a pending prompt', () => {
        savePendingPrompt('session1', 'prompt1', 'Hello world', []);

        const pending = getPendingPrompts();
        expect(pending['prompt1']).toBeDefined();
        expect(pending['prompt1'].sessionId).toBe('session1');
        expect(pending['prompt1'].message).toBe('Hello world');
        expect(pending['prompt1'].imageIds).toEqual([]);
        expect(pending['prompt1'].timestamp).toBeDefined();
    });

    test('saves prompt with image IDs', () => {
        savePendingPrompt('session1', 'prompt1', 'With images', ['img1', 'img2']);

        const pending = getPendingPrompts();
        expect(pending['prompt1'].imageIds).toEqual(['img1', 'img2']);
    });

    test('saves multiple prompts', () => {
        savePendingPrompt('session1', 'prompt1', 'First', []);
        savePendingPrompt('session1', 'prompt2', 'Second', []);
        savePendingPrompt('session2', 'prompt3', 'Third', []);

        const pending = getPendingPrompts();
        expect(Object.keys(pending).length).toBe(3);
    });

    test('returns empty object when no pending prompts', () => {
        const pending = getPendingPrompts();
        expect(pending).toEqual({});
    });
});

describe('removePendingPrompt', () => {
    beforeEach(() => {
        localStorageMock.clear();
    });

    test('removes a pending prompt', () => {
        savePendingPrompt('session1', 'prompt1', 'Hello', []);
        savePendingPrompt('session1', 'prompt2', 'World', []);

        removePendingPrompt('prompt1');

        const pending = getPendingPrompts();
        expect(pending['prompt1']).toBeUndefined();
        expect(pending['prompt2']).toBeDefined();
    });

    test('handles removing non-existent prompt gracefully', () => {
        savePendingPrompt('session1', 'prompt1', 'Hello', []);

        // Should not throw
        removePendingPrompt('nonexistent');

        const pending = getPendingPrompts();
        expect(pending['prompt1']).toBeDefined();
    });
});

describe('getPendingPromptsForSession', () => {
    beforeEach(() => {
        localStorageMock.clear();
    });

    test('returns prompts for specific session', () => {
        savePendingPrompt('session1', 'prompt1', 'First', []);
        savePendingPrompt('session1', 'prompt2', 'Second', []);
        savePendingPrompt('session2', 'prompt3', 'Third', []);

        const session1Prompts = getPendingPromptsForSession('session1');
        expect(session1Prompts.length).toBe(2);
        expect(session1Prompts.map(p => p.promptId)).toContain('prompt1');
        expect(session1Prompts.map(p => p.promptId)).toContain('prompt2');
    });

    test('returns empty array for session with no prompts', () => {
        savePendingPrompt('session1', 'prompt1', 'Hello', []);

        const prompts = getPendingPromptsForSession('session2');
        expect(prompts).toEqual([]);
    });

    test('returns prompts sorted by timestamp (oldest first)', () => {
        // Save prompts with explicit timestamps by manipulating the stored data
        const now = Date.now();
        localStorageMock.setItem('mitto_pending_prompts', JSON.stringify({
            'prompt1': { sessionId: 'session1', message: 'First', imageIds: [], timestamp: now - 2000 },
            'prompt2': { sessionId: 'session1', message: 'Second', imageIds: [], timestamp: now - 1000 },
            'prompt3': { sessionId: 'session1', message: 'Third', imageIds: [], timestamp: now }
        }));

        const prompts = getPendingPromptsForSession('session1');
        expect(prompts[0].promptId).toBe('prompt1');
        expect(prompts[1].promptId).toBe('prompt2');
        expect(prompts[2].promptId).toBe('prompt3');
    });

    test('excludes expired prompts (older than 5 minutes)', () => {
        const now = Date.now();
        const fiveMinutesAgo = now - (5 * 60 * 1000);

        localStorageMock.setItem('mitto_pending_prompts', JSON.stringify({
            'prompt1': { sessionId: 'session1', message: 'Fresh', imageIds: [], timestamp: now - 1000 },
            'prompt2': { sessionId: 'session1', message: 'Expired', imageIds: [], timestamp: fiveMinutesAgo - 1000 }
        }));

        const prompts = getPendingPromptsForSession('session1');
        expect(prompts.length).toBe(1);
        expect(prompts[0].promptId).toBe('prompt1');
    });
});

describe('cleanupExpiredPrompts', () => {
    beforeEach(() => {
        localStorageMock.clear();
    });

    test('removes expired prompts', () => {
        const now = Date.now();
        const fiveMinutesAgo = now - (5 * 60 * 1000);

        localStorageMock.setItem('mitto_pending_prompts', JSON.stringify({
            'prompt1': { sessionId: 'session1', message: 'Fresh', imageIds: [], timestamp: now - 1000 },
            'prompt2': { sessionId: 'session1', message: 'Expired1', imageIds: [], timestamp: fiveMinutesAgo - 1000 },
            'prompt3': { sessionId: 'session2', message: 'Expired2', imageIds: [], timestamp: fiveMinutesAgo - 2000 }
        }));

        cleanupExpiredPrompts();

        const pending = getPendingPrompts();
        expect(Object.keys(pending).length).toBe(1);
        expect(pending['prompt1']).toBeDefined();
        expect(pending['prompt2']).toBeUndefined();
        expect(pending['prompt3']).toBeUndefined();
    });

    test('does nothing when no prompts exist', () => {
        // Should not throw
        cleanupExpiredPrompts();
        expect(getPendingPrompts()).toEqual({});
    });

    test('does nothing when all prompts are fresh', () => {
        savePendingPrompt('session1', 'prompt1', 'Fresh1', []);
        savePendingPrompt('session1', 'prompt2', 'Fresh2', []);

        cleanupExpiredPrompts();

        const pending = getPendingPrompts();
        expect(Object.keys(pending).length).toBe(2);
    });
});

// =============================================================================
// User Message Markdown Tests
// =============================================================================

describe('hasMarkdownContent', () => {
    // Invalid inputs
    test('returns false for null', () => {
        expect(hasMarkdownContent(null)).toBe(false);
    });

    test('returns false for undefined', () => {
        expect(hasMarkdownContent(undefined)).toBe(false);
    });

    test('returns false for empty string', () => {
        expect(hasMarkdownContent('')).toBe(false);
    });

    test('returns false for non-string input', () => {
        expect(hasMarkdownContent(123)).toBe(false);
        expect(hasMarkdownContent({})).toBe(false);
        expect(hasMarkdownContent([])).toBe(false);
    });

    // Plain text (no markdown)
    test('returns false for plain text', () => {
        expect(hasMarkdownContent('Hello world')).toBe(false);
        expect(hasMarkdownContent('Just a simple message')).toBe(false);
        expect(hasMarkdownContent('This is a normal sentence.')).toBe(false);
    });

    test('returns false for text with standalone asterisks', () => {
        // Single asterisks surrounded by spaces are not markdown
        expect(hasMarkdownContent('I like * patterns * in text')).toBe(false);
    });

    // Headers
    test('detects headers', () => {
        expect(hasMarkdownContent('# Header')).toBe(true);
        expect(hasMarkdownContent('## Second Level')).toBe(true);
        expect(hasMarkdownContent('### Third Level')).toBe(true);
        expect(hasMarkdownContent('#### Fourth Level')).toBe(true);
        expect(hasMarkdownContent('Some text\n# Header')).toBe(true);
    });

    test('does not detect hash without space as header', () => {
        expect(hasMarkdownContent('#hashtag')).toBe(false);
        expect(hasMarkdownContent('Issue #123')).toBe(false);
    });

    // Bold
    test('detects bold text', () => {
        expect(hasMarkdownContent('This is **bold** text')).toBe(true);
        expect(hasMarkdownContent('This is __bold__ text')).toBe(true);
    });

    // Italic
    test('detects italic text', () => {
        expect(hasMarkdownContent('This is *italic* text')).toBe(true);
        expect(hasMarkdownContent('This is _italic_ text')).toBe(true);
    });

    // Code
    test('detects inline code', () => {
        expect(hasMarkdownContent('Use `code` here')).toBe(true);
        expect(hasMarkdownContent('Run `npm install`')).toBe(true);
    });

    test('detects code blocks', () => {
        expect(hasMarkdownContent('```javascript\nconst x = 1;\n```')).toBe(true);
        expect(hasMarkdownContent('```\ncode block\n```')).toBe(true);
    });

    // Links
    test('detects links', () => {
        expect(hasMarkdownContent('Check [this link](https://example.com)')).toBe(true);
        expect(hasMarkdownContent('See [reference][1]')).toBe(true);
    });

    // Lists
    test('detects unordered lists', () => {
        expect(hasMarkdownContent('- Item 1')).toBe(true);
        expect(hasMarkdownContent('* Item 1')).toBe(true);
        expect(hasMarkdownContent('+ Item 1')).toBe(true);
        expect(hasMarkdownContent('Some text\n- Item')).toBe(true);
    });

    test('detects ordered lists', () => {
        expect(hasMarkdownContent('1. First item')).toBe(true);
        expect(hasMarkdownContent('2. Second item')).toBe(true);
        expect(hasMarkdownContent('10. Tenth item')).toBe(true);
    });

    // Blockquotes
    test('detects blockquotes', () => {
        expect(hasMarkdownContent('> This is a quote')).toBe(true);
        expect(hasMarkdownContent('Text before\n> Quote')).toBe(true);
    });

    // Horizontal rules
    test('detects horizontal rules', () => {
        expect(hasMarkdownContent('---')).toBe(true);
        expect(hasMarkdownContent('***')).toBe(true);
        expect(hasMarkdownContent('___')).toBe(true);
    });

    // Tables
    test('detects tables', () => {
        expect(hasMarkdownContent('| Header | Header |\n| --- | --- |')).toBe(true);
    });

    // Strikethrough
    test('detects strikethrough', () => {
        expect(hasMarkdownContent('This is ~~deleted~~ text')).toBe(true);
    });

    // Complex examples
    test('detects markdown in complex messages', () => {
        expect(hasMarkdownContent('Please run `npm install` and then:\n\n1. Start the server\n2. Open browser')).toBe(true);
        expect(hasMarkdownContent('Here is the **important** part:\n\n- First\n- Second')).toBe(true);
    });
});

describe('renderUserMarkdown', () => {
    // Note: These tests run in Node.js where window.marked is not available,
    // so renderUserMarkdown will return null. We test the logic that doesn't
    // depend on the browser environment.

    test('returns null for null input', () => {
        expect(renderUserMarkdown(null)).toBeNull();
    });

    test('returns null for undefined input', () => {
        expect(renderUserMarkdown(undefined)).toBeNull();
    });

    test('returns null for empty string', () => {
        expect(renderUserMarkdown('')).toBeNull();
    });

    test('returns null for non-string input', () => {
        expect(renderUserMarkdown(123)).toBeNull();
        expect(renderUserMarkdown({})).toBeNull();
    });

    test('returns null for plain text without markdown', () => {
        // Even if marked were available, plain text should return null
        // because hasMarkdownContent returns false
        expect(renderUserMarkdown('Hello world')).toBeNull();
    });

    test('returns null for text exceeding MAX_MARKDOWN_LENGTH', () => {
        const longText = '# Header\n' + 'x'.repeat(MAX_MARKDOWN_LENGTH + 1);
        expect(renderUserMarkdown(longText)).toBeNull();
    });

    test('returns null when window.marked is not available', () => {
        // In Node.js test environment, window is not defined
        // This tests the graceful fallback
        expect(renderUserMarkdown('# Header')).toBeNull();
    });
});
