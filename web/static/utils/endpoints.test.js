/**
 * Shim-contract tests for the legacy `endpoints` registry (mitto-7gta.6).
 *
 * The exhaustive per-builder / per-group assertions now live in
 * `web/static/sdk/core/endpoints.test.js`, which exercises `createEndpoints()`
 * directly via injected config objects (no `window` mutation needed there).
 * This file only verifies the *shim* contract that the SDK test cannot: that
 * `endpoints` re-reads `window.mittoApiPrefix`/`window.location` live (per
 * call, not once at import time — memoized per (prefix, wsBase) pair so a
 * change is still picked up on the very next call), that the ws builders
 * derive their origin from `window.location`, and that the proxy correctly
 * delegates to the SDK registry for a couple of spot-checked builders across
 * resource groups.
 */
import { endpoints } from "./endpoints.js";

describe("endpoints shim (legacy utils/endpoints.js)", () => {
  let originalMittoApiPrefix;

  beforeEach(() => {
    originalMittoApiPrefix = window.mittoApiPrefix;
  });

  afterEach(() => {
    window.mittoApiPrefix = originalMittoApiPrefix;
  });

  describe("live prefix re-reading", () => {
    test("applies /mitto prefix when set", () => {
      window.mittoApiPrefix = "/mitto";
      expect(endpoints.sessions.list()).toBe("/mitto/api/sessions");
    });

    test("no prefix when mittoApiPrefix is empty string", () => {
      window.mittoApiPrefix = "";
      expect(endpoints.sessions.list()).toBe("/api/sessions");
    });

    test("no prefix when mittoApiPrefix is undefined", () => {
      delete window.mittoApiPrefix;
      expect(endpoints.sessions.list()).toBe("/api/sessions");
    });

    test("reflects a prefix change on the very next call (no import-time snapshot)", () => {
      window.mittoApiPrefix = "";
      expect(endpoints.sessions.list()).toBe("/api/sessions");
      window.mittoApiPrefix = "/mitto";
      expect(endpoints.sessions.list()).toBe("/mitto/api/sessions");
      window.mittoApiPrefix = "";
      expect(endpoints.sessions.list()).toBe("/api/sessions");
    });

    test("path-param builder also respects prefix", () => {
      window.mittoApiPrefix = "/mitto";
      expect(endpoints.sessions.get("20260101-120000-deadbeef")).toBe(
        "/mitto/api/sessions/20260101-120000-deadbeef",
      );
    });
  });

  describe("ws builders derive origin from window.location", () => {
    beforeEach(() => {
      window.mittoApiPrefix = "";
    });

    test("events.ws returns ws(s):// URL ending in /api/events", () => {
      const url = endpoints.events.ws();
      expect(url).toMatch(/^wss?:\/\//);
      expect(url).toMatch(/\/api\/events$/);
    });

    test("sessions.ws returns ws(s):// URL ending in /api/sessions/abc/ws", () => {
      const url = endpoints.sessions.ws("abc");
      expect(url).toMatch(/^wss?:\/\//);
      expect(url).toMatch(/\/api\/sessions\/abc\/ws$/);
    });

    test("events.ws includes prefix when set", () => {
      window.mittoApiPrefix = "/mitto";
      const url = endpoints.events.ws();
      expect(url).toContain("/mitto");
      expect(url).toMatch(/\/api\/events$/);
    });
  });

  describe("delegation spot-checks across resource groups", () => {
    beforeEach(() => {
      window.mittoApiPrefix = "";
    });

    test("issues.list builds the same URL as the SDK registry", () => {
      expect(endpoints.issues.list({ working_dir: "/w" })).toBe(
        "/api/issues?working_dir=%2Fw",
      );
    });

    test("workspaces.metadata encodes the uuid path param", () => {
      expect(endpoints.workspaces.metadata("uuid with space")).toBe(
        "/api/workspaces/uuid%20with%20space/metadata",
      );
    });

    test("workspacePrompts.get encodes a slash in the prompt name", () => {
      expect(endpoints.workspacePrompts.get("team/my-prompt")).toBe(
        "/api/workspace-prompts/team%2Fmy-prompt",
      );
    });

    test("misc.csrfToken (a zero-arg builder) resolves through the proxy", () => {
      expect(endpoints.misc.csrfToken()).toBe("/api/csrf-token");
    });
  });

  test("Object.keys(endpoints) enumerates all resource groups (outer object is plain, not a Proxy)", () => {
    expect(Object.keys(endpoints).sort()).toEqual(
      [
        "acpServers",
        "agents",
        "aux",
        "beads",
        "config",
        "events",
        "folders",
        "global",
        "issues",
        "misc",
        "runners",
        "sessions",
        "workspaceDirs",
        "workspaceFiles",
        "workspacePrompts",
        "workspaces",
      ].sort(),
    );
  });
});

