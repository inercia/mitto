/**
 * Centralized API endpoint registry for the Mitto frontend.
 *
 * This is now a thin, browser-environment shim over the environment-agnostic
 * SDK registry (`sdk/core/endpoints.js`, mitto-7gta.6) — kept so the ~200
 * existing `endpoints.*` call sites across the frontend keep working
 * unchanged until they migrate to the SDK client directly.
 *
 * The registry is rebuilt from `window.mittoApiPrefix`/`window.location` on
 * every property access (memoized on the (prefix, wsBase) pair actually in
 * effect), NOT once at import time: `getApiPrefix()` reads a value the
 * server injects into the page and which may not be set yet at module-eval
 * time, and the existing test suite (and some app flows) mutate
 * `window.mittoApiPrefix` between calls and expect the very next builder
 * call to reflect it.
 *
 * Usage:
 *   import { endpoints } from "./endpoints.js";
 *   const url = endpoints.sessions.queue(id);          // GET/POST queue
 *   const url = endpoints.issues.list({ working_dir }); // GET with QS
 */
import { getApiPrefix } from "./api.js";
import { createEndpoints } from "../sdk/core/endpoints.js";

/** Absolute ws(s):// origin for the current page. Mirrors utils/api.js's
 *  wsUrl() scheme mapping; needed because the SDK's wsUrlFor() cannot
 *  derive a ws(s):// scheme from the relative baseUrl used here. */
function currentWsBaseUrl() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}`;
}

let _cachedKey = null;
let _cachedRegistry = null;

/** Returns the SDK endpoint registry for the live (apiPrefix, wsBaseUrl)
 *  pair, rebuilding only when either has changed since the last call. */
function currentRegistry() {
  const apiPrefix = getApiPrefix();
  const wsBaseUrl = currentWsBaseUrl();
  const key = `${apiPrefix}\u0000${wsBaseUrl}`;
  if (key !== _cachedKey) {
    _cachedRegistry = createEndpoints({ baseUrl: "", apiPrefix }, { wsBaseUrl });
    _cachedKey = key;
  }
  return _cachedRegistry;
}

/** Lazily proxies each resource group so `endpoints.sessions.list()` always
 *  resolves against the live prefix/origin at call time. */
function groupProxy(group) {
  return new Proxy(
    {},
    {
      get(_target, prop) {
        return currentRegistry()[group][prop];
      },
    },
  );
}

// Resource groups mirror sdk/core/endpoints.js's registry shape exactly —
// see that file for the actual path/query-building logic. Each group is a
// lazy proxy so builder calls always resolve against the live prefix/origin.
const GROUPS = [
  "issues",
  "beads",
  "sessions",
  "workspaces",
  "workspacePrompts",
  "workspaceFiles",
  "workspaceDirs",
  "folders",
  "global",
  "config",
  "agents",
  "acpServers",
  "aux",
  "runners",
  "events",
  "misc",
];

export const endpoints = Object.fromEntries(
  GROUPS.map((group) => [group, groupProxy(group)]),
);
