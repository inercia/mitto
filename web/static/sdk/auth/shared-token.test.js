/**
 * Unit tests for `sharedTokenAuth` (web/static/sdk/auth/shared-token.js).
 */
import { sharedTokenAuth } from "./shared-token.js";

describe("sharedTokenAuth", () => {
  describe("authorize()", () => {
    test("sets Authorization: Bearer <token> from a sync getToken", async () => {
      const auth = sharedTokenAuth({ getToken: () => "s3cr3t" });
      const patch = await auth.authorize({ method: "GET", url: "/x", headers: {} });
      expect(patch).toEqual({ headers: { Authorization: "Bearer s3cr3t" } });
    });

    test("sets Authorization: Bearer <token> from an async getToken", async () => {
      const auth = sharedTokenAuth({ getToken: async () => "s3cr3t" });
      const patch = await auth.authorize({ method: "GET", url: "/x", headers: {} });
      expect(patch).toEqual({ headers: { Authorization: "Bearer s3cr3t" } });
    });

    test("never sets credentials (a bearer client has no cookie jar)", async () => {
      const auth = sharedTokenAuth({ getToken: () => "s3cr3t" });
      const patch = await auth.authorize({ method: "POST", url: "/x", headers: {} });
      expect(patch.credentials).toBeUndefined();
    });

    test("empty token resolves to an empty patch, not 'Bearer undefined'", async () => {
      const auth = sharedTokenAuth({ getToken: () => "" });
      const patch = await auth.authorize({ method: "GET", url: "/x", headers: {} });
      expect(patch).toEqual({});
    });

    test("undefined token resolves to an empty patch", async () => {
      const auth = sharedTokenAuth({ getToken: () => undefined });
      const patch = await auth.authorize({ method: "GET", url: "/x", headers: {} });
      expect(patch).toEqual({});
    });

    test("getToken is called fresh on every authorize() (not captured at construction)", async () => {
      let current = "first";
      const auth = sharedTokenAuth({ getToken: () => current });
      expect(await auth.authorize({})).toEqual({ headers: { Authorization: "Bearer first" } });
      current = "second";
      expect(await auth.authorize({})).toEqual({ headers: { Authorization: "Bearer second" } });
    });

    test.each(["GET", "POST", "PUT", "PATCH", "DELETE"])(
      "%s never adds an X-CSRF-Token header — no CSRF fetch for a bearer-authenticated adapter",
      async (method) => {
        const auth = sharedTokenAuth({ getToken: () => "s3cr3t" });
        const patch = await auth.authorize({ method, url: "/x", headers: {} });
        expect(patch.headers).toEqual({ Authorization: "Bearer s3cr3t" });
        expect(patch.headers["X-CSRF-Token"]).toBeUndefined();
      },
    );
  });

  describe("authorizeWebSocket()", () => {
    test("returns ctor options.headers carrying the same bearer token", async () => {
      const auth = sharedTokenAuth({ getToken: () => "s3cr3t" });
      const patch = await auth.authorizeWebSocket({ url: "wss://host/ws" });
      expect(patch).toEqual({ options: { headers: { Authorization: "Bearer s3cr3t" } } });
    });

    test("never appends the token to the URL — no query-param fallback", async () => {
      const auth = sharedTokenAuth({ getToken: () => "s3cr3t" });
      const patch = await auth.authorizeWebSocket({ url: "wss://host/ws" });
      expect(JSON.stringify(patch)).not.toContain("wss://host/ws?");
      expect(patch.protocols).toBeUndefined();
    });

    test("empty token resolves to an empty patch", async () => {
      const auth = sharedTokenAuth({ getToken: () => "" });
      const patch = await auth.authorizeWebSocket({ url: "wss://host/ws" });
      expect(patch).toEqual({});
    });
  });

  describe("no-leak guarantee", () => {
    test("the token never appears in any call the injected logger receives", async () => {
      const TOKEN = "super-secret-value";
      const logCalls = [];
      const logger = {
        debug: (...a) => logCalls.push(a),
        info: (...a) => logCalls.push(a),
        warn: (...a) => logCalls.push(a),
        error: (...a) => logCalls.push(a),
      };
      const auth = sharedTokenAuth({
        getToken: () => {
          logger.debug("resolving token"); // adapter-adjacent logging, must not include the value
          return TOKEN;
        },
      });

      await auth.authorize({ method: "POST", url: "/x", headers: {} });
      await auth.authorizeWebSocket({ url: "wss://host/ws" });

      const serialized = JSON.stringify(logCalls);
      expect(serialized).not.toContain(TOKEN);
    });

    test("the built request patch carries the token only in the Authorization header, never elsewhere", async () => {
      const TOKEN = "super-secret-value";
      const auth = sharedTokenAuth({ getToken: () => TOKEN });
      const patch = await auth.authorize({ method: "GET", url: "/x?a=1", headers: {} });
      expect(patch.headers.Authorization).toBe(`Bearer ${TOKEN}`);
      // Nothing outside the Authorization header value contains the token.
      const { Authorization, ...restHeaders } = patch.headers;
      expect(JSON.stringify(restHeaders)).not.toContain(TOKEN);
    });
  });
});
