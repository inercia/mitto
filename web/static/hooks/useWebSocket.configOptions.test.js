/**
 * Execute the real composer callbacks with only their state/transport seams
 * stubbed. This catches global-vs-session routing mistakes without importing
 * the full browser hook or copying its config-state reducer (mitto-6w3).
 */
import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";
import {
  describeConfigAdjustment,
  configAdjustmentDedupKey,
} from "../utils/configOptions.js";

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

// Richer fixture carrying a reasoning-effort-like selector with a display
// `name` and an `options[]` catalog, so describeConfigAdjustment's
// display-name resolution has something real to look up (mitto-bnk).
function sessionWithReasoningEffort(model) {
  const base = session(model);
  return {
    ...base,
    info: {
      ...base.info,
      config_options: [
        ...base.info.config_options,
        {
          id: "thought_level",
          name: "Reasoning Effort",
          category: "reasoning",
          current_value: "high",
          options: [
            { value: "low", name: "Low" },
            { value: "medium", name: "Medium" },
            { value: "high", name: "High" },
          ],
        },
      ],
    },
  };
}

function harness(text = source, { initialSessions } = {}) {
  let sessions = initialSessions ?? {
    active: session("opus"),
    background: session("sonnet"),
  };
  const flushed = [];
  const toasts = [];
  const activeSessionIdRef = { current: "active" };
  const handleSessionMessage = callback(
    "handleSessionMessage",
    {
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
      showToast: (opts) => {
        toasts.push(opts);
      },
      activeSessionIdRef,
      sessionsRef: { current: sessions },
      configAdjustmentNoticeKeysRef: { current: new Set() },
      describeConfigAdjustment,
      configAdjustmentDedupKey,
    },
    text,
  );
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
    toasts,
    activeSessionIdRef,
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

// Builds a full-snapshot broadcast (the shape the backend sends for model
// changes), optionally carrying the new `adjustments` array. The real wire
// format omits the `adjustments` key entirely when there are none, rather
// than sending an empty array, so this mirrors that (mitto-bnk).
const snapshotChange = (sessionId, configOptions, adjustments) => ({
  type: "config_option_changed",
  data: {
    session_id: sessionId,
    config_options: configOptions,
    ...(adjustments !== undefined ? { adjustments } : {}),
  },
});

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

describe("model-change config snapshots and adjustments (mitto-bnk)", () => {
  test("replaces the complete option snapshot and removes selectors that disappeared", () => {
    const h = harness(source, {
      initialSessions: {
        active: sessionWithReasoningEffort("opus"),
        background: session("sonnet"),
      },
    });
    const nextSnapshot = [
      { id: "model", name: "Model", category: "model", current_value: "sonnet" },
      { id: "mode", name: "Mode", category: "mode", current_value: "default" },
    ];

    h.global(snapshotChange("active", nextSnapshot));

    expect(h.sessions.active.info.config_options).toBe(nextSnapshot);
    expect(h.sessions.active.info.config_options).toEqual(nextSnapshot);
    expect(
      h.sessions.active.info.config_options.some(
        (option) => option.id === "thought_level",
      ),
    ).toBe(false);
    expect(h.sessions.background.info.config_options).toHaveLength(2);
    expect(h.flushed).toEqual(["active"]);
  });

  test("an empty snapshot removes all config selectors", () => {
    const h = harness(source, {
      initialSessions: {
        active: sessionWithReasoningEffort("opus"),
        background: session("sonnet"),
      },
    });

    h.global(snapshotChange("active", []));

    expect(h.sessions.active.info.config_options).toEqual([]);
  });

  test("shows a warning for a removed option using its prior display name", () => {
    const h = harness(source, {
      initialSessions: {
        active: sessionWithReasoningEffort("opus"),
        background: session("sonnet"),
      },
    });
    const snapshot = [
      { id: "model", name: "Model", category: "model", current_value: "sonnet" },
    ];

    h.global(
      snapshotChange("active", snapshot, [
        { config_id: "thought_level", reason: "option_removed" },
      ]),
    );

    expect(h.toasts).toHaveLength(1);
    expect(h.toasts[0].style).toBe("warning");
    expect(h.toasts[0].message).toContain("Reasoning Effort");
    expect(h.toasts[0].message).toContain("cleared");
  });

  test("shows a warning for an unsupported value and uses snapshot display names", () => {
    const h = harness(source);
    const snapshot = [
      {
        id: "thought_level",
        name: "Reasoning Effort",
        current_value: "low",
        options: [{ value: "low", name: "Low" }],
      },
    ];

    h.global(
      snapshotChange("active", snapshot, [
        {
          config_id: "thought_level",
          requested_value: "high",
          applied_value: "low",
          reason: "value_not_advertised",
        },
      ]),
    );

    expect(h.toasts).toHaveLength(1);
    expect(h.toasts[0].style).toBe("warning");
    expect(h.toasts[0].message).toContain("Reasoning Effort");
    expect(h.toasts[0].message).toContain("Low");
  });

  test("shows an informational notice when the option snapshot is unconfirmed", () => {
    const h = harness(source, {
      initialSessions: {
        active: sessionWithReasoningEffort("opus"),
        background: session("sonnet"),
      },
    });

    h.global(
      snapshotChange("active", [], [
        { config_id: "thought_level", reason: "snapshot_unconfirmed" },
      ]),
    );

    expect(h.toasts).toHaveLength(1);
    expect(h.toasts[0].style).toBe("info");
    expect(h.toasts[0].message).toContain("Reasoning Effort");
    expect(h.toasts[0].message.toLowerCase()).toContain("couldn't confirm");
  });

  test("shows no notice when adjustments are absent or empty", () => {
    const h = harness();
    const snapshot = [
      { id: "model", category: "model", current_value: "sonnet" },
    ];

    h.global(snapshotChange("active", snapshot));
    h.global(snapshotChange("active", snapshot, []));

    expect(h.toasts).toHaveLength(0);
  });

  test("suppresses duplicate notices and ignores background-session adjustments", () => {
    const h = harness();
    const snapshot = [
      { id: "thought_level", name: "Reasoning Effort", current_value: "low" },
    ];
    const event = snapshotChange("active", snapshot, [
      {
        config_id: "thought_level",
        requested_value: "high",
        applied_value: "low",
        reason: "value_not_advertised",
      },
    ]);

    h.global(event);
    h.global(event);
    expect(h.toasts).toHaveLength(1);

    h.global(
      snapshotChange("background", snapshot, [
        {
          config_id: "thought_level",
          requested_value: "high",
          applied_value: "low",
          reason: "value_not_advertised",
        },
      ]),
    );
    expect(h.toasts).toHaveLength(1);
  });
});
