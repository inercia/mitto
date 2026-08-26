// Login form handler for Mitto auth page.
//
// Pre-auth: this page runs before any session/CSRF cookie exists, so it uses
// a minimal noneAuth SDK client (mitto-7gta.19.1) rather than
// utils/sdkClient.js's getSdkClient() — that seam wires a CSRF adapter whose
// onUnauthorized redirects to THIS page, which would be wrong here (a 401
// from /api/login is the expected "bad password" outcome, not a session
// expiry). Kept as its own module rather than importing sdkClient.js,
// preserving the pre-auth/authenticated boundary.
import {
  createClient,
  browserEnv,
  browserCookieAuth,
  browserCookieReader,
  MittoApiError,
  MittoNetworkError,
} from "./sdk/index.js";
import {
  isWebAuthnSupported,
  decodeRequestOptions,
  serializeAssertion,
  decodeCreationOptions,
  serializeCreatedCredential,
  supportsConditionalCreate,
  isConditionalMediationAvailable,
} from "./utils/webauthn.js";

// One-shot flag (mitto-4mz.7): set here right after a successful
// Conditional-Create auto-enroll, read+cleared by app.js on mount to show a
// non-intrusive success toast. sessionStorage (not localStorage) so it only
// survives the single redirect from this page to "/", not future sessions.
const PASSKEY_AUTOENROLLED_FLAG = "mitto_passkey_autoenrolled";

// AbortController for the background Conditional-Get ceremony (passkey
// autofill, mitto-ykm) started by startConditionalGet() on page load. Module
// scope so attemptConditionalCreate() can abort it before starting its own
// conditional mediation ceremony — only one may be pending at a time.
let conditionalGetController = null;

function getApiPrefix() {
  return window.mittoApiPrefix || "";
}

/**
 * Best-effort, silent passkey auto-enroll (mitto-4mz.7) run right after a
 * successful password sign-in, before redirecting to the main app. Gated on
 * the browser supporting Conditional Create; all failures are swallowed so a
 * slow/broken authenticator never blocks or delays the login redirect. The
 * ceremony mirrors SettingsDialog.createPasskey's explicit "Create a
 * passkey" flow, adding `mediation: "conditional"` so it never shows a
 * modal prompt.
 *
 * Registration (`/api/webauthn/register/begin|finish`) requires an
 * authenticated session AND a CSRF token, unlike this page's default
 * `noneAuth` client — so a separate CSRF-aware client is built here, reusing
 * the same `browserCookieAuth` adapter the authenticated app uses (see
 * utils/sdkClient.js), now that login() has just set the session cookie.
 *
 * Only one WebAuthn "conditional" mediation ceremony may be pending at a
 * time (mitto-ykm): the background Conditional-Get autofill started by
 * startConditionalGet() on page load must be aborted before this function's
 * own conditional `create()`, otherwise the browser rejects the new request
 * ("operation already pending"/AbortError).
 */
async function attemptConditionalCreate(client) {
  if (!(await supportsConditionalCreate())) return;

  // Abort the pending conditional get() (autofill), if any, before starting
  // a new conditional mediation ceremony — see the header comment above.
  if (conditionalGetController) {
    conditionalGetController.abort();
    conditionalGetController = null;
  }

  // Reuse `client`'s already-resolved fetch (see the module header comment:
  // this file must never reference the bare `fetch` identifier itself,
  // since it is off the SDK boundary allowlist).
  const csrfClient = createClient({
    ...browserEnv(),
    apiPrefix: getApiPrefix(),
    auth: browserCookieAuth({
      getCookie: browserCookieReader(),
      fetch: client.config.fetch,
      csrfTokenUrl: getApiPrefix() + "/api/csrf-token",
    }),
  });

  try {
    const options = await csrfClient.misc.webauthn.registerBegin();
    const publicKey = decodeCreationOptions(options);
    const credential = await navigator.credentials.create({
      publicKey,
      mediation: "conditional",
    });
    if (!credential) return; // no discoverable state to enroll from
    await csrfClient.misc.webauthn.registerFinish(
      serializeCreatedCredential(credential),
    );
    sessionStorage.setItem(PASSKEY_AUTOENROLLED_FLAG, "1");
  } catch (_err) {
    // Best-effort: silently skip on any failure (cancelled, unsupported,
    // not armed server-side, network error, etc). The explicit "Create a
    // passkey" button in Settings remains the fallback.
  }
}

function initAuthPage() {
  // Loopback/native-app guard (mitto-4mz): the backend bypasses auth entirely
  // for the internal 127.0.0.1 listener, so the login page must never be shown
  // there. WKWebView persists and restores its last-navigated URL across
  // relaunches, so once the app ever landed on /auth.html (e.g. from an old
  // pre-fix redirect) it would reopen straight to this login form and sit
  // there forever ("nothing happens"). The server injects
  // window.mittoIsExternal === false on the loopback page (true on the
  // external listener); an explicit false means "local app, auth bypassed" —
  // redirect back to the app instead of rendering the form.
  if (window.mittoIsExternal === false) {
    window.location.replace(getApiPrefix() + "/");
    return;
  }

  const form = document.getElementById("loginForm");
  const errorDiv = document.getElementById("error");
  const submitBtn = document.getElementById("submitBtn");
  const cloudflareMsg = document.getElementById("cloudflare-message");
  const passkeyBtn = document.getElementById("passkeyBtn");

  if (!form || !errorDiv || !submitBtn) {
    console.error("Required form elements not found");
    return;
  }

  // No `fetch` option: this file is off the SDK boundary allowlist
  // (mitto-7gta.19.1), so it must not reference the global `fetch`
  // identifier itself — resolveConfig() already falls back to
  // `globalThis.fetch` when none is injected (see sdk/core/config.js).
  const client = createClient({
    ...browserEnv(),
    apiPrefix: getApiPrefix(),
  });

  /**
   * Starts a background Conditional-Get ceremony (mitto-ykm): the browser
   * surfaces a matching passkey as an inline autofill suggestion in the
   * username field (no modal), and this promise only settles once the user
   * picks the suggestion or the ceremony is aborted/fails. Runs concurrently
   * with the password form, which stays fully usable while this is pending
   * — never awaited by the caller. Stores its AbortController at module
   * scope so attemptConditionalCreate() can abort it before its own
   * conditional create() (see that function's header comment).
   */
  async function startConditionalGet(client) {
    conditionalGetController = new AbortController();
    try {
      const options = await client.misc.webauthn.loginBegin();
      const publicKey = decodeRequestOptions(options);
      const assertion = await navigator.credentials.get({
        publicKey,
        mediation: "conditional",
        signal: conditionalGetController.signal,
      });
      await client.misc.webauthn.loginFinish(serializeAssertion(assertion));
      window.location.href = "/";
    } catch (err) {
      if (
        err &&
        (err.name === "AbortError" || err.name === "NotAllowedError")
      ) {
        // Aborted (a password login ran attemptConditionalCreate() instead)
        // or the user dismissed the autofill suggestion; not worth
        // alarming over.
        return;
      }
      if (err instanceof MittoApiError && err.status === 429) {
        errorDiv.textContent =
          err.message || "Too many attempts. Please try again later.";
        errorDiv.classList.remove("hidden");
      }
      // Other failures (network errors, unsupported browser, etc.) are
      // swallowed: the password form is the fallback and must not be
      // disrupted by a background ceremony the user never explicitly
      // started.
    }
  }

  // Fetch auth info to adapt the UI before showing the form
  client.misc
    .authInfo()
    .then(async function (info) {
      if (!info.simple && info.cloudflare) {
        // Only Cloudflare auth configured: hide login form, show message
        form.style.display = "none";
        if (cloudflareMsg) {
          cloudflareMsg.style.display = "";
        }
      } else if (!info.simple && !info.cloudflare) {
        // No auth configured: show a generic notice in the error div
        errorDiv.textContent = "No authentication method is configured.";
        errorDiv.classList.remove("hidden");
        form.style.display = "none";
      }
      // If info.simple is true, show the normal login form (default state)

      if (info.passkey && isWebAuthnSupported()) {
        if (await isConditionalMediationAvailable()) {
          // Conditional Mediation available: offer the passkey inline via
          // autofill in the background; no explicit button needed.
          startConditionalGet(client);
        } else if (passkeyBtn) {
          // Degradation fallback (mitto-ykm): no autofill entry point on
          // this browser, so keep the explicit passkey button.
          passkeyBtn.style.display = "";
        }
      }
    })
    .catch(function (err) {
      console.warn("Failed to fetch auth info:", err);
      // On error, keep the login form visible so the user can still try
    });

  if (passkeyBtn) {
    passkeyBtn.addEventListener("click", async function () {
      passkeyBtn.disabled = true;
      const originalText = passkeyBtn.textContent;
      passkeyBtn.textContent = "Waiting for passkey...";
      errorDiv.classList.add("hidden");

      try {
        const options = await client.misc.webauthn.loginBegin();
        const publicKey = decodeRequestOptions(options);
        const assertion = await navigator.credentials.get({ publicKey });
        await client.misc.webauthn.loginFinish(
          serializeAssertion(assertion),
        );
        window.location.href = "/";
      } catch (err) {
        if (err && err.name === "NotAllowedError") {
          // User cancelled the platform prompt; not an error worth alarming over.
        } else if (err instanceof MittoApiError) {
          if (err.status === 429) {
            errorDiv.textContent =
              err.message || "Too many attempts. Please try again later.";
          } else {
            errorDiv.textContent = err.message || "Passkey sign-in failed.";
          }
          errorDiv.classList.remove("hidden");
        } else if (err instanceof MittoNetworkError) {
          errorDiv.textContent = "Network error. Please try again.";
          errorDiv.classList.remove("hidden");
        } else {
          errorDiv.textContent = "Passkey sign-in failed.";
          errorDiv.classList.remove("hidden");
        }
      } finally {
        passkeyBtn.disabled = false;
        passkeyBtn.textContent = originalText;
      }
    });
  }

  form.addEventListener("submit", async function (e) {
    e.preventDefault();

    // Disable form during submission
    submitBtn.disabled = true;
    submitBtn.textContent = "Signing in...";
    errorDiv.classList.add("hidden");

    const username = document.getElementById("username").value;
    const password = document.getElementById("password").value;

    try {
      await client.misc.login({ username: username, password: password });
      // Best-effort passkey auto-enroll (mitto-4mz.7): awaited so the flag
      // is set before redirecting, but never allowed to block/fail the
      // login itself — attemptConditionalCreate() swallows all its own
      // errors and resolves immediately when unsupported.
      await attemptConditionalCreate(client);
      // Redirect to main app
      window.location.href = "/";
    } catch (err) {
      if (err instanceof MittoApiError) {
        // /api/login answers a flat {"error": "<message>"} body, which the
        // SDK surfaces as err.message (see core/errors.js's errorFromResponse).
        errorDiv.textContent = err.message || "Invalid username or password";
      } else if (err instanceof MittoNetworkError) {
        errorDiv.textContent = "Network error. Please try again.";
      } else {
        errorDiv.textContent = "Invalid username or password";
      }
      errorDiv.classList.remove("hidden");
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = "Sign In";
    }
  });
}

// A module script's execution can be deferred past DOMContentLoaded (unlike
// a classic script), so guard with an immediate-run fallback.
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", initAuthPage);
} else {
  initAuthPage();
}
