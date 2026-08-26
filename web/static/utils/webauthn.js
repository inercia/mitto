// Native WebAuthn (passkey) helpers — no external JS dependency (mitto-4mz.6).
//
// Shared by web/static/auth.js (login page, pre-auth) and
// components/SettingsDialog.js (authenticated "Manage passkeys" section).
// Wraps base64url<->ArrayBuffer conversion and the ceremony option/result
// (de)serialization the browser's `navigator.credentials.create()/get()`
// APIs need, matching the standard W3C JSON shape go-webauthn's Go handlers
// (webauthn_register.go, webauthn_login.go) produce/consume directly from
// the request/response bodies.

/** True when this page can perform a WebAuthn ceremony at all: a secure
 *  context (https or localhost) and a browser implementing the API. */
export function isWebAuthnSupported() {
  return (
    typeof window !== "undefined" &&
    window.isSecureContext === true &&
    typeof window.PublicKeyCredential !== "undefined"
  );
}

/**
 * True when the browser supports Conditional Create (mitto-4mz.7): silently
 * offering `navigator.credentials.create({publicKey, mediation:"conditional"})`
 * without a modal prompt, so a passkey can be auto-enrolled right after a
 * password sign-in. Feature-guarded: browsers without the
 * `PublicKeyCredential.getClientCapabilities()` static (or that report
 * `conditionalCreate` as anything but `true`) resolve to `false` so callers
 * can silently skip and fall back to an explicit "Create a passkey" button.
 * @returns {Promise<boolean>}
 */
export async function supportsConditionalCreate() {
  if (!isWebAuthnSupported()) return false;
  if (typeof window.PublicKeyCredential.getClientCapabilities !== "function") {
    return false;
  }
  try {
    const capabilities = await window.PublicKeyCredential.getClientCapabilities();
    return capabilities?.conditionalCreate === true;
  } catch (_err) {
    return false;
  }
}

/**
 * True when the browser supports Conditional Mediation (passkey autofill,
 * mitto-ykm): silently offering
 * `navigator.credentials.get({publicKey, mediation:"conditional"})` so a
 * matching passkey surfaces as an inline autofill suggestion in the username
 * field, with no modal prompt. Feature-guarded: browsers without the
 * `PublicKeyCredential.isConditionalMediationAvailable()` static resolve to
 * `false` so callers can fall back to an explicit passkey button.
 * @returns {Promise<boolean>}
 */
export async function isConditionalMediationAvailable() {
  if (!isWebAuthnSupported()) return false;
  if (
    typeof window.PublicKeyCredential.isConditionalMediationAvailable !==
    "function"
  ) {
    return false;
  }
  try {
    return await window.PublicKeyCredential.isConditionalMediationAvailable();
  } catch (_err) {
    return false;
  }
}

/** Decodes a base64url string (no padding) into an ArrayBuffer. */
export function base64urlToBuffer(value) {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const pad = padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
  const binary = atob(padded + pad);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes.buffer;
}

/** Encodes an ArrayBuffer/TypedArray into a base64url string (no padding). */
export function bufferToBase64url(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * Converts the server's PublicKeyCredentialCreationOptions JSON (as returned
 * by POST /api/webauthn/register/begin) into the shape
 * `navigator.credentials.create({publicKey})` expects, decoding the
 * base64url challenge/user.id/excludeCredentials[].id fields to ArrayBuffers.
 */
export function decodeCreationOptions(json) {
  const publicKey = { ...json.publicKey };
  publicKey.challenge = base64urlToBuffer(publicKey.challenge);
  publicKey.user = {
    ...publicKey.user,
    id: base64urlToBuffer(publicKey.user.id),
  };
  if (Array.isArray(publicKey.excludeCredentials)) {
    publicKey.excludeCredentials = publicKey.excludeCredentials.map((c) => ({
      ...c,
      id: base64urlToBuffer(c.id),
    }));
  }
  return publicKey;
}

/**
 * Converts the server's PublicKeyCredentialRequestOptions JSON (as returned
 * by POST /api/webauthn/login/begin) into the shape
 * `navigator.credentials.get({publicKey})` expects.
 */
export function decodeRequestOptions(json) {
  const publicKey = { ...json.publicKey };
  publicKey.challenge = base64urlToBuffer(publicKey.challenge);
  if (Array.isArray(publicKey.allowCredentials)) {
    publicKey.allowCredentials = publicKey.allowCredentials.map((c) => ({
      ...c,
      id: base64urlToBuffer(c.id),
    }));
  }
  return publicKey;
}

/**
 * Serializes a PublicKeyCredential returned by `create()` into the W3C JSON
 * shape go-webauthn's FinishRegistration parses directly from the request
 * body (POST /api/webauthn/register/finish).
 */
export function serializeCreatedCredential(credential) {
  const response = credential.response;
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      attestationObject: bufferToBase64url(response.attestationObject),
    },
  };
}

/**
 * Serializes a PublicKeyCredential returned by `get()` into the W3C JSON
 * shape go-webauthn's FinishPasskeyLogin parses directly from the request
 * body (POST /api/webauthn/login/finish or .../register handled elsewhere).
 */
export function serializeAssertion(credential) {
  const response = credential.response;
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      authenticatorData: bufferToBase64url(response.authenticatorData),
      signature: bufferToBase64url(response.signature),
      userHandle: response.userHandle
        ? bufferToBase64url(response.userHandle)
        : undefined,
    },
  };
}
