/**
 * Unit tests for the SDK error taxonomy (web/static/sdk/core/errors.js).
 *
 * Covers: class hierarchy / instanceof chains, errorCodeForStatus's HTTP
 * status -> code mapping, errorMessageFromBody's precedence order, and
 * errorFromResponse building typed errors from both the canonical nested
 * envelope and the legacy flat envelope, plus degenerate bodies (non-JSON,
 * empty, null, array).
 */
import {
  MittoError,
  ConfigError,
  MittoApiError,
  MittoAuthError,
  MittoNetworkError,
  errorCodeForStatus,
  errorMessageFromBody,
  errorFromResponse,
} from "./errors.js";

describe("error class hierarchy", () => {
  test("ConfigError is a MittoError with the pinned name/code", () => {
    const e = new ConfigError("bad option");
    expect(e).toBeInstanceOf(MittoError);
    expect(e).toBeInstanceOf(Error);
    expect(e.name).toBe("ConfigError");
    expect(e.code).toBe("invalid_config");
    expect(e.message).toBe("bad option");
  });

  test("MittoApiError is a MittoError and carries status/code/details/body", () => {
    const body = { error: { code: "not_found", message: "nope" } };
    const e = new MittoApiError("nope", { status: 404, code: "not_found", details: { x: 1 }, body });
    expect(e).toBeInstanceOf(MittoError);
    expect(e.name).toBe("MittoApiError");
    expect(e.status).toBe(404);
    expect(e.code).toBe("not_found");
    expect(e.details).toEqual({ x: 1 });
    expect(e.body).toBe(body);
  });

  test("MittoAuthError is a MittoApiError (and thus a MittoError)", () => {
    const e = new MittoAuthError("denied", { status: 401, code: "unauthenticated" });
    expect(e).toBeInstanceOf(MittoApiError);
    expect(e).toBeInstanceOf(MittoError);
    expect(e.name).toBe("MittoAuthError");
    expect(e.status).toBe(401);
  });

  test("MittoNetworkError is a MittoError with code=network_error and preserves cause", () => {
    const cause = new TypeError("fetch failed");
    const e = new MittoNetworkError("network down", { cause });
    expect(e).toBeInstanceOf(MittoError);
    expect(e.name).toBe("MittoNetworkError");
    expect(e.code).toBe("network_error");
    expect(e.cause).toBe(cause);
  });

  test("MittoNetworkError tolerates a missing cause", () => {
    // Note: the constructor call is wrapped in a block (not returned) because
    // Bun's `.not.toThrow()` treats a callback that *returns* an Error
    // instance as if it threw, even when it was merely constructed.
    expect(() => {
      new MittoNetworkError("network down");
    }).not.toThrow();
    const e = new MittoNetworkError("network down");
    expect(e.cause).toBeUndefined();
  });
});

describe("errorCodeForStatus", () => {
  test.each([
    [400, "bad_request"],
    [401, "unauthenticated"],
    [403, "forbidden"],
    [404, "not_found"],
    [405, "method_not_allowed"],
    [409, "conflict"],
    [413, "too_large"],
    [429, "rate_limited"],
    [503, "unavailable"],
    [500, "server_error"],
    [418, "server_error"],
  ])("status %p -> %p", (status, expected) => {
    expect(errorCodeForStatus(status)).toBe(expected);
  });
});

describe("errorMessageFromBody", () => {
  test("prefers the canonical nested envelope message", () => {
    const body = { error: { code: "not_found", message: "canonical msg" } };
    expect(errorMessageFromBody(body, "fallback")).toBe("canonical msg");
  });

  test("uses the legacy flat string envelope's error field", () => {
    const body = { error: "not_found" };
    expect(errorMessageFromBody(body, "fallback")).toBe("not_found");
  });

  test("legacy flat error string takes precedence over a sibling top-level message", () => {
    // Matches web/static/utils/api.js's errorMessageFromData precedence: once
    // `error` is a string, the flat-envelope branch wins even if a top-level
    // `message` field is also present (e.g. /api/callback/{token} responses).
    const body = { error: "not_found", message: "legacy msg" };
    expect(errorMessageFromBody(body, "fallback")).toBe("not_found");
  });

  test("falls back to a top-level message when error has no message", () => {
    const body = { message: "top-level msg" };
    expect(errorMessageFromBody(body, "fallback")).toBe("top-level msg");
  });

  test("falls back to the caller-supplied fallback for a degenerate body", () => {
    expect(errorMessageFromBody(null, "fallback")).toBe("fallback");
    expect(errorMessageFromBody(undefined, "fallback")).toBe("fallback");
    expect(errorMessageFromBody("plain text", "fallback")).toBe("fallback");
    expect(errorMessageFromBody({}, "fallback")).toBe("fallback");
  });
});

describe("errorFromResponse", () => {
  test("builds a MittoApiError from the canonical envelope", () => {
    const body = { error: { code: "conflict", message: "already exists", details: { id: "x" } } };
    const e = errorFromResponse({ status: 409, body });
    expect(e).toBeInstanceOf(MittoApiError);
    expect(e).not.toBeInstanceOf(MittoAuthError);
    expect(e.status).toBe(409);
    expect(e.code).toBe("conflict");
    expect(e.details).toEqual({ id: "x" });
    expect(e.message).toBe("already exists");
    expect(e.body).toBe(body);
  });

  test("builds a MittoApiError from the legacy flat envelope (e.g. /api/callback/{token})", () => {
    const body = { error: "invalid_token" };
    const e = errorFromResponse({ status: 400, body });
    expect(e).toBeInstanceOf(MittoApiError);
    expect(e.code).toBe("invalid_token");
    expect(e.message).toBe("invalid_token");
  });

  test("derives the code from status when the body has none", () => {
    const e = errorFromResponse({ status: 429, body: {} });
    expect(e.code).toBe("rate_limited");
    expect(e.message).toBe("Request failed with status 429");
  });

  test("returns a MittoAuthError for 401 and 403, and a MittoApiError otherwise", () => {
    expect(errorFromResponse({ status: 401, body: {} })).toBeInstanceOf(MittoAuthError);
    expect(errorFromResponse({ status: 403, body: {} })).toBeInstanceOf(MittoAuthError);
    expect(errorFromResponse({ status: 500, body: {} })).not.toBeInstanceOf(MittoAuthError);
  });

  // Note: assertions below wrap the call in a block statement (not returned)
  // because Bun's `.not.toThrow()` treats a callback that *returns* an Error
  // instance as if it threw — even though errorFromResponse never throws.

  test("never throws on a non-JSON (string) body", () => {
    expect(() => {
      errorFromResponse({ status: 500, body: "<html>oops</html>" });
    }).not.toThrow();
    const e = errorFromResponse({ status: 500, body: "<html>oops</html>" });
    expect(e.code).toBe("server_error");
    expect(e.message).toBe("Request failed with status 500");
  });

  test("never throws on an empty/null/undefined body", () => {
    expect(() => {
      errorFromResponse({ status: 404, body: null });
    }).not.toThrow();
    expect(() => {
      errorFromResponse({ status: 404, body: undefined });
    }).not.toThrow();
    const e = errorFromResponse({ status: 404, body: null });
    expect(e.code).toBe("not_found");
  });

  test("never throws on an array body", () => {
    expect(() => {
      errorFromResponse({ status: 500, body: [1, 2, 3] });
    }).not.toThrow();
    const e = errorFromResponse({ status: 500, body: [1, 2, 3] });
    expect(e.code).toBe("server_error");
  });
});
