/**
 * Unit tests for SessionStream (web/static/sdk/realtime/session-stream.js).
 *
 * Uses a manual fake clock (deterministic setTimeout/setInterval) and a fake
 * WebSocket class injected via config.getWebSocket() — no real sockets, no
 * real timers, no DOM. Mirrors the injection style already exercised by
 * config.test.js / browser.test.js.
 */
import { readdirSync, readFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { resolveConfig } from "../core/config.js";
import { ConfigError, MittoNetworkError } from "../core/errors.js";
import {
  SessionStream,
  createSessionStream,
  calculateReconnectDelay,
  isReconnectLimitReached,
  SESSION_STREAM_CONSTANTS,
} from "./session-stream.js";

const REALTIME_DIR = dirname(fileURLToPath(import.meta.url));

/**
 * Deterministic virtual clock: no real timers ever fire in these tests.
 * Starts at a non-zero time so `now() > 0` truthiness checks in the code
 * under test (e.g. forceReconnect's debounce guard) behave as they would
 * against a real Date.now() clock, which is never exactly 0.
 */
function createFakeClock(startTime = 1) {
  let currentTime = startTime;
  let nextId = 1;
  const timers = new Map();

  function setTimeoutFn(cb, delay = 0) {
    const id = nextId++;
    timers.set(id, { kind: "timeout", time: currentTime + delay, cb });
    return id;
  }
  function clearTimeoutFn(id) {
    timers.delete(id);
  }
  function setIntervalFn(cb, delay) {
    const id = nextId++;
    timers.set(id, { kind: "interval", time: currentTime + delay, interval: delay, cb });
    return id;
  }
  function clearIntervalFn(id) {
    timers.delete(id);
  }
  /** Advances virtual time by `ms`, firing every timer/interval due along the way. */
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
      if (!timer) continue;
      if (timer.kind === "timeout") {
        timers.delete(nextId2);
      } else {
        timer.time = currentTime + timer.interval;
      }
      timer.cb();
    }
    currentTime = target;
  }

  return {
    now: () => currentTime,
    setTimeout: setTimeoutFn,
    clearTimeout: clearTimeoutFn,
    setInterval: setIntervalFn,
    clearInterval: clearIntervalFn,
    advance,
  };
}

/** Fake WebSocket class; pushes every instance into `instances` for inspection. */
function makeFakeWebSocketClass(instances) {
  return class FakeWebSocket {
    constructor(url) {
      this.url = url;
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
  const stream = createSessionStream(config, "sess-1", {
    now: clock.now,
    setTimeout: clock.setTimeout,
    clearTimeout: clock.clearTimeout,
    setInterval: clock.setInterval,
    clearInterval: clock.clearInterval,
    random: () => 0,
    ...streamOptions,
  });
  return { stream, instances, clock, logCalls, config };
}

/** Drives a stream to the "open" state, returning its FakeWebSocket. */
function openStream(harness) {
  harness.stream.connect();
  const ws = harness.instances[harness.instances.length - 1];
  ws.readyState = 1;
  ws.onopen();
  return ws;
}

describe("calculateReconnectDelay", () => {
  test("attempt 0 with no jitter equals the base delay", () => {
    expect(calculateReconnectDelay(0, { random: () => 0 })).toBe(1000);
  });

  test("grows exponentially with attempt number", () => {
    expect(calculateReconnectDelay(1, { random: () => 0 })).toBe(2000);
    expect(calculateReconnectDelay(2, { random: () => 0 })).toBe(4000);
    expect(calculateReconnectDelay(3, { random: () => 0 })).toBe(8000);
  });

  test("is capped at maxDelay for large attempt numbers", () => {
    expect(calculateReconnectDelay(10, { random: () => 0 })).toBe(30000);
    expect(calculateReconnectDelay(50, { random: () => 0 })).toBe(30000);
  });

  test("adds jitter proportional to jitterFactor and the random draw", () => {
    // exponentialDelay=1000, jitter = 1000*0.3*1 = 300 -> 1300
    expect(calculateReconnectDelay(0, { random: () => 1 })).toBe(1300);
    // custom jitterFactor
    expect(calculateReconnectDelay(0, { random: () => 1, jitterFactor: 0.5 })).toBe(1500);
  });

  test("honors custom baseDelay/maxDelay overrides", () => {
    expect(calculateReconnectDelay(0, { baseDelay: 500, random: () => 0 })).toBe(500);
    expect(calculateReconnectDelay(5, { baseDelay: 500, maxDelay: 2000, random: () => 0 })).toBe(2000);
  });
});

describe("isReconnectLimitReached", () => {
  test("false below the default cap (15), true at/above it", () => {
    expect(isReconnectLimitReached(14)).toBe(false);
    expect(isReconnectLimitReached(15)).toBe(true);
    expect(isReconnectLimitReached(16)).toBe(true);
  });

  test("honors a custom maxAttempts option", () => {
    expect(isReconnectLimitReached(2, { maxAttempts: 3 })).toBe(false);
    expect(isReconnectLimitReached(3, { maxAttempts: 3 })).toBe(true);
  });
});

describe("SessionStream: construction and URL derivation", () => {
  test("starts in the idle state without connecting", () => {
    const { stream, instances } = makeHarness();
    expect(stream.state).toBe("idle");
    expect(instances).toHaveLength(0);
  });

  test("maps an https:// baseUrl to wss:// and appends the session path", () => {
    const { stream, instances } = makeHarness({}, { baseUrl: "https://host:1234", apiPrefix: "/api-x" });
    stream.connect();
    expect(instances[0].url).toBe("wss://host:1234/api-x/api/sessions/sess-1/ws");
  });

  test("maps an http:// baseUrl to ws://", () => {
    const { stream, instances } = makeHarness({}, { baseUrl: "http://host:1234" });
    stream.connect();
    expect(instances[0].url).toBe("ws://host:1234/api/sessions/sess-1/ws");
  });

  test("passes through an already ws(s):// baseUrl unchanged", () => {
    const { stream, instances } = makeHarness({}, { baseUrl: "wss://host:1234" });
    stream.connect();
    expect(instances[0].url).toBe("wss://host:1234/api/sessions/sess-1/ws");
  });

  test("URL-encodes the session id", () => {
    const { instances, clock, config } = makeHarness();
    const s2 = new SessionStream(config, "a/b c", {
      now: clock.now,
      setTimeout: clock.setTimeout,
      clearTimeout: clock.clearTimeout,
      setInterval: clock.setInterval,
      clearInterval: clock.clearInterval,
    });
    s2.connect();
    expect(instances[0].url).toContain(encodeURIComponent("a/b c"));
  });

  test("throws ConfigError for an empty baseUrl with no wsBaseUrl override", () => {
    const { stream } = makeHarness({}, { baseUrl: "" });
    expect(() => stream.connect()).toThrow(ConfigError);
  });

  test("options.wsBaseUrl overrides an empty/relative config.baseUrl", () => {
    const { instances, clock, config } = makeHarness({}, { baseUrl: "" });
    const s2 = new SessionStream(config, "sess-1", {
      now: clock.now,
      setTimeout: clock.setTimeout,
      clearTimeout: clock.clearTimeout,
      setInterval: clock.setInterval,
      clearInterval: clock.clearInterval,
      wsBaseUrl: "ws://override:9",
    });
    s2.connect();
    expect(instances[0].url).toBe("ws://override:9/api/sessions/sess-1/ws");
  });

  test("throws ConfigError for an unrecognized baseUrl scheme", () => {
    const { stream } = makeHarness({}, { baseUrl: "ftp://host" });
    expect(() => stream.connect()).toThrow(ConfigError);
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

describe("SessionStream: open / message / close lifecycle", () => {
  test("open transitions to \"open\" and emits open + health(healthy=true)", () => {
    const h = makeHarness();
    const events = [];
    h.stream.on("open", () => events.push("open"));
    h.stream.on("health", (e) => events.push(["health", e.healthy]));
    openStream(h);
    expect(h.stream.state).toBe("open");
    expect(events).toEqual(["open", ["health", true]]);
  });

  test("emits a parsed \"message\" event for a well-formed non-keepalive message", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const received = [];
    h.stream.on("message", (m) => received.push(m));
    ws.onmessage({ data: JSON.stringify({ type: "agent_message", data: { seq: 5 } }) });
    expect(received).toEqual([{ type: "agent_message", data: { seq: 5 } }]);
  });

  test("updates the seq store from a message's data.seq", () => {
    const h = makeHarness();
    const ws = openStream(h);
    expect(h.stream.lastSeenSeq()).toBe(0);
    ws.onmessage({ data: JSON.stringify({ type: "x", data: { seq: 7 } }) });
    expect(h.stream.lastSeenSeq()).toBe(7);
    // A lower/equal seq never regresses the store.
    ws.onmessage({ data: JSON.stringify({ type: "x", data: { seq: 3 } }) });
    expect(h.stream.lastSeenSeq()).toBe(7);
  });

  test("malformed JSON is logged and does not emit \"message\" or throw", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const received = [];
    h.stream.on("message", (m) => received.push(m));
    expect(() => ws.onmessage({ data: "{not json" })).not.toThrow();
    expect(received).toEqual([]);
    expect(h.logCalls.some(([level]) => level === "error")).toBe(true);
  });

  test("keepalive_ack updates keepalive state, emits health(true), and is not surfaced as \"message\"", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const received = [];
    const health = [];
    h.stream.on("message", (m) => received.push(m));
    h.stream.on("health", (e) => health.push(e.healthy));
    ws.onmessage({ data: JSON.stringify({ type: "keepalive_ack" }) });
    expect(received).toEqual([]);
    expect(health[health.length - 1]).toBe(true);
  });

  test("explicit close() never schedules a reconnect and lands on \"stopped\"", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const reconnecting = [];
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    h.stream.close();
    expect(h.stream.state).toBe("stopped");
    h.clock.advance(60000);
    expect(reconnecting).toEqual([]);
    expect(h.instances).toHaveLength(1);
  });

  test("close() on an idle stream (no socket) goes straight to \"stopped\"", () => {
    const { stream } = makeHarness();
    stream.close();
    expect(stream.state).toBe("stopped");
  });

  test("an unexpected close schedules a reconnect with backoff and emits \"reconnecting\"", () => {
    const h = makeHarness();
    const ws = openStream(h);
    const reconnecting = [];
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    ws.close(1006, "abnormal");
    expect(h.stream.state).toBe("closed");
    expect(reconnecting).toEqual([{ attempt: 1, delayMs: 1000 }]);
    expect(h.instances).toHaveLength(1);
    h.clock.advance(1000);
    expect(h.instances).toHaveLength(2);
    expect(h.stream.state).toBe("connecting");
  });

  test("a successful reconnect resets the attempt counter", () => {
    const h = makeHarness();
    let ws = openStream(h);
    ws.close(1006, "abnormal");
    h.clock.advance(1000);
    ws = h.instances[h.instances.length - 1];
    ws.readyState = 1;
    ws.onopen();
    expect(h.stream.state).toBe("open");
    // Drop again: the next backoff should restart from attempt 0 (1000ms), not attempt 2.
    const reconnecting = [];
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    ws.close(1006, "abnormal");
    expect(reconnecting).toEqual([{ attempt: 1, delayMs: 1000 }]);
  });

  test("reaching the reconnect attempt cap stops retrying and emits an error", () => {
    // maxReconnectAttempts=1: attempt 0 (initial) may still schedule one
    // retry; the close following that retry (now at attempt 1) hits the cap.
    const h = makeHarness({ maxReconnectAttempts: 1 });
    const errors = [];
    h.stream.on("error", (e) => errors.push(e));
    let ws = openStream(h);
    ws.close(1006, "abnormal"); // attempt 0 -> schedules retry -> attempt becomes 1
    h.clock.advance(60000);
    ws = h.instances[h.instances.length - 1];
    ws.close(1006, "abnormal"); // attempt 1 >= cap (1) -> stop
    expect(h.stream.state).toBe("stopped");
    expect(errors).toHaveLength(1);
    expect(errors[0]).toBeInstanceOf(MittoNetworkError);
    // No further reconnect attempts are made past the cap.
    const countBefore = h.instances.length;
    h.clock.advance(60000);
    expect(h.instances).toHaveLength(countBefore);
  });

  test("a stale/superseded socket's late close/open callbacks are ignored", () => {
    const h = makeHarness();
    const ws1 = openStream(h);
    // Force a new connection while ws1 is still around (e.g. via forceReconnect).
    h.stream.forceReconnect();
    const ws2 = h.instances[h.instances.length - 1];
    expect(ws2).not.toBe(ws1);
    // ws1's belated onclose must not disturb the now-current ws2-driven state.
    ws2.readyState = 1;
    ws2.onopen();
    expect(h.stream.state).toBe("open");
    ws1.onclose({ code: 1000, reason: "", wasClean: true });
    expect(h.stream.state).toBe("open");
  });
});

describe("SessionStream: forceReconnect debounce", () => {
  test("collapses a burst of forceReconnect() calls within the debounce window", () => {
    const h = makeHarness();
    const ws1 = openStream(h);
    h.stream.forceReconnect();
    expect(h.instances).toHaveLength(2);
    // Still within the 3000ms debounce window: swallowed.
    h.clock.advance(1000);
    h.stream.forceReconnect();
    expect(h.instances).toHaveLength(2);
    void ws1;
  });

  test("allows a new forceReconnect() once the debounce window has elapsed", () => {
    const h = makeHarness();
    openStream(h);
    h.stream.forceReconnect();
    expect(h.instances).toHaveLength(2);
    h.clock.advance(SESSION_STREAM_CONSTANTS.RECONNECT_DEBOUNCE_MS);
    h.stream.forceReconnect();
    expect(h.instances).toHaveLength(3);
  });
});

describe("SessionStream: keepalive and zombie detection", () => {
  test("sends a keepalive frame carrying last_seen_seq every keepaliveIntervalMs", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    const ws = openStream(h);
    ws.onmessage({ data: JSON.stringify({ type: "x", data: { seq: 9 } }) });
    h.clock.advance(1000);
    expect(ws.sent).toHaveLength(1);
    const frame = JSON.parse(ws.sent[0]);
    expect(frame.type).toBe("keepalive");
    expect(frame.data.last_seen_seq).toBe(9);
  });

  test("an ack clears the pending flag and resets the missed counter", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    const ws = openStream(h);
    h.clock.advance(1000);
    ws.onmessage({ data: JSON.stringify({ type: "keepalive_ack" }) });
    expect(h.stream.isHealthy()).toBe(true);
  });

  test("misses accumulate and force-close the socket at the default threshold (2)", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    const ws = openStream(h);
    const health = [];
    h.stream.on("health", (e) => health.push(e.healthy));
    const reconnecting = [];
    h.stream.on("reconnecting", (e) => reconnecting.push(e));
    h.clock.advance(1000); // sends #1, no ack yet
    h.clock.advance(1000); // miss #1 (pending was true), sends #2
    h.clock.advance(1000); // miss #2 (pending was true) -> reaches default max (2) -> force close
    expect(ws.readyState).toBe(3);
    expect(health).toContain(false);
    // Force-close feeds into the normal (non-explicit) close path -> reconnect scheduled.
    expect(reconnecting).toHaveLength(1);
  });

  test("large sessions (lastSeenSeq > 500) tolerate up to 4 missed acks before force-closing", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    const ws = openStream(h);
    ws.onmessage({ data: JSON.stringify({ type: "x", data: { seq: 501 } }) });
    h.clock.advance(1000); // send #1
    h.clock.advance(1000); // miss #1, send #2
    h.clock.advance(1000); // miss #2, send #3 -- would have force-closed a small session
    expect(ws.readyState).not.toBe(3);
    h.clock.advance(1000); // miss #3, send #4
    h.clock.advance(1000); // miss #4 -> reaches large-session max (4) -> force close
    expect(ws.readyState).toBe(3);
  });

  test("isSyncInFlight() suppresses miss-counting while true", () => {
    let syncing = true;
    const h = makeHarness({ keepaliveIntervalMs: 1000, isSyncInFlight: () => syncing });
    const ws = openStream(h);
    h.clock.advance(1000);
    h.clock.advance(1000);
    h.clock.advance(1000);
    h.clock.advance(1000);
    h.clock.advance(1000);
    // Well past the default miss threshold, but every tick was suppressed.
    expect(ws.readyState).not.toBe(3);
    syncing = false;
  });

  test("keepalive stops after close() (no further sends)", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    const ws = openStream(h);
    h.stream.close();
    const sentBefore = ws.sent.length;
    h.clock.advance(10000);
    expect(ws.sent).toHaveLength(sentBefore);
  });
});

describe("SessionStream: send / sendWhenConnected", () => {
  test("send() returns true and writes to the socket when open", () => {
    const h = makeHarness();
    const ws = openStream(h);
    expect(h.stream.send({ type: "ping" })).toBe(true);
    expect(JSON.parse(ws.sent[0])).toEqual({ type: "ping" });
  });

  test("send() returns false without a connection", () => {
    const { stream } = makeHarness();
    expect(stream.send({ type: "ping" })).toBe(false);
  });

  test("sendWhenConnected() sends immediately when already open", async () => {
    const h = makeHarness();
    const ws = openStream(h);
    await h.stream.sendWhenConnected({ type: "ping" });
    expect(JSON.parse(ws.sent[0])).toEqual({ type: "ping" });
  });

  test("sendWhenConnected() connects first, then sends once open", async () => {
    const h = makeHarness();
    expect(h.stream.state).toBe("idle");
    const pending = h.stream.sendWhenConnected({ type: "ping" });
    expect(h.stream.state).toBe("connecting");
    const ws = h.instances[0];
    ws.readyState = 1;
    ws.onopen();
    await pending;
    expect(JSON.parse(ws.sent[0])).toEqual({ type: "ping" });
  });

  test("sendWhenConnected() rejects with MittoNetworkError after the timeout elapses", async () => {
    const h = makeHarness();
    const pending = h.stream.sendWhenConnected({ type: "ping" }, { timeout: 500 });
    h.clock.advance(500);
    await expect(pending).rejects.toBeInstanceOf(MittoNetworkError);
  });

  test("sendWhenConnected() does not fire twice if open arrives after a prior call already resolved", async () => {
    const h = makeHarness();
    const ws = openStream(h);
    await h.stream.sendWhenConnected({ type: "a" });
    await h.stream.sendWhenConnected({ type: "b" });
    expect(ws.sent.map((s) => JSON.parse(s).type)).toEqual(["a", "b"]);
  });
});

describe("SessionStream: isHealthy()", () => {
  test("false before any connection is made", () => {
    const { stream } = makeHarness();
    expect(stream.isHealthy()).toBe(false);
  });

  test("true immediately after opening", () => {
    const h = makeHarness();
    openStream(h);
    expect(h.stream.isHealthy()).toBe(true);
  });

  test("false once the connection is closed", () => {
    const h = makeHarness();
    const ws = openStream(h);
    ws.close(1006, "abnormal");
    expect(h.stream.isHealthy()).toBe(false);
  });

  test("false once elapsed-since-ack exceeds 2x the keepalive interval", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    openStream(h);
    h.clock.advance(2001);
    expect(h.stream.isHealthy()).toBe(false);
  });

  test("false while there is any outstanding missed-ack count", () => {
    const h = makeHarness({ keepaliveIntervalMs: 1000 });
    openStream(h);
    h.clock.advance(1000); // send #1
    h.clock.advance(1000); // miss #1 recorded
    expect(h.stream.isHealthy()).toBe(false);
  });
});

describe("SessionStream: event subscription", () => {
  test("on() returns an unsubscribe function that stops future delivery", () => {
    const h = makeHarness();
    const calls = [];
    const off = h.stream.on("open", () => calls.push(1));
    off();
    openStream(h);
    expect(calls).toEqual([]);
  });

  test("once() fires exactly one time", () => {
    const h = makeHarness();
    const calls = [];
    h.stream.on("open", () => {}); // unrelated persistent listener, sanity noise
    h.stream.once("health", (e) => calls.push(e.healthy));
    const ws = openStream(h);
    ws.onmessage({ data: JSON.stringify({ type: "keepalive_ack" }) });
    expect(calls).toEqual([true]);
  });
});

describe("SessionStream: lastSeenSeq() and seqStore isolation", () => {
  test("defaults to 0 for a session the store has never seen", () => {
    const { stream } = makeHarness();
    expect(stream.lastSeenSeq()).toBe(0);
  });

  test("a custom injected seqStore is honored (read and write)", () => {
    const seqStore = { get: () => 42, set: () => {} };
    const h = makeHarness({ seqStore });
    expect(h.stream.lastSeenSeq()).toBe(42);
  });
});

describe("createSessionStream()", () => {
  test("returns a SessionStream instance bound to the given config/sessionId", () => {
    const config = resolveConfig({ fetch: () => {}, baseUrl: "https://h", WebSocket: class {} }, {});
    const s = createSessionStream(config, "sess-x");
    expect(s).toBeInstanceOf(SessionStream);
    expect(s.state).toBe("idle");
  });

  test("does not connect automatically", () => {
    const instances = [];
    const FakeWebSocket = makeFakeWebSocketClass(instances);
    const config = resolveConfig({ fetch: () => {}, baseUrl: "https://h", WebSocket: FakeWebSocket }, {});
    createSessionStream(config, "sess-x");
    expect(instances).toHaveLength(0);
  });
});

describe("no-browser-globals source guarantee (sdk/realtime/**)", () => {
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

  /** Strip block/line comments so doc mentions of forbidden words don't false-positive. */
  function stripComments(src) {
    return src.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/.*$/gm, "$1");
  }

  test("no forbidden browser-global identifiers appear under sdk/realtime/", () => {
    const offenders = [];
    for (const file of jsFilesUnder(REALTIME_DIR)) {
      const src = stripComments(readFileSync(file, "utf8"));
      for (const token of FORBIDDEN) {
        const re = new RegExp(`\\b${token.replace(".", "\\.")}\\b`);
        if (re.test(src)) offenders.push(`${file}: ${token}`);
      }
      if (/\bconsole\s*\./.test(src)) offenders.push(`${file}: console.*`);
    }
    expect(offenders).toEqual([]);
  });
});
