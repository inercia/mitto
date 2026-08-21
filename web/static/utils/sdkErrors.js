// Mitto Web Interface — SDK error adapter (mitto-7gta.17 slice S0)
//
// Small seam that lets call sites migrated from authFetch/secureFetch (which
// returned a raw Response and required a manual `res.ok` / `res.status`
// check) onto the SDK client (web/static/sdk/index.js, which throws a typed
// MittoApiError/MittoAuthError/MittoNetworkError on failure per
// sdk/core/errors.js) be rewritten mechanically, without re-deriving the
// existing error-message/beads-error-shape precedence at every call site.
//
// This file intentionally duplicates no logic: it only reads the fields the
// SDK error classes already populate (status/code/details/message).

/**
 * Returns the HTTP status of a thrown SDK error, or undefined for errors that
 * never reached the server (e.g. MittoNetworkError, or a plain Error thrown
 * by something else). Mirrors the `res.status` check call sites previously
 * did directly on the fetch Response.
 * @param {*} err
 * @returns {number|undefined}
 */
export function errorStatus(err) {
  return typeof err?.status === "number" ? err.status : undefined;
}

/**
 * Returns true when `err` is a 404 MittoApiError — the common "this record
 * was deleted externally" branch several call sites (e.g. BeadsView.js) special-case.
 * @param {*} err
 * @returns {boolean}
 */
export function isNotFoundError(err) {
  return errorStatus(err) === 404;
}

/**
 * Extracts a human-readable error message from a thrown SDK error, falling
 * back to `fallback` for errors without one (e.g. a MittoNetworkError with an
 * empty message, or a non-SDK error). Mirrors utils/api.js's
 * errorMessageFromData() precedence, but operates on the already-thrown
 * error object rather than a raw parsed response body, since
 * sdk/core/errors.js's errorFromResponse() already applied that precedence
 * once when building `err.message`.
 * @param {*} err
 * @param {string} fallback
 * @returns {string}
 */
export function errorMessage(err, fallback) {
  return (err && err.message) || fallback;
}

/**
 * Adapts a thrown SDK error into the flat `{error, code, stderr, details}`
 * shape utils/beads.js's readBeadsResponse() produces from a raw beads-API
 * Response, so beads call sites that branch on `data.error` /
 * isBeadsSchemaSkew(data) / toSchemaSkewState(data) keep working unchanged
 * after switching from authFetch/secureFetch + readBeadsResponse to
 * client.issues.*.
 * @param {*} err
 * @param {string} [fallback] - Used when the error carries no message.
 * @returns {{error: string, code: string|undefined, stderr: string|undefined, details: *}}
 */
export function beadsErrorFrom(err, fallback = "Request failed") {
  return {
    error: errorMessage(err, fallback),
    code: err?.code,
    stderr: err?.details?.stderr,
    details: err?.details,
  };
}
