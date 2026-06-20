/**
 * Unit tests for BeadsView response-parsing logic.
 *
 * Tests cover readBeadsResponse: the defensive helper that reads a fetch
 * Response as text and only then attempts JSON.parse, so a non-JSON body
 * (e.g. a plain-text 403 from the old localhost gate) never triggers Safari's
 * cryptic "The string did not match the expected pattern." error.
 */

// =============================================================================
// readBeadsResponse logic
// =============================================================================

/**
 * Duplicated from BeadsView.js for testing (component imports window.preact
 * globals at module load, so the module itself cannot be imported under jsdom).
 * Keep this in sync with the implementation in BeadsView.js.
 */
async function readBeadsResponse(res) {
  const text = await res.text();
  if (text) {
    try {
      return JSON.parse(text);
    } catch (_e) {
      // fall through to error object below
    }
  }
  return { error: (text && text.trim()) || `Request failed (HTTP ${res.status})` };
}

/**
 * Build a minimal mock fetch Response whose text() resolves to `body`.
 */
function mockResponse(body, status = 200) {
  return {
    status,
    text: () => Promise.resolve(body),
  };
}

describe("readBeadsResponse", () => {
  describe("valid JSON bodies", () => {
    test("parses a JSON object body", async () => {
      const res = mockResponse('{"id":"abc-1","title":"Hello"}');
      const data = await readBeadsResponse(res);
      expect(data).toEqual({ id: "abc-1", title: "Hello" });
    });

    test("parses a JSON array body (the list endpoint shape)", async () => {
      const res = mockResponse('[{"id":"abc-1"},{"id":"abc-2"}]');
      const data = await readBeadsResponse(res);
      expect(Array.isArray(data)).toBe(true);
      expect(data).toHaveLength(2);
      expect(data[0].id).toBe("abc-1");
    });

    test("passes through a JSON error object unchanged", async () => {
      const res = mockResponse('{"error":"bd not found"}', 200);
      const data = await readBeadsResponse(res);
      expect(data).toEqual({ error: "bd not found" });
    });

    test("parses an empty JSON array", async () => {
      const res = mockResponse("[]");
      const data = await readBeadsResponse(res);
      expect(data).toEqual([]);
    });
  });

  describe("non-JSON bodies become an error object", () => {
    test("plain-text 403 body is surfaced as { error: <text> }", async () => {
      const res = mockResponse(
        "This endpoint is only available from localhost\n",
        403,
      );
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("This endpoint is only available from localhost");
    });

    test("HTML error page is surfaced as { error: <text> } (not thrown)", async () => {
      const res = mockResponse("<html><body>500</body></html>", 500);
      const data = await readBeadsResponse(res);
      expect(typeof data.error).toBe("string");
      expect(data.error).toContain("<html>");
    });

    test("does not throw on invalid JSON", async () => {
      const res = mockResponse("Unexpected token W", 200);
      await expect(readBeadsResponse(res)).resolves.toBeDefined();
    });
  });

  describe("empty and whitespace bodies fall back to an HTTP-status error", () => {
    test("empty body falls back to the HTTP status", async () => {
      const res = mockResponse("", 502);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("Request failed (HTTP 502)");
    });

    test("whitespace-only body falls back to the HTTP status", async () => {
      const res = mockResponse("   \n  ", 504);
      const data = await readBeadsResponse(res);
      expect(data.error).toBe("Request failed (HTTP 504)");
    });
  });
});

// =============================================================================
// matchesSearch logic — beads list search filtering
// =============================================================================

/**
 * Duplicated from BeadsView.js for testing (component imports window.preact
 * globals at module load, so the module itself cannot be imported under jsdom).
 * Keep this in sync with the implementation in BeadsView.js.
 */
function matchesSearch(issue, search) {
  if (!search) return true;
  const tokens = search.toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return true;
  const id = (issue.id || "").toLowerCase();
  const title = (issue.title || "").toLowerCase();
  const owner = (issue.owner || "").toLowerCase();
  const description = (issue.description || "").toLowerCase();
  for (const t of tokens) {
    if (!(id.includes(t) || title.includes(t) || owner.includes(t) || description.includes(t))) {
      return false;
    }
  }
  return true;
}

describe("matchesSearch", () => {
  const issue = {
    id: "mitto-3bx",
    title: "Beads Search Filtering",
    owner: "saurin@adobe.com",
    description: "Implement smart filtering in the beads list view search box.",
  };

  describe("empty queries match everything", () => {
    test("empty string matches", () => {
      expect(matchesSearch(issue, "")).toBe(true);
    });
    test("null / undefined matches", () => {
      expect(matchesSearch(issue, null)).toBe(true);
      expect(matchesSearch(issue, undefined)).toBe(true);
    });
    test("whitespace-only matches", () => {
      expect(matchesSearch(issue, "   \t  ")).toBe(true);
    });
  });

  describe("id matching", () => {
    test("exact id matches", () => {
      expect(matchesSearch(issue, "mitto-3bx")).toBe(true);
    });
    test("id is case-insensitive", () => {
      expect(matchesSearch(issue, "MITTO-3BX")).toBe(true);
    });
    test("partial id substring matches", () => {
      expect(matchesSearch(issue, "3bx")).toBe(true);
    });
    test("non-matching id returns false", () => {
      expect(matchesSearch(issue, "mitto-9zz")).toBe(false);
    });
  });

  describe("title matching", () => {
    test("single title word matches", () => {
      expect(matchesSearch(issue, "filtering")).toBe(true);
    });
    test("title is case-insensitive", () => {
      expect(matchesSearch(issue, "BEADS")).toBe(true);
    });
    test("title substring matches", () => {
      expect(matchesSearch(issue, "filt")).toBe(true);
    });
  });

  describe("description (body) matching", () => {
    test("body word matches when not in title", () => {
      expect(matchesSearch(issue, "smart")).toBe(true);
    });
    test("body substring matches", () => {
      expect(matchesSearch(issue, "view search")).toBe(true);
    });
    test("missing description does not throw", () => {
      const bare = { id: "x-1", title: "hi" };
      expect(matchesSearch(bare, "hi")).toBe(true);
      expect(matchesSearch(bare, "nope")).toBe(false);
    });
  });

  describe("owner matching is preserved", () => {
    test("owner email matches", () => {
      expect(matchesSearch(issue, "saurin")).toBe(true);
    });
  });

  describe("multi-word AND semantics", () => {
    test("all tokens must match (one in title, one in body)", () => {
      expect(matchesSearch(issue, "beads smart")).toBe(true);
    });
    test("returns false when any token is unmatched", () => {
      expect(matchesSearch(issue, "beads zzznope")).toBe(false);
    });
    test("tokens may match different fields (id + title)", () => {
      expect(matchesSearch(issue, "3bx filtering")).toBe(true);
    });
    test("extra whitespace between tokens is ignored", () => {
      expect(matchesSearch(issue, "   beads    smart  ")).toBe(true);
    });
  });

  describe("non-matching queries", () => {
    test("unrelated word returns false", () => {
      expect(matchesSearch(issue, "frontend")).toBe(false);
    });
  });
});

// =============================================================================
// "prompts" upstream: argument-free prompt filtering logic
// =============================================================================

/**
 * Duplicated filter from WorkspacesDialog.js / loadBeadsUpstreamPrompts for testing.
 * Keep in sync with implementation: filters to enabled AND parameter-free prompts.
 */
function filterArgumentFreePrompts(prompts) {
  return prompts.filter(p =>
    p.enabled !== false && (!p.parameters || p.parameters.length === 0)
  );
}

describe("filterArgumentFreePrompts (prompts upstream picker)", () => {
  const basePrompts = [
    { name: "sync-tasks", enabled: true, parameters: [] },
    { name: "pull-issues", enabled: true, parameters: undefined },
    { name: "create-issue", enabled: true, parameters: [{ name: "title" }] },
    { name: "disabled-prompt", enabled: false, parameters: [] },
    { name: "disabled-param", enabled: false, parameters: [{ name: "type" }] },
    { name: "no-fields-at-all", enabled: true },
  ];

  test("includes prompts with empty parameters array", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map(p => p.name)).toContain("sync-tasks");
  });

  test("includes prompts with undefined parameters", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map(p => p.name)).toContain("pull-issues");
  });

  test("includes prompts with no parameters field", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map(p => p.name)).toContain("no-fields-at-all");
  });

  test("excludes prompts that have parameters (has required args)", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map(p => p.name)).not.toContain("create-issue");
  });

  test("excludes prompts where enabled === false", () => {
    const result = filterArgumentFreePrompts(basePrompts);
    expect(result.map(p => p.name)).not.toContain("disabled-prompt");
    expect(result.map(p => p.name)).not.toContain("disabled-param");
  });

  test("treats enabled: undefined as enabled (included)", () => {
    const prompt = { name: "no-enabled-field", parameters: [] };
    const result = filterArgumentFreePrompts([prompt]);
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe("no-enabled-field");
  });

  test("returns empty array when no prompts pass the filter", () => {
    const allParameterized = [
      { name: "a", enabled: true, parameters: [{ name: "x" }] },
      { name: "b", enabled: false, parameters: [] },
    ];
    expect(filterArgumentFreePrompts(allParameterized)).toHaveLength(0);
  });

  test("returns all argument-free enabled prompts when all qualify", () => {
    const all = [
      { name: "x", enabled: true, parameters: [] },
      { name: "y", enabled: true },
    ];
    expect(filterArgumentFreePrompts(all)).toHaveLength(2);
  });
});

// =============================================================================
// "prompts" upstream: button disabled logic
// =============================================================================

/**
 * Mirrors the disable condition used in BeadsView for the "prompts" upstream buttons.
 * A button is disabled when its prompt name is empty OR onLaunchPrompt is absent.
 */
function isPromptButtonDisabled(promptName, onLaunchPrompt) {
  return !promptName || !onLaunchPrompt;
}

describe("prompts upstream button disabled logic", () => {
  const launcher = () => {};

  test("disabled when promptName is empty string", () => {
    expect(isPromptButtonDisabled("", launcher)).toBe(true);
  });

  test("disabled when promptName is undefined", () => {
    expect(isPromptButtonDisabled(undefined, launcher)).toBe(true);
  });

  test("disabled when onLaunchPrompt is absent (no prop wired)", () => {
    expect(isPromptButtonDisabled("my-prompt", undefined)).toBe(true);
  });

  test("disabled when both promptName and launcher are absent", () => {
    expect(isPromptButtonDisabled("", undefined)).toBe(true);
  });

  test("enabled when both promptName and onLaunchPrompt are present", () => {
    expect(isPromptButtonDisabled("sync-tasks", launcher)).toBe(false);
  });
});

// =============================================================================
// "prompts" upstream: onLaunchPrompt call convention
// =============================================================================

describe("onLaunchPrompt call convention", () => {
  /**
   * Simulates what the Pull/Push/Sync buttons do when clicked with a configured prompt:
   *   onLaunchPrompt(action, promptName)
   * — no arguments object, no periodic, no acpServer (handled by handler in app.js).
   */
  function simulateButtonClick(action, promptName, onLaunchPrompt) {
    if (!promptName || !onLaunchPrompt) return;
    onLaunchPrompt(action, promptName);
  }

  /** Minimal call spy without jest.fn() (file uses ESM without @jest/globals import). */
  function makeSpy() {
    const calls = [];
    const spy = (...args) => calls.push(args);
    spy.calls = calls;
    spy.callCount = () => calls.length;
    spy.lastCall = () => calls[calls.length - 1];
    return spy;
  }

  test("pull button calls launcher with 'pull' action and the configured promptName", () => {
    const launcher = makeSpy();
    simulateButtonClick("pull", "sync-issues", launcher);
    expect(launcher.callCount()).toBe(1);
    expect(launcher.lastCall()).toEqual(["pull", "sync-issues"]);
  });

  test("push button calls launcher with 'push' action", () => {
    const launcher = makeSpy();
    simulateButtonClick("push", "push-tasks", launcher);
    expect(launcher.lastCall()).toEqual(["push", "push-tasks"]);
  });

  test("sync button calls launcher with 'sync' action", () => {
    const launcher = makeSpy();
    simulateButtonClick("sync", "full-sync", launcher);
    expect(launcher.lastCall()).toEqual(["sync", "full-sync"]);
  });

  test("button does NOT call launcher when promptName is empty", () => {
    const launcher = makeSpy();
    simulateButtonClick("pull", "", launcher);
    expect(launcher.callCount()).toBe(0);
  });

  test("button does NOT call launcher when onLaunchPrompt is absent", () => {
    // Nothing to assert — just ensure it doesn't throw
    expect(() => simulateButtonClick("pull", "my-prompt", undefined)).not.toThrow();
  });

  test("launcher is NOT called with an arguments object (argument-free)", () => {
    const launcher = makeSpy();
    simulateButtonClick("sync", "sync-prompt", launcher);
    // Must have exactly 2 args: action + promptName (no args/periodic object)
    expect(launcher.lastCall()).toHaveLength(2);
  });
});
