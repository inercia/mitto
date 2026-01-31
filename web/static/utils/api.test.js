/**
 * Unit tests for Mitto Web Interface API utility functions
 *
 * These tests verify the API prefix handling that is critical for
 * external access via Tailscale Funnel and other reverse proxies.
 */

import { getApiPrefix, apiUrl, wsUrl } from "./api.js";

// =============================================================================
// Setup and Teardown
// =============================================================================

describe("API Utilities", () => {
  // Store original window properties
  let originalMittoApiPrefix;
  let originalLocation;

  beforeEach(() => {
    // Save original values
    originalMittoApiPrefix = window.mittoApiPrefix;
    originalLocation = window.location;

    // Mock window.location
    delete window.location;
    window.location = {
      protocol: "https:",
      host: "example.com",
    };
  });

  afterEach(() => {
    // Restore original values
    window.mittoApiPrefix = originalMittoApiPrefix;
    window.location = originalLocation;
  });

  // =============================================================================
  // getApiPrefix Tests
  // =============================================================================

  describe("getApiPrefix", () => {
    test("returns empty string when mittoApiPrefix is undefined", () => {
      delete window.mittoApiPrefix;
      expect(getApiPrefix()).toBe("");
    });

    test("returns empty string when mittoApiPrefix is null", () => {
      window.mittoApiPrefix = null;
      expect(getApiPrefix()).toBe("");
    });

    test("returns empty string when mittoApiPrefix is empty string", () => {
      window.mittoApiPrefix = "";
      expect(getApiPrefix()).toBe("");
    });

    test("returns /mitto when set to /mitto", () => {
      window.mittoApiPrefix = "/mitto";
      expect(getApiPrefix()).toBe("/mitto");
    });

    test("returns custom prefix when set", () => {
      window.mittoApiPrefix = "/custom/prefix";
      expect(getApiPrefix()).toBe("/custom/prefix");
    });
  });

  // =============================================================================
  // apiUrl Tests
  // =============================================================================

  describe("apiUrl", () => {
    test("prepends prefix to path with leading slash", () => {
      window.mittoApiPrefix = "/mitto";
      expect(apiUrl("/api/sessions")).toBe("/mitto/api/sessions");
    });

    test("adds leading slash if missing", () => {
      window.mittoApiPrefix = "/mitto";
      expect(apiUrl("api/sessions")).toBe("/mitto/api/sessions");
    });

    test("works with empty prefix", () => {
      window.mittoApiPrefix = "";
      expect(apiUrl("/api/sessions")).toBe("/api/sessions");
    });

    test("handles root path", () => {
      window.mittoApiPrefix = "/mitto";
      expect(apiUrl("/")).toBe("/mitto/");
    });

    test("handles complex paths", () => {
      window.mittoApiPrefix = "/mitto";
      expect(apiUrl("/api/sessions/123/events?limit=50")).toBe(
        "/mitto/api/sessions/123/events?limit=50",
      );
    });

    test("handles undefined prefix gracefully", () => {
      delete window.mittoApiPrefix;
      expect(apiUrl("/api/sessions")).toBe("/api/sessions");
    });
  });

  // =============================================================================
  // wsUrl Tests
  // =============================================================================

  describe("wsUrl", () => {
    test("builds wss URL for https protocol", () => {
      window.mittoApiPrefix = "/mitto";
      window.location.protocol = "https:";
      window.location.host = "example.com";

      expect(wsUrl("/api/events")).toBe("wss://example.com/mitto/api/events");
    });

    test("builds ws URL for http protocol", () => {
      window.mittoApiPrefix = "/mitto";
      window.location.protocol = "http:";
      window.location.host = "localhost:8080";

      expect(wsUrl("/api/events")).toBe("ws://localhost:8080/mitto/api/events");
    });

    test("adds leading slash if missing", () => {
      window.mittoApiPrefix = "/mitto";
      window.location.protocol = "https:";
      window.location.host = "example.com";

      expect(wsUrl("api/events")).toBe("wss://example.com/mitto/api/events");
    });

    test("works with empty prefix", () => {
      window.mittoApiPrefix = "";
      window.location.protocol = "https:";
      window.location.host = "example.com";

      expect(wsUrl("/api/events")).toBe("wss://example.com/api/events");
    });

    test("handles session-specific WebSocket paths", () => {
      window.mittoApiPrefix = "/mitto";
      window.location.protocol = "https:";
      window.location.host = "satie.boreal-vega.ts.net";

      expect(wsUrl("/api/sessions/20260130-165958-ff8d5eb5/ws")).toBe(
        "wss://satie.boreal-vega.ts.net/mitto/api/sessions/20260130-165958-ff8d5eb5/ws",
      );
    });

    test("handles undefined prefix gracefully", () => {
      delete window.mittoApiPrefix;
      window.location.protocol = "https:";
      window.location.host = "example.com";

      expect(wsUrl("/api/events")).toBe("wss://example.com/api/events");
    });
  });
});
