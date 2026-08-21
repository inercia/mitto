/**
 * Unit tests for the SDK acpServers resource module (mitto-7gta.9).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createAcpServersResource } from "./acp-servers.js";

const mk = resourceMounter((config) => ({ acpServers: createAcpServersResource(config) }));

describe("acpServers resource", () => {
  test("prepareDelete(name) calls GET /api/acp-servers/{name}/prepare-delete, encoding special chars", async () => {
    const { acpServers, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { server: "srv a/b", has_active: false, folders: [] } }));
    const result = await acpServers.prepareDelete("srv a/b");
    expect(calls[0].url).toBe("/api/acp-servers/srv%20a%2Fb/prepare-delete");
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual({ server: "srv a/b", has_active: false, folders: [] });
  });

  test("reassignAndDelete(name, body) POSTs {folders} to .../reassign-and-delete", async () => {
    const { acpServers, calls } = mk();
    const body = { folders: { "/tmp/ws": "other-server" } };
    await acpServers.reassignAndDelete("srv", body);
    expect(calls[0].url).toBe("/api/acp-servers/srv/reassign-and-delete");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
    expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
  });

  test("reassignAndDelete(name, body) resolves with the reassignment summary", async () => {
    const { acpServers, respondWith } = mk();
    const summary = {
      server: "srv",
      reassigned_workspaces: ["w1"],
      reassigned_conversation_count: 1,
      deleted_conversation_count: 0,
      reassigned_workspace_count: 1,
      deleted_workspace_count: 0,
    };
    respondWith(() => fakeResponse({ body: summary }));
    const result = await acpServers.reassignAndDelete("srv", { folders: {} });
    expect(result).toEqual(summary);
  });

  test("reassignAndDelete surfaces a 409 (active_session_ids) as MittoApiError when active conversations block deletion", async () => {
    const { acpServers, respondWith } = mk();
    respondWith(() =>
      fakeResponse({
        status: 409,
        body: {
          error: {
            code: "conflict",
            message: "Cannot delete server: active conversations are using it",
            details: { active_session_ids: ["c1", "c2"] },
          },
        },
      }),
    );
    let caught;
    try {
      await acpServers.reassignAndDelete("srv", { folders: {} });
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(MittoApiError);
    expect(caught.status).toBe(409);
    expect(caught.details).toEqual({ active_session_ids: ["c1", "c2"] });
  });

  test("reassignAndDelete surfaces a 403 as MittoApiError when the config is read-only", async () => {
    const { acpServers, respondWith } = mk();
    respondWith(() =>
      fakeResponse({
        status: 403,
        body: { error: { code: "forbidden", message: "Configuration is read-only (loaded from config file)" } },
      }),
    );
    await expect(acpServers.reassignAndDelete("srv", { folders: {} })).rejects.toThrow(MittoApiError);
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { acpServers, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 404,
          body: { error: { code: "not_found", message: "ACP server not found: x" } },
        }),
      );
      await expect(acpServers.prepareDelete("x")).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { acpServers, calls } = mk({ apiPrefix: "/mitto" });
      await acpServers.prepareDelete("srv");
      expect(calls[0].url).toBe("/mitto/api/acp-servers/srv/prepare-delete");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { acpServers, calls } = mk();
      const controller = new AbortController();
      await acpServers.prepareDelete("srv", { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
