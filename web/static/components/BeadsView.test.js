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
