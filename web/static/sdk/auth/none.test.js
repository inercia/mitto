/**
 * Unit tests for `noneAuth` (web/static/sdk/auth/none.js).
 */
import { noneAuth } from "./none.js";

describe("noneAuth", () => {
  test("authorize() resolves to an empty patch — no headers, no credentials", async () => {
    const auth = noneAuth();
    const patch = await auth.authorize({ method: "POST", url: "/x", headers: {} });
    expect(patch).toEqual({});
  });

  test("authorize() ignores the request shape entirely", async () => {
    const auth = noneAuth();
    await expect(auth.authorize()).resolves.toEqual({});
    await expect(auth.authorize({})).resolves.toEqual({});
  });

  test("does not implement authorizeWebSocket or onUnauthorized", () => {
    const auth = noneAuth();
    expect(auth.authorizeWebSocket).toBeUndefined();
    expect(auth.onUnauthorized).toBeUndefined();
  });
});
