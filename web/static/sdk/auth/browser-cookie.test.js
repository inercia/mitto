/**
 * Unit tests for `browserCookieAuth` (web/static/sdk/auth/browser-cookie.js).
 *
 * `getCookie`/`fetch` are injected fakes — never real `document`/global
 * `fetch` — mirroring the injection style already exercised by
 * config.test.js / env/browser.test.js.
 */
import { browserCookieAuth } from "./browser-cookie.js";

function makeHarness({ cookieValue = null, fetchToken = "fetched-token" } = {}) {
  const cookies = new Map();
  if (cookieValue) cookies.set("mitto_csrf", cookieValue);
  const fetchCalls = [];
  let fetchResolvers = [];

  const fetchImpl = (url, opts) => {
    fetchCalls.push({ url, opts });
    return new Promise((resolve) => {
      fetchResolvers.push(() =>
        resolve({ ok: true, json: async () => ({ token: fetchToken }) }),
      );
    });
  };

  const auth = browserCookieAuth({
    getCookie: (name) => cookies.get(name) ?? null,
    fetch: fetchImpl,
    csrfTokenUrl: "/api/csrf-token",
  });

  return {
    auth,
    cookies,
    fetchCalls,
    resolveAllFetches: () => {
      const pending = fetchResolvers;
      fetchResolvers = [];
      pending.forEach((r) => r());
    },
  };
}

describe("browserCookieAuth", () => {
  describe("CSRF header presence by method", () => {
    test.each(["POST", "PUT", "PATCH", "DELETE", "post", "put"])(
      "%s includes the CSRF header",
      async (method) => {
        const { auth } = makeHarness({ cookieValue: "cookie-token" });
        const patch = await auth.authorize({ method });
        expect(patch.headers).toEqual({ "X-CSRF-Token": "cookie-token" });
      },
    );

    test.each(["GET", "HEAD", "OPTIONS", undefined])(
      "%s omits the CSRF header",
      async (method) => {
        const { auth } = makeHarness({ cookieValue: "cookie-token" });
        const patch = await auth.authorize({ method });
        expect(patch.headers).toBeUndefined();
      },
    );
  });

  test("credentials is always 'include', regardless of method", async () => {
    const { auth } = makeHarness({ cookieValue: "cookie-token" });
    expect((await auth.authorize({ method: "GET" })).credentials).toBe("include");
    expect((await auth.authorize({ method: "POST" })).credentials).toBe("include");
  });

  test("cookie-first: an existing cookie is used without fetching", async () => {
    const { auth, fetchCalls } = makeHarness({ cookieValue: "cookie-token" });
    const patch = await auth.authorize({ method: "POST" });
    expect(patch.headers["X-CSRF-Token"]).toBe("cookie-token");
    expect(fetchCalls).toHaveLength(0);
  });

  test("no cookie: fetches a token from csrfTokenUrl with credentials included", async () => {
    const { auth, fetchCalls, resolveAllFetches } = makeHarness({ fetchToken: "fetched-token" });
    const pending = auth.authorize({ method: "POST" });
    await Promise.resolve(); // let the fetch call happen
    resolveAllFetches();
    const patch = await pending;
    expect(patch.headers["X-CSRF-Token"]).toBe("fetched-token");
    expect(fetchCalls).toHaveLength(1);
    expect(fetchCalls[0].url).toBe("/api/csrf-token");
    expect(fetchCalls[0].opts).toEqual({ credentials: "include" });
  });

  test("concurrent misses share a single in-flight fetch (single-flight)", async () => {
    const { auth, fetchCalls, resolveAllFetches } = makeHarness({ fetchToken: "shared-token" });
    const p1 = auth.authorize({ method: "POST" });
    const p2 = auth.authorize({ method: "PUT" });
    await Promise.resolve();
    resolveAllFetches();
    const [patch1, patch2] = await Promise.all([p1, p2]);
    expect(patch1.headers["X-CSRF-Token"]).toBe("shared-token");
    expect(patch2.headers["X-CSRF-Token"]).toBe("shared-token");
    expect(fetchCalls).toHaveLength(1);
  });

  test("a failed token fetch rejects authorize()", async () => {
    const auth = browserCookieAuth({
      getCookie: () => null,
      fetch: async () => ({ ok: false, status: 500 }),
      csrfTokenUrl: "/api/csrf-token",
    });
    await expect(auth.authorize({ method: "POST" })).rejects.toThrow(
      "Failed to fetch CSRF token",
    );
  });

  test("onUnauthorized() clears the cached in-flight promise so the next call refetches", async () => {
    let tokenValue = "first-token";
    const fetchCalls = [];
    const auth = browserCookieAuth({
      getCookie: () => null,
      fetch: async (url) => {
        fetchCalls.push(url);
        return { ok: true, json: async () => ({ token: tokenValue }) };
      },
      csrfTokenUrl: "/api/csrf-token",
    });

    const patch1 = await auth.authorize({ method: "POST" });
    expect(patch1.headers["X-CSRF-Token"]).toBe("first-token");
    expect(fetchCalls).toHaveLength(1);

    auth.onUnauthorized(new Error("401"));
    tokenValue = "second-token";
    const patch2 = await auth.authorize({ method: "POST" });
    expect(patch2.headers["X-CSRF-Token"]).toBe("second-token");
    expect(fetchCalls).toHaveLength(2);
  });

  test("honors custom cookieName/headerName", async () => {
    const auth = browserCookieAuth({
      getCookie: (name) => (name === "custom_cookie" ? "custom-token" : null),
      fetch: () => {
        throw new Error("should not fetch");
      },
      csrfTokenUrl: "/api/csrf-token",
      cookieName: "custom_cookie",
      headerName: "X-Custom-CSRF",
    });
    const patch = await auth.authorize({ method: "POST" });
    expect(patch.headers).toEqual({ "X-Custom-CSRF": "custom-token" });
  });

  test("does not implement authorizeWebSocket", () => {
    const { auth } = makeHarness();
    expect(auth.authorizeWebSocket).toBeUndefined();
  });
});
