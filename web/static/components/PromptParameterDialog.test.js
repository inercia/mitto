/**
 * Unit tests for PromptParameterDialog render-branch logic.
 *
 * Because the component imports window.preact globals at module load, it
 * cannot be imported under jsdom. Instead the key render-branch logic is
 * duplicated here and tested directly — the same pattern used by
 * BeadsView.test.js and Message.test.js.
 */

// =============================================================================
// workspaceId render-branch logic
// Duplicated from ParamField in PromptParameterDialog.js — keep in sync.
// =============================================================================

/**
 * Mirrors the workspaceId branch of ParamField.
 * Returns a plain descriptor so tests can assert without a real DOM.
 *   { kind: "spinner" | "textInput" | "select", options?: Array<{value,label}> }
 */
function renderWorkspaceIdControl({
  loadingWorkspaces,
  workspaces,
  workingDir,
}) {
  if (loadingWorkspaces) {
    return { kind: "spinner" };
  }
  if (!workspaces || workspaces.length === 0) {
    return { kind: "textInput", placeholder: "Workspace ID" };
  }
  const options = workspaces.map((ws) => ({
    value: ws.uuid,
    label:
      (ws.name || ws.working_dir) +
      (ws.working_dir === workingDir ? " (current)" : ""),
  }));
  return { kind: "select", options };
}

// =============================================================================
// workspaceId fetch logic
// Mirrors the fetch+parse logic from the workspaces useEffect.
// =============================================================================

/**
 * Mirrors the data-extraction logic from the workspaces fetch effect.
 * Returns the array of workspaces from a parsed response body.
 */
function parseWorkspacesResponse(data) {
  return Array.isArray(data?.workspaces) ? data.workspaces : [];
}

// =============================================================================
// Tests
// =============================================================================

describe("workspaceId render branch", () => {
  describe("loading state", () => {
    test("shows spinner while loadingWorkspaces is true", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: true,
        workspaces: [],
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("spinner");
    });

    test("shows spinner even when workspaces are populated (still loading)", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: true,
        workspaces: [{ uuid: "abc", working_dir: "/foo" }],
        workingDir: "/foo",
      });
      expect(result.kind).toBe("spinner");
    });
  });

  describe("empty / unavailable workspaces list → text input fallback", () => {
    test("renders text input when workspaces is empty array", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces: [],
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("textInput");
      expect(result.placeholder).toBe("Workspace ID");
    });

    test("renders text input when workspaces is null", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces: null,
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("textInput");
    });

    test("renders text input when workspaces is undefined", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces: undefined,
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("textInput");
    });
  });

  describe("workspaces present → select dropdown", () => {
    const workspaces = [
      { uuid: "uuid-1", name: "Main Project", working_dir: "/home/user/main" },
      { uuid: "uuid-2", name: "", working_dir: "/home/user/other" },
      { uuid: "uuid-3", name: "Current", working_dir: "/home/user/current" },
    ];

    test("renders a select with one option per workspace", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/home/user/current",
      });
      expect(result.kind).toBe("select");
      expect(result.options).toHaveLength(3);
    });

    test("option value equals workspace uuid", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/home/user/current",
      });
      expect(result.options[0].value).toBe("uuid-1");
      expect(result.options[1].value).toBe("uuid-2");
      expect(result.options[2].value).toBe("uuid-3");
    });

    test("label uses name when present", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/some/other/dir",
      });
      expect(result.options[0].label).toBe("Main Project");
    });

    test("label falls back to working_dir when name is absent", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/some/other/dir",
      });
      expect(result.options[1].label).toBe("/home/user/other");
    });

    test("marks the current workspace with '(current)'", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/home/user/current",
      });
      expect(result.options[2].label).toBe("Current (current)");
    });

    test("does not mark non-current workspaces with '(current)'", () => {
      const result = renderWorkspaceIdControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/home/user/current",
      });
      expect(result.options[0].label).not.toContain("(current)");
      expect(result.options[1].label).not.toContain("(current)");
    });
  });
});

// =============================================================================
// workspaceFolder render-branch logic
// Duplicated from ParamField in PromptParameterDialog.js — keep in sync.
// =============================================================================

/**
 * Mirrors the workspaceFolder branch of ParamField (including de-duplication).
 * Returns a plain descriptor so tests can assert without a real DOM.
 *   { kind: "spinner" | "textInput" | "select", options?: Array<{value,label}> }
 */
function renderWorkspaceFolderControl({
  loadingWorkspaces,
  workspaces,
  workingDir,
}) {
  const seen = new Set();
  const folders = (workspaces || []).filter((ws) => {
    if (!ws.working_dir || seen.has(ws.working_dir)) return false;
    seen.add(ws.working_dir);
    return true;
  });
  if (loadingWorkspaces) {
    return { kind: "spinner" };
  }
  if (folders.length === 0) {
    return { kind: "textInput", placeholder: "Absolute folder path" };
  }
  const options = folders.map((ws) => ({
    value: ws.working_dir,
    label: ws.working_dir + (ws.working_dir === workingDir ? " (current)" : ""),
  }));
  return { kind: "select", options };
}

describe("workspaceFolder render branch", () => {
  describe("loading state", () => {
    test("shows spinner while loadingWorkspaces is true", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: true,
        workspaces: [],
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("spinner");
    });
  });

  describe("empty / unavailable workspaces list → text input fallback", () => {
    test("renders text input when workspaces is empty array", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces: [],
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("textInput");
      expect(result.placeholder).toBe("Absolute folder path");
    });

    test("renders text input when workspaces is null", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces: null,
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("textInput");
    });

    test("renders text input when workspaces is undefined", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces: undefined,
        workingDir: "/home/user/project",
      });
      expect(result.kind).toBe("textInput");
    });
  });

  describe("workspaces present → select dropdown", () => {
    const workspaces = [
      { uuid: "uuid-1", name: "Alpha", working_dir: "/home/user/alpha" },
      { uuid: "uuid-2", name: "Alpha ACP2", working_dir: "/home/user/alpha" },
      { uuid: "uuid-3", name: "Beta", working_dir: "/home/user/beta" },
      { uuid: "uuid-4", name: "Current", working_dir: "/home/user/current" },
    ];

    test("de-duplicates by working_dir (two workspaces sharing a dir → one option)", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/other",
      });
      expect(result.kind).toBe("select");
      expect(result.options).toHaveLength(3);
    });

    test("option value equals working_dir (the absolute path)", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/other",
      });
      expect(result.options[0].value).toBe("/home/user/alpha");
      expect(result.options[1].value).toBe("/home/user/beta");
      expect(result.options[2].value).toBe("/home/user/current");
    });

    test("label is the working_dir path", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/other",
      });
      expect(result.options[0].label).toBe("/home/user/alpha");
    });

    test("marks the current folder with '(current)'", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/home/user/current",
      });
      expect(result.options[2].label).toBe("/home/user/current (current)");
    });

    test("does not mark non-current folders with '(current)'", () => {
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces,
        workingDir: "/home/user/current",
      });
      expect(result.options[0].label).not.toContain("(current)");
      expect(result.options[1].label).not.toContain("(current)");
    });

    test("skips entries with missing working_dir", () => {
      const sparse = [
        { uuid: "a", working_dir: "/valid/path" },
        { uuid: "b", working_dir: "" },
        { uuid: "c", working_dir: null },
      ];
      const result = renderWorkspaceFolderControl({
        loadingWorkspaces: false,
        workspaces: sparse,
        workingDir: "/other",
      });
      expect(result.options).toHaveLength(1);
      expect(result.options[0].value).toBe("/valid/path");
    });
  });
});

// =============================================================================
// childSessionId render-branch logic
// Duplicated from ParamField in PromptParameterDialog.js — keep in sync.
// =============================================================================

/**
 * Mirrors the childSessionId branch of ParamField.
 * Returns a plain descriptor so tests can assert without a real DOM.
 *   { kind: "spinner" | "textInput" | "select", options?: Array<{value,label}> }
 */
function renderChildSessionIdControl({
  loadingSessions,
  sessions,
  hostSessionId,
}) {
  const childSessions = (sessions || []).filter(
    (s) => hostSessionId && s.parent_session_id === hostSessionId,
  );
  if (loadingSessions) {
    return { kind: "spinner" };
  }
  if (childSessions.length === 0) {
    return { kind: "textInput", placeholder: "Child conversation ID" };
  }
  const options = childSessions.map((s) => ({
    value: s.session_id,
    // Mirror the sidebar's canonical display priority (SessionItem.js): the
    // user-set `name` first, then the auto-generated `description`, and
    // finally the opaque session_id as a last-ditch fallback.
    label: s.name || s.description || s.session_id,
  }));
  return { kind: "select", options };
}

/**
 * Mirrors the childSessions filter logic.
 */
function filterChildSessions(sessions, hostSessionId) {
  return (sessions || []).filter(
    (s) => hostSessionId && s.parent_session_id === hostSessionId,
  );
}

describe("childSessionId render branch", () => {
  describe("loading state", () => {
    test("shows spinner while loadingSessions is true", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: true,
        sessions: [],
        hostSessionId: "host-1",
      });
      expect(result.kind).toBe("spinner");
    });
  });

  describe("text input fallback", () => {
    test("renders text input when hostSessionId is undefined (even if sessions exist)", () => {
      const sessions = [
        { session_id: "child-1", name: "Child", parent_session_id: "host-1" },
      ];
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions,
        hostSessionId: undefined,
      });
      expect(result.kind).toBe("textInput");
      expect(result.placeholder).toBe("Child conversation ID");
    });

    test("renders text input when no session matches the host", () => {
      const sessions = [
        {
          session_id: "child-1",
          name: "Child",
          parent_session_id: "other-host",
        },
      ];
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions,
        hostSessionId: "host-1",
      });
      expect(result.kind).toBe("textInput");
    });

    test("renders text input when sessions is empty", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions: [],
        hostSessionId: "host-1",
      });
      expect(result.kind).toBe("textInput");
    });
  });

  describe("select dropdown when matches exist", () => {
    const sessions = [
      // `name` set → wins outright.
      { session_id: "child-1", name: "Alpha", parent_session_id: "host-1" },
      // No `name`, no `description` → falls all the way through to session_id.
      { session_id: "child-2", name: "", parent_session_id: "host-1" },
      {
        session_id: "child-3",
        name: "Other",
        parent_session_id: "other-host",
      },
    ];

    test("renders select with only children of the host", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions,
        hostSessionId: "host-1",
      });
      expect(result.kind).toBe("select");
      expect(result.options).toHaveLength(2);
    });

    test("option value equals session_id", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions,
        hostSessionId: "host-1",
      });
      expect(result.options[0].value).toBe("child-1");
      expect(result.options[1].value).toBe("child-2");
    });

    test("label prefers name, then description, then session_id", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions,
        hostSessionId: "host-1",
      });
      expect(result.options[0].label).toBe("Alpha");
      expect(result.options[1].label).toBe("child-2");
    });

    test("label falls back to description when name is missing", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions: [
          {
            session_id: "child-9",
            description: "auto-generated summary",
            parent_session_id: "host-1",
          },
        ],
        hostSessionId: "host-1",
      });
      expect(result.options[0].label).toBe("auto-generated summary");
    });
  });
});

describe("filterChildSessions", () => {
  const sessions = [
    { session_id: "c1", parent_session_id: "host-1" },
    { session_id: "c2", parent_session_id: "host-1" },
    { session_id: "c3", parent_session_id: "host-2" },
  ];

  test("returns only children of the given host", () => {
    expect(filterChildSessions(sessions, "host-1")).toHaveLength(2);
    expect(filterChildSessions(sessions, "host-2")).toHaveLength(1);
  });

  test("returns empty array when no children match", () => {
    expect(filterChildSessions(sessions, "host-99")).toHaveLength(0);
  });

  test("returns empty array when hostSessionId is undefined", () => {
    expect(filterChildSessions(sessions, undefined)).toHaveLength(0);
  });

  test("returns empty array when sessions is empty", () => {
    expect(filterChildSessions([], "host-1")).toHaveLength(0);
  });

  test("handles null sessions gracefully", () => {
    expect(filterChildSessions(null, "host-1")).toHaveLength(0);
  });
});

// =============================================================================
// acpServer render-branch logic
// Duplicated from ParamField in PromptParameterDialog.js — keep in sync.
// =============================================================================

/**
 * Mirrors the acpServer branch of ParamField.
 * Returns a plain descriptor so tests can assert without a real DOM.
 *   { kind: "spinner" | "textInput" | "select", options?: Array<{value,label}> }
 */
function renderAcpServerControl({ loadingWorkspaces, acpServers }) {
  if (loadingWorkspaces) {
    return { kind: "spinner" };
  }
  if (!acpServers || acpServers.length === 0) {
    return { kind: "textInput", placeholder: "Agent (ACP server) name" };
  }
  const options = acpServers.map((s) => ({ value: s.name, label: s.name }));
  return { kind: "select", options };
}

/**
 * Mirrors the acp_servers extraction from the workspaces fetch effect.
 */
function parseAcpServersResponse(data) {
  return Array.isArray(data?.acp_servers) ? data.acp_servers : [];
}

describe("acpServer render branch", () => {
  describe("loading state", () => {
    test("shows spinner while loadingWorkspaces is true", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: true,
        acpServers: [],
      });
      expect(result.kind).toBe("spinner");
    });

    test("shows spinner even when acpServers are populated (still loading)", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: true,
        acpServers: [{ name: "auggie" }],
      });
      expect(result.kind).toBe("spinner");
    });
  });

  describe("empty / unavailable list → text input fallback", () => {
    test("renders text input when acpServers is empty array", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: false,
        acpServers: [],
      });
      expect(result.kind).toBe("textInput");
      expect(result.placeholder).toBe("Agent (ACP server) name");
    });

    test("renders text input when acpServers is null", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: false,
        acpServers: null,
      });
      expect(result.kind).toBe("textInput");
    });

    test("renders text input when acpServers is undefined", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: false,
        acpServers: undefined,
      });
      expect(result.kind).toBe("textInput");
    });
  });

  describe("servers present → select dropdown", () => {
    const acpServers = [
      { name: "auggie", command: "auggie --acp" },
      { name: "claude-code", command: "claude --acp" },
    ];

    test("renders a select with one option per server", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: false,
        acpServers,
      });
      expect(result.kind).toBe("select");
      expect(result.options).toHaveLength(2);
    });

    test("option value and label both equal the server name", () => {
      const result = renderAcpServerControl({
        loadingWorkspaces: false,
        acpServers,
      });
      expect(result.options[0].value).toBe("auggie");
      expect(result.options[0].label).toBe("auggie");
      expect(result.options[1].value).toBe("claude-code");
      expect(result.options[1].label).toBe("claude-code");
    });
  });
});

describe("parseAcpServersResponse", () => {
  test("extracts acp_servers array from valid response", () => {
    const data = {
      workspaces: [],
      acp_servers: [{ name: "auggie" }, { name: "claude-code" }],
    };
    expect(parseAcpServersResponse(data)).toHaveLength(2);
    expect(parseAcpServersResponse(data)[0].name).toBe("auggie");
  });

  test("returns empty array when acp_servers key is missing", () => {
    expect(parseAcpServersResponse({})).toEqual([]);
  });

  test("returns empty array when data is null", () => {
    expect(parseAcpServersResponse(null)).toEqual([]);
  });

  test("returns empty array when data is undefined", () => {
    expect(parseAcpServersResponse(undefined)).toEqual([]);
  });

  test("returns empty array when acp_servers value is not an array", () => {
    expect(parseAcpServersResponse({ acp_servers: null })).toEqual([]);
    expect(parseAcpServersResponse({ acp_servers: "oops" })).toEqual([]);
  });
});

describe("parseWorkspacesResponse", () => {
  test("extracts workspaces array from valid response", () => {
    const data = {
      workspaces: [{ uuid: "abc", working_dir: "/foo" }],
      acp_servers: [],
    };
    expect(parseWorkspacesResponse(data)).toHaveLength(1);
    expect(parseWorkspacesResponse(data)[0].uuid).toBe("abc");
  });

  test("returns empty array when workspaces key is missing", () => {
    expect(parseWorkspacesResponse({})).toEqual([]);
  });

  test("returns empty array when data is null", () => {
    expect(parseWorkspacesResponse(null)).toEqual([]);
  });

  test("returns empty array when data is undefined", () => {
    expect(parseWorkspacesResponse(undefined)).toEqual([]);
  });

  test("returns empty array when workspaces value is not an array", () => {
    expect(parseWorkspacesResponse({ workspaces: null })).toEqual([]);
    expect(parseWorkspacesResponse({ workspaces: "oops" })).toEqual([]);
  });
});

// =============================================================================
// boolean render-branch + submit/save logic
// Duplicated from ParamField / handleSubmit / canSave in
// PromptParameterDialog.js — keep in sync.
// =============================================================================

/**
 * Mirrors the boolean branch of ParamField: the checkbox `checked` state.
 * value is a JS boolean (true), the string "true", or anything falsy/unset.
 */
function booleanCheckboxChecked(value) {
  return value === true || value === "true";
}

/**
 * Mirrors the boolean handling in handleSubmit: always emit a definite
 * "true"/"false" string (default unchecked = "false").
 */
function serializeBooleanArg(value) {
  return value === true || value === "true" ? "true" : "false";
}

/**
 * Mirrors the canSave filter: required params count toward Save-enablement
 * EXCEPT booleans (a checkbox always has a definite answer).
 */
function canSave(parameters, values) {
  return parameters
    .filter((p) => p.required && p.type !== "boolean")
    .every((p) => (values[p.name] || "").trim() !== "");
}

describe("boolean checkbox state", () => {
  test("checked when value is JS boolean true", () => {
    expect(booleanCheckboxChecked(true)).toBe(true);
  });

  test("checked when value is the string 'true'", () => {
    expect(booleanCheckboxChecked("true")).toBe(true);
  });

  test("unchecked when value is unset / empty / false", () => {
    expect(booleanCheckboxChecked(undefined)).toBe(false);
    expect(booleanCheckboxChecked("")).toBe(false);
    expect(booleanCheckboxChecked(false)).toBe(false);
    expect(booleanCheckboxChecked("false")).toBe(false);
  });
});

describe("serializeBooleanArg (handleSubmit boolean handling)", () => {
  test("checked boolean → 'true'", () => {
    expect(serializeBooleanArg(true)).toBe("true");
    expect(serializeBooleanArg("true")).toBe("true");
  });

  test("unchecked / unset → 'false'", () => {
    expect(serializeBooleanArg(false)).toBe("false");
    expect(serializeBooleanArg("")).toBe("false");
    expect(serializeBooleanArg(undefined)).toBe("false");
  });
});

describe("canSave with boolean params", () => {
  test("a required boolean does NOT block Save (always answered)", () => {
    const parameters = [{ name: "Commit", type: "boolean", required: true }];
    // No value set at all → still saveable, default is unchecked/false
    expect(canSave(parameters, {})).toBe(true);
  });

  test("a required text param still blocks Save until filled", () => {
    const parameters = [
      { name: "Commit", type: "boolean", required: true },
      { name: "Note", type: "text", required: true },
    ];
    expect(canSave(parameters, {})).toBe(false);
    expect(canSave(parameters, { Note: "hello" })).toBe(true);
  });
});

// =============================================================================
// initialValues seeding logic
// Mirrors the reset effect in PromptParameterDialog.js: when the dialog opens,
// values are seeded from initialValues (if provided) rather than starting empty.
// =============================================================================

/**
 * Mirrors the reset effect: returns the initial values map that should be set
 * when the dialog opens.
 */
function seedValues(initialValues) {
  return initialValues ? { ...initialValues } : {};
}

/**
 * Mirrors handleSubmit: applies any per-parameter transformations (boolean
 * serialization) and omits undefined keys. Returns the final args map.
 */
function buildSubmitArgs(parameters, values) {
  const args = {};
  for (const p of parameters) {
    if (p.type === "boolean") {
      args[p.name] = serializeBooleanArg(values[p.name]);
    } else {
      args[p.name] = values[p.name] || "";
    }
  }
  return args;
}

describe("initialValues seeding", () => {
  test("seeds text field from initialValues when dialog opens", () => {
    const initialValues = { FOO: "bar" };
    const seeded = seedValues(initialValues);
    expect(seeded).toEqual({ FOO: "bar" });
  });

  test("text field value reflects seeded initialValue", () => {
    const parameters = [{ name: "FOO", type: "text", required: true }];
    const initialValues = { FOO: "bar" };
    const seeded = seedValues(initialValues);
    // The seeded value for FOO should match
    expect(seeded["FOO"]).toBe("bar");
    // canSave should be true because the required field is pre-filled
    expect(canSave(parameters, seeded)).toBe(true);
  });

  test("submitting with seeded+edited value calls onSubmit with edited value", () => {
    const parameters = [{ name: "FOO", type: "text", required: true }];
    const initialValues = { FOO: "bar" };
    // Simulate: seed then user edits to "baz"
    const values = { ...seedValues(initialValues), FOO: "baz" };
    const args = buildSubmitArgs(parameters, values);
    expect(args).toEqual({ FOO: "baz" });
  });

  test("boolean field seeded as string 'true' is checked", () => {
    const initialValues = { Flag: "true" };
    const seeded = seedValues(initialValues);
    // ParamField reads value and treats "true" as checked
    expect(booleanCheckboxChecked(seeded["Flag"])).toBe(true);
  });

  test("boolean field seeded as string 'false' is unchecked", () => {
    const initialValues = { Flag: "false" };
    const seeded = seedValues(initialValues);
    expect(booleanCheckboxChecked(seeded["Flag"])).toBe(false);
  });

  test("submitting with seeded boolean 'true' emits 'true'", () => {
    const parameters = [{ name: "Flag", type: "boolean" }];
    const seeded = seedValues({ Flag: "true" });
    const args = buildSubmitArgs(parameters, seeded);
    expect(args["Flag"]).toBe("true");
  });

  test("empty initialValues produces empty seed", () => {
    expect(seedValues({})).toEqual({});
  });

  test("null initialValues produces empty seed (no crash)", () => {
    expect(seedValues(null)).toEqual({});
  });

  test("seeded values are a copy (mutations don't affect original)", () => {
    const original = { FOO: "bar" };
    const seeded = seedValues(original);
    seeded["FOO"] = "mutated";
    expect(original["FOO"]).toBe("bar");
  });
});

// =============================================================================
// text render-branch logic (single-line input vs multiLine textarea)
// Duplicated from the `text` branch of ParamField in PromptParameterDialog.js —
// keep in sync.
// =============================================================================

/**
 * Mirrors the `text` branch of ParamField: a dropdown when options is a
 * non-empty array, otherwise a single-line input by default or a resizable
 * textarea when multiLine is true. options wins over multiLine when both are
 * set (defensive — backend validation rejects that combination).
 *   { kind: "input" | "textarea" | "select", options?: string[] }
 */
function renderTextControl({ multiLine, options }) {
  if (Array.isArray(options) && options.length > 0) {
    return { kind: "select", options };
  }
  return multiLine ? { kind: "textarea" } : { kind: "input" };
}

describe("text render branch (multiLine)", () => {
  test("renders a single-line input when multiLine is absent", () => {
    expect(renderTextControl({}).kind).toBe("input");
  });

  test("renders a single-line input when multiLine is false", () => {
    expect(renderTextControl({ multiLine: false }).kind).toBe("input");
  });

  test("renders a textarea when multiLine is true", () => {
    expect(renderTextControl({ multiLine: true }).kind).toBe("textarea");
  });
});

describe("text render branch (options dropdown)", () => {
  test("renders a select when options is a non-empty array", () => {
    const result = renderTextControl({ options: ["Simplification", "Cleanup"] });
    expect(result.kind).toBe("select");
    expect(result.options).toEqual(["Simplification", "Cleanup"]);
  });

  test("falls back to input when options is an empty array", () => {
    expect(renderTextControl({ options: [] }).kind).toBe("input");
  });

  test("falls back to input when options is absent", () => {
    expect(renderTextControl({}).kind).toBe("input");
  });

  test("falls back to input when options is not an array", () => {
    expect(renderTextControl({ options: "a,b" }).kind).toBe("input");
    expect(renderTextControl({ options: null }).kind).toBe("input");
  });

  test("select wins over multiLine when both are set", () => {
    const result = renderTextControl({
      multiLine: true,
      options: ["a", "b"],
    });
    expect(result.kind).toBe("select");
  });
});

// =============================================================================
// prompts render-branch logic
// Duplicated from the `prompts` branch of ParamField in PromptParameterDialog.js —
// keep in sync.
// =============================================================================

/**
 * Mirrors the `prompts` branch of ParamField.
 *   { kind: "spinner" | "textInput" | "select", options?: Array<{value,label}> }
 */
function renderPromptsControl({ loadingPrompts, promptsList }) {
  if (loadingPrompts) {
    return { kind: "spinner" };
  }
  if (!promptsList || promptsList.length === 0) {
    return { kind: "textInput", placeholder: "Prompt name" };
  }
  const options = promptsList.map((p) => ({ value: p.name, label: p.name }));
  return { kind: "select", options };
}

describe("prompts render branch", () => {
  describe("loading state", () => {
    test("shows spinner while loadingPrompts is true", () => {
      const result = renderPromptsControl({
        loadingPrompts: true,
        promptsList: [],
      });
      expect(result.kind).toBe("spinner");
    });

    test("shows spinner even when promptsList is populated (still loading)", () => {
      const result = renderPromptsControl({
        loadingPrompts: true,
        promptsList: [{ name: "foo" }],
      });
      expect(result.kind).toBe("spinner");
    });
  });

  describe("empty / unavailable prompts list → text input fallback", () => {
    test("renders text input with 'Prompt name' placeholder when promptsList is empty", () => {
      const result = renderPromptsControl({
        loadingPrompts: false,
        promptsList: [],
      });
      expect(result.kind).toBe("textInput");
      expect(result.placeholder).toBe("Prompt name");
    });

    test("renders text input when promptsList is null", () => {
      const result = renderPromptsControl({
        loadingPrompts: false,
        promptsList: null,
      });
      expect(result.kind).toBe("textInput");
    });

    test("renders text input when promptsList is undefined", () => {
      const result = renderPromptsControl({
        loadingPrompts: false,
        promptsList: undefined,
      });
      expect(result.kind).toBe("textInput");
    });
  });

  describe("populated list → select of prompt names", () => {
    test("renders one option per prompt, using name as both value and label", () => {
      const result = renderPromptsControl({
        loadingPrompts: false,
        promptsList: [{ name: "alpha" }, { name: "beta" }, { name: "gamma" }],
      });
      expect(result.kind).toBe("select");
      expect(result.options).toEqual([
        { value: "alpha", label: "alpha" },
        { value: "beta", label: "beta" },
        { value: "gamma", label: "gamma" },
      ]);
    });

    test("preserves declared order in options", () => {
      const result = renderPromptsControl({
        loadingPrompts: false,
        promptsList: [{ name: "zeta" }, { name: "alpha" }],
      });
      expect(result.options.map((o) => o.value)).toEqual(["zeta", "alpha"]);
    });
  });
});

// =============================================================================
// filename render-branch logic (mitto-vlg)
// Duplicated from ParamField in PromptParameterDialog.js — keep in sync.
// =============================================================================

/**
 * Mirrors the filename branch of ParamField.
 * Returns a plain descriptor so tests can assert without a real DOM.
 *   { kind: "spinner" | "textInput" | "select", options?: Array<{value,label}> }
 */
function renderFilenameControl({ param, filesByParam, loadingFilesByParam }) {
  const name = param.name;
  const files = (filesByParam && filesByParam[name]) || [];
  const loadingFiles = !!(
    loadingFilesByParam && loadingFilesByParam[name]
  );
  if (loadingFiles) {
    return { kind: "spinner" };
  }
  if (files.length === 0) {
    return { kind: "textInput", placeholder: "Workspace-relative file path" };
  }
  const options = files.map((path) => ({ value: path, label: path }));
  return { kind: "select", options };
}

describe("filename render branch", () => {
  describe("loading state", () => {
    test("shows spinner when this param's loading flag is true", () => {
      const result = renderFilenameControl({
        param: { name: "F", type: "filename" },
        filesByParam: {},
        loadingFilesByParam: { F: true },
      });
      expect(result.kind).toBe("spinner");
    });

    test("does NOT show spinner when a different param is loading", () => {
      const result = renderFilenameControl({
        param: { name: "F", type: "filename" },
        filesByParam: {},
        loadingFilesByParam: { G: true },
      });
      // F is not loading and has no files → text-input fallback.
      expect(result.kind).toBe("textInput");
    });
  });

  describe("empty / unavailable files list → text input fallback", () => {
    test("renders text input with workspace-relative placeholder when list is empty", () => {
      const result = renderFilenameControl({
        param: { name: "F", type: "filename" },
        filesByParam: { F: [] },
        loadingFilesByParam: { F: false },
      });
      expect(result.kind).toBe("textInput");
      expect(result.placeholder).toBe("Workspace-relative file path");
    });

    test("renders text input when this param has no entry in filesByParam", () => {
      const result = renderFilenameControl({
        param: { name: "F", type: "filename" },
        filesByParam: { G: ["docs/g.md"] },
        loadingFilesByParam: {},
      });
      expect(result.kind).toBe("textInput");
    });

    test("renders text input when filesByParam is null/undefined", () => {
      expect(
        renderFilenameControl({
          param: { name: "F", type: "filename" },
          filesByParam: null,
          loadingFilesByParam: null,
        }).kind,
      ).toBe("textInput");
      expect(
        renderFilenameControl({
          param: { name: "F", type: "filename" },
          filesByParam: undefined,
          loadingFilesByParam: undefined,
        }).kind,
      ).toBe("textInput");
    });
  });

  describe("populated list → select of file paths", () => {
    test("renders one option per file, using path as both value and label", () => {
      const result = renderFilenameControl({
        param: { name: "F", type: "filename" },
        filesByParam: { F: ["docs/a.md", "docs/b.md"] },
        loadingFilesByParam: { F: false },
      });
      expect(result.kind).toBe("select");
      expect(result.options).toEqual([
        { value: "docs/a.md", label: "docs/a.md" },
        { value: "docs/b.md", label: "docs/b.md" },
      ]);
    });

    test("keeps per-param isolation — file lists keyed by param name", () => {
      const filesByParam = {
        Alpha: ["a1.md", "a2.md"],
        Beta: ["b1.md"],
      };
      const loadingFilesByParam = { Alpha: false, Beta: false };
      const alpha = renderFilenameControl({
        param: { name: "Alpha", type: "filename" },
        filesByParam,
        loadingFilesByParam,
      });
      const beta = renderFilenameControl({
        param: { name: "Beta", type: "filename" },
        filesByParam,
        loadingFilesByParam,
      });
      expect(alpha.options.map((o) => o.value)).toEqual(["a1.md", "a2.md"]);
      expect(beta.options.map((o) => o.value)).toEqual(["b1.md"]);
    });
  });
});
