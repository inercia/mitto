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
  getMaxSeq,
  isStaleClientState,
  resolveHasMoreAfterEventsLoaded,
} from "../lib.js";

import {
  setLastActiveSessionId,
  getLastActiveSessionId,
  getLastSeenSeq,
  setLastSeenSeq,
  getSingleExpandedGroupMode,
  setGroupExpanded,
  isGroupExpanded,
  getExpandedGroups,
} from "../utils/storage.js";

import { playAgentCompletedSound } from "../utils/audio.js";

import { getApiPrefix } from "../utils/api.js";
import { getSdkClient } from "../utils/sdkClient.js";
import { errorStatus, errorMessage } from "../utils/sdkErrors.js";

// Import WebSocket utilities (M1, M2 implementations)
// Reconnect backoff/debounce, keepalive tuning, and the raw stale/behind-seq
// sync constants moved into SessionStream (mitto-7gta.30) and are consumed
// solely by useWSConnection.js now — this composer only needs the two
// content-level helpers below.
import {
  isTerminalSessionError,
  isReusedConversationResponse,
} from "../utils/websocket.js";
import { useWSSeqSync } from "./useWSSeqSync.js";
import { useWSWorkspaces } from "./useWSWorkspaces.js";
import { useWSQueue } from "./useWSQueue.js";
import { useWSNotifications } from "./useWSNotifications.js";
import { useWSConfigOptions } from "./useWSConfigOptions.js";
import { useWSSessionSelectors } from "./useWSSessionSelectors.js";
import { useWSActionButtons } from "./useWSActionButtons.js";
import { useWSMobileResilience } from "./useWSMobileResilience.js";
import { useWSConnection } from "./useWSConnection.js";
import { useWSDeliveryVerification } from "./useWSDeliveryVerification.js";
import {
  createSessionUpdateScheduler,
  sessionHasLoadedMessages,
  sessionWasStreaming,
} from "./sessionUpdateScheduler.js";

// =============================================================================
// Session creation retry state (module-level, persists across re-renders)
// Auto-retries POST /api/sessions on 503 session_creation_timeout instead of
// silently blocking clicks. Keeps the button in a visible busy/spinner state.
// =============================================================================

// Maximum number of automatic retries on 503 session_creation_timeout
const SESSION_CREATION_MAX_RETRIES = 4;

// Fixed delay between retries (ms). The agent needs time to finish its turn;
// a fixed 30s gap is more predictable than exponential backoff here.
const SESSION_CREATION_RETRY_DELAY_MS = 30000;

const COALESCED_BACKGROUND_MESSAGE_TYPES = new Set([
  "agent_message",
  "agent_thought",
  "tool_call",
  "tool_update",
]);

// Number of retries attempted for the current creation series (0 = first attempt)
let _sessionCreationRetryCount = 0;

// setTimeout handle for the pending auto-retry (null = no retry scheduled)
let _sessionCreationRetryTimer = null;

// Options snapshot for the pending auto-retry
let _sessionCreationPendingOpts = null;

/**
 * WebSocket Hook with Per-Session WebSocket Support
 * Manages both global events WebSocket and per-session WebSockets
 *
 * @param {Object} [options]
 * @param {Object} [options.onActiveSessionRemovedRef] - Ref whose `.current` is a
 *   callback invoked with the removed conversation's folder working dir when the
 *   ACTIVE conversation is removed from view (deleted or archived). When set, it
 *   takes over post-removal navigation (e.g. showing the global Dashboard)
 *   instead of switching to another conversation. Falls back to the previous
 *   behavior when unset.
 * @param {Object} [options.onNoInitialSessionRef] - Ref whose `.current` is a
 *   callback invoked on initial connection when there is no valid last-active
 *   conversation to restore (either no persisted id, or the persisted id no
 *   longer maps to an existing session). When set, the hook does NOT auto-switch
 *   to the most-recent session and instead lets the callback route the UI (e.g.
 *   to the global Dashboard). Falls back to switching to sessions[0] when unset.
 */
export function useWebSocket({
  onActiveSessionRemovedRef,
  onNoInitialSessionRef,
} = {}) {
  // Initialize window.__debug for test observability.
  // Tests can read window.__debug.lastLoadEventsAfterSeq to assert the client
  // used its stored watermark (not 0) when sending load_events after reconnect.
  if (typeof window !== "undefined" && !window.__debug) {
    window.__debug = {};
  }

  // Set of workingDir strings with an in-flight session-creation request or pending auto-retry.
  // Used to show a per-folder spinner on the "+" button and prevent duplicate clicks.
  const [creatingWorkingDirs, setCreatingWorkingDirs] = useState(
    () => new Set(),
  );

  // Derived: true if ANY folder has an in-flight create (for non-folder consumers).
  const isCreatingSession = creatingWorkingDirs.size > 0;

  // Multi-session state: { sessionId: { messages: [], info: {}, lastSeq: 0, isStreaming: false, ws: WebSocket } }
  const [sessions, setSessions] = useState({});
  const [activeSessionId, setActiveSessionId] = useState(null);
  const [storedSessions, setStoredSessions] = useState([]); // Sessions from the store

  // Global MCP server bind status from the `connected` message: { available, reason, port } | null
  const [mcpStatus, setMcpStatus] = useState(null);

  const {
    workspaces,
    acpServers,
    fetchWorkspaces,
    addWorkspace,
    removeWorkspace,
  } = useWSWorkspaces();
  // MCP tools per workspace UUID: { [workspaceUUID]: [{name, description}] }
  const [workspaceMcpTools, setWorkspaceMcpTools] = useState({});

  // Background notification state (completions, loop started, background UI prompts) — extracted to useWSNotifications sub-hook, mitto-90f.5
  const {
    backgroundCompletion,
    setBackgroundCompletion,
    clearBackgroundCompletion,
    loopStarted,
    setLoopStarted,
    clearLoopStarted,
    backgroundUIPrompt,
    setBackgroundUIPrompt,
    clearBackgroundUIPrompt,
    backgroundUIPromptTimeout,
    setBackgroundUIPromptTimeout,
    clearBackgroundUIPromptTimeout,
  } = useWSNotifications();

  // Queue state + REST callbacks (extracted to useWSQueue sub-hook, mitto-90f.5)
  const {
    queueLength,
    queueMessages,
    queueConfig,
    setQueueLength,
    setQueueMessages,
    setQueueConfig,
    fetchQueueMessages,
    deleteQueueMessage,
    addToQueue,
    moveQueueMessage,
  } = useWSQueue(activeSessionId);

  // Available slash commands for the active session (from ACP agent)
  // Array of { name: string, description: string, input_hint?: string }
  const [availableCommands, setAvailableCommands] = useState([]);

  // Config options are derived from the active session's info (per-session, not global)
  // Array of { id: string, name: string, description?: string, category?: string, type: string, current_value: string, options: [] }
  // See https://agentclientprotocol.com/protocol/session-config-options

  const activeSessionIdRef = useRef(activeSessionId);
  const sessionsRef = useRef(sessions); // For accessing sessions in callbacks
  const sessionUpdateSchedulerRef = useRef(null);
  if (sessionUpdateSchedulerRef.current === null) {
    sessionUpdateSchedulerRef.current = createSessionUpdateScheduler({
      setSessions,
      getActiveSessionId: () => activeSessionIdRef.current,
    });
  }
  const retryPendingPromptsRef = useRef(null); // Ref to retry function (set later to avoid circular deps)
  const rejectOversizedPromptsRef = useRef(null); // Ref to quarantine callback for close code 1009
  const resolvePendingSendsRef = useRef(null); // Ref to resolve function (set later to avoid circular deps)
  // Always points to the latest createNewSession callback — used by the retry timer
  // to avoid stale-closure issues when connectToSession changes between retries.
  const createNewSessionRef = useRef(null);
  // Track pending send operations for ACK handling
  // { promptId: { resolve, reject, timeoutId } }
  const pendingSendsRef = useRef({});
  // Track last confirmed prompt ID per session (from connected message)
  // Used to verify delivery after zombie WebSocket timeout/reconnect
  // { sessionId: { promptId: string, seq: number } }
  const lastConfirmedPromptRef = useRef({});

  const { isSeqDuplicate, markSeqSeen, clearSeenSeqs } = useWSSeqSync();

  // ============================================================================
  // Forward-refs for C1 (useWSConnection) transport functions (mitto-90f.6.2)
  // ============================================================================
  // useWSConnection is called BELOW useWSMobileResilience (which owns
  // staleRecoveryCooldownRef) so its returned functions are not available at
  // the declaration site of composer callbacks that use them (switchSession,
  // createNewSession, sendPrompt, cancelPrompt, forceReset, retryPendingPrompts,
  // ensureResumed, sendUIPromptAnswer, the init useEffect, the auto-recovery
  // useEffect). Each such callback closes over one of these refs via .current(...)
  // and omits the C1 function from its dep array. Populated right after
  // useWSConnection returns.
  const connectToSessionRef = useRef(null);
  const sendToSessionRef = useRef(null);
  const waitForSessionConnectionRef = useRef(null);
  const isConnectionHealthyRef = useRef(null);
  const connectToEventsRef = useRef(null);
  const forceReconnectActiveSessionRef = useRef(null);
  const reconnectAllSessionsStaggeredRef = useRef(null);

  // handleSessionMessage lives in the composer but is threaded through this ref
  // into useWSConnection so its identity stays stable across reconnects
  // (rule 21-web-frontend-state). Populated unconditionally after the useCallback
  // declaration below.
  const handleSessionMessageRef = useRef(null);
  // handleSessionKeepaliveAck (UI-only bookkeeping for keepalive_ack frames,
  // mitto-7gta.30) is threaded the same way — populated after its own
  // useCallback declaration, below handleSessionMessage's.
  const handleSessionKeepaliveAckRef = useRef(null);

  // Stable wrapper: useWSConfigOptions (called mid-composer) needs a sendToSession
  // but the real C1 implementation is not returned until below useWSMobileResilience.
  // The wrapper ref-indirects to the current C1 function; the ref is populated
  // after useWSConnection returns.
  const sendToSessionStable = useCallback(
    (sessionId, msg) => sendToSessionRef.current?.(sessionId, msg) ?? false,
    [],
  );

  // Track in-flight sync (load_events) requests per session.
  // When a sync is pending, keepalive misses are suppressed to prevent
  // reconnection storms during large event syncs (e.g., 790 events).
  // { sessionId: boolean }
  const pendingSyncRef = useRef({});

  // Auto-clear timeout for pendingSyncRef to prevent indefinite suppression.
  // If events_loaded never arrives (e.g., server error, WebSocket drop),
  // the sync flag is cleared after SYNC_TIMEOUT_MS so keepalive miss
  // counting resumes and triggers a reconnect.
  // { sessionId: timeoutId }
  const syncTimeoutRef = useRef({});
  const SYNC_TIMEOUT_MS = 30_000;

  // Track when each sync (load_events) was sent so we can log round-trip time
  // when events_loaded arrives. Helps measure real sync latency from webview.log.
  // { sessionId: number (Date.now()) }
  const syncStartTimeRef = useRef({});

  // Dedicated ref for tracking last known seq per session
  // Survives reconnections, always current, updated on every received event
  // This is the PRIMARY source for client max seq (React state is fallback)
  // { sessionId: number }
  const lastKnownSeqRef = useRef({});

  // Expose test hooks so Playwright can manipulate internal state directly.
  // Only used in test environments — harmless in production.
  if (typeof window !== "undefined") {
    if (!window.__debug) window.__debug = {};
    // _setLastKnownSeq also write-throughs to the localStorage watermark
    // (same "mitto_last_seen_seq_<id>" key the SDK's seqStore reads —
    // see utils/sdkClient.js's SEQ_STORE_KEY_PREFIX). Gap-fill/stale
    // detection now live inside SessionStream (mitto-7gta.30) and read
    // their client watermark from that seqStore via lastSeenSeq(), not
    // from this ref, so tests simulating a gap/stale-client must move
    // the real watermark, not just this composer-local ref.
    window.__debug._setLastKnownSeq = (sessionId, seq) => {
      lastKnownSeqRef.current[sessionId] = seq;
      setLastSeenSeq(sessionId, seq);
    };
  }

  // Track sessions that need a historical context load after the localStorage
  // watermark was used as after_seq and zero new events were returned.
  // Without this, an app restart for an idle session would show an empty conversation.
  // { sessionId: boolean }
  const needsContextLoadRef = useRef({});

  /**
   * Update the last known seq for a session.
   * Only updates if the new seq is higher than the current value.
   * This is called on every received event to maintain an accurate watermark.
   *
   * @param {string} sessionId - The session ID
   * @param {number} seq - The sequence number to record
   */
  const updateLastKnownSeq = useCallback((sessionId, seq) => {
    if (seq && seq > (lastKnownSeqRef.current[sessionId] || 0)) {
      lastKnownSeqRef.current[sessionId] = seq;
      // Persist to localStorage so that app restarts and WKWebView page reloads
      // can start from the correct watermark instead of always requesting the
      // last 50 events.  getLastSeenSeq/setLastSeenSeq are try/catch-safe.
      setLastSeenSeq(sessionId, seq);
    }
  }, []);

  /**
   * Clear the in-flight sync flag for a session and cancel its auto-clear timeout.
   * Call this when events_loaded arrives or when the WebSocket closes/errors.
   *
   * @param {string} sessionId - The session ID
   */
  const clearPendingSync = useCallback((sessionId) => {
    if (syncTimeoutRef.current[sessionId]) {
      clearTimeout(syncTimeoutRef.current[sessionId]);
      delete syncTimeoutRef.current[sessionId];
    }
    if (pendingSyncRef.current[sessionId]) {
      // Log round-trip time for every successful sync so webview.log captures
      // real latency data (useful for tuning SYNC_TIMEOUT_MS).
      const startTime = syncStartTimeRef.current[sessionId];
      if (startTime) {
        console.log(
          `[sync] events_loaded for session ${sessionId} in ${Date.now() - startTime}ms`,
        );
      }
      delete syncStartTimeRef.current[sessionId];
    }
    pendingSyncRef.current[sessionId] = false;
    // Also discard any pending context-load flag so a WebSocket drop during the
    // after_seq phase doesn't leave a stale flag that fires on reconnect.
    delete needsContextLoadRef.current[sessionId];
  }, []);

  /**
   * Set the in-flight sync flag for a session and start a 30s auto-clear timeout.
   * If events_loaded never arrives (server error, WebSocket drop), the flag is
   * cleared automatically and the stream is force-reconnected — eliminating the
   * extra 5-20s that would otherwise be wasted waiting for keepalive
   * miss-counting to reach the threshold.
   *
   * @param {string} sessionId - The session ID
   */
  const setPendingSync = useCallback(
    (sessionId) => {
      // Cancel any existing timeout before starting a new one
      if (syncTimeoutRef.current[sessionId]) {
        clearTimeout(syncTimeoutRef.current[sessionId]);
      }
      pendingSyncRef.current[sessionId] = true;
      // Record send time so clearPendingSync can log the round-trip duration.
      syncStartTimeRef.current[sessionId] = Date.now();
      syncTimeoutRef.current[sessionId] = setTimeout(() => {
        delete syncTimeoutRef.current[sessionId];
        if (pendingSyncRef.current[sessionId]) {
          pendingSyncRef.current[sessionId] = false;
          console.warn(
            `[sync] Sync timeout for session ${sessionId} — events_loaded did not arrive within ${SYNC_TIMEOUT_MS}ms. Forcing reconnect.`,
          );
          // Reconnect immediately instead of waiting for keepalive miss-counting
          // to fire (which would add another 10-20s of dead time). If
          // events_loaded took >30s, the connection is almost certainly a zombie.
          // Use SessionStream.forceReconnect() — it closes and reopens internally
          // and is debounced against concurrent triggers (mitto-7gta.30).
          // NOTE: close() must NOT be used here — it marks the stream explicitly
          // closed, which permanently suppresses reconnection.
          sessionWsRefs.current[sessionId]?.forceReconnect();
        }
      }, SYNC_TIMEOUT_MS);
    },
    [], // sessionWsRefs is a stable ref object — safe to close over without declaring as dep
  );

  // checkAndFillGap is gone (mitto-7gta.30): SessionStream's internal
  // _checkAndFillGap (debounced on every message's max_seq, GAP_FILL_LIMIT
  // events) replaces this composer-level duplicate — see session-stream.js.

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

  // Stable reference to the active session's entry. setSessions() preserves
  // unchanged session entries by reference, so this only changes identity when
  // the ACTIVE session's own data changes — not when a background session ticks.
  // Deriving active-session values from this (instead of the whole `sessions`
  // map) keeps their references stable across background streaming updates.
  const activeSession = activeSessionId ? sessions[activeSessionId] : null;

  // Active-session-derived selectors (extracted to useWSSessionSelectors sub-hook, mitto-90f.5)
  const {
    messages,
    sessionInfo,
    isStreaming,
    agentWorking,
    isRunning,
    hasMoreMessages,
    isLoadingMore,
    hasReachedLimit,
  } = useWSSessionSelectors(activeSession);

  // Action buttons for active session (extracted to useWSActionButtons sub-hook, mitto-90f.5)
  const { actionButtons } = useWSActionButtons(sessions, activeSessionId);

  // Get all active sessions as array for sidebar
  // Memoized with structural fingerprint to prevent unnecessary re-renders
  // when only non-structural properties change (e.g., messageCount, timestamps)
  const prevActiveSessionsFingerprint = useRef("");
  const prevActiveSessionsResult = useRef([]);

  const activeSessions = useMemo(() => {
    const result = Object.entries(sessions).map(([id, data]) => {
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
      const isArchived =
        data.info?.archived || storedSession?.archived || false;
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
        last_user_message_at:
          lastUserMsgTime || data.info?.last_user_message_at,
        // Archived sessions are not "active" - they have no ACP connection
        status: isArchived ? "archived" : "active",
        isActive: !isArchived,
        isStreaming: !isArchived && (data.isStreaming || false),
        isWaitingForChildren: data.isWaitingForChildren || false,
        isWaitingForUserInput: data.isWaitingForUserInput || false,
        messageCount: data.messages?.length || 0,
        archived: isArchived,
        // Protected-conversation flag (mitto-yvel.4): sourced from the
        // connected-message metadata or the stored session, mirroring the
        // archived flag's fallback shape above.
        no_archive: data.info?.no_archive || storedSession?.no_archive || false,
        archive_pending: isArchivePending,
        gc_suspended: data.info?.gc_suspended || false,
        // Linked beads issue ID — sourced from the connected-message metadata or
        // the stored session. Needed so the beads view can detect when a
        // streaming conversation belongs to an issue (pulsing ring).
        beads_issue: data.info?.beads_issue || storedSession?.beads_issue || "",
        // Conversation accent color (SessionItem renders it as a left stripe)
        background_color:
          data.info?.background_color || storedSession?.background_color || "",
        // Parent-child hierarchy. Sourced from the `connected` message first so
        // a child reconnecting before fetchStoredSessions() resolves still nests
        // under its parent instead of being hoisted to the sidebar root.
        parent_session_id:
          data.info?.parent_session_id ||
          storedSession?.parent_session_id ||
          null,
        child_origin:
          data.info?.child_origin || storedSession?.child_origin || null,
      };
    });

    // Structural fingerprint: only produce a new array reference when
    // sidebar-relevant fields change. Fields like messageCount, timestamps,
    // and archive_pending change frequently during streaming but don't
    // affect the sidebar layout or tree structure.
    const fingerprint = result
      .map(
        (s) =>
          `${s.session_id}|${s.name}|${s.working_dir}|${s.acp_server}|${s.archived}|${s.no_archive}|${s.isActive}|${s.isStreaming}|${s.isWaitingForChildren}|${s.isWaitingForUserInput}|${s.status}|${s.gc_suspended}|${s.background_color}|${s.parent_session_id || ""}|${s.child_origin || ""}`,
      )
      .sort()
      .join("\n");

    if (fingerprint === prevActiveSessionsFingerprint.current) {
      return prevActiveSessionsResult.current;
    }
    prevActiveSessionsFingerprint.current = fingerprint;
    prevActiveSessionsResult.current = result;
    return result;
  }, [sessions, storedSessions, workingDirMap]);

  // Handle messages from per-session WebSocket
  const handleSessionMessage = useCallback((sessionId, msg) => {
    // Preserve wire ordering when a status/config/user event follows queued
    // background content. Active-session queues are normally empty, so this is
    // effectively free on the foreground path.
    let hadPendingContent = false;
    if (!COALESCED_BACKGROUND_MESSAGE_TYPES.has(msg.type)) {
      hadPendingContent =
        sessionUpdateSchedulerRef.current.flushSession(sessionId);
    }
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

        // Global MCP bind status (same for all sessions); drives a persistent badge.
        if (msg.data.mcp) {
          setMcpStatus(msg.data.mcp);
        }

        // Update available slash commands from agent
        if (msg.data.available_commands) {
          setAvailableCommands(msg.data.available_commands);
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
                // Use server-sent archived flag, falling back to existing session info
                archived: msg.data.archived ?? session.info?.archived ?? false,
                // Protected-conversation flag (mitto-yvel.4): suppresses archive
                // affordances everywhere. Always sent by the server, so no
                // stale-value risk from the ?? fallback chain.
                no_archive:
                  msg.data.no_archive ?? session.info?.no_archive ?? false,
                archive_reason:
                  msg.data.archive_reason ?? session.info?.archive_reason ?? "",
                archived_at:
                  msg.data.archived_at ?? session.info?.archived_at ?? null,
                // Preserve archive_pending flag from existing session info
                archive_pending: session.info?.archive_pending || false,
                // Parent-child hierarchy (sent by the server in `connected`).
                // Kept on info so activeSessions can surface it before
                // fetchStoredSessions() has populated storedSessions —
                // otherwise a reconnecting child renders as a root.
                parent_session_id:
                  msg.data.parent_session_id ??
                  session.info?.parent_session_id ??
                  null,
                child_origin:
                  msg.data.child_origin ?? session.info?.child_origin ?? null,
                // Loop state from server:
                // loop_configured: config exists → drives editor UI + reconnect long-lived check
                // loop_enabled: runs active → drives sidebar category + clock icon
                loop_configured:
                  msg.data.loop_configured ??
                  session.info?.loop_configured ??
                  false,
                loop_enabled:
                  msg.data.loop_enabled ?? session.info?.loop_enabled ?? false,
                loop_stopped_reason:
                  msg.data.loop_stopped_reason ??
                  session.info?.loop_stopped_reason ??
                  null,
                loop_trigger:
                  msg.data.loop_trigger ?? session.info?.loop_trigger ?? null,
                // loop_triggers is the canonical list of armed triggers
                // (mitto-r6j). loop_trigger stays as the legacy scalar for
                // backward-compat consumers; new UI paths read triggers.
                loop_triggers:
                  msg.data.loop_triggers ?? session.info?.loop_triggers ?? null,
                loop_delay_seconds:
                  msg.data.loop_delay_seconds ??
                  session.info?.loop_delay_seconds ??
                  null,
                loop_max_duration_seconds:
                  msg.data.loop_max_duration_seconds ??
                  session.info?.loop_max_duration_seconds ??
                  null,
                workspace_uuid: msg.data.workspace_uuid ?? null,
                // ACP readiness: false until acp_started event or explicit true in connected msg
                acp_ready: msg.data.acp_ready ?? false,
                // GC-suspended state from server (for fresh loads/reconnections)
                gc_suspended:
                  msg.data.gc_suspended ?? session.info?.gc_suspended ?? false,
                // Linked beads issue ID (always include, even if empty, so frontend can clear the control)
                beads_issue:
                  msg.data.beads_issue ?? session.info?.beads_issue ?? "",
                // Processor stats
                processor_count:
                  msg.data.processor_count ??
                  session.info?.processor_count ??
                  0,
                processor_activations:
                  msg.data.processor_activations ??
                  session.info?.processor_activations ??
                  0,
                processor_last_activation:
                  msg.data.processor_last_activation ??
                  session.info?.processor_last_activation ??
                  null,
                processor_last_names:
                  msg.data.processor_last_names ??
                  session.info?.processor_last_names ??
                  null,
                // Token usage from last prompt
                usage: msg.data.usage ?? session.info?.usage ?? null,
                // Context window usage (size/used from ACP)
                context_usage:
                  msg.data.context_usage ?? session.info?.context_usage ?? null,
                // MCP usage stats (event-derived, survive restart)
                mcp_calls_total:
                  msg.data.mcp_calls_total ??
                  session.info?.mcp_calls_total ??
                  0,
                mcp_ui_calls:
                  msg.data.mcp_ui_calls ?? session.info?.mcp_ui_calls ?? 0,
                mcp_children_wait_calls:
                  msg.data.mcp_children_wait_calls ??
                  session.info?.mcp_children_wait_calls ??
                  0,
                // Orchestration stats: children spawned (event-derived) + child
                // wait times (in-memory, resets on restart)
                children_spawned:
                  msg.data.children_spawned ??
                  session.info?.children_spawned ??
                  0,
                child_wait_count:
                  msg.data.child_wait_count ??
                  session.info?.child_wait_count ??
                  0,
                child_wait_total_ms:
                  msg.data.child_wait_total_ms ??
                  session.info?.child_wait_total_ms ??
                  0,
                // Activity stats (event-derived, survive restart)
                turns: msg.data.turns ?? session.info?.turns ?? 0,
                acp_tool_calls:
                  msg.data.acp_tool_calls ?? session.info?.acp_tool_calls ?? 0,
                permissions_allowed:
                  msg.data.permissions_allowed ??
                  session.info?.permissions_allowed ??
                  0,
                permissions_denied:
                  msg.data.permissions_denied ??
                  session.info?.permissions_denied ??
                  0,
                errors: msg.data.errors ?? session.info?.errors ?? 0,
                images_uploaded:
                  msg.data.images_uploaded ??
                  session.info?.images_uploaded ??
                  0,
                // Cumulative token usage across all turns (in-memory, resets on restart)
                usage_cumulative:
                  msg.data.usage_cumulative ??
                  session.info?.usage_cumulative ??
                  null,
                // Config options (model, mode, etc.) - per-session
                // Use ?? to preserve existing options when server omits the field (e.g. pre-acp_started reconnect)
                config_options:
                  msg.data.config_options ?? session.info?.config_options ?? [],
                // Agent-native context-flush command (e.g. "/clear"). Omitted by
                // the server when no BackgroundSession is attached yet, so keep
                // the previous value rather than clearing it (mitto-1o8).
                context_flush_command:
                  msg.data.context_flush_command ??
                  session.info?.context_flush_command ??
                  "",
              },
              isStreaming: msg.data.is_prompting || false,
              isRunning: msg.data.is_running ?? session.isRunning ?? false,
            },
          };
        });
        break;

      case "agent_message": {
        const msgSeq = msg.data.seq;
        const maxSeq = msg.data.max_seq;
        const htmlLen = msg.data.html?.length || 0;
        const isPromptingFromServer = msg.data.is_prompting;

        // Update last known seq from this event. Gap detection/fill is now
        // owned internally by SessionStream (mitto-7gta.30).
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));

        // Agent is responding - this proves any pending prompts were received.
        // Resolve pending sends to prevent false "delivery not confirmed" errors on mobile.
        if (resolvePendingSendsRef.current) {
          resolvePendingSendsRef.current(sessionId);
        }

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        // Frontend only needs to coalesce chunks with the same seq (streaming continuation).
        sessionUpdateSchedulerRef.current.schedule(sessionId, (prev) => {
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

          if (shouldAppend) {
            // Safeguard: Check if the incoming HTML is a duplicate of what we already have.
            // This can happen when the backend sends the same complete message multiple times
            // (e.g., due to replayBufferedEventsWithDedup after a load_events fallback).
            // If the existing HTML already ends with the incoming HTML, skip the append.
            const existingHtml = last.html || "";
            const incomingHtml = msg.data.html;
            if (existingHtml.endsWith(incomingHtml)) {
              return prev; // Skip duplicate append
            }

            const newHtml = existingHtml + incomingHtml;
            messages[messages.length - 1] = {
              ...last,
              html: newHtml,
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
          const isPrompting = isPromptingFromServer ?? true;
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

        // Update last known seq from this event. Gap detection/fill is now
        // owned internally by SessionStream (mitto-7gta.30).
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));

        // Agent is responding - this proves any pending prompts were received.
        // Resolve pending sends to prevent false "delivery not confirmed" errors on mobile.
        if (resolvePendingSendsRef.current) {
          resolvePendingSendsRef.current(sessionId);
        }

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        sessionUpdateSchedulerRef.current.schedule(sessionId, (prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          let messages = [...session.messages];
          const last = messages[messages.length - 1];

          // M1 fix: Check for duplicate events (but allow same-seq for coalescing)
          if (isSeqDuplicate(sessionId, msgSeq, last?.seq)) {
            return prev; // Skip duplicate
          }

          // Coalesce consecutive incomplete thoughts into a single bubble.
          // ThoughtBuffer flushes assign different seq numbers, but they're
          // part of the same logical thinking block. Coalesce as long as
          // the last message is an incomplete thought.
          if (last && last.role === ROLE_THOUGHT && !last.complete) {
            // Mark the new seq as seen even when coalescing
            markSeqSeen(sessionId, msgSeq);
            messages[messages.length - 1] = {
              ...last,
              text: (last.text || "") + msg.data.text,
              seq: msgSeq, // Update to latest seq
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

        // Update last known seq from this event. Gap detection/fill is now
        // owned internally by SessionStream (mitto-7gta.30).
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));

        // M1 fix: Check for duplicate events
        if (isSeqDuplicate(sessionId, msgSeq, null)) {
          break; // Skip duplicate
        }

        // Mark seq as seen
        markSeqSeen(sessionId, msgSeq);

        // WebSocket-only architecture: Server guarantees no duplicate events via seq tracking.
        sessionUpdateSchedulerRef.current.schedule(sessionId, (prev) => {
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

        // Update last known seq from this event. Gap detection/fill is now
        // owned internally by SessionStream (mitto-7gta.30).
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));

        sessionUpdateSchedulerRef.current.schedule(sessionId, (prev) => {
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

      case "action_buttons": {
        // Store action buttons from async follow-up analysis
        // These are suggested response options generated by analyzing the agent's message
        const newButtons = msg.data.buttons || [];
        console.log("[ActionButtons] Received action_buttons message:", {
          sessionId,
          buttons: newButtons,
          buttonCount: newButtons.length,
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

          // Dedup: skip state update if the incoming buttons are identical to
          // what is already displayed. This prevents redundant re-renders when
          // the backend resends cached buttons on every WebSocket reconnect
          // (AddObserver → sendCachedActionButtonsTo fires on each reconnect).
          const existing = session.actionButtons || [];
          if (
            newButtons.length > 0 &&
            newButtons.length === existing.length &&
            newButtons.every(
              (b, i) =>
                b.label === existing[i]?.label &&
                b.response === existing[i]?.response,
            )
          ) {
            console.log(
              "[ActionButtons] Ignoring - identical to current buttons:",
              sessionId,
            );
            return prev;
          }

          return {
            ...prev,
            [sessionId]: {
              ...session,
              actionButtons: newButtons,
            },
          };
        });
        break;
      }

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

        // Dedup: ignore if already showing the same requestId (e.g. after reconnect re-send)
        const alreadyActive =
          sessionsRef.current[sessionId]?.activeUIPrompt?.requestId ===
          msg.data.request_id;
        if (alreadyActive) {
          console.log(
            "[UIPrompt] Ignoring duplicate ui_prompt for requestId:",
            msg.data.request_id,
          );
          break;
        }

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
              true, // sticky — user input required, keep until dismissed
            );
          }
        }

        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) {
            console.warn("[UIPrompt] Session not found:", sessionId);
            return prev;
          }

          // Secondary dedup guard inside setSessions (handles concurrent state updates)
          if (session.activeUIPrompt?.requestId === msg.data.request_id) {
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
                allowFreeText: msg.data.allow_free_text || false,
                freeTextPlaceholder: msg.data.free_text_placeholder || "",
                // Textbox fields
                text: msg.data.text || "",
                resultMode: msg.data.result_mode || "text",
                // Form fields
                formHTML: msg.data.form_html || "",
              },
            },
          };
        });
        break;
      }

      case "notification": {
        // Fire-and-forget notification from mitto_ui_notify MCP tool
        console.log("[Notification] Received:", msg.data);
        // Dispatch a custom event for the app to handle (toast + optional sound/native)
        window.dispatchEvent(
          new CustomEvent("mitto:notification", { detail: msg.data }),
        );
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
        const wasStreaming = sessionWasStreaming(
          currentSession,
          hadPendingContent,
        );
        const lastMessage =
          currentSession?.messages?.[currentSession.messages.length - 1];
        const maxSeq = msg.data.max_seq;

        // Update last known seq from max_seq (server's authoritative max).
        // Gap detection/fill is now owned internally by SessionStream (mitto-7gta.30).
        updateLastKnownSeq(sessionId, maxSeq || 0);

        sessionUpdateSchedulerRef.current.applyImmediate(sessionId, (prev) => {
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

          if (lastIdx >= 0) {
            const last = messages[lastIdx];
            if (last.role === ROLE_AGENT || last.role === ROLE_THOUGHT) {
              messages[lastIdx] = { ...last, complete: true };
            }
          }
          return {
            ...prev,
            [sessionId]: {
              ...session,
              messages,
              isStreaming: false,
              activeUIPrompt: null,
              agentWorking: null,
              // Update processor stats from prompt_complete
              info: {
                ...session.info,
                processor_count:
                  msg.data.processor_count ??
                  session.info?.processor_count ??
                  0,
                processor_activations:
                  msg.data.processor_activations ??
                  session.info?.processor_activations ??
                  0,
                processor_last_activation:
                  msg.data.processor_last_activation ??
                  session.info?.processor_last_activation ??
                  null,
                processor_last_names:
                  msg.data.processor_last_names ??
                  session.info?.processor_last_names ??
                  null,
                // Token usage from last prompt
                usage: msg.data.usage ?? session.info?.usage ?? null,
                // Context window usage (updated with each prompt)
                context_usage:
                  msg.data.context_usage ?? session.info?.context_usage ?? null,
              },
            },
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
          }
          // Always remove from localStorage — even if this was a retry
          // from retryPendingPrompts() (which doesn't register in pendingSendsRef).
          // Without this, retried prompts that get errors stay in localStorage
          // forever, causing an infinite retry loop on every reconnect.
          removePendingPrompt(errorPromptId);

          // If this is a terminal error (session gone/closed), clear ALL
          // pending prompts for this session to prevent retry storms
          if (isTerminalSessionError(msg.data.message)) {
            const allPending = getPendingPromptsForSession(sessionId);
            for (const { promptId: pid } of allPending) {
              removePendingPrompt(pid);
              // Also reject any in-flight sends for these prompts
              const inFlight = pendingSendsRef.current[pid];
              if (inFlight) {
                clearTimeout(inFlight.timeoutId);
                inFlight.reject(new Error(msg.data.message));
                delete pendingSendsRef.current[pid];
              }
            }
            console.log(
              `Cleared all pending prompts for closed session ${sessionId}`,
            );
          }
        }

        sessionUpdateSchedulerRef.current.applyImmediate(sessionId, (prev) => {
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
            [sessionId]: {
              ...session,
              messages,
              isStreaming: false,
              activeUIPrompt: null,
            },
          };
        });
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

      case "session_beads_issue_updated":
        // Linked beads issue changed for this session (via REST PATCH or
        // MCP tool). Update session info so the header's linked-issue button
        // re-renders with the new id (or clears when empty).
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          return {
            ...prev,
            [sessionId]: {
              ...session,
              info: {
                ...session.info,
                beads_issue: msg.data.beads_issue || "",
              },
            },
          };
        });
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === sessionId
              ? { ...s, beads_issue: msg.data.beads_issue || "" }
              : s,
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
            [sessionId]: {
              ...session,
              messages,
              isStreaming: false,
              activeUIPrompt: null,
            },
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
        //
        // IMPORTANT: Never run stale detection on prepend (paginated older events).
        // Prepend batches load historical context and arrive AFTER the initial stale
        // recovery has already reset lastKnownSeqRef synchronously. However, React's
        // async batching means sessionsRef.current may still contain the old stale
        // messages (getMaxSeq=2181) until the setSessions updater flushes. Running
        // stale detection here would incorrectly trigger M1 fix again, scheduling
        // another auto-load prepend → cascade of 2181>2180 detections until React
        // finally flushes. Skip stale detection entirely for prepend batches.
        const currentSession = sessionsRef.current[sessionId];
        const sessionMessages = currentSession?.messages || [];
        // Include lastKnownSeqRef for accurate stale detection
        const refSeq = lastKnownSeqRef.current[sessionId] || 0;
        const clientLastSeq = Math.max(
          refSeq,
          getMaxSeq(sessionMessages),
          currentSession?.lastLoadedSeq || 0,
        );
        const isStaleClient =
          !isPrepend && isStaleClientState(clientLastSeq, maxSeq);

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
          // Also reset the lastKnownSeqRef watermark and its localStorage mirror —
          // the server is the source of truth after stale recovery.
          delete lastKnownSeqRef.current[sessionId];
          setLastSeenSeq(sessionId, 0);
        }

        // Update last known seq from max_seq (server's authoritative max)
        updateLastKnownSeq(sessionId, maxSeq || 0);

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
              // Set cooldown to prevent keepalive from re-triggering stale detection
              // while React state and auto-load prepend are settling.
              staleRecoveryCooldownRef.current[sessionId] = Date.now();
            }
            messages = newMessages;
          } else {
            // Sync after reconnect - merge with deduplication
            // Use mergeMessagesWithSync to handle cases where:
            // 1. Messages already in UI have seq values from streaming
            // 2. Server returns events that overlap with what's already displayed
            messages = mergeMessagesWithSync(session.messages, newMessages);
          }

          // Track the highest seq we've confirmed with the server.
          // Include maxSeq (server's confirmed highest seq) so that even empty
          // events_loaded responses (when client is already caught up) properly
          // update our watermark. Without this, lastLoadedSeq stays stale after
          // empty sync responses, causing keepalive to repeatedly trigger
          // unnecessary load_events requests.
          const newLastLoadedSeq = isStaleClient
            ? lastSeq
            : Math.max(session.lastLoadedSeq || 0, lastSeq, maxSeq);

          // has_more from a forward sync (after_seq) only reflects whether events
          // exist older than the fetched delta — NOT whether older history is still
          // missing from memory. resolveHasMoreAfterEventsLoaded keeps has_more
          // authoritative only on replace (initial load / stale recovery) or
          // prepend (load more), and preserves the existing flag on a merge-sync.
          // See lib.js for the full rationale.
          const updatedSession = {
            ...session,
            messages: limitMessages(messages),
            isStreaming: isPrompting,
            hasMoreMessages: resolveHasMoreAfterEventsLoaded({
              isPrepend,
              isStaleClient,
              existingMessageCount: session.messages.length,
              serverHasMore: hasMore,
              existingHasMore: session.hasMoreMessages,
            }),
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
          // Small delay to let the UI update first, then load remaining messages.
          // Mark the sync as pending BEFORE sending so the keepalive handler does not
          // fire a duplicate stale-detection load_events while this prepend is in-flight.
          // (The auto-load sends a before_seq prepend request which does not set
          // pendingSyncRef on its own, leaving keepalive free to fire concurrently
          // and pile up additional M1-fix cycles.)
          setTimeout(() => {
            const currentWs = sessionWsRefs.current[sessionId];
            if (currentWs && currentWs.state === "open") {
              // Request all events before the first one we just loaded
              setPendingSync(sessionId);
              currentWs.send({
                type: "load_events",
                data: {
                  before_seq: firstSeq,
                  limit: firstSeq - 1, // Load all remaining events
                },
              });
            }
          }, 100);
        }

        // Context-load fallback for the localStorage-watermark fast-path:
        // When the app restarts we send after_seq=<stored_seq> instead of limit:50.
        // The after_seq delta only returns events NEWER than the watermark, so when
        // the client has no in-memory messages the recent history is missing. This
        // happens both when the delta is empty (nothing changed while away) AND when
        // it returns only a partial page (e.g. a loop run produced a few events
        // since the watermark): the user would otherwise see just those few events
        // plus a "Load earlier messages…" button until a hard reload. Detect either
        // case (no prior messages + delta smaller than a full page) and fall back to
        // the normal initial load so the user sees the recent history immediately.
        if (
          needsContextLoadRef.current[sessionId] &&
          !isPrepend &&
          newMessages.length < INITIAL_EVENTS_LIMIT &&
          (currentSession?.messages?.length || 0) === 0 &&
          totalCount > 0
        ) {
          delete needsContextLoadRef.current[sessionId];
          console.log(
            `[localStorage-watermark] Session ${sessionId} has ${totalCount} events on server but only ${newMessages.length} new since watermark — loading recent context`,
          );
          const currentWs = sessionWsRefs.current[sessionId];
          if (currentWs && currentWs.state === "open") {
            currentWs.send({
              type: "load_events",
              data: { limit: INITIAL_EVENTS_LIMIT },
            });
            // Export to window.__debug for Playwright test observability.
            // Set lastLoadEventsAfterSeq=0 to signal "fallback full-history load fired"
            // (the fallback uses limit: N with no after_seq, equivalent to after_seq=0).
            if (typeof window !== "undefined" && window.__debug) {
              window.__debug.lastLoadEventsAfterSeq = 0;
              window.__debug.lastLoadEventsSessionId = sessionId;
              window.__debug.fallbackContextLoadFired = true;
            }
          }
        } else if (needsContextLoadRef.current[sessionId] && !isPrepend) {
          // We received events (new messages) or session is genuinely empty on the
          // server — either way, the flag is no longer needed.
          delete needsContextLoadRef.current[sessionId];
        }

        // Clear in-flight sync flag — events have been delivered
        clearPendingSync(sessionId);
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
          prompt_name,
          argument_count,
          arguments: promptArguments,
          meta,
          provenance,
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

        // Update last known seq from this event. Gap detection/fill is now
        // owned internally by SessionStream (mitto-7gta.30).
        updateLastKnownSeq(sessionId, Math.max(seq || 0, max_seq || 0));

        // Mark seq as seen for tracking (but don't use it for dedup — the
        // alreadyExists check inside setSessions handles dedup by seq match).
        // Previously, the M1 isSeqDuplicate check here would race with
        // events_loaded marking seqs as seen, causing user_prompt messages
        // to be silently dropped for loop prompts.
        if (!is_mine && seq) {
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
            // legitimate loop prompts (same text sent on each run).
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
              promptName: prompt_name || undefined,
              argumentCount: argument_count || undefined,
              arguments: promptArguments || undefined,
              meta: meta || undefined, // Generic event metadata conduit (experimental annotations only)
              provenance: provenance || undefined, // Loop-trigger provenance (mitto-rg79)
            };
            // Add image references if present, constructing full image objects
            // with URLs so the Message component can render them immediately
            // (matching the format produced by convertEventsToMessages in lib.js)
            if (image_ids && image_ids.length > 0) {
              userMessage.images = image_ids.map((id) => ({
                id,
                url: getSdkClient().endpoints.sessions.image(sessionId, id),
                name: id,
              }));
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

      case "session_change": {
        const { seq, max_seq, kind, label, value, previous_value, items } =
          msg.data;
        updateLastKnownSeq(sessionId, Math.max(seq || 0, max_seq || 0));
        if (seq) markSeqSeen(sessionId, seq);
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          // Dedup by seq: skip if a message with this seq already exists.
          if (seq && (session.messages || []).some((m) => m.seq === seq))
            return prev;
          const newMessage = {
            role: ROLE_SYSTEM,
            kind,
            label,
            value,
            previousValue: previous_value,
            items,
            seq,
            timestamp: Date.now(),
          };
          return {
            ...prev,
            [sessionId]: {
              ...session,
              messages: [...(session.messages || []), newMessage],
            },
          };
        });
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

      case "memory_recycled":
        // Server notifies that the GC's memory-recycle tier restarted a
        // memory-bloated idle agent process to reclaim memory.
        console.log("Memory recycled:", msg.data);
        if (msg.data) {
          // Dispatch event for toast notification
          window.dispatchEvent(
            new CustomEvent("mitto:memory_recycled", { detail: msg.data }),
          );
        }
        break;

      case "agent_recycled":
        // Server notifies that the GC's health-recycle tiers (Tier 5/6)
        // restarted a shared ACP process that stopped completing
        // session/new or session/load RPCs (mitto-aoo).
        console.log("Agent recycled (health):", msg.data);
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:agent_recycled", { detail: msg.data }),
          );
        }
        break;

      case "agent_degraded":
        // Server notifies that a workspace's shared ACP process entered or
        // recovered from a degraded state (saturated, MCP-init gated, or
        // MCP-init wedged) — fired BEFORE an eventual health recycle, so this
        // is often the only user-visible signal while the process is degraded
        // but not yet idle enough to recycle (mitto-13n.3).
        console.log("Agent degraded:", msg.data);
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:agent_degraded", { detail: msg.data }),
          );
        }
        break;

      case "mcp_initializing":
        // Server notifies that the agent for a workspace is blocked waiting for
        // MCP servers to initialize on this cold start (mitto-8ul.1). Informational
        // only — the pending session/new is still expected to succeed.
        console.log("MCP initializing:", msg.data);
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:mcp_initializing", { detail: msg.data }),
          );
        }
        break;

      case "mcp_init_timed_out":
        // Server notifies that the agent's MCP-init wait budget elapsed before all
        // MCP servers finished handshake, so the pending session/new was aborted
        // with an actionable error (mitto-8ul.1).
        console.warn("MCP init timed out:", msg.data);
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:mcp_init_timed_out", { detail: msg.data }),
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

      case "acp_started":
        // ACP started for this specific session (sent after async resume completes).
        // Defense-in-depth: the global events WS also handles this, but it may be
        // temporarily disconnected. Just set isRunning=true; no system message here
        // to avoid duplicating the one added by the global handler.
        // Also updates config_options and agent_models that weren't available in the
        // initial "connected" message due to the async ACP initialization timing race.
        console.log("ACP started for session (per-session WS):", sessionId);
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          return {
            ...prev,
            [sessionId]: {
              ...session,
              isRunning: true,
              info: {
                ...session.info,
                acp_ready: true,
                // Update image support capability
                agent_supports_images:
                  msg.data.agent_supports_images ??
                  session.info?.agent_supports_images ??
                  false,
                // Update available commands
                available_commands:
                  msg.data.available_commands ??
                  session.info?.available_commands ??
                  [],
                // Update processor stats (may have changed since connected message)
                processor_count:
                  msg.data.processor_count ??
                  session.info?.processor_count ??
                  0,
                processor_activations:
                  msg.data.processor_activations ??
                  session.info?.processor_activations ??
                  0,
                processor_last_activation:
                  msg.data.processor_last_activation ??
                  session.info?.processor_last_activation ??
                  null,
                processor_last_names:
                  msg.data.processor_last_names ??
                  session.info?.processor_last_names ??
                  null,
                // Update config options if provided
                config_options:
                  msg.data.config_options || session.info?.config_options || [],
                // Context-flush command, resolved once the ACP server is known
                // (not available in "connected" when the session was not yet
                // attached).
                context_flush_command:
                  msg.data.context_flush_command ??
                  session.info?.context_flush_command ??
                  "",
              },
            },
          };
        });
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

          // Categorize the stop reason for appropriate UX:
          // - archived/archived_timeout: neutral system message
          // - gc_suspended: friendly info message (session will auto-resume)
          // - anything else: error message
          const reason = msg.data?.reason || "unknown reason";
          const isArchived =
            reason === "archived" || reason === "archived_timeout";
          const isGCSuspended = reason === "gc_suspended";

          let messageRole, messageText;
          if (isArchived) {
            messageRole = "system";
            messageText = "Session archived. Unarchive to continue.";
          } else if (isGCSuspended) {
            messageRole = "system";
            messageText =
              "Session suspended to save resources. It will resume when you need it.";
          } else {
            messageRole = "error";
            messageText = `Session stopped: ${reason}. Unarchive to continue.`;
          }

          // Only append the stop message when the session was actually running.
          // Multiple stop broadcasts (per-session WS + global events + coalesced
          // resume paths) can arrive for the same underlying transition; gating
          // on the prior isRunning state prevents duplicate messages.
          const wasRunning = session.isRunning === true;
          const nextInfo = {
            ...session.info,
            acp_ready: false,
            // Track GC-suspended state so the UI can suppress the
            // "Reconnecting to AI agent..." spinner for suspended sessions.
            gc_suspended: isGCSuspended || false,
          };
          if (!wasRunning) {
            return {
              ...prev,
              [sessionId]: {
                ...session,
                isRunning: false,
                isStreaming: false,
                activeUIPrompt: null,
                info: nextInfo,
              },
            };
          }
          return {
            ...prev,
            [sessionId]: {
              ...session,
              isRunning: false,
              isStreaming: false,
              activeUIPrompt: null,
              info: nextInfo,
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

        // When the server is shutting down, suppress reconnection.
        // Close the stream preemptively so it doesn't try to reconnect.
        if (msg.data?.reason === "server_shutdown") {
          serverShuttingDownRef.current = true;
          console.log(
            `Server shutdown detected for session ${sessionId}, suppressing reconnect`,
          );
          // SessionStream.close() marks the stream explicitly closed, which
          // suppresses reconnection on its own — the ref is kept so a later
          // connect() reuses the same stream (mitto-7gta.30). Reconnect
          // suppression is also enforced by the stream's shouldReconnect veto
          // via serverShuttingDownRef.
          sessionWsRefs.current[sessionId]?.close();
          break; // Skip the delayed sync — server is going away
        }

        // Delayed sync to catch session_end event.
        // The server writes session_end AFTER sending acp_stopped (see background_session.go Close()),
        // so we need a short delay to allow recorder.End() to complete before requesting the event.
        // This provides faster session_end delivery (~2s) vs waiting for keepalive (~5-10s).
        setTimeout(() => {
          const ws = sessionWsRefs.current[sessionId];
          const session = sessionsRef.current[sessionId];
          // Get our last known seq (primary: ref, fallback: React state)
          const refSeq = lastKnownSeqRef.current[sessionId] || 0;
          const stateSeq = Math.max(
            getMaxSeq(session?.messages || []),
            session?.lastLoadedSeq || 0,
          );
          const lastSeq = Math.max(refSeq, stateSeq);
          if (ws && ws.state === "open" && lastSeq > 0) {
            console.log(
              `[acp_stopped] Requesting delayed sync for session ${sessionId} after_seq=${lastSeq}`,
            );
            // Mark sync in-flight so the keepalive handler does not fire a concurrent
            // stale-detection load_events while this delayed sync is pending.
            // Without this guard, keepalive can detect clientMaxSeq > serverMaxSeq and
            // pile up additional M1-fix cycles on top of the delayed sync response.
            setPendingSync(sessionId);
            ws.send({
              type: "load_events",
              data: { after_seq: lastSeq },
            });
          }
        }, 2000);
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
        const msgSeq = msg.data?.seq;
        const maxSeq = msg.data?.max_seq;
        const entries = msg.data?.entries || [];
        console.log(`Plan update received: ${entries.length} entries`);

        // Update last known seq from this event
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));

        // Dispatch event for AgentPlanPanel to handle
        window.dispatchEvent(
          new CustomEvent("mitto:plan_update", {
            detail: { sessionId, entries },
          }),
        );
        break;
      }

      case "file_read": {
        // Agent read a file
        const msgSeq = msg.data?.seq;
        const maxSeq = msg.data?.max_seq;
        console.log(`File read: ${msg.data?.path} (${msg.data?.size} bytes)`);

        // Update last known seq from this event
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));
        break;
      }

      case "file_write": {
        // Agent wrote a file
        const msgSeq = msg.data?.seq;
        const maxSeq = msg.data?.max_seq;
        console.log(`File write: ${msg.data?.path} (${msg.data?.size} bytes)`);

        // Update last known seq from this event
        updateLastKnownSeq(sessionId, Math.max(msgSeq || 0, maxSeq || 0));
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
        // The context-flush command may only become resolvable once the
        // agent's available commands arrive (runtime-detected fallback,
        // mitto-1o8); merge it in so the flush button un-greys live. Use ??
        // to preserve the previous value when the server omits the field.
        if (msg.data?.context_flush_command !== undefined) {
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;
            return {
              ...prev,
              [sessionId]: {
                ...session,
                info: {
                  ...session.info,
                  context_flush_command:
                    msg.data.context_flush_command ??
                    session.info?.context_flush_command ??
                    "",
                },
              },
            };
          });
        }
        break;

      case "context_usage_update": {
        // Context window usage updated by the agent
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          return {
            ...prev,
            [sessionId]: {
              ...session,
              info: {
                ...session.info,
                context_usage: {
                  size: msg.data.size,
                  used: msg.data.used,
                },
              },
            },
          };
        });
        break;
      }

      case "agent_working": {
        // Transient "agent is still working" heartbeat during a prolonged silent
        // stretch of a prompt (e.g. a long tool call streaming no output). Does
        // NOT touch isStreaming — it only annotates the current streaming turn
        // with idle time / in-flight tool title so the UI can show honest progress.
        setSessions((prev) => {
          const session = prev[sessionId];
          if (!session) return prev;
          return {
            ...prev,
            [sessionId]: {
              ...session,
              agentWorking: {
                idleMs: msg.data.idle_ms || 0,
                toolTitle: msg.data.tool_title || "",
                receivedAt: Date.now(),
              },
            },
          };
        });
        break;
      }

      case "config_option_changed":
        // Config option changed (by user or agent)
        // Update the current_value for the specified config option in session info
        // Use !== undefined to allow falsy values like empty strings
        if (msg.data?.config_id && msg.data?.value !== undefined) {
          console.log(
            `Config option changed: ${msg.data.config_id} = ${msg.data.value}`,
          );
          setSessions((prev) => {
            const session = prev[sessionId];
            if (!session) return prev;
            const updatedOptions = (session.info?.config_options || []).map(
              (opt) =>
                opt.id === msg.data.config_id
                  ? { ...opt, current_value: msg.data.value }
                  : opt,
            );
            return {
              ...prev,
              [sessionId]: {
                ...session,
                info: { ...session.info, config_options: updatedOptions },
              },
            };
          });
        }
        break;
    }
  }, []);

  // Route handleSessionMessage through a ref so useWSConnection's onmessage
  // closures see the latest identity across reconnects (rule 21).
  handleSessionMessageRef.current = handleSessionMessage;

  // UI-only bookkeeping for keepalive_ack frames (mitto-7gta.30). SessionStream
  // excludes keepalive_ack from its "message" event — it interprets the
  // seq/stale-detection payload internally — so this handles everything else
  // the pre-migration composer's "keepalive_ack" case did: streaming/running
  // state sync, processor stats, and (for the active session) queue length.
  const handleSessionKeepaliveAck = useCallback((sessionId, data) => {
    const serverIsPrompting = data?.is_prompting || false;
    const currentSession = sessionsRef.current[sessionId];
    if (currentSession && currentSession.isStreaming !== serverIsPrompting) {
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

    if (data?.processor_count !== undefined) {
      setSessions((prev) => {
        const session = prev[sessionId];
        if (!session) return prev;
        const newInfo = {
          ...session.info,
          processor_count: data.processor_count,
          processor_activations:
            data.processor_activations ??
            session.info?.processor_activations ??
            0,
          processor_last_activation:
            data.processor_last_activation ??
            session.info?.processor_last_activation ??
            null,
          processor_last_names:
            data.processor_last_names ??
            session.info?.processor_last_names ??
            null,
        };
        // Only update if something changed to avoid unnecessary re-renders
        if (
          newInfo.processor_count === session.info?.processor_count &&
          newInfo.processor_activations ===
            session.info?.processor_activations &&
          newInfo.processor_last_activation ===
            session.info?.processor_last_activation
        ) {
          return prev;
        }
        return {
          ...prev,
          [sessionId]: { ...session, info: newInfo },
        };
      });
    }

    // Sync queue length from keepalive (for multi-tab sync and mobile wake recovery)
    // Only update if this is the active session to avoid unnecessary state updates
    if (
      data?.queue_length !== undefined &&
      sessionId === activeSessionIdRef.current
    ) {
      setQueueLength((prev) => {
        if (prev !== data.queue_length) {
          console.log(
            `[keepalive] Queue length sync: ${prev} -> ${data.queue_length}`,
          );
          return data.queue_length;
        }
        return prev;
      });
    }

    // Sync session status from keepalive (detect completed/error sessions)
    if (data?.status && currentSession?.info?.status !== data.status) {
      console.log(
        `[keepalive] Session ${sessionId} status sync: ${currentSession?.info?.status} -> ${data.status}`,
      );
      setSessions((prev) => {
        const session = prev[sessionId];
        if (!session) return prev;
        return {
          ...prev,
          [sessionId]: {
            ...session,
            info: { ...session.info, status: data.status },
          },
        };
      });
    }

    // Sync is_running state (detect if background session disconnected).
    // Also syncs acp_ready to match is_running — this prevents the
    // "Reconnecting to AI agent..." banner from getting stuck when the
    // client misses the acp_started event (e.g., race during unarchive).
    const serverIsRunning = data?.is_running ?? true;
    const clientAcpReady = currentSession?.info?.acp_ready ?? false;
    if (
      currentSession?.isRunning !== serverIsRunning ||
      clientAcpReady !== serverIsRunning
    ) {
      console.log(
        `[keepalive] Session ${sessionId} running state sync: isRunning=${currentSession?.isRunning}->${serverIsRunning}, acp_ready=${clientAcpReady}->${serverIsRunning}`,
      );
      setSessions((prev) => {
        const session = prev[sessionId];
        if (!session) return prev;
        return {
          ...prev,
          [sessionId]: {
            ...session,
            isRunning: serverIsRunning,
            info: { ...session.info, acp_ready: serverIsRunning },
          },
        };
      });
    }
  }, []);

  // Route through a ref (rule 21) — populated unconditionally, read by
  // useWSConnection's stream "keepalive_ack" handler.
  handleSessionKeepaliveAckRef.current = handleSessionKeepaliveAck;

  // connectToSession lives in useWSConnection (C1) — mitto-90f.6.2.
  // Composer callbacks use connectToSessionRef.current(...) instead.

  // Fetch stored sessions
  const fetchStoredSessions = useCallback(async () => {
    try {
      const data = await getSdkClient().sessions.list();
      // Update global working_dir map for each session
      (data || []).forEach((s) => {
        if (s.session_id && s.working_dir) {
          updateGlobalWorkingDir(s.session_id, s.working_dir);
        }
      });
      // Map server-side snake_case field to frontend camelCase so that the
      // hourglass icon survives fetchStoredSessions() overwriting storedSessions.
      const mapped = (data || []).map((s) => ({
        ...s,
        // Mark non-archived sessions as active so the sidebar dot shows green.
        // Under lazy-connect most sessions lack a per-session WebSocket (only the
        // active session connects one), so they don't enter activeSessions which
        // sets isActive. Without this, background sessions fall through to the
        // amber "Not connected" dot even though they are perfectly available.
        isActive: !s.archived,
        isWaitingForChildren: s.is_waiting_for_children || false,
        isStreaming: s.is_streaming || false,
        // Server-persisted UI-prompt state — restores the sidebar "?"
        // indicator (and any prior server-side dismissal) across a full
        // page reload before any live WebSocket broadcast arrives.
        isWaitingForUserInput: s.is_waiting_for_user_input || false,
        acked_ui_prompt_request_id: s.acked_ui_prompt_request_id || null,
        // Server-persisted loop-error dismissal — mirrors loop_stopped_reason
        // symmetry so the amber warning icon respects an earlier ack across
        // reloads and connected browsers.
        loop_acknowledged_stopped_reason:
          s.loop_acknowledged_stopped_reason || null,
      }));
      setStoredSessions(mapped);
      return mapped;
    } catch (err) {
      console.error("Failed to fetch sessions:", err);
      return [];
    }
  }, []);

  // Helper to expand the target session's group when navigating
  // Always expands the group containing the session so it's visible in the sidebar
  // In accordion mode, also collapses all other groups
  // Resolve a session's root-parent working_dir, which is the folder key the
  // unified sidebar tree groups it under (children are nested below their root
  // parent's folder). Returns "Unknown" when no working_dir is available.
  const resolveFolderKey = (session, storedSessions, fallbackWorkingDir) => {
    let rootParent = session;
    let depth = 0;
    while (rootParent?.parent_session_id && depth < 10) {
      const next = storedSessions.find(
        (s) => s.session_id === rootParent.parent_session_id,
      );
      if (!next) break;
      rootParent = next;
      depth++;
    }
    return rootParent?.working_dir || fallbackWorkingDir || "Unknown";
  };

  const expandGroupForSession = useCallback(
    (sessionId, workingDir, acpServer) => {
      const storedSessions = storedSessionsRef.current || [];
      const storedSession = storedSessions.find(
        (s) => s.session_id === sessionId,
      );

      // The unified sidebar tree (mitto-1er.8) groups conversations by folder
      // (working_dir, resolved to the root parent for nested children) and uses
      // UNSCOPED keys: folder.key, `archived:<folderKey>`, and `parent:<id>`.
      // Auto-expand-on-navigate must write those same unscoped keys so the
      // sidebar (and keyboard/swipe nav) react to the dispatched event.
      const folderKey = resolveFolderKey(
        storedSession,
        storedSessions,
        workingDir,
      );

      // In accordion mode, collapse every other folder first (unscoped keys).
      if (getSingleExpandedGroupMode()) {
        const allFolderKeys = new Set();
        for (const s of storedSessions) {
          allFolderKeys.add(resolveFolderKey(s, storedSessions, null));
        }
        for (const key of allFolderKeys) {
          if (key !== folderKey && isGroupExpanded(key)) {
            setGroupExpanded(key, false);
          }
        }
      }

      // Expand the folder so the row is visible (folders default to expanded,
      // so this only fires when the folder was explicitly collapsed).
      if (!isGroupExpanded(folderKey)) {
        setGroupExpanded(folderKey, true);
      }

      // Archived rows live in a per-folder `archived:<folderKey>` subgroup that
      // defaults to collapsed in the sidebar — force-expand it (and dispatch the
      // event) when navigating to an archived row.
      if (storedSession?.archived) {
        setGroupExpanded(`archived:${folderKey}`, true);
      }

      // Child rows live in a `parent:<id>` group; expand it for child sessions.
      if (storedSession?.parent_session_id) {
        const parentGroupKey = `parent:${storedSession.parent_session_id}`;
        if (!isGroupExpanded(parentGroupKey)) {
          setGroupExpanded(parentGroupKey, true);
        }
      }
    },
    [],
  );

  // sendToSession lives in useWSConnection (C1) — mitto-90f.6.2.
  // Composer callbacks use sendToSessionRef.current(...) via sendToSessionStable.

  // Config options (per-session, extracted to useWSConfigOptions sub-hook, mitto-90f.5)
  const { configOptions, setConfigOption } = useWSConfigOptions(
    activeSession,
    activeSessionId,
    sendToSessionStable,
  );

  // Send ensure_resumed to the active session's WebSocket.
  // Called when the user focuses on a conversation so the server can resume
  // the ACP connection immediately, bypassing any startup stagger delay.
  const ensureResumed = useCallback((sessionId) => {
    const targetId = sessionId || activeSessionIdRef.current;
    if (!targetId) return;
    sendToSessionRef.current?.(targetId, { type: "ensure_resumed", data: {} });
  }, []);

  // Switch to an existing session
  // Uses reverse-order loading for better UX: newest messages load first,
  // so the conversation opens already positioned at the latest message.
  const switchSession = useCallback(
    async (sessionId) => {
      // Selection is immediate UI intent, not a consequence of metadata loading.
      // Activating before the first await keeps unloaded sessions responsive and
      // prevents a slower earlier click from stealing focus after a later click.
      const hadPendingContent =
        sessionUpdateSchedulerRef.current.flushSession(sessionId);
      setActiveSessionId(sessionId);

      // Reconnect attempt tracking is now internal to SessionStream
      // (mitto-7gta.30) — no per-session counter to reset here.

      // Use sessionsRef to get current sessions state and avoid stale closures
      const currentSessions = sessionsRef.current;
      // Check if session already has messages loaded (not just an empty placeholder from WebSocket)
      const existingSession = currentSessions[sessionId];
      const hasLoadedMessages = sessionHasLoadedMessages(
        existingSession,
        hadPendingContent,
      );
      const hasWorkingDir = existingSession?.info?.working_dir;

      // Get session info from stored sessions (for accordion mode group expansion)
      const storedSession = storedSessionsRef.current?.find(
        (s) => s.session_id === sessionId,
      );

      const workingDir =
        existingSession?.info?.working_dir || storedSession?.working_dir || "";
      const acpServer =
        existingSession?.info?.acp_server || storedSession?.acp_server || "";

      // In accordion mode, expand the group containing this session
      // (and collapse all other groups)
      expandGroupForSession(sessionId, workingDir, acpServer);

      if (hasLoadedMessages && hasWorkingDir) {
        // Ensure the session stream is connected and synced.
        // On mobile, the connection may have died while the phone slept.
        // If not connected, connect now — the stream's "open" handler syncs events.
        const existingWs = sessionWsRefs.current[sessionId];
        if (!existingWs || existingWs.state !== "open") {
          console.log(
            `Session ${sessionId} has messages but WebSocket is not connected, reconnecting...`,
          );
          // Discard the stale stream entirely (it may be stuck in "connecting")
          // so connectToSession builds a fresh one — dropping the ref BEFORE
          // close() so the closed stream is never reused (mitto-7gta.30).
          if (existingWs) {
            delete sessionWsRefs.current[sessionId];
            existingWs.close();
          }
          connectToSessionRef.current?.(sessionId);
        } else {
          // WebSocket is already open — hint the server to resume ACP if not running.
          // This bypasses any startup stagger and allows the session to become
          // interactive immediately when the user navigates to it.
          // Skip for archived sessions — they have no ACP connection to resume.
          const isArchived =
            existingSession?.info?.archived || storedSession?.archived || false;
          if (!isArchived) {
            ensureResumed(sessionId);
          }
        }
        return;
      }

      // Load session events from API (with limit for faster initial load)
      try {
        // Get session metadata first to know total event count and working_dir
        const meta = await getSdkClient()
          .sessions.get(sessionId)
          .catch(() => ({}));

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
        connectToSessionRef.current?.(sessionId);
      } catch (err) {
        console.error("Failed to switch session:", err);
      }
    },
    [expandGroupForSession, ensureResumed],
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

        // If this is a child session, auto-expand the parent session group and folder
        if (msg.data.parent_session_id) {
          const parentKey = `parent:${msg.data.parent_session_id}`;
          setGroupExpanded(parentKey, true);

          // Also expand the folder containing the parent session (unscoped key).
          const folderKey = resolveFolderKey(
            msg.data,
            storedSessionsRef.current,
            msg.data.working_dir,
          );
          if (folderKey) setGroupExpanded(folderKey, true);
          if (msg.data.archived && folderKey)
            setGroupExpanded(`archived:${folderKey}`, true);
        }

        setStoredSessions((prev) => {
          const exists = prev.find((s) => s.session_id === msg.data.session_id);
          if (exists) return prev;

          return [
            {
              session_id: msg.data.session_id,
              name: msg.data.name || "New conversation",
              acp_server: msg.data.acp_server,
              working_dir: msg.data.working_dir,
              parent_session_id: msg.data.parent_session_id || null, // Preserve parent-child relationship
              child_origin: msg.data.child_origin || null, // Preserve child origin (mcp, auto, human) for icon rendering
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

      case "session_beads_issue_updated":
        // Update linked beads issue in stored sessions so the sidebar/list
        // reflects the new link without a refetch.
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, beads_issue: msg.data.beads_issue || "" }
              : s,
          ),
        );
        // Also update the active session's info so the conversation header's
        // linked-issue button re-renders with the new id (or clears when empty).
        setSessions((prev) => {
          const session = prev[msg.data.session_id];
          if (!session) return prev;
          return {
            ...prev,
            [msg.data.session_id]: {
              ...session,
              info: {
                ...session.info,
                beads_issue: msg.data.beads_issue || "",
              },
            },
          };
        });
        break;

      case "session_archived":
        // Update session archived state in stored sessions
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? {
                  ...s,
                  archived: msg.data.archived,
                  archive_pending: false,
                  archive_reason: msg.data.archive_reason || "",
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
                archived: msg.data.archived,
                archive_pending: false,
                archive_reason: msg.data.archive_reason || "",
              },
            },
          };
        });
        // If the active session was just archived, prefer navigating to that
        // conversation's folder Tasks (beads) view so the user stays in the same
        // workspace context instead of being bounced to another conversation
        // (mitto-17d). The callback is wired by app.js to handleBeadsOpen; fall
        // back to switching to another non-archived conversation when it isn't
        // set or the folder can't be resolved. (Same-window archives navigate
        // synchronously in archiveSession; this covers cross-window broadcasts.)
        if (
          msg.data.archived &&
          msg.data.session_id === activeSessionIdRef.current
        ) {
          const archivedSess = (storedSessionsRef.current || []).find(
            (s) => s.session_id === msg.data.session_id,
          );
          const archivedFolderWorkingDir = resolveFolderKey(
            archivedSess,
            storedSessionsRef.current || [],
            archivedSess?.working_dir,
          );
          if (onActiveSessionRemovedRef?.current) {
            setActiveSessionId(null);
            onActiveSessionRemovedRef.current(archivedFolderWorkingDir);
          } else {
            const remaining = (storedSessionsRef.current || []).filter(
              (s) => s.session_id !== msg.data.session_id && !s.archived,
            );
            if (remaining.length > 0) {
              setActiveSessionId(remaining[0].session_id);
            } else {
              setActiveSessionId(null);
            }
          }
        }
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

      case "session_waiting":
        // Update session waiting-for-children state
        // This is broadcast when a parent starts/stops blocking on mitto_children_tasks_wait
        console.log(
          `[global] Session waiting state changed: ${msg.data.session_id} -> ${msg.data.is_waiting}`,
        );
        // Update in stored sessions (for sidebar display)
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? { ...s, isWaitingForChildren: msg.data.is_waiting }
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
              isWaitingForChildren: msg.data.is_waiting,
            },
          };
        });
        break;

      case "session_ui_prompt":
        // Update session waiting-for-user-input state
        // This is broadcast when a session starts/stops blocking on a UI prompt,
        // OR when a user acknowledges (dismisses) the sidebar "?" indicator for
        // the currently-active prompt. The acked_request_id field is present in
        // the latter case; when isWaiting flips false the ack is cleared so a
        // future prompt re-surfaces the indicator.
        console.log(
          `[global] Session UI prompt state changed: ${msg.data.session_id} -> ${msg.data.is_waiting}${msg.data.acked_request_id ? ` (acked=${msg.data.acked_request_id})` : ""}`,
        );
        // Update in stored sessions (for sidebar display)
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? {
                  ...s,
                  isWaitingForUserInput: msg.data.is_waiting,
                  acked_ui_prompt_request_id: msg.data.is_waiting
                    ? msg.data.acked_request_id ||
                      s.acked_ui_prompt_request_id ||
                      null
                    : null,
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
              isWaitingForUserInput: msg.data.is_waiting,
              acked_ui_prompt_request_id: msg.data.is_waiting
                ? msg.data.acked_request_id ||
                  session.acked_ui_prompt_request_id ||
                  null
                : null,
            },
          };
        });
        break;

      case "background_ui_prompt_timeout": {
        // A blocking UI prompt timed out in a session the user was not actively viewing.
        // Show a native OS notification (sticky) so the user knows the session needed input.
        const {
          session_id: timedOutSessionId,
          session_name: timedOutSessionName,
          question: timedOutQuestion,
        } = msg.data;
        console.log(
          `[global] Background UI prompt timed out: ${timedOutSessionId} (${timedOutSessionName})`,
        );
        setBackgroundUIPromptTimeout({
          sessionId: timedOutSessionId,
          sessionName: timedOutSessionName,
          question: timedOutQuestion,
          timestamp: Date.now(),
        });
        break;
      }

      case "loop_updated":
        // Update session loop state
        // This is broadcast when any session's loop state changes
        //
        // Two separate concepts:
        // - loop_configured: true if loop config exists (determines UI mode - shows frequency panel)
        // - loop_enabled: true if loop runs are active (determines lock state)
        //
        // Also includes frequency and next_scheduled_at for cross-client sync
        console.log(
          `[global] Session loop state changed: ${msg.data.session_id} -> configured=${msg.data.loop_configured}, enabled=${msg.data.loop_enabled}`,
        );
        // Update in stored sessions:
        // loop_enabled: runs active → sidebar category + clock icon
        // loop_configured: config exists → editor UI mode
        setStoredSessions((prev) =>
          prev.map((s) =>
            s.session_id === msg.data.session_id
              ? {
                  ...s,
                  loop_enabled: msg.data.loop_enabled,
                  loop_configured: msg.data.loop_configured,
                  next_scheduled_at: msg.data.next_scheduled_at || null,
                  loop_frequency: msg.data.frequency || null,
                  loop_iteration_count: msg.data.iteration_count ?? null,
                  loop_max_iterations: msg.data.max_iterations ?? null,
                  loop_stopped_reason: msg.data.loop_stopped_reason || null,
                  loop_acknowledged_stopped_reason:
                    msg.data.loop_acknowledged_stopped_reason || null,
                  loop_trigger: msg.data.trigger ?? null,
                  // mitto-r6j: canonical armed-triggers list. The scalar
                  // `trigger` above stays for backward-compat readers.
                  loop_triggers: msg.data.triggers ?? null,
                  loop_delay_seconds: msg.data.delay_seconds ?? null,
                  loop_max_duration_seconds:
                    msg.data.max_duration_seconds ?? null,
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
                // loop_enabled: runs active → sidebar category + clock icon
                loop_enabled: msg.data.loop_enabled,
                // loop_configured: config exists → editor UI mode
                loop_configured: msg.data.loop_configured,
                next_scheduled_at: msg.data.next_scheduled_at || null,
                loop_frequency: msg.data.frequency || null,
                loop_iteration_count: msg.data.iteration_count ?? null,
                loop_max_iterations: msg.data.max_iterations ?? null,
                loop_stopped_reason: msg.data.loop_stopped_reason || null,
                loop_acknowledged_stopped_reason:
                  msg.data.loop_acknowledged_stopped_reason || null,
                loop_trigger: msg.data.trigger ?? null,
                // mitto-r6j: canonical armed-triggers list; scalar above
                // stays for backward-compat readers.
                loop_triggers: msg.data.triggers ?? null,
                loop_delay_seconds: msg.data.delay_seconds ?? null,
                loop_max_duration_seconds:
                  msg.data.max_duration_seconds ?? null,
              },
            },
          };
        });
        // Dispatch the authoritative config for open editors/controls. The
        // compatibility fields remain while consumers migrate to loopConfig.
        window.dispatchEvent(
          new CustomEvent("mitto:loop_config_updated", {
            detail: {
              sessionId: msg.data.session_id,
              loopConfig:
                msg.data.loop_config && typeof msg.data.loop_config === "object"
                  ? msg.data.loop_config
                  : null,
              // loopConfigured controls UI mode
              loopConfigured: msg.data.loop_configured,
              // loopEnabled controls lock state (whether runs are active)
              loopEnabled: msg.data.loop_enabled,
              frequency: msg.data.frequency,
              nextScheduledAt: msg.data.next_scheduled_at,
              freshContext: msg.data.fresh_context,
              iterationCount: msg.data.iteration_count,
              maxIterations: msg.data.max_iterations,
              stoppedReason: msg.data.loop_stopped_reason || null,
            },
          }),
        );
        break;

      case "loop_started":
        // A loop prompt was delivered to a session
        // Show toast notification and trigger native notification if enabled
        console.log(
          `[global] Loop started: ${msg.data.session_id} (${msg.data.session_name})`,
        );
        setLoopStarted({
          sessionId: msg.data.session_id,
          sessionName: msg.data.session_name,
          timestamp: Date.now(),
        });
        break;

      case "session_deleted": {
        const deletedId = msg.data.session_id;
        // Resolve the deleted conversation's folder before removing it from
        // local state, so that if it was the active conversation we can navigate
        // to that folder's Tasks view (mitto-17d).
        const deletedSessForFolder = (storedSessionsRef.current || []).find(
          (s) => s.session_id === deletedId,
        );
        const deletedFolderWorkingDir = resolveFolderKey(
          deletedSessForFolder,
          storedSessionsRef.current || [],
          deletedSessForFolder?.working_dir,
        );
        setStoredSessions((prev) =>
          prev.filter((s) => s.session_id !== deletedId),
        );
        const currentId = activeSessionIdRef.current;
        if (deletedId === currentId) {
          // Clear both the callback-visible ref and persisted selection before
          // any queued sync/reconnect callback can revive the deleted session.
          activeSessionIdRef.current = null;
          setLastActiveSessionId(null);
        } else if (getLastActiveSessionId() === deletedId) {
          setLastActiveSessionId(null);
        }
        setSessions((prev) => {
          const { [deletedId]: removed, ...rest } = prev;
          if (deletedId === currentId) {
            // Prefer navigating to the deleted conversation's folder Tasks view
            // so the user stays in the same workspace context instead of being
            // bounced to another conversation or an empty state (mitto-17d).
            if (onActiveSessionRemovedRef?.current) {
              setActiveSessionId(null);
              onActiveSessionRemovedRef.current(deletedFolderWorkingDir);
            } else {
              const remainingStored = (storedSessionsRef.current || []).filter(
                (s) => s.session_id !== deletedId && !s.archived,
              );
              if (remainingStored.length > 0) {
                setActiveSessionId(remainingStored[0].session_id);
              } else {
                // Don't create a new session here - let the user do it manually
                // or let the initiating window handle it. This prevents multiple
                // windows from all creating new sessions simultaneously.
                setActiveSessionId(null);
              }
            }
          }
          return rest;
        });
        // Close the session WebSocket (SessionStream owns its own reconnect
        // timer internally and cancels it on close — mitto-7gta.30).
        if (sessionWsRefs.current[deletedId]) {
          sessionWsRefs.current[deletedId].close();
          delete sessionWsRefs.current[deletedId];
        }
        // M1 fix: Clear seen seqs for deleted session
        clearSeenSeqs(deletedId);
        // Clear the persisted seq watermark so a future session with the same ID
        // (unlikely but possible) starts fresh.
        setLastSeenSeq(deletedId, 0);
        break;
      }

      case "acp_started":
        // Server notifies that the ACP connection for a session was started
        // This is broadcast to all clients after unarchiving a session
        console.log("ACP started for session:", msg.data?.session_id);
        // Update session state to allow prompts
        // Must also set acp_ready to dismiss the "Reconnecting to AI agent..." banner
        setSessions((prev) => {
          const session = prev[msg.data?.session_id];
          if (!session) return prev;
          // Only append the "Session resumed" system message when the session was
          // actually suspended or stopped. Multiple resume paths (WS connect,
          // ensure_resumed on focus, unarchive) can each trigger acp_started
          // broadcasts; gating on the prior state prevents duplicate messages
          // when the session was already running.
          const wasSuspended =
            session.info?.gc_suspended === true || session.isRunning === false;
          const nextInfo = {
            ...session.info,
            acp_ready: true,
            gc_suspended: false, // Clear GC-suspended state on resume
          };
          if (!wasSuspended) {
            return {
              ...prev,
              [msg.data.session_id]: {
                ...session,
                isRunning: true,
                info: nextInfo,
              },
            };
          }
          return {
            ...prev,
            [msg.data.session_id]: {
              ...session,
              isRunning: true,
              info: nextInfo,
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

      case "task_label_colors_updated":
        window.dispatchEvent(
          new CustomEvent("mitto:task_label_colors_updated", {
            detail: msg.data,
          }),
        );
        break;

      case "folder_task_label_colors_updated":
        window.dispatchEvent(
          new CustomEvent("mitto:folder_task_label_colors_updated", {
            detail: msg.data,
          }),
        );
        break;

      case "beads_cleanup_progress":
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:beads_cleanup_progress", {
              detail: msg.data,
            }),
          );
        }
        break;

      case "beads_changed":
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:beads_changed", { detail: msg.data }),
          );
        }
        break;

      case "mcp_tools_unavailable":
        // Server notifies that Mitto MCP tools are not available in the ACP agent.
        // Dispatches an event so UI components can show an installation prompt.
        console.warn("MCP tools unavailable:", msg.data);
        window.dispatchEvent(
          new CustomEvent("mitto:mcp_tools_unavailable", { detail: msg.data }),
        );
        break;

      case "mcp_tools_available":
        // Server notifies that MCP tools are available for a workspace.
        // Store them keyed by workspace UUID so the UI can display them.
        console.log(
          "[MCP] Tools available for workspace:",
          msg.data.workspace_uuid,
          "count:",
          msg.data.tools?.length,
        );
        if (msg.data.workspace_uuid) {
          setWorkspaceMcpTools((prev) => ({
            ...prev,
            [msg.data.workspace_uuid]: msg.data.tools || [],
          }));
        }
        // Signal that prompts should be re-fetched: backend filters using new MCP tool context
        window.dispatchEvent(
          new CustomEvent("mitto:prompts_changed", {
            detail: { reason: "mcp_tools_available" },
          }),
        );
        break;

      case "prewarm_pin_alert":
        console.warn("Prewarm pin alert:", msg.data);
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:prewarm_pin_alert", { detail: msg.data }),
          );
        }
        break;

      case "slack_connection_status":
        // Live per-app Socket Mode connection status (mitto-yn5). Credential-free;
        // relayed as-is for Settings > Slack to derive delivery-health warnings.
        if (msg.data) {
          window.dispatchEvent(
            new CustomEvent("mitto:slack_connection_status", {
              detail: msg.data,
            }),
          );
          // mitto-mfd: the durable event journal just started rejecting this
          // app's events (error_class "journal"). Fire a dedicated event so a
          // background hook can surface a throttled toast even when Settings
          // > Slack isn't open — before Slack's own sustained-failure
          // auto-disable kicks in.
          if (msg.data.error_class === "journal") {
            window.dispatchEvent(
              new CustomEvent("mitto:slack_journal_rejecting", {
                detail: msg.data,
              }),
            );
          }
        }
        break;
    }
  }, []);

  // connectToEvents lives in useWSConnection (C1) — mitto-90f.6.2.
  // Composer callbacks use connectToEventsRef.current(...) instead.

  // Create a new session via REST API
  // Options: { name?: string, workingDir?: string, acpServer?: string }
  // Returns on first call:
  //   { sessionId } on immediate success
  //   { error, errorCode: "session_creation_timeout", retrying: true } when agent is busy
  //     → the auto-retry loop continues silently; isCreatingSession stays true
  //   { error, errorCode } for other failures
  // When an auto-retry eventually succeeds, setActiveSessionId fires and the session
  // appears in the sidebar (the original caller has already returned).
  const createNewSession = useCallback(async (options = {}) => {
    // Cancel any pending auto-retry — a fresh manual click supersedes it.
    if (_sessionCreationRetryTimer !== null) {
      clearTimeout(_sessionCreationRetryTimer);
      _sessionCreationRetryTimer = null;
    }

    // Support both old (name string) and new (options object) signatures
    const opts = typeof options === "string" ? { name: options } : options;
    // Capture the working dir early so all clear sites use the same value.
    const wd = opts.workingDir || "";

    // Mark creation as in-flight so the targeted folder button shows a spinner.
    setCreatingWorkingDirs((prev) => {
      const s = new Set(prev);
      s.add(wd);
      return s;
    });

    try {
      const sessionBody = {
        name: opts.name || "",
        working_dir: wd,
        acp_server: opts.acpServer || "",
        beads_issue: opts.beadsIssue || "",
        origin_prompt_name: opts.originPromptName || "",
        initial_prompt_name: opts.initialPromptName || "",
      };
      if (opts.arguments && Object.keys(opts.arguments).length > 0) {
        sessionBody.arguments = opts.arguments;
      }
      const data = await getSdkClient().sessions.create(sessionBody);

      // Success — reset all retry state and clear busy indicator.
      _sessionCreationRetryCount = 0;
      _sessionCreationPendingOpts = null;
      setCreatingWorkingDirs((prev) => {
        const s = new Set(prev);
        s.delete(wd);
        return s;
      });

      const sessionId = data.session_id;

      // Singleton find-or-route: backend routed this create to an EXISTING
      // conversation. Do NOT seed placeholder state — that would clobber the
      // already-loaded messages/info and flash "Start chatting with undefined".
      // Focus it instead; connect/sync restores/loads its real state. (mitto-4mb.10)
      if (isReusedConversationResponse(data)) {
        const existing = sessionsRef.current[sessionId];
        const wdForGroup = existing?.info?.working_dir || wd;
        const acpForGroup = existing?.info?.acp_server || opts.acpServer || "";
        expandGroupForSession(sessionId, wdForGroup, acpForGroup);
        connectToSessionRef.current?.(sessionId);
        setActiveSessionId(sessionId);
        return { sessionId, reused: true };
      }

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
      connectToSessionRef.current?.(sessionId);
      setActiveSessionId(sessionId);

      return { sessionId, reused: data.reused === true };
    } catch (err) {
      // The SDK throws for both HTTP-level failures (mirrors the old
      // `!response.ok` branch, which ran inside the try) and true
      // network/fetch failures (the old outer catch) — errorStatus(err)
      // distinguishes them: defined for a MittoApiError, undefined for a
      // MittoNetworkError.
      const status = errorStatus(err);
      if (status === undefined) {
        // Network/fetch error — clear busy state
        _sessionCreationRetryCount = 0;
        _sessionCreationPendingOpts = null;
        setCreatingWorkingDirs((prev) => {
          const s = new Set(prev);
          s.delete(wd);
          return s;
        });
        console.error(`[createNewSession] Network error:`, err);
        return { error: err.message || "Network error" };
      }

      console.error("Failed to create session:", err);
      const errorCode = err.code;

      // Agent is busy (503 session_creation_timeout) — schedule auto-retry
      // instead of silently backing off. isCreatingSession stays true so the
      // button remains in spinner state until the retry succeeds or is exhausted.
      if (
        errorCode === "session_creation_timeout" &&
        _sessionCreationRetryCount < SESSION_CREATION_MAX_RETRIES
      ) {
        _sessionCreationRetryCount++;
        _sessionCreationPendingOpts = opts;
        console.warn(
          `[createNewSession] Agent busy — scheduling retry ${_sessionCreationRetryCount}/${SESSION_CREATION_MAX_RETRIES} in ${SESSION_CREATION_RETRY_DELAY_MS}ms`,
        );
        _sessionCreationRetryTimer = setTimeout(() => {
          const pendingOpts = _sessionCreationPendingOpts;
          _sessionCreationRetryTimer = null;
          if (pendingOpts) {
            // Use ref to always call the latest version of createNewSession
            createNewSessionRef.current?.(pendingOpts);
          }
        }, SESSION_CREATION_RETRY_DELAY_MS);
        // Return immediately so the caller can show a toast; isCreatingSession
        // remains true while the retry is pending.
        return {
          error: "Agent is busy — retrying automatically\u2026",
          errorCode: "session_creation_timeout",
          retrying: true,
        };
      }

      // Other errors, or retry limit exhausted — clear busy state.
      _sessionCreationRetryCount = 0;
      _sessionCreationPendingOpts = null;
      setCreatingWorkingDirs((prev) => {
        const s = new Set(prev);
        s.delete(wd);
        return s;
      });
      return {
        error: errorMessage(err, "Failed to create session"),
        errorCode,
      };
    }
  }, []);

  // Keep ref current so the retry timer always calls the latest createNewSession
  // (connectToSession may change between retries, recreating the callback).
  createNewSessionRef.current = createNewSession;

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

  // sendPrompt, cancelPrompt, forceReset, retryPendingPrompts, and
  // resolvePendingSendsForSession live in useWSDeliveryVerification (C2) —
  // mitto-90f.6.3. The sub-hook is called AFTER useWSConnection returns so
  // it can consume the transport primitives (see below). The composer keeps
  // pendingSendsRef and lastConfirmedPromptRef ownership because
  // handleSessionMessage reads/writes them at ~10 sites; both are passed
  // in as props to C2.

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
    if (!ws || ws.state !== "open") {
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
    ws.send({
      type: "load_events",
      data: {
        limit: INITIAL_EVENTS_LIMIT,
        before_seq: session.firstLoadedSeq,
      },
    });
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
        await getSdkClient().sessions.update(sessionId, { name });
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
      await getSdkClient().sessions.update(sessionId, { pinned });
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

  // Set/clear a session's background color via REST API
  const setSessionColor = useCallback(async (sessionId, color) => {
    try {
      await getSdkClient().sessions.update(sessionId, {
        background_color: color,
      });
      // Update local state for stored sessions
      setStoredSessions((prev) =>
        prev.map((s) =>
          s.session_id === sessionId ? { ...s, background_color: color } : s,
        ),
      );
      // Update local state for active sessions
      setSessions((prev) => {
        const session = prev[sessionId];
        if (!session) return prev;
        return {
          ...prev,
          [sessionId]: {
            ...session,
            info: { ...session.info, background_color: color },
          },
        };
      });
    } catch (err) {
      console.error("Failed to set session color:", err);
    }
  }, []);

  // Archive/unarchive a session via REST API
  const archiveSession = useCallback(async (sessionId, archived) => {
    try {
      await getSdkClient().sessions.update(sessionId, { archived });
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
      // If the active conversation was just archived, navigate to its folder
      // Tasks (beads) view so the user stays in the same workspace context
      // instead of being bounced to another conversation (mitto-17d). Mirrors
      // removeSession; the session_archived broadcast covers cross-window. When
      // the callback isn't set or the folder can't be resolved, the broadcast
      // handler's fallback takes over (active session is left unchanged here).
      if (archived && sessionId === activeSessionIdRef.current) {
        const archivedSess = (storedSessionsRef.current || []).find(
          (s) => s.session_id === sessionId,
        );
        const folderWorkingDir = resolveFolderKey(
          archivedSess,
          storedSessionsRef.current || [],
          archivedSess?.working_dir,
        );
        if (onActiveSessionRemovedRef?.current) {
          setActiveSessionId(null);
          onActiveSessionRemovedRef.current(folderWorkingDir);
        }
      }
    } catch (err) {
      console.error("Failed to archive/unarchive session:", err);
    }
  }, []);

  const removeSession = useCallback(
    async (sessionId) => {
      const currentActiveSessionId = activeSessionIdRef.current;
      const wasActiveSession = sessionId === currentActiveSessionId;

      // Capture deleted session's group info before removing (for accordion mode fallback)
      const deletedSession = storedSessionsRef.current?.find(
        (s) => s.session_id === sessionId,
      );
      const deletedWorkingDir = deletedSession?.working_dir || "";

      // Close the session WebSocket (SessionStream owns its own reconnect
      // timer internally and cancels it on close — mitto-7gta.30).
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
        await getSdkClient().sessions.remove(sessionId);
      } catch (err) {
        console.error("Failed to delete session:", err);
      }

      // If we removed the active session, decide where to navigate.
      if (wasActiveSession) {
        // Prefer letting app.js's landing callback route the UI (to the global
        // Dashboard — mitto-ce3) so the user stays out of an arbitrary
        // remaining conversation. Fall back to switching to the most recent
        // remaining conversation when the callback isn't wired.
        const folderWorkingDir = resolveFolderKey(
          deletedSession,
          storedSessionsRef.current || [],
          deletedWorkingDir,
        );
        if (onActiveSessionRemovedRef?.current) {
          setActiveSessionId(null);
          onActiveSessionRemovedRef.current(folderWorkingDir);
          // Refresh the sidebar so it reflects the deletion.
          await fetchStoredSessions();
          return;
        }
        // Fetch remaining sessions from server to get accurate list
        const remainingSessions = await fetchStoredSessions();
        if (remainingSessions && remainingSessions.length > 0) {
          const remainingActive = remainingSessions.filter((s) => !s.archived);

          if (remainingActive.length > 0) {
            // In accordion mode, prefer a session from the same folder
            let nextSession = remainingActive[0];
            if (getSingleExpandedGroupMode()) {
              const deletedFolderKey = resolveFolderKey(
                deletedSession,
                remainingSessions,
                deletedWorkingDir,
              );
              if (deletedFolderKey) {
                const sameFolderSession = remainingActive.find(
                  (s) =>
                    resolveFolderKey(s, remainingSessions, s.working_dir) ===
                    deletedFolderKey,
                );
                if (sameFolderSession) {
                  nextSession = sameFolderSession;
                }
              }
            }
            switchSession(nextSession.session_id);
          } else {
            // No sessions left — clear active session, don't switch tabs
            setActiveSessionId(null);
          }
        } else {
          // No sessions left at all - show empty state, let user create manually
          setActiveSessionId(null);
        }
      }
    },
    [fetchStoredSessions, switchSession],
  );

  // Initialize on mount
  useEffect(() => {
    connectToEventsRef.current?.();
    return () => {
      if (eventsStreamRef.current) eventsStreamRef.current.close();
      // Session reconnect timers are now owned internally by each
      // SessionStream (mitto-7gta.30) and cancelled by ws.close() below.
      // Close all session WebSockets
      for (const ws of Object.values(sessionWsRefs.current)) {
        ws.close();
      }
      // Clear all pending sync timeouts to prevent memory leaks on unmount
      for (const timerId of Object.values(syncTimeoutRef.current)) {
        clearTimeout(timerId);
      }
      syncTimeoutRef.current = {};
      // Clear pending staggered background-session reconnect timers
      for (const timerId of Object.values(
        staggeredBackgroundTimersRef.current,
      )) {
        clearTimeout(timerId);
      }
      staggeredBackgroundTimersRef.current = {};
      sessionUpdateSchedulerRef.current.dispose();
    };
  }, []);

  // forceReconnectActiveSession lives in useWSConnection (C1) — mitto-90f.6.2.
  // A stable wrapper is returned from this hook so callers get a consistent
  // identity across renders even if C1's internal callback is re-memoised.
  const forceReconnectActiveSession = useCallback(() => {
    forceReconnectActiveSessionRef.current?.();
  }, []);

  // reconnectAllSessionsStaggered lives in useWSConnection (C1) — mitto-90f.6.2.
  // Stable wrapper — same rationale as forceReconnectActiveSession above.
  const reconnectAllSessionsStaggered = useCallback(() => {
    reconnectAllSessionsStaggeredRef.current?.();
  }, []);

  // Lazy-connect background-session disconnect sweep lives in useWSConnection (C1)
  // — mitto-90f.6.2. (moved from composer)

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
      if (ws && ws.state === "open") {
        ws.send({
          type: "load_events",
          data: { limit: INITIAL_EVENTS_LIMIT },
        });
      } else {
        // WebSocket not connected, reconnect
        forceReconnectActiveSessionRef.current?.();
      }
    }
  }, [activeSessionId, sessions]);

  // Mobile resilience sub-hook (mitto-90f.6.1): owns lastHiddenTimeRef,
  // staleRecoveryCooldownRef, isMobileDevice, and the visibilitychange effect
  // that reconnects sessions after wake from sleep. Destructured at composer
  // scope so pre-existing bare references (in handleSessionMessage's closure
  // and INITIAL_ACK_TIMEOUT_MS above) resolve to the sub-hook's bindings.
  // Placement rationale: must be after reconnectAllSessionsStaggered /
  // fetchStoredSessions / switchSession are defined (they are passed as
  // props). Closure captures the lexical binding, which is initialised before
  // any effect body or event handler ever runs (post-render), so the earlier
  // callbacks that reference the ref names still work.
  const mobileResilience = useWSMobileResilience({
    reconnectAllSessionsStaggered,
    fetchStoredSessions,
    switchSession,
    setActiveSessionId,
    activeSessionIdRef,
  });
  const { isMobileDevice, staleRecoveryCooldownRef, lastHiddenTimeRef } =
    mobileResilience;
  // Silence unused-var warnings from static analyzers: these are consumed by
  // closures declared earlier in this file (handleSessionMessage) and by
  // INITIAL_ACK_TIMEOUT_MS via lexical scope.
  void staleRecoveryCooldownRef;
  void lastHiddenTimeRef;
  void isMobileDevice;

  // ============================================================================
  // WebSocket transport sub-hook (C1, mitto-90f.6.2)
  // ============================================================================
  // Owns per-session and global-events WebSocket lifecycles, keepalive/zombie
  // detection, exponential-backoff reconnect, staggered wake reconnects, and
  // the lazy-connect background-disconnect sweep. Called after useWSMobileResilience
  // because it consumes staleRecoveryCooldownRef. Its returned callbacks are
  // routed back to composer callbacks declared earlier via seven forward-refs
  // populated on the next line (see the top of this composer for declarations).
  const connection = useWSConnection({
    activeSessionId,
    activeSessionIdRef,
    sessionsRef,
    storedSessionsRef,
    setSessions,
    handleSessionMessageRef,
    handleSessionKeepaliveAckRef,
    handleGlobalEvent,
    fetchStoredSessions,
    switchSession,
    onNoInitialSessionRef,
    retryPendingPromptsRef,
    rejectOversizedPromptsRef,
    lastKnownSeqRef,
    staleRecoveryCooldownRef,
    pendingSyncRef,
    needsContextLoadRef,
    setPendingSync,
    clearPendingSync,
  });
  const {
    connected,
    forceReconnectActiveSession: c1ForceReconnectActiveSession,
    reconnectAllSessionsStaggered: c1ReconnectAllSessionsStaggered,
    connectToSession,
    connectToEvents,
    isConnectionHealthy,
    waitForSessionConnection,
    sendToSession,
    sessionWsRefs,
    serverShuttingDownRef,
    eventsStreamRef,
    staggeredBackgroundTimersRef,
  } = connection;
  // Populate forward-refs so the composer callbacks declared earlier (which
  // ref-indirect via xxxRef.current) reach the sub-hook's implementations.
  connectToSessionRef.current = connectToSession;
  sendToSessionRef.current = sendToSession;
  waitForSessionConnectionRef.current = waitForSessionConnection;
  isConnectionHealthyRef.current = isConnectionHealthy;
  connectToEventsRef.current = connectToEvents;
  forceReconnectActiveSessionRef.current = c1ForceReconnectActiveSession;
  reconnectAllSessionsStaggeredRef.current = c1ReconnectAllSessionsStaggered;

  // ==========================================================================
  // Delivery verification sub-hook (mitto-90f.6.3, cluster C2): owns sendPrompt
  // with its nested attemptSend/verifyDeliveryAfterReconnect budget loop,
  // cancelPrompt, forceReset, retryPendingPrompts, and
  // resolvePendingSendsForSession. Composer keeps pendingSendsRef and
  // lastConfirmedPromptRef because handleSessionMessage reads/writes them at
  // ~10 sites; both are passed in as props (sub-hook never allocates its own).
  // C1 transport primitives (isConnectionHealthy / waitForSessionConnection)
  // are consumed via stable ref-indirect wrappers so their identities stay
  // stable across renders (rule 21). sendToSession is consumed via the
  // pre-existing sendToSessionStable wrapper.
  // ==========================================================================
  const isConnectionHealthyStable = useCallback(
    (sessionId) => isConnectionHealthyRef.current?.(sessionId),
    [],
  );
  const waitForSessionConnectionStable = useCallback(
    (sessionId, timeout) =>
      waitForSessionConnectionRef.current?.(sessionId, timeout),
    [],
  );
  const {
    sendPrompt,
    cancelPrompt,
    forceReset,
    retryPendingPrompts,
    rejectOversizedPromptsForSession,
    resolvePendingSendsForSession,
  } = useWSDeliveryVerification({
    activeSessionId,
    addMessageToSession,
    updateLastMessage,
    clearActionButtons,
    setSessions,
    sendToSession: sendToSessionStable,
    waitForSessionConnection: waitForSessionConnectionStable,
    isConnectionHealthy: isConnectionHealthyStable,
    sessionWsRefs,
    isMobileDevice,
    pendingSendsRef,
    lastConfirmedPromptRef,
  });
  // Wire the composer-owned ref-bridges to C2's callbacks so C1's onopen /
  // onmessage handlers (which invoke retryPendingPromptsRef.current(sid) and
  // resolvePendingSendsRef.current(sid)) reach C2's implementations.
  // Unconditional per-render assignment is safe for mutable refs.
  retryPendingPromptsRef.current = retryPendingPrompts;
  rejectOversizedPromptsRef.current = rejectOversizedPromptsForSession;
  resolvePendingSendsRef.current = resolvePendingSendsForSession;

  // Send UI prompt answer (yes/no or select response)
  const sendUIPromptAnswer = useCallback(
    (sessionId, requestId, optionId, label, freeText = "") => {
      console.log("[UIPrompt] Sending answer:", {
        sessionId,
        requestId,
        optionId,
        label,
        freeText,
      });

      const sent = sendToSessionRef.current?.(sessionId, {
        type: "ui_prompt_answer",
        data: {
          request_id: requestId,
          option_id: optionId,
          label: label,
          free_text: freeText,
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
    [],
  );

  // Get active UI prompt for the current session
  const activeUIPrompt = useMemo(() => {
    return activeSession?.activeUIPrompt || null;
  }, [activeSession]);

  // MCP tools for the currently active session's workspace
  const mcpTools = useMemo(() => {
    const uuid = sessionInfo?.workspace_uuid;
    if (!uuid) return [];
    return workspaceMcpTools[uuid] || [];
  }, [sessionInfo, workspaceMcpTools]);

  return {
    connected,
    messages,
    sendPrompt,
    cancelPrompt,
    forceReset,
    newSession,
    isCreatingSession,
    creatingWorkingDirs,
    switchSession,
    setActiveSessionId,
    loadSession,
    loadMoreMessages,
    updateSessionName,
    renameSession,
    pinSession,
    setSessionColor,
    archiveSession,
    removeSession,
    isStreaming,
    agentWorking,
    isRunning,
    hasMoreMessages,
    hasReachedLimit,
    isLoadingMore,
    actionButtons,
    sessionInfo,
    mcpTools,
    mcpStatus,
    activeSessionId,
    activeSessions,
    storedSessions,
    fetchStoredSessions,
    backgroundCompletion,
    clearBackgroundCompletion,
    loopStarted,
    clearLoopStarted,
    backgroundUIPrompt,
    clearBackgroundUIPrompt,
    backgroundUIPromptTimeout,
    clearBackgroundUIPromptTimeout,
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
    reconnectAllSessionsStaggered,
    availableCommands,
    configOptions,
    setConfigOption,
    activeUIPrompt,
    sendUIPromptAnswer,
    ensureResumed,
  };
}
