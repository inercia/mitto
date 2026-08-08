/**
 * SDK auth adapters (mitto-7gta.5) — three implementations of the interface
 * `core/transport.js` and `realtime/session-stream.js` consume:
 *
 *   authorize({ method, url, headers }) -> Promise<{ headers?, credentials? }>
 *   authorizeWebSocket({ url })         -> Promise<{ protocols?, options? }>  (optional)
 *   onUnauthorized(error)               -> void                              (optional)
 *
 * `authorizeWebSocket`/`onUnauthorized` are optional so a minimal adapter
 * can omit them; callers use optional chaining (`config.auth.onUnauthorized?.(...)`).
 *
 * See docs/devel/js-client-library.md §4 for the full contract.
 */
export { noneAuth } from "./none.js";
export { sharedTokenAuth } from "./shared-token.js";
export { browserCookieAuth } from "./browser-cookie.js";
