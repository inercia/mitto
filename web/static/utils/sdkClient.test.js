/**
 * Unit tests for the SDK client seam (mitto-7gta.18, slice S1; extended by
 * mitto-7gta.19.1 to cover the folded-in getCSRFToken/initCSRF/
 * redirectToLogin and the endpoints shim-contract assertions previously in
 * utils/endpoints.test.js, both retired when utils/csrf.js and
 * utils/endpoints.js were deleted).
 *
 * Runs under happy-dom (bunfig.toml preload) so real `window`/`localStorage`
 * are used, matching how this module is actually consumed.
 */
import {
  getSdkClient,
  getSdkWsBaseUrl,
  getCSRFToken,
  initCSRF,
  redirectToLogin,
  createSdkSeqStore,
  createSdkPendingPromptStore,
  _resetSdkClientForTests,
} from "./sdkClient.js";
import { getLastSeenSeq, setLastSeenSeq } from "./storage.js";
import { MittoAuthError } from "../sdk/index.js";

describe("sdkClient", () => {
  afterEach(() => {
    _resetSdkClientForTests();
    localStorage.clear();
    delete window.mittoApiPrefix;
  });

  describe("getSdkClient", () => {
    test("returns the same instance on repeated calls (singleton)", () => {
      const a = getSdkClient();
      const b = getSdkClient();
      expect(a).toBe(b);
    });

    test("exposes sessionStream/eventsStream factories", () => {
      const client = getSdkClient();
      expect(typeof client.sessionStream).toBe("function");
      expect(typeof client.eventsStream).toBe("function");
    });

    test("wires apiPrefix from getApiPrefix() (window.mittoApiPrefix)", () => {
      window.mittoApiPrefix = "/mitto";
      const client = getSdkClient();
      expect(client.config.apiPrefix).toBe("/mitto");
    });

    test("apiPrefix defaults to empty string when window.mittoApiPrefix is unset", () => {
      const client = getSdkClient();
      expect(client.config.apiPrefix).toBe("");
    });

    test("baseUrl is relative (empty string)", () => {
      const client = getSdkClient();
      expect(client.config.baseUrl).toBe("");
    });

    test("auth adapter includes credentials and CSRF header from the cookie", async () => {
      document.cookie = "mitto_csrf=seam-test-token; path=/";
      const client = getSdkClient();
      const patch = await client.config.auth.authorize({ method: "POST" });
      expect(patch.credentials).toBe("include");
      expect(patch.headers["X-CSRF-Token"]).toBe("seam-test-token");
      document.cookie =
        "mitto_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    });

    test("auth adapter omits the CSRF header on GET (safe method)", async () => {
      document.cookie = "mitto_csrf=seam-test-token-2; path=/";
      const client = getSdkClient();
      const patch = await client.config.auth.authorize({ method: "GET" });
      expect(patch.credentials).toBe("include");
      expect(patch.headers).toBeUndefined();
      document.cookie =
        "mitto_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    });

    test("config.fetch calls the CURRENT global fetch, not a snapshot taken at client-construction time", async () => {
      const client = getSdkClient();
      const originalFetch = globalThis.fetch;
      // Installed after getSdkClient() above, proving config.fetch binds
      // `fetch` lazily rather than snapshotting it at construction time (see
      // sdkClient.js's late-bound comment).
      let called = false;
      globalThis.fetch = async () => {
        called = true;
        return { ok: true, status: 200, json: async () => ({}) };
      };
      try {
        await client.config.fetch("/anything");
      } finally {
        globalThis.fetch = originalFetch;
      }
      expect(called).toBe(true);
    });

    test("config.onUnauthorized delegates to this module's redirectToLogin (401 policy preserved)", () => {
      const originalHref = window.location.href;
      try {
        const client = getSdkClient();
        client.config.onUnauthorized();
        expect(window.location.href).toContain("/auth.html");
      } finally {
        window.location.href = originalHref;
      }
    });

    test("a real 401 through a resource call redirects via onUnauthorized AND still rejects (documented delta from authFetch's never-resolving promise)", async () => {
      const originalHref = window.location.href;
      const originalFetch = globalThis.fetch;
      globalThis.fetch = async () => ({
        ok: false,
        status: 401,
        headers: { get: (name) => (name.toLowerCase() === "content-type" ? "application/json" : null) },
        text: async () =>
          JSON.stringify({
            error: { code: "unauthenticated", message: "Authentication required" },
          }),
      });
      try {
        const client = getSdkClient();
        // GET is a "safe" method for browserCookieAuth's authorize(), so no
        // CSRF-token fetch is triggered first — the stub above only ever
        // needs to answer the one request this call makes.
        await expect(client.serverConfig.get()).rejects.toBeInstanceOf(
          MittoAuthError,
        );
        expect(window.location.href).toContain("/auth.html");
      } finally {
        globalThis.fetch = originalFetch;
        window.location.href = originalHref;
      }
    });

    test("auth adapter fetches a fresh token via the CURRENT global fetch when no cookie exists, hitting endpoints.misc.csrfToken()", async () => {
      window.mittoApiPrefix = "/mitto";
      const seenUrls = [];
      const originalFetch = globalThis.fetch;
      // Installed after getSdkClient() below (via afterEach's reset), proving
      // the adapter binds `fetch` lazily rather than snapshotting it at
      // client-construction time (see sdkClient.js's late-bound comment).
      globalThis.fetch = async (url) => {
        seenUrls.push(String(url));
        return {
          ok: true,
          status: 200,
          json: async () => ({ token: "fetched-token" }),
        };
      };
      let patch;
      try {
        const client = getSdkClient();
        patch = await client.config.auth.authorize({ method: "POST" });
      } finally {
        globalThis.fetch = originalFetch;
      }
      expect(seenUrls).toEqual(["/mitto/api/csrf-token"]);
      expect(patch.headers["X-CSRF-Token"]).toBe("fetched-token");
    });
  });

  describe("getSdkWsBaseUrl", () => {
    test("derives ws:// from http: page origin", () => {
      expect(getSdkWsBaseUrl()).toBe(`ws://${window.location.host}`);
    });

    test("derives wss:// from https: page origin", () => {
      const original = window.location.protocol;
      // happy-dom allows reassigning location.protocol; guard by restoring after.
      window.location.protocol = "https:";
      try {
        expect(getSdkWsBaseUrl()).toBe(`wss://${window.location.host}`);
      } finally {
        window.location.protocol = original;
      }
    });
  });

  describe("createSdkSeqStore key compatibility with utils/storage.js", () => {
    test("a watermark written via utils/storage.js is readable via the SDK seq store", () => {
      setLastSeenSeq("sess-1", 42);
      const seqStore = createSdkSeqStore();
      expect(seqStore.get("sess-1")).toBe(42);
    });

    test("a watermark written via the SDK seq store is readable via utils/storage.js", () => {
      const seqStore = createSdkSeqStore();
      seqStore.set("sess-2", 7);
      expect(getLastSeenSeq("sess-2")).toBe(7);
    });

    test("reset() clears the watermark for both readers", () => {
      setLastSeenSeq("sess-3", 5);
      const seqStore = createSdkSeqStore();
      seqStore.reset("sess-3");
      expect(seqStore.get("sess-3")).toBe(0);
      expect(getLastSeenSeq("sess-3")).toBe(0);
    });

    test("monotonic: set() never lowers an existing watermark", () => {
      const seqStore = createSdkSeqStore();
      seqStore.set("sess-4", 10);
      seqStore.set("sess-4", 3);
      expect(seqStore.get("sess-4")).toBe(10);
    });
  });

  describe("createSdkPendingPromptStore", () => {
    test("save/getForSession/remove round-trip", () => {
      const store = createSdkPendingPromptStore();
      store.save("sess-5", "prompt-1", "hello", ["img-1"], []);
      const pending = store.getForSession("sess-5");
      expect(pending).toHaveLength(1);
      expect(pending[0]).toMatchObject({
        promptId: "prompt-1",
        message: "hello",
        imageIds: ["img-1"],
      });

      store.remove("prompt-1");
      expect(store.getForSession("sess-5")).toHaveLength(0);
    });

    test("uses a storage key distinct from the legacy lib.js pending-prompts key", () => {
      const store = createSdkPendingPromptStore();
      store.save("sess-6", "prompt-2", "hi", [], []);
      expect(localStorage.getItem("mitto_pending_prompts")).toBe(null);
      expect(localStorage.getItem("mitto_sdk_pending_prompts")).not.toBe(null);
    });
  });

  // mitto-7gta.19.1: getCSRFToken/initCSRF/redirectToLogin were folded in
  // from the deleted utils/csrf.js, now sharing the SDK client's single
  // browserCookieAuth() instance instead of constructing a second one.
  describe("getCSRFToken / initCSRF / redirectToLogin (folded from utils/csrf.js)", () => {
    afterEach(() => {
      document.cookie =
        "mitto_csrf=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
    });

    test("getCSRFToken reads the token from the cookie", async () => {
      document.cookie = "mitto_csrf=folded-test-token; path=/";
      await expect(getCSRFToken()).resolves.toBe("folded-test-token");
    });

    test("initCSRF resolves without throwing even on failure", async () => {
      const originalFetch = globalThis.fetch;
      globalThis.fetch = async () => ({ ok: false, status: 500 });
      try {
        await expect(initCSRF()).resolves.toBeUndefined();
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    test("redirectToLogin navigates to /auth.html and clears in-flight adapter state", () => {
      const originalHref = window.location.href;
      try {
        getSdkClient(); // ensure the auth adapter exists
        redirectToLogin();
        expect(window.location.href).toContain("/auth.html");
      } finally {
        window.location.href = originalHref;
      }
    });

    test("redirectToLogin is a no-op-safe call before getSdkClient() has ever run", () => {
      // _resetSdkClientForTests() (afterEach, outer describe) clears both
      // the client and the captured auth adapter — this must not throw.
      const originalHref = window.location.href;
      try {
        expect(() => redirectToLogin()).not.toThrow();
      } finally {
        window.location.href = originalHref;
      }
    });
  });

  // Shim-contract tests migrated from the deleted utils/endpoints.js's
  // endpoints.test.js (mitto-7gta.19.1). The exhaustive per-builder /
  // per-group assertions live in sdk/core/endpoints.test.js; these only
  // verify getSdkClient().endpoints wiring: apiPrefix/wsBaseUrl are baked in
  // at client construction (unlike the old live-rereading proxy, a prefix
  // change requires _resetSdkClientForTests() + a fresh getSdkClient() call,
  // matching this file's existing "wires apiPrefix" test above), and ws
  // builders resolve via wsBaseUrl derived from window.location.
  describe("getSdkClient().endpoints (folded from utils/endpoints.js)", () => {
    test("applies the current apiPrefix at construction time", () => {
      window.mittoApiPrefix = "/mitto";
      expect(getSdkClient().endpoints.sessions.list()).toBe(
        "/mitto/api/sessions",
      );
    });

    test("no prefix when mittoApiPrefix is unset", () => {
      expect(getSdkClient().endpoints.sessions.list()).toBe("/api/sessions");
    });

    test("a prefix change requires a fresh client to take effect", () => {
      expect(getSdkClient().endpoints.sessions.list()).toBe("/api/sessions");
      window.mittoApiPrefix = "/mitto";
      _resetSdkClientForTests();
      expect(getSdkClient().endpoints.sessions.list()).toBe(
        "/mitto/api/sessions",
      );
    });

    test("path-param builder also respects the prefix", () => {
      window.mittoApiPrefix = "/mitto";
      expect(
        getSdkClient().endpoints.sessions.get("20260101-120000-deadbeef"),
      ).toBe("/mitto/api/sessions/20260101-120000-deadbeef");
    });

    test("ws builders derive an absolute ws(s):// origin from getSdkWsBaseUrl()", () => {
      const url = getSdkClient().endpoints.events.ws();
      expect(url).toMatch(/^wss?:\/\//);
      expect(url).toMatch(/\/api\/events$/);
    });

    test("sessions.ws builder resolves to an absolute ws(s):// URL", () => {
      const url = getSdkClient().endpoints.sessions.ws("abc");
      expect(url).toMatch(/^wss?:\/\//);
      expect(url).toMatch(/\/api\/sessions\/abc\/ws$/);
    });

    test("misc.csrfToken (a zero-arg builder) resolves through the registry", () => {
      expect(getSdkClient().endpoints.misc.csrfToken()).toBe(
        "/api/csrf-token",
      );
    });
  });
});
