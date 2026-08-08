/**
 * SDK error taxonomy.
 *
 * Class names and `code` values below are pinned per the stability promise
 * in docs/devel/js-client-library.md §7 and must not change in a minor
 * release. `errorCodeForStatus` mirrors the HTTP-status → code table in
 * docs/devel/rest-api-conventions.md §4 (kept in sync with
 * `internal/web/handlers/helpers.go`'s `defaultCodeForStatus`).
 */

/**
 * Base class for every error the SDK itself throws. Lets callers do a
 * single `instanceof MittoError` check without enumerating every subtype.
 * Never thrown directly.
 */
export class MittoError extends Error {
  constructor(message, options) {
    super(message, options);
    this.name = "MittoError";
  }
}

/**
 * Thrown by `resolveConfig()` / `createClient()` when the caller-supplied
 * options are invalid: an unknown option key, or a required environment
 * capability (e.g. `fetch`) that could not be resolved from injected
 * options or the injected `globals`.
 */
export class ConfigError extends MittoError {
  constructor(message) {
    super(message);
    this.name = "ConfigError";
    this.code = "invalid_config";
  }
}

/**
 * Thrown when the server answered with a non-2xx HTTP status. Carries the
 * parsed error envelope fields so callers can branch on `code` without
 * string-matching `message`.
 */
export class MittoApiError extends MittoError {
  constructor(message, { status, code, details, body } = {}) {
    super(message);
    this.name = "MittoApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.body = body;
  }
}

/**
 * Specialization of `MittoApiError` for 401/403 responses, so callers that
 * only care about "the request failed" still catch it via `MittoApiError`,
 * while auth adapters can branch on it precisely (e.g. to trigger
 * `onUnauthorized`).
 */
export class MittoAuthError extends MittoApiError {
  constructor(message, options) {
    super(message, options);
    this.name = "MittoAuthError";
  }
}

/**
 * Thrown when the request never produced a response: `fetch` rejected,
 * the request was aborted, or a lower-level network failure (DNS/TLS/
 * offline) occurred. `cause` carries the original error when available.
 */
export class MittoNetworkError extends MittoError {
  constructor(message, { cause } = {}) {
    super(message, cause !== undefined ? { cause } : undefined);
    this.name = "MittoNetworkError";
    this.code = "network_error";
    this.cause = cause;
  }
}

/**
 * Maps an HTTP status code to the canonical error code, mirroring
 * `defaultCodeForStatus` in `internal/web/handlers/helpers.go` and the
 * table in docs/devel/rest-api-conventions.md §4. Used when the response
 * body doesn't carry an explicit `code` (e.g. a non-JSON error body).
 * @param {number} status
 * @returns {string}
 */
export function errorCodeForStatus(status) {
  switch (status) {
    case 400:
      return "bad_request";
    case 401:
      return "unauthenticated";
    case 403:
      return "forbidden";
    case 404:
      return "not_found";
    case 405:
      return "method_not_allowed";
    case 409:
      return "conflict";
    case 413:
      return "too_large";
    case 429:
      return "rate_limited";
    case 503:
      return "unavailable";
    default:
      return "server_error";
  }
}

/**
 * Extracts a human-readable error message from a parsed API response body.
 * Mirrors the precedence in `web/static/utils/api.js` `errorMessageFromData`:
 * the canonical nested envelope (`{"error":{"message"}}`), the legacy flat
 * shape (`{"error":"..."}`, e.g. `/api/callback/{token}`), a top-level
 * `{"message":"..."}`, and finally the caller-supplied fallback.
 * @param {*} body - The parsed response body (object, or anything).
 * @param {string} fallback - Message to use when none can be extracted.
 * @returns {string}
 */
export function errorMessageFromBody(body, fallback) {
  return (
    body?.error?.message ||
    (typeof body?.error === "string" ? body.error : undefined) ||
    body?.message ||
    fallback
  );
}

/**
 * Builds a typed `MittoApiError` (or `MittoAuthError` for 401/403) from a
 * non-2xx HTTP response. Never throws, even on a malformed/empty body.
 * @param {{status: number, body: *}} response
 * @returns {MittoApiError}
 */
export function errorFromResponse({ status, body }) {
  const isObjectBody = body !== null && typeof body === "object" && !Array.isArray(body);
  const nestedError =
    isObjectBody && body.error !== null && typeof body.error === "object"
      ? body.error
      : undefined;
  const code =
    nestedError?.code ||
    (isObjectBody && typeof body.error === "string" ? body.error : undefined) ||
    errorCodeForStatus(status);
  const details = nestedError?.details;
  const message = errorMessageFromBody(body, `Request failed with status ${status}`);

  const ErrorClass = status === 401 || status === 403 ? MittoAuthError : MittoApiError;
  return new ErrorClass(message, { status, code, details, body });
}
