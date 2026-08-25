/**
 * Misc REST resource module (mitto-7gta.12).
 *
 * `createMiscResource(config, configResource)` curries the SDK's `request()`
 * primitive (`core/transport.js`) with the resolved client `config`,
 * following the mitto-7gta.7/.10 precedent: raw relative paths mirroring
 * `internal/web/routes.go`, never built through `core/endpoints.js`.
 *
 * This is a deep import, not part of the public surface
 * (docs/devel/js-client-library.md §5) — `createClient()` exposes it as
 * `client.misc`.
 *
 * The discovery endpoints listed in this bead's description (advancedFlags,
 * externalStatus, supportedRunners, runnerDefaults) already exist on
 * `resources/config.js` (`client.serverConfig`, mitto-7gta.10) — they are
 * NOT reimplemented here. `createMiscResource` takes the already-constructed
 * config resource instance and delegates to its methods directly, so
 * `client.misc.advancedFlags === client.serverConfig.advancedFlags` (same
 * function object, no duplicate implementation).
 */
import { request } from "../core/transport.js";

/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 * @param {object} configResource - the resource created by
 *   `createConfigResource(config)` (see resources/config.js)
 */
export function createMiscResource(config, configResource) {
  const call = (method, path, opts = {}) =>
    request(config, { method, path, ...opts });

  return {
    uiPreferences: {
      get: (opts) => call("GET", "/api/ui-preferences", opts),
      /** @param {object} prefs - see UIPreferences in ui_preferences.go */
      save: (prefs, opts) =>
        call("PUT", "/api/ui-preferences", { body: prefs, ...opts }),
    },

    /** GET /api/csrf-token. */
    csrfToken: (opts) => call("GET", "/api/csrf-token", opts),

    /** GET /api/auth-info — pre-auth: reports which auth method(s) are
     *  configured so the login page can adapt its UI.
     *  @returns {Promise<{simple: boolean, cloudflare: boolean}>} */
    authInfo: (opts) => call("GET", "/api/auth-info", opts),

    /** POST /api/login — pre-auth, CSRF-exempt server-side. A 401 (bad
     *  credentials) is the expected failure path, not an auth error to
     *  redirect on — callers must NOT wire this through a CSRF-adapter
     *  client whose onUnauthorized redirects to the login page itself.
     *  @param {{username: string, password: string}} credentials */
    login: (credentials, opts) =>
      call("POST", "/api/login", { body: credentials, ...opts }),

    /** GET /api/check-file-exists?path= — localhost-only server-side; a
     *  403 from the external listener surfaces as `MittoApiError`.
     *  @param {string} path - absolute file path
     *  @returns {Promise<{exists: boolean}>} */
    checkFileExists: (path, opts) =>
      call("GET", "/api/check-file-exists", { query: { path }, ...opts }),

    /** POST /api/save-file-to-path — localhost-only server-side.
     *  @param {string} path - absolute file path
     *  @param {string} content */
    saveFileToPath: (path, content, opts) =>
      call("POST", "/api/save-file-to-path", {
        body: { path, content },
        ...opts,
      }),

    /** POST /api/aux/improve-prompt. Retries one canonical 503 unavailable
     *  response while the auxiliary session warms up.
     *  @param {string} prompt
     *  @param {string} workspaceUUID
     *  @returns {Promise<{improved_prompt: string}>} */
    improvePrompt: (prompt, workspaceUUID, opts) =>
      call("POST", "/api/aux/improve-prompt", {
        body: { prompt, workspace_uuid: workspaceUUID },
        retryUnavailable: true,
        ...opts,
      }),

    /** POST /api/badge-click — localhost-only server-side (native macOS app).
     *  Executes a configured OpenTarget's shell command for the given
     *  workspace. Rejected with 403 from a non-loopback client.
     *  @param {object} body - {workspace_path, action: "open", target_id} */
    badgeClick: (body, opts) =>
      call("POST", "/api/badge-click", { body, ...opts }),

    /** Folder-native sidebar pin flag (folders.json), scoped by `working_dir`
     *  (not a workspace uuid — a folder may hold several workspaces). */
    folderPin: {
      /** GET /api/folders/pin?working_dir=...
       *  @param {object} params - {working_dir}
       *  @returns {Promise<{pinned: boolean}>} */
      get: (params, opts) =>
        call("GET", "/api/folders/pin", { query: params, ...opts }),
      /** PUT /api/folders/pin?working_dir=...
       *  @param {object} params - {working_dir}
       *  @param {object} body - {pinned}
       *  @returns {Promise<{pinned: boolean}>} */
      set: (params, body, opts) =>
        call("PUT", "/api/folders/pin", { query: params, body, ...opts }),
    },

    // Delegated discovery endpoints (mitto-7gta.10, resources/config.js) —
    // same function objects, not reimplementations.
    advancedFlags: configResource.advancedFlags,
    externalStatus: configResource.externalStatus,
    supportedRunners: configResource.supportedRunners,
    runnerDefaults: configResource.runnerDefaults,

    // Passkey (WebAuthn) credential management (mitto-4mz.6). Registration
    // is authenticated (session cookie + CSRF); login is pre-auth (see
    // auth.js, which uses its own noAuth client instead of this one). All
    // return 404 when passkeys are not enabled/derivable server-side.
    webauthn: {
      /** POST /api/webauthn/register/begin — starts a registration
       *  ceremony; returns PublicKeyCredentialCreationOptions JSON. */
      registerBegin: (opts) =>
        call("POST", "/api/webauthn/register/begin", opts),
      /** POST /api/webauthn/register/finish — completes the ceremony.
       *  @param {object} credential - serialized PublicKeyCredential (see
       *    utils/webauthn.js's serializeCreatedCredential) */
      registerFinish: (credential, opts) =>
        call("POST", "/api/webauthn/register/finish", {
          body: credential,
          ...opts,
        }),
      /** GET /api/webauthn/register/list.
       *  @returns {Promise<Array<{id: string, created_at: string, last_used_at: string}>>} */
      list: (opts) => call("GET", "/api/webauthn/register/list", opts),
      /** DELETE /api/webauthn/register/{id} — id is the base64url credential id.
       *  @param {string} id */
      delete: (id, opts) =>
        call("DELETE", `/api/webauthn/register/${encodeURIComponent(id)}`, opts),
      /** POST /api/webauthn/login/begin — pre-auth, CSRF-exempt; starts a
       *  discoverable login ceremony. Sets an HttpOnly ceremony cookie, so
       *  callers must use a client whose fetch sends credentials
       *  same-origin (both auth.js's noAuth client and getSdkClient()
       *  qualify). Returns PublicKeyCredentialRequestOptions JSON. */
      loginBegin: (opts) => call("POST", "/api/webauthn/login/begin", opts),
      /** POST /api/webauthn/login/finish — pre-auth, CSRF-exempt; completes
       *  the ceremony and mints the same mitto_session cookie password
       *  login issues.
       *  @param {object} assertion - serialized PublicKeyCredential (see
       *    utils/webauthn.js's serializeAssertion) */
      loginFinish: (assertion, opts) =>
        call("POST", "/api/webauthn/login/finish", {
          body: assertion,
          ...opts,
        }),
    },
  };
}
