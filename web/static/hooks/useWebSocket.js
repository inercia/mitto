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
  MAX_MESSAGES,
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
  getMaxSeq,
  isStaleClientState,
} from "../lib.js";

import {
  getLastActiveSessionId,
  setLastActiveSessionId,
  getSingleExpandedGroupMode,
  setGroupExpanded,
  isGroupExpanded,
  getExpandedGroups,
  getFilterTabGrouping,
  FILTER_TAB,
} from "../utils/storage.js";

import { playAgentCompletedSound } from "../utils/audio.js";

import {
  secureFetch,
  authFetch,
  checkAuth,
  redirectToLogin,
} from "../utils/csrf.js";

import { apiUrl, wsUrl, getApiPrefix } from "../utils/api.js";

import { isNativeApp } from "../utils/native.js";

// Import WebSocket utilities (M1, M2 implementations)
import {
  createSeqTracker,
  isSeqDuplicate as isSeqDuplicateUtil,
  markSeqSeen as markSeqSeenUtil,
  calculateReconnectDelay,
} from "../utils/websocket.js";

// Time threshold (in ms) for considering the session potentially stale
// If the page has been hidden for longer than this, we do an explicit auth check
// before trying to reconnect. The server session expires after 24 hours.
const STALE_THRESHOLD_MS = 60 * 60 * 1000; // 1 hour

// Keepalive configuration for detecting zombie WebSocket connections and sequence sync
// On mobile, connections can appear open but be dead (zombie connections)
// Keepalive also piggybacks sequence numbers to detect out-of-sync situations
// Native macOS app uses shorter interval (5s) since it's local with no network latency
// Browser uses longer interval (10s) to reduce network overhead
const KEEPALIVE_INTERVAL_NATIVE_MS = 5000; // Send keepalive every 5 seconds in native app
const KEEPALIVE_INTERVAL_BROWSER_MS = 10000; // Send keepalive every 10 seconds in browser
const KEEPALIVE_TIMEOUT_MS = 10000; // Consider connection unhealthy if no response in 10 seconds
const KEEPALIVE_MAX_MISSED = 2; // Force reconnect after 2 missed keepalives

// Sync tolerance: only request sync if client is more than N sequences behind server.
// This avoids excessive sync requests during normal streaming where the markdown buffer
// may hold content briefly before flushing to the UI. A tolerance of 2 prevents
// sync requests when client is just 1-2 behind due to normal buffering delays.
const KEEPALIVE_SYNC_TOLERANCE = 2;

/**
 * Get the appropriate keepalive interval based on the runtime environment.
 * Native macOS app uses a shorter interval for faster sync detection.
 * @returns {number} Keepalive interval in milliseconds
 */
function getKeepaliveInterval() {
  return isNativeApp()
    ? KEEPALIVE_INTERVAL_NATIVE_MS
    : KEEPALIVE_INTERVAL_BROWSER_MS;
}

/**
 * Quick authentication check before WebSocket reconnection.
 * If auth is invalid (401), redirects to login page and never returns.
 * For network errors or server errors, returns true to allow reconnection to proceed
 * (the WebSocket reconnect will handle retries with exponential backoff).
 *
 * @returns {Promise<boolean>} True if auth is valid OR if status is unknown (network/server error).
 *                             False is only returned after redirecting to login (effectively unreachable).
 */
async function checkAuthOrRedirect() {
  try {
    // Quick auth check using the config endpoint
    const response = await fetch(apiUrl("/api/config"), {
      credentials: "same-origin",
    });
    checkAuth(response); // This will redirect if 401

    // If we got here, auth is valid (200) or there's a server error (5xx)
    // Either way, let reconnection proceed - the WebSocket will retry with backoff
    if (!response.ok) {
      console.warn(
        `Auth check returned status ${response.status}, allowing reconnection to proceed`,
      );
    }
    return true;
  } catch (err) {
    // Network error - let reconnection proceed
    // The WebSocket reconnection will naturally retry with exponential backoff
    console.warn(
      "Auth check network error, allowing reconnection to proceed:",
      err.message,
    );
    return true;
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

  // Track periodic session starts for toast notifications
  // { sessionId, sessionName, timestamp }
  const [periodicStarted, setPeriodicStarted] = useState(null);

  // Track background UI prompts for toast notifications
  // { sessionId, sessionName, question, timestamp }
  const [backgroundUIPrompt, setBackgroundUIPrompt] = useState(null);

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

  // Available slash commands for the active session (from ACP agent)
  // Array of { name: string, description: string, input_hint?: string }
  const [availableCommands, setAvailableCommands] = useState([]);

  // Session config options from the ACP agent (unified format for modes and other settings)
  // Array of { id: string, name: string, description?: string, category?: string, type: string, current_value: string, options: [] }
  // See https://agentclientprotocol.com/protocol/session-config-options
  const [configOptions, setConfigOptions] = useState([]);

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
  // Track last confirmed prompt ID per session (from connected message)
  // Used to verify delivery after zombie WebSocket timeout/reconnect
  // { sessionId: { promptId: string, seq: number } }
  const lastConfirmedPromptRef = useRef({});

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
  const isSeqDuplicate = useCallback(
    (sessionId, seq, lastMessageSeq) => {
      const tracker = getSeqTracker(sessionId);
      const isDuplicate = isSeqDuplicateUtil(tracker, seq, lastMessageSeq);
      if (isDuplicate) {
        console.log(
          `M1 dedup: Skipping duplicate seq ${seq} for session ${sessionId}`,
        );
      }
      return isDuplicate;
    },
    [getSeqTracker],
  );

  /**
   * Mark a sequence number as seen for a session.
   * Wrapper around utility function that manages per-session state.
   */
  const markSeqSeen = useCallback(
    (sessionId, seq) => {
      const tracker = getSeqTracker(sessionId);
      markSeqSeenUtil(tracker, seq);
    },
    [getSeqTracker],
  );

  /**
   * Clear seen sequences for a session (e.g., when session is deleted or reset).
   */
  const clearSeenSeqs = useCallback((sessionId) => {
    delete seenSeqsRef.current[sessionId];
  }, []);

  // Track pending gap fill requests to avoid duplicate requests
  // { sessionId: { afterSeq: number, requestTime: number } }
  const pendingGapFillRef = useRef({});

  // Debounce timeout for gap fill requests (ms)
  // We wait a bit before requesting to allow for out-of-order delivery
  const GAP_FILL_DEBOUNCE_MS = 500;

  // Track if this is a reconnection (vs initial connection)
  const wasConnectedRef = useRef(false);

  // Track when the page was last hidden (for staleness detection on mobile)
  const lastHiddenTimeRef = useRef(null);

  // Keepalive tracking for detecting zombie connections
  // { sessionId: { intervalId, lastAckTime, missedCount, pendingKeepalive } }
  const keepaliveRef = useRef({});

  /**
   * Check for gaps in message sequence and request missing events if needed.
   *
   * When we receive a message with max_seq, we can detect if we're missing events:
   * - If max_seq > our last known seq + 1, we have a gap
   * - We request the missing events via load_events with after_seq
   *
   * This is called from streaming message handlers (agent_message, tool_call, etc.)
   * to provide immediate gap detection instead of waiting for keepalive.
   *
   * @param {string} sessionId - The session ID
   * @param {number} maxSeq - The server's max_seq from the message
   * @param {number} msgSeq - The seq of the current message (optional)
   */
  const checkAndFillGap = useCallback(
    (sessionId, maxSeq, msgSeq) => {
      if (!maxSeq || maxSeq <= 0) return;

      const session = sessionsRef.current[sessionId];
      if (!session) return;

      // Get our last known seq (highest of messages in memory or lastLoadedSeq)
      const sessionMessages = session.messages || [];
      const clientMaxSeq = Math.max(
        getMaxSeq(sessionMessages),
        session.lastLoadedSeq || 0,
      );

      // If client has stale state (client > server), don't try to fill gaps
      // This will be handled by the stale detection in keepalive or events_loaded
      if (isStaleClientState(clientMaxSeq, maxSeq)) {
        return;
      }

      // Check if there's a gap: server has more events than we know about
      // We use a threshold of 1 to avoid false positives from out-of-order delivery
      // (the current message with msgSeq is being processed, so we should have msgSeq - 1)
      const expectedSeq = msgSeq ? msgSeq : clientMaxSeq + 1;
      const gap = maxSeq - expectedSeq;

      if (gap <= 0) {
        // No gap, or we're ahead (stale) - nothing to do
        return;
      }

      // We have a gap! Check if we already have a pending request
      const pending = pendingGapFillRef.current[sessionId];
      if (pending && Date.now() - pending.requestTime < GAP_FILL_DEBOUNCE_MS) {
        // Recent request is pending, skip to avoid duplicate requests
        return;
      }

      // Schedule a gap fill request (debounced)
      console.log(
        `[gap-fill] Session ${sessionId}: Detected gap of ${gap} events (client has up to ${clientMaxSeq}, server has ${maxSeq}), scheduling fill request`,
      );

      // Clear any existing timeout
      if (pending?.timeoutId) {
        clearTimeout(pending.timeoutId);
      }

      // Schedule the request with debounce
      const timeoutId = setTimeout(() => {
        const ws = sessionWsRefs.current[sessionId];
        if (ws && ws.readyState === WebSocket.OPEN) {
          // Request events after our last known seq
          const afterSeq = clientMaxSeq;
          console.log(
            `[gap-fill] Session ${sessionId}: Requesting events after seq ${afterSeq}`,
          );
          ws.send(
            JSON.stringify({
              type: "load_events",
              data: {
                after_seq: afterSeq,
                limit: 100, // Request up to 100 events to fill the gap
              },
            }),
          );
        }
        // Clear pending state after request is sent
        delete pendingGapFillRef.current[sessionId];
      }, GAP_FILL_DEBOUNCE_MS);

      pendingGapFillRef.current[sessionId] = {
        afterSeq: clientMaxSeq,
        requestTime: Date.now(),
        timeoutId,
      };
    },
    [sessionsRef],
  );

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
    async (message, imageIds = [], fileIds = []) => {
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
              file_ids: fileIds,
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

  // Get current session info (enhanced with message count)
  const sessionInfo = useMemo(() => {
    if (!activeSessionId || !sessions[activeSessionId]) return null;
    const session = sessions[activeSessionId];
    const info = session.info || {};
    // Include message count from the messages array
    return {
      ...info,
      messageCount: session.messages?.length || 0,
    };
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

  // Check if active session is currently loading more messages
  const isLoadingMore = useMemo(() => {
    if (!activeSessionId || !sessions[activeSessionId]) return false;
    return sessions[activeSessionId].isLoadingMore || false;
  }, [sessions, activeSessionId]);

  // Check if active session has reached the message limit
  // When true, we've loaded MAX_MESSAGES and can't load more to protect memory
  const hasReachedLimit = useMemo(() => {
    if (!activeSessionId || !sessions[activeSessionId]) return false;
    const messageCount = sessions[activeSessionId].messages?.length || 0;
    return messageCount >= MAX_MESSAGES;
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
        buttons: buttons.map((b) => b.label),
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
    // Check if session is archived (from session info or stored session)
    // Archived sessions should not be marked as "active" since they have no ACP connection
    const isArchived = data.info?.archived || storedSession?.archived || false;
    // Check if archive is pending (waiting for agent to finish)
    const isArchivePending =
      data.info?.archive_pending || storedSession?.archive_pending || false;
    return {
      session_id: id,
      name: data.info?.name || "New conversation",
      acp_server: data.info?.acp_server || "",
      working_dir: workingDir,
      created_at: data.info?.created_at || new Date().toISOString(),
      updated_at: data.info?.updated_at || new Date().toISOString(),
      last_user_message_at: lastUserMsgTime || data.info?.last_user_message_at,
      // Archived sessions are not "active" - they have no ACP connection
      status: isArchived ? "archived" : "active",
      isActive: !isArchived,
      isStreaming: !isArchived && (data.isStreaming || false),
      messageCount: data.messages?.length || 0,
      archived: isArchived,
      archive_pending: isArchivePending,
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

        // Update available slash commands from agent
        if (msg.data.available_commands) {
          setAvailableCommands(msg.data.available_commands);
        }

        // Update session config options from agent
        // This is the unified format that includes modes and other settings
        if (msg.data.config_options) {
          setConfigOptions(msg.data.config_options);
        } else {
          // Clear config options if not provided
          setConfigOptions([]);
        }

        // Store last confirmed prompt info for delivery verification on reconnect
        // This helps verify if a pending prompt was actually delivered when
        // reconnecting after a zombie WebSocket timeout
        if (msg.data.last_user_prompt_id) {
          lastConfirmedPromptRef.current[sessionId] = {
            promptId: msg.data.last_user_prompt_id,
            seq: msg.data.last_user_prompt_seq || 0,
          };
          console.log(
            `Connected: last confirmed prompt for session ${sessionId}:`,
            msg.data.last_user_prompt_id,
          );
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
                runner_restricted:
                  msg.data.runner_restricted ?? session.info?.runner_restricted,
                // Preserve archived flag from existing session info (set by switchSession)
                archived: session.info?.archived || false,
                // Preserve archive_pending flag from existing session info
                archive_pending: session.info?.archive_pending || false,
                // Periodic enabled state from server
                periodic_enabled:
                  msg.data.periodic_enabled ??
                  session.info?.periodic_enabled ??
                  false,
              },
              isStreaming: msg.data.is_prompting || false,
            },
          };
        });
        break;

      case "agent_message": {
        const msgSeq = msg.data.seq;
        const maxSeq = msg.data.max_seq;
        const htmlLen = msg.data.html?.length || 0;
        const isPromptingFromServer = msg.data.is_prompting;
        console.log(
          "[DEBUG agent_message] received:",
          "sessionId:",
          sessionId,
          "seq:",
          msgSeq,
          "max_seq:",
          maxSeq,
          "html_len:",
          htmlLen,
          "is_prompting:",
          isPromptingFromServer,
          "html_preview:",
          msg.data.html?.substring(0, 80) + (htmlLen > 80 ? "..." : ""),
        );

        // Check for gaps using max_seq (immediate gap detection)
        if (maxSeq) {
          checkAndFillGap(sessionId, maxSeq, msgSeq);
        }

        // Agent is responding - this proves any pending prompts were received.
        // Resolve pending sends to prevent false "delivery not confirmed" errors on mobile.
        if (resolvePendingSendsRef.current) {
          resolvePendingSendsRef.current(sessionId);
        }

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        // Frontend only needs to coalesce chunks with the same seq (streaming continuation).
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) {
            console.warn(
              "[DEBUG agent_message] No session found for:",
              sessionId,
            );
            return prev;
          }
          let messages = [...session.messages];
          const last = messages[messages.length - 1];

          // M1 fix: Check for duplicate events (but allow same-seq for coalescing)
          if (isSeqDuplicate(sessionId, msgSeq, last?.seq)) {
            console.log(
              "[DEBUG agent_message] Skipping duplicate seq:",
              msgSeq,
            );
            return prev; // Skip duplicate
          }

          // Check if we should append to existing message:
          // - Same seq means it's a continuation of the same logical message
          // - Or if last message is incomplete agent message (backward compat)
          const sameSeq = msgSeq && last?.seq === msgSeq;
          const shouldAppend =
            last &&
            last.role === ROLE_AGENT &&
            !last.complete &&
            (sameSeq || !msgSeq);

          console.log("[DEBUG agent_message] State update:", {
            msgSeq,
            lastSeq: last?.seq,
            lastRole: last?.role,
            lastComplete: last?.complete,
            sameSeq,
            shouldAppend,
            prevHtmlLen: last?.html?.length || 0,
            newHtmlLen: htmlLen,
            messageCount: messages.length,
          });

          if (shouldAppend) {
            // Safeguard: Check if the incoming HTML is a duplicate of what we already have.
            // This can happen when the backend sends the same complete message multiple times
            // (e.g., due to replayBufferedEventsWithDedup after a load_events fallback).
            // If the existing HTML already ends with the incoming HTML, skip the append.
            const existingHtml = last.html || "";
            const incomingHtml = msg.data.html;
            if (existingHtml.endsWith(incomingHtml)) {
              console.log(
                "[DEBUG agent_message] Skipping duplicate append - HTML already contains this content, seq:",
                msgSeq,
              );
              return prev; // Skip duplicate append
            }

            const newHtml = existingHtml + incomingHtml;
            messages[messages.length - 1] = {
              ...last,
              html: newHtml,
            };
            console.log(
              "[DEBUG agent_message] Appended to existing message, new html_len:",
              newHtml.length,
            );
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
            console.log(
              "[DEBUG agent_message] Created new message, total messages:",
              messages.length,
            );
          }
          const isPrompting = isPromptingFromServer ?? true;
          console.log(
            "[DEBUG agent_message] Setting isStreaming to:",
            isPrompting,
          );
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: isPrompting },
          };
        });
        break;
      }

      case "agent_thought": {
        const msgSeq = msg.data.seq;
        const maxSeq = msg.data.max_seq;
        console.log(
          "agent_thought received:",
          sessionId,
          "seq:",
          msgSeq,
          "max_seq:",
          maxSeq,
          "text:",
          msg.data.text?.substring(0, 50) + "...",
          "is_prompting:",
          msg.data.is_prompting,
        );

        // Check for gaps using max_seq (immediate gap detection)
        if (maxSeq) {
          checkAndFillGap(sessionId, maxSeq, msgSeq);
        }

        // Agent is responding - this proves any pending prompts were received.
        // Resolve pending sends to prevent false "delivery not confirmed" errors on mobile.
        if (resolvePendingSendsRef.current) {
          resolvePendingSendsRef.current(sessionId);
        }

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
          if (
            last &&
            last.role === ROLE_THOUGHT &&
            !last.complete &&
            (sameSeq || !msgSeq)
          ) {
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
        const maxSeq = msg.data.max_seq;
        console.log(
          "tool_call received:",
          sessionId,
          "seq:",
          msgSeq,
          "max_seq:",
          maxSeq,
          "id:",
          msg.data.id,
          "title:",
          msg.data.title,
          "status:",
          msg.data.status,
          "is_prompting:",
          msg.data.is_prompting,
        );

        // Check for gaps using max_seq (immediate gap detection)
        if (maxSeq) {
          checkAndFillGap(sessionId, maxSeq, msgSeq);
        }

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

      case "tool_update": {
        const msgSeq = msg.data.seq;
        const maxSeq = msg.data.max_seq;
        console.log(
          "tool_update received:",
          sessionId,
          "seq:",
          msgSeq,
          "max_seq:",
          maxSeq,
          "id:",
          msg.data.id,
          "status:",
          msg.data.status,
          "is_prompting:",
          msg.data.is_prompting,
        );

        // Check for gaps using max_seq (immediate gap detection)
        if (maxSeq) {
          checkAndFillGap(sessionId, maxSeq, msgSeq);
        }

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
      }

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

          console.log(
            "[ActionButtons] Storing buttons for session:",
            sessionId,
          );
          return {
            ...prev,
            [sessionId]: {
              ...session,
              actionButtons: msg.data.buttons || [],
            },
          };
        });
        break;

      case "ui_prompt": {
        // UI prompt from an MCP tool - display yes/no or select prompt
        console.log("[UIPrompt] Received ui_prompt message:", {
          sessionId,
          requestId: msg.data.request_id,
          promptType: msg.data.prompt_type,
          question: msg.data.question,
          options: msg.data.options,
          timeoutSeconds: msg.data.timeout_seconds,
        });

        // Check if we should show a notification
        // Show notification when:
        // 1. This is not the active session, OR
        // 2. The document is hidden (user is looking at another app/tab)
        const isBackgroundUIPrompt =
          sessionId !== activeSessionIdRef.current ||
          document.visibilityState === "hidden";

        if (isBackgroundUIPrompt) {
          const currentSession = sessionsRef.current[sessionId];
          const sessionName = currentSession?.info?.name || "Conversation";
          const question = msg.data.question || "Agent needs input";

          // Set background UI prompt state for in-app toast
          setBackgroundUIPrompt({
            sessionId,
            sessionName,
            question,
            timestamp: Date.now(),
          });

          // Check if native notifications are enabled (macOS app only)
          const useNativeNotification =
            window.mittoNativeNotificationsEnabled &&
            typeof window.mittoShowNativeNotification === "function";

          if (useNativeNotification) {
            // Show native macOS notification
            console.log(
              "[UIPrompt] Showing native notification for background prompt",
            );
            window.mittoShowNativeNotification(
              sessionName,
              question,
              sessionId,
            );
          }
        }

        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) {
            console.warn("[UIPrompt] Session not found:", sessionId);
            return prev;
          }

          // Store the active UI prompt (unified: MCP questions, permissions)
          return {
            ...prev,
            [sessionId]: {
              ...session,
              activeUIPrompt: {
                requestId: msg.data.request_id,
                promptType: msg.data.prompt_type,
                question: msg.data.question,
                options: msg.data.options || [],
                timeoutSeconds: msg.data.timeout_seconds,
                receivedAt: Date.now(),
                // New fields for unified prompts
                title: msg.data.title || null,
                toolCallId: msg.data.tool_call_id || null,
                blocking: msg.data.blocking !== false, // Default true for backwards compat
              },
            },
          };
        });
        break;
      }

      case "ui_prompt_dismiss":
        // Dismiss an active UI prompt (timeout, cancelled, or replaced)
        console.log("[UIPrompt] Received ui_prompt_dismiss message:", {
          sessionId,
          requestId: msg.data.request_id,
          reason: msg.data.reason,
        });
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;

          // Only dismiss if the request ID matches
          if (session.activeUIPrompt?.requestId !== msg.data.request_id) {
            console.log("[UIPrompt] Dismiss ignored - different request_id");
            return prev;
          }

          return {
            ...prev,
            [sessionId]: {
              ...session,
              activeUIPrompt: null,
            },
          };
        });
        break;

      case "prompt_complete": {
        // Check if this is a background session completing (not the active one)
        const currentSession = sessionsRef.current[sessionId];
        const isBackgroundSession = sessionId !== activeSessionIdRef.current;
        const wasStreaming = currentSession?.isStreaming;
        const lastMessage =
          currentSession?.messages?.[currentSession.messages.length - 1];
        const maxSeq = msg.data.max_seq;

        console.log(
          "[DEBUG prompt_complete] received:",
          "sessionId:",
          sessionId,
          "event_count:",
          msg.data.event_count,
          "max_seq:",
          maxSeq,
          "wasStreaming:",
          wasStreaming,
          "lastMessage:",
          lastMessage
            ? {
                role: lastMessage.role,
                complete: lastMessage.complete,
                seq: lastMessage.seq,
                html_len: lastMessage.html?.length || 0,
              }
            : null,
        );

        // Check for gaps using max_seq (immediate gap detection)
        // This is important for prompt_complete as it signals the end of a response
        if (maxSeq) {
          checkAndFillGap(sessionId, maxSeq, null);
        }

        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) {
            console.warn(
              "[DEBUG prompt_complete] No session found for:",
              sessionId,
            );
            return prev;
          }
          const messages = [...session.messages];
          const lastIdx = messages.length - 1;

          console.log("[DEBUG prompt_complete] State update:", {
            messageCount: messages.length,
            lastIdx,
            lastMessage:
              lastIdx >= 0
                ? {
                    role: messages[lastIdx].role,
                    complete: messages[lastIdx].complete,
                    seq: messages[lastIdx].seq,
                    html_len: messages[lastIdx].html?.length || 0,
                  }
                : null,
          });

          if (lastIdx >= 0) {
            const last = messages[lastIdx];
            if (last.role === ROLE_AGENT || last.role === ROLE_THOUGHT) {
              messages[lastIdx] = { ...last, complete: true };
              console.log(
                "[DEBUG prompt_complete] Marked last message as complete, html_len:",
                last.html?.length || 0,
              );
            }
          }
          return {
            ...prev,
            [sessionId]: { ...session, messages, isStreaming: false },
          };
        });

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
        console.log(
          "[prompt_complete] wasStreaming:",
          wasStreaming,
          "soundEnabled:",
          window.mittoAgentCompletedSoundEnabled,
          "isBackgroundSession:",
          isBackgroundSession,
        );
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

        // Sync streaming state with server
        // This ensures the UI reflects the actual server state (agent responding or not)
        const serverIsPrompting = msg.data?.is_prompting || false;
        const currentSession = sessionsRef.current[sessionId];
        if (
          currentSession &&
          currentSession.isStreaming !== serverIsPrompting
        ) {
          console.log(
            `[keepalive] Session ${sessionId} streaming state mismatch: client=${currentSession.isStreaming}, server=${serverIsPrompting}, syncing`,
          );
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;
            return {
              ...prev,
              [sessionId]: { ...session, isStreaming: serverIsPrompting },
            };
          });
        }

        // Sync queue length from keepalive (for multi-tab sync and mobile wake recovery)
        // Only update if this is the active session to avoid unnecessary state updates
        if (
          msg.data?.queue_length !== undefined &&
          sessionId === activeSessionIdRef.current
        ) {
          setQueueLength((prev) => {
            if (prev !== msg.data.queue_length) {
              console.log(
                `[keepalive] Queue length sync: ${prev} -> ${msg.data.queue_length}`,
              );
              return msg.data.queue_length;
            }
            return prev;
          });
        }

        // Sync session status from keepalive (detect completed/error sessions)
        if (
          msg.data?.status &&
          currentSession?.info?.status !== msg.data.status
        ) {
          console.log(
            `[keepalive] Session ${sessionId} status sync: ${currentSession?.info?.status} -> ${msg.data.status}`,
          );
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;
            return {
              ...prev,
              [sessionId]: {
                ...session,
                info: { ...session.info, status: msg.data.status },
              },
            };
          });
        }

        // Sync is_running state (detect if background session disconnected)
        // This is useful for showing a "reconnect" indicator in the UI
        const serverIsRunning = msg.data?.is_running ?? true;
        if (
          currentSession?.info?.isRunning !== undefined &&
          currentSession.info.isRunning !== serverIsRunning
        ) {
          console.log(
            `[keepalive] Session ${sessionId} running state sync: ${currentSession.info.isRunning} -> ${serverIsRunning}`,
          );
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;
            return {
              ...prev,
              [sessionId]: {
                ...session,
                info: { ...session.info, isRunning: serverIsRunning },
              },
            };
          });
        }

        // Check sequence number sync between client and server
        // This detects out-of-sync situations where the client missed messages OR has stale state
        // Note: max_seq is the new field name, server_max_seq is deprecated but kept for backward compat
        const serverMaxSeq = msg.data?.max_seq || msg.data?.server_max_seq || 0;
        if (serverMaxSeq > 0) {
          const session = sessionsRef.current[sessionId];
          const sessionMessages = session?.messages || [];
          // Use the higher of: max seq from messages OR lastLoadedSeq (which includes session_end, etc.)
          const clientMaxSeq = Math.max(
            getMaxSeq(sessionMessages),
            session?.lastLoadedSeq || 0,
          );

          if (isStaleClientState(clientMaxSeq, serverMaxSeq)) {
            // Client has stale state! Server is always right.
            // This happens when mobile client reconnects after phone was sleeping,
            // or after server restart while client was offline.
            // Trigger a full reload by requesting initial events (no after_seq).
            console.log(
              `[keepalive] Session ${sessionId} has STALE state: client_max_seq=${clientMaxSeq} > server_max_seq=${serverMaxSeq}, triggering full reload`,
            );
            const ws = sessionWsRefs.current[sessionId];
            if (ws && ws.readyState === WebSocket.OPEN) {
              // Request initial load (last 50 messages) - server will detect stale and fall back
              ws.send(
                JSON.stringify({
                  type: "load_events",
                  data: { limit: INITIAL_EVENTS_LIMIT },
                }),
              );
            }
          } else if (serverMaxSeq > clientMaxSeq + KEEPALIVE_SYNC_TOLERANCE) {
            // We're significantly behind! Request missing events.
            // Using tolerance to avoid sync noise during normal streaming where
            // markdown buffer may hold content briefly before flushing to UI.
            console.log(
              `[keepalive] Session ${sessionId} is behind: client_max_seq=${clientMaxSeq}, server_max_seq=${serverMaxSeq} (tolerance=${KEEPALIVE_SYNC_TOLERANCE}), requesting sync`,
            );
            const ws = sessionWsRefs.current[sessionId];
            if (ws && ws.readyState === WebSocket.OPEN) {
              ws.send(
                JSON.stringify({
                  type: "load_events",
                  data: { after_seq: clientMaxSeq },
                }),
              );
            }
          }
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
        const newMessages = convertEventsToMessages(events, {
          sessionId,
          apiPrefix: getApiPrefix(),
        });
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
        const maxSeq = msg.data.max_seq || lastSeq; // Use max_seq if available, fallback to lastSeq
        const isPrompting = msg.data.is_prompting || false;
        const totalCount = msg.data.total_count || 0;

        // Check if client has stale state: client's max seq > server's max_seq
        // This happens when mobile client reconnects after being in background with
        // cached state from a different server instance or after server restart.
        // In this case, server wins - we treat this as a fresh load.
        //
        // IMPORTANT: We must check BOTH lastLoadedSeq AND getMaxSeq(messages) because:
        // - lastLoadedSeq tracks the highest seq from events_loaded responses
        // - getMaxSeq(messages) tracks the highest seq from messages in memory (including streamed)
        // If either is higher than server's max_seq, the client has stale state.
        // This fixes a bug where messages in memory had high seq values from a previous
        // server session, but lastLoadedSeq was reset, causing stale detection to fail.
        const currentSession = sessionsRef.current[sessionId];
        const sessionMessages = currentSession?.messages || [];
        const clientLastSeq = Math.max(
          getMaxSeq(sessionMessages),
          currentSession?.lastLoadedSeq || 0,
        );
        const isStaleClient = isStaleClientState(clientLastSeq, maxSeq);

        console.log("[DEBUG events_loaded] received:", {
          sessionId,
          eventCount: events.length,
          isPrepend,
          hasMore,
          firstSeq,
          lastSeq,
          maxSeq,
          isPrompting,
          totalCount,
          clientLastSeq,
          clientLastLoadedSeq: currentSession?.lastLoadedSeq || 0,
          clientMaxSeqFromMessages: getMaxSeq(sessionMessages),
          messageCount: sessionMessages.length,
          isStaleClient,
        });

        // DEBUG: Log event order to check if they're in correct seq order
        console.log(
          "[DEBUG events_loaded] Event order:",
          events.map((e) => ({
            seq: e.seq,
            type: e.type,
            preview:
              e.type === "agent_message"
                ? e.data?.html?.substring(0, 50) + "..."
                : e.type === "tool_call"
                  ? e.data?.title
                  : e.type === "user_prompt"
                    ? e.data?.message?.substring(0, 50) + "..."
                    : e.type,
          })),
        );

        // M1 fix: When client has stale state, reset the seq tracker BEFORE processing events.
        // Without this, the seq tracker's highestSeq from the stale state would cause
        // all fresh events from the server to be wrongly rejected as duplicates.
        // Example: if client had highestSeq=200 but server now has lastSeq=50,
        // any event with seq < 100 (highestSeq - MAX_RECENT_SEQS) would be rejected!
        if (isStaleClient) {
          console.log(
            `[M1 fix] Resetting seq tracker for stale client (highestSeq was from stale state)`,
          );
          clearSeenSeqs(sessionId);
        }

        // Convert events to messages
        const newMessages = convertEventsToMessages(events, {
          sessionId,
          apiPrefix: getApiPrefix(),
        });

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
          } else if (session.messages.length === 0 || isStaleClient) {
            // Initial load OR stale client recovery - replace all messages
            // When client has stale state, server wins - we discard client's messages
            // and use the fresh data from server
            if (isStaleClient) {
              console.log(
                `[Stale client recovery] Replacing ${session.messages.length} stale messages with ${newMessages.length} fresh messages`,
              );
            }
            messages = newMessages;
          } else {
            // Sync after reconnect - merge with deduplication
            // Use mergeMessagesWithSync to handle cases where:
            // 1. Messages already in UI have seq values from streaming
            // 2. Server returns events that overlap with what's already displayed
            messages = mergeMessagesWithSync(session.messages, newMessages);
          }

          // Track the highest seq we've seen from events_loaded
          // This includes session_end and other events that don't become messages
          // For stale client recovery, reset to server's lastSeq
          const newLastLoadedSeq = isStaleClient
            ? lastSeq
            : Math.max(session.lastLoadedSeq || 0, lastSeq);

          const updatedSession = {
            ...session,
            messages: limitMessages(messages),
            isStreaming: isPrompting,
            hasMoreMessages: hasMore,
            // For stale client recovery, reset firstLoadedSeq to server's value
            firstLoadedSeq: isPrepend
              ? firstSeq
              : isStaleClient
                ? firstSeq
                : session.firstLoadedSeq || firstSeq,
            // Track highest seq from loaded events (includes session_end, etc.)
            lastLoadedSeq: newLastLoadedSeq,
            // Flag to indicate this is a fresh load - used for instant scroll positioning
            justLoaded:
              !isPrepend && (session.messages.length === 0 || isStaleClient),
            // Clear loading state when prepend (load more) completes
            isLoadingMore: isPrepend ? false : session.isLoadingMore,
          };

          const newState = {
            ...prev,
            [sessionId]: updatedSession,
          };

          // Synchronously update sessionsRef to prevent keepalive race conditions
          // This ensures the keepalive handler sees the updated lastLoadedSeq immediately,
          // avoiding loops where client_max_seq appears stale after receiving session_end events
          sessionsRef.current = newState;

          return newState;
        });

        // If client had stale state and there are more messages to load,
        // automatically load all remaining messages to prevent user confusion.
        // This handles the case where a mobile client reconnects after being in background
        // with stale sequence numbers - without this, the user would only see the last 50 messages.
        if (isStaleClient && hasMore && firstSeq > 1) {
          console.log(
            `[Stale client recovery] Auto-loading remaining ${firstSeq - 1} events for session ${sessionId}`,
          );
          // Small delay to let the UI update first, then load remaining messages
          setTimeout(() => {
            const currentWs = sessionWsRefs.current[sessionId];
            if (currentWs && currentWs.readyState === WebSocket.OPEN) {
              // Request all events before the first one we just loaded
              currentWs.send(
                JSON.stringify({
                  type: "load_events",
                  data: {
                    before_seq: firstSeq,
                    limit: firstSeq - 1, // Load all remaining events
                  },
                }),
              );
            }
          }, 100);
        }
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
          max_seq,
          is_mine,
          prompt_id,
          message,
          image_ids,
          sender_id,
          is_prompting,
        } = msg.data;
        console.log("user_prompt received:", {
          seq,
          max_seq,
          is_mine,
          prompt_id,
          sender_id,
          is_prompting,
          message: message?.substring(0, 50),
          is_queue_message: sender_id === "queue",
        });

        // Check for gaps using max_seq (immediate gap detection)
        if (max_seq) {
          checkAndFillGap(sessionId, max_seq, seq);
        }

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
            if (!session) {
              console.log(
                "user_prompt: No session found for:",
                sessionId,
                "skipping message add",
              );
              return prev;
            }

            // Check if this message already exists (by seq number)
            // Only dedupe by seq - content deduplication was too aggressive and blocked
            // legitimate periodic prompts (same text sent on each run).
            // The seq number is authoritative: if the server sends a new seq, it's a new message.
            const alreadyExists = session.messages.some((m) => {
              if (m.role !== ROLE_USER) return false;
              // If seq matches exactly, it's the same message
              if (seq && m.seq && m.seq === seq) return true;
              return false;
            });

            if (alreadyExists) {
              console.log(
                "Skipping duplicate user_prompt:",
                prompt_id,
                "seq:",
                seq,
                "sender_id:",
                sender_id,
              );
              return prev;
            }

            console.log(
              "user_prompt: Adding message to UI:",
              "prompt_id:",
              prompt_id,
              "seq:",
              seq,
              "sender_id:",
              sender_id,
              "message_preview:",
              message?.substring(0, 50),
              "existing_messages:",
              session.messages.length,
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
            console.log(
              "user_prompt: Message added successfully:",
              "new_message_count:",
              messages.length,
              "last_message_role:",
              messages[messages.length - 1]?.role,
              "last_message_text_preview:",
              messages[messages.length - 1]?.text?.substring(0, 30),
            );
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

          // Update queueMessages based on the action to keep in sync
          const action = msg.data.action;
          const messageId = msg.data.message_id;
          if (action === "removed" && messageId) {
            // Remove the message from local state
            setQueueMessages((prev) => prev.filter((m) => m.id !== messageId));
          } else if (action === "cleared") {
            // Clear all messages
            setQueueMessages([]);
          }
          // For "added" action, we don't have the full message data, so dispatch event to refresh

          // Dispatch event for queue dropdown to refresh (handles "added" case)
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

      case "acp_start_failed":
        // Server notifies that the ACP server failed to start
        console.error("ACP start failed:", msg.data);
        if (msg.data) {
          // Dispatch event for toast notification
          window.dispatchEvent(
            new CustomEvent("mitto:acp_start_failed", { detail: msg.data }),
          );
        }
        break;

      case "acp_stopped":
        // Server notifies that the ACP connection for this session was stopped.
        // This happens when the session is archived or explicitly closed.
        // We need to update the session state to prevent further prompts.
        console.log(
          "ACP stopped for session:",
          sessionId,
          "reason:",
          msg.data?.reason,
        );
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;

          // For archived sessions, show a neutral system message
          // For other stop reasons (errors, crashes), show an error message
          const reason = msg.data?.reason || "unknown reason";
          const isArchived =
            reason === "archived" || reason === "archived_timeout";
          const messageRole = isArchived ? "system" : "error";
          const messageText = isArchived
            ? "Session archived. Unarchive to continue."
            : `Session stopped: ${reason}. Unarchive to continue.`;

          return {
            ...prev,
            [sessionId]: {
              ...session,
              isRunning: false,
              isStreaming: false,
              // Add a system/error message to inform the user
              messages: [
                ...session.messages,
                {
                  role: messageRole,
                  text: messageText,
                  timestamp: Date.now(),
                },
              ],
            },
          };
        });
        // Also update stored sessions
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === sessionId ? { ...s, isStreaming: false } : s,
          ),
        );
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

      case "plan": {
        // Agent sent a plan update with task entries
        const entries = msg.data?.entries || [];
        console.log(`Plan update received: ${entries.length} entries`);
        // Dispatch event for AgentPlanPanel to handle
        window.dispatchEvent(
          new CustomEvent("mitto:plan_update", {
            detail: { sessionId, entries },
          }),
        );
        break;
      }

      case "available_commands_updated":
        // Agent sent updated list of available slash commands
        if (msg.data?.commands) {
          console.log(
            `Available commands updated: ${msg.data.commands.length} commands`,
          );
          setAvailableCommands(msg.data.commands);
        }
        break;

      case "config_option_changed":
        // Config option changed (by user or agent)
        // Update the current_value for the specified config option
        // Use !== undefined to allow falsy values like empty strings
        if (msg.data?.config_id && msg.data?.value !== undefined) {
          console.log(
            `Config option changed: ${msg.data.config_id} = ${msg.data.value}`,
          );
          setConfigOptions((prev) =>
            prev.map((opt) =>
              opt.id === msg.data.config_id
                ? { ...opt, current_value: msg.data.value }
                : opt,
            ),
          );
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
        // Calculate lastSeenSeq dynamically from messages in state (not localStorage)
        // This avoids stale localStorage issues, especially in WKWebView
        const session = sessionsRef.current[sessionId];
        const sessionMessages = session?.messages || [];
        // Use the higher of: max seq from messages OR lastLoadedSeq (which includes session_end, etc.)
        const lastSeq = Math.max(
          getMaxSeq(sessionMessages),
          session?.lastLoadedSeq || 0,
        );
        if (lastSeq > 0) {
          // Reconnection: sync events after lastSeq
          console.log(
            `Syncing session ${sessionId} from seq ${lastSeq} (lastLoadedSeq=${session?.lastLoadedSeq}, messages=${sessionMessages.length})`,
          );
          ws.send(
            JSON.stringify({
              type: "load_events",
              data: { after_seq: lastSeq },
            }),
          );
        } else {
          // Initial load: load last N events
          console.log(`Loading session ${sessionId} events (initial load)`);
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

          // Send keepalive with last_seen_seq
          // This allows the server to tell us if we're behind
          const session = sessionsRef.current[sessionId];
          const sessionMessages = session?.messages || [];
          // Use the higher of: max seq from messages OR lastLoadedSeq (which includes session_end, etc.)
          const lastSeenSeq = Math.max(
            getMaxSeq(sessionMessages),
            session?.lastLoadedSeq || 0,
          );

          keepaliveRef.current[sessionId] = {
            ...keepaliveRef.current[sessionId],
            intervalId,
            pendingKeepalive: true,
            lastSentTime: Date.now(),
          };

          currentWs.send(
            JSON.stringify({
              type: "keepalive",
              data: { client_time: Date.now(), last_seen_seq: lastSeenSeq },
            }),
          );
        }, getKeepaliveInterval());

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
        // Note: We intentionally do NOT clear isStreaming here.
        // The server may still be processing a prompt even if the WebSocket dropped.
        // On reconnection, the 'connected' message will sync the correct is_prompting state.
        // Setting isStreaming: false here would cause a desync where the user sees
        // the Send button (instead of Stop) but the server rejects with "prompt already in progress".

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
          console.log(
            `Scheduling reconnect for session ${sessionId} (attempt ${attempt + 1}, delay ${delay}ms)`,
          );

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

  // Helper to expand the target session's group when navigating
  // Always expands the group containing the session so it's visible in the sidebar
  // In accordion mode, also collapses all other groups
  const expandGroupForSession = useCallback((sessionId, workingDir, acpServer) => {
    // Build the group key for this session based on current grouping mode
    const groupingMode = getFilterTabGrouping(FILTER_TAB.CONVERSATIONS);
    let groupKey;
    if (groupingMode === "server") {
      groupKey = acpServer || "Unknown";
    } else if (groupingMode === "workspace") {
      // Workspace mode uses composite key: working_dir|acp_server
      groupKey = `${workingDir || ""}|${acpServer || ""}`;
    }

    // Only expand if we have a valid group key and groups are being used
    if (groupKey && groupingMode && groupingMode !== "none") {
      // In accordion mode, collapse all other groups first
      if (getSingleExpandedGroupMode()) {
        const expandedGroups = getExpandedGroups();
        for (const key of Object.keys(expandedGroups)) {
          if (key !== groupKey && isGroupExpanded(key)) {
            setGroupExpanded(key, false);
          }
        }
      }
      // Always expand the session's group so it's visible
      if (!isGroupExpanded(groupKey)) {
        setGroupExpanded(groupKey, true);
      }
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

      // Get session info from stored sessions (for accordion mode group expansion)
      const storedSession = storedSessionsRef.current?.find(
        (s) => s.session_id === sessionId
      );
      const workingDir =
        existingSession?.info?.working_dir ||
        storedSession?.working_dir ||
        "";
      const acpServer =
        existingSession?.info?.acp_server ||
        storedSession?.acp_server ||
        "";

      // In accordion mode, expand the group containing this session
      // (and collapse all other groups)
      expandGroupForSession(sessionId, workingDir, acpServer);

      if (hasLoadedMessages && hasWorkingDir) {
        // Session already has messages and working_dir, just set it active
        setActiveSessionId(sessionId);

        // Ensure WebSocket is connected and synced
        // On mobile, the WebSocket may have died while the phone slept
        // If not connected, connect now - the onopen handler will sync events
        const existingWs = sessionWsRefs.current[sessionId];
        if (!existingWs || existingWs.readyState !== WebSocket.OPEN) {
          console.log(
            `Session ${sessionId} has messages but WebSocket is not connected, reconnecting...`,
          );
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
                archived: meta.archived || false,
                isReadOnly: meta.isReadOnly || false,
              },
              isStreaming: existing.isStreaming || false,
              // Reset these to prevent stale UI state while loading
              hasMoreMessages:
                existing.messages?.length > 0
                  ? existing.hasMoreMessages
                  : false,
              firstLoadedSeq:
                existing.messages?.length > 0
                  ? existing.firstLoadedSeq
                  : undefined,
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
    [connectToSession, expandGroupForSession],
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

      case "session_archived":
        // Update session archived state in stored sessions
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, archived: msg.data.archived, archive_pending: false }
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
              info: {
                ...session.info,
                archived: msg.data.archived,
                archive_pending: false,
              },
            },
          };
        });
        break;

      case "session_archive_pending":
        // Update session archive_pending state (archiving initiated, waiting for agent to finish)
        console.log(
          `[global] Session archive pending: ${msg.data.session_id} -> ${msg.data.archive_pending}`,
        );
        // Update in stored sessions (for sidebar display)
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, archive_pending: msg.data.archive_pending }
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
              info: {
                ...session.info,
                archive_pending: msg.data.archive_pending,
              },
            },
          };
        });
        break;

      case "session_streaming":
        // Update session streaming state (agent responding or not)
        // This is broadcast when any session starts or stops streaming
        console.log(
          `[global] Session streaming state changed: ${msg.data.session_id} -> ${msg.data.is_streaming}`,
        );
        // Update in stored sessions (for sidebar display)
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, isStreaming: msg.data.is_streaming }
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
              isStreaming: msg.data.is_streaming,
            },
          };
        });
        break;

      case "periodic_updated":
        // Update session periodic state
        // This is broadcast when any session's periodic state changes
        //
        // Two separate concepts:
        // - periodic_configured: true if periodic config exists (determines UI mode - shows frequency panel)
        // - periodic_enabled: true if periodic runs are active (determines lock state)
        //
        // Also includes frequency and next_scheduled_at for cross-client sync
        console.log(
          `[global] Session periodic state changed: ${msg.data.session_id} -> configured=${msg.data.periodic_configured}, enabled=${msg.data.periodic_enabled}`,
        );
        // Update in stored sessions (for sidebar display - uses periodic_configured for UI)
        // Also store next_scheduled_at and frequency for progress indicator
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? {
                  ...s,
                  periodic_enabled: msg.data.periodic_configured,
                  next_scheduled_at: msg.data.next_scheduled_at || null,
                  periodic_frequency: msg.data.frequency || null,
                }
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
              info: {
                ...session.info,
                // Use periodic_configured for UI mode (shows frequency panel, lock/unlock buttons)
                periodic_enabled: msg.data.periodic_configured,
                next_scheduled_at: msg.data.next_scheduled_at || null,
                periodic_frequency: msg.data.frequency || null,
              },
            },
          };
        });
        // Dispatch custom event for ChatInput to handle frequency and lock state updates
        // This allows the frequency panel to update in real-time when another client changes it
        window.dispatchEvent(
          new CustomEvent("mitto:periodic_config_updated", {
            detail: {
              sessionId: msg.data.session_id,
              // periodicConfigured controls UI mode
              periodicConfigured: msg.data.periodic_configured,
              // periodicEnabled controls lock state (whether runs are active)
              periodicEnabled: msg.data.periodic_enabled,
              frequency: msg.data.frequency,
              nextScheduledAt: msg.data.next_scheduled_at,
            },
          }),
        );
        break;

      case "periodic_started":
        // A periodic prompt was delivered to a session
        // Show toast notification and trigger native notification if enabled
        console.log(
          `[global] Periodic started: ${msg.data.session_id} (${msg.data.session_name})`,
        );
        setPeriodicStarted({
          sessionId: msg.data.session_id,
          sessionName: msg.data.session_name,
          timestamp: Date.now(),
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

      case "acp_started":
        // Server notifies that the ACP connection for a session was started
        // This is broadcast to all clients after unarchiving a session
        console.log("ACP started for session:", msg.data?.session_id);
        // Update session state to allow prompts
        setSessions((prev) => {
          const session = prev[msg.data?.session_id];
          if (!session) return prev;
          return {
            ...prev,
            [msg.data.session_id]: {
              ...session,
              isRunning: true,
              // Add a system message to inform the user
              messages: [
                ...session.messages,
                {
                  role: "system",
                  text: "Session resumed. You can continue the conversation.",
                  timestamp: Date.now(),
                },
              ],
            },
          };
        });
        break;

      case "acp_start_failed":
        // Server notifies that the ACP server failed to start
        // This is broadcast to all clients when session creation fails
        console.error("ACP start failed:", msg.data);
        if (msg.data) {
          // Dispatch event for toast notification
          window.dispatchEvent(
            new CustomEvent("mitto:acp_start_failed", { detail: msg.data }),
          );
        }
        break;

      case "hook_failed":
        // Server notifies that a lifecycle hook failed to execute
        console.warn("Hook failed:", msg.data);
        if (msg.data) {
          // Dispatch event for toast notification
          window.dispatchEvent(
            new CustomEvent("mitto:hook_failed", { detail: msg.data }),
          );
        }
        break;

      case "session_settings_updated":
        // Server notifies that session settings (advanced flags) have changed
        // This is broadcast to all clients when settings are updated via API
        console.log("Session settings updated:", msg.data?.session_id);
        if (msg.data) {
          // Dispatch event for components that need to update (e.g., ConversationPropertiesPanel)
          window.dispatchEvent(
            new CustomEvent("mitto:session_settings_updated", {
              detail: msg.data,
            }),
          );
        }
        break;

      case "prompts_changed":
        // Server notifies that prompt files have changed on disk
        // Dispatch event so components (e.g., SlashCommandPicker) can refresh their prompts list
        console.log("Prompts changed:", msg.data?.changed_dirs);
        window.dispatchEvent(
          new CustomEvent("mitto:prompts_changed", { detail: msg.data }),
        );
        break;
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
      console.log(
        `Scheduling global events reconnect (attempt ${attempt + 1}, delay ${delay}ms)`,
      );
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
              archived: false,
            },
            isStreaming: false,
          },
        }));

        // In accordion mode, expand the group containing this new session
        // (and collapse all other groups) - reuse expandGroupForSession helper
        expandGroupForSession(sessionId, data.working_dir, data.acp_server);

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

  // Timeout configuration for message delivery with automatic retry
  // Total budget: 10 seconds - user can wait this long for message delivery
  const TOTAL_DELIVERY_BUDGET_MS = 10000;
  // Initial ACK timeout: short to quickly detect zombie connections
  // Mobile gets slightly longer due to network variability
  const isMobileDevice = useMemo(() => {
    if (typeof navigator === "undefined") return false;
    const ua = navigator.userAgent || "";
    return /iPhone|iPad|iPod|Android|webOS|BlackBerry|IEMobile|Opera Mini/i.test(
      ua,
    );
  }, []);
  const INITIAL_ACK_TIMEOUT_MS = isMobileDevice ? 4000 : 3000;
  // Timeout for reconnection during retry
  const RECONNECT_TIMEOUT_MS = 4000;

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
          // Calculate lastSeenSeq dynamically from messages in state (not localStorage)
          // Use load_events instead of deprecated sync_session
          const session = sessionsRef.current[sessionId];
          const sessionMessages = session?.messages || [];
          // Use the higher of: max seq from messages OR lastLoadedSeq (which includes session_end, etc.)
          const lastSeq = Math.max(
            getMaxSeq(sessionMessages),
            session?.lastLoadedSeq || 0,
          );
          if (lastSeq > 0) {
            console.log(
              `Syncing session ${sessionId} from seq ${lastSeq} (lastLoadedSeq=${session?.lastLoadedSeq}, messages=${sessionMessages.length})`,
            );
            ws.send(
              JSON.stringify({
                type: "load_events",
                data: { after_seq: lastSeq },
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
      timeSinceLastAck < getKeepaliveInterval() * 2 &&
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
   * @param {Array} files - Optional array of files
   * @param {Object} options - Optional settings: { timeout: number, skipMessageAdd: boolean }
   * @returns {Promise<{success: boolean, promptId: string}>}
   */
  const sendPrompt = useCallback(
    async (message, images = [], files = [], options = {}) => {
      const startTime = Date.now();

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

      // Clear any existing action buttons when sending a new prompt
      clearActionButtons(activeSessionId);

      // Add user message with optional images and files (unless skipped for retry)
      if (!options.skipMessageAdd) {
        const userMessage = {
          role: ROLE_USER,
          text: message,
          timestamp: Date.now(),
        };
        if (images.length > 0) {
          userMessage.images = images; // Array of { id, url, name, mimeType }
        }
        if (files.length > 0) {
          userMessage.files = files; // Array of { id, name, mimeType, size, category }
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
      const fileIds = files.map((f) => f.id);

      // Save to pending queue BEFORE sending (for mobile reliability)
      savePendingPrompt(activeSessionId, promptId, message, imageIds, fileIds);

      /**
       * Helper to attempt sending and wait for ACK with timeout.
       * Returns: { success: true, promptId } on ACK, or throws on timeout/failure.
       */
      const attemptSend = (ackTimeout) => {
        return new Promise((resolve, reject) => {
          const timeoutId = setTimeout(() => {
            const pending = pendingSendsRef.current[promptId];
            if (!pending) return; // Already resolved
            delete pendingSendsRef.current[promptId];
            reject(new Error("ACK_TIMEOUT"));
          }, ackTimeout);

          // Track the pending send
          pendingSendsRef.current[promptId] = { resolve, reject, timeoutId };

          // Send prompt with prompt_id for acknowledgment
          const sent = sendToSession(activeSessionId, {
            type: "prompt",
            data: {
              message,
              image_ids: imageIds,
              file_ids: fileIds,
              prompt_id: promptId,
            },
          });

          if (!sent) {
            // WebSocket send failed immediately
            clearTimeout(timeoutId);
            delete pendingSendsRef.current[promptId];
            reject(new Error("Failed to send message"));
          }
        });
      };

      /**
       * Helper to force reconnect and verify if the prompt was delivered.
       * Returns: true if delivered, false if not delivered.
       * Throws on reconnection failure.
       */
      const verifyDeliveryAfterReconnect = async (reconnectTimeout) => {
        console.log(
          `Forcing reconnect to verify delivery of prompt ${promptId}`,
        );

        // Force close the potentially zombie connection
        const currentWs = sessionWsRefs.current[activeSessionId];
        if (currentWs) {
          delete sessionWsRefs.current[activeSessionId];
          currentWs.close();
        }

        // Wait for fresh connection - this will receive the connected message
        // which includes last_user_prompt_id for delivery verification
        await waitForSessionConnection(activeSessionId, reconnectTimeout);

        // Small delay to ensure the connected message handler has run
        await new Promise((r) => setTimeout(r, 100));

        // Check if our prompt was the last one delivered
        const confirmed = lastConfirmedPromptRef.current[activeSessionId];
        if (confirmed && confirmed.promptId === promptId) {
          console.log(
            `Prompt ${promptId} was confirmed delivered after reconnect`,
          );
          return true;
        }

        console.log(
          `Prompt ${promptId} was NOT delivered (last confirmed: ${confirmed?.promptId})`,
        );
        return false;
      };

      // Main delivery logic with retry
      try {
        // First attempt with short ACK timeout
        const result = await attemptSend(INITIAL_ACK_TIMEOUT_MS);
        removePendingPrompt(promptId);
        return result;
      } catch (err) {
        if (err.message !== "ACK_TIMEOUT") {
          // Non-timeout error (e.g., send failed) - don't retry
          throw err;
        }

        // ACK timeout - reconnect and verify/retry
        const elapsed = Date.now() - startTime;
        const remainingBudget = TOTAL_DELIVERY_BUDGET_MS - elapsed;

        if (remainingBudget <= 0) {
          throw new Error(
            "Message delivery timed out. Please check your connection and try again.",
          );
        }

        console.log(
          `ACK timeout after ${elapsed}ms, ${remainingBudget}ms budget remaining`,
        );

        try {
          // Reconnect and check if message was delivered
          const reconnectTimeout = Math.min(
            remainingBudget,
            RECONNECT_TIMEOUT_MS,
          );
          const wasDelivered =
            await verifyDeliveryAfterReconnect(reconnectTimeout);

          if (wasDelivered) {
            removePendingPrompt(promptId);
            return { success: true, promptId, verifiedOnReconnect: true };
          }

          // Message was NOT delivered - retry on fresh connection
          const elapsedAfterReconnect = Date.now() - startTime;
          const retryBudget = TOTAL_DELIVERY_BUDGET_MS - elapsedAfterReconnect;

          if (retryBudget <= 500) {
            // Not enough time for a meaningful retry
            throw new Error(
              "Message delivery could not be confirmed. Please try again.",
            );
          }

          console.log(`Retrying send with ${retryBudget}ms budget`);

          // Retry the send on the fresh connection
          const result = await attemptSend(retryBudget);
          removePendingPrompt(promptId);
          return { ...result, retriedOnReconnect: true };
        } catch (reconnectErr) {
          if (reconnectErr.message === "ACK_TIMEOUT") {
            throw new Error(
              "Message delivery could not be confirmed after retry. Please check your connection.",
            );
          }
          // Reconnection or retry failed
          console.error("Delivery retry failed:", reconnectErr);
          throw new Error(
            "Connection lost and could not reconnect. Please check your network and try again.",
          );
        }
      }
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

  // Change a session config option value
  // For mode changes, use configId "mode" with the desired mode value
  const setConfigOption = useCallback(
    (configId, value) => {
      // Use value == null to allow falsy values like empty strings
      if (!activeSessionId || !configId || value == null) return;
      console.log(`Setting config option: ${configId} = ${value}`);
      sendToSession(activeSessionId, {
        type: "set_config_option",
        data: { config_id: configId, value: value },
      });
    },
    [activeSessionId, sendToSession],
  );

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

    // Prevent duplicate requests if already loading
    if (session.isLoadingMore) {
      console.log(`Already loading more messages for ${sessionId}, skipping`);
      return;
    }

    // Get the WebSocket for this session
    const ws = sessionWsRefs.current[sessionId];
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      console.error("WebSocket not connected for session:", sessionId);
      return;
    }

    // Set loading state before sending request
    setSessions((prev) => {
      const prevSession = prev[sessionId];
      if (!prevSession) return prev;
      return {
        ...prev,
        [sessionId]: {
          ...prevSession,
          isLoadingMore: true,
        },
      };
    });

    // Send load_events request with before_seq for pagination
    console.log(
      `Loading more messages for ${sessionId} before seq ${session.firstLoadedSeq}`,
    );
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

  // Archive/unarchive a session via REST API
  const archiveSession = useCallback(async (sessionId, archived) => {
    try {
      const response = await secureFetch(apiUrl(`/api/sessions/${sessionId}`), {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ archived }),
      });
      if (!response.ok) {
        console.error("Failed to archive/unarchive session");
        return;
      }
      // Update local state for stored sessions
      setStoredSessions((prev) =>
        prev.map((s) => (s.session_id === sessionId ? { ...s, archived } : s)),
      );
      // Update local state for active sessions
      setSessions((prev) => {
        const session = prev[sessionId];
        if (!session) return prev;
        return {
          ...prev,
          [sessionId]: {
            ...session,
            info: { ...session.info, archived },
          },
        };
      });
    } catch (err) {
      console.error("Failed to archive/unarchive session:", err);
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
      console.log(
        `Detected inconsistent state for ${activeSessionId}: hasMoreMessages=true but messages=[], triggering reload...`,
      );

      // Mark that we've attempted recovery to prevent infinite loops
      recoveryAttemptedRef.current[activeSessionId] = true;

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

  // Clear periodic started notification
  const clearPeriodicStarted = useCallback(() => {
    setPeriodicStarted(null);
  }, []);

  // Clear background UI prompt notification
  const clearBackgroundUIPrompt = useCallback(() => {
    setBackgroundUIPrompt(null);
  }, []);

  // Send UI prompt answer (yes/no or select response)
  const sendUIPromptAnswer = useCallback(
    (sessionId, requestId, optionId, label) => {
      console.log("[UIPrompt] Sending answer:", {
        sessionId,
        requestId,
        optionId,
        label,
      });

      const sent = sendToSession(sessionId, {
        type: "ui_prompt_answer",
        data: {
          request_id: requestId,
          option_id: optionId,
          label: label,
        },
      });

      if (sent) {
        // Clear the active UI prompt immediately on the frontend
        // The backend will also send a dismiss message, but this provides instant feedback
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          if (session.activeUIPrompt?.requestId !== requestId) return prev;
          return {
            ...prev,
            [sessionId]: {
              ...session,
              activeUIPrompt: null,
            },
          };
        });
      }

      return sent;
    },
    [sendToSession],
  );

  // Get active UI prompt for the current session
  const activeUIPrompt = useMemo(() => {
    const session = sessions[activeSessionId];
    return session?.activeUIPrompt || null;
  }, [sessions, activeSessionId]);

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
    archiveSession,
    removeSession,
    isStreaming,
    hasMoreMessages,
    hasReachedLimit,
    isLoadingMore,
    actionButtons,
    sessionInfo,
    activeSessionId,
    activeSessions,
    storedSessions,
    fetchStoredSessions,
    backgroundCompletion,
    clearBackgroundCompletion,
    periodicStarted,
    clearPeriodicStarted,
    backgroundUIPrompt,
    clearBackgroundUIPrompt,
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
    availableCommands,
    configOptions,
    setConfigOption,
    activeUIPrompt,
    sendUIPromptAnswer,
  };
}
