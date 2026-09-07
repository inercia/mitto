/**
 * Execute the real composer callbacks with only their state/transport seams
 * stubbed. This catches global-vs-session routing mistakes without importing
 * the full browser hook or copying its config-state reducer (mitto-6w3).
 */
import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";

const source = readFileSync(
  new URL("./useWebSocket.js", import.meta.url),
  "utf8",
);

function callback(name, bindings, text = source) {
  const marker = `const ${name} = useCallback(`;
  const start = text.indexOf(marker);
  expect(start).toBeGreaterThan(-1);
  const end = text.indexOf("}, []);", start);
  expect(end).toBeGreaterThan(start);
  return new Function(
    ...Object.keys(bindings),
    `return ${text.slice(start + marker.length, end + 1)};`,
  )(...Object.values(bindings));
}

function session(model) {
  return {
    messages: [{ role: "agent", text: "Existing response" }],
    isStreaming: false,
    info: {
      name: "Conversation",
      config_options: [
        { id: "model", category: "model", current_value: model },
        { id: "mode", category: "mode", current_value: "default" },
      ],
    },
  };
}

function harness(text = source) {
  let sessions = { active: session("opus"), background: session("sonnet") };
  const flushed = [];
  const handleSessionMessage = callback("handleSessionMessage", {
    setSessions: (update) => {
      sessions = update(sessions);
    },
    sessionUpdateSchedulerRef: {
      current: {
        flushSession: (id) => {
          flushed.push(id);
          return false;
        },
      },
    },
    COALESCED_BACKGROUND_MESSAGE_TYPES: new Set(),
    console: { log() {} },
  });
  const handleGlobalEvent = callback(
    "handleGlobalEvent",
    {
      handleSessionMessageRef: { current: handleSessionMessage },
    },
    text,
  );
  return {
    get sessions() {
      return sessions;
    },
    flushed,
    global: handleGlobalEvent,
    perSession: handleSessionMessage,
  };
}

const change = (sessionId, value, configId = "model") => ({
  type: "config_option_changed",
  data: { session_id: sessionId, config_id: configId, value },
});
const model = (h, id = "active") =>
  h.sessions[id].info.config_options[0].current_value;

describe("composer config broadcasts (mitto-6w3)", () => {
  test("global override and restoration update the dropdown's authoritative options", () => {
    const h = harness();
    const initial = h.sessions;
    h.global(change("active", "sonnet"));
    expect(model(h)).toBe("sonnet");
    h.global(change("active", "opus"));
    expect(model(h)).toBe("opus");
    expect(h.sessions.active.info.config_options).not.toBe(
      initial.active.info.config_options,
    );
    expect(h.sessions.active.info.config_options[1]).toBe(
      initial.active.info.config_options[1],
    );
    expect(h.sessions.active.messages).toBe(initial.active.messages);
    expect(h.sessions.active.isStreaming).toBe(false);
    expect(h.sessions.active.info.name).toBe("Conversation");
    expect(h.sessions.background).toBe(initial.background);
    expect(h.flushed).toEqual(["active", "active"]);
  });

  test("reproduces the stale Sonnet value when the global route is absent", () => {
    const globalStart = source.indexOf(
      "const handleGlobalEvent = useCallback(",
    );
    const routeStart = source.indexOf(
      'case "config_option_changed":',
      globalStart,
    );
    const routeEnd = source.indexOf('case "session_created":', routeStart);
    expect(routeStart).toBeGreaterThan(globalStart);
    expect(routeEnd).toBeGreaterThan(routeStart);
    // Mutate only the in-memory source, never the working tree.
    const h = harness(source.slice(0, routeStart) + source.slice(routeEnd));
    h.perSession("active", change("active", "sonnet"));
    h.global(change("active", "opus"));
    expect(model(h)).toBe("sonnet");
  });

  test("targets cached background conversations without changing the active one", () => {
    const h = harness();
    const active = h.sessions.active;
    h.global(change("background", "opus"));
    expect(model(h, "background")).toBe("opus");
    expect(h.sessions.active).toBe(active);
    expect(h.flushed).toEqual(["background"]);
  });

  test("retains per-session delivery compatibility and accepts empty config values", () => {
    const h = harness();
    h.perSession("active", change("active", "sonnet"));
    expect(model(h)).toBe("sonnet");
    h.global(change("active", "", "mode"));
    expect(h.sessions.active.info.config_options[1].current_value).toBe("");
    expect(model(h)).toBe("sonnet");
  });

  test("ignores malformed broadcasts and unknown conversations or options", () => {
    for (const data of [
      undefined,
      null,
      {},
      { config_id: "model", value: "sonnet" },
      { session_id: "", config_id: "model", value: "sonnet" },
      { session_id: 123, config_id: "model", value: "sonnet" },
      { session_id: "active", value: "sonnet" },
      { session_id: "active", config_id: "model" },
      { session_id: "unknown", config_id: "model", value: "sonnet" },
      { session_id: "active", config_id: "unknown", value: "sonnet" },
    ]) {
      const h = harness();
      const initial = h.sessions;
      expect(() =>
        h.global({ type: "config_option_changed", data }),
      ).not.toThrow();
      expect(h.sessions).toEqual(initial);
    }
  });
});
