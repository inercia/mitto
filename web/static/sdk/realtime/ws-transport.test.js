/**
 * Unit tests for the shared WebSocket transport primitives
 * (web/static/sdk/realtime/ws-transport.js, mitto-7gta.23).
 *
 * Closes the last SDK-module test gap: this module was previously covered
 * only indirectly via session-stream.test.js / events-stream.test.js (which
 * re-export and exercise it through SessionStream/EventsStream). No real
 * timers, no real sockets, no DOM — matches the purity rules for
 * sdk/realtime/** enforced by the no-browser-globals scan below.
 */
import { resolveConfig } from "../core/config.js";
import { ConfigError } from "../core/errors.js";
import {
  RECONNECT_BASE_DELAY_MS,
  RECONNECT_MAX_DELAY_MS,
  RECONNECT_JITTER_FACTOR,
  MAX_RECONNECT_ATTEMPTS,
  calculateReconnectDelay,
  isReconnectLimitReached,
  wsUrlFor,
  createEmitter,
} from "./ws-transport.js";

function configWith(overrides = {}) {
  return resolveConfig({ fetch: async () => {}, ...overrides }, {});
}

describe("calculateReconnectDelay", () => {
  test("with random() pinned to 0, returns exactly the exponential delay (no jitter)", () => {
    expect(calculateReconnectDelay(0, { random: () => 0 })).toBe(RECONNECT_BASE_DELAY_MS);
    expect(calculateReconnectDelay(1, { random: () => 0 })).toBe(RECONNECT_BASE_DELAY_MS * 2);
    expect(calculateReconnectDelay(2, { random: () => 0 })).toBe(RECONNECT_BASE_DELAY_MS * 4);
  });

  test("with random() pinned to 1, adds the full jitter factor on top", () => {
    const expected = Math.floor(
      RECONNECT_BASE_DELAY_MS * 2 + RECONNECT_BASE_DELAY_MS * 2 * RECONNECT_JITTER_FACTOR,
    );
    expect(calculateReconnectDelay(1, { random: () => 1 })).toBe(expected);
  });

  test("clamps the exponential component at maxDelay before applying jitter", () => {
    const delay = calculateReconnectDelay(20, { random: () => 0 });
    expect(delay).toBe(RECONNECT_MAX_DELAY_MS);
  });

  test("respects baseDelay/maxDelay/jitterFactor overrides", () => {
    expect(
      calculateReconnectDelay(0, { baseDelay: 500, maxDelay: 500, jitterFactor: 0, random: () => 1 }),
    ).toBe(500);
  });

  test("defaults to Math.random when no random is injected (result stays within bounds)", () => {
    const delay = calculateReconnectDelay(0);
    expect(delay).toBeGreaterThanOrEqual(RECONNECT_BASE_DELAY_MS);
    expect(delay).toBeLessThanOrEqual(
      Math.ceil(RECONNECT_BASE_DELAY_MS * (1 + RECONNECT_JITTER_FACTOR)),
    );
  });
});

describe("isReconnectLimitReached", () => {
  test("false below the default max, true at and above it", () => {
    expect(isReconnectLimitReached(MAX_RECONNECT_ATTEMPTS - 1)).toBe(false);
    expect(isReconnectLimitReached(MAX_RECONNECT_ATTEMPTS)).toBe(true);
    expect(isReconnectLimitReached(MAX_RECONNECT_ATTEMPTS + 1)).toBe(true);
  });

  test("honors a maxAttempts override", () => {
    expect(isReconnectLimitReached(2, { maxAttempts: 3 })).toBe(false);
    expect(isReconnectLimitReached(3, { maxAttempts: 3 })).toBe(true);
  });
});

describe("wsUrlFor", () => {
  test("maps http:// baseUrl to ws://", () => {
    const config = configWith({ baseUrl: "http://host:1234" });
    expect(wsUrlFor(config, "/api/events")).toBe("ws://host:1234/api/events");
  });

  test("maps https:// baseUrl to wss://", () => {
    const config = configWith({ baseUrl: "https://host" });
    expect(wsUrlFor(config, "/api/events")).toBe("wss://host/api/events");
  });

  test("passes an already-ws(s):// baseUrl through untouched", () => {
    const config = configWith({ baseUrl: "wss://host" });
    expect(wsUrlFor(config, "/api/events")).toBe("wss://host/api/events");
  });

  test("inserts apiPrefix between the mapped base and the path", () => {
    const config = configWith({ baseUrl: "http://host", apiPrefix: "/mitto" });
    expect(wsUrlFor(config, "/api/events")).toBe("ws://host/mitto/api/events");
  });

  test("options.wsBaseUrl overrides config.baseUrl", () => {
    const config = configWith({ baseUrl: "https://host" });
    expect(wsUrlFor(config, "/api/events", { wsBaseUrl: "ws://other:5" })).toBe(
      "ws://other:5/api/events",
    );
  });

  test("an empty baseUrl (and no wsBaseUrl override) throws ConfigError with the given label", () => {
    const config = configWith({ baseUrl: "" });
    expect(() => wsUrlFor(config, "/api/events", {}, "MyLabel")).toThrow(ConfigError);
    expect(() => wsUrlFor(config, "/api/events", {}, "MyLabel")).toThrow(/^MyLabel: /);
  });

  test("an unrecognized baseUrl scheme throws ConfigError with the given label", () => {
    const config = configWith({ baseUrl: "ftp://host" });
    expect(() => wsUrlFor(config, "/x", {}, "MyLabel")).toThrow(ConfigError);
    expect(() => wsUrlFor(config, "/x", {}, "MyLabel")).toThrow(/^MyLabel: unrecognized baseUrl scheme/);
  });

  test("defaults the label to MittoWsTransport when omitted", () => {
    const config = configWith({ baseUrl: "" });
    expect(() => wsUrlFor(config, "/x")).toThrow(/^MittoWsTransport: /);
  });
});

describe("createEmitter", () => {
  test("on() delivers emitted args to the registered handler", () => {
    const emitter = createEmitter();
    const seen = [];
    emitter.on("msg", (a, b) => seen.push([a, b]));
    emitter.emit("msg", 1, 2);
    expect(seen).toEqual([[1, 2]]);
  });

  test("on() returns an unsubscribe function that stops future delivery", () => {
    const emitter = createEmitter();
    const seen = [];
    const off = emitter.on("msg", (v) => seen.push(v));
    emitter.emit("msg", "a");
    off();
    emitter.emit("msg", "b");
    expect(seen).toEqual(["a"]);
  });

  test("once() fires exactly once then auto-unsubscribes", () => {
    const emitter = createEmitter();
    const seen = [];
    emitter.once("msg", (v) => seen.push(v));
    emitter.emit("msg", 1);
    emitter.emit("msg", 2);
    expect(seen).toEqual([1]);
  });

  test("emit() with no handlers registered is a silent no-op", () => {
    const emitter = createEmitter();
    expect(() => emitter.emit("nothing")).not.toThrow();
  });

  test("multiple handlers on the same event all receive the emission", () => {
    const emitter = createEmitter();
    const seen = [];
    emitter.on("msg", () => seen.push("a"));
    emitter.on("msg", () => seen.push("b"));
    emitter.emit("msg");
    expect(seen).toEqual(["a", "b"]);
  });

  test("a handler unsubscribing a not-yet-called sibling during emit prevents that sibling from firing this round", () => {
    // The Set-based dispatch loop reflects live mutations mid-iteration: an
    // unsubscribe for an as-yet-unvisited handler removes it before its turn.
    const emitter = createEmitter();
    const seen = [];
    let offB;
    emitter.on("msg", () => {
      seen.push("a");
      offB();
    });
    offB = emitter.on("msg", () => seen.push("b"));
    emitter.emit("msg");
    expect(seen).toEqual(["a"]);
    seen.length = 0;
    emitter.emit("msg");
    expect(seen).toEqual(["a"]);
  });
});
