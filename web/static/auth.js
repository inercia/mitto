// Login form handler for Mitto auth page.
//
// Pre-auth: this page runs before any session/CSRF cookie exists, so it uses
// a minimal noneAuth SDK client (mitto-7gta.19.1) rather than
// utils/sdkClient.js's getSdkClient() — that seam wires a CSRF adapter whose
// onUnauthorized redirects to THIS page, which would be wrong here (a 401
// from /api/login is the expected "bad password" outcome, not a session
// expiry). Kept as its own module rather than importing sdkClient.js,
// preserving the pre-auth/authenticated boundary.
import { createClient, browserEnv, MittoApiError, MittoNetworkError } from "./sdk/index.js";
import {
  isWebAuthnSupported,
  decodeRequestOptions,
  serializeAssertion,
} from "./utils/webauthn.js";

function getApiPrefix() {
  return window.mittoApiPrefix || "";
}

function initAuthPage() {
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

  // Fetch auth info to adapt the UI before showing the form
  client.misc
    .authInfo()
    .then(function (info) {
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

      if (info.passkey && isWebAuthnSupported() && passkeyBtn) {
        passkeyBtn.style.display = "";
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
