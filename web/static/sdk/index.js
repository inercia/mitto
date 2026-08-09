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
  return {
    config,
    // Deep-import `createEndpoints(config, { wsBaseUrl })` directly instead
    // when `config.baseUrl` is relative and a ws(s):// URL is needed (e.g.
    // a same-origin browser client) — this default has no wsBaseUrl.
    endpoints: createEndpoints(config),
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
