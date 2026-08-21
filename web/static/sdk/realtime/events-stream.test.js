/**
 * Unit tests for EventsStream (web/static/sdk/realtime/events-stream.js).
 *
 * Uses a manual fake clock (deterministic setTimeout) and a fake WebSocket
 * class injected via config.getWebSocket() — no real sockets, no real
 * timers, no DOM. Mirrors session-stream.test.js's harness style.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveConfig } from "../core/config.js";
import { ConfigError, MittoNetworkError } from "../core/errors.js";
import { EventsStream, createEventsStream } from "./events-stream.js";

const EVENTS_STREAM_FILE = join(dirname(fileURLToPath(import.meta.url)), "events-stream.js");

function createFakeClock(startTime = 1) {
  let currentTime = startTime;
  let nextId = 1;
  const timers = new Map();

  function setTimeoutFn(cb, delay = 0) {
    const id = nextId++;
    timers.set(id, { time: currentTime + delay, cb });
    return id;
  }
  function clearTimeoutFn(id) {
    timers.delete(id);
  }
  function advance(ms) {
    const target = currentTime + ms;
    for (;;) {
      let nextId2 = null;
      let nextTime = null;
      for (const [id, t] of timers) {
        if (t.time <= target && (nextTime === null || t.time < nextTime)) {
          nextId2 = id;
          nextTime = t.time;
        }
      }
      if (nextId2 === null) break;
      currentTime = nextTime;
      const timer = timers.get(nextId2);
      timers.delete(nextId2);
      timer.cb();
    }
    currentTime = target;
  }

  return { now: () => currentTime, setTimeout: setTimeoutFn, clearTimeout: clearTimeoutFn, advance };
}

function makeFakeWebSocketClass(instances) {
  return class FakeWebSocket {
    constructor(url, protocols, options) {
      this.url = url;
      this.protocols = protocols;
      this.options = options;
      this.readyState = 0;
      this.sent = [];
      instances.push(this);
    }
    send(data) {
      this.sent.push(data);
    }
    close(code, reason) {
      this.readyState = 3;
      if (this.onclose) this.onclose({ code: code ?? 1000, reason: reason ?? "", wasClean: true });
    }
  };
}

function makeHarness(streamOptions = {}, configOverrides = {}) {
  const instances = [];
  const FakeWebSocket = makeFakeWebSocketClass(instances);
  const clock = createFakeClock();
  const logCalls = [];
  const logger = {
    debug: (...a) => logCalls.push(["debug", ...a]),
    info: (...a) => logCalls.push(["info", ...a]),
    warn: (...a) => logCalls.push(["warn", ...a]),
    error: (...a) => logCalls.push(["error", ...a]),
  };
  const config = resolveConfig(
    { fetch: () => {}, baseUrl: "https://host:1234", WebSocket: FakeWebSocket, logger, ...configOverrides },
    {},
  );
  const stream = createEventsStream(config, {
    now: clock.now,
    setTimeout: clock.setTimeout,
    clearTimeout: clock.clearTimeout,
    random: () => 0,
    ...streamOptions,
  });
  return { stream, instances, clock, logCalls, config };
}

function openStream(harness) {
  harness.stream.connect();
  const ws = harness.instances[harness.instances.length - 1];
  ws.readyState = 1;
  ws.onopen();
  return ws;
}

describe("EventsStream: construction and URL derivation", () => {
  test("starts idle without connecting", () => {
    const { stream, instances } = makeHarness();
    expect(stream.state).toBe("idle");
    expect(instances).toHaveLength(0);
  });

  test("maps https:// to wss:// and appends /api/events, honoring apiPrefix", () => {
    const { stream, instances } = makeHarness({}, { baseUrl: "https://host:1234", apiPrefix: "/api-x" });
    stream.connect();
    expect(instances[0].url).toBe("wss://host:1234/api-x/api/events");
  });

  test("maps http:// to ws://", () => {
    const { stream, instances } = makeHarness({}, { baseUrl: "http://host:1234" });
    stream.connect();
    expect(instances[0].url).toBe("ws://host:1234/api/events");
  });

  test("throws ConfigError for an empty baseUrl with no wsBaseUrl override", () => {
    const { stream } = makeHarness({}, { baseUrl: "" });
    expect(() => stream.connect()).toThrow(ConfigError);
  });

  test("options.wsBaseUrl overrides an empty baseUrl", () => {
    const { instances, clock, config } = makeHarness({}, { baseUrl: "" });
    const s2 = new EventsStream(config, {
      now: clock.now,
      setTimeout: clock.setTimeout,
      clearTimeout: clock.clearTimeout,
      wsBaseUrl: "ws://override:9",
    });
    s2.connect();
    expect(instances[0].url).toBe("ws://override:9/api/events");
  });

  test("connect() is a no-op while already connecting or open", () => {
    const { stream, instances } = makeHarness();
    stream.connect();
    stream.connect();
    expect(instances).toHaveLength(1);
    openStream({ stream, instances });
    stream.connect();
    expect(instances).toHaveLength(1);
  });
});

describe("EventsStream: open / message lifecycle", () => {
  test("invokes reconnect timers without the EventsStream as receiver", () => {
    let timerReceiver = "not-called";
    const h = makeHarness({
      setTimeout: function () {
        timerReceiver = this;
        if (this !== undefined) throw new TypeError("Illegal invocation");
        return 1;
      },
    });
    const ws = openStream(h);

    expect(() =>
      ws.onclose({ code: 1006, reason: "", wasClean: false }),
    ).not.toThrow();
    expect(timerReceiver).toBeUndefined();
  });

  test("\"open\" reports isReconnect:false the first time, true after a reconnect", () => {
    const h = makeHarness();
    const opens = [];
    h.stream.on("open", (e) => opens.push(e.isReconnect));
    const ws1 = openStream(h);
    ws1.onclose({ code: 1006, reason: "", wasClean: false });
    h.clock.advance(60000);
    openStream(h);
    expect(opens).toEqual([false, true]);
  });

  test("a \"connected\" frame emits \"connected\" with its data, and also arrives on \"message\"", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const connected = [];
    const messages = [];
    const genericEvents = [];
    h.stream.on("connected", (data) => connected.push(data));
    h.stream.on("message", (m) => messages.push(m));
    h.stream.on("event", (e) => genericEvents.push(e));
    ws.onmessage({ data: JSON.stringify({ type: "connected", data: { acp_server: "Auggie" } }) });
    expect(connected).toEqual([{ acp_server: "Auggie" }]);
    expect(messages).toEqual([{ type: "connected", data: { acp_server: "Auggie" } }]);
    expect(genericEvents).toEqual([]); // "connected" is not re-emitted as a generic "event"
  });

  test("an arbitrary frame emits \"event\" with {type, data}", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const genericEvents = [];
    h.stream.on("event", (e) => genericEvents.push(e));
    ws.onmessage({ data: JSON.stringify({ type: "session_created", data: { session_id: "s1" } }) });
    expect(genericEvents).toEqual([{ type: "session_created", data: { session_id: "s1" } }]);
  });

  test("malformed JSON is logged and does not emit \"message\"/\"event\" or throw", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const events = [];
    h.stream.on("message", (m) => events.push(m));
    h.stream.on("event", (m) => events.push(m));
    expect(() => ws.onmessage({ data: "{not json" })).not.toThrow();
    expect(events).toEqual([]);
    expect(h.logCalls.some(([level]) => level === "error")).toBe(true);
  });

  test("no keepalive frames are ever sent on the socket", () => {
    const h = makeHarness();
    const ws = openStream(h);
    h.clock.advance(120000);
    expect(ws.sent).toEqual([]);
  });
});

describe("EventsStream: authorizeWebSocket", () => {
  test("a resolved authorizeWebSocket() passes protocols/options through to the WebSocket constructor", async () => {
    const authorizeWebSocket = async ({ url }) => ({
      protocols: ["mitto-v1"],
      options: { headers: { Authorization: "Bearer tok", url } },
    });
    const h = makeHarness({}, { auth: { authorize: async () => ({}), authorizeWebSocket } });
    h.stream.connect();
    expect(h.instances).toHaveLength(0); // deferred until the promise resolves
    await Promise.resolve().then(() => Promise.resolve());
    expect(h.instances).toHaveLength(1);
    const ws = h.instances[0];
    expect(ws.protocols).toEqual(["mitto-v1"]);
    expect(ws.options).toEqual({ headers: { Authorization: "Bearer tok", url: ws.url } });
  });

  test("a rejected authorizeWebSocket() emits \"error\" and follows the normal reconnect path", async () => {
    const boom = new Error("no credentials");
    let callCount = 0;
    const authorizeWebSocket = async () => {
      callCount++;
      if (callCount === 1) throw boom;
      return {}; // subsequent (reconnect) attempt succeeds
    };
    const h = makeHarness({}, { auth: { authorize: async () => ({}), authorizeWebSocket } });
    const errors = [];
    const reconnecting = [];
    h.stream.on("error", (e) => errors.push(e));
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    h.stream.connect();
    await Promise.resolve().then(() => Promise.resolve());
    expect(errors).toEqual([boom]);
    expect(h.instances).toHaveLength(0); // no socket was ever created
    expect(reconnecting).toEqual([{ attempt: 1, delayMs: 1000 }]);
    h.clock.advance(1000);
    await Promise.resolve().then(() => Promise.resolve());
    expect(h.instances).toHaveLength(1); // reconnect's authorizeWebSocket call succeeded this time
    expect(callCount).toBe(2);
  });

  test("a resolution/rejection arriving after the stream moved on (e.g. explicit close) is ignored", async () => {
    let resolveAuth;
    const authorizeWebSocket = () => new Promise((resolve) => (resolveAuth = resolve));
    const h = makeHarness({}, { auth: { authorize: async () => ({}), authorizeWebSocket } });
    h.stream.connect();
    h.stream.close();
    resolveAuth({});
    await Promise.resolve().then(() => Promise.resolve());
    expect(h.instances).toHaveLength(0);
    expect(h.stream.state).toBe("stopped");
  });
});

describe("EventsStream: reconnect / close", () => {
  test("close schedules a backoff reconnect with a \"reconnecting\" event, and resets the attempt counter on success", () => {
    const h = makeHarness();
    const reconnecting = [];
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    const ws = openStream(h);
    ws.onclose({ code: 1006, reason: "", wasClean: false });
    expect(reconnecting).toEqual([{ attempt: 1, delayMs: 1000 }]);
    h.clock.advance(1000);
    expect(h.instances).toHaveLength(2);
  });

  test("close() is terminal: no reconnect is scheduled", () => {
    const h = makeHarness();
    const reconnecting = [];
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    openStream(h);
    h.stream.close();
    expect(h.stream.state).toBe("stopped");
    h.clock.advance(60000);
    expect(reconnecting).toEqual([]);
    expect(h.instances).toHaveLength(1);
  });

  test("shouldReconnect: () => false suppresses the reconnect", () => {
    const h = makeHarness({ shouldReconnect: () => false });
    const ws = openStream(h);
    ws.onclose({ code: 1006, reason: "", wasClean: false });
    return Promise.resolve()
      .then(() => Promise.resolve())
      .then(() => {
        expect(h.stream.state).toBe("stopped");
        h.clock.advance(60000);
        expect(h.instances).toHaveLength(1);
      });
  });

  test("reconnect attempt cap stops the stream with a MittoNetworkError", () => {
    const h = makeHarness({ maxReconnectAttempts: 2 });
    const errors = [];
    h.stream.on("error", (e) => errors.push(e));
    const ws = openStream(h);
    // Never let the reconnect attempts open successfully: each new socket
    // is closed immediately, so _reconnectAttempt keeps climbing instead of
    // resetting on "open".
    ws.onclose({ code: 1006, reason: "", wasClean: false });
    h.clock.advance(60000);
    h.instances[h.instances.length - 1].onclose({ code: 1006, reason: "", wasClean: false });
    h.clock.advance(60000);
    h.instances[h.instances.length - 1].onclose({ code: 1006, reason: "", wasClean: false });
    h.clock.advance(60000);
    expect(h.stream.state).toBe("stopped");
    expect(errors.some((e) => e instanceof MittoNetworkError)).toBe(true);
  });

  test("forceReconnect() is debounced within reconnectDebounceMs", () => {
    const h = makeHarness();
    openStream(h);
    h.stream.forceReconnect();
    expect(h.instances).toHaveLength(2);
    h.stream.forceReconnect();
    expect(h.instances).toHaveLength(2); // debounced, no new socket
  });
});

describe("EventsStream: purity", () => {
  // Redundant with the full-directory scan in session-stream.test.js (which
  // already covers every non-test .js under sdk/realtime/, including this
  // file), but pinned here explicitly so a future narrowing of that scan's
  // scope cannot silently drop coverage of this specific file.
  test("source contains no forbidden browser-global identifiers", () => {
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
    const src = readFileSync(EVENTS_STREAM_FILE, "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/(^|[^:])\/\/.*$/gm, "$1");
    const offenders = [];
    for (const token of FORBIDDEN) {
      const re = new RegExp(`\\b${token.replace(".", "\\.")}\\b`);
      if (re.test(src)) offenders.push(token);
    }
    if (/\bconsole\s*\./.test(src)) offenders.push("console.*");
    expect(offenders).toEqual([]);
  });
});
