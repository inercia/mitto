/**
 * SDK core transport — the single `request()` primitive every resource
 * module (`.7`–`.12`) is built on. Environment-agnostic per
 * docs/devel/js-client-library.md §4: same forbidden-globals rule as the
 * rest of `sdk/core/` (see config.js's header). This module is a deep
 * import, not part of the public surface (§5) — it is never re-exported
 * from `sdk/index.js`.
 */
import { MittoNetworkError, errorFromResponse } from "./errors.js";

/** Build a query string from a params object, omitting null/undefined/""
 *  values. Array values emit repeated `key=v` params (one per element);
 *  empty arrays are "no filter" and omitted entirely. Ported from
 *  utils/endpoints.js's `qs()` so behavior stays byte-identical. */
function qs(params) {
  if (!params) return "";
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    if (Array.isArray(v)) {
      for (const item of v) {
        if (item === undefined || item === null || item === "") continue;
        sp.append(k, item);
      }
      continue;
    }
    sp.append(k, v);
  }
  const s = sp.toString();
  return s ? "?" + s : "";
}

const ABSOLUTE_URL_RE = /^https?:\/\//i;

/**
 * Builds the full request URL from injected config. An absolute `path`
 * (http(s)://) is used as-is (query params are still appended); otherwise
 * `config.baseUrl + config.apiPrefix + path` is used.
 * @param {object} config - resolved config (see core/config.js)
 * @param {string} path
 * @param {object} [query]
 * @returns {string}
 */
export function buildUrl(config, path, query) {
  const base = ABSOLUTE_URL_RE.test(path)
    ? path
    : `${config.baseUrl}${config.apiPrefix}${path}`;
  return base + qs(query);
}

/** Duck-typed detection of bodies that must be sent untouched, with no
 *  Content-Type set (the runtime sets it, e.g. multipart boundary). */
function isPassthroughBody(body) {
  if (typeof body === "string") return true;
  if (typeof FormData !== "undefined" && body instanceof FormData) return true;
  if (typeof Blob !== "undefined" && body instanceof Blob) return true;
  if (typeof ArrayBuffer !== "undefined" && body instanceof ArrayBuffer)
    return true;
  if (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams)
    return true;
  // Duck-typed fallback for environments/mocks without the globals above.
  if (
    body &&
    typeof body.append === "function" &&
    typeof body.getAll !== "function"
  ) {
    return true; // FormData-like
  }
  return false;
}

/** Encodes `body` into `{ body, contentType }`. `contentType` is `null`
 *  when none should be set (no body, or a passthrough body). */
function encodeBody(body) {
  if (body === undefined || body === null)
    return { body: undefined, contentType: null };
  if (isPassthroughBody(body)) return { body, contentType: null };
  return { body: JSON.stringify(body), contentType: "application/json" };
}

function isJsonContentType(contentType) {
  return typeof contentType === "string" && /\bjson\b/i.test(contentType);
}

/** Decodes a Response body: null for 204/205/empty, JSON when the
 *  content-type says so, otherwise text. Never throws on malformed JSON —
 *  falls back to text so callers get *something* rather than a decode
 *  error masking the real (non-2xx) failure. */
async function decodeBody(response) {
  if (response.status === 204 || response.status === 205) return null;
  const contentType = response.headers?.get?.("content-type") ?? null;
  const text = await response.text();
  if (text === "") return null;
  if (isJsonContentType(contentType)) {
    try {
      return JSON.parse(text);
    } catch {
      return text;
    }
  }
  return text;
}

/**
 * The single request primitive. Resource modules curry `config` and call
 * this for every HTTP call.
 * @param {object} config - resolved config (see core/config.js)
 * @param {object} options
 * @param {string} options.method
 * @param {string} options.path - relative (joined with baseUrl+apiPrefix) or absolute
 * @param {object} [options.query] - query params, see `qs()`
 * @param {*} [options.body] - JSON-serializable value, or a passthrough body
 *   (FormData/Blob/ArrayBuffer/URLSearchParams/string)
 * @param {object} [options.headers]
 * @param {AbortSignal} [options.signal]
 * @param {boolean} [options.raw] - when true, resolve with the untouched
 *   `Response` instead of a decoded body (for streaming/blob callers)
 * @returns {Promise<*>}
 */
export async function request(config, options = {}) {
  const method = (options.method || "GET").toUpperCase();
  const url = buildUrl(config, options.path, options.query);
  const { body, contentType } = encodeBody(options.body);

  const headers = { ...(options.headers || {}) };
  if (
    contentType &&
    !Object.keys(headers).some((k) => k.toLowerCase() === "content-type")
  ) {
    headers["Content-Type"] = contentType;
  }

  const patch = (await config.auth.authorize({ method, url, headers })) || {};
  const finalHeaders = { ...headers, ...(patch.headers || {}) };

  const fetchInit = {
    method,
    headers: finalHeaders,
    body,
    signal: options.signal,
  };
  if (patch.credentials) fetchInit.credentials = patch.credentials;

  let response;
  try {
    response = await config.fetch(url, fetchInit);
  } catch (cause) {
    throw new MittoNetworkError(`Request to ${url} failed: ${cause.message}`, {
      cause,
    });
  }

  if (!response.ok) {
    const responseBody = await decodeBody(response);
    const error = errorFromResponse({
      status: response.status,
      body: responseBody,
    });
    if (response.status === 401) config.onUnauthorized(error);
    throw error;
  }

  if (options.raw) return response;
  return decodeBody(response);
}
