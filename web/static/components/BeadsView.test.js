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
