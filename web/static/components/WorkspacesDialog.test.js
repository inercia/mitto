/**
 * Unit tests for WorkspacesDialog MCP "Copy server config" logic.
 *
 * Tests cover buildMcpServerJson: the helper that produces the clipboard
 * payload for the per-row Copy button. The payload must use the `mcpServers`
 * wrapper format accepted by the "+" Add dialog (round-trip guarantee) and
 * include only non-empty fields, with `env` included only when it has keys.
 */

import { jest } from "@jest/globals";

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
    const out = JSON.parse(
      buildMcpServerJson({ name: "srv", command: "node" }),
    );
    expect(Object.keys(out)).toEqual(["mcpServers"]);
    expect(Object.keys(out.mcpServers)).toEqual(["srv"]);
  });

  test("includes command and non-empty args", () => {
    const out = JSON.parse(
      buildMcpServerJson({
        name: "srv",
        command: "node",
        args: ["server.js", "--port", "3000"],
      }),
    );
    expect(out.mcpServers.srv).toEqual({
      command: "node",
      args: ["server.js", "--port", "3000"],
    });
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
    const out = JSON.parse(
      buildMcpServerJson({ name: "srv", command: "node", env: {} }),
    );
    expect(out.mcpServers.srv).not.toHaveProperty("env");
  });

  test("omits env when it is undefined", () => {
    const out = JSON.parse(
      buildMcpServerJson({ name: "srv", command: "node" }),
    );
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
  return procEdits[param.name] !== undefined
    ? procEdits[param.name]
    : param.value;
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
  for (const p of proc.parameters || []) {
    args[p.name] =
      procEdits[p.name] !== undefined ? procEdits[p.name] : p.value;
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
    const edits = {
      "auggie-manage-rules": { filename: "NOTES.md", mode: "prepend" },
    };
    const args = buildSaveArgs(edits, proc);
    expect(args).toEqual({ filename: "NOTES.md", mode: "prepend" });
  });

  test("empty parameters array produces empty args object", () => {
    const emptyProc = { name: "x", parameters: [] };
    expect(buildSaveArgs({}, emptyProc)).toEqual({});
  });
});

// ---------------------------------------------------------------------------
// Beads upstream "prompts" — args button gating and PUT body composition.
// Mirrors the inline logic in the Pull/Push/Sync row renderer and in
// saveBeadsPromptArgs. Duplicated for jsdom-friendly unit tests.
// ---------------------------------------------------------------------------

/**
 * Mirrors promptParameters(prompt) from web/static/utils/prompts.js — returns
 * the structured parameters array for a prompt, or [] if absent/empty.
 */
function promptParameters(prompt) {
  const params = prompt?.parameters;
  if (Array.isArray(params) && params.length > 0) return params;
  return [];
}

/**
 * Mirrors the canEditArgs / argsDisabled computation for a single row of the
 * "Prompt Actions" fieldset. `selectedName` is the value of the row's <select>;
 * `prompts` is the list of enabled folder prompts; `saving` reflects
 * beadsUpstreamSaving.
 */
function computeArgsButtonState(selectedName, prompts, saving) {
  const selectedPrompt = selectedName
    ? (prompts || []).find((p) => p.name === selectedName)
    : null;
  const params = selectedPrompt ? promptParameters(selectedPrompt) : [];
  const canEditArgs = !!selectedName && params.length > 0;
  const disabled = !canEditArgs || !!saving;
  return { selectedPrompt, params, canEditArgs, disabled };
}

/**
 * Mirrors the body composition of saveBeadsPromptArgs — the full upstream body
 * includes all three prompt names + all three arg maps, with the target
 * field's map replaced by `args`.
 */
function buildSavePromptArgsBody(field, args, state) {
  return {
    upstream: "prompts",
    pull_prompt: state.pullPrompt,
    push_prompt: state.pushPrompt,
    sync_prompt: state.syncPrompt,
    pull_prompt_args:
      field === "pull_prompt" ? args : state.pullPromptArgs,
    push_prompt_args:
      field === "push_prompt" ? args : state.pushPromptArgs,
    sync_prompt_args:
      field === "sync_prompt" ? args : state.syncPromptArgs,
  };
}

describe("beads upstream args button — computeArgsButtonState", () => {
  const paramFree = { name: "sync-plain", parameters: [] };
  const paramFul = {
    name: "sync-with-args",
    parameters: [{ name: "target", type: "string" }],
  };
  const prompts = [paramFree, paramFul];

  test("disabled when no prompt is selected (empty value)", () => {
    const s = computeArgsButtonState("", prompts, false);
    expect(s.canEditArgs).toBe(false);
    expect(s.disabled).toBe(true);
    expect(s.params).toEqual([]);
  });

  test("disabled when selected prompt has no parameters", () => {
    const s = computeArgsButtonState("sync-plain", prompts, false);
    expect(s.selectedPrompt).toBe(paramFree);
    expect(s.params).toEqual([]);
    expect(s.canEditArgs).toBe(false);
    expect(s.disabled).toBe(true);
  });

  test("enabled when selected prompt declares parameters", () => {
    const s = computeArgsButtonState("sync-with-args", prompts, false);
    expect(s.selectedPrompt).toBe(paramFul);
    expect(s.params).toEqual([{ name: "target", type: "string" }]);
    expect(s.canEditArgs).toBe(true);
    expect(s.disabled).toBe(false);
  });

  test("disabled while upstream is saving even when parametrized", () => {
    const s = computeArgsButtonState("sync-with-args", prompts, true);
    expect(s.canEditArgs).toBe(true);
    expect(s.disabled).toBe(true);
  });

  test("disabled when selected name is not in the prompts list", () => {
    const s = computeArgsButtonState("missing", prompts, false);
    expect(s.selectedPrompt).toBeUndefined();
    expect(s.params).toEqual([]);
    expect(s.canEditArgs).toBe(false);
    expect(s.disabled).toBe(true);
  });
});

describe("saveBeadsPromptArgs PUT body (buildSavePromptArgsBody)", () => {
  const baseState = {
    pullPrompt: "pull-p",
    pushPrompt: "push-p",
    syncPrompt: "sync-p",
    pullPromptArgs: { a: "1" },
    pushPromptArgs: { b: "2" },
    syncPromptArgs: { c: "3" },
  };

  test("replaces pull_prompt_args, keeps the others intact", () => {
    const body = buildSavePromptArgsBody(
      "pull_prompt",
      { a: "new" },
      baseState,
    );
    expect(body).toEqual({
      upstream: "prompts",
      pull_prompt: "pull-p",
      push_prompt: "push-p",
      sync_prompt: "sync-p",
      pull_prompt_args: { a: "new" },
      push_prompt_args: { b: "2" },
      sync_prompt_args: { c: "3" },
    });
  });

  test("replaces push_prompt_args only", () => {
    const body = buildSavePromptArgsBody(
      "push_prompt",
      { b: "new" },
      baseState,
    );
    expect(body.pull_prompt_args).toEqual({ a: "1" });
    expect(body.push_prompt_args).toEqual({ b: "new" });
    expect(body.sync_prompt_args).toEqual({ c: "3" });
  });

  test("replaces sync_prompt_args only", () => {
    const body = buildSavePromptArgsBody(
      "sync_prompt",
      { c: "new" },
      baseState,
    );
    expect(body.pull_prompt_args).toEqual({ a: "1" });
    expect(body.push_prompt_args).toEqual({ b: "2" });
    expect(body.sync_prompt_args).toEqual({ c: "new" });
  });

  test("always carries all three prompt names so switching a name does not wipe args", () => {
    const body = buildSavePromptArgsBody("pull_prompt", {}, baseState);
    expect(body.pull_prompt).toBe("pull-p");
    expect(body.push_prompt).toBe("push-p");
    expect(body.sync_prompt).toBe("sync-p");
  });

  test("passes empty args map through (clears saved args)", () => {
    const body = buildSavePromptArgsBody("sync_prompt", {}, baseState);
    expect(body.sync_prompt_args).toEqual({});
  });
});

// ---------------------------------------------------------------------------
// onOpenPromptParamDialog dispatch — mirrors the args-button onClick guard.
// ---------------------------------------------------------------------------

/**
 * Mirrors the args-button onClick: bails when disabled, when no dialog opener
 * is available, or when no prompt is resolved. Otherwise forwards prompt,
 * params, an onSubmit callback, and initialValues.
 */
function dispatchArgsButtonClick({
  canEditArgs,
  onOpenPromptParamDialog,
  selectedPrompt,
  params,
  savedArgs,
  saveBeadsPromptArgs,
  field,
}) {
  if (!canEditArgs || !onOpenPromptParamDialog || !selectedPrompt) return false;
  onOpenPromptParamDialog(
    selectedPrompt,
    params,
    async (userArgs) => {
      await saveBeadsPromptArgs(field, userArgs);
    },
    { initialValues: savedArgs || {} },
  );
  return true;
}

describe("args-button onClick (dispatchArgsButtonClick)", () => {
  const prompt = {
    name: "sync-with-args",
    parameters: [{ name: "target", type: "string" }],
  };
  const params = prompt.parameters;

  test("no-op when canEditArgs is false", () => {
    const spy = jest.fn();
    const ok = dispatchArgsButtonClick({
      canEditArgs: false,
      onOpenPromptParamDialog: spy,
      selectedPrompt: prompt,
      params,
      savedArgs: {},
      saveBeadsPromptArgs: jest.fn(),
      field: "sync_prompt",
    });
    expect(ok).toBe(false);
    expect(spy).not.toHaveBeenCalled();
  });

  test("no-op when onOpenPromptParamDialog is missing", () => {
    const ok = dispatchArgsButtonClick({
      canEditArgs: true,
      onOpenPromptParamDialog: null,
      selectedPrompt: prompt,
      params,
      savedArgs: {},
      saveBeadsPromptArgs: jest.fn(),
      field: "sync_prompt",
    });
    expect(ok).toBe(false);
  });

  test("no-op when selectedPrompt is null", () => {
    const spy = jest.fn();
    const ok = dispatchArgsButtonClick({
      canEditArgs: true,
      onOpenPromptParamDialog: spy,
      selectedPrompt: null,
      params,
      savedArgs: {},
      saveBeadsPromptArgs: jest.fn(),
      field: "sync_prompt",
    });
    expect(ok).toBe(false);
    expect(spy).not.toHaveBeenCalled();
  });

  test("opens the dialog with prompt, params, onSubmit, and initialValues", () => {
    const spy = jest.fn();
    const savedArgs = { target: "prod" };
    const ok = dispatchArgsButtonClick({
      canEditArgs: true,
      onOpenPromptParamDialog: spy,
      selectedPrompt: prompt,
      params,
      savedArgs,
      saveBeadsPromptArgs: jest.fn(),
      field: "pull_prompt",
    });
    expect(ok).toBe(true);
    expect(spy).toHaveBeenCalledTimes(1);
    const [passedPrompt, passedParams, onSubmit, opts] = spy.mock.calls[0];
    expect(passedPrompt).toBe(prompt);
    expect(passedParams).toBe(params);
    expect(typeof onSubmit).toBe("function");
    expect(opts).toEqual({ initialValues: savedArgs });
  });

  test("onSubmit forwards user args to saveBeadsPromptArgs with the correct field", async () => {
    const save = jest.fn().mockResolvedValue(undefined);
    let captured = null;
    dispatchArgsButtonClick({
      canEditArgs: true,
      onOpenPromptParamDialog: (_p, _params, onSubmit) => {
        captured = onSubmit;
      },
      selectedPrompt: prompt,
      params,
      savedArgs: {},
      saveBeadsPromptArgs: save,
      field: "push_prompt",
    });
    await captured({ target: "staging" });
    expect(save).toHaveBeenCalledWith("push_prompt", { target: "staging" });
  });

  test("defaults savedArgs to {} when absent", () => {
    const spy = jest.fn();
    dispatchArgsButtonClick({
      canEditArgs: true,
      onOpenPromptParamDialog: spy,
      selectedPrompt: prompt,
      params,
      savedArgs: undefined,
      saveBeadsPromptArgs: jest.fn(),
      field: "pull_prompt",
    });
    const [, , , opts] = spy.mock.calls[0];
    expect(opts).toEqual({ initialValues: {} });
  });
});
