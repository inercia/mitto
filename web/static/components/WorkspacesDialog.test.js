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

// ---------------------------------------------------------------------------
// Processor argument argument helpers — duplicated from WorkspacesDialog.js
// for unit testing (the component cannot be directly imported under jsdom).
// Keep in sync with the implementation in WorkspacesDialog.js.
// ---------------------------------------------------------------------------

/**
 * Computes the displayed/edited value for a single parameter.
 * Mirrors the expression used in the parameters map inside the render:
 *   (processorArgEdits[proc.name] || {})[p.name] !== undefined
 *     ? (processorArgEdits[proc.name] || {})[p.name]
 *     : p.value
 */
function currentParamValue(edits, procName, param) {
  const procEdits = edits[procName] || {};
  return procEdits[param.name] !== undefined ? procEdits[param.name] : param.value;
}

/**
 * Returns true when any param's edited value differs from p.value.
 * Mirrors the isDirty check used to show/hide the Save button.
 */
function isProcessorDirty(edits, procName, parameters) {
  const procEdits = edits[procName] || {};
  return (parameters || []).some((p) => {
    const edited = procEdits[p.name];
    return edited !== undefined && edited !== p.value;
  });
}

/**
 * Builds the arguments object sent to the PUT endpoint.
 * Mirrors the args-building loop inside saveProcessorArguments.
 */
function buildSaveArgs(edits, proc) {
  const procEdits = edits[proc.name] || {};
  const args = {};
  for (const p of (proc.parameters || [])) {
    args[p.name] = procEdits[p.name] !== undefined ? procEdits[p.name] : p.value;
  }
  return args;
}

describe("processor argument display value (currentParamValue)", () => {
  const param = { name: "filename", value: "AGENTS.md" };

  test("returns p.value when no edits exist for the processor", () => {
    expect(currentParamValue({}, "proc-a", param)).toBe("AGENTS.md");
  });

  test("returns p.value when edits exist for other processor", () => {
    const edits = { "other-proc": { filename: "OTHER.md" } };
    expect(currentParamValue(edits, "proc-a", param)).toBe("AGENTS.md");
  });

  test("returns edited value when an edit exists for this param", () => {
    const edits = { "proc-a": { filename: "CLAUDE.md" } };
    expect(currentParamValue(edits, "proc-a", param)).toBe("CLAUDE.md");
  });

  test("returns edited value even when it is an empty string (clear override)", () => {
    const edits = { "proc-a": { filename: "" } };
    expect(currentParamValue(edits, "proc-a", param)).toBe("");
  });
});

describe("dirty detection (isProcessorDirty)", () => {
  const params = [
    { name: "filename", value: "AGENTS.md" },
    { name: "mode", value: "append" },
  ];

  test("not dirty when no edits", () => {
    expect(isProcessorDirty({}, "proc-a", params)).toBe(false);
  });

  test("not dirty when edit matches current value", () => {
    const edits = { "proc-a": { filename: "AGENTS.md" } };
    expect(isProcessorDirty(edits, "proc-a", params)).toBe(false);
  });

  test("dirty when one param is edited to a different value", () => {
    const edits = { "proc-a": { filename: "CLAUDE.md" } };
    expect(isProcessorDirty(edits, "proc-a", params)).toBe(true);
  });

  test("dirty when a param is edited to empty string", () => {
    const edits = { "proc-a": { filename: "" } };
    expect(isProcessorDirty(edits, "proc-a", params)).toBe(true);
  });

  test("not dirty when null parameters array", () => {
    expect(isProcessorDirty({}, "proc-a", null)).toBe(false);
  });
});

describe("buildSaveArgs (argument map for PUT endpoint)", () => {
  const proc = {
    name: "auggie-manage-rules",
    parameters: [
      { name: "filename", value: "AGENTS.md" },
      { name: "mode", value: "append" },
    ],
  };

  test("uses effective values when no edits", () => {
    const args = buildSaveArgs({}, proc);
    expect(args).toEqual({ filename: "AGENTS.md", mode: "append" });
  });

  test("uses edited value when an edit exists", () => {
    const edits = { "auggie-manage-rules": { filename: "CLAUDE.md" } };
    const args = buildSaveArgs(edits, proc);
    expect(args).toEqual({ filename: "CLAUDE.md", mode: "append" });
  });

  test("passes empty string through (clears override)", () => {
    const edits = { "auggie-manage-rules": { filename: "" } };
    const args = buildSaveArgs(edits, proc);
    expect(args).toEqual({ filename: "", mode: "append" });
  });

  test("all params edited", () => {
    const edits = { "auggie-manage-rules": { filename: "NOTES.md", mode: "prepend" } };
    const args = buildSaveArgs(edits, proc);
    expect(args).toEqual({ filename: "NOTES.md", mode: "prepend" });
  });

  test("empty parameters array produces empty args object", () => {
    const emptyProc = { name: "x", parameters: [] };
    expect(buildSaveArgs({}, emptyProc)).toEqual({});
  });
});
