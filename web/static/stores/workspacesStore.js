// Mitto Web Interface - Subscribable global workspaces/acpServers store (mitto-sus.11)
//
// Generalizes the mitto-sus.7 sessionsStore.js pattern (module-level state +
// Set of listeners) to the two GLOBAL (not per-session) fields
// useWSWorkspaces.js used to own as App-level useState: workspaces,
// acpServers.
//
// Problem this addresses: useWSWorkspaces's setWorkspaces/setAcpServers
// lived in useWebSocket.js's own state, which useWebSocket returns and App
// destructures -- so App re-renders (and, through it, every prop-drilled
// consumer of useWebSocket's return value, e.g. MessageList/SessionList)
// whenever the workspaces list is (re)fetched, even for components that
// only need to read it, not react to every App-level cause of re-render.
//
// Consumers: see hooks/useWorkspacesStore.js, which wraps these subscribe
// functions with useState + useEffect (Preact does not export
// useSyncExternalStore). See docs/devel/frontend-render-domains.md for the
// render-domain contract this store is part of.
//
// Writer: useWSWorkspaces.js's fetchWorkspaces callback (initial mount fetch
// + explicit refreshes after add/remove/save). Unlike queueStore/
// notificationsStore, this store is global (not per-session id) since
// workspaces/acpServers are not scoped to a single conversation.

/** @type {object[]} */
let workspaces = [];
/** @type {object[]} */
let acpServers = [];

/** @type {Set<(value: object[]) => void>} */
const workspacesListeners = new Set();
/** @type {Set<(value: object[]) => void>} */
const acpServersListeners = new Set();

function notify(listenerSet, value) {
  if (listenerSet.size === 0) return;
  for (const callback of listenerSet) {
    try {
      callback(value);
    } catch {
      // A misbehaving listener must not break the store or other listeners.
    }
  }
}

/** Returns the current workspaces array. */
export function getWorkspaces() {
  return workspaces;
}

/** Returns the current ACP servers array. */
export function getAcpServers() {
  return acpServers;
}

/** Subscribes to workspaces-array changes. Returns an unsubscribe function. */
export function subscribeWorkspaces(callback) {
  workspacesListeners.add(callback);
  return () => workspacesListeners.delete(callback);
}

/** Subscribes to acpServers-array changes. Returns an unsubscribe function. */
export function subscribeAcpServers(callback) {
  acpServersListeners.add(callback);
  return () => acpServersListeners.delete(callback);
}

/**
 * Sets the workspaces array. No-ops (does not notify) when the value is
 * reference-equal to the current value, mirroring queueStore.js's setters.
 */
export function setWorkspaces(next) {
  const resolved = next || [];
  if (resolved === workspaces) return;
  workspaces = resolved;
  notify(workspacesListeners, workspaces);
}

/** Sets the ACP servers array. See setWorkspaces for the no-op contract. */
export function setAcpServers(next) {
  const resolved = next || [];
  if (resolved === acpServers) return;
  acpServers = resolved;
  notify(acpServersListeners, acpServers);
}

/** Test-only: reset the shared store between test cases. */
export function _resetWorkspacesStoreForTests() {
  workspaces = [];
  acpServers = [];
  workspacesListeners.clear();
  acpServersListeners.clear();
}
