/**
 * Focused tests pinning the utils/csrf.js -> sdk/auth/browser-cookie.js shim
 * swap (mitto-7gta.5): secureFetch's CSRF header behavior and its 401 ->
 * redirectToLogin delegation must be unchanged for the module's 17 existing
 * importers. Runs under happy-dom (bunfig.toml preload), so real
 * `document.cookie` / `window.location` are used, matching how this module
 * is actually consumed.
 */
import { secureFetch, checkAuth } from "./csrf.js";

function setCsrfCookie(value) {
  document.cookie = `mitto_csrf=${value}; path=/`;
}

function clearCsrfCookie() {
  document.cookie = "mitto_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
}

describe("utils/csrf.js shim (browserCookieAuth-backed)", () => {
  afterEach(() => {
    clearCsrfCookie();
  });

  test("secureFetch sends the CSRF header from the cookie on POST", async () => {
    setCsrfCookie("cookie-token-1");
    let capturedHeaders;
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async (_url, opts) => {
      capturedHeaders = opts.headers;
      return { ok: true, status: 200, json: async () => ({}) };
    };
    try {
      await secureFetch("/api/x", { method: "POST" });
    } finally {
      globalThis.fetch = originalFetch;
    }
    expect(capturedHeaders.get("X-CSRF-Token")).toBe("cookie-token-1");
  });

  test("secureFetch omits the CSRF header on GET", async () => {
    setCsrfCookie("cookie-token-2");
    let capturedHeaders;
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async (_url, opts) => {
      capturedHeaders = opts?.headers;
      return { ok: true, status: 200, json: async () => ({}) };
    };
    try {
      await secureFetch("/api/x", { method: "GET" });
    } finally {
      globalThis.fetch = originalFetch;
    }
    expect(capturedHeaders?.get?.("X-CSRF-Token")).toBeFalsy();
  });

  test("secureFetch always includes credentials", async () => {
    setCsrfCookie("cookie-token-3");
    let capturedOpts;
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async (_url, opts) => {
      capturedOpts = opts;
      return { ok: true, status: 200, json: async () => ({}) };
    };
    try {
      await secureFetch("/api/x", { method: "GET" });
    } finally {
      globalThis.fetch = originalFetch;
    }
    expect(capturedOpts.credentials).toBe("include");
  });

  test("checkAuth on a 401 response redirects to the login page", () => {
    const originalLocation = window.location.href;
    // happy-dom allows reassigning location.href; guard by restoring after.
    checkAuth({ status: 401 });
    expect(window.location.href).toContain("/auth.html");
    window.location.href = originalLocation;
  });

  test("checkAuth on a non-401 response passes it through unchanged", () => {
    const response = { status: 200 };
    expect(checkAuth(response)).toBe(response);
  });
});
