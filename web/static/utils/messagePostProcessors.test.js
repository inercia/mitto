/**
 * Unit tests for messagePostProcessors.js (mitto-sus.5).
 *
 * Each processor is idempotent by design (its own DOM marker makes a second
 * call against unchanged content a no-op) — these tests exercise that
 * guarantee directly, in isolation from AgentMessageBlock's effect wiring
 * (covered separately by Message.test.js's "AgentMessageBlock keyed
 * rendering" mounted group).
 *
 * processBeadsLinks reaches the network via fetchAndCacheBeadsIds (SDK
 * client → global fetch) to populate the shared known-IDs cache; GET
 * requests skip the CSRF preflight (browserCookieAuth.authorize short-
 * circuits on safe methods), so stubbing global.fetch is enough — matching
 * beadsPreload.test.js / useBeadsKnownIds.test.js's established approach.
 */

import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "./testing/testGlobals.js";
import {
  processTables,
  processMermaid,
  processBeadsLinks,
} from "./messagePostProcessors.js";
import { fetchAndCacheBeadsIds } from "./beadsKnownIds.js";
import { _resetBeadsPreloadCache } from "./beadsPreload.js";

global.window = global.window || {};
window.mittoApiPrefix = "";
if (typeof document === "undefined") {
  global.document = { cookie: "" };
}

let currentFetch;
let originalFetch;
let originalRenderMermaid;

/**
 * Builds a minimal fetch Response-like object. The SDK's transport.js
 * `decodeBody()` reads `response.headers.get("content-type")` and
 * `response.text()` (not `.json()`) to decide how to parse the body, so a
 * bare `{ json: () => ... }` stub (fine for preloadBeadsIssues's
 * fire-and-forget calls, which never await the decoded value) is not enough
 * here — fetchAndCacheBeadsIds awaits the real decoded array.
 */
function jsonResponse(body, { ok = true, status = 200 } = {}) {
  const text = JSON.stringify(body);
  return {
    ok,
    status,
    headers: { get: (name) => (name === "content-type" ? "application/json" : null) },
    text: () => Promise.resolve(text),
    json: () => Promise.resolve(body),
  };
}

beforeEach(() => {
  originalFetch = global.fetch;
  currentFetch = jest.fn(() => Promise.resolve(jsonResponse([])));
  global.fetch = currentFetch;
  originalRenderMermaid = window.renderMermaidDiagrams;
  _resetBeadsPreloadCache();
});

afterEach(() => {
  global.fetch = originalFetch;
  window.renderMermaidDiagrams = originalRenderMermaid;
  jest.restoreAllMocks();
});

function makeDiv(html) {
  const div = document.createElement("div");
  div.innerHTML = html;
  return div;
}

describe("processTables", () => {
  test("returns 0 and does not throw for a null blockEl", () => {
    expect(processTables(null)).toBe(0);
  });

  test("wraps every un-wrapped table and returns the count newly wrapped", () => {
    const block = makeDiv(
      "<table><tr><td>a</td></tr></table><p>x</p><table><tr><td>b</td></tr></table>",
    );
    expect(processTables(block)).toBe(2);
    expect(block.querySelectorAll(".table-wrapper")).toHaveLength(2);
    expect(block.querySelectorAll(".table-wrapper > table")).toHaveLength(2);
  });

  test("idempotent: a second call against unchanged content wraps nothing", () => {
    const block = makeDiv("<table><tr><td>a</td></tr></table>");
    expect(processTables(block)).toBe(1);
    expect(processTables(block)).toBe(0);
    expect(block.querySelectorAll(".table-wrapper")).toHaveLength(1);
  });
});

describe("processMermaid", () => {
  test("returns 0 and does not call the renderer for a null blockEl", () => {
    window.renderMermaidDiagrams = jest.fn();
    expect(processMermaid(null)).toBe(0);
    expect(window.renderMermaidDiagrams).not.toHaveBeenCalled();
  });

  test("returns 0 when there are no pending mermaid blocks", () => {
    window.renderMermaidDiagrams = jest.fn();
    const block = makeDiv("<p>no diagrams here</p>");
    expect(processMermaid(block)).toBe(0);
    expect(window.renderMermaidDiagrams).toHaveBeenCalledWith(block);
  });

  test("counts unprocessed diagram blocks and delegates rendering to the global renderer", () => {
    window.renderMermaidDiagrams = jest.fn();
    const block = makeDiv(
      '<pre class="mermaid">graph TD;A--&gt;B</pre><pre class="mermaid" data-mermaid-processed="true">graph TD;C--&gt;D</pre>',
    );
    expect(processMermaid(block)).toBe(1);
    expect(window.renderMermaidDiagrams).toHaveBeenCalledTimes(1);
    expect(window.renderMermaidDiagrams).toHaveBeenCalledWith(block);
  });

  test("does not throw when window.renderMermaidDiagrams is not a function", () => {
    window.renderMermaidDiagrams = undefined;
    const block = makeDiv('<pre class="mermaid">graph TD;A--&gt;B</pre>');
    expect(() => processMermaid(block)).not.toThrow();
    expect(processMermaid(block)).toBe(1);
  });
});

describe("processBeadsLinks", () => {
  test("returns 0 and does not throw for a null blockEl", () => {
    expect(processBeadsLinks(null, "/tmp/wsA")).toBe(0);
  });

  test("returns 0 when no known IDs are cached for the working dir", () => {
    const block = makeDiv("<p>See mitto-unknown for details.</p>");
    expect(processBeadsLinks(block, "/tmp/ws-uncached")).toBe(0);
    expect(block.querySelectorAll("a.beads-link")).toHaveLength(0);
  });

  test("linkifies newly-committed known IDs and preloads them exactly once", async () => {
    const workingDir = "/tmp/ws-processBeadsLinks-1";
    currentFetch.mockImplementation((url) =>
      Promise.resolve(
        String(url).includes("/api/issues/")
          ? jsonResponse({})
          : jsonResponse([
              { id: "mitto-aaa", title: "Test Issue", status: "open" },
            ]),
      ),
    );
    await fetchAndCacheBeadsIds(workingDir);

    const block = makeDiv("<p>See mitto-aaa for details.</p>");
    const linked = processBeadsLinks(block, workingDir);
    expect(linked).toBe(1);
    expect(block.querySelectorAll("a.beads-link")).toHaveLength(1);

    // Preload fired once for the newly-linked ID.
    await Promise.resolve();
    const preloadUrls = currentFetch.mock.calls
      .map((c) => String(c[0]))
      .filter((u) => u.includes("/api/issues/mitto-aaa"));
    expect(preloadUrls).toHaveLength(1);
  });

  test("idempotent: a second call against already-linked content links and preloads nothing new", async () => {
    const workingDir = "/tmp/ws-processBeadsLinks-2";
    currentFetch.mockImplementation((url) =>
      Promise.resolve(
        String(url).includes("/api/issues/")
          ? jsonResponse({})
          : jsonResponse([{ id: "mitto-bbb", title: "Another", status: "open" }]),
      ),
    );
    await fetchAndCacheBeadsIds(workingDir);

    const block = makeDiv("<p>See mitto-bbb for details.</p>");
    expect(processBeadsLinks(block, workingDir)).toBe(1);
    await Promise.resolve();
    const callsAfterFirst = currentFetch.mock.calls.length;

    expect(processBeadsLinks(block, workingDir)).toBe(0);
    expect(block.querySelectorAll("a.beads-link")).toHaveLength(1);
    await Promise.resolve();
    expect(currentFetch.mock.calls.length).toBe(callsAfterFirst);
  });
});
