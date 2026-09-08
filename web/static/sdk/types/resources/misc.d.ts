/**
 * @param {import("../core/config.js").ResolvedConfig} config - resolved config
 * @param {object} configResource - the resource created by
 *   `createConfigResource(config)` (see resources/config.js)
 */
export function createMiscResource(config: import("../core/config.js").ResolvedConfig, configResource: object): {
    uiPreferences: {
        get: (opts: any) => Promise<any>;
        /** @param {object} prefs - see UIPreferences in ui_preferences.go */
        save: (prefs: object, opts: any) => Promise<any>;
    };
    /** GET /api/csrf-token. */
    csrfToken: (opts: any) => Promise<any>;
    /** GET /api/auth-info — pre-auth: reports which auth method(s) are
     *  configured so the login page can adapt its UI.
     *  @returns {Promise<{simple: boolean, cloudflare: boolean}>} */
    authInfo: (opts: any) => Promise<{
        simple: boolean;
        cloudflare: boolean;
    }>;
    /** POST /api/login — pre-auth, CSRF-exempt server-side. A 401 (bad
     *  credentials) is the expected failure path, not an auth error to
     *  redirect on — callers must NOT wire this through a CSRF-adapter
     *  client whose onUnauthorized redirects to the login page itself.
     *  @param {{username: string, password: string}} credentials */
    login: (credentials: {
        username: string;
        password: string;
    }, opts: any) => Promise<any>;
    /** GET /api/check-file-exists?path= — localhost-only server-side; a
     *  403 from the external listener surfaces as `MittoApiError`.
     *  @param {string} path - absolute file path
     *  @returns {Promise<{exists: boolean}>} */
    checkFileExists: (path: string, opts: any) => Promise<{
        exists: boolean;
    }>;
    /** POST /api/save-file-to-path — localhost-only server-side.
     *  @param {string} path - absolute file path
     *  @param {string} content */
    saveFileToPath: (path: string, content: string, opts: any) => Promise<any>;
    /** POST /api/aux/improve-prompt. Retries one canonical 503 unavailable
     *  response while the auxiliary session warms up.
     *  @param {string} prompt
     *  @param {string} workspaceUUID
     *  @returns {Promise<{improved_prompt: string}>} */
    improvePrompt: (prompt: string, workspaceUUID: string, opts: any) => Promise<{
        improved_prompt: string;
    }>;
    /** POST /api/badge-click — localhost-only server-side (native macOS app).
     *  Executes a configured OpenTarget's shell command for the given
     *  workspace. Rejected with 403 from a non-loopback client.
     *  @param {object} body - {workspace_path, action: "open", target_id} */
    badgeClick: (body: object, opts: any) => Promise<any>;
    /** Folder-native sidebar pin flag (folders.json), scoped by `working_dir`
     *  (not a workspace uuid — a folder may hold several workspaces). */
    folderPin: {
        /** GET /api/folders/pin?working_dir=...
         *  @param {object} params - {working_dir}
         *  @returns {Promise<{pinned: boolean}>} */
        get: (params: object, opts: any) => Promise<{
            pinned: boolean;
        }>;
        /** PUT /api/folders/pin?working_dir=...
         *  @param {object} params - {working_dir}
         *  @param {object} body - {pinned}
         *  @returns {Promise<{pinned: boolean}>} */
        set: (params: object, body: object, opts: any) => Promise<{
            pinned: boolean;
        }>;
    };
    advancedFlags: any;
    externalStatus: any;
    supportedRunners: any;
    runnerDefaults: any;
    webauthn: {
        /** POST /api/webauthn/register/begin — starts a registration
         *  ceremony; returns PublicKeyCredentialCreationOptions JSON. */
        registerBegin: (opts: any) => Promise<any>;
        /** POST /api/webauthn/register/finish — completes the ceremony.
         *  @param {object} credential - serialized PublicKeyCredential (see
         *    utils/webauthn.js's serializeCreatedCredential) */
        registerFinish: (credential: object, opts: any) => Promise<any>;
        /** GET /api/webauthn/register/list.
         *  @returns {Promise<Array<{id: string, created_at: string, last_used_at: string}>>} */
        list: (opts: any) => Promise<Array<{
            id: string;
            created_at: string;
            last_used_at: string;
        }>>;
        /** DELETE /api/webauthn/register/{id} — id is the base64url credential id.
         *  @param {string} id */
        delete: (id: string, opts: any) => Promise<any>;
        /** POST /api/webauthn/login/begin — pre-auth, CSRF-exempt; starts a
         *  discoverable login ceremony. Sets an HttpOnly ceremony cookie, so
         *  callers must use a client whose fetch sends credentials
         *  same-origin (both auth.js's noAuth client and getSdkClient()
         *  qualify). Returns PublicKeyCredentialRequestOptions JSON. */
        loginBegin: (opts: any) => Promise<any>;
        /** POST /api/webauthn/login/finish — pre-auth, CSRF-exempt; completes
         *  the ceremony and mints the same mitto_session cookie password
         *  login issues.
         *  @param {object} assertion - serialized PublicKeyCredential (see
         *    utils/webauthn.js's serializeAssertion) */
        loginFinish: (assertion: object, opts: any) => Promise<any>;
    };
};
