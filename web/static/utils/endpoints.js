/**
 * Centralized API endpoint registry for the Mitto frontend.
 *
 * Every builder returns a full URL string via `apiUrl()` so the server prefix
 * is always applied. Query parameters are built with URLSearchParams (never
 * manual concatenation). Path params are `encodeURIComponent`-escaped.
 *
 * Usage:
 *   import { endpoints } from "./endpoints.js";
 *   const url = endpoints.sessions.queue(id);          // GET/POST queue
 *   const url = endpoints.issues.list({ working_dir }); // GET with QS
 */
import { apiUrl, wsUrl } from "./api.js";

/** Build a query string from a params object, omitting null/undefined/"" values. */
function qs(params) {
  if (!params) return "";
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    sp.append(k, v);
  }
  const s = sp.toString();
  return s ? "?" + s : "";
}

const enc = encodeURIComponent;

export const endpoints = {
  /** Beads issue tracker — all migrated to /api/issues (Decision #12). */
  issues: {
    list:         (params)      => apiUrl("/api/issues") + qs(params),
    stats:        (params)      => apiUrl("/api/issues/stats") + qs(params),
    show:         (id, params)  => apiUrl(`/api/issues/${enc(id)}`) + qs(params),
    create:       (params)      => apiUrl("/api/issues") + qs(params),
    update:       (id, params)  => apiUrl(`/api/issues/${enc(id)}`) + qs(params),
    remove:       (id, params)  => apiUrl(`/api/issues/${enc(id)}`) + qs(params),
    status:       (id, params)  => apiUrl(`/api/issues/${enc(id)}/status`) + qs(params),
    comments:     (id, params)  => apiUrl(`/api/issues/${enc(id)}/comments`) + qs(params),
    dependencies: (id, params)  => apiUrl(`/api/issues/${enc(id)}/dependencies`) + qs(params),
    cleanup:      (params)      => apiUrl("/api/issues/cleanup") + qs(params),
    config:       (params)      => apiUrl("/api/issues/config") + qs(params),
    upstream:     (params)      => apiUrl("/api/issues/upstream") + qs(params),
    sync:         (params)      => apiUrl("/api/issues/sync") + qs(params),
  },

  /** Session lifecycle and sub-resources. */
  sessions: {
    list:           ()                  => apiUrl("/api/sessions"),
    running:        ()                  => apiUrl("/api/sessions/running"),
    get:            (id)                => apiUrl(`/api/sessions/${enc(id)}`),
    create:         ()                  => apiUrl("/api/sessions"),
    update:         (id)                => apiUrl(`/api/sessions/${enc(id)}`),
    remove:         (id)                => apiUrl(`/api/sessions/${enc(id)}`),
    events:         (id, params)        => apiUrl(`/api/sessions/${enc(id)}/events`) + qs(params),
    ws:             (id)                => wsUrl(`/api/sessions/${enc(id)}/ws`),
    changes:        (id)                => apiUrl(`/api/sessions/${enc(id)}/changes`),
    settings:       (id)                => apiUrl(`/api/sessions/${enc(id)}/settings`),
    periodic:       (id)                => apiUrl(`/api/sessions/${enc(id)}/periodic`),
    periodicRunNow: (id)                => apiUrl(`/api/sessions/${enc(id)}/periodic/run-now`),
    callback:       (id)                => apiUrl(`/api/sessions/${enc(id)}/callback`),
    userData:       (id)                => apiUrl(`/api/sessions/${enc(id)}/user-data`),
    queue:          (id)                => apiUrl(`/api/sessions/${enc(id)}/queue`),
    queueMsg:       (id, msgId)         => apiUrl(`/api/sessions/${enc(id)}/queue/${enc(msgId)}`),
    queueMove:      (id, msgId)         => apiUrl(`/api/sessions/${enc(id)}/queue/${enc(msgId)}/move`),
    images:         (id)                => apiUrl(`/api/sessions/${enc(id)}/images`),
    imagesFromPath: (id)                => apiUrl(`/api/sessions/${enc(id)}/images/from-path`),
    files:          (id)                => apiUrl(`/api/sessions/${enc(id)}/files`),
    filesFromPath:  (id)                => apiUrl(`/api/sessions/${enc(id)}/files/from-path`),
  },

  /** Workspaces and their sub-resources. */
  workspaces: {
    list:                 (params)         => apiUrl("/api/workspaces") + qs(params),
    create:               ()               => apiUrl("/api/workspaces"),
    effectiveRunnerConfig:(uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/effective-runner-config`),
    metadata:             (uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/metadata`),
    userDataSchema:       (uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/user-data-schema`),
    mcpTools:             (uuid, params)   => apiUrl(`/api/workspaces/${enc(uuid)}/mcp-tools`) + qs(params),
    mcpToolsInstall:      (uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/mcp-tools/install`),
    mcpToolsRemove:       (uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/mcp-tools/remove`),
    restartAcp:           (uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/restart-acp`),
    processors:           (uuid)           => apiUrl(`/api/workspaces/${enc(uuid)}/processors`),
    processor:            (uuid, name)     => apiUrl(`/api/workspaces/${enc(uuid)}/processors/${enc(name)}`),
  },

  /** Workspace-scoped prompt management. */
  workspacePrompts: {
    list:   (params)      => apiUrl("/api/workspace-prompts") + qs(params),
    create: ()            => apiUrl("/api/workspace-prompts"),
    get:    (name, params)=> apiUrl(`/api/workspace-prompts/${enc(name)}`) + qs(params),
    update: (name, params)=> apiUrl(`/api/workspace-prompts/${enc(name)}`) + qs(params),
    remove: (name, params)=> apiUrl(`/api/workspace-prompts/${enc(name)}`) + qs(params),
  },

  /** Global server configuration. */
  config: {
    get:    (params) => apiUrl("/api/config" + qs(params)),
    update: () => apiUrl("/api/config"),
  },

  /** Agent discovery and metadata. */
  agents: {
    scan:    () => apiUrl("/api/agents/scan"),
    confirm: () => apiUrl("/api/agents/confirm"),
    types:   () => apiUrl("/api/agents/types"),
  },

  /** Auxiliary AI operations (improve-prompt, etc.). */
  aux: {
    improvePrompt: () => apiUrl("/api/aux/improve-prompt"),
  },

  /** Runner and infrastructure metadata. */
  runners: {
    supported: () => apiUrl("/api/supported-runners"),
    defaults:  () => apiUrl("/api/runner-defaults"),
  },

  /** Global WebSocket event stream. */
  events: {
    ws: () => wsUrl("/api/events"),
  },

  /** Miscellaneous / top-level utility endpoints. */
  misc: {
    advancedFlags:  () => apiUrl("/api/advanced-flags"),
    externalStatus: () => apiUrl("/api/external-status"),
    uiPreferences:  () => apiUrl("/api/ui-preferences"),
    csrfToken:      () => apiUrl("/api/csrf-token"),
    checkFileExists:(params) => apiUrl("/api/check-file-exists") + qs(params),
    saveFileToPath: () => apiUrl("/api/save-file-to-path"),
  },
};
