// Mitto Web Interface - WebSocket Hook
// Manages WebSocket connections for global events and per-session communication

const { useState, useEffect, useRef, useCallback, useMemo } = window.preact;

import {
  ROLE_USER,
  ROLE_AGENT,
  ROLE_THOUGHT,
  ROLE_TOOL,
  ROLE_ERROR,
  ROLE_SYSTEM,
  INITIAL_EVENTS_LIMIT,
  convertEventsToMessages,
  limitMessages,
  mergeMessagesWithSync,
  updateGlobalWorkingDir,
  getGlobalWorkingDir,
  generatePromptId,
  savePendingPrompt,
  removePendingPrompt,
  getPendingPromptsForSession,
  cleanupExpiredPrompts,
} from "../lib.js";

import {
  getLastActiveSessionId,
  setLastActiveSessionId,
  getLastSeenSeq,
  setLastSeenSeq,
} from "../utils/storage.js";

import { playAgentCompletedSound } from "../utils/audio.js";

import {
  secureFetch,
  authFetch,
  checkAuth,
  redirectToLogin,
} from "../utils/csrf.js";

import { apiUrl, wsUrl, getApiPrefix } from "../utils/api.js";

// Import WebSocket utilities (H1, M1, M2 implementations)
import {
  updateLastSeenSeqIfHigher,
  createSeqTracker,
  isSeqDuplicate as isSeqDuplicateUtil,
  markSeqSeen as markSeqSeenUtil,
  calculateReconnectDelay,
} from "../utils/websocket.js";

// Time threshold (in ms) for considering the session potentially stale
// If the page has been hidden for longer than this, we do an explicit auth check
// before trying to reconnect. The server session expires after 24 hours.
const STALE_THRESHOLD_MS = 60 * 60 * 1000; // 1 hour

// Keepalive configuration for detecting zombie WebSocket connections
// On mobile, connections can appear open but be dead (zombie connections)
const KEEPALIVE_INTERVAL_MS = 25000; // Send keepalive every 25 seconds
const KEEPALIVE_TIMEOUT_MS = 10000; // Consider connection unhealthy if no response in 10 seconds
const KEEPALIVE_MAX_MISSED = 2; // Force reconnect after 2 missed keepalives

/**
 * Check if the user is authenticated.
 * If not authenticated, redirects to login page.
 * @returns {Promise<boolean>} True if authenticated, never returns false (redirects instead)
 */
async function checkAuthOrRedirect() {
  try {
    // Quick auth check using the config endpoint
    const response = await fetch(apiUrl("/api/config"), {
      credentials: "same-origin",
    });
    checkAuth(response); // This will redirect if 401
    return response.ok;
  } catch (err) {
    console.error("Auth check failed:", err);
    return false;
  }
}

/**
 * Check authentication with retry logic for network errors.
 * After prolonged phone sleep, the network may take a moment to recover.
 * This function retries a few times before giving up.
 *
 * @param {number} maxRetries - Maximum number of retries (default: 3)
 * @param {number} retryDelay - Delay between retries in ms (default: 500)
 * @returns {Promise<{authenticated: boolean, networkError: boolean}>}
 *   - authenticated: true if the session is valid
 *   - networkError: true if all retries failed due to network errors
 */
async function checkAuthWithRetry(maxRetries = 3, retryDelay = 500) {
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    try {
      const response = await fetch(apiUrl("/api/config"), {
        credentials: "same-origin",
      });

      // Got a response - check if authenticated
      if (response.status === 401) {
        console.log(
          "Auth check: session expired or invalid (401), redirecting to login",
        );
        redirectToLogin();
        return { authenticated: false, networkError: false };
      }

      if (response.ok) {
        return { authenticated: true, networkError: false };
      }

      // Other error status - treat as auth failure if persistent
      console.warn(`Auth check returned status ${response.status}`);
      if (attempt < maxRetries) {
        await new Promise((r) => setTimeout(r, retryDelay));
        continue;
      }
      return { authenticated: false, networkError: false };
    } catch (err) {
      // Network error - retry if we have attempts left
      console.warn(
        `Auth check network error (attempt ${attempt + 1}/${maxRetries + 1}):`,
        err.message,
      );
      if (attempt < maxRetries) {
        await new Promise((r) => setTimeout(r, retryDelay));
        continue;
      }
      // All retries exhausted
      return { authenticated: false, networkError: true };
    }
  }
  // Should not reach here
  return { authenticated: false, networkError: true };
}

/**
 * WebSocket Hook with Per-Session WebSocket Support
 * Manages both global events WebSocket and per-session WebSockets
 */
export function useWebSocket() {
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

  // Queue length for the active session
  const [queueLength, setQueueLength] = useState(0);

  // Queue messages for the active session
  // Array of { id, message, title, queued_at }
  const [queueMessages, setQueueMessages] = useState([]);

  // Queue configuration for the active session
  // { enabled: bool, max_size: int, delay_seconds: int }
  const [queueConfig, setQueueConfig] = useState({
    enabled: true,
    max_size: 10,
    delay_seconds: 0,
  });

  const eventsWsRef = useRef(null);
  const reconnectRef = useRef(null);
  const activeSessionIdRef = useRef(activeSessionId);
  const sessionWsRefs = useRef({}); // { sessionId: WebSocket }
  const sessionReconnectRefs = useRef({}); // { sessionId: timeoutId } for session reconnection
  const sessionsRef = useRef(sessions); // For accessing sessions in callbacks
  const workspacesRef = useRef(workspaces); // For accessing workspaces in callbacks
  const retryPendingPromptsRef = useRef(null); // Ref to retry function (set later to avoid circular deps)
  const resolvePendingSendsRef = useRef(null); // Ref to resolve function (set later to avoid circular deps)
  // Track pending send operations for ACK handling
  // { promptId: { resolve, reject, timeoutId } }
  const pendingSendsRef = useRef({});

  // M2: Track reconnection attempts for exponential backoff
  // { sessionId: attemptCount } for session WebSockets
  const sessionReconnectAttemptsRef = useRef({});
  // Attempt count for global events WebSocket
  const eventsReconnectAttemptRef = useRef(0);

  // M1 fix: Track seen sequence numbers per session for client-side deduplication
  // { sessionId: { highestSeq: number, recentSeqs: Set<number> } }
  // Uses utility functions from utils/websocket.js for testability
  const seenSeqsRef = useRef({});

  /**
   * Get or create a seq tracker for a session.
   * @param {string} sessionId - The session ID
   * @returns {{highestSeq: number, recentSeqs: Set<number>}}
   */
  const getSeqTracker = useCallback((sessionId) => {
    if (!seenSeqsRef.current[sessionId]) {
      seenSeqsRef.current[sessionId] = createSeqTracker();
    }
    return seenSeqsRef.current[sessionId];
  }, []);

  /**
   * Check if a sequence number has already been seen for a session.
   * Wrapper around utility function that manages per-session state.
   */
  const isSeqDuplicate = useCallback((sessionId, seq, lastMessageSeq) => {
    const tracker = getSeqTracker(sessionId);
    const isDuplicate = isSeqDuplicateUtil(tracker, seq, lastMessageSeq);
    if (isDuplicate) {
      console.log(`M1 dedup: Skipping duplicate seq ${seq} for session ${sessionId}`);
    }
    return isDuplicate;
  }, [getSeqTracker]);

  /**
   * Mark a sequence number as seen for a session.
   * Wrapper around utility function that manages per-session state.
   */
  const markSeqSeen = useCallback((sessionId, seq) => {
    const tracker = getSeqTracker(sessionId);
    markSeqSeenUtil(tracker, seq);
  }, [getSeqTracker]);

  /**
   * Clear seen sequences for a session (e.g., when session is deleted or reset).
   */
  const clearSeenSeqs = useCallback((sessionId) => {
    delete seenSeqsRef.current[sessionId];
  }, []);

  // Track if this is a reconnection (vs initial connection)
  const wasConnectedRef = useRef(false);

  // Track when the page was last hidden (for staleness detection on mobile)
  const lastHiddenTimeRef = useRef(null);

  // Keepalive tracking for detecting zombie connections
  // { sessionId: { intervalId, lastAckTime, missedCount, pendingKeepalive } }
  const keepaliveRef = useRef({});

  // Fetch workspaces and ACP servers
  const fetchWorkspaces = useCallback(async () => {
    try {
      const response = await authFetch(apiUrl("/api/workspaces"));
      if (response.ok) {
        const data = await response.json();
        setWorkspaces(data.workspaces || []);
        setAcpServers(data.acp_servers || []);
      }
    } catch (err) {
      console.error("Failed to fetch workspaces:", err);
    }
  }, []);

  // Fetch workspaces on mount
  useEffect(() => {
    fetchWorkspaces();
  }, [fetchWorkspaces]);

  // Add a new workspace
  const addWorkspace = useCallback(
    async (workingDir, acpServer) => {
      try {
        const response = await secureFetch(apiUrl("/api/workspaces"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            working_dir: workingDir,
            acp_server: acpServer,
          }),
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
        console.error("Failed to add workspace:", err);
        return { error: err.message || "Failed to add workspace" };
      }
    },
    [fetchWorkspaces],
  );

  // Remove a workspace
  const removeWorkspace = useCallback(
    async (workingDir) => {
      try {
        const response = await secureFetch(
          apiUrl(`/api/workspaces?dir=${encodeURIComponent(workingDir)}`),
          {
            method: "DELETE",
          },
        );

        if (!response.ok) {
          // Try to parse as JSON for structured errors
          const contentType = response.headers.get("content-type");
          if (contentType && contentType.includes("application/json")) {
            const errorData = await response.json();
            const error = new Error(
              errorData.message || "Failed to remove workspace",
            );
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
        console.error("Failed to remove workspace:", err);
        throw err;
      }
    },
    [fetchWorkspaces],
  );

  // Fetch queue messages for the active session
  const fetchQueueMessages = useCallback(async () => {
    if (!activeSessionId) {
      setQueueMessages([]);
      return;
    }
    try {
      const response = await authFetch(
        apiUrl(`/api/sessions/${activeSessionId}/queue`),
      );
      if (response.ok) {
        const data = await response.json();
        setQueueMessages(data.messages || []);
        setQueueLength(data.count || 0);
      }
    } catch (err) {
      console.error("Failed to fetch queue messages:", err);
    }
  }, [activeSessionId]);

  // Fetch queue messages when active session changes
  useEffect(() => {
    if (activeSessionId) {
      fetchQueueMessages();
    } else {
      // Clear queue state when no session is active
      setQueueMessages([]);
      setQueueLength(0);
    }
  }, [activeSessionId, fetchQueueMessages]);

  // Delete a message from the queue
  const deleteQueueMessage = useCallback(
    async (messageId) => {
      if (!activeSessionId || !messageId) return false;
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${activeSessionId}/queue/${messageId}`),
          { method: "DELETE" },
        );
        if (response.ok || response.status === 204) {
          // Refresh queue messages after deletion
          await fetchQueueMessages();
          return true;
        }
        console.error("Failed to delete queue message:", response.status);
        return false;
      } catch (err) {
        console.error("Failed to delete queue message:", err);
        return false;
      }
    },
    [activeSessionId, fetchQueueMessages],
  );

  // Add a message to the queue
  const addToQueue = useCallback(
    async (message, imageIds = []) => {
      if (!activeSessionId || !message?.trim()) return { success: false };
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${activeSessionId}/queue`),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              message: message.trim(),
              image_ids: imageIds,
            }),
          },
        );
        if (response.ok || response.status === 201) {
          // Parse response to get the message ID
          const data = await response.json().catch(() => ({}));
          // Refresh queue messages after addition
          await fetchQueueMessages();
          return { success: true, messageId: data.id };
        }
        // Handle queue full error
        if (response.status === 409) {
          const data = await response.json().catch(() => ({}));
          return {
            success: false,
            error: data.error || "queue_full",
            message: data.message,
          };
        }
        console.error("Failed to add to queue:", response.status);
        return { success: false, error: "request_failed" };
      } catch (err) {
        console.error("Failed to add to queue:", err);
        return { success: false, error: "request_failed" };
      }
    },
    [activeSessionId, fetchQueueMessages],
  );

  // Move a message up or down in the queue
  const moveQueueMessage = useCallback(
    async (messageId, direction) => {
      if (!activeSessionId || !messageId) return false;
      if (direction !== "up" && direction !== "down") return false;
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${activeSessionId}/queue/${messageId}/move`),
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ direction }),
          },
        );
        if (response.ok) {
          // The response contains the updated queue, update local state
          const data = await response.json();
          setQueueMessages(data.messages || []);
          setQueueLength(data.count || 0);
          return true;
        }
        console.error("Failed to move queue message:", response.status);
        return false;
      } catch (err) {
        console.error("Failed to move queue message:", err);
        return false;
      }
    },
    [activeSessionId],
  );

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
    storedSessions.forEach((s) => {
      if (s.working_dir) {
        updates[s.session_id] = s.working_dir;
        workingDirMapRef.current[s.session_id] = s.working_dir;
      }
    });
    if (Object.keys(updates).length > 0) {
      setWorkingDirMap((prev) => ({ ...prev, ...updates }));
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

  // Get action buttons for active session
  const actionButtons = useMemo(() => {
    if (!activeSessionId || !sessions[activeSessionId]) {
      console.log("[ActionButtons] No active session for buttons");
      return [];
    }
    const buttons = sessions[activeSessionId].actionButtons || [];
    if (buttons.length > 0) {
      console.log("[ActionButtons] Returning buttons for render:", {
        sessionId: activeSessionId,
        buttonCount: buttons.length,
        buttons: buttons.map(b => b.label),
      });
    }
    return buttons;
  }, [sessions, activeSessionId]);

  // Get all active sessions as array for sidebar
  // Note: Not using useMemo to ensure working_dir is always up-to-date
  const activeSessions = Object.entries(sessions).map(([id, data]) => {
    // Find the most recent user message timestamp
    const userMessages = (data.messages || []).filter(
      (m) => m.role === ROLE_USER,
    );
    const lastUserMsgTime =
      userMessages.length > 0
        ? new Date(
            Math.max(...userMessages.map((m) => m.timestamp || 0)),
          ).toISOString()
        : null;
    // Get working_dir from multiple sources (in order of priority):
    // 1. Global map (populated from API responses, most reliable)
    // 2. workingDirMap state (populated from storedSessions and WebSocket connected messages)
    // 3. storedSessions (original API response)
    // 4. session info (set by switchSession or WebSocket connected handler)
    const storedSession = storedSessions.find((s) => s.session_id === id);
    const workingDir =
      getGlobalWorkingDir(id) ||
      workingDirMap[id] ||
      storedSession?.working_dir ||
      data.info?.working_dir ||
      "";
    return {
      session_id: id,
      name: data.info?.name || "New conversation",
      acp_server: data.info?.acp_server || "",
      working_dir: workingDir,
      created_at: data.info?.created_at || new Date().toISOString(),
      updated_at: data.info?.updated_at || new Date().toISOString(),
      last_user_message_at: lastUserMsgTime || data.info?.last_user_message_at,
      status: "active",
      isActive: true,
      isStreaming: data.isStreaming || false,
      messageCount: data.messages?.length || 0,
    };
  });

  // Handle messages from per-session WebSocket
  const handleSessionMessage = useCallback((sessionId, msg) => {
    switch (msg.type) {
      case "connected":
        // Session WebSocket connected, update session info
        // Note: working_dir should come from the WebSocket message, but we also
        // preserve any existing value in case of race conditions with switchSession

        // Store working_dir in both ref and state
        if (msg.data.working_dir) {
          workingDirMapRef.current[sessionId] = msg.data.working_dir;
          setWorkingDirMap((prev) => ({
            ...prev,
            [sessionId]: msg.data.working_dir,
          }));
        }

        // Update queue length from server
        if (msg.data.queue_length !== undefined) {
          setQueueLength(msg.data.queue_length);
        }

        // Update queue configuration from server
        if (msg.data.queue_config) {
          setQueueConfig(msg.data.queue_config);
        }

        setSessions((prev) => {
          const session = prev[sessionId] || { messages: [], info: {} };
          // Prefer the WebSocket message value, then ref, then existing value
          const newWorkingDir =
            msg.data.working_dir ||
            workingDirMapRef.current[sessionId] ||
            session.info?.working_dir ||
            "";
          return {
            ...prev,
            [sessionId]: {
              ...session,
              info: {
                ...session.info,
                session_id: sessionId,
                name: msg.data.name || session.info?.name || "New conversation",
                acp_server: msg.data.acp_server || session.info?.acp_server,
                working_dir: newWorkingDir,
                created_at: msg.data.created_at || session.info?.created_at,
                status: msg.data.status || "active",
                runner_type: msg.data.runner_type || session.info?.runner_type,
                runner_restricted: msg.data.runner_restricted ?? session.info?.runner_restricted,
              },
              isStreaming: msg.data.is_prompting || false,
            },
          };
        });
        break;

      case "agent_message": {
        const msgSeq = msg.data.seq;
        console.log(
          "agent_message received:",
          sessionId,
          msg.data.html?.substring(0, 50) + "...",
          "is_prompting:",
          msg.data.is_prompting,
          "seq:",
          msgSeq,
        );

        // Agent is responding - this proves any pending prompts were received.
        // Resolve pending sends to prevent false "delivery not confirmed" errors on mobile.
        if (resolvePendingSendsRef.current) {
          resolvePendingSendsRef.current(sessionId);
        }

        // Update lastSeenSeq immediately so reconnection sync is up-to-date
        // even if client disconnects mid-stream (H1 fix)
        updateLastSeenSeqIfHigher(sessionId, msgSeq);

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        // Frontend only needs to coalesce chunks with the same seq (streaming continuation).
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          let messages = [...session.messages];
          const last = messages[messages.length - 1];

          // M1 fix: Check for duplicate events (but allow same-seq for coalescing)
          if (isSeqDuplicate(sessionId, msgSeq, last?.seq)) {
            return prev; // Skip duplicate
          }

          // Check if we should append to existing message:
          // - Same seq means it's a continuation of the same logical message
          // - Or if last message is incomplete agent message (backward compat)
          const sameSeq = msgSeq && last?.seq === msgSeq;
          if (last && last.role === ROLE_AGENT && !last.complete && (sameSeq || !msgSeq)) {
            messages[messages.length - 1] = {
              ...last,
              html: (last.html || "") + msg.data.html,
            };
          } else {
            // New message - mark seq as seen
            markSeqSeen(sessionId, msgSeq);
            messages.push({
              role: ROLE_AGENT,
              html: msg.data.html,
              complete: false,
              timestamp: Date.now(),
              seq: msgSeq,
            });
            messages = limitMessages(messages);
          }
          const isPrompting = msg.data.is_prompting ?? true;
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: isPrompting },
          };
        });
        break;
      }

      case "agent_thought": {
        const msgSeq = msg.data.seq;

        // Agent is responding - this proves any pending prompts were received.
        // Resolve pending sends to prevent false "delivery not confirmed" errors on mobile.
        if (resolvePendingSendsRef.current) {
          resolvePendingSendsRef.current(sessionId);
        }

        // Update lastSeenSeq immediately so reconnection sync is up-to-date (H1 fix)
        updateLastSeenSeqIfHigher(sessionId, msgSeq);

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          let messages = [...session.messages];
          const last = messages[messages.length - 1];

          // M1 fix: Check for duplicate events (but allow same-seq for coalescing)
          if (isSeqDuplicate(sessionId, msgSeq, last?.seq)) {
            return prev; // Skip duplicate
          }

          // Check if we should append to existing thought:
          // - Same seq means it's a continuation of the same logical thought
          // - Or if last message is incomplete thought (backward compat)
          const sameSeq = msgSeq && last?.seq === msgSeq;
          if (last && last.role === ROLE_THOUGHT && !last.complete && (sameSeq || !msgSeq)) {
            messages[messages.length - 1] = {
              ...last,
              text: (last.text || "") + msg.data.text,
            };
          } else {
            // New thought - mark seq as seen
            markSeqSeen(sessionId, msgSeq);
            messages.push({
              role: ROLE_THOUGHT,
              text: msg.data.text,
              complete: false,
              timestamp: Date.now(),
              seq: msgSeq,
            });
            messages = limitMessages(messages);
          }
          const isPrompting = msg.data.is_prompting ?? true;
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: isPrompting },
          };
        });
        break;
      }

      case "tool_call": {
        const msgSeq = msg.data.seq;
        // Update lastSeenSeq immediately so reconnection sync is up-to-date (H1 fix)
        updateLastSeenSeqIfHigher(sessionId, msgSeq);

        // M1 fix: Check for duplicate events
        if (isSeqDuplicate(sessionId, msgSeq, null)) {
          break; // Skip duplicate
        }

        // Mark seq as seen
        markSeqSeen(sessionId, msgSeq);

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;

          const messages = limitMessages([
            ...session.messages,
            {
              role: ROLE_TOOL,
              id: msg.data.id,
              title: msg.data.title,
              status: msg.data.status,
              timestamp: Date.now(),
              seq: msgSeq,
            },
          ]);
          const isPrompting = msg.data.is_prompting ?? true;
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: isPrompting },
          };
        });
        break;
      }

      case "tool_update":
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          const messages = [...session.messages];
          const idx = messages.findLastIndex(
            (m) => m.role === ROLE_TOOL && m.id === msg.data.id,
          );
          if (idx >= 0 && msg.data.status) {
            messages[idx] = { ...messages[idx], status: msg.data.status };
          }
          // Only set isStreaming if is_prompting is true (agent is responding to a user prompt)
          const isPrompting = msg.data.is_prompting ?? true;
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: isPrompting },
          };
        });
        break;

      case "action_buttons":
        // Store action buttons from async follow-up analysis
        // These are suggested response options generated by analyzing the agent's message
        console.log("[ActionButtons] Received action_buttons message:", {
          sessionId,
          buttons: msg.data.buttons,
          buttonCount: msg.data.buttons?.length || 0,
        });
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) {
            console.warn("[ActionButtons] Session not found:", sessionId);
            return prev;
          }

          // Ignore buttons if the session is currently streaming
          // (user has already sent a new message or agent is responding again)
          if (session.isStreaming) {
            console.log(
              "[ActionButtons] Ignoring - session is streaming:",
              sessionId,
            );
            return prev;
          }

          console.log("[ActionButtons] Storing buttons for session:", sessionId);
          return {
            ...prev,
            [sessionId]: {
              ...session,
              actionButtons: msg.data.buttons || [],
            },
          };
        });
        break;

      case "prompt_complete": {
        // Check if this is a background session completing (not the active one)
        const currentSession = sessionsRef.current[sessionId];
        const isBackgroundSession = sessionId !== activeSessionIdRef.current;
        const wasStreaming = currentSession?.isStreaming;

        setSessions((prev) => {
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
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: false },
          };
        });

        // Update lastSeenSeq from event_count so we can sync properly on reconnect
        // The server sends event_count with prompt_complete to indicate the current position
        if (msg.data.event_count) {
          setLastSeenSeq(sessionId, msg.data.event_count);
        }

        // Notify about background session completion
        if (isBackgroundSession && wasStreaming) {
          const sessionName = currentSession?.info?.name || "Conversation";
          setBackgroundCompletion({
            sessionId,
            sessionName,
            timestamp: Date.now(),
          });
        }

        // Play notification sound if enabled (macOS only)
        console.log("[prompt_complete] wasStreaming:", wasStreaming, "soundEnabled:", window.mittoAgentCompletedSoundEnabled, "isBackgroundSession:", isBackgroundSession);
        if (wasStreaming && window.mittoAgentCompletedSoundEnabled) {
          console.log("[prompt_complete] Playing notification sound");
          playAgentCompletedSound();
        }
        break;
      }

      case "error": {
        // If this error includes a prompt_id, reject the pending send for that prompt
        // This cancels the send timeout and prevents duplicate error messages
        const errorPromptId = msg.data.prompt_id;
        if (errorPromptId) {
          const pending = pendingSendsRef.current[errorPromptId];
          if (pending) {
            clearTimeout(pending.timeoutId);
            pending.reject(new Error(msg.data.message));
            delete pendingSendsRef.current[errorPromptId];
            // Remove from pending prompts in localStorage
            removePendingPrompt(errorPromptId);
          }
        }

        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          const messages = limitMessages([
            ...session.messages,
            {
              role: ROLE_ERROR,
              text: msg.data.message,
              timestamp: Date.now(),
            },
          ]);
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: false },
          };
        });
        break;
      }

      case "keepalive_ack": {
        // Server responded to our keepalive - connection is healthy
        const keepalive = keepaliveRef.current[sessionId];
        if (keepalive) {
          keepalive.lastAckTime = Date.now();
          keepalive.missedCount = 0;
          keepalive.pendingKeepalive = false;
        }
        break;
      }

      case "session_renamed":
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          return {
            ...prev,
            [sessionId]: {
              ...session,
              info: { ...session.info, name: msg.data.name },
            },
          };
        });
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === sessionId ? { ...s, name: msg.data.name } : s,
          ),
        );
        break;

      case "session_reset":
        // Session was forcefully reset due to unresponsive agent
        console.log("Session forcefully reset:", sessionId);
        // The server also sends prompt_complete, so isStreaming will be reset
        // Add a system message to inform the user
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          const messages = limitMessages([
            ...session.messages,
            {
              role: ROLE_ERROR,
              text: "Session was forcefully reset due to unresponsive agent.",
              timestamp: Date.now(),
            },
          ]);
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: false },
          };
        });
        break;

      case "session_sync": {
        // DEPRECATED: Use events_loaded instead
        // Handle incremental sync response (kept for backward compatibility)
        const events = msg.data.events || [];
        const newMessages = convertEventsToMessages(events, { sessionId, apiPrefix: getApiPrefix() });
        const lastSeq =
          events.length > 0
            ? Math.max(...events.map((e) => e.seq || 0))
            : msg.data.after_seq;
        const isPrompting = msg.data.is_prompting || false;

        console.log("session_sync received (deprecated):", {
          sessionId,
          afterSeq: msg.data.after_seq,
          eventCount: events.length,
        });

        setLastSeenSeq(sessionId, lastSeq);

        setSessions((prev) => {
          const session = prev[sessionId] || { messages: [], info: {} };
          const existingMessages = session.messages;
          const mergedMessages = mergeMessagesWithSync(
            existingMessages,
            newMessages,
          );

          return {
            ...prev,
            [sessionId]: {
              ...session,
              messages: limitMessages(mergedMessages),
              lastSeq,
              isStreaming: isPrompting,
              info: {
                ...session.info,
                name: msg.data.name || session.info?.name,
                status: msg.data.status || session.info?.status,
              },
            },
          };
        });
        break;
      }

      case "events_loaded": {
        // Handle events_loaded response from load_events request
        // This is the new WebSocket-only approach for loading events
        const events = msg.data.events || [];
        const isPrepend = msg.data.prepend || false;
        const hasMore = msg.data.has_more || false;
        const firstSeq = msg.data.first_seq || 0;
        const lastSeq = msg.data.last_seq || 0;
        const isPrompting = msg.data.is_prompting || false;
        const totalCount = msg.data.total_count || 0;

        console.log("events_loaded received:", {
          sessionId,
          eventCount: events.length,
          isPrepend,
          hasMore,
          firstSeq,
          lastSeq,
          isPrompting,
          totalCount,
        });

        // Check if this was a stale sync request that returned no events
        // but the session has events on the server. This happens when:
        // 1. localStorage has a stale lastSeenSeq (higher than current event count)
        // 2. Sync request with after_seq returns 0 events
        // 3. But session actually has events (total_count > 0)
        // In this case, clear the stale seq and request a fresh initial load.
        const storedSeq = getLastSeenSeq(sessionId);
        const isStaleSync = !isPrepend && events.length === 0 && totalCount > 0 && storedSeq > 0;

        if (isStaleSync) {
          console.log(`Stale sync detected for ${sessionId}: stored seq ${storedSeq}, but session has ${totalCount} events. Requesting fresh load.`);
          // Clear stale seq
          setLastSeenSeq(sessionId, 0);

          // Request fresh initial load
          const ws = sessionWsRefs.current[sessionId];
          if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(
              JSON.stringify({
                type: "load_events",
                data: { limit: INITIAL_EVENTS_LIMIT },
              }),
            );
          }
          // Don't update state - wait for the fresh load response
          break;
        }

        // Convert events to messages
        const newMessages = convertEventsToMessages(events, {
          sessionId,
          apiPrefix: getApiPrefix(),
        });

        // Update lastSeenSeq for non-prepend loads (initial load and sync)
        if (!isPrepend && lastSeq > 0) {
          setLastSeenSeq(sessionId, lastSeq);
        }

        // M1 fix: Mark all loaded event seqs as seen to prevent duplicates
        // This is important for sync after reconnect where we might receive
        // events that overlap with what we already have
        for (const event of events) {
          if (event.seq) {
            markSeqSeen(sessionId, event.seq);
          }
        }

        setSessions((prev) => {
          const session = prev[sessionId] || { messages: [], info: {} };
          let messages;

          if (isPrepend) {
            // Load more (older events) - prepend to existing messages
            // No deduplication needed - server guarantees no duplicates
            messages = [...newMessages, ...session.messages];
          } else if (session.messages.length === 0) {
            // Initial load - just use the new messages
            messages = newMessages;
          } else {
            // Sync after reconnect - merge with deduplication
            // Use mergeMessagesWithSync to handle cases where:
            // 1. lastSeenSeq in localStorage is stale (visibility change during streaming)
            // 2. Messages already in UI have seq values from streaming
            // 3. Server returns events that overlap with what's already displayed
            messages = mergeMessagesWithSync(session.messages, newMessages);
          }

          return {
            ...prev,
            [sessionId]: {
              ...session,
              messages: limitMessages(messages),
              isStreaming: isPrompting,
              hasMoreMessages: hasMore,
              firstLoadedSeq: isPrepend ? firstSeq : session.firstLoadedSeq || firstSeq,
              // Flag to indicate this is a fresh load - used for instant scroll positioning
              justLoaded: !isPrepend && session.messages.length === 0,
            },
          };
        });
        break;
      }

      case "prompt_received":
        // Acknowledgment that the prompt was received and persisted by the server
        // Remove from pending queue - the message is now safely stored
        if (msg.data.prompt_id) {
          removePendingPrompt(msg.data.prompt_id);
          console.log("Prompt acknowledged:", msg.data.prompt_id);
          // Resolve any pending send promise
          const pending = pendingSendsRef.current[msg.data.prompt_id];
          if (pending) {
            clearTimeout(pending.timeoutId);
            pending.resolve({ success: true, promptId: msg.data.prompt_id });
            delete pendingSendsRef.current[msg.data.prompt_id];
          }
        }
        break;

      case "user_prompt": {
        // Broadcast notification that a user prompt was sent
        // This is sent to ALL connected clients for multi-browser sync
        const {
          seq,
          is_mine,
          prompt_id,
          message,
          image_ids,
          sender_id,
          is_prompting,
        } = msg.data;
        console.log("user_prompt received:", {
          seq,
          is_mine,
          prompt_id,
          sender_id,
          is_prompting,
          message: message?.substring(0, 50),
        });

        // Update lastSeenSeq immediately so reconnection sync is up-to-date (H1 fix)
        updateLastSeenSeqIfHigher(sessionId, seq);

        // M1 fix: Mark seq as seen (for our own prompts, we mark after confirmation)
        if (!is_mine && seq) {
          // For other clients' prompts, check for duplicates
          if (isSeqDuplicate(sessionId, seq, null)) {
            console.log("M1 dedup: Skipping duplicate user_prompt seq", seq);
            break; // Skip duplicate
          }
          markSeqSeen(sessionId, seq);
        }

        // Set isStreaming = true immediately when a prompt is sent
        // This shows the Stop button right away, not waiting for agent response
        if (is_prompting) {
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;
            if (session.isStreaming) return prev; // Already streaming
            return {
              ...prev,
              [sessionId]: { ...session, isStreaming: true },
            };
          });
        }

        if (is_mine) {
          // This client sent the prompt - it's already in our UI
          // Just remove from pending queue (same as prompt_received)
          // Also update the seq on the existing message if we have it
          if (prompt_id) {
            removePendingPrompt(prompt_id);
            console.log("Own prompt confirmed:", prompt_id, "seq:", seq);
            // M1 fix: Mark seq as seen now that it's confirmed
            if (seq) {
              markSeqSeen(sessionId, seq);
            }
            // Update the seq on the existing user message
            if (seq) {
              setSessions((prev) => {
                const session = prev[sessionId];
                if (!session) return prev;
                const messages = session.messages.map((m) => {
                  // Find the user message we just sent (by content match)
                  if (m.role === ROLE_USER && !m.seq && m.text === message) {
                    return { ...m, seq };
                  }
                  return m;
                });
                return { ...prev, [sessionId]: { ...session, messages } };
              });
            }
            // Resolve any pending send promise
            const pending = pendingSendsRef.current[prompt_id];
            if (pending) {
              clearTimeout(pending.timeoutId);
              pending.resolve({ success: true, promptId: prompt_id });
              delete pendingSendsRef.current[prompt_id];
            }
          }
        } else {
          // Another client sent this prompt - add to our UI
          // But first check if we have a pending send for this prompt_id
          // This can happen if the WebSocket reconnected and got a new clientID,
          // causing is_mine to be false even though we sent the prompt
          if (prompt_id) {
            const pending = pendingSendsRef.current[prompt_id];
            if (pending) {
              // This is actually our prompt, but is_mine is false due to WebSocket reconnection
              console.log(
                "Own prompt confirmed (after reconnect):",
                prompt_id,
                "seq:",
                seq,
              );
              removePendingPrompt(prompt_id);
              clearTimeout(pending.timeoutId);
              pending.resolve({ success: true, promptId: prompt_id });
              delete pendingSendsRef.current[prompt_id];
              // Update the seq on the existing user message
              if (seq) {
                setSessions((prev) => {
                  const session = prev[sessionId];
                  if (!session) return prev;
                  const messages = session.messages.map((m) => {
                    // Find the user message we just sent (by content match)
                    if (m.role === ROLE_USER && !m.seq && m.text === message) {
                      return { ...m, seq };
                    }
                    return m;
                  });
                  return { ...prev, [sessionId]: { ...session, messages } };
                });
              }
              break; // Don't add duplicate message
            }
          }

          // Check if this message already exists (by seq or content)
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;

            // Check if this message already exists (by seq if available, or by content)
            // Note: We always check content because a prompt retry can result in a new seq
            // for the same message content (server assigns new seq on each persist).
            const alreadyExists = session.messages.some((m) => {
              if (m.role !== ROLE_USER) return false;
              // If seq matches exactly, it's the same message
              if (seq && m.seq && m.seq === seq) return true;
              // Also check content - handles case where retry created new seq for same message
              const messageContent = message?.substring(0, 200) || "";
              return (m.text || "").substring(0, 200) === messageContent;
            });

            if (alreadyExists) {
              console.log("Skipping duplicate user_prompt:", prompt_id, "seq:", seq);
              return prev;
            }

            console.log(
              "Prompt from another client:",
              prompt_id,
              "seq:",
              seq,
              "adding to UI",
            );
            let messages = [...session.messages];
            // Mark any previous streaming message as complete
            const last = messages[messages.length - 1];
            if (
              last &&
              !last.complete &&
              (last.role === ROLE_AGENT || last.role === ROLE_THOUGHT)
            ) {
              messages[messages.length - 1] = { ...last, complete: true };
            }
            // Add the user message from the other client
            const userMessage = {
              role: ROLE_USER,
              text: message,
              timestamp: Date.now(),
              fromOtherClient: true,
              seq, // Include seq for ordering and deduplication
            };
            // Add image references if present (we don't have the actual image data)
            if (image_ids && image_ids.length > 0) {
              userMessage.imageIds = image_ids;
            }
            messages = limitMessages([...messages, userMessage]);
            return { ...prev, [sessionId]: { ...session, messages } };
          });
        }
        break;
      }

      case "permission":
        console.log("Permission requested:", msg.data);
        break;

      case "queue_updated":
        // Server notifies us about queue state changes
        if (msg.data?.queue_length !== undefined) {
          setQueueLength(msg.data.queue_length);
          console.log(
            `Queue updated: ${msg.data.action || "unknown"}, length: ${msg.data.queue_length}`,
          );
          // Dispatch event for queue dropdown to refresh
          window.dispatchEvent(new CustomEvent("mitto:queue_updated"));
        }
        break;

      case "queue_message_sending":
        // Server notifies that a queued message is about to be sent to the agent
        // This happens when the agent is idle and auto-processes the queue
        if (msg.data?.message_id) {
          console.log(`Queue message sending: ${msg.data.message_id}`);
          // Dispatch event so UI can show "sending" state
          window.dispatchEvent(
            new CustomEvent("mitto:queue_message_sending", {
              detail: { messageId: msg.data.message_id },
            }),
          );
        }
        break;

      case "queue_message_sent":
        // Server notifies that a queued message was delivered to the agent
        if (msg.data?.message_id) {
          console.log(`Queue message sent: ${msg.data.message_id}`);
          // Dispatch event so UI can update
          window.dispatchEvent(
            new CustomEvent("mitto:queue_message_sent", {
              detail: { messageId: msg.data.message_id },
            }),
          );
        }
        break;

      case "runner_fallback":
        // Server notifies that a configured runner is not supported and fell back to exec
        console.log("Runner fallback:", msg.data);
        if (msg.data) {
          // Dispatch event for toast notification
          window.dispatchEvent(
            new CustomEvent("mitto:runner_fallback", { detail: msg.data }),
          );
        }
        break;

      case "queue_message_titled":
        // Server notifies us that a queued message received an auto-generated title
        if (msg.data?.message_id && msg.data?.title) {
          console.log(
            `Queue message titled: ${msg.data.message_id} -> "${msg.data.title}"`,
          );
          // Update the title in the local queue messages state
          setQueueMessages((prev) =>
            prev.map((m) =>
              m.id === msg.data.message_id
                ? { ...m, title: msg.data.title }
                : m,
            ),
          );
        }
        break;

      case "queue_reordered":
        // Server notifies us that the queue order has changed
        if (msg.data?.messages) {
          console.log(`Queue reordered: ${msg.data.messages.length} messages`);
          setQueueMessages(msg.data.messages);
          setQueueLength(msg.data.messages.length);
        }
        break;
    }
  }, []);

  // Connect to per-session WebSocket
  const connectToSession = useCallback(
    (sessionId) => {
      // Clear any pending reconnect timer for this session
      if (sessionReconnectRefs.current[sessionId]) {
        clearTimeout(sessionReconnectRefs.current[sessionId]);
        delete sessionReconnectRefs.current[sessionId];
      }

      // Don't connect if already connected
      if (sessionWsRefs.current[sessionId]) {
        return sessionWsRefs.current[sessionId];
      }

      const ws = new WebSocket(wsUrl(`/api/sessions/${sessionId}/ws`));
      const wsId = Math.random().toString(36).substring(2, 8); // Debug ID for this connection
      ws._debugId = wsId;

      ws.onopen = () => {
        console.log(`Session WebSocket connected: ${sessionId} (ws: ${wsId})`);

        // M2: Reset reconnection attempt counter on successful connection
        delete sessionReconnectAttemptsRef.current[sessionId];

        // Use the new WebSocket-only approach for loading events
        // Check if we have a lastSeenSeq (reconnection) or need initial load
        const lastSeq = getLastSeenSeq(sessionId);
        if (lastSeq > 0) {
          // Reconnection: sync events after lastSeq
          console.log(`Syncing session ${sessionId} from seq ${lastSeq} (WebSocket-only)`);
          ws.send(
            JSON.stringify({
              type: "load_events",
              data: { after_seq: lastSeq },
            }),
          );
        } else {
          // Initial load: load last N events
          console.log(`Loading session ${sessionId} events (WebSocket-only)`);
          ws.send(
            JSON.stringify({
              type: "load_events",
              data: { limit: INITIAL_EVENTS_LIMIT },
            }),
          );
        }

        // Retry any pending prompts after a short delay to ensure connection is stable
        setTimeout(() => {
          if (retryPendingPromptsRef.current) {
            retryPendingPromptsRef.current(sessionId);
          }
        }, 500);

        // Start keepalive interval to detect zombie connections
        // Clear any existing keepalive for this session first
        if (keepaliveRef.current[sessionId]?.intervalId) {
          clearInterval(keepaliveRef.current[sessionId].intervalId);
        }

        const intervalId = setInterval(() => {
          const currentWs = sessionWsRefs.current[sessionId];
          if (!currentWs || currentWs.readyState !== WebSocket.OPEN) {
            // WebSocket is not open, clear the interval
            clearInterval(intervalId);
            delete keepaliveRef.current[sessionId];
            return;
          }

          const keepalive = keepaliveRef.current[sessionId];
          if (keepalive?.pendingKeepalive) {
            // Previous keepalive didn't get a response
            keepalive.missedCount = (keepalive.missedCount || 0) + 1;
            console.log(
              `Keepalive missed for session ${sessionId}, count: ${keepalive.missedCount}`,
            );

            if (keepalive.missedCount >= KEEPALIVE_MAX_MISSED) {
              // Connection is likely dead, force close to trigger reconnect
              console.log(
                `Too many missed keepalives for session ${sessionId}, forcing reconnect`,
              );
              clearInterval(intervalId);
              delete keepaliveRef.current[sessionId];
              currentWs.close();
              return;
            }
          }

          // Send keepalive
          keepaliveRef.current[sessionId] = {
            ...keepaliveRef.current[sessionId],
            intervalId,
            pendingKeepalive: true,
            lastSentTime: Date.now(),
          };

          currentWs.send(
            JSON.stringify({
              type: "keepalive",
              data: { client_time: Date.now() },
            }),
          );
        }, KEEPALIVE_INTERVAL_MS);

        keepaliveRef.current[sessionId] = {
          intervalId,
          lastAckTime: Date.now(),
          missedCount: 0,
          pendingKeepalive: false,
        };
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          console.log(
            `[WS ${wsId}] Received:`,
            msg.type,
            msg.data?.html?.substring(0, 50) ||
              msg.data?.message?.substring(0, 50) ||
              "",
          );
          handleSessionMessage(sessionId, msg);
        } catch (err) {
          console.error(
            "Failed to parse session WebSocket message:",
            err,
            event.data,
          );
        }
      };

      ws.onclose = async () => {
        console.log(`Session WebSocket closed: ${sessionId} (ws: ${wsId})`);

        // Clean up keepalive interval for this session
        if (keepaliveRef.current[sessionId]?.intervalId) {
          clearInterval(keepaliveRef.current[sessionId].intervalId);
          delete keepaliveRef.current[sessionId];
        }

        // Only delete the ref if it still points to this WebSocket (not a newer one)
        if (sessionWsRefs.current[sessionId] === ws) {
          delete sessionWsRefs.current[sessionId];
        } else {
          console.log(
            `WebSocket ${wsId} closed but ref points to different WebSocket - not deleting`,
          );
        }
        // Clear streaming state for this session
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          return { ...prev, [sessionId]: { ...session, isStreaming: false } };
        });

        // Before reconnecting, check if the close was due to auth failure
        // WebSocket doesn't provide HTTP status codes, so we make a quick auth check
        const isAuthenticated = await checkAuthOrRedirect();
        if (!isAuthenticated) {
          // checkAuthOrRedirect already redirected to login if 401
          return;
        }

        // Reconnect if this session is still active (user hasn't switched away)
        // and no newer WebSocket has been created
        // This handles cases like mobile browser suspension when phone is locked
        if (
          activeSessionIdRef.current === sessionId &&
          !sessionWsRefs.current[sessionId]
        ) {
          // M2: Use exponential backoff for reconnection
          const attempt = sessionReconnectAttemptsRef.current[sessionId] || 0;
          const delay = calculateReconnectDelay(attempt);
          console.log(`Scheduling reconnect for session ${sessionId} (attempt ${attempt + 1}, delay ${delay}ms)`);

          sessionReconnectRefs.current[sessionId] = setTimeout(() => {
            delete sessionReconnectRefs.current[sessionId];
            // Double-check the session is still active before reconnecting
            if (activeSessionIdRef.current === sessionId) {
              // Increment attempt counter before reconnecting
              sessionReconnectAttemptsRef.current[sessionId] = attempt + 1;
              console.log(`Reconnecting to session: ${sessionId}`);
              connectToSession(sessionId);
            }
          }, delay);
        }
      };

      ws.onerror = (err) => {
        console.error(`Session WebSocket error: ${sessionId}`, err);
        ws.close();
      };

      sessionWsRefs.current[sessionId] = ws;
      return ws;
    },
    [handleSessionMessage],
  );

  // Fetch stored sessions
  const fetchStoredSessions = useCallback(async () => {
    try {
      const res = await authFetch(apiUrl("/api/sessions"));
      const data = await res.json();
      // Update global working_dir map for each session
      (data || []).forEach((s) => {
        if (s.session_id && s.working_dir) {
          updateGlobalWorkingDir(s.session_id, s.working_dir);
        }
      });
      setStoredSessions(data || []);
      return data || [];
    } catch (err) {
      console.error("Failed to fetch sessions:", err);
      return [];
    }
  }, []);

  // Switch to an existing session
  // Uses reverse-order loading for better UX: newest messages load first,
  // so the conversation opens already positioned at the latest message.
  const switchSession = useCallback(
    async (sessionId) => {
      // Use sessionsRef to get current sessions state and avoid stale closures
      const currentSessions = sessionsRef.current;
      // Check if session already has messages loaded (not just an empty placeholder from WebSocket)
      const existingSession = currentSessions[sessionId];
      const hasLoadedMessages =
        existingSession &&
        existingSession.messages &&
        existingSession.messages.length > 0;
      const hasWorkingDir = existingSession?.info?.working_dir;

      if (hasLoadedMessages && hasWorkingDir) {
        // Session already has messages and working_dir, just set it active
        setActiveSessionId(sessionId);

        // Ensure WebSocket is connected and synced
        // On mobile, the WebSocket may have died while the phone slept
        // If not connected, connect now - the onopen handler will sync events
        const existingWs = sessionWsRefs.current[sessionId];
        if (!existingWs || existingWs.readyState !== WebSocket.OPEN) {
          console.log(`Session ${sessionId} has messages but WebSocket is not connected, reconnecting...`);
          // Remove stale WebSocket reference if any
          if (existingWs) {
            delete sessionWsRefs.current[sessionId];
            existingWs.close();
          }
          connectToSession(sessionId);
        }
        return;
      }

      // Load session events from API (with limit for faster initial load)
      try {
        // Get session metadata first to know total event count and working_dir
        const metaResponse = await authFetch(
          apiUrl(`/api/sessions/${sessionId}`),
        );
        const meta = metaResponse.ok ? await metaResponse.json() : {};

        // If we already have messages, just update the info with working_dir
        if (hasLoadedMessages) {
          // Store working_dir in both ref and state
          if (meta.working_dir) {
            workingDirMapRef.current[sessionId] = meta.working_dir;
            setWorkingDirMap((prev) => ({
              ...prev,
              [sessionId]: meta.working_dir,
            }));
          }
          setSessions((prev) => {
            const existing = prev[sessionId] || {};
            return {
              ...prev,
              [sessionId]: {
                ...existing,
                info: {
                  ...existing.info,
                  working_dir: meta.working_dir,
                },
              },
            };
          });
          setActiveSessionId(sessionId);
          return;
        }

        // WebSocket-only architecture: Connect to WebSocket first, then load events via WebSocket
        // This eliminates race conditions between REST and WebSocket and simplifies deduplication

        // Store working_dir in both ref and state (from metadata)
        if (meta.working_dir) {
          workingDirMapRef.current[sessionId] = meta.working_dir;
          setWorkingDirMap((prev) => ({
            ...prev,
            [sessionId]: meta.working_dir,
          }));
        }

        // Initialize session state with metadata (messages will be loaded via WebSocket)
        // Important: Reset hasMoreMessages and firstLoadedSeq when starting a fresh load
        // to prevent stale values from showing incorrect UI state while loading
        setSessions((prev) => {
          const existing = prev[sessionId] || {};
          return {
            ...prev,
            [sessionId]: {
              ...existing,
              messages: existing.messages || [],
              info: {
                ...existing.info,
                session_id: sessionId,
                name: meta.name || "Conversation",
                acp_server: meta.acp_server,
                working_dir: meta.working_dir,
                created_at: meta.created_at,
                status: meta.status || "active",
              },
              isStreaming: existing.isStreaming || false,
              // Reset these to prevent stale UI state while loading
              hasMoreMessages: existing.messages?.length > 0 ? existing.hasMoreMessages : false,
              firstLoadedSeq: existing.messages?.length > 0 ? existing.firstLoadedSeq : undefined,
            },
          };
        });

        // Connect to the session WebSocket - this will trigger load_events on open
        // The events_loaded handler will populate the messages
        connectToSession(sessionId);
        setActiveSessionId(sessionId);
      } catch (err) {
        console.error("Failed to switch session:", err);
      }
    },
    [connectToSession],
  );

  // Handle global events (session lifecycle)
  const handleGlobalEvent = useCallback((msg) => {
    switch (msg.type) {
      case "connected":
        // Global events WS connected
        console.log("Global events ready");
        break;

      case "session_created":
        // A new session was created (possibly by another client)
        setStoredSessions((prev) => {
          const exists = prev.find((s) => s.session_id === msg.data.session_id);
          if (exists) return prev;
          return [
            {
              session_id: msg.data.session_id,
              name: msg.data.name || "New conversation",
              acp_server: msg.data.acp_server,
              working_dir: msg.data.working_dir,
              status: "active",
              created_at: new Date().toISOString(),
            },
            ...prev,
          ];
        });
        break;

      case "session_renamed":
        // Update session name in stored sessions
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, name: msg.data.name }
              : s,
          ),
        );
        // Also update in active sessions
        setSessions((prev) => {
          const session = prev[msg.data.session_id];
          if (!session) return prev;
          return {
            ...prev,
            [msg.data.session_id]: {
              ...session,
              info: { ...session.info, name: msg.data.name },
            },
          };
        });
        break;

      case "session_pinned":
        // Update session pinned state in stored sessions
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, pinned: msg.data.pinned }
              : s,
          ),
        );
        // Also update in active sessions
        setSessions((prev) => {
          const session = prev[msg.data.session_id];
          if (!session) return prev;
          return {
            ...prev,
            [msg.data.session_id]: {
              ...session,
              info: { ...session.info, pinned: msg.data.pinned },
            },
          };
        });
        break;

      case "session_deleted": {
        const deletedId = msg.data.session_id;
        setStoredSessions((prev) =>
          prev.filter((s) => s.session_id !== deletedId),
        );
        const currentId = activeSessionIdRef.current;
        setSessions((prev) => {
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
        // M1 fix: Clear seen seqs for deleted session
        clearSeenSeqs(deletedId);
        // M2: Clear reconnection attempt counter for deleted session
        delete sessionReconnectAttemptsRef.current[deletedId];
        break;
      }
    }
  }, []);

  // Connect to global events WebSocket
  const connectToEvents = useCallback(() => {
    const socket = new WebSocket(wsUrl("/api/events"));

    socket.onopen = () => {
      setEventsConnected(true);
      // M2: Reset reconnection attempt counter on successful connection
      eventsReconnectAttemptRef.current = 0;

      const isReconnect = wasConnectedRef.current;
      console.log(
        "Global events WebSocket connected",
        isReconnect ? "(reconnect)" : "(initial)",
      );

      if (isReconnect) {
        // On reconnect: refresh the session list to catch any changes
        // that occurred while disconnected (e.g., mobile phone locked)
        // but don't switch sessions - keep the user's current session
        console.log("Refreshing session list after reconnect");
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
        console.error(
          "Failed to parse global events message:",
          err,
          event.data,
        );
      }
    };

    socket.onclose = async () => {
      if (eventsWsRef.current) {
        wasConnectedRef.current = true;
      }
      setEventsConnected(false);
      eventsWsRef.current = null;

      // Before reconnecting, check if the close was due to auth failure
      // WebSocket doesn't provide HTTP status codes, so we make a quick auth check
      const isAuthenticated = await checkAuthOrRedirect();
      if (!isAuthenticated) {
        // checkAuthOrRedirect already redirected to login if 401
        return;
      }

      // M2: Use exponential backoff for reconnection
      const attempt = eventsReconnectAttemptRef.current;
      const delay = calculateReconnectDelay(attempt);
      console.log(`Scheduling global events reconnect (attempt ${attempt + 1}, delay ${delay}ms)`);
      eventsReconnectAttemptRef.current = attempt + 1;
      reconnectRef.current = setTimeout(connectToEvents, delay);
    };

    socket.onerror = (err) => {
      console.error("Global events WebSocket error:", err);
      socket.close();
    };

    eventsWsRef.current = socket;
  }, [fetchStoredSessions, handleGlobalEvent, switchSession]);

  // Create a new session via REST API
  // Options: { name?: string, workingDir?: string, acpServer?: string }
  // Returns: { sessionId: string } on success, { error: string, errorCode?: string } on failure, or null on network error
  const createNewSession = useCallback(
    async (options = {}) => {
      try {
        // Support both old (name string) and new (options object) signatures
        const opts = typeof options === "string" ? { name: options } : options;

        const response = await secureFetch(apiUrl("/api/sessions"), {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            name: opts.name || "",
            working_dir: opts.workingDir || "",
            acp_server: opts.acpServer || "",
          }),
        });

        if (!response.ok) {
          // Try to parse as JSON for structured error
          const contentType = response.headers.get("content-type");
          if (contentType && contentType.includes("application/json")) {
            const errorData = await response.json();
            console.error("Failed to create session:", errorData);
            return {
              error: errorData.message || "Failed to create session",
              errorCode: errorData.error,
            };
          }
          const error = await response.text();
          console.error("Failed to create session:", error);
          return { error: error || "Failed to create session" };
        }

        const data = await response.json();
        const sessionId = data.session_id;

        // Build system message with workspace info
        let systemMsg = `Start chatting with ${data.acp_server}`;
        if (data.working_dir) {
          systemMsg += ` to work on ${data.working_dir}`;
        }

        // Initialize session state
        setSessions((prev) => ({
          ...prev,
          [sessionId]: {
            messages: [
              {
                role: ROLE_SYSTEM,
                text: systemMsg,
                timestamp: Date.now(),
              },
            ],
            info: {
              session_id: sessionId,
              name: data.name || "New conversation",
              acp_server: data.acp_server,
              working_dir: data.working_dir,
              status: "active",
            },
            isStreaming: false,
          },
        }));

        // Connect to the session WebSocket
        connectToSession(sessionId);
        setActiveSessionId(sessionId);

        return { sessionId };
      } catch (err) {
        console.error("Failed to create session:", err);
        return { error: err.message || "Network error" };
      }
    },
    [connectToSession],
  );

  // Helper functions for session state updates
  const addMessageToSession = useCallback((sessionId, message) => {
    setSessions((prev) => {
      const session = prev[sessionId];
      if (!session) return prev;
      const messages = limitMessages([...session.messages, message]);
      return { ...prev, [sessionId]: { ...session, messages } };
    });
  }, []);

  const updateLastMessage = useCallback((sessionId, updater) => {
    setSessions((prev) => {
      const session = prev[sessionId];
      if (!session || session.messages.length === 0) return prev;
      const messages = [...session.messages];
      messages[messages.length - 1] = updater(messages[messages.length - 1]);
      return { ...prev, [sessionId]: { ...session, messages } };
    });
  }, []);

  // Clear action buttons for a session (called when sending a new prompt)
  const clearActionButtons = useCallback((sessionId) => {
    setSessions((prev) => {
      const session = prev[sessionId];
      if (!session || !session.actionButtons?.length) return prev;
      return { ...prev, [sessionId]: { ...session, actionButtons: [] } };
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

  // Default timeout for waiting on message ACK (in milliseconds)
  // Use longer timeout on mobile devices due to slower/flakier networks
  const isMobileDevice = useMemo(() => {
    if (typeof navigator === "undefined") return false;
    const ua = navigator.userAgent || "";
    return /iPhone|iPad|iPod|Android|webOS|BlackBerry|IEMobile|Opera Mini/i.test(
      ua,
    );
  }, []);
  const SEND_ACK_TIMEOUT = isMobileDevice ? 30000 : 15000;

  // Timeout for waiting for WebSocket to connect (in milliseconds)
  const WS_CONNECT_TIMEOUT = 5000;

  /**
   * Resolve all pending sends for a session as successful.
   * Called when we receive agent response, which proves the prompt was received.
   * @param {string} sessionId - The session ID
   */
  const resolvePendingSendsForSession = useCallback((sessionId) => {
    // Find all pending sends for this session and resolve them
    for (const [promptId, pending] of Object.entries(pendingSendsRef.current)) {
      // We don't track sessionId in pendingSendsRef, but we can check localStorage
      // For simplicity, resolve all pending sends when agent responds
      // (there should typically only be one pending send at a time)
      if (pending) {
        console.log(
          `Resolving pending send ${promptId} - agent response received`,
        );
        clearTimeout(pending.timeoutId);
        pending.resolve({ success: true, promptId });
        delete pendingSendsRef.current[promptId];
        removePendingPrompt(promptId);
      }
    }
  }, []);

  // Keep the ref in sync with the callback
  useEffect(() => {
    resolvePendingSendsRef.current = resolvePendingSendsForSession;
  }, [resolvePendingSendsForSession]);

  /**
   * Wait for the session WebSocket to be connected.
   * If not connected, triggers a reconnection and waits.
   * @param {string} sessionId - The session ID
   * @param {number} timeout - Timeout in milliseconds
   * @returns {Promise<WebSocket>} The connected WebSocket
   */
  const waitForSessionConnection = useCallback(
    (sessionId, timeout = WS_CONNECT_TIMEOUT) => {
      return new Promise((resolve, reject) => {
        // Check if already connected
        const existingWs = sessionWsRefs.current[sessionId];
        if (existingWs && existingWs.readyState === WebSocket.OPEN) {
          resolve(existingWs);
          return;
        }

        console.log(
          `WebSocket not connected for session ${sessionId}, triggering reconnect`,
        );

        // Clear any pending reconnect timer
        if (sessionReconnectRefs.current[sessionId]) {
          clearTimeout(sessionReconnectRefs.current[sessionId]);
          delete sessionReconnectRefs.current[sessionId];
        }

        // Close existing zombie WebSocket if any
        if (existingWs) {
          delete sessionWsRefs.current[sessionId];
          existingWs.close();
        }

        // Set up timeout
        const timeoutId = setTimeout(() => {
          reject(
            new Error(
              "Connection timed out. Please check your network and try again.",
            ),
          );
        }, timeout);

        // Create new WebSocket connection
        const ws = new WebSocket(wsUrl(`/api/sessions/${sessionId}/ws`));
        const wsId = Math.random().toString(36).substring(2, 8);
        ws._debugId = wsId;

        ws.onopen = () => {
          clearTimeout(timeoutId);
          console.log(
            `Session WebSocket connected (reconnect): ${sessionId} (ws: ${wsId})`,
          );

          // Store the WebSocket reference
          sessionWsRefs.current[sessionId] = ws;

          // Sync events we may have missed while disconnected
          const lastSeq = getLastSeenSeq(sessionId);
          if (lastSeq > 0) {
            console.log(`Syncing session ${sessionId} from seq ${lastSeq}`);
            ws.send(
              JSON.stringify({
                type: "sync_session",
                data: { session_id: sessionId, after_seq: lastSeq },
              }),
            );
          }

          resolve(ws);
        };

        ws.onerror = (err) => {
          clearTimeout(timeoutId);
          console.error(`Session WebSocket error during reconnect:`, err);
          reject(new Error("Failed to connect. Please try again."));
        };

        ws.onclose = () => {
          // If we haven't resolved yet, this is an early close
          clearTimeout(timeoutId);
          if (sessionWsRefs.current[sessionId] === ws) {
            delete sessionWsRefs.current[sessionId];
          }
        };

        // Set up message handler (reuse existing logic)
        ws.onmessage = (event) => {
          try {
            const msg = JSON.parse(event.data);
            handleSessionMessage(sessionId, msg);
          } catch (err) {
            console.error("Failed to parse session message:", err);
          }
        };
      });
    },
    [handleSessionMessage],
  );

  /**
   * Check if the WebSocket connection for a session is healthy.
   * A connection is considered healthy if we've received a keepalive_ack recently.
   * @param {string} sessionId - The session ID
   * @returns {boolean} True if connection is healthy
   */
  const isConnectionHealthy = useCallback((sessionId) => {
    const keepalive = keepaliveRef.current[sessionId];
    if (!keepalive) return true; // No keepalive tracking yet, assume healthy

    const timeSinceLastAck = Date.now() - (keepalive.lastAckTime || 0);
    // Consider unhealthy if we haven't received an ACK in 2x the keepalive interval
    // or if we have missed keepalives
    const isHealthy =
      timeSinceLastAck < KEEPALIVE_INTERVAL_MS * 2 &&
      (keepalive.missedCount || 0) === 0;

    if (!isHealthy) {
      console.log(
        `Connection unhealthy for session ${sessionId}: timeSinceLastAck=${timeSinceLastAck}ms, missedCount=${keepalive.missedCount}`,
      );
    }
    return isHealthy;
  }, []);

  /**
   * Send a prompt to the active session.
   * Returns a Promise that resolves on ACK or rejects on timeout/failure.
   * If WebSocket is not connected or unhealthy, automatically triggers reconnection and waits.
   * @param {string} message - The message text
   * @param {Array} images - Optional array of images
   * @param {Object} options - Optional settings: { timeout: number, skipMessageAdd: boolean }
   * @returns {Promise<{success: boolean, promptId: string}>}
   */
  const sendPrompt = useCallback(
    async (message, images = [], options = {}) => {
      const timeout = options.timeout || SEND_ACK_TIMEOUT;

      if (!activeSessionId) {
        throw new Error("No active session");
      }

      // Check if WebSocket is connected and healthy
      let ws = sessionWsRefs.current[activeSessionId];
      const needsReconnect =
        !ws ||
        ws.readyState !== WebSocket.OPEN ||
        !isConnectionHealthy(activeSessionId);

      if (needsReconnect) {
        console.log(
          `Connection needs reconnect before sending (ws=${!!ws}, readyState=${ws?.readyState}, healthy=${isConnectionHealthy(activeSessionId)})`,
        );
        // Force close any existing zombie connection
        if (ws) {
          delete sessionWsRefs.current[activeSessionId];
          ws.close();
        }
        // Wait for fresh connection
        ws = await waitForSessionConnection(activeSessionId);
      }

      return new Promise((resolve, reject) => {
        // Clear any existing action buttons when sending a new prompt
        clearActionButtons(activeSessionId);

        // Add user message with optional images (unless skipped for retry)
        if (!options.skipMessageAdd) {
          const userMessage = {
            role: ROLE_USER,
            text: message,
            timestamp: Date.now(),
          };
          if (images.length > 0) {
            userMessage.images = images; // Array of { id, url, name, mimeType }
          }
          addMessageToSession(activeSessionId, userMessage);
          // Mark any previous streaming message as complete
          updateLastMessage(activeSessionId, (m) =>
            !m.complete && (m.role === ROLE_AGENT || m.role === ROLE_THOUGHT)
              ? { ...m, complete: true }
              : m,
          );
        }

        // Generate a unique prompt ID for delivery tracking
        const promptId = generatePromptId();
        const imageIds = images.map((img) => img.id);

        // Save to pending queue BEFORE sending (for mobile reliability)
        savePendingPrompt(activeSessionId, promptId, message, imageIds);

        // Set up timeout for ACK with improved handling
        const timeoutId = setTimeout(async () => {
          const pending = pendingSendsRef.current[promptId];
          if (!pending) return; // Already resolved

          console.log(
            `Send timeout for prompt ${promptId}, forcing reconnect to check status`,
          );

          // Force reconnect to get a fresh connection
          const currentWs = sessionWsRefs.current[activeSessionId];
          if (currentWs) {
            delete sessionWsRefs.current[activeSessionId];
            currentWs.close();
          }

          // The pending prompt is still in localStorage, so when we reconnect
          // and sync, we'll see if the message was actually received.
          // For now, reject with a more helpful message.
          delete pendingSendsRef.current[promptId];
          reject(
            new Error(
              "Message delivery could not be confirmed. The message may have been sent - please check after the page reconnects.",
            ),
          );
        }, timeout);

        // Track the pending send
        pendingSendsRef.current[promptId] = { resolve, reject, timeoutId };

        // Send prompt with prompt_id for acknowledgment
        const sent = sendToSession(activeSessionId, {
          type: "prompt",
          data: { message, image_ids: imageIds, prompt_id: promptId },
        });

        if (!sent) {
          // WebSocket send failed
          clearTimeout(timeoutId);
          delete pendingSendsRef.current[promptId];
          reject(new Error("Failed to send message"));
        }
      });
    },
    [
      activeSessionId,
      addMessageToSession,
      updateLastMessage,
      sendToSession,
      waitForSessionConnection,
      clearActionButtons,
      isConnectionHealthy,
    ],
  );

  const cancelPrompt = useCallback(() => {
    if (!activeSessionId) return;
    sendToSession(activeSessionId, { type: "cancel" });
  }, [activeSessionId, sendToSession]);

  // Force reset a stuck session (when agent is unresponsive)
  const forceReset = useCallback(() => {
    if (!activeSessionId) return;
    console.log("Force resetting session:", activeSessionId);
    sendToSession(activeSessionId, { type: "force_reset" });
  }, [activeSessionId, sendToSession]);

  // Retry pending prompts for a session (called on reconnect or visibility change)
  const retryPendingPrompts = useCallback(
    (sessionId) => {
      const pending = getPendingPromptsForSession(sessionId);
      if (pending.length === 0) return;

      console.log(
        `Retrying ${pending.length} pending prompt(s) for session ${sessionId}`,
      );

      for (const { promptId, message, imageIds } of pending) {
        const sent = sendToSession(sessionId, {
          type: "prompt",
          data: { message, image_ids: imageIds || [], prompt_id: promptId },
        });
        if (sent) {
          console.log(`Retried pending prompt: ${promptId}`);
        } else {
          console.warn(
            `Failed to retry pending prompt (WebSocket not ready): ${promptId}`,
          );
          // Stop retrying if WebSocket is not ready - will retry on next reconnect
          break;
        }
      }
    },
    [sendToSession],
  );

  // Keep the ref in sync with the callback
  useEffect(() => {
    retryPendingPromptsRef.current = retryPendingPrompts;
  }, [retryPendingPrompts]);

  const newSession = useCallback(
    async (options) => {
      return await createNewSession(options);
    },
    [createNewSession],
  );

  const loadSession = useCallback(
    async (sessionId) => {
      // Use sessionsRef to get current sessions state and avoid stale closures
      const currentSessions = sessionsRef.current;
      // If session is already loaded in memory, just switch to it
      if (currentSessions[sessionId]) {
        setActiveSessionId(sessionId);
        return;
      }
      // Load session for read-only viewing
      await switchSession(sessionId);
    },
    [switchSession],
  );

  // Load more (older) messages for a session
  // Uses WebSocket-only architecture: sends load_events with before_seq
  const loadMoreMessages = useCallback((sessionId) => {
    // Use sessionsRef to get current sessions state and avoid stale closures
    const currentSessions = sessionsRef.current;
    const session = currentSessions[sessionId];
    if (!session || !session.hasMoreMessages || !session.firstLoadedSeq) {
      return;
    }

    // Get the WebSocket for this session
    const ws = sessionWsRefs.current[sessionId];
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      console.error("WebSocket not connected for session:", sessionId);
      return;
    }

    // Send load_events request with before_seq for pagination
    console.log(`Loading more messages for ${sessionId} before seq ${session.firstLoadedSeq}`);
    ws.send(
      JSON.stringify({
        type: "load_events",
        data: {
          limit: INITIAL_EVENTS_LIMIT,
          before_seq: session.firstLoadedSeq,
        },
      }),
    );
    // The events_loaded handler will process the response and prepend messages
  }, []);

  const updateSessionName = useCallback((sessionId, name) => {
    setSessions((prev) => {
      const session = prev[sessionId];
      if (!session) return prev;
      return {
        ...prev,
        [sessionId]: {
          ...session,
          info: { ...session.info, name },
        },
      };
    });
  }, []);

  // Rename a session via REST API
  const renameSession = useCallback(
    async (sessionId, name) => {
      try {
        const response = await secureFetch(
          apiUrl(`/api/sessions/${sessionId}`),
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name }),
          },
        );
        if (!response.ok) {
          console.error("Failed to rename session");
          return;
        }
        // Update local state
        updateSessionName(sessionId, name);
        // Update stored sessions
        setStoredSessions((prev) =>
          prev.map((s) => (s.session_id === sessionId ? { ...s, name } : s)),
        );
      } catch (err) {
        console.error("Failed to rename session:", err);
      }
    },
    [updateSessionName],
  );

  // Pin/unpin a session via REST API
  const pinSession = useCallback(async (sessionId, pinned) => {
    try {
      const response = await secureFetch(apiUrl(`/api/sessions/${sessionId}`), {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pinned }),
      });
      if (!response.ok) {
        console.error("Failed to pin/unpin session");
        return;
      }
      // Update local state for stored sessions
      setStoredSessions((prev) =>
        prev.map((s) => (s.session_id === sessionId ? { ...s, pinned } : s)),
      );
      // Update local state for active sessions
      setSessions((prev) => {
        const session = prev[sessionId];
        if (!session) return prev;
        return {
          ...prev,
          [sessionId]: {
            ...session,
            info: { ...session.info, pinned },
          },
        };
      });
    } catch (err) {
      console.error("Failed to pin/unpin session:", err);
    }
  }, []);

  const removeSession = useCallback(
    async (sessionId) => {
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
      setSessions((prev) => {
        const { [sessionId]: removed, ...rest } = prev;
        return rest;
      });

      // Delete from server first
      try {
        await secureFetch(apiUrl(`/api/sessions/${sessionId}`), {
          method: "DELETE",
        });
      } catch (err) {
        console.error("Failed to delete session:", err);
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
    },
    [fetchStoredSessions, switchSession],
  );

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

  // Force reconnect active session WebSocket - closes existing connection and creates new one
  // This is more reliable than trying to sync over a potentially stale connection
  const forceReconnectActiveSession = useCallback(() => {
    const currentSessionId = activeSessionIdRef.current;
    if (!currentSessionId) return;

    console.log(`Force reconnecting session ${currentSessionId}`);

    // Clear any pending reconnect timer
    if (sessionReconnectRefs.current[currentSessionId]) {
      clearTimeout(sessionReconnectRefs.current[currentSessionId]);
      delete sessionReconnectRefs.current[currentSessionId];
    }

    // Close existing WebSocket if any (this will trigger a clean reconnect)
    const existingWs = sessionWsRefs.current[currentSessionId];
    if (existingWs) {
      // Remove the ref first so onclose doesn't schedule another reconnect
      delete sessionWsRefs.current[currentSessionId];
      existingWs.close();
    }

    // Connect to session - this will sync events in the onopen handler
    connectToSession(currentSessionId);
  }, [connectToSession]);

  // Ref to track which sessions we've already attempted to recover from inconsistent state
  // This prevents infinite loops where recovery triggers state change which triggers recovery
  const recoveryAttemptedRef = useRef({});

  // Auto-recovery for inconsistent state: hasMoreMessages=true but messages=[]
  // This can happen due to race conditions during session loading.
  // If detected, trigger a fresh load.
  useEffect(() => {
    if (!activeSessionId) return;
    const session = sessions[activeSessionId];
    if (!session) return;

    const hasMessages = session.messages && session.messages.length > 0;
    const hasMoreFlag = session.hasMoreMessages;

    // Check if we've already attempted recovery for this session
    if (recoveryAttemptedRef.current[activeSessionId]) {
      // If messages are now loaded, clear the recovery flag
      if (hasMessages) {
        delete recoveryAttemptedRef.current[activeSessionId];
      }
      return;
    }

    // Inconsistent state: server said there's more but we have no messages
    if (hasMoreFlag && !hasMessages) {
      console.log(`Detected inconsistent state for ${activeSessionId}: hasMoreMessages=true but messages=[], triggering reload...`);

      // Mark that we've attempted recovery to prevent infinite loops
      recoveryAttemptedRef.current[activeSessionId] = true;

      // Clear the stale state
      setLastSeenSeq(activeSessionId, 0);

      // Trigger a fresh load via WebSocket
      const ws = sessionWsRefs.current[activeSessionId];
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(
          JSON.stringify({
            type: "load_events",
            data: { limit: INITIAL_EVENTS_LIMIT },
          }),
        );
      } else {
        // WebSocket not connected, reconnect
        forceReconnectActiveSession();
      }
    }
  }, [activeSessionId, sessions, forceReconnectActiveSession]);

  // Refresh session list, force reconnect session WebSocket, and retry pending prompts when app becomes visible
  // On mobile, when the phone sleeps, WebSocket connections can become "zombie" connections
  // that appear open but are actually dead. The safest approach is to force a fresh reconnection.
  // Also detect if the session might be stale (phone locked overnight) and verify auth first.
  useEffect(() => {
    const handleVisibilityChange = async () => {
      if (document.visibilityState === "hidden") {
        // Track when the page was hidden so we can detect staleness on wake
        lastHiddenTimeRef.current = Date.now();
        console.log("App hidden, tracking time");
        return;
      }

      if (document.visibilityState === "visible") {
        const now = Date.now();
        const hiddenDuration = lastHiddenTimeRef.current
          ? now - lastHiddenTimeRef.current
          : 0;
        const wasHiddenLong = hiddenDuration > STALE_THRESHOLD_MS;

        console.log(
          `App became visible after ${Math.round(hiddenDuration / 1000)}s` +
            (wasHiddenLong ? " (checking auth first)" : ""),
        );

        // Clean up expired prompts first
        cleanupExpiredPrompts();

        // If the page was hidden for a long time (e.g., phone locked overnight),
        // do an explicit auth check before trying to reconnect.
        // This prevents the user from seeing a stuck/stale state.
        if (wasHiddenLong) {
          console.log("Session may be stale, verifying authentication...");
          const { authenticated, networkError } = await checkAuthWithRetry();

          if (!authenticated) {
            if (networkError) {
              // Network is not available yet - this is common right after phone unlock
              // Wait a bit longer and try again
              console.log(
                "Network not available, will retry auth check in 2s...",
              );
              setTimeout(async () => {
                const retry = await checkAuthWithRetry();
                if (!retry.authenticated && !retry.networkError) {
                  // 401 - session expired
                  return;
                }
                // Either authenticated or still network error - proceed with normal reconnect
                // If still network error, the WebSocket reconnect will handle retries
                const retrySessions = await fetchStoredSessions();

                // Check if the active session still exists
                const retryCurrentSessionId = activeSessionIdRef.current;
                const retrySessionExists =
                  retryCurrentSessionId &&
                  retrySessions.some(
                    (s) => s.session_id === retryCurrentSessionId,
                  );

                if (retryCurrentSessionId && !retrySessionExists) {
                  // Active session was deleted
                  console.log(
                    `Active session ${retryCurrentSessionId} no longer exists, switching...`,
                  );
                  if (retrySessions.length > 0) {
                    switchSession(retrySessions[0].session_id);
                  } else {
                    setActiveSessionId(null);
                  }
                } else {
                  setTimeout(() => {
                    forceReconnectActiveSession();
                  }, 300);
                }
              }, 2000);
              return;
            }
            // Auth check returned 401 - redirectToLogin was already called
            return;
          }
          console.log("Authentication verified, proceeding with reconnect");
        }

        // Fetch stored sessions (updates the session list in sidebar)
        // We await this to ensure we have the latest session list before reconnecting
        const sessions = await fetchStoredSessions();

        // Check if the active session still exists (it may have been deleted while phone was sleeping)
        const currentSessionId = activeSessionIdRef.current;
        const activeSessionExists =
          currentSessionId &&
          sessions.some((s) => s.session_id === currentSessionId);

        if (currentSessionId && !activeSessionExists) {
          // Active session was deleted while phone was sleeping
          console.log(
            `Active session ${currentSessionId} no longer exists, switching...`,
          );
          if (sessions.length > 0) {
            // Switch to the most recent session
            switchSession(sessions[0].session_id);
          } else {
            // No sessions left
            setActiveSessionId(null);
          }
        } else {
          // Force reconnect the active session WebSocket
          // This is more reliable than trying to sync over a stale connection
          // The reconnect will trigger sync in the ws.onopen handler
          // Use a small delay to allow the network stack to stabilize after wake
          setTimeout(() => {
            forceReconnectActiveSession();
          }, 300);
        }
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [fetchStoredSessions, forceReconnectActiveSession, switchSession]);

  // Clear background completion notification
  const clearBackgroundCompletion = useCallback(() => {
    setBackgroundCompletion(null);
  }, []);

  return {
    connected: eventsConnected,
    messages,
    sendPrompt,
    cancelPrompt,
    forceReset,
    newSession,
    switchSession,
    loadSession,
    loadMoreMessages,
    updateSessionName,
    renameSession,
    pinSession,
    removeSession,
    isStreaming,
    hasMoreMessages,
    actionButtons,
    sessionInfo,
    activeSessionId,
    activeSessions,
    storedSessions,
    fetchStoredSessions,
    backgroundCompletion,
    clearBackgroundCompletion,
    queueLength,
    queueMessages,
    queueConfig,
    fetchQueueMessages,
    deleteQueueMessage,
    addToQueue,
    moveQueueMessage,
    workspaces,
    acpServers,
    addWorkspace,
    removeWorkspace,
    refreshWorkspaces: fetchWorkspaces,
    forceReconnectActiveSession,
  };
}
