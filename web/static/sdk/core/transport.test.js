/**
 * Unit tests for the SDK transport (web/static/sdk/core/transport.js).
 *
 * All fetch behavior is driven by an injected `config.fetch` stub — never
 * global fetch mocking — mirroring the injection style already exercised
 * by config.test.js / session-stream.test.js.
 */
import { resolveConfig } from "./config.js";
import { MittoApiError, MittoAuthError, MittoNetworkError } from "./errors.js";
import { buildUrl, request } from "./transport.js";

/** Builds a minimal fake Response-like object. */
function fakeResponse({ status = 200, headers = {}, text = "" } = {}) {
  const lower = Object.fromEntries(
    Object.entries(headers).map(([k, v]) => [k.toLowerCase(), v]),
  );
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: { get: (name) => lower[name.toLowerCase()] ?? null },
    text: async () => text,
  };
}

function configWithFetch(fetchImpl, extra = {}) {
  return resolveConfig({ fetch: fetchImpl, ...extra }, {});
}

describe("buildUrl", () => {
  test("joins baseUrl + apiPrefix + path", () => {
    const config = configWithFetch(() => {}, {
      baseUrl: "http://host",
      apiPrefix: "/api",
    });
    expect(buildUrl(config, "/sessions")).toBe("http://host/api/sessions");
  });

  test("appends a query string built with URLSearchParams semantics", () => {
    const config = configWithFetch(() => {});
    expect(
      buildUrl(config, "/x", { a: "1", b: null, c: undefined, d: "" }),
    ).toBe("/x?a=1");
  });

  test("array query values emit repeated keys, skipping empty entries", () => {
    const config = configWithFetch(() => {});
    expect(buildUrl(config, "/x", { tag: ["a", "", "b"] })).toBe(
      "/x?tag=a&tag=b",
    );
  });

  test("an absolute path is used as-is (query still appended)", () => {
    const config = configWithFetch(() => {}, {
      baseUrl: "http://host",
      apiPrefix: "/api",
    });
    expect(buildUrl(config, "http://other/x", { a: "1" })).toBe(
      "http://other/x?a=1",
    );
  });
});

describe("request — body encoding", () => {
  test("JSON-encodes a plain object body and sets Content-Type", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { method: "POST", path: "/x", body: { a: 1 } });
    expect(seenInit.body).toBe(JSON.stringify({ a: 1 }));
    expect(seenInit.headers["Content-Type"]).toBe("application/json");
  });

  test("passes a string body through untouched with no Content-Type", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { method: "POST", path: "/x", body: "raw text" });
    expect(seenInit.body).toBe("raw text");
    expect(seenInit.headers["Content-Type"]).toBeUndefined();
  });

  test("passes a FormData-like body through untouched with no Content-Type", async () => {
    let seenInit;
    const fakeFormData = { append: () => {} };
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { method: "POST", path: "/x", body: fakeFormData });
    expect(seenInit.body).toBe(fakeFormData);
    expect(seenInit.headers["Content-Type"]).toBeUndefined();
  });

  test("no body means no Content-Type header", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { method: "GET", path: "/x" });
    expect(seenInit.body).toBeUndefined();
    expect(seenInit.headers["Content-Type"]).toBeUndefined();
  });

  test("caller-supplied Content-Type is respected over the inferred one", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, {
      method: "POST",
      path: "/x",
      body: { a: 1 },
      headers: { "Content-Type": "application/merge-patch+json" },
    });
    expect(seenInit.headers["Content-Type"]).toBe(
      "application/merge-patch+json",
    );
  });

  test("a lowercase caller-supplied content-type header also blocks the inferred one", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, {
      method: "POST",
      path: "/x",
      body: { a: 1 },
      headers: { "content-type": "application/merge-patch+json" },
    });
    expect(seenInit.headers["Content-Type"]).toBeUndefined();
    expect(seenInit.headers["content-type"]).toBe(
      "application/merge-patch+json",
    );
  });
});

describe("request — method handling", () => {
  test("defaults to GET when no method is given", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { path: "/x" });
    expect(seenInit.method).toBe("GET");
  });

  test("uppercases a lowercase method", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { method: "post", path: "/x" });
    expect(seenInit.method).toBe("POST");
  });
});

describe("request — AbortSignal", () => {
  test("forwards the caller's signal to fetch untouched", async () => {
    let seenInit;
    const controller = new AbortController();
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, {
      method: "GET",
      path: "/x",
      signal: controller.signal,
    });
    expect(seenInit.signal).toBe(controller.signal);
  });

  test("omits signal from fetch init when none is given", async () => {
    let seenInit;
    const config = configWithFetch(async (url, init) => {
      seenInit = init;
      return fakeResponse({ status: 204 });
    });
    await request(config, { method: "GET", path: "/x" });
    expect(seenInit.signal).toBeUndefined();
  });
});

describe("request — auth adapter", () => {
  test("merges the auth adapter's header patch over caller headers", async () => {
    let seenInit;
    const auth = {
      authorize: async () => ({ headers: { "X-CSRF-Token": "tok" } }),
    };
    const config = configWithFetch(
      async (url, init) => {
        seenInit = init;
        return fakeResponse({ status: 204 });
      },
      { auth },
    );
    await request(config, {
      method: "POST",
      path: "/x",
      headers: { "X-Custom": "1" },
    });
    expect(seenInit.headers["X-CSRF-Token"]).toBe("tok");
    expect(seenInit.headers["X-Custom"]).toBe("1");
  });

  test("adapter headers win over caller-supplied headers of the same name", async () => {
    let seenInit;
    const auth = {
      authorize: async () => ({ headers: { "X-Custom": "from-auth" } }),
    };
    const config = configWithFetch(
      async (url, init) => {
        seenInit = init;
        return fakeResponse({ status: 204 });
      },
      { auth },
    );
    await request(config, {
      method: "GET",
      path: "/x",
      headers: { "X-Custom": "from-caller" },
    });
    expect(seenInit.headers["X-Custom"]).toBe("from-auth");
  });

  test("honors a credentials patch from the auth adapter", async () => {
    let seenInit;
    const auth = { authorize: async () => ({ credentials: "include" }) };
    const config = configWithFetch(
      async (url, init) => {
        seenInit = init;
        return fakeResponse({ status: 204 });
      },
      { auth },
    );
    await request(config, { method: "GET", path: "/x" });
    expect(seenInit.credentials).toBe("include");
  });
});

describe("request — response decoding", () => {
  test("204 decodes to null", async () => {
    const config = configWithFetch(async () => fakeResponse({ status: 204 }));
    expect(await request(config, { method: "DELETE", path: "/x" })).toBeNull();
  });

  test("empty body decodes to null even with a 200 status", async () => {
    const config = configWithFetch(async () =>
      fakeResponse({ status: 200, text: "" }),
    );
    expect(await request(config, { method: "GET", path: "/x" })).toBeNull();
  });

  test("application/json is parsed", async () => {
    const config = configWithFetch(async () =>
      fakeResponse({
        status: 200,
        headers: { "content-type": "application/json" },
        text: JSON.stringify({ ok: true }),
      }),
    );
    expect(await request(config, { method: "GET", path: "/x" })).toEqual({
      ok: true,
    });
  });

  test("a json content-type with a charset parameter is still parsed as JSON", async () => {
    const config = configWithFetch(async () =>
      fakeResponse({
        status: 200,
        headers: { "content-type": "application/json; charset=utf-8" },
        text: JSON.stringify({ ok: true }),
      }),
    );
    expect(await request(config, { method: "GET", path: "/x" })).toEqual({
      ok: true,
    });
  });

  test("non-JSON content-type decodes to text", async () => {
    const config = configWithFetch(async () =>
      fakeResponse({
        status: 200,
        headers: { "content-type": "text/plain" },
        text: "hello",
      }),
    );
    expect(await request(config, { method: "GET", path: "/x" })).toBe("hello");
  });

  test("raw: true resolves with the untouched Response", async () => {
    const response = fakeResponse({ status: 200, text: "hi" });
    const config = configWithFetch(async () => response);
    expect(
      await request(config, { method: "GET", path: "/x", raw: true }),
    ).toBe(response);
  });
});

describe("request — errors", () => {
  test("a non-2xx response throws a MittoApiError built from the body", async () => {
    const config = configWithFetch(async () =>
      fakeResponse({
        status: 409,
        headers: { "content-type": "application/json" },
        text: JSON.stringify({
          error: { code: "conflict", message: "already exists" },
        }),
      }),
    );
    await expect(
      request(config, { method: "POST", path: "/x" }),
    ).rejects.toMatchObject({
      status: 409,
      code: "conflict",
      message: "already exists",
    });
  });

  test("401 throws a MittoAuthError and fires onUnauthorized exactly once", async () => {
    let calls = 0;
    let received;
    const config = configWithFetch(
      async () => fakeResponse({ status: 401, text: "" }),
      {
        onUnauthorized: (err) => {
          calls++;
          received = err;
        },
      },
    );
    await expect(
      request(config, { method: "GET", path: "/x" }),
    ).rejects.toBeInstanceOf(MittoAuthError);
    expect(calls).toBe(1);
    expect(received).toBeInstanceOf(MittoAuthError);
  });

  test("403 throws a MittoAuthError but does not fire onUnauthorized", async () => {
    let calls = 0;
    const config = configWithFetch(
      async () => fakeResponse({ status: 403, text: "" }),
      {
        onUnauthorized: () => {
          calls++;
        },
      },
    );
    await expect(
      request(config, { method: "GET", path: "/x" }),
    ).rejects.toBeInstanceOf(MittoAuthError);
    expect(calls).toBe(0);
  });

  test("500 throws a plain MittoApiError, not a MittoAuthError", async () => {
    const config = configWithFetch(async () =>
      fakeResponse({ status: 500, text: "" }),
    );
    let error;
    try {
      await request(config, { method: "GET", path: "/x" });
    } catch (e) {
      error = e;
    }
    expect(error).toBeInstanceOf(MittoApiError);
    expect(error).not.toBeInstanceOf(MittoAuthError);
  });

  test("a rejecting fetch is wrapped in a MittoNetworkError with the cause preserved", async () => {
    const cause = new TypeError("network down");
    const config = configWithFetch(async () => {
      throw cause;
    });
    let error;
    try {
      await request(config, { method: "GET", path: "/x" });
    } catch (e) {
      error = e;
    }
    expect(error).toBeInstanceOf(MittoNetworkError);
    expect(error.cause).toBe(cause);
  });

  test("an aborted fetch is wrapped in a MittoNetworkError", async () => {
    const abortError = new DOMException("aborted", "AbortError");
    const config = configWithFetch(async () => {
      throw abortError;
    });
    let error;
    try {
      await request(config, {
        method: "GET",
        path: "/x",
        signal: new AbortController().signal,
      });
    } catch (e) {
      error = e;
    }
    expect(error).toBeInstanceOf(MittoNetworkError);
    expect(error.cause).toBe(abortError);
    expect(error.cause.name).toBe("AbortError");
  });
});
