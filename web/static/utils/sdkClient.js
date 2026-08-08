// Mitto Web Interface — SDK client seam (mitto-7gta.18, slice S1)
//
// Lazily-created singleton Mitto JS SDK client (web/static/sdk/index.js) for
// the browser UI. This is the seam every later .18 slice (S2 EventsStream,
// S3 SessionStream, S4 delivery verification) will build on; it makes no
// call-site changes itself.
//
// Wiring:
//   - storage/logger: browserEnv() (localStorage + console, see sdk/env/browser.js)
//   - apiPrefix: getApiPrefix() (window.mittoApiPrefix, server-injected)
//   - auth: browserCookieAuth(), the same double-submit-cookie adapter
//     utils/csrf.js already shims — never a second CSRF implementation.
//   - baseUrl is left "" (relative): REST calls resolve against the current
//     origin exactly like utils/api.js's apiUrl(). WebSocket callers (S2/S3)
//     must pass an explicit `wsBaseUrl` (see getSdkWsBaseUrl() below) since
//     wsUrlFor() cannot derive a ws(s):// scheme from a relative baseUrl.
//
// The seq-watermark store is keyed identically to utils/storage.js's
// getLastSeenSeq/setLastSeenSeq ("mitto_last_seen_seq_<sessionId>") so
// existing watermarks are read/written by both today (during the migration)
// and by the SDK alone once S3/S5 land — no data migration step needed.
import {
  createClient,
  browserEnv,
  browserCookieAuth,
  browserCookieReader,
  createStorageSeqStore,
  createStoragePendingPromptStore,
} from "../sdk/index.js";
import { getApiPrefix } from "./api.js";
import { endpoints } from "./endpoints.js";

// Matches utils/storage.js's getLastSeenSeq/setLastSeenSeq key exactly.
const SEQ_STORE_KEY_PREFIX = "mitto_last_seen_seq_";

let _client = null;

/**
 * Returns the process-wide Mitto SDK client, creating it on first call.
 * Lazy because `getApiPrefix()` reads `window.mittoApiPrefix`, which the
 * server injects into the page and may not be set yet at module-eval time.
 * @returns {ReturnType<typeof createClient>}
 */
export function getSdkClient() {
  if (_client) return _client;
  _client = createClient({
    ...browserEnv(),
    apiPrefix: getApiPrefix(),
    auth: browserCookieAuth({
      getCookie: browserCookieReader(),
      // Late-bound (mirrors utils/csrf.js): a bare `fetch` identifier would
      // snapshot the binding at import time, so a test replacing
      // globalThis.fetch afterward would be silently ignored.
      fetch: (...args) => fetch(...args),
      csrfTokenUrl: endpoints.misc.csrfToken(),
    }),
  });
  return _client;
}

/**
 * Absolute ws(s):// origin for the current page, for SDK realtime streams'
 * `wsBaseUrl` option (wsUrlFor() cannot derive a scheme from a relative
 * baseUrl). Mirrors utils/api.js's wsUrl() scheme mapping.
 * @returns {string} e.g. "wss://host:1234"
 */
export function getSdkWsBaseUrl() {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}`;
}

/**
 * A seq-watermark store backed by the SDK client's storage adapter, keyed
 * identically to utils/storage.js's getLastSeenSeq/setLastSeenSeq.
 * @param {object} [options] - forwarded to createStorageSeqStore (e.g. `now`).
 */
export function createSdkSeqStore(options = {}) {
  return createStorageSeqStore(getSdkClient().config.storage, {
    keyPrefix: SEQ_STORE_KEY_PREFIX,
    ...options,
  });
}

/**
 * A pending-prompt delivery-verification store backed by the SDK client's
 * storage adapter. Uses the SDK's own default storage key
 * ("mitto_sdk_pending_prompts"), deliberately distinct from lib.js's legacy
 * "mitto_pending_prompts" until S4 switches sendPrompt() over to it — see
 * sdk/realtime/pending-prompts.js.
 * @param {object} [options] - forwarded to createStoragePendingPromptStore.
 */
export function createSdkPendingPromptStore(options = {}) {
  return createStoragePendingPromptStore(getSdkClient().config.storage, options);
}

/**
 * Test-only: clears the cached singleton so the next getSdkClient() call
 * re-reads getApiPrefix() and rebuilds the client from scratch. Not part of
 * the module's runtime behavior — the app never needs to rebuild the client.
 */
export function _resetSdkClientForTests() {
  _client = null;
}
