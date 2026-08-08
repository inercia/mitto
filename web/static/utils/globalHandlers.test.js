/**
 * Tests for the global click handler in globalHandlers.js — specifically the
 * `/viewer.html?…` branch and its fallback when `ws_path` is absent.
 *
 * Regression coverage for mitto-tac5: clicking a viewer link whose extension
 * is known to be non-viewable (e.g. `.xlsx`) and which is missing the
 * `ws_path` query parameter must NOT open the internal viewer (which would
 * otherwise render garbled binary content). It should fall back to a plain
 * download via `/api/files?ws=…&path=…`.
 *
 * globalHandlers.js registers a `click` listener on `document` as an import
 * side effect, so importing the module once at the top of this file is
 * enough — subsequent tests dispatch clicks against that installed listener.
 */

// In ESM mode (--experimental-vm-modules), `jest` is not auto-injected as a
// global — it must be imported explicitly. testGlobals.js re-exports the
// lifecycle globals and `jest` from whichever runner is active (Jest or
// bun:test), so a single import works under both.
import {
  describe,
  test,
  expect,
  beforeEach,
  afterEach,
  jest,
} from "./testing/testGlobals.js";
import { isOverHorizontallyScrollable } from "./globalHandlers.js";

describe("globalHandlers viewer.html click handler — missing ws_path fallback (mitto-tac5)", () => {
  let openSpy;

  beforeEach(() => {
    // Browser mode: isNativeApp() returns true only when window.mittoPickFolder
    // is a function; leave every native binding unset so the handler routes
    // through the browser branch.
    delete window.mittoPickFolder;
    delete window.mittoOpenViewer;
    delete window.mittoOpenFileURL;
    delete window.mittoOpenExternalURL;

    // Deterministic API prefix so any /api/files fallback URL is predictable.
    window.mittoApiPrefix = "";

    openSpy = jest.spyOn(window, "open").mockImplementation(() => null);
    document.body.innerHTML = "";
  });

  afterEach(() => {
    openSpy.mockRestore();
  });

  function clickAnchor(href) {
    const a = document.createElement("a");
    a.setAttribute("href", href);
    a.textContent = "link";
    document.body.appendChild(a);
    a.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );
    return a;
  }

  test("does NOT open the internal viewer for a non-viewable link missing ws_path", () => {
    // Non-viewable extension (.xlsx) + `ws` (UUID) + `path`, but NO `ws_path`.
    clickAnchor("/viewer.html?ws=SOME-UUID&path=report.xlsx");

    // The handler must never open the viewer URL for a non-viewable extension.
    const viewerCall = openSpy.mock.calls.find(
      ([url]) => typeof url === "string" && url.includes("/viewer.html"),
    );
    expect(viewerCall).toBeUndefined();
  });

  test("falls back to /api/files download when ws_path is missing on a non-viewable link", () => {
    clickAnchor("/viewer.html?ws=SOME-UUID&path=report.xlsx");

    // Graceful degradation: route through /api/files download using the ws
    // (workspace UUID) and path already present on the viewer URL.
    const apiFilesCall = openSpy.mock.calls.find(
      ([url]) => typeof url === "string" && url.includes("/api/files"),
    );
    expect(apiFilesCall).toBeDefined();
    // The download URL must carry both the workspace UUID and the path.
    expect(apiFilesCall[0]).toContain("ws=SOME-UUID");
    expect(apiFilesCall[0]).toContain("path=report.xlsx");
  });

  test("still opens the internal viewer for a VIEWABLE link missing ws_path (no regression)", () => {
    // Regression guard: text/code files with no ws_path should keep working
    // exactly as they do today — the fallback only applies to non-viewable
    // extensions. This test must PASS on current code and continue to pass
    // after the fix (no behavior change for viewable extensions).
    clickAnchor("/viewer.html?ws=SOME-UUID&path=script.js");

    const viewerCall = openSpy.mock.calls.find(
      ([url]) => typeof url === "string" && url.includes("/viewer.html"),
    );
    expect(viewerCall).toBeDefined();
  });

  test("no behavior change when ws_path IS present on a non-viewable link", () => {
    // Ground-truth: with ws_path supplied, the existing routing already opens
    // the file with the OS default app via openFileURL(). In browser mode
    // without any native bridge, openFileURL falls through to
    // convertFileURLToHTTP + window.open — but that requires a workspace to
    // be registered. We only assert that the viewer is NOT opened.
    window.mittoOpenFileURL = jest.fn();

    clickAnchor(
      "/viewer.html?ws=SOME-UUID&path=report.xlsx&ws_path=%2Ftmp%2Fws",
    );

    // openFileURL routes to mittoOpenFileURL in "native-ish" mode; the
    // internal viewer must not be opened.
    expect(window.mittoOpenFileURL).toHaveBeenCalledTimes(1);
    const viewerCall = openSpy.mock.calls.find(
      ([url]) => typeof url === "string" && url.includes("/viewer.html"),
    );
    expect(viewerCall).toBeUndefined();
  });
});

describe("isOverHorizontallyScrollable — data-mitto-no-swipe opt-out (mitto-7c98)", () => {
  let fromPointSpy;

  beforeEach(() => {
    document.body.innerHTML = "";
  });

  afterEach(() => {
    fromPointSpy?.mockRestore();
  });

  // The cursor tracker only updates on mousemove, so point the hit-test at a
  // chosen element directly instead of simulating pointer coordinates.
  function hitTest(el) {
    fromPointSpy = jest
      .spyOn(document, "elementFromPoint")
      .mockImplementation(() => el);
    return isOverHorizontallyScrollable();
  }

  test("returns true for an element inside a data-mitto-no-swipe container", () => {
    // jsdom reports scrollWidth === clientWidth === 0, so the overflow
    // heuristic alone cannot detect this container — the marker must.
    document.body.innerHTML = `
      <div data-mitto-no-swipe><button id="btn">Suggestion</button></div>
    `;
    expect(hitTest(document.getElementById("btn"))).toBe(true);
  });

  test("returns true when the marked element itself is under the cursor", () => {
    document.body.innerHTML = `<div id="strip" data-mitto-no-swipe></div>`;
    expect(hitTest(document.getElementById("strip"))).toBe(true);
  });

  test("returns false for unmarked, non-overflowing content", () => {
    document.body.innerHTML = `<div><button id="btn">Plain</button></div>`;
    expect(hitTest(document.getElementById("btn"))).toBe(false);
  });

  test("returns false when nothing is under the cursor", () => {
    expect(hitTest(null)).toBe(false);
  });
});
