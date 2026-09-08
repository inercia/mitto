// Mitto Web Interface - useAgentAuthState Hook
// Tracks per-workspace "agent auth required" state (mitto-3du) so the sidebar
// can show a persistent health pill when a workspace's agent hit an
// authentication-required failure, driven by mitto-6vs's durable auth-expiry
// signal (JSON-RPC -32000 "Authentication required"). Backed by the backend
// broadcasts internal/conversation SessionManager.broadcastAgentAuthState
// ("agent_auth_required" / "agent_auth_cleared" on /api/events) — no protocol
// change beyond those two message types.
//
// State is keyed by workspace_uuid, falling back to working_dir when the UUID
// is absent — mirrors useMCPInitState.js's lookup, since a folder-group pill
// needs to resolve state without necessarily knowing every workspace's UUID.
// Entries self-clear after a safety cap so a workspace that never recovers
// (e.g. the CLI is never re-authenticated) doesn't pin a stale pill forever.

const { useState, useCallback, useEffect, useRef } = window.preact;

const SAFETY_CAP_MS = 60 * 60 * 1000; // 1 hour — auth outages can be long-lived
const SAFETY_SWEEP_INTERVAL_MS = 60 * 1000;

function workspaceKey(workspaceUUID, workingDir) {
  return workspaceUUID || workingDir || null;
}

/**
 * @returns {{
 *   getAgentAuthState: (workspaceUUID: string, workingDir: string) => (
 *     { required: boolean, workspaceUUID: string, workingDir: string, firstSeenAt: number } | null
 *   ),
 *   getAgentAuthStateForWorkingDir: (workingDir: string) => (
 *     { required: boolean, workspaceUUID: string, workingDir: string, firstSeenAt: number } | null
 *   ),
 *   clearAgentAuth: (workspaceUUID: string, workingDir: string) => void,
 * }}
 */
export function useAgentAuthState() {
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

  // mitto:agent_auth_required — agent needs the CLI re-authenticated.
  useEffect(() => {
    const handleRequired = (event) => {
      const data = event.detail;
      if (!data) return;
      const key = workspaceKey(data.workspace_uuid, data.working_dir);
      if (!key) return;
      setEntry(key, {
        required: true,
        workspaceUUID: data.workspace_uuid || null,
        workingDir: data.working_dir || null,
        firstSeenAt: Date.now(),
      });
    };
    window.addEventListener("mitto:agent_auth_required", handleRequired);
    return () =>
      window.removeEventListener("mitto:agent_auth_required", handleRequired);
  }, [setEntry]);

  // mitto:agent_auth_cleared — a prompt succeeded after guidance was surfaced.
  useEffect(() => {
    const handleCleared = (event) => {
      const data = event.detail;
      if (!data) return;
      const key = workspaceKey(data.workspace_uuid, data.working_dir);
      if (!key) return;
      clearEntry(key);
    };
    window.addEventListener("mitto:agent_auth_cleared", handleCleared);
    return () =>
      window.removeEventListener("mitto:agent_auth_cleared", handleCleared);
  }, [clearEntry]);

  // Safety sweep: drop any entry older than SAFETY_CAP_MS so a workspace that
  // never recovers doesn't pin a stale pill indefinitely.
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
  const getAgentAuthState = useCallback(
    (workspaceUUID, workingDir) => {
      const key = workspaceKey(workspaceUUID, workingDir);
      if (!key) return null;
      return statesRef.current.get(key) || null;
    },
    [statesByKey],
  );

  // Group-level lookup by working_dir alone (mitto-3du decision #3): a
  // sidebar folder can host multiple workspaces/ACP servers, so the folder
  // header pill cannot rely on knowing a single workspace_uuid up front.
  // Scans entries for one whose stored working_dir matches, regardless of
  // which workspace_uuid key it was recorded under.
  const getAgentAuthStateForWorkingDir = useCallback(
    (workingDir) => {
      if (!workingDir) return null;
      for (const entry of statesRef.current.values()) {
        if (entry.workingDir === workingDir) return entry;
      }
      return null;
    },
    [statesByKey],
  );

  const clearAgentAuth = useCallback(
    (workspaceUUID, workingDir) => {
      clearEntry(workspaceKey(workspaceUUID, workingDir));
    },
    [clearEntry],
  );

  return { getAgentAuthState, getAgentAuthStateForWorkingDir, clearAgentAuth };
}
