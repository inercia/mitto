// Mitto Web Interface - useMCPInitState Hook
// Tracks per-workspace MCP-init lifecycle state (mitto-8fm) so a persistent
// inline "Waiting for MCP servers…" indicator can survive past the existing
// transient toast (mitto:mcp_initializing / mitto:mcp_init_timed_out, wired
// in useBackgroundNotifications.js and useWebSocket.js). Backed by the same
// backend broadcasts (internal/web/server.go BroadcastMCPInitializing /
// BroadcastMCPInitTimedOut) — no protocol change required.
//
// State is keyed by workspace_uuid, falling back to working_dir when the
// UUID is absent (mirrors the lookup already used by
// useBackgroundNotifications.js's toast handlers). Entries self-clear after
// a safety cap so a workspace that never reports completion (e.g. no
// acp_ready/stream-start signal ever arrives) doesn't show a stale spinner
// or error forever.

const { useState, useEffect, useCallback, useRef } = window.preact;

const SAFETY_CAP_MS = 10 * 60 * 1000; // 10 minutes
const SAFETY_SWEEP_INTERVAL_MS = 30 * 1000;

function workspaceKey(workspaceUUID, workingDir) {
  return workspaceUUID || workingDir || null;
}

/**
 * @returns {{
 *   getMCPInitState: (workspaceUUID: string, workingDir: string) => (
 *     { initializing: boolean, timedOutAt: number|null, servers: string[], firstSeenAt: number } | null
 *   ),
 *   clearMCPInit: (workspaceUUID: string, workingDir: string) => void,
 * }}
 */
export function useMCPInitState() {
  const [statesByKey, setStatesByKey] = useState(() => new Map());
  const statesRef = useRef(statesByKey);
  statesRef.current = statesByKey;

  const setEntry = useCallback((key, entry) => {
    if (!key) return;
    setStatesByKey((prev) => {
      const next = new Map(prev);
      next.set(key, entry);
      return next;
    });
  }, []);

  const clearEntry = useCallback((key) => {
    if (!key) return;
    setStatesByKey((prev) => {
      if (!prev.has(key)) return prev;
      const next = new Map(prev);
      next.delete(key);
      return next;
    });
  }, []);

  // mitto:mcp_initializing (mcp_initializing) — agent is blocked on MCP init.
  useEffect(() => {
    const handleInitializing = (event) => {
      const data = event.detail;
      if (!data) return;
      const key = workspaceKey(data.workspace_uuid, data.working_dir);
      if (!key) return;
      setEntry(key, {
        initializing: true,
        timedOutAt: null,
        servers: [],
        firstSeenAt: Date.now(),
      });
    };
    window.addEventListener("mitto:mcp_initializing", handleInitializing);
    return () =>
      window.removeEventListener("mitto:mcp_initializing", handleInitializing);
  }, [setEntry]);

  // mitto:mcp_init_timed_out (mcp_init_timed_out) — agent gave up; persistent
  // error state until a fresh mcp_initializing restarts the cycle or the
  // safety cap elapses.
  useEffect(() => {
    const handleTimedOut = (event) => {
      const data = event.detail;
      if (!data) return;
      const key = workspaceKey(data.workspace_uuid, data.working_dir);
      if (!key) return;
      setEntry(key, {
        initializing: false,
        timedOutAt: Date.now(),
        servers: Array.isArray(data.mcp_servers) ? data.mcp_servers : [],
        firstSeenAt: Date.now(),
      });
    };
    window.addEventListener("mitto:mcp_init_timed_out", handleTimedOut);
    return () =>
      window.removeEventListener("mitto:mcp_init_timed_out", handleTimedOut);
  }, [setEntry]);

  // Safety sweep: drop any entry older than SAFETY_CAP_MS so a workspace that
  // never reports completion doesn't show a stale spinner/error indefinitely.
  useEffect(() => {
    const interval = setInterval(() => {
      const now = Date.now();
      setStatesByKey((prev) => {
        let changed = false;
        const next = new Map(prev);
        for (const [key, entry] of prev) {
          if (now - entry.firstSeenAt > SAFETY_CAP_MS) {
            next.delete(key);
            changed = true;
          }
        }
        return changed ? next : prev;
      });
    }, SAFETY_SWEEP_INTERVAL_MS);
    return () => clearInterval(interval);
  }, []);

  // Depends on statesByKey (not statesRef.current directly) so callers relying
  // on this callback's identity to know "the state changed" re-run correctly.
  const getMCPInitState = useCallback(
    (workspaceUUID, workingDir) => {
      const key = workspaceKey(workspaceUUID, workingDir);
      if (!key) return null;
      return statesRef.current.get(key) || null;
    },
    [statesByKey],
  );

  const clearMCPInit = useCallback(
    (workspaceUUID, workingDir) => {
      clearEntry(workspaceKey(workspaceUUID, workingDir));
    },
    [clearEntry],
  );

  return { getMCPInitState, clearMCPInit };
}
