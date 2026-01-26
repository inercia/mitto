// Mitto Web Interface - Preact Application
const { h, render, useState, useEffect, useRef, useCallback, useMemo, html } = window.preact;

// Import shared library functions (for browser, we also define them inline as fallback)
import {
    ROLE_USER,
    ROLE_AGENT,
    ROLE_THOUGHT,
    ROLE_TOOL,
    ROLE_ERROR,
    ROLE_SYSTEM,
    computeAllSessions,
    convertEventsToMessages,
    safeJsonParse,
    limitMessages
} from './lib.js';

// =============================================================================
// Sync State Persistence (localStorage)
// =============================================================================

// Get the last seen sequence number for a session from localStorage
function getLastSeenSeq(sessionId) {
    try {
        const key = `mitto_session_seq_${sessionId}`;
        const value = localStorage.getItem(key);
        return value ? parseInt(value, 10) : 0;
    } catch (e) {
        console.warn('Failed to read last seen seq from localStorage:', e);
        return 0;
    }
}

// Save the last seen sequence number for a session to localStorage
function setLastSeenSeq(sessionId, seq) {
    try {
        const key = `mitto_session_seq_${sessionId}`;
        localStorage.setItem(key, String(seq));
    } catch (e) {
        console.warn('Failed to save last seen seq to localStorage:', e);
    }
}

// Get the last active session ID from localStorage
function getLastActiveSessionId() {
    try {
        return localStorage.getItem('mitto_last_session_id') || null;
    } catch (e) {
        return null;
    }
}

// Save the last active session ID to localStorage
function setLastActiveSessionId(sessionId) {
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

// =============================================================================
// WebSocket Hook with Multi-Session Support
// =============================================================================

function useWebSocket() {
    const [connected, setConnected] = useState(false);
    const [isStreaming, setIsStreaming] = useState(false);

    // Multi-session state: { sessionId: { messages: [], info: {}, lastSeq: 0 } }
    const [sessions, setSessions] = useState({});
    const [activeSessionId, setActiveSessionId] = useState(null);
    const [storedSessions, setStoredSessions] = useState([]); // Sessions from the store

    const wsRef = useRef(null);
    const reconnectRef = useRef(null);
    const activeSessionIdRef = useRef(activeSessionId);

    // Ref to hold the latest handleMessage function to avoid stale closures
    const handleMessageRef = useRef(null);

    // Track if this is a reconnection (vs initial connection)
    const wasConnectedRef = useRef(false);

    // Keep ref in sync with state
    useEffect(() => {
        activeSessionIdRef.current = activeSessionId;
        // Persist last active session ID
        setLastActiveSessionId(activeSessionId);
    }, [activeSessionId]);

    // Get current session's messages
    const messages = useMemo(() => {
        if (!activeSessionId || !sessions[activeSessionId]) return [];
        return sessions[activeSessionId].messages || [];
    }, [sessions, activeSessionId]);

    // Get current session info
    const sessionInfo = useMemo(() => {
        if (!activeSessionId || !sessions[activeSessionId]) return null;
        return sessions[activeSessionId].info || null;
    }, [sessions, activeSessionId]);

    // Get all active sessions as array for sidebar
    const activeSessions = useMemo(() => {
        return Object.entries(sessions).map(([id, data]) => {
            // Find the most recent user message timestamp
            const userMessages = (data.messages || []).filter(m => m.role === ROLE_USER);
            const lastUserMsgTime = userMessages.length > 0
                ? new Date(Math.max(...userMessages.map(m => m.timestamp || 0))).toISOString()
                : null;
            return {
                session_id: id,
                name: data.info?.name || 'New conversation',
                acp_server: data.info?.acp_server || '',
                created_at: data.info?.created_at || new Date().toISOString(),
                updated_at: data.info?.updated_at || new Date().toISOString(),
                last_user_message_at: lastUserMsgTime || data.info?.last_user_message_at,
                status: 'active',
                isActive: true,
                messageCount: data.messages?.length || 0
            };
        });
    }, [sessions]);

    const connect = useCallback(() => {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const socket = new WebSocket(`${protocol}//${window.location.host}/ws`);

        socket.onopen = () => {
            setConnected(true);
            console.log('WebSocket connected');
        };

        socket.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                // Use ref to always call the latest handleMessage (avoids stale closure)
                if (handleMessageRef.current) {
                    handleMessageRef.current(msg);
                }
            } catch (err) {
                console.error('Failed to parse WebSocket message:', err, event.data);
            }
        };

        socket.onclose = () => {
            // Track that we were connected (for reconnection logic)
            if (wsRef.current) {
                wasConnectedRef.current = true;
            }
            setConnected(false);
            setIsStreaming(false);
            wsRef.current = null;
            reconnectRef.current = setTimeout(connect, 2000);
        };

        socket.onerror = (err) => {
            console.error('WebSocket error:', err);
            socket.close();
        };

        wsRef.current = socket;
    }, []);

    const addMessageToSession = useCallback((sessionId, message) => {
        setSessions(prev => {
            const session = prev[sessionId] || { messages: [], info: {} };
            return {
                ...prev,
                [sessionId]: {
                    ...session,
                    messages: limitMessages([...session.messages, message])
                }
            };
        });
    }, []);

    const updateLastMessage = useCallback((sessionId, updater) => {
        setSessions(prev => {
            const session = prev[sessionId];
            if (!session || session.messages.length === 0) return prev;
            const messages = [...session.messages];
            const lastIdx = messages.length - 1;
            messages[lastIdx] = updater(messages[lastIdx]);
            return {
                ...prev,
                [sessionId]: { ...session, messages }
            };
        });
    }, []);

    const send = useCallback((msg) => {
        if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
            wsRef.current.send(JSON.stringify(msg));
        }
    }, []);

    // Helper to validate session ID and log discarded messages
    const validateSessionId = useCallback((msgType, sessionId) => {
        if (!sessionId) {
            console.warn(`[${msgType}] Message missing session_id, discarding`);
            return false;
        }
        // We don't discard messages for unknown sessions here because
        // the session might be created by a concurrent session_created message.
        // The setSessions callback will handle the case where session doesn't exist.
        return true;
    }, []);

    const handleMessage = useCallback((msg) => {
        switch (msg.type) {
            case 'connected': {
                // Check if this is a reconnection (we had a session before)
                const isReconnect = wasConnectedRef.current;
                const lastSessionId = getLastActiveSessionId();
                const currentSessionId = activeSessionIdRef.current;

                // On reconnection with an active session, request incremental sync
                if (isReconnect && currentSessionId) {
                    const lastSeq = getLastSeenSeq(currentSessionId);
                    console.log(`Reconnecting: requesting sync for session ${currentSessionId} after seq ${lastSeq}`);
                    send({ type: 'sync_session', data: { session_id: currentSessionId, after_seq: lastSeq } });
                    // Also fetch stored sessions to update the sidebar
                    fetch('/api/sessions')
                        .then(res => res.json())
                        .then(sessions => setStoredSessions(sessions || []))
                        .catch(err => console.error('Failed to fetch sessions:', err));
                    break;
                }

                // On initial connection, fetch stored sessions and check if we should resume one
                fetch('/api/sessions')
                    .then(res => res.json())
                    .then(sessions => {
                        setStoredSessions(sessions || []);

                        // If we have a last session ID from localStorage, try to resume it
                        if (lastSessionId) {
                            const lastSession = (sessions || []).find(s => s.session_id === lastSessionId);
                            if (lastSession) {
                                console.log('Resuming last session from localStorage:', lastSessionId);
                                send({ type: 'switch_session', data: { session_id: lastSessionId } });
                                return;
                            }
                        }

                        // Find the most recent active session to resume
                        const activeSessions = (sessions || []).filter(s => s.status === 'active');
                        if (activeSessions.length > 0) {
                            // Sort by updated_at descending and resume the most recent
                            activeSessions.sort((a, b) => new Date(b.updated_at) - new Date(a.updated_at));
                            const mostRecent = activeSessions[0];
                            console.log('Resuming session:', mostRecent.session_id);
                            send({ type: 'switch_session', data: { session_id: mostRecent.session_id } });
                        } else {
                            // No active sessions, create a new one
                            send({ type: 'new_session' });
                        }
                    })
                    .catch(err => {
                        console.error('Failed to fetch sessions:', err);
                        // Fallback: create new session
                        send({ type: 'new_session' });
                    });
                break;
            }

            case 'session_created': {
                const sessionId = msg.data.session_id;
                setSessions(prev => ({
                    ...prev,
                    [sessionId]: {
                        messages: [{
                            role: ROLE_SYSTEM,
                            text: `Connected to ${msg.data.acp_server}`,
                            timestamp: Date.now()
                        }],
                        info: {
                            session_id: sessionId,
                            name: msg.data.name || 'New conversation',
                            acp_server: msg.data.acp_server,
                            created_at: msg.data.created_at,
                            status: 'active'
                        }
                    }
                }));
                setActiveSessionId(sessionId);
                break;
            }

            case 'session_switched': {
                const sessionId = msg.data.session_id;
                const events = msg.data.events || [];
                // Convert stored events to messages
                const messages = convertEventsToMessages(events);
                messages.unshift({
                    role: ROLE_SYSTEM,
                    text: `Resumed session: ${msg.data.name || sessionId}`,
                    timestamp: Date.now()
                });
                // Track the last sequence number from the events
                const lastSeq = events.length > 0 ? Math.max(...events.map(e => e.seq || 0)) : 0;
                setLastSeenSeq(sessionId, lastSeq);
                setSessions(prev => ({
                    ...prev,
                    [sessionId]: {
                        messages,
                        lastSeq,
                        info: {
                            session_id: sessionId,
                            name: msg.data.name || 'Conversation',
                            acp_server: msg.data.acp_server,
                            created_at: msg.data.created_at,
                            status: 'active'
                        }
                    }
                }));
                setActiveSessionId(sessionId);
                break;
            }

            case 'session_sync': {
                // Incremental sync response - merge new events into existing session
                const sessionId = msg.data.session_id;
                const events = msg.data.events || [];
                const newMessages = convertEventsToMessages(events);
                const lastSeq = events.length > 0 ? Math.max(...events.map(e => e.seq || 0)) : msg.data.after_seq;
                const isRunning = msg.data.is_running || false;

                console.log(`Session sync: received ${events.length} new events for ${sessionId}, lastSeq=${lastSeq}, isRunning=${isRunning}`);

                // Update last seen sequence
                setLastSeenSeq(sessionId, lastSeq);

                // If the session is still running (agent is processing), show streaming indicator
                if (isRunning) {
                    setIsStreaming(true);
                }

                if (newMessages.length === 0) {
                    // No new messages, just update metadata
                    setSessions(prev => {
                        const session = prev[sessionId];
                        if (!session) return prev;
                        return {
                            ...prev,
                            [sessionId]: {
                                ...session,
                                lastSeq,
                                info: {
                                    ...session.info,
                                    name: msg.data.name || session.info?.name,
                                    status: msg.data.status || session.info?.status
                                }
                            }
                        };
                    });
                } else {
                    // Append new messages to existing session
                    setSessions(prev => {
                        const session = prev[sessionId];
                        if (!session) {
                            // Session doesn't exist locally, create it with the new messages
                            return {
                                ...prev,
                                [sessionId]: {
                                    messages: [{
                                        role: ROLE_SYSTEM,
                                        text: `Synced ${newMessages.length} new messages`,
                                        timestamp: Date.now()
                                    }, ...newMessages],
                                    lastSeq,
                                    info: {
                                        session_id: sessionId,
                                        name: msg.data.name || 'Conversation',
                                        status: msg.data.status || 'active'
                                    }
                                }
                            };
                        }
                        // Append to existing messages
                        return {
                            ...prev,
                            [sessionId]: {
                                ...session,
                                messages: limitMessages([...session.messages, ...newMessages]),
                                lastSeq,
                                info: {
                                    ...session.info,
                                    name: msg.data.name || session.info?.name,
                                    status: msg.data.status || session.info?.status
                                }
                            }
                        };
                    });
                }
                break;
            }

            case 'agent_message': {
                // Use session_id from message to route to correct session (fixes race condition on session switch)
                const targetSessionId = msg.data.session_id;
                if (!validateSessionId('agent_message', targetSessionId)) break;
                // Only show streaming indicator if message is for the active session
                if (targetSessionId === activeSessionIdRef.current) {
                    setIsStreaming(true);
                }
                setSessions(prev => {
                    const session = prev[targetSessionId];
                    if (!session) {
                        // Session doesn't exist - message from closed/switched session, discard
                        console.debug(`[agent_message] Discarding message for unknown session ${targetSessionId}`);
                        return prev;
                    }
                    let messages = [...session.messages];
                    const last = messages[messages.length - 1];
                    if (last && last.role === ROLE_AGENT && !last.complete) {
                        messages[messages.length - 1] = { ...last, html: (last.html || '') + msg.data.html };
                    } else {
                        messages.push({ role: ROLE_AGENT, html: msg.data.html, complete: false, timestamp: Date.now() });
                        messages = limitMessages(messages);
                    }
                    return { ...prev, [targetSessionId]: { ...session, messages } };
                });
                break;
            }

            case 'agent_thought': {
                // Use session_id from message to route to correct session
                const targetSessionId = msg.data.session_id;
                if (!validateSessionId('agent_thought', targetSessionId)) break;
                setSessions(prev => {
                    const session = prev[targetSessionId];
                    if (!session) {
                        console.debug(`[agent_thought] Discarding message for unknown session ${targetSessionId}`);
                        return prev;
                    }
                    let messages = [...session.messages];
                    const last = messages[messages.length - 1];
                    if (last && last.role === ROLE_THOUGHT && !last.complete) {
                        messages[messages.length - 1] = { ...last, text: (last.text || '') + msg.data.text };
                    } else {
                        messages.push({ role: ROLE_THOUGHT, text: msg.data.text, complete: false, timestamp: Date.now() });
                        messages = limitMessages(messages);
                    }
                    return { ...prev, [targetSessionId]: { ...session, messages } };
                });
                break;
            }

            case 'tool_call': {
                // Use session_id from message to route to correct session
                const targetSessionId = msg.data.session_id;
                if (!validateSessionId('tool_call', targetSessionId)) break;
                addMessageToSession(targetSessionId, {
                    role: ROLE_TOOL, id: msg.data.id, title: msg.data.title, status: msg.data.status, timestamp: Date.now()
                });
                break;
            }

            case 'tool_update': {
                // Use session_id from message to route to correct session
                const targetSessionId = msg.data.session_id;
                if (!validateSessionId('tool_update', targetSessionId)) break;
                setSessions(prev => {
                    const session = prev[targetSessionId];
                    if (!session) {
                        console.debug(`[tool_update] Discarding message for unknown session ${targetSessionId}`);
                        return prev;
                    }
                    const messages = [...session.messages];
                    const idx = messages.findLastIndex(m => m.role === ROLE_TOOL && m.id === msg.data.id);
                    if (idx >= 0 && msg.data.status) {
                        messages[idx] = { ...messages[idx], status: msg.data.status };
                    }
                    return { ...prev, [targetSessionId]: { ...session, messages } };
                });
                break;
            }

            case 'prompt_complete': {
                // Use session_id from message to route to correct session
                const targetSessionId = msg.data?.session_id;
                if (!validateSessionId('prompt_complete', targetSessionId)) break;
                // Only clear streaming indicator if this is for the active session
                if (targetSessionId === activeSessionIdRef.current) {
                    setIsStreaming(false);
                }
                updateLastMessage(targetSessionId, m =>
                    (m.role === ROLE_AGENT || m.role === ROLE_THOUGHT) ? { ...m, complete: true } : m
                );
                break;
            }

            case 'error': {
                // Use session_id from message to route to correct session
                const targetSessionId = msg.data?.session_id;
                // Only clear streaming indicator if this is for the active session
                if (targetSessionId === activeSessionIdRef.current) {
                    setIsStreaming(false);
                }
                if (validateSessionId('error', targetSessionId)) {
                    addMessageToSession(targetSessionId, {
                        role: ROLE_ERROR, text: msg.data.message, timestamp: Date.now()
                    });
                }
                break;
            }

            case 'permission':
                console.log('Permission requested:', msg.data);
                break;

            case 'session_loaded': {
                // Read-only view of a past session
                const sessionId = msg.data.session_id;
                const messages = convertEventsToMessages(msg.data.events || []);
                messages.unshift({
                    role: ROLE_SYSTEM,
                    text: `Viewing session: ${msg.data.name || sessionId} (read-only)`,
                    timestamp: Date.now()
                });
                setSessions(prev => ({
                    ...prev,
                    [sessionId]: {
                        messages,
                        info: {
                            session_id: sessionId,
                            name: msg.data.name || 'Past conversation',
                            acp_server: msg.data.acp_server,
                            created_at: msg.data.created_at,
                            status: msg.data.status || 'completed',
                            isReadOnly: true
                        }
                    }
                }));
                setActiveSessionId(sessionId);
                break;
            }

            case 'session_renamed': {
                // Update session name (e.g., from auto-title generation)
                const sessionId = msg.data.session_id;
                const newName = msg.data.name;
                // Update in-memory session info
                setSessions(prev => {
                    if (!prev[sessionId]) return prev;
                    return {
                        ...prev,
                        [sessionId]: {
                            ...prev[sessionId],
                            info: {
                                ...prev[sessionId].info,
                                name: newName
                            }
                        }
                    };
                });
                // Also update stored sessions list
                setStoredSessions(prev => prev.map(s =>
                    s.session_id === sessionId ? { ...s, name: newName } : s
                ));
                break;
            }

            case 'session_deleted': {
                // Session was deleted (possibly from another browser window)
                const sessionId = msg.data.session_id;
                console.log('Session deleted:', sessionId);

                // Remove from stored sessions list
                setStoredSessions(prev => prev.filter(s => s.session_id !== sessionId));

                // Remove from in-memory sessions and handle active session switch in a single update
                // This avoids race conditions from multiple setSessions calls
                const currentSessionId = activeSessionIdRef.current;
                const isActiveSession = sessionId === currentSessionId;

                setSessions(prev => {
                    const { [sessionId]: removed, ...rest } = prev;

                    // If the deleted session is the active one, switch to another or create new
                    if (isActiveSession) {
                        const remainingIds = Object.keys(rest);
                        if (remainingIds.length > 0) {
                            setActiveSessionId(remainingIds[0]);
                        } else {
                            setActiveSessionId(null);
                            // Request a new session
                            send({ type: 'new_session' });
                        }
                    }

                    return rest;
                });
                break;
            }
        }
    }, [addMessageToSession, updateLastMessage, send, validateSessionId]);

    // Keep handleMessageRef in sync with handleMessage to avoid stale closures in WebSocket callbacks
    useEffect(() => {
        handleMessageRef.current = handleMessage;
    }, [handleMessage]);

    const sendPrompt = useCallback((message) => {
        if (!activeSessionId) return;
        addMessageToSession(activeSessionId, { role: ROLE_USER, text: message, timestamp: Date.now() });
        // Mark any previous streaming message as complete
        updateLastMessage(activeSessionId, m =>
            !m.complete && (m.role === ROLE_AGENT || m.role === ROLE_THOUGHT) ? { ...m, complete: true } : m
        );
        send({ type: 'prompt', data: { message } });
    }, [send, activeSessionId, addMessageToSession, updateLastMessage]);

    const cancelPrompt = useCallback(() => {
        send({ type: 'cancel' });
    }, [send]);

    const newSession = useCallback((name) => {
        send({ type: 'new_session', data: { name: name || '' } });
    }, [send]);

    const switchSession = useCallback((sessionId) => {
        // If session is already active in memory, just switch to it
        if (sessions[sessionId]) {
            setActiveSessionId(sessionId);
            return;
        }
        // Otherwise, switch session (starts new ACP connection)
        send({ type: 'switch_session', data: { session_id: sessionId } });
    }, [send, sessions]);

    const loadSession = useCallback((sessionId) => {
        // If session is already loaded in memory, just switch to it
        if (sessions[sessionId]) {
            setActiveSessionId(sessionId);
            return;
        }
        // Load session for read-only viewing (no ACP connection)
        send({ type: 'load_session', data: { session_id: sessionId } });
    }, [send, sessions]);

    const updateSessionName = useCallback((sessionId, name) => {
        setSessions(prev => {
            const session = prev[sessionId];
            if (!session) return prev;
            return {
                ...prev,
                [sessionId]: {
                    ...session,
                    info: { ...session.info, name }
                }
            };
        });
    }, []);

    // Rename a session via WebSocket (persists to storage and broadcasts to all clients)
    const renameSession = useCallback((sessionId, name) => {
        send({ type: 'rename_session', data: { session_id: sessionId, name } });
    }, [send]);

    const removeSession = useCallback((sessionId) => {
        // Use activeSessionIdRef to get current value and avoid stale closure
        const currentActiveSessionId = activeSessionIdRef.current;

        // Remove session and handle active session switch in a single update
        // This avoids using stale `sessions` object from closure
        setSessions(prev => {
            const { [sessionId]: removed, ...rest } = prev;

            // If we removed the active session, switch to another or create new
            if (sessionId === currentActiveSessionId) {
                const remainingIds = Object.keys(rest);
                if (remainingIds.length > 0) {
                    setActiveSessionId(remainingIds[0]);
                } else {
                    setActiveSessionId(null);
                    send({ type: 'new_session' });
                }
            }

            return rest;
        });
    }, [send]);

    // Fetch stored sessions on mount
    const fetchStoredSessions = useCallback(() => {
        fetch('/api/sessions')
            .then(res => res.json())
            .then(data => setStoredSessions(data || []))
            .catch(err => console.error('Failed to fetch sessions:', err));
    }, []);

    useEffect(() => {
        connect();
        fetchStoredSessions();
        return () => {
            if (reconnectRef.current) clearTimeout(reconnectRef.current);
            if (wsRef.current) wsRef.current.close();
        };
    }, [connect, fetchStoredSessions]);

    return {
        connected,
        messages,
        sendPrompt,
        cancelPrompt,
        newSession,
        switchSession,
        loadSession,
        updateSessionName,
        renameSession,
        removeSession,
        isStreaming,
        sessionInfo,
        activeSessionId,
        activeSessions,
        storedSessions,
        fetchStoredSessions
    };
}

// Note: convertEventsToMessages is imported from lib.js

// =============================================================================
// Message Component
// =============================================================================

function Message({ message, isLast, isStreaming }) {
    const isUser = message.role === ROLE_USER;
    const isAgent = message.role === ROLE_AGENT;
    const isThought = message.role === ROLE_THOUGHT;
    const isTool = message.role === ROLE_TOOL;
    const isError = message.role === ROLE_ERROR;
    const isSystem = message.role === ROLE_SYSTEM;

    // System messages
    if (isSystem) {
        return html`
            <div class="message-enter flex justify-center mb-3">
                <div class="text-xs text-gray-500 bg-slate-800/50 px-3 py-1 rounded-full">
                    ${message.text}
                </div>
            </div>
        `;
    }

    // Tool call display
    if (isTool) {
        const isRunning = message.status === 'running';
        const isCompleted = message.status === 'completed';
        const isFailed = message.status === 'failed';

        const renderStatus = () => {
            if (isCompleted) {
                // Green checkmark icon for completed
                return html`
                    <svg class="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
                    </svg>
                `;
            }
            if (isFailed) {
                // Red X icon for failed
                return html`
                    <svg class="w-4 h-4 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                `;
            }
            if (isRunning) {
                // Spinning indicator for running
                return html`
                    <svg class="w-4 h-4 text-yellow-400 animate-spin" fill="none" viewBox="0 0 24 24">
                        <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                        <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                    </svg>
                `;
            }
            // Default: gray text for unknown status
            return html`<span class="text-xs text-gray-400">${message.status}</span>`;
        };

        return html`
            <div class="message-enter flex justify-center mb-3">
                <div class="text-sm text-gray-400 flex items-center gap-2 bg-slate-800/50 dark:bg-slate-800/50 px-3 py-1.5 rounded-lg">
                    <span class="text-yellow-500">🔧</span>
                    <span class="font-medium">${message.title}</span>
                    ${renderStatus()}
                </div>
            </div>
        `;
    }

    // Thought display (plain text)
    if (isThought) {
        const showCursor = isLast && isStreaming && !message.complete;
        return html`
            <div class="message-enter flex justify-start mb-3">
                <div class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-slate-800/50 text-gray-400 rounded-bl-sm border border-slate-700">
                    <div class="flex items-start gap-2">
                        <span class="text-purple-400 mt-0.5">💭</span>
                        <span class="italic ${showCursor ? 'streaming-cursor' : ''}">${message.text}</span>
                    </div>
                </div>
            </div>
        `;
    }

    // Error message
    if (isError) {
        return html`
            <div class="message-enter flex justify-start mb-3">
                <div class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-red-900/30 text-red-200 rounded-bl-sm border border-red-800">
                    <div class="flex items-start gap-2">
                        <span>❌</span>
                        <span>${message.text}</span>
                    </div>
                </div>
            </div>
        `;
    }

    // User message (plain text)
    if (isUser) {
        return html`
            <div class="message-enter flex justify-end mb-3">
                <div class="max-w-[85%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-mitto-user text-white rounded-br-sm">
                    <pre class="whitespace-pre-wrap font-sans text-sm m-0">${message.text}</pre>
                </div>
            </div>
        `;
    }

    // Agent message (HTML content)
    if (isAgent) {
        const showCursor = isLast && isStreaming && !message.complete;
        return html`
            <div class="message-enter flex justify-start mb-3">
                <div class="max-w-[85%] md:max-w-[75%] px-4 py-3 rounded-2xl bg-mitto-agent text-gray-100 rounded-bl-sm">
                    <div
                        class="markdown-content text-sm ${showCursor ? 'streaming-cursor' : ''}"
                        dangerouslySetInnerHTML=${{ __html: message.html || '' }}
                    />
                </div>
            </div>
        `;
    }

    return null;
}

// =============================================================================
// Chat Input Component
// =============================================================================

function ChatInput({ onSend, onCancel, disabled, isStreaming, isReadOnly, predefinedPrompts = [] }) {
    const [text, setText] = useState('');
    const [showDropup, setShowDropup] = useState(false);
    const textareaRef = useRef(null);
    const dropupRef = useRef(null);

    // Close dropup when clicking outside
    useEffect(() => {
        const handleClickOutside = (e) => {
            if (dropupRef.current && !dropupRef.current.contains(e.target)) {
                setShowDropup(false);
            }
        };
        if (showDropup) {
            document.addEventListener('mousedown', handleClickOutside);
            return () => document.removeEventListener('mousedown', handleClickOutside);
        }
    }, [showDropup]);

    const handleSubmit = (e) => {
        e.preventDefault();
        if (text.trim() && !disabled && !isReadOnly) {
            onSend(text.trim());
            setText('');
            if (textareaRef.current) {
                textareaRef.current.style.height = 'auto';
            }
        }
    };

    const handleKeyDown = (e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleSubmit(e);
        }
        // Close dropup on Escape
        if (e.key === 'Escape') {
            setShowDropup(false);
        }
    };

    const handleInput = (e) => {
        setText(e.target.value);
        const textarea = e.target;
        textarea.style.height = 'auto';
        textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
    };

    const handlePredefinedPrompt = (prompt) => {
        onSend(prompt.prompt);
        setShowDropup(false);
    };

    const getPlaceholder = () => {
        if (isReadOnly) return "This is a read-only session. Create a new session to chat.";
        if (isStreaming) return "Agent is responding...";
        return "Type your message...";
    };

    const hasPrompts = predefinedPrompts && predefinedPrompts.length > 0;

    return html`
        <form onSubmit=${handleSubmit} class="p-4 bg-mitto-input border-t border-slate-700">
            <div class="flex gap-2 max-w-4xl mx-auto">
                <textarea
                    ref=${textareaRef}
                    value=${text}
                    onInput=${handleInput}
                    onKeyDown=${handleKeyDown}
                    placeholder=${getPlaceholder()}
                    rows="1"
                    class="flex-1 bg-mitto-input-box text-white rounded-xl px-4 py-3 resize-none focus:outline-none focus:ring-2 focus:ring-blue-500 max-h-[200px] placeholder-gray-400 placeholder:text-sm border border-slate-600 ${isReadOnly ? 'opacity-50' : ''}"
                    disabled=${disabled || isStreaming || isReadOnly}
                />
                ${isStreaming ? html`
                    <button
                        type="button"
                        onClick=${onCancel}
                        class="bg-red-600 hover:bg-red-700 text-white px-4 py-3 rounded-xl font-medium transition-colors self-end"
                        title="Cancel"
                    >
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                        </svg>
                    </button>
                ` : html`
                    <!-- Split button container -->
                    <div class="relative self-end" ref=${dropupRef}>
                        <!-- Dropup menu -->
                        ${showDropup && hasPrompts && html`
                            <div class="absolute bottom-full right-0 mb-2 w-56 bg-slate-800 border border-slate-600 rounded-xl shadow-lg overflow-hidden z-50">
                                <div class="py-1">
                                    ${predefinedPrompts.map((prompt, idx) => html`
                                        <button
                                            key=${idx}
                                            type="button"
                                            onClick=${() => handlePredefinedPrompt(prompt)}
                                            class="w-full text-left px-4 py-2.5 text-sm text-gray-200 hover:bg-slate-700 transition-colors flex items-center gap-2"
                                        >
                                            <svg class="w-4 h-4 text-blue-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
                                            </svg>
                                            <span class="truncate">${prompt.name}</span>
                                        </button>
                                    `)}
                                </div>
                            </div>
                        `}

                        <!-- Split button -->
                        <div class="flex rounded-xl overflow-hidden">
                            <!-- Primary send button -->
                            <button
                                type="submit"
                                disabled=${disabled || !text.trim() || isReadOnly}
                                class="bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 disabled:cursor-not-allowed text-white px-5 py-3 font-medium transition-colors"
                            >
                                Send
                            </button>
                            <!-- Dropdown toggle (only show if there are prompts) -->
                            ${hasPrompts && html`
                                <button
                                    type="button"
                                    onClick=${() => setShowDropup(!showDropup)}
                                    disabled=${disabled || isReadOnly}
                                    class="bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 disabled:cursor-not-allowed text-white px-2 py-3 border-l border-blue-500 transition-colors"
                                    title="Predefined prompts"
                                >
                                    <svg class="w-4 h-4 transition-transform ${showDropup ? 'rotate-180' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
                                    </svg>
                                </button>
                            `}
                        </div>
                    </div>
                `}
            </div>
        </form>
    `;
}

// =============================================================================
// Rename Dialog Component
// =============================================================================

function RenameDialog({ isOpen, sessionName, onSave, onCancel }) {
    const [name, setName] = useState(sessionName);
    const inputRef = useRef(null);

    useEffect(() => {
        if (isOpen) {
            setName(sessionName);
            setTimeout(() => inputRef.current?.focus(), 100);
        }
    }, [isOpen, sessionName]);

    if (!isOpen) return null;

    const handleSubmit = (e) => {
        e.preventDefault();
        onSave(name.trim() || 'Untitled');
    };

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${onCancel}>
            <div class="bg-mitto-sidebar rounded-xl p-6 w-80 shadow-2xl" onClick=${e => e.stopPropagation()}>
                <h3 class="text-lg font-semibold mb-4">Rename Session</h3>
                <form onSubmit=${handleSubmit}>
                    <input
                        ref=${inputRef}
                        type="text"
                        value=${name}
                        onInput=${e => setName(e.target.value)}
                        class="w-full bg-slate-800 text-white rounded-lg px-4 py-2 mb-4 focus:outline-none focus:ring-2 focus:ring-blue-500"
                        placeholder="Session name"
                    />
                    <div class="flex justify-end gap-2">
                        <button
                            type="button"
                            onClick=${onCancel}
                            class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
                        >
                            Cancel
                        </button>
                        <button
                            type="submit"
                            class="px-4 py-2 bg-blue-600 hover:bg-blue-700 rounded-lg transition-colors"
                        >
                            Save
                        </button>
                    </div>
                </form>
            </div>
        </div>
    `;
}

// =============================================================================
// Delete Confirmation Dialog
// =============================================================================

function DeleteDialog({ isOpen, sessionName, isActive, onConfirm, onCancel }) {
    if (!isOpen) return null;

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${onCancel}>
            <div class="bg-mitto-sidebar rounded-xl p-6 w-80 shadow-2xl" onClick=${e => e.stopPropagation()}>
                <h3 class="text-lg font-semibold mb-2">Delete Session</h3>
                <p class="text-gray-400 text-sm mb-4">
                    Are you sure you want to delete "${sessionName}"?
                    ${isActive && html`<br/><span class="text-yellow-400">This is the active session.</span>`}
                </p>
                <div class="flex justify-end gap-2">
                    <button
                        type="button"
                        onClick=${onCancel}
                        class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
                    >
                        Cancel
                    </button>
                    <button
                        type="button"
                        onClick=${onConfirm}
                        class="px-4 py-2 bg-red-600 hover:bg-red-700 rounded-lg transition-colors"
                    >
                        Delete
                    </button>
                </div>
            </div>
        </div>
    `;
}

// =============================================================================
// Session Item Component
// =============================================================================

function SessionItem({ session, isActive, onSelect, onRename, onDelete }) {
    const [showActions, setShowActions] = useState(false);

    const handleRename = (e) => {
        e.stopPropagation();
        onRename(session);
    };

    const handleDelete = (e) => {
        e.stopPropagation();
        onDelete(session);
    };

    const displayName = session.name || session.description || 'Untitled';
    const isActiveSession = session.isActive || session.status === 'active';

    return html`
        <div
            onClick=${() => onSelect(session.session_id)}
            onMouseEnter=${() => setShowActions(true)}
            onMouseLeave=${() => setShowActions(false)}
            class="p-3 border-b border-slate-700 cursor-pointer hover:bg-slate-700/50 transition-colors ${
                isActive ? 'bg-blue-900/30 border-l-2 border-l-blue-500' : ''
            }"
        >
            <div class="flex items-start justify-between gap-2">
                <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-2">
                        ${isActiveSession && html`
                            <span class="w-2 h-2 bg-green-400 rounded-full flex-shrink-0"></span>
                        `}
                        <span class="text-sm font-medium truncate">${displayName}</span>
                    </div>
                    <div class="text-xs text-gray-500 mt-1">
                        ${new Date(session.created_at).toLocaleDateString()} ${new Date(session.created_at).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'})}
                    </div>
                    <div class="flex items-center gap-2 mt-1">
                        ${session.messageCount !== undefined ? html`
                            <span class="text-xs text-gray-500">${session.messageCount} msgs</span>
                        ` : session.event_count !== undefined ? html`
                            <span class="text-xs text-gray-500">${session.event_count} events</span>
                        ` : null}
                        ${!session.isActive && html`
                            <span class="text-xs px-1.5 py-0.5 rounded bg-slate-700 text-gray-400">stored</span>
                        `}
                    </div>
                </div>
                <div class="flex items-center gap-1 ${showActions ? 'opacity-100' : 'opacity-0'} transition-opacity">
                    <button
                        onClick=${handleRename}
                        class="p-1.5 bg-slate-700 hover:bg-slate-600 rounded transition-colors text-gray-300 hover:text-white"
                        title="Rename"
                    >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
                        </svg>
                    </button>
                    <button
                        onClick=${handleDelete}
                        class="p-1.5 bg-slate-700 hover:bg-red-600 rounded transition-colors text-gray-300 hover:text-white"
                        title="Delete"
                    >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                    </button>
                </div>
            </div>
        </div>
    `;
}

// =============================================================================
// Session List Component (Sidebar)
// =============================================================================

function SessionList({ activeSessions, storedSessions, activeSessionId, onSelect, onNewSession, onRename, onDelete, onClose, theme, onToggleTheme }) {
    // Combine active and stored sessions using shared helper function
    const allSessions = useMemo(
        () => computeAllSessions(activeSessions, storedSessions),
        [activeSessions, storedSessions]
    );

    const isLight = theme === 'light';

    return html`
        <div class="h-full flex flex-col">
            <div class="p-4 border-b border-slate-700 flex items-center justify-between">
                <h2 class="font-semibold text-lg">Sessions</h2>
                <div class="flex items-center gap-2">
                    <button
                        onClick=${() => onNewSession()}
                        class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                        title="New Session"
                    >
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                        </svg>
                    </button>
                    ${onClose && html`
                        <button
                            onClick=${onClose}
                            class="p-2 hover:bg-slate-700 rounded-lg transition-colors md:hidden"
                            title="Close"
                        >
                            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                            </svg>
                        </button>
                    `}
                </div>
            </div>
            <div class="flex-1 overflow-y-auto scrollbar-hide">
                ${allSessions.length === 0 && html`
                    <div class="p-4 text-gray-500 text-sm text-center">
                        No sessions yet
                    </div>
                `}
                ${allSessions.map(session => html`
                    <${SessionItem}
                        key=${session.session_id}
                        session=${session}
                        isActive=${activeSessionId === session.session_id}
                        onSelect=${onSelect}
                        onRename=${onRename}
                        onDelete=${onDelete}
                    />
                `)}
            </div>
            <!-- Footer with theme toggle -->
            <div class="p-4 border-t border-slate-700">
                <div class="flex items-center justify-between">
                    <div class="flex items-center gap-2 text-sm text-gray-400">
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            ${isLight ? html`
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
                            ` : html`
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />
                            `}
                        </svg>
                        <span>${isLight ? 'Light' : 'Dark'}</span>
                    </div>
                    <button
                        onClick=${onToggleTheme}
                        class="theme-toggle ${isLight ? 'light' : ''}"
                        title="Toggle theme"
                        aria-label="Toggle theme"
                    />
                </div>
            </div>
        </div>
    `;
}

// =============================================================================
// Main App Component
// =============================================================================

function App() {
    const {
        connected,
        messages,
        sendPrompt,
        cancelPrompt,
        newSession,
        switchSession,
        updateSessionName,
        renameSession,
        removeSession,
        isStreaming,
        sessionInfo,
        activeSessionId,
        activeSessions,
        storedSessions,
        fetchStoredSessions
    } = useWebSocket();

    const [showSidebar, setShowSidebar] = useState(false);
    const [renameDialog, setRenameDialog] = useState({ isOpen: false, session: null });
    const [deleteDialog, setDeleteDialog] = useState({ isOpen: false, session: null });
    const [predefinedPrompts, setPredefinedPrompts] = useState([]);
    const messagesEndRef = useRef(null);
    const mainContentRef = useRef(null);

    // Compute all sessions for navigation using shared helper function
    const allSessions = useMemo(
        () => computeAllSessions(activeSessions, storedSessions),
        [activeSessions, storedSessions]
    );

    // Navigate to previous/next session
    const navigateToPreviousSession = useCallback(() => {
        if (allSessions.length <= 1) return;
        const currentIndex = allSessions.findIndex(s => s.session_id === activeSessionId);
        if (currentIndex === -1) return;
        const prevIndex = currentIndex === 0 ? allSessions.length - 1 : currentIndex - 1;
        switchSession(allSessions[prevIndex].session_id);
    }, [allSessions, activeSessionId, switchSession]);

    const navigateToNextSession = useCallback(() => {
        if (allSessions.length <= 1) return;
        const currentIndex = allSessions.findIndex(s => s.session_id === activeSessionId);
        if (currentIndex === -1) return;
        const nextIndex = currentIndex === allSessions.length - 1 ? 0 : currentIndex + 1;
        switchSession(allSessions[nextIndex].session_id);
    }, [allSessions, activeSessionId, switchSession]);

    // Enable swipe navigation on mobile
    useSwipeNavigation(mainContentRef, navigateToNextSession, navigateToPreviousSession, {
        threshold: 80,      // Require a decent swipe distance
        maxVertical: 80,    // Allow some vertical movement
        edgeWidth: 40       // Start from edge zone
    });

    // Fetch config on mount to get predefined prompts
    useEffect(() => {
        fetch('/api/config')
            .then(res => res.json())
            .then(config => {
                if (config?.web?.prompts) {
                    setPredefinedPrompts(config.web.prompts);
                }
            })
            .catch(err => console.error('Failed to fetch config:', err));
    }, []);

    // Theme state - default to dark, persist in localStorage
    const [theme, setTheme] = useState(() => {
        if (typeof localStorage !== 'undefined') {
            return localStorage.getItem('mitto-theme') || 'dark';
        }
        return 'dark';
    });

    // Apply theme class to document
    useEffect(() => {
        const root = document.documentElement;
        if (theme === 'light') {
            root.classList.add('light');
            root.classList.remove('dark');
        } else {
            root.classList.add('dark');
            root.classList.remove('light');
        }
        localStorage.setItem('mitto-theme', theme);
    }, [theme]);

    const toggleTheme = useCallback(() => {
        setTheme(prev => prev === 'dark' ? 'light' : 'dark');
    }, []);

    // Auto-scroll to bottom on new messages
    useEffect(() => {
        if (messagesEndRef.current) {
            messagesEndRef.current.scrollIntoView({ behavior: 'smooth' });
        }
    }, [messages]);

    const handleNewSession = () => {
        newSession();
        setShowSidebar(false);
    };

    const handleSelectSession = (sessionId) => {
        switchSession(sessionId);
        setShowSidebar(false);
    };

    const handleRenameSession = (session) => {
        setRenameDialog({ isOpen: true, session });
    };

    const handleSaveRename = (newName) => {
        const session = renameDialog.session;
        if (!session) return;

        // Rename via WebSocket - this persists to storage and broadcasts to all clients
        renameSession(session.session_id, newName);

        setRenameDialog({ isOpen: false, session: null });
    };

    const handleDeleteSession = (session) => {
        setDeleteDialog({ isOpen: true, session });
    };

    const handleConfirmDelete = async () => {
        const session = deleteDialog.session;
        if (!session) return;

        // Remove from local state
        removeSession(session.session_id);

        // Always delete via API - both active and stored sessions have persistent storage
        try {
            await fetch(`/api/sessions/${session.session_id}`, {
                method: 'DELETE'
            });
            fetchStoredSessions();
        } catch (err) {
            console.error('Failed to delete session:', err);
        }

        setDeleteDialog({ isOpen: false, session: null });
    };

    return html`
        <div class="h-screen flex">
            <!-- Rename Dialog -->
            <${RenameDialog}
                isOpen=${renameDialog.isOpen}
                sessionName=${renameDialog.session?.name || renameDialog.session?.description || 'Untitled'}
                onSave=${handleSaveRename}
                onCancel=${() => setRenameDialog({ isOpen: false, session: null })}
            />

            <!-- Delete Dialog -->
            <${DeleteDialog}
                isOpen=${deleteDialog.isOpen}
                sessionName=${deleteDialog.session?.name || deleteDialog.session?.description || 'Untitled'}
                isActive=${deleteDialog.session?.session_id === activeSessionId}
                onConfirm=${handleConfirmDelete}
                onCancel=${() => setDeleteDialog({ isOpen: false, session: null })}
            />

            <!-- Sidebar (hidden on mobile by default) -->
            <div class="hidden md:block w-80 bg-mitto-sidebar border-r border-slate-700 flex-shrink-0">
                <${SessionList}
                    activeSessions=${activeSessions}
                    storedSessions=${storedSessions}
                    activeSessionId=${activeSessionId}
                    onSelect=${handleSelectSession}
                    onNewSession=${handleNewSession}
                    onRename=${handleRenameSession}
                    onDelete=${handleDeleteSession}
                    theme=${theme}
                    onToggleTheme=${toggleTheme}
                />
            </div>

            <!-- Mobile sidebar overlay -->
            ${showSidebar && html`
                <div class="md:hidden fixed inset-0 z-40 flex">
                    <div class="w-80 bg-mitto-sidebar flex-shrink-0 shadow-2xl">
                        <${SessionList}
                            activeSessions=${activeSessions}
                            storedSessions=${storedSessions}
                            activeSessionId=${activeSessionId}
                            onSelect=${handleSelectSession}
                            onNewSession=${handleNewSession}
                            onRename=${handleRenameSession}
                            onDelete=${handleDeleteSession}
                            onClose=${() => setShowSidebar(false)}
                            theme=${theme}
                            onToggleTheme=${toggleTheme}
                        />
                    </div>
                    <div class="flex-1 bg-black/50" onClick=${() => setShowSidebar(false)} />
                </div>
            `}

            <!-- Main chat area (swipe left/right to switch sessions on mobile) -->
            <div ref=${mainContentRef} class="flex-1 flex flex-col min-w-0">
                <!-- Header -->
                <div class="p-4 bg-mitto-sidebar border-b border-slate-700 flex items-center gap-3 flex-shrink-0">
                    <button
                        class="md:hidden p-2 hover:bg-slate-700 rounded-lg transition-colors"
                        onClick=${() => setShowSidebar(true)}
                    >
                        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
                        </svg>
                    </button>
                    <h1 class="font-bold text-xl">Mitto</h1>
                    ${sessionInfo?.name && html`
                        <span class="text-sm text-gray-400 hidden sm:inline truncate max-w-[200px]">
                            · ${sessionInfo.name}
                        </span>
                    `}
                    <div class="ml-auto flex items-center gap-3">
                        ${isStreaming && html`
                            <span class="w-2 h-2 bg-blue-400 rounded-full animate-pulse"></span>
                        `}
                        ${activeSessionId && html`
                            <span class="text-xs text-gray-500 flex items-center gap-1" title="Session is auto-saved">
                                <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4" />
                                </svg>
                                <span class="hidden sm:inline">Saved</span>
                            </span>
                        `}
                        <span class="text-sm flex items-center gap-1.5 ${connected ? 'text-green-400' : 'text-red-400'}">
                            <span class="w-2 h-2 rounded-full ${connected ? 'bg-green-400' : 'bg-red-400'}"></span>
                            <span class="hidden sm:inline">${connected ? 'Connected' : 'Disconnected'}</span>
                        </span>
                    </div>
                </div>

                <!-- Messages -->
                <div class="flex-1 overflow-y-auto scroll-smooth scrollbar-hide p-4">
                    <div class="max-w-4xl mx-auto">
                        ${messages.length === 0 && html`
                            <div class="text-center text-gray-500 mt-20">
                                <div class="text-6xl mb-4">💬</div>
                                <p class="text-xl font-medium">Welcome to Mitto</p>
                                <p class="text-sm mt-2 text-gray-600">Type a message to start chatting with the AI agent</p>
                                ${!connected && html`
                                    <p class="text-sm mt-4 text-yellow-500">Connecting to server...</p>
                                `}
                            </div>
                        `}
                        ${messages.map((msg, i) => html`
                            <${Message}
                                key=${msg.timestamp + '-' + i}
                                message=${msg}
                                isLast=${i === messages.length - 1}
                                isStreaming=${isStreaming}
                            />
                        `)}
                        <div ref=${messagesEndRef} />
                    </div>
                </div>

                <!-- Input -->
                <${ChatInput}
                    onSend=${sendPrompt}
                    onCancel=${cancelPrompt}
                    disabled=${!connected}
                    isStreaming=${isStreaming}
                    isReadOnly=${sessionInfo?.isReadOnly}
                    predefinedPrompts=${predefinedPrompts}
                />
            </div>
        </div>
    `;
}

// =============================================================================
// Swipe Navigation Hook
// =============================================================================

function useSwipeNavigation(ref, onSwipeLeft, onSwipeRight, options = {}) {
    const {
        threshold = 50,       // Minimum distance to trigger swipe
        maxVertical = 100,    // Maximum vertical movement allowed
        edgeWidth = 30        // Width of edge zone where swipe starts (mobile)
    } = options;

    const touchStartRef = useRef(null);

    useEffect(() => {
        const element = ref.current;
        if (!element) return;

        const handleTouchStart = (e) => {
            const touch = e.touches[0];
            // Only start tracking if touch begins near the edge (for mobile)
            const isNearLeftEdge = touch.clientX < edgeWidth;
            const isNearRightEdge = touch.clientX > window.innerWidth - edgeWidth;

            touchStartRef.current = {
                x: touch.clientX,
                y: touch.clientY,
                time: Date.now(),
                isEdgeSwipe: isNearLeftEdge || isNearRightEdge
            };
        };

        const handleTouchEnd = (e) => {
            if (!touchStartRef.current) return;

            const touch = e.changedTouches[0];
            const deltaX = touch.clientX - touchStartRef.current.x;
            const deltaY = touch.clientY - touchStartRef.current.y;
            const duration = Date.now() - touchStartRef.current.time;

            // Only trigger if:
            // - Horizontal movement exceeds threshold
            // - Vertical movement is within limit (not scrolling)
            // - Gesture was reasonably quick (under 500ms)
            // - Started from edge (on mobile) or threshold is met
            if (Math.abs(deltaX) >= threshold &&
                Math.abs(deltaY) <= maxVertical &&
                duration < 500) {

                if (deltaX > 0) {
                    // Swiped right -> go to previous
                    onSwipeRight?.();
                } else {
                    // Swiped left -> go to next
                    onSwipeLeft?.();
                }
            }

            touchStartRef.current = null;
        };

        const handleTouchCancel = () => {
            touchStartRef.current = null;
        };

        element.addEventListener('touchstart', handleTouchStart, { passive: true });
        element.addEventListener('touchend', handleTouchEnd, { passive: true });
        element.addEventListener('touchcancel', handleTouchCancel, { passive: true });

        return () => {
            element.removeEventListener('touchstart', handleTouchStart);
            element.removeEventListener('touchend', handleTouchEnd);
            element.removeEventListener('touchcancel', handleTouchCancel);
        };
    }, [ref, onSwipeLeft, onSwipeRight, threshold, maxVertical, edgeWidth]);
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById('app'));
