import { readFileSync } from "node:fs";
import { describe, test, expect } from "../utils/testing/testGlobals.js";
import { createModelTagRequests } from "../utils/modelTagRequests.js";

const source = readFileSync(new URL("./useWSConfigOptions.js", import.meta.url), "utf8");

// Execute the real hook with deterministic hook/timer seams, not a copied hook.
function harness({ deferEffects = false } = {}) {
  const refs = [];
  const effects = [];
  const writes = [];
  const timers = new Map();
  let cursor = 0;
  let timerId = 0;
  const hook = new Function("useRef", "useEffect", "useCallback", "setConfigOptions", "createModelTagRequests",
    `${source.slice(source.indexOf("export function")).replace("export ", "")}; return useWSConfigOptions;`)(
    (value) => { const index = cursor++; return refs[index] ?? (refs[index] = { current: value }); },
    (setup, deps) => {
      const index = cursor++;
      const previous = effects[index];
      if (!previous || deps.some((value, i) => value !== previous.deps[i])) {
        previous?.cleanup?.();
        effects[index] = { setup, deps, cleanup: deferEffects ? undefined : setup() };
      }
    },
    (fn) => fn,
    (...args) => writes.push(args),
    (options) => createModelTagRequests({ ...options,
      setTimeoutFn: (fn) => { timers.set(++timerId, fn); return timerId; },
      clearTimeoutFn: (id) => timers.delete(id),
    }),
  );
  return {
    writes, timers,
    render(active, send) { cursor = 0; return hook({ info: { config_options: [active] } }, active, send); },
    cleanup() { for (const effect of effects) effect?.cleanup?.(); },
    flushEffects() { for (const effect of effects) if (effect && !effect.cleanup) effect.cleanup = effect.setup(); },
    replay() { for (const effect of effects) if (effect) effect.cleanup = effect.setup(); },
  };
}

describe("useWSConfigOptions tag commands", () => {
  test("a press before passive setup is sent and remains pending through setup", async () => {
    const h = harness({ deferEffects: true });
    const sent = [];
    const api = h.render("B", (...args) => { sent.push(args); return true; });
    const p = api.applyModelTag("A", "Tag");
    expect(sent.length).toBe(1);
    h.flushEffects();
    api.handleModelTagApplied("A", { ...sent[0][1].data, session_id: "A", status: "applied" });
    expect(await p).toEqual({ status: "applied" });
    h.cleanup();
  });

  test("explicit target and current sender win over active-session changes; setter/store retained", async () => {
    const h = harness();
    const firstSends = [];
    const currentSends = [];
    const first = h.render("A", (...args) => { firstSends.push(args); return true; });
    first.setConfigOption("model", "old-dropdown-value");
    const current = h.render("B", (...args) => { currentSends.push(args); return true; });
    const p = first.applyModelTag("A", " Tag ");
    expect(firstSends).toEqual([["A", { type: "set_config_option", data: { config_id: "model", value: "old-dropdown-value" } }]]);
    expect(currentSends[0][0]).toBe("A");
    expect(h.writes).toEqual([["A", ["A"]], ["B", ["B"]]]);
    current.handleModelTagApplied("A", { ...currentSends[0][1].data, session_id: "A", status: "applied", model_id: "not-optimistic" });
    expect(await p).toEqual({ status: "applied" });
    expect(h.writes.length).toBe(2);
    expect(h.timers.size).toBe(0);
    h.cleanup();
  });

  test("unmount settles pending; effect replay creates a live controller for stable callbacks", async () => {
    const h = harness();
    const sent = [];
    const api = h.render("B", (...args) => { sent.push(args); return true; });
    const p = api.applyModelTag("A", "Tag");
    h.cleanup();
    expect(await p).toEqual({ status: "cancelled" });
    expect(h.timers.size).toBe(0);
    expect(await api.applyModelTag("A", "Tag")).toEqual({ status: "cancelled" });
    h.replay();
    const fresh = api.applyModelTag("A", "Tag");
    api.handleModelTagApplied("A", { ...sent[1][1].data, session_id: "A", status: "already_active" });
    expect(await fresh).toEqual({ status: "already_active" });
    h.cleanup();
  });

  test("disconnected/lost result times out without store writes or reconnect", async () => {
    const h = harness();
    let sends = 0;
    const api = h.render("B", () => { sends++; return true; });
    const p = api.applyModelTag("A", "Tag");
    for (const fn of h.timers.values()) fn();
    expect(await p).toEqual({ status: "timeout" });
    expect(sends).toBe(1);
    expect(h.writes).toEqual([["B", ["B"]]]);
    h.cleanup();
  });
});
