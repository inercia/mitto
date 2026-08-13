/**
 * Mitto JavaScript client SDK — public entrypoint.
 *
 * This is the ONLY supported import surface (docs/devel/js-client-library.md
 * §5). Everything under `sdk/core/`, `sdk/env/`, etc. is a deep import and
 * may change without notice in any release.
 */
import { resolveConfig } from "./core/config.js";
import { createEndpoints } from "./core/endpoints.js";
import {
  MittoError,
  ConfigError,
  MittoApiError,
  MittoAuthError,
  MittoNetworkError,
} from "./core/errors.js";
import { createSessionStream } from "./realtime/session-stream.js";
import { createEventsStream } from "./realtime/events-stream.js";
import {
  EVENTS,
  COMMANDS,
  LEGACY_EVENTS,
  isKnownEventType,
  isCommandType,
} from "./realtime/events.js";
import {
  createSeqTracker,
  isSeqDuplicate,
  markSeqSeen,
  getMaxSeq,
  isStaleClientState,
  isTerminalSessionError,
  createMemorySeqStore,
  createStorageSeqStore,
} from "./realtime/seq.js";
import {
  generatePromptId,
  createMemoryPendingPromptStore,
  createStoragePendingPromptStore,
} from "./realtime/pending-prompts.js";
import { noneAuth, sharedTokenAuth, browserCookieAuth } from "./auth/index.js";
import { createSessionsResource } from "./resources/sessions.js";
import { createPromptsResource } from "./resources/prompts.js";
import { createProcessorsResource } from "./resources/processors.js";
import { createShortcutsResource } from "./resources/shortcuts.js";
import { createTaskLabelColorsResource } from "./resources/task-label-colors.js";
import { createConfigResource } from "./resources/config.js";
import { createIssuesResource, withIssueCaches } from "./resources/issues.js";
import { createFilesResource } from "./resources/files.js";
import { createDashboardResource } from "./resources/dashboard.js";
import { createMiscResource } from "./resources/misc.js";
import { createWorkspacesResource } from "./resources/workspaces.js";
import { createAcpServersResource } from "./resources/acp-servers.js";
import { createAgentsResource } from "./resources/agents.js";
import { createTtlCache, keyForParams } from "./cache/ttl-cache.js";

/**
 * The embedded copy ships lockstep with the server (§6): its version is the
 * Mitto release tag it is served inside.
 */
export const VERSION = "0.3.0";

/**
 * Creates a Mitto API client from environment-agnostic, injectable config.
 * See docs/devel/js-client-library.md §4 for the full contract.
 */
export function createClient(options = {}) {
  const config = resolveConfig(options);
  const sessions = createSessionsResource(config);
  const serverConfig = createConfigResource(config);
  return {
    config,
    // `wsBaseUrl` is an optional createClient() option (see core/config.js)
    // for hosts whose `baseUrl` is relative and need an absolute ws(s)://
    // origin for WebSocket builders (e.g. a same-origin browser client —
    // see utils/sdkClient.js's getSdkWsBaseUrl()). Deep-import
    // `createEndpoints(config, { wsBaseUrl })` directly only if a caller
    // needs a *different* wsBaseUrl than the one already on `config`.
    endpoints: createEndpoints(config, { wsBaseUrl: config.wsBaseUrl }),
    sessions,
    prompts: createPromptsResource(config),
    processors: createProcessorsResource(config),
    shortcuts: createShortcutsResource(config),
    taskLabelColors: createTaskLabelColorsResource(config),
    issues: createIssuesResource(config),
    // Named `serverConfig`, not `config`, because `client.config` is already
    // the resolved internal SDK config object (see `config` above and
    // utils/sdkClient.js's `getSdkClient().config.storage` call site).
    serverConfig,
    files: createFilesResource(config),
    // Thin alias, not a new module (mitto-7gta.12 plan, decision 1): avoids
    // a duplicate implementation of the session-scoped images surface
    // already built in resources/sessions.js (mitto-7gta.7).
    images: sessions.images,
    dashboard: createDashboardResource(config),
    // Delegates its discovery methods to `serverConfig` (mitto-7gta.10) —
    // same function objects, see resources/misc.js's header comment.
    misc: createMiscResource(config, serverConfig),
    workspaces: createWorkspacesResource(config),
    acpServers: createAcpServersResource(config),
    agents: createAgentsResource(config),
    sessionStream: (sessionId, streamOptions) =>
      createSessionStream(config, sessionId, streamOptions),
    eventsStream: (streamOptions) => createEventsStream(config, streamOptions),
  };
}

export { MittoError, ConfigError, MittoApiError, MittoAuthError, MittoNetworkError };
export { browserEnv, browserCookieReader } from "./env/browser.js";
export { noneAuth, sharedTokenAuth, browserCookieAuth };
export { createSessionStream, SessionStream } from "./realtime/session-stream.js";
export { createEventsStream, EventsStream } from "./realtime/events-stream.js";
export {
  createSeqTracker,
  isSeqDuplicate,
  markSeqSeen,
  getMaxSeq,
  isStaleClientState,
  isTerminalSessionError,
  createMemorySeqStore,
  createStorageSeqStore,
};
export { generatePromptId, createMemoryPendingPromptStore, createStoragePendingPromptStore };
export { EVENTS, COMMANDS, LEGACY_EVENTS, isKnownEventType, isCommandType };
export { createTtlCache, keyForParams };
export { withIssueCaches };
