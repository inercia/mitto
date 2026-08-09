/**
 * Unit tests for the SDK workspaces resource module (mitto-7gta.9).
 *
 * Mirrors sessions.test.js's style: every call is driven by an injected
 * `config.fetch` stub — never global fetch.
 */
import { MittoApiError } from "../core/errors.js";
import { fakeResponse, resourceMounter } from "../testing/fake-server.js";
import { createWorkspacesResource } from "./workspaces.js";

const mk = resourceMounter((config) => ({ workspaces: createWorkspacesResource(config) }));

describe("workspaces resource", () => {
  test("list() with no params omits the query string", async () => {
    const { workspaces, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { workspaces: [], acp_servers: [] } }));
    const result = await workspaces.list();
    expect(calls[0].url).toBe("/api/workspaces");
    expect(calls[0].init.method).toBe("GET");
    expect(result).toEqual({ workspaces: [], acp_servers: [] });
  });

  test("list({working_dir}) appends the query param", async () => {
    const { workspaces, calls } = mk();
    await workspaces.list({ working_dir: "/tmp/ws" });
    expect(calls[0].url).toBe("/api/workspaces?working_dir=%2Ftmp%2Fws");
  });

  test("create(body) POSTs JSON to /api/workspaces", async () => {
    const { workspaces, calls } = mk();
    const body = { acp_server: "auggie", working_dir: "/tmp/ws", name: "n" };
    await workspaces.create(body);
    expect(calls[0].url).toBe("/api/workspaces");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
    expect(calls[0].init.headers["Content-Type"]).toBe("application/json");
  });

  test("remove(uuid) DELETEs /api/workspaces?uuid=..., encoding special chars", async () => {
    const { workspaces, calls } = mk();
    await workspaces.remove("uuid a/b");
    // Query-param encoding goes through URLSearchParams (qs() in
    // core/transport.js), which encodes spaces as "+", unlike the
    // encodeURIComponent()-based path-segment encoding used elsewhere.
    expect(calls[0].url).toBe("/api/workspaces?uuid=uuid+a%2Fb");
    expect(calls[0].init.method).toBe("DELETE");
  });

  test("getMetadata(uuid) calls GET /api/workspaces/{uuid}/metadata", async () => {
    const { workspaces, calls } = mk();
    await workspaces.getMetadata("u1");
    expect(calls[0].url).toBe("/api/workspaces/u1/metadata");
    expect(calls[0].init.method).toBe("GET");
  });

  test("setMetadata(uuid, body) PUTs to /api/workspaces/{uuid}/metadata", async () => {
    const { workspaces, calls } = mk();
    const body = { description: "d", url: "https://x", group: "g" };
    await workspaces.setMetadata("u1", body);
    expect(calls[0].url).toBe("/api/workspaces/u1/metadata");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  test("getUserDataSchema(uuid) calls GET /api/workspaces/{uuid}/user-data-schema", async () => {
    const { workspaces, calls } = mk();
    await workspaces.getUserDataSchema("u1");
    expect(calls[0].url).toBe("/api/workspaces/u1/user-data-schema");
    expect(calls[0].init.method).toBe("GET");
  });

  test("setUserDataSchema(uuid, body) PUTs to /api/workspaces/{uuid}/user-data-schema", async () => {
    const { workspaces, calls } = mk();
    const body = { schema: [] };
    await workspaces.setUserDataSchema("u1", body);
    expect(calls[0].url).toBe("/api/workspaces/u1/user-data-schema");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  test("getEffectiveRunnerConfig(uuid) calls GET .../effective-runner-config", async () => {
    const { workspaces, calls } = mk();
    await workspaces.getEffectiveRunnerConfig("u1");
    expect(calls[0].url).toBe("/api/workspaces/u1/effective-runner-config");
    expect(calls[0].init.method).toBe("GET");
  });

  test("getAcpStatus(uuid) calls GET .../acp-status", async () => {
    const { workspaces, calls, respondWith } = mk();
    respondWith(() => fakeResponse({ body: { alive: true } }));
    const result = await workspaces.getAcpStatus("u1");
    expect(calls[0].url).toBe("/api/workspaces/u1/acp-status");
    expect(result).toEqual({ alive: true });
  });

  test("restartAcp(uuid) POSTs to .../restart-acp", async () => {
    const { workspaces, calls } = mk();
    await workspaces.restartAcp("u1");
    expect(calls[0].url).toBe("/api/workspaces/u1/restart-acp");
    expect(calls[0].init.method).toBe("POST");
  });

  test("setFolderGroup(uuid, group) PUTs {group} to .../folder-group", async () => {
    const { workspaces, calls } = mk();
    await workspaces.setFolderGroup("u1", "team-a");
    expect(calls[0].url).toBe("/api/workspaces/u1/folder-group");
    expect(calls[0].init.method).toBe("PUT");
    expect(calls[0].init.body).toBe(JSON.stringify({ group: "team-a" }));
  });

  test("listMcpTools(uuid, acpServer) calls GET .../mcp-tools?acp_server=...", async () => {
    const { workspaces, calls } = mk();
    await workspaces.listMcpTools("u1", "auggie");
    expect(calls[0].url).toBe("/api/workspaces/u1/mcp-tools?acp_server=auggie");
    expect(calls[0].init.method).toBe("GET");
  });

  test("installMcpTool(uuid, body) POSTs to .../mcp-tools/install", async () => {
    const { workspaces, calls } = mk();
    const body = { acp_server: "auggie", definition: { mcpServers: { x: {} } } };
    await workspaces.installMcpTool("u1", body);
    expect(calls[0].url).toBe("/api/workspaces/u1/mcp-tools/install");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  test("removeMcpTool(uuid, body) POSTs to .../mcp-tools/remove", async () => {
    const { workspaces, calls } = mk();
    const body = { acp_server: "auggie", name: "x" };
    await workspaces.removeMcpTool("u1", body);
    expect(calls[0].url).toBe("/api/workspaces/u1/mcp-tools/remove");
    expect(calls[0].init.method).toBe("POST");
    expect(calls[0].init.body).toBe(JSON.stringify(body));
  });

  describe("cross-cutting concerns", () => {
    test("a non-2xx response surfaces a MittoApiError", async () => {
      const { workspaces, respondWith } = mk();
      respondWith(() =>
        fakeResponse({
          status: 404,
          body: { error: { code: "not_found", message: "Workspace not found" } },
        }),
      );
      await expect(workspaces.getMetadata("missing")).rejects.toThrow(MittoApiError);
    });

    test("apiPrefix appears exactly once in the URL (no double-prefixing)", async () => {
      const { workspaces, calls } = mk({ apiPrefix: "/mitto" });
      await workspaces.list();
      expect(calls[0].url).toBe("/mitto/api/workspaces");
      expect(calls[0].url.split("/mitto").length - 1).toBe(1);
    });

    test("forwards an AbortSignal to fetch", async () => {
      const { workspaces, calls } = mk();
      const controller = new AbortController();
      await workspaces.list(undefined, { signal: controller.signal });
      expect(calls[0].init.signal).toBe(controller.signal);
    });
  });
});
