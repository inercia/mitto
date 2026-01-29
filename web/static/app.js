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
    INITIAL_EVENTS_LIMIT,
    computeAllSessions,
    convertEventsToMessages,
    safeJsonParse,
    limitMessages,
    getWorkspaceVisualInfo,
    getBasename,
    updateGlobalWorkingDir,
    getGlobalWorkingDir,
    validateUsername,
    validatePassword,
    generatePromptId,
    savePendingPrompt,
    removePendingPrompt,
    getPendingPromptsForSession,
    cleanupExpiredPrompts
} from './lib.js';

// =============================================================================
// External URL Helper
// =============================================================================

// Opens an external URL in the default browser.
// In the native macOS app, uses the bound native function.
// In the web browser, uses window.open.
function openExternalURL(url) {
    if (typeof window.mittoOpenExternalURL === 'function') {
        // Native macOS app - use bound function
        window.mittoOpenExternalURL(url);
    } else {
        // Web browser - open in new tab
        window.open(url, '_blank', 'noopener,noreferrer');
    }
}

// =============================================================================
// Global Link Click Handler
// =============================================================================

// Intercept clicks on external links (http/https) to prevent WebView navigation.
// In the native macOS app, this ensures links open in the default browser
// instead of navigating within the WebView window.
document.addEventListener('click', (e) => {
    // Find the closest anchor element (handles clicks on nested elements inside links)
    const link = e.target.closest('a');
    if (!link) return;

    const href = link.getAttribute('href');
    if (!href) return;

    // Only intercept external URLs (http/https)
    if (href.startsWith('http://') || href.startsWith('https://')) {
        e.preventDefault();
        e.stopPropagation();
        openExternalURL(href);
    }
});

// =============================================================================
// Folder Picker Helper
// =============================================================================

// Check if native folder picker is available (macOS app)
function hasNativeFolderPicker() {
    return typeof window.mittoPickFolder === 'function';
}

// Opens a native folder picker dialog and returns the selected path.
// In the native macOS app, uses the bound native function.
// Returns a Promise that resolves to the selected path or empty string if cancelled.
async function pickFolder() {
    if (typeof window.mittoPickFolder === 'function') {
        // Native macOS app - use bound function
        // The webview binding returns a Promise
        const result = await window.mittoPickFolder();
        return result || '';
    }
    // Web browser - no native folder picker available
    // The caller should use a file input with webkitdirectory as fallback
    return '';
}

// Opens a native image picker dialog and returns the selected file paths.
// In the native macOS app, uses the bound native function.
// Returns a Promise that resolves to an array of file paths or empty array if cancelled.
async function pickImages() {
    if (typeof window.mittoPickImages === 'function') {
        // Native macOS app - use bound function
        // The webview binding returns a Promise
        const result = await window.mittoPickImages();
        return result || [];
    }
    // Web browser - no native image picker available
    // The caller should use a file input as fallback
    return null; // null indicates native picker is not available
}

// Check if the native image picker is available (running in macOS app)
function hasNativeImagePicker() {
    return typeof window.mittoPickImages === 'function';
}

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
// Notification Sound Helper (macOS only)
// =============================================================================

// Audio context for playing notification sounds (created on first use)
let notificationAudioContext = null;

// Get or create the Web Audio context
function getAudioContext() {
    if (!notificationAudioContext) {
        notificationAudioContext = new (window.AudioContext || window.webkitAudioContext)();
    }
    return notificationAudioContext;
}

// Play the agent completed notification sound.
// Uses the native macOS function if available, otherwise falls back to Web Audio API.
function playAgentCompletedSound() {
    // Check if native macOS sound function is available
    if (typeof window.mittoPlayNotificationSound === 'function') {
        window.mittoPlayNotificationSound();
        return;
    }

    // Fall back to Web Audio API - play a pleasant two-tone chime
    try {
        const ctx = getAudioContext();
        const now = ctx.currentTime;

        // First tone (higher pitch)
        const osc1 = ctx.createOscillator();
        const gain1 = ctx.createGain();
        osc1.type = 'sine';
        osc1.frequency.value = 880; // A5
        gain1.gain.setValueAtTime(0.15, now);
        gain1.gain.exponentialRampToValueAtTime(0.01, now + 0.15);
        osc1.connect(gain1);
        gain1.connect(ctx.destination);
        osc1.start(now);
        osc1.stop(now + 0.15);

        // Second tone (slightly lower, played after first)
        const osc2 = ctx.createOscillator();
        const gain2 = ctx.createGain();
        osc2.type = 'sine';
        osc2.frequency.value = 659.25; // E5
        gain2.gain.setValueAtTime(0.15, now + 0.1);
        gain2.gain.exponentialRampToValueAtTime(0.01, now + 0.3);
        osc2.connect(gain2);
        gain2.connect(ctx.destination);
        osc2.start(now + 0.1);
        osc2.stop(now + 0.3);
    } catch (err) {
        console.warn('Failed to play notification sound:', err);
    }
}

// Global ref for agent completed sound setting (used by WebSocket handler)
// This is set by the App component when config is loaded
window.mittoAgentCompletedSoundEnabled = false;

// =============================================================================
// WebSocket Hook with Per-Session WebSocket Support (New Architecture)
// =============================================================================

function useWebSocket() {
    const [eventsConnected, setEventsConnected] = useState(false);

    // Multi-session state: { sessionId: { messages: [], info: {}, lastSeq: 0, isStreaming: false, ws: WebSocket } }
    const [sessions, setSessions] = useState({});
    const [activeSessionId, setActiveSessionId] = useState(null);
    const [storedSessions, setStoredSessions] = useState([]); // Sessions from the store

    // Workspaces state: list of configured workspaces from server
    const [workspaces, setWorkspaces] = useState([]);
    // Available ACP servers from config
    const [acpServers, setAcpServers] = useState([]);

    // Track background session completions for toast notifications
    // { sessionId, sessionName, timestamp }
    const [backgroundCompletion, setBackgroundCompletion] = useState(null);

    const eventsWsRef = useRef(null);
    const reconnectRef = useRef(null);
    const activeSessionIdRef = useRef(activeSessionId);
    const sessionWsRefs = useRef({}); // { sessionId: WebSocket }
    const sessionReconnectRefs = useRef({}); // { sessionId: timeoutId } for session reconnection
    const sessionsRef = useRef(sessions); // For accessing sessions in callbacks
    const workspacesRef = useRef(workspaces); // For accessing workspaces in callbacks
    const retryPendingPromptsRef = useRef(null); // Ref to retry function (set later to avoid circular deps)

    // Track if this is a reconnection (vs initial connection)
    const wasConnectedRef = useRef(false);

    // Fetch workspaces and ACP servers
    const fetchWorkspaces = useCallback(async () => {
        try {
            const response = await fetch('/api/workspaces');
            if (response.ok) {
                const data = await response.json();
                setWorkspaces(data.workspaces || []);
                setAcpServers(data.acp_servers || []);
            }
        } catch (err) {
            console.error('Failed to fetch workspaces:', err);
        }
    }, []);

    // Fetch workspaces on mount
    useEffect(() => {
        fetchWorkspaces();
    }, [fetchWorkspaces]);

    // Add a new workspace
    const addWorkspace = useCallback(async (workingDir, acpServer) => {
        try {
            const response = await fetch('/api/workspaces', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ working_dir: workingDir, acp_server: acpServer })
            });

            if (!response.ok) {
                const errorText = await response.text();
                return { error: errorText };
            }

            const data = await response.json();
            // Refresh workspaces list
            await fetchWorkspaces();
            return { workspace: data };
        } catch (err) {
            console.error('Failed to add workspace:', err);
            return { error: err.message || 'Failed to add workspace' };
        }
    }, [fetchWorkspaces]);

    // Remove a workspace
    const removeWorkspace = useCallback(async (workingDir) => {
        try {
            const response = await fetch(`/api/workspaces?dir=${encodeURIComponent(workingDir)}`, {
                method: 'DELETE'
            });

            if (!response.ok) {
                // Try to parse as JSON for structured errors
                const contentType = response.headers.get('content-type');
                if (contentType && contentType.includes('application/json')) {
                    const errorData = await response.json();
                    const error = new Error(errorData.message || 'Failed to remove workspace');
                    error.code = errorData.error;
                    error.conversationCount = errorData.conversation_count;
                    throw error;
                }
                const errorText = await response.text();
                throw new Error(errorText);
            }

            // Refresh workspaces list
            await fetchWorkspaces();
        } catch (err) {
            console.error('Failed to remove workspace:', err);
            throw err;
        }
    }, [fetchWorkspaces]);

    // Keep refs in sync with state
    const storedSessionsRef = useRef(null);
    // Store working_dir values from API/WebSocket to ensure they're always available
    // Using state instead of ref to trigger re-renders when working_dir is updated
    const [workingDirMap, setWorkingDirMap] = useState({});
    const workingDirMapRef = useRef({});
    useEffect(() => {
        activeSessionIdRef.current = activeSessionId;
        // Persist last active session ID
        setLastActiveSessionId(activeSessionId);
    }, [activeSessionId]);

    useEffect(() => {
        sessionsRef.current = sessions;
    }, [sessions]);

    useEffect(() => {
        workspacesRef.current = workspaces;
    }, [workspaces]);

    useEffect(() => {
        storedSessionsRef.current = storedSessions;
        // Also update workingDirMap from storedSessions
        const updates = {};
        storedSessions.forEach(s => {
            if (s.working_dir) {
                updates[s.session_id] = s.working_dir;
                workingDirMapRef.current[s.session_id] = s.working_dir;
            }
        });
        if (Object.keys(updates).length > 0) {
            setWorkingDirMap(prev => ({ ...prev, ...updates }));
        }
    }, [storedSessions]);

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

    // Get streaming state for active session
    const isStreaming = useMemo(() => {
        if (!activeSessionId || !sessions[activeSessionId]) return false;
        return sessions[activeSessionId].isStreaming || false;
    }, [sessions, activeSessionId]);

    // Check if active session has more messages to load
    const hasMoreMessages = useMemo(() => {
        if (!activeSessionId || !sessions[activeSessionId]) return false;
        return sessions[activeSessionId].hasMoreMessages || false;
    }, [sessions, activeSessionId]);

    // Get all active sessions as array for sidebar
    // Note: Not using useMemo to ensure working_dir is always up-to-date
    const activeSessions = Object.entries(sessions).map(([id, data]) => {
        // Find the most recent user message timestamp
        const userMessages = (data.messages || []).filter(m => m.role === ROLE_USER);
        const lastUserMsgTime = userMessages.length > 0
            ? new Date(Math.max(...userMessages.map(m => m.timestamp || 0))).toISOString()
            : null;
        // Get working_dir from multiple sources (in order of priority):
        // 1. Global map (populated from API responses, most reliable)
        // 2. workingDirMap state (populated from storedSessions and WebSocket connected messages)
        // 3. storedSessions (original API response)
        // 4. session info (set by switchSession or WebSocket connected handler)
        const storedSession = storedSessions.find(s => s.session_id === id);
        const workingDir = getGlobalWorkingDir(id) || workingDirMap[id] || storedSession?.working_dir || data.info?.working_dir || '';
        return {
            session_id: id,
            name: data.info?.name || 'New conversation',
            acp_server: data.info?.acp_server || '',
            working_dir: workingDir,
            created_at: data.info?.created_at || new Date().toISOString(),
            updated_at: data.info?.updated_at || new Date().toISOString(),
            last_user_message_at: lastUserMsgTime || data.info?.last_user_message_at,
            status: 'active',
            isActive: true,
            isStreaming: data.isStreaming || false,
            messageCount: data.messages?.length || 0
        };
    });

    // Connect to per-session WebSocket
    const connectToSession = useCallback((sessionId) => {
        // Clear any pending reconnect timer for this session
        if (sessionReconnectRefs.current[sessionId]) {
            clearTimeout(sessionReconnectRefs.current[sessionId]);
            delete sessionReconnectRefs.current[sessionId];
        }

        // Don't connect if already connected
        if (sessionWsRefs.current[sessionId]) {
            return sessionWsRefs.current[sessionId];
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const ws = new WebSocket(`${protocol}//${window.location.host}/api/sessions/${sessionId}/ws`);

        ws.onopen = () => {
            console.log(`Session WebSocket connected: ${sessionId}`);
            // Retry any pending prompts after a short delay to ensure connection is stable
            setTimeout(() => {
                if (retryPendingPromptsRef.current) {
                    retryPendingPromptsRef.current(sessionId);
                }
            }, 500);
        };

        ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                handleSessionMessage(sessionId, msg);
            } catch (err) {
                console.error('Failed to parse session WebSocket message:', err, event.data);
            }
        };

        ws.onclose = () => {
            console.log(`Session WebSocket closed: ${sessionId}`);
            delete sessionWsRefs.current[sessionId];
            // Clear streaming state for this session
            setSessions(prev => {
                const session = prev[sessionId];
                if (!session) return prev;
                return { ...prev, [sessionId]: { ...session, isStreaming: false } };
            });

            // Reconnect if this session is still active (user hasn't switched away)
            // This handles cases like mobile browser suspension when phone is locked
            if (activeSessionIdRef.current === sessionId) {
                console.log(`Scheduling reconnect for active session: ${sessionId}`);
                sessionReconnectRefs.current[sessionId] = setTimeout(() => {
                    delete sessionReconnectRefs.current[sessionId];
                    // Double-check the session is still active before reconnecting
                    if (activeSessionIdRef.current === sessionId) {
                        console.log(`Reconnecting to session: ${sessionId}`);
                        connectToSession(sessionId);
                    }
                }, 2000);
            }
        };

        ws.onerror = (err) => {
            console.error(`Session WebSocket error: ${sessionId}`, err);
            ws.close();
        };

        sessionWsRefs.current[sessionId] = ws;
        return ws;
    }, []);

    // Handle messages from per-session WebSocket
    const handleSessionMessage = useCallback((sessionId, msg) => {
        switch (msg.type) {
            case 'connected':
                // Session WebSocket connected, update session info
                // Note: working_dir should come from the WebSocket message, but we also
                // preserve any existing value in case of race conditions with switchSession

                // Store working_dir in both ref and state
                if (msg.data.working_dir) {
                    workingDirMapRef.current[sessionId] = msg.data.working_dir;
                    setWorkingDirMap(prev => ({ ...prev, [sessionId]: msg.data.working_dir }));
                }

                setSessions(prev => {
                    const session = prev[sessionId] || { messages: [], info: {} };
                    // Prefer the WebSocket message value, then ref, then existing value
                    const newWorkingDir = msg.data.working_dir ||
                                          workingDirMapRef.current[sessionId] ||
                                          session.info?.working_dir || '';
                    return {
                        ...prev,
                        [sessionId]: {
                            ...session,
                            info: {
                                ...session.info,
                                session_id: sessionId,
                                name: msg.data.name || session.info?.name || 'New conversation',
                                acp_server: msg.data.acp_server || session.info?.acp_server,
                                working_dir: newWorkingDir,
                                created_at: msg.data.created_at || session.info?.created_at,
                                status: msg.data.status || 'active'
                            },
                            isStreaming: msg.data.is_prompting || false
                        }
                    };
                });
                break;

            case 'agent_message':
                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    let messages = [...session.messages];
                    const last = messages[messages.length - 1];
                    if (last && last.role === ROLE_AGENT && !last.complete) {
                        messages[messages.length - 1] = { ...last, html: (last.html || '') + msg.data.html };
                    } else {
                        messages.push({ role: ROLE_AGENT, html: msg.data.html, complete: false, timestamp: Date.now() });
                        messages = limitMessages(messages);
                    }
                    return { ...prev, [sessionId]: { ...session, messages, isStreaming: true } };
                });
                break;

            case 'agent_thought':
                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    let messages = [...session.messages];
                    const last = messages[messages.length - 1];
                    if (last && last.role === ROLE_THOUGHT && !last.complete) {
                        messages[messages.length - 1] = { ...last, text: (last.text || '') + msg.data.text };
                    } else {
                        messages.push({ role: ROLE_THOUGHT, text: msg.data.text, complete: false, timestamp: Date.now() });
                        messages = limitMessages(messages);
                    }
                    // Agent thoughts indicate the agent is still working
                    return { ...prev, [sessionId]: { ...session, messages, isStreaming: true } };
                });
                break;

            case 'tool_call':
                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    const messages = limitMessages([...session.messages, {
                        role: ROLE_TOOL, id: msg.data.id, title: msg.data.title, status: msg.data.status, timestamp: Date.now()
                    }]);
                    // Tool calls indicate the agent is still working
                    return { ...prev, [sessionId]: { ...session, messages, isStreaming: true } };
                });
                break;

            case 'tool_update':
                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    const messages = [...session.messages];
                    const idx = messages.findLastIndex(m => m.role === ROLE_TOOL && m.id === msg.data.id);
                    if (idx >= 0 && msg.data.status) {
                        messages[idx] = { ...messages[idx], status: msg.data.status };
                    }
                    // Tool updates indicate the agent is still working
                    return { ...prev, [sessionId]: { ...session, messages, isStreaming: true } };
                });
                break;

            case 'prompt_complete':
                // Check if this is a background session completing (not the active one)
                const currentSession = sessionsRef.current[sessionId];
                const isBackgroundSession = sessionId !== activeSessionIdRef.current;
                const wasStreaming = currentSession?.isStreaming;

                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    const messages = [...session.messages];
                    const lastIdx = messages.length - 1;
                    if (lastIdx >= 0) {
                        const last = messages[lastIdx];
                        if (last.role === ROLE_AGENT || last.role === ROLE_THOUGHT) {
                            messages[lastIdx] = { ...last, complete: true };
                        }
                    }
                    return { ...prev, [sessionId]: { ...session, messages, isStreaming: false } };
                });

                // Notify about background session completion
                if (isBackgroundSession && wasStreaming) {
                    const sessionName = currentSession?.info?.name || 'Conversation';
                    setBackgroundCompletion({
                        sessionId,
                        sessionName,
                        timestamp: Date.now()
                    });
                }

                // Play notification sound if enabled (macOS only)
                if (wasStreaming && window.mittoAgentCompletedSoundEnabled) {
                    playAgentCompletedSound();
                }
                break;

            case 'error':
                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    const messages = limitMessages([...session.messages, {
                        role: ROLE_ERROR, text: msg.data.message, timestamp: Date.now()
                    }]);
                    return { ...prev, [sessionId]: { ...session, messages, isStreaming: false } };
                });
                break;

            case 'session_renamed':
                setSessions(prev => {
                    const session = prev[sessionId];
                    if (!session) return prev;
                    return {
                        ...prev,
                        [sessionId]: {
                            ...session,
                            info: { ...session.info, name: msg.data.name }
                        }
                    };
                });
                setStoredSessions(prev => prev.map(s =>
                    s.session_id === sessionId ? { ...s, name: msg.data.name } : s
                ));
                break;

            case 'session_sync':
                // Handle incremental sync response
                const events = msg.data.events || [];
                const newMessages = convertEventsToMessages(events);
                const lastSeq = events.length > 0 ? Math.max(...events.map(e => e.seq || 0)) : msg.data.after_seq;
                const isRunning = msg.data.is_running || false;

                setLastSeenSeq(sessionId, lastSeq);

                setSessions(prev => {
                    const session = prev[sessionId] || { messages: [], info: {} };
                    return {
                        ...prev,
                        [sessionId]: {
                            ...session,
                            messages: limitMessages([...session.messages, ...newMessages]),
                            lastSeq,
                            isStreaming: isRunning,
                            info: {
                                ...session.info,
                                name: msg.data.name || session.info?.name,
                                status: msg.data.status || session.info?.status
                            }
                        }
                    };
                });
                break;

            case 'prompt_received':
                // Acknowledgment that the prompt was received and persisted by the server
                // Remove from pending queue - the message is now safely stored
                if (msg.data.prompt_id) {
                    removePendingPrompt(msg.data.prompt_id);
                    console.log('Prompt acknowledged:', msg.data.prompt_id);
                }
                break;

            case 'permission':
                console.log('Permission requested:', msg.data);
                break;
        }
    }, []);

    // Connect to global events WebSocket
    const connectToEvents = useCallback(() => {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const socket = new WebSocket(`${protocol}//${window.location.host}/api/events`);

        socket.onopen = () => {
            setEventsConnected(true);
            const isReconnect = wasConnectedRef.current;
            console.log('Global events WebSocket connected', isReconnect ? '(reconnect)' : '(initial)');

            if (isReconnect) {
                // On reconnect: refresh the session list to catch any changes
                // that occurred while disconnected (e.g., mobile phone locked)
                // but don't switch sessions - keep the user's current session
                console.log('Refreshing session list after reconnect');
                fetchStoredSessions();
            } else {
                // Initial connection: fetch stored sessions and resume last session
                fetchStoredSessions().then((storedSessionsList) => {
                    const lastSessionId = getLastActiveSessionId();
                    if (lastSessionId) {
                        // Connect to the last session from localStorage
                        switchSession(lastSessionId);
                    } else if (storedSessionsList && storedSessionsList.length > 0) {
                        // No last session in localStorage, but there are stored sessions
                        // Switch to the most recent one (first in the list, sorted by updated_at desc)
                        const mostRecentSession = storedSessionsList[0];
                        switchSession(mostRecentSession.session_id);
                    }
                    // No stored sessions - show empty state, let user create manually
                });
            }
        };

        socket.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                handleGlobalEvent(msg);
            } catch (err) {
                console.error('Failed to parse global events message:', err, event.data);
            }
        };

        socket.onclose = () => {
            if (eventsWsRef.current) {
                wasConnectedRef.current = true;
            }
            setEventsConnected(false);
            eventsWsRef.current = null;
            // Reconnect after delay
            reconnectRef.current = setTimeout(connectToEvents, 2000);
        };

        socket.onerror = (err) => {
            console.error('Global events WebSocket error:', err);
            socket.close();
        };

        eventsWsRef.current = socket;
    }, []);

    // Handle global events (session lifecycle)
    const handleGlobalEvent = useCallback((msg) => {
        switch (msg.type) {
            case 'connected':
                // Global events WS connected
                console.log('Global events ready');
                break;

            case 'session_created':
                // A new session was created (possibly by another client)
                setStoredSessions(prev => {
                    const exists = prev.find(s => s.session_id === msg.data.session_id);
                    if (exists) return prev;
                    return [{
                        session_id: msg.data.session_id,
                        name: msg.data.name || 'New conversation',
                        acp_server: msg.data.acp_server,
                        working_dir: msg.data.working_dir,
                        status: 'active',
                        created_at: new Date().toISOString()
                    }, ...prev];
                });
                break;

            case 'session_renamed':
                // Update session name in stored sessions
                setStoredSessions(prev => prev.map(s =>
                    s.session_id === msg.data.session_id ? { ...s, name: msg.data.name } : s
                ));
                // Also update in active sessions
                setSessions(prev => {
                    const session = prev[msg.data.session_id];
                    if (!session) return prev;
                    return {
                        ...prev,
                        [msg.data.session_id]: {
                            ...session,
                            info: { ...session.info, name: msg.data.name }
                        }
                    };
                });
                break;

            case 'session_deleted':
                const deletedId = msg.data.session_id;
                setStoredSessions(prev => prev.filter(s => s.session_id !== deletedId));
                const currentId = activeSessionIdRef.current;
                setSessions(prev => {
                    const { [deletedId]: removed, ...rest } = prev;
                    if (deletedId === currentId) {
                        const remainingIds = Object.keys(rest);
                        if (remainingIds.length > 0) {
                            setActiveSessionId(remainingIds[0]);
                        } else {
                            // Don't create a new session here - let the user do it manually
                            // or let the initiating window handle it. This prevents multiple
                            // windows from all creating new sessions simultaneously.
                            setActiveSessionId(null);
                        }
                    }
                    return rest;
                });
                // Cancel any pending reconnect for this session
                if (sessionReconnectRefs.current[deletedId]) {
                    clearTimeout(sessionReconnectRefs.current[deletedId]);
                    delete sessionReconnectRefs.current[deletedId];
                }
                // Close the session WebSocket
                if (sessionWsRefs.current[deletedId]) {
                    sessionWsRefs.current[deletedId].close();
                    delete sessionWsRefs.current[deletedId];
                }
                break;
        }
    }, []);

    // Create a new session via REST API
    // Options: { name?: string, workingDir?: string, acpServer?: string }
    // Returns: { sessionId: string } on success, { error: string, errorCode?: string } on failure, or null on network error
    const createNewSession = useCallback(async (options = {}) => {
        try {
            // Support both old (name string) and new (options object) signatures
            const opts = typeof options === 'string' ? { name: options } : options;

            const response = await fetch('/api/sessions', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    name: opts.name || '',
                    working_dir: opts.workingDir || '',
                    acp_server: opts.acpServer || ''
                })
            });

            if (!response.ok) {
                // Try to parse as JSON for structured error
                const contentType = response.headers.get('content-type');
                if (contentType && contentType.includes('application/json')) {
                    const errorData = await response.json();
                    console.error('Failed to create session:', errorData);
                    return { error: errorData.message || 'Failed to create session', errorCode: errorData.error };
                }
                const error = await response.text();
                console.error('Failed to create session:', error);
                return { error: error || 'Failed to create session' };
            }

            const data = await response.json();
            const sessionId = data.session_id;

            // Build system message with workspace info
            let systemMsg = `Start chatting with ${data.acp_server}`;
            if (data.working_dir) {
                systemMsg += ` to work on ${data.working_dir}`;
            }

            // Initialize session state
            setSessions(prev => ({
                ...prev,
                [sessionId]: {
                    messages: [{
                        role: ROLE_SYSTEM,
                        text: systemMsg,
                        timestamp: Date.now()
                    }],
                    info: {
                        session_id: sessionId,
                        name: data.name || 'New conversation',
                        acp_server: data.acp_server,
                        working_dir: data.working_dir,
                        status: 'active'
                    },
                    isStreaming: false
                }
            }));

            // Connect to the session WebSocket
            connectToSession(sessionId);
            setActiveSessionId(sessionId);

            return { sessionId };
        } catch (err) {
            console.error('Failed to create session:', err);
            return { error: err.message || 'Network error' };
        }
    }, [connectToSession]);

    // Switch to an existing session
    const switchSession = useCallback(async (sessionId) => {
        // Use sessionsRef to get current sessions state and avoid stale closures
        const currentSessions = sessionsRef.current;
        // Check if session already has messages loaded (not just an empty placeholder from WebSocket)
        const existingSession = currentSessions[sessionId];
        const hasLoadedMessages = existingSession && existingSession.messages && existingSession.messages.length > 0;
        const hasWorkingDir = existingSession?.info?.working_dir;

        if (hasLoadedMessages && hasWorkingDir) {
            // Session already has messages and working_dir, just set it active
            setActiveSessionId(sessionId);
            return;
        }

        // Load session events from API (with limit for faster initial load)
        try {
            // Get session metadata first to know total event count and working_dir
            const metaResponse = await fetch(`/api/sessions/${sessionId}`);
            const meta = metaResponse.ok ? await metaResponse.json() : {};
            const totalEvents = meta.event_count || 0;

            // If we already have messages, just update the info with working_dir
            if (hasLoadedMessages) {
                // Store working_dir in both ref and state
                if (meta.working_dir) {
                    workingDirMapRef.current[sessionId] = meta.working_dir;
                    setWorkingDirMap(prev => ({ ...prev, [sessionId]: meta.working_dir }));
                }
                setSessions(prev => {
                    const existing = prev[sessionId] || {};
                    return {
                        ...prev,
                        [sessionId]: {
                            ...existing,
                            info: {
                                ...existing.info,
                                working_dir: meta.working_dir
                            }
                        }
                    };
                });
                setActiveSessionId(sessionId);
                return;
            }

            // Load only the last N events initially
            const eventsResponse = await fetch(`/api/sessions/${sessionId}/events?limit=${INITIAL_EVENTS_LIMIT}`);
            if (!eventsResponse.ok) {
                console.error('Failed to load session events');
                return;
            }
            const events = await eventsResponse.json();

            // Convert events to messages
            const messages = convertEventsToMessages(events);

            // Determine if there are more messages to load
            const firstSeq = events.length > 0 ? events[0].seq : 0;
            const hasMoreMessages = firstSeq > 1;

            // Store working_dir in both ref and state
            console.log('[switchSession] meta.working_dir:', meta.working_dir);
            if (meta.working_dir) {
                workingDirMapRef.current[sessionId] = meta.working_dir;
                setWorkingDirMap(prev => {
                    console.log('[switchSession] setWorkingDirMap called with:', sessionId, meta.working_dir);
                    return { ...prev, [sessionId]: meta.working_dir };
                });
            }

            // Initialize session state (merge with any existing state from WebSocket)
            setSessions(prev => {
                const existing = prev[sessionId] || {};
                return {
                    ...prev,
                    [sessionId]: {
                        ...existing,
                        messages,
                        info: {
                            ...existing.info,
                            session_id: sessionId,
                            name: meta.name || 'Conversation',
                            acp_server: meta.acp_server,
                            working_dir: meta.working_dir,
                            created_at: meta.created_at,
                            status: meta.status || 'active'
                        },
                        isStreaming: existing.isStreaming || false,
                        hasMoreMessages,
                        firstLoadedSeq: firstSeq
                    }
                };
            });

            // Connect to the session WebSocket (if not already connected)
            connectToSession(sessionId);
            setActiveSessionId(sessionId);

        } catch (err) {
            console.error('Failed to switch session:', err);
        }
    }, [connectToSession]);

    // Helper functions for session state updates
    const addMessageToSession = useCallback((sessionId, message) => {
        setSessions(prev => {
            const session = prev[sessionId];
            if (!session) return prev;
            const messages = limitMessages([...session.messages, message]);
            return { ...prev, [sessionId]: { ...session, messages } };
        });
    }, []);

    const updateLastMessage = useCallback((sessionId, updater) => {
        setSessions(prev => {
            const session = prev[sessionId];
            if (!session || session.messages.length === 0) return prev;
            const messages = [...session.messages];
            messages[messages.length - 1] = updater(messages[messages.length - 1]);
            return { ...prev, [sessionId]: { ...session, messages } };
        });
    }, []);

    // Send message to the current session's WebSocket
    const sendToSession = useCallback((sessionId, msg) => {
        const ws = sessionWsRefs.current[sessionId];
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify(msg));
            return true;
        }
        return false;
    }, []);

    const sendPrompt = useCallback((message, images = []) => {
        if (!activeSessionId) return;
        // Add user message with optional images
        const userMessage = { role: ROLE_USER, text: message, timestamp: Date.now() };
        if (images.length > 0) {
            userMessage.images = images; // Array of { id, url, name, mimeType }
        }
        addMessageToSession(activeSessionId, userMessage);
        // Mark any previous streaming message as complete
        updateLastMessage(activeSessionId, m =>
            !m.complete && (m.role === ROLE_AGENT || m.role === ROLE_THOUGHT) ? { ...m, complete: true } : m
        );
        // Generate a unique prompt ID for delivery tracking
        const promptId = generatePromptId();
        const imageIds = images.map(img => img.id);

        // Save to pending queue BEFORE sending (for mobile reliability)
        savePendingPrompt(activeSessionId, promptId, message, imageIds);

        // Send prompt with prompt_id for acknowledgment
        sendToSession(activeSessionId, { type: 'prompt', data: { message, image_ids: imageIds, prompt_id: promptId } });
    }, [activeSessionId, addMessageToSession, updateLastMessage, sendToSession]);

    const cancelPrompt = useCallback(() => {
        if (!activeSessionId) return;
        sendToSession(activeSessionId, { type: 'cancel' });
    }, [activeSessionId, sendToSession]);

    // Retry pending prompts for a session (called on reconnect or visibility change)
    const retryPendingPrompts = useCallback((sessionId) => {
        const pending = getPendingPromptsForSession(sessionId);
        if (pending.length === 0) return;

        console.log(`Retrying ${pending.length} pending prompt(s) for session ${sessionId}`);

        for (const { promptId, message, imageIds } of pending) {
            const sent = sendToSession(sessionId, {
                type: 'prompt',
                data: { message, image_ids: imageIds || [], prompt_id: promptId }
            });
            if (sent) {
                console.log(`Retried pending prompt: ${promptId}`);
            } else {
                console.warn(`Failed to retry pending prompt (WebSocket not ready): ${promptId}`);
                // Stop retrying if WebSocket is not ready - will retry on next reconnect
                break;
            }
        }
    }, [sendToSession]);

    // Keep the ref in sync with the callback
    useEffect(() => {
        retryPendingPromptsRef.current = retryPendingPrompts;
    }, [retryPendingPrompts]);

    const newSession = useCallback(async (options) => {
        return await createNewSession(options);
    }, [createNewSession]);

    const loadSession = useCallback(async (sessionId) => {
        // Use sessionsRef to get current sessions state and avoid stale closures
        const currentSessions = sessionsRef.current;
        // If session is already loaded in memory, just switch to it
        if (currentSessions[sessionId]) {
            setActiveSessionId(sessionId);
            return;
        }
        // Load session for read-only viewing
        await switchSession(sessionId);
    }, [switchSession]);

    // Load more (older) messages for a session
    const loadMoreMessages = useCallback(async (sessionId) => {
        // Use sessionsRef to get current sessions state and avoid stale closures
        const currentSessions = sessionsRef.current;
        const session = currentSessions[sessionId];
        if (!session || !session.hasMoreMessages || !session.firstLoadedSeq) {
            return;
        }

        try {
            // Load events before the first currently loaded sequence
            const eventsResponse = await fetch(
                `/api/sessions/${sessionId}/events?limit=${INITIAL_EVENTS_LIMIT}&before=${session.firstLoadedSeq}`
            );
            if (!eventsResponse.ok) {
                console.error('Failed to load more events');
                return;
            }
            const events = await eventsResponse.json();

            if (events.length === 0) {
                // No more events to load
                setSessions(prev => ({
                    ...prev,
                    [sessionId]: { ...prev[sessionId], hasMoreMessages: false }
                }));
                return;
            }

            // Convert events to messages
            const olderMessages = convertEventsToMessages(events);

            // Determine if there are still more messages
            const newFirstSeq = events.length > 0 ? events[0].seq : 0;
            const hasMoreMessages = newFirstSeq > 1;

            // Prepend older messages to existing messages
            setSessions(prev => {
                const currentSession = prev[sessionId];
                if (!currentSession) return prev;
                return {
                    ...prev,
                    [sessionId]: {
                        ...currentSession,
                        messages: [...olderMessages, ...currentSession.messages],
                        hasMoreMessages,
                        firstLoadedSeq: newFirstSeq
                    }
                };
            });
        } catch (err) {
            console.error('Failed to load more messages:', err);
        }
    }, []);

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

    // Rename a session via REST API
    const renameSession = useCallback(async (sessionId, name) => {
        try {
            const response = await fetch(`/api/sessions/${sessionId}`, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name })
            });
            if (!response.ok) {
                console.error('Failed to rename session');
                return;
            }
            // Update local state
            updateSessionName(sessionId, name);
            // Update stored sessions
            setStoredSessions(prev => prev.map(s =>
                s.session_id === sessionId ? { ...s, name } : s
            ));
        } catch (err) {
            console.error('Failed to rename session:', err);
        }
    }, [updateSessionName]);

    // Fetch stored sessions
    const fetchStoredSessions = useCallback(async () => {
        try {
            const res = await fetch('/api/sessions');
            const data = await res.json();
            // Update global working_dir map for each session
            (data || []).forEach(s => {
                if (s.session_id && s.working_dir) {
                    updateGlobalWorkingDir(s.session_id, s.working_dir);
                }
            });
            setStoredSessions(data || []);
            return data || [];
        } catch (err) {
            console.error('Failed to fetch sessions:', err);
            return [];
        }
    }, []);

    const removeSession = useCallback(async (sessionId) => {
        const currentActiveSessionId = activeSessionIdRef.current;
        const wasActiveSession = sessionId === currentActiveSessionId;

        // Cancel any pending reconnect for this session
        if (sessionReconnectRefs.current[sessionId]) {
            clearTimeout(sessionReconnectRefs.current[sessionId]);
            delete sessionReconnectRefs.current[sessionId];
        }
        // Close the session WebSocket
        if (sessionWsRefs.current[sessionId]) {
            sessionWsRefs.current[sessionId].close();
            delete sessionWsRefs.current[sessionId];
        }

        // Remove from local state
        setSessions(prev => {
            const { [sessionId]: removed, ...rest } = prev;
            return rest;
        });

        // Delete from server first
        try {
            await fetch(`/api/sessions/${sessionId}`, { method: 'DELETE' });
        } catch (err) {
            console.error('Failed to delete session:', err);
        }

        // If we removed the active session, switch to another or set to null
        if (wasActiveSession) {
            // Fetch remaining sessions from server to get accurate list
            const remainingSessions = await fetchStoredSessions();
            if (remainingSessions && remainingSessions.length > 0) {
                // Switch to the most recent remaining session
                const nextSession = remainingSessions[0];
                switchSession(nextSession.session_id);
            } else {
                // No sessions left - show empty state, let user create manually
                setActiveSessionId(null);
            }
        }
    }, [createNewSession, fetchStoredSessions, switchSession]);

    // Initialize on mount
    useEffect(() => {
        connectToEvents();
        return () => {
            if (reconnectRef.current) clearTimeout(reconnectRef.current);
            if (eventsWsRef.current) eventsWsRef.current.close();
            // Clear all session reconnect timers
            for (const timerId of Object.values(sessionReconnectRefs.current)) {
                clearTimeout(timerId);
            }
            sessionReconnectRefs.current = {};
            // Close all session WebSockets
            for (const ws of Object.values(sessionWsRefs.current)) {
                ws.close();
            }
        };
    }, [connectToEvents]);

    // Refresh session list and retry pending prompts when app becomes visible
    useEffect(() => {
        const handleVisibilityChange = () => {
            if (document.visibilityState === 'visible') {
                console.log('App became visible, refreshing session list and checking pending prompts');

                // Clean up expired prompts first
                cleanupExpiredPrompts();

                // Fetch stored sessions
                fetchStoredSessions();

                // Retry pending prompts for the active session after a short delay
                // (to allow WebSocket to reconnect if needed)
                const currentSessionId = activeSessionIdRef.current;
                if (currentSessionId) {
                    setTimeout(() => {
                        retryPendingPrompts(currentSessionId);
                    }, 1000);
                }
            }
        };

        document.addEventListener('visibilitychange', handleVisibilityChange);
        return () => {
            document.removeEventListener('visibilitychange', handleVisibilityChange);
        };
    }, [fetchStoredSessions, retryPendingPrompts]);

    // Clear background completion notification
    const clearBackgroundCompletion = useCallback(() => {
        setBackgroundCompletion(null);
    }, []);

    return {
        connected: eventsConnected,
        messages,
        sendPrompt,
        cancelPrompt,
        newSession,
        switchSession,
        loadSession,
        loadMoreMessages,
        updateSessionName,
        renameSession,
        removeSession,
        isStreaming,
        hasMoreMessages,
        sessionInfo,
        activeSessionId,
        activeSessions,
        storedSessions,
        fetchStoredSessions,
        backgroundCompletion,
        clearBackgroundCompletion,
        workspaces,
        acpServers,
        addWorkspace,
        removeWorkspace,
        refreshWorkspaces: fetchWorkspaces
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
            <div class="message-enter flex justify-center mb-1">
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

    // User message (plain text with optional images)
    if (isUser) {
        const hasImages = message.images && message.images.length > 0;
        return html`
            <div class="message-enter flex justify-end mb-3">
                <div class="max-w-[95%] md:max-w-[75%] px-4 py-2 rounded-2xl bg-mitto-user text-mitto-user-text border border-mitto-user-border rounded-br-sm">
                    ${hasImages && html`
                        <div class="flex flex-wrap gap-2 mb-2">
                            ${message.images.map(img => html`
                                <div key=${img.id} class="relative group">
                                    <img
                                        src=${img.url}
                                        alt=${img.name || 'Attached image'}
                                        class="max-w-[200px] max-h-[150px] rounded-lg object-cover cursor-pointer hover:opacity-90 transition-opacity"
                                        onClick=${() => window.open(img.url, '_blank')}
                                    />
                                </div>
                            `)}
                        </div>
                    `}
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
                <div class="max-w-[95%] md:max-w-[75%] px-4 py-3 rounded-2xl bg-mitto-agent text-gray-100 rounded-bl-sm">
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

function ChatInput({ onSend, onCancel, disabled, isStreaming, isReadOnly, predefinedPrompts = [], inputRef, noSession = false, sessionId, draft = '', onDraftChange, sessionDraftsRef }) {
    // Use the draft from parent state instead of local state
    const text = draft;
    const setText = useCallback((newText) => {
        if (onDraftChange) {
            onDraftChange(sessionId, newText);
        }
    }, [onDraftChange, sessionId]);

    const [showDropup, setShowDropup] = useState(false);
    // Track ongoing prompt improvements: { targetSessionId, abortController }
    const [improvingState, setImprovingState] = useState(null);
    const [improveError, setImproveError] = useState(null);
    const textareaRef = useRef(null);
    const dropupRef = useRef(null);

    // Image upload state
    const [pendingImages, setPendingImages] = useState([]); // Array of { id, url, name, mimeType, uploading }
    const [isDragOver, setIsDragOver] = useState(false);
    const [uploadError, setUploadError] = useState(null);
    const fileInputRef = useRef(null);

    // Track window width for responsive placeholder
    const [isSmallWindow, setIsSmallWindow] = useState(window.innerWidth < 640);
    useEffect(() => {
        const handleResize = () => setIsSmallWindow(window.innerWidth < 640);
        window.addEventListener('resize', handleResize);
        return () => window.removeEventListener('resize', handleResize);
    }, []);

    // Clear pending images when session changes
    useEffect(() => {
        setPendingImages([]);
        setUploadError(null);
    }, [sessionId]);

    // For backwards compatibility
    const isImproving = !!improvingState;

    // Determine if input should be fully disabled (no session or explicitly disabled)
    const isFullyDisabled = disabled || noSession;

    // Expose focus method via inputRef for native menu integration
    useEffect(() => {
        if (inputRef) {
            inputRef.current = {
                focus: () => {
                    if (textareaRef.current) {
                        textareaRef.current.focus();
                    }
                }
            };
        }
    }, [inputRef]);

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

    // Adjust textarea height when draft changes (e.g., switching sessions)
    useEffect(() => {
        const textarea = textareaRef.current;
        if (textarea) {
            textarea.style.height = 'auto';
            textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
        }
    }, [text]);

    const handleSubmit = (e) => {
        e.preventDefault();
        // Allow sending if there's text OR images (or both)
        const hasContent = text.trim() || pendingImages.some(img => !img.uploading);
        if (hasContent && !disabled && !isReadOnly && !isStreaming) {
            // Filter out images that are still uploading
            const readyImages = pendingImages.filter(img => !img.uploading);
            onSend(text.trim(), readyImages);
            setText('');
            setPendingImages([]);
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
        // Ctrl+P to improve prompt (magic wand)
        if (e.ctrlKey && e.key === 'p') {
            e.preventDefault();
            handleImprovePrompt();
        }
    };

    const handleInput = (e) => {
        setText(e.target.value);
        const textarea = e.target;
        textarea.style.height = 'auto';
        textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
    };

    const handlePredefinedPrompt = (prompt) => {
        const textarea = textareaRef.current;
        if (textarea) {
            // Get cursor position and insert prompt text at that position
            const start = textarea.selectionStart;
            const end = textarea.selectionEnd;
            const newText = text.substring(0, start) + prompt.prompt + text.substring(end);
            setText(newText);

            // Close dropdown and focus textarea
            setShowDropup(false);

            // Set cursor position after inserted text and adjust textarea height
            requestAnimationFrame(() => {
                const newCursorPos = start + prompt.prompt.length;
                textarea.selectionStart = newCursorPos;
                textarea.selectionEnd = newCursorPos;
                textarea.focus();
                // Adjust height to fit content
                textarea.style.height = 'auto';
                textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
            });
        } else {
            // Fallback: just set the text
            setText(prompt.prompt);
            setShowDropup(false);
        }
    };

    const handleImprovePrompt = async () => {
        if (!text.trim() || isImproving) return;

        // Capture the current sessionId - this is the session the improvement is for
        const targetSessionId = sessionId;
        const controller = new AbortController();

        setImprovingState({ targetSessionId, abortController: controller });
        setImproveError(null);

        try {
            const timeoutId = setTimeout(() => controller.abort(), 65000); // 65s timeout (slightly more than backend's 60s)

            const response = await fetch('/api/aux/improve-prompt', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ prompt: text }),
                signal: controller.signal,
            });

            clearTimeout(timeoutId);

            if (!response.ok) {
                const errorText = await response.text();
                throw new Error(errorText || 'Failed to improve prompt');
            }

            const data = await response.json();
            if (data.improved_prompt && onDraftChange) {
                // Update the draft for the SESSION THAT STARTED the improvement
                // not the currently active session
                onDraftChange(targetSessionId, data.improved_prompt);
                // Only adjust textarea if we're still on the same session
                if (targetSessionId === sessionId) {
                    requestAnimationFrame(() => {
                        const textarea = textareaRef.current;
                        if (textarea) {
                            textarea.style.height = 'auto';
                            textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px';
                            textarea.focus();
                        }
                    });
                }
            }
        } catch (err) {
            console.error('Failed to improve prompt:', err);
            if (err.name === 'AbortError') {
                setImproveError('Request timed out. Please try again.');
            } else {
                setImproveError(err.message || 'Failed to improve prompt');
            }
            // Clear error after 5 seconds
            setTimeout(() => setImproveError(null), 5000);
        } finally {
            setImprovingState(null);
        }
    };

    const getPlaceholder = () => {
        if (noSession) return "Create a new conversation to start chatting...";
        if (isReadOnly) return "This is a read-only session. Create a new session to chat.";
        if (isStreaming) return "Agent is responding...";
        if (isImproving) return "Improving prompt...";
        if (isDragOver) return "Drop image here...";
        return isSmallWindow ? "Type your message..." : "Type your message... (drop or paste images)";
    };

    // Upload an image file to the session
    const uploadImage = async (file) => {
        if (!sessionId) return null;

        // Validate file type
        const validTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
        if (!validTypes.includes(file.type)) {
            setUploadError('Only PNG, JPEG, GIF, and WebP images are supported');
            setTimeout(() => setUploadError(null), 5000);
            return null;
        }

        // Validate file size (10MB)
        if (file.size > 10 * 1024 * 1024) {
            setUploadError('Image exceeds 10MB limit');
            setTimeout(() => setUploadError(null), 5000);
            return null;
        }

        // Create a temporary preview
        const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
        const previewUrl = URL.createObjectURL(file);
        const tempImage = { id: tempId, url: previewUrl, name: file.name, mimeType: file.type, uploading: true };
        setPendingImages(prev => [...prev, tempImage]);

        try {
            const formData = new FormData();
            formData.append('image', file);

            const response = await fetch(`/api/sessions/${sessionId}/images`, {
                method: 'POST',
                body: formData,
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.message || 'Failed to upload image');
            }

            const data = await response.json();

            // Replace temp image with uploaded image
            setPendingImages(prev => prev.map(img =>
                img.id === tempId
                    ? { id: data.id, url: data.url, name: data.name, mimeType: data.mime_type, uploading: false }
                    : img
            ));

            // Revoke the temporary blob URL
            URL.revokeObjectURL(previewUrl);

            return data;
        } catch (err) {
            console.error('Failed to upload image:', err);
            setUploadError(err.message || 'Failed to upload image');
            setTimeout(() => setUploadError(null), 5000);
            // Remove the temp image
            setPendingImages(prev => prev.filter(img => img.id !== tempId));
            URL.revokeObjectURL(previewUrl);
            return null;
        }
    };

    // Upload images from file paths (for native macOS app)
    const uploadImagesFromPaths = async (paths) => {
        if (!sessionId || !paths || paths.length === 0) return [];

        // Create temporary placeholders for each path
        const tempImages = paths.map(path => {
            const filename = path.split('/').pop() || 'image';
            const tempId = `temp_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;
            return { id: tempId, filename, path };
        });

        // Add placeholders to pending images (without preview URL since we don't have the image data)
        tempImages.forEach(({ id, filename }) => {
            setPendingImages(prev => [...prev, { id, url: '', name: filename, mimeType: '', uploading: true }]);
        });

        try {
            const response = await fetch(`/api/sessions/${sessionId}/images/from-path`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ paths }),
            });

            if (!response.ok) {
                const error = await response.json();
                throw new Error(error.message || 'Failed to upload images');
            }

            const results = await response.json();

            // Remove all temp placeholders
            const tempIds = tempImages.map(t => t.id);
            setPendingImages(prev => prev.filter(img => !tempIds.includes(img.id)));

            // Add uploaded images
            for (const data of results) {
                setPendingImages(prev => [...prev, {
                    id: data.id,
                    url: data.url,
                    name: data.name,
                    mimeType: data.mime_type,
                    uploading: false
                }]);
            }

            return results;
        } catch (err) {
            console.error('Failed to upload images from paths:', err);
            setUploadError(err.message || 'Failed to upload images');
            setTimeout(() => setUploadError(null), 5000);
            // Remove temp placeholders
            const tempIds = tempImages.map(t => t.id);
            setPendingImages(prev => prev.filter(img => !tempIds.includes(img.id)));
            return [];
        }
    };

    // Handle attach button click - uses native picker on macOS, file input otherwise
    const handleAttachClick = async () => {
        if (hasNativeImagePicker()) {
            // Use native macOS image picker
            const paths = await pickImages();
            if (paths && paths.length > 0) {
                await uploadImagesFromPaths(paths);
            }
        } else {
            // Fall back to file input
            if (fileInputRef.current) {
                fileInputRef.current.click();
            }
        }
    };

    // Handle file drop
    const handleDrop = async (e) => {
        e.preventDefault();
        setIsDragOver(false);

        if (isFullyDisabled || isReadOnly || !sessionId) return;

        const files = Array.from(e.dataTransfer.files);
        const imageFiles = files.filter(f => f.type.startsWith('image/'));

        for (const file of imageFiles) {
            await uploadImage(file);
        }
    };

    const handleDragOver = (e) => {
        e.preventDefault();
        if (!isFullyDisabled && !isReadOnly && sessionId) {
            setIsDragOver(true);
        }
    };

    const handleDragLeave = (e) => {
        e.preventDefault();
        setIsDragOver(false);
    };

    // Handle paste (for clipboard images)
    const handlePaste = async (e) => {
        if (isFullyDisabled || isReadOnly || !sessionId) return;

        const items = Array.from(e.clipboardData.items);
        const imageItems = items.filter(item => item.type.startsWith('image/'));

        if (imageItems.length > 0) {
            e.preventDefault(); // Prevent pasting image as text
            for (const item of imageItems) {
                const file = item.getAsFile();
                if (file) {
                    await uploadImage(file);
                }
            }
        }
    };

    // Remove a pending image
    const removeImage = (imageId) => {
        setPendingImages(prev => {
            const img = prev.find(i => i.id === imageId);
            if (img && img.url.startsWith('blob:')) {
                URL.revokeObjectURL(img.url);
            }
            return prev.filter(i => i.id !== imageId);
        });
    };

    // Handle file input change
    const handleFileInputChange = async (e) => {
        const files = Array.from(e.target.files);
        for (const file of files) {
            await uploadImage(file);
        }
        // Reset input so the same file can be selected again
        e.target.value = '';
    };

    const hasPrompts = predefinedPrompts && predefinedPrompts.length > 0;
    const hasPendingImages = pendingImages.length > 0;

    return html`
        <form
            onSubmit=${handleSubmit}
            onDrop=${handleDrop}
            onDragOver=${handleDragOver}
            onDragLeave=${handleDragLeave}
            class="p-4 bg-mitto-input border-t border-slate-700 ${isDragOver ? 'ring-2 ring-blue-500 ring-inset' : ''}"
        >
            <!-- Hidden file input for image picker -->
            <input
                ref=${fileInputRef}
                type="file"
                accept="image/png,image/jpeg,image/gif,image/webp"
                multiple
                class="hidden"
                onChange=${handleFileInputChange}
            />

            <!-- Image preview area -->
            ${hasPendingImages && html`
                <div class="max-w-4xl mx-auto mb-3">
                    <div class="flex flex-wrap gap-2">
                        ${pendingImages.map(img => html`
                            <div key=${img.id} class="relative group">
                                <img
                                    src=${img.url}
                                    alt=${img.name || 'Pending image'}
                                    class="w-16 h-16 rounded-lg object-cover border border-slate-600 ${img.uploading ? 'opacity-50' : ''}"
                                />
                                ${img.uploading ? html`
                                    <div class="absolute inset-0 flex items-center justify-center">
                                        <svg class="w-5 h-5 text-white animate-spin" fill="none" viewBox="0 0 24 24">
                                            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                                            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                                        </svg>
                                    </div>
                                ` : html`
                                    <button
                                        type="button"
                                        onClick=${() => removeImage(img.id)}
                                        class="absolute -top-1 -right-1 w-5 h-5 bg-red-600 hover:bg-red-700 rounded-full flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                                        title="Remove image"
                                    >
                                        <svg class="w-3 h-3 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                                        </svg>
                                    </button>
                                `}
                            </div>
                        `)}
                    </div>
                </div>
            `}

            <div class="flex gap-2 max-w-4xl mx-auto">
                <!-- Textarea column -->
                <textarea
                    ref=${textareaRef}
                    value=${text}
                    onInput=${handleInput}
                    onKeyDown=${handleKeyDown}
                    onPaste=${handlePaste}
                    placeholder=${getPlaceholder()}
                    rows="2"
                    class="flex-1 bg-mitto-input-box text-white rounded-xl px-4 py-3 resize-none focus:outline-none focus:ring-2 focus:ring-blue-500 max-h-[200px] placeholder-gray-400 placeholder:text-sm border border-slate-600 ${isFullyDisabled || isReadOnly || isImproving ? 'opacity-50 cursor-not-allowed' : ''}"
                    disabled=${isFullyDisabled || isReadOnly || isImproving}
                />
                <!-- Right column: stacked buttons -->
                <div class="relative flex flex-col gap-1.5" ref=${dropupRef}>
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

                    <!-- Top row: Send/Stop button (full width of right column) -->
                    <div class="flex rounded-xl overflow-hidden flex-1">
                        ${isStreaming ? html`
                            <button
                                type="button"
                                onClick=${onCancel}
                                class="w-full bg-red-600 hover:bg-red-700 text-white px-4 py-2 font-medium transition-colors flex items-center justify-center gap-2"
                            >
                                <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                                    <rect x="6" y="6" width="12" height="12" rx="2" />
                                </svg>
                                <span>Stop</span>
                            </button>
                        ` : html`
                            <button
                                type="submit"
                                disabled=${isFullyDisabled || (!text.trim() && !hasPendingImages) || isReadOnly}
                                class="flex-1 bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 disabled:opacity-50 disabled:cursor-not-allowed text-white px-4 py-2 font-medium transition-colors flex items-center justify-center gap-1.5"
                            >
                                <span>Send</span>
                                <span class="text-blue-300 text-xs hidden sm:inline">⌘↵</span>
                            </button>
                        `}
                        <!-- Dropdown toggle (only show if there are prompts and not streaming) -->
                        ${hasPrompts && !isStreaming && html`
                            <button
                                type="button"
                                onClick=${() => setShowDropup(!showDropup)}
                                disabled=${isFullyDisabled || isReadOnly || isStreaming}
                                class="bg-blue-600 hover:bg-blue-700 disabled:bg-slate-700 disabled:cursor-not-allowed text-white px-2 py-2 border-l border-blue-500 transition-colors"
                                title="Insert predefined prompt"
                            >
                                <svg class="w-4 h-4 transition-transform ${showDropup ? 'rotate-180' : ''}" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
                                </svg>
                            </button>
                        `}
                    </div>

                    <!-- Bottom row: Image + Magic Wand buttons side by side -->
                    <div class="flex gap-1.5 flex-1">
                        <!-- Image attach button -->
                        <button
                            type="button"
                            onClick=${handleAttachClick}
                            disabled=${isFullyDisabled || isReadOnly || isStreaming}
                            class="flex-1 bg-slate-700 hover:bg-slate-600 disabled:bg-slate-800 disabled:cursor-not-allowed text-white px-3 py-2 rounded-xl transition-colors flex items-center justify-center"
                            title="Attach image"
                        >
                            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                            </svg>
                        </button>

                        <!-- Magic wand button -->
                        <button
                            type="button"
                            onClick=${handleImprovePrompt}
                            disabled=${isFullyDisabled || !text.trim() || isReadOnly || isStreaming || isImproving}
                            class="flex-1 bg-slate-700 hover:bg-slate-600 disabled:bg-slate-800 disabled:opacity-50 disabled:cursor-not-allowed text-white px-3 py-2 rounded-xl transition-colors flex items-center justify-center"
                            title="Improve prompt with AI"
                        >
                            ${isImproving ? html`
                                <svg class="w-5 h-5 animate-spin" fill="none" viewBox="0 0 24 24">
                                    <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                                    <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                                </svg>
                            ` : html`
                                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" />
                                </svg>
                            `}
                        </button>
                    </div>
                </div>
            </div>
            <!-- Error toast for improve prompt -->
            ${improveError && html`
                <div class="max-w-4xl mx-auto mt-2">
                    <div class="bg-red-900/50 border border-red-700 text-red-200 px-4 py-2 rounded-lg text-sm flex items-center gap-2">
                        <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                        </svg>
                        <span>${improveError}</span>
                        <button
                            type="button"
                            onClick=${() => setImproveError(null)}
                            class="ml-auto text-red-300 hover:text-red-100"
                        >
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                            </svg>
                        </button>
                    </div>
                </div>
            `}
            <!-- Error toast for image upload -->
            ${uploadError && html`
                <div class="max-w-4xl mx-auto mt-2">
                    <div class="bg-red-900/50 border border-red-700 text-red-200 px-4 py-2 rounded-lg text-sm flex items-center gap-2">
                        <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
                        </svg>
                        <span>${uploadError}</span>
                        <button
                            type="button"
                            onClick=${() => setUploadError(null)}
                            class="ml-auto text-red-300 hover:text-red-100"
                        >
                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                            </svg>
                        </button>
                    </div>
                </div>
            `}
        </form>
    `;
}

// =============================================================================
// Workspace Badge Component
// =============================================================================

/**
 * A colored badge showing a three-letter abbreviation for a workspace.
 * The color is deterministically generated from the workspace path,
 * or uses a custom color if provided.
 *
 * @param {string} path - The workspace directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} size - Size variant: 'sm', 'md', 'lg' (default: 'md')
 * @param {boolean} showPath - Whether to show the full path below the badge
 */
function WorkspaceBadge({ path, customColor, size = 'md', showPath = false, className = '' }) {
    if (!path) return null;

    const { abbreviation, color, basename } = getWorkspaceVisualInfo(path, customColor);

    const sizeClasses = {
        sm: 'w-8 h-8 text-xs',
        md: 'w-10 h-10 text-sm',
        lg: 'w-12 h-12 text-base'
    };

    return html`
        <div class="flex items-center gap-3 ${className}">
            <div
                class="flex items-center justify-center rounded-lg font-bold ${sizeClasses[size] || sizeClasses.md}"
                style=${{
                    backgroundColor: color.background,
                    color: color.text
                }}
                title=${path}
            >
                ${abbreviation}
            </div>
            ${showPath && html`
                <div class="min-w-0 flex-1">
                    <div class="font-medium text-sm">${basename}</div>
                    <div class="text-xs text-gray-500 truncate" title=${path}>${path}</div>
                </div>
            `}
        </div>
    `;
}

/**
 * A pill-shaped workspace badge for compact display.
 * Shows abbreviation and truncated workspace name.
 *
 * @param {string} path - The workspace directory path
 * @param {string} customColor - Optional custom hex color (e.g., "#ff5500")
 * @param {string} className - Additional CSS classes
 */
function WorkspacePill({ path, customColor, className = '' }) {
    if (!path) return null;

    const { abbreviation, color, basename } = getWorkspaceVisualInfo(path, customColor);

    return html`
        <div
            class="workspace-pill inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium ${className}"
            style=${{
                backgroundColor: color.background,
                color: color.text
            }}
            title=${path}
        >
            <span class="font-bold">${abbreviation}</span>
            <span class="truncate max-w-[80px]">${basename}</span>
        </div>
    `;
}

// =============================================================================
// Session Properties Dialog Component
// =============================================================================

function SessionPropertiesDialog({ isOpen, session, onSave, onCancel, workspaces = [] }) {
    const [name, setName] = useState('');
    const inputRef = useRef(null);

    const sessionName = session?.name || session?.description || 'Untitled';
    const workingDir = session?.working_dir || session?.info?.working_dir || '';
    const acpServer = session?.acp_server || session?.info?.acp_server || '';
    // Look up workspace color
    const workspace = workspaces.find(ws => ws.working_dir === workingDir);
    const workspaceColor = workspace?.color || null;

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
            <div class="bg-mitto-sidebar rounded-xl p-6 w-96 shadow-2xl" onClick=${e => e.stopPropagation()}>
                <h3 class="text-lg font-semibold mb-4">Session Properties</h3>
                <form onSubmit=${handleSubmit}>
                    <!-- Session Name (editable) -->
                    <div class="mb-4">
                        <label class="block text-sm text-gray-400 mb-1">Name</label>
                        <input
                            ref=${inputRef}
                            type="text"
                            value=${name}
                            onInput=${e => setName(e.target.value)}
                            class="w-full bg-slate-800 text-white rounded-lg px-4 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500"
                            placeholder="Session name"
                        />
                    </div>

                    <!-- Workspace Info (read-only) -->
                    ${(workingDir || acpServer) && html`
                        <div class="mb-4 p-3 bg-slate-800/50 rounded-lg border border-slate-700">
                            <div class="text-xs text-gray-500 uppercase tracking-wide mb-3">Workspace</div>
                            ${workingDir && html`
                                <${WorkspaceBadge} path=${workingDir} customColor=${workspaceColor} size="md" showPath=${true} className="mb-3" />
                            `}
                            ${acpServer && html`
                                <div class="flex items-center gap-2 ${workingDir ? 'ml-13 pl-0.5' : ''}">
                                    <svg class="w-4 h-4 text-gray-400 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
                                    </svg>
                                    <span class="px-2 py-1 bg-blue-500/20 text-blue-400 rounded text-xs">
                                        ${acpServer}
                                    </span>
                                </div>
                            `}
                        </div>
                    `}

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

function DeleteDialog({ isOpen, sessionName, isActive, isStreaming, onConfirm, onCancel }) {
    if (!isOpen) return null;

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${onCancel}>
            <div class="bg-mitto-sidebar rounded-xl p-6 w-80 shadow-2xl" onClick=${e => e.stopPropagation()}>
                <h3 class="text-lg font-semibold mb-2">Delete Session</h3>
                <p class="text-gray-400 text-sm mb-4">
                    Are you sure you want to delete "${sessionName}"?
                    ${isStreaming && html`<br/><span class="text-orange-400">⚠️ This session is still receiving a response.</span>`}
                    ${isActive && !isStreaming && html`<br/><span class="text-yellow-400">This is the active session.</span>`}
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
// Clean Inactive Sessions Confirmation Dialog
// =============================================================================

function CleanInactiveDialog({ isOpen, inactiveCount, onConfirm, onCancel }) {
    if (!isOpen) return null;

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${onCancel}>
            <div class="bg-mitto-sidebar rounded-xl p-6 w-80 shadow-2xl" onClick=${e => e.stopPropagation()}>
                <h3 class="text-lg font-semibold mb-2">Clean Inactive Conversations</h3>
                <p class="text-gray-400 text-sm mb-4">
                    ${inactiveCount === 0
                        ? 'There are no inactive conversations to clean.'
                        : html`Are you sure you want to delete <span class="text-white font-medium">${inactiveCount}</span> inactive conversation${inactiveCount === 1 ? '' : 's'}?
                            <br/><span class="text-gray-500 text-xs mt-2 block">Only stored conversations without an active ACP connection will be removed.</span>`
                    }
                </p>
                <div class="flex justify-end gap-2">
                    <button
                        type="button"
                        onClick=${onCancel}
                        class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
                    >
                        ${inactiveCount === 0 ? 'Close' : 'Cancel'}
                    </button>
                    ${inactiveCount > 0 && html`
                        <button
                            type="button"
                            onClick=${onConfirm}
                            class="px-4 py-2 bg-red-600 hover:bg-red-700 rounded-lg transition-colors"
                        >
                            Clean All
                        </button>
                    `}
                </div>
            </div>
        </div>
    `;
}

// =============================================================================
// Keyboard Shortcuts Dialog
// =============================================================================

// Define keyboard shortcuts in a central location for easy maintenance
// Note: Some shortcuts only work in the native macOS app (handled by native menu)
const KEYBOARD_SHORTCUTS = [
    // Global hotkey (works even when app is not focused - macOS app only)
    { keys: '⌘⌃M', description: 'Show/hide window', macOnly: true, section: 'Global' },
    // File menu shortcuts (native menu in macOS app, not available in browser)
    { keys: '⌘N', description: 'New conversation', macOnly: true, section: 'Conversations' },
    { keys: '⌘W', description: 'Close conversation', macOnly: true, section: 'Conversations' },
    // Web shortcuts (work in both macOS app and browser)
    { keys: '⌘1-9', description: 'Switch to conversation 1-9', section: 'Conversations' },
    { keys: '⌘⌃↑', description: 'Previous conversation', macOnly: true, section: 'Conversations' },
    { keys: '⌘⌃↓', description: 'Next conversation', macOnly: true, section: 'Conversations' },
    { keys: '⌘,', description: 'Settings', section: 'Navigation' },
    // View menu shortcuts (native menu in macOS app, not available in browser)
    { keys: '⌘L', description: 'Focus input', macOnly: true, section: 'Navigation' },
    { keys: '⌘⇧S', description: 'Toggle sidebar', macOnly: true, section: 'Navigation' },
    // Input shortcuts (work in both macOS app and browser)
    { keys: '⌃P', description: 'Improve prompt (magic wand)', section: 'Input' },
];

function KeyboardShortcutsDialog({ isOpen, onClose }) {
    if (!isOpen) return null;

    // Check if running in the native macOS app
    const isMacApp = typeof window.mittoPickFolder === 'function';

    // Handle Escape key to close dialog
    useEffect(() => {
        const handleKeyDown = (e) => {
            if (e.key === 'Escape') {
                onClose();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [onClose]);

    // Filter shortcuts based on environment and group by section
    // In browser (not macOS app), hide macOnly shortcuts since they're handled by native menu
    const sections = {};
    KEYBOARD_SHORTCUTS.forEach(shortcut => {
        // Skip macOnly shortcuts when not in the macOS app
        if (shortcut.macOnly && !isMacApp) {
            return;
        }
        const section = shortcut.section || 'General';
        if (!sections[section]) {
            sections[section] = [];
        }
        sections[section].push(shortcut);
    });

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${onClose}>
            <div class="bg-mitto-sidebar rounded-xl p-6 w-[420px] md:w-[700px] shadow-2xl max-h-[80vh] overflow-y-auto" onClick=${e => e.stopPropagation()}>
                <div class="flex items-center justify-between mb-4">
                    <h3 class="text-lg font-semibold">Keyboard Shortcuts</h3>
                    <button
                        onClick=${onClose}
                        class="p-1 hover:bg-slate-700 rounded-lg transition-colors"
                        title="Close"
                    >
                        <svg class="w-5 h-5 text-gray-400 hover:text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                        </svg>
                    </button>
                </div>
                <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
                    ${Object.entries(sections).map(([sectionName, shortcuts]) => html`
                        <div key=${sectionName}>
                            <h4 class="text-xs font-medium text-gray-400 uppercase tracking-wide mb-2">${sectionName}</h4>
                            <div class="space-y-1">
                                ${shortcuts.map(shortcut => html`
                                    <div key=${shortcut.keys} class="flex items-center justify-between py-2 px-3 rounded-lg bg-slate-700/30">
                                        <div class="flex items-center gap-2">
                                            <span class="text-gray-300">${shortcut.description}</span>
                                            ${shortcut.macOnly && html`
                                                <span class="text-[10px] px-1.5 py-0.5 rounded bg-slate-600 text-gray-400">macOS app</span>
                                            `}
                                        </div>
                                        <kbd class="px-2 py-1 text-sm font-mono bg-slate-700 rounded border border-slate-600 text-gray-200">
                                            ${shortcut.keys}
                                        </kbd>
                                    </div>
                                `)}
                            </div>
                        </div>
                    `)}
                </div>
                <div class="mt-4 pt-3 border-t border-slate-700">
                    <p class="text-xs text-gray-500 text-center">
                        Press Escape to close
                    </p>
                </div>
            </div>
        </div>
    `;
}

// =============================================================================
// Workspace Selection Dialog
// =============================================================================

function WorkspaceDialog({ isOpen, workspaces, onSelect, onCancel }) {
    // Sort workspaces alphabetically by working_dir for deterministic ordering
    const sortedWorkspaces = useMemo(() => {
        return [...workspaces].sort((a, b) => a.working_dir.localeCompare(b.working_dir));
    }, [workspaces]);

    // Handle keyboard shortcuts (1-9) to select workspaces
    useEffect(() => {
        if (!isOpen) return;

        const handleKeyDown = (e) => {
            // Check for number keys 1-9
            const key = e.key;
            if (key >= '1' && key <= '9') {
                const index = parseInt(key, 10) - 1;
                if (index < sortedWorkspaces.length) {
                    e.preventDefault();
                    onSelect(sortedWorkspaces[index]);
                }
            }
            // Escape to cancel
            if (key === 'Escape') {
                e.preventDefault();
                onCancel();
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [isOpen, sortedWorkspaces, onSelect, onCancel]);

    if (!isOpen) return null;

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${onCancel}>
            <div class="bg-mitto-sidebar rounded-xl p-6 w-[420px] max-h-[80vh] overflow-y-auto shadow-2xl" onClick=${e => e.stopPropagation()}>
                <h3 class="text-lg font-semibold mb-2">Select Workspace</h3>
                <p class="text-gray-400 text-sm mb-4">
                    Click on a workspace or press its number to select it.
                </p>
                <div class="space-y-2">
                    ${sortedWorkspaces.map((ws, index) => html`
                        <button
                            key=${ws.working_dir}
                            onClick=${() => onSelect(ws)}
                            class="w-full p-4 text-left rounded-lg bg-slate-700/50 hover:bg-slate-700 transition-colors flex items-center gap-4"
                        >
                            <div class="w-8 h-8 flex items-center justify-center rounded-lg bg-slate-600 text-gray-300 font-mono text-sm flex-shrink-0">
                                ${index + 1}
                            </div>
                            <${WorkspaceBadge} path=${ws.working_dir} customColor=${ws.color} size="lg" />
                            <div class="flex-1 min-w-0">
                                <div class="font-medium">${getBasename(ws.working_dir)}</div>
                                <div class="text-xs text-gray-500 truncate" title=${ws.working_dir}>
                                    ${ws.working_dir}
                                </div>
                                <div class="text-xs text-blue-400 mt-1">
                                    ${ws.acp_server}
                                </div>
                            </div>
                        </button>
                    `)}
                </div>
                <div class="flex justify-end mt-4">
                    <button
                        type="button"
                        onClick=${onCancel}
                        class="px-4 py-2 rounded-lg hover:bg-slate-700 transition-colors"
                    >
                        Cancel
                    </button>
                </div>
            </div>
        </div>
    `;
}

// =============================================================================
// Settings Dialog Component
// =============================================================================

function SettingsDialog({ isOpen, onClose, onSave, forceOpen = false }) {
    const [activeTab, setActiveTab] = useState('servers');
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState('');
    const [success, setSuccess] = useState('');

    // Configuration state
    const [workspaces, setWorkspaces] = useState([]);
    const [acpServers, setAcpServers] = useState([]);
    const [authEnabled, setAuthEnabled] = useState(false);
    const [authUsername, setAuthUsername] = useState('');
    const [authPassword, setAuthPassword] = useState('');
    const [externalPort, setExternalPort] = useState(''); // Empty string = random port
    const [currentExternalPort, setCurrentExternalPort] = useState(null); // Currently running external port
    const [externalEnabled, setExternalEnabled] = useState(false); // Is external listener currently running
    const [hookUpCommand, setHookUpCommand] = useState('');
    const [hookDownCommand, setHookDownCommand] = useState('');

    // Stored sessions for checking workspace usage
    const [storedSessions, setStoredSessions] = useState([]);

    // Form state for adding new items
    const [showAddWorkspace, setShowAddWorkspace] = useState(false);
    const [newWorkspacePath, setNewWorkspacePath] = useState('');
    const [newWorkspaceServer, setNewWorkspaceServer] = useState('');

    // Handle browse button click for workspace directory (native macOS app only)
    const handleBrowseFolder = async () => {
        const path = await pickFolder();
        if (path) {
            setNewWorkspacePath(path);
        }
    };

    const [showAddServer, setShowAddServer] = useState(false);
    const [newServerName, setNewServerName] = useState('');
    const [newServerCommand, setNewServerCommand] = useState('');

    const [editingServer, setEditingServer] = useState(null);

    // Prompts state
    const [prompts, setPrompts] = useState([]);
    const [showAddPrompt, setShowAddPrompt] = useState(false);
    const [newPromptName, setNewPromptName] = useState('');
    const [newPromptText, setNewPromptText] = useState('');
    const [editingPrompt, setEditingPrompt] = useState(null);

    // UI settings state (macOS only)
    const [agentCompletedSound, setAgentCompletedSound] = useState(false);
    const [showInAllSpaces, setShowInAllSpaces] = useState(false);

    // Confirmation settings (all platforms)
    const [confirmDeleteSession, setConfirmDeleteSession] = useState(true);

    // Check if running in the native macOS app
    const isMacApp = typeof window.mittoPickFolder === 'function';

    // Load configuration when dialog opens
    useEffect(() => {
        if (isOpen) {
            // Clear any previous messages when dialog opens
            setError('');
            setSuccess('');
            loadConfig();
            loadStoredSessions();
        }
    }, [isOpen]);

    // Load stored sessions to check workspace usage
    const loadStoredSessions = async () => {
        try {
            const res = await fetch('/api/sessions');
            if (res.ok) {
                const sessions = await res.json();
                setStoredSessions(sessions || []);
            }
        } catch (err) {
            console.error('Failed to load stored sessions:', err);
        }
    };

    // Count conversations using a specific workspace
    const getWorkspaceConversationCount = (workingDir) => {
        return storedSessions.filter(s => s.working_dir === workingDir).length;
    };

    const loadConfig = async () => {
        setLoading(true);
        setError('');
        try {
            // Fetch config and external status in parallel
            const [configRes, externalStatusRes] = await Promise.all([
                fetch('/api/config'),
                fetch('/api/external-status')
            ]);
            const config = await configRes.json();

            // Load external status
            if (externalStatusRes.ok) {
                const externalStatus = await externalStatusRes.json();
                setExternalEnabled(externalStatus.enabled);
                setCurrentExternalPort(externalStatus.port || null);
            }

            // Load ACP servers first (needed for workspace validation)
            const servers = config.acp_servers || [];
            setAcpServers(servers);

            // Filter out invalid workspaces:
            // - Must have a non-empty working_dir
            // - Must reference an existing ACP server
            const serverNames = new Set(servers.map(s => s.name));
            const rawWorkspaces = config.workspaces || [];
            const validWorkspaces = rawWorkspaces.filter(ws => {
                // Check for valid working_dir
                if (!ws.working_dir || typeof ws.working_dir !== 'string' || ws.working_dir.trim() === '') {
                    console.warn('Filtering out workspace with invalid working_dir:', ws);
                    return false;
                }
                // Check for valid ACP server reference
                if (!ws.acp_server || !serverNames.has(ws.acp_server)) {
                    console.warn('Filtering out workspace with invalid/missing ACP server:', ws);
                    return false;
                }
                return true;
            });
            setWorkspaces(validWorkspaces);

            // Load auth settings - check if external access is enabled
            // External access is enabled if auth is configured OR host is 0.0.0.0
            const hasAuth = config.web?.auth?.simple;
            const isExternalHost = config.web?.host === '0.0.0.0';
            if (hasAuth || isExternalHost) {
                setAuthEnabled(true);
                setAuthUsername(config.web?.auth?.simple?.username || '');
                setAuthPassword(config.web?.auth?.simple?.password || '');
            } else {
                setAuthEnabled(false);
                setAuthUsername('');
                setAuthPassword('');
            }

            // Load external port setting (0 or empty = random)
            const extPort = config.web?.external_port;
            setExternalPort(extPort && extPort > 0 ? String(extPort) : '');

            // Load hook settings
            setHookUpCommand(config.web?.hooks?.up?.command || '');
            setHookDownCommand(config.web?.hooks?.down?.command || '');

            // Load prompts
            setPrompts(config.web?.prompts || []);

            // Load UI settings (macOS only)
            setAgentCompletedSound(config.ui?.mac?.notifications?.sounds?.agent_completed || false);
            setShowInAllSpaces(config.ui?.mac?.show_in_all_spaces || false);

            // Load confirmation settings (all platforms, default to true)
            setConfirmDeleteSession(config.ui?.confirmations?.delete_session !== false);

            // Set default server for new workspace
            if (servers.length > 0) {
                setNewWorkspaceServer(servers[0].name);
            }
        } catch (err) {
            setError('Failed to load configuration: ' + err.message);
        } finally {
            setLoading(false);
        }
    };

    const handleSave = async () => {
        setError('');
        setSuccess('');

        // Validation
        if (workspaces.length === 0) {
            setError('At least one workspace is required');
            setActiveTab('workspaces');
            return;
        }

        if (acpServers.length === 0) {
            setError('At least one ACP server is required');
            setActiveTab('servers');
            return;
        }

        if (authEnabled) {
            const usernameError = validateUsername(authUsername);
            if (usernameError) {
                setError(usernameError);
                setActiveTab('web');
                return;
            }
            const passwordError = validatePassword(authPassword);
            if (passwordError) {
                setError(passwordError);
                setActiveTab('web');
                return;
            }
        }

        setSaving(true);
        try {
            // Build web config
            const webConfig = {
                // Set host based on external access setting
                host: authEnabled ? '0.0.0.0' : '127.0.0.1',
                // External port: parse as int, 0 or empty means random
                external_port: externalPort ? parseInt(externalPort, 10) : 0,
                auth: authEnabled ? {
                    simple: {
                        username: authUsername.trim(),
                        password: authPassword.trim()
                    }
                } : null
            };

            // Add hooks if configured
            if (hookUpCommand.trim() || hookDownCommand.trim()) {
                webConfig.hooks = {};
                if (hookUpCommand.trim()) {
                    webConfig.hooks.up = { command: hookUpCommand.trim() };
                }
                if (hookDownCommand.trim()) {
                    webConfig.hooks.down = { command: hookDownCommand.trim() };
                }
            }

            // Add prompts
            webConfig.prompts = prompts;

            // Build UI config
            const uiConfig = {
                // Confirmations (all platforms)
                confirmations: {
                    delete_session: confirmDeleteSession
                }
            };

            // Add macOS-specific settings
            if (isMacApp) {
                uiConfig.mac = {
                    notifications: {
                        sounds: {
                            agent_completed: agentCompletedSound
                        }
                    },
                    show_in_all_spaces: showInAllSpaces
                };
            }

            const config = {
                workspaces: workspaces,
                acp_servers: acpServers,
                web: webConfig,
                ui: uiConfig
            };

            const res = await fetch('/api/config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(config)
            });

            const result = await res.json();

            if (!res.ok) {
                throw new Error(result.error || 'Failed to save configuration');
            }

            // Update the global sound setting flag
            if (isMacApp) {
                window.mittoAgentCompletedSoundEnabled = agentCompletedSound;
            }

            // Fetch updated external status to get the actual running port
            let actualExternalPort = null;
            let externalAccessActive = false;
            try {
                const statusRes = await fetch('/api/external-status');
                if (statusRes.ok) {
                    const status = await statusRes.json();
                    externalAccessActive = status.enabled;
                    actualExternalPort = status.port;
                    setExternalEnabled(status.enabled);
                    setCurrentExternalPort(status.port || null);
                }
            } catch (e) {
                console.error('Failed to fetch external status:', e);
            }

            // Build success message based on what was applied
            let successMsg = 'Configuration saved successfully';
            if (externalAccessActive && actualExternalPort) {
                successMsg = `Configuration saved. External access on port ${actualExternalPort}`;
            } else if (result.applied) {
                const details = [];
                if (result.applied.external_access_enabled) {
                    details.push('external access enabled');
                }
                if (result.applied.auth_enabled) {
                    details.push('authentication active');
                }
                if (details.length > 0) {
                    successMsg += ` (${details.join(', ')})`;
                }
            }
            setSuccess(successMsg);
            onSave?.();

            // Always close dialog after short delay
            setTimeout(() => onClose?.(), 500);
        } catch (err) {
            setError(err.message);
        } finally {
            setSaving(false);
        }
    };

    const handleClose = () => {
        // Always require at least one ACP server
        if (acpServers.length === 0) {
            setError('At least one ACP server is required');
            setActiveTab('servers');
            return;
        }
        // Always require at least one workspace
        if (workspaces.length === 0) {
            setError('At least one workspace is required');
            setActiveTab('workspaces');
            return;
        }
        onClose?.();
    };

    // Workspace management
    const addWorkspace = () => {
        if (!newWorkspacePath.trim()) {
            setError('Please enter a directory path');
            return;
        }
        if (!newWorkspaceServer) {
            setError('Please select an ACP server');
            return;
        }

        // Find the ACP command for this server
        const server = acpServers.find(s => s.name === newWorkspaceServer);
        if (!server) {
            setError('Selected ACP server not found');
            return;
        }

        // Check for duplicate
        if (workspaces.some(ws => ws.working_dir === newWorkspacePath.trim())) {
            setError('A workspace with this path already exists');
            return;
        }

        setWorkspaces([...workspaces, {
            working_dir: newWorkspacePath.trim(),
            acp_server: newWorkspaceServer,
            acp_command: server.command
        }]);
        setNewWorkspacePath('');
        setShowAddWorkspace(false);
        setError('');
    };

    const removeWorkspace = (workingDir) => {
        if (workspaces.length <= 1) {
            setError('At least one workspace is required');
            return;
        }

        // Check if any conversations are using this workspace
        const conversationCount = getWorkspaceConversationCount(workingDir);
        if (conversationCount > 0) {
            setError(`Cannot remove workspace: ${conversationCount} conversation(s) are using it. Delete the conversations first.`);
            return;
        }

        setWorkspaces(workspaces.filter(ws => ws.working_dir !== workingDir));
    };

    // Update workspace color
    const updateWorkspaceColor = (workingDir, color) => {
        setWorkspaces(workspaces.map(ws =>
            ws.working_dir === workingDir
                ? { ...ws, color: color || undefined }  // undefined to omit from JSON if empty
                : ws
        ));
    };

    // ACP Server management
    const addServer = () => {
        if (!newServerName.trim()) {
            setError('Please enter a server name');
            return;
        }
        if (!newServerCommand.trim()) {
            setError('Please enter a server command');
            return;
        }
        if (acpServers.some(s => s.name === newServerName.trim())) {
            setError('A server with this name already exists');
            return;
        }

        setAcpServers([...acpServers, {
            name: newServerName.trim(),
            command: newServerCommand.trim()
        }]);
        setNewServerName('');
        setNewServerCommand('');
        setShowAddServer(false);
        setError('');
    };

    const updateServer = (oldName, newName, newCommand, prompts = []) => {
        if (!newName.trim() || !newCommand.trim()) {
            setError('Server name and command cannot be empty');
            return;
        }

        // Check for duplicate name (excluding current)
        if (newName !== oldName && acpServers.some(s => s.name === newName.trim())) {
            setError('A server with this name already exists');
            return;
        }

        // Update server (including prompts)
        setAcpServers(acpServers.map(s =>
            s.name === oldName ? { name: newName.trim(), command: newCommand.trim(), prompts } : s
        ));

        // Update workspaces that reference this server
        if (newName !== oldName) {
            setWorkspaces(workspaces.map(ws =>
                ws.acp_server === oldName ? { ...ws, acp_server: newName.trim() } : ws
            ));
        }

        setEditingServer(null);
        setError('');
    };

    const removeServer = (serverName) => {
        // Check if any workspace uses this server
        const usedBy = workspaces.filter(ws => ws.acp_server === serverName);
        if (usedBy.length > 0) {
            // Build a helpful error message listing the workspaces using this server
            const workspacePaths = usedBy.map(ws => ws.working_dir).slice(0, 3); // Show up to 3
            const pathList = workspacePaths.join(', ');
            const moreCount = usedBy.length - workspacePaths.length;
            const moreText = moreCount > 0 ? ` and ${moreCount} more` : '';
            setError(`Cannot delete "${serverName}": used by workspace(s): ${pathList}${moreText}. Remove or reassign these workspaces first.`);
            setActiveTab('workspaces'); // Switch to workspaces tab to help user fix the issue
            return;
        }

        if (acpServers.length <= 1) {
            setError('At least one ACP server is required');
            return;
        }

        setAcpServers(acpServers.filter(s => s.name !== serverName));
        setError(''); // Clear any previous errors
    };

    if (!isOpen) return null;

    // Can close if we have both ACP servers and workspaces configured
    const canClose = acpServers.length > 0 && workspaces.length > 0;

    return html`
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick=${canClose ? handleClose : null}>
            <div class="bg-mitto-sidebar rounded-xl w-[600px] max-w-[95vw] max-h-[90vh] overflow-hidden shadow-2xl flex flex-col" onClick=${e => e.stopPropagation()}>
                <!-- Header -->
                <div class="flex items-center justify-between p-4 border-b border-slate-700">
                    <h3 class="text-lg font-semibold flex items-center gap-2">
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                        </svg>
                        Settings
                    </h3>
                    ${canClose && html`
                        <button onClick=${handleClose} class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors">
                            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                            </svg>
                        </button>
                    `}
                </div>

                <!-- Tabs -->
                <div class="flex border-b border-slate-700">
                    <button
                        onClick=${() => setActiveTab('servers')}
                        class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab === 'servers' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-white'}"
                    >
                        ACP Servers
                    </button>
                    <button
                        onClick=${() => setActiveTab('workspaces')}
                        class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab === 'workspaces' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-white'}"
                    >
                        Workspaces
                    </button>
                    <button
                        onClick=${() => setActiveTab('prompts')}
                        class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab === 'prompts' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-white'}"
                    >
                        Prompts
                    </button>
                    <button
                        onClick=${() => setActiveTab('web')}
                        class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab === 'web' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-white'}"
                    >
                        Web
                    </button>
                    <button
                        onClick=${() => setActiveTab('ui')}
                        class="flex-1 px-4 py-3 text-sm font-medium transition-colors ${activeTab === 'ui' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-400 hover:text-white'}"
                    >
                        UI
                    </button>
                </div>

                <!-- Content -->
                <div class="flex-1 overflow-y-auto p-4">
                    ${loading ? html`
                        <div class="flex items-center justify-center py-12">
                            <svg class="w-8 h-8 animate-spin text-blue-400" fill="none" viewBox="0 0 24 24">
                                <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                                <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                            </svg>
                        </div>
                    ` : html`
                        <!-- Workspaces Tab -->
                        ${activeTab === 'workspaces' && html`
                            <div class="space-y-4">
                                <!-- Workspace explanation -->
                                <div class="p-3 bg-slate-800/50 rounded-lg border border-slate-700">
                                    <p class="text-gray-300 text-sm leading-relaxed">
                                        A <span class="text-blue-400 font-medium">Workspace</span> pairs a directory with an ACP server.
                                        Each workspace allows you to work on a specific project with a chosen AI agent.
                                        You can configure multiple workspaces to work on different projects simultaneously.
                                    </p>
                                </div>

                                <div class="flex items-center justify-between">
                                    <p class="text-gray-400 text-sm">Configured workspaces:</p>
                                    <button
                                        onClick=${() => acpServers.length > 0 && setShowAddWorkspace(!showAddWorkspace)}
                                        disabled=${acpServers.length === 0}
                                        class="p-1.5 rounded-lg transition-colors ${acpServers.length === 0 ? 'opacity-50 cursor-not-allowed' : 'hover:bg-slate-700'} ${showAddWorkspace ? 'bg-slate-700' : ''}"
                                        title=${acpServers.length === 0 ? "Add an ACP server first" : "Add Workspace"}
                                    >
                                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                                        </svg>
                                    </button>
                                </div>
                                ${acpServers.length === 0 && html`
                                    <div class="p-3 bg-yellow-500/10 border border-yellow-500/30 rounded-lg text-yellow-400 text-sm">
                                        ⚠️ Add an ACP server first before creating workspaces.
                                    </div>
                                `}

                                ${showAddWorkspace && html`
                                    <div class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3">
                                        <div>
                                            <label class="block text-sm text-gray-400 mb-1">Directory Path</label>
                                            <div class="flex gap-2">
                                                <input
                                                    type="text"
                                                    value=${newWorkspacePath}
                                                    onInput=${e => setNewWorkspacePath(e.target.value)}
                                                    placeholder="/path/to/project"
                                                    class="${hasNativeFolderPicker() ? 'flex-1' : 'w-full'} px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                />
                                                ${hasNativeFolderPicker() && html`
                                                    <button
                                                        type="button"
                                                        onClick=${handleBrowseFolder}
                                                        class="px-3 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm transition-colors"
                                                        title="Browse for folder"
                                                    >
                                                        📁
                                                    </button>
                                                `}
                                            </div>
                                        </div>
                                        <div>
                                            <label class="block text-sm text-gray-400 mb-1">ACP Server</label>
                                            <select
                                                value=${newWorkspaceServer}
                                                onChange=${e => setNewWorkspaceServer(e.target.value)}
                                                class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                            >
                                                ${acpServers.map(srv => html`
                                                    <option key=${srv.name} value=${srv.name}>${srv.name}</option>
                                                `)}
                                            </select>
                                        </div>
                                        <div class="flex justify-end gap-2">
                                            <button
                                                onClick=${() => { setShowAddWorkspace(false); setNewWorkspacePath(''); }}
                                                class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                                            >
                                                Cancel
                                            </button>
                                            <button
                                                onClick=${addWorkspace}
                                                class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
                                            >
                                                Add
                                            </button>
                                        </div>
                                    </div>
                                `}

                                ${workspaces.length === 0 ? html`
                                    <div class="text-center py-8 text-gray-500">
                                        <svg class="w-12 h-12 mx-auto mb-2 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 7v10a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-6l-2-2H5a2 2 0 00-2 2z" />
                                        </svg>
                                        <p>No workspaces configured.</p>
                                        <p class="text-xs mt-1">Click + to add a workspace.</p>
                                    </div>
                                ` : html`
                                    <div class="space-y-2">
                                        ${workspaces.map(ws => html`
                                            <div key=${ws.working_dir} class="flex items-center gap-3 p-3 bg-slate-800/30 rounded-lg hover:bg-slate-800/50 transition-colors group">
                                                <${WorkspaceBadge} path=${ws.working_dir} customColor=${ws.color} size="sm" />
                                                <div class="flex-1 min-w-0">
                                                    <div class="font-medium text-sm">${getBasename(ws.working_dir)}</div>
                                                    <div class="text-xs text-gray-500 truncate" title=${ws.working_dir}>${ws.working_dir}</div>
                                                </div>
                                                <span class="px-2 py-1 bg-blue-500/20 text-blue-400 rounded text-xs flex-shrink-0">
                                                    ${ws.acp_server}
                                                </span>
                                                <input
                                                    type="color"
                                                    value=${ws.color || getWorkspaceVisualInfo(ws.working_dir).color.backgroundHex || '#808080'}
                                                    onChange=${(e) => updateWorkspaceColor(ws.working_dir, e.target.value)}
                                                    class="w-8 h-8 rounded cursor-pointer border border-slate-600 bg-transparent p-0.5 opacity-0 group-hover:opacity-100 transition-opacity"
                                                    title="Change badge color"
                                                />
                                                <button
                                                    onClick=${() => removeWorkspace(ws.working_dir)}
                                                    class="p-1.5 text-gray-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                                    title="Remove workspace"
                                                >
                                                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                                                    </svg>
                                                </button>
                                            </div>
                                        `)}
                                    </div>
                                `}
                            </div>
                        `}

                        <!-- ACP Servers Tab -->
                        ${activeTab === 'servers' && html`
                            <div class="space-y-4">
                                <div class="flex items-center justify-between">
                                    <p class="text-gray-400 text-sm">ACP servers are AI coding assistants. <a href="https://agentclientprotocol.com/overview/agents" onClick=${(e) => { e.preventDefault(); openExternalURL('https://agentclientprotocol.com/overview/agents'); }} class="text-blue-400 hover:text-blue-300 underline cursor-pointer">Popular examples</a> include Auggie and Claude Code. You can configure multiple servers and choose which one to use for each workspace.</p>
                                    <button
                                        onClick=${() => setShowAddServer(!showAddServer)}
                                        class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddServer ? 'bg-slate-700' : ''}"
                                        title="Add Server"
                                    >
                                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                                        </svg>
                                    </button>
                                </div>

                                ${showAddServer && html`
                                    <div class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3">
                                        <div>
                                            <label class="block text-sm text-gray-400 mb-1">Server Name</label>
                                            <input
                                                type="text"
                                                value=${newServerName}
                                                onInput=${e => setNewServerName(e.target.value)}
                                                placeholder="e.g., claude-code"
                                                class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                            />
                                        </div>
                                        <div>
                                            <label class="block text-sm text-gray-400 mb-1">Command</label>
                                            <input
                                                type="text"
                                                value=${newServerCommand}
                                                onInput=${e => setNewServerCommand(e.target.value)}
                                                placeholder="e.g., npx -y @anthropic/claude-code-acp"
                                                class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                            />
                                        </div>
                                        <div class="flex justify-end gap-2">
                                            <button
                                                onClick=${() => { setShowAddServer(false); setNewServerName(''); setNewServerCommand(''); }}
                                                class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                                            >
                                                Cancel
                                            </button>
                                            <button
                                                onClick=${addServer}
                                                class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
                                            >
                                                Add
                                            </button>
                                        </div>
                                    </div>
                                `}

                                ${acpServers.length === 0 ? html`
                                    <div class="text-center py-8 text-gray-500">
                                        <svg class="w-12 h-12 mx-auto mb-2 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
                                        </svg>
                                        <p>No ACP servers configured.</p>
                                        <p class="text-xs mt-1">Click + to add a server.</p>
                                    </div>
                                ` : html`
                                    <div class="space-y-2">
                                        ${acpServers.map(srv => html`
                                            <div key=${srv.name} class="p-3 bg-slate-800/30 rounded-lg hover:bg-slate-800/50 transition-colors group">
                                                ${editingServer === srv.name ? html`
                                                    <${ServerEditForm}
                                                        server=${srv}
                                                        onSave=${(name, cmd, prompts) => updateServer(srv.name, name, cmd, prompts)}
                                                        onCancel=${() => setEditingServer(null)}
                                                    />
                                                ` : html`
                                                    <div class="flex items-center gap-3">
                                                        <div class="flex-1 min-w-0">
                                                            <div class="font-medium text-sm flex items-center gap-2">
                                                                ${srv.name}
                                                                ${srv.prompts?.length > 0 && html`
                                                                    <span class="text-xs text-purple-400" title="${srv.prompts.length} custom prompt(s)">
                                                                        📝 ${srv.prompts.length}
                                                                    </span>
                                                                `}
                                                            </div>
                                                            <div class="text-xs text-gray-500 truncate" title=${srv.command}>${srv.command}</div>
                                                        </div>
                                                        <button
                                                            onClick=${() => setEditingServer(srv.name)}
                                                            class="p-1.5 text-gray-500 hover:text-blue-400 hover:bg-blue-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                                            title="Edit server"
                                                        >
                                                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                                                            </svg>
                                                        </button>
                                                        <button
                                                            onClick=${() => removeServer(srv.name)}
                                                            class="p-1.5 text-gray-500 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors opacity-0 group-hover:opacity-100"
                                                            title="Remove server"
                                                        >
                                                            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                                                            </svg>
                                                        </button>
                                                    </div>
                                                `}
                                            </div>
                                        `)}
                                    </div>
                                `}
                            </div>
                        `}

                        <!-- Prompts Tab -->
                        ${activeTab === 'prompts' && html`
                            <div class="space-y-4">
                                <div class="flex items-center justify-between">
                                    <p class="text-gray-400 text-sm">Predefined prompts appear as quick-access buttons in the chat input.</p>
                                    <button
                                        onClick=${() => setShowAddPrompt(!showAddPrompt)}
                                        class="p-1.5 hover:bg-slate-700 rounded-lg transition-colors ${showAddPrompt ? 'bg-slate-700' : ''}"
                                        title="Add Prompt"
                                    >
                                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                                        </svg>
                                    </button>
                                </div>

                                <!-- Add New Prompt Form -->
                                ${showAddPrompt && html`
                                    <div class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3">
                                        <div>
                                            <label class="block text-sm text-gray-400 mb-1">Button Label</label>
                                            <input
                                                type="text"
                                                value=${newPromptName}
                                                onInput=${e => setNewPromptName(e.target.value)}
                                                placeholder="e.g., Continue"
                                                class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                            />
                                        </div>
                                        <div>
                                            <label class="block text-sm text-gray-400 mb-1">Prompt Text</label>
                                            <textarea
                                                value=${newPromptText}
                                                onInput=${e => setNewPromptText(e.target.value)}
                                                placeholder="e.g., Please continue with the current task."
                                                rows="3"
                                                class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                                            />
                                        </div>
                                        <div class="flex justify-end gap-2">
                                            <button
                                                onClick=${() => {
                                                    setShowAddPrompt(false);
                                                    setNewPromptName('');
                                                    setNewPromptText('');
                                                }}
                                                class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                                            >
                                                Cancel
                                            </button>
                                            <button
                                                onClick=${() => {
                                                    if (newPromptName.trim() && newPromptText.trim()) {
                                                        setPrompts([...prompts, { name: newPromptName.trim(), prompt: newPromptText.trim() }]);
                                                        setNewPromptName('');
                                                        setNewPromptText('');
                                                        setShowAddPrompt(false);
                                                    }
                                                }}
                                                disabled=${!newPromptName.trim() || !newPromptText.trim()}
                                                class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50"
                                            >
                                                Add Prompt
                                            </button>
                                        </div>
                                    </div>
                                `}

                                <!-- Prompts List -->
                                <div class="space-y-2">
                                    ${prompts.length === 0 ? html`
                                        <div class="p-4 text-center text-gray-500 text-sm">
                                            No prompts configured. Click + to add one.
                                        </div>
                                    ` : prompts.map((prompt, index) => html`
                                        <div key=${index} class="p-3 bg-slate-800/30 rounded-lg border border-slate-700">
                                            ${editingPrompt === index ? html`
                                                <${PromptEditForm}
                                                    prompt=${prompt}
                                                    onSave=${(name, text) => {
                                                        const updated = [...prompts];
                                                        updated[index] = { name, prompt: text };
                                                        setPrompts(updated);
                                                        setEditingPrompt(null);
                                                    }}
                                                    onCancel=${() => setEditingPrompt(null)}
                                                />
                                            ` : html`
                                                <div class="flex items-start justify-between gap-3">
                                                    <div class="flex-1 min-w-0">
                                                        <div class="font-medium text-sm text-blue-400">${prompt.name}</div>
                                                        <div class="text-xs text-gray-500 mt-1 truncate">${prompt.prompt}</div>
                                                    </div>
                                                    <div class="flex items-center gap-1">
                                                        <button
                                                            onClick=${() => setEditingPrompt(index)}
                                                            class="p-1.5 hover:bg-slate-700 rounded transition-colors"
                                                            title="Edit"
                                                        >
                                                            <svg class="w-4 h-4 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                                                            </svg>
                                                        </button>
                                                        <button
                                                            onClick=${() => {
                                                                const updated = prompts.filter((_, i) => i !== index);
                                                                setPrompts(updated);
                                                            }}
                                                            class="p-1.5 hover:bg-red-500/20 rounded transition-colors"
                                                            title="Delete"
                                                        >
                                                            <svg class="w-4 h-4 text-gray-400 hover:text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                                                            </svg>
                                                        </button>
                                                    </div>
                                                </div>
                                            `}
                                        </div>
                                    `)}
                                </div>
                            </div>
                        `}

                        <!-- Web Tab -->
                        ${activeTab === 'web' && html`
                            <div class="space-y-4">
                                <p class="text-gray-400 text-sm">Configure external access settings${authEnabled ? ' and lifecycle hooks' : ''}.</p>

                                <!-- External Access Section -->
                                <div class="space-y-3">
                                    <h4 class="text-sm font-medium text-gray-300">External Access</h4>

                                    <label class="flex items-center gap-3 p-4 bg-slate-800/30 rounded-lg cursor-pointer hover:bg-slate-800/50 transition-colors">
                                        <input
                                            type="checkbox"
                                            checked=${authEnabled}
                                            onChange=${e => setAuthEnabled(e.target.checked)}
                                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                        />
                                        <div>
                                            <div class="font-medium text-sm">Allow External Access</div>
                                            <div class="text-xs text-gray-500">Listen on all interfaces (0.0.0.0) and require authentication</div>
                                        </div>
                                    </label>

                                    ${authEnabled && html`
                                        <div class="p-4 bg-slate-800/50 rounded-lg border border-slate-700 space-y-3">
                                            <!-- Username and Password in same row -->
                                            <div class="flex items-center gap-4">
                                                <div class="flex items-center gap-2">
                                                    <label class="text-sm text-gray-400">Username</label>
                                                    <input
                                                        type="text"
                                                        value=${authUsername}
                                                        onInput=${e => setAuthUsername(e.target.value)}
                                                        placeholder="admin"
                                                        class="w-28 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                    />
                                                </div>
                                                <div class="flex items-center gap-2">
                                                    <label class="text-sm text-gray-400">Password</label>
                                                    <input
                                                        type="password"
                                                        value=${authPassword}
                                                        onInput=${e => setAuthPassword(e.target.value)}
                                                        placeholder="••••••••"
                                                        class="w-28 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                    />
                                                </div>
                                            </div>
                                            <div class="flex items-center gap-3">
                                                <label class="text-sm text-gray-400">Port</label>
                                                <input
                                                    type="number"
                                                    value=${externalPort}
                                                    onInput=${e => setExternalPort(e.target.value)}
                                                    placeholder="Random"
                                                    min="0"
                                                    max="65535"
                                                    class="w-24 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                />
                                                <span class="text-xs text-gray-500">
                                                    ${!externalPort && externalEnabled && currentExternalPort
                                                        ? html`Leave empty for random port, currently <strong class="text-green-400">${currentExternalPort}</strong>`
                                                        : 'Leave empty for random port'}
                                                </span>
                                            </div>
                                            <p class="text-xs text-yellow-500">
                                                ⚠️ These credentials will be required to access Mitto from other devices.
                                            </p>
                                        </div>
                                    `}
                                </div>

                                <!-- Lifecycle Hooks Section (only shown when external access is enabled) -->
                                ${authEnabled && html`
                                    <div class="space-y-3 pt-2">
                                        <h4 class="text-sm font-medium text-gray-300">Lifecycle Hooks</h4>
                                        <p class="text-xs text-gray-500">Commands to run when the server starts or stops. Useful for setting up tunnels (<a href="https://github.com/inercia/mitto/blob/main/docs/config-web.md#using-ngrok" onClick=${(e) => { e.preventDefault(); openExternalURL('https://github.com/inercia/mitto/blob/main/docs/config-web.md#using-ngrok'); }} class="text-blue-400 hover:underline cursor-pointer">ngrok</a>, <a href="https://github.com/inercia/mitto/blob/main/docs/config-web.md#using-tailscale-funnel" onClick=${(e) => { e.preventDefault(); openExternalURL('https://github.com/inercia/mitto/blob/main/docs/config-web.md#using-tailscale-funnel'); }} class="text-blue-400 hover:underline cursor-pointer">Tailscale</a>, etc.). Use \${PORT} as a placeholder for the server port.</p>

                                        <div class="p-4 bg-slate-800/30 rounded-lg space-y-3">
                                            <div class="flex items-center gap-3">
                                                <label class="text-sm text-gray-400 w-32 flex-shrink-0">Post Up Command</label>
                                                <input
                                                    type="text"
                                                    value=${hookUpCommand}
                                                    onInput=${e => setHookUpCommand(e.target.value)}
                                                    placeholder="e.g., echo 'Server started on port \${PORT}'"
                                                    class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                                                />
                                            </div>
                                            <div class="flex items-center gap-3">
                                                <label class="text-sm text-gray-400 w-32 flex-shrink-0">Pre Down Command</label>
                                                <input
                                                    type="text"
                                                    value=${hookDownCommand}
                                                    onInput=${e => setHookDownCommand(e.target.value)}
                                                    placeholder="e.g., echo 'Server stopping...'"
                                                    class="flex-1 px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
                                                />
                                            </div>
                                        </div>
                                    </div>
                                `}
                            </div>
                        `}

                        <!-- UI Tab -->
                        ${activeTab === 'ui' && html`
                            <div class="space-y-4">
                                <p class="text-gray-400 text-sm">Configure UI preferences.</p>

                                <!-- Confirmations Section -->
                                <div class="space-y-3">
                                    <h4 class="text-sm font-medium text-gray-300">Confirmations</h4>

                                    <label class="flex items-center gap-3 p-4 bg-slate-800/30 rounded-lg cursor-pointer hover:bg-slate-800/50 transition-colors">
                                        <input
                                            type="checkbox"
                                            checked=${confirmDeleteSession}
                                            onChange=${e => setConfirmDeleteSession(e.target.checked)}
                                            class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                        />
                                        <div>
                                            <div class="font-medium text-sm">Conversation delete</div>
                                            <div class="text-xs text-gray-500">Show confirmation dialog before deleting a conversation</div>
                                        </div>
                                    </label>
                                </div>

                                <!-- macOS-specific sections -->
                                ${isMacApp && html`
                                    <!-- Window Section -->
                                    <div class="space-y-3">
                                        <h4 class="text-sm font-medium text-gray-300">Window</h4>

                                        <label class="flex items-center gap-3 p-4 bg-slate-800/30 rounded-lg cursor-pointer hover:bg-slate-800/50 transition-colors">
                                            <input
                                                type="checkbox"
                                                checked=${showInAllSpaces}
                                                onChange=${e => setShowInAllSpaces(e.target.checked)}
                                                class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                            />
                                            <div>
                                                <div class="font-medium text-sm">Show in All Spaces</div>
                                                <div class="text-xs text-gray-500">Window appears in all macOS Spaces (virtual desktops). Requires app restart.</div>
                                            </div>
                                        </label>
                                    </div>

                                    <!-- Notifications Section -->
                                    <div class="space-y-3">
                                        <h4 class="text-sm font-medium text-gray-300">Notifications</h4>

                                        <div class="space-y-2">
                                            <h5 class="text-xs font-medium text-gray-400 uppercase tracking-wide">Sounds</h5>
                                            <label class="flex items-center gap-3 p-4 bg-slate-800/30 rounded-lg cursor-pointer hover:bg-slate-800/50 transition-colors">
                                                <input
                                                    type="checkbox"
                                                    checked=${agentCompletedSound}
                                                    onChange=${e => setAgentCompletedSound(e.target.checked)}
                                                    class="w-5 h-5 rounded bg-slate-700 border-slate-600 text-blue-500 focus:ring-blue-500 focus:ring-offset-0"
                                                />
                                                <div>
                                                    <div class="font-medium text-sm">Agent Completed</div>
                                                    <div class="text-xs text-gray-500">Play a sound when the agent finishes its response</div>
                                                </div>
                                            </label>
                                        </div>
                                    </div>
                                `}
                            </div>
                        `}
                    `}
                </div>

                <!-- Footer -->
                <div class="p-4 border-t border-slate-700">
                    ${error && html`
                        <div class="mb-3 p-3 bg-red-500/20 border border-red-500/50 rounded-lg text-red-400 text-sm">
                            ${error}
                        </div>
                    `}
                    ${success && html`
                        <div class="mb-3 p-3 bg-green-500/20 border border-green-500/50 rounded-lg text-green-400 text-sm">
                            ${success}
                        </div>
                    `}
                    <div class="flex justify-end gap-2">
                        ${canClose && html`
                            <button
                                onClick=${handleClose}
                                class="px-4 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                            >
                                Cancel
                            </button>
                        `}
                        <button
                            onClick=${handleSave}
                            disabled=${saving || loading}
                            class="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50 flex items-center gap-2"
                        >
                            ${saving ? html`
                                <svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                                    <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                                    <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                                </svg>
                                Saving...
                            ` : 'Save Changes'}
                        </button>
                    </div>
                </div>
            </div>
        </div>
    `;
}

// Helper component for editing a server inline
function ServerEditForm({ server, onSave, onCancel }) {
    const [name, setName] = useState(server.name);
    const [command, setCommand] = useState(server.command);
    const [prompts, setPrompts] = useState(server.prompts || []);
    const [showAddPrompt, setShowAddPrompt] = useState(false);
    const [newPromptName, setNewPromptName] = useState('');
    const [newPromptText, setNewPromptText] = useState('');

    const addPrompt = () => {
        if (newPromptName.trim() && newPromptText.trim()) {
            setPrompts([...prompts, { name: newPromptName.trim(), prompt: newPromptText.trim() }]);
            setNewPromptName('');
            setNewPromptText('');
            setShowAddPrompt(false);
        }
    };

    const removePrompt = (index) => {
        setPrompts(prompts.filter((_, i) => i !== index));
    };

    return html`
        <div class="space-y-3">
            <div>
                <label class="block text-sm text-gray-400 mb-1">Server Name</label>
                <input
                    type="text"
                    value=${name}
                    onInput=${e => setName(e.target.value)}
                    class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
            </div>
            <div>
                <label class="block text-sm text-gray-400 mb-1">Command</label>
                <input
                    type="text"
                    value=${command}
                    onInput=${e => setCommand(e.target.value)}
                    class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
            </div>

            <!-- Server-specific prompts -->
            <div>
                <div class="flex items-center justify-between mb-2">
                    <label class="text-sm text-gray-400">Server-specific prompts</label>
                    <button
                        type="button"
                        onClick=${() => setShowAddPrompt(!showAddPrompt)}
                        class="p-1 hover:bg-slate-600 rounded transition-colors ${showAddPrompt ? 'bg-slate-600' : ''}"
                        title="Add prompt"
                    >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                        </svg>
                    </button>
                </div>

                ${showAddPrompt && html`
                    <div class="p-2 bg-slate-800 rounded-lg mb-2 space-y-2">
                        <input
                            type="text"
                            placeholder="Button label"
                            value=${newPromptName}
                            onInput=${e => setNewPromptName(e.target.value)}
                            class="w-full px-2 py-1.5 bg-slate-700 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                        />
                        <textarea
                            placeholder="Prompt text"
                            value=${newPromptText}
                            onInput=${e => setNewPromptText(e.target.value)}
                            rows="2"
                            class="w-full px-2 py-1.5 bg-slate-700 rounded text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                        />
                        <div class="flex justify-end gap-2">
                            <button
                                type="button"
                                onClick=${() => { setShowAddPrompt(false); setNewPromptName(''); setNewPromptText(''); }}
                                class="px-2 py-1 text-xs hover:bg-slate-700 rounded transition-colors"
                            >
                                Cancel
                            </button>
                            <button
                                type="button"
                                onClick=${addPrompt}
                                disabled=${!newPromptName.trim() || !newPromptText.trim()}
                                class="px-2 py-1 text-xs bg-blue-600 hover:bg-blue-500 rounded transition-colors disabled:opacity-50"
                            >
                                Add
                            </button>
                        </div>
                    </div>
                `}

                ${prompts.length === 0 ? html`
                    <div class="text-xs text-gray-500 italic">No server-specific prompts</div>
                ` : html`
                    <div class="space-y-1">
                        ${prompts.map((p, idx) => html`
                            <div key=${idx} class="flex items-center gap-2 p-2 bg-slate-800 rounded text-sm group">
                                <div class="flex-1 min-w-0">
                                    <div class="font-medium text-xs">${p.name}</div>
                                    <div class="text-xs text-gray-500 truncate" title=${p.prompt}>${p.prompt}</div>
                                </div>
                                <button
                                    type="button"
                                    onClick=${() => removePrompt(idx)}
                                    class="p-1 text-gray-500 hover:text-red-400 hover:bg-red-500/10 rounded transition-colors opacity-0 group-hover:opacity-100"
                                    title="Remove"
                                >
                                    <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                                    </svg>
                                </button>
                            </div>
                        `)}
                    </div>
                `}
            </div>

            <div class="flex justify-end gap-2">
                <button
                    onClick=${onCancel}
                    class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                >
                    Cancel
                </button>
                <button
                    onClick=${() => onSave(name, command, prompts)}
                    class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors"
                >
                    Save
                </button>
            </div>
        </div>
    `;
}

// Helper component for editing a prompt inline
function PromptEditForm({ prompt, onSave, onCancel }) {
    const [name, setName] = useState(prompt.name);
    const [text, setText] = useState(prompt.prompt);

    return html`
        <div class="space-y-3">
            <div>
                <label class="block text-sm text-gray-400 mb-1">Button Label</label>
                <input
                    type="text"
                    value=${name}
                    onInput=${e => setName(e.target.value)}
                    class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
            </div>
            <div>
                <label class="block text-sm text-gray-400 mb-1">Prompt Text</label>
                <textarea
                    value=${text}
                    onInput=${e => setText(e.target.value)}
                    rows="3"
                    class="w-full px-3 py-2 bg-slate-700 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                />
            </div>
            <div class="flex justify-end gap-2">
                <button
                    onClick=${onCancel}
                    class="px-3 py-1.5 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                >
                    Cancel
                </button>
                <button
                    onClick=${() => onSave(name, text)}
                    disabled=${!name.trim() || !text.trim()}
                    class="px-3 py-1.5 text-sm bg-blue-600 hover:bg-blue-500 rounded-lg transition-colors disabled:opacity-50"
                >
                    Save
                </button>
            </div>
        </div>
    `;
}

// =============================================================================
// Session Item Component
// =============================================================================

function SessionItem({ session, isActive, onSelect, onRename, onDelete, workspaceColor = null }) {
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
    const isStreaming = session.isStreaming || false;
    // Get working_dir from session, or fall back to global map
    const workingDir = session.working_dir || getGlobalWorkingDir(session.session_id) || '';

    return html`
        <div
            onClick=${() => onSelect(session.session_id)}
            onMouseEnter=${() => setShowActions(true)}
            onMouseLeave=${() => setShowActions(false)}
            class="p-3 border-b border-slate-700 cursor-pointer hover:bg-slate-700/50 transition-colors relative ${
                isActive ? 'bg-blue-900/30 border-l-2 border-l-blue-500' : ''
            }"
        >
            <!-- Top row: status indicator, title, and action buttons -->
            <div class="flex items-start gap-2">
                <div class="flex-1 min-w-0">
                    <div class="flex items-center gap-2">
                        ${isStreaming ? html`
                            <span class="w-2 h-2 bg-blue-400 rounded-full flex-shrink-0 streaming-indicator" title="Receiving response..."></span>
                        ` : isActiveSession ? html`
                            <span class="w-2 h-2 bg-green-400 rounded-full flex-shrink-0"></span>
                        ` : null}
                        <span class="text-sm font-medium truncate">${displayName}</span>
                    </div>
                    <div class="text-xs text-gray-500 mt-1">
                        ${new Date(session.created_at).toLocaleDateString()} ${new Date(session.created_at).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'})}
                    </div>
                </div>
                <div class="flex items-center gap-1 ${showActions ? 'opacity-100' : 'opacity-0'} transition-opacity flex-shrink-0">
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
            <!-- Bottom row: message count, stored badge, and workspace pill -->
            <div class="flex items-center justify-between mt-2">
                <div class="flex items-center gap-2">
                    ${session.messageCount !== undefined ? html`
                        <span class="text-xs text-gray-500">${session.messageCount} msgs</span>
                    ` : session.event_count !== undefined ? html`
                        <span class="text-xs text-gray-500">${session.event_count} events</span>
                    ` : null}
                    ${!session.isActive && html`
                        <span class="text-xs px-1.5 py-0.5 rounded bg-slate-700 text-gray-400">stored</span>
                    `}
                </div>
                ${workingDir && html`
                    <${WorkspacePill} path=${workingDir} customColor=${workspaceColor} />
                `}
            </div>
        </div>
    `;
}

// =============================================================================
// Session List Component (Sidebar)
// =============================================================================

function SessionList({ activeSessions, storedSessions, activeSessionId, onSelect, onNewSession, onCleanInactive, onRename, onDelete, onClose, workspaces, theme, onToggleTheme, fontSize, onToggleFontSize, onShowSettings, onShowKeyboardShortcuts, configReadonly = false, rcFilePath = null }) {
    // Combine active and stored sessions using shared helper function
    // Note: Not using useMemo to ensure working_dir is always up-to-date
    const allSessions = computeAllSessions(activeSessions, storedSessions);

    const isLight = theme === 'light';
    const isLargeFont = fontSize === 'large';

    return html`
        <div class="h-full flex flex-col">
            <div class="p-4 border-b border-slate-700 flex items-center justify-between">
                <h2 class="font-semibold text-lg">Conversations</h2>
                <div class="flex items-center gap-2">
                    <button
                        onClick=${() => onNewSession()}
                        class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                        title="New Conversation"
                    >
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
                        </svg>
                    </button>
                    <button
                        onClick=${onCleanInactive}
                        class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                        title="Clean Inactive Conversations"
                    >
                        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
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
                        No conversations yet
                    </div>
                `}
                ${allSessions.map(session => {
                    // Ensure working_dir is available by looking up in storedSessions or global map
                    const storedSession = storedSessions.find(s => s.session_id === session.session_id);
                    const workingDir = session.working_dir || storedSession?.working_dir || getGlobalWorkingDir(session.session_id) || '';
                    const finalSession = workingDir ? { ...session, working_dir: workingDir } : session;
                    // Look up workspace color
                    const workspace = workspaces.find(ws => ws.working_dir === workingDir);
                    const workspaceColor = workspace?.color || null;
                    return html`
                        <${SessionItem}
                            key=${session.session_id}
                            session=${finalSession}
                            isActive=${activeSessionId === session.session_id}
                            onSelect=${onSelect}
                            onRename=${onRename}
                            onDelete=${onDelete}
                            workspaceColor=${workspaceColor}
                        />
                    `;
                })}
            </div>
            <!-- Footer with settings, theme and font size toggles -->
            <div class="p-4 border-t border-slate-700">
                <div class="flex items-center justify-center gap-3">
                    <!-- Settings button (disabled with tooltip when using RC file, hidden when fully read-only without RC file) -->
                    ${!configReadonly ? html`
                        <button
                            onClick=${onShowSettings}
                            class="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                            title="Settings"
                        >
                            <svg class="w-5 h-5 text-gray-400 hover:text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                            </svg>
                        </button>
                    ` : rcFilePath ? html`
                        <button
                            disabled
                            class="p-2 rounded-lg opacity-50 cursor-not-allowed"
                            title="Using ${rcFilePath}"
                        >
                            <svg class="w-5 h-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                            </svg>
                        </button>
                    ` : null}
                    <!-- Theme toggle -->
                    <div
                        class="theme-toggle-v2"
                        onClick=${onToggleTheme}
                        role="button"
                        tabIndex="0"
                        title="${isLight ? 'Switch to dark theme' : 'Switch to light theme'}"
                        aria-label="Toggle between light and dark theme"
                    >
                        <!-- Sun icon -->
                        <div class="theme-toggle-v2__option ${isLight ? 'active' : ''}">
                            <svg viewBox="0 0 24 24">
                                <circle cx="12" cy="12" r="4"></circle>
                                <path d="M12 2v2"></path>
                                <path d="M12 20v2"></path>
                                <path d="m4.93 4.93 1.41 1.41"></path>
                                <path d="m17.66 17.66 1.41 1.41"></path>
                                <path d="M2 12h2"></path>
                                <path d="M20 12h2"></path>
                                <path d="m6.34 17.66-1.41 1.41"></path>
                                <path d="m19.07 4.93-1.41 1.41"></path>
                            </svg>
                        </div>
                        <!-- Moon icon -->
                        <div class="theme-toggle-v2__option ${!isLight ? 'active' : ''}">
                            <svg viewBox="0 0 24 24">
                                <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"></path>
                            </svg>
                        </div>
                    </div>
                    <!-- Font size toggle -->
                    <div
                        class="font-size-toggle"
                        onClick=${onToggleFontSize}
                        role="button"
                        tabIndex="0"
                        title="${isLargeFont ? 'Switch to small font' : 'Switch to large font'}"
                        aria-label="Toggle between small and large font size"
                    >
                        <span class="font-size-toggle__option ${!isLargeFont ? 'active' : ''}">A</span>
                        <span class="font-size-toggle__option font-size-toggle__option--large ${isLargeFont ? 'active' : ''}">A</span>
                    </div>
                    <!-- Keyboard shortcuts button -->
                    <button
                        onClick=${onShowKeyboardShortcuts}
                        class="p-2 hover:bg-slate-700 rounded-lg transition-colors group"
                        title="Keyboard Shortcuts"
                    >
                        <svg class="w-4 h-4 text-gray-400 group-hover:text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <rect x="2" y="4" width="20" height="16" rx="2" ry="2"></rect>
                            <path d="M6 8h.001"></path>
                            <path d="M10 8h.001"></path>
                            <path d="M14 8h.001"></path>
                            <path d="M18 8h.001"></path>
                            <path d="M8 12h.001"></path>
                            <path d="M12 12h.001"></path>
                            <path d="M16 12h.001"></path>
                            <path d="M7 16h10"></path>
                        </svg>
                    </button>
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
        loadMoreMessages,
        updateSessionName,
        renameSession,
        removeSession,
        isStreaming,
        hasMoreMessages,
        sessionInfo,
        activeSessionId,
        activeSessions,
        storedSessions,
        fetchStoredSessions,
        backgroundCompletion,
        clearBackgroundCompletion,
        workspaces,
        acpServers,
        addWorkspace,
        removeWorkspace,
        refreshWorkspaces
    } = useWebSocket();

    const [showSidebar, setShowSidebar] = useState(false);
    const [renameDialog, setRenameDialog] = useState({ isOpen: false, session: null });
    const [deleteDialog, setDeleteDialog] = useState({ isOpen: false, session: null });
    const [cleanInactiveDialog, setCleanInactiveDialog] = useState({ isOpen: false });
    const [workspaceDialog, setWorkspaceDialog] = useState({ isOpen: false }); // Workspace selector for new session
    const [settingsDialog, setSettingsDialog] = useState({ isOpen: false, forceOpen: false }); // Settings dialog
    const [keyboardShortcutsDialog, setKeyboardShortcutsDialog] = useState({ isOpen: false }); // Keyboard shortcuts dialog
    const [globalPrompts, setGlobalPrompts] = useState([]); // Global prompts from web.prompts
    const [acpServersWithPrompts, setAcpServersWithPrompts] = useState([]); // ACP servers with their per-server prompts
    const [configReadonly, setConfigReadonly] = useState(false); // True when --config flag was used or using RC file
    const [rcFilePath, setRcFilePath] = useState(null); // Path to RC file when config is read-only due to RC file
    const [swipeDirection, setSwipeDirection] = useState(null); // 'left' or 'right' for animation
    const [swipeArrow, setSwipeArrow] = useState(null); // 'left' or 'right' for arrow indicator
    const [toastVisible, setToastVisible] = useState(false);
    const [toastData, setToastData] = useState(null); // { sessionId, sessionName }
    const [loadingMore, setLoadingMore] = useState(false);
    const [isUserAtBottom, setIsUserAtBottom] = useState(true);
    const [hasNewMessages, setHasNewMessages] = useState(false);
    // Per-session draft text: { sessionId: draftText } - null key for "no session" state
    const [sessionDrafts, setSessionDrafts] = useState({});
    const sessionDraftsRef = useRef(sessionDrafts);
    useEffect(() => { sessionDraftsRef.current = sessionDrafts; }, [sessionDrafts]);
    const messagesEndRef = useRef(null);
    const mainContentRef = useRef(null);
    const messagesContainerRef = useRef(null);
    const prevMessagesLengthRef = useRef(0);

    // Compute all sessions for navigation using shared helper function
    const allSessions = useMemo(
        () => computeAllSessions(activeSessions, storedSessions),
        [activeSessions, storedSessions]
    );

    // Compute merged prompts: global prompts + server-specific prompts for active session
    const predefinedPrompts = useMemo(() => {
        // Start with global prompts
        const merged = [...globalPrompts];

        // Find server-specific prompts for the active session's ACP server
        if (sessionInfo?.acp_server && acpServersWithPrompts.length > 0) {
            const server = acpServersWithPrompts.find(s => s.name === sessionInfo.acp_server);
            console.log('[predefinedPrompts] sessionInfo.acp_server:', sessionInfo.acp_server,
                        'acpServersWithPrompts:', acpServersWithPrompts,
                        'found server:', server);
            if (server?.prompts?.length > 0) {
                // Add server-specific prompts after global prompts
                merged.push(...server.prompts);
            }
        }

        return merged;
    }, [globalPrompts, sessionInfo?.acp_server, acpServersWithPrompts]);

    // Clear swipe direction after animation completes
    useEffect(() => {
        if (swipeDirection) {
            const timer = setTimeout(() => setSwipeDirection(null), 250);
            return () => clearTimeout(timer);
        }
    }, [swipeDirection, activeSessionId]);

    // Clear swipe arrow indicator after animation completes (1 second)
    useEffect(() => {
        if (swipeArrow) {
            const timer = setTimeout(() => setSwipeArrow(null), 1000);
            return () => clearTimeout(timer);
        }
    }, [swipeArrow]);

    // Ref to track toast hide timer
    const toastTimerRef = useRef(null);

    // Show toast when a background session completes
    useEffect(() => {
        if (backgroundCompletion) {
            // Clear any existing timer
            if (toastTimerRef.current) {
                clearTimeout(toastTimerRef.current);
            }

            setToastData(backgroundCompletion);
            setToastVisible(true);
            clearBackgroundCompletion();

            // Set timer to hide toast after 5 seconds
            toastTimerRef.current = setTimeout(() => {
                setToastVisible(false);
                toastTimerRef.current = null;
            }, 5000);
        }
    }, [backgroundCompletion, clearBackgroundCompletion]);

    // Clear toast data after exit animation completes
    useEffect(() => {
        if (!toastVisible && toastData) {
            const clearTimer = setTimeout(() => {
                setToastData(null);
            }, 200);
            return () => clearTimeout(clearTimer);
        }
    }, [toastVisible, toastData]);

    // Cleanup timer on unmount
    useEffect(() => {
        return () => {
            if (toastTimerRef.current) {
                clearTimeout(toastTimerRef.current);
            }
        };
    }, []);

    // Get the current draft for the active session (null key = no session)
    const currentDraft = sessionDrafts[activeSessionId ?? '__no_session__'] || '';

    // Update draft for a specific session (or null = no session)
    const updateDraft = useCallback((sessionId, text) => {
        const key = sessionId ?? '__no_session__';
        setSessionDrafts(prev => ({ ...prev, [key]: text }));
    }, []);

    // Ref-based version for async callbacks (avoid stale closure)
    const updateDraftForSession = useCallback((sessionId, text) => {
        const key = sessionId ?? '__no_session__';
        setSessionDrafts(prev => ({ ...prev, [key]: text }));
    }, []);

    // Handle loading more messages
    const handleLoadMore = useCallback(async () => {
        if (loadingMore || !activeSessionId || !hasMoreMessages) return;

        // Remember scroll position to maintain it after loading
        const container = messagesContainerRef.current;
        const scrollHeightBefore = container?.scrollHeight || 0;

        setLoadingMore(true);
        await loadMoreMessages(activeSessionId);
        setLoadingMore(false);

        // Restore scroll position (keep user at same visual position)
        if (container) {
            const scrollHeightAfter = container.scrollHeight;
            container.scrollTop = scrollHeightAfter - scrollHeightBefore;
        }
    }, [loadingMore, activeSessionId, hasMoreMessages, loadMoreMessages]);

    // Navigate to previous/next session with animation direction (wraps around for swipe gestures)
    const navigateToPreviousSession = useCallback(() => {
        if (allSessions.length <= 1) return;
        const currentIndex = allSessions.findIndex(s => s.session_id === activeSessionId);
        if (currentIndex === -1) return;
        const prevIndex = currentIndex === 0 ? allSessions.length - 1 : currentIndex - 1;
        setSwipeDirection('right'); // Content slides in from left
        setSwipeArrow('right'); // Show right arrow (user swiped right)
        switchSession(allSessions[prevIndex].session_id);
    }, [allSessions, activeSessionId, switchSession]);

    const navigateToNextSession = useCallback(() => {
        if (allSessions.length <= 1) return;
        const currentIndex = allSessions.findIndex(s => s.session_id === activeSessionId);
        if (currentIndex === -1) return;
        const nextIndex = currentIndex === allSessions.length - 1 ? 0 : currentIndex + 1;
        setSwipeDirection('left'); // Content slides in from right
        setSwipeArrow('left'); // Show left arrow (user swiped left)
        switchSession(allSessions[nextIndex].session_id);
    }, [allSessions, activeSessionId, switchSession]);

    // Navigate to session above in the list (no wrap-around, for keyboard shortcuts)
    const navigateToSessionAbove = useCallback(() => {
        if (allSessions.length <= 1) return;
        const currentIndex = allSessions.findIndex(s => s.session_id === activeSessionId);
        if (currentIndex === -1 || currentIndex === 0) return; // Already at top or not found
        setSwipeDirection('right');
        switchSession(allSessions[currentIndex - 1].session_id);
    }, [allSessions, activeSessionId, switchSession]);

    // Navigate to session below in the list (no wrap-around, for keyboard shortcuts)
    const navigateToSessionBelow = useCallback(() => {
        if (allSessions.length <= 1) return;
        const currentIndex = allSessions.findIndex(s => s.session_id === activeSessionId);
        if (currentIndex === -1 || currentIndex === allSessions.length - 1) return; // Already at bottom or not found
        setSwipeDirection('left');
        switchSession(allSessions[currentIndex + 1].session_id);
    }, [allSessions, activeSessionId, switchSession]);

    // Open sidebar handler for edge swipe
    const openSidebar = useCallback(() => {
        setShowSidebar(true);
    }, []);

    // Enable swipe navigation on mobile
    // - Swipe left/right anywhere: switch sessions
    // - Swipe right from left edge: open sidebar
    useSwipeNavigation(mainContentRef, navigateToNextSession, navigateToPreviousSession, {
        threshold: 80,           // Require a decent swipe distance
        maxVertical: 80,         // Allow some vertical movement
        edgeWidth: 40,           // Start from edge zone
        onEdgeSwipeRight: openSidebar  // Swipe right from left edge opens sidebar
    });

    // Navigate to session by index (0-based) for keyboard shortcuts
    const navigateToSessionByIndex = useCallback((index) => {
        if (index >= 0 && index < allSessions.length) {
            const targetSession = allSessions[index];
            if (targetSession.session_id !== activeSessionId) {
                switchSession(targetSession.session_id);
            }
        }
    }, [allSessions, activeSessionId, switchSession]);

    // Global keyboard shortcuts for Command+1-9 to switch sessions and Command+, for settings
    useEffect(() => {
        const handleGlobalKeyDown = (e) => {
            // Command+Control+Up/Down to navigate between conversations (macOS)
            if (e.metaKey && e.ctrlKey && !e.shiftKey && !e.altKey) {
                if (e.key === 'ArrowUp') {
                    e.preventDefault();
                    navigateToSessionAbove();
                    setTimeout(() => {
                        if (chatInputRef.current) {
                            chatInputRef.current.focus();
                        }
                    }, 100);
                    return;
                }
                if (e.key === 'ArrowDown') {
                    e.preventDefault();
                    navigateToSessionBelow();
                    setTimeout(() => {
                        if (chatInputRef.current) {
                            chatInputRef.current.focus();
                        }
                    }, 100);
                    return;
                }
            }

            // Check for Command (macOS) or Ctrl (other platforms)
            if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey) {
                const key = e.key;
                // Check if key is 1-9
                if (key >= '1' && key <= '9') {
                    e.preventDefault();
                    const index = parseInt(key, 10) - 1; // Convert to 0-based index
                    navigateToSessionByIndex(index);
                    // Focus the input after switching sessions
                    setTimeout(() => {
                        if (chatInputRef.current) {
                            chatInputRef.current.focus();
                        }
                    }, 100);
                }
                // Command+, to open settings (standard macOS convention)
                if (key === ',') {
                    e.preventDefault();
                    if (!configReadonly) {
                        setSettingsDialog({ isOpen: true, forceOpen: false });
                    }
                }
            }
        };

        window.addEventListener('keydown', handleGlobalKeyDown);
        return () => window.removeEventListener('keydown', handleGlobalKeyDown);
    }, [navigateToSessionByIndex, navigateToSessionAbove, navigateToSessionBelow, configReadonly]);

    // State for UI theme style (v2 = Clawdbot-inspired)
    const [uiTheme, setUiTheme] = useState('default');

    // UI settings (macOS only)
    const [agentCompletedSoundEnabled, setAgentCompletedSoundEnabled] = useState(false);

    // UI confirmation settings (default: true - show confirmations)
    const [confirmDeleteSession, setConfirmDeleteSession] = useState(true);

    // Check if running in the native macOS app
    const isMacApp = typeof window.mittoPickFolder === 'function';

    // Fetch config on mount to get predefined prompts, UI theme, and check for workspaces
    useEffect(() => {
        fetch('/api/config')
            .then(res => res.json())
            .then(config => {
                // Load global prompts from web.prompts
                if (config?.web?.prompts) {
                    setGlobalPrompts(config.web.prompts);
                }
                // Store ACP servers with their per-server prompts
                if (config?.acp_servers) {
                    console.log('[config] ACP servers with prompts:', config.acp_servers);
                    setAcpServersWithPrompts(config.acp_servers);
                }
                // Track if config is read-only (loaded from --config file or RC file)
                if (config?.config_readonly) {
                    setConfigReadonly(true);
                    // If using an RC file, store the path for tooltip display
                    if (config?.rc_file_path) {
                        setRcFilePath(config.rc_file_path);
                    }
                }
                // Load v2 stylesheet if configured
                if (config?.web?.theme === 'v2') {
                    setUiTheme('v2');
                    // Dynamically load the v2 stylesheet
                    const existingLink = document.getElementById('mitto-theme-v2');
                    if (!existingLink) {
                        const link = document.createElement('link');
                        link.id = 'mitto-theme-v2';
                        link.rel = 'stylesheet';
                        link.href = './styles-v2.css';
                        document.head.appendChild(link);
                    }
                    // Add v2-theme class to body for CSS overrides
                    document.body.classList.add('v2-theme');
                }
                // Load UI confirmation settings
                if (config?.ui?.confirmations?.delete_session === false) {
                    setConfirmDeleteSession(false);
                }
                // Load UI settings (macOS only)
                if (config?.ui?.mac?.notifications?.sounds?.agent_completed) {
                    setAgentCompletedSoundEnabled(true);
                    window.mittoAgentCompletedSoundEnabled = true;
                }
                // Check if ACP servers or workspaces are configured - if not, force open settings
                // Skip this if config is read-only (user manages config via file)
                const noAcpServers = !config?.acp_servers || config.acp_servers.length === 0;
                const noWorkspaces = !config?.workspaces || config.workspaces.length === 0;
                if ((noAcpServers || noWorkspaces) && !config?.config_readonly) {
                    setSettingsDialog({ isOpen: true, forceOpen: true });
                }
            })
            .catch(err => console.error('Failed to fetch config:', err));
    }, []);

    // Theme state - default depends on UI theme (v2 defaults to light, default theme defaults to dark)
    const [theme, setTheme] = useState(() => {
        if (typeof localStorage !== 'undefined') {
            const saved = localStorage.getItem('mitto-theme');
            if (saved) return saved;
        }
        // If v2 theme is active (set by index.html script), default to light
        if (window.mittoTheme === 'v2' || document.documentElement.classList.contains('v2-theme')) {
            return 'light';
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

    // Font size state - persisted to localStorage
    const [fontSize, setFontSize] = useState(() => {
        if (typeof localStorage !== 'undefined') {
            const saved = localStorage.getItem('mitto-font-size');
            if (saved === 'small' || saved === 'large') return saved;
        }
        return 'small'; // Default to small
    });

    // Apply font size class to document
    useEffect(() => {
        const root = document.documentElement;
        if (fontSize === 'large') {
            root.classList.add('font-large');
            root.classList.remove('font-small');
        } else {
            root.classList.add('font-small');
            root.classList.remove('font-large');
        }
        localStorage.setItem('mitto-font-size', fontSize);
    }, [fontSize]);

    const toggleFontSize = useCallback(() => {
        setFontSize(prev => prev === 'small' ? 'large' : 'small');
    }, []);

    // Threshold for considering user "at bottom" (in pixels)
    const SCROLL_THRESHOLD = 100;

    // Check if the user is at the bottom of the messages container
    const checkIfAtBottom = useCallback(() => {
        const container = messagesContainerRef.current;
        if (!container) return true;
        const { scrollTop, scrollHeight, clientHeight } = container;
        return scrollHeight - scrollTop - clientHeight <= SCROLL_THRESHOLD;
    }, []);

    // Scroll to bottom handler
    const scrollToBottom = useCallback((smooth = true) => {
        if (messagesEndRef.current) {
            messagesEndRef.current.scrollIntoView({ behavior: smooth ? 'smooth' : 'auto' });
            setIsUserAtBottom(true);
            setHasNewMessages(false);
        }
    }, []);

    // Handle scroll events to track user's scroll position
    useEffect(() => {
        const container = messagesContainerRef.current;
        if (!container) return;

        const handleScroll = () => {
            const atBottom = checkIfAtBottom();
            setIsUserAtBottom(atBottom);
            // Clear new messages indicator when user scrolls to bottom
            if (atBottom) {
                setHasNewMessages(false);
            }
        };

        container.addEventListener('scroll', handleScroll, { passive: true });
        return () => container.removeEventListener('scroll', handleScroll);
    }, [checkIfAtBottom]);

    // Smart auto-scroll: only scroll if user is at bottom
    useEffect(() => {
        const currentLength = messages.length;
        const prevLength = prevMessagesLengthRef.current;
        const hasNewContent = currentLength > prevLength || (isStreaming && currentLength > 0);

        if (hasNewContent) {
            if (isUserAtBottom) {
                // User is at bottom, auto-scroll
                scrollToBottom(true);
            } else {
                // User has scrolled up, show new messages indicator
                setHasNewMessages(true);
            }
        }

        prevMessagesLengthRef.current = currentLength;
    }, [messages, isStreaming, isUserAtBottom, scrollToBottom]);

    // Reset scroll state when switching sessions
    useEffect(() => {
        setIsUserAtBottom(true);
        setHasNewMessages(false);
        prevMessagesLengthRef.current = 0;
        // Scroll to bottom when switching sessions
        // Use multiple attempts to ensure content is fully rendered, especially on mobile
        // First attempt immediately after state update
        requestAnimationFrame(() => {
            scrollToBottom(false);
            // Second attempt after a short delay for initial render
            setTimeout(() => {
                scrollToBottom(false);
                // Third attempt with longer delay for mobile devices where
                // content loading/rendering may take more time
                setTimeout(() => scrollToBottom(false), 150);
            }, 50);
        });
    }, [activeSessionId, scrollToBottom]);

    // Ref for the chat input component to allow focusing from native menu
    const chatInputRef = useRef(null);

    // Expose global functions for native macOS menu integration
    useEffect(() => {
        // New Conversation - called from native Cmd+N menu
        window.mittoNewConversation = async () => {
            // Use handleNewSession logic to support workspace selection
            if (workspaces.length === 0) {
                // No workspaces configured - open settings dialog (unless config is read-only)
                if (!configReadonly) {
                    setSettingsDialog({ isOpen: true, forceOpen: true });
                }
                setShowSidebar(false);
                return;
            }
            if (workspaces.length > 1) {
                setWorkspaceDialog({ isOpen: true });
            } else {
                // Single workspace - create session directly with workspace info
                const ws = workspaces[0];
                const result = await newSession({ workingDir: ws.working_dir, acpServer: ws.acp_server });
                // If session creation failed due to no workspace configured, open settings
                if (result?.errorCode === 'no_workspace_configured' && !configReadonly) {
                    setSettingsDialog({ isOpen: true, forceOpen: true });
                }
            }
            setShowSidebar(false);
            // Focus the input after creating new session
            setTimeout(() => {
                if (chatInputRef.current) {
                    chatInputRef.current.focus();
                }
            }, 100);
        };

        // Focus Input - called from native Cmd+L menu
        window.mittoFocusInput = () => {
            if (chatInputRef.current) {
                chatInputRef.current.focus();
            }
        };

        // Toggle Sidebar - called from native Cmd+Shift+S menu
        window.mittoToggleSidebar = () => {
            setShowSidebar(prev => !prev);
        };

        // Show Settings - called from native Cmd+, menu
        window.mittoShowSettings = () => {
            if (!configReadonly) {
                setSettingsDialog({ isOpen: true, forceOpen: false });
            }
        };

        // Close Conversation - called from native Cmd+W menu
        window.mittoCloseConversation = async () => {
            if (!activeSessionId) return;

            // If confirmation is enabled, show the delete dialog
            if (confirmDeleteSession) {
                // Find the current session to pass to the dialog
                const currentSession = activeSessions.find(s => s.session_id === activeSessionId)
                    || storedSessions.find(s => s.session_id === activeSessionId);
                if (currentSession) {
                    setDeleteDialog({ isOpen: true, session: currentSession });
                }
                return;
            }

            // Otherwise delete immediately
            await removeSession(activeSessionId);
            fetchStoredSessions();
        };

        // Cleanup on unmount
        return () => {
            delete window.mittoNewConversation;
            delete window.mittoFocusInput;
            delete window.mittoToggleSidebar;
            delete window.mittoShowSettings;
            delete window.mittoCloseConversation;
        };
    }, [newSession, workspaces, removeSession, fetchStoredSessions, activeSessionId, confirmDeleteSession, activeSessions, storedSessions, configReadonly]);

    const handleNewSession = async () => {
        // If no workspaces configured, open settings dialog (unless config is read-only)
        if (workspaces.length === 0) {
            if (!configReadonly) {
                setSettingsDialog({ isOpen: true, forceOpen: true });
            }
            setShowSidebar(false);
            return;
        }
        // If multiple workspaces, show workspace selector
        if (workspaces.length > 1) {
            setWorkspaceDialog({ isOpen: true });
            setShowSidebar(false);
        } else {
            // Single workspace - create session directly with workspace info
            setShowSidebar(false);
            const ws = workspaces[0];
            const result = await newSession({ workingDir: ws.working_dir, acpServer: ws.acp_server });
            // If session creation failed due to no workspace configured, open settings
            if (result?.errorCode === 'no_workspace_configured' && !configReadonly) {
                setSettingsDialog({ isOpen: true, forceOpen: true });
            } else {
                // Focus the input after creating new session
                setTimeout(() => {
                    if (chatInputRef.current) {
                        chatInputRef.current.focus();
                    }
                }, 100);
            }
        }
    };

    const handleWorkspaceSelect = async (workspace) => {
        setWorkspaceDialog({ isOpen: false });
        const result = await newSession({ workingDir: workspace.working_dir, acpServer: workspace.acp_server });
        // If session creation failed due to no workspace configured, open settings (unless config is read-only)
        if (result?.errorCode === 'no_workspace_configured' && !configReadonly) {
            setSettingsDialog({ isOpen: true, forceOpen: true });
        } else {
            // Focus the input after creating new session
            setTimeout(() => {
                if (chatInputRef.current) {
                    chatInputRef.current.focus();
                }
            }, 100);
        }
    };

    const handleShowSettings = () => {
        // Don't open settings dialog if config is read-only
        if (configReadonly) {
            return;
        }
        setSettingsDialog({ isOpen: true, forceOpen: false });
    };

    const handleShowKeyboardShortcuts = () => {
        setKeyboardShortcutsDialog({ isOpen: true });
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

    const handleDeleteSession = async (session) => {
        // If confirmation is disabled, delete immediately
        if (!confirmDeleteSession) {
            await removeSession(session.session_id);
            fetchStoredSessions();
            return;
        }
        // Otherwise show the confirmation dialog
        setDeleteDialog({ isOpen: true, session });
    };

    const handleConfirmDelete = async () => {
        const session = deleteDialog.session;
        if (!session) return;

        // Close the dialog first
        setDeleteDialog({ isOpen: false, session: null });

        // removeSession handles: closing WebSocket, updating local state,
        // switching to another session (or creating new if none left), and calling DELETE API
        await removeSession(session.session_id);

        // Refresh the stored sessions list
        fetchStoredSessions();
    };

    // Get inactive sessions (stored sessions without an active ACP connection)
    const inactiveSessions = useMemo(() => {
        const activeIds = new Set(activeSessions.map(s => s.session_id));
        return storedSessions.filter(s => !activeIds.has(s.session_id));
    }, [activeSessions, storedSessions]);

    const handleCleanInactive = () => {
        setCleanInactiveDialog({ isOpen: true });
    };

    const handleConfirmCleanInactive = async () => {
        setCleanInactiveDialog({ isOpen: false });

        // Delete all inactive sessions
        for (const session of inactiveSessions) {
            try {
                await fetch(`/api/sessions/${session.session_id}`, { method: 'DELETE' });
            } catch (err) {
                console.error('Failed to delete session:', session.session_id, err);
            }
        }

        // Refresh the stored sessions list
        fetchStoredSessions();
    };

    return html`
        <div class="h-screen flex">
            <!-- Session Properties Dialog -->
            <${SessionPropertiesDialog}
                isOpen=${renameDialog.isOpen}
                session=${renameDialog.session}
                onSave=${handleSaveRename}
                onCancel=${() => setRenameDialog({ isOpen: false, session: null })}
                workspaces=${workspaces}
            />

            <!-- Delete Dialog -->
            <${DeleteDialog}
                isOpen=${deleteDialog.isOpen}
                sessionName=${deleteDialog.session?.name || deleteDialog.session?.description || 'Untitled'}
                isActive=${deleteDialog.session?.session_id === activeSessionId}
                isStreaming=${deleteDialog.session?.isStreaming || false}
                onConfirm=${handleConfirmDelete}
                onCancel=${() => setDeleteDialog({ isOpen: false, session: null })}
            />

            <!-- Clean Inactive Dialog -->
            <${CleanInactiveDialog}
                isOpen=${cleanInactiveDialog.isOpen}
                inactiveCount=${inactiveSessions.length}
                onConfirm=${handleConfirmCleanInactive}
                onCancel=${() => setCleanInactiveDialog({ isOpen: false })}
            />

            <!-- Workspace Selection Dialog (for new conversations) -->
            <${WorkspaceDialog}
                isOpen=${workspaceDialog.isOpen}
                workspaces=${workspaces}
                onSelect=${handleWorkspaceSelect}
                onCancel=${() => setWorkspaceDialog({ isOpen: false })}
            />

            <!-- Settings Dialog -->
            <${SettingsDialog}
                isOpen=${settingsDialog.isOpen}
                forceOpen=${settingsDialog.forceOpen}
                onClose=${() => setSettingsDialog({ isOpen: false, forceOpen: false })}
                onSave=${async () => {
                    // Refresh workspaces after saving
                    refreshWorkspaces();
                    // Reload config to update prompts and UI settings
                    try {
                        const res = await fetch('/api/config');
                        if (res.ok) {
                            const config = await res.json();
                            // Reload global prompts (use empty array if not present)
                            setGlobalPrompts(config?.web?.prompts || []);
                            // Reload ACP servers with their per-server prompts
                            setAcpServersWithPrompts(config?.acp_servers || []);
                            // Reload UI settings
                            setConfirmDeleteSession(config?.ui?.confirmations?.delete_session !== false);
                        }
                    } catch (err) {
                        console.error('Failed to reload config after save:', err);
                    }
                }}
            />

            <!-- Keyboard Shortcuts Dialog -->
            <${KeyboardShortcutsDialog}
                isOpen=${keyboardShortcutsDialog.isOpen}
                onClose=${() => setKeyboardShortcutsDialog({ isOpen: false })}
            />

            <!-- Background completion toast -->
            ${toastData && html`
                <div
                    class="fixed top-4 left-1/2 -translate-x-1/2 z-50 ${toastVisible ? 'toast-enter' : 'toast-exit'}"
                    onClick=${() => {
                        switchSession(toastData.sessionId);
                        setToastVisible(false);
                        setTimeout(() => setToastData(null), 200);
                    }}
                >
                    <div class="flex items-center gap-2 px-4 py-2 bg-green-600 text-white rounded-full shadow-lg cursor-pointer hover:bg-green-500 transition-colors">
                        <span class="text-lg">✓</span>
                        <span class="text-sm font-medium truncate max-w-[200px]">${toastData.sessionName}</span>
                        <span class="text-xs opacity-75">finished</span>
                    </div>
                </div>
            `}

            <!-- Sidebar (hidden on mobile by default) -->
            <div class="hidden md:block w-80 bg-mitto-sidebar border-r border-slate-700 flex-shrink-0">
                <${SessionList}
                    activeSessions=${activeSessions}
                    storedSessions=${storedSessions}
                    activeSessionId=${activeSessionId}
                    onSelect=${handleSelectSession}
                    onNewSession=${handleNewSession}
                    onCleanInactive=${handleCleanInactive}
                    onRename=${handleRenameSession}
                    onDelete=${handleDeleteSession}
                    workspaces=${workspaces}
                    theme=${theme}
                    onToggleTheme=${toggleTheme}
                    fontSize=${fontSize}
                    onToggleFontSize=${toggleFontSize}
                    onShowSettings=${handleShowSettings}
                    onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
                    configReadonly=${configReadonly}
                    rcFilePath=${rcFilePath}
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
                            onCleanInactive=${handleCleanInactive}
                            onRename=${handleRenameSession}
                            onDelete=${handleDeleteSession}
                            onClose=${() => setShowSidebar(false)}
                            workspaces=${workspaces}
                            theme=${theme}
                            onToggleTheme=${toggleTheme}
                            fontSize=${fontSize}
                            onToggleFontSize=${toggleFontSize}
                            onShowSettings=${handleShowSettings}
                            onShowKeyboardShortcuts=${handleShowKeyboardShortcuts}
                            configReadonly=${configReadonly}
                            rcFilePath=${rcFilePath}
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
                    <h1 class="font-bold text-xl truncate max-w-[300px] sm:max-w-[400px] ${!activeSessionId ? 'text-gray-500' : ''}">${activeSessionId ? (sessionInfo?.name || 'New conversation') : 'No Active Session'}</h1>
                    <div class="ml-auto flex items-center gap-2">
                        ${isStreaming && html`
                            <span class="w-2 h-2 bg-blue-400 rounded-full animate-pulse" title="Streaming"></span>
                        `}
                        ${activeSessionId && html`
                            <span class="text-gray-500" title="Session is auto-saved">
                                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4" />
                                </svg>
                            </span>
                        `}
                        <span class="w-2 h-2 rounded-full ${connected ? 'bg-green-400' : 'bg-red-400'}" title="${connected ? 'Connected' : 'Disconnected'}"></span>
                    </div>
                </div>

                <!-- Messages -->
                <div ref=${messagesContainerRef} class="flex-1 overflow-y-auto scroll-smooth scrollbar-hide p-4 relative">
                    ${swipeDirection && html`
                        <div key=${`flash-${activeSessionId}`} class="swipe-flash swipe-flash-${swipeDirection}" />
                    `}
                    ${swipeArrow && html`
                        <div key=${`arrow-${activeSessionId}-${Date.now()}`} class="swipe-arrow-indicator">
                            <div class="swipe-arrow-indicator__content">
                                <span class="swipe-arrow-indicator__arrow">${swipeArrow === 'left' ? '←' : '→'}</span>
                            </div>
                        </div>
                    `}
                    <div key=${activeSessionId} class="max-w-4xl mx-auto ${swipeDirection ? `swipe-slide-${swipeDirection}` : ''}">
                        ${hasMoreMessages && html`
                            <div class="flex justify-center mb-4">
                                <button
                                    onClick=${handleLoadMore}
                                    disabled=${loadingMore}
                                    class="px-4 py-2 text-sm text-gray-400 hover:text-white bg-slate-800 hover:bg-slate-700 rounded-full transition-colors flex items-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed"
                                >
                                    ${loadingMore ? html`
                                        <svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                                            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
                                            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path>
                                        </svg>
                                        <span>Loading...</span>
                                    ` : html`
                                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 15l7-7 7 7" />
                                        </svg>
                                        <span>Load earlier messages</span>
                                    `}
                                </button>
                            </div>
                        `}
                        ${messages.length === 0 && !hasMoreMessages && html`
                            <div class="flex items-center justify-center h-full">
                                <div class="text-center text-gray-400">
                                    <div class="text-6xl mb-6">💬</div>
                                    <p class="text-2xl font-medium text-gray-300 mb-4">Welcome to Mitto</p>
                                    ${workspaces.length === 0 ? html`
                                        <p class="text-base text-gray-500 max-w-md">
                                            Get started by creating a workspace in Settings
                                            (<span class="inline-block align-middle">
                                                <svg class="w-5 h-5 inline" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                                                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                                                </svg>
                                            </span> icon in the sidebar)
                                        </p>
                                    ` : activeSessionId ? html`
                                        <p class="text-base text-gray-500">Type a message to start chatting with the AI agent</p>
                                    ` : html`
                                        <div class="text-base text-gray-500 max-w-md">
                                            <p>Create a new conversation using the
                                                <span class="inline-flex items-center justify-center w-6 h-6 rounded text-white text-sm font-bold mx-1">+</span>
                                                button in the sidebar
                                            </p>
                                            ${workspaces.length > 1 ? html`
                                                <p class="text-sm text-gray-600 mt-3">You'll be able to choose which workspace to use</p>
                                            ` : ''}
                                        </div>
                                    `}
                                    ${!connected && html`
                                        <p class="text-sm mt-6 text-yellow-500">Connecting to server...</p>
                                    `}
                                </div>
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

                    <!-- Scroll to bottom button -->
                    ${(!isUserAtBottom || hasNewMessages) && messages.length > 0 && html`
                        <button
                            onClick=${() => scrollToBottom(true)}
                            class="scroll-to-bottom-btn ${hasNewMessages ? 'has-new' : ''}"
                            title="Scroll to bottom"
                        >
                            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 14l-7 7m0 0l-7-7m7 7V3" />
                            </svg>
                            ${hasNewMessages && html`
                                <span class="new-messages-indicator"></span>
                            `}
                        </button>
                    `}
                </div>

                <!-- Input -->
                <${ChatInput}
                    onSend=${sendPrompt}
                    onCancel=${cancelPrompt}
                    disabled=${!connected || !activeSessionId}
                    isStreaming=${isStreaming}
                    isReadOnly=${sessionInfo?.isReadOnly}
                    predefinedPrompts=${predefinedPrompts}
                    inputRef=${chatInputRef}
                    noSession=${!activeSessionId}
                    sessionId=${activeSessionId}
                    draft=${currentDraft}
                    onDraftChange=${updateDraft}
                    sessionDraftsRef=${sessionDraftsRef}
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
        threshold = 50,           // Minimum distance to trigger swipe
        maxVertical = 100,        // Maximum vertical movement allowed
        edgeWidth = 30,           // Width of edge zone where swipe starts (mobile)
        onEdgeSwipeRight = null,  // Callback for right swipe from left edge (e.g., open sidebar)
        onEdgeSwipeLeft = null    // Callback for left swipe from right edge
    } = options;

    const touchStartRef = useRef(null);

    useEffect(() => {
        const element = ref.current;
        if (!element) return;

        const handleTouchStart = (e) => {
            const touch = e.touches[0];
            // Track if touch begins near the edge (for mobile)
            const isNearLeftEdge = touch.clientX < edgeWidth;
            const isNearRightEdge = touch.clientX > window.innerWidth - edgeWidth;

            touchStartRef.current = {
                x: touch.clientX,
                y: touch.clientY,
                time: Date.now(),
                isLeftEdge: isNearLeftEdge,
                isRightEdge: isNearRightEdge
            };
        };

        const handleTouchEnd = (e) => {
            if (!touchStartRef.current) return;

            const touch = e.changedTouches[0];
            const deltaX = touch.clientX - touchStartRef.current.x;
            const deltaY = touch.clientY - touchStartRef.current.y;
            const duration = Date.now() - touchStartRef.current.time;
            const { isLeftEdge, isRightEdge } = touchStartRef.current;

            // Only trigger if:
            // - Horizontal movement exceeds threshold
            // - Vertical movement is within limit (not scrolling)
            // - Gesture was reasonably quick (under 500ms)
            if (Math.abs(deltaX) >= threshold &&
                Math.abs(deltaY) <= maxVertical &&
                duration < 500) {

                if (deltaX > 0) {
                    // Swiped right
                    if (isLeftEdge && onEdgeSwipeRight) {
                        // Edge swipe from left -> open sidebar
                        onEdgeSwipeRight();
                    } else {
                        // Regular swipe right -> go to previous session
                        onSwipeRight?.();
                    }
                } else {
                    // Swiped left
                    if (isRightEdge && onEdgeSwipeLeft) {
                        // Edge swipe from right -> custom action
                        onEdgeSwipeLeft();
                    } else {
                        // Regular swipe left -> go to next session
                        onSwipeLeft?.();
                    }
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
    }, [ref, onSwipeLeft, onSwipeRight, onEdgeSwipeRight, onEdgeSwipeLeft, threshold, maxVertical, edgeWidth]);
}

// =============================================================================
// Mount Application
// =============================================================================

render(html`<${App} />`, document.getElementById('app'));
