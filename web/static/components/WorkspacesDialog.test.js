/**
 * Unit tests for WorkspacesDialog MCP "Copy server config" logic.
 *
 * Tests cover buildMcpServerJson: the helper that produces the clipboard
 * payload for the per-row Copy button. The payload must use the `mcpServers`
 * wrapper format accepted by the "+" Add dialog (round-trip guarantee) and
 * include only non-empty fields, with `env` included only when it has keys.
 */

/**
 * Duplicated from WorkspacesDialog.js for testing (the component imports
 * window.preact globals, so it cannot be imported directly under jsdom).
 * Keep this in sync with the implementation.
 */
const buildMcpServerJson = (srv) => {
  const cfg = {};
  if (srv.command) cfg.command = srv.command;
  if (Array.isArray(srv.args) && srv.args.length > 0) cfg.args = srv.args;
  if (srv.url) cfg.url = srv.url;
  if (srv.env && Object.keys(srv.env).length > 0) cfg.env = srv.env;
  return JSON.stringify({ mcpServers: { [srv.name]: cfg } }, null, 2);
};

describe("buildMcpServerJson", () => {
  test("wraps the server config under mcpServers keyed by name", () => {
    const out = JSON.parse(buildMcpServerJson({ name: "srv", command: "node" }));
    expect(Object.keys(out)).toEqual(["mcpServers"]);
    expect(Object.keys(out.mcpServers)).toEqual(["srv"]);
  });

  test("includes command and non-empty args", () => {
    const out = JSON.parse(
      buildMcpServerJson({ name: "srv", command: "node", args: ["server.js", "--port", "3000"] }),
    );
    expect(out.mcpServers.srv).toEqual({ command: "node", args: ["server.js", "--port", "3000"] });
  });

  test("includes env when it has keys", () => {
    const out = JSON.parse(
      buildMcpServerJson({
        name: "srv",
        command: "node",
        env: { API_KEY: "secret", DEBUG: "1" },
      }),
    );
    expect(out.mcpServers.srv.env).toEqual({ API_KEY: "secret", DEBUG: "1" });
  });

  test("omits env when it is empty", () => {
    const out = JSON.parse(buildMcpServerJson({ name: "srv", command: "node", env: {} }));
    expect(out.mcpServers.srv).not.toHaveProperty("env");
  });

  test("omits env when it is undefined", () => {
    const out = JSON.parse(buildMcpServerJson({ name: "srv", command: "node" }));
    expect(out.mcpServers.srv).not.toHaveProperty("env");
  });

  test("url-only server includes just url", () => {
    const out = JSON.parse(
      buildMcpServerJson({ name: "remote", url: "http://127.0.0.1:5757/mcp" }),
    );
    expect(out.mcpServers.remote).toEqual({ url: "http://127.0.0.1:5757/mcp" });
  });

  test("omits empty command, args, and url", () => {
    const out = JSON.parse(
      buildMcpServerJson({ name: "srv", command: "", args: [], url: "" }),
    );
    expect(out.mcpServers.srv).toEqual({});
  });

  test("produces pretty-printed JSON", () => {
    const text = buildMcpServerJson({ name: "srv", command: "node" });
    expect(text).toContain("\n");
    expect(text).toContain('  "mcpServers"');
  });

  test("round-trips: output parses back to the same server config", () => {
    const srv = {
      name: "my-server",
      command: "node",
      args: ["server.js"],
      env: { TOKEN: "abc" },
    };
    const parsed = JSON.parse(buildMcpServerJson(srv));
    expect(parsed.mcpServers["my-server"]).toEqual({
      command: "node",
      args: ["server.js"],
      env: { TOKEN: "abc" },
    });
  });
});
