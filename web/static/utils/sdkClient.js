// Mitto Web Interface — SDK client seam (mitto-7gta.18 slice S1; extended by
// mitto-7gta.17 slice S0 for the REST call-site migration; extended by
// mitto-7gta.19.1 to absorb utils/csrf.js's remaining responsibilities and
// drop the utils/endpoints.js deep-import shim)
//
// Lazily-created singleton Mitto JS SDK client (web/static/sdk/index.js) for
// the browser UI. This is the seam every later .18 slice (S2 EventsStream,
// S3 SessionStream, S4 delivery verification) and every .17 REST call-site
// slice (S1-S8) builds on.
//
// Wiring:
//   - storage/logger: browserEnv() (localStorage + console, see sdk/env/browser.js)
//   - apiPrefix: getApiPrefix() (window.mittoApiPrefix, server-injected)
//   - fetch: late-bound (see comment above the option below) so a test
//     stubbing globalThis.fetch after import is always honored, not just at
//     first client construction.
//   - auth: browserCookieAuth(), the single double-submit-cookie adapter
//     instance for the whole app (mitto-7gta.19.1 folded in the duplicate
//     instance utils/csrf.js used to construct) — never a second CSRF
//     implementation.
//   - onUnauthorized: delegates to redirectToLogin() below, so every
//     migrated call site keeps the exact 401 -> redirect-to-login policy the
//     original authFetch/secureFetch enforced. One deliberate behavior
//     difference (mitto-7gta.17 plan, decision 2): sdk/core/transport.js
//     calls this hook and THEN throws a MittoAuthError, where authFetch
//     returned a never-resolving promise instead. Callers that migrate a 401
//     branch should expect the throw; the redirect itself still fires first.
//   - baseUrl is left "" (relative): REST calls resolve against the current
//     origin exactly like utils/api.js's apiUrl(). wsBaseUrl is set from
//     getSdkWsBaseUrl() below so `getSdkClient().endpoints`'s WebSocket
//     builders (and S2/S3 realtime streams) resolve without callers passing
//     it separately (wsUrlFor() cannot derive a ws(s):// scheme from a
//     relative baseUrl on its own).
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

// Matches utils/storage.js's getLastSeenSeq/setLastSeenSeq key exactly.
const SEQ_STORE_KEY_PREFIX = "mitto_last_seen_seq_";

const CSRF_HEADER_NAME = "X-CSRF-Token";

let _client = null;
// The browserCookieAuth() instance passed to createClient() as `auth`,
// captured so getCSRFToken()/initCSRF()/redirectToLogin() below can reuse
// its single-flight token-fetch state instead of building a second adapter
// (mitto-7gta.19.1: utils/csrf.js used to construct its own).
let _auth = null;

/**
 * Returns the process-wide Mitto SDK client, creating it on first call.
 * Lazy because `getApiPrefix()` reads `window.mittoApiPrefix`, which the
 * server injects into the page and may not be set yet at module-eval time.
 * @returns {ReturnType<typeof createClient>}
 */
export function getSdkClient() {
  if (_client) return _client;
  _auth = browserCookieAuth({
    getCookie: browserCookieReader(),
    // Late-bound: a bare `fetch` identifier would snapshot the binding at
    // client-construction time, so a test replacing globalThis.fetch
    // afterward would be silently ignored.
    fetch: (...args) => fetch(...args),
    csrfTokenUrl: getApiPrefix() + "/api/csrf-token",
    headerName: CSRF_HEADER_NAME,
  });
  _client = createClient({
    ...browserEnv(),
    apiPrefix: getApiPrefix(),
    wsBaseUrl: getSdkWsBaseUrl(),
    // Late-bound: a bare `fetch` identifier would snapshot the binding at
    // client-construction time (resolveConfig() reads it eagerly), so a test
    // replacing globalThis.fetch afterward would be silently ignored. Mirrors
    // the same late-binding already done for the auth adapter's `fetch`
    // option above.
    fetch: (...args) => fetch(...args),
    auth: _auth,
    // Preserves the authFetch/secureFetch 401 -> redirect-to-login policy for
    // every call site migrated onto this client (mitto-7gta.17). See the
    // module header comment for the one documented behavior delta (the SDK
    // still throws after redirecting).
    onUnauthorized: () => redirectToLogin(),
  });
  return _client;
}

/**
 * Get a valid CSRF token, fetching from the server if no cookie exists yet.
 * Reuses the SDK client's own auth adapter (single-flight fetch, see
 * sdk/auth/browser-cookie.js) rather than a second instance.
 * @returns {Promise<string>} The CSRF token
 */
export async function getCSRFToken() {
  getSdkClient(); // ensure _auth is constructed
  const patch = await _auth.authorize({ method: "POST" });
  return patch.headers[CSRF_HEADER_NAME];
}

/**
 * Redirect to the login page. Clears the CSRF adapter's in-flight token
 * fetch state (if any) before redirecting, e.g. on 401 (see onUnauthorized
 * above) or on explicit logout.
 *
 * Native/local-app guard (mitto-4mz): the backend bypasses auth entirely for
 * loopback (127.0.0.1) listeners, so the native macOS app / local browser
 * never has (nor needs) an External Access session. A 401 there is expected
 * for the few session-scoped endpoints that don't honor the loopback bypass
 * (e.g. /api/webauthn/register/list) — it must NEVER bounce the whole app to a
 * Sign In page. The server injects `window.mittoIsExternal = false` into the
 * loopback-served index.html (and `true` for the external listener), so we
 * treat an explicit `false` as "local app, redirect is never correct" and
 * make this a no-op. External access (true) and the test env (undefined) keep
 * the original redirect. The CSRF in-flight state is still cleared either way.
 */
export function redirectToLogin() {
  _auth?.onUnauthorized();
  if (window.mittoIsExternal === false) {
    // Local/native app: auth is bypassed on loopback; do not navigate away.
    return;
  }
  window.location.href = getApiPrefix() + "/auth.html";
}

/**
 * Initialize CSRF protection by ensuring a token cookie exists.
 * If no cookie is present, fetches one from the server.
 * Call this early in app initialization.
 * @returns {Promise<void>}
 */
export async function initCSRF() {
  try {
    // Just ensure we have a token - getCSRFToken will fetch if needed
    await getCSRFToken();
  } catch (err) {
    console.warn("Failed to initialize CSRF token:", err);
    // Don't throw - let individual requests handle failures
  }
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
  _auth = null;
}
