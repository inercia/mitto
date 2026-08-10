/**
 * Centralized API endpoint registry — SDK-native, environment-agnostic
 * (mitto-7gta.6). Same forbidden-globals rule as the rest of `sdk/core/`
 * (no `window`, `document`, `location` — see config.js's header).
 *
 * `createEndpoints(config, options)` builds the registry from an injected,
 * already-resolved client config (see core/config.js) rather than reading
 * ambient globals. REST builders resolve via `buildUrl()` (this module's
 * `qs()` dependency, ported byte-identical from the legacy
 * utils/endpoints.js). WebSocket builders delegate to `wsUrlFor()`, which
 * cannot derive a ws(s):// scheme from a relative/empty `config.baseUrl` —
 * pass an absolute `options.wsBaseUrl` (e.g. "ws://host:1234") when
 * `config.baseUrl` is relative (the common case for a same-origin browser
 * client; see utils/endpoints.js's shim for how it supplies one).
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — not re-exported from sdk/index.js
 * directly; `createClient()` exposes it as `client.endpoints`.
 *
 * Usage:
 *   const endpoints = createEndpoints(config, { wsBaseUrl: "wss://host" });
 *   const url = endpoints.sessions.queue(id);
 *   const url = endpoints.issues.list({ working_dir });
 */
import { buildUrl } from "./transport.js";
import { wsUrlFor } from "../realtime/ws-transport.js";

const enc = encodeURIComponent;

/**
 * Builds the endpoint registry for a given resolved config.
 * @param {object} config - resolved config (see core/config.js)
 * @param {object} [options]
 * @param {string} [options.wsBaseUrl] - absolute ws(s):// or http(s):// origin
 *   used to derive WebSocket URLs when `config.baseUrl` is relative/empty.
 * @returns {object} the endpoint registry
 */
export function createEndpoints(config, options = {}) {
  const url = (path, query) => buildUrl(config, path, query);
  const ws = (path, label) =>
    wsUrlFor(config, path, { wsBaseUrl: options.wsBaseUrl }, label);

  return {
    /** Beads issue tracker — all migrated to /api/issues (Decision #12). */
    issues: {
      list: (params) => url("/api/issues", params),
      stats: (params) => url("/api/issues/stats", params),
      show: (id, params) => url(`/api/issues/${enc(id)}`, params),
      create: (params) => url("/api/issues", params),
      update: (id, params) => url(`/api/issues/${enc(id)}`, params),
      remove: (id, params) => url(`/api/issues/${enc(id)}`, params),
      status: (id, params) => url(`/api/issues/${enc(id)}/status`, params),
      comments: (id, params) => url(`/api/issues/${enc(id)}/comments`, params),
      dependencies: (id, params) =>
        url(`/api/issues/${enc(id)}/dependencies`, params),
      labels: (id, params) => url(`/api/issues/${enc(id)}/labels`, params),
      labelsAll: (params) => url("/api/issues/labels", params),
      cleanup: (params) => url("/api/issues/cleanup", params),
      config: (params) => url("/api/issues/config", params),
      upstream: (params) => url("/api/issues/upstream", params),
      sync: (params) => url("/api/issues/sync", params),
    },

    /** Beads database operations (schema migration, adopt, etc.). */
    beads: {
      migrate: () => url("/api/beads/migrate"),
    },

    /** Session lifecycle and sub-resources. */
    sessions: {
      list: () => url("/api/sessions"),
      running: () => url("/api/sessions/running"),
      get: (id) => url(`/api/sessions/${enc(id)}`),
      create: () => url("/api/sessions"),
      update: (id) => url(`/api/sessions/${enc(id)}`),
      remove: (id) => url(`/api/sessions/${enc(id)}`),
      events: (id, params) => url(`/api/sessions/${enc(id)}/events`, params),
      ws: (id) => ws(`/api/sessions/${enc(id)}/ws`, "endpoints.sessions.ws"),
      changes: (id) => url(`/api/sessions/${enc(id)}/changes`),
      settings: (id) => url(`/api/sessions/${enc(id)}/settings`),
      prune: (id) => url(`/api/sessions/${enc(id)}/prune`),
      loop: (id) => url(`/api/sessions/${enc(id)}/loop`),
      loopRunNow: (id) => url(`/api/sessions/${enc(id)}/loop/run-now`),
      loopRestore: (id) => url(`/api/sessions/${enc(id)}/loop/restore`),
      loopSuggestFromRecent: (id) =>
        url(`/api/sessions/${enc(id)}/loop/suggest-from-recent`),
      loopAcknowledgeStoppedReason: (id) =>
        url(`/api/sessions/${enc(id)}/loop/acknowledge-stopped-reason`),
      uiPromptAcknowledge: (id) =>
        url(`/api/sessions/${enc(id)}/ui-prompt/acknowledge`),
      flush: (id) => url(`/api/sessions/${enc(id)}/flush`),
      callback: (id) => url(`/api/sessions/${enc(id)}/callback`),
      userData: (id) => url(`/api/sessions/${enc(id)}/user-data`),
      promptArgCache: (id, promptName) =>
        url(`/api/sessions/${enc(id)}/prompt-arg-cache`, { prompt: promptName }),
      queue: (id) => url(`/api/sessions/${enc(id)}/queue`),
      queueMsg: (id, msgId) => url(`/api/sessions/${enc(id)}/queue/${enc(msgId)}`),
      queueMove: (id, msgId) =>
        url(`/api/sessions/${enc(id)}/queue/${enc(msgId)}/move`),
      images: (id) => url(`/api/sessions/${enc(id)}/images`),
      image: (id, imageId) => url(`/api/sessions/${enc(id)}/images/${enc(imageId)}`),
      imagesFromPath: (id) => url(`/api/sessions/${enc(id)}/images/from-path`),
      files: (id) => url(`/api/sessions/${enc(id)}/files`),
      filesFromPath: (id) => url(`/api/sessions/${enc(id)}/files/from-path`),
    },

    /** Workspaces and their sub-resources. */
    workspaces: {
      list: (params) => url("/api/workspaces", params),
      create: () => url("/api/workspaces"),
      effectiveRunnerConfig: (uuid) =>
        url(`/api/workspaces/${enc(uuid)}/effective-runner-config`),
      metadata: (uuid) => url(`/api/workspaces/${enc(uuid)}/metadata`),
      userDataSchema: (uuid) => url(`/api/workspaces/${enc(uuid)}/user-data-schema`),
      mcpTools: (uuid, params) => url(`/api/workspaces/${enc(uuid)}/mcp-tools`, params),
      mcpToolsInstall: (uuid) => url(`/api/workspaces/${enc(uuid)}/mcp-tools/install`),
      mcpToolsRemove: (uuid) => url(`/api/workspaces/${enc(uuid)}/mcp-tools/remove`),
      restartAcp: (uuid) => url(`/api/workspaces/${enc(uuid)}/restart-acp`),
      acpStatus: (uuid) => url(`/api/workspaces/${enc(uuid)}/acp-status`),
      processors: (uuid) => url(`/api/workspaces/${enc(uuid)}/processors`),
      processor: (uuid, name) =>
        url(`/api/workspaces/${enc(uuid)}/processors/${enc(name)}`),
      processorArguments: (uuid, name) =>
        url(`/api/workspaces/${enc(uuid)}/processors/${enc(name)}/arguments`),
    },

    /** Workspace-scoped prompt management. */
    workspacePrompts: {
      list: (params) => url("/api/workspace-prompts", params),
      create: () => url("/api/workspace-prompts"),
      get: (name, params) => url(`/api/workspace-prompts/${enc(name)}`, params),
      update: (name, params) => url(`/api/workspace-prompts/${enc(name)}`, params),
      remove: (name, params) => url(`/api/workspace-prompts/${enc(name)}`, params),
      // mitto-x8v, mitto-47y.6.2: per-argument "remember last value" for prompt
      // dialogs. sessionId is optional; when provided the server merges
      // conversation-scoped values on top of folder-scoped values.
      rememberedArgs: (workingDir, promptName, sessionId) =>
        url("/api/workspace-prompts/remembered-args", {
          working_dir: workingDir,
          prompt: promptName,
          session_id: sessionId,
        }),
    },

    /** Workspace file listing — feeds the "filename" prompt parameter type. */
    workspaceFiles: {
      list: (params) => url("/api/workspace-files", params),
    },

    /** Workspace directory listing — feeds the "dirname" prompt parameter type. */
    workspaceDirs: {
      list: (params) => url("/api/workspace-dirs", params),
    },

    /** Folder-level settings (stored in folders.json, per-user). */
    folders: {
      shortcuts: (params) => url("/api/folders/shortcuts", params),
      pin: (params) => url("/api/folders/pin", params),
    },

    /** Global settings (stored in settings.json). */
    global: {
      // Pass { include_prompts: true } to also receive the merged global prompts
      // list (~750 KB) needed by the shortcuts editor. Read-only callers that
      // only render existing sections must omit it — see mitto-r4t0.
      shortcuts: (params) => url("/api/global/shortcuts", params),
    },

    /** Global server configuration. */
    config: {
      get: (params) => url("/api/config", params),
      update: () => url("/api/config"),
    },

    /** Agent discovery and metadata. */
    agents: {
      scan: () => url("/api/agents/scan"),
      confirm: () => url("/api/agents/confirm"),
      types: () => url("/api/agents/types"),
    },

    /** ACP server lifecycle operations (delete flow requires guided reassign). */
    acpServers: {
      prepareDelete: (name) => url(`/api/acp-servers/${enc(name)}/prepare-delete`),
      reassignAndDelete: (name) =>
        url(`/api/acp-servers/${enc(name)}/reassign-and-delete`),
    },

    /** Auxiliary AI operations (improve-prompt, etc.). */
    aux: {
      improvePrompt: () => url("/api/aux/improve-prompt"),
    },

    /** Runner and infrastructure metadata. */
    runners: {
      supported: () => url("/api/supported-runners"),
      defaults: () => url("/api/runner-defaults"),
    },

    /** Global WebSocket event stream. */
    events: {
      ws: () => ws("/api/events", "endpoints.events.ws"),
    },

    /** Miscellaneous / top-level utility endpoints. */
    misc: {
      advancedFlags: () => url("/api/advanced-flags"),
      externalStatus: () => url("/api/external-status"),
      uiPreferences: () => url("/api/ui-preferences"),
      csrfToken: () => url("/api/csrf-token"),
      checkFileExists: (params) => url("/api/check-file-exists", params),
      saveFileToPath: () => url("/api/save-file-to-path"),
      dashboard: (params) => url("/api/dashboard", params),
      dashboardTimeseries: (params) => url("/api/dashboard/timeseries", params),
      // Pre-auth endpoints (mitto-7gta.19.1): used by auth.js, which runs
      // before any session/cookie state exists.
      authInfo: () => url("/api/auth-info"),
      login: () => url("/api/login"),
    },
  };
}
