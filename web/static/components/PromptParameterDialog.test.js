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
    label: s.title || s.session_id,
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
        { session_id: "child-1", title: "Child", parent_session_id: "host-1" },
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
          title: "Child",
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
      { session_id: "child-1", title: "Alpha", parent_session_id: "host-1" },
      { session_id: "child-2", title: "", parent_session_id: "host-1" },
      {
        session_id: "child-3",
        title: "Other",
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

    test("label uses title when present, falls back to session_id", () => {
      const result = renderChildSessionIdControl({
        loadingSessions: false,
        sessions,
        hostSessionId: "host-1",
      });
      expect(result.options[0].label).toBe("Alpha");
      expect(result.options[1].label).toBe("child-2");
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
