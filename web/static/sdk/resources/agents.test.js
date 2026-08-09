/**
 * Unit tests for the SDK agents resource module (mitto-7gta.9).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createAgentsResource } from "./agents.js";

const mk = resourceMounter((config) => ({ agents: createAgentsResource(config) }));

describe("agents resource", () => {
  test("types() calls GET /api/agents/types", async () => {
    const { agents, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { agent_types: ["auggie"] } }));
    const result = await agents.types();
    expect(calls[0].url).toBe("/api/agents/types");
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual({ agent_types: ["auggie"] });
  });

  test("scan() calls POST /api/agents/scan with no body", async () => {
    const { agents, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: [] }));
    const result = await agents.scan();
    expect(calls[0].url).toBe("/api/agents/scan");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBeUndefined();
    expect(result).toEqual([]);
  });

  test("confirm(agentsList) POSTs {agents} to /api/agents/confirm", async () => {
    const { agents, calls } = mk();
    const list = [{ name: "n", command: "c", dir_name: "auggie" }];
    await agents.confirm(list);
    expect(calls[0].url).toBe("/api/agents/confirm");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify({ agents: list }));
    expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { agents, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 403,
          body: { error: { code: "forbidden", message: "Configuration is read-only" } },
        }),
      );
      await expect(agents.confirm([])).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { agents, calls } = mk({ apiPrefix: "/mitto" });
      await agents.types();
      expect(calls[0].url).toBe("/mitto/api/agents/types");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { agents, calls } = mk();
      const controller = new AbortController();
      await agents.types({ signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
