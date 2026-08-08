/**
 * Unit tests for the SDK core config resolver.
 *
 * Covers: defaults under fully injected env, baseUrl/apiPrefix
 * normalization, fetch resolution (injected vs. globals vs. missing),
 * lazy WebSocket resolution, storage/logger/auth defaults + isolation,
 * unknown-key rejection, frozen result, and the no-browser-globals
 * source-level guarantee for everything under sdk/core/.
 */
import { readdirSync, readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveConfig } from "./config.js";
import { ConfigError } from "./errors.js";

const CORE_DIR = dirname(fileURLToPath(import.meta.url));

describe("resolveConfig", () => {
  describe("defaults under fully injected env", () => {
    test("returns a frozen config object", () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(Object.isFrozen(config)).toBe(true);
    });

    test("baseUrl defaults to empty string", () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(config.baseUrl).toBe("");
    });

    test("apiPrefix defaults to empty string", () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(config.apiPrefix).toBe("");
    });

    test("storage defaults to an in-memory adapter", async () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(config.storage.getItem("missing")).toBe(null);
      config.storage.setItem("k", "v");
      expect(config.storage.getItem("k")).toBe("v");
      config.storage.removeItem("k");
      expect(config.storage.getItem("k")).toBe(null);
    });

    test("storage is isolated per resolveConfig() call", () => {
      const a = resolveConfig({ fetch: () => {} }, {});
      const b = resolveConfig({ fetch: () => {} }, {});
      a.storage.setItem("k", "v");
      expect(b.storage.getItem("k")).toBe(null);
    });

    test("logger defaults to a silent no-op (never throws, no console output required)", () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(() => config.logger.debug("x")).not.toThrow();
      expect(() => config.logger.info("x")).not.toThrow();
      expect(() => config.logger.warn("x")).not.toThrow();
      expect(() => config.logger.error("x")).not.toThrow();
    });

    test("injected logger is used instead of the default", () => {
      const calls = [];
      const logger = {
        debug: (...a) => calls.push(["debug", ...a]),
        info: (...a) => calls.push(["info", ...a]),
        warn: (...a) => calls.push(["warn", ...a]),
        error: (...a) => calls.push(["error", ...a]),
      };
      const config = resolveConfig({ fetch: () => {}, logger }, {});
      config.logger.info("hello");
      expect(calls).toEqual([["info", "hello"]]);
    });

    test("auth defaults to a no-op passthrough adapter", async () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      const patch = await config.auth.authorize({
        method: "GET",
        url: "/x",
        headers: {},
      });
      expect(patch).toEqual({});
    });

    test("onUnauthorized defaults to a no-op", () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(() => config.onUnauthorized()).not.toThrow();
    });
  });

  describe("baseUrl / apiPrefix normalization", () => {
    test.each([
      ["", ""],
      [undefined, ""],
      ["/x", "/x"],
      ["/x/", "/x"],
      ["http://host:1234/", "http://host:1234"],
      ["/", "/"],
    ])("baseUrl %p -> %p", (input, expected) => {
      const config = resolveConfig({ fetch: () => {}, baseUrl: input }, {});
      expect(config.baseUrl).toBe(expected);
    });

    test.each([
      ["", ""],
      [undefined, ""],
      ["api", "/api"],
      ["/api", "/api"],
      ["/api/", "/api"],
      ["api/", "/api"],
    ])("apiPrefix %p -> %p", (input, expected) => {
      const config = resolveConfig({ fetch: () => {}, apiPrefix: input }, {});
      expect(config.apiPrefix).toBe(expected);
    });
  });

  describe("fetch resolution", () => {
    test("uses the injected fetch when provided", () => {
      const injected = () => {};
      const config = resolveConfig({ fetch: injected }, {});
      expect(config.fetch).toBe(injected);
    });

    test("falls back to globals.fetch, bound to globals", () => {
      let receivedThis;
      const globals = {
        fetch: function () {
          receivedThis = this;
        },
      };
      const config = resolveConfig({}, globals);
      config.fetch();
      expect(receivedThis).toBe(globals);
    });

    test("throws ConfigError when no fetch is available anywhere", () => {
      expect(() => resolveConfig({}, {})).toThrow(ConfigError);
      try {
        resolveConfig({}, {});
      } catch (e) {
        expect(e.code).toBe("invalid_config");
      }
    });
  });

  describe("WebSocket lazy resolution", () => {
    test("does not throw at resolveConfig() time when WebSocket is absent", () => {
      expect(() => resolveConfig({ fetch: () => {} }, {})).not.toThrow();
    });

    test("throws ConfigError only on first access when absent", () => {
      const config = resolveConfig({ fetch: () => {} }, {});
      expect(() => config.getWebSocket()).toThrow(ConfigError);
    });

    test("returns the injected WebSocket implementation when provided", () => {
      class FakeWebSocket {}
      const config = resolveConfig(
        { fetch: () => {}, WebSocket: FakeWebSocket },
        {},
      );
      expect(config.getWebSocket()).toBe(FakeWebSocket);
    });

    test("falls back to globals.WebSocket when not injected", () => {
      class FakeWebSocket {}
      const config = resolveConfig(
        { fetch: () => {} },
        { WebSocket: FakeWebSocket },
      );
      expect(config.getWebSocket()).toBe(FakeWebSocket);
    });
  });

  describe("unknown option keys", () => {
    test("rejects an unknown key with ConfigError", () => {
      expect(() =>
        resolveConfig({ fetch: () => {}, fetchImpl: () => {} }, {}),
      ).toThrow(ConfigError);
    });

    test("unknown-key error message names the offending key", () => {
      try {
        resolveConfig({ bogus: 1 }, {});
        throw new Error("expected resolveConfig to throw");
      } catch (e) {
        expect(e).toBeInstanceOf(ConfigError);
        expect(e.message).toContain("bogus");
      }
    });
  });

  describe("no-browser-globals source guarantee (sdk/core/**)", () => {
    const FORBIDDEN = [
      "window",
      "document",
      "cookie",
      "localStorage",
      "sessionStorage",
      "navigator",
      "location",
      "native.js",
    ];

    function jsFilesUnder(dir) {
      const out = [];
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        if (entry.name.endsWith(".test.js")) continue;
        const full = join(dir, entry.name);
        if (entry.isDirectory()) {
          out.push(...jsFilesUnder(full));
        } else if (entry.name.endsWith(".js")) {
          out.push(full);
        }
      }
      return out;
    }

    /** Strip /* block *\/ and // line comments so doc mentions of the
     *  forbidden words (e.g. this very file's header comments) don't
     *  false-positive the scan below — only actual source code is checked. */
    function stripComments(src) {
      return src
        .replace(/\/\*[\s\S]*?\*\//g, "")
        .replace(/(^|[^:])\/\/.*$/gm, "$1");
    }

    test("no forbidden browser-global identifiers appear under sdk/core/", () => {
      const offenders = [];
      for (const file of jsFilesUnder(CORE_DIR)) {
        const src = stripComments(readFileSync(file, "utf8"));
        for (const token of FORBIDDEN) {
          // Word-boundary match so e.g. "windowSize" doesn't false-positive.
          const re = new RegExp(`\\b${token.replace(".", "\\.")}\\b`);
          if (re.test(src)) offenders.push(`${file}: ${token}`);
        }
        if (/\bconsole\s*\./.test(src)) offenders.push(`${file}: console.*`);
      }
      expect(offenders).toEqual([]);
    });
  });
});
